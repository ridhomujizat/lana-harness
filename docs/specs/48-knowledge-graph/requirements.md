# Requirements: Knowledge graph and tabbed Knowledge page

- **Issue**: —
- **Status**: Approved (owner approved the figcraft design 2026-11-11: "sip approved implement")
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (HTTP)
- **Depends on**: spec 30 (vault, index), spec 31 (upkeep, lint, log), spec 46 (scopes, proposals,
  review queue)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner 2026-11-11 | "tampilan knowladgenya saya masih ga paham … bisa di buat kaya graph obsidian gaya?" | goal |
| Owner 2026-11-11 | "tab pages review activity belum ada viewnya" → views drawn, then "sip approved implement" | scope |
| Design | `design/lana-design-system.figcraft`, page "Knowledge": frames Graph (light, dark, mobile), Pages, Review, Activity; PNGs `design/export/knowledge-*.png` | layout |

## Goal

The owner understands at a glance what the agents know. The Knowledge page opens on a map:
agents are large nodes, pages are dots in the agent's colour, and `[[links]]` between pages are
lines. Clicking a page shows what it says and what it links to. The rest of the page splits into
tabs: Pages (list and reader), Review (proposals waiting for the owner), and Activity (what the
nightly run and the agents did, and the health of the knowledge).

## Definitions

- **Link**: a `[[target]]` or `[[target|label]]` or `[[target#heading]]` in a vault markdown file.
  The target is a page title, a page alias, or a vault path with or without `.md`.
- **Resolved link**: a link whose target matches an indexed file. Title and alias matching is
  case-insensitive. When several files match, the source file's own scope wins, then `shared`,
  then the first by path.
- **Broken link**: a link that resolves to no indexed file.
- **Hub**: a graph node for a scope (one per agent domain, one for `shared`). Every page node is
  joined to its scope's hub.

## Requirements

### Link index

- **REQ-KGR-001** The index stores the links of every indexed markdown file (pages, journals,
  reports), with the raw target, the line text around the link (at most 200 runes), and the
  source path. Links are rewritten whenever the file is reindexed and removed with the file.
- **REQ-KGR-002** Opening an index of an older schema adds the link store and fills it from the
  vault files on the next sync without dropping chunks or vectors (no re-embedding).
- **REQ-KGR-003** Link resolution happens when the graph or a page is read, so a link to a page
  created later resolves without reindexing the linking file.

### Graph

- **REQ-KGR-010** `GET /v1/knowledge/graph` returns page nodes, resolved page-to-page edges
  (deduplicated, no self links, direction kept), hub nodes for the scopes present, and counts
  (pages, links, broken links).
- **REQ-KGR-011** The graph can be filtered by scope (`all`, `shared`, or an agent). Journals and
  sources are left out unless requested; when included they are nodes with their own kind.
- **REQ-KGR-012** Each node carries path, title, scope, kind, type, updated, and degree (edges in
  plus out). Large vaults are capped at 2,000 nodes, highest degree first, with `truncated: true`.

### Page detail

- **REQ-KGR-020** `GET /v1/knowledge/page` also returns type, description, updated, frontmatter
  sources, outgoing resolved links, unresolved targets, and backlinks with the line context from
  the linking file.

### Review

- **REQ-KGR-030** `GET /v1/review?status=decided` returns the latest decided knowledge proposals
  (applied, approved, rejected), newest first, at most 20. The default stays the pending list.

### Activity and health

- **REQ-KGR-040** `GET /v1/knowledge/activity` returns log entries parsed from `log.md`, newest
  first, paged with `limit` and `before`, each with time, heading, kind, actor (agent name when
  known), and lines. Kind is one of `run` (consolidation), `promotion`, `write`, `review`, `lint`,
  `error`, `other`.
- **REQ-KGR-041** `GET /v1/knowledge/health` returns: the last nightly run (time, sessions
  journaled, ops applied and skipped across the promotions of that run, error), the next run
  time, counts from the latest lint report per section, the number of review items waiting, and
  `MEMORY.md` size and limit for every agent that has a domain.

### Web page

- **REQ-KGR-050** `#/knowledge` has tabs Graph, Pages, Review, Activity, chosen by `?tab=`; Graph
  is the default. The Review tab shows its pending count as a badge. `?scope=` and `?page=` keep
  working and select the filter and the open page.
- **REQ-KGR-051** Graph tab: force-directed canvas with zoom, pan, drag, fit; hub nodes labelled
  with the agent's display name; page dots coloured by scope; labels shown for larger nodes and on
  hover; hovering or selecting a node dims every node and edge not next to it; scope chips with
  page counts; search that focuses the first match; toggles for journals and sources; a legend
  and the counts. Selecting a page opens a side panel (desktop) or bottom sheet (mobile) with
  scope, type, updated, summary (description or first paragraph), the latest superseded change
  when the page has one, links to, linked from, sources, and buttons "Open page" (Pages tab) and
  "Ask <agent>" (agent room with the page title prefilled).
- **REQ-KGR-052** With fewer than three pages the graph still renders and shows a short note that
  the map grows as agents learn.
- **REQ-KGR-053** Pages tab: list with search, scope chips, sort (recently updated, title, most
  linked); reader with path, title, scope and type chips, rendered markdown where `[[links]]` open
  that page in the reader, superseded lines struck through, and a "Linked from" list with
  context. Buttons "Show in graph" and "Ask <agent>".
- **REQ-KGR-054** Review tab: a one-line explanation, filters Waiting / Knowledge / Skills /
  Decided, cards for knowledge proposals (from → to agent, title, action, body as an added block,
  old text struck for replace and supersede) and skill proposals (body preview, open full), with
  Approve and Reject; Decided shows recent decisions with status.
- **REQ-KGR-055** Activity tab: last-run summary with counts and next run; filters All / Nightly
  runs / Agent writes / Reviews; a timeline grouped by day with `[[links]]` that open pages; a
  health card with lint counts (opening the report), waiting reviews, and `MEMORY.md` bars per
  agent. Vault, upload, sources, and embedding details move into a collapsible "Details" section
  at the bottom of this tab.
- **REQ-KGR-056** The header keeps Run now (consolidation) and Upload. Every view works at 390 px
  and in dark mode; the canvas reads its colours from the theme tokens.

## Out of scope

- Editing pages in the web page.
- Local graph per page (depth 1–2 neighbourhood) beyond the hover and selection dimming.
- Graph physics settings and saved layouts.

## Acceptance criteria

- **AC-KGR-001** Index: a page with `[[A]]`, `[[A|x]]`, `[[A#h]]`, `[[agents/dana/wiki/B]]` stores
  four links; reindexing after an edit replaces them; deleting the file removes them; an index at
  schema 3 migrates and backfills without changing chunk or vector counts.
- **AC-KGR-002** Graph: two scopes with cross links produce correct nodes, hubs, deduplicated
  edges, broken count, scope filter, journal toggle, and the 2,000-node cap.
- **AC-KGR-003** Page detail: out links, unresolved targets, and backlinks with context.
- **AC-KGR-004** Activity and health parse the real log and lint formats (fixtures copied from
  spec 31/46 output) and page correctly.
- **AC-KGR-005** Web handlers: validation (bad scope, bad limit, bad `before`) returns 400.
- **AC-KGR-006** Vitest: tabs and routing, graph data mapped to nodes and hubs, selection panel,
  reader link navigation, review filters and decisions, activity filters and health; 390 px and
  dark rendering checked by screenshots on a live daemon.
