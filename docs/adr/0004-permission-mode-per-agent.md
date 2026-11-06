# 0004. Set permissions per agent and decide approvals through hooks

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0002](0002-core-from-modules-services-and-hooks.md), [0009](0009-session-as-append-only-event-log.md), [0011](0011-subagent-as-child-session.md)

## Context

Tools such as `bash` and `write_file` can damage data. The owner wants to choose per agent:
**full trust**, or an **approval gate like Claude Code**. Approvals must be answerable from the channel
they originated in: terminal, Telegram, WhatsApp, web dashboard, or an editor via ACP.

`dsh` separates two things:

- **Policy** per session (`ask` or `never`). The effective value is the last `approval/policy` event in the
  session log, so a replay reconstructs the policy.
- **Responder chain** through the `approval/request` waterfall. The channel UI or ACP bridge acts as responder.
  The outcome is closed and *fail-closed*: anything other than `allowed-once` rejects the tool. A missing,
  failing, or invalid responder produces `unavailable`.

OS-level sandboxing is not part of v1.

## Decision

**Session policy** is `trust`, `ask`, or `never`.

- The initial value is taken from the agent's `permission` in the config.
- Changes (`/permission ask`, the `--yolo` flag) are written as `approval/policy` events.
- Cron turns and the `headless` profile use `never` unless configured otherwise.

**Per-agent rules** follow the Claude Code pattern:

```toml
[agents.coder]
permission = "ask"   # "trust" | "ask" | "never"

[agents.coder.rules]
allow = ["read_file", "glob", "grep", "bash(git status*)", "bash(cargo test*)"]
deny  = ["bash(rm -rf*)", "bash(sudo*)", "write_file(~/.ssh/**)"]
```

**Evaluation** runs in the `tools.pre_execute` listener of the `permission` module:

1. Matches `deny` → reject. This also applies under `trust`.
2. Path outside the agent's workspace → reject. Paths are canonicalized; symlinks that leave the workspace are rejected.
3. Matches `allow`, or a matching session grant exists → run.
4. Policy `trust` → run.
5. Tool risk `Read` → run.
6. Policy `never` → reject.
7. Policy `ask` → call `ctx.approval.request(...)`.

**Approval outcomes** are closed:

| Outcome | Meaning |
|---|---|
| `allowed_once` | Only this tool call |
| `allowed_session` | This tool call, and the same pattern until the session ends (equivalent to "allow always" in Claude Code). Stored as an event, not written to the config |
| `rejected` | Rejected by the user, with an optional reason for the model |
| `cancelled` | The turn was cancelled while waiting |
| `unavailable` | No responder, the responder errored, or a 10-minute timeout |

A tool only runs for `allowed_once` and `allowed_session`. All other outcomes produce a
`tool/result` error that is sent back to the model.

**Responder chain** (`approval.request`, waterfall) is filled by channel modules:

- The channel that owns the session (CLI, Telegram, WhatsApp, web) answers if the session is its own and a human is connected.
- The ACP server forwards to `session/request_permission` in the editor.
- The SDK forwards as a notification and waits for `approval.respond`.
- If no responder takes over → `unavailable`.

**Audit.** Every approval writes an `approval/asked` and `approval/decided` pair to the session log,
with a new `request_id` per request. Requests do **not** copy the tool arguments; the responder
displays the arguments from `tool/call` via `call_id`, so the display cannot drift from the original input.

## Consequences

**Positive**
- One permission concept for all channels and protocols.
- Fail-closed: a bug in a responder never opens the gate.
- Policies and grants live in the log, so resume and fork keep the same permissions.

**Negative / tradeoffs**
- Without an OS sandbox, an allowed `bash` has the full access of the user. Command patterns can be circumvented.
- Approvals through chat add latency when the user is away.

**Neutral**
- An OS sandbox can be added later as a `tools.execute` listener that wraps argv, without changing this flow.

## Alternatives considered

- **Always trust** — rejected for agents that have `bash`.
- **Permissions inside the agent loop** — rejected. Policy must be a hook so it can be replaced (see [0002](0002-core-from-modules-services-and-hooks.md)).
- **OS sandbox from v1** — deferred because the implementation differs per OS.
