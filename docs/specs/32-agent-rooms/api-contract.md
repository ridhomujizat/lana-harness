# Contract: Agent rooms

- **Issue**: `—`
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 1
- **Requirements status**: Approved
- **Contract revision**: 1
- **Applies to**: config / HTTP / events (SSE)
- **Status**: Approved

## Contract rules

Requirements own behavior. This file owns the exact shape of the new and changed interface.

## Shared conventions

| Topic | Decision or authority |
|---|---|
| CLI exit codes | Not applicable |
| Config format | `config.toml`, `[agents.<name>]` table ([#2](../2-foundation/api-contract.md)) |
| Authentication | Existing web session cookie + CSRF header, enforced by `Server.Handle` ([#9](../9-web-settings-chat/api-contract.md)) |
| Errors | Existing `{"error":{"code","message"}}` body; field errors use the existing agent issue shape |
| Serialization | JSON; times are Unix milliseconds; ids are strings |
| Versioning | All paths live under `/v1`; additions only |

## Operation index

| Operation ID | Type | Method | Path/key/topic | Requirements | Acceptance criteria |
|---|---|---|---|---|---|
| `API-ROOM-001` | HTTP | `GET` | `/v1/rooms` | `REQ-ROOM-001`, `REQ-ROOM-005`, `REQ-ROOM-006` | `AC-ROOM-001`, `AC-ROOM-007`, `AC-ROOM-008`, `AC-ROOM-009`, `AC-ROOM-010` |
| `API-ROOM-002` | HTTP | `GET` | `/v1/rooms/{name}/segments` | `REQ-ROOM-002` | `AC-ROOM-002`, `AC-ROOM-003` |
| `API-ROOM-003` | HTTP | `POST` | `/v1/rooms/{name}/messages` | `REQ-ROOM-003`, `REQ-ROOM-008` | `AC-ROOM-004`, `AC-ROOM-005`, `AC-ROOM-013` |
| `API-ROOM-004` | HTTP | `POST` | `/v1/rooms/{name}/read` | `REQ-ROOM-005` | `AC-ROOM-007` |
| `API-ROOM-005` | SSE | `GET` | `/v1/rooms/stream` | `REQ-ROOM-007` | `AC-ROOM-011` |
| `API-ROOM-006` | Config | — | `[agents.<name>] title`, `color` | `REQ-ROOM-009` | `AC-ROOM-014`, `AC-ROOM-015` |
| `API-ROOM-007` | HTTP | `GET`/`PUT` | `/v1/agents`, `/v1/agents/{name}`, `/v1/agents/{name}/files/job` | `REQ-ROOM-009` | `AC-ROOM-014`, `AC-ROOM-015`, `AC-ROOM-016` |
| `API-ROOM-008` | HTTP | `GET` | `/v1/agents/{name}/overview` | `REQ-ROOM-010`, `REQ-ROOM-011` | `AC-ROOM-017`, `AC-ROOM-018`, `AC-ROOM-019` |

## Shared object: `presence`

| Field | Type | Meaning |
|---|---|---|
| `state` | string | `waiting` \| `working` \| `problem` \| `online` \| `resting` |
| `label` | string | Short text, at most 80 characters, e.g. `Running bash`, `Waiting for your answer`, `provider returned 429`, `Active 12 min ago` |
| `since` | int | Unix ms when this state started, or the last activity time for `online`/`resting`; `0` when unknown |
| `session_id` | string | The session that caused `waiting`/`working`/`problem`; empty otherwise |

## API-ROOM-001: List rooms

- **Goal**: One row per configured agent and team.
- **Stability**: New
- **Authorization**: web owner

### Success — `200`

```json
{
  "rooms": [
    {
      "name": "dana",
      "kind": "agent",
      "title": "Finance",
      "color": "success",
      "members": [],
      "lead": "",
      "presence": {"state": "waiting", "label": "Waiting for your answer", "since": 1793550000000, "session_id": "s_1"},
      "last_message": {"role": "assistant", "agent": "dana", "preview": "Ada transaksi Rp 245.000…", "at": 1793550000000, "session_id": "s_1", "channel": "telegram"},
      "unread": 2,
      "turns_7d": 64,
      "last_active_at": 1793550000000
    }
  ]
}
```

- `kind` is `agent` or `team`. For a team, `members` lists member names, `lead` is the lead, `title`
  is the team description, and `color` is `brand`.
- `last_message` is `null` when the room has no session. `preview` is at most 140 characters, with
  runtime notes hidden. `role` is `user` or `assistant`.
- Sort: `last_active_at` descending; rooms with `last_active_at = 0` last, by name.

## API-ROOM-002: Room segments

- **Goal**: Page the sessions that make up a room.
- **Stability**: New

### Input

| Location | Name | Type | Required | Meaning |
|---|---|---|---:|---|
| Path | `name` | string | Yes | Agent or team name |
| Query | `before` | int | No | Return segments created strictly before this Unix ms |
| Query | `limit` | int | No | 1–20, default 5 |

### Success — `200`

```json
{
  "segments": [
    {"session_id": "s_0", "channel": "telegram", "address": "12345", "created_at": 1793460000000, "updated_at": 1793463000000, "turn_count": 3, "status": "idle"}
  ],
  "has_more": true
}
```

- Segments are top-level sessions with `agent = name`, in **ascending** `created_at` order within
  the page; the page itself is the newest `limit` segments before `before`.
- The client reads each segment's events with the existing `GET /v1/sessions/{id}/events`.

### Failure

| Condition | Status | Code |
|---|---:|---|
| Unknown room | 404 | `room_not_found` |
| `limit` or `before` not a valid integer | 400 | `invalid_query` |

## API-ROOM-003: Send to a room

### Input — JSON body

| Name | Type | Required | Meaning |
|---|---|---:|---|
| `text` | string | Yes | Message text; must not be blank |
| `attachments` | array | No | Same as `POST /v1/sessions/{id}/messages` |
| `new_topic` | bool | No | `true` creates a new web session before sending |

### Success — `200`

```json
{"session_id": "s_2", "created": true, "turn": "started", "turn_id": "t_9"}
```

- `created` is `true` when a web session was created by this call. `turn` and `turn_id` are the
  same as the existing messages endpoint (`started` or `queued`).
- For a team room, when `text` starts with `@<member>` (a member of the team, case-insensitive),
  the turn is started with a runtime note that asks the lead to hand the request to that member.
  The stored user message is the text as typed.

### Failure

| Condition | Status | Code |
|---|---:|---|
| Unknown room | 404 | `room_not_found` |
| Blank text | 400 | `empty_message` |
| Errors of the existing messages endpoint | same | same |

## API-ROOM-004: Mark a room read

- Body: none. Sets the room's read time to now. Response `200` `{"read_at": 1793550000000}`.
- Unknown room: `404 room_not_found`.

## API-ROOM-005: Room stream

- `GET /v1/rooms/stream`, `text/event-stream`, with `: ping` every 15 s.
- On connect the server sends one `rooms` event with the full API-ROOM-001 body, then sends a
  `room` event for each room whose row changed:

```text
event: room
data: {"name":"dana","kind":"agent", ... same object as one API-ROOM-001 row ... }
```

- Changes are detected at least every 1.5 s. A client that reconnects gets a fresh `rooms` event.
- `event: shutdown` is sent when the daemon stops, as on the session stream.

## API-ROOM-006: Config keys

| Key | Type | Default | Validation |
|---|---|---|---|
| `[agents.<name>] title` | string | `""` | at most 60 characters, single line |
| `[agents.<name>] color` | string | `""` (derived) | one of `brand`, `success`, `warning`, `error`, `blue`, `gray` |

- A derived color is `["brand","success","warning","error","blue","gray"][fnv32(name) % 6]`.
- `lana config check` reports invalid values with the key path `agents.<name>.title` / `.color`.

## API-ROOM-007: Agents API changes

- `GET /v1/agents` rows add `title` (string) and `color` (resolved, never empty). Team rows have
  `title` = description and `color` = `brand`.
- `PUT /v1/agents/{name}` accepts `title` and `color`; an empty string removes the key. Invalid
  values return the existing field-issue response with keys `agents.<name>.title` / `.color`.
- `GET`/`PUT /v1/agents/{name}/files/job` reads and writes `$LANA_HOME/agents/<name>/JOB.md`, with
  limit 4000 and the same `{content, version, characters, limit}` / `expected_version` shape as
  the `agent`, `user`, and `memory` files. A missing file reads as empty content.

## API-ROOM-008: Agent overview

### Success — `200`

```json
{
  "name": "dana",
  "kind": "agent",
  "title": "Finance",
  "color": "success",
  "persona_line": "Saya mencatat setiap rupiah …",
  "created_at": 1792700000000,
  "presence": {"state": "working", "label": "Running gmail.search", "since": 1793550000000, "session_id": "s_1"},
  "now": {"session_id": "s_1", "turn_id": "t_1", "started_at": 1793549990000,
          "steps": [{"name": "gmail.search", "status": "ok", "at": 1793549992000}, {"name": "read_file", "status": "running", "at": 1793549995000}]},
  "activity": [
    {"kind": "answered", "title": "Answered in Web", "detail": "Rp 1.420.000 sampai …", "at": 1793549000000, "session_id": "s_1"}
  ],
  "week": {"days": [{"day": "2026-10-27", "turns": 5}], "turns": 64, "questions": 5, "notes": 12, "tokens": 412000},
  "memory": {"entries": ["Gaji masuk tiap tanggal 25"], "total": 312},
  "job": "• Catat transaksi …",
  "looks_after": {"channels": [{"channel": "telegram", "match": "chat 12345"}], "teams": ["career-plan"]}
}
```

- `now` is `null` when no turn runs. `steps` are the tool calls of the running turn in order;
  `status` is `running`, `ok`, or `error`.
- `activity` holds at most 20 items from the last 7 days, newest first. `kind` is one of
  `answered`, `failed`, `asked`, `approval`, `learned`, `remembered`, `summarized`.
- `week.days` has exactly 7 entries, oldest first, in the daemon's local time zone. `notes` counts
  `memory/captured` and `knowledge/compiled` events.
- `memory.entries` holds at most 6 entries of MEMORY.md; `total` counts all entries.
- `persona_line` is the first non-empty, non-heading line of AGENT.md, at most 140 characters.
- Unknown agent or team: `404 not_found`.

## Contract traceability

| Operation | Requirements | Acceptance criteria | Design component | Verification |
|---|---|---|---|---|
| `API-ROOM-001` | `REQ-ROOM-001/005/006` | `AC-ROOM-001/007/008/009/010` | Room service, presence | `web/rooms_test.go` |
| `API-ROOM-002` | `REQ-ROOM-002` | `AC-ROOM-002/003` | Room service | `web/rooms_test.go`, `views/chats.test.tsx` |
| `API-ROOM-003` | `REQ-ROOM-003/008` | `AC-ROOM-004/005/013` | Room service | `web/rooms_test.go` |
| `API-ROOM-004` | `REQ-ROOM-005` | `AC-ROOM-007` | Read marks | `store` + `web/rooms_test.go` |
| `API-ROOM-005` | `REQ-ROOM-007` | `AC-ROOM-011` | Room stream | `web/rooms_test.go` |
| `API-ROOM-006` | `REQ-ROOM-009` | `AC-ROOM-014/015` | Config | `config/agent_keys_test.go` |
| `API-ROOM-007` | `REQ-ROOM-009` | `AC-ROOM-014/015/016` | Agents API | `web/agents_test.go`, `web/agentfiles_test.go` |
| `API-ROOM-008` | `REQ-ROOM-010/011` | `AC-ROOM-017/018/019` | Overview | `web/overview_test.go` |

## Blocking Contract Questions

None.

## Approval

- [x] Requirements are Approved and their revision is recorded above.
- [x] Contract applicability has been determined.
- [x] Every operation has a stable ID and is linked to requirements and acceptance criteria.
- [x] Input, success, failure, authorization, and compatibility are explicit.
- [x] No behavior exists only in this file.
- [x] Blocking Contract Questions is `None`.
- **Approved by**: agent on behalf of the owner (gate lifted 2026-11-02)
- **Approved on**: 2026-11-02
