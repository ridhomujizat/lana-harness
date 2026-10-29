# 0004. Atur permission per agent dan putuskan approval lewat hook

- **Status**: Proposed
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0002](0002-core-dari-modul-service-dan-hook.md), [0009](0009-session-sebagai-event-log-append-only.md), [0011](0011-subagent-sebagai-child-session.md)

## Context

Tool seperti `bash` dan `write_file` bisa merusak data. Pemilik ingin memilih per agent:
**trust penuh**, atau **approval gate seperti Claude Code**. Approval harus bisa dijawab dari
channel asal: terminal, Telegram, WhatsApp, web dashboard, atau editor lewat ACP.

`dsh` memisahkan dua hal:

- **Policy** per session (`ask` atau `never`). Nilai efektif = event `approval/policy` terakhir di
  session log, sehingga replay merekonstruksi policy.
- **Rantai penjawab** lewat waterfall `approval/request`. UI channel atau ACP bridge menjadi penjawab.
  Outcome tertutup dan *fail-closed*: selain `allowed-once`, tool ditolak. Penjawab yang hilang,
  error, atau tidak valid menghasilkan `unavailable`.

Sandbox level OS tidak masuk v1.

## Decision

**Policy session** bernilai `trust`, `ask`, atau `never`.

- Nilai awal diambil dari `permission` agent di config.
- Perubahan (`/permission ask`, flag `--yolo`) ditulis sebagai event `approval/policy`.
- Turn cron dan profile `headless` memakai `never` kecuali dikonfigurasi lain.

**Rule per agent** mengikuti pola Claude Code:

```toml
[agents.coder]
permission = "ask"   # "trust" | "ask" | "never"

[agents.coder.rules]
allow = ["read_file", "glob", "grep", "bash(git status*)", "bash(cargo test*)"]
deny  = ["bash(rm -rf*)", "bash(sudo*)", "write_file(~/.ssh/**)"]
```

**Evaluasi** dijalankan oleh listener `tools.pre_execute` milik modul `permission`:

1. Cocok `deny` → tolak. Berlaku juga di `trust`.
2. Path di luar workspace agent → tolak. Path di-canonicalize; symlink keluar workspace ditolak.
3. Cocok `allow`, atau ada grant session yang cocok → jalankan.
4. Policy `trust` → jalankan.
5. Risk tool `Read` → jalankan.
6. Policy `never` → tolak.
7. Policy `ask` → panggil `ctx.approval.request(...)`.

**Outcome approval** tertutup:

| Outcome | Arti |
|---|---|
| `allowed_once` | Hanya tool call ini |
| `allowed_session` | Tool call ini, dan pattern yang sama sampai session selesai (setara "allow always" di Claude Code). Disimpan sebagai event, tidak ditulis ke config |
| `rejected` | Ditolak user, dengan alasan opsional untuk model |
| `cancelled` | Turn dibatalkan saat menunggu |
| `unavailable` | Tidak ada penjawab, penjawab error, atau timeout 10 menit |

Tool hanya jalan untuk `allowed_once` dan `allowed_session`. Semua outcome lain menghasilkan
`tool/result` error yang dikirim balik ke model.

**Rantai penjawab** (`approval.request`, waterfall) diisi oleh modul channel:

- Channel pemilik session (CLI, Telegram, WhatsApp, web) menjawab jika session miliknya dan ada manusia yang terhubung.
- Server ACP meneruskan ke `session/request_permission` di editor.
- SDK meneruskan sebagai notifikasi dan menunggu `approval.respond`.
- Tidak ada penjawab yang mengambil alih → `unavailable`.

**Audit.** Setiap approval menulis pasangan `approval/asked` dan `approval/decided` ke session log,
dengan `request_id` baru per permintaan. Request **tidak** menyalin argumen tool; penjawab
menampilkan argumen dari `tool/call` lewat `call_id`, supaya tampilan tidak menyimpang dari input asli.

## Consequences

**Positive**
- Satu konsep izin untuk semua channel dan protokol.
- Fail-closed: bug di penjawab tidak pernah membuka gate.
- Policy dan grant ada di log, jadi resume dan fork mempertahankan izin yang sama.

**Negative / tradeoffs**
- Tanpa sandbox OS, `bash` yang diizinkan punya akses penuh user. Pattern perintah bisa diakali.
- Approval lewat chat menambah latensi saat user tidak aktif.

**Neutral**
- Sandbox OS bisa ditambah nanti sebagai listener `tools.execute` yang membungkus argv, tanpa mengubah alur ini.

## Alternatives considered

- **Selalu trust** — ditolak untuk agent yang punya `bash`.
- **Permission di dalam agent loop** — ditolak. Kebijakan harus berupa hook agar bisa diganti (lihat [0002](0002-core-dari-modul-service-dan-hook.md)).
- **Sandbox OS sejak v1** — ditunda karena implementasi berbeda per OS.
