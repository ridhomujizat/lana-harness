# 0015. Jadikan lana runtime yang bisa di-embed di service

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0005](0005-sqlite-sebagai-storage-tunggal.md), [0009](0009-session-sebagai-event-log-append-only.md), [0013](0013-gunakan-go-sebagai-single-binary.md), [0014](0014-integrasi-sebagai-agentic-tool.md)

## Context

ADR 0014 menyediakan Go library sebagai salah satu permukaan integrasi. Pemilik ingin memakai
`lana` sebagai fondasi untuk service seperti `sxored-be-scrapper-engine`: service berbasis job yang
menjalankan agent per tugas dengan tool domain (browser, parser, database), berjalan di beberapa
replica di Kubernetes, dan memakai Postgres.

Rencana sekarang masih dibentuk untuk harness pribadi:

- Storage hanya SQLite satu file dengan satu penulis, dan lock session memakai PID dan hostname.
- Agent hanya didefinisikan lewat TOML.
- Belum ada batas biaya atau waktu per run.
- Observability hanya `slog`.
- Belum ada aturan yang mencegah package library menarik dependency TUI, Telegram, atau web.

Pertimbangan beban: dalam mode embed tidak ada service lana terpisah. Agent loop berjalan di
proses service, dan sebagian besar waktunya menunggu provider LLM. Komputasi berat ada di tool
domain milik service itu sendiri, dan skalanya ikut replica service. Daemon lana terpusat tetap
ada untuk pemakaian pribadi dan MCP.

## Decision

**1. Dua peran, dua repository.**

| Peran | Lokasi | Isi |
|---|---|---|
| SDK runtime agent | Repo ini (`lana-harness`) | Package `lana`, `core`, `agent`, `providers/*`, `tools`, `store/*`, plus binary `lana` untuk pemakaian pribadi |
| Template service | Repo terpisah, berbasis kerangka Go service milik pemilik | HTTP, Postgres, antrean job, deploy, contoh tool domain. Meng-import `lana` |

Repo ini tidak menjadi boilerplate service. Template service dibuat di repo sendiri setelah
runtime embeddable selesai.

**2. Package library tetap ramping.** Package `lana`, `core`, `agent`, `providers/*`, `tools`,
`store/*`, dan `observability/*` tidak boleh meng-import `channels/`, `web/`, atau `protocol/`.
Aturan ini diuji oleh test aturan import yang sama dengan spec #2.

**3. Store bisa diganti.** `lana.Options.Store` menerima implementasi `core.SessionStore` apa pun:

| Store | Package | Dipakai untuk |
|---|---|---|
| SQLite | `store/sqlite` | Binary `lana`, satu mesin (ADR 0005) |
| Postgres | `store/postgres` | Service dengan banyak replica |
| Memori | `store/memstore` | Job sementara dan test, tanpa persistence |

`store/postgres` mengikuti semantik ADR 0009: tabel `session_events` append-only (trigger menolak
`UPDATE` dan `DELETE`), projection `sessions`, fork tanpa salin. Lock session memakai
`pg_try_advisory_lock` di koneksi yang dipegang selama session terbuka, sehingga lock lepas
otomatis saat proses atau koneksi mati. Cek PID dan hostname tidak dipakai di Postgres.
Nama schema Postgres bisa diatur agar tabel lana tidak bercampur dengan tabel service.

**4. Agent bisa didefinisikan di kode.**

```go
rt, err := lana.New(ctx, lana.Options{
    Store: pgstore,
    Agents: []lana.Agent{{
        Name: "scraper", Provider: "openai", Model: "gpt-5-mini",
        Prompt: scraperPrompt, Tools: []core.Tool{browserTool, parseTool},
        Permission: core.PolicyNever, MaxSteps: 40,
    }},
})
```

Definisi di kode dipetakan ke lapisan config memori bernama `code` (REQ-CONF-009 spec #2), sehingga
validasi, peta asal, dan aturan merge tetap sama. Tool yang hanya dikenal di kode boleh dirujuk
oleh agent di lapisan `code`.

**5. Budget per run.**

```go
res, err := rt.Run(ctx, lana.Request{
    Agent: "scraper", Prompt: p,
    Budget: lana.Budget{MaxTokens: 200_000, MaxDuration: 3 * time.Minute, MaxSteps: 40},
})
```

Saat budget habis, turn berhenti dengan `turn/end` reason `budget` (nilai baru di samping
`end_turn`, `max_steps`, `cancelled`, `interrupted`, `error`). Pemeriksaan token memakai usage dari
`step/end`.

**6. Konkurensi per proses.** Satu `lana.Runtime` per proses, aman dipakai banyak goroutine.
`Options.MaxConcurrentRuns` membatasi run yang berjalan bersamaan. Run yang melebihi batas menunggu
sampai ada slot atau `ctx` dibatalkan.

**7. OpenTelemetry opsional.** Package `observability/otel` memasang listener di hook `agent.*`,
`llm.stream`, dan `tools.*` untuk membuat span per turn, step, dan tool call, serta metrik token
dan durasi. Diaktifkan lewat `lana.Options{Tracer, Meter}`. Tanpa option itu, tidak ada dependency
OpenTelemetry yang ikut di-link.

**8. Mode deploy.**

| Mode | Lokasi beban | Cocok untuk |
|---|---|---|
| Embed di service | Proses service, skala ikut replica | Service job dengan tool domain |
| Daemon `lana` atau `lana mcp` | Satu proses lana | Pemakaian pribadi, Claude Code via MCP, tool bersama yang ringan |

## Consequences

**Positive**
- Service baru yang butuh agent tidak menyalin agent loop; cukup menulis tool domain dan memanggil `rt.Run`.
- Tidak ada service lana terpusat yang menjadi titik sempit untuk beban job.
- Event log tetap dipakai untuk audit dan debugging di service, sekarang di Postgres.

**Negative / tradeoffs**
- Dua implementasi store persisten (SQLite dan Postgres) harus diuji dengan test kontrak yang sama.
- API publik package `lana` bertambah (`Agent`, `Budget`, `MaxConcurrentRuns`, store), sehingga lebih banyak yang harus dijaga stabil.
- Service TypeScript (seperti scraper engine sekarang) tidak bisa meng-embed library Go; mereka memakai MCP atau REST.

**Neutral**
- Template service dibuat di repo terpisah dan tidak termasuk roadmap repo ini.
- Driver Postgres dipilih di design spec yang membawanya (kandidat: `github.com/jackc/pgx/v5`).

## Alternatives considered

- **Repo ini sebagai boilerplate service** — satu repo. Ditolak karena harness pribadi dan service punya kebutuhan berbeda, dan setiap service akan membawa kode yang tidak dipakai.
- **Daemon lana terpusat untuk semua service** — satu tempat operasional. Ditolak untuk beban job karena menjadi titik sempit dan tool domain harus bolak-balik lewat jaringan.
- **Lease Postgres dengan tabel dan heartbeat** — tidak bergantung koneksi. Ditolak karena advisory lock lebih sederhana dan otomatis lepas saat proses mati.
