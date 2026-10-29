# 0005. Pakai SQLite sebagai storage tunggal

- **Status**: Proposed
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0003](0003-multi-agent-dengan-routing-channel.md), [0007](0007-scheduler-cron-di-dalam-daemon.md), [0009](0009-session-sebagai-event-log-append-only.md)

## Context

Harness perlu menyimpan session dan riwayat pesan, memory jangka panjang, job cron, dan audit
izin. zeroclaw memakai SQLite. nouride menyimpan semua state di satu folder data
(`.nouride/`); dokumentasi publiknya hanya menyebut file `nougate.db`, engine database tidak
disebut eksplisit.

Target ringan menolak server database terpisah.

## Decision

Kita memakai **satu file SQLite** di `~/.lana/lana.db` lewat `rusqlite` (fitur `bundled`).

- Mode `WAL`, `synchronous = NORMAL`, `foreign_keys = ON`.
- Satu writer task (actor) menerima perintah tulis lewat channel `mpsc`. Read memakai pool kecil
  koneksi read-only. Ini menghindari `SQLITE_BUSY` tanpa lock manual.
- Migration skema berupa file SQL bernomor yang di-embed ke binary dan dijalankan saat boot
  (`PRAGMA user_version`).

Memory v1 memakai **FTS5** (full-text search, ranking BM25). Agent mengakses lewat tool
`memory_save` dan `memory_search`. Kolom `embedding BLOB` disiapkan tapi kosong; pencarian
vektor (misalnya `sqlite-vec`) bisa ditambah nanti tanpa migrasi data.

Tabel inti:

| Tabel | Isi |
|---|---|
| `session_events` | Event log append-only, source of truth session (lihat [0009](0009-session-sebagai-event-log-append-only.md)) |
| `sessions` | Projection: `id`, `agent`, `channel`, `address`, `title`, `status`, `parent_session_id`, `inherited_seq`, `last_seq` |
| `memories` + `memories_fts` | `scope` (global/agent), `content`, `tags`, `embedding` |
| `cron_jobs` | `agent`, `schedule`, `prompt`, `target_channel`, `target_address`, `next_run_at`, `enabled` |
| `cron_runs` | `job_id`, `session_id`, `status`, `started_at`, `finished_at`, `error` |

Audit approval tidak punya tabel sendiri. Pasangan event `approval/asked` dan `approval/decided`
di `session_events` adalah audit-nya. Index `(kind, created_at)` dipakai untuk halaman audit di dashboard.

Context window: jika token session melewati batas (default 70% context model), modul compaction
menulis event `compaction/summary`. Event lama tetap disimpan.

Secret (API key, token bot) **tidak** disimpan di DB. Secret dibaca dari env atau file
`~/.lana/secrets.env` dengan permission `0600`.

## Consequences

**Positive**
- Backup = copy satu file. Tidak ada service tambahan.
- FTS5 cukup untuk memory personal tanpa biaya embedding.
- Riwayat lengkap memudahkan replay dan debug.

**Negative / tradeoffs**
- `bundled` menambah sekitar 1-2 MB ke binary.
- Satu writer membatasi throughput tulis. Cukup untuk pemakaian pribadi.
- FTS5 tidak paham sinonim atau makna semantik.

**Neutral**
- Data riwayat tumbuh terus. Perintah `lana db prune --older-than 90d` disediakan.

## Alternatives considered

- **SQLite + embeddings sejak v1** — pencarian semantik. Ditunda karena butuh embedding provider dan biaya.
- **File Markdown** — mudah diedit manual. Ditolak untuk session dan cron, tapi prompt agent dan skill tetap file.
- **Postgres** — ditolak karena butuh server terpisah.
