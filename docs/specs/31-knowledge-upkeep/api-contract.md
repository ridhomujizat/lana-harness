# API contract: Knowledge upkeep

- **Status**: Approved (on behalf of the owner, gate lifted 2026-11-01)
- **Revision**: 1

## API-KNU-001: config keys in `[knowledge]`

| Key | Type | Default | Rule |
|---|---|---|---|
| `agent` | string | first agent in name order with a knowledge tool | must name a configured agent |
| `journal` | bool | `true` | REQ-KNU-003 |
| `consolidate_at` | string | `"03:00"` | `HH:MM` 24-hour local time, or `""` to turn nightly runs off |

Errors: `knowledge.agent: unknown agent "x"`, `knowledge.consolidate_at: want HH:MM or ""`.

## API-KNU-002: Telegram `/save`

- Forms: reply `/save` to a message; `/save <text>`; `/save <url>`; `/save` as a document caption.
- Owner-only in groups (same rule as `/new`). Added to the `/help` command list.
- Replies: `✓ saved <path> — compiling into wiki pages`, `· already saved as <path>`,
  `✗ nothing to save: reply to a message or add text`, `✗ unsupported file type <ext>`,
  `✗ knowledge is not available`, `✗ only the owner can use /save`.

## API-KNU-003: `GET /v1/knowledge`

Registered through `Server.Handle`. Lists are never `null`. Times are Unix milliseconds, `0` = never.

```json
{
  "available": true,
  "path": "/Users/ridho/.lana/knowledge",
  "exists": true,
  "counts": { "pages": 42, "sources": 10, "journal_days": 7 },
  "sources_pending": ["sources/2026-11-01 Rapat Q4.pdf"],
  "sources_failed": [{ "path": "sources/x.md", "error": "invalid JSON from model" }],
  "inbox_unsupported": ["inbox/notes.docx"],
  "embedding": { "provider": "openai", "model": "text-embedding-3-small", "chunks": 900, "vectors": 880 },
  "agent": "lana",
  "agent_error": "",
  "consolidation": {
    "running": false, "last_at": 1761958800000, "last_error": "", "next_at": 1762045200000,
    "report": "reports/lint-2026-11-01.md"
  },
  "log": [{ "heading": "2026-11-01 03:00 consolidation", "lines": ["- created [[Proyek X]]"] }]
}
```

`available: false` (other fields zero) when the runtime has no knowledge service.
`embedding.provider` is `""` when keyword-only. `log` holds the 20 newest `log.md` entries, newest
first.

## API-KNU-004: `GET /v1/knowledge/report`

`200` `{ "path": "reports/lint-2026-11-01.md", "markdown": "..." }`; `404` `not_found` when no report
exists.

## API-KNU-005: `POST /v1/knowledge/upload`

`multipart/form-data`, field `file` (one or more), each ≤ 30 MB, extensions `.md`, `.txt`, `.pdf`,
`.html`. `200` `{ "saved": ["inbox/Rapat Q4.pdf"] }`. Name clashes in `inbox/` get ` (2)`. Other
extension → `400` `invalid_request` naming the file. Triggers an inbox scan.

## API-KNU-006: `POST /v1/knowledge/consolidate`

`202` `{ "started": true }`; `409` `{ "error": { "code": "busy", ... } }` when a run is in progress;
`503` `unavailable` when not in the daemon.
