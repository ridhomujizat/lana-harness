# Requirements: Room discussion

- **Issue**: —
- **Status**: Approved (owner approved the figcraft design 2026-11-11: "oke sekarang implement")
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (new session events, team keys, routes)
- **Depends on**: spec 10 (teams, `task` runner), spec 32 (rooms, group room), spec 46 (domain
  knowledge, journals, `PromoteSoon`)

## Sources

- Owner interview 2026-11-10 ([domain knowledge decisions]): the lead moderates a multi-round
  discussion (ask A, pass the point to B, sum up), members see a summary, a round cap, room memory,
  a small promotion pass after a discussion.
- Owner, 2026-11-11, while reviewing the design:
  - rounds stay, but as a safety net; the lead picks who speaks, members may pass, the lead stops on
    agreement or when replies bring nothing new (research: Hermes Agent Bot Mode caps at 3 rounds
    with passes, AutoGen requires a termination condition, Magentic-One counts stalls);
  - the lead may add rounds: one on its own when the last round brought something new and one
    question is still open, after that only with the owner's yes; never more than 5;
  - a discussion starts from a plain question (the lead decides), a Discuss toggle or words such as
    "diskusikan"; the owner can ask for a direct answer instead;
  - the discussion shows as a normal group chat; the summary is a small button that opens a dialog;
  - the group avatar shows two faces, three faces for three agents, two faces and "+N" above three;
  - a "New group room" flow is needed.
- Design: `design/lana-design-system.figcraft`, page "Room discussion"; exports
  `design/export/room-discussion-1..16-*.png`.

## Definitions

- **Discussion**: one moderated exchange inside one turn of a team session. The lead opens it,
  asks members, and ends it with a summary. A session has at most one open discussion.
- **Round**: the n-th time the lead asks a given member in a discussion. The discussion's round is
  the highest round asked so far.
- **Room memory**: notes kept per team (decisions, facts, open questions) in
  `<home>/rooms/<team>/memory.json`.

## Requirements

### Team settings

- **REQ-RDS-001** A team has discussion settings, each optional with a default:
  `discuss` (`auto` | `ask`, default `auto`), `rounds` (1–5, default 3), `extra_rounds`
  (`once` | `ask` | `never`, default `once`), `room_memory`, `share_summary`, `learn`, and
  `telegram_all` (booleans, default true). `description` is the room's purpose ("What is it for?").
- **REQ-RDS-002** The team API reads and writes these settings with the existing team fields
  (API-RDS-010). Unknown values are rejected with the config check's message.

### The discussion tools

- **REQ-RDS-010** The lead of a team session is offered four tools: `discuss_start`, `discuss_ask`,
  `discuss_more`, `discuss_end` (API-RDS-020). Other sessions are not offered them.
- **REQ-RDS-011** `discuss_start` opens a discussion with a topic, two or more members with the
  angle each covers, and an opening message to the owner. Members must belong to the team. It
  fails when a discussion is already open.
- **REQ-RDS-012** `discuss_ask` asks one member with a short message (shown in the room as the
  lead's message) and returns the member's reply. The member's first ask creates a child session
  (spec 10 runner); later asks continue it with only the messages since its last reply. The member
  sees the topic, its angle, the discussion so far, the lead's message, and the room memory, and is
  told to answer in at most 120 words or to pass with `PASS: <reason>`. Asks in one step run in
  parallel, at most three at a time; the rest wait.
- **REQ-RDS-013** An ask whose round would pass the limit (`rounds` plus granted extra rounds) is
  refused with a message telling the lead to end the discussion or call `discuss_more`.
- **REQ-RDS-014** `discuss_more` adds one round. With `extra_rounds = once` the first call is granted
  at once; later calls, and every call with `ask`, ask the owner through the session's question
  service ("Add 1 round" / "Sum up now"). `never` refuses. No discussion passes 5 rounds.
- **REQ-RDS-015** `discuss_end` closes the discussion with a reason, a conclusion, agreed points,
  points still different, open points (each with the member it came from, when there is one), the
  part each member played, and notes for room memory. The tool output tells the lead to answer the
  owner in a few sentences.
- **REQ-RDS-016** Every discussion step is a session event in the team session (API-RDS-001..003):
  the start, each lead ask, each member reply or pass, each granted extra round, owner stops, and
  the end with its summary.
- **REQ-RDS-017** The lead's system prompt for a team explains when to open a discussion (by
  `discuss`), how to moderate (one member at a time, @mention, pass a point on, parallel opening
  views for large rooms), when to stop (agreement, two replies with nothing new, a difference of
  priorities), and the round rules. It also lists the room memory.

### Owner control

