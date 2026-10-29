# 0013. Use Go as a single binary and library

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: Supersedes [0001](0001-use-rust-single-binary-daemon.md). Replaces the Rust implementation details in [0002](0002-core-from-modules-services-and-hooks.md), [0005](0005-sqlite-as-single-storage.md), [0006](0006-extend-via-mcp-client-and-skills.md), [0008](0008-cli-channel-http-and-web-dashboard-interfaces.md), [0012](0012-acp-and-sdk-json-rpc.md). Related to [0014](0014-integration-as-agentic-tool.md).

## Context

ADR 0001 chose Rust for the smallest footprint. The owner now wants `lana` to be integrable with other services as an
agentic tool (see [0014](0014-integration-as-agentic-tool.md)), including being embedded as a library by Go
services. Other factors:

- Development speed matters more than a few MB of binary size or RAM.
- The Go ecosystem for services, MCP (official SDK), and TUI (Bubble Tea) is mature.
- Cross-compilation and static binaries in Go are simple when cgo is not used.

The architectural patterns from ADR 0002 through 0012 (modules, services, hooks, event log, profiles, fail-closed
approval, subagents, protocols) do not change. What changes is the language and the libraries.

## Decision

`lana` is written in **Go** as a single module `github.com/ridhomujizat/lana-harness` that produces one `lana`
binary and is also used as a library.

**Toolchain.** Go from nixpkgs, pinned by `flake.lock`. `go.mod` declares the `go` directive, and the dev
shell sets `GOTOOLCHAIN=local` so Go does not download another toolchain.

**Build.** `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=<v>" ./cmd/lana`.
Targets: `darwin/arm64`, `linux/amd64`, `linux/arm64`, all static. Large features (WhatsApp, web
dashboard, A2A) are selected with build tags, replacing Cargo features.

**v1 resource targets** (recorded, do not fail CI): binary < 40 MB, idle RAM < 30 MB,
cold start < 150 ms.

**Package layout** (replaces the crate layout in ADR 0002):

```text
cmd/lana/             main: subcommands and wiring
lana.go               package lana: public API for embedding (ADR 0014)
core/                 Context, Module, generic hooks, message and event types, service interfaces
agent/                agent loop (turn and step), inbox, subagents, compaction
store/                SQLite: event log, projections, memory, cron, lease
providers/openai/     openai_compat adapter
providers/anthropic/  Anthropic adapter (next spec)
tools/                builtin tools and permissions
mcp/                  MCP client
config/               layered profile loader
channels/cli/         TUI and one-shot runner
channels/telegram/    Telegram channel
protocol/acp/         ACP server
protocol/sdk/         SDK JSON-RPC server
protocol/mcpserver/   lana as an MCP server (ADR 0014)
protocol/a2a/         A2A (ADR 0014)
web/                  REST + SSE API and dashboard (embed.FS)
internal/             helpers that are not public
```

Dependency rule: `core` does not import other packages in this module. Implementation packages import `core`
and do not import each other except through interfaces in `core`. This rule is enforced by a test that inspects
`go list -deps`.

**Hooks in Go** use generics, replacing the Rust trait examples in ADR 0002:

```go
type Next[I, O any] func(ctx context.Context, in I) (O, error)

type WaterfallListener[I, O any] func(ctx context.Context, in I, next Next[I, O]) (O, error)

type Waterfall[I, O any] struct{ /* ordered listeners, default handler */ }

func (w *Waterfall[I, O]) Use(l WaterfallListener[I, O], opts ...UseOption) (release func())
func (w *Waterfall[I, O]) Call(ctx context.Context, in I) (O, error)
```

**Library mapping:**

| Need | Rust (old) | Go |
|---|---|---|
| Async runtime | `tokio` | goroutines, `context.Context`, channels |
| HTTP client | `reqwest` + `rustls` | `net/http` |
| HTTP server and SSE | `axum` | `net/http` (`ServeMux` with method and path patterns) |
| SQLite | `rusqlite` bundled | `modernc.org/sqlite` (pure Go, FTS5) |
| TOML with positions | `toml_edit` | `github.com/pelletier/go-toml/v2` (`unstable` parser for positions) |
| CLI | `clap` | `github.com/spf13/cobra` |
| Inline TUI | `ratatui` + `crossterm` | `github.com/charmbracelet/bubbletea` without alt screen, `tea.Println` for scrollback |
| Multi-line editor | `tui-textarea` | `github.com/charmbracelet/bubbles/textarea` |
| Markdown rendering | `pulldown-cmark` | `github.com/charmbracelet/glamour` (on top of `goldmark`) |
| Wizard | `dialoguer` | `github.com/charmbracelet/huh` |
| Logging | `tracing` | `log/slog` |
| MCP | `rmcp` | `github.com/modelcontextprotocol/go-sdk` (client and server) |
| IDs | `ulid` | `github.com/oklog/ulid/v2` |
| PID check | `rustix` | `syscall.Kill(pid, 0)` |
| Dashboard assets | `rust-embed` | `embed.FS` |
| CLI tests | `assert_cmd` | `github.com/rogpeppe/go-internal/testscript` |
| PTY tests | `portable-pty` | `github.com/creack/pty` |
| Snapshots | `insta` | golden files in `testdata/` with an `-update` flag |

Additional libraries are chosen in each design spec, with the standard library preferred.

## Consequences

**Positive**
- Other Go services can import `lana` directly (ADR 0014).
- Development and iteration are faster; compilation takes seconds.
- Static cross-compilation without a C toolchain.
- The official MCP SDK is available for both client and server.

**Negative / tradeoffs**
- The binary and RAM are larger than the Rust plan (targets raised from 15 MB/20 MB to 40 MB/30 MB).
- The GC adds a small amount of latency variation. Not significant compared to LLM latency.
- Generic hooks in Go are less expressive than Rust traits; some checks move into tests.
- `modernc.org/sqlite` is slower than the C SQLite. Sufficient for personal use.

**Neutral**
- All non-language decisions in ADR 0002 through 0012 still apply.
- Specs #1 and #2 are revised to replace the Rust details.

## Alternatives considered

- **Stay with Rust (ADR 0001)** — the lightest option. Rejected because of embedding as a library in other services and because development speed matters more.
- **TypeScript (Bun)** — the most complete AI SDKs. Rejected because it needs a runtime and more RAM.
- **`mattn/go-sqlite3`** — faster, but needs cgo, which makes cross-compilation and static builds harder.
