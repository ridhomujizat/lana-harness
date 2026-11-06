# Contract: Agent teams

- **Issue**: `—`
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 1
- **Requirements status**: Approved
- **Contract revision**: 1
- **Applies to**: Config / tools / events / HTTP / SSE
- **Status**: Approved

Do not define new behavior here. Requirements own behavior; this file owns the exact form of the interface. This contract
extends the web contract of [spec #9](../9-web-settings-chat/api-contract.md) (revision 2 for the config writer, the agent
endpoints, and the session filters). Where this file names an operation of spec #9 (for example `API-WEB-003`), the
operation's shared rules (authentication, `expected_version`, error envelope, paging) apply unchanged.

## Contract rules

- Document only new or changed operations. Link shared conventions, do not copy them.
- Every operation has a stable ID `API-TEAM-NNN`.
- Every operation is linked to at least one `REQ-TEAM-*` and `AC-TEAM-*`.
- Write the input, successful output, failure codes, authorization, and compatibility.

## Shared conventions

| Topic | Decision or authority |
|---|---|
| CLI exit codes | Not applicable. Team names are accepted by `lana chat` and `lana run` through the agent name argument and `/agent`; exit codes are unchanged |
| stdout and stderr output | Not applicable |
| Config format | `config.toml` in `$LANA_HOME`, table `[teams.<name>]`. Writes follow spec #9 `API-WEB-014` (section rewrite, comments kept, `config.Check` before write) |
| Authentication | Owner cookie and `X-Lana-CSRF` for every write (spec #9 `API-WEB-002`). Tool calls run under the session's owner |
| Versioning | `version = 1` stays. A team table is additive: a config without it loads as before |
| Serialization | JSON with snake_case keys, as spec #9. Session IDs are ULIDs. Durations in ms in `duration_ms`. Tool outputs are JSON text in `tool/result.output` |

Names:

- Team and agent names use one rule: `^[a-z][a-z0-9_-]{0,31}$` (`agenthome.ValidName`).
- A team name and an agent name must not be the same.

## Operation index

| Operation ID | Type | Command/method | Path/flag/topic | Requirements | Acceptance criteria |
|---|---|---|---|---|---|
| `API-TEAM-001` | Config | `[teams.<name>]` table | `config.toml` | `REQ-TEAM-001`, `REQ-TEAM-002`, `REQ-TEAM-005` | `AC-TEAM-001` to `AC-TEAM-005` |
| `API-TEAM-002` | Tool | `task` | tool name | `REQ-TEAM-005`, `REQ-TEAM-006`, `REQ-TEAM-008`, `REQ-TEAM-009`, `REQ-TEAM-012`, `REQ-TEAM-013` | `AC-TEAM-010` to `AC-TEAM-014`, `AC-TEAM-018` to `AC-TEAM-021`, `AC-TEAM-029`, `AC-TEAM-031` |
| `API-TEAM-003` | Tool | `task_continue` | tool name | `REQ-TEAM-007`, `REQ-TEAM-008` | `AC-TEAM-015` to `AC-TEAM-017` |
| `API-TEAM-004` | Event | payload fields | session log | `REQ-TEAM-006`, `REQ-TEAM-010`, `REQ-TEAM-013` | `AC-TEAM-012`, `AC-TEAM-025`, `AC-TEAM-031` |
| `API-TEAM-005` | HTTP | `GET` | `/v1/sessions`, `/v1/sessions/{id}` | `REQ-TEAM-014`, `REQ-TEAM-016` | `AC-TEAM-032`, `AC-TEAM-036`, `AC-TEAM-037` |
| `API-TEAM-006` | HTTP | `GET` | `/v1/agents` | `REQ-TEAM-017` | `AC-TEAM-038` |
| `API-TEAM-007` | HTTP | `POST` | `/v1/teams` | `REQ-TEAM-018` | `AC-TEAM-039`, `AC-TEAM-040`, `AC-TEAM-041` |
| `API-TEAM-008` | HTTP | `GET` | `/v1/teams/{name}` | `REQ-TEAM-018` | `AC-TEAM-039` |
| `API-TEAM-009` | HTTP | `PUT` | `/v1/teams/{name}` | `REQ-TEAM-018` | `AC-TEAM-039`, `AC-TEAM-040`, `AC-TEAM-041` |
| `API-TEAM-010` | HTTP | `DELETE` | `/v1/teams/{name}` | `REQ-TEAM-018` | `AC-TEAM-042` |
| `API-TEAM-011` | HTTP (SSE) | `GET` | `/v1/sessions/{id}/stream` | `REQ-TEAM-015` | `AC-TEAM-033` to `AC-TEAM-035` |

## API-TEAM-001: `[teams.<name>]` config table

- **Goal**: declare a team: its lead agent, its members, and an optional description.
- **Requirements**: `REQ-TEAM-001`, `REQ-TEAM-002`, `REQ-TEAM-005`
- **Acceptance criteria**: `AC-TEAM-001` to `AC-TEAM-005`
- **Stability**: New
- **Authorization**: Owner. Editing the file needs local access or the owner's web session (`API-TEAM-007` to `API-TEAM-010`).

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Table | `[teams.<name>]` | table | No | `<name>` matches `^[a-z][a-z0-9_-]{0,31}$`. Shares the namespace with `[agents.<name>]` |
| Key | `lead` | string | Yes | Name of a configured agent (not a team) |
| Key | `members` | array of string | Yes | 1 to 8 agent names (not teams). No duplicate. The lead is not listed |
| Key | `description` | string | No | At most 120 characters. Shown in the roster and the agents list |

Example:

```toml
[teams.research-squad]
lead = "lead"
members = ["researcher", "writer"]
description = "Research a topic and write a short digest"
```

### Validation errors

The key is the path of the config value, as `config.Check` reports it.

| Key | Condition | Message |
|---|---|---|
| `teams` | not a table | `must be table, got <type>` |
| `teams.<name>` | name does not match the rule | `name must match [a-z][a-z0-9_-]{0,31}` |
| `teams.<name>` | a configured agent has the same name | `name is already used by an agent` |
| `teams.<name>` | value is not a table | `must be table, got <type>` |
| `teams.<name>.<key>` | key is not `lead`, `members`, or `description` | `unknown key (did you mean: <key>?)` (or `unknown key` without a suggestion) |
| `teams.<name>.lead` | missing | `is required` |
| `teams.<name>.lead` | not a string | `must be string, got <type>` |
| `teams.<name>.lead` | names no agent | `agent '<x>' is not registered (available: <agents>)` |
| `teams.<name>.lead` | names a team | `is a team; a team lead must be an agent` |
| `teams.<name>.members` | missing | `is required` |
| `teams.<name>.members` | not an array of strings | `must be array string, got <type>` |
| `teams.<name>.members` | empty | `must list at least one agent` |
| `teams.<name>.members` | more than 8 items | `must list at most 8 agents` |
| `teams.<name>.members.<i>` | names no agent | `agent '<x>' is not registered (available: <agents>)` |
| `teams.<name>.members.<i>` | names a team | `is a team; teams cannot contain teams` |
| `teams.<name>.members.<i>` | listed twice | `agent '<x>' is listed twice` |
| `teams.<name>.members.<i>` | equals `lead` | `is the lead; the lead is not listed in members` |
| `teams.<name>.description` | not a string, or over 120 characters | `must be string, got <type>` or `must be at most 120 characters` |
| `agents.<a>.subagents.<i>` | names a team | `is a team; subagents must be agents` |
| `routes.<i>.agent` | names neither an agent nor a team | `agent '<x>' is not registered (available: <agents and teams>)` |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Every table valid | — | The effective config has `Teams[name] = {lead, members, description}` |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Any row above | Non-zero exit of `lana config check` (CLI) or `422 validation_failed` (web) | `validation_failed` | No | Nothing loads or is written. The error names the key and the line when the parser has it |

### Side effects and guarantees

- **State transition**: None until the owner saves. A loaded team exists in the runtime after a daemon restart.
- **Idempotency**: Not applicable (static config).
- **Concurrency**: Not applicable here. Writes use spec #9 `API-WEB-014`.
- **Data exposure**: No secret is in a team table.

### Compatibility

- **Breaking change**: No. A config without `[teams]` loads as before. `teams` is added to the list of known top-level keys.
- **Deprecation**: Not applicable.

## API-TEAM-002: `task` tool

- **Goal**: delegate one task to a member agent (or any agent in the delegation list) in a child session, and get its answer back.
- **Requirements**: `REQ-TEAM-005`, `REQ-TEAM-006`, `REQ-TEAM-008`, `REQ-TEAM-009`, `REQ-TEAM-012`, `REQ-TEAM-013`
- **Acceptance criteria**: `AC-TEAM-010` to `AC-TEAM-014`, `AC-TEAM-018` to `AC-TEAM-021`, `AC-TEAM-029`, `AC-TEAM-031`
- **Stability**: New. Replaces nothing; ADR 0011 defined the name
- **Authorization**: Risk `Exec` (ARCHITECTURE §9). Under `ask` each call is an approval; a rule `task(<agent>)` in `rules.allow` or a session grant skips it. Under `never` the call is refused.

Tool spec sent to the model: the name is `task`. The input schema is static. The allowed agent names are listed in the roster section of the system prompt, not in the schema.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Tool input | `agent` | string | Yes | Must be in the delegation list of the calling agent (`REQ-TEAM-004`) |
| Tool input | `prompt` | string | Yes | Not blank. 1 to 8,000 characters. The child's only user message |
| Tool input | `title` | string | No | At most 80 characters. Used as the child session title. Default: the agent name |

Input schema (static):

```json
{"type":"object","required":["agent","prompt"],"properties":{
  "agent":{"type":"string","description":"Name of an agent from the roster"},
  "prompt":{"type":"string","description":"Complete instructions. The agent sees nothing else from this conversation"},
  "title":{"type":"string","description":"Short label for the task"}}}
```

### Success

The tool status is `success`. The output is a JSON object text. The child's outcome is in `status`.

```json
{"session_id":"01J9Z…","agent":"researcher","status":"end_turn","answer":"Three findings: …",
 "steps":3,"tool_calls":2,"duration_ms":12345}
```

| Field | Type | Meaning |
|---|---|---|
| `session_id` | string | The child session. Use it with `task_continue` |
| `agent` | string | The child's agent |
| `status` | string | `end_turn` (the child answered), `max_steps` (stopped at its step limit), or `error` (the child's turn failed) |
| `answer` | string | Text of the child's last assistant message; empty when none |
| `error` | string | Present only when `status = "error"`: the child's error message |
| `steps` | number | Model steps in the child's turn |
| `tool_calls` | number | Tool calls made by the child in this turn |
| `duration_ms` | number | Wall time of the child's turn |

### Failure

A call that could not produce a child answer has tool status `error` and plain text output.

| Condition | Tool status | Output text | Retryable | Observable behavior |
|---|---|---|---:|---|
| Input does not match the schema | `error` | `invalid input: <detail>` | Yes | No child |
| `agent` not in the delegation list | `error` | `agent '<x>' is not a subagent of this agent (allowed: <list>)` | Yes | No child |
| Delegation list empty | `error` | `no subagents are configured for this agent` | No | No child |
| Session is at depth 2 | `error` | `maximum subagent depth reached` | No | No child |
| Concurrency limit reached | `error` | `<n> subagents are already running` (n = 3) | Yes | No child; running children unaffected |
| Parent cancelled, or this child cancelled | `error` | `cancelled` | No | Child `turn/end` has `reason = "cancelled"` |
| Store or runtime error | `error` | `internal error` | Yes | Child may be left with an error `turn/end`; the parent continues |
| Child turn ended with a provider error | `success` | JSON with `status = "error"` and `error` | Yes | The lead decides what to do |

### Side effects and guarantees

- **State transition**: One child session is created (`session/created`), one child turn runs. The parent's `tool/call` and `tool/result` are logged as for any tool.
- **Idempotency**: Not idempotent. Each call creates a new child.
- **Concurrency**: Calls in one step run in parallel, up to the limit (`REQ-TEAM-012`). Approvals run one at a time.
- **Data exposure**: The child's answer enters the parent's log as the tool output. The child's own log keeps the full detail.
- **Usage**: The child turns' token usage is added to the parent's `step/end` usage (`API-TEAM-004`).

### Compatibility

- **Breaking change**: No. The name `task` was reserved by ADR 0011 and not registered before.
- **Deprecation**: Not applicable.

## API-TEAM-003: `task_continue` tool

- **Goal**: send a follow-up to a child that this session created, so the child keeps its earlier context.
- **Requirements**: `REQ-TEAM-007`, `REQ-TEAM-008`
- **Acceptance criteria**: `AC-TEAM-015` to `AC-TEAM-017`
- **Stability**: New
- **Authorization**: Risk `Exec`, same as `task`. The child must be a child of the calling session.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Tool input | `session_id` | string | Yes | A child session of the calling session (exact ID) |
| Tool input | `prompt` | string | Yes | Not blank. 1 to 8,000 characters |

### Success

The tool status is `success` and the output has the shape of `API-TEAM-002`, with `session_id` equal to the input.

### Failure

| Condition | Tool status | Output text | Retryable | Observable behavior |
|---|---|---|---:|---|
| `session_id` is not a child of the calling session | `error` | `session <id> is not a subagent of this session` | No | Nothing changes |
| The child's turn is running | `error` | `subagent is busy` | Yes | Nothing changes |
| Child unknown | `error` | `session <id> is not a subagent of this session` | No | Same message as above (no existence leak) |
| Limits as `API-TEAM-002` | `error` | as `API-TEAM-002` | as above | No child turn |

### Side effects and guarantees

- **State transition**: A new turn is appended to the child's log.
- **Idempotency**: Not idempotent.
- **Concurrency**: Same limit and busy rule as `task`.
- **Data exposure**: As `API-TEAM-002`.

### Compatibility

- **Breaking change**: No. New tool.
- **Deprecation**: Not applicable.

## API-TEAM-004: Event payload fields

- **Goal**: state exactly which existing events carry the team and child facts. No event kind is added.
- **Requirements**: `REQ-TEAM-006`, `REQ-TEAM-010`, `REQ-TEAM-013`
- **Acceptance criteria**: `AC-TEAM-012`, `AC-TEAM-025`, `AC-TEAM-031`
- **Stability**: New use of existing kinds (ADR 0009)
- **Authorization**: Same as the session log.

### Fields by event

| Event (log) | Written when | Fields | Notes |
|---|---|---|---|
| `session/created` (child) | `task` or `task_continue` creates the child | `agent`, `channel` (= root), `address` (= root), `workspace` (= root), `parent_session_id`, `parent_call_id`, `inherited_seq = 0` | Existing kind and fields (ADR 0009) |
| `system/message` | Team session or agent with a non-empty delegation list, when the prompt changes | `text` includes the roster section: one line per allowed agent `- <name>: <description>` (description omitted when empty) and a line that names the `task` tool | Written like any system message. Max 8 members |
| `tool/call` (`task`) | Lead or any agent calls `task` | `input` = `{agent, prompt, title}` | Existing shape |
| `tool/result` (`task`) | The call ends | `output` = the JSON object of `API-TEAM-002`, or the error text of its table | Existing shape |
| `approval/asked` (child) | A child call needs approval | `label` = the child agent name (existing field) | Written to the child's log |
| `approval/decided` (child) | The child approval is answered | As today | Written to the child's log |
| `step/end` (parent) | End of a parent step that ran `task` or `task_continue` | `usage` = the parent model usage plus the child turns' usage of that step | Changes the meaning of `usage` for these steps only (`REQ-TEAM-013`) |
| `turn/end` (child) | Child turn ends | `reason` as for any turn (`end_turn`, `max_steps`, `cancelled`, `error`) | Not shown as a root `turn` event (`API-TEAM-011`) |

### Compatibility

- **Breaking change**: No. Existing readers ignore new fields. The `usage` of a step that ran `task` is larger than the model usage of that step alone; cost displays that want the model usage only must subtract the child total from the child sessions.
- **Deprecation**: Not applicable.

## API-TEAM-005: Sessions list and detail

- **Goal**: show team sessions with their team, and keep child sessions out of the list.
- **Requirements**: `REQ-TEAM-014`, `REQ-TEAM-016`
- **Acceptance criteria**: `AC-TEAM-032`, `AC-TEAM-036`, `AC-TEAM-037`
- **Stability**: Additive change to `API-WEB-003` and `API-WEB-004` (spec #9 revision 2)
- **Authorization**: Owner cookie (`GET`).

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Query (`GET /v1/sessions`) | `agent` | string | No | Name of an agent or a team (spec #9 rev 2 filter). Unknown names return no rows |
| Path (`GET /v1/sessions/{id}`) | `id` | string | Yes | Session ID or unique prefix, as today |

Child sessions are never returned by `GET /v1/sessions` in v1 (no parameter includes them).

### Success

Each row of `GET /v1/sessions` and the detail adds one field:

| Field | Type | Value |
|---|---|---|
| `team` | object or `null` | `null` for a non-team agent. For a team: `{"name": "research-squad", "lead": "lead", "members": ["researcher", "writer"]}` |

`facets.agent` (spec #9 rev 2) lists agents and teams. Each entry has `name`, `kind` (`agent` or `team`), and `count`.

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Unknown session ID | `404` | `not_found` | No | As `API-WEB-004` |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Not applicable.
- **Concurrency**: Not applicable.
- **Data exposure**: Team members' names only. No descriptions beyond `API-TEAM-006`.

### Compatibility

- **Breaking change**: No. New field. Clients that ignore unknown fields are unaffected.
- **Deprecation**: Not applicable.

## API-TEAM-006: Agents list with teams

- **Goal**: list agents and teams in one list, with `kind` to tell them apart.
- **Requirements**: `REQ-TEAM-017`
- **Acceptance criteria**: `AC-TEAM-038`
- **Stability**: Additive change to `API-WEB-012`
- **Authorization**: Owner cookie.

### Success

`GET /v1/agents` returns `agents` sorted by `name`, with teams mixed in. Each row has `kind`.

Agent row (unchanged fields, plus `kind`):

```json
{"name":"coder","kind":"agent","model":"anthropic:claude-sonnet-5-5","policy":"ask",
 "skills":["git-workflow"],"mcp":["github"],"mcp_status":[{"name":"github","state":"connected","error":""}]}
```

Team row:

```json
{"name":"research-squad","kind":"team","lead":"lead","members":["researcher","writer"],
 "description":"Research a topic and write a short digest","model":null,"policy":"ask",
 "skills":[],"mcp":[],"mcp_status":[]}
```

| Field | Team value | Meaning |
|---|---|---|
| `model` | `null` | Each agent uses its own model (`REQ-TEAM-022`) |
| `policy` | the lead's policy | `REQ-TEAM-017`, `Q-010` |
| `skills`, `mcp`, `mcp_status` | empty | Team rows do not own skills or MCP servers |

### Failure

Not applicable beyond `API-WEB-012`.

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Not applicable.
- **Concurrency**: Not applicable.
- **Data exposure**: The `description` of the team. Agent descriptions follow spec #9 rev 2.

### Compatibility

- **Breaking change**: No. New `kind` field on each row, and new rows for teams. Clients that expected only agents must
  filter on `kind = "agent"`.
- **Deprecation**: Not applicable.

## API-TEAM-007: Create a team

- **Goal**: add a `[teams.<name>]` table through the browser.
- **Requirements**: `REQ-TEAM-018`
- **Acceptance criteria**: `AC-TEAM-039`, `AC-TEAM-040`, `AC-TEAM-041`
- **Stability**: New. Same style as the agent endpoints of spec #9 rev 2
- **Authorization**: Owner cookie and CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Body | `name` | string | Yes | Team name rule (`API-TEAM-001`). Must not exist as a team or an agent |
| Body | `lead` | string | Yes | As `API-TEAM-001` |
| Body | `members` | array of string | Yes | As `API-TEAM-001` |
| Body | `description` | string | No | As `API-TEAM-001`. Empty means no key |
| Body | `expected_version` | string | Yes | `version` of `GET /v1/config` (spec #9 `API-WEB-013`) |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Written | `201` | `{"name", "kind": "team", "version": "<sha256 after write>", "restart_required": true}` |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Body is not JSON, or `name` does not match the rule | `400` | `invalid_request` | No | Nothing written |
| Name used by a team or an agent | `409` | `name_taken` | No | Nothing written |
| `expected_version` differs from disk | `409` | `stale_version` | Yes | Nothing written; the owner reloads |
| `config.Check` fails on the candidate | `422` | `validation_failed` | No | `details` lists `file`, `key`, `message` (`API-TEAM-001`). Nothing written |
| Write fails | `500` | `internal_error` | Yes | Original file unchanged |

### Side effects and guarantees

- **State transition**: `[teams.<name>]` table appended at the end of the file, after one blank line. No other bytes change.
- **Idempotency**: Not idempotent. A repeat gets `409 name_taken`.
- **Concurrency**: The write lock and `expected_version` of spec #9 `API-WEB-014`.
- **Data exposure**: Audit line names the key paths (`teams.<name>.lead`, `teams.<name>.members`, `teams.<name>.description`) and no values.

### Compatibility

- **Breaking change**: No; new endpoint.
- **Deprecation**: Not applicable.

## API-TEAM-008: Read a team

- **Goal**: read one team for the editor and for the `expected_version` of the next save.
- **Requirements**: `REQ-TEAM-018`
- **Acceptance criteria**: `AC-TEAM-039`
- **Stability**: New
- **Authorization**: Owner cookie.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `name` | string | Yes | Team name |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Team exists | `200` | `{"name", "lead", "members", "description", "version": "<sha256 of config.toml>", "restart_required": <bool>}` |

`restart_required` is `true` when the team differs from the loaded config (spec #9 definition).

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| No team of that name (agents do not count) | `404` | `not_found` | No | Nothing changes |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Yes.
- **Concurrency**: Not applicable.
- **Data exposure**: No secret. Team tables have none.

### Compatibility

- **Breaking change**: No; new endpoint.
- **Deprecation**: Not applicable.

## API-TEAM-009: Replace a team

- **Goal**: change the lead, members, or description of an existing team.
- **Requirements**: `REQ-TEAM-018`
- **Acceptance criteria**: `AC-TEAM-039`, `AC-TEAM-040`, `AC-TEAM-041`
- **Stability**: New
- **Authorization**: Owner cookie and CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `name` | string | Yes | Existing team |
| Body | `lead` | string | Yes | As `API-TEAM-001` |
| Body | `members` | array of string | Yes | As `API-TEAM-001` |
| Body | `description` | string | No | As `API-TEAM-001`; omitted or empty removes the key |
| Body | `expected_version` | string | Yes | `version` from `API-TEAM-008` |

The name cannot be changed. Rename is a delete and a create.

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Written | `200` | As `API-TEAM-008`, with the new `version` and `restart_required: true` |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| No team of that name | `404` | `not_found` | No | Nothing written |
| Body invalid | `400` | `invalid_request` | No | Nothing written |
| `expected_version` differs | `409` | `stale_version` | Yes | Nothing written |
| `config.Check` fails on the candidate | `422` | `validation_failed` | No | Issue list as `API-TEAM-001`; nothing written |
| Write fails | `500` | `internal_error` | Yes | Original file unchanged |

### Side effects and guarantees

- **State transition**: Only the lines of the `[teams.<name>]` table change. Comments before and after it are kept.
- **Idempotency**: Same body with the new version writes the same bytes. The response reports no change through
  `version`.
- **Concurrency**: As `API-TEAM-007`.
- **Data exposure**: As `API-TEAM-007`.

### Compatibility

- **Breaking change**: No; new endpoint.
- **Deprecation**: Not applicable.

## API-TEAM-010: Delete a team

- **Goal**: remove a `[teams.<name>]` table. Agents stay.
- **Requirements**: `REQ-TEAM-018`
- **Acceptance criteria**: `AC-TEAM-042`
- **Stability**: New
- **Authorization**: Owner cookie and CSRF header.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `name` | string | Yes | Existing team |
| Query | `expected_version` | string | Yes | `version` from `API-TEAM-008`. A query parameter, because `DELETE` has no body in this API |

### Success

| Condition | Exit code or status | Output |
|---|---:|---|
| Removed | `200` | `{"name", "deleted": true, "version": "<sha256 after write>", "restart_required": true}` |

### Failure

| Condition | Exit code or status | Stable code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| No team of that name | `404` | `not_found` | No | Nothing written |
| `expected_version` differs | `409` | `stale_version` | Yes | Nothing written |
| A route names the team | `422` | `validation_failed` | No | `details` names the route key (`routes.<i>.agent`). Nothing written |
| Write fails | `500` | `internal_error` | Yes | Original file unchanged |

### Side effects and guarantees

- **State transition**: The table is removed. Its lines and the blank line before it are removed; other bytes are kept.
- **Idempotency**: A repeat gets `404`.
- **Concurrency**: As `API-TEAM-007`.
- **Data exposure**: Audit line names the key path only.

### Compatibility

- **Breaking change**: No; new endpoint.
- **Deprecation**: Not applicable.

## API-TEAM-011: Stream items for child sessions

- **Goal**: let the dashboard show a team's work on the root session's stream, and keep the root's turn state correct.
- **Requirements**: `REQ-TEAM-015`
- **Acceptance criteria**: `AC-TEAM-033`, `AC-TEAM-034`, `AC-TEAM-035`
- **Stability**: Additive change to `API-WEB-008` (spec #9 rev 2 stream rules)
- **Authorization**: Owner cookie. The stream is read-only.

### Items

| Item | Root item (unchanged) | Child item (new fields) |
|---|---|---|
| `event` | `{"seq", "kind", "turn_id", "created_at", "payload"}`, with SSE `id = seq` | Same keys plus `"child_session_id"` and `"agent"`. The child's `seq` is in `seq`. No SSE `id` line |
| `frame` | `{"kind", "turn_id", "text", "call_id", "name", "input", "status", "output"}` | Same keys plus `"child_session_id"` and `"agent"`. `kind` is `text_delta`, `tool_call`, or `tool_result` |
| `approval` | As spec #9 | Same keys plus `"child_session_id"` and `"agent"` (also set in `label`) |
| `turn` | Root `turn/end` only | Never sent for a child |
| `status` | Root only | Root only |

Rules:

- The root `Last-Event-ID` or `from_seq` applies to the root's `seq` only.
- A reconnect replays the children of the root's current turn from `seq` 1. A client drops an item when the pair (`child_session_id`, `seq`) was seen.
- The stream covers every descendant of the root session (depth 1 and depth 2, `REQ-TEAM-008`). Each item carries the `child_session_id` and `agent` of the session that wrote it.

### Failure

| Condition | Behavior |
|---|---|
| A child log cannot be read | The item is skipped; a `warn` line is logged; the root stream continues (as the root poll does today) |

### Side effects and guarantees

- **State transition**: None.
- **Idempotency**: Not applicable.
- **Concurrency**: The stream reads each child's log by `seq` in the same poll loop as the root.
- **Data exposure**: Child text and tool output are sent as the root's frames are (same owner).

### Compatibility

- **Breaking change**: No. New optional fields. Clients that ignore them see child events as unknown root events, so
  the dashboard must check `child_session_id` before it changes the root state.
- **Deprecation**: Not applicable.

## Contract traceability

| Operation | Requirements | Acceptance criteria | Design component | Verification |
|---|---|---|---|---|
| `API-TEAM-001` | `REQ-TEAM-001`, `REQ-TEAM-002`, `REQ-TEAM-005` | `AC-TEAM-001` to `AC-TEAM-005` | `config/validate.go`, `core/config.go` | `config` table tests |
| `API-TEAM-002` | `REQ-TEAM-005`, `REQ-TEAM-006`, `REQ-TEAM-008`, `REQ-TEAM-009`, `REQ-TEAM-012`, `REQ-TEAM-013` | `AC-TEAM-010` to `AC-TEAM-014`, `AC-TEAM-018` to `AC-TEAM-021`, `AC-TEAM-029`, `AC-TEAM-031` | `tools/task.go`, `agent/subagent.go`, `agent/toolrunner.go` | `agent` tests with `MockLlm` |
| `API-TEAM-003` | `REQ-TEAM-007`, `REQ-TEAM-008` | `AC-TEAM-015` to `AC-TEAM-017` | `agent/subagent.go` | `agent` tests |
| `API-TEAM-004` | `REQ-TEAM-006`, `REQ-TEAM-010`, `REQ-TEAM-013` | `AC-TEAM-012`, `AC-TEAM-025`, `AC-TEAM-031` | `agent/turn.go`, `tools/permission/approval.go` | `agent` and `tools/permission` tests; log reads |
| `API-TEAM-005` | `REQ-TEAM-014`, `REQ-TEAM-016` | `AC-TEAM-032`, `AC-TEAM-036`, `AC-TEAM-037` | `web/sessions.go`, `cmd/lana/web.go` | `web/sessions_test.go` |
| `API-TEAM-006` | `REQ-TEAM-017` | `AC-TEAM-038` | `web/sessions.go` (`handleAgents`) | `web/sessions_test.go` |
| `API-TEAM-007` to `API-TEAM-010` | `REQ-TEAM-018` | `AC-TEAM-039` to `AC-TEAM-042` | `web/teams.go`, `config/check.go` | `web/teams_test.go` |
| `API-TEAM-011` | `REQ-TEAM-015` | `AC-TEAM-033` to `AC-TEAM-035` | `web/sse.go`, `web/assets/views/session.js` | `web/sse_test.go` and dashboard check |

## Blocking Contract Questions

None. Every interface question was answered in `requirements.md` (`Q-001` to `Q-016`).

## Approval

- [x] Requirements are Approved and their revision is recorded above.
- [x] Contract applicability has been determined.
- [x] Every operation has a stable ID and is linked to requirements and acceptance criteria.
- [x] Input, success, failure, authorization, and compatibility are explicit.
- [x] No behavior exists only in this file.
- [x] Blocking Contract Questions is `None`.
- **Approved by**: Ridho, on behalf of the owner (lifted gate, 2026-10-30)
- **Approved on**: 2026-10-30