- **REQ-RDS-020** The owner can turn on Discuss for one message; the lead is then told to open a
  discussion. With `discuss = ask` the lead opens one only then, or when the owner asks in words.
- **REQ-RDS-021** The owner can stop an open discussion (Stop) or ask for a direct answer instead
  (API-RDS-011). The next ask is refused with an instruction to end the discussion now (stop) or
  to end it and answer alone (direct). A stop is recorded as an event.
- **REQ-RDS-022** An owner message sent during a discussion reaches the lead between steps (the
  existing queue), so the lead can pass it on.

### After a discussion

- **REQ-RDS-030** With `room_memory`, the end's notes are added to room memory. A note that names a
  note it replaces marks the old one replaced (valid until, replaced by) instead of deleting it.
- **REQ-RDS-031** With `share_summary`, each member's own room gets a message from that member's
  side: the room name, the conclusion, and its part, marked as a discussion summary.
- **REQ-RDS-032** With `learn`, each member that has a knowledge domain gets a journal block with
  the topic, the conclusion and its part, and a domain promotion is requested (spec 46
  `PromoteSoon`).
- **REQ-RDS-033** With `telegram_all`, a team session on Telegram posts every discussion message
  to the chat as `<Name>: <text>`; without it only the lead's reply is posted.

### Room memory

- **REQ-RDS-040** The owner can list, add, edit and delete room memory notes (API-RDS-012). Notes
  have a kind (`decision`, `fact`, `open`), text (≤ 400 characters), the agent or owner that added
  them, the source (a discussion id or `owner`), and timestamps.
- **REQ-RDS-041** The lead's prompt and every member ask include the current notes (not replaced),
  newest first, up to 4,000 characters.

### Web

- **REQ-RDS-050** A team room renders a discussion as a normal group chat: the lead's opening and
  asks as lead messages, each reply as a message of that member (with "replying to" and a quote
  when it answers someone), passes as a small line, extra rounds as a lead message with an
  "Extra round · n/m" chip, a stop as a divider, a failed member as an error line.
- **REQ-RDS-051** While a discussion runs, a bar under the room header shows the topic, the lead's
  current action, a face per member (spoke, speaking, waiting, passed), the round `n/m`, and Stop.
  The lead's opening shows "Answer directly instead".
- **REQ-RDS-052** The turn that ended a discussion shows a small "Summary" button under the lead's
  final message; it opens a dialog with the conclusion, Agreed / Still different / Open with member
  chips, and what happened after (notes saved, summary shared, members learning), with View room
  memory.
- **REQ-RDS-053** A member's room shows a shared summary as a card: team avatar, room name, time,
  rounds, the conclusion, its part, and Open discussion (opens the team room).
- **REQ-RDS-054** The room header's Members button opens Room settings with tabs Members (lead,
  members, add, remove, make lead), Memory (notes by kind, search, add, edit, delete, replaced
  shown struck through), and Discussion (the settings of REQ-RDS-001).
- **REQ-RDS-055** Chats has "+ New" with "New agent" and "New group room". New group room asks for
  a name, a purpose, members (search, checkboxes, the lead marked, Make lead), shows the discussion
  defaults and a preview, and creates the team (API-RDS-010). More than three members shows a cost
  hint. A new room opens on an empty state with its members and three suggestions.
- **REQ-RDS-056** A team's avatar shows two faces for two agents, three faces for three, and two
  faces and "+N" for more.
- **REQ-RDS-057** The room composer has a Discuss toggle (REQ-RDS-020). Phones get the same views.

## Acceptance

- **AC-RDS-001** In a team with two members, a plain question that needs both views produces a
  discussion: opening, at least one ask per member, replies shown as each member's messages, an
  end, and a final lead answer with a Summary button.
- **AC-RDS-002** With `rounds = 1` and `extra_rounds = never`, a second ask to the same member is
  refused and `discuss_more` is refused.
- **AC-RDS-003** With `extra_rounds = once`, the first `discuss_more` is granted without a question
  and the second asks the owner.
- **AC-RDS-004** Stop during a discussion makes the next ask fail with the stop instruction and the
  room shows the stop divider.
- **AC-RDS-005** After the end, room memory holds the notes, each member's room shows the summary
  card, and members with a domain have a journal block.
- **AC-RDS-006** A team created from New group room appears in Chats with the right avatar and
  opens on the empty state.

## Out of scope

- Members speaking without the lead, or several discussions at once in one session.
- Decision buttons generated by the summary itself (the lead may still use `ask_user`).
- A per-room cost estimate beyond the static hint.

[domain knowledge decisions]: ../46-domain-knowledge/requirements.md
