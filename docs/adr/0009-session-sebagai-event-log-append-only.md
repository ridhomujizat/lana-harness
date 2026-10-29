# 0009. Simpan session sebagai event log append-only di SQLite

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0002](0002-core-dari-modul-service-dan-hook.md), [0005](0005-sqlite-sebagai-storage-tunggal.md), [0011](0011-subagent-sebagai-child-session.md)

## Context

`dsh` memakai **session log append-only** sebagai sumber konteks model. Prinsipnya *model-visible
means logged*: setiap request ke model harus bisa direkonstruksi dari log. Riwayat pesan,
fork, resume, transcript, dan telemetry semuanya diturunkan dari event. `dsh` menyimpan log
sebagai file JSONL per session.

`lana` sudah memakai satu file SQLite (lihat [0005](0005-sqlite-sebagai-storage-tunggal.md)).
Pemilik ingin log event tetap di SQLite. Fitur yang butuh log ini: fork session, resume setelah
crash, subagent, dan replay di web dashboard.

## Decision

Session adalah **urutan event append-only** di tabel `session_events`. Tabel `messages` tidak ada.
Riwayat untuk model dihitung oleh fungsi murni `derive_messages(events) -> Vec<Message>`.

```sql
CREATE TABLE session_events (
  session_id TEXT    NOT NULL,
  seq        INTEGER NOT NULL,          -- naik 1 per event, mulai dari 1
  kind       TEXT    NOT NULL,          -- contoh: 'user/message'
  payload    TEXT    NOT NULL,          -- JSON
  turn_id    TEXT,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;
-- trigger BEFORE UPDATE dan BEFORE DELETE memanggil RAISE(ABORT): log tidak bisa diubah
```

Event durable v1:

| Event | Isi |
|---|---|
| `session/created` | agent, channel, address, `parent_session_id`, `inherited_seq` |
| `turn/start`, `turn/end` | id turn, alasan berhenti (`end_turn`, `max_steps`, `cancelled`, `interrupted`, `error`) |
| `step/start`, `step/end` | nomor step, provider, model, usage token |
| `system/message` | system prompt yang dipakai. Event baru hanya jika prompt berubah |
| `user/message` | input user, termasuk konteks yang di-inject modul |
| `assistant/message` | jawaban model lengkap (text dan `tool_use`) |
| `assistant/attempt` | percobaan gagal atau di-retry. Tidak masuk riwayat model |
| `tool/call`, `tool/result` | call id, nama tool, input, output atau error |
| `approval/asked`, `approval/decided` | pasangan audit approval (lihat [0004](0004-permission-mode-per-agent.md)) |
| `approval/policy` | perubahan policy session |
| `model/selection` | perubahan provider atau model di tengah session |
| `compaction/summary` | ringkasan yang menggantikan event sampai `upto_seq` |

Aturan:

- Event ditulis oleh store writer task, lalu di-broadcast lewat hook `session.event`.
- Streaming token tidak ditulis per token. Stream hidup dikirim sebagai frame sementara ke channel;
  log hanya menyimpan `assistant/message` atau `assistant/attempt` final.
- Tabel `sessions` adalah **projection** (title, agent, status, `last_seq`, parent) yang bisa
  dibangun ulang dari event. Source of truth tetap `session_events`.
- **Compaction** menulis `compaction/summary { upto_seq }`. `derive_messages` memakai ringkasan
  terakhir ditambah event setelah `upto_seq`. Event lama tidak dihapus.

**Fork.** `fork(session, at_seq)` membuat session baru dengan `parent_session_id` dan
`inherited_seq = at_seq`. Event induk tidak disalin. Saat derive, store membaca event induk sampai
`inherited_seq`, lalu event milik session anak. Fork hanya boleh di batas turn (`turn/end`).

**Resume.** Saat session dibuka dan event terakhir bukan `turn/end`, store menambah event penutup:
`tool/result` error untuk setiap `tool/call` tanpa pasangan, lalu `turn/end { reason: "interrupted" }`.
Setelah itu session bisa menerima pesan baru.

**Versi format.** Payload memakai `serde` dengan field `v`. Migrasi format dilakukan saat decode
(versi lama ke versi sekarang di memori), bukan dengan menulis ulang baris lama.

## Consequences

**Positive**
- Setiap request model bisa direkonstruksi. Debug dan replay jadi mudah.
- Fork murah karena tidak menyalin event.
- Crash di tengah turn tidak merusak session.
- Web dashboard, CLI, dan ACP membaca sumber yang sama.

**Negative / tradeoffs**
- Derive riwayat butuh membaca banyak event. Dimitigasi dengan compaction dan index `(session_id, seq)`.
- Data tumbuh terus. `lana db prune` hanya menghapus session utuh, tidak pernah memotong log.
- Payload JSON di SQLite kurang efisien dibanding format biner. Cukup untuk pemakaian pribadi.

**Neutral**
- Export session ke JSONL (`lana session export <id>`) tersedia untuk kompatibilitas dengan pola `dsh`.

## Alternatives considered

- **JSONL per session seperti `dsh`** — mudah dibaca dan di-diff. Ditolak karena butuh index terpisah
  dan pemilik ingin satu file SQLite.
- **Tabel `messages` yang bisa diubah** — sederhana. Ditolak karena fork, resume, dan audit jadi tidak bisa dipercaya.
