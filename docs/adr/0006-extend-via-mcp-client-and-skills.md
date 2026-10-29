# 0006. Add capabilities through an MCP client and Skills

- **Status**: Accepted (Rust implementation details replaced by [0013](0013-use-go-as-single-binary.md); the architectural decision stands)
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0002](0002-core-from-modules-services-and-hooks.md), [0004](0004-permission-mode-per-agent.md)

## Context

Builtin tools will not cover every need (GitHub, calendar, databases, browsers).
Writing a custom integration for each service makes the binary bloat. Two standards are already
in common use:

- **MCP (Model Context Protocol)** — external servers provide tools over stdio or HTTP.
- **Skills (`SKILL.md`)** — a folder containing Markdown instructions and scripts, loaded only when relevant (Claude Code style).

## Decision

**MCP client.** `lana-mcp` uses the official `rmcp` crate with `stdio` and streamable HTTP transports.

```toml
[mcp.github]
command = "github-mcp-server"
args = ["stdio"]
env = { GITHUB_TOKEN = "${GITHUB_TOKEN}" }

[mcp.notion]
url = "https://mcp.notion.com/mcp"
```

- MCP servers are started **lazily**: a process starts when an agent that uses it becomes active, and is stopped after 10 minutes of idleness.
- Each MCP tool is wrapped as a `Tool` named `mcp__<server>__<tool>`. The default risk is `Exec`,
  so in `ask` mode it always requests approval unless it is in `allow`.
- An agent only sees tools from servers listed in its own `mcp = [...]` list.

**Skills.** Skills are stored in `~/.lana/skills/<name>/SKILL.md` with frontmatter:

```markdown
---
name: git-workflow
description: Create a branch, conventional commits, and open a PR with gh.
---
Steps ...
```

- When a session starts, only the `name` and `description` of the agent's skills enter the system prompt.
- The model calls the `skill_load(name)` tool to read the full content. This saves tokens (progressive disclosure).
- Scripts inside a skill folder are run through the regular `bash` tool, so they still pass through the permission engine.

## Consequences

**Positive**
- New integrations do not require rebuilding the binary.
- Existing MCP servers and skills can be used directly.
- Lazy start keeps idle RAM small.

**Negative / tradeoffs**
- Third-party MCP servers (often Node/Python) use their own RAM while active.
- The quality and security of MCP servers are outside our control. MCP tool output is treated as data, not instructions.

**Neutral**
- `rmcp` follows the MCP specification versions. Upgrading the specification means upgrading the crate.

## Alternatives considered

- **WASM plugins (zeroclaw `wit` style)** — good sandboxing. Deferred because it needs the large `wasmtime` runtime.
- **Dynamic library plugins** — rejected because the Rust ABI is not stable and is unsafe.
