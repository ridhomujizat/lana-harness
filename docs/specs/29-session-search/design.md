# Design: Session search

- **Requirements**: [requirements.md](requirements.md) — revision 1, Approved
- **Contract**: [api-contract.md](api-contract.md) — Not required
- **Status**: Approved (on behalf of the owner, gate lifted 2026-11-01)
- **Design revision**: 1

## Architecture decision scan

| Topic | Existing authority | Action |
|---|---|---|
| Persistence | ADR 0009 (append-only event log), `store/migrations` | reuse: a derived table beside `session_events`, written in the same write |
| Public interface | none (model tool only) | none |
| Concurrency | `Store.withWrite` single writer | reuse |
| Layering | `internal/archtest` (tools must not import store) | optional core interface, like `core.EventScanner` |

Diagram: not required. One component, no new boundary.

## 1. Schema (REQ-SSR-001–003)

`store/migrations/0004_session_text.sql`:

```sql
CREATE TABLE session_text (
  session_id TEXT    NOT NULL,
  seq        INTEGER NOT NULL,
  role       TEXT    NOT NULL CHECK (role IN ('user', 'assistant')),
  created_at INTEGER NOT NULL,
  text       TEXT    NOT NULL,
  UNIQUE (session_id, seq)
);

CREATE VIRTUAL TABLE session_text_fts USING fts5(
  text,
  content = 'session_text',
  content_rowid = 'rowid',
  tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TRIGGER session_text_ai AFTER INSERT ON session_text BEGIN
  INSERT INTO session_text_fts (rowid, text) VALUES (new.rowid, new.text);
END;

CREATE TRIGGER session_text_ad AFTER DELETE ON session_text BEGIN
  INSERT INTO session_text_fts (session_text_fts, rowid, text) VALUES ('delete', old.rowid, old.text);
END;
```

`session_text` is a rowid table (FTS5 external content needs a rowid). It has no append-only
trigger: it is a projection, not the log.

`store/store.go`:

- `schemaVersion = 4`, `names[4] = "session_text"`.
- `migrate`: for `v == 4`, run the SQL and then `backfillSessionText(ctx, tx)` in the same
  transaction before `PRAGMA user_version = 4`. Backfill reads
  `SELECT session_id, seq, kind, payload, created_at FROM session_events WHERE kind IN ('user/message','assistant/message')`
  and inserts one row per event that has conversation text. An error rolls the transaction back
  (REQ-SSR-003).
- `conversationText(kind string, payload []byte) (role, text string, ok bool)` (private, store
  package):
  - `user/message`: decode `core.UserMessage`; `Runtime` → not ok; take `Content[ContextBlocks:]`
    (guard the index), keep blocks with `Kind == core.BlockText`, join their `Text` with `"\n"`.
  - `assistant/message`: decode `core.AssistantMessage`; keep `core.BlockText` blocks only.
  - Trimmed empty text → not ok.
- `Append`: inside the existing write, after each event row insert, call `conversationText` on the
  marshaled payload and insert into `session_text` with the event's `session_id`, `seq`,
  `created_at`.
- `Prune`: `DELETE FROM session_text WHERE session_id = ?` before the event delete.

## 2. Store search API

`core/sessionsearch.go`, an optional extension of `SessionStore` (the SQLite store implements it;
`memstore` does not):

```go
type SessionSearcher interface {
	SearchText(ctx context.Context, q TextQuery) ([]TextHit, error)
	// TextWindow resolves sessionID (full id or unique prefix) and returns the
	// session and up to radius conversation rows on each side of aroundSeq.
	TextWindow(ctx context.Context, sessionID string, aroundSeq int64, radius int) (SessionInfo, []TextRow, error)
}

type TextQuery struct {
	Terms         []string // already tokenized, lower case
	Agent         string   // "" = all agents
	CallerSession string   // REQ-SSR-015
	Limit         int      // sessions
	Context       int      // rows on each side of the first hit (REQ-SSR-012)
}

type TextRow struct {
	SessionID string
	Seq       int64
	Role      string // "user" | "assistant"
	CreatedAt int64
	Text      string
}

type TextHit struct {
	Session SessionInfo
	Row     TextRow
	Snippet string
	Around  []TextRow // only on the first hit; rows in seq order, the hit included
}
```

`Store.SearchText`:

1. Empty `Terms` → `nil, nil`.
2. MATCH string: each term wrapped in double quotes (inner `"` doubled), joined with ` OR `.
3. Query (one statement):

```sql
SELECT t.session_id, t.seq, t.role, t.created_at, t.text,
       snippet(session_text_fts, 0, '«', '»', '…', 24),
       s.agent, s.channel, s.title, s.address, s.parent_session_id, s.inherited_seq
FROM session_text_fts
JOIN session_text t ON t.rowid = session_text_fts.rowid
JOIN sessions s ON s.id = t.session_id
WHERE session_text_fts MATCH ?
  AND (s.parent_session_id IS NULL OR COALESCE(s.inherited_seq, 0) > 0)
  AND (? = '' OR s.agent = ?)
ORDER BY bm25(session_text_fts)
LIMIT 300
```

