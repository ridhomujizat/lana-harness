# 0018. Run Claude Code as an external agent runtime over headless stream-json

- **Status**: Accepted
- **Date**: 2026-11-08
- **Deciders**: Ridho
- **Related ADRs**: [0003](0003-multi-agent-with-channel-routing.md), [0004](0004-permission-mode-per-agent.md), [0009](0009-session-as-append-only-event-log.md), [0011](0011-subagent-as-child-session.md), [0012](0012-acp-and-sdk-json-rpc.md)

## Context

The owner has a Claude Pro/Max subscription and wants a lana agent that uses it, reachable from the
CLI, Telegram, and the web dashboard like any other agent.

lana's only LLM path is `core.Provider` (`core/chat.go`): lana runs its own agent loop and tools, and
the provider returns tokens and tool calls. Today only `openai_compat` providers are built.

Anthropic's terms bound how a subscription can be used:

- OAuth is for *"ordinary use of Claude Code and other native Anthropic applications"*. Third-party
  developers may not *"route requests through Free, Pro, Max, or Ultra plan credentials"*, and may not
  *"collect, store, or intermediate Claude.ai credentials or session tokens"*.
  ([Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance))
- The same page allows *"an end user signing in to the unmodified Claude Code binary with their own
  Claude subscription"*. Plan limits *"assume ordinary, individual usage of Claude Code and the Agent SDK"*.
- Unless Anthropic approves it, third-party products may not offer claude.ai login, including agents
  built on the Agent SDK. ([Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview))
- Since 2026, Anthropic blocks subscription tokens used outside Claude Code at the server and may act
  without notice. Tools that spoof Claude Code headers (for example omp) put the account at risk.

Because of these terms, lana cannot use the subscription as a `core.Provider`. Claude Code is a whole
agent, with its own loop, tools, permissions, and compaction. The legal way to use the subscription is
to run that agent as it is.

Claude Code has two non-interactive integration surfaces:

1. **Headless stream-json.** `claude -p --input-format stream-json --output-format stream-json`
   accepts JSON lines on stdin and writes them to stdout. A control channel on the same pipe carries
   `initialize`, `interrupt`, and `can_use_tool` (with `--permission-prompt-tool stdio`). The Agent
   SDK docs recommend this surface for languages other than Python and TypeScript. MonoCode uses it in
   production (`claudeProtocol.ts`).
2. **ACP adapter.** `@agentclientprotocol/claude-agent-acp`, an npm package built on the TypeScript
   Agent SDK, which in turn runs the same CLI.

## Decision

lana adds an **external agent runtime**. An agent sets `runtime = "claude-code"` instead of
`provider`. For each lana session, lana starts the user's installed `claude` binary in headless
stream-json mode and translates between that stream and lana's session events. lana does not
implement Claude through `core.Provider`.

```toml
[agents.claude]
runtime   = "claude-code"
auth      = "subscription"        # or "api_key" (uses api_key_env)
model     = "opus"                # passed to --model; "" uses Claude Code's default
workspace = "~/coding"
permission = "ask"
binary    = ""                    # "" finds `claude` on PATH
```

**Process.** lana runs `claude -p --input-format stream-json --output-format stream-json --verbose
--include-partial-messages --permission-prompt-tool stdio [--model M] [--resume ID]`, with
`cwd = workspace`. There is one long-lived process per active session, and it exits after an idle
timeout. lana stores the `session_id` from the `system/init` event in the session metadata and uses it
for `--resume` after a restart. Cancel sends an `interrupt` control request, then SIGTERM after 5 s.

**Credentials (hard rules).**
- lana never reads, copies, refreshes, or stores Claude credentials. It does not touch the Keychain
  (`Claude Code-credentials`) or `~/.claude/.credentials.json`, and it does not call Anthropic
  endpoints directly.
