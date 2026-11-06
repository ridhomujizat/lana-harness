# Design: Knowledge upkeep

- **Requirements**: [requirements.md](requirements.md) — revision 1, Approved
- **Contract**: [api-contract.md](api-contract.md) — revision 1, Approved
- **Status**: Approved (on behalf of the owner, gate lifted 2026-11-01)
- **Design revision**: 1

## Architecture decision scan

| Topic | Existing authority | Action |
|---|---|---|
| Background passes | spec 30 `agent/knowledge.Service` | extend the same service |
| Conversation text | spec 29 `session_text` | add a read-only optional store interface |
| Compaction | `agent/compaction.Service.run` | add an observer callback after a summary is stored |
| Daemon-only work | `cmd/lana/daemon.go` starts channels and web | the daemon calls `Runtime.StartKnowledgeUpkeep` |
| Web data | optional backend interfaces (`DashboardBackend`) | add `KnowledgeBackend` the same way |
| Telegram commands | `channels/telegram/chat.go handleCommand` | add `/save`; optional backend interface |

### Flow

```text
compaction summary ──observer──▶ journal pass (session agent model) ──▶ journal/YYYY-MM-DD.md
daemon upkeep loop (1 min tick)
  ├─ every 5 min / after upload: inbox scan ──▶ sources/ ──▶ compile queue (spec 30)
  └─ at consolidate_at or missed: inbox scan → journal passes → promotion (compile journal text) → lint ──▶ reports/lint-*.md
Telegram /save ──▶ Runtime.IngestKnowledge ──▶ Knowledge.Ingest (spec 30)
```

## 1. Store: conversation text reader (REQ-KNU-001, -003)

`core/sessionsearch.go` adds an optional interface, implemented by the SQLite store:

```go
type SessionTextReader interface {
	// TextHeads lists searchable sessions (spec 29 definition) that have
	// conversation text, with their newest text seq.
	TextHeads(ctx context.Context) ([]TextHead, error)
	// TextRows returns rows of sessionID with seq > afterSeq in seq order,
	// stopping before the total text would pass maxRunes (at least one row).
	TextRows(ctx context.Context, sessionID string, afterSeq int64, maxRunes int) ([]TextRow, error)
}

type TextHead struct {
	Session SessionInfo
	MaxSeq  int64
}
```

`TextHeads`: `SELECT session_id, MAX(seq) FROM session_text GROUP BY session_id` joined with
searchable sessions.

## 2. Index state tables

`internal/knowledge` index schema version 2 (migration from 1):

```sql
CREATE TABLE journal_marks (session_id TEXT PRIMARY KEY, upto_seq INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE promote_marks (path TEXT PRIMARY KEY, offset INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE upkeep (key TEXT PRIMARY KEY, value TEXT NOT NULL);
```

`upkeep` keys: `last_consolidation_at`, `last_consolidation_error`, `last_report`.
Getter/setter methods on `Index`.

`Vault.AppendJournal(day string, block string) (path string, err error)`: under the vault lock,
creates `journal/<day>.md` with `# <day>\n` when missing, appends `"\n" + block`. No other journal
edit exists.

## 3. Config (API-KNU-001)

`core.KnowledgeConfig` gains `Agent string`, `Journal *bool`, `ConsolidateAt *string`
(`nil` = `"03:00"`; pointer so `""` can mean off). Validation in `config/validate.go`.
`usedProviders` also marks the provider of `knowledge.agent` (agents are already marked).

Knowledge agent (REQ-KNU-030) — `knowledgeAgent(cfg) (name string, reason string)`:
`knowledge.agent` if set; else the first agent in sorted name order whose `tools` contains any of
`knowledge_search`, `knowledge_read`, `knowledge_write`, `knowledge_ingest`; else `""` with reason
`no agent has a knowledge tool; set knowledge.agent`.

## 4. Journal pass (`agent/knowledge/journal.go`; REQ-KNU-001–004)

- Eligibility: session agent config has a knowledge tool; `Journal` not `false`; store implements
  `SessionTextReader`.
- `journalSession(ctx, info)`: mark = `journal_marks.upto_seq` or 0; rows = `TextRows(id, mark, 40000)`.
  No rows → return. No `user` row → set mark to the last row seq, return (REQ-KNU-002).
- Request: provider/model of the session's agent; `System: journalSystem`; one user message holding
  the excerpt as lines `seq <n> <time> <owner|agent>: <text>`; no tools; `MaxTokens 2048`; 120 s
  timeout. Reply JSON `{"notes": ["..."]}` (fence stripped), at most 12 notes, each ≤ 500 runes,
  secret-like notes dropped.
- Non-empty notes → `AppendJournal(day of the last row, local)` with block:

  ```text
  ## 14:05 · lana · telegram · session 06GS2SGJJSEVXER9PGXKGS0YZG "Deploy staging"
  - note one
  - note two
  ```

  then `SyncFile`. Mark = last row seq. Provider or parse error → mark unchanged, logged.
- Concurrency: a per-session in-flight set; a second request for the same session while one runs is
  dropped (the next trigger catches up). Journal passes share no lock with compile passes except
  the vault write lock.

