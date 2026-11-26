# Spec 78: Claude Code and Antigravity models through the Router endpoint

Status: approved (owner, 2026-11-26) · Branch: feature/workbench-router-accounts (stacked on spec 77)

## Context

The Router's OpenAI-compatible endpoint (`/router/v1/chat/completions`, `/router/v1/models`,
`router/openaiproxy`) serves API providers only. Claude Code targets fail in `Router.attempt` ("claude-code
targets run in the Claude Code runtime, not through the router", ADR 0020) and Antigravity addresses answer
`model_not_found` (ADR 0022, AC-AGY-016). The owner wants any OpenAI-compatible client to use the Claude Code
and Antigravity accounts of the Router, like `cliproxy-antigravity` (CLIProxyAPI plugin) does for agy: run the
official CLI headless, never touch its tokens.

Owner decision (2026-11-26): "ya di buat bisa aja agy dan claude code. tidak perlu opt in tersebut" — no
opt-in toggle, no per-account switch, no extra rate limit. The owner was told the Google and Anthropic terms
risk of serving subscription accounts to other clients and accepted it.

This supersedes the proxy part of ADR 0020 and ADR 0022 (new ADR 0023). Agent turns keep their own runtimes.

## Requirements

### REQ-RPR-001: Models listed

`GET /models` lists, after the combos and API providers, every enabled Claude Code account's models as
`<account>/<model>` (`owned_by` `"claude-code"`) and every enabled Antigravity account's models as
`<account>/<model>` (`owned_by` `"antigravity"`), from the same model lists the agent pickers use
(`Router.Models`). An account whose CLI is not installed lists nothing. A disabled account lists nothing.

### REQ-RPR-002: Calls run the official CLI

A proxy call whose target is a Claude Code or Antigravity account (directly, or as a combo target) runs one
headless CLI process for that account and streams its answer back. Only calls with `CallMeta.Source ==
"proxy"` run this way; for any other source `Router.attempt` keeps today's not-found error, so a lana agent
chain with tools never falls through to a text-only CLI call.

Claude Code: `claude -p --output-format stream-json --verbose --include-partial-messages --model <model>
--tools "" --strict-mcp-config --setting-sources project,local --no-session-persistence`, plus `--system-prompt <system text>` when the
request has system or developer messages, plus `--effort <level>` when `reasoning_effort` is set. The
account's environment is the one its agent turns use (named account: `CLAUDE_CONFIG_DIR` and
`CLAUDE_SECURESTORAGE_CONFIG_DIR` = its config dir; built-in: inherited). No built-in tools, no MCP, nothing
saved to `/resume` history.

Antigravity: `agy --input-format stream-json --output-format stream-json --disable-slash-commands --model <model> --sandbox` (the prompt as one stdin message `{"event":"user","message":{"content":…}}`; agy 1.5.17 `-p` takes the next argument as its prompt), plus `--effort <level>` when set,
with the account's `HOME` and environment as its agent turns use them (including the file-token setting of
added accounts). Never `--dangerously-skip-permissions`; agy's tools get no approval.

Both: the prompt goes in on stdin, never as an argument. The working directory is a new empty directory under
`<LANA_HOME>/router/proxy-work/`, removed after the call. The process runs in its own process group and is
killed (group) when the client goes away or the call's context ends. Lana never reads the CLI's tokens or
Keychain items and never calls Anthropic or Google endpoints itself.

### REQ-RPR-003: Request mapping

The messages become one prompt: system and developer messages join (blank line between) into the system text
(Claude Code: `--system-prompt`; Antigravity: a leading "System instructions:" block of the prompt). With one
user message and no other turns, the prompt is its text. Otherwise the prompt is the transcript, each message
as `User:` / `Assistant:` followed by its text, ending with the last user message, then
"Answer the last User message." on its own line.

Text content parts are joined; any other part (image, audio, file) or a request with `tools`, `tool_choice`
other than `"none"`, or a `tool` role message makes the target fail with class `client` and message "tool
calls and non-text input are not supported for claude-code and antigravity models"; a combo moves on to its
next target, a direct address answers 400 `invalid_request_error`. `temperature`, `top_p`, `max_tokens`,
`stop`, and similar sampling fields are ignored.

### REQ-RPR-004: Response mapping

Streaming: each text delta of the CLI stream becomes a `chat.completion.chunk` `delta.content`; thinking is
not sent. The last chunk has `finish_reason` `"stop"` and, when the client asked for
`stream_options.include_usage`, the usage. Non-streaming: one `chat.completion` with the whole text. Usage
maps the CLI's input, output, and cache-read tokens to `prompt_tokens` (input + cache read),
`completion_tokens`, `total_tokens`. `model` echoes the requested name, as for API providers.

### REQ-RPR-005: Errors and failover

A CLI exit or stream error maps to the router error classes so combos and pools behave as for API providers:
usage or rate limit reached → `rate_limit` (the account cools down until the reset time when the CLI reports
one, else the default cooldown); not signed in / auth failure → `auth`; CLI not installed or account disabled
→ `not_found`; unknown model → `client`; anything else → `server`. The error message is the CLI's own short
message, never its full stderr, and never includes tokens or environment values. Pool failover (spec 75)
applies to proxy calls the same way it applies to agent turns.

### REQ-RPR-006: Shared limits and records

A proxy call takes a slot of the account's concurrency limit (the same counter agent turns use), so the Router
pages count it in running/queued and a busy account queues it. Each call is recorded like other proxy calls
(`Source` "proxy", the key id as agent, tokens from REQ-RPR-004, cost 0) and shows in the Router's call log and
stats. After a call the account's usage windows are refreshed the same way they are after an agent turn.

