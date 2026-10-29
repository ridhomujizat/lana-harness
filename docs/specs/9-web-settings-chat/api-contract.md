# Contract: Web view: settings and chat sessions

- **Issue**: `—`
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 1
- **Requirements status**: Draft
- **Contract revision**: 1
- **Applies to**: `lana daemon` (`[web]` config, listener), browser login, `/v1` session, settings, secret, agent-file, and skill endpoints, SSE stream
- **Status**: Draft

Do not define new behavior here. Requirements own behavior; this file owns the exact form of the interface. Paths
below use the `/v1` prefix of ADR 0014 and are provisional until `Q-003` is answered. Every item marked
`(pending Q-0NN)` depends on an open question in [requirements.md](requirements.md).

Shared conventions (exit codes, stderr `error: ` prefix, `LANA_HOME`, secrets, session IDs, JSON) follow the
[spec #1 contract](../1-cli-tui/api-contract.md#shared-conventions). The daemon contract is in
[spec #6](../6-daemon-telegram/api-contract.md#api-dmn-001-lana-daemon).

## Shared conventions

| Topic | Decision or authority |
|---|---|
| CLI exit codes | `lana daemon`: `0`, `1` (port in use, lock held), `2` (invalid `[web]` config, non-loopback address). See `API-WEB-001` |
| stdout and stderr | Daemon start line and audit lines go to stderr. The HTTP API writes nothing to stdout |
| Config format | `[web]` table in `$LANA_HOME/config.toml`. Owner token file `$LANA_HOME/web.json` (`Q-002`) |
| Authentication | Browser cookie `lana_session` (HTTP-only, `SameSite=Strict`, 12 hours idle). Login with the owner token (`API-WEB-002`). No bearer tokens in this spec (`Q-003`) |
| CSRF | `X-Lana-CSRF` header on `POST`, `PUT`, `PATCH`, `DELETE`. Value issued at login and returned by `GET /v1/auth/session` |
| Host and Origin | `Host` must equal `[web] addr`. `Origin`, when present on a state-changing request, must equal `http://<addr>` |
| Versioning | Path prefix `/v1`. Additive fields are allowed; removing or renaming a field needs a new version |
| Serialization | UTF-8 JSON, `snake_case` fields, times as Unix milliseconds, session IDs as 26-character ULID (spec #1), model as `<provider>:<model>` |
| Error envelope | `{"error": {"code": "<stable code>", "message": "<English text>", "details": [...]}}`. `details` is optional |
| Stable error codes | `unauthenticated`, `forbidden_host`, `forbidden_origin`, `csrf_invalid`, `invalid_request`, `not_found`, `unknown_agent`, `validation_failed`, `over_limit`, `stale_version`, `config_missing`, `session_in_use`, `invalid_state`, `approval_closed` |
| Secret values | Never returned. Shown as `"***"` in any structured read where a value could appear |
| Caching | Every `/v1` response sends `Cache-Control: no-store` |
| Language | English text in messages (spec #6 `Q-006` precedent) |

## Operation index

| Operation ID | Type | Command/method | Path/flag/topic | Requirements | Acceptance criteria |
|---|---|---|---|---|---|
| `API-WEB-001` | Config and CLI | `[web]` table, `lana daemon` start | `[web] enabled, addr` | `REQ-WEB-001`, `REQ-WEB-003`, `REQ-WEB-004` | `AC-WEB-001` through `AC-WEB-005` |
| `API-WEB-002` | HTTP | `POST` / `DELETE` / `GET` | `/v1/auth/login`, `/v1/auth/logout`, `/v1/auth/session` | `REQ-WEB-005`, `REQ-WEB-006`, `REQ-WEB-007` | `AC-WEB-006`, `AC-WEB-007`, `AC-WEB-008` |
| `API-WEB-003` | HTTP | `GET` | `/v1/sessions` | `REQ-WEB-008` | `AC-WEB-009` |
| `API-WEB-004` | HTTP | `GET` | `/v1/sessions/{id}`, `/v1/sessions/{id}/events` | `REQ-WEB-009`, `REQ-WEB-012` | `AC-WEB-010`, `AC-WEB-014` |
| `API-WEB-005` | HTTP | `POST` | `/v1/sessions` | `REQ-WEB-010` | `AC-WEB-011` |
| `API-WEB-006` | HTTP | `PATCH` | `/v1/sessions/{id}` | `REQ-WEB-017`, `REQ-WEB-018` | `AC-WEB-019`, `AC-WEB-020` |
| `API-WEB-007` | HTTP | `POST` | `/v1/sessions/{id}/messages` | `REQ-WEB-011`, `REQ-WEB-018`, `REQ-WEB-019` | `AC-WEB-012`, `AC-WEB-013`, `AC-WEB-020`, `AC-WEB-021`, `AC-WEB-032` |
| `API-WEB-008` | HTTP (SSE) | `GET` | `/v1/sessions/{id}/stream` | `REQ-WEB-011`, `REQ-WEB-012`, `REQ-WEB-029` | `AC-WEB-012`, `AC-WEB-014`, `AC-WEB-031` |
| `API-WEB-009` | HTTP | `POST` | `/v1/sessions/{id}/cancel` | `REQ-WEB-016` | `AC-WEB-018` |
| `API-WEB-010` | HTTP | `POST` | `/v1/sessions/{id}/compact`, `/v1/sessions/{id}/fork` | `REQ-WEB-017` | `AC-WEB-019` |
| `API-WEB-011` | HTTP | `POST` | `/v1/approvals/{request_id}` | `REQ-WEB-014`, `REQ-WEB-015` | `AC-WEB-016`, `AC-WEB-017` |
| `API-WEB-012` | HTTP | `GET` | `/v1/agents` | `REQ-WEB-010` | `AC-WEB-011` |
| `API-WEB-013` | HTTP | `GET` | `/v1/config` | `REQ-WEB-020` | `AC-WEB-022` |
| `API-WEB-014` | HTTP | `PUT` | `/v1/config` | `REQ-WEB-021`, `REQ-WEB-022`, `REQ-WEB-023`, `REQ-WEB-027` | `AC-WEB-023` through `AC-WEB-025`, `AC-WEB-029` |
| `API-WEB-015` | HTTP | `GET`, `PUT`, `DELETE` | `/v1/secrets`, `/v1/secrets/{NAME}` | `REQ-WEB-024`, `REQ-WEB-027` | `AC-WEB-026`, `AC-WEB-029` |
| `API-WEB-016` | HTTP | `GET`, `PUT` | `/v1/agents/{name}/files/{file}` | `REQ-WEB-025` | `AC-WEB-027` |
| `API-WEB-017` | HTTP | `GET`, `PUT` | `/v1/skills`, `/v1/skills/{scope}/{name}` | `REQ-WEB-026` | `AC-WEB-028` |

Static assets are served at `GET /` and `GET /assets/<path>` without login. The login page is the only
dashboard page without a cookie. All other pages call `/v1` and need the cookie.

## API-WEB-001: `[web]` config and daemon start

- **Goal**: enable and place the web view.
- **Requirements**: `REQ-WEB-001`, `REQ-WEB-003`, `REQ-WEB-004`
- **Acceptance criteria**: `AC-WEB-001` through `AC-WEB-005`
- **Stability**: New
- **Authorization**: Local file owner only (config and `LANA_HOME` permissions).

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| config `[web]` | `enabled` | bool | No | Default `true` (`Q-005`). `false` opens no port |
| config `[web]` | `addr` | string | No | Default `127.0.0.1:18300`. `host:port`. `host` must be `127.0.0.1`, `::1`, or `localhost` (`REQ-WEB-003`). Port 1 to 65535 |

Unknown keys under `[web]` are a config error as for `[channels.telegram]` (spec #6 `API-TG-001`).

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Web view starts | `0` when the daemon stops normally | stderr: `web: listening on http://<addr>` |
| Web view disabled | — | No `web:` line |
| `[web]` absent | — | Defaults apply (`enabled = true`), pending `Q-005` |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| `addr` not loopback | `2` | — | No | stderr `error: web: addr must be a loopback address` |
| `addr` port in use | `1` | — | No | stderr `error: web: address already in use` |
| Unknown or wrong-typed key under `[web]` | `2` | — | No | stderr `error: config: web.<key>: <reason>` |
| Token file unreadable or invalid | `2` | — | No | stderr names `web.json` and the fix (`Q-002`) |
| No Telegram channel and web view enabled | `0` / runs | — | — | Runs with the web view only (`Q-005`, pending) |

### Side effects and guarantees

- **State transition**: daemon starts a listener. On stop the listener closes (`API-WEB-008` shutdown event).
- **Idempotency**: Not applicable.
- **Concurrency**: One daemon per `LANA_HOME` (spec #6 `API-DMN-001` lock).
- **Data exposure**: The token is never printed except at creation (`REQ-WEB-006`).

### Compatibility

- **Breaking change**: No. Configs without `[web]` start with the defaults (`Q-005` decides whether the defaults change).
- **Deprecation**: Not applicable.

## API-WEB-002: Login, logout, and session check

- **Goal**: let the owner's browser act as the owner, and protect state-changing calls.
- **Requirements**: `REQ-WEB-005`, `REQ-WEB-006`, `REQ-WEB-007`
- **Acceptance criteria**: `AC-WEB-006`, `AC-WEB-007`, `AC-WEB-008`
- **Stability**: New (`Q-002`, `Q-003` pending)
- **Authorization**: The owner token. The login endpoint is the only API call without a cookie.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Body of `POST /v1/auth/login` | `token` | string | Yes | Compared to the SHA-256 hash in `web.json` in constant time |
| Header `X-Lana-CSRF` | — | string | Yes for `logout` | Must match the CSRF value of the cookie |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| `POST /v1/auth/login`, token correct | `204` | `Set-Cookie: lana_session=<opaque>; HttpOnly; SameSite=Strict; Path=/` |
| `GET /v1/auth/session`, cookie valid | `200` | `{"csrf": "<token>", "expires_at": <ms>}` |
| `POST /v1/auth/logout` | `204` | Cookie cleared on the server and in the response |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Token wrong | `401` | `unauthenticated` | Yes | No cookie set |
| Cookie missing or expired | `401` | `unauthenticated` | Yes | Browser goes to the login page |
| `Host` not the configured address | `403` | `forbidden_host` | No | Nothing processed |
| `Origin` present and different on a state-changing call | `403` | `forbidden_origin` | No | Nothing changed |
| CSRF header missing or wrong | `403` | `csrf_invalid` | Yes | Nothing changed |

### Side effects and guarantees

- **State transition**: a browser session is created in memory. It is lost when the daemon stops (`Q-002`).
- **Idempotency**: login is not idempotent; each success creates a new browser session.
- **Concurrency**: Browser sessions may be used from several tabs.
- **Data exposure**: The token and the hash are never in a response (`REQ-WEB-006`).

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-003: List sessions

- **Goal**: show every chat session of every channel.
- **Requirements**: `REQ-WEB-008`
- **Acceptance criteria**: `AC-WEB-009`
- **Stability**: New
- **Authorization**: Owner cookie.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Query | `channel` | string | No | One of `cli`, `telegram`, `library`, `web`. Empty means all |
| Query | `limit` | integer | No | 1 to 50, default 50 |
| Query | `before` | integer | No | `updated_at` in ms; returns sessions updated before it |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Listing | `200` | `{"sessions": [...], "next_before": <ms or null>}` |

Each session object: `id`, `channel`, `address`, `agent`, `title`, `status`, `turn_count`, `created_at`,
`updated_at`, `in_use` (`"none"`, `"self"`, or `"other"`), `holder_pid` (present when `in_use` is `"other"`).

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| `channel` unknown | `400` | `invalid_request` | No | Nothing listed |
| `limit` out of range | `400` | `invalid_request` | No | Nothing listed |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Yes, read-only.
- **Concurrency**: Lists the state at call time; no lock taken.
- **Data exposure**: No message text in the list.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-004: Session detail and events

- **Goal**: show one session and its history derived from the log.
- **Requirements**: `REQ-WEB-009`, `REQ-WEB-012`
- **Acceptance criteria**: `AC-WEB-010`, `AC-WEB-014`
- **Stability**: New
- **Authorization**: Owner cookie. Reading never takes a lease.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `id` | string | Yes | Full ULID or unique prefix of at least 6 characters (spec #1) |
| Query (`events`) | `from_seq` | integer | No | Return events with `seq > from_seq`; default `0` |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| `GET /v1/sessions/{id}` | `200` | Session object (as `API-WEB-003`) plus `model`, `policy`, `workspace`, `parent_session_id`, `turn_running` |
| `GET /v1/sessions/{id}/events` | `200` | `{"events": [{"seq", "kind", "turn_id", "created_at", "payload"}], "last_seq": <n>}` |

Event `kind` values are the log kinds of ADR 0009 (`user/message`, `assistant/message`, `tool/call`,
`tool/result`, `approval/asked`, `approval/decided`, `approval/policy`, `compaction/summary`, and so on).
`payload` is the logged payload as stored. This contract adds no redaction beyond what the runtime already writes (see `design.md`, Security).

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Unknown or ambiguous ID | `404` | `not_found` | No | Nothing shown |
| `from_seq` negative or not an integer | `400` | `invalid_request` | No | Nothing shown |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Yes.
- **Concurrency**: Events are read in `seq` order; a concurrent turn may add events after `last_seq`.
- **Data exposure**: Tool output and user text appear as logged. The token and secrets never appear in the log by design.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-005: Create a web session

- **Goal**: start a chat with an agent from the browser.
- **Requirements**: `REQ-WEB-010`
- **Acceptance criteria**: `AC-WEB-011`
- **Stability**: New
- **Authorization**: Owner cookie and CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Body | `agent` | string | Yes | Name of a configured agent (`API-WEB-012`) |
| Body | `model` | string | No | `<provider>:<model>`; empty keeps the agent's model |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Created | `201` | Session object (as `API-WEB-004`) with `channel = "web"`, `address = "owner"` (`Q-006`) |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Unknown agent | `400` | `unknown_agent` | No | Nothing created |
| Bad model format | `400` | `invalid_request` | No | Nothing created |
| Store error | `500` | — | Yes | Nothing created; logged at `error` |

### Side effects and guarantees

- **State transition**: New session with a `session/created` event. If `model` is given, a `model/selection` event is written.
- **Idempotency**: No; each call creates a session.
- **Concurrency**: Not applicable.
- **Data exposure**: Not applicable.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-006: Change session settings

- **Goal**: change agent, model, or policy of a session.
- **Requirements**: `REQ-WEB-017`, `REQ-WEB-018`
- **Acceptance criteria**: `AC-WEB-019`, `AC-WEB-020`
- **Stability**: New
- **Authorization**: Owner cookie and CSRF header. Refused when the session is in use elsewhere.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `id` | string | Yes | Session ID |
| Body | `agent` | string | No | Known agent name |
| Body | `model` | string | No | `<provider>:<model>` |
| Body | `policy` | string | No | `ask` or `never`; `trust` only with `confirm_trust: true` (`Q-012`, pending) |
| Body | `confirm_trust` | bool | No | Required `true` for `trust` (pending `Q-012`) |

At least one of `agent`, `model`, `policy` is required.

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Changed | `200` | Session object (as `API-WEB-004`) |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Session in use elsewhere | `409` | `session_in_use` | Yes | `details: [{"pid": <n>}]`; nothing written |
| Turn running in this daemon | `409` | `invalid_state` | Yes | Nothing written |
| Unknown agent, bad model, bad policy | `400` | `unknown_agent` or `invalid_request` | No | Nothing written |
| `trust` without `confirm_trust` | `400` | `invalid_request` | No | Nothing written |

### Side effects and guarantees

- **State transition**: `model/selection` event for model changes; `approval/policy` event for policy changes. Agent change is recorded as the runtime `SetAgent` does.
- **Idempotency**: Setting the same value again writes no new event.
- **Concurrency**: The lease check happens before the write; a process that takes the lease after the check is a known race and is handled by the store writer.
- **Data exposure**: Not applicable.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-007: Send a message

- **Goal**: continue a session with a new user message.
- **Requirements**: `REQ-WEB-011`, `REQ-WEB-018`, `REQ-WEB-019`
- **Acceptance criteria**: `AC-WEB-012`, `AC-WEB-013`, `AC-WEB-020`, `AC-WEB-021`
- **Stability**: New
- **Authorization**: Owner cookie and CSRF header. Refused when the session is in use elsewhere.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `id` | string | Yes | Session ID |
| Body | `text` | string | Yes | Non-empty after trimming; maximum length per `CQ-002` |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Idle session, turn started | `202` | `{"turn": "started", "turn_id": "<id>"}` |
| Turn running, message queued in inbox | `202` | `{"turn": "queued"}`; the message is claimed at the next step |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Session in use elsewhere | `409` | `session_in_use` | Yes | `details: [{"pid": <n>}]`; message not stored |
| Empty or too long text | `400` | `invalid_request` | No | Nothing stored |
| Unknown session | `404` | `not_found` | No | Nothing stored |

### Side effects and guarantees

- **State transition**: `user/message` event at the next claim; a turn starts when the session is idle.
- **Idempotency**: Not idempotent. The UI does not retry automatically.
- **Concurrency**: The turn runs on the daemon root context, not on the request context, so a closed browser does not cancel it (`REQ-WEB-011`). The lease set is shared with Telegram (`REQ-WEB-019`).
- **Data exposure**: The message is stored as typed.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-008: Session stream (SSE)

- **Goal**: deliver live frames for turns in this daemon and polled events for turns in other processes.
- **Requirements**: `REQ-WEB-011`, `REQ-WEB-012`, `REQ-WEB-029`
- **Acceptance criteria**: `AC-WEB-012`, `AC-WEB-014`, `AC-WEB-031`
- **Stability**: New
- **Authorization**: Owner cookie. Read-only; no lease taken.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `id` | string | Yes | Session ID |
| Query | `from_seq` | integer | No | Replay log events with `seq > from_seq` first |
| Header | `Last-Event-ID` | integer | No | Same meaning as `from_seq`; takes precedence when both are given |

### Success

Response is `text/event-stream`. Events:

| Event name | Data | When |
|---|---|---|
| `event` | One log event (as `API-WEB-004`) | Each durable event, from the log, in `seq` order |
| `frame` | `{"kind": "text_delta" or "tool_call" or "tool_result", "turn_id", "text", "call_id", "name", "input", "status", "output"}` | Live frames of a turn running in this daemon only |
| `turn` | `{"turn_id", "stop_reason"}` | Turn ended (`end_turn`, `max_steps`, `cancelled`, `error`) |
| `approval` | `{"request_id", "tool", "argument", "risk", "expires_at"}` | An approval opened (`API-WEB-011`) |
| `status` | `{"holder": "self" or "other", "pid": <n or null>, "turn_running": <bool>}` | On connect and on change |
| `shutdown` | `{}` | The daemon is stopping (`REQ-WEB-029`) |

Heartbeat: a comment line `: ping` every 15 seconds. The `id:` field of each `event` line is the `seq`.
For a session held by another process, `frame` is not sent; `event` lines are polled from the log about once per second.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Unknown session | `404` | `not_found` | No | Stream not opened |
| Daemon stopping | — | — | — | `shutdown` event, then the stream closes |
| Client disconnect | — | — | — | Stream closed; turn continues |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Reconnecting with `Last-Event-ID` yields each `event` once.
- **Concurrency**: Frames are best-effort; the log is the source of truth. A slow client may lose `frame` events, but never `event` lines.
- **Data exposure**: Frames contain tool arguments and output as the runtime emits them.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-009: Cancel a turn

- **Goal**: stop the running turn of a session.
- **Requirements**: `REQ-WEB-016`
- **Acceptance criteria**: `AC-WEB-018`
- **Stability**: New
- **Authorization**: Owner cookie and CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `id` | string | Yes | Session ID |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Turn running and cancelled | `202` | `{"turn": "cancelling"}`; the `turn` SSE event follows with `cancelled` |
| Idle session | `200` | `{"turn": "idle"}`; no change |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Session in use elsewhere | `409` | `session_in_use` | Yes | Nothing cancelled |
| Unknown session | `404` | `not_found` | No | Nothing cancelled |

### Side effects and guarantees

- **State transition**: `turn/end` with `cancelled`; a pending approval is recorded as `cancelled`.
- **Idempotency**: Yes; a second cancel on an idle session is a no-op.
- **Concurrency**: Uses the runtime cancel, which is safe while a turn runs.
- **Data exposure**: Not applicable.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-010: Compact and fork

- **Goal**: summarize a session's history, or branch a session at its last completed turn.
- **Requirements**: `REQ-WEB-017`
- **Acceptance criteria**: `AC-WEB-019`
- **Stability**: New
- **Authorization**: Owner cookie and CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `id` | string | Yes | Session ID |
| Path suffix | `compact` or `fork` | — | Yes | Operation |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| `compact` done | `200` | `{"tokens_before": <n>, "tokens_after": <n>}` |
| `fork` done | `201` | New session object with `parent_session_id` and `inherited_seq` |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Turn running | `409` | `invalid_state` | Yes | Nothing done (spec #6 `/compact` rule) |
| Session in use elsewhere (`compact` only) | `409` | `session_in_use` | Yes | Nothing done |
| Fork without a completed turn | `409` | `invalid_state` | No | Nothing done (ADR 0009) |
| Unknown session | `404` | `not_found` | No | Nothing done |

### Side effects and guarantees

- **State transition**: `compaction/summary` event (`compact`), or a new session (`fork`). Parent events are not copied (ADR 0009).
- **Idempotency**: `compact` is not idempotent; each call writes a new summary. `fork` is not idempotent.
- **Concurrency**: Blocked during a turn (see `REQ-WEB-017`).
- **Data exposure**: The summary is written to the log as ordinary text.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-011: Answer an approval

- **Goal**: let the owner allow or reject a tool call from the browser.
- **Requirements**: `REQ-WEB-014`, `REQ-WEB-015`
- **Acceptance criteria**: `AC-WEB-016`, `AC-WEB-017`
- **Stability**: New; the outcome names follow ADR 0004.
- **Authorization**: Owner cookie and CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `request_id` | string | Yes | Open approval request ID from the `approval` SSE event |
| Body | `outcome` | string | Yes | `allowed_once`, `allowed_session`, or `rejected` |
| Body | `reason` | string | No | Optional text the model receives with a rejection; maximum length per `CQ-002` |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Accepted | `200` | `{"outcome": "<outcome>"}`; `approval/decided` event is written |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Already answered, timed out, or cancelled | `409` | `approval_closed` | No | Dialog closes with `Approval closed` |
| Unknown request ID | `404` | `not_found` | No | Nothing changed |
| Invalid outcome | `400` | `invalid_request` | No | Nothing changed |

### Side effects and guarantees

- **State transition**: `approval/decided` (with the outcome) and then the tool runs or is rejected (ADR 0004).
- **Idempotency**: First answer wins; later answers receive `409`.
- **Concurrency**: The check and the answer happen under one lock per request.
- **Timeout**: 10 minutes from `approval` event. Outcome for timeout: `unavailable` (`Q-014`, pending).
- **Data exposure**: The argument shown is the one from the `tool/call` event.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-012: List agents

- **Goal**: fill the agent picker.
- **Requirements**: `REQ-WEB-010`
- **Acceptance criteria**: `AC-WEB-011`
- **Stability**: New (ADR 0014 lists `GET /v1/agents` with the same purpose)
- **Authorization**: Owner cookie.

### Input

None.

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Listing | `200` | `{"agents": [{"name", "model", "policy", "skills", "mcp", "mcp_status": [{"name", "state", "error"}]}]}` |

Agents are sorted by name. `model` is `<provider>:<model>`. `mcp_status` comes from `Runtime.MCPStatus`.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| No config loaded | `500` | — | No | Error page; logged at `error` |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Yes.
- **Concurrency**: Reads the config loaded at daemon start (`Q-008`).
- **Data exposure**: Names and models only; no provider keys.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-013: Read config

- **Goal**: show the effective settings and the raw `config.toml` text for editing.
- **Requirements**: `REQ-WEB-020`
- **Acceptance criteria**: `AC-WEB-022`
- **Stability**: New
- **Authorization**: Owner cookie.

### Input

None.

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Read | `200` | `{"version": "<sha256 of file on disk>", "loaded_sha256": "<sha256 at daemon start>", "restart_required": <bool>, "validation": {"ok": <bool>, "errors": [...]}, "raw": "<toml with masked values>", "effective": {...structured read model...}}` |
| `config.toml` missing | `200` | `version` is `""`, `validation.ok` is `false`, `raw` is `""`; the page says `run lana init` |

`effective` contains `providers`, `agents`, `routes`, `channels` (Telegram with `token_env` and `owner_ids`, no token),
`mcp`, and `modules`. Values under `mcp.*.env` and `mcp.*.headers` are `"***"` in `effective` and `raw`.
`restart_required` is `true` when `version` differs from `loaded_sha256`.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| File unreadable | `500` | — | Yes | Error shown; logged at `error` |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Yes.
- **Concurrency**: Reads the file at call time.
- **Data exposure**: Masking as above. Env names such as `api_key_env` are shown; values never are.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-014: Save config

- **Goal**: validate and write the edited `config.toml`.
- **Requirements**: `REQ-WEB-021`, `REQ-WEB-022`, `REQ-WEB-023`, `REQ-WEB-027`
- **Acceptance criteria**: `AC-WEB-023`, `AC-WEB-024`, `AC-WEB-025`, `AC-WEB-029`
- **Stability**: New; the raw editor is `Q-009` (pending)
- **Authorization**: Owner cookie and CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Body | `raw` | string | Yes | Full candidate TOML. Masked values (`"***"`) under `mcp.*.env` and `mcp.*.headers` keep the stored value |
| Body | `expected_version` | string | Yes | `version` the owner loaded (`API-WEB-013`) |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Valid and written | `200` | `{"version": "<new sha256>", "restart_required": true}` |

Write rule: write `config.toml.tmp` in the same directory with the same mode as the current file (new files: `0644`,
as `lana init` writes today), then rename over `config.toml`.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Candidate invalid | `422` | `validation_failed` | Yes | `details: [{"file", "key", "message"}]`; nothing written |
| `expected_version` differs from disk | `409` | `stale_version` | Yes | Nothing written; the owner reloads |
| `config.toml` missing | `409` | `config_missing` | No | Nothing written |
| Missing or empty `raw` | `400` | `invalid_request` | No | Nothing written |
| Write fails | `500` | — | Yes | Temporary file removed; original unchanged |

### Side effects and guarantees

- **State transition**: `config.toml` replaced. The running daemon keeps the loaded config (`Q-008`).
- **Idempotency**: Saving the same text again returns the same version and writes nothing new.
- **Concurrency**: One write at a time per daemon; `expected_version` guards against other writers.
- **Data exposure**: The audit line names changed keys, not values (`REQ-WEB-027`).

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-015: Secrets

- **Goal**: show whether named secrets are set, and set or unset them without revealing values.
- **Requirements**: `REQ-WEB-024`, `REQ-WEB-027`
- **Acceptance criteria**: `AC-WEB-026`, `AC-WEB-029`
- **Stability**: New; the browser write rule is `Q-010` (pending)
- **Authorization**: Owner cookie and CSRF header for `PUT` and `DELETE`.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `NAME` | string | Yes (`PUT`, `DELETE`) | Env name: `[A-Za-z_][A-Za-z0-9_]*` |
| Body (`PUT`) | `value` | string | Yes | Non-empty; no newline; at most 4,096 bytes. Quoted in the file when it contains spaces, `#`, or quotes |
| Body (`PUT`) | `expected_version` | string | Yes | SHA-256 of `secrets.env` the owner loaded (`GET` `version`) |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| `GET /v1/secrets` | `200` | `{"version": "<sha256>", "secrets": [{"name", "state": "set" or "unset", "source": "env", "file", "none", "overridden_by_env": <bool>}], "restart_required": <bool>}` |
| `PUT` written | `200` | `{"name", "state": "set", "restart_required": true}` |
| `DELETE` removed | `200` | `{"name", "state": "unset", "restart_required": true}` |

`restart_required` is `true` after any change to `secrets.env` since the daemon started. Values are never returned.
File mode of `secrets.env` is `0600` after every write.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Invalid env name | `422` | `validation_failed` | No | Nothing written |
| Value with newline or too long | `422` | `validation_failed` | No | Nothing written |
| `expected_version` differs | `409` | `stale_version` | Yes | Nothing written |
| `DELETE` of an unknown name | `404` | `not_found` | No | Nothing written |

### Side effects and guarantees

- **State transition**: `secrets.env` line replaced, added, or removed. Other lines and comments are kept.
- **Idempotency**: `PUT` with the same value writes the same bytes; `DELETE` of an unset name returns `404`.
- **Concurrency**: Same write lock and version check as `API-WEB-014`.
- **Data exposure**: Never returns a value. `overridden_by_env` tells the owner that the process environment wins (spec #1 secrets rule).

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-016: Agent files

- **Goal**: read and replace `AGENT.md`, `USER.md`, and `MEMORY.md` of an agent.
- **Requirements**: `REQ-WEB-025`
- **Acceptance criteria**: `AC-WEB-027`
- **Stability**: New; deletion is out of scope (`Q-011`, pending)
- **Authorization**: Owner cookie; `PUT` needs the CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `name` | string | Yes | Agent name (`agenthome.ValidName`) |
| Path | `file` | string | Yes | `agent`, `user`, or `memory` (maps to `AGENT.md`, `USER.md`, `MEMORY.md`) |
| Body (`PUT`) | `content` | string | Yes | Full file text |
| Body (`PUT`) | `expected_version` | string | Yes | SHA-256 of the file the owner loaded |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| `GET` | `200` | `{"content", "version", "characters", "limit": <2000 or 4000 or null>}` |
| `PUT` written | `200` | `{"version", "characters"}`; used from the next turn |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Unknown agent | `404` | `not_found` | No | Nothing written |
| `user` or `memory` over limit | `422` | `over_limit` | No | `details` names the limit; nothing written |
| `expected_version` differs | `409` | `stale_version` | Yes | Nothing written |
| Write fails | `500` | — | Yes | Original file unchanged |

### Side effects and guarantees

- **State transition**: File replaced atomically (temporary file and rename), mode `0600`.
- **Idempotency**: Same content, same version; no new write.
- **Concurrency**: Same lock and version rule as `API-WEB-014`. A running turn reads the file at its start (spec #5 `REQ-PROMPT-002`).
- **Data exposure**: The file text is shown to the owner only.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-017: Skills

- **Goal**: list, read, create, and edit skills.
- **Requirements**: `REQ-WEB-026`
- **Acceptance criteria**: `AC-WEB-028`
- **Stability**: New (`Q-011` pending for scope)
- **Authorization**: Owner cookie; `PUT` needs the CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Query (`GET /v1/skills`) | `scope` | string | No | `global` or `agent:<name>`; empty lists all |
| Path (`GET`, `PUT`) | `scope` | string | Yes | `global` or `agent:<name>` |
| Path (`GET`, `PUT`) | `name` | string | Yes | Skill name (`agenthome.ValidSkillName`) |
| Body (`PUT`) | `content` | string | Yes | Full `SKILL.md` text, with frontmatter |
| Body (`PUT`) | `expected_version` | string | No | Required when editing an existing skill; empty when creating |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| `GET /v1/skills` | `200` | `{"skills": [{"name", "scope", "description", "valid", "error"}], "invalid": [...]}` |
| `GET /v1/skills/{scope}/{name}` | `200` | `{"content", "version", "path"}` |
| `PUT` created | `201` | `{"version", "path"}` |
| `PUT` edited | `200` | `{"version", "path"}` |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Frontmatter invalid or `description` missing | `422` | `validation_failed` | No | `details` names the field; nothing written |
| Create when the name exists | `409` | `stale_version` | No | Nothing written |
| Edit with stale `expected_version` | `409` | `stale_version` | Yes | Nothing written |
| Unknown agent in `scope` | `404` | `not_found` | No | Nothing written |

### Side effects and guarantees

- **State transition**: `SKILL.md` created or replaced in `$LANA_HOME/skills/<name>/` (global) or `$LANA_HOME/agents/<agent>/skills/<name>/` (agent).
- **Idempotency**: `PUT` with the same content is a no-op.
- **Concurrency**: Same lock and version rule as `API-WEB-014`.
- **Data exposure**: Skill text only.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## Contract traceability

| Operation | Requirements | Acceptance criteria | Design component | Verification |
|---|---|---|---|---|
| `API-WEB-001` | `REQ-WEB-001`, `REQ-WEB-003`, `REQ-WEB-004` | `AC-WEB-001` through `AC-WEB-005` | `web` server, `cmd/lana` daemon wiring, config `[web]` | `cmd/lana` daemon test, runtime smoke |
| `API-WEB-002` | `REQ-WEB-005` through `REQ-WEB-007` | `AC-WEB-006` through `AC-WEB-008` | `web/auth.go` | handler tests with `httptest` |
| `API-WEB-003` | `REQ-WEB-008` | `AC-WEB-009` | `web/sessions.go`, `lana` list method | handler test with two runtimes and SQLite |
| `API-WEB-004` | `REQ-WEB-009`, `REQ-WEB-012` | `AC-WEB-010`, `AC-WEB-014` | `web/sessions.go`, `web/sse.go` | handler test; integration test with `lana chat` process |
| `API-WEB-005` | `REQ-WEB-010` | `AC-WEB-011` | `web/sessions.go`, `lana` channel session | handler test |
| `API-WEB-006` | `REQ-WEB-017`, `REQ-WEB-018` | `AC-WEB-019`, `AC-WEB-020` | `web/sessions.go`, `leaseSet` | handler test with held lease |
| `API-WEB-007` | `REQ-WEB-011`, `REQ-WEB-018`, `REQ-WEB-019` | `AC-WEB-012`, `AC-WEB-013`, `AC-WEB-020`, `AC-WEB-021`, `AC-WEB-032` | `web/turns.go`, daemon lease set | integration test with `MockLlm` |
| `API-WEB-008` | `REQ-WEB-011`, `REQ-WEB-012`, `REQ-WEB-029` | `AC-WEB-012`, `AC-WEB-014`, `AC-WEB-031` | `web/sse.go` | SSE client test; runtime smoke |
| `API-WEB-009` | `REQ-WEB-016` | `AC-WEB-018` | `web/turns.go` | integration test with approval pending |
| `API-WEB-010` | `REQ-WEB-017` | `AC-WEB-019` | `web/sessions.go` | integration test |
| `API-WEB-011` | `REQ-WEB-014`, `REQ-WEB-015` | `AC-WEB-016`, `AC-WEB-017` | `web/approvals.go`, daemon approval router | integration test with fake clock |
| `API-WEB-012` | `REQ-WEB-010` | `AC-WEB-011` | `web/agents.go` | handler test |
| `API-WEB-013` | `REQ-WEB-020` | `AC-WEB-022` | `web/config.go`, `config` candidate check | handler test with MCP secrets |
| `API-WEB-014` | `REQ-WEB-021` through `REQ-WEB-023`, `REQ-WEB-027` | `AC-WEB-023` through `AC-WEB-025`, `AC-WEB-029` | `web/config.go`, `config.Check`, atomic write helper | table tests; integration test with two tabs |
| `API-WEB-015` | `REQ-WEB-024`, `REQ-WEB-027` | `AC-WEB-026`, `AC-WEB-029` | `web/secrets.go`, `secrets.env` editor | table tests on the file editor |
| `API-WEB-016` | `REQ-WEB-025` | `AC-WEB-027` | `web/agenthome.go`, `agenthome` | handler test; turn test |
| `API-WEB-017` | `REQ-WEB-026` | `AC-WEB-028` | `web/skills.go`, `agenthome.ParseSkill` | handler test |

## Blocking Contract Questions

| ID | Question | Controls | Requested reference | Agent recommendation | Owner | Status | Resolution |
|---|---|---|---|---|---|---|---|
| `CQ-001` | Final path names, the `/v1` prefix, and the cookie name for the browser API | All `API-WEB-*` paths | ADR 0014 and ADR 0008 | Keep the names here. They are provisional until `Q-003` and the new auth ADR are accepted | Ridho | Open | — |
| `CQ-002` | Maximum length of a chat message (`API-WEB-007`) and of an approval rejection reason (`API-WEB-011`) | `API-WEB-007`, `API-WEB-011` | Limits of the runtime or the store, if any | Message: 32,000 characters. Rejection reason: 500 characters. Both are checked in the handler with `400 invalid_request`. No runtime limit was found in the code read for this draft | Ridho | Open | — |

The contract cannot be approved while `CQ-001` and the questions in `requirements.md` are open.

## Approval

- [ ] Requirements are Approved and their revision is recorded above.
- [ ] Contract applicability has been determined.
- [ ] Every operation has a stable ID and is linked to requirements and acceptance criteria.
- [ ] Input, success, failure, authorization, and compatibility are explicit.
- [ ] No behavior exists only in this file.
- [ ] Blocking Contract Questions is `None`.
- **Approved by**: —
- **Approved on**: —
