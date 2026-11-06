# Contract: MCP client and Skills

- **Issue**: —
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 1
- **Requirements status**: Approved
- **Contract revision**: 1
- **Applies to**: `[mcp.*]` config, MCP tool names and risk, runtime context line, `# Skills` prompt section, tools `skill_load` and `skill_write`, skill files, `lana mcp`, `lana skill`, `/mcp`, `/skills`, agent templates
- **Status**: Approved

Shared conventions follow the [spec #1 contract](../1-cli-tui/api-contract.md#shared-conventions).

## Operation index

| ID | Operation | Requirements |
|---|---|---|
| `API-MCP-001` | `[mcp.<name>]` and agent `mcp` | `REQ-MCP-001`, `REQ-MCP-006` |
| `API-MCP-002` | Server lifecycle | `REQ-MCP-002`, `REQ-MCP-003` |
| `API-MCP-003` | MCP tools: names, schema, risk, results | `REQ-MCP-001`, `REQ-MCP-004`, `REQ-MCP-005` |
| `API-SKL-001` | Skill files and visibility | `REQ-SKL-001`, `REQ-SKL-002` |
| `API-SKL-002` | `# Skills` prompt section | `REQ-SKL-003` |
| `API-SKL-003` | Tool `skill_load` | `REQ-SKL-004` |
| `API-SKL-004` | Tool `skill_write` | `REQ-SKL-005` |
| `API-CLI-001` | `lana mcp` | `REQ-CLI-001` |
| `API-CLI-002` | `lana skill` | `REQ-CLI-002` |
| `API-CMD-001` | `/mcp`, `/skills` | `REQ-CMD-001` |

## API-MCP-001: Config

```toml
[mcp.github]
command = "github-mcp-server"
args = ["stdio"]
env = { GITHUB_PERSONAL_ACCESS_TOKEN = "${GITHUB_TOKEN}" }

[mcp.docs]
url = "https://example.com/mcp"
headers = { Authorization = "Bearer ${DOCS_TOKEN}" }

[agents.dev]
mcp = ["github"]
```

No new keys. Existing validation applies: exactly one of `command` or `url`; every name in an agent's `mcp` must exist in `[mcp.*]`.

Stdio servers: environment = lana process environment plus `env` (env wins); working directory `$LANA_HOME`; stderr of the server goes to the lana log at `debug`.
HTTP servers: streamable HTTP; `headers` sent on every request.

## API-MCP-002: Lifecycle

| Event | Effect |
|---|---|
| Turn start of an agent with `mcp` | Each listed server that is not running is connected (initialize + tools/list), timeout 30 seconds |
| Connect or list fails | Server state `unavailable` with a short reason; retried at the next turn start |
| No call for 10 minutes and no call running | Server closed (state `stopped`) |
| Process exit (`lana chat`, `lana run`, `lana daemon`) | All servers closed |
| Server process exits during a call | The call fails; state `stopped`; reconnected at the next turn start |

Runtime context line, added only when at least one listed server is unavailable:

```text
- unavailable MCP servers: github (command not found), docs (unauthorized)
```

The reason is at most 80 characters.

## API-MCP-003: MCP tools

| Topic | Decision |
|---|---|
| Name | `mcp__<server>__<tool>`; characters other than `a-zA-Z0-9_-` in the tool name become `_`; cut to 64 characters |
| Description | The server's description, cut to 1,024 characters |
| Input schema | The server's `inputSchema` unchanged |
| Risk | `read` when `annotations.readOnlyHint` is `true`; otherwise `exec` |
| Enabled | Only for agents that list the server in `mcp` |
| Timeout | 120 seconds per call: status `error`, output `timed out after 120 seconds` |
| Output | Text content joined with `\n`; other content as `[image omitted]`, `[audio omitted]`, `[resource omitted]`; structured content without text as compact JSON; truncated at 30,000 characters with the built-in truncation note |
| Error | `isError = true` gives status `error` with the same output; a protocol error gives status `error` with `mcp: <message>` |

Permission patterns match MCP tools by name like any tool, for example `mcp__github__*` or `mcp__github__create_issue`.

## API-SKL-001: Skill files and visibility

```text
$LANA_HOME/skills/<name>/SKILL.md                 global skill
$LANA_HOME/agents/<agent>/skills/<name>/SKILL.md  agent skill
```

```markdown
---
name: git-workflow
description: Create a branch, write conventional commits, and open a PR with gh.
---
1. ...
```

| Rule | Decision |
|---|---|
| `name` | Required, `^[a-z][a-z0-9_-]{0,63}$`, equal to the folder name |
| `description` | Required, one line, 1 to 300 characters |
| Other frontmatter keys | Ignored |
| Body | Everything after the closing `---` |
| Visible to agent A | Global skills named in A's `skills` + all of A's agent skills; an agent skill shadows a global skill with the same name |
| Order | By name |
| Invalid skill | Skipped; reported as `invalid: <reason>` |

## API-SKL-002: `# Skills` prompt section

Placed after `# Your memory` and before `# Runtime context`, only when the agent has visible skills:

```markdown
# Skills
Call skill_load with the skill name before following a skill.
- git-workflow: Create a branch, write conventional commits, and open a PR with gh.
- disk-cleanup: Free disk space on the home server safely.
```

`skill_load` is added to the turn's tools when the agent has visible skills, even if `tools` does not list it.

## API-SKL-003: `skill_load`

Risk `read`.

```json
{"name": "skill_load", "input_schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"], "additionalProperties": false}}
```

Output:

```text
skill <name> (<global|agent>)
folder: <absolute path>
files: <comma list of other files, relative, or "none">

<body>
```

Not visible: status `error`, `unknown skill "<name>" (available: <comma list>)`.

## API-SKL-004: `skill_write`

Risk `write`. Writes `$LANA_HOME/agents/<calling agent>/skills/<name>/SKILL.md` atomically, mode `0600`, folders `0700`.

```json
{"name": "skill_write", "input_schema": {"type": "object", "properties": {"name": {"type": "string"}, "description": {"type": "string"}, "body": {"type": "string"}}, "required": ["name", "description", "body"], "additionalProperties": false}}
```

| Case | Status | Output |
|---|---|---|
| Created | `success` | `created skill <name>` |
| Replaced | `success` | `updated skill <name>` |
| Invalid name, description, or empty body | `error` | `invalid input: <reason>` |
| Body over 20,000 characters | `error` | `invalid input: body is longer than 20000 characters` |

## API-CLI-001: `lana mcp`

```text
lana mcp list
lana mcp tools <server>
```

`list` (stdout), one line per server sorted by name:

```text
<name>  <stdio|http>  <command or url>  agents=<comma list or ->
```

`tools`: connects, prints `mcp__<server>__<tool>  <read|exec>  <description cut to 80>` per tool, closes. Errors: unknown server, exit 2 `error: unknown MCP server "<name>"`; connect failure, exit 1 `error: mcp <name>: <reason>`.

## API-CLI-002: `lana skill`

```text
lana skill list
lana skill new <name>
lana skill show <name> [--agent <agent>]
```

`list`: one line per skill: `<name>  <global|agent:<agent>>  <description or "invalid: <reason>">`.
`new`: creates `$LANA_HOME/skills/<name>/SKILL.md` with frontmatter (`description: TODO: describe when to use this skill`) and prints `created <path>` and `add "<name>" to skills = [...] of an agent to use it`; exists or invalid name: exit 2.
`show`: prints the file; with `--agent`, the skill visible to that agent; unknown: exit 2.

`lana config check` prints `· warning: skill <path>: <reason>` for each invalid skill.

## API-CMD-001: `/mcp` and `/skills`

`/mcp` (TUI and Telegram):

```text
· github running, 26 tools
· docs unavailable (unauthorized)
```

No servers: `· no MCP servers for <agent>`.

`/skills`:

```text
· git-workflow (global): Create a branch, write conventional commits, and open a PR with gh.
· disk-cleanup (agent): Free disk space on the home server safely.
```

No skills: `· no skills for <agent>`. Both commands are listed in `/help`, TUI completion, and Telegram `setMyCommands`.

## Templates

`skill_write` is added to the tools of the `assistant`, `worker`, and `coder` templates.

## Compatibility

- Agents without `mcp` and without visible skills behave as before.
- The `mcp` module is added to the base profile; it starts no process until an agent needs a server.
