# 0013. Gunakan Go sebagai single binary dan library

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: Menggantikan [0001](0001-gunakan-rust-single-binary-daemon.md). Mengganti detail implementasi Rust di [0002](0002-core-dari-modul-service-dan-hook.md), [0005](0005-sqlite-sebagai-storage-tunggal.md), [0006](0006-ekstensi-lewat-mcp-client-dan-skills.md), [0008](0008-interface-cli-channel-http-dan-web-dashboard.md), [0012](0012-acp-dan-sdk-json-rpc.md). Terkait [0014](0014-integrasi-sebagai-agentic-tool.md).

## Context

ADR 0001 memilih Rust untuk footprint terkecil. Pemilik sekarang ingin `lana` bisa diintegrasikan
dengan service lain sebagai agentic tool (lihat [0014](0014-integrasi-sebagai-agentic-tool.md)),
termasuk di-embed sebagai library oleh service Go. Faktor lain:

- Kecepatan development lebih penting daripada beberapa MB binary atau RAM.
- Ekosistem Go untuk service, MCP (SDK resmi), dan TUI (Bubble Tea) matang.
- Cross-compile dan static binary di Go sederhana jika tanpa cgo.

Pola arsitektur dari ADR 0002 sampai 0012 (modul, service, hook, event log, profile, approval
fail-closed, subagent, protokol) tidak berubah. Yang berubah adalah bahasa dan library.

## Decision

`lana` ditulis dalam **Go** sebagai satu module `github.com/ridhomujizat/lana-harness` yang
menghasilkan satu binary `lana` dan sekaligus dipakai sebagai library.

**Toolchain.** Go dari nixpkgs, dipin oleh `flake.lock`. `go.mod` menulis direktif `go`, dan dev
shell men-set `GOTOOLCHAIN=local` agar Go tidak mengunduh toolchain lain.

**Build.** `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=<v>" ./cmd/lana`.
Target: `darwin/arm64`, `linux/amd64`, `linux/arm64`, semuanya static. Fitur besar (WhatsApp, web
dashboard, A2A) dipilih dengan build tag, menggantikan Cargo feature.

**Target resource v1** (dicatat, tidak menggagalkan CI): binary < 40 MB, RAM idle < 30 MB,
cold start < 150 ms.

**Layout package** (menggantikan layout crate di ADR 0002):

```text
cmd/lana/             main: subcommand dan wiring
lana.go               package lana: API publik untuk embed (ADR 0014)
core/                 Context, Module, hook generik, tipe pesan dan event, interface service
agent/                agent loop (turn dan step), inbox, subagent, compaction
store/                SQLite: event log, projection, memory, cron, lease
providers/openai/     adapter openai_compat
providers/anthropic/  adapter Anthropic (spec berikutnya)
tools/                builtin tools dan permission
mcp/                  MCP client
config/               profile loader berlapis
channels/cli/         TUI dan runner one-shot
channels/telegram/    channel Telegram
protocol/acp/         server ACP
protocol/sdk/         server SDK JSON-RPC
protocol/mcpserver/   lana sebagai MCP server (ADR 0014)
protocol/a2a/         A2A (ADR 0014)
web/                  REST + SSE API dan dashboard (embed.FS)
internal/             helper yang tidak untuk publik
```

Aturan dependency: `core` tidak meng-import package lain di module ini. Package implementasi
meng-import `core`, tidak saling meng-import kecuali lewat interface di `core`. Aturan ini dijaga
oleh test yang memeriksa `go list -deps`.

**Hook di Go** memakai generic, menggantikan contoh trait Rust di ADR 0002:

```go
type Next[I, O any] func(ctx context.Context, in I) (O, error)

type WaterfallListener[I, O any] func(ctx context.Context, in I, next Next[I, O]) (O, error)

type Waterfall[I, O any] struct{ /* listener terurut, handler default */ }

func (w *Waterfall[I, O]) Use(l WaterfallListener[I, O], opts ...UseOption) (release func())
func (w *Waterfall[I, O]) Call(ctx context.Context, in I) (O, error)
```

**Pemetaan library:**

| Kebutuhan | Rust (lama) | Go |
|---|---|---|
| Async runtime | `tokio` | goroutine, `context.Context`, channel |
| HTTP client | `reqwest` + `rustls` | `net/http` |
| HTTP server dan SSE | `axum` | `net/http` (`ServeMux` dengan pola method dan path) |
| SQLite | `rusqlite` bundled | `modernc.org/sqlite` (pure Go, FTS5) |
| TOML dengan posisi | `toml_edit` | `github.com/pelletier/go-toml/v2` (parser `unstable` untuk posisi) |
| CLI | `clap` | `github.com/spf13/cobra` |
| TUI inline | `ratatui` + `crossterm` | `github.com/charmbracelet/bubbletea` tanpa alt screen, `tea.Println` untuk scrollback |
| Editor multi-baris | `tui-textarea` | `github.com/charmbracelet/bubbles/textarea` |
| Render Markdown | `pulldown-cmark` | `github.com/charmbracelet/glamour` (di atas `goldmark`) |
| Wizard | `dialoguer` | `github.com/charmbracelet/huh` |
| Log | `tracing` | `log/slog` |
| MCP | `rmcp` | `github.com/modelcontextprotocol/go-sdk` (client dan server) |
| ID | `ulid` | `github.com/oklog/ulid/v2` |
| Cek PID | `rustix` | `syscall.Kill(pid, 0)` |
| Aset dashboard | `rust-embed` | `embed.FS` |
| Test CLI | `assert_cmd` | `github.com/rogpeppe/go-internal/testscript` |
| Test PTY | `portable-pty` | `github.com/creack/pty` |
| Snapshot | `insta` | golden file di `testdata/` dengan flag `-update` |

Library tambahan dipilih di design spec masing-masing, dengan prioritas stdlib.

## Consequences

**Positive**
- Service Go lain bisa meng-import `lana` langsung (ADR 0014).
- Development dan iterasi lebih cepat; compile hitungan detik.
- Cross-compile static tanpa toolchain C.
- SDK MCP resmi tersedia untuk client dan server.

**Negative / tradeoffs**
- Binary dan RAM lebih besar daripada rencana Rust (target naik dari 15 MB/20 MB ke 40 MB/30 MB).
- GC menambah variasi latensi kecil. Tidak signifikan dibanding latensi LLM.
- Hook generik di Go kurang ekspresif dibanding trait Rust; sebagian pengecekan pindah ke test.
- `modernc.org/sqlite` lebih lambat daripada SQLite C. Cukup untuk pemakaian pribadi.

**Neutral**
- Semua keputusan non-bahasa di ADR 0002 sampai 0012 tetap berlaku.
- Spec #1 dan #2 direvisi untuk mengganti detail Rust.

## Alternatives considered

- **Tetap Rust (ADR 0001)** — paling ringan. Ditolak karena embed sebagai library di service lain dan kecepatan development lebih penting.
- **TypeScript (Bun)** — SDK AI paling lengkap. Ditolak karena butuh runtime dan RAM lebih besar.
- **`mattn/go-sqlite3`** — lebih cepat, tapi butuh cgo sehingga cross-compile dan static build lebih sulit.
