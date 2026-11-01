# Requirements: Agent rooms

- **Issue**: `—`
- **Status**: Approved
- **Revision**: 1
- **Owner**: ridho
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Related ADR**: [ADR Index](../../adr/README.md) — `—`
- **Related diagram**: [Diagram index](../../diagram/README.md) — `—`

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner decision | Chat, 2026-11-02 | Grok-style concept: every agent has its own room, profile, domain and job |
| Owner decision | Chat, 2026-11-02 | A room is one long timeline, not Slack-style threads |
| Owner decision | Chat, 2026-11-02 | The Sessions page stays with the same function and is renamed History; rooms are a new page |
| Owner decision | Chat, 2026-11-02 | The agent page must look alive |
| Owner decision | Chat, 2026-11-02 | Telegram stays as it is; a Telegram group routed to a team is a group room |
| Owner decision | Chat, 2026-11-02 | Sidebar: Home, Chats, Workflows without a title; OBSERVE: History; CONFIGURE: Agents, Knowledge, Skills, MCP, Channels, Settings |
| Owner decision | Chat, 2026-11-02 | The web must be interactive, with loading and live animations as in the design |
| Design | `design/lana-design-system.figcraft`, page "Agent rooms"; `design/export/rooms-1..4-*.png` | Layout and states |
| Existing specs | [#9 web](../9-web-settings-chat/requirements.md), [#10 teams](../10-agent-teams/requirements.md), [#27 trajectory](../27-session-trajectory/requirements.md), [#28 dashboard](../28-monitoring-dashboard/requirements.md) | Behavior that this feature reuses |

> **Gate lifted (2026-11-02).** The owner approved the design and said "aku percaya sama kamu jadi
> implement approve dengan rekomendasi kamu". Open questions are resolved with
> `Rekomendasi agent:` in the resolution table and marked Approved on behalf of the owner.

## Goal

The owner talks to each agent the way they talk to a person in a chat app. Each agent has a name, a
role, a face and one room where all of its conversations, results and questions appear in one
timeline. From the web the owner can see at a glance which agents are working, which are waiting
for them, which have a problem and which are idle. A team of agents has a group room where the
lead hands work to members and the members' answers appear as their own messages.

## Actors

- **Owner** — the only user of lana. Uses the web dashboard and Telegram. Has every permission.
- **Agent** — a configured `[agents.<name>]`. Owns one room.
- **Team** — a configured `[teams.<name>]` with a lead and members. Owns one group room.

## Definitions

- **Room** — the view of every top-level session whose `agent` is one agent or one team, across
  every channel, ordered by time. A room has the same name as its agent or team.
- **Group room** — the room of a team.
- **Segment** — one session inside a room. A room timeline is its segments in creation order.
- **Current web session** — the most recent top-level session of the room's agent with channel
  `web`. Messages sent from the room go to it.
- **Presence** — the live state of an agent: `working`, `waiting`, `problem`, `online`, `resting`.
- **Profile fields** — `title`, `color` and the `JOB.md` file of an agent.
- **Unread** — assistant answers and questions in a room created after the owner last read it.

## Scope

### Included

- A new web page **Chats** with a room list and a room timeline (design `rooms-1`, `rooms-4`).
- Room list with presence, last message preview, time and unread count.
- Room timeline that joins segments, shows session and compaction dividers, and loads older
  segments when the owner scrolls up.
- Sending a message from a room, and starting a new topic (a new web session) from a room.
- Live presence for every agent and team, pushed to the web without a page reload.
- Group room rendering: the lead's handoffs and each member's answer as messages of that member.
- Agent profile fields: `title`, `color` and `JOB.md`.
- An alive agent profile (design `rooms-2`): hero, live status, "Right now" steps, activity feed,
  "What it remembers" (MEMORY.md entries), "This week" stats, job, and what it looks after.
- An alive agent roster (design `rooms-3`): team pulse, one status card per agent and team, and a
  card to create an agent.
- Sidebar grouping and renaming Sessions to History.
- Motion: loading skeletons, presence pulse, typing dots, message enter, progress shimmer, and
  unread badge pop. All motion stops when the OS asks for reduced motion.

### Not included

- Workflows and the posts that workflows make into rooms. Workflows have their own future spec.
- Members talking to each other without the lead. Every member message in a group room comes from
  a lead handoff (spec #10).
- Any change to Telegram behavior, routing, commands or reply format.
- Per-agent knowledge folders (knowledge scope). "What it remembers" uses MEMORY.md.
- Money cost. lana stores tokens, not prices; the profile shows tokens.
- Agent avatars from image files. The avatar is the initial on the agent color.
- Scheduled "next run" lines. lana has no scheduler yet; cards show the last activity instead.

## Assumptions

- `sessions.agent` holds the agent or the team name of a top-level session. — Source: code map,
  `lana.go` `routeAgent`.
- A team member's work runs in a child session created by the `task` tool, and the root session
  stores the `tool/call` and `tool/result` of that handoff. — Source: spec #10.
- The web owner address is `owner`. — Source: spec #9.

## Behavior rules

### REQ-ROOM-001: Room list

The system shall list one room for every configured agent and every configured team, with its
presence, its last message preview and time, its unread count and its number of finished turns in
the last 7 days, sorted by last activity, most recent first. Rooms with no session come last, in
name order.

#### Acceptance criteria

- `AC-ROOM-001`: Given agents `a` and `b` and team `t`, where `b` answered most recently, when the
  owner opens Chats, then the list shows `b` first, then the others by last activity, and each row
  shows the preview of the last user or assistant message of its newest segment.

### REQ-ROOM-002: Room timeline joins segments

The system shall show a room as one timeline of its segments in creation order. Between two
segments it shall show a divider with the channel and date of the newer segment. A
`compaction/summary` event shall show as a divider that says earlier messages were summarized and
that the full log is in History. The newest segment loads first; older segments load when the
owner scrolls to the top.

#### Acceptance criteria

- `AC-ROOM-002`: Given a room with a Telegram segment from yesterday and a web segment from today
  that contains a compaction, when the owner opens the room, then the timeline shows the Telegram
  messages, a divider "via Web · <date>", the web messages, and a "summarized" divider at the
  compaction point.
- `AC-ROOM-003`: Given a room with more segments than one page, when the owner scrolls to the top,
  then the previous page of segments loads above without moving the visible message.

### REQ-ROOM-003: Send from a room

The system shall send a message typed in a room to the room's current web session, and create a
web session for the room's agent when none exists. "New topic" shall create a new web session for
the room's agent and send the next message there.

#### Acceptance criteria

- `AC-ROOM-004`: Given a room with no web session, when the owner sends "halo", then a web session
  for that agent is created, the turn starts there, and the message shows in the timeline.
- `AC-ROOM-005`: Given a room whose current web session exists, when the owner picks "New topic"
  and sends a message, then a new web session is created and the timeline shows a divider before
  the message.

### REQ-ROOM-004: Live room updates

The system shall stream the live output of the room's newest segment into the timeline (text,
tool steps, approvals and questions), exactly as the session view does, and shall show a typing
indicator with a short activity line while a turn runs.

#### Acceptance criteria

- `AC-ROOM-006`: Given an open room, when its agent starts a turn, then a typing indicator with
  the current activity appears within 2 seconds and the answer text streams into the timeline.

### REQ-ROOM-005: Unread count

The system shall count assistant answers and questions in a room's sessions that were created
after the owner last read the room, show the count on the room row, and reset it when the owner
opens the room. A room the owner never opened counts only the last 24 hours.

#### Acceptance criteria

- `AC-ROOM-007`: Given a room read at 10:00 and two assistant answers at 10:05 from Telegram, when
  the owner views the room list, then the row shows unread 2; after the owner opens the room, the
  row shows no unread count.

### REQ-ROOM-006: Presence

The system shall derive the presence of every agent and team, in this precedence:

1. `waiting` — an open approval or question in a session of the agent.
2. `working` — a turn is running in a session of the agent, including a child session of a team
   handoff.
3. `problem` — the last turn of the agent's newest session ended with an error, and no turn
   finished after it.
4. `online` — the agent had activity in the last 6 hours.
5. `resting` — otherwise.

Each presence has a short label: the tool or "Thinking" for `working`, the tool or "Waiting for
your answer" for `waiting`, the error summary for `problem`, the time of the last activity for
`online` and `resting`.

#### Acceptance criteria

- `AC-ROOM-008`: Given an agent with a running turn that also has an open approval, when presence
  is read, then it is `waiting` with the approval tool in the label.
- `AC-ROOM-009`: Given an agent whose newest turn failed with "provider returned 429", when
  presence is read, then it is `problem` with that summary in the label.
- `AC-ROOM-010`: Given an agent with no activity for 7 hours and no open request, when presence is
  read, then it is `resting`.

### REQ-ROOM-007: Live presence push

The system shall push presence and room row changes to every open web page within 2 seconds of the
change, without a page reload.

#### Acceptance criteria

- `AC-ROOM-011`: Given the Chats page open in one tab, when a Telegram message starts a turn for
  agent `a`, then the row of `a` changes to `working` within 2 seconds.

### REQ-ROOM-008: Group room

The system shall render a team room as a group chat: the lead's handoff as a message from the lead
with a handoff chip per member (member name, task title, state), each member's answer as a message
from that member, and the lead's final answer as a message from the lead. Messages from different
agents use each agent's color. The composer offers a mention chip per member; a message that starts
with `@<member>` asks the lead to hand the request to that member.

#### Acceptance criteria

- `AC-ROOM-012`: Given a team turn where the lead handed one task to member `k` and then answered,
  when the owner opens the group room, then the timeline shows the lead's handoff chip for `k`, a
  message from `k` with its answer, and the lead's final answer.
- `AC-ROOM-013`: Given a team room, when the owner sends "@k cek gaji", then the lead's turn
  context tells it to hand the request to member `k`.

### REQ-ROOM-009: Agent profile fields

The system shall let the owner set an agent's `title` (role, at most 60 characters) and `color`
(one of `brand`, `success`, `warning`, `error`, `blue`, `gray`), and edit the agent's `JOB.md`
(at most 4000 characters). An agent without a color gets a stable color from its name.

#### Acceptance criteria

- `AC-ROOM-014`: Given agent `dana`, when the owner saves title "Finance" and color `success`, then
  the config holds them, the room list and profile show "Finance" on a green avatar.
- `AC-ROOM-015`: Given a title of 61 characters or an unknown color, when the owner saves, then the
  save fails with a field error and the config is unchanged.
- `AC-ROOM-016`: Given agent `dana`, when the owner saves `JOB.md`, then the file is written in the
  agent home and the profile shows its content.

### REQ-ROOM-010: Alive agent profile

The system shall open an agent on an **Overview** tab that shows: the hero (avatar with presence,
name, title, persona first line, live status, age, model, permission, channels), **Right now**
(the steps of the running turn, live), **Activity** (the last 20 activity items of the last 7
days), **What it remembers** (the first MEMORY.md entries and the total), **This week** (finished
turns per day for 7 days, finished turns, questions asked, notes learned, tokens), **Job**
(JOB.md) and **Looks after** (Telegram routes and the teams it belongs to). The existing tabs stay.

#### Acceptance criteria

- `AC-ROOM-017`: Given agent `dana` with a running turn, when the owner opens its profile, then
  Right now shows the finished steps and the current step, and updates without a reload.
- `AC-ROOM-018`: Given agent `dana` that answered 3 turns today, asked 1 question, and captured 2
  memories this week, when the owner opens its profile, then This week shows 3 for today, 1
  question, 2 notes, and Activity lists those items newest first.

### REQ-ROOM-011: Alive roster

The system shall show the Agents page as a team pulse (count per presence and finished turns this
week) and one card per agent and team with avatar, presence, title, status line, last activity,
finished turns in 7 days, and a context action: "Answer" when waiting, "Watch live" when working,
"Open room" otherwise. The last card creates a new agent.

#### Acceptance criteria

- `AC-ROOM-019`: Given one working, one waiting and two online agents, when the owner opens
  Agents, then the pulse shows 1 working, 1 waiting, 2 online, and the waiting card's action opens
  its room.

### REQ-ROOM-012: Navigation

The system shall group the sidebar as: Home, Chats, Workflows (no title); **OBSERVE**: History;
**CONFIGURE**: Agents, Knowledge, Skills, MCP, Channels, Settings. History is the existing
Sessions page with the same function. Workflows is shown only when a Workflows page exists. The
default page after login is Chats. Old `#/sessions` links keep working.

#### Acceptance criteria

- `AC-ROOM-020`: Given a logged-in owner, when the dashboard opens with no hash, then Chats opens
  and the sidebar shows the groups and labels above, with History pointing at the sessions list.

### REQ-ROOM-013: Motion and loading states

The system shall show skeleton placeholders while room lists, timelines and profiles load; a pulse
on `working` presence; animated typing dots while a turn runs; a short enter animation for new
messages; and a pop on unread badges. When `prefers-reduced-motion: reduce` is set, the system
shall show the same states without animation.

#### Acceptance criteria

- `AC-ROOM-021`: Given a slow network, when the owner opens Chats, then skeleton rows show until
  data arrives; given reduced motion, then no element animates.

## Contract commitments

| Requirement | Observable commitment | Contract operation |
|---|---|---|
| `REQ-ROOM-001`, `REQ-ROOM-005`, `REQ-ROOM-006` | Room list with presence and unread | `API-ROOM-001` |
| `REQ-ROOM-002` | Segments of a room, paged | `API-ROOM-002` |
| `REQ-ROOM-003` | Send to a room, new topic | `API-ROOM-003` |
| `REQ-ROOM-005` | Mark a room read | `API-ROOM-004` |
| `REQ-ROOM-007` | Global room stream | `API-ROOM-005` |
| `REQ-ROOM-009` | Profile fields in config and agents API | `API-ROOM-006`, `API-ROOM-007` |
| `REQ-ROOM-010` | Agent overview | `API-ROOM-008` |

## State and operation matrix

| Current state | Operation | Result | Allowed |
|---|---|---|---:|
| Room has no web session | Send | Create web session, start turn | Yes |
| Room has a web session with a running turn | Send | Message is queued, as in the session view | Yes |
| Room has a web session | New topic + send | Create web session, start turn | Yes |
| Room of an agent removed from config | Open | Not found | No |

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| Room name is not an agent or team | Not found | HTTP 404 `room_not_found` |
| Empty message | Rejected, nothing created | HTTP 400 `empty_message` |
| Invalid title or color | Rejected, config unchanged | HTTP 400 field error |
| JOB.md too long or stale version | Rejected, file unchanged | HTTP 400 / 409, as other agent files |
| Room stream drops | Client reconnects after 1 s and redraws presence | — |

## Authorization and security

- Every new endpoint uses the existing web auth and CSRF check (`Server.Handle`).
- Previews and activity items are taken from the owner's own sessions; no secret values are shown.
  Tool arguments in labels are truncated to 80 characters.

## Data integrity and concurrency

- Rooms are derived from sessions; no session row or event is rewritten.
- Read marks are one row per room; a later read time always wins.
- Two concurrent "send" calls to a room with no web session create at most one session.

## Accessibility

- Presence is shown with color and a text label, never color alone.
- The room list and timeline are keyboard reachable; new messages are announced through the
  existing announcer.
- Motion follows `prefers-reduced-motion`.

## Scenarios

### SC-001: Answer a waiting agent

**Given** Dana is waiting on a question in a Telegram session
**When** the owner opens Chats
**Then** Dana's row shows `waiting` with an unread badge, and opening it shows the question card at
the bottom of the timeline, where the owner can answer it.

### SC-002: Team works together

**Given** team Career plan with lead Lana and members Karin and Dana
**When** the owner asks the group room a question
**Then** the timeline shows Lana's handoffs, Karin's and Dana's answers as their own messages, and
Lana's summary, while their presence in the room list turns `working` and back.

## Blocking Open Questions

| ID | Question | Controls | Requested reference | Owner | Status | Resolution |
|---|---|---|---|---|---|---|
| `Q-001` | Should a room also send Telegram DMs into the same session as the web? | REQ-ROOM-003 | — | ridho | Resolved | Rekomendasi agent: no. Telegram stays as it is (owner). The room joins sessions for display; web sends go to the current web session. |
| `Q-002` | How long without activity is `resting`? | REQ-ROOM-006 | — | ridho | Resolved | Rekomendasi agent: 6 hours. |
| `Q-003` | Can members talk to each other directly in a group room? | REQ-ROOM-008 | — | ridho | Resolved | Rekomendasi agent: not in this spec. Members answer the lead's handoffs (spec #10). `@member` asks the lead to hand off. |
| `Q-004` | Do proactive posts (workflow results) belong here? | Scope | — | ridho | Resolved | Rekomendasi agent: no, they belong to the Workflows spec. The timeline already renders `knowledge/compiled` and compaction events between messages. |
| `Q-005` | What does the profile show for cost? | REQ-ROOM-010 | — | ridho | Resolved | Rekomendasi agent: tokens, because lana stores no prices. |

None remain open.

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-ROOM-001` | `AC-ROOM-001` | Owner 2026-11-02, design rooms-1 |
| `REQ-ROOM-002` | `AC-ROOM-002`, `AC-ROOM-003` | Owner 2026-11-02 (one timeline) |
| `REQ-ROOM-003` | `AC-ROOM-004`, `AC-ROOM-005` | Design rooms-1 |
| `REQ-ROOM-004` | `AC-ROOM-006` | Design rooms-1 |
| `REQ-ROOM-005` | `AC-ROOM-007` | Design rooms-1 |
| `REQ-ROOM-006` | `AC-ROOM-008`, `AC-ROOM-009`, `AC-ROOM-010` | Owner 2026-11-02 (alive), design rooms-3 |
| `REQ-ROOM-007` | `AC-ROOM-011` | Owner 2026-11-02 (interactive) |
| `REQ-ROOM-008` | `AC-ROOM-012`, `AC-ROOM-013` | Owner 2026-11-02, design rooms-4 |
| `REQ-ROOM-009` | `AC-ROOM-014`, `AC-ROOM-015`, `AC-ROOM-016` | Owner 2026-11-02 (domain/job) |
| `REQ-ROOM-010` | `AC-ROOM-017`, `AC-ROOM-018` | Design rooms-2 |
| `REQ-ROOM-011` | `AC-ROOM-019` | Design rooms-3 |
| `REQ-ROOM-012` | `AC-ROOM-020` | Owner 2026-11-02 (sidebar) |
| `REQ-ROOM-013` | `AC-ROOM-021` | Owner 2026-11-02 (animations) |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit or stated as not applicable.
- [x] State transitions and destructive effects are explicit or not applicable.
- [x] Defaults, ordering, retry, and idempotency are explicit where applicable.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Contract applicability is explicit.
- [x] Blocking Open Questions is `None`.
- **Approved by**: agent on behalf of the owner (gate lifted 2026-11-02)
- **Approved on**: 2026-11-02
