# API contract: Domain knowledge per agent

- **Status**: Approved (on behalf of the owner, gate lifted 2026-11-10)
- **Revision**: 1

## API-DKN-001: tool inputs

### `knowledge_search`

New optional property:

```json
"scope": {"type": "string", "description": "mine (default: your domain and the shared wiki), domain (your domain only), shared, all, or agent:<name> for another agent's domain."}
```

Invalid value → tool error `invalid input: scope must be mine, domain, shared, all, or agent:<name>`.
`agent:<name>` for a name with no domain folder → `no knowledge domain for agent "<name>"`.
Each output line gains the scope in brackets after the path: `1. Gaji (agents/dana/wiki/Gaji.md) [dana] …`,
`[shared]` for shared files.

### `knowledge_read`

New optional property `scope` with the same values, used only when `title` is given; default
resolution order is REQ-DKN-011.

### `knowledge_write`

- New optional property `scope`: `mine`, `shared`, or `agent:<name>` (default REQ-DKN-020).
- `action` enum gains `supersede`. `supersede` needs `title`, `old_text` (one line), `new_text`;
  optional `source` (a page or source title, rendered as a wikilink).
- New optional property `source` (string), used by `supersede` only.
- Proposal reply (REQ-DKN-021): `proposed to rio as p_01JC…; rio applies it at its next promotion`.
- Errors: `old_text must be one line`, `old_text matches <n> places; use more specific text`,
  `no text in <title> matches old_text`, `agent "<x>" has no knowledge domain`,
  `you have no knowledge domain; use scope shared`.

### `skill_write`

Unchanged input. Replies: `proposed skill <name>; the owner reviews it in Knowledge → Review`
(staged), or the existing `created skill <name>` / `updated skill <name>` (direct).

## API-DKN-002: config key `[agents.<name>] skill_write`

| Key | Type | Default | Rule |
|---|---|---|---|
| `skill_write` | string | `"approve"` | `"approve"` or `"direct"` (REQ-DKN-050) |

Error: `agents.<name>.skill_write: want "approve" or "direct"`.

## API-DKN-003: `GET /v1/knowledge` additions

```json
{
  "scopes": [
    { "scope": "shared", "pages": 42, "journal_days": 7, "updated_at": 1762045200000 },
    { "scope": "dana", "pages": 12, "journal_days": 3, "updated_at": 1762041600000 }
  ],
  "review_pending": 3
}
```

`scopes` lists `shared` first, then each domain folder in name order; never `null`. `counts`
keeps its meaning (all scopes together). `review_pending` = pending proposals + pending skill
proposals.

## API-DKN-004: `GET /v1/knowledge/pages`

Query: `scope` (`all` default, `shared`, or an agent name), `q` (optional text filter, matched
against title, aliases, and description, case-insensitive), `limit` (1–500, default 200).

`200`:

```json
{
  "pages": [
    {
      "path": "agents/dana/wiki/Gaji.md", "title": "Gaji", "scope": "dana", "type": "topic",
      "description": "Aturan gaji tim", "updated": "2026-11-10", "mtime": 1762041600000
    }
  ],
  "total": 54
}
```

Sorted by `mtime` newest first. `total` is the count before `limit`. `400 invalid_request` for a
bad `limit` or `scope`. `503 unavailable` without a knowledge service.

## API-DKN-005: `GET /v1/knowledge/page`

Query: `path` (vault-relative, must end in `.md` and stay inside `wiki/` or `agents/*/wiki/` or
`agents/*/journal/` or `journal/`). `200` `{ "path": "...", "title": "...", "scope": "dana", "markdown": "..." }`.
`400 invalid_request` for a path outside those folders or with `..`; `404 not_found`.

## API-DKN-006: `GET /v1/review`

`200`:

```json
{
  "items": [
    {
      "id": "p_01JC5…", "kind": "knowledge", "agent": "rio", "from": "dana",
      "title": "Gaji", "action": "append", "body": "…", "owner_turn": true,
      "session_id": "06GS…", "created_at": 1762041600000
    },
    {
      "id": "s_dana.payroll-check", "kind": "skill", "agent": "dana", "from": "dana",
      "title": "payroll-check", "action": "update", "description": "When …", "body": "…",
      "owner_turn": true, "session_id": "06GS…", "created_at": 1762041000000
    }
  ]
}
```

Newest first, pending only. `action` for knowledge items is the requested write action; for
skills `create` or `update` (whether the skill exists now). `body` for knowledge items is a
readable rendering of the requested change (text, or `old → new` for replace/supersede).

## API-DKN-007: `POST /v1/review/{id}/approve` and `POST /v1/review/{id}/reject`

`200` `{ "id": "...", "status": "approved" | "applied" | "rejected" }`. Skill approve returns
`applied`; knowledge approve returns `approved` and requests a promotion of the target agent.
`404 not_found` for an unknown or already decided id. `400 invalid_request` when the skill body no
longer passes the skill rules. Both routes need the same auth and CSRF as other POST routes.

## API-DKN-008: `GET /v1/agents/{name}/overview` addition

```json
"knowledge": {
  "available": true,
  "pages": 12,
  "changed_7d": 3,
  "weeks": [0, 0, 1, 2, 0, 4, 2, 3],
  "recent": [{ "title": "Gaji", "path": "agents/dana/wiki/Gaji.md", "updated": "2026-11-10" }]
}
```

`weeks` holds 8 counts of domain pages whose `mtime` falls in each 7-day bucket, oldest first, the
last bucket ending now. `recent` holds at most 5 pages, newest first. `available: false` (other
fields zero, lists empty) when the agent has no knowledge tool or the runtime has no knowledge
service. For a team, `knowledge` describes the lead.

## API-DKN-009: `GET`/`PUT /v1/agents/{name}/files/MEMORY.md`

Limit becomes 8,000. `PUT` with a body over the limit returns `400 invalid_request`
(`MEMORY.md is <n>/8000 characters`) unless the body is not longer than the current file
(REQ-DKN-040).
