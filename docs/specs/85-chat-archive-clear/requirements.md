# Spec 85: Archive and clear chats

- **Status**: Approved (owner 2026-11-26: "di halam chat bisa archive bot biar yg ga kepake bisa di hide, dan bisa clear chat", design approved with "sip implement")
- **Revision**: 1
- **Branch**: feature/chat-archive
- **Design**: design/lana-design-system.figcraft, page "Chat · Archive & clear" (frames 1–6 desktop, 7–9 phone)
- **Related**: spec 32 (agent rooms, API-ROOM-*), spec 72 (floating agent)

## Context

The Chats page lists one room per agent and team (API-ROOM-001). Agents the owner no longer uses stay in the
list forever, and a room's timeline keeps every old session. The owner wants to hide unused agents (archive)
and to empty a room's timeline (clear).

Decisions taken with the design (defaults the owner approved):

- An archived room stays archived when new messages arrive. Its unread count shows on the Archived row.
- Clear keeps the old sessions in History. It only moves the start of the room timeline. Undo is possible.
- The Archived entry is a row at the bottom of the Chats list, not a fourth filter tab.
- There is no "delete the old sessions" option in this spec (Lana has no session delete). The design's
  checkbox is left out.

## Requirements

### REQ-ARC-001: Room state is stored

The store keeps, per room name, `archived_at` (Unix ms, 0 = not archived) and `cleared_at` (Unix ms, 0 = never
cleared) next to the existing `read_at` (migration 13 adds both columns to `room_reads`, default 0). A row
created only for archive or clear has `read_at` 0, which reads as "never read" as before.

### REQ-ARC-002: Archive a room

`POST /v1/rooms/{name}/archive` with `{"archived": true|false}`. `true` sets `archived_at` to now, `false` sets
it to 0. The answer is `{"archived_at": n}`. Unknown room: 404 `room_not_found`. Body without a boolean
`archived`: 400 `invalid_body`. The agent named `lana` cannot be archived: 409 `room_not_archivable`
(unarchiving it is allowed and harmless).

Archive changes nothing else: schedules, workflows, channels and sending to the room keep working, and sending
a message does not unarchive.

### REQ-ARC-003: Clear a room

`POST /v1/rooms/{name}/clear` with `{}` sets `cleared_at` to now and answers `{"cleared_at": now, "previous":
old}`. It also moves `read_at` forward to now. With `{"restore": n}` it is the undo: `n` must be `0 <= n <=`
the current `cleared_at`, else 400 `invalid_restore`; it sets `cleared_at` to `n` and answers `{"cleared_at":
n, "previous": current}`. Unknown room: 404 `room_not_found`.

Clearing deletes nothing. Sessions, events, memory, knowledge, files, schedules and History stay.

### REQ-ARC-004: Rows carry the state

Every API-ROOM-001 row (list and stream) adds `archived_at` and `cleared_at` (numbers, 0 when unset). After a
clear:

- `last_message` is null unless the newest message is newer than `cleared_at`.
- `unread` counts only events after `max(read_at, cleared_at)`.

### REQ-ARC-005: The timeline starts at the clear

`GET /v1/rooms/{name}/segments` leaves out sessions last updated at or before `cleared_at`
(`core.SessionFilter.UpdatedAfter`, applied in the store query so paging stays right). A segment that started
before the clear and kept going (a channel session) still shows; the web timeline hides its events at or
before `cleared_at`.

### REQ-ARC-006: The next message starts fresh

`POST /v1/rooms/{name}/messages` does not reuse a web session created at or before `cleared_at`; it creates a
new session, so the agent answers with an empty context.

### REQ-ARC-007: Chats list hides archived rooms (frames 1, 2)

- The list, the filter counts, search and the "open the first room" default use only rooms with
  `archived_at == 0`.
- A row shows a ⋯ button on hover in place of the time; right-click on the row opens the same menu:
  "Archive chat", "Clear chat…", separator, "View profile" (`#/agents/<name>`). The `lana` row has no Archive
  item.
- After Archive, a toast "Chat archived" with Undo (6 s) appears at the bottom of the list column. Undo calls
  archive `false`. If the archived room was open, the page goes to `#/chats`.
- When at least one room is archived, the list ends with a rule and an "Archived" row: archive tile, "Archived",
  the archived count, the archived names joined by ", " (truncated), a chevron, and an unread badge when the
  archived rooms have unread messages. It shows under every filter but not while a search has text.

### REQ-ARC-008: Archived screen (frame 3)

- The Archived row opens the archived view (`#/chats/archived`): header "‹ Archived" (‹ goes back to
  `#/chats`), a search "Search archived chats", no filter tabs, no explanation note, and the archived rows
  (same row component, newest activity first). A row on hover shows an "Unarchive" button; Unarchive shows the
  toast "Chat unarchived" with Undo.
- An archived room opens in the normal room view (`#/chats/<name>`, the list column shows the archived view
  while the open room is archived). The room shows a banner above the timeline: archive icon, "Archived —
  hidden from Chats. Messages here keep it archived.", and an "Unarchive" button.
- Empty archived view: "No archived chats".

### REQ-ARC-009: Room header menu (frame 4)

The room header's ⋯ button opens: "Clear chat…" (sub "Remove every message in this chat"), "Archive chat" or
"Unarchive chat" (for `lana`: disabled, sub "Main agent, always in Chats"), separator, "Room settings" (the
existing settings action, if the room has one; otherwise "View profile").

### REQ-ARC-010: Clear dialog (frame 5)

A modal: eraser tile, "Clear chat with <label>?", text "Every message in this chat leaves the timeline.
<label> answers the next message with an empty context." A "STAYS" box lists "<label>'s memory, knowledge and
files", "Schedules, workflows and channels", "Old sessions, readable in History". When the room is working, an
amber line "<label> is working. Clearing stops the turn first." Buttons Cancel and Clear chat (danger). Esc
and Cancel close it. Clear chat first cancels the running turn (`POST /v1/sessions/{id}/cancel` with the
presence session id) when the room is working, then calls clear.

### REQ-ARC-011: Cleared state (frame 6)

- The timeline drops the cleared segments and events at once (the row's new `cleared_at`).
- An empty timeline whose room has `cleared_at > 0` shows: a divider "Chat cleared · <relative date and
  time>", the room avatar, "Fresh start with <label>", "Earlier messages are in History.", and a button "Open
  in History" (`#/history` filtered to the agent if the History page takes an agent filter, else `#/history`).
- The row preview reads "Chat cleared" while `last_message` is null and `cleared_at > 0`.
- A toast "Chat cleared" with Undo (6 s) appears; Undo calls clear with `restore: previous`.

### REQ-ARC-012: Floating agent

The floating agent's chat list, its stack (phone) and its notification counts leave out archived rooms. An
archived room open in the panel stays open.

### REQ-ARC-013: Phone (frames 7–9)

- Swiping a row left reveals two actions: More (opens the action sheet) and Archive (accent). Lana's row shows
  only More.
- Long-press on a row opens a bottom sheet: the room avatar, name and presence, then "Archive chat" (sub "Hide
  it from Chats"), "Clear chat…" (sub "Remove every message"), "View profile".
- The Archived row and screen work as on desktop; Unarchive is in the row's swipe action (label "Unarchive").

## Out of scope

- Deleting old sessions from History.
- Archiving from the Agents page.
