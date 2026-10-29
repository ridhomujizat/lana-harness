# 0007. Jalankan scheduler cron di dalam daemon

- **Status**: Proposed
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0004](0004-permission-mode-per-agent.md), [0005](0005-sqlite-sebagai-storage-tunggal.md)

## Context

Personal assistant butuh reminder ("ingatkan jam 9 pagi"). Automation/ops butuh task berulang
("cek disk VPS tiap jam, lapor ke Telegram jika > 80%"). nouride menyebut fitur ini scheduled
work dan missions. Memakai `crontab` OS memisahkan state dari daemon dan sulit dibuat oleh agent.

## Decision

Daemon menjalankan **satu scheduler task** yang membaca `cron_jobs` dari SQLite.

- Format jadwal: ekspresi cron 5 field (`0 9 * * *`), atau one-shot ISO 8601 (`at = "2026-11-01T09:00:00+07:00"`).
  Parsing memakai crate `cron`. Zona waktu per job, default zona waktu lokal.
- Loop: hitung `next_run_at` terdekat, `sleep_until`, jalankan job yang jatuh tempo, hitung ulang.
  Perubahan job (create/update/delete) membangunkan loop lewat `Notify`.
- Setiap run membuat session baru `(channel = "cron", address = job_id, agent)` lalu menjalankan
  satu turn agent dengan `prompt` job. Hasil akhir dikirim ke `target_channel` dan `target_address`.
- Run yang terlewat saat daemon mati: dijalankan **sekali** saat boot jika masih dalam jendela
  `catchup` (default 1 jam), selain itu dicatat sebagai `skipped`.
- Satu job tidak boleh overlap dengan dirinya sendiri. Jika run sebelumnya belum selesai, run baru `skipped`.
- Session cron memakai approval policy `never` karena tidak ada manusia. Tool yang butuh approval otomatis ditolak (lihat [0004](0004-permission-mode-per-agent.md)).

Job dibuat dari tiga jalur:

1. Agent lewat tool `cron_create`, `cron_list`, `cron_delete` (tool berisiko `Write`).
2. CLI `lana cron add --agent ops --schedule "0 * * * *" --prompt "..." --to telegram:123`.
3. Web dashboard.

## Consequences

**Positive**
- Tidak ada dependency ke `crontab` atau `launchd` per job.
- Riwayat run tersimpan di `cron_runs` dan bisa dilihat di dashboard.

**Negative / tradeoffs**
- Job hanya jalan saat daemon hidup. Untuk keandalan, daemon dipasang sebagai service (`lana service install`).
- Akurasi jadwal setingkat detik, bukan real-time.

**Neutral**
- "Missions" jangka panjang (multi-step, berhari-hari) tidak termasuk v1.

## Alternatives considered

- **crontab/launchd per job** — ditolak karena state tersebar dan butuh akses sistem.
- **Crate `tokio-cron-scheduler`** — fitur lengkap, tapi menyimpan state sendiri. Ditolak agar SQLite tetap satu-satunya source of truth.
