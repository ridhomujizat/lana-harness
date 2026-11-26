# 0023. Serve Claude Code and Antigravity models through the router as headless CLI calls

- **Status**: Accepted
- **Date**: 2026-11-26
- **Deciders**: Ridho
- **Related ADRs**: [0020](0020-claude-code-as-router-target-with-lana-context.md) (superseded in part), [0022](0022-antigravity-as-router-target-through-agy-acp.md) (superseded in part), [0019](0019-llm-router-as-embeddable-core.md), [0018](0018-claude-code-as-external-agent-runtime.md)
- **Spec**: [78](../specs/78-router-proxy-runtimes/requirements.md)

## Context

ADR 0020 and ADR 0022 made Claude Code and Antigravity accounts router targets, but only for agent turns:
the agent dispatches them to their runtimes before the router, the router answers `not_found` for them,
the OpenAI-compatible endpoint (`/router/v1`) refuses Antigravity addresses (AC-AGY-016), and background
passes (titles, compaction, memory capture, knowledge, avatars, workflow guardian calls) never run in
them (REQ-CCT-007, REQ-AGY-008, spec 39 API-CCT-028).

The owner wants any OpenAI-compatible client to use the Router's Claude Code and Antigravity accounts,
the way `cliproxy-antigravity` serves agy: run the official CLI headless and never touch its tokens
(2026-11-26: "ya di buat bisa aja agy dan claude code. tidak perlu opt in tersebut"). The same day the
owner asked for avatar generation and all background work to use those models too.

## Decision

1. **A router call on a Claude Code or Antigravity account runs one headless CLI process.** The router
   gets a `RuntimeStreamer` from lana. For every call source (the proxy endpoint, avatars, background
   passes) a Claude Code target runs `claude -p --output-format stream-json --verbose
   --include-partial-messages --model <m> --tools "" --strict-mcp-config --no-session-persistence`
   (plus `--system-prompt`, `--effort`), and an Antigravity target runs `agy -p --output-format
   stream-json --model <m> --sandbox` (plus `--effort`). The prompt goes in on stdin; the working
   directory is a new empty folder under `<LANA_HOME>/router/proxy-work/`, removed afterwards; the
   process group is killed when the caller goes away. Never `--dangerously-skip-permissions`.
2. **Text only.** Messages become one prompt (system text apart for Claude Code, a "System
   instructions:" block for Antigravity, a `User:`/`Assistant:` transcript for a conversation). A request
   with tools (unless `tool_choice` is `"none"`), tool calls or results, or images fails that target with
   class `client`, and the chain moves on to its next target.
3. **Same accounts, limits, and records as agent turns.** The CLI gets the account's environment
   (`CLAUDE_CONFIG_DIR` and `CLAUDE_SECURESTORAGE_CONFIG_DIR`, or the account `HOME` with the file-token
   setting of added accounts) and takes a slot of the runtime's concurrency limit. Errors map to router
   classes (limit → `rate_limit`, cooling the account until the reported reset; not signed in → `auth`;
   CLI missing or account disabled → `not_found`; unknown model → `client`; else `server`). Pooled
   addresses fail over across their accounts (spec 75). Calls are recorded with cost 0.
4. **No opt-in.** No toggle, no per-account switch, no extra rate limit. `GET /models` lists every
   enabled account whose CLI is found.
5. **Background work uses the agent's chain as it is.** `llmcall.BackgroundChain` keeps Claude Code and
   Antigravity targets; `[router] background_model` is used only when the chain is empty. A workflow
   guardian's structured call (spec 39 API-CCT-028) runs as an ordinary turn on the guardian's chain,
   in Claude Code or Antigravity when the chain starts there, with the workflow tool gate; it is not a
   scheduled run. Workflow node runs keep `allow_scheduled`.
6. **Agent turns are unchanged.** They still run in their runtimes with lana's context and tools (ADR
   0020 items 1 to 5, ADR 0022 items 1 to 4 and 6).

This supersedes ADR 0020 item 6 (background passes never use Claude Code) and the proxy clause of ADR
0022 item 5 (Antigravity addresses are never served through the proxy). Their agent runtime clauses
stay.

## Consequences

**Positive**
- Any OpenAI-compatible tool on the Mac or the tailnet can use the owner's subscriptions through one
  endpoint, with the router's combos, failover, cooldown, and call log.
- An agent that runs only on Claude Code or Antigravity gets titles, knowledge passes, avatars, and
  guardian calls without a separate API model.

**Negative**
- **Terms risk.** Subscription accounts are served to other local or Tailscale clients. Anthropic and
  Google may restrict or close accounts used this way. The owner was told and accepted it.
- Each call starts a CLI process: seconds of start-up, and the CLI's own system prompt and token use.
- Compaction and memory capture send tools and tool blocks, so a Claude Code or Antigravity target
  refuses them; an agent whose chain has only such targets gets no compaction summary or memory capture
  from that chain.
- agy's stream-json event shapes are not documented in detail; lana reads the terminal result event and
  any step text deltas it recognizes, so streaming may arrive as one chunk at the end.

## Alternatives considered

- **Call Anthropic and Google endpoints with the CLI's tokens.** Faster, but lana would read and send
  subscription credentials, which ADR 0018, 0020, and 0022 rule out.
- **An opt-in per account.** The owner declined it.
- **Keep background work off the CLIs.** Kept the API-model requirement the owner asked to drop.