`journalSystem`:

```text
You keep a journal of what the owner and the agent did and learned. From the conversation excerpt, write short notes worth keeping for months: facts learned about people, projects, places, and topics; decisions and their reasons; plans and deadlines; documents or links the owner shared; outcomes of work. Skip greetings, chit-chat, tool mechanics, and anything temporary. Keep names, numbers, and dates. Write in the language of the conversation. Never include passwords, API keys, or tokens.
Reply with only JSON: {"notes": ["...", "..."]}, or {"notes": []} when nothing is worth keeping.
```

### Compaction trigger (REQ-KNU-003a)

- `agent/compaction.Service`: `OnCompacted(fn func(sessionID, agent string))` appends to an
  observer list; `run` calls observers in a new goroutine after the `compaction/summary` append
  succeeds.
- `agent/knowledge` module `Install`: `b.Lookup(core.ServiceCompaction)`; if it implements
  `interface{ OnCompacted(func(string, string)) }`, register `service.JournalSoon`. The `knowledge`
  module stays after `compaction` in `baseTOML`.
- `JournalSoon(sessionID, agent)` runs `journalSession` on the service base context.

## 5. Upkeep loop (`agent/knowledge/upkeep.go`; REQ-KNU-011, -020, -024)

- `core.KnowledgeAdmin` (implemented by the service):

  ```go
  type KnowledgeAdmin interface {
  	StartUpkeep(ctx context.Context)
  	Status(ctx context.Context) (KnowledgeStatus, error)
  	Report(ctx context.Context) (path, markdown string, err error)
  	SaveInbox(ctx context.Context, name string, data []byte) (string, error)
  	Consolidate(ctx context.Context) error // ErrKnowledgeBusy when running
  	ScanInboxNow()
  }
  ```

  `KnowledgeStatus` mirrors API-KNU-003.
- `StartUpkeep` (once; later calls no-op) starts one goroutine with a 1-minute ticker and an
  injectable clock. Each tick: inbox scan when 5 minutes passed since the last scan or a scan was
  requested; consolidation when due.
- Due rule: `consolidate_at` set; `at` = today's local time `HH:MM`; due when `now ≥ at` and
  `last_consolidation_at < at`. The first tick runs right after start, so a missed run starts within a
  minute (REQ-KNU-020).
- `Consolidate`: `atomic.Bool` running flag (`ErrKnowledgeBusy`); steps in order, each step's error
  collected, the run continues (REQ-KNU-024): inbox scan → journal passes for all `TextHeads` whose
  `MaxSeq` > mark (eligible sessions only, one at a time) → promotion → lint. Then
  `last_consolidation_at = now`, `last_consolidation_error` = joined errors, `last_report`, one
  `log.md` entry `## <time> consolidation` with lines like `- journaled 3 sessions`,
  `- promoted journal/2026-11-01.md (5 operations)`, `- report [[reports/lint-2026-11-01]]`, and
  `- error: <step>: <msg>`.

## 6. Inbox (`agent/knowledge/inbox.go`; REQ-KNU-010)

- `Vault.Open` also creates `inbox/` and `journal/` (spec 30 layout grows; existing vaults get them on
  open).
- Scan: regular files directly in `inbox/` (no recursion, dot files skipped). Supported extensions
  as in REQ-KNU-010; `.html` converted to markdown (same converter as spec 30). `mtime` within 10 s
  → skip this scan. Unsupported → kept in an in-memory list for `Status`.
- Ingest: `Ingest(KnowledgeIngest{Title: base name, Ext, Data, AgentName: knowledge agent})`; on
  success (new or duplicate) `os.Remove` the inbox file. No knowledge agent → skip with the status
  reason.
- `SaveInbox(name, data)`: validate extension and 30 MB, write `inbox/<name>` (clash → ` (2)`),
  then `ScanInboxNow()` with the 10 s age rule waived for files written by `SaveInbox`.

## 7. Promotion (`agent/knowledge/promote.go`; REQ-KNU-021)

- Refactor the spec 30 compile into `compile(ctx, compileInput{Text, SourceLink, SourceTitle string; SourcePage bool; Agent, Actor string})`.
  Ingested sources call it with `SourcePage: true`; promotion with `SourcePage: false`,
  `SourceLink: "[[journal/<day>]]"`, actor `promotion`.
- A compile mutex makes queue compiles and promotion run one at a time (REQ-KNW-023).
- For each `journal/*.md`: `offset` from `promote_marks`; size < offset → offset 0; new text =
  bytes from offset; empty → skip. Compile with the knowledge agent; on success offset = size.
- The compile system prompt gets one extra line when `SourcePage` is false:
  `This source is the owner's journal. Do not create a page for the journal itself.`

## 8. Lint (`agent/knowledge/lint.go`; REQ-KNU-022, -023)

Deterministic part over all `wiki/` pages (parsed with spec 30 `ParsePage`):

- Links: `\[\[([^\]|#]+)(#[^\]|]*)?(\|[^\]]*)?\]\]`; a target is fine when it names a page title or
  alias (case-insensitive) or an existing vault-relative file (with or without `.md`).
