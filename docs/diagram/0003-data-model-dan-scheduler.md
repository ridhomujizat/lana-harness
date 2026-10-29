# 0003. Data Model Dan Scheduler

- **ADR reference**: [0005. SQLite sebagai storage tunggal](../adr/0005-sqlite-sebagai-storage-tunggal.md), [0009. Session event log](../adr/0009-session-sebagai-event-log-append-only.md), [0007. Scheduler cron di dalam daemon](../adr/0007-scheduler-cron-di-dalam-daemon.md)
- **Related diagram**: [0002. Agent Loop Dan Approval](0002-agent-loop-dan-approval.md)
- **Purpose**: skema SQLite, layout folder data, derive riwayat dari event log, dan alur scheduler cron.

> Jaga diagram ini sinkron dengan ADR terkait. Jika ADR dan diagram berbeda, perbarui keduanya dalam satu perubahan.

## Layout Folder Data

```text
~/.lana/                 (override: LANA_HOME)
  config.toml            providers, agents, routes, mcp, channels, modules
  profiles/<profile>.toml patch per profile
  secrets.env            API key dan token bot, permission 0600
  lana.db                SQLite WAL: session event log, memory, cron
  agents/<nama>.md       system prompt per agent
  skills/<nama>/SKILL.md skill dan script pendukung
  workspaces/<agent>/    workspace default jika agent tidak set workspace
  logs/lana.log          log tracing, rotasi harian
```

## Data Model

```mermaid
erDiagram
  SESSIONS ||--o{ SESSION_EVENTS : berisi
  SESSIONS ||--o{ SESSIONS : fork_atau_subagent
  CRON_JOBS ||--o{ CRON_RUNS : menghasilkan
  CRON_RUNS ||--o| SESSIONS : memakai

  SESSION_EVENTS {
    TEXT session_id PK
    INTEGER seq PK
    TEXT kind
    TEXT payload
    TEXT turn_id
    INTEGER created_at
  }

  SESSIONS {
    TEXT id PK
    TEXT agent
    TEXT channel
    TEXT address
    TEXT title
    TEXT status
    TEXT parent_session_id
    TEXT parent_call_id
    INTEGER inherited_seq
    INTEGER last_seq
    INTEGER updated_at
  }

  MEMORIES {
    INTEGER id PK
    TEXT scope
    TEXT content
    TEXT tags
    BLOB embedding
    INTEGER created_at
  }

  CRON_JOBS {
    TEXT id PK
    TEXT title
    TEXT agent
    TEXT schedule
    TEXT tz
    TEXT prompt
    TEXT target_channel
    TEXT target_address
    INTEGER next_run_at
    INTEGER enabled
  }

  CRON_RUNS {
    INTEGER id PK
    TEXT job_id FK
    TEXT session_id FK
    TEXT status
    INTEGER started_at
    INTEGER finished_at
    TEXT error
  }
```

Catatan:

- `SESSION_EVENTS` adalah source of truth. Trigger menolak `UPDATE` dan `DELETE` (kecuali `lana db prune` yang menghapus session utuh).
- `SESSIONS` adalah projection yang bisa dibangun ulang dari event. `parent_session_id` dipakai oleh fork (dengan `inherited_seq`) dan subagent (dengan `parent_call_id`).
- Audit approval = event `approval/asked` dan `approval/decided`. Index `(kind, created_at)` untuk halaman audit.
- `MEMORIES` punya tabel virtual pasangan `memories_fts` (FTS5) yang disinkronkan dengan trigger. `scope` bernilai `global` atau `agent:<nama>`.
- `embedding` kosong di v1. Disiapkan untuk pencarian vektor nanti.
- Secret tidak pernah masuk DB. Source of truth secret adalah env atau `secrets.env`.

## Derive Riwayat Model

```mermaid
flowchart TD
  A["Buka session"] --> B{"Punya parent dengan inherited_seq?"}
  B -- "Ya" --> C["Baca event induk sampai inherited_seq, rekursif"]
  B -- "Tidak" --> D["Mulai dari kosong"]
  C --> E["Tambah event milik session"]
  D --> E
  E --> F{"Ada compaction/summary?"}
  F -- "Ya" --> G["Pakai summary terakhir dan event setelah upto_seq"]
  F -- "Tidak" --> H["Pakai semua event"]
  G --> I["derive_messages: system, user, assistant, tool_result"]
  H --> I
  I --> J["Riwayat untuk request model"]
```

## Scheduler Loop

```mermaid
sequenceDiagram
  autonumber
  participant Sched as Scheduler
  participant Store
  participant Agent as Agent runtime
  participant Channel

  Sched->>Store: Ambil job enabled dengan next_run_at terdekat
  Sched->>Sched: sleep_until atau bangun oleh Notify
  Sched->>Store: Cek run aktif untuk job ini
  alt Masih ada run aktif
    Sched->>Store: Catat run skipped
  else Tidak ada run aktif
    Sched->>Store: Buat cron_run running dan session baru
    Sched->>Agent: Jalankan turn dengan prompt job, policy never
    Agent-->>Sched: Hasil akhir
    Sched->>Channel: Kirim hasil ke target
    Sched->>Store: Update cron_run selesai atau error
  end
  Sched->>Store: Hitung dan simpan next_run_at
```

## Catchup Saat Boot

```mermaid
flowchart TD
  A["Daemon boot"] --> B["Ambil job dengan next_run_at di masa lalu"]
  B --> C{"Terlewat kurang dari 1 jam?"}
  C -- "Ya" --> D["Jalankan sekali"]
  C -- "Tidak" --> E["Catat skipped"]
  D --> F["Hitung next_run_at berikutnya"]
  E --> F
  F --> G["Masuk scheduler loop"]
```
