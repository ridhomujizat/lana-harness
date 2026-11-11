# Tasks: Room discussion

- **Status**: Completed (Telegram posting not checked live)
- **Revision**: 1

Implemented directly in the main session (owner, 2026-11-11: "saat implement full opus untuk
sementara"), Go and web.

### T-001 Go: team settings and team API

- Files: `core/config.go` (TeamConfig discussion keys, display_name, defaults), `config/validate.go`,
  `web/teams.go`, `web/rooms.go` (team display_name on room rows), tests.
- Status: Done (9fedf44, fec996e). Covers REQ-RDS-001, REQ-RDS-002, API-RDS-010.

### T-002 Go: discussion engine

- Files: `core/discussion.go` (events, inputs, DiscussionService, DiscussionShare), `store/store.go`,
  `agent/discussion.go` (state per session, rounds, extra rounds, stop, member prompt, end),
  `agent/turn.go` (offer + ToolCtx + prompt), `agent/tooloffers.go`, `tools/discuss.go`,
  `internal/agenthome/prompt.go` (discussion guide), `agent/discussion_test.go`.
- Status: Done (9fedf44). Covers REQ-RDS-010..017, REQ-RDS-021, API-RDS-001..003, API-RDS-020.

### T-003 Go: after a discussion, room memory, routes

- Files: `internal/roommemory`, `agent/knowledge/discussion.go` (journal + PromoteSoon),
  `lana_discussion.go`, `lana.go` (DiscussionPostWith, StopDiscussion, RoomMemory),
  `cmd/lana/daemon.go`, `cmd/lana/web.go`, `channels/telegram/discussion.go`, `web/discussion.go`,
  `web/rooms.go` (discuss flag), tests.
- Status: Done (9fedf44). Covers REQ-RDS-020, REQ-RDS-022 (existing queue), REQ-RDS-030..033,
  REQ-RDS-040, REQ-RDS-041, API-RDS-011..013.

### T-004 Web

- Files: `lib/discussion.ts`, `views/discussion.tsx`, `views/discussion-settings.tsx`,
  `views/room-settings.tsx`, `views/new-group-room.tsx`, `views/room-group.tsx`,
  `views/room-timeline.tsx`, `views/chats.tsx`, `views/turn.tsx`, `views/presence.tsx`,
  `api/discussion.ts`, `api/teams.ts`, `api/rooms.ts`, `styles/pages-gr8r/discussion.css`, tests.
- Status: Done (fec996e). Covers REQ-RDS-050..057.
- Not built from the design: the "TRY" suggestion buttons on a new room's empty state (no source
  for room-specific suggestions yet).

### T-005 Live check

- Status: Done 2026-11-11 for the web; Telegram posting pending (the scratch home has no Telegram).
- Scratch daemon (:18399): team `career-plan` ("Career plan", lead `lana` on `claude-code/sonnet`,
  members `karin` and `dana` on `deepseek/deepseek-flash`) created with `POST /v1/teams` and applied
  live without a restart.
- First run: a plain decision question was answered by the lead alone. Fix: the lead prompt now
  says a decision touching more than one member's area must open a discussion, and names the
  `mcp__lana__` prefix Claude Code shows.
- Second run (same question, new topic): Claude Code loaded the tools with ToolSearch, opened the
  discussion, asked Karin and Dana (sequentially over MCP), ended with reason `agreed` after one
  round, saved 3 room memory notes (decision, fact, open), shared the summary into both member
  rooms, and journaled it for both. The answer was 4 short paragraphs with a question.
- Screens checked at 1440 and 390: the group chat, Summary dialog, member card in Karin's room,
  Room settings › Memory, New group room. Fixed during the check: the lead's answer showed the
  turn's start time.

### T-006 Knowledge-aware discussions and team work (owner, 2026-11-11)

- Design: frames "Knowledge · Development/Trading – discussion/members" and "Work · Development/Trading"
  on the Room discussion page; exports `room-discussion-17..22-*.png`.
- Go (98a9faf): `core.AgentCapabilities`; the lead roster lists each member's knowledge (pages and
  topics) and capabilities; `discuss_start` checks each team member's matching notes, records
  them and the members left out, and tells the lead; a member's briefing lists its matching pages;
  replies carry `notes` (from a trailing "Notes:" line and knowledge_read calls) and `new_point`;
  any team member can be asked mid-discussion. `task` takes `context` (earlier child sessions whose
  answers the child receives) and adds the agent's matching pages; the task result reports
  `tools` by name, `notes` and `files`. Room rows carry `can`.
- Web: numbered handoff rows with capabilities, notes and state; tool tags, "Used N notes" popover,
  changed-file chips and "passed X's findings to Y" on team work; knowledge chips with "Why X",
  "not invited" lines and "New point" in discussions; knowledge and capabilities per agent in
  Room settings › Members and New group room.
- Live check (scratch :18399): "Karin then Dana" request — the lead delegated to Karin naming her
  notes, then to Dana with `context` = Karin's session; results reported knowledge_read ×4 with 4
  notes (Karin) and 2 notes (Dana); the room showed the numbered plan, tags and the pass line.
- Not built: the "waiting" state of a planned step (the lead does not declare a plan before the
  first task).
