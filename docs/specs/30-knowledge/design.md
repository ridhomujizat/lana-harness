# Design: Knowledge vault

- **Requirements**: [requirements.md](requirements.md) — revision 1, Approved
- **Contract**: [api-contract.md](api-contract.md) — revision 1, Approved
- **Status**: Approved (on behalf of the owner, gate lifted 2026-11-01)
- **Design revision**: 1

## Architecture decision scan

| Topic | Existing authority | Action |
|---|---|---|
| Layering | `internal/archtest`: library packages import only `core` and `internal/*` | vault and index in `internal/knowledge`; LLM service in `agent/knowledge`; tools reach it through `core.Knowledge` |
| Background LLM pass | spec 20 `agent/memcapture` (module, registry sink, base context cancelled on Stop) | reuse the pattern |
| Persistence | ADR 0009 covers sessions only | local: the vault is files; `$LANA_HOME/knowledge.db` is a derived index |
| File locking | `internal/agenthome.Update` (`flock` on `<path>.lock`) | reuse the same `flock` approach with one lock file |
| Provider calls | `providers/openai` adapter | add an optional `core.Embedder` |

### Component view

```text
tools/knowledge.go ──ToolCtx.Knowledge──▶ core.Knowledge ◀── agent/knowledge.Service
agent/turn.go (recall) ──registry sink──────────┘              │  compile pass (LLM via core.LLMRegistry)
                                                                ▼
                                       internal/knowledge.Vault (files)  +  internal/knowledge.Index (knowledge.db)
                                                                                    │ optional
                                                                                    ▼
                                                                core.Embedder (providers/openai)
```

## 1. Config (API-KNW-001)

- `core/config.go`: `Config.Knowledge KnowledgeConfig` `toml:"knowledge"`;
  `KnowledgeConfig{Path string; AutoRecall *bool; EmbeddingProvider, EmbeddingModel string}` with
  toml tags `path`, `auto_recall`, `embedding_provider`, `embedding_model`.
- `config/validate.go`: `knowledge` in `topKeys`; `knowledgeKeys`; the provider must exist and be
  `openai_compat`; model required when provider set. Messages as in the contract.
- `providers/openai/module.go` `usedProviders`: also mark `config.Knowledge.EmbeddingProvider`.
- `config/profile.go` `baseTOML`: add module `knowledge` after `memcapture`.
- Path resolution helper `knowledge.ResolvePath(home, cfgPath string) string` (expand `~`, join
  relative with home, default `filepath.Join(home, "knowledge")`).

## 2. Vault (`internal/knowledge`, REQ-KNW-001–013, -020)

Files: `vault.go`, `page.go`, `write.go`, `sources.go`, `templates/KNOWLEDGE.md` (`embed`).
New direct dependency `gopkg.in/yaml.v3` (already in the module graph) for frontmatter.

- `Open(root string) (*Vault, error)` creates `sources/` and `wiki/` and writes `KNOWLEDGE.md` only
  when it does not exist (AC-KNW-001). It never removes files.
- One process-and-file lock for all vault writes: `flock` on `<root>/.lana.lock` (dot files are
  ignored by Obsidian and by the indexer), plus an in-process mutex.
- `Page{Path, Title string; Meta PageMeta; Body string; Raw string}`;
  `PageMeta{Type, Description string; Aliases, Tags, Sources []string; Created, Updated string; Extra map[string]any}`.
  `ParsePage` splits a leading `---` block; a missing or invalid block yields empty meta and the
  whole file as body (REQ-KNW-003). `Extra` keeps unknown keys so a meta update preserves them.
  `FormatPage` writes keys in the order `type, description, aliases, tags, sources, created, updated`,
  then `Extra` keys sorted.
- Titles: `ValidTitle` (REQ-KNW-013). File `wiki/<Title>.md`. `FindPage(title)` matches file names
  under `wiki/` recursively and frontmatter `aliases`, case-insensitive (REQ-KNW-011).