- lana does not set or spoof identity headers, User-Agent, or Claude Code version strings.
- With `auth = "subscription"`, lana removes `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, and
  `CLAUDE_CODE_OAUTH_TOKEN` from the child's environment, so Claude Code uses its own login. With
  `auth = "api_key"`, lana passes the key from `api_key_env` through the secrets service.
- When the user is not logged in, the turn fails with: "Claude Code is not logged in. Run `claude`,
  then `/login`." lana never runs a login flow itself.

**Event mapping** (the [0009](0009-session-as-append-only-event-log.md) log mirrors the stream):

| Claude Code stream | lana session event |
|---|---|
| `system/init` | session meta: `claude_session_id`, model, tool list |
| `stream_event` text deltas | assistant text delta |
| `assistant` `tool_use` block | `tool/call` with `external = true` (Claude Code executes it) |
| `user` `tool_result` block | `tool/result` |
| `control_request` `can_use_tool` | `approval.request` chain ([0004](0004-permission-mode-per-agent.md)) |
| `rate_limit_event` | stats: plan window and reset time (no direct API call) |
| `result` | turn end with usage, cost, `is_error` |

**Permissions.** `can_use_tool` uses lana's existing permission engine. Claude tool names map to lana
risk classes: `Read`, `Glob`, and `Grep` map to Read; `Edit` and `Write` to Write; `Bash` to Exec;
`WebFetch` and `WebSearch` to Network; MCP tools to Exec. `rules.allow` and `rules.deny` match
`bash(<command>)` against the Bash input. The outcome stays fail-closed.

**What does not apply.** lana's compaction, memory capture, loop guard, `max_steps`, and lana tools do
not run inside this runtime. Claude Code owns its context. lana's own tools (memory, knowledge,
calendar) become available only after a later ADR adds `lana mcp-serve`, which ADR 0012 lists as
"possible later".

**Usage guardrails.** These keep usage within "ordinary, individual usage":
- Background jobs (memory capture, knowledge upkeep, compaction, titles, embeddings) never use a
  `claude-code` agent.
- Cron and workflows may target a `claude-code` agent only with `allow_scheduled = true` on that agent.
  The default is false.
- At most 2 concurrent `claude` processes across the daemon. Further turns queue.
- The runtime is personal. lana does not advertise "Login with Claude", and it does not use "Claude
  Code" as the name of a lana feature. The UI label is "Claude Agent".

## Consequences

**Positive**
- The subscription is used as the terms intend: the unmodified binary, the user's own login, and no
  credential handling in lana.
- Pure Go with no new runtime dependency. lana stays `CGO_ENABLED=0` and needs no Node.
- Claude Code features keep working: its tools, skills, hooks, `~/.claude` settings, MCP servers, and
  auto-compaction.
- Lana agents can delegate to the Claude agent through `task` ([0011](0011-subagent-as-child-session.md)),
  because a child session can use this runtime.

**Negative / tradeoffs**
- The stream-json control messages (`can_use_tool`, `interrupt`) are what the Agent SDK speaks. They are
  not a versioned public spec, so a Claude Code update can break them. Mitigations: pin a minimum
  version checked with `claude --version`, record fixture transcripts, and add contract tests.
- Tools that Claude Code's own settings auto-allow never reach lana's approval chain. lana's `deny`
  rules are therefore a second line of defence, not the only one.
- Two sources of truth for history. Claude Code's transcript drives the model; lana's log is a mirror for
  the UI and search.
- Usage limits are shared with the owner's interactive Claude Code use.

**Neutral**
- `runtime` becomes a new axis on `AgentConfig`. Native agents keep `provider` and leave `runtime`
  empty. Config validation rejects an agent that sets both.

## Alternatives considered

- **ACP through `@agentclientprotocol/claude-agent-acp`.** This is a standard protocol, and Go has an
  SDK (`github.com/coder/acp-go-sdk`). It was not chosen as the first runtime for three reasons: it
  needs Node and an npm package at runtime, it adds a process layer on top of the same CLI, and the
  adapter changes often. The runtime interface is kept generic so `runtime = "acp"` can be added later
  for agents that speak ACP natively (Codex, Cursor, Devin).
- **`kind = "anthropic"` provider with the subscription OAuth token.** This is the omp approach. Rejected
  because it violates the Consumer Terms, needs header spoofing, and risks a ban.
- **`kind = "anthropic"` provider with an API key.** This is allowed and complementary: lana's own loop
  plus Claude models, billed per token. It is out of scope here and tracked separately.
- **Agent SDK sidecar (TypeScript or Python).** This needs a second runtime. Anthropic's docs point
  other languages to the CLI subprocess, which is this decision.
- **Read the OAuth token for a usage footer (MonoCode does this).** Rejected because `rate_limit_event`
  from the stream gives the same data without touching credentials.
