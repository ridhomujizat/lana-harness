# 0020. Offer Claude Code as a router target that runs with lana's context and tools

- **Status**: Accepted
- **Date**: 2026-11-08
- **Deciders**: Ridho
- **Related ADRs**: [0018](0018-claude-code-as-external-agent-runtime.md) (amended), [0019](0019-llm-router-as-embeddable-core.md), [0012](0012-acp-and-sdk-json-rpc.md)
- **Amended**: 2026-11-08 (one-click sign-in, below)

## Context

ADR 0018 added Claude Code as a second agent engine, chosen per agent (`runtime = "claude-code"`). The
owner found the engine choice confusing. They want one engine, lana. They also want the Claude
subscription usable in every lana service (chat, Telegram, delegation, workflows, calendar), with lana's
persona, memory, knowledge, and tools, the way MonoCode's Monos wrap installed agent CLIs.

The terms that shaped ADR 0018 still apply. The subscription may only be used through the unmodified
`claude` binary, so lana cannot use Claude Code as a raw completion model inside its own loop.

## Decision

1. **No engine choice.** Every agent is a lana agent. Claude Code becomes a router target with the
   addresses `claude-code/<model>` (`opus`, `sonnet`, `haiku`, or a full model ID). The Router lists it
   under Subscriptions. `runtime = "claude-code"` with `model = "M"` stays accepted and means
   `model = "claude-code/M"`.
2. **The turn still runs in Claude Code** (ADR 0018 process, credentials, and approval rules unchanged).
   When the first target of a turn's chain is a `claude-code/*` address, lana runs the turn through the
   Claude Code runtime instead of its own loop.
3. **lana supplies the context.** Each Claude process starts with `--append-system-prompt` built from the
   agent's persona, USER.md, memory, and skill list, the same prompt head a native turn gets. Per-turn
   knowledge recall goes in front of the user message, as in native turns.
4. **lana supplies its tools over MCP.** The runtime hosts a loopback MCP server (streamable HTTP on
   `127.0.0.1`, random port, one bearer token per session). It is passed to Claude with `--mcp-config`.
   It exposes lana's own tools allowed for the agent (memory, knowledge, calendar, workflows, sessions,
   `task`, `ask_user`, web search, and the agent's lana MCP servers), not file or shell tools, which Claude
   Code has itself. Calls run through lana's tool runner and permission engine.
5. **Fallback across engines happens only before output.** If the Claude run fails before its first
   text or tool event (not installed, not logged in, plan window rejected, process error), the turn moves
   to the next chain target, which runs in lana's own loop.
6. **Background passes never use Claude Code.** They use the first non-`claude-code` target of the
   agent's chain, else `[router] background_model`, else they are skipped with a warning.

## Consequences

**Positive**
- One mental model: pick a model or a combo. Claude Code sits next to API providers.
- Claude gets lana's memory, knowledge, and services while the subscription stays inside the official
  binary.
- Plan exhaustion can fall back to an API model automatically.

**Negative**
- A loopback MCP server is a new local attack surface; per-session tokens and loopback-only binding
  limit it.
- Agent keys that only affect lana's loop (`max_steps`, compaction, `tools` that Claude Code already
  has) do not apply to Claude Code turns; the UI must say so.

## Alternatives considered

- **Keep the engine choice.** Rejected by the owner as confusing.
- **Use Claude Code as a text-only model inside lana's loop** (`--tools ""`, parse tool calls from text).
  Rejected: it uses the subscription as a model API for a third-party loop, which the terms forbid, and
  it is fragile.
- **A local CLI plus instructions, as MonoCode does.** Works, but Claude Code supports MCP natively, which
  gives typed tools and no prompt overhead.

## Amendment 2026-11-08: one-click sign-in

The owner found adding a Claude Code account too hard: create the account, copy a
`CLAUDE_CONFIG_DIR=… claude` command into a terminal, type `/login`. MonoCode signs agent CLIs in by
starting their own login command, and the terms allow *"an end user signing in to the unmodified Claude
Code binary with their own Claude subscription"* (ADR 0018).

1. **lana may start the official `claude auth login` and `claude auth logout`** for an account folder
   (the built-in account uses the owner's own `~/.claude`), when the owner clicks Sign in, Sign out, or
   Remove in the dashboard. Each run has the account's environment (rule 3), stdin closed, stdout
   discarded, and its own process group. Sign-in waits at most 10 minutes and can be cancelled; sign-out
   waits at most 30 s. One sign-in or sign-out runs per account at a time. Success is exit code 0.
2. **The binary owns the credentials.** It opens the browser, talks to Anthropic, and writes the login
   into the account folder or the Keychain. lana never reads, copies, refreshes, or stores them, and
   still never calls Anthropic endpoints.
3. **Account environment.** Every `claude` lana runs for a named account (turns, status probes, test
   turns, sign-in, sign-out) gets `CLAUDE_CONFIG_DIR=<folder>` and `CLAUDE_SECURESTORAGE_CONFIG_DIR=<folder>`
   (so the macOS Keychain entry is per account too), without `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`,
   and `CLAUDE_CODE_OAUTH_TOKEN`. The built-in account keeps the daemon's folder variables. New folders
   are created with mode 0700.
4. **Nothing secret is relayed.** lana keeps only the last non-empty stderr line of a failed run, with
   every URL replaced by "sign-in link", ANSI and control characters removed, and at most 240
   characters, in memory, to show the owner why the sign-in failed. URLs, OAuth codes, and tokens never
   reach a log, an API answer, or storage.
5. **Removal** signs the account out first, then deletes its folder only when the folder lies inside
   `$LANA_HOME/claude/`; a folder configured elsewhere is never deleted.
6. The terminal command stays available (`CLAUDE_CONFIG_DIR=… CLAUDE_SECURESTORAGE_CONFIG_DIR=… claude
   auth login`) for a daemon on another machine, where the browser would open on the wrong computer.

Consequence: signing out the built-in account signs the owner out of their normal Claude Code, so the
dashboard asks for an explicit confirm and the API needs `{"confirm": true}`.
