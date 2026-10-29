# Requirements: CLI dan TUI chat

- **Issue**: [`#1`](https://github.com/ridhomujizat/lana-harness/issues/1)
- **Status**: Approved
- **Revisi**: 4
- **Pemilik**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Diperlukan (subcommand, flag, exit code, format output, slash command, tombol)
- **ADR terkait**: [Index ADR](../../adr/README.md) — [0003](../../adr/0003-multi-agent-dengan-routing-channel.md), [0004](../../adr/0004-permission-mode-per-agent.md), [0008](../../adr/0008-interface-cli-channel-http-dan-web-dashboard.md), [0009](../../adr/0009-session-sebagai-event-log-append-only.md), [0010](../../adr/0010-profile-dan-config-patch.md)
- **Diagram terkait**: [Index diagram](../../diagram/README.md) — [0002](../../diagram/0002-agent-loop-dan-approval.md), [0004](../../diagram/0004-profile-fork-subagent-dan-protokol.md)

## Sumber

| Sumber | Referensi | Dipakai untuk |
|---|---|---|
| Issue | [`#1`](https://github.com/ridhomujizat/lana-harness/issues/1) | scope fitur |
| ADR 0008 | Interface CLI/TUI | streaming token, prompt approval `[y]es / [a]lways / [n]o`, feature `tui` |
| ADR 0003 | Multi-agent | route channel `cli`, `/agent`, inbox per session |
| ADR 0004 | Permission | policy session, outcome approval, timeout 10 menit, `--yolo` |
| ADR 0009 | Event log | session, fork, resume, compaction |
| ADR 0010 | Profile | `headless` untuk `lana run`, `minimal` untuk chat embedded, `--dump-config`, `config check` |
| Wawancara 2026-10-29 | Jawaban pemilik untuk `Q-001` sampai `Q-019` | semua keputusan di bagian Resolusi |
| Referensi pembanding | Claude Code | TUI inline, input multi-baris, `--continue` per direktori |

## Tujuan

Pemilik bisa memakai agent `lana` dari terminal: memasang konfigurasi awal, mengobrol secara
interaktif dengan jawaban streaming, melihat dan menyetujui tindakan berisiko, mengelola session,
dan menjalankan perintah one-shot dari script.

## Aktor

- **Pemilik** — satu-satunya user. Menjalankan `lana` sebagai user OS sendiri di mesin lokal.
- **Script** — program atau shell pipeline yang memanggil `lana run` tanpa interaksi.

## Definisi

- **Chat** — proses `lana chat` yang membaca input dari terminal sampai user keluar.
- **One-shot** — satu pemanggilan `lana run` yang menjalankan satu turn lalu keluar.
- **Mode embedded** — agent berjalan di dalam proses `lana chat` atau `lana run`, tanpa daemon.
- **TUI inline** — tampilan yang menulis riwayat chat ke scrollback terminal dan hanya menggambar
  ulang area input dan status bar di bagian bawah. Tidak memakai alternate screen.
- **Session CLI** — session dengan channel `cli` yang terikat ke satu direktori kerja (cwd).
- **Slash command** — input yang diawali `/` dan ditangani CLI tanpa request ke model.

## Scope

### Termasuk

- `lana chat`: TUI inline, streaming, tampilan tool call, approval, slash command, input multi-baris, riwayat input.
- Session: baru secara default, `--continue`, `--resume <id>`, `/new`, `/sessions`, `/fork`, `/compact`.
- `lana run`: one-shot dengan output `text`, `json`, `stream-json`, dan prompt dari stdin.
- `lana init`: wizard konfigurasi awal.
- `lana config check`, `lana config path`, `lana config edit`, dan flag global `--dump-config`.
- Exit code per kelas kegagalan.

### Tidak termasuk

- Mode attach ke daemon. Ditambahkan di spec daemon.
- Channel Telegram, WhatsApp, web dashboard, ACP, dan SDK.
- Subcommand `session`, `cron`, `db`, dan `service`.
- Implementasi agent loop, provider, permission engine, dan event log. Spec ini memakai komponen itu sebagai dependency.
- Tampilan alternate screen (layar penuh).

## Asumsi

- Agent, provider, dan route dibaca dari konfigurasi efektif profile. — Sumber: ADR 0010.
- Pesan yang dikirim saat turn berjalan masuk inbox dan di-claim di awal step berikutnya. — Sumber: ADR 0003.
- Policy session awal diambil dari `permission` agent. — Sumber: ADR 0004.
- Fork hanya boleh di batas turn. — Sumber: ADR 0009.

## Aturan perilaku

### Chat

#### REQ-CHAT-001: Memilih agent saat chat dimulai

Tanpa `--agent`, sistem harus memakai agent dari route pertama dengan `channel = "cli"`. Dengan
`--agent <nama>`, sistem harus memakai agent tersebut.

- `AC-CHAT-001`: Given route `cli` ke `coder`, when user menjalankan `lana chat`, then status bar menampilkan agent `coder`.
- `AC-CHAT-002`: Given agent `asisten` terdaftar, when user menjalankan `lana chat --agent asisten`, then session memakai `asisten`.
- `AC-CHAT-003`: Given agent `xyz` tidak terdaftar, when user menjalankan `lana chat --agent xyz`, then proses keluar dengan exit code 2 dan pesan yang menyebut `xyz`.
- `AC-CHAT-004`: Given tidak ada route `cli` dan tidak ada `--agent`, when user menjalankan `lana chat`, then proses keluar dengan exit code 2 dan pesan yang menyarankan `--agent` atau menambah route.

#### REQ-CHAT-002: Mode embedded

Sistem harus menjalankan agent di dalam proses `lana chat`, dan status bar harus menampilkan mode `embedded`.

- `AC-CHAT-005`: Given daemon tidak berjalan, when user menjalankan `lana chat`, then chat bisa dipakai dan status bar menampilkan `embedded`.

#### REQ-CHAT-003: TUI inline

Sistem harus menulis riwayat chat ke scrollback terminal dan hanya menggambar ulang area input dan
status bar di bawah. Status bar harus menampilkan agent, mode, policy session, dan jumlah token session.

- `AC-CHAT-006`: Given chat berjalan, when beberapa pesan selesai, then pesan tersebut bisa di-scroll dan di-copy dengan fitur native terminal.
- `AC-CHAT-007`: Given user keluar dari chat, when proses selesai, then riwayat chat tetap ada di scrollback terminal.
- `AC-CHAT-008`: Given chat berjalan, when status berubah (agent, policy, token), then status bar menampilkan nilai baru.

#### REQ-CHAT-004: Streaming jawaban

Sistem harus menampilkan teks jawaban model secara bertahap saat diterima.

- `AC-CHAT-009`: Given provider mengirim beberapa text delta, when turn berjalan, then setiap delta tampil sebelum turn selesai.

#### REQ-CHAT-005: Tool call ringkas dan mode verbose

Sistem harus menampilkan setiap tool call sebagai satu baris berisi nama tool, argumen utama, dan
status (berjalan, sukses, ditolak, error). User harus bisa menyalakan mode verbose: tool call
terakhir dicetak ulang lengkap, dan tool call berikutnya tampil dengan input dan output lengkap
sampai mode verbose dimatikan.

- `AC-CHAT-010`: Given model memanggil `bash` dengan `cargo test`, when tool selesai, then satu baris menampilkan `bash`, `cargo test`, dan status.
- `AC-CHAT-011`: Given tool call sudah tampil dan mode verbose mati, when user menyalakan mode verbose, then input dan output lengkap tool call terakhir dicetak ke scrollback.
- `AC-CHAT-024`: Given mode verbose menyala, when model memanggil tool lalu user mematikan mode verbose dan model memanggil tool lagi, then tool call pertama tampil lengkap dan tool call kedua tampil ringkas.

#### REQ-CHAT-006: Input saat turn berjalan

Sistem harus menerima input saat turn berjalan dan memasukkannya ke inbox session.

- `AC-CHAT-012`: Given turn berjalan, when user mengirim pesan, then pesan di-claim di awal step berikutnya dan tercatat sebagai `user/message`.

#### REQ-CHAT-007: Input multi-baris

`Enter` harus mengirim pesan. Sistem harus menyediakan baris baru lewat `Shift+Enter` (di terminal
yang mendukung keyboard protocol), `Alt+Enter`/`Option+Enter`, `Ctrl+J`, dan `\` di akhir baris
diikuti `Enter`. Paste multi-baris tidak boleh langsung mengirim pesan.

- `AC-CHAT-013`: Given input berisi teks, when user menekan `Alt+Enter`, then baris baru ditambahkan dan pesan belum terkirim.
- `AC-CHAT-014`: Given input berakhir dengan `\`, when user menekan `Enter`, then `\` diganti baris baru dan pesan belum terkirim.
- `AC-CHAT-015`: Given user mem-paste tiga baris, when paste selesai, then ketiga baris ada di input dan pesan belum terkirim.

#### REQ-CHAT-008: Batal dan keluar

Saat turn berjalan, `Ctrl+C` harus membatalkan turn. Saat idle, `Ctrl+C` harus mengosongkan input,
dan `Ctrl+C` dua kali dalam 2 detik harus keluar. `Ctrl+D` pada input kosong harus keluar.

- `AC-CHAT-016`: Given turn berjalan, when user menekan `Ctrl+C`, then turn berhenti dengan `turn/end` reason `cancelled` dan chat tetap terbuka.
- `AC-CHAT-017`: Given idle dengan input berisi teks, when user menekan `Ctrl+C`, then input kosong dan chat tetap terbuka.
- `AC-CHAT-018`: Given idle, when user menekan `Ctrl+C` dua kali dalam 2 detik, then proses keluar dengan exit code 0.
- `AC-CHAT-019`: Given input kosong, when user menekan `Ctrl+D`, then proses keluar dengan exit code 0.

#### REQ-CHAT-009: Riwayat input

Sistem harus menyimpan input yang dikirim ke `~/.lana/history` (maksimal 1000 entri, permission
`0600`). Panah atas dan bawah menelusuri riwayat. `Ctrl+R` mencari riwayat.

- `AC-CHAT-020`: Given user mengirim pesan lalu keluar, when chat baru dibuka dan user menekan panah atas, then pesan tadi muncul di input.
- `AC-CHAT-021`: Given riwayat berisi 1000 entri, when pesan baru dikirim, then entri tertua dihapus.

#### REQ-CHAT-010: Workspace dari direktori kerja

Untuk `lana chat` dan `lana run`, workspace session harus direktori kerja (cwd) saat proses
dijalankan, bukan `workspace` dari config agent.

- `AC-CHAT-022`: Given `lana chat` dijalankan di `~/coding/x`, when model membaca file di luar `~/coding/x`, then tool ditolak karena di luar workspace.

#### REQ-CHAT-011: Chat butuh terminal

`lana chat` harus berjalan hanya jika stdin dan stdout adalah TTY.

- `AC-CHAT-023`: Given stdin bukan TTY, when user menjalankan `lana chat`, then proses keluar dengan exit code 2 dan pesan yang menyarankan `lana run`.

### Session

#### REQ-SESS-001: Session baru, lanjut, dan buka ulang

Default `lana chat` membuat session CLI baru untuk cwd. `--continue` harus membuka session CLI
terakhir untuk cwd yang sama. `--resume <id>` harus membuka session tersebut jika cwd-nya sama.

- `AC-SESS-001`: Given ada session CLI sebelumnya di cwd ini, when user menjalankan `lana chat`, then session baru dibuat.
- `AC-SESS-002`: Given ada session CLI sebelumnya di cwd ini, when user menjalankan `lana chat --continue`, then session terakhir di cwd ini terbuka dan riwayatnya tampil.
- `AC-SESS-003`: Given tidak ada session CLI di cwd ini, when user menjalankan `lana chat --continue`, then session baru dibuat dan pesan memberi tahu tidak ada session sebelumnya.
- `AC-SESS-004`: Given session `S` dibuat di cwd lain, when user menjalankan `lana chat --resume S`, then proses keluar dengan exit code 2 dan pesan yang menyebut cwd asal `S`.
- `AC-SESS-005`: Given session terakhir terputus di tengah turn, when session dibuka ulang, then session bisa menerima pesan baru (resume sesuai ADR 0009).

### Approval

#### REQ-APPR-001: Prompt approval di terminal

Saat policy session `ask` dan tool butuh approval, sistem harus menampilkan tool call terkait dan
meminta jawaban `[y]es / [a]lways / [n]o`.

- `AC-APPR-001`: Given approval diminta, when user menjawab `y`, then outcome `allowed_once` dan tool berjalan.
- `AC-APPR-002`: Given approval diminta, when user menjawab `a`, then outcome `allowed_session` dan tool call berikutnya dengan pattern sama di session itu tidak meminta approval.
- `AC-APPR-003`: Given approval diminta, when user menjawab `n`, then outcome `rejected` dan model menerima `tool_result` error.
- `AC-APPR-004`: Given approval diminta, when 10 menit berlalu tanpa jawaban, then outcome `unavailable` dan tool tidak berjalan.
- `AC-APPR-005`: Given approval diminta, when user menekan `Ctrl+C`, then outcome `cancelled` dan turn dibatalkan.

#### REQ-APPR-002: Mode yolo

`--yolo` dan `/yolo` harus mengubah policy session menjadi `trust` dan menampilkan peringatan yang
terlihat selama policy itu aktif. Rule `deny` dan batas workspace tetap berlaku.

- `AC-APPR-006`: Given `lana chat --yolo`, when session dimulai, then peringatan tampil dan log session berisi `approval/policy` bernilai `trust`.
- `AC-APPR-007`: Given policy `trust` dan rule `deny` cocok, when model memanggil tool itu, then tool ditolak.

### Slash command

#### REQ-CMD-001: Daftar slash command

Sistem harus menangani slash command berikut tanpa request ke model:

| Perintah | Perilaku |
|---|---|
| `/agent` | Tampilkan agent aktif dan daftar agent |
| `/agent <nama>` | Ganti agent untuk session ini |
| `/new` | Buat session CLI baru di cwd ini |
| `/sessions` | Tampilkan session CLI di cwd ini dan pindah ke yang dipilih |
| `/permission` | Tampilkan policy session |
| `/permission <trust\|ask\|never>` | Ubah policy session |
| `/yolo` | Sama dengan `/permission trust`, dengan peringatan |
| `/model` | Tampilkan provider dan model aktif |
| `/model <provider>:<model>` | Ganti model untuk session ini |
| `/compact` | Jalankan compaction sekarang |
| `/fork` | Fork di `turn/end` terakhir dan pindah ke session baru |
| `/clear` | Bersihkan tampilan terminal; session dan context tidak berubah |
| `/help` | Tampilkan daftar perintah dan tombol |
| `/exit` | Keluar dengan exit code 0 |

- `AC-CMD-001`: Given agent `asisten` terdaftar, when user mengetik `/agent asisten`, then pesan berikutnya dijawab oleh `asisten`.
- `AC-CMD-002`: Given user mengetik `/agent`, when perintah diproses, then agent aktif dan daftar agent tampil, dan tidak ada request ke model.
- `AC-CMD-003`: Given session `A` aktif, when user mengetik `/new`, then session baru dibuat dan `A` tidak berubah.
- `AC-CMD-004`: Given ada beberapa session CLI di cwd ini, when user mengetik `/sessions` dan memilih satu, then chat pindah ke session itu dan riwayatnya tampil.
- `AC-CMD-005`: Given user mengetik `/permission never`, when perintah diproses, then log session berisi `approval/policy` bernilai `never` dan status bar menampilkan `never`.
- `AC-CMD-006`: Given provider `openrouter` terdaftar, when user mengetik `/model openrouter:openai/gpt-5-mini`, then log session berisi `model/selection` dan request berikutnya memakai model itu.
- `AC-CMD-007`: Given provider `abc` tidak terdaftar, when user mengetik `/model abc:x`, then pesan error tampil dan model tidak berubah.
- `AC-CMD-008`: Given session punya riwayat, when user mengetik `/compact`, then log session berisi `compaction/summary`.
- `AC-CMD-009`: Given session `A` punya minimal satu turn selesai, when user mengetik `/fork`, then session baru `B` dibuat dengan `parent_session_id = A`, chat pindah ke `B`, dan ID `A` ditampilkan.
- `AC-CMD-010`: Given session belum punya turn selesai, when user mengetik `/fork`, then pesan error tampil dan session tidak berubah.
- `AC-CMD-011`: Given user mengetik `/clear`, when perintah diproses, then layar bersih dan pesan berikutnya tetap memakai context session yang sama.
- `AC-CMD-012`: Given user mengetik `/xyz`, when perintah diproses, then pesan "perintah tidak dikenal" dan saran `/help` tampil, dan tidak ada request ke model.
- `AC-CMD-013`: Given turn sedang berjalan, when user mengetik `/new`, `/sessions`, `/fork`, `/agent <nama>`, `/model <...>`, atau `/compact`, then perintah ditolak dengan pesan untuk menunggu atau membatalkan turn.
- `AC-CMD-014`: Given turn sedang berjalan, when user mengetik `/permission <trust|ask|never>` atau `/yolo`, then `approval/policy` ditulis dan policy baru berlaku mulai evaluasi tool berikutnya di turn yang sama.

### One-shot

#### REQ-RUN-001: Menjalankan satu turn

`lana run [--agent <nama>]` harus menjalankan satu turn dengan profile `headless` di cwd, lalu keluar. Prompt diambil
dari argumen. Jika argumen tidak ada dan stdin bukan TTY, prompt diambil dari stdin. Jika
keduanya ada, isi stdin ditambahkan setelah prompt argumen.

- `AC-RUN-001`: Given `lana run --agent asisten "halo"`, when turn selesai, then jawaban final ada di stdout dan exit code 0.
- `AC-RUN-002`: Given `cat notes.md | lana run --agent asisten`, when turn berjalan, then isi `notes.md` menjadi prompt.
- `AC-RUN-003`: Given tidak ada argumen prompt dan stdin adalah TTY, when user menjalankan `lana run`, then proses keluar dengan exit code 2.
- `AC-RUN-010`: Given tanpa `--agent`, when `lana run` dijalankan, then agent dipilih dengan aturan yang sama seperti `REQ-CHAT-001`; tanpa route `cli`, exit code 2.

#### REQ-RUN-002: Format output

`--output text` (default) menulis jawaban final saja ke stdout. `--output json` menulis satu objek
JSON di akhir berisi jawaban, ID session, alasan berhenti, dan usage token. `--output stream-json`
menulis satu event per baris (JSON Lines) selama turn berjalan. Log selalu ke stderr.

- `AC-RUN-004`: Given `--output text`, when turn selesai, then stdout hanya berisi jawaban final.
- `AC-RUN-005`: Given `--output json`, when turn selesai, then stdout berisi tepat satu objek JSON yang valid.
- `AC-RUN-006`: Given `--output stream-json`, when turn berjalan, then setiap baris stdout adalah objek JSON yang valid dan baris terakhir menandai akhir turn.

#### REQ-RUN-003: Approval di one-shot

Tanpa `--ask`, policy session `never`. Dengan `--ask`, policy `ask` dan prompt approval tampil lewat
`/dev/tty`. Jika `/dev/tty` tidak tersedia, outcome `unavailable`.

- `AC-RUN-007`: Given tanpa `--ask`, when model memanggil tool yang butuh approval, then tool ditolak tanpa prompt.
- `AC-RUN-008`: Given `--ask` dan `/dev/tty` tersedia, when tool butuh approval, then prompt tampil di terminal dan stdout tidak tercampur prompt.
- `AC-RUN-009`: Given `--ask` dan `/dev/tty` tidak tersedia, when tool butuh approval, then outcome `unavailable` dan tool ditolak.

### Exit code

#### REQ-EXIT-001: Exit code per kelas

Semua subcommand di spec ini harus memakai exit code berikut:

| Kode | Arti |
|---:|---|
| 0 | Sukses |
| 1 | Error umum atau runtime |
| 2 | Salah pemakaian atau config tidak valid |
| 3 | Provider gagal (autentikasi, atau habis retry dan fallback) |
| 4 | Turn berhenti karena `max_steps` |
| 130 | Dibatalkan dengan `Ctrl+C` |

- `AC-EXIT-001`: Given API key provider salah, when `lana run` berjalan, then exit code 3.
- `AC-EXIT-002`: Given turn mencapai `max_steps`, when `lana run` selesai, then exit code 4.
- `AC-EXIT-003`: Given `lana run` berjalan, when user menekan `Ctrl+C`, then exit code 130.

### Init

#### REQ-INIT-001: Wizard konfigurasi awal

`lana init` harus menanyakan provider (Anthropic, OpenAI, OpenRouter), nama env API key, dan model
default, lalu membuat `LANA_HOME` (default `~/.lana/`) berisi `config.toml`, `agents/default.md`,
dan `secrets.env` dengan permission `0600`. File yang sudah ada tidak boleh ditimpa kecuali `--force`.
`--yes` menjalankan wizard tanpa pertanyaan dengan nilai default.

- `AC-INIT-001`: Given `LANA_HOME` belum ada, when user menyelesaikan wizard, then ketiga file dibuat dan `lana config check` lulus.
- `AC-INIT-002`: Given `config.toml` sudah ada, when user menjalankan `lana init` tanpa `--force`, then file tidak berubah dan pesan menyebut file yang sudah ada.
- `AC-INIT-003`: Given wizard selesai, when `secrets.env` diperiksa, then permission-nya `0600`.
- `AC-INIT-004`: Given `lana init --yes`, when dijalankan tanpa TTY, then file dibuat tanpa pertanyaan.

Nilai default `--yes` ditetapkan di contract.

### Config

#### REQ-CONF-001: Perintah config

| Perintah | Perilaku |
|---|---|
| `lana config check` | Validasi semua lapisan config. Laporkan setiap error dengan file dan key |
| `lana --profile <p> --dump-config` | Cetak config efektif dan asal tiap baris |
| `lana config path` | Cetak `LANA_HOME` dan daftar file yang dibaca beserta status ada atau tidak |
| `lana config edit` | Buka `config.toml` di `$EDITOR`, lalu jalankan check setelah editor ditutup |

- `AC-CONF-001`: Given config valid, when `lana config check` dijalankan, then exit code 0.
- `AC-CONF-002`: Given `agents.coder.provider` merujuk provider yang tidak ada, when `lana config check` dijalankan, then exit code 2 dan pesan menyebut file dan key `agents.coder.provider`.
- `AC-CONF-003`: Given patch menimpa baris modul, when `--dump-config` dijalankan, then baris itu menampilkan lapisan asalnya.
- `AC-CONF-004`: Given `LANA_HOME=/tmp/x`, when `lana config path` dijalankan, then output menyebut `/tmp/x` dan file yang dicari.
- `AC-CONF-005`: Given `$EDITOR` kosong, when `lana config edit` dijalankan, then proses keluar dengan exit code 2 dan pesan meminta `$EDITOR` diisi.
- `AC-CONF-006`: Given user menyimpan config tidak valid di editor, when editor ditutup, then hasil check tampil dan exit code 2.

## Matriks state chat

| State | Input | Hasil | Boleh |
|---|---|---|---:|
| Idle | Pesan | Turn dimulai | Ya |
| Idle | Slash command | Perintah dijalankan | Ya |
| Turn berjalan | Pesan | Masuk inbox | Ya |
| Turn berjalan | `/help`, `/clear`, `/permission`, `/model`, `/agent` tanpa argumen | Perintah baca dijalankan | Ya |
| Turn berjalan | `/permission <x>`, `/yolo` | Policy berubah mulai evaluasi tool berikutnya | Ya |
| Turn berjalan | Perintah yang mengubah session (lihat `AC-CMD-013`) | Ditolak dengan pesan | Tidak |
| Turn berjalan | `Ctrl+C` | Turn dibatalkan | Ya |
| Menunggu approval | `y`, `a`, `n` | Outcome dikirim | Ya |
| Menunggu approval | `Ctrl+C` | Outcome `cancelled`, turn dibatalkan | Ya |

## Kasus gagal

| Kondisi | Perilaku yang diharapkan | Kode hasil |
|---|---|---|
| Agent tidak terdaftar | Pesan menyebut nama agent | 2 |
| Config tidak valid | Pesan dari validasi config dengan file dan key | 2 |
| `LANA_HOME` belum ada | Pesan menyarankan `lana init` | 2 |
| Env API key tidak ada | Pesan menyebut nama env | 3 |
| Provider gagal setelah retry dan fallback | Chat: pesan error, chat tetap terbuka. `run`: keluar | Chat: tetap jalan. `run`: 3 |
| `--resume` ke session cwd lain | Pesan menyebut cwd asal | 2 |
| `--resume` ke ID yang tidak ada | Pesan menyebut ID | 2 |
| `lana chat` tanpa TTY | Pesan menyarankan `lana run` | 2 |

## Otorisasi dan keamanan

- CLI berjalan sebagai user OS yang menjalankannya. Tidak ada login tambahan di mode embedded.
- Approval fail-closed: tanpa jawaban valid, tool tidak berjalan.
- Secret tidak pernah dicetak ke terminal, stdout, atau stderr. `--dump-config` me-redact nilai secret.
- `~/.lana/history` dan `secrets.env` memakai permission `0600`.

## Integritas data dan konkurensi

- Semua pesan, tool call, perubahan policy, perubahan model, dan approval dari CLI tercatat di session event log.
- Satu session hanya punya satu turn aktif.
- Dua proses `lana chat` yang membuka session yang sama tidak boleh menjalankan turn bersamaan. Perilaku persisnya ditetapkan di design.

## Aksesibilitas

- Semua aksi bisa dilakukan dengan keyboard.
- Status penting (approval, error, yolo, policy) ditandai dengan teks, tidak hanya warna.
- `NO_COLOR` mematikan warna.
- `lana run` dan `lana config` tetap terbaca saat stdout bukan TTY.

## Skenario

### SC-001: Coding dengan approval

**Given** agent `coder` dengan policy `ask` dan cwd `~/coding/x`
**When** user meminta "jalankan test" dan model memanggil `bash(cargo test)` yang tidak ada di `allow`
**Then** satu baris tool call dan prompt approval tampil; setelah `y`, status tool menjadi sukses dan model melanjutkan jawaban

### SC-002: One-shot dari script

**Given** agent `asisten` terdaftar
**When** script menjalankan `lana run --agent asisten --output json "ringkas berita hari ini"`
**Then** stdout berisi satu objek JSON dan exit code 0

### SC-003: Instalasi pertama

**Given** `~/.lana/` belum ada
**When** user menjalankan `lana init`, memilih Anthropic, lalu `lana chat`
**Then** chat terbuka dengan agent `default`

### SC-004: Coba arah lain dengan fork

**Given** session `A` sudah punya tiga turn
**When** user mengetik `/fork` lalu mengirim pesan baru
**Then** pesan masuk ke session baru `B`, dan `A` tetap punya tiga turn

## Resolusi pertanyaan

| ID | Pertanyaan | Resolusi |
|---|---|---|
| `Q-001` | Nomor GitHub issue | `#1` di `ridhomujizat/lana-harness` (private) |
| `Q-002` | Subcommand dalam scope | `chat`, `run`, `init`, `config` |
| `Q-003` | Attach atau embedded | Embedded saja |
| `Q-004` | Line mode atau TUI | TUI (`ratatui`) |
| `Q-005` | Lifecycle session | Baru secara default, `--continue`, `--resume <id>` |
| `Q-006` | Slash command | `/agent`, `/new`, `/sessions`, `/help`, `/exit`, `/permission`, `/yolo`, `/model`, `/compact`, `/fork`, `/clear` |
| `Q-007` | `Ctrl+C` dan `Ctrl+D` | `Ctrl+C` batal turn; idle: kosongkan input, dua kali dalam 2 detik keluar; `Ctrl+D` di input kosong keluar |
| `Q-008` | Detail tool call | Satu baris ringkas, bisa dibuka |
| `Q-009` | Input multi-baris | Seperti Claude Code: `Shift+Enter`, `Alt/Option+Enter`, `Ctrl+J`, `\` + `Enter`; paste tidak langsung terkirim |
| `Q-010` | Output dan exit code `run` | `text`, `json`, `stream-json`; prompt dari stdin; exit code per kelas |
| `Q-011` | Workspace | cwd |
| `Q-012` | Riwayat input | `~/.lana/history`, 1000 entri, `0600`, `Ctrl+R` |
| `Q-013` | `lana init` | Wizard interaktif, `--force`, `--yes` |
| `Q-014` | `lana config` | `check`, `--dump-config`, `path`, `edit` |
| `Q-015` | Alternate screen atau inline | Inline seperti Claude Code |
| `Q-016` | Cakupan `--continue` dan `/sessions` | Per direktori |
| `Q-017` | Arti `/clear` | Bersihkan layar saja |
| `Q-018` | Arti `/fork` | Fork di turn terakhir lalu pindah |
| `Q-020` | `/permission` dan `/yolo` saat turn berjalan | Boleh; berlaku mulai evaluasi tool berikutnya (usulan agent, di-approve bersama revisi 3) |
| `CQ-001` | Cara membuka detail tool call di TUI inline | Mode verbose dengan `Ctrl+O` (revisi 4) |
| `CQ-002` | Agent `lana run` tanpa `--agent` | Sama seperti chat (revisi 4) |
| `Q-019` | Tanpa TTY | `chat` butuh TTY (exit 2); `run --ask` lewat `/dev/tty`, tanpa TTY outcome `unavailable` |

## Blocking Open Questions

None.

## Cakupan requirement

| Requirement | Acceptance criteria | Sumber |
|---|---|---|
| `REQ-CHAT-001` | `AC-CHAT-001`, `AC-CHAT-002`, `AC-CHAT-003`, `AC-CHAT-004` | ADR 0003, `Q-010` |
| `REQ-CHAT-002` | `AC-CHAT-005` | `Q-003` |
| `REQ-CHAT-003` | `AC-CHAT-006`, `AC-CHAT-007`, `AC-CHAT-008` | `Q-004`, `Q-015` |
| `REQ-CHAT-004` | `AC-CHAT-009` | ADR 0008 |
| `REQ-CHAT-005` | `AC-CHAT-010`, `AC-CHAT-011`, `AC-CHAT-024` | `Q-008`, `CQ-001` |
| `REQ-CHAT-006` | `AC-CHAT-012` | ADR 0003 |
| `REQ-CHAT-007` | `AC-CHAT-013`, `AC-CHAT-014`, `AC-CHAT-015` | `Q-009` |
| `REQ-CHAT-008` | `AC-CHAT-016`, `AC-CHAT-017`, `AC-CHAT-018`, `AC-CHAT-019` | `Q-007` |
| `REQ-CHAT-009` | `AC-CHAT-020`, `AC-CHAT-021` | `Q-012` |
| `REQ-CHAT-010` | `AC-CHAT-022` | `Q-011` |
| `REQ-CHAT-011` | `AC-CHAT-023` | `Q-019` |
| `REQ-SESS-001` | `AC-SESS-001`, `AC-SESS-002`, `AC-SESS-003`, `AC-SESS-004`, `AC-SESS-005` | `Q-005`, `Q-016`, ADR 0009 |
| `REQ-APPR-001` | `AC-APPR-001`, `AC-APPR-002`, `AC-APPR-003`, `AC-APPR-004`, `AC-APPR-005` | ADR 0004, ADR 0008 |
| `REQ-APPR-002` | `AC-APPR-006`, `AC-APPR-007` | ADR 0004, `Q-006` |
| `REQ-CMD-001` | `AC-CMD-001` sampai `AC-CMD-014` | `Q-006`, `Q-017`, `Q-018`, ADR 0003, ADR 0009 |
| `REQ-RUN-001` | `AC-RUN-001`, `AC-RUN-002`, `AC-RUN-003`, `AC-RUN-010` | `Q-010`, ADR 0010, `CQ-002` |
| `REQ-RUN-002` | `AC-RUN-004`, `AC-RUN-005`, `AC-RUN-006` | `Q-010` |
| `REQ-RUN-003` | `AC-RUN-007`, `AC-RUN-008`, `AC-RUN-009` | ADR 0010, `Q-019` |
| `REQ-EXIT-001` | `AC-EXIT-001`, `AC-EXIT-002`, `AC-EXIT-003` | `Q-010` |
| `REQ-INIT-001` | `AC-INIT-001`, `AC-INIT-002`, `AC-INIT-003`, `AC-INIT-004` | `Q-013` |
| `REQ-CONF-001` | `AC-CONF-001` sampai `AC-CONF-006` | `Q-014`, ADR 0010 |

## Approval

- [x] Scope dan pengecualian eksplisit.
- [x] Perilaku sukses dan gagal terukur.
- [x] Otorisasi eksplisit.
- [x] Transisi state dan efek destruktif eksplisit.
- [x] Default, urutan, retry, dan idempotensi eksplisit jika berlaku.
- [x] Setiap requirement dan acceptance criterion punya ID stabil dan sumber.
- [x] Keberlakuan contract eksplisit.
- [x] Blocking Open Questions bernilai `None`.
- **Di-approve oleh**: Ridho
- **Di-approve pada**: 2026-10-29 (revisi 3; revisi 4 dari jawaban pemilik atas `CQ-001` dan `CQ-002`)
