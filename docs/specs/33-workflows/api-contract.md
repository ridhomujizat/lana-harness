# Contract: Workflows

- **Issue**: `—`
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 1
- **Requirements status**: Approved
- **Contract revision**: 1
- **Applies to**: HTTP / events (SSE) / Telegram callbacks
- **Status**: Approved

## Shared conventions

| Topic | Decision or authority |
|---|---|
| Authentication | Web session cookie + CSRF header via `Server.Handle` (spec #9). Exception: `API-WF-020` webhook uses `X-Lana-Token`. |
| Errors | `{"error":{"code","message","details"}}`. Graph problems: `details = [{"issues": [{node_id, code, message}]}]`. |
| Serialization | JSON, snake_case. Times are Unix milliseconds. Ids are strings. |
| Versioning | Paths under `/v1`; additions only. |

## Operation index

| Operation ID | Method | Path | Requirements |
|---|---|---|---|
| `API-WF-001` | `GET` | `/v1/workflows` | REQ-WF-001, REQ-WF-011 |
| `API-WF-002` | `POST` | `/v1/workflows` | REQ-WF-001, REQ-WF-002 |
| `API-WF-003` | `GET` | `/v1/workflows/{id}` | REQ-WF-001 |
| `API-WF-004` | `PUT` | `/v1/workflows/{id}` | REQ-WF-001, REQ-WF-002, REQ-WF-009 |
| `API-WF-005` | `DELETE` | `/v1/workflows/{id}` | REQ-WF-001 |
| `API-WF-006` | `GET` | `/v1/workflows/{id}/versions` | REQ-WF-001 |
| `API-WF-007` | `POST` | `/v1/workflows/{id}/restore` | REQ-WF-001, REQ-WF-010 |
| `API-WF-008` | `GET` | `/v1/workflows/{id}/lock` | REQ-WF-009 |
| `API-WF-009` | `POST` | `/v1/workflows/{id}/activate` | REQ-WF-009 |
| `API-WF-010` | `POST` | `/v1/workflows/{id}/pause`, `/resume` | REQ-WF-003, REQ-WF-008 |
| `API-WF-011` | `POST` | `/v1/workflows/{id}/runs` | REQ-WF-003 |
| `API-WF-012` | `GET` | `/v1/workflows/{id}/runs` | REQ-WF-007 |
| `API-WF-013` | `GET` | `/v1/workflows/{id}/runs/{run}` | REQ-WF-004, REQ-WF-007 |
| `API-WF-014` | `POST` | `/v1/workflows/{id}/runs/{run}/cancel` | REQ-WF-007 |
| `API-WF-015` | `POST` | `/v1/workflows/{id}/decisions/{decision}` | REQ-WF-005, REQ-WF-008 |
| `API-WF-016` | `GET`/`PUT`/`DELETE` | `/v1/workflows/{id}/state[/{key}]` | REQ-WF-006 |
| `API-WF-017` | `GET`/`POST` | `/v1/workflows/{id}/assistant` | REQ-WF-010 |
| `API-WF-018` | `POST` | `/v1/workflows/{id}/webhook/rotate` | REQ-WF-003 |
| `API-WF-019` | SSE `GET` | `/v1/workflows/stream` | REQ-WF-011 |
| `API-WF-020` | `POST` | `/hooks/{id}` | REQ-WF-003 |
| `API-WF-021` | (changed) `POST` | session/room message endpoints: chat command | REQ-WF-003 |
| `API-WF-022` | Telegram | callback data `w:<decision>:<choice>`, command `/<command>` | REQ-WF-003, REQ-WF-008 |

## Shared objects

### `graph`

```json
{
  "nodes": [
    {"id": "every-day", "type": "trigger.schedule", "name": "Every day 07:00",
     "position": {"x": 240, "y": 40}, "params": {"cron": "0 7 * * *", "timezone": "Asia/Jakarta", "enabled": true}},
    {"id": "each", "type": "loop", "name": "For each source", "position": {"x": 240, "y": 160},
     "params": {"items": "{{ state.sources }}"}},
    {"id": "scrape", "type": "browser", "name": "Scrape page", "parent": "each", "position": {"x": 240, "y": 280},
     "params": {"url": "https://{{ item }}", "format": "markdown", "stealth": true},
     "on_failure": {"mode": "guardian", "retries": 2, "backoff_secs": [10, 30]}}
  ],
  "edges": [
    {"id": "e1", "from": "every-day", "from_port": "out", "to": "each"},
    {"id": "e2", "from": "each", "from_port": "body", "to": "scrape"}
  ]
}
```

- `node.id`: `^[a-z0-9][a-z0-9_-]{0,47}$`, unique in the graph.
- `node.parent`: optional loop node id; the node is in that loop's body.
- `node.disabled`: optional bool; a disabled node is skipped.
- `node.on_failure`: optional `{mode: "default"|"guardian"|"stop"|"continue", retries, backoff_secs, fallback}`.
  `continue` marks the node `failed` but lets later nodes run with `null` output. `fallback` is any JSON used by the
  guardian action `fallback`.
- `edge.from_port`: `out` (default), `true`/`false` (if), a case name or `default` (switch),
  `approved`/`denied` (approval), `body`/`done` (loop: `body` edges go to the first body nodes, `done` or `out`
  edges run after all items).

### Node types and params

| `type` | Params (required **bold**) | Output |
|---|---|---|
| `trigger.manual` | `enabled` | the run input |
| `trigger.schedule` | **`cron`**, `timezone`, `enabled` | `{fired_at}` merged with input |
| `trigger.interval` | **`minutes`**, `enabled` | same |
| `trigger.webhook` | `enabled` | the request body (JSON, or `{"text": ...}`) |
| `trigger.event` | **`kind`**, `agent`, `enabled` | `{kind, session_id, agent, data}` |
| `trigger.chat` | **`command`**, `enabled` | `{text, channel}` |
| `agent` | **`prompt`**, `output_schema`, `max_tokens` | parsed JSON when a schema is set, else `{text}` |
| `bot` | **`agent`**, **`prompt`**, `output_schema` | same |
| `worker` | **`task`**, `agent`, `model`, `tools` (string[]), `max_steps` (12), `max_tokens` (20000), `output_schema` | same |
| `browser` | **`url`**, `format` (`markdown`), `stealth` (false), `selector`, `timeout_secs` (30) | `{url, format, content}` |
| `bash` | **`command`**, `timeout_secs` (120) | `{exit_code, output}` |
| `webfetch` | **`url`** | `{url, title, markdown}` |
| `tool` | **`name`**, `input` (object) | `{output}` |
| `message` | **`text`** | `{sent, text}` |
| `knowledge` | **`title`**, **`text`**, `folder` | `{saved, title}` |
| `if` | **`left`**, **`op`**, `right` | `{result, left, right}`; port `true`/`false` |
| `switch` | **`value`**, **`cases`** (string[]) | `{value, port}` |
| `loop` | **`items`** | `{items, count, ok, skipped}` |
| `wait` | **`seconds`** (1..604800) | `{waited}` |
| `approval` | **`message`**, `timeout_hours` (24) | `{approved, by}`; port `approved`/`denied` |
| `merge` | — | `{<input node id>: output, ...}` |
| `state_get` | **`key`** | `{key, value, exists}` |
| `state_set` | **`key`**, **`value`** | `{key, value}` |

`op`: `==`, `!=`, `>`, `>=`, `<`, `<=`, `contains`, `not_contains`, `exists`, `not_exists`, `truthy`, `falsy`.

`output_schema`: an object `{"field": "<type>"}` where type is `string`, `integer`, `number`, `boolean`,
`object`, `array`, `string[]`, `integer[]`, or one of those followed by a free note (`"integer 1-10"`). Every field
is required.

### `workflow` (detail)

```json
{
  "id": "riset-harian", "name": "Riset harian", "description": "Scrape 5 tech sources ...",
  "guardian": "default", "status": "active", "version": 14,
  "version_author": "guardian", "version_summary": "on failure → guardian", "version_at": 1762300000000,
  "graph": {"nodes": [], "edges": []},
  "lock": {"version": 14, "locked_at": 1762300000000,
           "tools": [{"tool": "browser", "used_by": ["Scrape page"], "risk": "read"}]},
  "lock_current": true,
  "failure_default": {"mode": "guardian", "retries": 2, "backoff_secs": [10, 30]},
  "webhook": {"path": "/hooks/riset-harian", "token": "…"},
  "consecutive_failures": 0,
  "created_at": 0, "updated_at": 0
}
```

`lock` is `null` until the first activation. `lock_current` is true when the lock tools cover every tool the
current graph needs.

### `workflow_summary`

```json
{"id": "...", "name": "...", "description": "...", "guardian": "default", "status": "active", "version": 14,
 "node_count": 9, "triggers": [{"type": "trigger.schedule", "label": "Schedule", "detail": "Every day 07:00", "enabled": true}],
 "runs_7d": 7, "success_7d": 1.0, "running": true, "waiting": false,
 "last_run": {"id": "...", "number": 7, "status": "running", "started_at": 0, "ended_at": 0},
 "bots": ["default", "researcher"], "updated_at": 0}
```

`success_7d` is `null` with no finished run in 7 days. `bots` lists the guardian then other agents used by
`bot`/`worker` nodes.

### `run`

```json
{"id": "r_…", "workflow_id": "riset-harian", "number": 7, "version": 14, "trigger": "schedule",
 "input": {}, "status": "running", "waiting_reason": "", "wake_at": 0, "error": "", "tokens": 18200,
 "nodes_done": 6, "nodes_total": 9, "summary": "digest sent", "started_at": 0, "ended_at": 0}
```

`status`: `queued` | `running` | `waiting` | `succeeded` | `failed` | `cancelled`.
`waiting_reason`: `wait` | `approval` | `owner` (guardian asked) | `tool_approval`.
`trigger`: `manual` | `schedule` | `interval` | `webhook` | `event` | `chat`.

### `node_run`

```json
{"node_id": "scrape", "item_index": 2, "status": "skipped",
 "attempts": [{"n": 1, "started_at": 0, "ended_at": 0, "error": "HTTP 403"}],
 "input": {}, "output": {}, "error": "HTTP 403", "logs": ["…"], "tokens": 0, "session_id": "",
 "started_at": 0, "ended_at": 0}
```

`item_index` is `-1` outside a loop. A loop node has one row with `item_index -1` (its own output) and body nodes have one
row per item. `status`: `pending` | `running` | `succeeded` | `failed` | `skipped` | `waiting` | `fallback`.

### `decision`

```json
{"id": "d_…", "workflow_id": "…", "run_id": "…", "run_number": 8, "node_id": "rank", "item_index": -1,
 "kind": "guardian", "action": "ask", "reason": "The ranking reply is cut off …",
 "fix": {"max_tokens": 4000}, "persistent": true, "confidence": 0.8, "tokens": 3100,
 "status": "pending", "choice": "", "created_at": 0, "expires_at": 0, "resolved_at": 0}
```

`kind`: `guardian` (error ladder) | `approval` (approval node). `action`: `retry_fix` | `skip` | `fallback` | `ask` |
`stop` (guardian) or `ask` (approval). `status`: `applied` | `pending` | `resolved` | `expired`. Choices for a pending
guardian `ask`: `apply` (save fix as a new version and retry), `run_only` (retry with the fix in this run), `skip`,
`stop`. Choices for an approval: `approve`, `deny`.

## API-WF-001: List

`GET /v1/workflows` → 200

```json
{"workflows": [workflow_summary],
 "stats": {"total": 6, "active": 5, "paused": 1, "error": 0, "waiting_on_you": 2, "approvals": 1, "decisions": 1,
           "success_7d": 0.942, "success_prev_7d": 0.924, "runs_today": 31, "runs_today_ok": 28,
           "runs_today_failed": 2, "runs_today_running": 1},
 "attention": [decision + {"workflow_name": "…", "node_name": "…"}]}
```

`attention` holds pending decisions (both kinds), newest first, plus pending tool approvals of workflow sessions
as `kind: "tool_approval"` with `id` = the web approval id (answered through the existing `POST /v1/approvals/{id}`).

## API-WF-002: Create

`POST /v1/workflows` `{"name", "description", "guardian", "graph"?}` → 201 `workflow`.
Errors: 400 `invalid_name`, 409 `workflow_exists`, 400 `unknown_agent`, 400 `invalid_graph`.

## API-WF-003: Get

`GET /v1/workflows/{id}` → 200 `workflow`; 404 `workflow_not_found`.

## API-WF-004: Update

`PUT /v1/workflows/{id}` `{"base_version", "name"?, "description"?, "guardian"?, "graph"?, "failure_default"?, "summary"?}`
→ 200 `workflow`. A graph change saves a new version (author `owner`). 409 `version_conflict` when `base_version` is not
the current version. A guardian change, or a graph that makes `lock_current` false, sets an `active` workflow to `draft`.

## API-WF-005: Delete

`DELETE /v1/workflows/{id}` → 204. Cancels live runs, removes the workflow, its versions, runs, state and chat.

## API-WF-006 / 007: Versions and restore

`GET /v1/workflows/{id}/versions` → `{"versions": [{"version", "author", "summary", "created_at", "node_count"}]}` newest first.
`POST /v1/workflows/{id}/restore` `{"version"}` → 200 `workflow` (a new version with summary `restore vN`).

## API-WF-008 / 009: Lock and activate

`GET /v1/workflows/{id}/lock` → `{"tools": [{"tool", "used_by", "risk"}]}` (what activation would lock).
`POST /v1/workflows/{id}/activate` `{"version"}` → 200 `workflow` with `status: active` and the new lock.
409 `version_conflict` if `version` is not current; 400 `invalid_graph` if the graph has no trigger.

## API-WF-010: Pause and resume

`POST /v1/workflows/{id}/pause` → 200 `workflow` (`paused`). `POST /v1/workflows/{id}/resume` → 200 `workflow`
(`active`, `consecutive_failures` reset); 409 `lock_required` when `lock_current` is false.

## API-WF-011: Start a run

`POST /v1/workflows/{id}/runs` `{"input"?: any}` → 202 `run` (trigger `manual`). Works for any status except when a run
of the workflow is already `running` (409 `run_active`).

## API-WF-012: List runs

`GET /v1/workflows/{id}/runs?limit=50&before=<number>` → `{"runs": [run]}` newest first.

## API-WF-013: Run detail

`GET /v1/workflows/{id}/runs/{run}` (`run` = id or number) → `{"run", "graph", "nodes": [node_run], "decisions": [decision]}`.
`graph` is the version the run used.

## API-WF-014: Cancel

`POST /v1/workflows/{id}/runs/{run}/cancel` → 200 `run` (`cancelled`). 409 `run_finished`.

## API-WF-015: Answer a decision

`POST /v1/workflows/{id}/decisions/{decision}` `{"choice"}` → 200 `run`. 409 `decision_resolved`, 400 `invalid_choice`.

## API-WF-016: State

`GET /v1/workflows/{id}/state` → `{"entries": [{"key", "value", "updated_at"}]}`.
`PUT /v1/workflows/{id}/state/{key}` `{"value"}` → 200 entry. `DELETE /v1/workflows/{id}/state/{key}` → 204.
`DELETE /v1/workflows/{id}/state` → 204 (clear).

## API-WF-017: Assistant

`GET /v1/workflows/{id}/assistant` → `{"messages": [{"id", "role": "owner"|"guardian", "text", "change"?, "issues"?, "created_at"}]}`.
`POST /v1/workflows/{id}/assistant` `{"message", "base_version"}` → 200

```json
{"message": {"id": 3, "role": "guardian", "text": "…", "change": {"from_version": 13, "to_version": 14,
  "summary": "…", "added": ["Scrape page"], "removed": [], "changed": ["Send digest"]}, "issues": [], "created_at": 0},
 "workflow": workflow, "tokens": 2300}
```

Blocks until the guardian answers (up to 3 minutes). 409 `version_conflict`, 503 `guardian_unavailable`.

## API-WF-018: Rotate webhook secret

`POST /v1/workflows/{id}/webhook/rotate` → `{"token"}`.

## API-WF-019: Stream

`GET /v1/workflows/stream` (SSE). Events: `changed` `{"workflow_id", "run_id"}` whenever a workflow, run, node run or
decision changes; `ping` comments every 15 s; `shutdown` on server stop. Clients refetch what they show.

## API-WF-020: Webhook

`POST /hooks/{id}` with header `X-Lana-Token: <token>` and any body up to 1 MiB → 202 `{"run_id", "number"}`.
401 `bad_token`, 404 `workflow_not_found` (also when the webhook trigger is disabled or the workflow is not active).
No session cookie, CSRF, Origin or Host check.

## API-WF-021: Chat command

When the text sent to `POST /v1/sessions/{id}/messages` (or a room message) is `/<command> [rest]` and an active
workflow has an enabled `trigger.chat` with that command, no turn starts and the response is
200 `{"command": {"workflow_id", "run_id", "number", "reply": "Run #8 of Riset harian started"}}`.

## API-WF-022: Telegram

- Owner chat command `/<command> [rest]`: same match as API-WF-021; the bot replies the same text.
- Notices for guardian decisions, failures and approval nodes go to every owner private chat. Pending choices are
  inline buttons with callback data `w:<decision id>:<choice>` (`apply`, `run_only`, `skip`, `stop`, `approve`, `deny`).
- A callback answers the decision like API-WF-015 and edits the message to show the result.
