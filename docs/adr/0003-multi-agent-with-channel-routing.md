# 0003. Run lightweight multi-agent with routing from channels

- **Status**: Accepted (Rust implementation details replaced by [0013](0013-use-go-as-single-binary.md); the architectural decision stands)
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0002](0002-core-from-modules-services-and-hooks.md), [0004](0004-permission-mode-per-agent.md), [0011](0011-subagent-as-child-session.md)

## Context

The owner needs three different roles: coding assistant, personal assistant, and
automation/ops. Each role needs a different system prompt, model, tools, and permission level.
zeroclaw uses one agent for all channels. nouride uses several isolated agents with their own personas.

A full agent per process (such as a container per agent) is too heavy for the "lightweight" target.

## Decision

We run **several agents inside one daemon**. An agent is data (configuration), not a process.
One `AgentRuntime` is created per active session as a `tokio` task.

Agents are defined in `config.toml`:

```toml
[providers.anthropic]
kind = "anthropic"
api_key_env = "ANTHROPIC_API_KEY"

[providers.openrouter]
kind = "openai_compat"
base_url = "https://openrouter.ai/api/v1"
api_key_env = "OPENROUTER_API_KEY"

[agents.coder]
provider = "anthropic"
model = "claude-sonnet-5-5"
fallback = ["openrouter:anthropic/claude-haiku-5-5"]
prompt_file = "agents/coder.md"
workspace = "~/coding"
tools = ["read_file", "write_file", "edit_file", "glob", "grep", "bash"]
mcp = ["github"]
skills = ["git-workflow"]
permission = "ask"            # see ADR 0004

[agents.assistant]
provider = "openrouter"
model = "openai/gpt-5-mini"
prompt_file = "agents/assistant.md"
tools = ["web_fetch", "memory_search", "memory_save", "cron_create"]
permission = "trust"

[[routes]]
channel = "telegram"
match = { chat_id = 123456789 }
agent = "assistant"

[[routes]]
channel = "cli"
agent = "coder"               # default for `lana chat`
```

Routing rules:

1. Incoming messages are matched against `[[routes]]` from top to bottom. The first matching route wins.
2. The user can switch agents inside a chat with the `/agent <name>` command. The choice is stored per session.
3. If no route matches, the message is rejected and logged. There is no silent fallback.

The session key is `(channel, address, agent)`. A session has only one active turn and one
**inbox**, following `dsh`. Messages that arrive while a turn is running go into the inbox. The agent loop
claims one message from the inbox at the start of the next step, so the user can correct the agent in the
middle of a turn without waiting for it to finish.

Delegation between agents uses subagents as child sessions (see [0011](0011-subagent-as-child-session.md)).

## Consequences

**Positive**
- The extra cost per agent is only its configuration and one `tokio` task while active.
- One Telegram bot can serve several agents through routes or `/agent`.
- Personas and tools are logically isolated per agent.

**Negative / tradeoffs**
- Isolation is only logical. A bug in one tool can affect the entire daemon.
- A per-session queue means rapid back-to-back messages are not processed in parallel.

**Neutral**
- Agent prompts are stored as Markdown files in `~/.lana/agents/`, easy to edit and version.

## Alternatives considered

- **Single agent (zeroclaw style)** — simplest. Rejected because the coding and personal roles need different permissions.
- **One process per agent** — strong isolation. Rejected because RAM multiplies and IPC is needed.
