# 0017. Model agent teams as a roster of agents over subagent child sessions

- **Status**: Accepted
- **Date**: 2026-10-30
- **Deciders**: Ridho
- **Related ADRs**: [0003](0003-multi-agent-with-channel-routing.md), [0004](0004-permission-mode-per-agent.md), [0009](0009-session-as-append-only-event-log.md), [0011](0011-subagent-as-child-session.md)

## Context

ADR 0011 gives an agent the `task` and `task_continue` tools. A parent agent delegates work to a child session
that runs another configured agent. ADR 0011 leaves team coordination out of v1.

The owner's dashboard mockup shows agent teams as a first-class thing. A team such as `research-squad` has a lead and
members, can be picked wherever an agent can (sessions, routes, the Telegram and CLI `/agent` command), and shows
its delegation work in the chat as a "Team work" panel. The mockup does not show a shared board, peer messages, or
an agent-to-agent chat.

Forces:

- Teams must reuse the session model. Each member's work must be a normal session with its own log, so replay,
  cancel, approvals, and the dashboard work the same way (ADR 0009, ADR 0011).
- Permissions must not widen through delegation (ADR 0004, ADR 0011).
- Config is data (ADR 0010). Teams must be declared in `config.toml`, like agents.
- A team and an agent appear in the same pickers and routes. Their names must not collide.
- `dsh` Agent Teams uses a shared roster and task board. That needs peer messaging and a board that the log model
  does not have yet.

## Decision

A **team** is a named roster in `config.toml`: `[teams.<name>]` with a `lead` agent, `members` agents, and an optional
`description`. Team names and agent names share one namespace.

A session whose agent is a team runs the **lead** agent. The lead's delegation list is the members plus the lead's own
`subagents`. The lead's system prompt gets a roster section. Delegation uses only ADR 0011 child sessions through
`task` and `task_continue`. A team adds no new session kind, event kind, or tool.

Rules that the team layer adds on top of ADR 0011:

- Teams do not nest. A member cannot be a team, and a team cannot lead a team.
- The lead is not listed in `members`.
- The team's permission is the lead's permission and rules. Members run under the intersection rule of ADR 0011.
- Team sessions do not offer a model choice. Each agent runs with its own provider and model.
- No peer messages and no shared task board. Work moves only from a parent to its child and back.

## Consequences

**Positive**
- A team needs no new storage, no new event kind, and no new permission path. Replay, cancel, and approvals work
  as they do for subagents.
- One concept for the pickers, routes, and `/agent`. An agent and a team are both "what answers this session".
- The dashboard's Team work panel can be rebuilt from the parent's log (the `task` calls and their results), with no
  child log needed for the collapsed view.

**Negative / tradeoffs**
- Members cannot talk to each other. Work that needs a back-and-forth between members goes through the lead.
- Each delegation is a separate session with its own context, so the lead must write a complete prompt.
- Under `permission = "ask"` each `task` call is an approval, unless the owner adds an allow rule such as
  `task(researcher)`.

**Neutral**
- A later ADR can add peer messaging or a board. It would extend this roster and does not change ADR 0011.

## Alternatives considered

- **Peer messaging between members** — flexible, but it gives every member a second input channel. That breaks
  "model-visible means logged" for the child's context (ADR 0009) and needs its own approval rules. Deferred.
- **Shared task board like `dsh` Agent Teams** — useful for long projects. It needs a board model, its own events,
  and UI. Out of v1 scope.
- **Team as a separate session kind with its own log** — duplicates ADR 0009 and the child session path for no gain.
- **Team as configuration only, with no roster in the prompt** — the lead would not know its members. Rejected.
- **Allow teams in `subagents`** — nested delegation makes the depth limit and the permission chain hard to reason
  about. Rejected; a team is a single level.