- `Apply(op Op, actor string) (Result, error)` under the lock:
  - `create`: if `FindPage` hits, becomes `append` of `Body` (REQ-KNW-011); else writes frontmatter
    with `created = updated = today` and body `Body`.
  - `append`: no `Heading` → body + `"\n\n"` + text. With `Heading`: find a line `## <Heading>` (any
    level 2–4, case-insensitive); insert before the next heading of the same or higher level, or at
    the end; missing heading → append `"\n\n## <Heading>\n\n" + text`.
  - `replace`: `OldText` must occur exactly once in the body (not frontmatter); errors
    `old_text not found in <title>` / `old_text matches <n> places in <title>; use more specific text`.
  - `meta`: sets the given non-empty fields; `aliases`/`tags`/`sources` are merged as sets.
  - Every op sets `updated = today`; adds `sources` given in the op as a set merge.
  - Read-modify-write: read file and mtime; compute; before rename, stat again; if mtime changed,
    redo the read and compute once (REQ-KNW-010); write via temp file + `os.Rename`.
  - Limits: 40,000 runes after the change (REQ-KNW-012); `secretlike.Match(text)` → refused with
    `content looks like a secret; not saved`.
  - After success: regenerate `index.md`, append `log.md`.
- `index.md` (REQ-KNW-004):

  ```text
  # Knowledge index

  Generated by lana after each change. Edits here are overwritten.

  ## People
  - [[Pak Budi]] — Klien utama proyek X
  ```

  Sections in order People, Projects, Organizations, Places, Topics, Sources, Other; titles sorted
  case-insensitively within a section; empty sections omitted.
- `log.md` entry: `## 2006-01-02 15:04 <actor>` then one line per op
  `- created [[Title]]` / `- appended to [[Title]]` / `- replaced text in [[Title]]` /
  `- updated details of [[Title]]`, and for a compile `- from [[sources/<file>]]`.
- Sources (`sources.go`): `SaveSource(title, ext string, data []byte) (path string, duplicate bool, error)`.
  SHA-256 of the data is compared with existing source hashes (read from the index `files.hash`
  column, falling back to hashing files under `sources/`). Name `sources/<YYYY-MM-DD> <title>.<ext>`;
  a name clash adds ` (2)`, ` (3)`. Max 30 MB.
- `internal/secretlike` (new): `Match(text string) bool` with the spec 20 patterns.
  `agent/memcapture` switches to it (no behavior change).

### `KNOWLEDGE.md` template

