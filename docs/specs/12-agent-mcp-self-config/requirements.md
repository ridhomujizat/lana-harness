# Requirements: Agent MCP self-configuration and workspace repair

- **Issue**: —
- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (`mcp_manage` tool, risk class `admin`, config effects, workspace error text)
- **Related ADR**: [ADR Index](../../adr/README.md) — [0004](../../adr/0004-permission-mode-per-agent.md), [0006](../../adr/0006-extend-via-mcp-client-and-skills.md)
- **Related diagram**: [Diagram Index](../../diagram/README.md) — [0002](../../diagram/0002-agent-loop-and-approval.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-30 | "kenapa agentnya ga bisa tambah mcp sendiri" with a web chat transcript | goal, failure evidence |
| Session diagnosis 2026-10-30 | `~/lana/workspace` missing; `bash` failed with `fork/exec /bin/sh: no such file or directory`; reproduced with `exec.Cmd{Dir: missing, Setpgid: true}` | `REQ-WS-*` |
| Owner decision 2026-10-30 | "buat docs/spec lalu kasih kerjaan ke dsh coder" after the proposal: new agent tool, owner approval on every change even under `trust`, secrets by reference, hot apply without daemon restart, workspace fix | scope, `Q-001` through `Q-005` |
| Spec #7 | `[mcp.<name>]`, `agents.<name>.mcp`, lazy start, `mcp__<server>__<tool>` | MCP model |
| Spec #9 | `PUT /v1/mcp/{name}`, `config.Check`, `secrets.env`, `${NAME}` references, audit log | config write rules |

## Goal

An agent can connect a new MCP server for itself during a chat, with the owner's approval, and use its
tools from the next turn without a daemon restart. An agent whose configured workspace folder does not
exist yet still has a working shell and file tools, and errors about the workspace say what is wrong.

## Actors

- **Owner** — approves every MCP change an agent asks for; adds secrets in Settings → Secrets.
- **Agent** — a root-session agent that lists, adds, and removes MCP servers for itself.
- **Child agent** — an agent in a delegated (task) session. It may not change MCP servers.

## Definitions

- **Attached server** — a server named in the calling agent's effective MCP list.
- **Effective MCP list** — `agents.<agent>.mcp` from config, replaced in memory by the last list that `mcp_manage` applied for that agent in this process.
- **Secret reference** — a `${NAME}` substring in an `env` or `headers` value, resolved from the process environment and `secrets.env` by the config loader.
- **Admin risk** — the new risk class `admin`. It always needs a fresh owner approval.

## Scope

### Included

- Built-in tool `mcp_manage` with actions `list`, `add`, `remove`.
- Risk class `admin` and its permission rule.
- Writing `[mcp.<name>]` and `agents.<agent>.mcp` in `$LANA_HOME/config.toml`, validated and audited.
- Applying the change to the running MCP manager and the agent's effective MCP list.
- Creating a missing workspace folder at turn start, and a clear error when that fails.

### Not included

- Changing MCP servers of another agent.
- Writing secret values into `secrets.env` from the agent.
- OAuth for HTTP servers (unchanged from spec #7).
- Refreshing the web settings view, tool catalog, and `/mcp` output before a restart. They keep showing the loaded config, and the web shows its normal "restart required" notice.
- Claude Code `.mcp.json` files.

## Assumptions

- The approval prompt in every channel shows the tool input, so the owner sees `command`, `args`, and `url` before approving — Source: spec #4 approval display.

## Behavior rules

### REQ-AMCP-001: Offer `mcp_manage`

The system shall offer `mcp_manage` to every root-session agent, the same way it offers `skill_load`, unless the agent lists it in `disabled_tools`. Child sessions never get it.

#### Acceptance criteria

- `AC-AMCP-001`: Given agent `lana` with `tools = ["read_file"]` and no `disabled_tools`, when a root turn starts, then the offered tool list contains `mcp_manage`.
- `AC-AMCP-002`: Given `disabled_tools = ["mcp_manage"]` or a child session, when a turn starts, then `mcp_manage` is not offered.

### REQ-AMCP-002: List servers

`mcp_manage` with `action = "list"` shall return every configured server with its type, target, env and header key names, whether it is attached to the caller, and its runtime state. It never returns env or header values.

#### Acceptance criteria

- `AC-AMCP-003`: Given `[mcp.github]` with `env = { GITHUB_TOKEN = "${GITHUB_TOKEN}" }` attached to the caller, when the agent lists, then the output has a line for `github` with `attached`, the key `GITHUB_TOKEN`, and no `${GITHUB_TOKEN}` or secret value.

### REQ-AMCP-003: Add or attach a server

`mcp_manage` with `action = "add"` shall:

1. With `command` or `url`: test-connect the definition, then write `[mcp.<name>]` and attach `<name>` to the caller.
2. Without `command` and `url`: attach an existing `[mcp.<name>]` to the caller.

If the test fails, nothing is written. A definition that differs from an existing `[mcp.<name>]` that another agent uses is rejected.

#### Acceptance criteria

- `AC-AMCP-004`: Given no `[mcp.fs]` and an approved call `add` `name = "fs"`, `command = "npx"`, `args = [...]`, when the server answers `tools/list` with 3 tools, then `config.toml` has `[mcp.fs]`, `agents.<caller>.mcp` contains `"fs"`, and the output says 3 tools are available from the next turn.
- `AC-AMCP-005`: Given an approved `add` whose server fails to start, when the call ends, then `config.toml` bytes are unchanged and the output starts with `connection test failed:`.
- `AC-AMCP-006`: Given `[mcp.db]` attached to agent `other`, when the caller adds `db` with a different `command`, then the call fails with `conflict:` and `config.toml` is unchanged.
- `AC-AMCP-007`: Given `[mcp.db]` exists and the caller adds `name = "db"` without `command` or `url`, when approved, then only `agents.<caller>.mcp` changes.

### REQ-AMCP-004: Remove a server

`mcp_manage` with `action = "remove"` shall detach `<name>` from the caller. It deletes `[mcp.<name>]` only when no other agent's `mcp` list names it.

#### Acceptance criteria

- `AC-AMCP-008`: Given `[mcp.fs]` attached only to the caller, when an approved `remove` runs, then `[mcp.fs]` is gone, the caller's list has no `fs`, and the running `fs` process is stopped.
- `AC-AMCP-009`: Given `[mcp.fs]` attached to the caller and agent `other`, when an approved `remove` runs, then `[mcp.fs]` stays and only the caller's list changes.

### REQ-AMCP-005: Hot apply

After a successful `add` or `remove`, the system shall update the running MCP manager and the caller's effective MCP list, so the next turn of any session of that agent offers the new tools, or stops offering the removed ones, without a restart.

#### Acceptance criteria

- `AC-AMCP-010`: Given a successful `add` of `fs` in turn N, when turn N+1 of the same session starts, then the offered tools include `mcp__fs__*` and no daemon restart happened.
- `AC-AMCP-011`: Given a successful `remove` of `fs`, when the next turn starts, then no `mcp__fs__*` tool is offered.

### REQ-AMCP-006: Owner approval on every change

`add` and `remove` shall have risk `admin`. An `admin` call shall ask the owner every time: `trust` policy, allow rules, and session grants do not apply. Deny rules apply. Policy `never` and child sessions reject it. `list` has risk `read`.

#### Acceptance criteria

- `AC-AMCP-012`: Given policy `trust` and allow rule `mcp_manage`, when the agent calls `add`, then the owner is asked.
- `AC-AMCP-013`: Given the owner answered `allowed_session` to an earlier `add`, when the agent calls `add` again, then the owner is asked again.
- `AC-AMCP-014`: Given policy `never`, or a deny rule `mcp_manage`, when the agent calls `add`, then the call is rejected without a question.
- `AC-AMCP-015`: Given a child session, when `mcp_manage` with `add` reaches the permission engine, then it is rejected.

### REQ-AMCP-007: Secrets stay out of the chat

The tool shall accept `env` and `headers` values with `${NAME}` references. When a reference does not resolve, the call shall fail and tell the agent which name the owner must add in Settings → Secrets. The tool never reads or writes `secrets.env`.

#### Acceptance criteria

- `AC-AMCP-016`: Given no `GITHUB_TOKEN` in the environment or `secrets.env`, when an approved `add` uses `env = { GITHUB_TOKEN = "${GITHUB_TOKEN}" }`, then the call fails with `secret GITHUB_TOKEN is not set` and `config.toml` is unchanged.

### REQ-AMCP-008: Safe config writes

Every write shall run under the same process-wide lock as the web config writers, re-read `config.toml`, pass `config.Check`, write atomically, and append an audit entry (`mcp.save` or `mcp.delete`, and `agent.save` for the list).

#### Acceptance criteria

- `AC-AMCP-017`: Given a successful `add`, when `GET /v1/audit` runs, then it shows an `mcp.save` entry for the server.
- `AC-AMCP-018`: Given a candidate that fails `config.Check`, when the call ends, then `config.toml` is unchanged and the output starts with `config.toml is not valid:`.

### REQ-WS-001: Create a missing workspace

At turn start the system shall create the session workspace folder with mode `0700` when it does not exist.

#### Acceptance criteria

- `AC-WS-001`: Given a session whose workspace `~/lana/workspace` does not exist, when a turn starts and the agent calls `bash` with `pwd`, then the folder exists and `bash` prints its path.

### REQ-WS-002: Clear workspace errors

When the workspace folder still does not exist or is not a folder, `bash`, `glob`, and `grep` shall fail with `workspace does not exist: <path>` or `workspace is not a directory: <path>` instead of the operating system error.

#### Acceptance criteria

- `AC-WS-002`: Given a workspace path that does not exist and cannot be created, when the agent calls `bash`, then the output is `workspace does not exist: <path>` and does not contain `fork/exec`.

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| Child session | Reject in the permission engine | `rejected` |
| No `$LANA_HOME` | `mcp_manage is not available: no home folder` | tool `error` |
| No MCP admin service in the runtime | `mcp_manage is not available in this runtime` | tool `error` |
| `config.toml` missing | `config.toml does not exist` | tool `error` |
| Bad input | `invalid input: <reason>` | tool `error` |
| Unresolved `${NAME}` | `secret NAME is not set; ask the owner to add it in Settings → Secrets` | tool `error` |
| Connection test fails | `connection test failed: <reason>`; nothing written | tool `error` |
| Different definition used by other agents | `conflict: mcp server <name> is used by <agents>` | tool `error` |
| `remove` of a server not attached | `mcp server <name> is not attached to <agent>` | tool `error` |
| `config.Check` fails | `config.toml is not valid: <issues>` | tool `error` |

## Authorization and security

- Adding a stdio server runs an arbitrary command as the lana user. That is why `admin` ignores `trust`, allow rules, and grants.
- Output and logs never contain env or header values. Errors pass through the web redaction rule for MCP values.
- The agent can only change its own list.

## Data integrity and concurrency

- The config write lock is shared with the web handlers, so a web save and an agent save never interleave.
- The in-memory apply happens only after the atomic write succeeds.
- A replaced server's running session is closed before the new definition is used.

## Accessibility

Not applicable. No new UI.

## Scenarios

### SC-001: Owner asks the agent to connect the filesystem server

**Given** agent `lana` in a web chat with policy `trust`
**When** the owner says "add the filesystem MCP for ~/notes" and approves the `mcp_manage` prompt
**Then** `config.toml` has `[mcp.filesystem]`, `agents.lana.mcp` has `"filesystem"`, and on the next message `lana` can call `mcp__filesystem__read_file`.

## Blocking Open Questions

| ID | Question | Controls | Requested reference | Owner | Status | Resolution |
|---|---|---|---|---|---|---|
| `Q-001` | How does the agent change MCP servers? | `REQ-AMCP-001` | proposal 2026-10-30 | Ridho | Resolved | Built-in tool `mcp_manage`, auto-offered to root sessions |
| `Q-002` | Does `trust` auto-approve MCP changes? | `REQ-AMCP-006` | proposal 2026-10-30 | Ridho | Resolved | No. Every `add` and `remove` asks the owner |
| `Q-003` | Where do secrets go? | `REQ-AMCP-007` | proposal 2026-10-30 | Ridho | Resolved | `${NAME}` references only; owner adds values in Settings → Secrets |
| `Q-004` | Restart or hot apply? | `REQ-AMCP-005` | proposal 2026-10-30 | Ridho | Resolved | Hot apply from the next turn |
| `Q-005` | Fix the missing workspace in the same spec? | `REQ-WS-*` | proposal 2026-10-30 | Ridho | Resolved | Yes |

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-AMCP-001` | `AC-AMCP-001`, `AC-AMCP-002` | `Q-001` |
| `REQ-AMCP-002` | `AC-AMCP-003` | `Q-001` |
| `REQ-AMCP-003` | `AC-AMCP-004` through `AC-AMCP-007` | `Q-001` |
| `REQ-AMCP-004` | `AC-AMCP-008`, `AC-AMCP-009` | `Q-001` |
| `REQ-AMCP-005` | `AC-AMCP-010`, `AC-AMCP-011` | `Q-004` |
| `REQ-AMCP-006` | `AC-AMCP-012` through `AC-AMCP-015` | `Q-002` |
| `REQ-AMCP-007` | `AC-AMCP-016` | `Q-003` |
| `REQ-AMCP-008` | `AC-AMCP-017`, `AC-AMCP-018` | Spec #9 |
| `REQ-WS-001` | `AC-WS-001` | `Q-005` |
| `REQ-WS-002` | `AC-WS-002` | `Q-005` |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit or stated as not applicable.
- [x] State transitions and destructive effects are explicit or not applicable.
- [x] Defaults, ordering, retry, and idempotency are explicit where applicable.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Contract applicability is explicit.
- [x] Blocking Open Questions is `None`.
- **Approved by**: Ridho
- **Approved on**: 2026-10-30
