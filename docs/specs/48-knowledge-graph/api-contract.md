# API contract: Knowledge graph and tabbed Knowledge page

- **Status**: Approved
- **Revision**: 1

All routes need the dashboard session, like the other `/v1/knowledge` routes. Errors use the
usual `{"error": {"code", "message"}}` body. 503 `unavailable` when knowledge is off.

## API-KGR-001 `GET /v1/knowledge/graph`

Query:

| Param | Values | Default |
|---|---|---|
| `scope` | `all`, `shared`, agent name `^[a-z][a-z0-9_-]{0,31}$` | `all` |
| `journals` | `0`, `1` | `0` |
| `sources` | `0`, `1` | `0` |

Response 200:

```json
{
  "nodes": [
    {"id": "agents/dana/wiki/Kebijakan Penggajian.md", "title": "Kebijakan Penggajian",
     "scope": "dana", "kind": "page", "type": "topic", "updated": "2026-11-11", "degree": 5}
  ],
  "hubs": [{"scope": "dana", "pages": 24}, {"scope": "shared", "pages": 18}],
  "edges": [{"source": "agents/dana/wiki/Kebijakan Penggajian.md", "target": "wiki/PPh 21.md"}],
  "counts": {"pages": 86, "links": 142, "broken": 3},
  "truncated": false
}
```

`kind` is `page`, `journal`, or `source`. Edges only join nodes in `nodes`. Hub edges are not
listed; every node joins the hub of its `scope`. 400 `invalid` on a bad parameter.

## API-KGR-002 `GET /v1/knowledge/page` (extended)

Existing fields `path, title, scope, markdown` stay. Added:

```json
{
  "type": "topic", "description": "", "updated": "2026-11-11",
  "sources": ["agents/dana/journal/2026-11-01"],
  "links_out": [{"path": "wiki/PPh 21.md", "title": "PPh 21", "scope": "shared"}],
  "unresolved": ["Slip Gaji"],
  "links_in": [{"path": "agents/dana/wiki/Tagihan Bulanan.md", "title": "Tagihan Bulanan",
                "scope": "dana", "context": "transfer gaji masuk tagihan tetap tanggal 25 …"}]
}
```

Lists are never null. `links_in` excludes the page itself.

## API-KGR-003 `GET /v1/review?status=decided`

`status` is `pending` (default, unchanged response) or `decided`. Decided response:

```json
{"items": [{"id": "p_…", "kind": "knowledge", "agent": "karin", "from": "dana",
            "title": "Gaji Pasar 2026", "action": "append", "body": "…",
            "status": "applied", "result": "", "created_at": 1762800000, "decided_at": 1762810000}]}
```

At most 20, newest `decided_at` first. `ReviewItem` gains optional `status`, `result`,
`decided_at`, `old_text` (the op's `OldText` for replace and supersede; also on pending items).

## API-KGR-004 `GET /v1/knowledge/activity`

Query: `limit` 1–200 (default 50); `before` (optional, RFC 3339 or `YYYY-MM-DDTHH:MM`, entries
strictly older); `kind` (optional, one of the kinds below, or `writes` = promotion + write).

```json
{
  "entries": [
    {"at": "2026-11-11T06:44:00+07:00", "heading": "consolidation", "kind": "run", "actor": "",
     "lines": ["journaled 11 sessions", "promoted lana: 2 ops", "report [[reports/lint-2026-11-11]]"]}
  ],
  "next_before": "2026-11-10T13:42:00+07:00"
}
```

Kinds: `run`, `promotion`, `write`, `review`, `lint`, `error`, `other`. Lines drop the leading
`- `. `next_before` is empty when there is nothing older. Times are local (vault time zone).

## API-KGR-005 `GET /v1/knowledge/health`

```json
{
  "last_run": {"at": "2026-11-11T06:44:00+07:00", "journaled": 11, "applied": 2, "skipped": 0,
               "error": ""},
  "next_at": "2026-11-12T03:00:00+07:00",
  "lint": {"date": "2026-11-11", "path": "reports/lint-2026-11-11.md", "pages": 2,
           "contradictions": 0, "conflicts": 0, "duplicates": 0, "broken": 0, "orphans": 1,
           "missing": 0},
  "review_pending": 0,
  "memory": [{"agent": "lana", "characters": 5210, "limit": 8000}]
}
```

`last_run` and `lint` are null when there is none.