```markdown
# Knowledge conventions

This vault is a wiki that lana and the owner keep together. lana reads this file before it changes
pages. Edit it to change how pages are written.

## Folders
- `sources/`: raw material (documents, links, notes). Never edited after it is saved.
- `wiki/`: one page per person, project, organization, place, topic, or source.
- `index.md` and `log.md` are written by lana.

## Pages
- File name = page title. Link other pages with [[Title]]; link sources with [[sources/<file>]].
- Frontmatter: type (person, project, organization, place, topic, source), description (one line),
  aliases, tags, sources, created, updated.
- Start the body with a short summary paragraph. Then use `##` sections.
- Write facts, decisions, dates, and who said what. Say where a fact came from.
- When a new fact contradicts a page, keep both and mark it: "**Conflict:** <old> vs <new> (source)".
- Write in the language of the source.
```

## 3. Index (`internal/knowledge/index.go`, `chunk.go`, `search.go`; REQ-KNW-030–032)

`OpenIndex(dbPath, vaultRoot string) (*Index, error)` on `$LANA_HOME/knowledge.db` (busy timeout
5 s, WAL), schema version 1 via `PRAGMA user_version`:

```sql
CREATE TABLE files (
  path TEXT PRIMARY KEY, kind TEXT NOT NULL, title TEXT NOT NULL, type TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '', aliases TEXT NOT NULL DEFAULT '[]', updated TEXT NOT NULL DEFAULT '',
  mtime INTEGER NOT NULL, size INTEGER NOT NULL, hash TEXT NOT NULL
);
CREATE TABLE chunks (
  id INTEGER PRIMARY KEY, path TEXT NOT NULL, ord INTEGER NOT NULL, title TEXT NOT NULL,
  heading TEXT NOT NULL DEFAULT '', start_line INTEGER NOT NULL, text TEXT NOT NULL,
  UNIQUE (path, ord)
);
CREATE VIRTUAL TABLE chunks_fts USING fts5(
  title, heading, text, content = 'chunks', content_rowid = 'id',
  tokenize = 'unicode61 remove_diacritics 2'
);
-- insert/delete triggers as in spec 29 design §1
CREATE TABLE vectors (chunk_id INTEGER PRIMARY KEY, model TEXT NOT NULL, dim INTEGER NOT NULL, vec BLOB NOT NULL);
CREATE TABLE sources_state (path TEXT PRIMARY KEY, status TEXT NOT NULL, error TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '', turn_id TEXT NOT NULL DEFAULT '', agent TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL);
```

- Kinds by top folder: `wiki/` → `page`, `sources/` → `source`, `journal/` → `journal` (spec 31).
  Other folders, dot entries, and root files are not indexed. Extensions: `.md`, `.txt` (≤ 5 MB),
  `.pdf` (≤ 30 MB, text through `internal/attach.PDFText`).
- `Sync(ctx)`: walk, compare `mtime`+`size`, re-chunk changed files, delete rows of missing files
  (vectors deleted with their chunks). Runs at most once per 10 s unless forced; `SyncFile(path)` is
  forced after a lana write.
- Chunking: markdown splits at headings (levels 1–3); frontmatter is not chunk text, but the first
  chunk starts with `title — description`. Sections over 1,500 runes split at blank lines, then
  hard-cut at 1,500. Text and PDF split at blank lines into ≤ 1,500-rune chunks.
  `start_line` is the 1-based line of the chunk in the file (PDF: line in the extracted text).
- Keyword search: tokens as spec 29 (≥ 2 runes, stop words dropped, ≤ 12, quoted, OR), ordered by
  `bm25(chunks_fts, 5.0, 2.0, 1.0)`, top 40 chunks, filtered by kind/type.
- Vectors: when an embedder is set, `Sync` embeds chunks with no vector for the current model in
  batches of 64 (best effort, errors logged; missing vectors retried on the next sync). An
  in-memory matrix per model is loaded lazily and dropped when rows change. Cosine top 40.
- Fusion (REQ-KNW-031): reciprocal rank fusion, score `Σ 1/(60 + rank)` over the keyword and vector
  lists; an embedding error leaves the keyword list only. Then best chunk per file, `limit` files.
- Snippet: FTS `snippet()` with `«»` for keyword hits; for vector-only hits the first 300 runes of
  the chunk.
- Recall rule (REQ-KNW-050): pages only; a chunk qualifies when the set of its tokens holds ≥ 2
  distinct query tokens, or its cosine is ≥ 0.45.

## 4. Embeddings (API-KNW-004)

- `core/chat.go`: `type Embedder interface { Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) }`.
- `providers/openai/embed.go`: `(*Adapter).Embed` posts to `<base_url>/embeddings` with the same
  auth, headers, timeout, and error classification as `Stream`.

## 5. Service and compile (`agent/knowledge`; REQ-KNW-020–025)

`core/knowledge.go`:

```go
type Knowledge interface {
	Search(ctx context.Context, q KnowledgeQuery) ([]KnowledgeHit, error)
	Read(ctx context.Context, ref string, from, to int) (KnowledgeDoc, error)
	Write(ctx context.Context, w KnowledgeWrite) (KnowledgeWriteResult, error)
	Ingest(ctx context.Context, in KnowledgeIngest) (KnowledgeIngestResult, error)
	Recall(ctx context.Context, text string, limit int, skip func(path string) bool) ([]KnowledgeHit, error)
}
```

with plain structs `KnowledgeQuery{Text string; Limit int; Kind, Type string}`,
`KnowledgeHit{Path, Title, Kind, Type, Description, Heading, Snippet, Updated string; StartLine int}`,
`KnowledgeDoc{Path, Title string; From, To, Total int; Lines []string}`,
`KnowledgeWrite{Action, Title, Type, Description, Body, Text, Heading, OldText, NewText, Actor string; Aliases, Tags []string}`,
`KnowledgeWriteResult{Path, Op string; Size int}`,
`KnowledgeIngest{Title, Ext, SourceURL, SessionID, TurnID, AgentName string; Data []byte}`,
`KnowledgeIngestResult{Path string; Duplicate, Queued bool}`.
`core.ServiceKnowledge ServiceKey = "knowledge"`.

`agent/knowledge` module `knowledge`: requires sessions, llm, agents; provides `ServiceKnowledge`;
installs the service on the agent registry through a sink
(`SetKnowledge(core.Knowledge)`, `AgentConfig(name)`), like memcapture. `Stop` cancels the base
context and closes the index.

- Lazy open: vault and index open on the first call (`sync.Mutex` + retry on error), so an install
  that never uses knowledge creates nothing. At `Install`, a goroutine checks whether
  `knowledge.db` exists and, if so, opens it and queues `pending` sources (REQ-KNW-023).
- `Ingest`: `SaveSource`, then `sources_state` row `pending` with session, turn, agent; enqueue.
  Duplicate → existing path, queued only if its state is `failed` or `pending`.
- Queue: one worker goroutine per service, FIFO channel (cap 256) (REQ-KNW-023).
- Compile pass for one source:
  1. Text: `.md`/`.txt` as is; `.pdf` through `PDFText`. Split at blank lines into parts of
     ≤ 24,000 runes; at most 8 parts (more → note `source cut after 8 parts` in the event error and
     log, the 8 parts still apply).
  2. Model: agent config `provider`/`model` of `sources_state.agent` (REQ-KNW-025); unknown agent or
     provider → `failed`.
  3. Per part, request `{Model, System: compileSystem, Messages: [user: instruction], MaxTokens: 8192}`,
     no tools, 180 s timeout. Instruction holds: `KNOWLEDGE.md`, the source path and title, the part
     number, the catalog (`title — type — description` lines from `files`, at most 400), the related
     pages (titles or aliases named in the part, then `Search(first 3,000 runes of the part, kind page)`,
     up to 8 pages, each cut to 6,000 runes), then the part text.
  4. Reply: a JSON array (a ```` ```json ```` fence is stripped), at most 12 ops of
     `{"op":"create","title","type","description","aliases","tags","body"}`,
     `{"op":"append","title","heading","text"}`, `{"op":"replace","title","old_text","new_text"}`,
     `{"op":"meta","title","description","aliases","tags"}`. Invalid JSON → the source is `failed`
     with the parse error; earlier parts' ops stay.
  5. Each op goes through `Vault.Apply` with actor `compile <source>`; each `create`/`append` also
     merges `[[sources/<file>]]` into the page `sources`. An op error is recorded and skipped.
  6. After part 1, if no page with `type: source` links this source, the service creates one: title
     = source title, description = first non-empty line (≤ 160 runes), body
     `Source: [[sources/<file>]]`.
  7. `SyncFile` for each changed page; state `done` (or `failed`).
  8. When `sources_state.session_id` is set, append `knowledge/compiled` (API-KNW-002) with the
     summed usage. `store/store.go` `validKinds` and `core/event.go` get the kind and payload type.

