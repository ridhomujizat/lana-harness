# Contract: Calendar

- **Issue**: `—`
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 1
- **Requirements status**: Approved
- **Contract revision**: 1
- **Applies to**: HTTP / events (SSE) / agent tool / Telegram
- **Status**: Approved

## Shared conventions

| Topic | Decision or authority |
|---|---|
| Authentication | Web session cookie + CSRF header via `Server.Handle` (spec #9). Exceptions: `API-CAL-011` (ICS token in path) and `API-CAL-013` callback (OAuth `state`). |
| Errors | `{"error":{"code","message","details"}}`. Validation: `details = [{"field","message"}]`. |
| Serialization | JSON, snake_case. Instants are Unix milliseconds. Dates are `YYYY-MM-DD`, times of day `HH:MM`, both in the calendar time zone. |
| Versioning | Paths under `/v1`; additions only. |

## Operation index

| Operation ID | Method | Path | Requirements |
|---|---|---|---|
| `API-CAL-001` | `GET` | `/v1/calendar/entries` | REQ-CAL-002, REQ-CAL-005, REQ-CAL-008 |
| `API-CAL-002` | `POST` | `/v1/calendar/items` | REQ-CAL-001, REQ-CAL-003 |
| `API-CAL-003` | `GET` | `/v1/calendar/items/{id}` | REQ-CAL-001, REQ-CAL-006 |
| `API-CAL-004` | `PATCH` | `/v1/calendar/items/{id}` | REQ-CAL-001, REQ-CAL-003 |
| `API-CAL-005` | `DELETE` | `/v1/calendar/items/{id}` | REQ-CAL-001 |
| `API-CAL-006` | `POST` / `DELETE` | `/v1/calendar/overrides` | REQ-CAL-004 |
| `API-CAL-007` | `POST` | `/v1/calendar/items/{id}/complete` | REQ-CAL-005 |
| `API-CAL-008` | `POST` | `/v1/calendar/firings/run`, `/v1/calendar/firings/dismiss` | REQ-CAL-006, REQ-CAL-007 |
| `API-CAL-009` | `GET` / `PUT` | `/v1/calendar/settings` | REQ-CAL-011, REQ-CAL-013 |
| `API-CAL-010` | `POST` | `/v1/calendar/ics/rotate` | REQ-CAL-013 |
| `API-CAL-011` | `GET` | `/cal/{token}.ics` | REQ-CAL-013 |
| `API-CAL-012` | `GET` / `PUT` | `/v1/calendar/google` | REQ-CAL-012 |
| `API-CAL-013` | `PUT` / `POST` / `GET` | `/v1/calendar/google/client`, `/connect`, `/callback` | REQ-CAL-012 |
| `API-CAL-014` | `POST` | `/v1/calendar/google/sync`, `/v1/calendar/google/disconnect` | REQ-CAL-012 |
| `API-CAL-015` | SSE `GET` | `/v1/calendar/stream` | REQ-CAL-014 |
| `API-CAL-016` | agent tool | `calendar` | REQ-CAL-009 |
| `API-CAL-017` | Telegram | daily agenda message, `/agenda` | REQ-CAL-011 |
| `API-CAL-018` | (changed) | workflow run `trigger` value `calendar` | REQ-CAL-004, REQ-CAL-006 |

## Shared objects

### `item`

```json
{
  "id": "ci_8m2k4q",
  "kind": "event",
  "title": "Dentist",
  "notes": "",
  "start": 1793084400000,
  "end": 1793088000000,
  "all_day": false,
  "date": "",
  "end_date": "",
  "time": "",
  "repeat": {"freq": "none", "days": [], "until": ""},
  "assignee": "owner",
  "color": "rose",
  "state": "",
  "action": null,
  "google": {"calendar_id": "primary", "event_id": "abc123", "sync": "synced"},
  "created_by": "owner",
  "updated_by": "agent:Dana",
  "created_at": 1793000000000,
  "updated_at": 1793000500000
}
```

Fields by kind (other time fields are empty or `0`):

| Kind | Required | Optional |
|---|---|---|
| `event` | `start` + `end` (`end` > `start`), or `all_day` + `date` (+ `end_date` ≥ `date`, inclusive) | `repeat` |
| `task` | `date` (due) | `time`, `repeat`; `state` `open` (default) or `done` |
| `reminder` | `start` | `repeat` |
| `note` | `date` | — (no repeat) |
| `habit` | `date` (first day), `repeat.freq` `daily` or `weekly` | `time` |

- `kind`: `event`, `task`, `reminder`, `note`, `habit`. It cannot change after create.
- `title`: 1–200 characters after trim. `notes`: ≤ 10 000 characters.
- `repeat.freq`: `none`, `daily`, `weekdays`, `weekly`, `monthly`, `yearly`. `repeat.days`: for `weekly`,
  1–7 of `mon tue wed thu fri sat sun`. `repeat.until`: optional inclusive date.
- `assignee`: `owner` or an existing agent name.
- `color`: `gray`, `indigo`, `blue`, `teal`, `green`, `amber`, `rose`, `violet`. Default by kind.
- `google`: present only on a synced item. `sync`: `synced`, `pending`, `error`.
- `created_by`, `updated_by`: `owner`, `agent:<name>`, `google`. Set by the server.

### `action`

```json
{"type": "agent_prompt", "agent": "Dana", "prompt": "Recap this week's spending in my room."}
{"type": "run_workflow", "workflow_id": "wf_daily_research", "input": {"topic": "AI"}}
{"type": "send_message", "target": "telegram", "text": "Take the laundry out"}
```

- `agent_prompt`: `agent` exists; `prompt` 1–4000 characters.
- `run_workflow`: `workflow_id` exists; `input` any JSON, optional.
- `send_message`: `target` `telegram` or `web`; `text` 1–2000 characters.
- An action is allowed on `event`, `task`, `reminder` and `habit`. It fires at the occurrence start
  (`event`, `reminder`), at `date`+`time` (`task`, `habit`; no `time` = 09:00).

### `firing`

```json
{"status": "done", "at": 1793084400000, "started_at": 1793084401200, "ended_at": 1793084409000,
 "session_id": "s_41k", "run": {"workflow_id": "wf_x", "number": 12}, "error": ""}
```

- `status`: `scheduled`, `running`, `done`, `failed`, `missed`, `dismissed`.

### `entry`

```json
{
  "key": "item:ci_8m2k4q:1793084400000",
  "source": "item",
  "kind": "event",
  "title": "Dentist",
  "start": 1793084400000,
  "end": 1793088000000,
  "all_day": false,
  "date": "2026-11-05",
  "end_date": "2026-11-05",
  "color": "rose",
  "status": "",
  "moved": false,
  "occurrence": 1793084400000,
  "item_id": "ci_8m2k4q",
  "repeating": false,
  "assignee": "owner",
  "created_by": "owner",
  "action": null,
  "firing": null,
  "habit": null,
  "workflow": null,
  "google": null,
  "readonly": false
}
```

- `key`: unique in a response; stable across reads for the same occurrence.
- `source`: `item`, `workflow_slot`, `workflow_run`, `google`.
- `kind`: an item kind, or `workflow` for the two workflow sources, or `event` for `google`.
- `start` / `end`: instants; `end` is `0` for point entries. For dated entries (`note`, task without
  `time`, all-day) `start` is local midnight of `date`.
- `date` / `end_date`: local dates the entry covers (inclusive).
- `status`: task `open` / `done`; workflow slot `scheduled` / `skipped`; run status of spec #33; firing
  status when a firing exists; else `""`.
- `moved`: an override moved this occurrence. `occurrence`: the computed (original) instant, used to
  address the occurrence in `API-CAL-006`/`007`/`008`.
- `habit`: `{"done": true, "streak": 5}` for habits.
- `workflow`: `{"id","name","guardian","node_id","trigger":"schedule"|"interval","every_minutes":0,
  "run_number":0,"run_status":""}`. For a short interval (`< 60` min) the entry is `all_day` with
  `every_minutes` set.
- `readonly`: true for `workflow_run` and for Google events on calendars that are not writable.

## Operations

### `API-CAL-001` List entries

`GET /v1/calendar/entries?from=<ms>&to=<ms>[&sources=item,workflow,google]`

- `to - from` ≤ 92 days, else `400 range_too_large`. `from` < `to`, else `400 invalid_range`.
- `sources` default: all. `workflow` covers both workflow sources.

`200`:

```json
{"entries": [/* entry */], "timezone": "Asia/Jakarta", "week_start": 1, "now": 1793080000000,
 "google": {"state": "linked"}, "missed": 1}
```

- `missed`: count of firings with status `missed` (any date), for the page banner.

### `API-CAL-002` Create item

`POST /v1/calendar/items` body: `item` without server fields. `201 {"item": item}`.
`400 invalid_item` with field details.

### `API-CAL-003` Get item

`GET /v1/calendar/items/{id}` → `200 {"item": item, "firings": [firing], "history": [{"at","by","summary"}]}`.
`firings`: latest 20. `404 not_found`.

### `API-CAL-004` Update item

`PATCH /v1/calendar/items/{id}` body: any item fields except `kind` and server fields.
`200 {"item": item}`. Changing time or repeat of a repeating item keeps its overrides whose original
instant still matches the new rule and drops the rest.

### `API-CAL-005` Delete item

`DELETE /v1/calendar/items/{id}` → `204`. A synced item is also deleted in Google.

### `API-CAL-006` Overrides

`POST /v1/calendar/overrides`

```json
{"source": "workflow", "ref": "wf_daily_research/every-day", "occurrence": 1793055600000,
 "op": "move", "to": 1793064600000}
```

- `source`: `item` (ref = item id) or `workflow` (ref = `<workflow id>/<trigger node id>`).
- `op`: `move` (needs `to`) or `skip`.
- An item that does not repeat is moved by `API-CAL-004`, not here: `400 not_repeating`.
- `occurrence` must be a computed occurrence of the ref: `400 no_such_occurrence`.
- `200 {"override": {"source","ref","occurrence","op","to","by","at"}}`. A second save on the same key
  replaces the first.

`DELETE /v1/calendar/overrides?source=&ref=&occurrence=` → `204`.

### `API-CAL-007` Complete

`POST /v1/calendar/items/{id}/complete` body `{"occurrence": <ms>, "done": true}`.

- Task: sets `state`; `occurrence` is the task occurrence for a repeating task, else ignored.
- Habit: marks the occurrence of that date. `occurrence` required.
- Other kinds: `400 not_completable`.
- `200 {"item": item, "habit": {"done": true, "streak": 5}}` (`habit` only for habits).

### `API-CAL-008` Firings

`POST /v1/calendar/firings/run` body `{"item_id","occurrence"}` → `202 {"firing": firing}`. Allowed for
`missed`, `failed` and `dismissed`; `running` → `409 already_running`; `done` → `409 already_done`.

`POST /v1/calendar/firings/dismiss` body `{"item_id","occurrence"}` → `200 {"firing": firing}`. Allowed
for `missed` and `failed`.

### `API-CAL-009` Settings

`GET /v1/calendar/settings` →

```json
{"timezone": "Asia/Jakarta", "week_start": 1,
 "agenda": {"enabled": true, "time": "07:00"},
 "ics": {"url": "http://127.0.0.1:18300/cal/3q…Zk.ics"}}
```

`PUT /v1/calendar/settings` body: `timezone`, `week_start` (`0` Sunday or `1` Monday), `agenda`.
`400 invalid_settings` for an unknown zone or a bad time.

### `API-CAL-010` Rotate ICS token

`POST /v1/calendar/ics/rotate` → `200 {"url": "…"}`. The old URL returns 404 at once.

### `API-CAL-011` ICS feed

`GET /cal/{token}.ics` → `200 text/calendar; charset=utf-8`. RFC 5545. One `VEVENT` per item occurrence
and per workflow slot, from 30 days back to 90 days ahead. `UID` = entry `key` + `@lana`. Run entries and
Google events are not included. Wrong token → `404` with an empty body.

### `API-CAL-012` Google link state

`GET /v1/calendar/google` →

```json
{"client_configured": true, "state": "linked", "account": "owner@example.com",
 "calendars": [{"id": "primary", "name": "Owner", "color": "#4285f4", "selected": true, "writable": true}],
 "target": "primary", "last_sync": 1793080000000, "error": ""}
```

- `state`: `off`, `linked`, `syncing`, `needs_reauth`, `error`.

`PUT /v1/calendar/google` body `{"calendars": [{"id","selected"}], "target": "<calendar id>"}` →
`200` same shape. `target` must be selected and writable: `400 invalid_target`.

### `API-CAL-013` Google OAuth

- `PUT /v1/calendar/google/client` body `{"client_id","client_secret"}` → `204`. Stored in the secret
  store; never returned.
- `POST /v1/calendar/google/connect` → `200 {"auth_url": "https://accounts.google.com/o/oauth2/v2/auth?…"}`.
  Scope `https://www.googleapis.com/auth/calendar`. Redirect URI
  `http://127.0.0.1:<port>/v1/calendar/google/callback`. `400 client_missing` without a client.
- `GET /v1/calendar/google/callback?code=&state=` → `302` to `/#/calendar?google=linked` or
  `/#/calendar?google=error`. `state` is a one-use value bound to the browser session.

### `API-CAL-014` Google sync and disconnect

- `POST /v1/calendar/google/sync` → `202`. Pull then push now.
- `POST /v1/calendar/google/disconnect` → `204`. Revokes the token. Linked lana items stay, without
  `google`.

### `API-CAL-015` Stream

`GET /v1/calendar/stream` (SSE). Events:

```text
event: changed
data: {"what": "item"|"firing"|"override"|"run"|"google"|"settings", "item_id": "ci_…"}
```

The client re-reads `API-CAL-001` for its visible range.

### `API-CAL-016` Agent tool `calendar`

Input:

```json
{"op": "list", "from": "2026-11-05", "to": "2026-11-12"}
{"op": "get", "id": "ci_8m2k4q"}
{"op": "create", "item": {/* item fields */}}
{"op": "update", "id": "ci_8m2k4q", "item": {/* fields */}}
{"op": "delete", "id": "ci_8m2k4q"}
{"op": "move", "source": "item", "ref": "ci_8m2k4q", "occurrence": "2026-11-06T07:00", "to": "2026-11-06T09:30"}
{"op": "skip", "source": "workflow", "ref": "wf_x/every-day", "occurrence": "2026-11-06T07:00"}
{"op": "complete", "id": "ci_8m2k4q", "occurrence": "2026-11-06", "done": true}
{"op": "run_missed", "id": "ci_8m2k4q", "occurrence": "2026-11-06T07:00"}
```

- Dates and times in the tool are local (`YYYY-MM-DD` / `YYYY-MM-DDTHH:MM`) in the calendar time zone,
  so the model never computes Unix time.
- `list` returns a compact text agenda (one line per entry with `key`).
- Errors use the codes of the HTTP operations. Risk: low. No approval.

### `API-CAL-017` Telegram

- Daily agenda at `agenda.time`, and on `/agenda`:

```text
📅 Thu 5 Nov
• 07:00 ⚙ Daily research (workflow)
• 10:00 ⏰ Take the laundry out
• 14:00–15:00 Dentist
☐ Task: Pay electricity (added by Dana)
✓ Habits: Read 20 min (🔥 4) · ☐ Walk
⚠ Yesterday: 1 missed — Weekly recap (Dana)
```

- Missed notice (REQ-CAL-007): one message per daemon start, listing up to 10 firings + "and N more".

### `API-CAL-018` Workflow trigger value

Workflow runs started by a moved slot or a `run_workflow` action have `trigger: "calendar"` in
`API-WF-012`/`013` of spec #33. Additive.
