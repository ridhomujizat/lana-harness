# Requirements: Agent teams

- **Issue**: `—`
- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (`[teams.<name>]` config, `task` and `task_continue` tools, event and SSE fields, sessions and agents API, team endpoints)
- **Related ADR**: [ADR Index](../../adr/README.md) — [0003](../../adr/0003-multi-agent-with-channel-routing.md), [0004](../../adr/0004-permission-mode-per-agent.md), [0009](../../adr/0009-session-as-append-only-event-log.md), [0010](../../adr/0010-profile-and-config-patch.md), [0011](../../adr/0011-subagent-as-child-session.md), [0017](../../adr/0017-agent-teams-as-roster-over-subagents.md)
- **Related diagram**: [Diagram index](../../diagram/README.md) — [0002](../../diagram/0002-agent-loop-and-approval.md), [0004](../../diagram/0004-profile-fork-subagent-and-protocols.md)
- **Related specs**: [Spec #9 web view](../9-web-settings-chat/requirements.md) (sessions, chat, agents, and settings pages that this spec extends), [Spec #6 daemon and Telegram](../6-daemon-telegram/requirements.md) (`/agent` command), [Spec #5 agent home](../5-agent-home/requirements.md) (agent prompt)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner instruction 2026-10-30 | Brief: "UI mockup is in design/… include everything in the mockup, including agent teams" | scope of this spec |
| Dashboard mockup | `design/export/screens/sessions-team-chat.layout.txt`, `design/export/screens/team-message-states.layout.txt`, `design/export/screens/sessions-new-session-agent-picker.layout.txt`, `design/export/screens/agents-list.layout.txt`, `design/export/screens/skills-editor-add-agents.layout.txt`, `design/export/screens/sessions-filter.layout.txt` | UI for team blocks, pickers, and team rows |
| Spec #9 rev 2 decisions 6, 7, 8, 10 | Sessions list facets, chat view, agents page, skills add-agent dialog include teams | UI extensions in this spec |
| ADR 0011 | `task` and `task_continue`, child sessions, limits, permission intersection, cancel cascade | delegation mechanism |
| ADR 0017 | Team as a roster over child sessions; no peer messages or board in v1 | team model |
| ADR 0004 | Policy order and fail-closed approvals; `Exec` risk for `task` (ARCHITECTURE §9) | permissions and approvals |
| ADR 0009 | Append-only log; history derived from events; children have their own log | event and replay rules |
| ADR 0010 | Config is data; `config.Check` validates before save | config keys and validation |
| Code (read 2026-10-30) | `agent/registry.go`, `agent/turn.go`, `agent/toolrunner.go`, `core/config.go`, `core/session.go`, `core/event.go`, `core/tool.go`, `core/approval.go`, `config/validate.go`, `tools/module.go`, `tools/permission/evaluator.go`, `internal/agenthome/prompt.go`, `web/sessions.go`, `web/sse.go`, `web/assets/views/session.js` | feasibility; gaps listed in `design.md` |

Anything not supported by a source or an explicit approval goes into Blocking Open Questions. Agent assumptions are
not requirements.

## Goal

The owner defines a team once in `config.toml`: a lead agent and the agents it can call. The owner can then pick the
team the same way as an agent, in the dashboard, with `/agent` in Telegram or the CLI, or in a route. When the owner
sends a task, the lead plans the work and delegates parts of it to members. The owner can see who did what in the chat,
without leaving the conversation, and each member's work stays a normal session the owner can open.

## Actors

- **Owner** — the only human user. Declares teams, picks a team as a session's agent, and answers the approvals of
  the lead and its members.
- **Lead agent** — the agent configured as `lead`. Runs the team's turns, decides when to delegate, and writes the final
  answer.
- **Member agent** — an agent listed in `members`. Runs only as a child session of the lead, for the work the lead
  gives it.
- **Model provider** — the provider of each agent. The lead and each member use their own provider and model.

## Definitions

- **Team** — a named roster in `[teams.<name>]`: one `lead`, one or more `members`, and an optional `description`.
- **Team session** — a session whose agent is a team name. Its turns run the lead.
- **Delegation list** — the agents the current agent may name in `task`. For a team session it is the members plus the
  lead's own `subagents`. For another agent it is its `subagents`.
- **Child session** — a session created by `task` (ADR 0011). It has `parent_session_id` and `parent_call_id`.
- **Delegation step** — one hop of team work: the lead sends a task to a member (outgoing step) or a member's result
  returns to the lead (return step). A completed `task` call is two delegation steps.
- **Team work** — the delegation steps and the child tool calls of one team turn, shown as one panel in the chat.
- **Root session** — the session with no parent. The owner's session.
- **Subagent depth** — the number of parent links above a session. A root session has depth 0.

## Scope

### Included

- `[teams.<name>]` in `config.toml`: keys, validation, and the shared name space with agents.
- A team as a session's agent: create, change with `SetAgent`, route target, `/agent <team>` in Telegram and the CLI.
- The lead runs with the delegation list and a roster in its prompt. `task` and `task_continue` are enabled for the lead.
- `task` and `task_continue`: child sessions, result shape, limits, permission intersection, approvals, cancel cascade,
  parallel `task` calls, and usage accounting.
- Child sessions are hidden from the top-level session list and are readable by id.
- Live frames and events of a running child are sent on the parent's web stream, tagged with the child session and agent.
- Web API: team information in the sessions list and detail, team rows in `GET /v1/agents`, and team endpoints for
  create, read, replace, and delete.
- Dashboard: the team message block and Team work panel, the team choice in the session picker, the composer model
  note for teams, team rows in the sessions filter and the agents list, and the skills "add to team" choice.

### Not included

- Nested teams: a team as a member or as a lead of another team.
- Peer messages between members, a shared task board, and a team-wide shared memory.
- Team-level permission, model, tools, MCP servers, or skills. A team uses its lead's permission and its members' own
  settings.
- A team detail page or a team form in the dashboard. The mockup has none (see `Q-013`).
- `ask_user` for child sessions. Spec #11 decides questions for root sessions only in this version.
- Fork of a child session, and a `task` that inherits the parent's history (ADR 0011).
- A cost screen that splits usage by agent. Usage is added to the parent step (`REQ-TEAM-021`).
- Hot reload of teams. A saved team takes effect after a daemon restart, as for every config key (spec #9 `Q-008`).
- A team in the MCP or skill configuration pages, except the skills "add to team" choice.

## Assumptions

- `task` and `task_continue` are built-in tools in the `tools` module, like `bash`. Source: ADR 0011; `tools/module.go`.
- Team names and agent names use the agent name rule `^[a-z][a-z0-9_-]{0,31}$`. Source: `internal/agenthome/agenthome.go` `ValidName`.
- The web view, the CLI, and Telegram all use the same agent loop and approval path. Source: spec #9 Assumptions; ADR 0004.
- A child session's approvals go to the channel of its root session. Source: ADR 0011 ("sent through the parent's channel").
- Spec #9 rev 2 defines the config writer (section rewrite, `expected_version`, `config.Check`, audit line, `restart_required`)
  and the agent endpoints in the same style. This spec reuses them.

## Behavior rules

### Configuration

#### REQ-TEAM-001: Team declaration

The system shall accept `[teams.<name>]` tables in `config.toml` with the keys `lead` (string, required), `members`
(array of strings, required), and `description` (string, optional). A team table shall be loaded into the effective
configuration beside `[agents.<name>]`.

- `AC-TEAM-001`: Given `[teams.research-squad]` with `lead = "lead"` and `members = ["researcher", "writer"]`, when `config.Check` runs, then it returns no error and the effective config lists the team with those members.
- `AC-TEAM-002`: Given a team named `coder` and an agent `[agents.coder]`, when `config.Check` runs, then it fails with key `teams.coder` and message `name is already used by an agent`.

#### REQ-TEAM-002: Team validation

The system shall validate teams before any save or load, with the errors listed in the contract (`API-TEAM-001`). A
team shall not be used when validation fails.

- `AC-TEAM-003`: Given each invalid team in the table of `API-TEAM-001` (unknown lead, lead that is a team, member that is a team, lead listed in `members`, duplicate member, empty `members`, more than 8 members, description over 120 characters, unknown key, name not matching the rule), when `config.Check` runs, then it fails with the key and message from that table row and loads nothing.
- `AC-TEAM-004`: Given `[agents.a] subagents = ["research-squad"]` where `research-squad` is a team, when `config.Check` runs, then it fails with key `agents.a.subagents.0` and message `is a team; subagents must be agents`.
- `AC-TEAM-005`: Given a route with `agent = "research-squad"` where that team exists, when `config.Check` runs, then the route is accepted; given a route with an unknown name, the message lists the available agents and teams.

### Team sessions

#### REQ-TEAM-003: A team as the agent of a session

The system shall accept a team name wherever it accepts an agent name: session creation, routes, the library
`CreateSession`, `SetAgent`, and the Telegram and CLI `/agent` command. A team session's agent is the team name.

- `AC-TEAM-006`: Given session `S` created with agent `research-squad`, when a user message is sent, then the turn uses the lead's provider, model, workspace, and permission, and the turn's system prompt contains the team roster.
- `AC-TEAM-007`: Given a session with agent `default`, when `SetAgent(S, "research-squad")` is called, then a `model/selection` event with `agent = "research-squad"` and the lead's provider and model is appended.

#### REQ-TEAM-004: The lead runs with the delegation list

The system shall run a team session's turns with the lead agent. The delegation list is the members plus the lead's own
`subagents`, without duplicates. The system shall enable `task` and `task_continue` for the lead of a team session,
even when they are not in the lead's `tools`.

- `AC-TEAM-008`: Given team `research-squad` whose lead has `subagents = ["explorer"]`, when a turn starts, then the enabled tools include `task` and `task_continue`, and `task` accepts exactly `researcher`, `writer`, and `explorer`.
- `AC-TEAM-009`: Given the same team, when a turn starts, then the system prompt of the turn has a roster section that lists each allowed agent with its `description` (or no description when none is set), and the roster is written to the log as part of `system/message`.

#### REQ-TEAM-005: Plain agents with subagents

The system shall give a non-team agent the same `task` and `task_continue` behavior when the agent lists them in `tools`
and has a non-empty `subagents`. The delegation list is that `subagents` list. The roster section is added whenever the
delegation list is not empty.

- `AC-TEAM-010`: Given agent `coder` with `tools = ["task", "read_file"]` and `subagents = ["explorer"]`, when `coder` calls `task(agent = "explorer", …)`, then the child runs as in `REQ-TEAM-006`.
- `AC-TEAM-011`: Given agent `coder` with `tools` containing `task` and no `subagents`, when a turn starts, then `task` returns an error result `no subagents are configured for this agent` for each call and the turn continues.

### Delegation tools

#### REQ-TEAM-006: `task` creates a child session

The system shall handle `task(agent, prompt, title)` by creating a child session for `agent`, running one turn in it with
`prompt` as its only user message, waiting for the turn to end, and returning the result (`API-TEAM-002`). The child shall
not inherit the parent's history. A call that reaches a child answer has tool status `success`, whatever the child's turn
stop reason was; the child's outcome is in the output JSON. A call that is refused, or interrupted by cancel or an internal error, has tool status `error`.

- `AC-TEAM-012`: Given the lead calls `task(agent = "researcher", prompt = "List the notes", title = "Notes")`, when the call runs, then one child session exists with `parent_session_id` set to the lead's session, `parent_call_id` set to the call id, `agent = "researcher"`, the same channel and address as the root session, and the same workspace; its log has `session/created`, `system/message`, `user/message` with the prompt, and no earlier message of the parent.
- `AC-TEAM-013`: Given the child's turn ends with `end_turn`, when the `task` call returns, then the `tool/result` of the parent has status `success` and an output that is the JSON object in `API-TEAM-002`, with `answer` equal to the child's last assistant text.
- `AC-TEAM-014`: Given the model calls `task` with an agent that is not in the delegation list, when the call runs, then no child session is created and the result is status `error` with the message `agent 'x' is not a subagent of this agent` (the list of allowed names is in the message).

#### REQ-TEAM-007: `task_continue` continues a child

The system shall handle `task_continue(session_id, prompt)` by sending `prompt` to an existing child session of the current
session, running one turn there, and returning the same result shape as `task`.

- `AC-TEAM-015`: Given a child `C` created by an earlier `task` in the same parent, when `task_continue(session_id = C, prompt = "Also check X")` runs, then the child keeps its earlier history, a new turn is appended to `C`'s log, and the result has `session_id = C`.
- `AC-TEAM-016`: Given a session `X` that is not a child of the current session, when `task_continue(session_id = X, …)` runs, then the result is status `error` with the message `session X is not a subagent of this session` and `X` is unchanged.
- `AC-TEAM-017`: Given a child whose turn is running, when `task_continue` names that child, then the result is status `error` with the message `subagent is busy` and no second turn starts.

### Limits

#### REQ-TEAM-008: Depth, concurrency, and step limits

The system shall enforce these limits for delegation:

- A session may create children only while its depth is less than 2. A root session has depth 0; its children have depth 1;
  their children have depth 2 and cannot create children.
- At most 3 children of one parent may run at the same time. A `task` or `task_continue` that would exceed this is refused at once, without waiting.
- A child's turn stops after the child agent's `max_steps` when that is set, and after 15 steps when it is not (the
  turn stop reason is `max_steps`).

- `AC-TEAM-018`: Given a chain root → child → grandchild, when the grandchild calls `task`, then the result is status `error` with the message `maximum subagent depth reached` and no child is created; the child's `task` call in the same chain succeeds.
- `AC-TEAM-019`: Given a parent with three running children, when a fourth `task` call runs, then it returns status `error` with the message `3 subagents are already running` within the time of one tool call, and the three running children are not affected.
- `AC-TEAM-020`: Given a child agent with no `max_steps` whose model keeps calling tools, when the child runs, then its turn ends with `max_steps` after 15 steps; given `max_steps = 5` on the child agent, it ends after 5 steps.

### Permissions

#### REQ-TEAM-009: Child permission is the intersection

The system shall compute the effective policy of a child tool call as the stricter of the child's own policy and the
policy of its parent session at that moment. The order from strict to permissive is `never`, `ask`, `trust`. The
child's `deny` rules and its parent's `deny` rules shall both apply. The child shall not use its parent's `allow`
rules, its parent's session grants, or the parent's approval outcomes. The child's workspace is the parent session's
workspace.

- `AC-TEAM-021`: Given a parent with policy `trust` and a child agent with `permission = "ask"`, when the child calls `bash`, then an approval is requested for the child. Given a parent with `ask` and a child with `trust`, the child also asks.
- `AC-TEAM-022`: Given a parent agent with `rules.deny = ["bash(rm -rf*)"]` and a child calling `bash rm -rf build` under trust, then the child's call is rejected with `deny rule`. Given the parent's `rules.allow` lists `bash(go test*)` and the child is on policy `ask`, then `bash go test ./...` in the child is still an approval request.
- `AC-TEAM-023`: Given a parent that the owner changes to `never` while a child runs, when the child's next `Exec` call runs, then it is rejected with `policy never`.
- `AC-TEAM-024`: Given the owner's `allowed_session` grant for `bash` in the parent session, when the child calls `bash`, then an approval is requested for the child (no carry-over).

### Approvals and cancel

#### REQ-TEAM-010: Approvals of a child

The system shall send each approval request of a child session to the channel of the root session. The request shall
carry the child's agent name as its label, and the approval audit events shall be written to the child session's log.

- `AC-TEAM-025`: Given a child approval, when it is requested, then `approval/asked` is written to the child log with `label = "researcher"`, and the web view of the root session shows the approval with the agent name (`API-TEAM-004`).
- `AC-TEAM-026`: Given the owner answers the child's approval in the web view, then the child's tool call runs or is rejected, and the answer appears in the child log only.

#### REQ-TEAM-011: Cancel cascade

The system shall cancel every running child when its parent turn is cancelled. A cancel of a child session alone shall
stop only that child. A child's cancel shall not change the parent turn.

- `AC-TEAM-027`: Given a parent turn that waits for two children, when the parent is cancelled, then both children end with `turn/end reason = "cancelled"`, the `task` calls end with status `error` and the output `cancelled`, the parent `turn/end` has `reason = "cancelled"`, and no child goroutine remains after the parent turn ends (checked by a leak test).
- `AC-TEAM-028`: Given the same two children, when only child `A` is cancelled, then `A` ends `cancelled`, `B` continues, and the parent's `task` call for `A` returns status `error` with output `cancelled` while the parent turn continues to its next step.

### Parallel delegation and usage

#### REQ-TEAM-012: `task` calls run in parallel

The system shall run the `task` and `task_continue` calls of one step in parallel, up to the concurrency limit of
`REQ-TEAM-008`. Approval decisions of the step remain sequential. This is the exception to the sequential `Exec` rule
recorded in ARCHITECTURE §8.

- `AC-TEAM-029`: Given one step with two `task` calls whose children each wait for 1 second, when the step runs, then both children are running at the same time (the second child starts before the first finishes) and the step takes less than 2 seconds.
- `AC-TEAM-030`: Given one step with two `task` calls on policy `ask`, when the step runs, then the two approvals are asked one after the other and the children start after their own approval.

#### REQ-TEAM-013: Usage of children

The system shall add the token usage of the child turns of a `task` or `task_continue` call to the usage of the parent's
`step/end` event in which the call ran. The child's own `step/end` events keep their own usage.

- `AC-TEAM-031`: Given a parent step whose own model call used 100 input and 20 output tokens and one child used 50 input and 10 output tokens, when the step ends, then the parent `step/end` usage is 150 input and 30 output tokens, and the child's `step/end` usage is 50 and 10.

### Visibility and events

#### REQ-TEAM-014: Child sessions in lists

The system shall leave child sessions out of the default session list, and shall keep them readable by id with their
events, with `parent_session_id` in the session detail.

- `AC-TEAM-032`: Given a root session with two children, when `GET /v1/sessions` runs, then it returns only root sessions; when `GET /v1/sessions/{child}` runs, then it returns the child with `parent_session_id`.

#### REQ-TEAM-015: Child events and frames on the parent stream

The system shall send, on the web stream of a root session, the events and live frames of its running children. Each such
item shall carry `child_session_id` and `agent`. Items of the root session shall keep their current shape. The web
stream shall not send a child's `turn/end` as a root `turn` event, so the root turn state does not change.

- `AC-TEAM-033`: Given a running child, when the root session's stream is open, then child `tool_call` and `tool_result` frames arrive with `child_session_id` and `agent`, and root frames arrive without these fields.
- `AC-TEAM-034`: Given a child `turn/end`, when it is written, then the root stream sends an `event` item tagged with `child_session_id` and no `turn` item, and the root's running state stays `true` until the root `turn/end`.
- `AC-TEAM-035`: Given the connection drops and reconnects, when the stream replays, then the root's events resume from the last root `seq`, the children's events are sent again from `seq` 1 for the root's current turn, and the dashboard drops duplicates by the pair (`child_session_id`, `seq`).

### Team information in the web API

#### REQ-TEAM-016: Team information in sessions

The system shall add a `team` object to each session that is a team session (`API-TEAM-005`), and shall accept team names in
the sessions list `agent` filter. The facet counts of the sessions list shall include teams as agent options.

- `AC-TEAM-036`: Given a session with agent `research-squad`, when `GET /v1/sessions/{id}` runs, then the body has `team = {name, lead, members}`; for a plain agent `team` is `null`.
- `AC-TEAM-037`: Given sessions for agent `research-squad` and `coder`, when `GET /v1/sessions?agent=research-squad` runs, then only the team's sessions are returned, and the `facets.agent` list contains both names.

#### REQ-TEAM-017: Teams in the agents list

The system shall include teams in `GET /v1/agents` with `kind = "team"`, and agents with `kind = "agent"` (`API-TEAM-006`).

- `AC-TEAM-038`: Given agents `default` and `coder` and team `research-squad`, when `GET /v1/agents` runs, then three rows are returned, the team row has `kind = "team"`, `lead`, `members`, `model = null`, and `policy` equal to the lead's policy, and the agent rows have `kind = "agent"`.

#### REQ-TEAM-018: Team endpoints

The system shall create, read, replace, and delete a team through `/v1/teams` (`API-TEAM-007` to `API-TEAM-010`). A write
shall follow the rules of spec #9 rev 2 for config writes: `expected_version`, `config.Check` before the atomic write,
an audit line that names changed keys without values, and `restart_required = true`. Only the `[teams.<name>]` table
changes; every other byte of `config.toml` is kept.

- `AC-TEAM-039`: Given a `config.toml` with comments before and after `[teams.research-squad]`, when the owner replaces the team, then only the lines of that table change, the comments are unchanged, and the response has `restart_required = true`.
- `AC-TEAM-040`: Given a stale `expected_version`, when `PUT /v1/teams/{name}` runs, then the response is `409 stale_version` and the file is unchanged.
- `AC-TEAM-041`: Given a candidate that fails `config.Check` (for example a member that does not exist), when the owner saves, then the response is `422 validation_failed` with the issue list and nothing is written.
- `AC-TEAM-042`: Given a route that names the team, when the owner deletes it, then the candidate fails `config.Check` and the response is `422 validation_failed` naming the route; nothing is written.

### Dashboard: team message block

#### REQ-TEAM-019: The team message block

The system shall render each assistant turn of a team session as one team message block, following the mockup
`team-message-states`. The block shows the agent avatars, the team name, a Team badge, the number of agents, and the
turn time. A single-agent session renders the same block with one avatar, no Team badge, and no work panel.

- `AC-TEAM-043`: Given a team turn, when it is shown, then the block has up to 3 avatars (the lead first), the team name, a `Team` badge, `N agents` where N is the roster size (the lead plus the members), and the time of the turn.
- `AC-TEAM-044`: Given a single-agent turn, when it is shown, then the block has one avatar, no `Team` badge, and no work panel.

#### REQ-TEAM-020: The Team work panel

The system shall show a Team work panel under the block's header. While the turn runs, the panel is expanded and its badge
reads `Working · step N`, where N is the number of delegation steps so far. After the turn ends, the panel is collapsed
and its badge reads `Done · N steps · S s` (or `Stopped`, `Failed`, or `Cancelled` with the step count). The panel has
a toggle `Show work ▾` / `Hide work ▴` (a button with `aria-expanded`).

- `AC-TEAM-045`: Given a team turn that is running, with one completed delegation (two steps) and a second delegation just sent to a member, when it is shown, then the panel is expanded, the badge is `Working · step 3` (the outgoing step of the second delegation is step 3), and the toggle reads `Hide work ▴`.
- `AC-TEAM-046`: Given the same turn after it ends with 2 delegations and 38 seconds, when the page is shown, then the panel is collapsed, the badge is `Done · 4 steps · 38 s`, and the toggle reads `Show work ▾`.
- `AC-TEAM-047`: Given the owner opens the collapsed panel, when it expands, then the steps of `API-TEAM-004` appear in order: lead to member with the task prompt, member's tool rows, member to lead with the result, and so on, followed by the lead's final answer. A reload of the page rebuilds the same content from the root log alone for the collapsed badge, and from the child logs for the step list.

#### REQ-TEAM-021: Work panel rows

The system shall render each delegation step as a row with the agent avatar, `from → to` (for example `lead → researcher`),
and its text: the task prompt for an outgoing step, and the child's answer for a return step. Child tool calls appear under
the member's row as tool rows with the tool name, the primary argument cut to 60 characters, and a status badge (`Running`,
`Done`, `Error`, or `Cancelled`). The lead's final answer appears last with the label `lead · answer`.

- `AC-TEAM-048`: Given a member that ran `glob` and `read_file`, when the step list is shown, then the member's row has two tool rows in call order, each with its status badge, and the arguments are cut to 60 characters with `…`.
- `AC-TEAM-049`: Given a return step whose answer is longer than 400 characters, when it is shown, then the text is cut to 400 characters with `…` and the full answer is available in the child's session.

#### REQ-TEAM-022: Team choice in the composer and in the pickers

The system shall show `per agent` in the model picker of a team session, disabled, with the explanation `Each agent in the team uses its own model.`.
The policy picker stays enabled. The composer placeholder reads `Message <team>…`. The menu `Switch agent or team` lists
teams in the same list as agents. The new-session agent picker shows teams in the RECENT and AGENTS sections with the
line `Team · N agents` and up to 3 stacked avatars.

- `AC-TEAM-050`: Given a team session, when the composer is shown, then the model control reads `per agent`, is disabled, and the policy control still changes the policy (`approval/policy`).
- `AC-TEAM-051`: Given the new-session picker with one team and two agents, when the owner types `research`, then the team row shows `Team · 3 agents` and the stacked avatars, and choosing it starts a team session.

#### REQ-TEAM-023: Teams in the lists and the skills dialog

The system shall show each team as a row in the agents list. The row has the stacked avatars, the team name, the `Team`
badge, the description line `Team · lead, researcher, writer`, `per agent` in the model column, the lead's permission
badge, `–` in the tools column, and the session count and last activity. The sessions filter's agent facet lists teams.
In the skills editor's `Add agent` dialog, a team choice adds the skill to every member of the team, and its helper
text reads `Adds it to all N agents in the team`.

- `AC-TEAM-052`: Given the agents list with a team, when it is shown, then the team row has the columns and values above, and the row count line reads `N agents · M team` (or `· M teams`).
- `AC-TEAM-053`: Given the skills dialog with a team of three agents, when the owner adds the team, then the skill is added to the `skills` list of each member (no duplicates) and to no other agent.

### Channels

#### REQ-TEAM-024: Telegram and CLI

The system shall accept `/agent <team>` in Telegram and in `lana chat`. A team chat shall send the lead's final answer as
the reply. A child's tool calls shall appear as tool lines in the same way as the lead's, prefixed with the child agent
name. An approval of a child shall show the child's name in its approval text.

- `AC-TEAM-054`: Given a Telegram chat on `/agent research-squad`, when the owner sends a task, then the bot replies with the lead's final answer, and a child's approval message contains the child agent name.

### Compatibility

#### REQ-TEAM-025: Existing behavior

The system shall keep the behavior of agents without teams and without delegation. Existing config files without
`[teams]` shall load and behave as before.

- `AC-TEAM-055`: Given a config file with no `[teams]` table and no `task` in any `tools` list, when the daemon starts and a session runs, then the behavior and the event log are the same as before this spec (the existing test suite passes unchanged).

## Contract commitments

| Requirement | Observable commitment | Contract operation |
|---|---|---|
| `REQ-TEAM-001`, `REQ-TEAM-002` | Team keys, names, and validation messages | `API-TEAM-001` |
| `REQ-TEAM-004`, `REQ-TEAM-005`, `REQ-TEAM-006` | `task` input, output JSON, and error messages | `API-TEAM-002` |
| `REQ-TEAM-007` | `task_continue` input and error messages | `API-TEAM-003` |
| `REQ-TEAM-010`, `REQ-TEAM-013`, `REQ-TEAM-015` | Event fields and SSE item fields | `API-TEAM-004`, `API-TEAM-011` |
| `REQ-TEAM-016`, `REQ-TEAM-014` | `team` in session list and detail, child exclusion, agent filter | `API-TEAM-005` |
| `REQ-TEAM-017` | `kind` and team rows in `GET /v1/agents` | `API-TEAM-006` |
| `REQ-TEAM-018` | Team create, read, replace, delete | `API-TEAM-007`, `API-TEAM-008`, `API-TEAM-009`, `API-TEAM-010` |

## State and operation matrix

| Session | Operation | Result |
|---|---|---|
| Root, agent | `task` | Child created, run, result returned |
| Root, team | `task` by the lead | Child created for the named member |
| Child, depth 1 | `task` | Grandchild created (depth 2) |
| Child, depth 2 | `task` | `maximum subagent depth reached` |
| Child, running | `task_continue` naming it | `subagent is busy` |
| Child, idle | `task_continue` | New turn, same session |
| Root, turn running | Cancel | Children cancelled, parent `cancelled` |
| Child, turn running | Cancel of the child only | Child `cancelled`, parent continues |
| Team session | `PATCH /v1/sessions/{id}` with `model` | Refused: `invalid_request` (teams use each agent's model) |

## Failure cases

- Lead agent missing or invalid at start: the daemon refuses to start, as for any invalid config (`config.Check`).
- Provider error in a child: the child's `turn/end` has `reason = "error"`; the `task` result is `status = success` with
  the child's `status = "error"` and the message, so the lead can react. The lead's turn is not stopped.
- Child store write fails: the `task` result is `status = error` with `internal error`; the child turn is cancelled.
- Child approval timeout (10 minutes): `unavailable` for the child call, as ADR 0004 says.
- The root session's lease is held by another process: the team runs in the process that holds it, as for every
  session (spec #9 `REQ-WEB-012`). Children are not separately leased.

## Authorization and security

- Team and child sessions are owned by the root session's owner (one owner, spec #9 Actors).
- A child cannot read or change its parent's config, approvals, or policy by itself. Only the owner can change a policy.
- `task` names an agent from the delegation list only. The model cannot name any other agent.
- Team config writes need the owner's cookie and the CSRF header (spec #9 `API-WEB-002`).
- No secret value appears in team responses, events, or the audit line.

## Data integrity and concurrency

- Each child session has its own log and its own lease. A child's events are written in the same way as a root's.
- The concurrency limit (3 children per parent) is checked and counted under the registry lock.
- Team config writes use the same write lock and `expected_version` as all other config writes.

## Accessibility

- The Team work toggle is a button with `aria-expanded` and an accessible name that includes the team name.
- A live region announces only the turn end (`Team finished in 38 seconds.`) and a refused delegation, not every step.
- Status badges have text; colour is never the only cue.

## Scenarios

1. **Owner runs a team.** The owner declares `research-squad` (lead, two members), starts a session with it, and sends
   the task. The Team work panel shows `Working · step 1`, then each delegation. At the end it collapses to
   `Done · 4 steps · 38 s`, and the final answer shows below.
2. **Member asks for approval.** A member wants `bash go test ./...`. Under `ask`, the approval card names the member
   and the owner answers in the root chat. Only that call waits.
3. **Owner cancels.** The owner presses stop during a team turn. Both running members end `cancelled`, and the block
   shows `Cancelled · 2 steps`.
4. **Owner removes a member.** The owner edits the team. The change needs a restart, and the team turn after the restart uses the new list.

## Blocking Open Questions

| ID | Question | Controls | Requested reference | Agent recommendation | Owner | Status | Resolution |
|---|---|---|---|---|---|---|---|
| `Q-001` | Does `lead` appear in `members`? | `REQ-TEAM-001`, `REQ-TEAM-002` | Brief: `lead` and `members` are separate keys; mockup shows `lead, researcher, writer` | No. The lead is not listed in `members`. Listing it is an error. The roster shows the lead too | Ridho | Answered | Rekomendasi agent: lead is separate; listing it in `members` fails validation (`API-TEAM-001`). Accepted with the owner's lifted gate (2026-10-30) |
| `Q-002` | Can a team contain a team, or lead a team? | `REQ-TEAM-001`, `REQ-TEAM-002`, `AC-TEAM-003` | ADR 0017 | No nesting in v1. A member or lead that is a team is a validation error | Ridho | Answered | Rekomendasi agent: no nesting (ADR 0017). Accepted with the owner's lifted gate (2026-10-30) |
| `Q-003` | What is the maximum depth, and what does "depth 2" count? | `REQ-TEAM-008`, `AC-TEAM-018` | ADR 0011 "maximum depth 2" | Depth = number of parent links. A session may create children while its depth is below 2, so the chain root → child → grandchild is the longest | Ridho | Answered | Rekomendasi agent: the chain root → child → grandchild is allowed; a grandchild cannot delegate. Accepted with the owner's lifted gate (2026-10-30) |
| `Q-004` | What happens to the 4th concurrent child? | `REQ-TEAM-008`, `AC-TEAM-019` | ADR 0011 "at most 3 children" | Refuse at once with an error result. The model can retry. No queue, so no hidden wait | Ridho | Answered | Rekomendasi agent: refuse at once with status `error`. Accepted with the owner's lifted gate (2026-10-30) |
| `Q-005` | Is `task` an `Exec` call that needs approval? | `REQ-TEAM-010`, `AC-TEAM-025` | ARCHITECTURE §9 lists `task` under `Exec`; under `never` a team could not delegate | Keep `Exec` as ARCHITECTURE §9 says. Each delegation is one approval under `ask`; `task(<agent>)` in `rules.allow` skips it. Under `never` delegation is refused, and the child's own tools stay restricted | Ridho | Answered | Rekomendasi agent: keep `Exec` (ARCHITECTURE §9). Accepted with the owner's lifted gate (2026-10-30) |
| `Q-006` | Do parallel `task` calls run in parallel when `Exec` runs sequentially? | `REQ-TEAM-012`, `AC-TEAM-029` | ADR 0011 "children run in parallel"; `agent/toolrunner.go` runs `Exec` calls one by one | Yes. `task` and `task_continue` run as a parallel batch; approvals stay sequential. ARCHITECTURE §8 records this one exception | Ridho | Answered | Rekomendasi agent: parallel batch for `task` and `task_continue` only. Accepted with the owner's lifted gate (2026-10-30) |
| `Q-007` | Is child usage added to the parent's step, or shown separately? | `REQ-TEAM-013`, `AC-TEAM-031` | ADR 0011 "added to the parent's step for cost display" | Add it to the parent step's usage, as ADR 0011 says, so no new event kind is needed. The child's own `step/end` keeps its own usage | Ridho | Answered | Rekomendasi agent: add to the parent `step/end` usage (ADR 0011 as written). Accepted with the owner's lifted gate (2026-10-30) |
| `Q-008` | Which permissions does a child use? | `REQ-TEAM-009`, `AC-TEAM-021` to `AC-TEAM-024` | ADR 0011 "intersection"; ADR 0004 | Stricter of the child's policy and the parent's live policy. Both `deny` lists apply. The parent's `allow` rules and grants do not carry over. Workspace = the parent's | Ridho | Answered | Rekomendasi agent: as stated in `REQ-TEAM-009`. Accepted with the owner's lifted gate (2026-10-30) |
| `Q-009` | Which team settings does the lead's model picker show, and can a team change its model? | `REQ-TEAM-022`, `AC-TEAM-050` | Brief: composer shows "per agent" for teams | No model change for a team session. The control shows `per agent` and is disabled. Policy stays available | Ridho | Answered | Rekomendasi agent: as stated in `REQ-TEAM-022`. Accepted with the owner's lifted gate (2026-10-30) |
| `Q-010` | Which permission does the team row show? | `REQ-TEAM-023`, `AC-TEAM-052` | Mockup shows `Ask` on the team row | The lead's permission, because the lead runs the turns | Ridho | Answered | Rekomendasi agent: the lead's permission (`API-TEAM-006` `policy`). Accepted with the owner's lifted gate (2026-10-30) |
| `Q-011` | How many children are shown in the Team work panel after a page reload, and where do older child logs come from? | `REQ-TEAM-020`, `AC-TEAM-047` | Mockup shows collapsed state for older turns | The collapsed badge comes from the root log. The step list of an older turn loads the child logs on expand with `GET /v1/sessions/{child}/events`. The root stream replays children only for the current turn | Ridho | Answered | Rekomendasi agent: as stated in `REQ-TEAM-020` and `AC-TEAM-035`. Accepted with the owner's lifted gate (2026-10-30) |
| `Q-012` | How does the mockup's expanded finished turn (`sessions-team-chat`) relate to "collapsed after the turn ends"? | `REQ-TEAM-020`, `AC-TEAM-046` | `team-message-states` says collapsed after the turn ends; `sessions-team-chat` shows the panel open for a finished turn | The `team-message-states` rule wins. The chat screen is a snapshot after the owner opened the panel | Ridho | Answered | Rekomendasi agent: collapsed after the turn ends (`team-message-states`). Accepted with the owner's lifted gate (2026-10-30) |
| `Q-013` | Can the owner create or edit a team in the dashboard? The mockup has no team form or team detail page. | `REQ-TEAM-018`, `REQ-TEAM-023` | Brief: "team CRUD endpoints"; the mockup has none for teams | v1: the endpoints exist (`API-TEAM-007` to `API-TEAM-010`). The dashboard edits teams only through the config file editor. A click on a team row opens Settings › Config file at its table. A team form needs its own mockup and is a follow-up | Ridho | Answered | Rekomendasi agent: endpoints now; team form later with a mockup. Accepted with the owner's lifted gate (2026-10-30). The owner should confirm that the mockup gap is acceptable |
| `Q-014` | Is a lead-agent's delegation list limited to 8 members? | `REQ-TEAM-001`, `AC-TEAM-003` | Prompt size | Max 8 members per team. Mockup shows 2 members | Ridho | Answered | Rekomendasi agent: max 8 members per team. Accepted with the owner's lifted gate (2026-10-30) |
| `Q-015` | Do child tool lines appear in Telegram and in the CLI? | `REQ-TEAM-024`, `AC-TEAM-054` | Brief: "child tool lines as usual" | Yes, with the child's name as prefix. Telegram sends the lead's answer as the reply and the child's tool lines as the same kind of line the lead uses | Ridho | Answered | Rekomendasi agent: as stated in `REQ-TEAM-024`. Accepted with the owner's lifted gate (2026-10-30) |
| `Q-016` | Is `ask_user` available to children? | Scope | Spec #11 (questions in root sessions) | No in v1. Spec #11 decides questions for root sessions; a child that calls `ask_user` gets `unavailable` | Ridho | Answered | Rekomendasi agent: not available to children in v1. Accepted with the owner's lifted gate (2026-10-30) |

The agent must ask every Open question and wait for the answers before approval or design. The owner lifted the
approval gate on 2026-10-30; every question above was answered with the agent recommendation.

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-TEAM-001` | `AC-TEAM-001`, `AC-TEAM-002` | Brief, ADR 0017, `Q-001` |
| `REQ-TEAM-002` | `AC-TEAM-003`, `AC-TEAM-004`, `AC-TEAM-005` | ADR 0017, `Q-002`, `Q-014` |
| `REQ-TEAM-003` | `AC-TEAM-006`, `AC-TEAM-007` | Brief (team as session agent), ADR 0003 |
| `REQ-TEAM-004` | `AC-TEAM-008`, `AC-TEAM-009` | Brief, ADR 0017 |
| `REQ-TEAM-005` | `AC-TEAM-010`, `AC-TEAM-011` | ADR 0011 |
| `REQ-TEAM-006` | `AC-TEAM-012` to `AC-TEAM-014` | ADR 0011 |
| `REQ-TEAM-007` | `AC-TEAM-015` to `AC-TEAM-017` | ADR 0011 |
| `REQ-TEAM-008` | `AC-TEAM-018` to `AC-TEAM-020` | ADR 0011, `Q-003`, `Q-004` |
| `REQ-TEAM-009` | `AC-TEAM-021` to `AC-TEAM-024` | ADR 0004, ADR 0011, `Q-008` |
| `REQ-TEAM-010` | `AC-TEAM-025`, `AC-TEAM-026` | ADR 0004, ADR 0011, `Q-005` |
| `REQ-TEAM-011` | `AC-TEAM-027`, `AC-TEAM-028` | ADR 0011 |
| `REQ-TEAM-012` | `AC-TEAM-029`, `AC-TEAM-030` | ADR 0011, `Q-006` |
| `REQ-TEAM-013` | `AC-TEAM-031` | ADR 0011, `Q-007` |
| `REQ-TEAM-014` | `AC-TEAM-032` | ADR 0011, ADR 0009 |
| `REQ-TEAM-015` | `AC-TEAM-033` to `AC-TEAM-035` | ADR 0009, spec #9 `REQ-WEB-012`, `Q-011` |
| `REQ-TEAM-016` | `AC-TEAM-036`, `AC-TEAM-037` | Spec #9 decision 6 (facets), `API-TEAM-005` |
| `REQ-TEAM-017` | `AC-TEAM-038` | Spec #9 decision 8, `Q-010` |
| `REQ-TEAM-018` | `AC-TEAM-039` to `AC-TEAM-042` | Spec #9 decision 3, `API-WEB-014`, `Q-013` |
| `REQ-TEAM-019` | `AC-TEAM-043`, `AC-TEAM-044` | Mockup `team-message-states`, spec #9 decision 7 |
| `REQ-TEAM-020` | `AC-TEAM-045`, `AC-TEAM-046`, `AC-TEAM-047` | Mockup `team-message-states`, `Q-011`, `Q-012` |
| `REQ-TEAM-021` | `AC-TEAM-048`, `AC-TEAM-049` | Mockup `sessions-team-chat` |
| `REQ-TEAM-022` | `AC-TEAM-050`, `AC-TEAM-051` | Mockup `sessions-team-chat`, `sessions-new-session-agent-picker`, `Q-009` |
| `REQ-TEAM-023` | `AC-TEAM-052`, `AC-TEAM-053` | Mockup `agents-list`, `skills-editor-add-agents`, spec #9 decision 10, `Q-010`, `Q-013` |
| `REQ-TEAM-024` | `AC-TEAM-054` | Brief, spec #6 `/agent`, `Q-015` |
| `REQ-TEAM-025` | `AC-TEAM-055` | Compatibility |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit or stated as not applicable.
- [x] State transitions and destructive effects are explicit or not applicable.
- [x] Defaults, ordering, retry, and idempotency are explicit where applicable.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Contract applicability is explicit.
- [x] Blocking Open Questions is `None`.
- **Approved by**: Ridho, on behalf of the owner (lifted gate, 2026-10-30; recommendations recorded as `Rekomendasi agent:`)
- **Approved on**: 2026-10-30