Compile system prompt (`compileSystem`):

```text
You maintain a personal knowledge wiki in Obsidian markdown. You receive one part of a source and the existing pages most related to it. Turn the source into durable knowledge:
- Create a page of type "source" for the source (title = the source title) with a summary and the key points, on the first part only.
- Create or update pages for the people, projects, organizations, places, and topics the source covers. Prefer updating an existing page (append under a fitting ## heading) over creating a near-duplicate; use the exact existing title.
- Link related pages with [[Title]]. Use only titles that exist or that you create in this reply.
- Keep facts specific: names, dates, numbers, decisions. Do not invent facts.
- When the source contradicts a page, append "**Conflict:** <page says> vs <source says>".
- Never include passwords, API keys, or tokens.
Reply with only a JSON array of operations, at most 12.
```

## 6. Tools (`tools/knowledge.go`; REQ-KNW-040–044)

`ToolCtx.Knowledge core.Knowledge`, set in `agent/turn.go` from the registry. Nil →
`knowledge is not available`.

- `knowledge_search` (read): `{query (required), limit (1–20, default 8), kind ("page"|"source"|"journal"), type}`.
  Output:

  ```text
  Found 2 results for "kontrak pak budi":
  1. wiki/Pak Budi.md — person — Klien utama proyek X (updated 2026-10-30)
     ## Kontrak, line 14: … «kontrak» ditandatangani 12 Okt …
  2. sources/2026-10-30 Kontrak PT X.pdf — source, line 40: …
  Read a file with knowledge_read path=<path>.
  ```

  No hits: `Nothing in knowledge matched "<query>".` `PromptGuidance()` returns the REQ-KNW-044 note:
  `Search knowledge_search before answering about people, projects, or things the owner shared earlier. Save lasting knowledge with knowledge_write, or knowledge_ingest for documents, links, and long notes. Keep memory_write for short facts about the owner and yourself.`
