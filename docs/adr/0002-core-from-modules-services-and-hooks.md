# 0002. Build the core from modules, services, and hooks in a Cargo workspace

- **Status**: Accepted (Rust implementation details replaced by [0013](0013-use-go-as-single-binary.md); the architectural decision stands)
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0001](0001-use-rust-single-binary-daemon.md), [0009](0009-session-as-append-only-event-log.md), [0010](0010-profile-and-config-patch.md)

## Context

The harness needs to support several LLM providers, several channels, several tool sources, and
policies such as approval, compaction, and audit. Without clear boundaries, every feature leaks into the
agent loop and the code quickly becomes tangled.

The reference for how things work is **deepseek-harness (`dsh`)**. In `dsh` every part is a Cordis
plugin: model adapter, tool registry, session log, even the agent loop. Plugins register *services* into a
shared context (`ctx.tools`, `ctx.llm`, `ctx.sessions`) and communicate through typed *events*. The
`waterfall` event (middleware with `next()`) is the extension point for policies, for example `agent/pre-step`,
`tools/pre-execute`, and `approval/request`. There is no privileged core that needs patching.

Constraints for `lana`:

- Rust, small binary. Loading plugins at runtime (dynamic library, WASM) is **not** part of v1.
- Hot reload (HMR) is not needed for a personal daemon.

## Decision

We adopt the `dsh` pattern in **compile-time** form:

1. **Modules.** Every feature is a `Module` implementation compiled into the binary. The profile
   selects which modules are active and their configuration (see [0010](0010-profile-and-config-patch.md)).
2. **Services.** Modules register typed services into the `Context`. Other modules use a service through
   a trait, not through a concrete type.
3. **Hooks.** Intercept points are typed hooks with either `waterfall` mode (middleware with `next`) or
   `observe` mode (listen only). Policies such as permission and compaction are hook listeners, not code
   inside the agent loop.
4. **Durable events.** Facts that must survive a restart are written to the session event log
   (see [0009](0009-session-as-append-only-event-log.md)) and then broadcast to observers.

```rust
pub trait Module: Send + Sync {
    fn id(&self) -> &'static str;
    fn requires(&self) -> &'static [ServiceKey];   // boot order from dependencies
    fn install(&self, cx: &mut ContextBuilder, cfg: &toml::Value) -> Result<()>;
}

pub struct Context {
    pub llm: Arc<dyn LlmRegistry>,        // ctx.llm      provider adapters
    pub tools: Arc<ToolRegistry>,         // ctx.tools    registry and execution pipeline
    pub sessions: Arc<dyn SessionStore>,  // ctx.sessions event log
    pub agents: Arc<AgentRegistry>,       // ctx.agents   live agents and inbox
    pub approval: Arc<ApprovalService>,   // ctx.approval
    pub prompt: Arc<SystemPrompt>,        // ctx.prompt   system prompt sections
    pub hooks: Hooks,                     // all typed hooks
    pub events: broadcast::Sender<SessionEventEnvelope>,
}

#[async_trait]
pub trait Waterfall<I: Send, O: Send>: Send + Sync {
    async fn call(&self, cx: &Context, input: I, next: Next<'_, I, O>) -> Result<O>;
}
```

Hooks in v1:

| Hook | Mode | Function |
|---|---|---|
| `agent.pre_step` | waterfall | Accept, modify, or reject input claimed before a step |
| `agent.request` | waterfall | Modify request controls (max tokens, temperature) before sending |
| `llm.stream` | waterfall | Wrap the provider stream (retry, fallback, token metering) |
| `tools.pre_execute` | waterfall | Validation and permission before a tool runs |
| `tools.execute` | waterfall | Tool execution (can be wrapped with a timeout, sandbox later) |
| `tools.post_execute` | waterfall | Modify or truncate tool output (spill large output to a file) |
| `approval.request` | waterfall | Chain of approval responders: channel, ACP client, or `unavailable` |
| `agent.turn_stopping` | serial | Decide whether the turn should really stop |
| `session.event` | observe | Broadcast durable events to channels, web, and protocols |

Workspace layout:

```text
crates/
  lana-core/       Context, Module, Hooks, message and event types, service traits. No I/O.
  lana-agent/      Agent loop (turn and step), inbox, subagents, compaction
  lana-store/      SQLite: session event log, projections, memory, cron, audit
  lana-providers/  anthropic, openai_compat (OpenAI, OpenRouter, Ollama)
  lana-tools/      builtin tools and permission engine
  lana-mcp/        MCP client that wraps MCP servers as tools
  lana-channels/   cli, telegram, whatsapp
  lana-protocol/   ACP server and SDK JSON-RPC
  lana-web/        axum HTTP API, SSE, embedded dashboard
  lana/            binary: subcommands, profiles, config loader
```

Internal message types (`Message`, `ContentBlock::{Text, ToolUse, ToolResult, Image}`) follow the shape
of the Anthropic Messages API. The `openai_compat` adapter converts to and from the OpenAI format.

## Consequences

**Positive**
- The agent loop itself can be replaced or tested with `MockLlm` without network access.
- New policies (approval, audit, redaction) only need a new hook listener.
- Typed hooks are checked by the compiler. There is no string-based event bus.
- Profiles let the same binary behave as a daemon, a one-shot run, or an ACP server.

**Negative / tradeoffs**
- More abstraction than a simple loop. Requires disciplined hook documentation.
- Without a plugin runtime, a new module requires rebuilding the binary.
- `dyn` and `async_trait` add a small amount of allocation. Not significant compared to LLM latency.

**Neutral**
- Third-party plugins (external processes via JSON-RPC, or WASM) can be added later as a `Module` that bridges hooks out of process.
- External integrations in v1 go through MCP and skills (see [0006](0006-extend-via-mcp-client-and-skills.md)).

## Alternatives considered

- **Monolithic agent loop with provider, channel, and tool traits only** — simpler. Rejected
  because policies would pile up inside the loop and would not follow the `dsh` approach.
- **Dynamic string-based event bus like Cordis** — flexible. Rejected because it loses type checking and is slower in Rust.
- **Plugin runtime (dynamic library or WASM) from v1** — deferred by the owner's decision.
