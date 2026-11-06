# 0001. Use Rust as a single-binary daemon

- **Status**: Superseded by [0013](0013-use-go-as-single-binary.md)
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0002](0002-core-from-modules-services-and-hooks.md), [0008](0008-cli-channel-http-and-web-dashboard-interfaces.md)

## Context

`lana-harness` is a personal agent harness for coding assistants, personal assistants, and
automation/ops. Main references:

- **zeroclaw** — Rust, one binary, one agent loop for all channels, TOML config,
  SQLite memory, supervised security policy.
- **nouride** — one multi-agent daemon, chat channels (Telegram/WhatsApp/Discord), approval gate,
  scheduled work, web dashboard, data in one folder (`.nouride/`).
- **deepseek-harness (`dsh`)** — reference for **how it works**: every part is a module that
  registers services and hooks, sessions as an append-only event log, profiles and patches,
  subagents, ACP server and SDK. `dsh` is written in TypeScript; `lana` takes its patterns, not its runtime.

Main requirement: **lightweight**. The harness must run on a laptop (macOS arm64) and on a small
VPS (Linux x86_64/aarch64) without additional runtimes (Node, Python, JVM).

Constraints:

- One process serves the CLI, chat channels, HTTP API, web dashboard, and scheduler.
- Development uses Nix (`flake.nix`), not global installs.
- v1 resource targets: release binary < 15 MB, idle RAM < 20 MB, cold start < 100 ms.

## Decision

We build `lana` as **a single Rust binary** (edition 2024) with the `tokio` async runtime.
The same binary runs several modes through subcommands:

| Subcommand | Function |
|---|---|
| `lana daemon` | Runs all channels, HTTP API, web dashboard, and scheduler |
| `lana chat [--agent X]` | Local TUI chat (attaches to the daemon over HTTP, or embedded if the daemon is down) |
| `lana run --agent X "prompt"` | One-shot, for scripts and pipes |
| `lana config check` | Validates `config.toml` |
| `lana service install` | Installs a LaunchAgent (macOS) or systemd user unit (Linux) |

All state lives in one data folder: `~/.lana/` (`config.toml`, `lana.db`, `skills/`,
`workspaces/`, `logs/`). Override via the `LANA_HOME` env var.

Dependencies are chosen to stay lightweight:

- HTTP client: `reqwest` with `rustls` (no OpenSSL).
- HTTP server: `axum`.
- Storage: `rusqlite` with the `bundled` feature (see [0005](0005-sqlite-as-single-storage.md)).
- Serialization: `serde`, `serde_json`, `toml`.
- Release profile: `opt-level = "z"`, `lto = "fat"`, `codegen-units = 1`, `panic = "abort"`, `strip = true`.
- Large features (WhatsApp, web dashboard) sit behind Cargo feature flags.

## Consequences

**Positive**
- No external runtime. Deployment is just copying one file.
- Small, predictable memory footprint (no GC).
- Static `musl` binary makes `scratch`/`distroless` Docker images easy.

**Negative / tradeoffs**
- Slow compilation, especially release builds with LTO.
- The Rust AI SDK ecosystem is thinner than TypeScript/Python. We write our own provider adapters.
- Steeper learning curve for async Rust (lifetimes, `Send + Sync`).

**Neutral**
- The Rust toolchain is provided via `flake.nix` (`rust-overlay` or `fenix`), not a global `rustup`.

## Alternatives considered

- **Go** — binary and RAM are also small, and development is faster. Rejected because of the owner's
  preference and because the RAM target is lower without a GC.
- **TypeScript (Bun/Node)** — the most complete SDKs. Rejected because idle RAM is > 50 MB and it needs a runtime.
- **Fork zeroclaw** — full-featured, but a large codebase (30+ channels, hardware support). Rejected because
  the goal of the project is a small harness that its owner fully understands.
