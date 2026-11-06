# 0008. Provide CLI, chat channels, HTTP API, and web dashboard interfaces

- **Status**: Accepted (Rust implementation details replaced by [0013](0013-use-go-as-single-binary.md); the HTTP API table is extended by [0014](0014-integration-as-agentic-tool.md))
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0001](0001-use-rust-single-binary-daemon.md), [0004](0004-permission-mode-per-agent.md), [0012](0012-acp-and-sdk-json-rpc.md)

## Context

The owner wants to use agents from the terminal, Telegram, WhatsApp, an HTTP API/webhook, and the web.
Every interface must be able to display streamed answers and respond to approvals. A full-featured bot library
(for example `teloxide`) adds a fairly large amount to the binary size.

## Decision

Every interface is a module (see [0002](0002-core-from-modules-services-and-hooks.md)) that sends messages to
`ctx.agents`, reads replies from the `session.event` hook, and installs a responder in the
`approval.request` hook. The HTTP interfaces share one `axum` server at `127.0.0.1:18300`
(default, configurable). ACP and SDK JSON-RPC are covered in [0012](0012-acp-and-sdk-json-rpc.md).

| Channel | v1 implementation | Streaming | Approval |
|---|---|---|---|
| CLI/TUI | `lana chat`: inline TUI with `ratatui` (`Viewport::Inline`) and `crossterm`, like Claude Code. History goes into terminal scrollback; only the input and status bar are redrawn. Always compiled. v1 runs embedded; attaching to the daemon over HTTP+SSE comes later. Details in [spec #1](../specs/1-cli-tui/requirements.md). | Token by token | Prompt `[y] / [a] / [n]` |
| Telegram | Bot API directly through `reqwest`, long polling `getUpdates` (no public webhook). | Periodic message edits (throttled to 1 second) | Inline keyboard |
| WhatsApp | Official WhatsApp Cloud API (incoming webhook + outgoing Graph API), `whatsapp` feature. Requires a public URL (tunnel/reverse proxy). | Final message only | Interactive reply button |
| HTTP API | `POST /v1/agents/{agent}/messages`, `GET /v1/sessions/{id}/events` (SSE), `POST /v1/webhooks/{hook}` for triggers from other systems. Bearer token auth. | SSE | `POST /v1/approvals/{id}` |
| Web dashboard | Static assets (Preact + `htm`, no build step) embedded via `rust-embed`, using the same HTTP API. Pages (spec #9 rev 2, hash routes): Home (`#/home`), Sessions (`#/sessions`, `#/sessions/new`, `#/sessions/{id}`), Agents (`#/agents`), Skills (`#/skills`), MCP (`#/mcp`), Channels (`#/channels`), Settings (`#/settings/{general,providers,secrets,config,audit}`), and the first-run setup wizard (`#/setup/{step}`). | SSE | Inline card above the composer |

Interface security rules:

- The HTTP server binds to `127.0.0.1` by default. Binding to `0.0.0.0` requires `api_token` in the config.
- Dashboard login uses the token created by `lana init` and stored as a hash.
- Telegram and WhatsApp only accept messages from IDs in the `[[routes]]` allowlist.
- WhatsApp webhooks are verified with the `X-Hub-Signature-256` signature.

## Consequences

**Positive**
- A single internal event stream (`ChatEvent`) is reused by all interfaces.
- Telegram works without a public webhook, suitable for a laptop behind NAT.
- The dashboard needs no Node toolchain; assets are under 200 KB.

**Negative / tradeoffs**
- The WhatsApp Cloud API requires a Meta Business account and a public URL.
- Writing our own Bot API client means we maintain the subset of the API we use ourselves.

**Neutral**
- Discord, Slack, and email can be added later as new `Channel`s.

## Alternatives considered

- **`teloxide`** — complete, but adds many dependencies. Rejected for v1.
- **WhatsApp through an unofficial bridge (whatsmeow/Baileys)** — no business account needed, but violates the ToS and requires a Go/Node runtime. Rejected.
- **React + Vite dashboard** — good DX, but needs a Node build step. Rejected for v1.
- **Desktop app (Tauri)** — rejected because the web dashboard is sufficient and lighter.
