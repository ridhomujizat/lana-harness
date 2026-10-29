# 0012. Sediakan server ACP dan SDK JSON-RPC

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0008](0008-interface-cli-channel-http-dan-web-dashboard.md), [0010](0010-profile-dan-config-patch.md)

## Context

Pemilik ingin agent bisa dikendalikan dari editor dan dari program lain. `dsh` punya dua profile
untuk ini: `acp` (server Agent Client Protocol untuk otomasi) dan `sdk` (server JSON-RPC yang
dipakai SDK TypeScript dan Python). Keduanya memakai runtime yang sama, hanya modul pintunya berbeda.

**ACP (Agent Client Protocol)** adalah protokol JSON-RPC di stdio antara editor (Zed dan editor lain)
dan coding agent. Editor menjadi client, agent menjadi server.

## Decision

`lana-protocol` menyediakan dua modul server. Keduanya dipilih lewat profile
(lihat [0010](0010-profile-dan-config-patch.md)).

**ACP — `lana acp`.** JSON-RPC 2.0 lewat stdio memakai crate `agent-client-protocol`.

| Arah | Method | Pemetaan ke `lana` |
|---|---|---|
| client ke agent | `initialize` | Kirim kemampuan: `loadSession`, prompt dengan gambar |
| client ke agent | `session/new` | Buat session dengan `cwd` dari editor sebagai workspace |
| client ke agent | `session/load` | Resume session dari event log |
| client ke agent | `session/prompt` | Jalankan satu turn |
| client ke agent | `session/cancel` | Batalkan turn aktif |
| agent ke client | `session/update` | Stream text, `tool_call`, dan status tool dari hook `session.event` |
| agent ke client | `session/request_permission` | Penjawab di rantai `approval.request` |
| agent ke client | `fs/read_text_file`, `fs/write_text_file` | Opsional: baca file lewat buffer editor jika client mendukung |

Agent yang dipakai ACP dipilih dengan `lana acp --agent coder`.

**SDK — `lana sdk`.** JSON-RPC 2.0 milik `lana` sendiri, lewat stdio (default) atau unix socket
`~/.lana/lana.sock`. Method mengikuti HTTP API supaya satu model mental untuk semua pintu:

| Method | Fungsi |
|---|---|
| `session.create { agent }` | Buat session |
| `session.send { session_id, content }` | Kirim pesan, mulai atau antre turn |
| `session.subscribe { session_id, from_seq }` | Notifikasi `session.event` mulai dari `seq` tertentu |
| `session.fork { session_id, at_seq }` | Fork di batas turn |
| `session.cancel { session_id }` | Batalkan turn |
| `approval.respond { request_id, outcome }` | Jawab approval |
| `agents.list`, `sessions.list` | Daftar |

Aturan:

- Server ACP dan SDK memakai `Context` yang sama dengan daemon. Tidak ada jalur eksekusi terpisah.
- Protokol stdio menulis log ke stderr atau file, **tidak pernah** ke stdout.
- SDK di unix socket memakai permission file `0600`. Tidak ada port TCP untuk SDK di v1.
- Library client SDK (TypeScript, Python) tidak termasuk v1. Protokol didokumentasikan dulu di `docs/specs/`.

## Consequences

**Positive**
- `lana` bisa langsung dipakai sebagai agent di Zed dan editor lain yang mendukung ACP.
- Script dan program lain bisa memakai `lana` tanpa HTTP server.
- `session.subscribe` dengan `from_seq` memanfaatkan event log untuk reconnect tanpa kehilangan event.

**Negative / tradeoffs**
- Spesifikasi ACP masih berkembang. Upgrade crate bisa memaksa perubahan.
- Dua protokol plus HTTP API berarti tiga permukaan yang harus dijaga konsisten.

**Neutral**
- Method SDK dan endpoint HTTP dibangun dari satu definisi handler supaya tidak menyimpang.

## Alternatives considered

- **Hanya HTTP API** — satu permukaan. Ditolak karena editor ACP memakai stdio dan program lokal tidak perlu port.
- **MCP server mode (lana sebagai MCP server)** — bisa nanti. MCP cocok untuk mengekspos tool, bukan agent dengan session dan approval.
