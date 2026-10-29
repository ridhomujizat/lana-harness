# Tasks: CLI dan TUI chat

- **Issue**: [`#1`](https://github.com/ridhomujizat/lana-harness/issues/1)
- **Requirements**: [requirements.md](requirements.md)
- **Contract**: [api-contract.md](api-contract.md) — Diperlukan
- **Design**: [design.md](design.md)
- **Revisi requirements**: 5
- **Revisi contract**: 3
- **Revisi design**: 2
- **Status**: Draft (revisi 2: pindah ke Go, ADR 0013)

> **Diblokir oleh spec #2, #3, dan #4 (core).** Semua task di bawah memakai interface core yang tercantum di
> [design: Interface core yang dibutuhkan](design.md#interface-core-yang-dibutuhkan-dari-spec-2-3-4).
> Task boleh mulai setelah task spec yang menyediakan interface tersebut berstatus Completed.

## Aturan eksekusi

- Implementasi hanya requirement dan acceptance criteria yang ditugaskan ke task.
- Berhenti jika requirements, design, ADR, diagram, atau contract saling bertentangan.
- Jangan menjawab pertanyaan produk atau arsitektur hanya di kode.
- Satu acceptance criterion punya tepat satu pemilik bukti.
- Verifikasi yang diwajibkan tidak pernah opsional.
- Tandai task selesai hanya setelah bukti verifikasi dicatat.
- Semua perintah dijalankan di dalam `nix develop`.

## Cakupan

| Acceptance criterion | Operasi contract | Pemilik bukti | Verifikasi | Bukti |
|---|---|---|---|---|
| `AC-INIT-001` sampai `AC-INIT-004` | `API-CLI-008` | `T-002` | `testscript init_*.txtar` | Pending |
| `AC-CONF-001` sampai `AC-CONF-006` | `API-CLI-001`, `009`, `010`, `011` | `T-003` | `testscript config_*.txtar` | Pending |
| `AC-SESS-001` sampai `AC-SESS-007` | `API-CLI-002` | `T-004` | `TestChatSessions` | Pending |
| `AC-CHAT-022` | `API-CLI-002` | `T-004` | `TestChatSessions/workspace_is_cwd` | Pending |
| `AC-CHAT-001` sampai `AC-CHAT-008` | `API-CLI-002`, `API-CLI-003` | `T-005` | `TestChatAgentSelect`, `TestChatEmbedded`, golden status bar | Pending |
| `AC-CHAT-023` | `API-CLI-002` | `T-005` | `testscript chat_requires_tty.txtar` | Pending |
| `AC-CHAT-009`, `AC-CHAT-010`, `AC-CHAT-011`, `AC-CHAT-024` | `API-CLI-003`, `API-CLI-004` | `T-006` | unit `render` Markdown streaming, golden ToolLine | Pending |
| `AC-CHAT-012` sampai `AC-CHAT-021` | `API-CLI-004` | `T-007` | unit `input` dan model tui; PTY untuk `AC-CHAT-012` | Pending |
| `AC-APPR-001` sampai `AC-APPR-007` | `API-CLI-002`, `API-CLI-005` | `T-008` | unit `TuiAnswerer`, `TestChatPolicy` | Pending |
| `AC-CMD-001` sampai `AC-CMD-014` | `API-CLI-006` | `T-009` | unit `commands`, `TestChatCommands` | Pending |
| `AC-RUN-001` sampai `AC-RUN-010` | `API-CLI-007` | `T-010` | `testscript run_*.txtar` | Pending |
| `AC-EXIT-001` sampai `AC-EXIT-003` | `API-CLI-001`, `API-CLI-007` | `T-010` | `testscript run_exit_codes.txtar` | Pending |

## Graf task

```mermaid
flowchart LR
  Core["Spec 2: core"] --> T1
  T1["T-001: Kerangka CLI"] --> T2["T-002: lana init"]
  T1 --> T3["T-003: lana config"]
  T1 --> T4["T-004: Session CLI dan lock"]
  T4 --> T5["T-005: Kerangka TUI"]
  T5 --> T6["T-006: Streaming dan tool call"]
  T5 --> T7["T-007: Input dan tombol"]
  T6 --> T8["T-008: Approval dan yolo"]
  T7 --> T8
  T8 --> T9["T-009: Slash command"]
  T4 --> T10["T-010: lana run"]
  T2 --> T11["T-011: Verifikasi akhir"]
  T3 --> T11
  T9 --> T11
  T10 --> T11
```

## Tasks

- [ ] `T-001` Buat kerangka CLI, error, dan logging
  - Issue/PR: `—`
  - Bergantung pada: spec #2 (Go module dan config loader)
  - Requirements: `REQ-EXIT-001` (sebagian)
  - Acceptance criteria: — (pendukung; bukti `AC-EXIT-*` dimiliki `T-010`)
  - Operasi contract: `API-CLI-001`
  - Scope:
    - Perintah `cobra` untuk flag global dan subcommand `chat`, `run`, `init`, `config`.
    - `ExitError` dan pemetaan exit code sesuai design.
    - Logging `slog`: file untuk `chat`, stderr untuk lainnya, `-v` dan `LANA_LOG`.
    - Pesan error diawali `error: ` di stderr.
  - Verifikasi:
    - `go test ./cmd/lana/...` (unit pemetaan exit code dan parsing flag)
    - `lana --help` dan `lana --version` exit 0
  - Bukti penyelesaian:
    - Status: Pending
    - Perintah/test: —
    - Hasil: —
    - PR/commit: —
    - Deviasi: Tidak ada

- [ ] `T-002` Implementasi `lana init`
  - Issue/PR: `—`
  - Bergantung pada: `T-001`
  - Requirements: `REQ-INIT-001`
  - Acceptance criteria: `AC-INIT-001`, `AC-INIT-002`, `AC-INIT-003`, `AC-INIT-004`
  - Operasi contract: `API-CLI-008`
  - Scope:
    - Wizard `huh` dengan tiga pertanyaan dan default per provider.
    - Penulisan atomik; cek semua path dulu tanpa `--force`.
    - `secrets.env` dengan mode `0600` sejak dibuat.
    - Isi `agents/default.md`.
  - Verifikasi:
    - `go test ./cmd/lana -run TestScript/init`
  - Bukti penyelesaian:
    - Status: Pending
    - Perintah/test: —
    - Hasil: —
    - PR/commit: —
    - Deviasi: Tidak ada

- [ ] `T-003` Implementasi `lana config` dan `--dump-config`
  - Issue/PR: `—`
  - Bergantung pada: `T-001`
  - Requirements: `REQ-CONF-001`
  - Acceptance criteria: `AC-CONF-001` sampai `AC-CONF-006`
  - Operasi contract: `API-CLI-001`, `API-CLI-009`, `API-CLI-010`, `API-CLI-011`
  - Scope:
    - `check` memakai error ber-posisi dari loader spec #2, semua error sekaligus.
    - `path`, `edit` (`$VISUAL` lalu `$EDITOR`, dipecah `shlex`).
    - `--dump-config=toml|json` dengan komentar asal dan redaksi `Secret`.
  - Verifikasi:
    - `go test ./cmd/lana -run TestScript/config`
  - Bukti penyelesaian:
    - Status: Pending
    - Perintah/test: —
    - Hasil: —
    - PR/commit: —
    - Deviasi: Tidak ada

- [ ] `T-004` Implementasi session CLI dan lock
  - Issue/PR: `—`
  - Bergantung pada: `T-001`, spec #4 (`ctx.Sessions`, `session_leases`)
  - Requirements: `REQ-SESS-001`, `REQ-SESS-002`, `REQ-CHAT-010`
  - Acceptance criteria: `AC-SESS-001` sampai `AC-SESS-007`, `AC-CHAT-022`
  - Operasi contract: `API-CLI-002`
  - Scope:
    - `CliSessions`: address = cwd canonical, `--continue`, `--resume` dengan prefix ULID, cek cwd asal.
    - Lease dengan `BEGIN IMMEDIATE`, cek PID lewat `syscall.Kill(pid, 0)`, lepas lewat `defer` dan handler sinyal.
    - Workspace session = cwd.
  - Verifikasi:
    - `go test ./channels/cli -run TestChatSessions`
    - Test dua proses dengan `SIGKILL` untuk `AC-SESS-007`
  - Bukti penyelesaian:
    - Status: Pending
    - Perintah/test: —
    - Hasil: —
    - PR/commit: —
    - Deviasi: Tidak ada

- [ ] `T-005` Buat kerangka TUI inline
  - Issue/PR: `—`
  - Bergantung pada: `T-004`
  - Requirements: `REQ-CHAT-001`, `REQ-CHAT-002`, `REQ-CHAT-003`, `REQ-CHAT-011`
  - Acceptance criteria: `AC-CHAT-001` sampai `AC-CHAT-008`, `AC-CHAT-023`
  - Operasi contract: `API-CLI-002`, `API-CLI-003`
  - Scope:
    - Model Bubble Tea tanpa alt screen dengan state machine dari design.
    - `tea.Println` untuk scrollback, `View()` untuk area stream, prompt, input, status bar, banner YOLO.
    - Pemilihan agent dari `--agent` atau route `cli`; cek TTY.
    - `recover` yang mengembalikan terminal dan melepas lease.
    - Harness PTY (`creack/pty` + `vt10x`) untuk test end-to-end.
  - Verifikasi:
    - `go test ./channels/cli -run 'TestChatAgentSelect|TestChatEmbedded'`
    - Golden status bar (`go test ./channels/cli/render -update` untuk memperbarui)
  - Bukti penyelesaian:
    - Status: Pending
    - Perintah/test: —
    - Hasil: —
    - PR/commit: —
    - Deviasi: Tidak ada

- [ ] `T-006` Implementasi streaming Markdown dan tampilan tool call
  - Issue/PR: `—`
  - Bergantung pada: `T-005`
  - Requirements: `REQ-CHAT-004`, `REQ-CHAT-005`
  - Acceptance criteria: `AC-CHAT-009`, `AC-CHAT-010`, `AC-CHAT-011`, `AC-CHAT-024`
  - Operasi contract: `API-CLI-003`, `API-CLI-004` (`Ctrl+O`)
  - Scope:
    - Markdown streaming: buffer per blok, render `glamour`, blok belum selesai di area stream.
    - `ToolLine` ringkas dan verbose, tabel argumen utama.
    - Toggle verbose dengan `Ctrl+O` dan cetak ulang tool call terakhir.
  - Verifikasi:
    - `go test ./channels/cli/render/...`
  - Bukti penyelesaian:
    - Status: Pending
    - Perintah/test: —
    - Hasil: —
    - PR/commit: —
    - Deviasi: Tidak ada

- [ ] `T-007` Implementasi input, tombol, dan riwayat
  - Issue/PR: `—`
  - Bergantung pada: `T-005`
  - Requirements: `REQ-CHAT-006`, `REQ-CHAT-007`, `REQ-CHAT-008`, `REQ-CHAT-009`
  - Acceptance criteria: `AC-CHAT-012` sampai `AC-CHAT-021`
  - Operasi contract: `API-CLI-004`
  - Scope:
    - Input dengan `bubbles/textarea`, keyboard enhancement Bubble Tea v2, bracketed paste.
    - Baris baru: `Shift+Enter`, `Alt+Enter`, `Ctrl+J`, `\` + `Enter`.
    - `Ctrl+C` batal/kosongkan/keluar dua kali, `Ctrl+D` keluar.
    - Pesan saat turn berjalan dikirim ke inbox dengan label `(antre)`.
    - `History` JSON Lines, 1000 entri, `0600`, `Ctrl+R`.
  - Verifikasi:
    - `go test ./channels/cli/input/... ./channels/cli/tui/...`
    - PTY: `TestChatInbox` untuk `AC-CHAT-012`
  - Bukti penyelesaian:
    - Status: Pending
    - Perintah/test: —
    - Hasil: —
    - PR/commit: —
    - Deviasi: Tidak ada

- [ ] `T-008` Implementasi approval TUI dan mode yolo
  - Issue/PR: `—`
  - Bergantung pada: `T-006`, `T-007`, spec #2 (hook `approval.request`), spec #4 (`ctx.Approval`)
  - Requirements: `REQ-APPR-001`, `REQ-APPR-002`
  - Acceptance criteria: `AC-APPR-001` sampai `AC-APPR-007`
  - Operasi contract: `API-CLI-002` (`--yolo`), `API-CLI-005`
  - Scope:
    - `TuiAnswerer` sebagai listener `approval.request` untuk session CLI milik proses ini.
    - Prompt `[y]/[a]/[n]`, alasan opsional setelah `n`, `Ctrl+C` = `cancelled`, pesan kedaluwarsa.
    - Label subagent dan input JSON saat verbose.
    - `--yolo` memanggil `SetPolicy(trust)` dan menampilkan banner.
  - Verifikasi:
    - `go test ./channels/cli/approval/...`
    - `go test ./channels/cli -run TestChatPolicy`
  - Bukti penyelesaian:
    - Status: Pending
    - Perintah/test: —
    - Hasil: —
    - PR/commit: —
    - Deviasi: Tidak ada

- [ ] `T-009` Implementasi slash command
  - Issue/PR: `—`
  - Bergantung pada: `T-008`
  - Requirements: `REQ-CMD-001`
  - Acceptance criteria: `AC-CMD-001` sampai `AC-CMD-014`
  - Operasi contract: `API-CLI-006`
  - Scope:
    - `ParseSlash`, `//` sebagai escape, `AllowedWhileRunning()`.
    - Dispatcher ke service core untuk semua perintah di contract.
    - Picker `/sessions` di viewport.
  - Verifikasi:
    - `go test ./channels/cli/commands/...`
    - `go test ./channels/cli -run TestChatCommands`
  - Bukti penyelesaian:
    - Status: Pending
    - Perintah/test: —
    - Hasil: —
    - PR/commit: —
    - Deviasi: Tidak ada

- [ ] `T-010` Implementasi `lana run`
  - Issue/PR: `—`
  - Bergantung pada: `T-004`, spec #3 (profile `headless`, `MockLlm`)
  - Requirements: `REQ-RUN-001`, `REQ-RUN-002`, `REQ-RUN-003`, `REQ-EXIT-001`
  - Acceptance criteria: `AC-RUN-001` sampai `AC-RUN-010`, `AC-EXIT-001` sampai `AC-EXIT-003`
  - Operasi contract: `API-CLI-007`
  - Scope:
    - `Runner`: prompt dari argumen dan stdin, pemilihan agent, policy `never` atau `ask`.
    - `TextWriter`, `JSONWriter`, `StreamJSONWriter`.
    - `TtyAnswerer` lewat `/dev/tty`, fail-closed.
    - `signal.NotifyContext` untuk SIGINT, exit 130.
  - Verifikasi:
    - `go test ./cmd/lana -run 'TestScript/run'`
  - Bukti penyelesaian:
    - Status: Pending
    - Perintah/test: —
    - Hasil: —
    - PR/commit: —
    - Deviasi: Tidak ada

- [ ] `T-011` Jalankan verifikasi akhir dan smoke manual
  - Issue/PR: `—`
  - Bergantung pada: `T-002`, `T-003`, `T-009`, `T-010`
  - Requirements: semua (verifikasi)
  - Acceptance criteria: — (memeriksa bukti semua task)
  - Operasi contract: semua
  - Scope:
    - Jalankan seluruh test, lint, format, dan build release.
    - Smoke manual di Ghostty, iTerm2, dan Terminal.app: `Shift+Enter`, paste, scrollback setelah keluar, `NO_COLOR`, approval, `/fork`.
    - Smoke dengan provider OpenAI sungguhan: `lana init --yes`, `lana chat`, `lana run --output json`.
    - Ukur ukuran binary release dan catat.
  - Verifikasi:
    - `test -z "$(gofmt -l .)" && golangci-lint run ./... && go test -race ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" ./cmd/lana`
  - Bukti penyelesaian:
    - Status: Pending
    - Perintah/test: —
    - Hasil: —
    - PR/commit: —
    - Deviasi: Tidak ada

## Verifikasi akhir

- [ ] Setiap acceptance criterion punya bukti lulus.
- [ ] Tidak ada perilaku terlihat tanpa ID requirement.
- [ ] Tidak ada perilaku di luar scope.
- [ ] ADR dan diagram sesuai implementasi.
- [ ] `gofmt`, `golangci-lint`, `go test -race`, dan build release lulus.
- [ ] Deviasi diselesaikan di requirements atau design, tidak hanya dicatat di sini.

## Approval

- [x] Requirements dan design sudah Approved dan revisinya dicatat.
- [x] Contract sudah Approved.
- [x] Setiap acceptance criterion punya tepat satu pemilik bukti.
- [x] Setiap task punya scope terbatas, dependency, dan verifikasi.
- [x] Tidak ada task yang memperkenalkan perilaku baru.
- **Di-approve oleh**: — (revisi 1 di-approve Ridho 2026-10-29; revisi 2 menunggu)
- **Di-approve pada**: —
