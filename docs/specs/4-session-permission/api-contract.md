# Contract: Session event log, permission, approval

- **Issue**: [`#4`](https://github.com/ridhomujizat/lana-harness/issues/4)
- **Requirements**: [requirements.md](requirements.md)
- **Revisi requirements**: 2 (REQ-APPR-007 dan AC-APPR-017 ditambah)
- **Status requirements**: Approved
- **Revisi contract**: 2 (rekonsiliasi, lihat `CQ-011` sampai `CQ-022`)
- **Berlaku untuk**: config (`[agents.<nama>]`, `[agents.<nama>.rules]`, `[[modules]]` baris `store`), event log (`session_events`, kind dan payload), dan API Go package `store`, `core`, permission, approval, dan compaction (ADR 0014)
- **Status**: Approved

File ini menetapkan bentuk persis penyimpanan, event, config, dan API Go. Perilaku dimiliki oleh
`requirements.md`. Operasi di sini tidak punya perintah CLI. Perintah dimiliki spec #1.

## Konvensi bersama

| Hal | Keputusan |
|---|---|
| Exit code CLI | Tidak berlaku. Spec #1 memetakan error ke exit code |
| Output stdout dan stderr | Tidak berlaku. Tidak ada output langsung |
| Format config | `$LANA_HOME/config.toml` dan profile. Lihat [Config](#config). Loader dan validasi dimiliki spec #2 |
| Autentikasi | Tidak berlaku. Akses lewat proses user OS. File `lana.db` mode `0600` (`REQ-STORE-001`) |
| Versi schema | `PRAGMA user_version`, nomor migrasi berurutan mulai 1. Migrasi hanya menambah, tidak menulis ulang data |
| Versi payload | Field `v` integer di setiap payload event, mulai 1. Versi lama di-decode ke versi sekarang di memori (`REQ-STORE-013`) |
| ID session | ULID 26 karakter, huruf besar (konvensi spec #1) |
| Waktu | Milidetik Unix, integer. Kolom `created_at`, `updated_at`, `acquired_at` |
| Serialisasi | JSON UTF-8. Field `snake_case`. Payload event berupa objek JSON |
| Kompatibilitas seluruh file | Semua operasi baru. Tidak ada breaking change. Tidak ada deprecation |

## Skema SQL

Migrasi dijalankan berurutan. Setiap file dijalankan dalam satu transaksi, lalu `user_version` diatur
ke nomor migrasi. Berkas `store/migrations/0001_session_log.sql` dan `0002_session_leases.sql`.

`store/migrations/0001_session_log.sql`:

```sql
CREATE TABLE session_events (
  session_id TEXT    NOT NULL,
  seq        INTEGER NOT NULL,
  kind       TEXT    NOT NULL,
  payload    TEXT    NOT NULL,
  turn_id    TEXT,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;

CREATE TABLE sessions (
  id                TEXT    PRIMARY KEY,
  agent             TEXT    NOT NULL,
  channel           TEXT    NOT NULL,
  address           TEXT    NOT NULL,
  workspace         TEXT    NOT NULL,
  title             TEXT    NOT NULL DEFAULT '',
  status            TEXT    NOT NULL CHECK (status IN ('idle', 'running')),
  parent_session_id TEXT    REFERENCES sessions (id) ON DELETE RESTRICT,
  inherited_seq     INTEGER,
  last_seq          INTEGER NOT NULL,
  turn_count        INTEGER NOT NULL DEFAULT 0,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);

CREATE INDEX idx_sessions_channel_address_updated
  ON sessions (channel, address, updated_at DESC);

CREATE INDEX idx_session_events_kind_created
  ON session_events (kind, created_at);

CREATE TABLE session_prune_allow (
  session_id TEXT PRIMARY KEY
) WITHOUT ROWID;

CREATE TRIGGER session_events_no_update
BEFORE UPDATE ON session_events
WHEN NOT EXISTS (SELECT 1 FROM session_prune_allow p WHERE p.session_id = OLD.session_id)
BEGIN
  SELECT RAISE(ABORT, 'session_events is append-only');
END;

CREATE TRIGGER session_events_no_delete
BEFORE DELETE ON session_events
WHEN NOT EXISTS (SELECT 1 FROM session_prune_allow p WHERE p.session_id = OLD.session_id)
BEGIN
  SELECT RAISE(ABORT, 'session_events is append-only');
END;
```

`store/migrations/0002_session_leases.sql`:

```sql
CREATE TABLE session_leases (
  session_id  TEXT    PRIMARY KEY,
  pid         INTEGER NOT NULL,
  host        TEXT    NOT NULL,
  acquired_at INTEGER NOT NULL
);
```

Aturan pemakaian DDL:

- `session_prune_allow` hanya diisi di dalam transaksi `Prune`, lalu dihapus di transaksi yang sama (`API-STORE-011`).
- Kolom `sessions.parent_session_id` memakai `ON DELETE RESTRICT`. Prune session yang masih punya child gagal dengan `ErrSessionHasChildren`.
- Tidak ada tabel `messages`. Tidak ada kolom `embedding`, `memories`, atau `cron_*` di spec ini (M5).

## Event kind dan payload

Field dan tipe setiap payload dimiliki `API-CORE-008` (spec #2, `CQ-029`). Spec #4 hanya menulis
kind dan field di tabel ini. Semua payload berisi `"v": 1`.

| Kind | Field payload (v = 1, lihat `API-CORE-008`) | Ditulis oleh | Masuk riwayat model |
|---|---|---|---|
| `session/created` | `agent`, `channel`, `address`, `workspace`, `parent_session_id` (opsional), `parent_call_id` (opsional), `inherited_seq` (opsional) | `Create`, `Fork` | Tidak |
| `turn/start` | kosong, `turn_id` di kolom | Agent loop | Tidak |
| `turn/end` | `reason`: `end_turn`, `max_steps`, `cancelled`, `interrupted`, `error`. `error` (`PayloadError`, opsional) | Agent loop, resume repair | Tidak |
| `step/start` | `step` (integer), `provider`, `model` | Agent loop | Tidak |
| `step/end` | `step`, `provider`, `model`, `usage` `{input_tokens, output_tokens}` | Agent loop | Tidak (dipakai ambang compaction) |
| `system/message` | `text` (string) | Agent loop | Tidak. Pesan terakhir dipakai sebagai `ChatRequest.System` (`Q-028`) |
| `user/message` | `content` (`[]core.ContentBlock`) | Agent loop | Ya |
| `assistant/message` | `content` (`[]core.ContentBlock` berisi `text` dan `tool_use`), `stop_reason` | Agent loop | Ya |
| `assistant/attempt` | `provider`, `model`, `attempt`, `error` (`PayloadError`) | Agent loop | Tidak |
| `tool/call` | `call_id`, `name`, `input` | Agent loop | Tidak |
| `tool/result` | `call_id`, `status` (`success`, `error`, `rejected`), `output` | Agent loop, resume repair | Ya |
| `approval/asked` | `request_id`, `call_id`, `tool`, `risk`, `label` (opsional). Tanpa argumen | Approval service | Tidak |
| `approval/decided` | `request_id`, `call_id`, `outcome`, `reason` (opsional) | Approval service, resume repair | Tidak |
| `approval/policy` | `policy` (`trust`, `ask`, `never`) | `SetPolicy` | Tidak |
| `model/selection` | `provider`, `model`, `agent` (opsional, hanya dari `SetAgent`) | `SetModel`, `SetAgent` (spec #3) | Tidak |
| `compaction/summary` | `summary`, `upto_seq`, `tokens_before`, `tokens_after`, `model` | Compaction | Ya (sebagai pesan `user`, `Q-013`) |

Aturan derive (detail di design, `API-STORE-010`):

- `tool/call` tidak masuk riwayat. Blok `tool_use` sudah ada di `assistant/message`.
- `system/message` tidak masuk riwayat `DeriveMessages`. Pesan terakhirnya dipakai sebagai `ChatRequest.System` (`Q-028`).
- Grant `allowed_session` tidak punya kind sendiri. Grant adalah `approval/decided` dengan `outcome = allowed_session`. Pola grant dihitung dari `tool/call` yang dirujuk `call_id`.

## Config

Baca oleh loader spec #2. Validasi di sini ikut aturan `REQ-CONF-003` spec #2.

| Key | Tipe | Default | Validasi |
|---|---|---|---|
| `[[modules]]` baris `id = "store"`, `module = "sqlite_store"`, `config.path` | string | `lana.db` | Tidak kosong. Relatif ke `LANA_HOME` jika tidak absolut |
| `[agents.<nama>] permission` | enum | `ask` | `trust`, `ask`, atau `never`. Nilai lain adalah error validasi dengan saran terdekat |
| `[agents.<nama>.rules] allow` | array string | kosong | Setiap item harus lolos `ParsePattern` (`API-PERM-002`) |
| `[agents.<nama>.rules] deny` | array string | kosong | Sama seperti `allow` |
| `[agents.<nama>] context_window` | integer | `128000` | Lebih dari 0 |

Validasi pola `rules` memakai `internal/pattern.ParsePattern` (`API-PERM-001`, `CQ-017`). Key `permission` dan `context_window` tercatat di daftar key spec #2 (`API-CONF-004`).

Contoh (tidak menggantikan contoh di ADR 0004):

```toml
[agents.coder]
permission = "ask"
context_window = 128000

[agents.coder.rules]
allow = ["read_file", "bash(git status*)"]
deny  = ["write_file(~/.ssh/**)"]
```

Modul baru di profile. Keempat modul ada di base (`API-CONF-009` spec #2, `CQ-031`, `Q-031`):

| Modul (`module`) | Service yang dipasang | Requires | Hook |
|---|---|---|---|
| `sqlite_store` | `sessions` | — | `session.event` (observe, sebagai pengirim) |
| `approval` | `approval` | `sessions` | `approval.request` (default: `unavailable`) |
| `permission` | — | `sessions`, `approval` | listener `tools.pre_execute` |
| `compaction` | `compaction` | `sessions`, `llm` | listener `agent.pre_step`, `llm.stream` |

## Signature Go

Blok ini menetapkan bentuk API. Nama package mengikuti ADR 0013.

### Tipe core (dimiliki spec #2)

Tipe yang dipakai spec ini didefinisikan di spec #2. Tabel ini mencatat revisi dari revisi 1:

| Tipe | Ada di | Revisi di spec ini |
|---|---|---|
| `core.NewSession` | `API-CORE-009` | Field `Workspace` dan `ParentCallID` masuk (`CQ-011`) |
| `core.EventDraft` | `API-CORE-008` | Menggantikan `NewEvent` (`CQ-024` spec #2) |
| `core.SessionInfo` | `API-CORE-009` | Gabungan field spec #2 dan spec #4 |
| `core.SessionFilter` | `API-CORE-009` | `Limit` 0 = tanpa batas (`CQ-022` spec #2) |
| `core.ApprovalRequest` | `API-CORE-011` | `ToolName`, `Label`. Tanpa `Input` dan `Summary` (`CQ-021` spec #2) |
| `core.ToolDecision` | `API-CORE-015` | `Allowed`, `Outcome`, `Status` (`ToolStatus`), `Reason` (`CQ-020` spec #2) |
| `core.ToolsPreExecuteInput` | `API-CORE-015` | Input listener permission (`CQ-017` spec #2) |
| `core.CompactionResult`, `core.Compactor` | `API-CORE-015` | Tanpa perubahan |
| `core.ProviderError`, `core.ErrContextTooLong` | `API-CORE-015`, `API-CORE-014` | Tanpa perubahan (`CQ-026` spec #2) |
| `core.LeaseHeldError` | `API-CORE-014` | Menggantikan `ErrLeaseHeld` |
| `core.ErrPrefixTooShort`, `core.ErrAmbiguousPrefix`, `core.ResolvePrefix` | `API-CORE-014`, `API-CORE-015` | Pindah dari package `store` (`CQ-023` spec #2) |
| `core.Lease` | Dihapus | `TryLease` mengembalikan `release func() error` (`CQ-023` spec #2) |

Interface `core.SessionStore` dan `core.ApprovalService` ada di `API-CORE-009` dan `API-CORE-011`.
Method yang dipakai spec ini:

```go
// core.SessionStore (API-CORE-009). Method Info, ChainEvents, DeriveMessages ditambah spec ini (CQ-022 spec #2).
Create(ctx context.Context, s NewSession) (SessionInfo, error)
Open(ctx context.Context, id string) (SessionInfo, error) // dengan resume repair, butuh lease
Info(ctx context.Context, id string) (SessionInfo, error) // baca saja, tanpa repair
Append(ctx context.Context, id string, drafts []EventDraft) ([]SessionEvent, error)
Events(ctx context.Context, id string, fromSeq int64) iter.Seq2[SessionEvent, error]
ChainEvents(ctx context.Context, id string) ([]SessionEvent, error)
DeriveMessages(ctx context.Context, id string) ([]Message, error)
List(ctx context.Context, f SessionFilter) ([]SessionInfo, error)
FindPrefix(ctx context.Context, prefix string) ([]SessionInfo, error)
Fork(ctx context.Context, parentID string, atSeq int64) (SessionInfo, error)
TryLease(ctx context.Context, id string) (release func() error, err error)
```

Catatan untuk `Fork`: child mewarisi `Agent`, `Channel`, `Address`, dan `Workspace` dari induk. `ParentSessionID` dan `InheritedSeq` diisi store. Child dengan agent lain (subagent, M4) dibuat lewat `Create` dengan `ParentSessionID` dan `ParentCallID` (`CQ-011`).

### Package `store` (di luar interface core)

```go
func Open(ctx context.Context, path string) (*Store, error) // path absolut
func (s *Store) Close() error
func (s *Store) Prune(ctx context.Context, sessionID string) error
func (s *Store) RebuildProjections(ctx context.Context) error
func NormalizePayload(kind string, raw json.RawMessage) (json.RawMessage, error)

var (
	ErrSessionNotFound       = errors.New("session tidak ditemukan")
	ErrForkNotAtTurnBoundary = errors.New("fork hanya boleh di turn/end")
	ErrTurnActive            = errors.New("masih ada turn yang berjalan")
	ErrLeaseNotHeld          = errors.New("session tidak dipegang proses ini")
	ErrSessionHasChildren    = errors.New("session masih punya child")
	ErrSchemaTooNew          = errors.New("schema database lebih baru dari binary")
	ErrStoreClosed           = errors.New("store sudah ditutup")
	ErrSessionChanged        = errors.New("session berubah saat compaction")
	ErrNothingToCompact      // sentinel store. Pesan ditentukan implementasi
	ErrInvalidPolicy         // sentinel store. Pesan ditentukan implementasi
)

// ErrUnknownPayloadVersion = core.ErrUnknownPayloadVersion (API-CORE-014).
// ErrPrefixTooShort, ErrAmbiguousPrefix, dan LeaseHeldError ada di core (API-CORE-014).
```

### Pola aturan (`internal/pattern`, dipakai `config` dan `tools/permission`)

```go
type Pattern struct {
	Raw  string
	Tool string
	Arg  *string // nil jika pola tanpa argumen
}

func ParsePattern(s string) (Pattern, error) // error memuat posisi dan alasan
func (p Pattern) Match(tool string, arg Argument) bool

type ArgKind int // ArgCommand, ArgPath, ArgURL, ArgAgent, ArgRaw
type Argument struct {
	Kind  ArgKind
	Value string // path sudah diselesaikan ke absolut dan dibersihkan
	Alt   string // path sebenarnya setelah EvalSymlinks, kosong jika bukan path
}
```

Pencocokan path (`REQ-PERM-001`): rule `deny` cocok jika `Value` atau `Alt` cocok. Rule `allow`
cocok jika `Value` dan `Alt` keduanya cocok.

### Package permission (`tools/permission`)

Listener `tools.pre_execute` dipasang sebagai `core.WaterfallListener[core.ToolsPreExecuteInput, core.ToolDecision]`.
Input dimiliki spec #2 (`API-CORE-015`). Field yang dipakai: `AgentName`, dan dari `Call`:
`SessionID`, `TurnID`, `CallID`, `Name`, `Input` (`json.RawMessage`), `Risk`. `Label` dihitung dari
`AgentName` jika session punya induk (`ParentSessionID`), kosong jika tidak.

### Package approval dan compaction

```go
// package tools/permission (approval service)
// Implementasi core.ApprovalService. Clock dan timeout dapat diganti untuk test.
// Timeout default 10 menit.

// package agent/compaction
type Service struct{ /* ... */ }

// Implementasi core.Compactor (API-CORE-015). Dipasang dengan key core.ServiceCompaction.
func (s *Service) CompactNow(ctx context.Context, sessionID string) (core.CompactionResult, error)
```

### Penjawab library (package `lana`)

```go
// Lihat API-APPR-005.
func ApproveWith(fn func(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome) lana.Option
```

## Index operasi

| ID operasi | Jenis | Nama | Requirements | Acceptance criteria |
|---|---|---|---|---|
| `API-STORE-001` | Go | Buka store, migrasi, DDL, trigger | `REQ-STORE-001`, `REQ-STORE-002`, `REQ-STORE-004`, `REQ-STORE-016` | `AC-STORE-001` sampai `AC-STORE-004`, `AC-STORE-007`, `AC-STORE-008`, `AC-STORE-038` |
| `API-STORE-002` | Go | `Create` | `REQ-STORE-006` | `AC-STORE-012` |
| `API-STORE-003` | Go | `Open` dengan resume repair, dan `Info` (baca saja) | `REQ-STORE-006` | `AC-STORE-013` sampai `AC-STORE-016` |
| `API-STORE-004` | Go | `Append` | `REQ-STORE-003`, `REQ-STORE-005`, `REQ-STORE-015` | `AC-STORE-005` sampai `AC-STORE-006`, `AC-STORE-009` sampai `AC-STORE-011`, `AC-STORE-036`, `AC-STORE-037` |
| `API-STORE-005` | Go | `Events` | `REQ-STORE-008` | `AC-STORE-019` |
| `API-STORE-006` | Go | `List` | `REQ-STORE-008` | `AC-STORE-020` |
| `API-STORE-007` | Go | `FindPrefix` dan `core.ResolvePrefix` | `REQ-STORE-009` | `AC-STORE-021` sampai `AC-STORE-023` |
| `API-STORE-008` | Go | `Fork` | `REQ-STORE-007` | `AC-STORE-017`, `AC-STORE-018` |
| `API-STORE-009` | Go | `TryLease` (mengembalikan `release`) | `REQ-STORE-010` | `AC-STORE-024` sampai `AC-STORE-027` |
| `API-STORE-010` | Go | `DeriveMessages` dan `ChainEvents` | `REQ-STORE-011` | `AC-STORE-028` sampai `AC-STORE-030` |
| `API-STORE-011` | Go | `Prune` | `REQ-STORE-014` | `AC-STORE-034`, `AC-STORE-035` |
| `API-STORE-012` | Go | `RebuildProjections` | `REQ-STORE-012` | `AC-STORE-031` |
| `API-STORE-013` | Go | `NormalizePayload` | `REQ-STORE-013` | `AC-STORE-032`, `AC-STORE-033` |
| `API-PERM-001` | Go | `ParsePattern` dan `Match` | `REQ-PERM-001` | `AC-PERM-001` sampai `AC-PERM-003` |
| `API-PERM-002` | Config | Validasi `permission` dan `rules` | `REQ-PERM-001` | `AC-PERM-004` |
| `API-PERM-003` | Go | Listener `tools.pre_execute` | `REQ-PERM-002` sampai `REQ-PERM-006` | `AC-PERM-005` sampai `AC-PERM-019` |
| `API-APPR-001` | Go | `SetPolicy` dan `EffectivePolicy` | `REQ-APPR-001` | `AC-APPR-001` sampai `AC-APPR-004` |
| `API-APPR-002` | Go | `Request` (audit, timeout, cancel, fail-closed) | `REQ-APPR-002`, `REQ-APPR-004`, `REQ-APPR-005` | `AC-APPR-005`, `AC-APPR-006`, `AC-APPR-011` sampai `AC-APPR-014` |
| `API-APPR-003` | Go | Hook `approval.request` (penjawab) | `REQ-APPR-003` | `AC-APPR-007` sampai `AC-APPR-010` |
| `API-APPR-004` | Go | Fungsi batas izin subagent | `REQ-APPR-006` | `AC-APPR-015`, `AC-APPR-016` |
| `API-APPR-005` | Go | `lana.ApproveWith` (penjawab library) | `REQ-APPR-007` | `AC-APPR-017` |
| `API-COMP-001` | Go | `CompactNow` | `REQ-COMP-002` | `AC-COMP-004`, `AC-COMP-005` |
| `API-COMP-002` | Go | Compaction otomatis dan retry | `REQ-COMP-001`, `REQ-COMP-003` | `AC-COMP-001` sampai `AC-COMP-003`, `AC-COMP-006`, `AC-COMP-007` |

## API-STORE-001: Buka store, migrasi, DDL, trigger

- **Tujuan**: membuka `lana.db`, menjalankan migrasi, dan memasang writer dan read pool.
- **Requirements**: `REQ-STORE-001`, `REQ-STORE-002`, `REQ-STORE-004`, `REQ-STORE-016`
- **Acceptance criteria**: `AC-STORE-001` sampai `AC-STORE-004`, `AC-STORE-007`, `AC-STORE-008`, `AC-STORE-038`
- **Stabilitas**: Baru
- **Otorisasi**: Proses pemilik `LANA_HOME`. Tidak ada autentikasi tambahan.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Config | `[[modules]]` `store` `config.path` | string | Tidak | Default `lana.db`. Relatif ke `LANA_HOME` |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Database baru atau sudah ada, migrasi lengkap | Tanpa error | `*Store` siap. `user_version` = 2. Tidak ada event |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Path tidak bisa dibuka atau folder tidak ada | Tidak berlaku | error biasa | Ya | Boot gagal, pesan menyebut path |
| `user_version` lebih tinggi dari migrasi tertinggi | Tidak berlaku | `ErrSchemaTooNew` | Tidak | Database tidak diubah |
| Migrasi gagal di tengah | Tidak berlaku | error biasa | Ya | Transaksi migrasi di-rollback, `user_version` tidak naik |

### Efek samping dan jaminan

- **Transisi state**: Tidak ada event. Membuat file `0600` jika belum ada, dan membuat `-wal` dan `-shm`.
- **Idempotensi**: Membuka ulang tidak menjalankan migrasi lagi.
- **Konkurensi**: Pragma `journal_mode = WAL`, `synchronous = NORMAL`, `foreign_keys = ON`, `busy_timeout = 5000`.
- **Paparan data**: Tidak ada secret di database (ADR 0005).

## API-STORE-002: `Create`

- **Tujuan**: membuat session dan menulis `session/created`.
- **Requirements**: `REQ-STORE-006`
- **Acceptance criteria**: `AC-STORE-012`
- **Stabilitas**: Baru
- **Otorisasi**: Proses pemilik store.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `in` | `core.NewSession` | Ya | `Agent`, `Channel`, `Address`, `Workspace` wajib tidak kosong. Parent opsional |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Session dibuat | Tidak berlaku | `core.SessionInfo` dengan ID ULID baru. Event `session/created` seq 1. Status `idle` |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Field wajib kosong | Tidak berlaku | error biasa | Tidak | Session tidak dibuat |
| `ParentSessionID` tidak ada | Tidak berlaku | `ErrSessionNotFound` | Tidak | Session tidak dibuat |
| Store ditutup | Tidak berlaku | `ErrStoreClosed` | Ya | Tidak ada event |

### Efek samping dan jaminan

- **Transisi state**: Baris `sessions` baru berstatus `idle`. Lease tidak diambil (`Q-015`).
- **Idempotensi**: Tidak berlaku. Setiap panggilan membuat session baru.
- **Konkurensi**: Lewat writer dan `BEGIN IMMEDIATE`.
- **Paparan data**: Tidak ada.

## API-STORE-003: `Open` dengan resume repair

- **Tujuan**: membuka session untuk pemegang lease dan menutup turn yatim.
- **Requirements**: `REQ-STORE-006`
- **Acceptance criteria**: `AC-STORE-013` sampai `AC-STORE-016`
- **Stabilitas**: Baru
- **Otorisasi**: Proses pemegang lease `session_id` (cek `pid` dan `host` di `session_leases`).

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `sessionID` | string | Ya | ULID lengkap, bukan prefix |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Session idle | Tidak berlaku | `SessionInfo`. Tidak ada event |
| Turn yatim ditutup | Tidak berlaku | `SessionInfo` dengan status `idle`. Event repair sesuai urutan di design |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Proses tidak memegang lease | Tidak berlaku | `ErrLeaseNotHeld` | Ya, setelah lease | Tidak ada event |
| Session tidak ada | Tidak berlaku | `ErrSessionNotFound` | Tidak | Tidak ada event |

### Efek samping dan jaminan

- **Transisi state**: Turn yatim menjadi `idle`. Urutan event: `approval/decided` untuk approval yatim, `tool/result` error untuk call yatim, lalu `turn/end` reason `interrupted`.
- **Info**: sama dengan `Open` tanpa lease dan tanpa repair. Hanya membaca `SessionInfo`.
- **Idempotensi**: Panggilan kedua tidak menulis event.
- **Konkurensi**: Repair satu transaksi `BEGIN IMMEDIATE`. Lease wajib dipegang sebelum repair.
- **Paparan data**: Output repair berisi teks tetap `interrupted`, tanpa isi tool.

## API-STORE-004: `Append`

- **Tujuan**: menulis batch event atomik ke session.
- **Requirements**: `REQ-STORE-003`, `REQ-STORE-005`, `REQ-STORE-015`
- **Acceptance criteria**: `AC-STORE-005`, `AC-STORE-006`, `AC-STORE-009` sampai `AC-STORE-011`, `AC-STORE-036`, `AC-STORE-037`
- **Stabilitas**: Baru
- **Otorisasi**: Proses pemegang lease. Append tanpa lease diizinkan hanya dari jalur internal (repair, `Create`).

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `sessionID` | string | Ya | Session harus ada |
| Argumen | `drafts` | `[]core.EventDraft` | Ya | Tidak kosong. `Kind` harus dikenal. `turn/start` ditolak jika ada turn terbuka |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Batch tersimpan | Tidak berlaku | `[]core.SessionEvent` dengan seq berurutan. Projection diperbarui. Hook `session.event` dikirim setelah commit |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| `turn/start` saat turn terbuka | Tidak berlaku | `ErrTurnActive` | Tidak | Tidak ada event |
| Kind tidak dikenal atau payload tidak valid | Tidak berlaku | error biasa | Tidak | Tidak ada event |
| `SQLITE_BUSY` lebih dari 5 detik | Tidak berlaku | error biasa | Ya | Tidak ada event |
| Store ditutup | Tidak berlaku | `ErrStoreClosed` | Ya | Tidak ada event |

### Efek samping dan jaminan

- **Transisi state**: `turn/start` membuat status `running`. `turn/end` untuk turn terbuka membuat status `idle`. `turn_count` bertambah saat `turn/start`.
- **Idempotensi**: Tidak berlaku. Setiap panggilan menambah event.
- **Konkurensi**: Writer tunggal per proses. `BEGIN IMMEDIATE` untuk lintas proses.
- **Paparan data**: Payload disimpan apa adanya. Tidak ada secret yang ditambahkan store.

## API-STORE-005: `Events`

- **Tujuan**: membaca event sendiri session mulai dari seq tertentu.
- **Requirements**: `REQ-STORE-008`
- **Acceptance criteria**: `AC-STORE-019`
- **Stabilitas**: Baru
- **Otorisasi**: Proses pemilik store.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `sessionID` | string | Ya | Session harus ada |
| Argumen | `fromSeq` | int64 | Ya | Seq minimal, inklusif. Nilai di bawah 1 dibaca sebagai 1 |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Session ada | Tidak berlaku | `iter.Seq2[core.SessionEvent, error]` urut seq naik. Kosong jika tidak ada event |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Session tidak ada | Tidak berlaku | `ErrSessionNotFound` | Tidak | Tidak ada hasil |

### Efek samping dan jaminan

- **Transisi state**: Tidak ada.
- **Idempotensi**: Berulang menghasilkan hasil sama.
- **Konkurensi**: Baca di read pool dalam satu snapshot.
- **Paparan data**: Payload dikembalikan apa adanya.

## API-STORE-006: `List`

- **Tujuan**: daftar session per channel dan address untuk `--continue` dan `/sessions`.
- **Requirements**: `REQ-STORE-008`
- **Acceptance criteria**: `AC-STORE-020`
- **Stabilitas**: Baru
- **Otorisasi**: Proses pemilik store.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `f` | `core.SessionFilter` | Ya | `Channel` dan `Address` cocok persis. `Limit` 0 = tanpa batas (`CQ-022` spec #2). `Limit` negatif ditolak |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Ada hasil | Tidak berlaku | `[]core.SessionInfo` urut `updated_at` menurun, paling banyak `Limit` (0 = semua) |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| `Limit` negatif | Tidak berlaku | error biasa | Tidak | Tidak ada hasil |

### Efek samping dan jaminan

- **Transisi state**: Tidak ada.
- **Idempotensi**: Berulang menghasilkan hasil sama jika tidak ada tulis.
- **Konkurensi**: Read pool.
- **Paparan data**: Tidak ada.

## API-STORE-007: `FindPrefix`

- **Tujuan**: mencari session dari prefix yang diketik user. Aturan pemilihan satu session ada di `core.ResolvePrefix`.
- **Requirements**: `REQ-STORE-009`
- **Acceptance criteria**: `AC-STORE-021` sampai `AC-STORE-023`
- **Stabilitas**: Baru
- **Otorisasi**: Proses pemilik store.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `prefix` | string | Ya | Dibandingkan dalam huruf besar. Panjang minimal dicek `core.ResolvePrefix` |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Ada yang cocok | Tidak berlaku | `[]core.SessionInfo` (bisa berisi lebih dari satu). Caller memanggil `core.ResolvePrefix` |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---|---|---:|---|
| Prefix kurang dari 6 karakter (lewat `core.ResolvePrefix`) | Tidak berlaku | `core.ErrPrefixTooShort` | Tidak | Tidak ada hasil |
| Tidak ada yang cocok (lewat `core.ResolvePrefix`) | Tidak berlaku | `core.ErrNotFound` | Tidak | Tidak ada hasil |
| Lebih dari satu cocok (lewat `core.ResolvePrefix`) | Tidak berlaku | `*core.AmbiguousPrefixError` dengan `Candidates` urut naik, `Is(core.ErrAmbiguousPrefix)` | Tidak | Daftar kandidat dikembalikan ke pemanggil |

### Efek samping dan jaminan

- **Transisi state**: Tidak ada.
- **Idempotensi**: Berulang menghasilkan hasil sama.
- **Konkurensi**: Read pool.
- **Paparan data**: Tidak ada.

## API-STORE-008: `Fork`

- **Tujuan**: membuat session turunan dari `turn/end` induk tanpa menyalin event.
- **Requirements**: `REQ-STORE-007`
- **Acceptance criteria**: `AC-STORE-017`, `AC-STORE-018`
- **Stabilitas**: Baru
- **Otorisasi**: Proses pemilik store. Pemanggil di atas bertanggung jawab atas lease session baru. Fork tidak punya argumen agent (`CQ-022` spec #2).

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `parentID` | string | Ya | Session harus ada |
| Argumen | `atSeq` | int64 | Ya | Harus seq event `turn/end` milik induk |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| `atSeq` valid | Tidak berlaku | `core.SessionInfo` session baru. Event `session/created` berisi `parent_session_id` dan `inherited_seq`. Agent, channel, address, dan workspace dari induk |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| `atSeq` bukan `turn/end` | Tidak berlaku | `ErrForkNotAtTurnBoundary` | Tidak | Tidak ada session baru |
| Induk tidak ada | Tidak berlaku | `ErrSessionNotFound` | Tidak | Tidak ada session baru |

### Efek samping dan jaminan

- **Transisi state**: Session baru berstatus `idle`. Induk tidak berubah.
- **Idempotensi**: Tidak berlaku. Setiap panggilan membuat session baru.
- **Konkurensi**: Lewat writer.
- **Paparan data**: Tidak ada.

## API-STORE-009: `TryLease`

- **Tujuan**: memberi satu session ke satu proses (`session_leases`).
- **Requirements**: `REQ-STORE-010`
- **Acceptance criteria**: `AC-STORE-024` sampai `AC-STORE-027`
- **Stabilitas**: Baru
- **Otorisasi**: Proses pemanggil. Lease diambil atas nama `os.Getpid()` dan `os.Hostname()`.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `id` | string | Ya | Session harus ada |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Lease kosong, atau pemegang mati di host sama | Tidak berlaku | `release func() error`. Baris `session_leases` berisi PID, host, waktu |
| `release` dipanggil | Tidak berlaku | Baris lease dihapus jika masih milik proses ini |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Pemegang hidup di host sama, atau pemegang di host lain | Tidak berlaku | `*core.LeaseHeldError{PID, Host}` (`errors.As`) | Ya, setelah lepas | Lease tidak berubah |
| Session tidak ada | Tidak berlaku | `ErrSessionNotFound` | Tidak | Tidak ada lease |

### Efek samping dan jaminan

- **Transisi state**: Baris `session_leases` ditambah atau diganti. Tidak ada event.
- **Idempotensi**: Panggilan kedua dari proses yang sama juga ditolak dengan `LeaseHeldError`, karena PID pemegang masih hidup. Ini sesuai design spec #1.
- **Konkurensi**: `BEGIN IMMEDIATE`. Cek PID lewat `syscall.Kill(pid, 0)`: `nil` atau `EPERM` berarti hidup, `ESRCH` berarti mati.
- **Paparan data**: PID dan host terlihat di pesan `LeaseHeldError`.

## API-STORE-010: `DeriveMessages` dan `ChainEvents`

- **Tujuan**: menyusun riwayat model dan rantai event dari session (dipakai agent loop, permission, approval, dan compaction).
- **Requirements**: `REQ-STORE-011`
- **Acceptance criteria**: `AC-STORE-028` sampai `AC-STORE-030`
- **Stabilitas**: Baru
- **Otorisasi**: Proses pemilik store.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `sessionID` | string | Ya | Session harus ada |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Session ada | Tidak berlaku | `[]core.Message` dari `core.DeriveMessages` (`API-CORE-015`) atas `ChainEvents`, sesuai pemetaan di design (`Q-012`, `Q-013`) |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Session tidak ada | Tidak berlaku | `ErrSessionNotFound` | Tidak | Tidak ada hasil |
| Payload versi tidak dikenal | Tidak berlaku | `core.ErrUnknownPayloadVersion` | Tidak | Derive gagal, tidak ada hasil parsial |

### Efek samping dan jaminan

- **Transisi state**: Tidak ada.
- **Idempotensi**: Fungsi murni terhadap isi log. Hasil sama jika log sama.
- **Konkurensi**: Satu read transaction untuk seluruh rantai.
- **Paparan data**: Isi konten model. Tidak dicatat ke log.

## API-STORE-011: `Prune`

- **Tujuan**: menghapus satu session utuh dengan event, projection, dan lease.
- **Requirements**: `REQ-STORE-014`
- **Acceptance criteria**: `AC-STORE-034`, `AC-STORE-035`
- **Stabilitas**: Baru. Hanya fungsi store. Tidak ada perintah CLI (`Q-010`).
- **Otorisasi**: Proses pemilik store. Pemanggil harus memegang lease session.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `sessionID` | string | Ya | Session harus ada dan tidak punya child |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Session tanpa child | Tidak berlaku | Event, baris `sessions`, dan lease session hilang dalam satu transaksi |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Session punya child | Tidak berlaku | `ErrSessionHasChildren` | Tidak | Tidak ada yang terhapus |
| Session tidak ada | Tidak berlaku | `ErrSessionNotFound` | Tidak | Tidak ada perubahan |

### Efek samping dan jaminan

- **Transisi state**: Session hilang. Ini satu-satunya pengecualian append-only.
- **Idempotensi**: Panggilan kedua mengembalikan `ErrSessionNotFound`.
- **Konkurensi**: Satu transaksi `BEGIN IMMEDIATE`. `session_prune_allow` diisi dan dihapus di transaksi itu.
- **Paparan data**: Tidak ada.

## API-STORE-012: `RebuildProjections`

- **Tujuan**: membangun ulang tabel `sessions` dari `session_events`.
- **Requirements**: `REQ-STORE-012`
- **Acceptance criteria**: `AC-STORE-031`
- **Stabilitas**: Baru
- **Otorisasi**: Proses pemilik store.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | (tidak ada) | — | — | — |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Rebuild selesai | Tidak berlaku | Tabel `sessions` berisi hasil dari event |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Payload tidak bisa di-decode | Tidak berlaku | `core.ErrUnknownPayloadVersion` | Tidak | Rebuild di-rollback |

### Efek samping dan jaminan

- **Transisi state**: Hanya tabel `sessions`. Tidak ada event.
- **Idempotensi**: Hasil sama untuk log sama.
- **Konkurensi**: Satu transaksi `BEGIN IMMEDIATE`.
- **Paparan data**: Tidak ada.

## API-STORE-013: `NormalizePayload`

- **Tujuan**: men-decode payload ke versi sekarang di memori.
- **Requirements**: `REQ-STORE-013`
- **Acceptance criteria**: `AC-STORE-032`, `AC-STORE-033`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku. Fungsi murni.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `kind` | string | Ya | Kind yang dikenal |
| Argumen | `raw` | `json.RawMessage` | Ya | Harus berisi `v` integer |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| `v` dikenal | Tidak berlaku | Payload versi sekarang. Baris database tidak diubah |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| `v` tidak dikenal | Tidak berlaku | `core.ErrUnknownPayloadVersion` | Tidak | Tidak ada hasil |
| `v` tidak ada atau bukan integer | Tidak berlaku | error biasa | Tidak | Tidak ada hasil |

### Efek samping dan jaminan

- **Transisi state**: Tidak ada.
- **Idempotensi**: Berulang menghasilkan hasil sama.
- **Konkurensi**: Tidak berlaku.
- **Paparan data**: Tidak ada.

## API-PERM-001: `ParsePattern` dan `Match`

- **Tujuan**: mengurai dan mencocokkan pola `tool` atau `tool(arg-glob)`.
- **Requirements**: `REQ-PERM-001`
- **Acceptance criteria**: `AC-PERM-001` sampai `AC-PERM-003`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku. Fungsi murni.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `s` | string | Ya | Format `tool` atau `tool(...)`. Nama tool `[a-z_][a-z0-9_]*` atau `mcp__<server>__<tool>` |

Grammar pola:

```text
pola     := tool | tool "(" arg ")"
tool     := nama tool, cocok persis
arg      := sisa teks sampai ")" terakhir, boleh mengandung "(" dan ")"
wildcard := "*" (urutan karakter) | "?" (satu karakter) | "**" (hanya argumen path)
escape   := "\" diikuti "*", "?", "\", "(", atau ")" lalu karakter itu literal
```

Aturan pencocokan (`REQ-PERM-001`):

| Argumen | `*` dan `?` | `**` | Sumber nilai |
|---|---|---|---|
| `command` | Melewati `/` | Tidak berlaku | Input `command` apa adanya |
| `path` | Tidak melewati `/` | Melewati `/` | Path absolut hasil `filepath.Clean`, `~` diganti home |
| `url`, `agent`, `raw` | Melewati karakter apa pun | Tidak berlaku | Input apa adanya, JSON compact untuk `raw` |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Pola valid | Tidak berlaku | `Pattern` |
| `Match` | Tidak berlaku | `true` jika tool sama dan argumen cocok |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Tanda kurung tidak tertutup | Tidak berlaku | error biasa (posisi dan alasan) | Tidak | Config ditolak |
| Nama tool tidak valid | Tidak berlaku | error biasa | Tidak | Config ditolak |

### Efek samping dan jaminan

- **Transisi state**: Tidak ada.
- **Idempotensi**: Fungsi murni.
- **Konkurensi**: Tidak berlaku.
- **Paparan data**: Tidak ada.

## API-PERM-002: Validasi `permission` dan `rules`

- **Tujuan**: memastikan config permission valid saat dimuat.
- **Requirements**: `REQ-PERM-001`
- **Acceptance criteria**: `AC-PERM-004`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Config | `agents.<nama>.permission` | enum | Tidak | Default `ask` |
| Config | `agents.<nama>.rules.allow`, `.deny` | array string | Tidak | Setiap item lolos `ParsePattern` |
| Config | `agents.<nama>.context_window` | integer | Tidak | Default 128000, lebih dari 0 |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Semua nilai valid | Tidak berlaku | Config diterima |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Pola tidak valid | Tidak berlaku | error validasi spec #2 | Tidak | Error menyebut key (contoh `agents.coder.rules.allow`), nilai pola, dan alasan |
| `permission` di luar enum | Tidak berlaku | error validasi spec #2 | Tidak | Error menyebut key dan saran terdekat |

### Efek samping dan jaminan

- **Transisi state**: Tidak ada.
- **Idempotensi**: Tidak berlaku.
- **Konkurensi**: Tidak berlaku.
- **Paparan data**: Tidak ada.

## API-PERM-003: Listener `tools.pre_execute`

- **Tujuan**: memutuskan apakah tool boleh berjalan.
- **Requirements**: `REQ-PERM-002` sampai `REQ-PERM-006`
- **Acceptance criteria**: `AC-PERM-005` sampai `AC-PERM-019`
- **Stabilitas**: Baru. Sensitif keamanan
- **Otorisasi**: Keputusan hanya dibentuk di listener ini. Tidak ada jalur lain yang membuat `ToolDecision.Allowed = true` untuk tool yang lewat hook.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Hook | `core.ToolsPreExecuteInput` | struct (spec #2, `API-CORE-015`) | Ya | `AgentName`, `Call.SessionID`, `Call.TurnID`, `Call.CallID`, `Call.Name`, `Call.Input`, `Call.Risk` |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Deny tidak cocok, workspace aman, dan salah satu jalur izin cocok | Tidak berlaku | `ToolDecision{Allowed: true}`. Outcome kosong, atau `allowed_once` atau `allowed_session` jika lewat approval |
| Ditolak oleh rule, workspace, policy, atau outcome approval | Tidak berlaku | `ToolDecision{Allowed: false, Status: ToolStatusRejected, Reason}` |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Store gagal dibaca (policy, grant, session) | Tidak berlaku | error biasa | Ya | `Allowed: false`, `Reason` menyebut kegagalan baca |
| `EvalSymlinks` gagal selain path belum ada | Tidak berlaku | error biasa | Ya | `Allowed: false` |
| Modul `permission` tidak aktif | Tidak berlaku | Default spec #3 (fail-closed) | Tidak | Tool ditolak |

### Efek samping dan jaminan

- **Transisi state**: Bisa menulis `approval/asked` dan `approval/decided` lewat `API-APPR-002`.
- **Idempotensi**: Tidak berlaku. Setiap `tool/call` diputuskan sekali.
- **Konkurensi**: Membaca policy terbaru setiap kali (`Q-014`). Hasil satu call tidak mempengaruhi call lain di turn yang sama kecuali lewat log.
- **Paparan data**: `Reason` menyebut nama tool dan pola yang cocok. Tidak menyalin argumen tool (ADR 0004).

## API-APPR-001: `SetPolicy` dan `EffectivePolicy`

- **Tujuan**: mengatur dan membaca policy session.
- **Requirements**: `REQ-APPR-001`
- **Acceptance criteria**: `AC-APPR-001` sampai `AC-APPR-004`
- **Stabilitas**: Baru
- **Otorisasi**: Proses pemilik store dan pemanggil dari channel pemilik session (CLI `/permission`, `/yolo`, `--yolo`). Tidak ada jalur lain.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `sessionID` | string | Ya | Session harus ada |
| Argumen | `policy` | `core.Policy` | Ya (`SetPolicy`) | `trust`, `ask`, atau `never` |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| `SetPolicy` | Tidak berlaku | Event `approval/policy` tertulis |
| `EffectivePolicy` | Tidak berlaku | Nilai `approval/policy` terakhir di rantai. Jika tidak ada, `permission` agent |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Nilai policy tidak valid | Tidak berlaku | `ErrInvalidPolicy` | Tidak | Tidak ada event |
| Session tidak ada | Tidak berlaku | `ErrSessionNotFound` | Tidak | Tidak ada event |

### Efek samping dan jaminan

- **Transisi state**: `approval/policy` tercatat. Berlaku mulai evaluasi tool berikutnya.
- **Idempotensi**: Tidak berlaku. Setiap `SetPolicy` menambah event.
- **Konkurensi**: Lewat writer.
- **Paparan data**: Tidak ada.

## API-APPR-002: `Request` (audit, timeout, cancel, fail-closed)

- **Tujuan**: meminta jawaban approval dan mencatat audit-nya.
- **Requirements**: `REQ-APPR-002`, `REQ-APPR-004`, `REQ-APPR-005`
- **Acceptance criteria**: `AC-APPR-005`, `AC-APPR-006`, `AC-APPR-011` sampai `AC-APPR-014`
- **Stabilitas**: Baru. Sensitif keamanan
- **Otorisasi**: Dipanggil hanya oleh listener `tools.pre_execute` di API-PERM-003.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `req` | `core.ApprovalRequest` | Ya | `CallID` harus merujuk `tool/call` yang ada di session |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Penjawab menjawab dengan outcome valid | Tidak berlaku | Outcome dari penjawab. `approval/asked` dan `approval/decided` tersimpan |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| `approval/asked` gagal ditulis | Tidak berlaku | error biasa | Ya | Penjawab tidak dipanggil. Outcome `unavailable` |
| Batas 10 menit lewat | Tidak berlaku | `unavailable` (reason `timeout`) | Tidak | `approval/decided` tertulis |
| Context turn dibatalkan | Tidak berlaku | `cancelled` | Tidak | `approval/decided` tertulis |
| `approval/decided` gagal ditulis | Tidak berlaku | error biasa | Ya | Outcome dianggap `unavailable`. Tool tidak berjalan |

### Efek samping dan jaminan

- **Transisi state**: `approval/asked` lalu `approval/decided`. Tepat satu `approval/decided` per `request_id`.
- **Idempotensi**: Tidak berlaku. Setiap permintaan punya `request_id` ULID baru.
- **Konkurensi**: Penulisan `approval/decided` memakai context tanpa pembatalan dengan batas 5 detik agar audit tetap tersimpan saat turn dibatalkan.
- **Paparan data**: Tidak ada argumen tool di `approval/asked`. Log level `info` mencatat `call_id` dan outcome saja.

## API-APPR-003: Hook `approval.request` (penjawab)

- **Tujuan**: memberi kesempatan channel menjawab approval untuk session miliknya.
- **Requirements**: `REQ-APPR-003`
- **Acceptance criteria**: `AC-APPR-007` sampai `AC-APPR-010`
- **Stabilitas**: Baru
- **Otorisasi**: Penjawab hanya menjawab session miliknya. Penjawab yang tidak memiliki session memanggil `next`.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Hook | `core.ApprovalRequest` | struct (spec #2, `API-CORE-011`) | Ya | Tanpa argumen tool |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Penjawab memiliki session dan menjawab | Tidak berlaku | `core.ApprovalOutcome` dari set tertutup: `allowed_once`, `allowed_session`, `rejected`, `cancelled`, `unavailable` |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Tidak ada penjawab | Tidak berlaku | `unavailable` (default handler) | Tidak | Tool ditolak |
| Penjawab mengembalikan error | Tidak berlaku | `unavailable` | Tidak | Listener berikutnya tidak berjalan |
| Outcome di luar set tertutup | Tidak berlaku | `unavailable` | Tidak | Tool ditolak |

### Efek samping dan jaminan

- **Transisi state**: Tidak ada. Penulisan audit dimiliki API-APPR-002.
- **Idempotensi**: Tidak berlaku.
- **Konkurensi**: Waterfall sesuai spec #2 (`REQ-HOOK-001`).
- **Paparan data**: Penjawab boleh membaca `tool/call` lewat `call_id`. Store tidak mengirim argumen lewat hook.

## API-APPR-004: Fungsi batas izin subagent

- **Tujuan**: menghitung policy child yang tidak melebihi induk.
- **Requirements**: `REQ-APPR-006`
- **Acceptance criteria**: `AC-APPR-015`, `AC-APPR-016`
- **Stabilitas**: Baru. Dipakai tool `task` di spec subagent (M4).
- **Otorisasi**: Tidak berlaku. Fungsi murni.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `parent`, `child` | `core.Policy` | Ya | Nilai valid |
| Argumen | `parentDeny`, `childDeny` | `[]Pattern` | Ya | Hasil `ParsePattern` |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Hitung | Tidak berlaku | Policy paling ketat. Daftar deny gabungan |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Policy tidak valid | Tidak berlaku | `ErrInvalidPolicy` | Tidak | Tidak ada hasil |

### Efek samping dan jaminan

- **Transisi state**: Tidak ada.
- **Idempotensi**: Fungsi murni.
- **Konkurensi**: Tidak berlaku.
- **Paparan data**: Tidak ada.

## API-APPR-005: `lana.ApproveWith` (penjawab library)

- **Tujuan**: memberi jawaban approval untuk session channel `library` lewat fungsi Go.
- **Requirements**: `REQ-APPR-007`
- **Acceptance criteria**: `AC-APPR-017`
- **Stabilitas**: Baru. Sensitif keamanan
- **Otorisasi**: Dipasang lewat opsi `lana.New` (spec #3 `API-LIB-001`). Hanya menjawab session dengan channel `library`. Session channel lain diteruskan dengan `next`.

```go
package lana

// ApproveWith mendaftarkan penjawab untuk channel library di hook core.ApprovalRequest.
// Opsi ini dipasang lewat New(ctx, opts, ApproveWith(fn)).
func ApproveWith(fn func(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome) Option
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `fn` | func | Ya | Dipanggil untuk tiap permintaan approval di session channel `library`. `nil` berarti tidak ada penjawab, sehingga hasilnya `unavailable` (fail-closed) |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Session channel `library` | — | Outcome dari `fn` dipakai (`API-APPR-003`). Audit ditulis `API-APPR-002` |
| Session channel lain | — | `fn` tidak dipanggil. Listener memanggil `next` |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| `fn` panik | — | `unavailable` | Tidak | Panic ditangkap dengan `recover`. Tool ditolak |
| `fn` mengembalikan outcome di luar set tertutup | — | `unavailable` | Tidak | Tool ditolak (`API-APPR-003`) |

### Efek samping dan jaminan

- **Transisi state**: Tidak ada. Penulisan audit dimiliki `API-APPR-002`.
- **Idempotensi**: Tidak berlaku.
- **Konkurensi**: `fn` bisa dipanggil paralel untuk tool `Read`. `fn` harus aman dipakai banyak goroutine.
- **Paparan data**: `fn` boleh membaca `tool/call` lewat `call_id`. Store tidak mengirim argumen lewat hook.

### Kompatibilitas

- **Breaking change**: Tidak. API baru.
- **Deprecation**: Tidak berlaku.

## API-COMP-001: `CompactNow`

- **Tujuan**: meringkas riwayat session saat pemilik meminta (`/compact`).
- **Requirements**: `REQ-COMP-002`
- **Acceptance criteria**: `AC-COMP-004`, `AC-COMP-005`
- **Stabilitas**: Baru
- **Otorisasi**: Dipanggil dari channel pemilik session (spec #1) lewat `core.Compactor` (`API-CORE-015`). Spec #3 tidak punya `CompactNow` (`CQ-019` spec #3).

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `sessionID` | string | Ya | Session harus ada dan tanpa turn terbuka |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Summary tertulis | Tidak berlaku | `core.CompactionResult` dengan `UptoSeq`, `TokensBefore`, `TokensAfter`. Event `compaction/summary` tertulis |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Turn sedang berjalan | Tidak berlaku | `ErrTurnActive` | Ya, setelah turn selesai | Tidak ada event |
| Session tanpa event | Tidak berlaku | `ErrNothingToCompact` | Tidak | Tidak ada event |
| Ringkasan gagal dari provider | Tidak berlaku | error provider | Ya | Tidak ada event |
| `last_seq` berubah sejak dibaca | Tidak berlaku | `ErrSessionChanged` | Ya | Tidak ada event |

### Efek samping dan jaminan

- **Transisi state**: Event `compaction/summary` tertulis. Event lama tidak dihapus.
- **Idempotensi**: Tidak berlaku. Setiap panggilan membuat summary baru.
- **Konkurensi**: Optimistic: `last_seq` diverifikasi terhadap nilai saat ringkasan dibuat, di transaksi append yang sama. Ini menjaga invarian `REQ-STORE-005`.
- **Paparan data**: Isi ringkasan tersimpan di log. Tidak dicatat ke log level `info`.

## API-COMP-002: Compaction otomatis dan retry

- **Tujuan**: menjaga konteks model di bawah batas dan mengulang sekali saat provider menolak karena terlalu panjang.
- **Requirements**: `REQ-COMP-001`, `REQ-COMP-003`
- **Acceptance criteria**: `AC-COMP-001` sampai `AC-COMP-003`, `AC-COMP-006`, `AC-COMP-007`
- **Stabilitas**: Baru
- **Otorisasi**: Listener hook dari modul `compaction`.

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Config | `agents.<nama>.context_window` | integer | Tidak | Default 128000 |
| Hook | `agent.pre_step` | input spec #3 | Ya | Session dan turn aktif |
| Hook | `llm.stream` error | `core.ErrContextTooLong` | Ya (retry) | Error dari provider |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Token konteks lebih dari 70% | Tidak berlaku | `compaction/summary` tertulis sebelum `step/start` |
| Retry berhasil setelah `ErrContextTooLong` | Tidak berlaku | Request kedua mengembalikan hasil. Turn lanjut |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Ringkasan otomatis gagal | Tidak berlaku | Log level `warn` | Ya, step berikutnya | Turn lanjut tanpa compaction |
| Retry juga `ErrContextTooLong` | Tidak berlaku | `core.ErrContextTooLong` | Tidak | Turn berakhir dengan `turn/end` reason `error` |

### Efek samping dan jaminan

- **Transisi state**: Event `compaction/summary` tertulis di dalam turn. `upto_seq` = seq tepat sebelum `step/start` step aktif.
- **Idempotensi**: Tidak berlaku. Retry hanya sekali per step.
- **Konkurensi**: Berjalan di goroutine turn. Ringkasan memakai adapter provider langsung, tidak lewat hook `llm.stream` (`Q-009`).
- **Paparan data**: Sama seperti API-COMP-001.

## Traceability contract

| Operasi | Requirements | Acceptance criteria | Komponen design | Verifikasi |
|---|---|---|---|---|
| `API-STORE-001` | `REQ-STORE-001`, `REQ-STORE-002`, `REQ-STORE-004`, `REQ-STORE-016` | `AC-STORE-001` sampai `AC-STORE-004`, `AC-STORE-007`, `AC-STORE-008`, `AC-STORE-038` | `store.Open`, migrator | Test `store` di `t.TempDir()` |
| `API-STORE-002` | `REQ-STORE-006` | `AC-STORE-012` | `store.writer`, projection | Test `store` |
| `API-STORE-003` | `REQ-STORE-006` | `AC-STORE-013` sampai `AC-STORE-016` | `repair`, `session_leases` | Test `store` dan simulasi crash |
| `API-STORE-004` | `REQ-STORE-003`, `REQ-STORE-005`, `REQ-STORE-015` | `AC-STORE-005`, `AC-STORE-006`, `AC-STORE-009` sampai `AC-STORE-011`, `AC-STORE-036`, `AC-STORE-037` | `store.writer`, hook observe | Test concurrency, test dua instance |
| `API-STORE-005` | `REQ-STORE-008` | `AC-STORE-019` | read pool | Test `store` |
| `API-STORE-006` | `REQ-STORE-008` | `AC-STORE-020` | read pool, index | Test `store` |
| `API-STORE-007` | `REQ-STORE-009` | `AC-STORE-021` sampai `AC-STORE-023` | read pool | Test `store` |
| `API-STORE-008` | `REQ-STORE-007` | `AC-STORE-017`, `AC-STORE-018` | `store.writer` | Test `store` |
| `API-STORE-009` | `REQ-STORE-010` | `AC-STORE-024` sampai `AC-STORE-027` | lease | Test `store` dengan PID palsu |
| `API-STORE-010` | `REQ-STORE-011` | `AC-STORE-028` sampai `AC-STORE-030` | derive, chain | Golden test |
| `API-STORE-011` | `REQ-STORE-014` | `AC-STORE-034`, `AC-STORE-035` | prune | Test `store` |
| `API-STORE-012` | `REQ-STORE-012` | `AC-STORE-031` | projection | Test `store` |
| `API-STORE-013` | `REQ-STORE-013` | `AC-STORE-032`, `AC-STORE-033` | payload | Test fixture versi |
| `API-PERM-001` | `REQ-PERM-001` | `AC-PERM-001` sampai `AC-PERM-003` | pattern | Test unit dan property test |
| `API-PERM-002` | `REQ-PERM-001` | `AC-PERM-004` | config validation | Test config |
| `API-PERM-003` | `REQ-PERM-002` sampai `REQ-PERM-006` | `AC-PERM-005` sampai `AC-PERM-019` | evaluator, workspace, grant | Test unit dan integrasi |
| `API-APPR-001` | `REQ-APPR-001` | `AC-APPR-001` sampai `AC-APPR-004` | policy | Test `store` dan approval |
| `API-APPR-002` | `REQ-APPR-002`, `REQ-APPR-004`, `REQ-APPR-005` | `AC-APPR-005`, `AC-APPR-006`, `AC-APPR-011` sampai `AC-APPR-014` | approval service | Test dengan jam palsu |
| `API-APPR-003` | `REQ-APPR-003` | `AC-APPR-007` sampai `AC-APPR-010` | waterfall | Test hook |
| `API-APPR-004` | `REQ-APPR-006` | `AC-APPR-015`, `AC-APPR-016` | fungsi batas | Test unit dan integrasi |
| `API-APPR-005` | `REQ-APPR-007` | `AC-APPR-017` | penjawab library (`lana`) | Test integrasi dengan `lana.New` dan MockLlm |
| `API-COMP-001` | `REQ-COMP-002` | `AC-COMP-004`, `AC-COMP-005` | compaction service | Test dengan `llm` palsu |
| `API-COMP-002` | `REQ-COMP-001`, `REQ-COMP-003` | `AC-COMP-001` sampai `AC-COMP-003`, `AC-COMP-006`, `AC-COMP-007` | compaction listener | Test dengan `llm` palsu |

## Blocking Contract Questions

| ID | Pertanyaan | Mengendalikan | Referensi yang diminta | Pemilik | Status | Resolusi |
|---|---|---|---|---|---|---|
| `CQ-001` | Bentuk `Lease`: fungsi `Release` atau token terpisah | `API-STORE-009` | Tidak ada | Ridho | Resolved | Rekomendasi agent: `core.Lease` dengan method `SessionID()` dan `Release(ctx)`. Paling sederhana dan cocok dengan spec #1 yang melepas lease saat pindah session. Direvisi: lihat `CQ-012`. |
| `CQ-002` | Tanda tangan `Open` dan `Create` | `API-STORE-002`, `API-STORE-003` | Tidak ada | Ridho | Resolved | Rekomendasi agent: `Open` mengembalikan `SessionInfo`. `Create` mengembalikan ID saja. Tidak ada handle session di memori. Direvisi: lihat `CQ-012`. |
| `CQ-003` | Output hook `tools.pre_execute` | `API-PERM-003` | Konfirmasi spec #3 | Ridho | Resolved | Rekomendasi agent: `core.ToolDecision`. Listener tidak memanggil `next`, sehingga default fail-closed tidak berjalan saat ada keputusan. |
| `CQ-004` | Field `core.ApprovalRequest` | `API-APPR-002`, `API-APPR-003` | Tidak ada | Ridho | Resolved | Rekomendasi agent: `SessionID`, `TurnID`, `CallID`, `Tool`, `Label`, `Risk`. Tanpa argumen (ADR 0004). Direvisi: lihat `CQ-011` (`Tool` menjadi `ToolName`). |
| `CQ-005` | Tanda tangan `Compactor` dan hasilnya | `API-COMP-001` | Tidak ada | Ridho | Resolved | Rekomendasi agent: `CompactNow(ctx, sessionID)` mengembalikan `core.CompactionResult`. Spec #1 memakai `core.Compactor.CompactNow(ctx, session)`. Usulan delegasi dari spec #3 dicabut (lihat `CQ-014`). |
| `CQ-006` | Kelas error konteks terlalu panjang | `API-COMP-002` | Konfirmasi spec #3 | Ridho | Resolved | Rekomendasi agent: sentinel `core.ErrContextTooLong` yang dibungkus adapter provider dengan `errors.Is`. |
| `CQ-007` | Field payload `approval/*` dan `compaction/summary` | Event contract | Konfirmasi spec #2 | Ridho | Resolved | Rekomendasi agent: field di tabel event contract. Jika struct belum ada di `core`, struct ditambahkan sebagai tambahan core dimiliki spec #4. Direvisi: field dimiliki spec #2 (`CQ-016`). |
| `CQ-008` | Ukuran dan arti kolom projection `sessions` | `API-STORE-001` | Tidak ada | Ridho | Resolved | Rekomendasi agent: tambahan `workspace` dan `turn_count` dibanding ADR 0009. Keduanya dibutuhkan spec #1 (`/sessions`, `session/created` workspace). |
| `CQ-009` | Default `context_window` | `API-PERM-002`, `API-COMP-002` | Tidak ada | Ridho | Resolved | Rekomendasi agent: 128000, per agent. Lihat `Q-006` di requirements. |
| `CQ-010` | Profile yang memuat modul `permission` dan `approval` | Config modul | Konfirmasi spec #2 dan ARCHITECTURE §3 | Ridho | Resolved | Rekomendasi agent: base profile dan `chat` (`minimal` plus channel cli) wajib memuat `permission`, `approval`, dan `compaction`. Profile `minimal` di ARCHITECTURE §3 perlu direvisi. Tanpa ini, `chat` jatuh ke fail-closed. |
| `CQ-011` | Tipe `core` yang dipakai spec #4 | Bagian Signature Go | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): tipe `NewSession`, `SessionInfo`, `ApprovalRequest`, `ToolDecision`, `CompactionResult`, `Compactor`, `ErrContextTooLong` dipindah ke `API-CORE-015`, `API-CORE-009`, dan `API-CORE-011` (spec #2). Perubahan: `NewEvent` menjadi `EventDraft`. `Lease` dihapus. `ApprovalRequest.Tool` menjadi `ToolName`, `Input` dan `Summary` dihapus, `Label` ditambah. Field `NewSession.Workspace` dan `ParentCallID` tetap. |
| `CQ-012` | Signature `SessionStore` dan `TryLease` | `API-STORE-002`, `API-STORE-003`, `API-STORE-005`, `API-STORE-006`, `API-STORE-007`, `API-STORE-008`, `API-STORE-009` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): signature spec #2 menang (`CQ-022` spec #2). `Create` dan `Open` mengembalikan `SessionInfo` (merevisi `CQ-002`). `TryLease` mengembalikan `release func() error` (merevisi `CQ-001`). `Events` mengembalikan iterator. `List` memakai `SessionFilter`. `FindPrefix` mengembalikan daftar. `Fork` tanpa argumen `in`. |
| `CQ-013` | Riwayat model dan `system/message` | `API-STORE-010`, Tabel event | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `DeriveMessages` (store) membungkus `core.DeriveMessages` (`API-CORE-015`, `CQ-028` spec #2). `system/message` tidak masuk riwayat. Pesan terakhirnya dipakai sebagai `ChatRequest.System` (`Q-028`). Compaction tetap sebagai pesan `user` (`Q-013`). |
| `CQ-014` | `CompactNow` dan `/compact` | `API-COMP-001` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `core.Compactor.CompactNow` dari spec ini dipakai `/compact` di spec #1. Spec #3 tidak punya `CompactNow` (`CQ-019` spec #3). Usulan delegasi dari spec #3 dicabut. |
| `CQ-015` | Konteks terlalu panjang dan retry | `API-COMP-002`, `API-PROV-003` spec #3 | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): adapter membuat `*core.ProviderError` class `context_length` yang `errors.Is(err, core.ErrContextTooLong)` (`CQ-026` spec #2). Listener `llm.stream` compaction mengulang sekali. Tanpa modul ini, turn berakhir `error` (`Q-005` spec #3). |
| `CQ-016` | Pemilik field payload dan `workspace`, `agent` | `API-STORE-002`, Tabel event | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): tabel field di `API-CORE-008` (spec #2, `CQ-029`). `session/created.workspace` dan `model/selection.agent` disetujui. |
| `CQ-017` | Lokasi grammar pola `rules` | `API-PERM-001`, `API-PERM-002` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): paket `internal/pattern` (`DQ-015` spec #2). Dipakai `config` untuk validasi `rules` dan `tools/permission` untuk evaluasi. Grammar tidak berubah. |
| `CQ-018` | Isi profile base | `API-PERM-003`, `API-APPR-001`, `API-COMP-002` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `sqlite_store`, `permission`, `approval`, dan `compaction` ada di base (`API-CONF-009` spec #2, `CQ-031`). `minimal` sama dengan base. Merevisi `CQ-010`: `chat` dan `run` memuatnya lewat base. |
| `CQ-019` | Penjawab library `lana.ApproveWith` | `API-APPR-005`, `REQ-APPR-007` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `lana.ApproveWith(fn)` adalah `Option` (spec #3 `API-LIB-001`). Penjawab khusus channel `library`. Ditambah sebagai `REQ-APPR-007`, `AC-APPR-017`, `API-APPR-005`, dan `T-021`. |
| `CQ-020` | Input hook `tools.pre_execute` | `API-PERM-003` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): input `core.ToolsPreExecuteInput` (spec #2, `CQ-017`), output `core.ToolDecision`. `Label` dihitung dari `AgentName` jika session punya induk. |
| `CQ-021` | Sentinel yang pindah ke `core` | `API-STORE-007`, `API-STORE-009` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `ErrPrefixTooShort` dan `ErrAmbiguousPrefix` pindah ke `core` (`core.ResolvePrefix`). `ErrLeaseHeld` menjadi `*core.LeaseHeldError` dengan `errors.As`. `ErrUnknownPayloadVersion` ada di `core`. Perilaku tidak berubah. |
| `CQ-022` | Batas `limit` daftar session | `API-STORE-006` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `SessionFilter.Limit` 0 = tanpa batas (spec #2). Batas 1 sampai 200 di revisi 1 dihapus dari kontrak. Requirements tidak menyebut batas itu, sehingga tidak ada perubahan requirements. |

None.

## Approval

- [x] Requirements sudah Approved dan revisinya dicatat di atas.
- [x] Keberlakuan contract sudah ditentukan.
- [x] Setiap operasi punya ID stabil dan tertaut ke requirement dan acceptance criteria.
- [x] Input, sukses, gagal, otorisasi, dan kompatibilitas eksplisit.
- [x] Tidak ada perilaku yang hanya ada di file ini.
- [x] Blocking Contract Questions bernilai `None`.
- **Di-approve oleh**: Ridho (gate dilepas 2026-10-29)
- **Di-approve pada**: 2026-10-29 (revisi 1). Revisi 2 rekonsiliasi oleh agent atas nama pemilik (gate dilepas 2026-10-29).