- Orphans: pages with `type` ≠ `source` and no inbound link from another page.
- Duplicates: fold titles and aliases (lower case, strip accents via `golang.org/x/text` if already in
  the module graph, else a small Latin table, drop non-letters/digits); a folded key held by two
  pages is a pair.
- Missing: empty `type` or `description`.
- Conflicts: lines containing `**Conflict:**`, with page and line number.

LLM part: pages whose file `mtime` > `last_consolidation_at` (at most 20, newest first). One request
per page with the page and up to 3 pages it links (each cut to 4,000 runes), knowledge agent model,
`MaxTokens 1024`, 90 s timeout, no tools. Reply JSON
`[{"page": "...", "claim": "...", "contradicts": "...", "where": "Title or section"}]`, at most 5.
Errors skip that page.

Report `reports/lint-YYYY-MM-DD.md` (same-day rerun overwrites the report; reports are lana's own
files):

```markdown
# Knowledge lint 2026-11-01

Pages: 42. Problems: 7.

## Contradictions
- [[Proyek X]]: "deadline 15 Nov" contradicts "deadline 20 Nov" ([[Rapat Q4]])
## Marked conflicts
- [[Pak Budi]] line 18: **Conflict:** ...
## Likely duplicates
- [[Budi Santoso]] and [[Pak Budi]]
## Broken links
- [[Proyek X]] → [[Proyek Y]]
## Orphan pages
- [[Catatan lama]]
## Missing details
- [[Kantor]]: no description
```

Empty sections are omitted; no problems → `No problems found.` Lint never writes under `wiki/`.
`reports/` is not indexed (spec 30 §3 already skips it).

## 9. Runtime and daemon wiring

- `lana.go`:
  - `(*Runtime).StartKnowledgeUpkeep(ctx)`: looks up the knowledge service as `core.KnowledgeAdmin`;
    no-op when absent.
  - `KnowledgeStatus`, `KnowledgeReport`, `KnowledgeUpload(name, data)`, `KnowledgeConsolidate` pass
    through; `KnowledgeConsolidate` runs `Consolidate` in a goroutine after checking busy, so the
    HTTP call returns at once.
  - `IngestKnowledge(ctx, KnowledgeSave{Address, Channel, Agent, Title, Ext string; Data []byte; URL, Text string})`:
    URL → `tools.FetchMarkdown(ctx, url)` (exported from the spec 30 shared helper); text → `.md`;
    session = the channel's current session for `Address` and `Agent` when one exists. Returns
    `core.KnowledgeIngestResult`.
- `cmd/lana/daemon.go`: call `rt.StartKnowledgeUpkeep(runCtx)` after channels start; the web bridge
  implements `web.KnowledgeBackend`; the Telegram bridge implements `telegram.KnowledgeSaver`.

## 10. Telegram `/save` (API-KNU-002)

- `channels/telegram/channel.go`: optional `KnowledgeSaver` interface on the backend:
  `SaveKnowledge(ctx, address, agent string, item SaveItem) (path string, duplicate bool, err error)`
  with `SaveItem{Title, Ext string; Data []byte; URL, Text string}`.
- `handleCommand` case `save`: owner-only in groups; target message = `m.ReplyToMessage` when set,
  else `m`. Document (`.md`, `.txt`, `.pdf`, `.html`) → download with the existing file download code,
  title = file name without extension. Else args text: one URL → `URL`; else `Text` (title = first
  line ≤ 80 runes). Else reply-to text or caption → `Text`. Nothing → the "nothing to save" reply.
- Caption: `handleMedia` routes a document whose trimmed caption starts with `/save` to the same code
  path instead of a turn.
- `/help` text lists `/save`.

## 11. Web (API-KNU-003–006, REQ-KNU-040, -041)

- `web/knowledge.go`: `KnowledgeBackend` interface (status, report, upload, consolidate), detected in
  `SetBackend` like `DashboardBackend`; routes through `s.Handle`. Upload uses `MaxBytesReader`
  (30 MB per file, 100 MB per request).
- UI: `web/ui/src/views/knowledge.tsx`, nav entry "Knowledge" after Skills, API client functions,
  `#/knowledge` route. Layout: status row (path, counts, embedding, agent or agent error), "Upload to
  inbox" file input (multiple), "Run consolidation now" button (disabled while running; shows busy),
  pending/failed sources and unsupported inbox files, latest report rendered with the existing
  markdown renderer, recent log entries. Poll status every 10 s while a run is in progress.
- Rebuild `web/dist`.

## 12. Verification

- Store: `TextHeads`, `TextRows` rune cap and order.
- `agent/knowledge`: AC-KNU-001, -002 (fake compactor observer), -003, -005 (fake clock), -006
  (fixture vault; assert no `wiki/` mtime changes), -007.
- `agent/compaction`: observer called once after a stored summary, not on failure.
- `channels/telegram`: AC-KNU-004.
- `web`: handler tests for AC-KNU-008; vitest for the page.
- Commands: `nix develop -c go vet ./...`, `nix develop -c go test -count=1 ./...`,
  `cd web/ui && npx vitest run && npm run build`.
