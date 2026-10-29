# Contract: Agent loop, provider, tools, Go library

- **Issue**: [`#3`](https://github.com/ridhomujizat/lana-harness/issues/3)
- **Requirements**: [requirements.md](requirements.md)
- **Revisi requirements**: 2
- **Status requirements**: Approved
- **Revisi contract**: 2 (rekonsiliasi lintas spec, lihat `CQ-011` sampai `CQ-019`)
- **Berlaku untuk**: Go API (package `lana` dan `agent`), key config `[providers.<name>]` dan `[agents.<nama>]`, skema input tool, payload event session log, format skrip MockLlm, tipe event `rt.Stream`
- **Status**: Approved

File ini menetapkan bentuk persis antarmuka. Perilaku dimiliki oleh `requirements.md`.
Package `core` (interface, hook, tipe pesan, dan tipe gabungan) dimiliki spec #2 (`API-CORE-015`).
Spec ini tidak mendefinisikan tipe `core`. Tipe yang dipakai spec ini dirujuk dengan namanya.

## Konvensi bersama

| Hal | Keputusan |
|---|---|
| Exit code | Tidak berlaku di package ini. CLI (spec #1) memetakan error provider ke exit `3` (`REQ-EXIT-001` spec #1) |
| Output stdout dan stderr | Tidak berlaku. Package ini tidak menulis ke stdout atau stderr |
| Log | `log/slog`. Level `info` mencatat id session, id turn, jumlah step, provider, model, reason, dan durasi. Isi pesan, teks model, dan output tool tidak dicatat di level `info` |
| Format config | TOML. Tabel `[providers.<name>]`, `[agents.<nama>]`, dan `[[routes]]` dibaca lewat loader spec #2 |
| Secret | Dibaca lewat `api_key_env` dari env proses, lalu `secrets.env` (spec #2). Tidak pernah dicetak. Header `Authorization: Bearer <nilai>` hanya dikirim ke `base_url` provider |
| Versi | Setiap payload event memakai field `v` bernilai `1`. Perubahan Go API sebelum `v1.0.0` dicatat di CHANGELOG (ADR 0014) |
| Serialisasi | JSON UTF-8, field `snake_case`. ID session dan turn berupa ULID 26 karakter (konvensi spec #1). Waktu event diisi `core.SessionEvent.CreatedAt` oleh store |
| Error Go | Sentinel error dengan `errors.Is`. Error provider berupa `*core.ProviderError` (alias `lana.ProviderError`) dengan field `Class` (`API-CORE-015`) |
| Stop reason turn | `end_turn`, `max_steps`, `cancelled`, `error`. Nilai `interrupted` disediakan spec #4 (ADR 0009) |
| Status tool | `success`, `error`, `rejected` (sama dengan spec #1 `API-CLI-007`) |
| Format model | `<provider>:<model>`, misalnya `openai:gpt-5-mini` |

## Index operasi

| ID operasi | Jenis | Nama | Requirements | Acceptance criteria |
|---|---|---|---|---|
| `API-LIB-001` | Go | `lana.New`, `lana.Options` | `REQ-LIB-001`, `REQ-LIB-006`, `REQ-LIB-008` | `AC-LIB-001`, `AC-LIB-002`, `AC-LIB-003`, `AC-LIB-013`, `AC-LIB-014` |
| `API-LIB-002` | Go | `(*Runtime).Run` | `REQ-LIB-002`, `REQ-LIB-007` | `AC-LIB-004` sampai `AC-LIB-008`, `AC-LIB-015`, `AC-LIB-016` |
| `API-LIB-003` | Go | `(*Runtime).Stream` | `REQ-LIB-003` | `AC-LIB-009`, `AC-LIB-010` |
| `API-LIB-004` | Go | `(*Runtime).Close` | `REQ-LIB-004` | `AC-LIB-011` |
| `API-LIB-005` | Config | Baris modul profile `minimal` dan `headless` | `REQ-LIB-008` | `AC-LIB-017` |
| `API-AGENT-001` | Go | `(*Registry).Send` | `REQ-LOOP-001`, `REQ-LOOP-002` | `AC-LOOP-001` sampai `AC-LOOP-006` |
| `API-AGENT-002` | Go | `(*Registry).Start` | `REQ-LOOP-002`, `REQ-LIB-002` | `AC-LIB-005` |
| `API-AGENT-003` | Go | `(*Registry).Cancel` | `REQ-LOOP-007` | `AC-LOOP-019` sampai `AC-LOOP-021` |
| `API-AGENT-004` | Go | `(*Registry).SetAgent` | `REQ-LOOP-010` | `AC-LOOP-028` |
| `API-AGENT-005` | Go | `(*Registry).SetModel` | `REQ-LOOP-010` | `AC-LOOP-029`, `AC-LOOP-030` |
| `API-AGENT-006` | Go | `(*Registry).SubscribeLive` | `REQ-LOOP-009` | `AC-LOOP-026`, `AC-LOOP-027` |
| `API-AGENT-007` | Config | `[agents.<nama>]` (key tambahan) | `REQ-LOOP-004`, `REQ-LOOP-005`, `REQ-LOOP-008`, `REQ-LOOP-010` | `AC-LOOP-011`, `AC-LOOP-015`, `AC-LOOP-016`, `AC-LOOP-023`, `AC-LOOP-031` |
| `API-AGENT-008` | Event log | Payload event yang ditulis spec ini | `REQ-LOOP-001`, `REQ-LOOP-003`, `REQ-LOOP-004`, `REQ-LOOP-008`, `REQ-LOOP-010` | `AC-LOOP-001`, `AC-LOOP-009`, `AC-LOOP-010`, `AC-LOOP-022`, `AC-LOOP-024`, `AC-LOOP-028`, `AC-LIB-016` |
| `API-AGENT-009` | Hook | Pipeline hook dan handler default | `REQ-TOOL-009`, `REQ-LOOP-003`, `REQ-LOOP-006`, `REQ-LIB-005` | `AC-TOOL-023`, `AC-TOOL-024`, `AC-LOOP-007`, `AC-LOOP-018`, `AC-LIB-012` |
| `API-PROV-001` | Config | `[providers.<name>]` `kind = "openai_compat"` | `REQ-PROV-001` | `AC-PROV-001` sampai `AC-PROV-004` |
| `API-PROV-002` | Wire | Pemetaan request dan stream OpenAI | `REQ-PROV-002`, `REQ-PROV-003` | `AC-PROV-005` sampai `AC-PROV-009` |
| `API-PROV-003` | Kelas error | Kelas error provider | `REQ-PROV-004`, `REQ-LOOP-008` | `AC-PROV-010` sampai `AC-PROV-012`, `AC-LOOP-022` sampai `AC-LOOP-025` |
| `API-PROV-004` | Test | Format skrip `MockLlm` | `REQ-MOCK-001` | `AC-MOCK-001`, `AC-MOCK-002` |
| `API-TOOL-001` | Tool | `read_file` | `REQ-TOOL-001` | `AC-TOOL-001`, `AC-TOOL-002` |
| `API-TOOL-002` | Tool | `write_file` | `REQ-TOOL-002` | `AC-TOOL-003` sampai `AC-TOOL-005` |
| `API-TOOL-003` | Tool | `edit_file` | `REQ-TOOL-003` | `AC-TOOL-006` sampai `AC-TOOL-008` |
| `API-TOOL-004` | Tool | `glob` | `REQ-TOOL-004` | `AC-TOOL-009`, `AC-TOOL-010` |
| `API-TOOL-005` | Tool | `grep` | `REQ-TOOL-005` | `AC-TOOL-011` sampai `AC-TOOL-013` |
| `API-TOOL-006` | Tool | `bash` | `REQ-TOOL-006` | `AC-TOOL-014` sampai `AC-TOOL-017` |
| `API-TOOL-007` | Tool | `web_fetch` | `REQ-TOOL-007` | `AC-TOOL-018` sampai `AC-TOOL-020` |

## Tipe core yang dipakai (dimiliki spec #2)

Semua tipe hook dan tipe pendukung ada di `API-CORE-015` (spec #2). Revisi 2 memindahkan tipe
"tambahan" yang sebelumnya ada di sini ke sana (`CQ-011`). Nama yang berubah:

| Nama lama (revisi 1) | Nama sekarang | Catatan |
|---|---|---|
| `StepInput` | `PreStepInput` | Ditambah `Reject` lewat `PreStepDecision` |
| `AgentTurnStopInput` | `TurnStopInput` | Field `AgentName`, `Steps`, `StopReason` |
| `TurnStopVote` | `StopVote` | Field sama |
| `ToolDecision.Allow` | `ToolDecision.Allowed` | Ditambah `Outcome` dan `Status` (`CQ-020` spec #2) |
| `ToolOutput.Status` | sama, tipe `ToolStatus` | Konstanta `ToolStatusSuccess`, `ToolStatusError`, `ToolStatusRejected` |
| `ChatEvent.Kind` `tool_use` | `ChatEvent` dengan `Kind = ChatBlock` dan `Block.Kind = tool_use` | Bentuk spec #2 (`CQ-019`) |
| `ToolCall.CallID`, `Name`, `Input` | `ToolCall` spec #2 | Ditambah `SessionID`, `TurnID`, `Risk` |
| `Agent` | `AgentName` | Di semua struct core |

Tipe hook yang dipakai spec ini:

| Hook | Mode | Input | Output | Handler default |
|---|---|---|---|---|
| `AgentPreStep` | waterfall | `PreStepInput` | `PreStepDecision` | Terima input apa adanya |
| `AgentRequest` | waterfall | `RequestControl` | `RequestControl` | Terima apa adanya |
| `LLMStream` | waterfall | `LLMStreamInput` | `ChatStream` | Panggil `Provider.Stream` untuk `Targets[0]`, isi `Request.Model` |
| `ToolsPreExecute` | waterfall | `ToolsPreExecuteInput` | `ToolDecision` | `Risk = Read` → allow. Selain itu deny `default fail-closed` |
| `ToolsExecute` | waterfall | `ToolsExecuteInput` | `ToolOutput` | Panggil `Tool.Call`. Error menjadi `ToolOutput{error, pesan}` |
| `ToolsPostExecute` | waterfall | `ToolsPostExecuteInput` | `ToolOutput` | Terima apa adanya |
| `AgentTurnStopping` | serial | `TurnStopInput` | `StopVote` | Tanpa listener, turn berhenti |
| `SessionEvent` | observe | `core.SessionEvent` | — | Dipanggil oleh store setelah `Append` |

## API-LIB-001: `lana.New`

- **Tujuan**: membangun runtime library dari opsi.
- **Requirements**: `REQ-LIB-001`, `REQ-LIB-006`, `REQ-LIB-008`
- **Acceptance criteria**: `AC-LIB-001`, `AC-LIB-002`, `AC-LIB-003`, `AC-LIB-013`, `AC-LIB-014`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku. Pemanggil adalah proses yang meng-import package

```go
package lana

type ApprovalMode int

const (
    PolicyNever ApprovalMode = iota // default (nilai nol)
    PolicyTrust                     // listener allow-all di tools.pre_execute
)

type Options struct {
    Home     string            // "" = tidak membaca file config dari disk
    Layers   []config.Layer    // lapisan config di memori (spec #2 REQ-CONF-009)
    Profile  string            // "" = "minimal"
    Tools    []core.Tool       // tool milik service
    Approval ApprovalMode
    Store    core.SessionStore // nil dan Home kosong = memstore. nil dan Home terisi = store dari profile (sqlite_store). Diisi = provided_store (DQ-016 spec #2)
}

// Option adalah opsi tambahan New.
type Option func(*runtimeOptions)

// ApproveFunc menjawab approval untuk session channel library (API-APPR-005 spec #4).
type ApproveFunc = func(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome

// ApproveWith memasang penjawab approval untuk channel library. Lihat API-APPR-005 (spec #4).
func ApproveWith(fn ApproveFunc) Option

func New(ctx context.Context, opts Options, options ...Option) (*Runtime, error)
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Options | `Home` | string | Tidak | Lokasi `LANA_HOME`. Kosong = tidak ada file yang dibaca |
| Options | `Layers` | `[]config.Layer` | Tidak | Lapisan di memori, diterapkan setelah profile |
| Options | `Profile` | string | Tidak | Salah satu profile bawaan (`API-LIB-005`). Kosong = `minimal` |
| Options | `Tools` | `[]core.Tool` | Tidak | Nama harus unik dan tidak boleh sama dengan tool bawaan |
| Options | `Approval` | `ApprovalMode` | Tidak | `PolicyNever` (nilai nol) atau `PolicyTrust` |
| Options | `Store` | `core.SessionStore` | Tidak | Nil dan `Home` kosong: `memstore`. Nil dan `Home` terisi: store dari profile. Diisi: `provided_store` |
| Option | `ApproveWith(fn)` | `ApproveFunc` | Tidak | Penjawab approval untuk channel `library`. Lihat `API-APPR-005` spec #4 (`CQ-016`) |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---|---|
| Boot selesai dan semua modul terpasang | — | `*Runtime` siap dipakai, error nil |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---|---|---:|---|
| Tool kustom bernama sama dengan tool bawaan | — | error boot `tool_name_conflict` | Tidak | Error menyebut nama tool (`AC-LIB-002`) |
| Agent memakai tool yang tidak terdaftar | — | error boot `unknown_tool` | Tidak | Error menyebut agent dan tool (`AC-LIB-003`) |
| Provider `kind` bukan `openai_compat` | — | error boot `unsupported_kind` | Tidak | Error menyebut provider (`AC-PROV-003`) |
| Env `api_key_env` kosong untuk provider yang dipakai agent | — | error class `missing_secret` | Tidak | Error menyebut env dan provider (`AC-PROV-004`) |
| Profile tidak dikenal atau config tidak valid | — | error dari spec #2 | Tidak | Diteruskan dari loader |

### Efek samping dan jaminan

- **Transisi state**: runtime berpindah dari belum-boot ke siap. Tidak ada session yang dibuat.
- **Idempotensi**: tidak berlaku. Setiap pemanggilan membuat runtime baru.
- **Konkurensi**: aman dipakai dari banyak goroutine setelah `New` kembali.
- **Paparan data**: API key tidak masuk error atau log.

### Kompatibilitas

- **Breaking change**: Tidak. API baru.
- **Deprecation**: Tidak berlaku.

## API-LIB-002: `(*Runtime).Run`

- **Tujuan**: menjalankan satu turn dan mengembalikan hasil final.
- **Requirements**: `REQ-LIB-002`, `REQ-LIB-007`
- **Acceptance criteria**: `AC-LIB-004` sampai `AC-LIB-008`, `AC-LIB-015`, `AC-LIB-016`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku

```go
type Request struct {
    Agent     string // kosong = route channel "library" pertama
    Prompt    string // wajib, tidak kosong
    SessionID string // kosong = session baru; isi = buka session itu
}

type Usage struct {
    InputTokens  int
    OutputTokens int
}

type Result struct {
    Text       string
    SessionID  string
    StopReason string // StopEndTurn, StopMaxSteps, StopCancelled, StopError
    Usage      Usage  // jumlah step/end usage satu turn
}

const (
    StopEndTurn   = "end_turn"
    StopMaxSteps  = "max_steps"
    StopCancelled = "cancelled"
    StopError     = "error"
)

func (rt *Runtime) Run(ctx context.Context, req Request) (Result, error)
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `ctx` | `context.Context` | Ya | Pembatalan ctx membatalkan turn (`StopCancelled`) |
| Request | `Agent` | string | Tidak | Harus terdaftar. Kosong = route `library` pertama |
| Request | `Prompt` | string | Ya | Tidak boleh kosong atau spasi saja |
| Request | `SessionID` | string | Tidak | ULID atau prefix unik minimal 6 karakter. Session harus ada |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Turn selesai `end_turn` | — | `Result` lengkap, error nil |
| Turn selesai `max_steps` | — | `StopReason` `max_steps`, error nil |
| Turn dibatalkan | — | `StopReason` `cancelled`, error nil |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| `Prompt` kosong | — | `ErrEmptyPrompt` | Ya | Tidak ada session dibuat (`AC-LIB-008`) |
| Tidak ada route `library` dan `Agent` kosong | — | `ErrNoRoute` | Tidak | (`AC-LIB-015`) |
| Agent tidak terdaftar | — | `ErrUnknownAgent` | Tidak | Menyebut nama agent |
| Session sedang berjalan | — | `ErrSessionBusy` | Ya | Pesan tidak masuk inbox (`AC-LIB-005`) |
| Session tidak ditemukan | — | `ErrSessionNotFound` | Tidak | Menyebut ID |
| Provider gagal setelah retry dan fallback | — | `*ProviderError{Class}` | Tergantung class | `StopReason` `error` (`AC-LIB-007`) |
| Runtime sudah ditutup | — | `ErrClosed` | Tidak | (`AC-LIB-011`) |

Tipe error provider:

Tipe `*core.ProviderError` (`API-CORE-015`), alias `lana.ProviderError`:

```go
type ProviderError struct {
    Class   string // auth | rate_limit | server | network | context_length | client | missing_secret
    Status  int
    Message string
}
// errors.Is(err, core.ErrContextTooLong) benar untuk Class context_length.
```

### Efek samping dan jaminan

- **Transisi state**: membuat session baru jika `SessionID` kosong. Menulis event turn dan step.
- **Idempotensi**: tidak berlaku. Setiap pemanggilan menambah satu turn.
- **Konkurensi**: `Run` dan `Stream` untuk session yang sama tidak boleh berjalan bersamaan. Pemanggil kedua menerima `ErrSessionBusy`.
- **Paparan data**: isi jawaban ada di `Result.Text`. Tidak ada secret.

### Kompatibilitas

- **Breaking change**: Tidak. API baru.
- **Deprecation**: Tidak berlaku.

## API-LIB-003: `(*Runtime).Stream`

- **Tujuan**: mengirim event turn sebagai iterator agar service bisa menampilkan progres.
- **Requirements**: `REQ-LIB-003`
- **Acceptance criteria**: `AC-LIB-009`, `AC-LIB-010`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku

```go
func (rt *Runtime) Stream(ctx context.Context, req Request) iter.Seq2[Event, error]

type Event interface{ isEvent() }

type Session struct {
    SessionID string
    Agent     string
    Model     string // "provider:model"
}
type TextDelta struct{ Text string }
type ToolCall struct {
    CallID string
    Name   string
    Input  json.RawMessage
}
type ToolResult struct {
    CallID string
    Status string // success | error | rejected
    Output string
}
type Done struct{ Result Result }
```

### Input

Sama dengan `API-LIB-002`.

### Sukses

| Kondisi | Urutan event | Output |
|---|---|---|
| Turn selesai | `Session`, lalu `TextDelta`, `ToolCall`, `ToolResult` sesuai kejadian, lalu `Done` | `Done.Result` berisi hasil seperti `Run` |

### Gagal

| Kondisi | Event terakhir | Kode stabil |
|---|---|---|
| Error pra-turn (`ErrEmptyPrompt`, `ErrNoRoute`, `ErrUnknownAgent`, `ErrSessionBusy`, `ErrSessionNotFound`) | `(nil, err)` | Sama dengan `API-LIB-002` |
| Provider gagal | `Done` dengan `StopReason` `error`, lalu `(nil, *ProviderError)` | `ProviderError.Class` |
| `ctx` dibatalkan | `Done` dengan `StopReason` `cancelled` | — |

### Efek samping dan jaminan

- **Transisi state**: sama dengan `Run`.
- **Idempotensi**: tidak berlaku.
- **Konkurensi**: sama dengan `Run`. Frame hidup dikirim tanpa menunggu iterator dibaca; event yang tidak dibaca dibuang dengan peringatan log (`REQ-LOOP-009`).
- **Paparan data**: sama dengan `Run`.

### Kompatibilitas

- **Breaking change**: Tidak. API baru.
- **Deprecation**: Tidak berlaku.

## API-LIB-004: `(*Runtime).Close`

- **Tujuan**: menghentikan semua turn dan melepas modul.
- **Requirements**: `REQ-LIB-004`
- **Acceptance criteria**: `AC-LIB-011`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku

```go
func (rt *Runtime) Close() error
```

### Input

Tidak ada.

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Semua turn berhenti dan modul dihentikan dalam 30 detik | — | error nil |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Turn melewati 30 detik | — | error `shutdown_timeout` (berisi nama modul) | Tidak | Modul yang lewat dicatat dan dilewati (spec #2 `REQ-MOD-003`) |
| Dipanggil dua kali | — | — | Ya | Pemanggilan kedua tidak melakukan apa-apa dan error nil |

### Efek samping dan jaminan

- **Transisi state**: runtime berpindah ke `closed`. `Run`, `Stream`, `Send`, `Start` setelahnya gagal dengan `ErrClosed`.
- **Idempotensi**: `Close` idempotent.
- **Konkurensi**: aman dipanggil bersamaan dengan turn berjalan.
- **Paparan data**: tidak berlaku.

### Kompatibilitas

- **Breaking change**: Tidak. API baru.
- **Deprecation**: Tidak berlaku.

## API-LIB-005: Baris modul profile bawaan

- **Tujuan**: menetapkan baris modul yang dipakai `lana.New` untuk profile bawaan.
- **Requirements**: `REQ-LIB-008`
- **Acceptance criteria**: `AC-LIB-017`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku

Baris profile bawaan dimiliki spec #2 (`API-CONF-009`, `CQ-031`). Spec ini memakai baris `llm`, `tools`, `store`, dan `agent` dari base. `minimal` sama dengan base. `headless` sama dengan base ditambah `cli_runner` (spec #1). Dua modul milik spec ini:

| `module` | Id | Kapan |
|---|---|---|
| `memstore` | `store` | `Home` kosong dan `Options.Store` nil (library) |
| `provided_store` | `store` | `Options.Store` diisi. Memasang instance itu lewat `Provide` (DQ-016 spec #2) |

### Input

Tidak ada. Baris ini adalah data profile bawaan (spec #2 `REQ-PROF-001`).

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Profile dimuat | — | Baris base sesuai `API-CONF-009`, dengan asal `profile:minimal` atau `profile:headless` |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Baris `store` diganti patch dengan `module` yang tidak dikenal | — | error spec #2 `REQ-CONF-003` | Tidak | Menyebut `module` |

### Efek samping dan jaminan

- **Transisi state**: tidak ada.
- **Idempotensi**: tidak berlaku.
- **Konkurensi**: tidak berlaku.
- **Paparan data**: tidak berlaku.

### Kompatibilitas

- **Breaking change**: Tidak.
- **Deprecation**: Tidak berlaku.
- **Catatan**: `store` memakai `sqlite_store` pada profile (spec #4). Library tanpa `Home` memakai `memstore` lewat baris di atas. Spec #1 menambah baris `runner` (`cli_runner`) untuk `headless`.

## API-AGENT-001: `(*Registry).Send`

- **Tujuan**: mengirim pesan ke session. Jika session idle, turn baru dimulai. Jika sibuk, pesan masuk inbox.
- **Requirements**: `REQ-LOOP-001`, `REQ-LOOP-002`
- **Acceptance criteria**: `AC-LOOP-001` sampai `AC-LOOP-006`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku di package ini. Pemanggil (CLI atau library) sudah memilih session.

```go
// Implementasi core.AgentRegistry (signature menurut CQ-010).
func (r *Registry) Send(ctx context.Context, sessionID, text string) error
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `ctx` | `context.Context` | Ya | Hanya untuk validasi dan pembatalan pemanggilan. Tidak membatalkan turn |
| Argumen | `sessionID` | string | Ya | Session harus ada |
| Argumen | `text` | string | Ya | Tidak boleh kosong. Hanya teks (`CQ-005`) |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Session idle | — | Turn baru dimulai. `turn/start` ditulis. Error nil |
| Session sibuk | — | Pesan masuk inbox. Error nil. Pesan di-claim di step berikutnya |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Session tidak ada | — | `ErrSessionNotFound` | Tidak | Tidak ada event |
| Teks kosong | — | `ErrEmptyText` | Ya | Tidak ada event |
| Runtime ditutup | — | `ErrClosed` | Tidak | Tidak ada event |

### Efek samping dan jaminan

- **Transisi state**: idle ke turn berjalan, atau inbox bertambah satu.
- **Idempotensi**: tidak berlaku. Setiap pemanggilan menambah satu pesan.
- **Konkurensi**: pesan di inbox diambil satu per step. Urutan kirim dijaga per session (`AC-LOOP-004`).
- **Paparan data**: teks pesan ditulis ke `user/message`.

### Kompatibilitas

- **Breaking change**: Tidak. Sesuai kebutuhan spec #1 (`ctx.agents.Send`).
- **Deprecation**: Tidak berlaku.

## API-AGENT-002: `(*Registry).Start`

- **Tujuan**: memulai turn baru untuk library dan mengembalikan id turn. Menolak session yang sedang berjalan.
- **Requirements**: `REQ-LOOP-002`, `REQ-LIB-002`
- **Acceptance criteria**: `AC-LIB-005`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku.

```go
func (r *Registry) Start(ctx context.Context, sessionID, text string) (turnID string, err error)
```

### Input

Sama dengan `API-AGENT-001`.

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Session idle | — | `turnID` ULID, error nil. Turn dimulai |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Session sedang berjalan | — | `ErrSessionBusy` | Ya | Tidak ada pesan masuk inbox |
| Kondisi lain | — | Sama dengan `API-AGENT-001` | Lihat tabel di sana | — |

### Efek samping dan jaminan

- **Transisi state**: idle ke turn berjalan.
- **Idempotensi**: tidak berlaku.
- **Konkurensi**: pemeriksaan idle dan mulai turn berada di satu kunci per session.
- **Paparan data**: sama dengan `API-AGENT-001`.

### Kompatibilitas

- **Breaking change**: Tidak. API baru.
- **Deprecation**: Tidak berlaku.

## API-AGENT-003: `(*Registry).Cancel`

- **Tujuan**: membatalkan turn yang sedang berjalan di session.
- **Requirements**: `REQ-LOOP-007`
- **Acceptance criteria**: `AC-LOOP-019`, `AC-LOOP-020`, `AC-LOOP-021`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku.

```go
// Implementasi core.AgentRegistry (API-CORE-010).
func (r *Registry) Cancel(ctx context.Context, sessionID string) error
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `ctx` | `context.Context` | Ya | Hanya untuk pembatalan pemanggilan. Tidak membatalkan turn lewat `ctx` ini |
| Argumen | `sessionID` | string | Ya | Session harus ada |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Turn berjalan | — | Turn berakhir dengan `turn/end` reason `cancelled`. Error nil |
| Session idle | — | Tidak ada event. Error nil (`AC-LOOP-020`) |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Session tidak ada | — | `ErrSessionNotFound` | Tidak | Tidak ada event |

### Efek samping dan jaminan

- **Transisi state**: turn berjalan ke `cancelled`. Inbox tidak berubah (`AC-LOOP-021`).
- **Idempotensi**: `Cancel` kedua pada turn yang sama tidak menulis event tambahan.
- **Konkurensi**: aman dipanggil dari goroutine lain.
- **Paparan data**: tidak berlaku.

### Kompatibilitas

- **Breaking change**: Tidak.
- **Deprecation**: Tidak berlaku.

## API-AGENT-004: `(*Registry).SetAgent`

- **Tujuan**: memilih agent untuk session. Berlaku mulai turn atau step berikutnya.
- **Requirements**: `REQ-LOOP-010`
- **Acceptance criteria**: `AC-LOOP-028`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku.

```go
// Implementasi core.AgentRegistry.
func (r *Registry) SetAgent(ctx context.Context, sessionID, agent string) error
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `sessionID` | string | Ya | Session harus ada |
| Argumen | `agent` | string | Ya | Harus terdaftar di config |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Agent terdaftar | — | Event `model/selection` ditulis (payload di `API-AGENT-008`). Error nil |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Agent tidak terdaftar | — | `ErrUnknownAgent` | Ya | Tidak ada event. Agent lama tetap (`AC-CMD-007` spec #1 berlaku untuk provider) |
| Session tidak ada | — | `ErrSessionNotFound` | Tidak | Tidak ada event |

### Efek samping dan jaminan

- **Transisi state**: agent session berubah. Berlaku mulai step berikutnya.
- **Idempotensi**: memilih agent yang sama dua kali menulis dua event. Tidak berbahaya.
- **Konkurensi**: dibaca per step.
- **Paparan data**: tidak berlaku.

### Kompatibilitas

- **Breaking change**: Tidak.
- **Deprecation**: Tidak berlaku.

## API-AGENT-005: `(*Registry).SetModel`

- **Tujuan**: memilih provider dan model untuk session. Berlaku mulai step berikutnya.
- **Requirements**: `REQ-LOOP-010`
- **Acceptance criteria**: `AC-LOOP-029`, `AC-LOOP-030`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku.

```go
// Implementasi core.AgentRegistry (API-CORE-010). model berbentuk "<provider>:<model>".
func (r *Registry) SetModel(ctx context.Context, sessionID, model string) error
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `sessionID` | string | Ya | Session harus ada |
| Argumen | `model` | string | Ya | `<provider>:<model>`. Bagian provider harus terdaftar di `[providers.<name>]`. Bagian model tidak kosong. Tidak diverifikasi ke provider |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Provider terdaftar | — | Event `model/selection` ditulis. Error nil |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Provider tidak terdaftar | — | `ErrUnknownProvider` | Ya | Pesan menyebut `provider tidak dikenal: <nama>`. Tidak ada event. Format tanpa `:` juga error ini |
| Model kosong | — | `ErrEmptyModel` | Ya | Tidak ada event |
| Session tidak ada | — | `ErrSessionNotFound` | Tidak | Tidak ada event |

### Efek samping dan jaminan

- **Transisi state**: model session berubah mulai step berikutnya.
- **Idempotensi**: sama dengan `SetAgent`.
- **Konkurensi**: dibaca per step.
- **Paparan data**: tidak berlaku.

### Kompatibilitas

- **Breaking change**: Tidak.
- **Deprecation**: Tidak berlaku.

## API-AGENT-006: `(*Registry).SubscribeLive`

- **Tujuan**: menerima frame teks sementara untuk satu session.
- **Requirements**: `REQ-LOOP-009`
- **Acceptance criteria**: `AC-LOOP-026`, `AC-LOOP-027`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku.

```go
// Implementasi core.AgentRegistry (API-CORE-010). Subscription memakai buffer core.ObserveBuffer (1024).
func (r *Registry) SubscribeLive(sessionID string) (*core.Subscription[core.LiveFrame], error)
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen | `sessionID` | string | Ya | Session boleh belum ada turn |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Subscribe berhasil | — | `*core.Subscription` dengan buffer 1024. `Close` pada subscription melepas subscriber |

Frame `text_delta` menjadi `LiveFrame{SessionID, TurnID, Kind: "text_delta", Text}`.

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Buffer penuh | — | — | — | Frame lama dibuang. `Received.Lagged` > 0 dan peringatan `frame hidup dibuang` ditulis ke log (`AC-LOOP-027`) |

### Efek samping dan jaminan

- **Transisi state**: tidak ada di log.
- **Idempotensi**: tidak berlaku.
- **Konkurensi**: satu channel per subscriber. Pengiriman tidak pernah memblokir turn.
- **Paparan data**: teks model. Tidak masuk log.

### Kompatibilitas

- **Breaking change**: Tidak.
- **Deprecation**: Tidak berlaku.

## API-AGENT-007: Key config agent tambahan

- **Tujuan**: key `[agents.<nama>]` yang dipakai loop agent. Key lain di tabel yang sama (`permission`, `rules`, `subagents`, `mcp`, `skills`) dimiliki spec lain.
- **Requirements**: `REQ-LOOP-004`, `REQ-LOOP-005`, `REQ-LOOP-008`, `REQ-LOOP-010`
- **Acceptance criteria**: `AC-LOOP-011`, `AC-LOOP-015`, `AC-LOOP-016`, `AC-LOOP-023`, `AC-LOOP-031`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku.

```toml
[agents.coder]
provider = "openai"
model = "gpt-5-mini"
fallback = ["openrouter:openai/gpt-5-mini"]
prompt_file = "agents/coder.md"
workspace = "~/coding"
tools = ["read_file", "write_file", "edit_file", "glob", "grep", "bash", "web_fetch"]
max_steps = 25
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Config | `provider` | string | Ya | Harus ada di `[providers]` |
| Config | `model` | string | Ya | Tidak kosong |
| Config | `fallback` | array string `provider:model` | Tidak | Setiap provider harus ada. Default kosong |
| Config | `prompt_file` | string | Tidak | Path relatif dari `LANA_HOME`. Kosong = system prompt kosong. Dibaca tiap step |
| Config | `workspace` | string | Tidak | Path absolut atau `~/...`. Kosong = cwd saat `New` (`CQ-010`) |
| Config | `tools` | array string | Tidak | Setiap nama harus tool bawaan (`API-TOOL-*`) atau tool kustom. Default kosong |
| Config | `max_steps` | integer | Tidak | 1 sampai 200. Default 25 (`Q-002`) |

Key `permission` (ditulis oleh `lana init` spec #1) dibaca oleh spec #2 sebagai key yang dikenal.
Spec ini tidak memakainya. Spec #4 yang memakainya.

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Semua key valid | — | Agent terdaftar |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| `max_steps` di luar 1 sampai 200 | — | error spec #2 `REQ-CONF-003` | Tidak | Menyebut key `agents.<nama>.max_steps` |
| Tool tidak dikenal | — | `unknown_tool` | Tidak | Lihat `AC-LIB-003` |
| Provider di `fallback` tidak terdaftar | — | error boot (rujukan config, ADR 0010) | Tidak | Boot gagal dan menyebut provider. Tidak ada fallback diam-diam |

### Efek samping dan jaminan

- **Transisi state**: tidak ada.
- **Idempotensi**: tidak berlaku.
- **Konkurensi**: tidak berlaku.
- **Paparan data**: tidak ada secret di key ini.

### Kompatibilitas

- **Breaking change**: Tidak.
- **Deprecation**: Tidak berlaku.

## API-AGENT-008: Payload event session log

- **Tujuan**: kind dan payload yang ditulis spec ini ke `core.SessionEvent.Payload`.
- **Requirements**: `REQ-LOOP-001`, `REQ-LOOP-003`, `REQ-LOOP-004`, `REQ-LOOP-008`, `REQ-LOOP-010`
- **Acceptance criteria**: `AC-LOOP-001`, `AC-LOOP-009`, `AC-LOOP-010`, `AC-LOOP-022`, `AC-LOOP-024`, `AC-LOOP-028`, `AC-LIB-016`
- **Stabilitas**: Baru.
- **Otorisasi**: Tidak berlaku.

Field setiap payload dimiliki `API-CORE-008` (spec #2, `CQ-029`). Spec ini hanya menulis kind berikut:

| Kind | Struct (`core`) | Keterangan |
|---|---|---|
| `turn/start` | `TurnStart` | Id turn ada di `TurnID` dan payload |
| `turn/end` | `TurnEnd` | `reason`. `error` (`PayloadError`) hanya jika reason `error` |
| `step/start` | `StepStart` | Dicatat sebelum request ke model |
| `step/end` | `StepEnd` | `usage` total step |
| `system/message` | `SystemMessage` | `text`. Hanya jika prompt berubah dari event terakhir |
| `user/message` | `UserMessage` | `content` berisi blok `text` dari pesan yang di-claim |
| `assistant/message` | `AssistantMessage` | `content` berisi blok `text` dan `tool_use`. Revisi 1 memakai `blocks`, diganti `content` (`CQ-029`) |
| `assistant/attempt` | `AssistantAttempt` | `provider`, `model`, `attempt`, `error` (`PayloadError`). Tidak masuk riwayat |
| `tool/call` | `ToolCallPayload` | `call_id`, `name`, `input`. Ditulis sebelum pipeline |
| `tool/result` | `ToolResultPayload` | `call_id`, `status`, `output`. Tepat satu per `tool/call` |
| `model/selection` | `ModelSelection` | `provider`, `model`, `agent` (hanya dari `SetAgent`, `Q-022`) |

Kind `session/created` ditulis spec #4 dengan `workspace` (`Q-017`). Spec ini hanya membacanya.

Contoh `turn/end` dan `tool/result`:

```json
{"v":1,"reason":"cancelled","error":null}
{"v":1,"call_id":"call_1","status":"rejected","output":"default fail-closed: tool write_file butuh izin"}
```

## API-AGENT-009: Pipeline hook tool

- **Tujuan**: urutan hook tool dan handler default. Listener spec #4 dan `PolicyTrust` ditambahkan ke rantai ini.
- **Requirements**: `REQ-TOOL-009`, `REQ-LOOP-003`, `REQ-LOOP-006`, `REQ-LIB-005`
- **Acceptance criteria**: `AC-TOOL-023`, `AC-TOOL-024`, `AC-LOOP-007`, `AC-LOOP-018`, `AC-LIB-012`
- **Stabilitas**: Baru
- **Otorisasi**: `tools.pre_execute` adalah titik izin. Spec ini hanya menyediakan default fail-closed.

Urutan per tool call: `tools.pre_execute` lalu `tools.execute` lalu `tools.post_execute`. Jika
`tools.pre_execute` mengembalikan `Allowed = false`, dua hook berikutnya tidak berjalan dan
`tool/result` berstatus `rejected` (`AC-TOOL-024`).

Listener `PolicyTrust` (`API-LIB-001`) dipasang di `tools.pre_execute` dengan mode waterfall dan
selalu mengembalikan `ToolDecision{Allowed: true}` tanpa memanggil `next`.

## API-PROV-001: Config provider `openai_compat`

- **Tujuan**: key `[providers.<name>]` untuk adapter OpenAI-compatible (OpenAI, OpenRouter, Ollama).
- **Requirements**: `REQ-PROV-001`
- **Acceptance criteria**: `AC-PROV-001`, `AC-PROV-002`, `AC-PROV-003`, `AC-PROV-004`
- **Stabilitas**: Baru
- **Otorisasi**: Key `api_key_env` menunjuk env. Nilainya tidak pernah ditulis di config.

```toml
[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
headers = { "X-Title" = "lana" }
timeout = "300s"

[providers.ollama]
kind = "openai_compat"
base_url = "http://localhost:11434/v1"
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Config | `kind` | string | Ya | Harus `openai_compat`. Nilai lain ditolak (`AC-PROV-003`) |
| Config | `base_url` | string | Ya | URL absolut `http` atau `https`. Tanpa `/chat/completions` di ujungnya |
| Config | `api_key_env` | string | Tidak | Nama env. Kosong = tidak ada header `Authorization` (`AC-PROV-002`) |
| Config | `headers` | tabel string ke string | Tidak | Nilai literal, tidak diinterpolasi (`CQ-007`) |
| Config | `timeout` | string durasi Go | Tidak | Batas total satu request. Default `300s` (`CQ-008`) |

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Config valid dan env terisi (jika dipakai) | — | Provider terdaftar di `LLMRegistry` |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| `kind` bukan `openai_compat` | — | `unsupported_kind` | Tidak | Boot gagal (`AC-PROV-003`) |
| `base_url` tidak absolut atau skema bukan http/https | — | error spec #2 `REQ-CONF-003` | Tidak | Menyebut key |
| Env `api_key_env` kosong untuk provider yang dipakai | — | `missing_secret` | Tidak | Boot gagal (`AC-PROV-004`) |

### Efek samping dan jaminan

- **Transisi state**: tidak ada.
- **Idempotensi**: tidak berlaku.
- **Konkurensi**: adapter aman dipakai dari banyak goroutine.
- **Paparan data**: nilai secret tidak pernah masuk log, error, atau event.

### Kompatibilitas

- **Breaking change**: Tidak.
- **Deprecation**: Tidak berlaku.

## API-PROV-002: Pemetaan wire OpenAI

- **Tujuan**: bentuk request dan stream yang dikirim ke `POST {base_url}/chat/completions`.
- **Requirements**: `REQ-PROV-002`, `REQ-PROV-003`
- **Acceptance criteria**: `AC-PROV-005` sampai `AC-PROV-009`
- **Stabilitas**: Baru
- **Otorisasi**: Header `Authorization: Bearer <secret>` jika `api_key_env` terisi.

Request body:

```json
{
  "model": "gpt-5-mini",
  "stream": true,
  "stream_options": {"include_usage": true},
  "messages": [],
  "tools": [{"type": "function", "function": {"name": "read_file", "description": "...", "parameters": {}}}],
  "max_tokens": 4096,
  "temperature": 0.2
}
```

`max_tokens` dan `temperature` hanya ada jika nilainya terisi di `ChatRequest`.

| `core` | Bentuk OpenAI |
|---|---|
| `ChatRequest.System` | Pesan pertama dengan `role = "system"` |
| Blok `text` pada pesan `user` | `role = "user"`, `content` string |
| Blok `text` pada pesan `assistant` | `role = "assistant"`, `content` string |
| Blok `tool_use` | `tool_calls: [{id, type: "function", function: {name, arguments: <JSON string>}}]` |
| Blok `tool_result` | Pesan `role = "tool"`, `tool_call_id` = `call_id`, `content` = output |
| Blok `image` | Gagal dengan `ErrUnsupported` (`AC-PROV-009`, `Q-023`) |

Stream SSE:

| Bagian stream | Arti |
|---|---|
| `data: {...}` per baris | Satu chunk JSON |
| `choices[0].delta.content` | `text_delta` |
| `choices[0].delta.tool_calls[i]` | Akumulasi per `index`: `id`, `function.name`, `function.arguments` |
| `choices[0].finish_reason` | `stop` menjadi `end_turn`, `tool_calls` menjadi `tool_use`, `length` menjadi `max_tokens` |
| `usage.prompt_tokens` dan `usage.completion_tokens` | `usage` dengan `input_tokens` dan `output_tokens` |
| `data: [DONE]` | Akhir stream |

Setiap `tool_use` terbit setelah `finish_reason` diterima, urut menurut `index`. Jika `id`
kosong, adapter memakai ULID baru sebagai `CallID`.

### Input

Lihat tabel konversi di atas.

### Sukses

| Kondisi | Exit atau status | Output |
|---|---:|---|
| Stream selesai dengan `[DONE]` atau `finish_reason` | — | Event `ChatTextDelta`, `ChatBlock` (`tool_use`), `ChatUsage`, dan `ChatStop` (`API-CORE-006`) |

### Gagal

| Kondisi | Exit atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| Stream terputus sebelum `finish_reason` | — | class `network` | Ya, jika belum ada delta | Lihat `API-PROV-003` |
| Chunk JSON tidak valid | — | class `server` | Ya, jika belum ada delta | Error dengan pesan `respons tidak valid` |
| Gambar dalam riwayat | — | `ErrUnsupported` | Tidak | Request tidak dikirim |

### Efek samping dan jaminan

- **Transisi state**: tidak ada.
- **Idempotensi**: tidak berlaku. Request dapat diulang oleh retry (`REQ-LOOP-008`).
- **Konkurensi**: satu stream per panggilan `Stream`.
- **Paparan data**: isi pesan dikirim ke `base_url`. Secret hanya di header.

### Kompatibilitas

- **Breaking change**: Tidak.
- **Deprecation**: Tidak berlaku.

## API-PROV-003: Kelas error provider

- **Tujuan**: pemetaan kegagalan ke class tetap untuk retry, fallback, dan error Go.
- **Requirements**: `REQ-PROV-004`, `REQ-LOOP-008`
- **Acceptance criteria**: `AC-PROV-010` sampai `AC-PROV-012`, `AC-LOOP-022` sampai `AC-LOOP-025`
- **Stabilitas**: Baru
- **Otorisasi**: Tidak berlaku.

| Class | Asal | Retry | Fallback | Hasil turn |
|---|---|---:|---:|---|
| `auth` | HTTP 401, 403 | Tidak | Tidak | `turn/end` `error` |
| `rate_limit` | HTTP 429 | Ya (3 kali) | Ya | `turn/end` `error` jika habis |
| `server` | HTTP 5xx, stream rusak | Ya (3 kali) | Ya | `turn/end` `error` jika habis |
| `network` | Koneksi gagal, timeout, EOF sebelum `finish_reason` | Ya (3 kali) | Ya | `turn/end` `error` jika habis |
| `context_length` | HTTP 400 dengan `error.code` `context_length_exceeded` | Tidak | Tidak | `turn/end` `error` |
| `client` | HTTP 400, 404, 422 dan 4xx lain | Tidak | Tidak | `turn/end` `error` |
| `missing_secret` | Env `api_key_env` kosong | — | — | Boot gagal (`API-PROV-001`) |

Class `context_length` dikembalikan sebagai `*core.ProviderError` yang `errors.Is(err, core.ErrContextTooLong)` (`CQ-014`). Tanpa compaction, turn berakhir `error` (`Q-005`). Dengan modul compaction (spec #4), listener `llm.stream` meringkas dan mengulang sekali.

Jeda retry: 1 detik, 2 detik, 4 detik (`Q-003`). Gagal setelah delta pertama tidak di-retry
(`Q-006`). Setiap percobaan gagal ditulis sebagai `assistant/attempt`.

## API-PROV-004: Format skrip MockLlm

- **Tujuan**: skrip respons untuk test tanpa jaringan.
- **Requirements**: `REQ-MOCK-001`
- **Acceptance criteria**: `AC-MOCK-001`, `AC-MOCK-002`
- **Stabilitas**: Baru. Hanya untuk test.
- **Otorisasi**: Tidak berlaku.

```go
package mock

type Llm struct{ /* ... */ }

func New(script Script) *Llm
func LoadScript(path string) (Script, error) // format JSON di bawah

type Script struct {
    Responses []Response `json:"responses"`
}

type Response struct {
    Events []Event      `json:"events,omitempty"`
    Error  *ScriptError `json:"error,omitempty"` // terminal untuk panggilan ini
}
```

Format JSON:

```json
{
  "responses": [
    {"events": [
      {"text": "Halo"},
      {"delay_ms": 50},
      {"text": " dunia"},
      {"tool_use": {"id": "call_1", "name": "read_file", "input": {"path": "a.txt"}}},
      {"usage": {"input_tokens": 10, "output_tokens": 4}},
      {"stop": "tool_use"}
    ]},
    {"error": {"class": "rate_limit", "message": "429 dari mock"}}
  ]
}
```

| Aturan | Nilai |
|---|---|
| Satu elemen `responses` | Satu panggilan `Stream` |
| Satu objek `events` | Tepat satu key: `text`, `delay_ms`, `tool_use`, `usage`, atau `stop` |
| `error` | Terminal. Boleh setelah event lain (error di tengah stream) |
| `error.class` | Salah satu class di `API-PROV-003` |
| Panggilan melebihi `responses` | Error `skrip habis` |
| Key tidak dikenal | Error saat `LoadScript` (`AC-MOCK-001`) |

Nilai `stop` memakai `ChatEvent.StopReason` (`end_turn`, `tool_use`, `max_tokens`). Key `tool_use` di skrip menghasilkan `ChatBlock` dengan `Block.Kind = tool_use`.

## API-TOOL-001: `read_file`

- **Tujuan**: membaca isi file teks di workspace.
- **Requirements**: `REQ-TOOL-001`, `REQ-TOOL-008`
- **Acceptance criteria**: `AC-TOOL-001`, `AC-TOOL-002`, `AC-TOOL-021`, `AC-TOOL-022`
- **Stabilitas**: Baru
- **Risk**: `Read`
- **Otorisasi**: Path harus di dalam workspace (`REQ-TOOL-008`).

```json
{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":2000}},"required":["path"],"additionalProperties":false}
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Input | `path` | string | Ya | Relatif terhadap workspace atau absolut di dalamnya |
| Input | `offset` | integer | Tidak | Baris pertama, 1-based. Default 1 |
| Input | `limit` | integer | Tidak | 1 sampai 2000. Default 2000 |

### Sukses

| Kondisi | Status | Output |
|---|---|---|
| File teks terbaca | `success` | Isi baris `offset` sampai `offset + limit - 1`, dipotong 30000 karakter dengan catatan |

### Gagal

| Kondisi | Status | Output |
|---|---|---|
| Input tidak valid | `error` | Pesan input |
| File tidak ada | `error` | `file tidak ditemukan` |
| File biner (NUL dalam 8000 byte pertama) | `error` | `file biner` |
| Path di luar workspace | `rejected` | `di luar workspace` |

## API-TOOL-002: `write_file`

- **Tujuan**: membuat atau mengganti file teks di workspace.
- **Requirements**: `REQ-TOOL-002`, `REQ-TOOL-008`
- **Acceptance criteria**: `AC-TOOL-003` sampai `AC-TOOL-005`
- **Stabilitas**: Baru
- **Risk**: `Write`
- **Otorisasi**: Gerbang `tools.pre_execute` (`REQ-TOOL-009`). Path di dalam workspace.

```json
{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Input | `path` | string | Ya | Path di dalam workspace |
| Input | `content` | string | Ya | Boleh kosong. Mengganti seluruh isi file |

### Sukses

| Kondisi | Status | Output |
|---|---|---|
| File dibuat atau diganti | `success` | `ditulis <n> byte` |

### Gagal

| Kondisi | Status | Output |
|---|---|---|
| Direktori induk tidak ada | `error` | `direktori induk tidak ada`. Tidak ada file dibuat |
| Path di luar workspace | `rejected` | `di luar workspace` |
| Tidak diizinkan gerbang | `rejected` | Alasan gerbang |

## API-TOOL-003: `edit_file`

- **Tujuan**: mengganti satu kemunculan teks persis dalam file.
- **Requirements**: `REQ-TOOL-003`, `REQ-TOOL-008`
- **Acceptance criteria**: `AC-TOOL-006` sampai `AC-TOOL-008`
- **Stabilitas**: Baru
- **Risk**: `Write`
- **Otorisasi**: Sama dengan `write_file`.

```json
{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string","minLength":1},"new_string":{"type":"string"}},"required":["path","old_string","new_string"],"additionalProperties":false}
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Input | `path` | string | Ya | Path di dalam workspace |
| Input | `old_string` | string | Ya | Tidak boleh kosong |
| Input | `new_string` | string | Ya | Boleh kosong (menghapus teks) |

### Sukses

| Kondisi | Status | Output |
|---|---|---|
| `old_string` tepat sekali | `success` | `diubah 1 kemunculan` |

### Gagal

| Kondisi | Status | Output |
|---|---|---|
| `old_string` tidak ada | `error` | `tidak ditemukan`. File tidak berubah |
| `old_string` lebih dari sekali | `error` | `ditemukan <n> kali; perbanyak konteks`. File tidak berubah |
| `old_string` kosong | `error` | Pesan input tidak valid |

## API-TOOL-004: `glob`

- **Tujuan**: mencari path dengan pola glob di workspace.
- **Requirements**: `REQ-TOOL-004`, `REQ-TOOL-008`
- **Acceptance criteria**: `AC-TOOL-009`, `AC-TOOL-010`
- **Stabilitas**: Baru
- **Risk**: `Read`
- **Otorisasi**: Path (jika diisi) di dalam workspace.

```json
{"type":"object","properties":{"pattern":{"type":"string","minLength":1},"path":{"type":"string"}},"required":["pattern"],"additionalProperties":false}
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Input | `pattern` | string | Ya | Glob dengan `**` (library `doublestar`) |
| Input | `path` | string | Tidak | Direktori di dalam workspace. Default workspace |

### Sukses

| Kondisi | Status | Output |
|---|---|---|
| Ada hasil | `success` | Path relatif workspace, satu per baris, terurut. Maksimal 1000. Jika lebih, catatan `dari <n> hasil` |
| Tidak ada hasil | `success` | `tidak ada hasil` |

Path di `.git/` dan path yang cocok dengan `.gitignore` root tidak muncul (`Q-013`).

### Gagal

| Kondisi | Status | Output |
|---|---|---|
| Pola kosong atau tidak valid | `error` | Pesan pola tidak valid |
| Path di luar workspace | `rejected` | `di luar workspace` |

## API-TOOL-005: `grep`

- **Tujuan**: mencari teks dengan regex RE2 di file workspace.
- **Requirements**: `REQ-TOOL-005`, `REQ-TOOL-008`
- **Acceptance criteria**: `AC-TOOL-011` sampai `AC-TOOL-013`
- **Stabilitas**: Baru
- **Risk**: `Read`
- **Otorisasi**: Path (jika diisi) di dalam workspace.

```json
{"type":"object","properties":{"pattern":{"type":"string","minLength":1},"path":{"type":"string"},"glob":{"type":"string"}},"required":["pattern"],"additionalProperties":false}
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Input | `pattern` | string | Ya | Sintaks `regexp` Go (RE2) |
| Input | `path` | string | Tidak | Direktori di dalam workspace. Default workspace |
| Input | `glob` | string | Tidak | Filter path file relatif (`doublestar`) |

### Sukses

| Kondisi | Status | Output |
|---|---|---|
| Ada kecocokan | `success` | Satu baris `path:baris: teks` per kecocokan. Maksimal 200. Teks baris dipotong 500 karakter |
| Tidak ada kecocokan | `success` | `tidak ada kecocokan` |

File biner dan file lebih dari 1 MiB dilewati (`AC-TOOL-013`). `.git/` dan `.gitignore` root dilewati.

### Gagal

| Kondisi | Status | Output |
|---|---|---|
| Pola tidak valid | `error` | `pola tidak valid: <pesan>` |
| Path di luar workspace | `rejected` | `di luar workspace` |

## API-TOOL-006: `bash`

- **Tujuan**: menjalankan perintah shell di workspace.
- **Requirements**: `REQ-TOOL-006`
- **Acceptance criteria**: `AC-TOOL-014` sampai `AC-TOOL-017`
- **Stabilitas**: Baru
- **Risk**: `Exec`
- **Otorisasi**: Gerbang `tools.pre_execute` (`REQ-TOOL-009`). Tidak ada sandbox (ADR 0004).

```json
{"type":"object","properties":{"command":{"type":"string","minLength":1},"timeout_secs":{"type":"integer","minimum":1,"maximum":600}},"required":["command"],"additionalProperties":false}
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Input | `command` | string | Ya | Dijalankan dengan `sh -c`. Cwd = workspace |
| Input | `timeout_secs` | integer | Tidak | 1 sampai 600. Default 120 (`CQ-009`) |

### Sukses

| Kondisi | Status | Output |
|---|---|---|
| Exit code 0 | `success` | stdout dan stderr gabungan. Maksimal 30000 karakter, sisanya dipotong dengan catatan jumlah karakter asli |

### Gagal

| Kondisi | Status | Output |
|---|---|---|
| Exit code bukan 0 | `error` | Baris `exit code <n>` diikuti output |
| Timeout | `error` | `timeout setelah <n> detik`. Grup proses dihentikan |
| `timeout_secs` di luar 1 sampai 600 | `error` | `timeout_secs harus 1 sampai 600`. Perintah tidak dijalankan |
| Dibatalkan turn | `error` | `dibatalkan` |
| Tidak diizinkan gerbang | `rejected` | Alasan gerbang |

## API-TOOL-007: `web_fetch`

- **Tujuan**: mengambil URL dan mengembalikan isi sebagai teks.
- **Requirements**: `REQ-TOOL-007`
- **Acceptance criteria**: `AC-TOOL-018` sampai `AC-TOOL-020`
- **Stabilitas**: Baru
- **Risk**: `Network`
- **Otorisasi**: Gerbang `tools.pre_execute` (`REQ-TOOL-009`). Host tidak dibatasi di spec ini (lihat risiko di design).

```json
{"type":"object","properties":{"url":{"type":"string","format":"uri","pattern":"^https?://"}},"required":["url"],"additionalProperties":false}
```

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Input | `url` | string | Ya | Harus `http` atau `https` |

### Sukses

| Kondisi | Status | Output |
|---|---|---|
| `text/html` | `success` | Markdown tanpa `script` dan `style`, dipotong 30000 karakter |
| `text/plain` atau `application/json` | `success` | Isi apa adanya, dipotong 30000 karakter |

Batas: timeout 30 detik, body maksimal 5 MiB, maksimal 5 redirect (`Q-014`).

### Gagal

| Kondisi | Status | Output |
|---|---|---|
| URL bukan `http` atau `https` | `error` | `url harus http atau https`. Tidak ada request |
| Content-type lain | `error` | `tipe konten tidak didukung: <tipe>` |
| Body lebih dari 5 MiB | `error` | `respons terlalu besar` |
| Timeout 30 detik | `error` | `timeout setelah 30 detik` |
| Status HTTP 4xx atau 5xx | `error` | `HTTP <kode>` |
| Tidak diizinkan gerbang | `rejected` | Alasan gerbang |

## Traceability contract

| Operasi | Requirements | Acceptance criteria | Komponen design | Verifikasi |
|---|---|---|---|---|
| `API-LIB-001` | `REQ-LIB-001`, `REQ-LIB-006`, `REQ-LIB-008` | `AC-LIB-001`, `AC-LIB-002`, `AC-LIB-003`, `AC-LIB-013`, `AC-LIB-014` | `lana.go`, `config` loader, modul `agent` | Test `lana` dengan layer memori, `MockLlm` |
| `API-LIB-002` | `REQ-LIB-002`, `REQ-LIB-007` | `AC-LIB-004` sampai `AC-LIB-008`, `AC-LIB-015`, `AC-LIB-016` | `lana.go`, `agent.Registry` | Test `lana` dengan `MockLlm` dan memstore |
| `API-LIB-003` | `REQ-LIB-003` | `AC-LIB-009`, `AC-LIB-010` | `lana.go`, hub frame | Test iterator dan context |
| `API-LIB-004` | `REQ-LIB-004` | `AC-LIB-011` | `lana.go`, `agent.Registry` | Test shutdown dengan turn tertahan |
| `API-LIB-005` | `REQ-LIB-008` | `AC-LIB-017` | `config` (data profile) | Test profile |
| `API-AGENT-001` | `REQ-LOOP-001`, `REQ-LOOP-002` | `AC-LOOP-001` sampai `AC-LOOP-006` | `agent.Registry`, `agent.Runner`, `agent.TurnLoop` | Test turn dengan `MockLlm` |
| `API-AGENT-002` | `REQ-LOOP-002`, `REQ-LIB-002` | `AC-LIB-005` | `agent.Registry` | Test concurrency |
| `API-AGENT-003` | `REQ-LOOP-007` | `AC-LOOP-019` sampai `AC-LOOP-021` | `agent.Runner` | Test cancel dengan `bash` tertahan |
| `API-AGENT-004` | `REQ-LOOP-010` | `AC-LOOP-028` | `agent.Registry` | Test selection |
| `API-AGENT-005` | `REQ-LOOP-010` | `AC-LOOP-029`, `AC-LOOP-030` | `agent.Registry` | Test selection |
| `API-AGENT-006` | `REQ-LOOP-009` | `AC-LOOP-026`, `AC-LOOP-027` | `agent.Hub` | Test frame |
| `API-AGENT-007` | `REQ-LOOP-004`, `REQ-LOOP-005`, `REQ-LOOP-008`, `REQ-LOOP-010` | `AC-LOOP-011`, `AC-LOOP-015`, `AC-LOOP-016`, `AC-LOOP-023`, `AC-LOOP-031` | `config`, `agent` | Test config dan turn |
| `API-AGENT-008` | `REQ-LOOP-001`, `REQ-LOOP-003`, `REQ-LOOP-004`, `REQ-LOOP-008`, `REQ-LOOP-010` | `AC-LOOP-001`, `AC-LOOP-009`, `AC-LOOP-010`, `AC-LOOP-022`, `AC-LOOP-024`, `AC-LOOP-028`, `AC-LIB-016` | `agent.Events` | Test payload golden |
| `API-AGENT-009` | `REQ-TOOL-009`, `REQ-LOOP-003`, `REQ-LOOP-006`, `REQ-LIB-005` | `AC-TOOL-023`, `AC-TOOL-024`, `AC-LOOP-007`, `AC-LOOP-018`, `AC-LIB-012` | `tools.Pipeline`, `agent.Hooks` | Test hook |
| `API-PROV-001` | `REQ-PROV-001` | `AC-PROV-001` sampai `AC-PROV-004` | `providers/openai` (config) | Test config |
| `API-PROV-002` | `REQ-PROV-002`, `REQ-PROV-003` | `AC-PROV-005` sampai `AC-PROV-009` | `providers/openai` (konversi, SSE) | Test httptest dengan fixture SSE |
| `API-PROV-003` | `REQ-PROV-004`, `REQ-LOOP-008` | `AC-PROV-010` sampai `AC-PROV-012`, `AC-LOOP-022` sampai `AC-LOOP-025` | `providers/openai` (classify), `agent.Retry` | Test tabel error |
| `API-PROV-004` | `REQ-MOCK-001` | `AC-MOCK-001`, `AC-MOCK-002` | `providers/mock` | Test unit |
| `API-TOOL-001` sampai `API-TOOL-007` | `REQ-TOOL-001` sampai `REQ-TOOL-008` | `AC-TOOL-001` sampai `AC-TOOL-022` | `tools/*`, `tools.Guard` | Test unit per tool |

## Blocking Contract Questions

| ID | Pertanyaan | Mengendalikan | Pemilik | Status | Resolusi |
|---|---|---|---|---|---|
| `CQ-001` | Bentuk `rt.Stream`: iterator atau channel | `API-LIB-003` | Ridho | Resolved | Rekomendasi agent: `iter.Seq2[Event, error]`. Sesuai ADR 0014 ("iterator event") dan Go 1.23+. |
| `CQ-002` | Nama error class provider | `API-PROV-003`, `API-LIB-002` | Ridho | Resolved | Rekomendasi agent: `auth`, `rate_limit`, `server`, `network`, `context_length`, `client`, `missing_secret`. |
| `CQ-003` | Format skrip MockLlm | `API-PROV-004` | Ridho | Resolved | Rekomendasi agent: JSON, dibaca `LoadScript`, dan sama dengan struct Go. |
| `CQ-004` | Field payload event, termasuk `v` dan field tambahan | `API-AGENT-008` | Ridho | Resolved | Rekomendasi agent: `v = 1`, `snake_case`. Tambahan: `model/selection.agent` (`Q-022`) dan `session/created.workspace` (`Q-017`). Disetujui spec #4. Dimasukkan di `API-CORE-008` (`CQ-017`, `CQ-029` spec #2). |
| `CQ-005` | Argumen `Send`: hanya teks atau `ContentBlock` | `API-AGENT-001` | Ridho | Resolved | Rekomendasi agent: teks saja. Gambar ditunda (`Q-023`). |
| `CQ-006` | Nilai status tool | `API-TOOL-*` | Ridho | Resolved | Rekomendasi agent: `success`, `error`, `rejected`, sama dengan spec #1. |
| `CQ-007` | Nilai `headers` diinterpolasi atau literal | `API-PROV-001` | Ridho | Resolved | Rekomendasi agent: literal. Interpolasi `${VAR}` hanya di `mcp.*` (spec #2 `REQ-CONF-007`). |
| `CQ-008` | Default `timeout` provider | `API-PROV-001` | Ridho | Resolved | Rekomendasi agent: `300s` untuk total satu request. |
| `CQ-009` | Nama input timeout bash | `API-TOOL-006` | Ridho | Resolved | Rekomendasi agent: `timeout_secs`, bilangan bulat, default 120, maksimal 600. |
| `CQ-010` | Signature `core.AgentRegistry` dan `core.Provider` | `API-AGENT-*`, `API-PROV-002` | Ridho | Resolved | Rekomendasi agent: signature di contract ini dipakai sampai spec #2 contract Approved. Spec #2 menyalin signature ini atau mengubahnya dengan catatan di `CQ` spec #2. Direvisi: lihat `CQ-012`. |
| `CQ-011` | Nama dan bentuk tipe tambahan ke `core` | Bagian tipe core | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): seluruh tipe "tambahan" revisi 1 dipindah ke `API-CORE-015` (spec #2). Rename: `StepInput` menjadi `PreStepInput`, `AgentTurnStopInput` menjadi `TurnStopInput`, `TurnStopVote` menjadi `StopVote`, `ToolDecision.Allow` menjadi `Allowed`, `Agent` menjadi `AgentName`. Bentuk `ChatEvent` dan `ToolCall` memakai spec #2 (`CQ-019` spec #2). |
| `CQ-012` | Signature `AgentRegistry`: `Cancel`, `SetModel`, `SubscribeLive` | `API-AGENT-003`, `API-AGENT-005`, `API-AGENT-006` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): signature spec #2 (`API-CORE-010`) menang. `Cancel(ctx, sessionID)`. `SetModel(ctx, sessionID, model)` dengan model `provider:model`. `SubscribeLive(sessionID)` mengembalikan `*core.Subscription[core.LiveFrame]`. |
| `CQ-013` | Riwayat model dari log | `API-AGENT-008`, `Derive` di design | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `core.DeriveMessages` (`API-CORE-015`) dipakai. Store menyediakan `DeriveMessages(ctx, sessionID)`. Spec ini mengimplementasikan subset tanpa compaction (`CQ-028` spec #2). Lihat `Q-024`. |
| `CQ-014` | Error konteks terlalu panjang | `API-PROV-003`, `API-LIB-002` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): adapter mengembalikan `*core.ProviderError` dengan class `context_length` yang `errors.Is(err, core.ErrContextTooLong)` (`CQ-026` spec #2). Turn berakhir `error` jika compaction spec #4 tidak aktif (`Q-005`). Dengan compaction, listener spec #4 meringkas lalu mengulang sekali. |
| `CQ-015` | Profile bawaan, baris `store`, dan `headless` | `API-LIB-005`, `API-LIB-001` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): profile bawaan dimiliki spec #2 (`API-CONF-009`, `CQ-031`). `minimal` sama dengan base. `headless` sama dengan base ditambah `cli_runner`. `lana.New` memakai `memstore` jika `Home` dan `Options.Store` kosong (`DQ-016` spec #2). |
| `CQ-016` | Opsi approval library `ApproveWith` | `API-LIB-001`, `REQ-LIB-006` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `lana.New(ctx, opts, ...Option)`. `ApproveWith(fn)` adalah `Option`. Implementasi, kontrak, dan AC di spec #4 (`REQ-APPR-007`, `AC-APPR-017`, `API-APPR-005`). |
| `CQ-017` | Payload `session/created.workspace` dan `model/selection.agent` | `API-AGENT-008` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): disetujui spec #4. Field masuk `API-CORE-008` (spec #2). |
| `CQ-018` | Kind `anthropic` pada build ini | `API-PROV-001` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): tetap ditolak sampai M4. `lana init` spec #1 hanya menawarkan kind yang didukung build (`CQ-009` spec #1). |
| `CQ-019` | `CompactNow` di `AgentRegistry` | `API-AGENT-*` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `AgentRegistry` tidak punya `CompactNow`. `/compact` memakai `core.Compactor.CompactNow` dari spec #4 (`API-COMP-001`, `CQ-027` spec #2). |

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
