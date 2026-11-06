# Requirements: MCP client and Skills

- **Issue**: —
- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (`[mcp.*]` config, tool names, `skill_load` and `skill_write`, skill files, `lana mcp` and `lana skill`, `/mcp` and `/skills`)
- **Related ADR**: [ADR Index](../../adr/README.md) — [0004](../../adr/0004-permission-mode-per-agent.md), [0006](../../adr/0006-extend-via-mcp-client-and-skills.md), [0013](../../adr/0013-use-go-as-single-binary.md)
- **Related diagram**: [Diagram Index](../../diagram/README.md) — [0002](../../diagram/0002-agent-loop-and-approval.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-30 | "improve the agent's abilities first, arm the agent, before cron" | goal |
| Interview 2026-10-30 | Scope: MCP client and Skills (not web_search, not subagents) | scope |
| Interview 2026-10-30 | Trust MCP `readOnlyHint`; global skills only when listed; agents may write skills with approval; HTTP MCP with static headers, no OAuth | `Q-001` through `Q-004` |
| ADR 0006 | `[mcp.<name>]` with `command` or `url`, lazy start, idle stop after 10 minutes, tools named `mcp__<server>__<tool>`, agent `mcp = [...]`, skills with frontmatter, `skill_load`, progressive disclosure, scripts through `bash` | MCP and skill rules |
| ADR 0004 | permission engine, risk classes, rules | approvals |
| ADR 0013 | Go single binary | official Go MCP SDK |
| Spec #5 | agent home under `$LANA_HOME/agents/<name>/` | agent-own skills |

## Goal

The owner can give agents new abilities without changing lana's code: connect existing MCP servers
(GitHub, browsers, databases, and others) and teach procedures as skills that agents load when relevant.
Agents can also save a procedure that worked as a skill for later, with the owner's approval.

## Actors

- **Owner** — configures MCP servers and writes global skills; approves risky MCP calls and skill writes.
- **Agent** — calls MCP tools and loads skills it can see; may write skills into its own agent home.
- **MCP server** — an external process (stdio) or HTTP endpoint that offers tools. Its output is data, never instructions to lana.

## Definitions

- **MCP server config** — a `[mcp.<name>]` table with either `command` (stdio) or `url` (streamable HTTP).
- **MCP tool** — a tool offered by an MCP server, exposed to the model as `mcp__<server>__<tool>`.
- **Global skill** — `$LANA_HOME/skills/<name>/SKILL.md`.
- **Agent skill** — `$LANA_HOME/agents/<agent>/skills/<name>/SKILL.md`, owned by one agent.
- **Visible skills** — the skills an agent can see and load (`REQ-SKL-002`).
- **Skill folder** — the folder that contains `SKILL.md` and optional scripts or reference files.

## Scope

### Included

- MCP client over stdio and streamable HTTP with static headers.
- Lazy start, idle stop, restart on next use, stop on process exit.
- MCP tools in the agent's tool list for servers in its `mcp = [...]`.
- Risk from `readOnlyHint`; rules and policies as for built-in tools.
- Skills: global and agent-owned, frontmatter, listing in the prompt, `skill_load`, `skill_write`.
- CLI: `lana mcp list`, `lana mcp tools <server>`, `lana skill list`, `lana skill new <name>`, `lana skill show <name>`.
- Slash commands `/mcp` and `/skills` in the TUI and on Telegram.

### Not included

- OAuth login for remote MCP servers.
- MCP resources, prompts, sampling, and elicitation (tools only).
- lana as an MCP server (`lana mcp serve`).
- `web_search` and subagents (later specs).
- Installing MCP servers or skills from a registry.
- Sandboxing MCP server processes beyond running them as the owner's OS user.

## Assumptions

- `[mcp.<name>]` with `command`, `args`, `env`, `url`, `headers` and `${VAR}` interpolation from secrets already validates and loads. — Source: spec #2 contract.
- Agents already have `mcp = [...]` and `skills = [...]` config keys. — Source: spec #2 config schema.
- Tool calls run through `tools.pre_execute`. — Source: ADR 0004.

## Behavior rules

### MCP

#### REQ-MCP-001: Agent access

An agent shall see the tools of exactly the MCP servers listed in its `mcp = [...]`, named `mcp__<server>__<tool>`, in addition to its `tools = [...]`. A server name not in `[mcp.*]` is a config error.

- `AC-MCP-001`: Given agent `dev` with `mcp = ["github"]` and server `github` offering `list_issues`, when a turn starts, then the tool list sent to the model contains `mcp__github__list_issues`.
- `AC-MCP-002`: Given agent `helper` without `mcp`, when a turn starts, then no `mcp__` tool is sent.
- `AC-MCP-003`: Given `mcp = ["nope"]` and no `[mcp.nope]`, when the config loads, then validation fails naming `agents.<name>.mcp`.

#### REQ-MCP-002: Lazy start and idle stop

The system shall start an MCP server when a turn of an agent that uses it starts and the server is not running, stop it after 10 minutes without calls, and start it again on next use. All servers shall stop when the lana process exits.

- `AC-MCP-004`: Given a running daemon with no turn yet, when no agent using `github` has run, then the `github` process is not running.
- `AC-MCP-005`: Given `github` idle for 10 minutes, when the idle check runs, then the process is stopped; the next turn starts it again.

#### REQ-MCP-003: Unavailable servers

If a server cannot start or list its tools, the turn shall run without that server's tools, and the runtime context shall state `unavailable MCP servers: <name> (<short error>)`.

- `AC-MCP-006`: Given `[mcp.broken]` whose command does not exist, when a turn of an agent using it starts, then the turn runs and the prompt names `broken` as unavailable.

#### REQ-MCP-004: Risk and permission

An MCP tool with `readOnlyHint = true` shall have risk `read`; every other MCP tool shall have risk `exec`. Rules (`allow`, `deny`) and policies apply as for built-in tools.

- `AC-MCP-007`: Given policy `ask`, when the agent calls a tool with `readOnlyHint = true`, then no approval is requested.
- `AC-MCP-008`: Given policy `ask`, when the agent calls a tool without that hint, then approval is requested.
- `AC-MCP-009`: Given `rules.allow = ["mcp__github__*"]`, when the agent calls any `github` tool, then no approval is requested.

#### REQ-MCP-005: Results

The system shall turn an MCP tool result into tool output: text parts joined with newlines; non-text parts as `[<type> omitted]`; `isError = true` as status `error`. Output longer than 30,000 characters shall be truncated like built-in tools. A call shall time out after 120 seconds and follow turn cancel.

- `AC-MCP-010`: Given a result with text `ok` and an image, when it returns, then the tool output is `ok\n[image omitted]`.
- `AC-MCP-011`: Given a result with `isError = true`, when it returns, then the tool status is `error`.

#### REQ-MCP-006: Process environment

A stdio server shall inherit the lana process environment plus its `env` table, and run in `$LANA_HOME` as working directory.

- `AC-MCP-012`: Given `env = { TOKEN = "${GH_TOKEN}" }`, when the server starts, then its environment has `TOKEN` set from the secret and `PATH` from the lana process.

### Skills

#### REQ-SKL-001: Skill files

A skill is a folder with `SKILL.md` that starts with YAML frontmatter holding `name` (same rule as agent names, equal to the folder name) and `description` (one line, at most 300 characters). A skill without valid frontmatter shall be skipped and reported by `lana skill list` and `lana config check`.

- `AC-SKL-001`: Given `skills/git-workflow/SKILL.md` without `description`, when `lana skill list` runs, then it shows `git-workflow` as invalid with the reason.

#### REQ-SKL-002: Visibility

An agent shall see the global skills listed in its `skills = [...]` plus all of its agent skills. An agent skill with the same name as a global skill wins for that agent.

- `AC-SKL-002`: Given global skills `a` and `b`, agent `dev` with `skills = ["a"]`, and agent skill `c`, when its visible skills are computed, then they are `a` and `c`.

#### REQ-SKL-003: Skills in the prompt

When an agent has visible skills, the system prompt shall list each as `- <name>: <description>` under `# Skills`, with the instruction to call `skill_load` before following a skill. Skill bodies are not in the prompt.

- `AC-SKL-003`: Given visible skill `git-workflow`, when a turn starts, then the prompt contains `- git-workflow: <description>` and not the body.

#### REQ-SKL-004: `skill_load`

The tool `skill_load(name)` (risk `read`) shall return the body of a visible skill, the skill folder path, and the list of other files in the folder. A name that is not visible shall return an error.

- `AC-SKL-004`: Given visible skill `deploy` with `deploy.sh`, when the agent calls `skill_load("deploy")`, then the output has the body, the folder path, and `deploy.sh`.
- `AC-SKL-005`: Given global skill `b` not listed for the agent, when the agent calls `skill_load("b")`, then the result is an error.

#### REQ-SKL-005: `skill_write`

The tool `skill_write(name, description, body)` (risk `write`) shall create or replace the agent skill `name` of the calling agent. It shall never write global skills, other agents' skills, or files other than `SKILL.md`.

- `AC-SKL-006`: Given policy `ask`, when the agent calls `skill_write`, then approval is requested; on approval `agents/<agent>/skills/<name>/SKILL.md` holds the frontmatter and body.
- `AC-SKL-007`: Given policy `never`, when the agent calls `skill_write`, then the result is `rejected` and nothing is written.

### CLI and commands

#### REQ-CLI-001: `lana mcp`

`lana mcp list` shall print each configured server with its transport and the agents that use it. `lana mcp tools <server>` shall start the server, print each tool with its risk and description, and stop it.

- `AC-CLI-001`: Given a working stdio server, when the owner runs `lana mcp tools github`, then each tool appears as `mcp__github__<tool>  <read|exec>  <description>` and the exit code is 0.
- `AC-CLI-002`: Given a server that fails to start, when the owner runs `lana mcp tools broken`, then stderr explains the error and the exit code is 1.

#### REQ-CLI-002: `lana skill`

`lana skill list` shall print global skills and agent skills with descriptions and validity. `lana skill new <name>` shall create a global skill from a template and print its path. `lana skill show <name> [--agent <a>]` shall print the skill file.

- `AC-CLI-003`: Given no skill `notes`, when the owner runs `lana skill new notes`, then `skills/notes/SKILL.md` exists with valid frontmatter and the exit code is 0.

#### REQ-CMD-001: `/mcp` and `/skills`

The TUI and Telegram shall offer `/mcp` (the active agent's servers with state `running`, `stopped`, or `unavailable` and tool count) and `/skills` (the active agent's visible skills).

- `AC-CMD-001`: Given agent `dev` with `mcp = ["github"]` after one turn, when the owner sends `/mcp`, then the reply shows `github running` with the tool count.

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| Server command missing or exits at start | Turn runs without it; runtime context names it | — |
| Server crashes during a call | Tool status `error` with the reason; server restarts on next use | — |
| Call takes longer than 120 seconds | Tool status `error`: `timed out after 120 seconds` | — |
| HTTP server returns 401/403 | Server marked unavailable with `unauthorized`; turn continues | — |
| Invalid `SKILL.md` | Skill skipped; listed as invalid | — |
| `skill_write` with invalid name or empty body | Tool status `error`, nothing written | — |
| `lana mcp tools` on unknown server | `error: unknown MCP server "<name>"` | exit 2 |

## Authorization and security

- MCP tools without `readOnlyHint` require approval under `ask`; `deny` rules can block any MCP tool by name pattern.
- MCP output is treated as tool data. The runtime never executes instructions from it.
- MCP servers run as the owner's OS user; the owner chooses which servers to configure.
- Secrets in `env` and `headers` come from `${VAR}` interpolation and are never logged.
- `skill_write` only writes `SKILL.md` inside the calling agent's own agent home, always under approval unless the policy is `trust` or a rule allows it.

## Data integrity and concurrency

- One client connection per server per process; concurrent calls from several sessions share it.
- `skill_write` replaces `SKILL.md` atomically.
- The idle timer resets on every call; a call never races with an idle stop (the stop waits for running calls).

## Accessibility

- CLI output is plain text; states are words (`running`, `stopped`, `unavailable`, `invalid`).

## Scenarios

### SC-001: GitHub through MCP

**Given** `[mcp.github]` running `github-mcp-server stdio` with a token, and agent `dev` with `mcp = ["github"]`
**When** the owner asks on Telegram "list my open issues in lana-harness"
**Then** the agent calls `mcp__github__list_issues` (read-only, no approval) and answers with the list.

### SC-002: The agent saves a procedure

**Given** agent `worker` just fixed a disk-full problem after several steps
**When** the owner says "save this as a skill"
**Then** the agent calls `skill_write("disk-cleanup", ...)`, the owner approves, and the next session lists `disk-cleanup` in the prompt.

## Blocking Open Questions

| ID | Question | Controls | Agent recommendation | Owner | Status | Resolution |
|---|---|---|---|---|---|---|
| `Q-001` | Trust MCP `readOnlyHint`? | `REQ-MCP-004` | Yes | Ridho | Resolved | Yes |
| `Q-002` | Which global skills does an agent see? | `REQ-SKL-002` | All unless restricted | Ridho | Resolved | Only those listed in `skills = [...]` |
| `Q-003` | May agents write skills? | `REQ-SKL-005` | Yes, with approval | Ridho | Resolved | Yes, with approval |
| `Q-004` | OAuth for remote MCP? | Scope | Static headers only | Ridho | Resolved | Static headers only |
| `Q-005` | Where do agent-written skills go, and are they visible without config changes? | `REQ-SKL-002`, `REQ-SKL-005` | `agents/<agent>/skills/`, always visible to that agent | Ridho | Resolved | Agent recommendation accepted |
| `Q-006` | What happens when an MCP server is unavailable? | `REQ-MCP-003` | Turn continues without it; prompt names it | Ridho | Resolved | Agent recommendation accepted |
| `Q-007` | Environment and working directory of stdio servers | `REQ-MCP-006` | Inherit lana env plus `env`; cwd `$LANA_HOME` | Ridho | Resolved | Agent recommendation accepted |
| `Q-008` | MCP call timeout and output cap | `REQ-MCP-005` | 120 s; 30,000 characters like built-in tools | Ridho | Resolved | Agent recommendation accepted |
| `Q-009` | CLI and slash commands | `REQ-CLI-*`, `REQ-CMD-001` | `lana mcp list|tools`, `lana skill list|new|show`, `/mcp`, `/skills` | Ridho | Resolved | Agent recommendation accepted |

All questions are resolved. Blocking Open Questions: None.

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-MCP-001` | `AC-MCP-001`, `AC-MCP-002`, `AC-MCP-003` | ADR 0006 |
| `REQ-MCP-002` | `AC-MCP-004`, `AC-MCP-005` | ADR 0006 |
| `REQ-MCP-003` | `AC-MCP-006` | `Q-006` |
| `REQ-MCP-004` | `AC-MCP-007`, `AC-MCP-008`, `AC-MCP-009` | ADR 0004, ADR 0006, `Q-001` |
| `REQ-MCP-005` | `AC-MCP-010`, `AC-MCP-011` | `Q-008` |
| `REQ-MCP-006` | `AC-MCP-012` | `Q-007` |
| `REQ-SKL-001` | `AC-SKL-001` | ADR 0006 |
| `REQ-SKL-002` | `AC-SKL-002` | `Q-002`, `Q-005` |
| `REQ-SKL-003` | `AC-SKL-003` | ADR 0006 |
| `REQ-SKL-004` | `AC-SKL-004`, `AC-SKL-005` | ADR 0006 |
| `REQ-SKL-005` | `AC-SKL-006`, `AC-SKL-007` | `Q-003`, `Q-005` |
| `REQ-CLI-001` | `AC-CLI-001`, `AC-CLI-002` | `Q-009` |
| `REQ-CLI-002` | `AC-CLI-003` | `Q-009` |
| `REQ-CMD-001` | `AC-CMD-001` | `Q-009` |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit or stated as not applicable.
- [x] State transitions and destructive effects are explicit or not applicable.
- [x] Defaults, ordering, retry, and idempotency are explicit where applicable.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Contract applicability is explicit.
- [x] Blocking Open Questions is `None`.
- **Approved by**: Ridho (interview answers 2026-10-30)
- **Approved on**: 2026-10-30
