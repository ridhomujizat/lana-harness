# 0011. Jalankan subagent sebagai child session

- **Status**: Proposed
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0003](0003-multi-agent-dengan-routing-channel.md), [0004](0004-permission-mode-per-agent.md), [0009](0009-session-sebagai-event-log-append-only.md)

## Context

Task besar (riset beberapa sumber, eksplorasi codebase, review) memenuhi context window jika
dikerjakan dalam satu session. `dsh` menyediakan **subagent provider** di balik satu interface:
child agent baru, atau turn yang didelegasikan, dan subagent bisa dilanjutkan. Pemilik meminta
subagent masuk v1.

## Decision

Subagent adalah **child session** biasa yang dijalankan oleh agent loop yang sama.

Tool untuk model:

| Tool | Fungsi |
|---|---|
| `task(agent, prompt, title)` | Buat child session dari agent `agent`, jalankan satu turn, kembalikan jawaban final sebagai `tool_result` |
| `task_continue(session_id, prompt)` | Kirim pesan lanjutan ke child session yang sudah ada |

Aturan:

- Child session dibuat dengan `session/created { parent_session_id, parent_call_id }`. Child
  **tidak** mewarisi riwayat induk; konteks hanya dari `prompt`. Fork (warisan riwayat) adalah fitur terpisah.
- `agent` harus terdaftar di `[agents.<nama>.subagents]` milik agent induk. Default: hanya agent itu sendiri.
- Permission child = **irisan** dari induk dan konfigurasi agent child. Child tidak pernah punya izin
  lebih besar dari induknya. Jika induk `trust` dan child `ask`, child tetap `ask`.
- Approval dari child dikirim lewat channel induk, dengan label nama child.
- Batas: kedalaman maksimal 2, paling banyak 3 child berjalan bersamaan per session induk,
  `max_steps` child default 15.
- Child berjalan paralel jika model memanggil beberapa `task` dalam satu step.
- Pembatalan turn induk membatalkan semua child yang sedang jalan (`turn/end { reason: "cancelled" }`).
- Usage token child dijumlahkan ke step induk untuk tampilan biaya.

## Consequences

**Positive**
- Context induk tetap bersih. Hanya ringkasan child yang masuk.
- Tidak ada mekanisme baru: child memakai event log, permission, dan agent loop yang sama.
- Child session bisa dibuka dan di-replay di dashboard seperti session lain.

**Negative / tradeoffs**
- Child paralel menambah pemakaian token dan rate limit provider.
- Approval dari beberapa child bersamaan bisa membingungkan di chat.

**Neutral**
- Koordinasi tim agent (roster, task board) seperti `dsh` Agent Teams tidak termasuk v1.

## Alternatives considered

- **Subagent sebagai proses terpisah** — isolasi kuat. Ditolak karena berat.
- **Child mewarisi seluruh riwayat induk** — konteks lengkap, tapi menghapus manfaat utama subagent. Ditolak.