- `knowledge_read` (read): `{path | title, from_line, to_line}`. Lines numbered like `read_file`;
  at most 2,000 lines or 20,000 runes per call with a `[more: from_line=<n>]` marker.
- `knowledge_write` (memory): `{action: create|append|replace|meta, title, type, description, body, text, heading, old_text, new_text, aliases, tags}`
  with the per-action required fields checked by the tool. Output e.g.
  `created wiki/Pak Budi.md (1,234 characters)`, `appended to wiki/Pak Budi.md (5,010 characters)`.
  Actor = `agent <name>`.
- `knowledge_ingest` (memory): exactly one of `path`, `url`, `text`, plus optional `title`.
  `path` resolves inside the workspace (reject escapes and symlinks out); `.html` → markdown; `url`
  uses the `web_fetch` fetch-and-convert code (refactor its helper so both call it) and stores
  `source_url` in a frontmatter block of the stored `.md`; `text` stored as `.md`. Title default:
  file base name, page `<title>`, or first line (≤ 80 runes). Output:
  `Saved sources/2026-11-01 Rapat Q4.pdf. Compiling it into wiki pages in the background; they appear in knowledge_search when done.`
  Duplicate: `Already saved as <path>.` (+ `Compiling again.` when re-queued).
- Register in `tools/module.go`; `lana.go` catalog group `{ID: "knowledge", Label: "Knowledge"}` after
  Memory; `agent/loopguard.go` read-only set gets `knowledge_search`, `knowledge_read`.

## 7. Recall in the turn (REQ-KNW-050)

- `internal/agenthome/prompt.go`: `PromptInput.RelevantKnowledge []string`; `ContextBlock` adds
  `relevant knowledge:` and `- <line>` entries after relevant memory.
- `agent/turn.go` `buildPrompt`: when `ownerText != ""`, the turn tools contain `knowledge_search`,
  `Config.Knowledge.AutoRecall` is not `false`, and the registry has a knowledge service: call
  `Recall(ctx2s, ownerText, 3, skip)` where `ctx2s` has a 2 s timeout and `skip` reads
  `rn.knowledgeShown` (per runner, like `memoryShown`). Lines: `<Title> (<path>): <description or snippet, ≤ 200 runes>`.
  Errors and timeouts are logged at debug and give no lines. `buildPrompt` gets the turn tool names
  as a new parameter.

## 8. Web (REQ-KNW-060)

- `web/ui/src/views/turn.tsx` and `web/ui/src/lib/format.ts`: `knowledge/compiled` renders a muted
  line `Knowledge updated from "<source file name>": created A, updated B` (append/replace/meta count
  as updated), or `Knowledge compile failed for "<name>": <error>`. Rebuild `web/dist`.

## 9. Verification

- `internal/knowledge`: AC-KNW-001, AC-KNW-002, AC-KNW-003 (save/dedup), AC-KNW-005 with a fake
  embedder (a synonym query finds a page only through vectors; an embedder error keeps keyword hits),
  chunking line numbers, PDF via a small fixture (reuse `internal/attach` test data if present).
- `agent/knowledge`: AC-KNW-003 (state survives reopen), AC-KNW-004 with a fake provider, queue order,
  unknown agent → failed.
- `providers/openai`: `Embed` against `httptest` (order by `index`, auth header, batch).
- `config`: validation messages.
- `tools`: AC-KNW-006 with a fake `core.Knowledge`.
- `agent`: AC-KNW-007.
- UI: vitest for the muted line; `npm run build`.
- Commands: `nix develop -c go vet ./...`, `nix develop -c go test -count=1 ./...`,
  `cd web/ui && npx vitest run && npm run build`.
