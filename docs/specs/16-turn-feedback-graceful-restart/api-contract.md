# Contract: Turn feedback, graceful restart, and live config changes

- **Requirements**: [requirements.md](requirements.md), revision 1, Approved
- **Contract revision**: 1
- **Applies to**: session events, HTTP `/v1`, Telegram callbacks, `mcp_manage` output
- **Status**: Approved

Spec #9 conventions apply (owner session, CSRF, error envelope).

## Operation index

| ID | Type | Operation | Requirements |
|---|---|---|---|
| `API-TFB-001` | Event | `turn/end` reason `interrupted` | `REQ-TFB-001` |
| `API-TFB-002` | HTTP | `POST /v1/daemon/restart` (changed), `DELETE /v1/daemon/restart` (new), `GET /v1/daemon` (field added) | `REQ-TFB-002`, `REQ-TFB-003` |
| `API-TFB-003` | HTTP | `POST /v1/sessions/{id}/continue` (new) | `REQ-TFB-005` |
| `API-TFB-004` | Event | `user/message` with `continue: true` | `REQ-TFB-005` |
| `API-TFB-005` | Telegram | end line + `Continue` callback `c:<session short id>` | `REQ-TFB-004`, `REQ-TFB-005` |
| `API-TFB-006` | HTTP | `restart_required` on agent, team, and MCP saves | `REQ-TFB-006` |
| `API-TFB-007` | Tool | `mcp_manage list` state column | `REQ-TFB-007` |
| `API-TFB-008` | HTTP | `503 daemon_draining` on new turns | `REQ-TFB-002` |

## API-TFB-001: `interrupted`

`turn/end` payload: `{ "reason": "interrupted", "error": { "message": "lana restarted while this turn was running" }, ... }`. For a plain stop the message is `lana stopped while this turn was running`. Existing reasons are unchanged. `error` keeps its current shape for `error` reasons.

## API-TFB-002: Restart

`POST /v1/daemon/restart` body `{ "confirm": true, "force"?: boolean }`:

| Condition | Status | Body |
|---|---:|---|
| No running turns, or `force: true` | `202` | `{ "restart": "scheduled", "running_turns": <n> }` — running turns end `interrupted`, then restart |
| Running turns, no force | `202` | `{ "restart": "draining", "running_turns": <n>, "deadline": <unix ms> }` — restart happens when the count reaches 0 or at the deadline (now + 60 s) |
| Already draining, no force | `202` | same `draining` body with the original deadline |
| Not supported | `409` | `invalid_state` (unchanged) |

`DELETE /v1/daemon/restart`: `200 { "restart": "cancelled" }` when draining; `409 invalid_state` when no restart is pending or it already started.

`GET /v1/daemon` adds `"restart": { "state": "idle" | "draining", "deadline": <unix ms> | null }`. `running_turns` is unchanged.

## API-TFB-003: Continue

`POST /v1/sessions/{id}/continue`, empty body.

| Condition | Status | Body |
|---|---:|---|
| Last turn ended abnormally and no turn runs | `202` | same body as `POST /v1/sessions/{id}/messages` |
| A turn runs | `409` | `turn_running` |
| Last turn ended normally | `409` | `nothing_to_continue` |
| Draining | `503` | `daemon_draining` |

## API-TFB-004: Continue message event

`user/message` payload adds `"continue": true` and `"continue_reason": "<reason>"`; `content[0].text` is `Continue the previous task from where it stopped. It ended because: <reason text>.` The web renders it as a `↻ Continued` chip.

## API-TFB-005: Telegram

Final message last line by reason: `⊘ Stopped by you.`, `⚠ lana restarted while working.`, `✗ Stopped by an error: <message>`, `⚠ Stopped after the step limit (<n> steps).` The message has an inline keyboard `[Continue]` with callback data `c:<session id>`. Only owners may press it; others get the existing "owners only" callback answer. Pressing it removes the button and starts the continue turn.

## API-TFB-006: `restart_required`

`PUT/DELETE /v1/mcp/{name}`, agent saves (`PUT /v1/agents/{name}`, create, duplicate, delete), team saves, and agent tab saves that write `[agents.*]` return `restart_required: false` after the runtime applied the change. If applying fails, the config stays written, the response has `restart_required: true`, and the error is logged.

## API-TFB-007: `mcp_manage list`

The state column (fifth column, `-` today) becomes one of: `running (<n> tools)`, `stopped`, `unavailable: <short reason>`, `not loaded`.

## API-TFB-008: Draining

`POST /v1/sessions/{id}/messages`, `POST /v1/sessions` with a first message, and `/continue` return `503 daemon_draining` ("lana is restarting. Try again in a moment.") while draining. Telegram replies with the same text.

## Approval

- **Approved by**: Ridho
- **Approved on**: 2026-10-30
