# Contract: Web view: settings and chat sessions

- **Issue**: `—`
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 2
- **Requirements status**: Approved
- **Contract revision**: 2 (rev 2 adds `API-WEB-018` to `API-WEB-030`, extends `API-WEB-002`, `API-WEB-003`, `API-WEB-012`, `API-WEB-015`, `API-WEB-016`, `API-WEB-017`; see "Rev 2 changes" at the end)
- **Applies to**: `lana daemon` (`[web]` config, listener), browser login and browser session records, `/v1` session, setup, provider, agent, skill, MCP, channel, secret, agent-file, daemon, and audit endpoints, SSE stream
- **Status**: Approved

Do not define new behavior here. Requirements own behavior; this file owns the exact form of the interface. Paths
below use the `/v1` prefix of ADR 0014 with cookie auth for the browser ([ADR 0016](../../adr/0016-owner-auth-for-browser.md), `Q-003`).

Shared conventions (exit codes, stderr `error: ` prefix, `LANA_HOME`, secrets, session IDs, JSON) follow the
[spec #1 contract](../1-cli-tui/api-contract.md#shared-conventions). The daemon contract is in
[spec #6](../6-daemon-telegram/api-contract.md#api-dmn-001-lana-daemon).

## Shared conventions

| Topic | Decision or authority |
|---|---|
| CLI exit codes | `lana daemon`: `0`, `1` (port in use, lock held), `2` (invalid `[web]` config, non-loopback address). See `API-WEB-001` |
| stdout and stderr | Daemon start line and audit lines go to stderr. The HTTP API writes nothing to stdout |
| Config format | `[web]` table in `$LANA_HOME/config.toml`. Owner token file `$LANA_HOME/web.json` (`Q-002`). Browser session records `$LANA_HOME/web-sessions.json` (mode `0600`, `Q-020`). Secret update times `$LANA_HOME/secrets.meta.json` (mode `0600`, `Q-022`). Audit lines `$LANA_HOME/logs/audit.jsonl` (mode `0600`, `Q-021`) |
| Authentication | Browser cookie `lana_session` (HTTP-only, `SameSite=Strict`, 12 hours idle). Login with the owner token (`API-WEB-002`). Browser session records survive a restart in place (`API-WEB-024`). No bearer tokens in this spec (`Q-003`) |
| CSRF | `X-Lana-CSRF` header on `POST`, `PUT`, `PATCH`, `DELETE`. Value issued at login and returned by `GET /v1/auth/session` |
| Host and Origin | `Host` must equal `[web] addr`. `Origin`, when present on a state-changing request, must equal `http://<addr>` |
| Versioning | Path prefix `/v1`. Additive fields are allowed; removing or renaming a field needs a new version |
| Serialization | UTF-8 JSON, `snake_case` fields, times as Unix milliseconds, session IDs as 26-character ULID (spec #1), model as `<provider>:<model>`. Rev 2 keeps these rules for every new field |
| Error envelope | `{"error": {"code": "<stable code>", "message": "<English text>", "details": [...]}}`. `details` is optional |
| Stable error codes | `unauthenticated`, `forbidden_host`, `forbidden_origin`, `csrf_invalid`, `invalid_request`, `not_found`, `unknown_agent`, `validation_failed`, `over_limit`, `stale_version`, `config_missing`, `session_in_use`, `invalid_state`, `approval_closed`. Rev 2 adds `connection_failed` (422), `setup_not_needed` (409), `restart_failed` (500) |
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
| `API-WEB-017` | HTTP | `GET`, `PUT`, `DELETE` | `/v1/skills`, `/v1/skills/{scope}/{name}` | `REQ-WEB-026`, `REQ-WEB-048`, `REQ-WEB-049` | `AC-WEB-028`, `AC-WEB-051`, `AC-WEB-052` |
| `API-WEB-018` | HTTP | `GET` | `/v1/setup` | `REQ-WEB-032` | `AC-WEB-035` |
| `API-WEB-019` | HTTP | `POST` | `/v1/providers/test` | `REQ-WEB-033` | `AC-WEB-036` |
| `API-WEB-020` | HTTP | `GET`, `PUT`, `DELETE` | `/v1/providers`, `/v1/providers/{name}` | `REQ-WEB-042`, `REQ-WEB-043` | `AC-WEB-045`, `AC-WEB-046` |
| `API-WEB-021` | HTTP | `POST` | `/v1/setup` | `REQ-WEB-034` | `AC-WEB-037` |
| `API-WEB-022` | HTTP | `GET` | `/v1/daemon` | `REQ-WEB-035`, `REQ-WEB-054` | `AC-WEB-038`, `AC-WEB-057` |
| `API-WEB-023` | HTTP | `POST` | `/v1/daemon/restart` | `REQ-WEB-035` | `AC-WEB-038` |
| `API-WEB-024` | HTTP | `GET`, `POST` | `/v1/auth/sessions`, `/v1/auth/sessions/revoke-others` | `REQ-WEB-036`, `REQ-WEB-054` | `AC-WEB-039` |
| `API-WEB-025` | HTTP | `POST`, `PUT` | `/v1/agents`, `/v1/agents/{name}`, `/v1/agents/{name}/duplicate` | `REQ-WEB-045`, `REQ-WEB-046`, `REQ-WEB-047` | `AC-WEB-048`, `AC-WEB-049`, `AC-WEB-050` |
| `API-WEB-026` | HTTP | `GET` | `/v1/tools?agent=<name>` | `REQ-WEB-047` | `AC-WEB-050` |
| `API-WEB-027` | HTTP | `GET`, `PUT`, `DELETE`, `POST` | `/v1/mcp`, `/v1/mcp/{name}`, `/v1/mcp/test`, `/v1/mcp/{name}/restart` | `REQ-WEB-050`, `REQ-WEB-051` | `AC-WEB-053`, `AC-WEB-054` |
| `API-WEB-028` | HTTP | `GET`, `PUT`, `DELETE`, `POST` | `/v1/channels`, `/v1/channels/telegram`, `/v1/channels/telegram/check`, `/v1/channels/telegram/waiting`, `/v1/channels/telegram/routes/{chat_id}` | `REQ-WEB-052`, `REQ-WEB-053` | `AC-WEB-055`, `AC-WEB-056` |
| `API-WEB-029` | HTTP | `PUT` | `/v1/web` | `REQ-WEB-054` | `AC-WEB-057` |
| `API-WEB-030` | HTTP | `GET` | `/v1/audit` | `REQ-WEB-056` | `AC-WEB-059` |

Static assets are served at `GET /` and `GET /assets/<path>` without login. The Unlock step (the login form, rev 2
`REQ-WEB-032`) is the only dashboard view without a cookie. All other views call `/v1` and need the cookie.

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
| `web.json` missing at start | — | `web.json` created (mode `0600`, hash only); stderr once: `web: owner token created at <path>; copy it now, it is shown only once: <token>` |
| Web view disabled | — | No `web:` line |
| `[web]` absent | — | Defaults apply (`enabled = true`) (`Q-005`) |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| `addr` not loopback | `2` | — | No | stderr `error: web: addr must be a loopback address` |
| `addr` port in use | `1` | — | No | stderr `error: web: address already in use` |
| Unknown or wrong-typed key under `[web]` | `2` | — | No | stderr `error: config: web.<key>: <reason>` |
| Token file unreadable or invalid | `2` | — | No | stderr names `web.json` and the fix (`Q-002`) |
| No Telegram channel and web view enabled | `0` / runs | — | — | Runs with the web view only (`Q-005`) |

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
- **Stability**: New (`Q-002`, `Q-003`, ADR 0016)
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

- **State transition**: a browser session record is written to `web-sessions.json` (mode `0600`, hashes only). It survives a restart in place and expires after 12 hours without use (`Q-020`, rev 2).
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
| Query | `q` | string | No | Rev 2. Case-insensitive match on `title` and `preview` (`REQ-WEB-037`). Empty means no text filter |
| Query | `status` | string | No | Rev 2. Comma list of `running`, `waiting_approval`, `waiting_input`, `idle`, `failed` |
| Query | `agent` | string | No | Rev 2. Agent or team name. Empty means all |
| Query | `channel` | string | No | Rev 2. Same as above. The UI offers `web`, `cli`, and `telegram` only (`Q-023`) |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Listing | `200` | `{"sessions": [...], "next_before": <ms or null>}` |

Rev 2 adds `facets` to the response: `{"sessions": [...], "next_before": ..., "facets": {"channel": {"web": n, "cli": n, "telegram": n}, "status": {"running": n, "waiting_approval": n, "waiting_input": n, "idle": n, "failed": n}, "agent": {"<name>": n}}}`. Facet counts use the `q` text only (`Q-023`, `AC-WEB-040`).

Rev 2 `status` values are derived when the list is read: `waiting_approval` when an approval is open, `waiting_input` when a question is open (spec #11), `running` when a turn runs in any process, `failed` when the last turn stopped with `error`, and `idle` otherwise. Every session object also has `preview` (string, at most 120 characters): the pending approval text, the running tool text, the last answer, or the error, in that order.

Each session object: `id`, `channel`, `address`, `agent`, `title`, `status`, `preview`, `turn_count`, `created_at`,
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
| Body | `policy` | string | No | `ask` or `never`; `trust` only with `confirm_trust: true` (`Q-012`) |
| Body | `confirm_trust` | bool | No | Required `true` for `trust` |

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
| `status` | `{"holder": "self", "other", or "none", "pid": <n or null>, "turn_running": <bool>}` | On connect and on change |
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
- **Timeout**: 10 minutes from `approval` event. Outcome for timeout: `unavailable` (`Q-014`).
- **Data exposure**: The argument shown is the one from the `tool/call` event.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-012: List agents

- **Goal**: fill the agent picker. Rev 2 adds the fields used by the agents list (`API-WEB-025`); the list keeps this path.
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

- **Goal**: validate and write the edited `config.toml`. Rev 2: this is the raw editor (`REQ-WEB-044`). Form saves for entities use `API-WEB-019` to `API-WEB-029` and the same rules (`REQ-WEB-043`).
- **Requirements**: `REQ-WEB-021`, `REQ-WEB-022`, `REQ-WEB-023`, `REQ-WEB-027`
- **Acceptance criteria**: `AC-WEB-023`, `AC-WEB-024`, `AC-WEB-025`, `AC-WEB-029`
- **Stability**: New; the raw editor is `Q-009` (answered)
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
- **Stability**: New; the browser write rule is `Q-010` (answered)
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
| `GET /v1/secrets` | `200` | `{"version": "<sha256>", "secrets": [{"name", "state": "set" or "unset", "source": "env", "file", "none", "overridden_by_env": <bool>, "updated_at": <ms or null>, "used_by": ["providers.openai", ...]}], "restart_required": <bool>}` (rev 2 adds `updated_at` from `secrets.meta.json` and `used_by`, `REQ-WEB-055`) |
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

- **Goal**: read and replace `AGENT.md`, `USER.md`, and `MEMORY.md` of an agent. Rev 2: `user` is the one shared file `$LANA_HOME/USER.md` for every agent (spec #5 rev 4). The `{name}` must still be a known agent, and the response is the same for every agent. `USER.md` limit stays 2,000 characters.
- **Requirements**: `REQ-WEB-025`
- **Acceptance criteria**: `AC-WEB-027`
- **Stability**: New; deletion is out of scope (`Q-011`)
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

- **Goal**: list, read, create, edit, and delete skills. Rev 2 adds `DELETE` (`REQ-WEB-048`); agent files still cannot be deleted (`Q-011`, `Q-026`).
- **Requirements**: `REQ-WEB-026`
- **Acceptance criteria**: `AC-WEB-028`
- **Stability**: New (`Q-011`)
- **Authorization**: Owner cookie; `PUT` and `DELETE` need the CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Query (`GET /v1/skills`) | `scope` | string | No | `global` or `agent:<name>`; empty lists all |
| Path (`GET`, `PUT`) | `scope` | string | Yes | `global` or `agent:<name>` |
| Path (`GET`, `PUT`) | `name` | string | Yes | Skill name (`agenthome.ValidSkillName`) |
| Body (`PUT`) | `content` | string | Yes | Full `SKILL.md` text, with frontmatter |
| Body (`PUT`) | `expected_version` | string | No | Required when editing an existing skill; empty when creating |
| Body (`DELETE`, rev 2) | `expected_version` | string | Yes | SHA-256 of the `SKILL.md` the owner loaded |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| `GET /v1/skills` | `200` | `{"skills": [{"name", "scope", "description", "valid", "error", "size", "updated_at"}], "invalid": [...]}` (rev 2 adds `size` in characters and `updated_at`) |
| `DELETE /v1/skills/{scope}/{name}` (rev 2) | `200` | `{"name", "scope", "deleted": true}`; the folder is removed |
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

## API-WEB-018: Setup status

- **Goal**: tell the dashboard whether to show the setup wizard (rev 2).
- **Requirements**: `REQ-WEB-032`
- **Acceptance criteria**: `AC-WEB-035`
- **Stability**: New
- **Authorization**: Owner cookie.

### Input

None.

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Read | `200` | `{"needed": <bool>, "reasons": [...], "provider_count": <n>, "agent_count": <n>}` |

`reasons` holds any of `config_missing`, `no_provider`, `no_agent`. It is `[]` when `needed` is `false`. The file is read at call time.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| `config.toml` unreadable | `500` | — | Yes | Logged at `error` |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Yes.
- **Concurrency**: No lock.
- **Data exposure**: Counts only.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-019: Test a provider connection

- **Goal**: check that a provider answers before the owner saves it (rev 2).
- **Requirements**: `REQ-WEB-033`
- **Acceptance criteria**: `AC-WEB-036`
- **Stability**: New (owner brief decision 5)
- **Authorization**: Owner cookie and CSRF header (the call sends a provider request, so it is treated as a write).

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Body | `kind` | string | Yes | `openai_compat` or `anthropic` |
| Body | `base_url` | string | Yes | `http://` or `https://` URL |
| Body | `model` | string | Yes | Model name, one line |
| Body | `api_key` | string | One of `api_key`, `api_key_env` | Inline key. Used for this call only; never stored, logged, or returned |
| Body | `api_key_env` | string | One of `api_key`, `api_key_env` | Env name (`[A-Za-z_][A-Za-z0-9_]*`). Read from the process environment or `secrets.env` |

Giving both `api_key` and `api_key_env`, or neither, is `400 invalid_request`. The test sends one chat request with one output token and a 15-second timeout.

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Provider answered | `200` | `{"ok": true, "latency_ms": <n>}` |
| Provider refused or failed | `200` | `{"ok": false, "status": <HTTP status, or 0 for no answer>, "message": "<text>"}` |

The HTTP status of the test is not the status of this call. A refused key is `200` with `ok: false` and `status: 401`, for example `401 Unauthorized from api.openai.com`.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Bad body | `400` | `invalid_request` | No | Nothing sent |
| `api_key_env` unset | `200` | — | Yes | `ok: false`, `status: 0`, message names the variable |

### Side effects and guarantees

- **State transition**: None. One outbound request to `base_url`.
- **Idempotency**: Not applicable; each call sends one request.
- **Concurrency**: Independent of locks.
- **Data exposure**: The inline key is never written, logged, or returned (`REQ-WEB-033`). The log line has the host and latency only.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-020: Providers

- **Goal**: list providers and edit one `[providers.<name>]` table with a form (rev 2).
- **Requirements**: `REQ-WEB-042`, `REQ-WEB-043`
- **Acceptance criteria**: `AC-WEB-045`, `AC-WEB-046`
- **Stability**: New
- **Authorization**: Owner cookie; `PUT` and `DELETE` need the CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `name` | string | Yes (`PUT`, `DELETE`) | Provider name, `[a-z0-9-]+` |
| Body (`PUT`) | `kind` | string | Yes | `openai_compat` or `anthropic` |
| Body (`PUT`) | `base_url` | string | Yes | `http://` or `https://` |
| Body (`PUT`) | `api_key_env` | string | No | Env name. Default `<NAME>_API_KEY` in upper case |
| Body (`PUT`) | `api_key` | string | No | New key value. Written to `secrets.env` under `api_key_env`. The test (`API-WEB-019`) runs first; a failed test refuses the save |
| Body (`PUT`) | `headers` | object | No | String values. A value `***` keeps the stored value |
| Body (`PUT`, `DELETE`) | `expected_version` | string | Yes | `version` of `GET /v1/providers` |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| `GET /v1/providers` | `200` | `{"version": "<sha256>", "providers": [{"name", "kind", "base_url", "api_key_env", "key_state": "set", "unset", or "overridden_by_env", "used_by": ["agents.coder", ...]}], "restart_required": <bool>}` |
| `PUT` saved | `200` | `{"name", "version", "restart_required": true}` |
| `DELETE` removed | `200` | `{"name", "version", "restart_required": true}` |

`key_state` shows the state of `api_key_env` only. No value is returned.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Bad name, kind, or URL | `422` | `validation_failed` | No | Nothing written; `details` names the key |
| Test fails when `api_key` is sent | `422` | `connection_failed` | Yes | Nothing written; message as `API-WEB-019` |
| `expected_version` differs | `409` | `stale_version` | Yes | Nothing written |
| `DELETE` while an agent uses it | `422` | `validation_failed` | No | Nothing removed; `details` lists the agents |
| `config.toml` missing | `409` | `config_missing` | No | Nothing written |

### Side effects and guarantees

- **State transition**: The `[providers.<name>]` table is replaced or removed (`REQ-WEB-043`). `secrets.env` changes when `api_key` is sent. One audit line (`API-WEB-030`).
- **Idempotency**: Saving the same values again writes the same bytes.
- **Concurrency**: One write lock; `expected_version` is checked under the lock.
- **Data exposure**: Key values are written to `secrets.env` only.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-021: Save setup

- **Goal**: save the first-run setup and restart the daemon in place (rev 2).
- **Requirements**: `REQ-WEB-034`
- **Acceptance criteria**: `AC-WEB-037`
- **Stability**: New
- **Authorization**: Owner cookie and CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Body | `provider.name` | string | Yes | `openai`, `anthropic`, or `openrouter` |
| Body | `provider.api_key` | string | Yes | Non-empty; the test runs first |
| Body | `provider.base_url` | string | No | Default from the provider card |
| Body | `provider.model` | string | Yes | Default model |
| Body | `agent.name` | string | Yes | `[a-z0-9-]+`, must not exist |
| Body | `agent.template` | string | Yes | `assistant`, `coder`, or `worker` |
| Body | `agent.workspace` | string | No | Default `~/lana/workspace` |
| Body | `agent.permission` | string | Yes | `ask`, `trust`, or `never`. Default `ask` |
| Body | `persona.agent_md` | string | Yes | Text of `AGENT.md`; prefilled from the template |
| Body | `persona.user_md` | string | No | Text of the shared `USER.md`; at most 2,000 characters. Used only when the file is missing |
| Body | `expected_version` | string | Yes | `""` when `config.toml` is missing, else its `version` |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Saved; restart scheduled | `202` | `{"restart": "scheduled", "pid": <n>}` |

The response is sent before the restart. The page then polls `GET /v1/daemon` (`API-WEB-022`) until `started_at` changes.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Setup not needed | `409` | `setup_not_needed` | No | Nothing written |
| Provider test fails | `422` | `connection_failed` | Yes | Nothing written |
| Bad name, template, or permission | `422` | `validation_failed` | No | Nothing written; `details` names the field |
| Agent name exists | `409` | `invalid_state` | No | Nothing written |
| `persona.user_md` over 2,000 characters | `422` | `over_limit` | No | Nothing written |
| `expected_version` differs | `409` | `stale_version` | Yes | Nothing written |
| A write fails after an earlier file was written | `500` | — | Yes | `details` names the step. Files written earlier stay; the owner can run setup again |

### Side effects and guarantees

- **State transition**: `config.toml` created or changed (`version = 1` when created); `secrets.env` line added or replaced (mode `0600`); agent home created (spec #5); `USER.md` written only when missing; one audit line (`setup.save`); then the in-place restart (`API-WEB-023`).
- **Idempotency**: No. A second call after success returns `409 setup_not_needed`.
- **Concurrency**: One write lock.
- **Data exposure**: The key is written to `secrets.env` only.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-022: Daemon status

- **Goal**: show the daemon card, the Daemon section of Settings › General, and the restart confirmation (rev 2).
- **Requirements**: `REQ-WEB-035`, `REQ-WEB-054`
- **Acceptance criteria**: `AC-WEB-038`, `AC-WEB-057`
- **Stability**: New
- **Authorization**: Owner cookie.

### Input

None.

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Read | `200` | `{"pid", "started_at", "uptime_ms", "addr", "home", "version", "go_version", "os", "arch", "restart_supported": <bool>, "running_turns": [{"session_id", "title", "agent"}], "browsers_signed_in": <n>}` |

`started_at` changes after each restart in place. `restart_supported` is `false` on Windows.

### Failure

None beyond `unauthenticated`.

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Yes.
- **Concurrency**: Reads the runtime state at call time.
- **Data exposure**: Home path and version; no secrets.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-023: Restart the daemon

- **Goal**: restart the daemon in place after the owner confirms (rev 2).
- **Requirements**: `REQ-WEB-035`
- **Acceptance criteria**: `AC-WEB-038`
- **Stability**: New (`Q-019`)
- **Authorization**: Owner cookie and CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Body | `confirm` | bool | Yes | Must be `true`. The page sends it after the confirmation dialog |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Restart scheduled | `202` | `{"restart": "scheduled", "running_turns": <n>}` |

After the response, the daemon: ends running turns with `cancelled`; sends `event: shutdown` on every SSE stream (`API-WEB-008`); flushes `web-sessions.json`; stops the listener; closes the store; and runs the same binary with the same arguments in the same process. The PID does not change. The daemon lock stays held (`design.md`, Restart in place). The page reconnects after `GET /v1/daemon` answers with a new `started_at`.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| `confirm` missing or not `true` | `400` | `invalid_request` | No | Nothing restarted |
| Platform does not support it (Windows) | `409` | `invalid_state` | No | Nothing restarted |
| Restart cannot be prepared (for example, the lock descriptor is lost) | `500` | `restart_failed` | Yes | Daemon keeps running; the error is logged at `error` |
| The `exec` itself fails after shutdown started | — | `restart_failed` | — | Process exits with code 1 and stderr names the error; the owner starts `lana daemon` from a terminal |

### Side effects and guarantees

- **State transition**: running turns end `cancelled`; SSE streams end with `shutdown`; the process image is replaced; the PID is kept.
- **Idempotency**: Not idempotent; each confirmed call restarts the daemon.
- **Concurrency**: Turns started during the restart are cancelled as part of the shutdown. The restart waits up to 10 seconds for turns to end, as spec #6 `REQ-DMN-004` requires.
- **Data exposure**: None.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-024: Browser session records

- **Goal**: list the browsers signed in as the owner, and sign out the others (rev 2).
- **Requirements**: `REQ-WEB-036`, `REQ-WEB-054`
- **Acceptance criteria**: `AC-WEB-039`
- **Stability**: New (`Q-020`, ADR 0016 rev 2)
- **Authorization**: Owner cookie; the `POST` needs the CSRF header.

### Input

None for `GET`. For `POST /v1/auth/sessions/revoke-others`, no body.

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| `GET /v1/auth/sessions` | `200` | `{"sessions": [{"id": "<first 12 hex characters of the SHA-256 of the session ID>", "user_agent": "<label, at most 120 characters>", "created_at": <ms>, "last_used_at": <ms>, "current": <bool>}]}` |
| `POST /v1/auth/sessions/revoke-others` | `200` | `{"revoked": <n>}`; the current browser stays signed in |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| No cookie or expired | `401` | `unauthenticated` | Yes | Unlock step shown |
| CSRF header wrong on `POST` | `403` | `csrf_invalid` | Yes | Nothing revoked |

### Side effects and guarantees

- **State transition**: `POST` removes the other records from `web-sessions.json` (atomic write, mode `0600`). Their cookies stop working on the next call.
- **Idempotency**: Yes; a second call revokes `0`.
- **Concurrency**: One write lock for `web-sessions.json`.
- **Data exposure**: No cookie, CSRF value, or full hash is returned. The user-agent label is the owner's own browser label.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-025: Agents (create, edit, duplicate)

- **Goal**: create an agent from a template, edit the `[agents.<name>]` table with a form, and duplicate an agent (rev 2). The agents list itself is `API-WEB-012`, extended.
- **Requirements**: `REQ-WEB-045`, `REQ-WEB-046`, `REQ-WEB-047`
- **Acceptance criteria**: `AC-WEB-048`, `AC-WEB-049`, `AC-WEB-050`
- **Stability**: New
- **Authorization**: Owner cookie and CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `name` | string | Yes (`PUT`, `duplicate`) | Existing agent, or for `duplicate` the source agent |
| Body (`POST`) | `name` | string | Yes | `[a-z0-9-]+`, must not exist |
| Body (`POST`) | `template` | string | Yes | `assistant`, `coder`, or `worker` |
| Body (`POST`) | `workspace` | string | No | Empty leaves `workspace` unset (rule of spec #6 `REQ-TG-005`) |
| Body (`POST`) | `permission` | string | No | `ask` (default), `trust`, or `never` |
| Body (`POST`) | `description` | string | No | At most 120 characters, one line (`REQ-WEB-046`) |
| Body (`PUT`) | `model`, `provider`, `fallback`, `permission`, `max_steps`, `context_window`, `workspace`, `description`, `disabled_tools`, `skills`, `mcp`, `rules` | mixed | No | Any subset. Unknown keys are `422 validation_failed`. `rules` is `{"allow": [...], "deny": [...]}` |
| Body (`PUT`) | `expected_version` | string | Yes | `version` of the config (`API-WEB-013`) |
| Body (`duplicate`) | `name` | string | Yes | New valid name |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| `POST` created | `201` | `{"name", "restart_required": true}` |
| `PUT` saved | `200` | `{"name", "version", "restart_required": true}` |
| `duplicate` created | `201` | `{"name", "restart_required": true}` |

`GET /v1/agents` (`API-WEB-012`, rev 2) adds these fields to each agent: `description`, `default` (bool, `Q-016`), `provider`, `permission`, `workspace`, `disabled_tools`, `tools_count`, `sessions_count`, and `last_active_at` (ms or null).

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Bad name, template, permission, or description | `422` | `validation_failed` | No | Nothing written; `details` names the key (`agents.<name>.description` for length) |
| Name exists (`POST`, `duplicate`) | `409` | `invalid_state` | No | Nothing written |
| Source agent unknown | `404` | `not_found` | No | Nothing written |
| `expected_version` differs | `409` | `stale_version` | Yes | Nothing written |

### Side effects and guarantees

- **State transition**: `[agents.<name>]` table created or replaced (`REQ-WEB-043`). `POST` creates the agent home (spec #5). `duplicate` copies the table and `AGENT.md` only (`Q-025`). Changes take effect after a restart (`Q-008`); `AGENT.md` changes apply at the next turn.
- **Idempotency**: `PUT` with the same values writes nothing new.
- **Concurrency**: One write lock; `expected_version` checked under it.
- **Data exposure**: None.

### Compatibility

- **Breaking change**: No; new API. `API-WEB-012` gains fields only.
- **Deprecation**: Not applicable.

## API-WEB-026: Tool catalog for an agent

- **Goal**: show the Tools tab: the tool groups, the On switches, and "Asks first" (rev 2).
- **Requirements**: `REQ-WEB-047`
- **Acceptance criteria**: `AC-WEB-050`
- **Stability**: New
- **Authorization**: Owner cookie.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Query | `agent` | string | Yes | Agent name |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Read | `200` | `{"agent": "<name>", "groups": [{"id", "label", "tools": [{"name", "risk", "asks_first": <bool>, "enabled": <bool>}]}]}` |

Groups come in this order: `files` (Files), `shell` (Shell), `web` (Web), `memory` (Memory), `skills` (Skills), `chat` (Chat), then one group `mcp:<server>` for each configured MCP server. `enabled` is false when the name is in `disabled_tools` (as `<tool>` or `<mcp_server>/<tool>`). `asks_first` is true when the tool needs approval under the agent's policy `ask`.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Unknown agent | `404` | `not_found` | No | Nothing listed |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Yes.
- **Concurrency**: Reads the loaded config.
- **Data exposure**: Tool names and risk labels only.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-027: MCP servers

- **Goal**: list, add, edit, delete, test, and restart MCP servers (rev 2).
- **Requirements**: `REQ-WEB-050`, `REQ-WEB-051`
- **Acceptance criteria**: `AC-WEB-053`, `AC-WEB-054`
- **Stability**: New
- **Authorization**: Owner cookie; writes need the CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `name` | string | Yes (`PUT`, `DELETE`, `restart`) | Server name in `[mcp.<name>]` |
| Body (`PUT`) | `type` | string | Yes | `command` or `url` |
| Body (`PUT`) | `command`, `args` | string, list | Command type | Command and arguments |
| Body (`PUT`) | `url` | string | URL type | `http://` or `https://` |
| Body (`PUT`) | `env`, `headers` | object | No | String values. `***` keeps the stored value. `env` for command type, `headers` for URL type |
| Body (`PUT`, `DELETE`) | `expected_version` | string | Yes | `version` of the config |
| Body (`POST /v1/mcp/test`) | definition | object | Yes | Same fields as `PUT`, without `expected_version`. Values are inline and never stored |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| `GET /v1/mcp` | `200` | `{"version", "servers": [{"name", "type", "command", "args", "url", "env_keys", "header_keys", "tools_count", "agents": [...], "state": "running", "stopped", or "unavailable", "status": "connected", "unreachable", "idle", or "needs_sign_in", "error", "running_for_ms"}]}` |
| `PUT` saved | `200` | `{"name", "version", "restart_required": true}` |
| `DELETE` removed | `200` | `{"name", "version", "restart_required": true}` |
| `POST /v1/mcp/test` | `200` | `{"ok": true, "tool_count": <n>}` or `{"ok": false, "message": "<text>"}` |
| `POST /v1/mcp/{name}/restart` | `200` | `{"name", "state", "error"}` |

`status` maps the client state: `running` to `connected`, `unavailable` to `unreachable`, `stopped` to `idle`. `needs_sign_in` is reserved and is not produced in v1 (`design.md`, Gaps G-22). `env` and `headers` values are never returned; only their keys. The test runs with a 15-second timeout and stops the client afterwards.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Bad name, type, or URL | `422` | `validation_failed` | No | Nothing written |
| `DELETE` while an agent lists it | `422` | `validation_failed` | No | Nothing removed; `details` lists the agents |
| Restart of a server not configured | `404` | `not_found` | No | Nothing changed |
| `expected_version` differs | `409` | `stale_version` | Yes | Nothing written |

### Side effects and guarantees

- **State transition**: `[mcp.<name>]` table replaced or removed. Restart stops and starts one client from the loaded definition (not the file on disk, `Q-008`). Test starts and stops a temporary client.
- **Idempotency**: Restart is not idempotent; each call restarts the client.
- **Concurrency**: One write lock for saves.
- **Data exposure**: Values of `env` and `headers` are written to the config only as typed (`***` kept). Errors from the test do not echo values.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-028: Channels and Telegram

- **Goal**: show the channel cards, edit the Telegram channel, check the token, list waiting chats, and edit Telegram routes (rev 2).
- **Requirements**: `REQ-WEB-052`, `REQ-WEB-053`
- **Acceptance criteria**: `AC-WEB-055`, `AC-WEB-056`
- **Stability**: New; spec #6 rev 3 holds the Telegram behavior.
- **Authorization**: Owner cookie; writes need the CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Body (`PUT /v1/channels/telegram`) | `enabled` | bool | No | `[channels.telegram] enabled`; default `true` (spec #6 rev 3) |
| Body (`PUT /v1/channels/telegram`) | `token_env` | string | No | Env name, default `TELEGRAM_BOT_TOKEN`. The token is never sent in this API |
| Body (`PUT /v1/channels/telegram`) | `owner_ids` | list of int | No | Telegram user IDs |
| Body (`PUT /v1/channels/telegram`) | `api_base` | string | No | Advanced |
| Body (`PUT /v1/channels/telegram`) | `expected_version` | string | Yes | `version` of the config |
| Path | `chat_id` | int | Yes (routes) | Telegram chat ID |
| Body (`PUT routes`) | `agent`, `name` | string | `agent` yes | Agent to route to; display name of the chat. Stored as `[[routes]]` with `channel = "telegram"` and `match = { chat_id = <int> }` (`channels/telegram/config.go`) |
| Body (`DELETE routes`) | `expected_version` | string | Yes | `version` of the config |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| `GET /v1/channels` | `200` | `{"channels": [...]}`; `telegram` has `status` (`connected`, `token_missing`, `off`, or `not_configured`), `bot_username`, `chats`, `owners`, `polling_since`, `last_message_at`; `web` has `status` `on` and `addr`; `cli` has `status` `built_in`; `discord`, `slack`, and `whatsapp` have `status` `coming_soon` |
| `PUT /v1/channels/telegram` | `200` | `{"version", "restart_required": true}` |
| `POST /v1/channels/telegram/check` | `200` | `{"ok": true, "bot_username": "<name>"}` or `{"ok": false, "message": "Token missing" or "<Bot API message>"}` |
| `GET /v1/channels/telegram/waiting` | `200` | `{"chats": [{"chat_id", "name", "type": "private" or "group", "last_message_at"}]}` (memory only, `Q-027`) |
| `PUT /v1/channels/telegram/routes/{chat_id}` | `200` or `201` | `{"chat_id", "agent", "restart_required": true}` |
| `DELETE /v1/channels/telegram/routes/{chat_id}` | `200` | `{"chat_id", "restart_required": true}` |

`check` calls Bot API `getMe` (spec #6 rev 3) with a 15-second timeout. The token is read from the process environment or `secrets.env`. `bot_username` is the value of the last successful check or the start-up `getMe`.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Bad `token_env` or `owner_ids` | `422` | `validation_failed` | No | Nothing written |
| `DELETE` route not found | `404` | `not_found` | No | Nothing changed |
| `expected_version` differs | `409` | `stale_version` | Yes | Nothing written |
| Waiting list asked while Telegram is off | `200` | — | — | `chats` is `[]` |

### Side effects and guarantees

- **State transition**: `[channels.telegram]` table replaced; `[[routes]]` entries for channel `telegram` added, replaced, or removed as one entry each (`REQ-WEB-043`). `check` makes one outbound `getMe` call. The waiting list is kept in memory by the Telegram channel and is never written to disk.
- **Idempotency**: Saves with the same values write nothing new.
- **Concurrency**: One write lock; `expected_version` checked under it.
- **Data exposure**: The token is never returned. Chat names come from Telegram and are shown as text.

### Compatibility

- **Breaking change**: No; new API. The `enabled` key is new and defaults to `true` (spec #6 rev 3).
- **Deprecation**: Not applicable.

## API-WEB-029: Web address

- **Goal**: change `[web] addr` from the Settings page (rev 2).
- **Requirements**: `REQ-WEB-054`, `REQ-WEB-003`
- **Acceptance criteria**: `AC-WEB-057`
- **Stability**: New (`Q-028`)
- **Authorization**: Owner cookie and CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Body | `addr` | string | Yes | `host:port` with a loopback host (`REQ-WEB-003`) |
| Body | `expected_version` | string | Yes | `version` of the config |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Saved | `200` | `{"addr": "<addr>", "url": "http://<addr>", "version", "restart_required": true}` |

The new address applies after a restart. The current listener keeps the old address until then.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Not a loopback address | `422` | `validation_failed` | No | Nothing written; `details` names `web.addr` |
| Bad port | `422` | `validation_failed` | No | Nothing written |
| `expected_version` differs | `409` | `stale_version` | Yes | Nothing written |

### Side effects and guarantees

- **State transition**: `[web] addr` changed in `config.toml` (`REQ-WEB-043`); one audit line.
- **Idempotency**: Same value writes nothing new.
- **Concurrency**: One write lock.
- **Data exposure**: None.

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## API-WEB-030: Audit log

- **Goal**: list the settings changes from `$LANA_HOME/logs/audit.jsonl`, newest first (rev 2).
- **Requirements**: `REQ-WEB-056`
- **Acceptance criteria**: `AC-WEB-059`
- **Stability**: New (`Q-021`)
- **Authorization**: Owner cookie.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Query | `before` | integer | No | Time in ms; returns entries older than it |
| Query | `limit` | integer | No | 1 to 100, default 50 |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Listing | `200` | `{"entries": [{"time": <ms>, "kind": "<kind>", "target": "<file, secret name, or entity>", "changed_keys": ["<key path>", ...]}], "next_before": <ms or null>}` |

Kinds: `config.save`, `setup.save`, `secret.set`, `secret.remove`, `agent.file.save`, `agent.create`, `agent.duplicate`, `agent.save`, `provider.save`, `provider.delete`, `mcp.save`, `mcp.delete`, `channel.telegram.save`, `route.save`, `route.delete`, `skill.save`, `skill.delete`, `web.save`, `daemon.restart`, `auth.sessions.revoke_others`. A missing file lists no entries.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| `limit` out of range | `400` | `invalid_request` | No | Nothing listed |

### Side effects and guarantees

- **State transition**: None. The file is written by the save paths: one JSON line per save, mode `0600`, appended under the write lock.
- **Idempotency**: Yes.
- **Concurrency**: Reads the file at call time.
- **Data exposure**: Key paths and secret names only; never a value (`REQ-WEB-056`).

### Compatibility

- **Breaking change**: No; new API.
- **Deprecation**: Not applicable.

## Rev 2 changes

| Operation | Rev 2 change |
|---|---|
| `API-WEB-002` | Browser session records persist in `web-sessions.json` (`API-WEB-024`) |
| `API-WEB-003` | Query `q`, `status`, `agent`, `channel` values; fields `status` (new values), `preview`; response `facets` |
| `API-WEB-012` | Agent fields added (see `API-WEB-025`) |
| `API-WEB-013` | Unchanged. Used by the raw editor and by forms (`version`) |
| `API-WEB-014` | Raw editor only; forms use `API-WEB-020`, `API-WEB-025`, `API-WEB-027`, `API-WEB-028`, `API-WEB-029` |
| `API-WEB-015` | Fields `updated_at` and `used_by` |
| `API-WEB-016` | `user` is the shared `USER.md` (spec #5 rev 4) |
| `API-WEB-017` | `DELETE` added; list fields `size` and `updated_at` |
| `API-WEB-011` | The UI buttons map to outcomes: Allow once to `allowed_once`, Allow for this session to `allowed_session`, Deny to `rejected` (`REQ-WEB-039`). No change to the API |


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
| `API-WEB-018` | `REQ-WEB-032` | `AC-WEB-035` | `web/setup.go`, `config` | handler test with missing and present config |
| `API-WEB-019` | `REQ-WEB-033` | `AC-WEB-036` | `web/providers.go` | handler test with fake provider server |
| `API-WEB-020` | `REQ-WEB-042`, `REQ-WEB-043` | `AC-WEB-045`, `AC-WEB-046` | `web/providers.go`, `internal/webfiles` section writer | table tests on bytes |
| `API-WEB-021` | `REQ-WEB-034` | `AC-WEB-037` | `web/setup.go`, `cmd/lana/web.go` | integration test with temporary `LANA_HOME` |
| `API-WEB-022` | `REQ-WEB-035`, `REQ-WEB-054` | `AC-WEB-038`, `AC-WEB-057` | `web/daemon.go` | handler test |
| `API-WEB-023` | `REQ-WEB-035` | `AC-WEB-038` | `web/daemon.go`, `cmd/lana/restart.go` | integration test with child process (same PID) |
| `API-WEB-024` | `REQ-WEB-036` | `AC-WEB-039` | `web/auth.go`, `web/browsersessions.go` | handler test and file scan |
| `API-WEB-025` | `REQ-WEB-045` to `REQ-WEB-047` | `AC-WEB-048` to `AC-WEB-050` | `web/agents.go`, `internal/webfiles` | table tests and turn test |
| `API-WEB-026` | `REQ-WEB-047` | `AC-WEB-050` | `web/tools.go`, `tools/permission` | handler test |
| `API-WEB-027` | `REQ-WEB-050`, `REQ-WEB-051` | `AC-WEB-053`, `AC-WEB-054` | `web/mcp.go`, `core/mcp.go` (restart) | integration test with fake MCP server |
| `API-WEB-028` | `REQ-WEB-052`, `REQ-WEB-053` | `AC-WEB-055`, `AC-WEB-056` | `web/channels.go`, `channels/telegram` | fake Bot API test |
| `API-WEB-029` | `REQ-WEB-054` | `AC-WEB-057` | `web/server.go`, `config` | handler test |
| `API-WEB-030` | `REQ-WEB-056` | `AC-WEB-059` | `web/audit.go`, `internal/webfiles` | handler test and file mode test |

## Blocking Contract Questions

| ID | Question | Controls | Requested reference | Agent recommendation | Owner | Status | Resolution |
|---|---|---|---|---|---|---|---|
| `CQ-001` | Final path names, the `/v1` prefix, and the cookie name for the browser API | All `API-WEB-*` paths | ADR 0014 and ADR 0008 | Keep the names here. They are provisional until `Q-003` and the new auth ADR are accepted | Ridho | Answered | Accepted agent recommendation (owner, 2026-10-30) |
| `CQ-002` | Maximum length of a chat message (`API-WEB-007`) and of an approval rejection reason (`API-WEB-011`) | `API-WEB-007`, `API-WEB-011` | Limits of the runtime or the store, if any | Message: 32,000 characters. Rejection reason: 500 characters. Both are checked in the handler with `400 invalid_request`. No runtime limit was found in the code read for this draft | Ridho | Answered | Accepted agent recommendation (owner, 2026-10-30) |

All contract questions are answered.

## Approval

- [x] Requirements are Approved and their revision is recorded above.
- [x] Contract applicability has been determined.
- [x] Every operation has a stable ID and is linked to requirements and acceptance criteria.
- [x] Input, success, failure, authorization, and compatibility are explicit.
- [x] No behavior exists only in this file.
- [x] Blocking Contract Questions is `None`.
- [x] Rev 2 operations `API-WEB-018` to `API-WEB-030` have input, success, failure, authorization, and compatibility sections.
- **Approved by**: Ridho, lifted gate 2026-10-30 (rev 2)
- **Approved on**: 2026-10-30 (rev 1 and rev 2)