4. Caller rule: read the caller's latest `compaction/summary` payload
   (`SELECT payload FROM session_events WHERE session_id = ? AND kind = 'compaction/summary' ORDER BY seq DESC LIMIT 1`);
   `upto = UptoSeq` or 0. Drop caller rows with `seq > upto` (all of them when 0).
5. Keep the first (best) row per session, in rank order, up to `Limit`.
6. For the first hit, fill `Around` with
   `SELECT ... FROM session_text WHERE session_id = ? AND seq BETWEEN ...` using the `UNIQUE`
   index: `Context` rows before (`seq < hit ORDER BY seq DESC LIMIT n`, then reversed), the hit, and
   `Context` rows after. For the caller session, the same `upto` cap applies.

`Store.TextWindow`: resolve with `FindPrefix` (0 → `ErrSessionNotFound`, >1 → error
`session id prefix is ambiguous`), then the same before/after queries with `radius`.

## 3. Tool `session_search` (REQ-SSR-010–018)

`tools/session_search.go`. Risk `core.RiskRead`. Input schema:

```json
{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "Words to look for in earlier conversations."},
    "agent": {"type": "string", "description": "Only this agent's sessions. Optional."},
    "limit": {"type": "integer", "description": "Sessions to return, 1-10. Default 5."},
    "session_id": {"type": "string", "description": "Read a session around around_seq instead of searching."},
    "around_seq": {"type": "integer", "description": "Message seq to center on, from a search result."}
  }
}
```

Description: `Search earlier conversations with the owner, across sessions and agents. Give query to find matches, or session_id with around_seq to read more of one session.`

- `ToolCtx` gets `Sessions core.SessionSearcher`. `agent/turn.go` sets it when `rn.reg.store`
  implements the interface. Nil → error output `session search is not available`.
- Mode: `session_id` set → scroll (needs `around_seq` ≥ 1, else input error); otherwise `query`
  must be non-blank (input error).
- Tokenizing (REQ-SSR-011): lower-case runs of letters and digits, at least 2 runes, not in the
  stop-word list of `internal/agenthome` (export `agenthome.IsStopWord`), deduplicated, first 12.
  No terms left → `No past conversation matched "<query>".`
- `limit` clamped to 1..10, default 5. `Context = 3` (REQ-SSR-012), `radius = 8` (REQ-SSR-013).
- Role label: `owner` for `user`, the session's agent name for `assistant`. Times are local
  `2006-01-02 15:04`.
- Discovery output:

```text
Found 2 sessions for "deploy staging":

1. session 06GS2SGJJSEVXER9PGXKGS0YZG (lana, telegram, "Deploy staging"), 2026-10-28 14:02
   seq 140 owner: …
 > seq 142 owner: «deploy» ke «staging» gagal karena …
   seq 143 lana: …
2. session 06GS2SKVCH70CEBAVX4WFTDN64 (lana, web, "Untitled"), 2026-10-20 09:13
 > seq 18 lana: … «staging» …

Read more with session_search session_id=<id> around_seq=<seq>.
```

  The `>` line shows the FTS snippet; context lines show the row text cut to 400 runes with `…`.
- Scroll output: header `session <id> (<agent>, <channel>, "<title>"), seq <first>–<last>:`, then
  one line block per row `seq <n> <time> <role>: <text cut to 1500 runes>`; the target row is marked
  with `>`.
- Total output cut at 12,000 runes with `\n[output cut]` (REQ-SSR-016).
- `PromptGuidance()`: `Use session_search when the owner mentions an earlier conversation or you need a detail that is not in memory or the current context. Quote what you find; do not guess.`

Other wiring:

- `tools/module.go`: register `NewSessionSearch()`.
- `lana.go` `toolCatalogGroupOrder`: Memory group names become
  `memory_read, memory_write, session_search`.
- `agent/loopguard.go` `loopGuardReadOnly`: add `session_search` (REQ-SSR-018).

## 4. Verification

- Store (`store/store_test.go`): AC-SSR-001 (append, runtime note, `context_blocks`, prune),
  AC-SSR-002 (build a v3 database with the old migrations and raw event rows, open with the new
  binary, search finds them), ranking (two terms beat one), subagent exclusion, fork inclusion,
  caller `upto` rule, `TextWindow` order and prefix resolution, MATCH safety for `"(*` and `OR`.
- Tool (`tools/session_search_test.go`) with a fake `SessionSearcher`: modes, input errors,
  tokenizing, limit clamp, formatting, 12,000-rune cut, nil searcher.
- `agent/loopguard_test.go` read-only case; catalog test in `lana_test.go` or the existing catalog
  test.
- Commands: `nix develop -c go vet ./...`, `nix develop -c go test -count=1 ./...`.
