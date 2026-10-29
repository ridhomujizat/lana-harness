# Requirements: Agent loop, provider, tools, Go library

- **Issue**: [`#3`](https://github.com/ridhomujizat/lana-harness/issues/3)
- **Status**: Approved
- **Revisi**: 2 (rekonsiliasi lintas spec: tipe error konteks, derive di `core`, profile base, `ApproveWith` di spec #4, lihat Q-027 sampai Q-032)
- **Pemilik**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Diperlukan (Go API `lana` dan `agent`, key config `[providers.<name>]`, skema input tool, format MockLlm, tipe event `rt.Stream`)
- **ADR terkait**: [Index ADR](../../adr/README.md) — [0002](../../adr/0002-core-dari-modul-service-dan-hook.md), [0003](../../adr/0003-multi-agent-dengan-routing-channel.md), [0004](../../adr/0004-permission-mode-per-agent.md), [0006](../../adr/0006-ekstensi-lewat-mcp-client-dan-skills.md), [0009](../../adr/0009-session-sebagai-event-log-append-only.md), [0010](../../adr/0010-profile-dan-config-patch.md), [0013](../../adr/0013-gunakan-go-sebagai-single-binary.md), [0014](../../adr/0014-integrasi-sebagai-agentic-tool.md)
- **Diagram terkait**: [Index diagram](../../diagram/README.md) — [0002](../../diagram/0002-agent-loop-dan-approval.md), [0005](../../diagram/0005-integrasi-agentic-tool.md)

## Sumber

| Sumber | Referensi | Dipakai untuk |
|---|---|---|
| Issue | [`#3`](https://github.com/ridhomujizat/lana-harness/issues/3) | cerita dan scope spec ini |
| ADR 0002 | Turn, step, hook, invariant `tool/call` dan `tool/result` | `REQ-LOOP-001` sampai `REQ-LOOP-006` |
| ADR 0003 | Inbox per session, claim satu pesan per step, routing | `REQ-LOOP-002`, `REQ-LIB-007` |
| ADR 0004 | Policy `never`, default fail-closed, outcome `unavailable` | `REQ-TOOL-009`, `REQ-LIB-006` |
| ADR 0006 | Risk per tool (`Read`, `Exec`, `Network`) | `REQ-TOOL-009` |
| ADR 0009 | Event log, `assistant/attempt`, `model/selection`, frame hidup | `REQ-LOOP-003`, `REQ-LOOP-009`, `REQ-LOOP-010`, `REQ-MEM-001` |
| ADR 0010 | Profile `minimal` dan `headless`, baris `[[modules]]` | `REQ-LIB-008` |
| ADR 0013 | Layout package `agent/`, `providers/openai/`, `tools/`, `lana.go` | seluruh spec |
| ADR 0014 | Go library, `Options`, `Run`, `Stream`, `Close`, sesi channel `library` | `REQ-LIB-*` |
| Diagram 0002 | Urutan turn dan step, failure handling (retry 3 kali lalu fallback) | `REQ-LOOP-001`, `REQ-LOOP-008` |
| Diagram 0005 | Embed Go library dan alur tool milik service | `REQ-LIB-005` |
| Spec #1 | Bagian `Interface core yang dibutuhkan`, nama argumen tool, exit code 3 untuk provider | `REQ-LOOP-007`, `REQ-LOOP-009`, `REQ-TOOL-*`, `REQ-PROV-004` |
| Spec #2 | Core: `Message`, `ContentBlock`, `SessionEvent`, `Provider`, `Tool`, `SessionStore`, `AgentRegistry`, hook, `config.Load` | dependency spec ini |
| Keputusan pemilik 2026-10-29 | Pembagian spec core (#2, #3, #4), daftar tool, `PolicyTrust`, `MockLlm`, memstore | scope |
| Resolusi agent | Pertanyaan yang dipilih agent atas izin pemilik (lihat Resolusi pertanyaan) | nilai default dan detail yang belum ada di sumber |

## Tujuan

Pemilik dan service Go bisa menjalankan satu turn agent dari pesan sampai jawaban final. Turn
memakai provider OpenAI-compatible, tool bawaan, dan session di memori. Loop yang sama dipakai
oleh CLI (spec #1) dan oleh library `lana`. Spec ini tidak menambah permission penuh, approval,
penyimpanan SQLite, compaction, atau fork.

## Aktor

- **Pemilik** — menjalankan `lana` lewat CLI (spec #1). Memilih agent dan model. Belum bisa
  mengizinkan tool selain `Read` tanpa listener `PolicyTrust` (default fail-closed).
- **Service Go** — meng-import package `lana`. Mendaftarkan tool sendiri dan memilih opsi approval.
- **Model LLM** — mengirim teks dan tool call. Tool call hanya jalan jika lolos gerbang dan skema.
- **Developer** — menjalankan test dengan `MockLlm` dan server HTTP palsu.

## Definisi

- **Turn** — rangkaian nol atau lebih step untuk satu pesan yang di-claim, sampai tidak ada lagi
  yang harus dijawab (ADR 0002).
- **Step** — satu request ke model, ditambah tool call yang dipanggil dari jawaban itu.
- **Inbox** — antrean pesan per session. Pesan masuk saat turn berjalan.
- **Claim** — pengambilan satu pesan dari inbox di awal step.
- **Target** — pasangan `provider` dan `model`. Target pertama adalah primary, sisanya fallback.
- **Gerbang default** — perilaku handler default `tools.pre_execute`: tool `Read` jalan, tool lain ditolak.
- **Frame hidup** — teks delta sementara untuk tampilan. Frame tidak masuk log (ADR 0009).
- **Workspace** — direktori kerja tempat tool file dan `bash` bekerja.
- **Error class** — kelas kegagalan provider: `auth`, `rate_limit`, `server`, `network`,
  `context_length`, `client`, dan `missing_secret` (saat boot).
- **Status tool** — hasil tool: `success`, `error`, atau `rejected` (ditolak gerbang atau batas workspace).

## Scope

### Termasuk

- Agent loop: turn, step, inbox, claim, hook `agent.*`, batas step, penghentian, pembatalan,
  retry, fallback, routing agent, `SetAgent`, `SetModel`, frame hidup.
- Provider `openai_compat` (OpenAI, OpenRouter, Ollama) dengan streaming SSE dan tool call.
- `MockLlm` untuk test.
- Tool bawaan: `read_file`, `write_file`, `edit_file`, `glob`, `grep`, `bash`, `web_fetch`.
- Helper batas workspace dan gerbang default `tools.pre_execute`.
- Store di memori `memstore` (implementasi `core.SessionStore`) untuk test dan library.
- Package `lana`: `New`, `Run`, `Stream`, `Close`, tool kustom, opsi approval `PolicyNever` dan `PolicyTrust`.
- Profile bawaan `minimal` dan `headless` sebagai baris modul.

### Tidak termasuk

- Compaction dan event `compaction/summary` (ADR 0009). Spec #4.
- SQLite store, resume setelah crash, fork, lease lintas proses, dan `derive_messages` lengkap. Spec #4.
- Permission engine: rule `allow` dan `deny`, policy session, approval service, hook `approval.request`,
  dan `lana.ApproveWith`. Spec #4.
- Tool subagent `task` dan `task_continue` (ADR 0011). M4.
- Adapter Anthropic native (ADR 0013, `providers/anthropic/`). M4.
- MCP client, skills, dan tool `skill_load` (ADR 0006). M4.
- CLI, TUI, subcommand, dan runner one-shot. Spec #1.
- Sandbox OS (ADR 0004).
- Input gambar dari user (block `Image` dalam pesan user).

## Asumsi

| Asumsi | Sumber |
|---|---|
| Tipe `core.Message`, `core.ContentBlock`, `core.SessionEvent`, `core.Secret`, interface `core.Provider`, `core.Tool`, `core.SessionStore`, `core.AgentRegistry`, hook bertipe, dan `core.Module` dimiliki spec #2 | Keputusan pemilik 2026-10-29 |
| Config efektif, secret, dan validasi referensi dimuat oleh `config.Load` spec #2 | Spec #2 `REQ-CONF-001`, `REQ-CONF-005`, `REQ-CONF-009` |
| Provider, agent, dan route dibaca dari config efektif profile | ADR 0010, ADR 0003 |
| Pesan yang masuk saat turn berjalan di-claim di awal step berikutnya | ADR 0003, spec #1 `AC-CHAT-012` |
| Setiap `tool/call` punya `tool/result`, termasuk saat ditolak, dibatalkan, atau error | ADR 0002 invariant |
| Setiap request model bisa direkonstruksi dari log (*model-visible means logged*) | ADR 0009 |
| Frame text delta tidak ditulis ke log | ADR 0009 |
| Format model `<provider>:<model>` untuk `fallback` dan `SetModel` | Spec #1 konvensi bersama, ADR 0003 |
| Secret tidak pernah dicetak, termasuk di log dan error | Spec #2 `REQ-CONF-005`, spec #1 konvensi secret |

## Aturan perilaku

### Agent loop

#### REQ-LOOP-001: Turn dan step

Sistem harus menjalankan turn sebagai nol atau lebih step sesuai ADR 0002. Turn dibuka dengan
`turn/start` sebelum input pertama di-claim dan ditutup dengan `turn/end`. Setiap step meng-claim
paling banyak satu pesan dari inbox.

- `AC-LOOP-001`: Given session idle dan satu pesan dikirim, when model menjawab tanpa tool call, then log berisi `turn/start`, `user/message`, `step/start`, `assistant/message`, `step/end`, dan `turn/end` berurutan dengan reason `end_turn`.
- `AC-LOOP-002`: Given hook `agent.pre_step` menolak pesan di step pertama, when turn berjalan, then tidak ada `step/start` dan log berisi `turn/end`.
- `AC-LOOP-003`: Given dua pesan antre saat step berikutnya dimulai, when step itu meng-claim input, then hanya satu pesan di-claim dan tercatat `user/message`, dan pesan kedua tetap di inbox untuk step berikutnya.

#### REQ-LOOP-002: Inbox dan satu turn aktif per session

Sistem harus menerima pesan saat turn berjalan dan menaruhnya di inbox session. Setiap session
hanya punya satu turn aktif. Session berbeda berjalan paralel.

- `AC-LOOP-004`: Given turn berjalan, when pesan dikirim ke session yang sama, then pesan masuk inbox, tidak ada turn kedua yang dimulai, dan pengirim tidak menunggu turn selesai.
- `AC-LOOP-005`: Given dua session berbeda menerima pesan bersamaan, when keduanya berjalan, then masing-masing selesai dengan log sendiri dan tidak ada event yang tertukar antar session.
- `AC-LOOP-006`: Given pesan tiba saat turn sedang menutup, when turn berakhir, then pesan itu tidak hilang dan dijawab oleh turn berikutnya.

#### REQ-LOOP-003: Pemanggilan model

Sistem harus menyusun request dari riwayat yang diturunkan dari log, memanggil hook
`agent.request` dan `llm.stream`, lalu mencatat jawaban model sebagai `assistant/message`. Delta
teks tidak ditulis ke log.

- `AC-LOOP-007`: Given listener `agent.request` mengubah `max_tokens`, when step meminta model, then provider menerima nilai hasil listener.
- `AC-LOOP-008`: Given `system/message` terakhir berisi prompt yang sama dengan prompt agent saat ini, when step berikutnya dimulai, then tidak ada `system/message` baru. Given prompt berubah, then `system/message` baru ditulis sebelum `user/message`.
- `AC-LOOP-009`: Given provider mengirim beberapa delta teks dan satu tool call, when stream selesai, then satu `assistant/message` berisi text block dan tool_use block, dan tidak ada event log untuk setiap delta.

#### REQ-LOOP-004: Tool call dan tool result

Sistem harus mencatat setiap tool call sebagai `tool/call`, menjalankan pipeline
`tools.pre_execute`, `tools.execute`, dan `tools.post_execute`, lalu mencatat `tool/result`. Setiap
`tool/call` punya tepat satu `tool/result`. Tool `Read` dalam satu step berjalan paralel. Tool
`Write` dan `Exec` berjalan satu per satu.

- `AC-LOOP-010`: Given model memanggil tiga tool, di mana satu ditolak gerbang, satu gagal, dan satu sukses, when step selesai, then setiap `tool/call` punya tepat satu `tool/result` dengan `call_id` sama dan status sesuai hasilnya.
- `AC-LOOP-011`: Given model memanggil tool yang tidak ada dalam daftar `tools` agent, when step berjalan, then tool tidak dijalankan dan `tool/result` berstatus `error` dengan pesan `tool tidak diaktifkan untuk agent`.
- `AC-LOOP-012`: Given tiga tool `Read` dalam satu step, when dijalankan, then ketiganya berjalan bersamaan dan `tool/result` ditulis sesuai urutan `tool/call`.
- `AC-LOOP-013`: Given tool `Write` atau `Exec` dalam satu step, when dijalankan, then tool itu berjalan sendiri dan tidak bersamaan dengan tool lain.
- `AC-LOOP-014`: Given input tool tidak sesuai skema tool, when tool dipanggil, then tool tidak berjalan dan `tool/result` berstatus `error`.

#### REQ-LOOP-005: Batas step

Sistem harus menghentikan turn dengan reason `max_steps` jika step berikutnya diperlukan tetapi
batas `max_steps` agent sudah tercapai. Default `max_steps` adalah 25.

- `AC-LOOP-015`: Given agent tanpa `max_steps` dan model selalu meminta tool, when step ke-25 selesai, then turn berakhir dengan `turn/end` reason `max_steps`.
- `AC-LOOP-016`: Given agent dengan `max_steps = 3` dan turn memerlukan step ke-4, when step ke-3 selesai, then step ke-4 tidak dimulai dan turn berakhir dengan reason `max_steps`.

#### REQ-LOOP-006: Penghentian turn

Sistem harus memanggil hook `agent.turn_stopping` sebelum turn ditutup dengan `end_turn`. Jika
listener mengembalikan `Continue`, turn berlanjut dengan step baru tanpa pesan baru, sampai batas
step tercapai.

- `AC-LOOP-017`: Given tidak ada listener `agent.turn_stopping` dan step selesai tanpa tool call, when inbox kosong, then turn berakhir dengan reason `end_turn`.
- `AC-LOOP-018`: Given listener `agent.turn_stopping` mengembalikan `Continue` satu kali, when step selesai, then step baru dimulai tanpa `user/message` baru, dan turn berakhir setelah step itu tanpa `Continue`.

#### REQ-LOOP-007: Pembatalan turn

Sistem harus menghentikan turn saat `Cancel` dipanggil. Tool yang sedang berjalan dihentikan dan
mendapat `tool/result` error. Pesan yang masih di inbox tetap di inbox.

- `AC-LOOP-019`: Given tool `bash` sedang berjalan, when `Cancel` dipanggil, then proses dihentikan, `tool/result` berstatus `error` dengan output `dibatalkan` ditulis, dan `turn/end` reason `cancelled` ditulis.
- `AC-LOOP-020`: Given session idle, when `Cancel` dipanggil, then tidak ada event yang ditulis dan hasilnya nil.
- `AC-LOOP-021`: Given ada pesan di inbox saat turn dibatalkan, when turn berakhir, then pesan itu tetap di inbox dan dijawab oleh turn berikutnya.

#### REQ-LOOP-008: Retry dan fallback provider

Sistem harus mengulang request ke target yang sama sampai 3 kali untuk error class `rate_limit`,
`server`, dan `network`, dengan jeda 1 detik, 2 detik, dan 4 detik. Jika target masih gagal,
sistem pindah ke fallback berikutnya dengan aturan yang sama. Error class lain tidak di-retry dan
tidak pindah ke fallback.

- `AC-LOOP-022`: Given primary membalas 429 dua kali lalu sukses, when step berjalan, then dua `assistant/attempt` gagal tercatat, jeda 1 detik lalu 2 detik terjadi, dan step selesai dengan `step/end`.
- `AC-LOOP-023`: Given primary gagal dengan 5xx empat kali dan fallback pertama sukses, when step berjalan, then `step/start` dan `step/end` mencatat provider dan model fallback.
- `AC-LOOP-024`: Given semua target gagal, when fallback habis, then `turn/end` reason `error` ditulis dengan error class terakhir, dan setiap percobaan tercatat sebagai `assistant/attempt`.
- `AC-LOOP-025`: Given provider membalas error class `auth`, `client`, atau `context_length`, when terjadi, then tidak ada retry, tidak ada fallback, dan `turn/end` reason `error` ditulis.

#### REQ-LOOP-009: Frame hidup

Sistem harus mengirim setiap text delta ke subscriber `SubscribeLive` sebelum turn selesai. Frame
hidup tidak ditulis ke log. Subscriber yang lambat tidak boleh menahan turn.

- `AC-LOOP-026`: Given subscriber terdaftar dan provider mengirim tiga delta, when stream berjalan, then subscriber menerima tiga frame `text_delta` berurutan sebelum `turn/end`.
- `AC-LOOP-027`: Given buffer subscriber penuh, when frame baru dikirim, then turn tetap berjalan, frame itu dibuang, dan peringatan dicatat di log.

#### REQ-LOOP-010: Agent, model, dan workspace per session

Sistem harus memakai agent dan model dari `model/selection` terakhir di log, lalu dari `agent`
di `session/created`. `SetAgent` dan `SetModel` berlaku mulai step berikutnya. Workspace diambil
dari `workspace` di `session/created`, lalu `workspace` agent, lalu cwd saat runtime dibuat.

- `AC-LOOP-028`: Given session idle, when `SetAgent` memilih agent `asisten`, then turn berikutnya memakai agent `asisten` dan `model/selection` berisi `agent`, `provider`, dan `model` agent baru.
- `AC-LOOP-029`: Given turn berjalan, when `SetModel` dipanggil, then step yang sedang berjalan tidak berubah, step berikutnya memakai provider dan model baru, dan `model/selection` ditulis.
- `AC-LOOP-030`: Given `SetModel` dengan provider yang tidak terdaftar, when dipanggil, then error menyebut nama provider, tidak ada event yang ditulis, dan model tidak berubah.
- `AC-LOOP-031`: Given `session/created` memuat `workspace` `W1` dan agent punya `workspace` `W2`, when tool file dipanggil, then workspace yang dipakai adalah `W1`.

### Provider

#### REQ-PROV-001: Konfigurasi provider `openai_compat`

Sistem harus mendaftarkan setiap `[providers.<name>]` dengan `kind = "openai_compat"` sebagai
provider. Provider memakai `base_url`, `api_key_env` (opsional), `headers`, dan `timeout`. Kind
lain ditolak pada build ini.

- `AC-PROV-001`: Given `[providers.openrouter]` dengan `kind = "openai_compat"` dan `base_url` valid, when config dimuat, then provider `openrouter` terdaftar dan request dikirim ke `base_url` ditambah `/chat/completions`.
- `AC-PROV-002`: Given `api_key_env` kosong, when request dikirim, then header `Authorization` tidak dikirim.
- `AC-PROV-003`: Given `kind = "anthropic"`, when boot, then boot gagal dan pesan menyebut nama provider dan bahwa kind itu belum didukung pada build ini.
- `AC-PROV-004`: Given provider dipakai agent (primary atau fallback) dan env `api_key_env` tidak terisi, when boot, then boot gagal dengan error class `missing_secret` yang menyebut nama env dan provider.

#### REQ-PROV-002: Streaming chat completions

Sistem harus membaca stream SSE dari `chat/completions` dan menghasilkan event `text_delta`,
`tool_use`, `usage`, dan `stop`.

- `AC-PROV-005`: Given stream dengan tiga chunk `delta.content`, when dibaca, then tiga event `text_delta` terbit berurutan sebelum `stop`.
- `AC-PROV-006`: Given `tool_calls` yang argumen JSON-nya terpecah di tiga chunk, when stream selesai, then satu event `tool_use` dengan input JSON utuh terbit.
- `AC-PROV-007`: Given chunk usage terakhir berisi token input dan output, when stream selesai, then event `usage` membawa kedua nilai itu.

#### REQ-PROV-003: Konversi pesan

Sistem harus mengonversi `core.Message` (bentuk Anthropic Messages API) ke request OpenAI dan
mengonversi jawaban OpenAI ke `core.Message`.

- `AC-PROV-008`: Given riwayat berisi system, user, assistant dengan tool_use, dan tool_result, when dikonversi, then request memakai role `system`, `assistant` dengan `tool_calls`, dan `tool` dengan `tool_call_id` yang sama.
- `AC-PROV-009`: Given `ContentBlock` bertipe Image dalam riwayat, when dikonversi, then konversi gagal dengan `ErrUnsupported` dan request tidak dikirim.

#### REQ-PROV-004: Klasifikasi error provider

Sistem harus memetakan kegagalan provider ke error class tetap. Retry, fallback, dan exit code
memakai class ini.

- `AC-PROV-010`: Given HTTP 401 atau 403, when diklasifikasi, then class `auth`.
- `AC-PROV-011`: Given HTTP 429, HTTP 5xx, atau koneksi gagal dan timeout, when diklasifikasi, then class berturut-turut `rate_limit`, `server`, dan `network`.
- `AC-PROV-012`: Given HTTP 400 dengan `error.code` `context_length_exceeded`, when diklasifikasi, then class `context_length`. HTTP 4xx lain menjadi class `client`.

### Mock provider

#### REQ-MOCK-001: MockLlm

Sistem harus menyediakan provider palsu `MockLlm` untuk test. Setiap panggilan `Stream` memutar
respons skrip berikutnya.

- `AC-MOCK-001`: Given skrip berisi delta, tool_use, delay, dan error, when `Stream` dipanggil berulang, then setiap panggilan memakai respons berikutnya dalam urutan, delay dijalankan, dan error dikembalikan dengan class yang ditulis di skrip.
- `AC-MOCK-002`: Given skrip sudah habis, when `Stream` dipanggil, then error berisi teks `skrip habis` dikembalikan.

### Tool bawaan

#### REQ-TOOL-001: `read_file`

Sistem harus membaca file teks di dalam workspace.

- `AC-TOOL-001`: Given file teks 3000 baris di workspace, when `read_file` dipanggil tanpa `offset` dan `limit`, then 2000 baris pertama dikembalikan dengan status `success`. Given `offset = 10` dan `limit = 5`, then baris 10 sampai 14 dikembalikan.
- `AC-TOOL-002`: Given file biner (ada byte NUL dalam 8000 byte pertama), when `read_file` dipanggil, then status `error` dengan pesan `file biner`.

#### REQ-TOOL-002: `write_file`

Sistem harus membuat atau mengganti isi file teks di dalam workspace.

- `AC-TOOL-003`: Given path baru dan direktori induknya ada di workspace, when `write_file` dipanggil, then file dibuat dengan isi `content` dan output menyebut jumlah byte.
- `AC-TOOL-004`: Given file sudah ada, when `write_file` dipanggil, then isi lama diganti seluruhnya.
- `AC-TOOL-005`: Given direktori induk tidak ada, when `write_file` dipanggil, then status `error` dan tidak ada file yang dibuat.

#### REQ-TOOL-003: `edit_file`

Sistem harus mengganti teks persis (exact string replace) dalam satu file dan hanya boleh mengganti
kemunculan yang tepat satu.

- `AC-TOOL-006`: Given `old_string` muncul tepat sekali, when `edit_file` dipanggil, then hanya kemunculan itu diganti dan status `success`.
- `AC-TOOL-007`: Given `old_string` muncul nol kali atau lebih dari sekali, when `edit_file` dipanggil, then status `error` dengan jumlah kemunculan dan file tidak berubah.
- `AC-TOOL-008`: Given `old_string` kosong, when `edit_file` dipanggil, then status `error` dengan pesan input tidak valid.

#### REQ-TOOL-004: `glob`

Sistem harus mencari path dengan pola glob (mendukung `**`) di dalam workspace dan menghormati `.gitignore`.

- `AC-TOOL-009`: Given pola `**/*.go` dan sebagian file cocok dengan `.gitignore`, when `glob` dipanggil, then hasil berupa path relatif terhadap workspace yang terurut, dan file yang di-ignore tidak muncul.
- `AC-TOOL-010`: Given lebih dari 1000 file cocok, when `glob` dipanggil, then 1000 path pertama dikembalikan dan output mencatat jumlah total.

#### REQ-TOOL-005: `grep`

Sistem harus mencari teks dengan regex RE2 di dalam workspace dan menghormati `.gitignore`.

- `AC-TOOL-011`: Given pola RE2 valid dan ada kecocokan, when `grep` dipanggil, then setiap kecocokan tampil sebagai `path:baris: teks` dengan batas 200 kecocokan.
- `AC-TOOL-012`: Given pola bukan RE2 yang valid, when `grep` dipanggil, then status `error` dengan pesan pola tidak valid.
- `AC-TOOL-013`: Given file biner atau file lebih dari 1 MiB, when `grep` dipanggil, then file itu dilewati.

#### REQ-TOOL-006: `bash`

Sistem harus menjalankan perintah shell dengan cwd di workspace, batas waktu, dan batas output.

- `AC-TOOL-014`: Given `command` `pwd`, when `bash` dipanggil, then perintah berjalan dengan cwd workspace dan stdout serta stderr dikembalikan.
- `AC-TOOL-015`: Given perintah belum selesai setelah `timeout_secs` (default 120), when waktu habis, then grup proses dihentikan dan status `error` dengan pesan timeout dikembalikan.
- `AC-TOOL-016`: Given `timeout_secs` lebih dari 600, when `bash` dipanggil, then status `error` dengan pesan batas 600 detik dan perintah tidak dijalankan.
- `AC-TOOL-017`: Given perintah keluar dengan kode bukan nol, when selesai, then status `error` dan output memuat kode keluar. Given output lebih dari 30000 karakter, then output dipotong dan diberi catatan jumlah karakter asli.

#### REQ-TOOL-007: `web_fetch`

Sistem harus mengambil URL dan mengembalikan isinya sebagai teks, dengan batas ukuran dan waktu.

- `AC-TOOL-018`: Given URL `https` yang mengembalikan HTML, when `web_fetch` dipanggil, then output berupa markdown tanpa elemen `script` dan `style`, dan dipotong sesuai batas output 30000 karakter.
- `AC-TOOL-019`: Given content-type `text/plain` atau `application/json`, when `web_fetch` dipanggil, then isi dikembalikan apa adanya. Given content-type lain (misalnya gambar atau PDF), then status `error`.
- `AC-TOOL-020`: Given URL bukan `http` atau `https`, when `web_fetch` dipanggil, then status `error` tanpa request jaringan. Given respons lebih dari 5 MiB atau belum selesai dalam 30 detik, then status `error`.

#### REQ-TOOL-008: Batas workspace untuk tool file

Sistem harus menolak path yang setelah resolusi symlink berada di luar workspace.

- `AC-TOOL-021`: Given path absolut atau relatif yang setelah `EvalSymlinks` berada di luar workspace, when `read_file`, `write_file`, `edit_file`, `glob`, atau `grep` dipanggil, then status `rejected` dan file tidak disentuh.
- `AC-TOOL-022`: Given symlink di dalam workspace yang menunjuk ke luar workspace, when `read_file` dipanggil ke symlink itu, then status `rejected`.

#### REQ-TOOL-009: Gerbang default `tools.pre_execute`

Sistem harus menolak tool selain `Read` jika tidak ada listener `tools.pre_execute` yang
mengizinkannya (fail-closed). Listener yang menolak menghentikan rantai dan tool tidak berjalan.

- `AC-TOOL-023`: Given tidak ada listener `tools.pre_execute`, when tool `Read` dipanggil, then tool berjalan. When tool `Write`, `Exec`, atau `Network` dipanggil, then status `rejected` dengan alasan `default fail-closed` dan tool tidak berjalan.
- `AC-TOOL-024`: Given listener `tools.pre_execute` mengembalikan deny, when tool dipanggil, then handler default tidak berjalan dan status `rejected` dengan alasan dari listener.

### Session di memori

#### REQ-MEM-001: `memstore`

Sistem harus menyediakan `core.SessionStore` di memori untuk test dan library tanpa SQLite. Data
hanya hidup selama proses berjalan.

- `AC-MEM-001`: Given session baru dan dua event ditambahkan, when `Events(from seq)` dipanggil, then `seq` mulai dari 1 dan naik 1, serta urutannya sama dengan urutan `Append`.
- `AC-MEM-002`: Given memstore, when `Fork` dipanggil, then error `ErrNotSupported` dikembalikan. When `TryLease` dipanggil, then lease selalu berhasil di proses yang sama.
- `AC-MEM-003`: Given runtime library ditutup, when runtime baru dibuat, then session lama tidak ditemukan.

### Go library

#### REQ-LIB-001: Boot library `lana.New`

Sistem harus membangun runtime dari `Options` dengan profile, layer config di memori, tool kustom,
dan store opsional.

- `AC-LIB-001`: Given `lana.New` dengan profile `minimal`, layer config di memori, dan `Home` kosong, when dipanggil, then runtime siap, tidak ada file config yang dibaca dari disk, dan modul `store`, `llm`, `tools`, dan `agent` terpasang.
- `AC-LIB-002`: Given `Options.Tools` berisi tool bernama `read_file`, when `lana.New` dipanggil, then error menyebut `read_file` sebagai nama yang bentrok.
- `AC-LIB-003`: Given agent di config memakai tool yang tidak terdaftar, when `lana.New` dipanggil, then boot gagal dan error menyebut nama agent dan nama tool.

#### REQ-LIB-002: `rt.Run`

Sistem harus menjalankan satu turn dan mengembalikan hasil final.

- `AC-LIB-004`: Given agent `asisten` dan prompt `halo`, when `rt.Run` selesai dengan jawaban, then `Result` berisi `Text` jawaban final, `SessionID`, `StopReason` `end_turn`, dan `Usage`, dan error nil.
- `AC-LIB-005`: Given session sedang menjalankan turn, when `rt.Run` atau `Start` dipanggil untuk session itu, then error `ErrSessionBusy` dikembalikan dan pesan tidak masuk inbox.
- `AC-LIB-006`: Given turn berakhir dengan reason `max_steps` atau `cancelled`, when `rt.Run` selesai, then `StopReason` sesuai reason itu dan error nil.
- `AC-LIB-007`: Given provider gagal setelah retry dan fallback habis, when `rt.Run` selesai, then `StopReason` `error` dan error bertipe `*lana.ProviderError` dengan class dikembalikan.
- `AC-LIB-008`: Given `Request.Prompt` kosong, when `rt.Run` dipanggil, then error `ErrEmptyPrompt` dikembalikan dan tidak ada session dibuat.

#### REQ-LIB-003: `rt.Stream`

Sistem harus mengirim event turn sebagai iterator agar service bisa menampilkan progres.

- `AC-LIB-009`: Given turn dengan satu tool call, when iterator dibaca, then event `Session`, `TextDelta`, `ToolCall`, `ToolResult`, dan `Done` (berisi `Result`) terbit berurutan, lalu iterasi selesai.
- `AC-LIB-010`: Given context `Stream` dibatalkan saat turn berjalan, when pembatalan terjadi, then turn berakhir dengan reason `cancelled` dan event `Done` tetap terbit dengan `StopReason` `cancelled`.

#### REQ-LIB-004: `rt.Close`

Sistem harus menutup runtime dengan menghentikan turn yang berjalan dalam batas waktu shutdown.

- `AC-LIB-011`: Given turn berjalan, when `rt.Close` dipanggil, then turn dibatalkan, `Close` kembali setelah `turn/end` tercatat atau setelah 30 detik, dan `Run` sesudahnya gagal dengan `ErrClosed`.

#### REQ-LIB-005: Tool kustom service

Sistem harus menjalankan tool milik service lewat pipeline yang sama dengan tool bawaan.

- `AC-LIB-012`: Given tool kustom `lookup_order` di `Options.Tools` dan agent mencantumkannya, when model memanggilnya, then tool melewati `tools.pre_execute`, `tools.execute`, dan `tools.post_execute`, dan hasilnya tercatat sebagai `tool/result`.

#### REQ-LIB-006: Opsi approval library

Sistem harus menyediakan dua opsi awal: `PolicyNever` (default) dan `PolicyTrust`. Keduanya
memakai gerbang default atau listener allow-all di `tools.pre_execute`.

- `AC-LIB-013`: Given `Approval` tidak diisi atau bernilai `PolicyNever`, when model memanggil `write_file`, then status `rejected`. When model memanggil `read_file`, then tool berjalan.
- `AC-LIB-014`: Given `Approval: PolicyTrust`, when model memanggil `bash` atau `write_file`, then listener allow-all mengizinkan dan tool berjalan.

#### REQ-LIB-007: Routing dan channel library

Sistem harus memilih agent dari route `library` jika `Request.Agent` kosong dan mencatat channel
`library` pada session baru.

- `AC-LIB-015`: Given `Request.Agent` kosong dan route pertama dengan `channel = "library"` menunjuk `asisten`, when `rt.Run` dipanggil, then agent `asisten` dipakai. Given tidak ada route `library`, then error `ErrNoRoute` dikembalikan.
- `AC-LIB-016`: Given session baru dibuat oleh `rt.Run`, when `session/created` ditulis, then field `channel` bernilai `library`.

#### REQ-LIB-008: Profile bawaan `minimal` dan `headless`

Sistem harus menyediakan profile bawaan `minimal` dan `headless` sebagai daftar baris modul yang
memakai modul spec ini.

- `AC-LIB-017`: Given profile `minimal` atau `headless`, when config efektif dibaca, then baris modul base yang dipakai spec ini (`store`, `llm`, `tools`, `agent`) sama dengan definisi di contract (`API-LIB-005`) dan `API-CONF-009` spec #2.

## Matriks state dan operasi

| State saat ini | Operasi | Hasil | Boleh |
|---|---|---|---:|
| Idle | `Send` berisi teks | Turn baru dimulai | Ya |
| Idle | `Start` (library) | Turn baru dimulai, `turn_id` dikembalikan | Ya |
| Idle | `Cancel` | Tidak ada event, hasil nil | Ya |
| Idle | `SetAgent` atau `SetModel` | `model/selection` ditulis, berlaku di turn berikutnya | Ya |
| Turn berjalan | `Send` | Pesan masuk inbox | Ya |
| Turn berjalan | `Start` (library) | Error `ErrSessionBusy` | Tidak |
| Turn berjalan | `Cancel` | Turn berakhir `cancelled`; inbox tidak berubah | Ya |
| Turn berjalan | `SetAgent` atau `SetModel` | Berlaku mulai step berikutnya | Ya |
| Turn sedang menutup | `Send` | Pesan dijawab turn berikutnya (`AC-LOOP-006`) | Ya |
| Runtime ditutup | `Send`, `Start`, `Run`, `Stream` | Error `ErrClosed` | Tidak |

## Kasus gagal

| Kondisi | Perilaku yang diharapkan | Kode hasil |
|---|---|---|
| Provider `auth` (401, 403) | Tidak retry, tidak fallback, turn berakhir `error` | Error class `auth`, exit 3 di CLI |
| Provider `rate_limit` atau `server` atau `network` sampai habis | Retry 3 kali per target, lalu fallback, lalu `error` | Error class terakhir |
| Provider `client` atau `context_length` | Tidak retry, turn berakhir `error` | Error class `client` atau `context_length` |
| Tool ditolak gerbang default | Status `rejected` ke model, turn tetap jalan | `tool/result` status `rejected` |
| Tool gagal (timeout, pola tidak valid, file biner) | Status `error` ke model | `tool/result` status `error` |
| Path di luar workspace | Status `rejected` | `tool/result` status `rejected` |
| Model memanggil tool yang tidak diaktifkan | Status `error`, tool tidak berjalan | `tool/result` status `error` |
| `SetModel` ke provider tidak terdaftar | Error dengan nama provider, model tidak berubah | Error Go |
| Kind provider `anthropic` pada build ini | Boot gagal | Error boot |
| Env `api_key_env` kosong untuk provider yang dipakai | Boot gagal | Error class `missing_secret` |
| Tool kustom bentrok nama dengan tool bawaan | `lana.New` gagal | Error boot |
| Ukuran body `web_fetch` lebih dari 5 MiB | Status `error` | `tool/result` status `error` |

## Otorisasi dan keamanan

- Tool selain `Read` (`write_file`, `edit_file`, `bash`, `web_fetch`) hanya jalan jika ada listener
  `tools.pre_execute` yang mengizinkan (`REQ-TOOL-009`). Spec ini hanya menyediakan
  `PolicyTrust` sebagai listener izin penuh.
- Tool file tidak bisa membaca atau menulis di luar workspace setelah resolusi symlink (`REQ-TOOL-008`).
- `bash` tidak memakai sandbox. Perintah berjalan sebagai user yang menjalankan `lana` (ADR 0004).
- `web_fetch` hanya menerima `http` dan `https`. Isi respons diperlakukan sebagai data, bukan instruksi (ADR 0006).
- Secret (`core.Secret`) tidak dicetak ke event, log, output tool, atau error (spec #2 `REQ-CONF-005`).

## Integritas data dan konkurensi

- Setiap `tool/call` punya tepat satu `tool/result` (`AC-LOOP-010`).
- Event satu session ditulis berurutan. Tidak ada dua turn aktif di session yang sama (`AC-LOOP-004`).
- Pesan di inbox tidak hilang saat turn berakhir, dibatalkan, atau ditutup (`AC-LOOP-006`, `AC-LOOP-021`).
- Frame hidup bersifat sementara. Jawaban final selalu ada di `assistant/message` (ADR 0009).
- Pembatalan tidak meninggalkan proses tool yang berjalan (`AC-LOOP-019`).

## Aksesibilitas

Tidak berlaku. Spec ini tidak punya antarmuka pengguna. Tampilan frame hidup dan output CLI diatur
di spec #1.

## Skenario

### SC-001: Jawaban satu turn tanpa tool

**Given** session `S` idle dengan agent `asisten` dan provider mengembalikan jawaban teks
**When** pesan `halo` dikirim ke `S`
**Then** log berisi `turn/start`, `user/message`, `step/start`, `assistant/message`, `step/end`, dan `turn/end` reason `end_turn`

### SC-002: Tool ditolak gerbang lalu model melanjutkan

**Given** agent `coder` tanpa listener izin dan model memanggil `bash` dan `read_file` dalam satu step
**When** step pertama selesai
**Then** `bash` menghasilkan `tool/result` status `rejected`, `read_file` berjalan dan menghasilkan `tool/result` status `success`, dan step berikutnya dimulai dengan kedua hasil

### SC-003: Pesan masuk saat turn berjalan

**Given** turn `T1` sedang berjalan di session `S` dan model memanggil `read_file`
**When** pengguna mengirim pesan `ringkas hasilnya`
**Then** pesan masuk inbox, di-claim di awal step berikutnya, dan tercatat sebagai `user/message` di step itu

### SC-004: Provider kena rate limit lalu fallback

**Given** primary `openai:gpt-5-mini` membalas 429 empat kali dan fallback `openrouter:openai/gpt-5-mini` sukses
**When** step berjalan
**Then** log berisi empat `assistant/attempt` dari primary, `step/start` berikutnya mencatat provider `openrouter`, dan turn selesai `end_turn`

### SC-005: Library dibatalkan lewat context

**Given** service Go memanggil `rt.Stream` dengan context yang nanti dibatalkan saat `bash` berjalan
**When** context dibatalkan
**Then** proses `bash` berhenti, `tool/result` status `error`, dan event `Done` terbit dengan `StopReason` `cancelled`

## Resolusi pertanyaan

Pemilik melepas gate approval pada 2026-10-29. Untuk setiap pertanyaan terbuka, agent memilih opsi
paling sederhana yang direkomendasikan dan mencatatnya di sini.

| ID | Pertanyaan | Mengendalikan | Referensi yang diminta | Pemilik | Status | Resolusi |
|---|---|---|---|---|---|---|
| `Q-001` | Opsi approval library: apakah `ApproveWith` ikut spec ini | `API-LIB-001`, `REQ-LIB-006` | Tidak diminta (gate dilepas) | Ridho | Resolved | Rekomendasi agent: opsi `PolicyNever` (default) dan `PolicyTrust` saja. `lana.ApproveWith` ditetapkan spec #4 (`REQ-APPR-007`, `AC-APPR-017`, `API-APPR-005`) karena butuh `approval.request`. Direvisi: lihat `Q-030`. |
| `Q-002` | Batas step per agent dan nama config | `REQ-LOOP-005`, `API-AGENT-007` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: key `agents.<nama>.max_steps` opsional, bilangan bulat 1 sampai 200, default 25. |
| `Q-003` | Aturan retry dan backoff | `REQ-LOOP-008` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: 3 retry per target dengan jeda 1 s, 2 s, 4 s. Setiap target (primary dan tiap fallback) memakai aturan yang sama. |
| `Q-004` | Error jaringan (koneksi putus, timeout) | `REQ-PROV-004`, `REQ-LOOP-008` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: class `network` diperlakukan seperti `server` (retry lalu fallback). Permintaan pemilik menyebut 429 dan 5xx; agent menambah `network` karena gagal sementara yang sama. |
| `Q-005` | Context terlalu panjang | `REQ-LOOP-008`, `REQ-PROV-004` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: tanpa modul compaction, turn berakhir `error` dengan class `context_length`. Dengan modul compaction (spec #4), listener `llm.stream` meringkas lalu mengulang sekali (`AC-COMP-006`, `AC-COMP-007`). Kedua perilaku berlaku sesuai `Q-027`. |
| `Q-006` | Gagal di tengah stream setelah ada delta | `REQ-LOOP-008` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: tidak di-retry, karena delta sudah tampil. Percobaan dicatat sebagai `assistant/attempt` dan turn berakhir `error`. |
| `Q-007` | Pesan antre saat `Cancel` | `REQ-LOOP-007` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: pesan tetap di inbox dan dijawab turn berikutnya. Tidak ada pesan yang dibuang diam-diam. |
| `Q-008` | Pesan ditolak `agent.pre_step` di step selain pertama | `REQ-LOOP-001` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: sama seperti step pertama. Pesan tidak ditulis ke log, turn berakhir `end_turn`, dan alasan ditulis ke log aplikasi. |
| `Q-009` | Paralelisme tool `Read` | `REQ-LOOP-004` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: paling banyak 8 tool `Read` berjalan bersamaan dalam satu batch. |
| `Q-010` | Batas output dan hasil tool | `REQ-TOOL-001`, `REQ-TOOL-004`, `REQ-TOOL-005`, `REQ-TOOL-006`, `REQ-TOOL-007` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: output teks 30000 karakter (`bash`, `read_file`, `web_fetch`). `glob` maksimal 1000 hasil. `grep` maksimal 200 kecocokan dan melewati file lebih dari 1 MiB atau biner. Sisa dipotong dengan catatan. |
| `Q-011` | Perilaku `bash` untuk exit code dan timeout | `REQ-TOOL-006` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: exit code bukan nol berstatus `error` dengan kode di output. `timeout_secs` di luar 1 sampai 600 ditolak sebelum eksekusi. Default 120. |
| `Q-012` | Bentuk input `read_file`, `write_file`, `edit_file` | `REQ-TOOL-001` sampai `REQ-TOOL-003` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: `read_file` memakai `offset` (1-based) dan `limit` (1 sampai 2000, default 2000 baris). `write_file` menimpa file dan tidak membuat direktori induk. `edit_file` tidak punya `replace_all`. |
| `Q-013` | Aturan `.gitignore` untuk `glob` dan `grep` | `REQ-TOOL-004`, `REQ-TOOL-005` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: hanya `.gitignore` di root workspace, dan direktori `.git` selalu dilewati. |
| `Q-014` | Batas `web_fetch` | `REQ-TOOL-007` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: timeout 30 detik, body maksimal 5 MiB (lebih = error), maksimal 5 redirect, hanya `http` dan `https`, HTML diubah ke markdown, tipe lain selain teks atau JSON ditolak. |
| `Q-015` | Isi `Result.Text` dan error `rt.Run` | `REQ-LIB-002` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: `Text` adalah teks blok `text` dari `assistant/message` terakhir di turn. Error hanya dikembalikan jika `StopReason` `error`. `max_steps` dan `cancelled` tidak menghasilkan error. |
| `Q-016` | Profile default dan arti `Home` kosong pada library | `REQ-LIB-001` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: profile default `minimal`. `Home` kosong berarti tidak ada file config yang dibaca dan tidak ada fallback ke `~/.lana`. |
| `Q-017` | Sumber workspace | `REQ-LOOP-010`, `REQ-TOOL-008` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: `session/created.workspace`, lalu `agents.<nama>.workspace`, lalu cwd saat `New`. Setiap path dinormalisasi dengan `filepath.EvalSymlinks`. |
| `Q-018` | Library memanggil session yang sedang berjalan | `REQ-LIB-002` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: `Run` dan `Stream` menolak dengan `ErrSessionBusy`. Pengiriman antre hanya lewat `Send` (jalur CLI). |
| `Q-019` | Subscriber frame hidup yang lambat | `REQ-LOOP-009` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: buffer 1024 frame per subscriber. Frame yang tidak muat dibuang dengan peringatan di log. Jawaban final tetap di log. |
| `Q-020` | Provider dipakai agent tanpa env key | `REQ-PROV-004`, `REQ-PROV-001` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: dicek saat boot untuk provider primary dan fallback yang dipakai agent. Gagal dengan class `missing_secret`. Provider yang tidak dipakai agent tidak dicek. |
| `Q-021` | Kind `anthropic` pada build ini | `REQ-PROV-001` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: ditolak saat boot sampai M4. Konflik dengan spec #1 (`lana init` menawarkan Anthropic, `SC-003` spec #1) perlu diselesaikan di spec #1. |
| `Q-022` | Isi `model/selection` saat `SetAgent` | `REQ-LOOP-010` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: payload `model/selection` mendapat field tambahan `agent`. Field itu hanya ada saat `SetAgent`. Disetujui spec #4 dan masuk `API-CORE-008` (`Q-032`). |
| `Q-023` | Block gambar dalam riwayat | `REQ-PROV-003` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: tidak didukung di spec ini. Konversi gagal dengan `ErrUnsupported`. Input `Send` hanya teks. |
| `Q-024` | Siapa yang membuat riwayat (`derive`) | `REQ-LOOP-003` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: spec ini membuat derive minimal (user, assistant, tool result) sebagai subset `core.DeriveMessages`. Compaction dan `inherited_seq` untuk fork dimasukkan spec #4. Direvisi: lihat `Q-028`. |
| `Q-025` | Status tool di luar workspace | `REQ-TOOL-008` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: `rejected`, sejalan dengan `AC-CHAT-022` di spec #1. |
| `Q-026` | Profile `headless` sebelum runner | `REQ-LIB-008` | Tidak diminta | Ridho | Resolved | Rekomendasi agent: `headless` = base plus `cli_runner` (spec #1). Modul permission dan approval berasal dari base (spec #2 `CQ-031`). Direvisi: lihat `Q-029`. |
| `Q-027` | Tipe error konteks terlalu panjang | `REQ-LOOP-008`, `REQ-PROV-004` | Tidak diminta | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): adapter membuat `*core.ProviderError` dengan class `context_length` yang cocok dengan `core.ErrContextTooLong` (`CQ-026` spec #2). Turn berakhir `error` tanpa compaction. Dengan compaction spec #4, retry sekali. Sesuai `Q-005`. |
| `Q-028` | Lokasi fungsi derive riwayat | `REQ-LOOP-003`, `REQ-STORE-011` | Tidak diminta | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `core.DeriveMessages` di `core` (`CQ-028` spec #2). Spec ini membuat subset non-compaction. `system/message` tidak masuk riwayat. Pesan itu dipakai sebagai `ChatRequest.System`. |
| `Q-029` | Profile dan library sebelum spec #4 dan #1 | `REQ-LIB-008`, `REQ-LIB-001` | Tidak diminta | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): profile bawaan dari spec #2. `minimal` = base. `headless` = base plus `cli_runner`. Library tanpa `Home` memakai `memstore`. Sebelum spec #4 dan #1 selesai, tes memakai `Layers` tanpa profile bawaan (`DQ-011`). |
| `Q-030` | Opsi approval library `ApproveWith` | `REQ-LIB-006` | Tidak diminta | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `ApproveWith` ditetapkan spec #4 (`REQ-APPR-007`, `AC-APPR-017`). Spec ini menyediakan tipe `Option` dan `PolicyNever`/`PolicyTrust`. Revisi `Q-001`. |
| `Q-031` | Kind `anthropic` dan konflik dengan spec #1 | `REQ-PROV-001` | Tidak diminta | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): konflik `Q-021` selesai di spec #1 (`REQ-INIT-001` revisi 6). `lana init` hanya menawarkan kind yang didukung build. `anthropic` tetap ditolak di boot sampai M4. |
| `Q-032` | Payload `session/created.workspace` dan `model/selection.agent` | `REQ-LOOP-010` | Tidak diminta | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): disetujui spec #4. Kedua field masuk `API-CORE-008` (spec #2, `CQ-029`). |

## Cakupan requirement

| Requirement | Acceptance criteria | Sumber |
|---|---|---|
| `REQ-LOOP-001` | `AC-LOOP-001`, `AC-LOOP-002`, `AC-LOOP-003` | ADR 0002, ADR 0003 |
| `REQ-LOOP-002` | `AC-LOOP-004`, `AC-LOOP-005`, `AC-LOOP-006` | ADR 0002, ADR 0003, `Q-007` |
| `REQ-LOOP-003` | `AC-LOOP-007`, `AC-LOOP-008`, `AC-LOOP-009` | ADR 0002, ADR 0009, `Q-024` |
| `REQ-LOOP-004` | `AC-LOOP-010`, `AC-LOOP-011`, `AC-LOOP-012`, `AC-LOOP-013`, `AC-LOOP-014` | ADR 0002, ADR 0006, `Q-009` |
| `REQ-LOOP-005` | `AC-LOOP-015`, `AC-LOOP-016` | ADR 0002, `Q-002` |
| `REQ-LOOP-006` | `AC-LOOP-017`, `AC-LOOP-018` | ADR 0002, Diagram 0002 |
| `REQ-LOOP-007` | `AC-LOOP-019`, `AC-LOOP-020`, `AC-LOOP-021` | Spec #1 `AC-CHAT-016`, `Q-007` |
| `REQ-LOOP-008` | `AC-LOOP-022`, `AC-LOOP-023`, `AC-LOOP-024`, `AC-LOOP-025` | Diagram 0002, `Q-003` sampai `Q-006` |
| `REQ-LOOP-009` | `AC-LOOP-026`, `AC-LOOP-027` | ADR 0009, Spec #1 `AC-CHAT-009`, `Q-019` |
| `REQ-LOOP-010` | `AC-LOOP-028`, `AC-LOOP-029`, `AC-LOOP-030`, `AC-LOOP-031` | ADR 0003, ADR 0009, `Q-017`, `Q-022` |
| `REQ-PROV-001` | `AC-PROV-001`, `AC-PROV-002`, `AC-PROV-003`, `AC-PROV-004` | ADR 0014, ADR 0013, `Q-020`, `Q-021` |
| `REQ-PROV-002` | `AC-PROV-005`, `AC-PROV-006`, `AC-PROV-007` | ADR 0013, Keputusan pemilik 2026-10-29 |
| `REQ-PROV-003` | `AC-PROV-008`, `AC-PROV-009` | ADR 0002 (bentuk Anthropic), `Q-023` |
| `REQ-PROV-004` | `AC-PROV-010`, `AC-PROV-011`, `AC-PROV-012` | Diagram 0002, `Q-004`, `Q-005` |
| `REQ-MOCK-001` | `AC-MOCK-001`, `AC-MOCK-002` | Keputusan pemilik 2026-10-29 |
| `REQ-TOOL-001` | `AC-TOOL-001`, `AC-TOOL-002` | Keputusan pemilik 2026-10-29, `Q-010`, `Q-012` |
| `REQ-TOOL-002` | `AC-TOOL-003`, `AC-TOOL-004`, `AC-TOOL-005` | Keputusan pemilik 2026-10-29, `Q-012` |
| `REQ-TOOL-003` | `AC-TOOL-006`, `AC-TOOL-007`, `AC-TOOL-008` | Keputusan pemilik 2026-10-29 (exact string replace) |
| `REQ-TOOL-004` | `AC-TOOL-009`, `AC-TOOL-010` | Keputusan pemilik 2026-10-29, `Q-010`, `Q-013` |
| `REQ-TOOL-005` | `AC-TOOL-011`, `AC-TOOL-012`, `AC-TOOL-013` | Keputusan pemilik 2026-10-29, `Q-010`, `Q-013` |
| `REQ-TOOL-006` | `AC-TOOL-014`, `AC-TOOL-015`, `AC-TOOL-016`, `AC-TOOL-017` | Keputusan pemilik 2026-10-29, `Q-010`, `Q-011` |
| `REQ-TOOL-007` | `AC-TOOL-018`, `AC-TOOL-019`, `AC-TOOL-020` | Keputusan pemilik 2026-10-29, `Q-014` |
| `REQ-TOOL-008` | `AC-TOOL-021`, `AC-TOOL-022` | ADR 0004, Spec #1 `AC-CHAT-022`, `Q-025` |
| `REQ-TOOL-009` | `AC-TOOL-023`, `AC-TOOL-024` | ADR 0004, ADR 0006, Keputusan pemilik 2026-10-29 |
| `REQ-MEM-001` | `AC-MEM-001`, `AC-MEM-002`, `AC-MEM-003` | Keputusan pemilik 2026-10-29, ADR 0009 |
| `REQ-LIB-001` | `AC-LIB-001`, `AC-LIB-002`, `AC-LIB-003` | ADR 0014, `Q-016` |
| `REQ-LIB-002` | `AC-LIB-004`, `AC-LIB-005`, `AC-LIB-006`, `AC-LIB-007`, `AC-LIB-008` | ADR 0014, `Q-015`, `Q-018` |
| `REQ-LIB-003` | `AC-LIB-009`, `AC-LIB-010` | ADR 0014 |
| `REQ-LIB-004` | `AC-LIB-011` | ADR 0014, spec #2 `REQ-MOD-003` (batas 30 detik) |
| `REQ-LIB-005` | `AC-LIB-012` | ADR 0014, Diagram 0005 |
| `REQ-LIB-006` | `AC-LIB-013`, `AC-LIB-014` | ADR 0004, ADR 0014, `Q-001` |
| `REQ-LIB-007` | `AC-LIB-015`, `AC-LIB-016` | ADR 0003, ADR 0014 |
| `REQ-LIB-008` | `AC-LIB-017` | ADR 0010, `Q-026` |

Setiap requirement dan acceptance criterion muncul tepat satu kali.

## Blocking Open Questions

None.

## Komitmen contract

| Requirement | Komitmen yang terlihat | Operasi contract |
|---|---|---|
| `REQ-LOOP-001`, `REQ-LOOP-002` | Urutan event turn, satu turn aktif per session, pesan antre | `API-AGENT-001` |
| `REQ-LOOP-002` | Start library menolak session berjalan | `API-AGENT-002` |
| `REQ-LOOP-007` | Pembatalan dan event `cancelled` | `API-AGENT-003` |
| `REQ-LOOP-009` | Frame `text_delta` per session | `API-AGENT-006` |
| `REQ-LOOP-010` | Agent, model, workspace per session | `API-AGENT-004`, `API-AGENT-005` |
| `REQ-LOOP-003`, `REQ-LOOP-004`, `REQ-LOOP-005`, `REQ-LOOP-008`, `REQ-LOOP-010` | Bentuk payload event di log dan key config agent | `API-AGENT-007`, `API-AGENT-008` |
| `REQ-TOOL-009` | Hook gerbang default dan urutan pipeline | `API-AGENT-009` |
| `REQ-PROV-001`, `REQ-PROV-004` | Key config provider dan error class | `API-PROV-001`, `API-PROV-003` |
| `REQ-PROV-002`, `REQ-PROV-003` | Wire format OpenAI | `API-PROV-002` |
| `REQ-MOCK-001` | Format skrip MockLlm | `API-PROV-004` |
| `REQ-TOOL-001` sampai `REQ-TOOL-008` | Nama, input, dan output tiap tool | `API-TOOL-001` sampai `API-TOOL-007` |
| `REQ-LIB-001` sampai `REQ-LIB-008` | Go API `lana`, error, event stream, profile | `API-LIB-001` sampai `API-LIB-005` |

## Approval

- [x] Scope dan pengecualian eksplisit.
- [x] Perilaku sukses dan gagal terukur.
- [x] Otorisasi eksplisit atau dinyatakan tidak berlaku.
- [x] Transisi state dan efek destruktif eksplisit.
- [x] Default, urutan, retry, dan idempotensi eksplisit.
- [x] Setiap requirement dan acceptance criterion punya ID stabil dan sumber.
- [x] Keberlakuan contract eksplisit.
- [x] Blocking Open Questions bernilai `None`.
- **Di-approve oleh**: Ridho (gate dilepas 2026-10-29)
- **Di-approve pada**: 2026-10-29 (revisi 1). Revisi 2 rekonsiliasi oleh agent atas nama pemilik (gate dilepas 2026-10-29).
