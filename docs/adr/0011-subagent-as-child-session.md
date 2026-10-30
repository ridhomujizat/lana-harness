# 0011. Run subagents as child sessions

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0003](0003-multi-agent-with-channel-routing.md), [0004](0004-permission-mode-per-agent.md), [0009](0009-session-as-append-only-event-log.md), [0017](0017-agent-teams-as-roster-over-subagents.md)

## Context

Large tasks (researching several sources, exploring a codebase, reviewing) fill the context window if they are
done in a single session. `dsh` provides a **subagent provider** behind one interface:
a new child agent, or a delegated turn, and a subagent can be continued. The owner asked for
subagents to be part of v1.

## Decision

A subagent is a regular **child session** run by the same agent loop.

Tools for the model:

| Tool | Function |
|---|---|
| `task(agent, prompt, title)` | Create a child session from agent `agent`, run one turn, return the final answer as a `tool_result` |
| `task_continue(session_id, prompt)` | Send a follow-up message to an existing child session |

Rules:

- A child session is created with `session/created { parent_session_id, parent_call_id }`. The child
  does **not** inherit the parent's history; its context comes only from `prompt`. Fork (inheriting history) is a separate feature.
- `agent` must be registered in the parent agent's `[agents.<name>.subagents]`. Default: only the agent itself.
- The child's permission = the **intersection** of the parent's and the child agent's configuration. A child never has
  more permission than its parent. If the parent is `trust` and the child is `ask`, the child stays `ask`.
- Approvals from a child are sent through the parent's channel, labeled with the child's name.
- Limits: maximum depth 2, at most 3 children running concurrently per parent session, and the child's
  `max_steps` defaults to 15.
- Children run in parallel if the model calls several `task`s in one step.
- Cancelling the parent turn cancels all running children (`turn/end { reason: "cancelled" }`).
- The child's token usage is added to the parent's step for cost display.

## Consequences

**Positive**
- The parent's context stays clean. Only the child's summary comes back.
- No new mechanism: children use the same event log, permissions, and agent loop.
- Child sessions can be opened and replayed in the dashboard like any other session.

**Negative / tradeoffs**
- Parallel children increase token usage and hit provider rate limits.
- Approvals from several children at the same time can be confusing in chat.

**Neutral**
- Coordination of an agent team (roster over child sessions) is decided in [ADR 0017](0017-agent-teams-as-roster-over-subagents.md). A shared task board and peer messaging are not part of v1.

## Alternatives considered

- **Subagent as a separate process** — strong isolation. Rejected because it is heavy.
- **Child inherits the parent's full history** — full context, but removes the main benefit of a subagent. Rejected.
