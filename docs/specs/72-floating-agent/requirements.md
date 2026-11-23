# Spec 72: Floating agent

Status: Implemented r1, 2026-11-23 (approved the same day: "oke sip skrng implement"). Owner: design reviewed in chat on 2026-11-23. Design: FigCraft page "Floating agent" in `design/lana-design-system.figcraft` (frames 1–14 and the behavior notes).

## Problem

The chat dock (released in 0.5.0-beta.14) keeps agent chats on screen as Facebook-style windows and tabs. It only appears after the owner leaves an active chat, it takes a lot of room with two windows, and switching agents means going back to the Chats page. On the Workbench the windows cover the terminals.

The owner wants one floating agent at the bottom right of every page, with full control (send, answer, approve, stop), a way to switch agents without leaving the page, and on a phone a draggable bubble that behaves like the iOS AssistiveTouch button.

## Decisions (owner, 2026-11-23)

1. One floating agent replaces the dock's windows, tabs, peek card and chat heads.
2. Picking an agent is not a dropdown. The panel has two screens: a chat list like the Chats page sidebar, and the room. A row opens the room; the room header has a back button (`‹`) to the list.
3. When the panel is open, the launcher pill stays visible under the panel (option 2B). There is no black round close button.
4. Pill, minimized state and notification are one component: the minimized state is half a pill stuck to the right screen edge; a notification grows the pill upward into a card whose bottom row stays the pill.
5. No settings menu. The pill starts minimized at the edge. First only on the Workbench; later the same day the owner asked for the Workbench look on every page ("terapin di semua halaman").
6. A notification makes the pill grow by itself; the owner can shrink it again and expand it later.
7. Phone: a draggable bubble that glides to the nearest screen edge when released, like AssistiveTouch. Several active agents show as a stack of heads; a tap fans them out.
8. All UI text is English.

## Defaults (from the design notes)

- **D1** The floating agent is hidden on the Chats section (`#/chats`, `#/chats/{name}`), like the dock.
- **D2** Inline Approve / Deny in the notification card is not built: there is no list of pending approvals, they only arrive on a session's SSE stream. The card has **Open**, which opens the panel in that room, where the room timeline shows the approval as usual.
- **D3** Web only. The status of every room comes from the existing rooms stream (`useRooms`); the full session stream runs only for the room open in the panel (RoomView).

## Requirements

### State

- **REQ-FA-001** A store `app/floating-agent-store.ts` keeps, per browser in localStorage (`lana.floatingAgent.v1`): the current room name, whether the panel is open, the panel view (`list` or `room`), whether the pill is tucked into the edge (default tucked), the phone bubble position (`side` left/right/top/bottom and `pct` 0–1 along that side), and whether the phone bubble is hidden. Blocked storage starts from defaults. The old dock key `lana.chatDock.v1` is removed on first load.
- **REQ-FA-002** The current room defaults to, in order: the room the owner last opened in the panel or on the Chats page, a room waiting on the owner, a working room, the room with the newest message.

### Desktop pill (> 760 px)

- **REQ-FA-010** A pill sits flush to the right screen edge, 24 px above the bottom, on every signed-in page except the Chats section. It shows the current room's avatar (a ring while working or waiting), its name and a status line (presence label while busy, else the title), a divider, up to two other rooms that wait on the owner or have unread messages (avatar with a `!` or count badge), and a round chevron button. Clicking an avatar of another room opens the panel in that room.
- **REQ-FA-011** The chevron button or a click on the current agent opens and closes the panel; the chevron points up when closed and down when open. ⌥Space toggles the panel from anywhere except a terminal (xterm) and text inputs.
- **REQ-FA-012** On every page the pill is tucked by default: half a pill flush to the right screen edge with the avatar, a badge (needs-you count, else unread count) and `‹`. Clicking it slides the full pill out, also flush to the edge; its round button becomes `›`, which tucks it again. The choice is remembered.

### Panel

- **REQ-FA-020** The panel (408 × 660, capped by the viewport) opens right above the pill, aligned to its right edge.
- **REQ-FA-021** List view: header "Chats" with "Open Chats page" (`↗`) and "Close" (`–`), a search field (name, title, message preview), filters All / Working / Needs you with counts, and rows built like the Chats page rows (avatar, name, time, title, status or preview, unread badge). A row opens that room in the panel.
- **REQ-FA-022** Room view: header with `‹` (and the number of unread or waiting rooms other than this one), avatar, name and presence, then "Open full chat" (`↗`) and "Close" (`–`). The body is `RoomView variant="dock"`, so the composer, questions, approvals, files and stop work as on the Chats page.
- **REQ-FA-023** When another room waits on the owner, an amber strip under the room header names it with "View", which switches the panel to that room.
- **REQ-FA-024** Esc in the room view goes back to the list; Esc in the list closes the panel. Opening a room marks it current.

### Notifications

- **REQ-FA-030** When the panel is closed and a room starts waiting on the owner, or its unread count goes up, the pill grows upward into a card: the room's avatar, name, "Needs you · now" or "New reply · now", the presence label or the message preview, and **Open** (panel in that room) and, for a reply, **Reply** (same, focusing the composer). A `–` button shrinks the card back to the pill.
- **REQ-FA-031** A waiting card stays until the owner shrinks it, opens it, or the room stops waiting; a reply card shrinks by itself after 8 s. A newer event replaces the card. No card while the panel shows that room.
- **REQ-FA-032** When tucked, a notification slides the pill out from the edge with the card; shrinking the card keeps the pill out until `›`.

### Phone (≤ 760 px)

- **REQ-FA-040** A 60 px bubble floats over the page with the current room's avatar, a status ring and a badge. It can be dragged anywhere; on release it glides (≈ 300 ms spring) to the nearest left or right edge, or to the top or bottom edge when released within 15 % of it, 8 px from the edge and inside the area between the top bar and the safe area. The position is saved as side + percentage.
- **REQ-FA-041** After 3 s without interaction or events the bubble fades to 55 % opacity; an event or a touch restores it. When the front room's state changes a short label shows next to the bubble for 2 s.
- **REQ-FA-042** While dragging, a × target appears at the bottom center; dropping on it hides the bubble. The phone navigation menu then shows "Show agent bubble", which brings it back.
- **REQ-FA-043** With two or more active rooms (working or waiting) the bubble is a stack: the front head is a waiting room, else the newest working one; up to two more heads peek behind it, and a count badge shows the number of active rooms. A tap fans the heads upward with a label each (name and status) and an "All chats" button; a tap on a head opens that room; a tap outside folds them again.
- **REQ-FA-044** A tap on the single bubble opens the current room in a sheet (full width, 52 px from the top) with the same header as the panel's room view; `‹` shows the list in the same sheet. The sheet closes with `–`, the scrim, or Esc.

### Clean-up

- **REQ-FA-050** The chat dock (`chat-dock.tsx`, `chat-dock-store.ts`, `chat-dock.css`, its tests) is removed. The Chats page rows move to a shared `room-list-row.tsx` used by the Chats page and the panel list.
- **REQ-FA-051** The preview harness accepts `?fa=` to pin the floating agent state (e.g. `?fa=lana:open:room`, `?fa=list`, `?fa=notif:researcher`).
