# 0008. Sediakan interface CLI, channel chat, HTTP API, dan web dashboard

- **Status**: Proposed
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0001](0001-gunakan-rust-single-binary-daemon.md), [0004](0004-permission-mode-per-agent.md), [0012](0012-acp-dan-sdk-json-rpc.md)

## Context

Pemilik ingin memakai agent dari terminal, Telegram, WhatsApp, HTTP API/webhook, dan web.
Setiap interface harus bisa menampilkan streaming jawaban dan menjawab approval. Library bot
yang lengkap (misalnya `teloxide`) menambah ukuran binary cukup besar.

## Decision

Setiap interface adalah modul (lihat [0002](0002-core-dari-modul-service-dan-hook.md)) yang
mengirim pesan ke `ctx.agents`, membaca balasan dari hook `session.event`, dan memasang penjawab
di hook `approval.request`. Interface HTTP berbagi satu `axum` server di `127.0.0.1:18300`
(default, bisa diubah). ACP dan SDK JSON-RPC dibahas di [0012](0012-acp-dan-sdk-json-rpc.md).

| Channel | Implementasi v1 | Streaming | Approval |
|---|---|---|---|
| CLI/TUI | `lana chat`: line editor `rustyline` + render Markdown sederhana. TUI penuh `ratatui` di belakang feature `tui`. Attach ke daemon lewat HTTP+SSE; jika daemon mati, jalan embedded. | Token per token | Prompt `[y]es / [a]lways / [n]o` |
| Telegram | Bot API langsung lewat `reqwest`, long polling `getUpdates` (tanpa webhook publik). | Edit pesan berkala (throttle 1 detik) | Inline keyboard |
| WhatsApp | WhatsApp Cloud API resmi (webhook masuk + Graph API keluar), feature `whatsapp`. Butuh URL publik (tunnel/reverse proxy). | Pesan final saja | Interactive reply button |
| HTTP API | `POST /v1/agents/{agent}/messages`, `GET /v1/sessions/{id}/events` (SSE), `POST /v1/webhooks/{hook}` untuk trigger dari sistem lain. Auth bearer token. | SSE | `POST /v1/approvals/{id}` |
| Web dashboard | Aset statis (Preact + `htm`, tanpa build step) di-embed lewat `rust-embed`, memakai HTTP API yang sama. Halaman: chat, sessions, agents, cron, approvals, audit. | SSE | Modal |

Aturan keamanan interface:

- HTTP server bind ke `127.0.0.1` secara default. Bind ke `0.0.0.0` butuh `api_token` di config.
- Dashboard login memakai token yang dibuat saat `lana init` dan disimpan sebagai hash.
- Telegram dan WhatsApp hanya menerima pesan dari ID yang ada di allowlist `[[routes]]`.
- Webhook WhatsApp diverifikasi dengan signature `X-Hub-Signature-256`.

## Consequences

**Positive**
- Satu event stream internal (`ChatEvent`) dipakai ulang oleh semua interface.
- Telegram tanpa webhook publik, cocok untuk laptop di belakang NAT.
- Dashboard tanpa toolchain Node, aset < 200 KB.

**Negative / tradeoffs**
- WhatsApp Cloud API butuh akun Meta Business dan URL publik.
- Menulis klien Bot API sendiri berarti kita menjaga sendiri subset API yang dipakai.

**Neutral**
- Discord, Slack, dan email bisa ditambah nanti sebagai `Channel` baru.

## Alternatives considered

- **`teloxide`** — lengkap, tapi menambah banyak dependency. Ditolak untuk v1.
- **WhatsApp lewat bridge tidak resmi (whatsmeow/Baileys)** — tanpa akun bisnis, tapi melanggar ToS dan butuh runtime Go/Node. Ditolak.
- **Dashboard React + Vite** — DX bagus, tapi butuh build step Node. Ditolak untuk v1.
- **Desktop app (Tauri)** — ditolak karena web dashboard sudah cukup dan lebih ringan.