### REQ-RPR-007: ADR

`docs/adr/0023-claude-code-and-antigravity-through-the-router-endpoint.md` records the decision, its risk
(subscription accounts served to other local or Tailscale clients; Anthropic and Google may restrict accounts),
and supersedes the proxy clauses of ADR 0020 and ADR 0022 (their agent runtime clauses stay). Both ADRs get a
"Superseded in part by ADR 0023" line.

### REQ-RPR-008: Avatars (scope addition, owner 2026-11-26)

Avatar generation may use Claude Code and Antigravity models through the same CLI streamer: an explicit
model address or combo is used as it is (no external target is refused or stripped), and the default (no
model) is the agent's background chain (REQ-RPR-009). The avatar call is text only (system text and
messages, effort "off", so no `--effort`). One avatar model call gets 120 s, enough for a CLI start. The
avatar dialog lists Claude Code and Antigravity models and combos that start on one.

### REQ-RPR-009: All background work (scope addition, owner 2026-11-26)

`Router.attempt` runs the CLI streamer for every `CallMeta.Source`, not only `proxy` and `avatar`. A
request with tools (unless `tool_choice` is `"none"`), tool calls or results, or non-text parts still
fails that target with class `client` (REQ-RPR-003), so the chain moves on. Agent turns keep being
dispatched to their runtimes before the router.

`llmcall.BackgroundChain` keeps Claude Code and Antigravity targets: the agent's chain is used as it is,
and `[router] background_model` is the fallback only when the chain is empty. Titles, knowledge passes,
and the avatar default model run on Claude Code or Antigravity through the streamer when that is the
agent's model. Compaction and memory capture send tools and tool blocks, which the CLIs do not serve, so
they use `llmcall.ToolBackgroundChain`: the chain without its Claude Code and Antigravity targets, else
`[router] background_model`, as before spec 78.

A workflow guardian's structured call (assistant graph edits, failure diagnosis; `RunAgent` with
`Background`) runs as an ordinary turn on the guardian's own chain: in Claude Code or Antigravity when the
chain starts there, with lana's tools over MCP and the workflow session's tool gate applied. It is not a
scheduled run, so `allow_scheduled` does not apply; scheduled workflow node runs keep it. The 503
`guardian_needs_background_model` remains only when no chain resolves at all.

This supersedes spec 39 REQ-CCT-007 and API-CCT-028 and spec 74 REQ-AGY-008's "never in an external
runtime" for background work (ADR 0023).

## Acceptance

- AC-RPR-001: `GET /router/v1/models` lists `claude-code/<model>` and `antigravity/<model>` (and named accounts)
  when the CLIs are installed; tests use the fake CLIs.
- AC-RPR-002: a streaming and a non-streaming call to `claude-code/<model>` and to `antigravity/<model>` answer
  with the fake CLI's text and usage; the fake CLI saw the flags of REQ-RPR-002, the prompt on stdin, the
  account env, and an empty work dir that is gone afterwards.
- AC-RPR-003: a request with `tools` to a Claude Code address answers 400; a combo `[claude-code/x, openai/y]`
  with tools answers from `openai/y`.
- AC-RPR-004: a fake CLI limit error cools the account and a pool/combo moves on.
- AC-RPR-005: client disconnect kills the fake CLI process.
- AC-RPR-006 (revised by REQ-RPR-009): without a runtime streamer a Claude Code target fails as before
  (`not_found`); with one, a call of any source runs the CLI when it is text only, and a call with tools
  fails that target with `client` and the chain moves on.
- AC-RPR-007: an avatar of an explicit `claude-code/<model>` or `antigravity/<model>` model calls that
  model through the router; the avatar dialog lists Claude Code and Antigravity models.
- AC-RPR-008: a workflow guardian on `claude-code/<model>` runs its failure diagnosis in Claude Code
  without a background model or `allow_scheduled`; a guardian combo whose Claude Code target fails before
  output answers from the combo's API model.
- `go test ./...`, `golangci-lint run`, web tests green.

## Implementation notes (2026-11-26)

- Agy's `stream-json` event shapes are not documented beyond the changelog (typed `init`, `step_update`,
  terminal `result`). lana reads text deltas from a `step_update`'s `text_delta` (or `textDelta`, or
  the same field of a nested `step` / `step_update` object) and always reads the terminal `result`
  (`status`, `response`, `usage`, `finished_reason`, `error`); when no delta was recognized the whole
  `response` is sent as one chunk. `AGY_ERROR:` lines are read as the error. A run where agy starts its
  own sign-in ("No valid authentication found … Starting login") is killed and answers `auth`.
- `--model` is left out for the Antigravity model `default` (agy's current model).
- Claude Code usage: `prompt_tokens` counts input, cache-write, and cache-read tokens (all of them are
  prompt tokens), with cache-read as `cached_tokens`, as for agent turns.
- A CLI too old for lana answers `not_found` (it cannot serve), like a missing CLI. `missing_secret` (an
  `api_key` account without its key) keeps its router class.
- A usage limit cools the target (account and model) and the chain moves on at once, without the
  rate-limit retries of API providers. A pooled address fails over only on `auth` or `rate_limit`; any
  other failure skips the rest of its pool (spec 75 REQ-PAF-001).

## Out of scope

Tool-call emulation, images and files, the Anthropic `/v1/messages` entry, conversation resume across proxy
calls, a UI switch.
