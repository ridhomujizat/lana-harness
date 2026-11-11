# Design: Knowledge graph and tabbed Knowledge page

- **Status**: Approved
- **Revision**: 1

## 1. Link index (`internal/knowledge`)

New file `links.go`.

```go
// Link is one [[target]] in a vault markdown file.
type Link struct {
    Ord     int    // position in the file, from 0
    Target  string // raw target, trimmed, without #heading and |label
    Norm    string // lower-case Target; for a path target also without a trailing ".md"
    Context string // the source line, trimmed, at most 200 runes, centred on the link
}

func ParseLinks(raw string) []Link
```

- Pattern `\[\[([^\[\]|#]+)(#[^\]|]*)?(\|[^\]]*)?\]\]` (same as `agent/knowledge/lint.go`).
- Lines inside fenced code blocks (```` ``` ```` or `~~~`) are skipped. Empty targets are skipped.
- Context: the whole line when it fits in 200 runes; otherwise a 200-rune window around the link
  with `…` at a cut end. Leading list markers (`- `, `* `, `1. `) are removed.

Schema v4 (`indexSchemaV4`, `indexSchemaVersion = 4`):

```sql
CREATE TABLE links (src TEXT NOT NULL, ord INTEGER NOT NULL, target TEXT NOT NULL,
  norm TEXT NOT NULL, context TEXT NOT NULL DEFAULT '', PRIMARY KEY (src, ord));
CREATE INDEX links_norm ON links(norm);
```

- `migrate`: `if version < 4` create the table and set upkeep key `links_backfill = "1"`. Chunks,
  vectors, and files stay.
- `syncOne`: for `.md` files, in the same transaction as the chunk rewrite, `DELETE FROM links
  WHERE src = ?` and insert `ParseLinks(raw)`. Non-markdown files get no links.
- `deleteFile`: also deletes the file's links.
- Backfill: at the end of `syncAll`, when `links_backfill` is set, read every indexed `.md` file
  from the vault, rewrite its links, then clear the key. A read error skips that file.

### Resolution

```go
type FileRef struct { Path, Title, Scope, Kind, Type, Updated string }

type resolver struct {
    byTitle map[string][]FileRef // lower title and lower aliases
    byPath  map[string]FileRef   // lower path, with and without ".md"
}
func (ix *Index) loadResolver(ctx) (*resolver, error) // one query over files
func (r *resolver) resolve(srcScope, norm string) (FileRef, bool)
```

- A target containing `/` resolves by path only. Otherwise by title/alias: candidates in the
  source scope first, then `shared`, then the lowest path. Only `page`, `journal`, `source`, and
  report files are candidates (whatever `kindOf` gives for vault markdown).

### Graph

```go
type GraphQuery struct { Scope string; Journals, Sources bool; Max int } // Max 0 → 2000
type GraphNode struct { ID, Title, Scope, Kind, Type, Updated string; Degree int }
type GraphEdge struct { Source, Target string }
type GraphHub  struct { Scope string; Pages int }
type Graph struct {
    Nodes []GraphNode; Edges []GraphEdge; Hubs []GraphHub
    Pages, Links, Broken int; Truncated bool
}
func (ix *Index) Graph(ctx, q GraphQuery) (Graph, error)
```

- Nodes: files of kind `page` (plus `journal`, `source` when asked) in the scope filter.
- Edges: every link whose source is a node and whose target resolves to another node; unique by
  (source, target). `Links` counts those edges; `Broken` counts links from node files that do not
  resolve at all.
- Degree = edges in + out. Over `Max`: keep the highest degree (tie: newest `updated`, then
  path), drop edges to removed nodes, set `Truncated`.
- Hubs: one per scope among the nodes, with its page count, `shared` last.

### Page links

```go
type Backlink struct { FileRef; Context string }
func (ix *Index) PageLinks(ctx, path string) (out []FileRef, unresolved []string, in []Backlink, err error)
```

- Out: the page's links resolved, unique, in file order; unresolved targets unique.
- In: links whose `norm` is one of the page's keys (lower title, lower aliases, lower path with
  and without `.md`), kept only when they resolve to this page; one entry per source file (first
  context); self links dropped.

## 2. Service and core (`agent/knowledge`, `core`)

`core/knowledge.go` gains the JSON types of the contract: `KnowledgeGraph` (+ node, edge, hub,
counts), `KnowledgeLinkRef`, `KnowledgeBacklink`, `KnowledgeActivityEntry`,
`KnowledgeActivityPage`, `KnowledgeHealth` (+ last run, lint counts, memory entry). The page
result type returned by `KnowledgeDomains.Page` gains the API-KGR-002 fields. `ReviewItem` gains
`Status`, `Result`, `DecidedAt`, `OldText` (omitempty).

`KnowledgeDomains` gains:

```go
Graph(ctx, scope string, journals, sources bool) (KnowledgeGraph, error)
Activity(ctx, limit int, before time.Time, kind string) (KnowledgeActivityPage, error)
Health(ctx) (KnowledgeHealth, error) // without memory; the runtime adds it
DecidedProposals(ctx, limit int) ([]KnowledgeProposal, error)
```

### Activity

- Read `log.md` whole (it is small; cap at 4 MB, reading the tail when larger). Split at `## `
  headings like `parseLogEntries`. Heading form `YYYY-MM-DD HH:MM rest`; time parsed in the local
  zone; an entry without a parsable time is dropped.
- Kind from the first word of `rest`, after checking every heading the code writes today
  (`appendUpkeepLog` titles, `Apply` actors, proposal decisions): `consolidation` → `run`,
  `promotion` → `promotion`, `lint` → `lint`, proposal or review decisions → `review`, agent and
  owner writes → `write`, anything else → `other`. A `run` with an `error:` line becomes `error`.
- Actor: the agent name when the heading names one (`promotion lana` → `lana`).
- Newest first; `before` filters strictly older; `kind=writes` matches `promotion` and `write`.

### Health

- Last run: the newest `run`/`error` entry. `journaled` from `journaled N sessions`; `applied` and
  `skipped` summed from `applied N ops (M skipped)` lines of promotion entries after the previous
  run entry and up to this one; `error` from an `error:` line, else `consolidation.last_error`.
- `next_at` from the status.
- Lint: the report at the upkeep report key; `pages` from `Pages: N.`; counts are the `- ` lines
  under each section heading (Contradictions, Marked conflicts, Likely duplicates, Broken links,
  Orphan pages, Missing details).
- `review_pending` like the status.

## 3. Runtime and web

- `lana.go`: `KnowledgeGraph`, `KnowledgeActivity`, `KnowledgeHealth` (adds `memory` for each
  agent with a domain: rune count of `MEMORY.md`, limit `agenthome.MemoryLimit`),
  `ReviewItems(status)`; decided items map proposals and set `Status`, `Result`, `DecidedAt`.
  Pending knowledge items set `OldText` from the op.
- `web/knowledge.go`: routes `GET /v1/knowledge/graph`, `/activity`, `/health`; `/v1/review`
  reads `status`. Validation as in the contract; `KnowledgeDomainBackend` gains the methods.
- Bridges in `cmd/lana/web.go` and `cmd/lana/daemon.go`.

## 4. Web UI (`web/ui`)

- Dependency: `force-graph` (canvas, d3-force inside), pinned exact version; imported lazily
  (`import("force-graph")`) inside the graph tab so the rest of the bundle and tests do not load
  it. Tests mock the module.
- `views/knowledge.tsx`: header (title, subtitle, last run line, Upload, Run now), `Tabs` from
  Gr8r, tab from `params.tab`, writes `?tab=` on change. The current panels (vault, upload,
  consolidation, sources, embedding, lint report, log) move to `knowledge-details.tsx`, shown in
  a collapsible "Details" section at the end of the Activity tab. Knowledge-off and error states
  stay.
- `views/knowledge-graph.tsx`: data from API-KGR-001; hub nodes added client-side (`hub:<scope>`)
  with edges to their pages. Scope colours: a fixed palette (violet, green, amber, blue, rose,
  teal, …) in scope order, `shared` grey; the same colour everywhere on the page (chips, dots,
  legend). Canvas colours from CSS tokens, re-read on theme change. Node size from degree; labels
  drawn when zoom × size passes a threshold, always for hubs, hovered, selected, and neighbours.
  Hover and select dim non-neighbours (alpha 0.25). Search focuses (centre + zoom) the first
  title match. Fit on first load and on the fit button. Side panel (desktop ≥ 900 px) or bottom
  sheet (narrower) loads API-KGR-002. "Ask <agent>" opens the agent's room with the composer
  prefilled ("Tentang [[Title]]: ").
- `views/knowledge-pages.tsx`: list (API pages, sort client-side; "most linked" uses graph
  degree when loaded, else hidden) and reader. Markdown rendered with the existing `Markdown`;
  `[[x]]` turned into links before rendering, resolved through `links_out` (unresolved shown
  muted). `~~x~~` already renders struck through.
- `views/knowledge-review.tsx`: replaces `ReviewPanel` usage; filters; cards per the design;
  Decided from API-KGR-003.
- `views/knowledge-activity.tsx`: summary from health, timeline from activity (group by day,
  "Load older" with `next_before`), filters, health card (lint counts open the report in a
  drawer), memory bars, Details.
- Styles in `styles/pages-gr8r/knowledge.css`; mobile at 390 px; dark mode via tokens.
- `web/dist` rebuilt.
