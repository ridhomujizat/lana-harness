# 0001. Gunakan Rust sebagai single binary daemon

- **Status**: Superseded by [0013](0013-gunakan-go-sebagai-single-binary.md)
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0002](0002-core-dari-modul-service-dan-hook.md), [0008](0008-interface-cli-channel-http-dan-web-dashboard.md)

## Context

`lana-harness` adalah agent harness pribadi untuk coding assistant, personal assistant, dan
automation/ops. Referensi utama:

- **zeroclaw** — Rust, satu binary, satu agent loop untuk semua channel, config TOML,
  memory SQLite, policy keamanan supervised.
- **nouride** — satu daemon multi-agent, channel chat (Telegram/WhatsApp/Discord), approval gate,
  scheduled work, web dashboard, data di satu folder (`.nouride/`).
- **deepseek-harness (`dsh`)** — referensi **cara kerja**: semua bagian adalah modul yang
  mendaftarkan service dan hook, session sebagai event log append-only, profile dan patch,
  subagent, server ACP dan SDK. `dsh` ditulis di TypeScript; `lana` mengambil polanya, bukan runtime-nya.

Kebutuhan utama: **ringan**. Harness harus bisa jalan di laptop (macOS arm64) dan VPS kecil
(Linux x86_64/aarch64) tanpa runtime tambahan (Node, Python, JVM).

Constraint:

- Satu proses melayani CLI, channel chat, HTTP API, web dashboard, dan scheduler.
- Development memakai Nix (`flake.nix`), bukan install global.
- Target resource v1: binary release < 15 MB, RAM idle < 20 MB, cold start < 100 ms.

## Decision

Kita membangun `lana` sebagai **satu binary Rust** (edition 2024) dengan runtime async `tokio`.
Binary yang sama menjalankan beberapa mode lewat subcommand:

| Subcommand | Fungsi |
|---|---|
| `lana daemon` | Menjalankan semua channel, HTTP API, web dashboard, dan scheduler |
| `lana chat [--agent X]` | Chat TUI lokal (attach ke daemon lewat HTTP, atau embedded jika daemon mati) |
| `lana run --agent X "prompt"` | One-shot, untuk script dan pipe |
| `lana config check` | Validasi `config.toml` |
| `lana service install` | Pasang LaunchAgent (macOS) atau systemd user unit (Linux) |

Semua state tinggal di satu folder data: `~/.lana/` (`config.toml`, `lana.db`, `skills/`,
`workspaces/`, `logs/`). Override lewat env `LANA_HOME`.

Dependency dipilih agar ringan:

- HTTP client: `reqwest` dengan `rustls` (tanpa OpenSSL).
- HTTP server: `axum`.
- Storage: `rusqlite` dengan fitur `bundled` (lihat [0005](0005-sqlite-sebagai-storage-tunggal.md)).
- Serialisasi: `serde`, `serde_json`, `toml`.
- Profil release: `opt-level = "z"`, `lto = "fat"`, `codegen-units = 1`, `panic = "abort"`, `strip = true`.
- Fitur besar (WhatsApp, web dashboard) di belakang Cargo feature flag.

## Consequences

**Positive**
- Tidak ada runtime eksternal. Deploy cukup copy satu file.
- Footprint memory kecil dan prediktif (tanpa GC).
- Static binary `musl` memudahkan Docker image `scratch`/`distroless`.

**Negative / tradeoffs**
- Kompilasi lambat, terutama build release dengan LTO.
- Ekosistem AI SDK di Rust lebih tipis daripada TypeScript/Python. Kita menulis adapter provider sendiri.
- Kurva belajar async Rust (lifetime, `Send + Sync`) lebih tinggi.

**Neutral**
- Toolchain Rust disediakan lewat `flake.nix` (`rust-overlay` atau `fenix`), bukan `rustup` global.

## Alternatives considered

- **Go** — binary dan RAM juga kecil, development lebih cepat. Ditolak karena preferensi pemilik
  dan target RAM lebih rendah tanpa GC.
- **TypeScript (Bun/Node)** — SDK paling lengkap. Ditolak karena RAM idle > 50 MB dan butuh runtime.
- **Fork zeroclaw** — fitur lengkap, tapi codebase besar (30+ channel, hardware). Ditolak karena
  tujuan proyek adalah harness kecil yang dipahami penuh oleh pemiliknya.
