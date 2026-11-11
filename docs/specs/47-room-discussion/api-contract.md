# API contract: Room discussion

- **Status**: Approved
- **Revision**: 1

## Session events

Appended to the team (lead) session in the current turn (`turn_id` set), served unchanged by the
session event routes and the SSE stream. Every payload has `v: 1` and the discussion `id`.

### API-RDS-001 `discussion/started`

```json
{"v":1,"id":"d_06GW…","call_id":"toolu_…","topic":"Ambil tawaran Tokopedia atau bertahan?",
 "opening":"Ini perlu dilihat dari karier dan uang, jadi saya ajak Karin dan Dana diskusi dulu.",
 "lead":"lana","members":[{"agent":"karin","angle":"karier & pasar"},{"agent":"dana","angle":"keuangan"}],
 "rounds":3,"extra_rounds":"once","requested":true}
```

`requested` is true when the owner turned on Discuss for the message.

### API-RDS-002 `discussion/message`

```json
{"v":1,"id":"d_…","call_id":"toolu_…","role":"member","agent":"dana","round":1,
 "text":"Hati-hati. Setelah kos…","reply_to":"karin","quote":"Saya condong ambil.",
 "passed":false,"failed":false,"child_session_id":"06GX…"}
```

`role`:

- `lead` — an ask (`agent` is the member asked, `text` the lead's message) or an extra round
  (`extra: true`, `round` is the new limit);
- `member` — a reply (`passed` when the member passed; `failed` with `text` as the reason when
  the member could not answer);
- `owner` — a stop: `stop` is `stop` or `direct`, `text` empty.

### API-RDS-003 `discussion/ended`

```json
{"v":1,"id":"d_…","call_id":"toolu_…","reason":"agreed","rounds":2,"limit":3,
 "summary":{"conclusion":"Ambil, tapi nego dulu ke 31 jt.",
   "agreed":[{"text":"Gaji 28 jt wajar untuk pasar Jakarta.","agent":"karin"}],
   "different":[{"text":"Ambil hanya kalau ≥ 31 jt.","agent":"dana"}],
   "open":[{"text":"Belum tahu apakah Tokopedia mau 31 jt."}]},
 "parts":{"dana":"Menunjukkan bonus Rp 12 jt yang hilang."},
 "notes_saved":3,"shared_with":["karin","dana"],"learning":["dana"]}
```

`reason`: `agreed`, `no_progress`, `limit`, `priorities`, `stopped`, `direct`, `done`.

## API-RDS-010 Team routes (spec 10, extended)

`POST /v1/teams`, `GET|PUT|DELETE /v1/teams/{name}` carry, besides `lead`, `members`,
`description`, `version`/`expected_version`:

```json
{"discuss":"auto","rounds":3,"extra_rounds":"once","room_memory":true,
 "share_summary":true,"learn":true,"telegram_all":true}
```

GET always returns every key with its default filled in. On write an omitted key keeps the
current value (PUT) or the default (POST); a key equal to its default is not written to
`config.toml`. Config keys in `[teams.<name>]`: the same names.

## API-RDS-011 `POST /v1/sessions/{id}/discussion/stop`

Body `{"mode":"stop"|"direct"}` → `200 {"stopped":true}`; `200 {"stopped":false}` when the session
has no open discussion. CSRF and Origin rules as for the other session writes.

## API-RDS-012 Room memory

- `GET /v1/rooms/{name}/memory` → `{"notes":[note…]}` newest first, replaced notes included.
- `POST /v1/rooms/{name}/memory` `{"kind","text"}` → `201 note`.
- `PUT /v1/rooms/{name}/memory/{id}` `{"kind"?,"text"?}` → `200 note`.
- `DELETE /v1/rooms/{name}/memory/{id}` → `200 {"deleted":true}`.

```json
{"id":"n_06GW…","kind":"fact","text":"Target nego: Rp 31 jt.","agent":"dana",
 "source":"d_06GW…","created_at":1762876680000,"updated_at":1762876680000,
 "valid_until":0,"replaced_by":""}
```

`name` must be a team (404 `not_found` otherwise). `kind` ∈ `decision`, `fact`, `open`; `text`
1–400 characters (422 `invalid`).

## API-RDS-013 Room message

`POST /v1/rooms/{name}/messages` accepts `"discuss": true` (team rooms only; ignored otherwise).

## API-RDS-020 Tools

- `discuss_start {topic, opening, members:[{agent, angle}]}` → text: the discussion id, the
  members, the round limit, and how to continue.
- `discuss_ask {agent, message, reply_to?, quote?}` → JSON
  `{"agent","round","limit","reply","passed","failed","error"?}`.
- `discuss_more {message, reason}` → text: granted (new limit) or refused/declined with what to do.
- `discuss_end {reason, conclusion, agreed[], different[], open[], parts{}, notes[{kind,text,replaces?}]}`
  → text: what was saved and shared, and to answer the owner now.
