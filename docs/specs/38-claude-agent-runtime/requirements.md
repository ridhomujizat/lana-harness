# Requirements: Claude Agent runtime

- **Issue**: — (owner request 2026-11-08)
- **Status**: Approved (owner: "implement 0018 … penerapannya seperti monocode"; "ya ikuti rekomendasimu" for the rest)
- **Revision**: 1
- **Owner**: ridho
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Related ADR**: [0018](../../adr/0018-claude-code-as-external-agent-runtime.md) (binding), [0004](../../adr/0004-permission-mode-per-agent.md), [0009](../../adr/0009-session-as-append-only-event-log.md), [0011](../../adr/0011-subagent-as-child-session.md), [0019](../../adr/0019-llm-router-as-embeddable-core.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request | "implement 0018-claude-code-as-external-agent-runtime.md, penerapannya seperti di monocode … di halaman routernya juga bisa melihat usage dan limit" | goal, usage and limits on the Router page |
| ADR 0018 | process, credentials, event mapping, permissions, guardrails | all rules below unless stated |
| External reference | [MonoCode](https://github.com/hardbeat920/monocode) | GUI over installed, logged-in agents; stream-json protocol |
| Local CLI | `claude` 2.1.319: `--session-id`, `--resume`, `--effort`, `--permission-prompts host`, `--permission-prompt-tool stdio`, `claude auth status` | flags |

## Goal

The owner runs a lana agent backed by their own logged-in Claude Code, from the CLI, Telegram, and the
dashboard, like any other agent. lana shows its replies, tool calls, and approvals in the same session
timeline, and the Router page shows its usage and the plan limits Claude Code reports, without lana ever
touching Claude credentials.

## Actors

- **Owner** — configures the agent, answers approvals, reads usage.
- **Claude Code** — the unmodified `claude` binary the owner installed and logged in to.

## Definitions

- **Claude Agent** — a lana agent with `runtime = "claude-code"`. UI label "Claude Agent".
- **Claude process** — one `claude -p` child process serving one lana session.
- **Plan window** — a usage window Claude Code reports in `rate_limit_event` (for example five-hour or
  weekly).

## Scope

### Included

- `runtime`, `auth`, `binary`, `allow_scheduled`, `model`, `reasoning_effort`, `workspace`, `permission`,
  `rules`, `description`, `title`, `color` on a Claude Agent.
- Process lifecycle, resume after restart, cancel, idle exit, concurrency cap of 2 with a queue.
- Event mapping, approvals through lana's permission engine, usage and cost records.
- Plan windows on the Router page, and a Claude Agent status card (binary, version, logged in).
- Delegation from lana agents through `task` (child sessions) and use from Telegram and the dashboard.
- Creating and editing a Claude Agent in the dashboard.

### Not included

- lana tools, memory, knowledge, compaction, loop guard, and `max_steps` inside the runtime (ADR 0018).
- `lana mcp-serve`, ACP runtimes, login flows, reading credentials.
- Attachments other than images (images are passed as stream-json image blocks).

## Behavior rules

### REQ-CCR-001: Configuration

A Claude Agent shall set `runtime = "claude-code"` and must not set `provider` or `fallback`. `auth` is
`subscription` (default) or `api_key` (needs `api_key_env`). `model` is passed to `--model` (empty uses
Claude Code's default). `binary` empty finds `claude` on `PATH`. `allow_scheduled` defaults to false.
Keys that do not apply (`tools`, `disabled_tools`, `mcp`, `skills`, `subagents`, `max_steps`,
compaction keys, `memory_capture`, `context_window`, `vision`) are validation errors.

#### Acceptance criteria

- `AC-CCR-001`: Given `[agents.claude] runtime = "claude-code"` and `provider = "x"`, when config is
  validated, then it fails at `agents.claude.provider` with "a claude-code agent cannot set provider".

### REQ-CCR-002: Process and session

For a turn, lana shall start (or reuse) one Claude process per lana session with `claude -p
--input-format stream-json --output-format stream-json --verbose --include-partial-messages
--permission-prompts host --permission-prompt-tool stdio`, plus `--model`, `--effort`, and
`--session-id <uuid>` on the first turn or `--resume <uuid>` when the session already has one, with
`cwd = workspace`. The UUID is stored in the session log. The process exits after 10 idle minutes and at
daemon shutdown.

#### Acceptance criteria

- `AC-CCR-002`: Given a new session, when the first turn runs, then the fake `claude` receives
  `--session-id <uuid>` and the session log holds that UUID; after a daemon restart the next turn passes
  `--resume <uuid>`.

### REQ-CCR-003: Credentials

lana shall not read, copy, or store Claude credentials, call Anthropic endpoints for this runtime, or set
identity headers. With `auth = "subscription"` the child environment drops `ANTHROPIC_API_KEY`,
`ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`, and `ANTHROPIC_BASE_URL`; with `api_key` it sets
`ANTHROPIC_API_KEY` from `api_key_env`. A not-logged-in result fails the turn with "Claude Code is not
logged in. Run `claude`, then `/login`."

#### Acceptance criteria

- `AC-CCR-003`: Given `ANTHROPIC_API_KEY` in the daemon env and `auth = "subscription"`, when a turn
  starts, then the child env has none of the four variables.

### REQ-CCR-004: Event mapping

Stream events shall map to session events as ADR 0018's table: text deltas stream live; each assistant
text becomes `assistant/message`; `tool_use` becomes `tool/call` with `external: true`; `tool_result`
becomes `tool/result`; `result` ends the turn with usage, cost, and error state. Subagent text from
Claude Code is not shown as the main reply.

#### Acceptance criteria

- `AC-CCR-004`: Given a recorded transcript with text, a `Bash` tool call and result, and a `result`
  event, when it is replayed through the runtime, then the session log holds `turn/start`, `step/start`,
  `assistant/message`, `tool/call` (external), `tool/result`, `step/end` with usage, and `turn/end`
  `end_turn`.

### REQ-CCR-005: Approvals

`can_use_tool` control requests shall go through lana's permission engine with the tool class map of ADR
0018 and the agent's `rules`. `ask` asks the owner through the existing approval flow (dashboard,
Telegram, CLI); allow answers `{"behavior":"allow","updatedInput":<input>}`, deny answers
`{"behavior":"deny","message":…}`. With no approver reachable the request is denied (fail-closed).

#### Acceptance criteria

- `AC-CCR-005`: Given `permission = "ask"` and `rules.deny = ["bash(rm *)"]`, when Claude Code asks to
  run `rm -rf x`, then lana denies without asking; when it asks to run `ls`, lana shows an approval and
  answers allow after the owner approves.

### REQ-CCR-006: Cancel

Cancelling a turn shall send an `interrupt` control request, and SIGTERM the process when it has not
ended the turn after 5 seconds. The turn ends with reason `cancelled`.

#### Acceptance criteria

- `AC-CCR-006`: Given a running turn, when the owner cancels, then the fake receives `interrupt`, and the
  turn ends `cancelled` within 6 seconds even if the fake ignores it.

### REQ-CCR-007: Guardrails

At most 2 Claude processes run at once across the daemon; more turns wait in order. Background passes
never use a Claude Agent. Calendar items and workflow nodes may target a Claude Agent only when it sets
`allow_scheduled = true`; otherwise the run fails with a clear message. lana's UI never says "Login with
Claude".

#### Acceptance criteria

- `AC-CCR-007`: Given three sessions on Claude Agents, when all start a turn, then two processes run and
  the third turn starts when one ends.
- `AC-CCR-008`: Given `allow_scheduled = false`, when a calendar item targets the agent, then the
  firing fails with "claude agent 'claude' does not allow scheduled runs".

### REQ-CCR-008: Usage, cost, and plan windows

Each Claude turn shall write one router call record (provider `claude-agent`, source `turn`, model from
`system/init`, tokens and `total_cost_usd` from `result`). Each `rate_limit_event` updates the
`claude-agent` limit snapshot (window type, status, utilization when given, reset time). The Router page
shows a Claude Agent card with binary path, version, login state (from `claude auth status`), plan
windows, and the cost Claude Code reports, labelled "reported by Claude Code; included in your plan" for
subscription auth.

#### Acceptance criteria

- `AC-CCR-009`: Given a transcript with a `rate_limit_event` for the five-hour window at 42 %, when the
  Router Overview loads, then the Claude Agent card shows "5-hour window · 42 % used · resets <time>".

### REQ-CCR-009: Dashboard

The agent create and edit form shall offer a runtime choice ("lana" or "Claude Agent"). For a Claude
Agent it shows only the keys of REQ-CCR-001, with model choices `default`, `opus`, `sonnet`, `haiku`, or a
custom ID. The agent profile shows "Claude Agent" as its engine.

#### Acceptance criteria

- `AC-CCR-010`: Given the owner creates agent `claude` with runtime Claude Agent and model `opus`, when
  saved, then `config.toml` has `runtime = "claude-code"`, `model = "opus"`, no `provider`, and the agent
  answers a chat message.

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| `claude` not found | Turn fails: "Claude Code is not installed or not on PATH" | turn error `runtime` |
| Version below 2.1.0 | Turn fails naming the version and the minimum | `runtime` |
| Process exits mid-turn | Turn fails with the last stderr line (redacted, 300 chars); next turn starts a new process with `--resume` | `runtime` |
| `result.is_error` | Turn ends with error and the result text | `runtime` |
| Unknown stream event type | Ignored and logged at debug | — |

## Authorization and security

- Approvals follow ADR 0004; fail-closed. Claude Code's own settings may auto-allow tools; lana's deny
  rules are a second line (ADR 0018).
- No credential access (REQ-CCR-003). stderr is redacted before storing.

## Blocking Open Questions

None. Resolutions:

| ID | Question | Resolution |
|---|---|---|
| `Q-001` | Minimum Claude Code version | Rekomendasi agent: 2.1.0 (stream-json control protocol and `--permission-prompts`). |
| `Q-002` | Idle timeout | Rekomendasi agent: 10 minutes. |
| `Q-003` | Where do usage and limits show? | Owner: on the Router page; usage goes into router call records. |
| `Q-004` | Model choices | Rekomendasi agent: aliases `opus`, `sonnet`, `haiku` plus custom. |

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-CCR-001` | `AC-CCR-001` | ADR 0018 |
| `REQ-CCR-002` | `AC-CCR-002` | ADR 0018, local CLI |
| `REQ-CCR-003` | `AC-CCR-003` | ADR 0018 |
| `REQ-CCR-004` | `AC-CCR-004` | ADR 0018 |
| `REQ-CCR-005` | `AC-CCR-005` | ADR 0018, 0004 |
| `REQ-CCR-006` | `AC-CCR-006` | ADR 0018 |
| `REQ-CCR-007` | `AC-CCR-007`, `AC-CCR-008` | ADR 0018 |
| `REQ-CCR-008` | `AC-CCR-009` | owner request, Q-003 |
| `REQ-CCR-009` | `AC-CCR-010` | owner request |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Blocking Open Questions is `None`.
- **Approved by**: ridho (request) + agent recommendations on the owner's instruction
- **Approved on**: 2026-11-08
