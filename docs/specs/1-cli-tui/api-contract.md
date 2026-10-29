# Contract: CLI dan TUI chat

- **Issue**: [`#1`](https://github.com/ridhomujizat/lana-harness/issues/1)
- **Requirements**: [requirements.md](requirements.md)
- **Revisi requirements**: 4
- **Status requirements**: Approved
- **Revisi contract**: 1
- **Berlaku untuk**: CLI (subcommand, flag, env, exit code, stdout/stderr, format JSON, tombol, slash command, file yang dibuat)
- **Status**: Draft

File ini menetapkan bentuk persis antarmuka terminal. Perilaku dimiliki oleh `requirements.md`.

## Konvensi bersama

| Hal | Keputusan |
|---|---|
| Nama binary | `lana` |
| Exit code | `0` sukses, `1` error umum/runtime, `2` salah pemakaian atau config tidak valid, `3` provider gagal, `4` `max_steps`, `130` dibatalkan (`REQ-EXIT-001`) |
| stdout | Hanya hasil yang diminta (jawaban, JSON, output `config`). Tidak pernah berisi log atau prompt approval |
| stderr | Pesan error dan log untuk `run` dan `config`. Pesan error diawali `error: ` |
| Log saat `chat` | File `$LANA_HOME/logs/lana.log`. Tidak ada log ke stderr selama TUI aktif |
| Level log | Env `LANA_LOG` (sintaks filter `tracing`, contoh `debug` atau `lana_agent=trace`). Flag `-v` = `info`, `-vv` = `debug`, `-vvv` = `trace`. Default `warn`. Flag menang atas env |
| `LANA_HOME` | Default `~/.lana`. Env `LANA_HOME` mengganti lokasi |
| Secret | Dibaca dari env proses, lalu dari `$LANA_HOME/secrets.env`. Env proses menang. Tidak pernah dicetak; tampil sebagai `"***"` |
| Warna | Mati jika env `NO_COLOR` terisi atau output bukan TTY |
| ID session | ULID 26 karakter (Crockford base32, huruf besar). Input menerima prefix unik minimal 6 karakter, tidak peka huruf besar-kecil |
| Format model | `<provider>:<model>`, sama dengan `fallback` di config. Contoh `openai:gpt-5-mini` |
| Serialisasi JSON | UTF-8, satu objek per baris untuk JSON Lines, field `snake_case`, waktu dalam milidetik |

## Index operasi

| ID operasi | Jenis | Perintah | Requirements | Acceptance criteria |
|---|---|---|---|---|
| `API-CLI-001` | CLI | Flag global | `REQ-CONF-001`, `REQ-EXIT-001` | `AC-CONF-003`, `AC-EXIT-001` |
| `API-CLI-002` | CLI | `lana chat` | `REQ-CHAT-001`, `REQ-CHAT-002`, `REQ-CHAT-010`, `REQ-CHAT-011`, `REQ-SESS-001`, `REQ-APPR-002` | `AC-CHAT-001` sampai `AC-CHAT-005`, `AC-CHAT-022`, `AC-CHAT-023`, `AC-SESS-001` sampai `AC-SESS-005`, `AC-APPR-006` |
| `API-CLI-003` | TUI | Layout dan status bar | `REQ-CHAT-003`, `REQ-CHAT-004`, `REQ-CHAT-005` | `AC-CHAT-006` sampai `AC-CHAT-011`, `AC-CHAT-024` |
| `API-CLI-004` | TUI | Tombol | `REQ-CHAT-006`, `REQ-CHAT-007`, `REQ-CHAT-008`, `REQ-CHAT-009` | `AC-CHAT-012` sampai `AC-CHAT-021` |
| `API-CLI-005` | TUI | Prompt approval | `REQ-APPR-001` | `AC-APPR-001` sampai `AC-APPR-005` |
| `API-CLI-006` | TUI | Slash command | `REQ-CMD-001`, `REQ-APPR-002` | `AC-CMD-001` sampai `AC-CMD-014`, `AC-APPR-007` |
| `API-CLI-007` | CLI | `lana run` | `REQ-RUN-001`, `REQ-RUN-002`, `REQ-RUN-003` | `AC-RUN-001` sampai `AC-RUN-010`, `AC-EXIT-002`, `AC-EXIT-003` |
| `API-CLI-008` | CLI | `lana init` | `REQ-INIT-001` | `AC-INIT-001` sampai `AC-INIT-004` |
| `API-CLI-009` | CLI | `lana config check` | `REQ-CONF-001` | `AC-CONF-001`, `AC-CONF-002` |
| `API-CLI-010` | CLI | `lana config path` | `REQ-CONF-001` | `AC-CONF-004` |
| `API-CLI-011` | CLI | `lana config edit` | `REQ-CONF-001` | `AC-CONF-005`, `AC-CONF-006` |

## API-CLI-001: Flag global

- **Tujuan**: flag yang berlaku untuk semua subcommand.
- **Stabilitas**: Baru

| Flag | Tipe | Default | Arti |
|---|---|---|---|
| `--profile <nama>` | string | sesuai subcommand (`chat` = `minimal`, `run` = `headless`) | Profile dasar |
| `--patch <file>` | path, bisa diulang | — | Overlay config, diterapkan berurutan |
| `--dump-config[=toml\|json]` | enum | `toml` | Cetak config efektif ke stdout lalu keluar `0` |
| `-v`, `-vv`, `-vvv` | hitungan | — | Level log |
| `-h`, `--help` | — | — | Bantuan, exit `0` |
| `-V`, `--version` | — | — | Versi, exit `0` |

Format `--dump-config=toml`:

```toml
# lana config efektif · profile: minimal
# lapisan: base, profile:minimal, ~/.lana/profiles/minimal.toml (tidak ada), ~/.lana/config.toml

# dari: ~/.lana/config.toml
[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"

# dari: base
[[modules]]
id = "store"
module = "sqlite_store"
config = { path = "lana.db" }
```

- Setiap tabel dan setiap baris `[[modules]]` diberi komentar `# dari: <lapisan>`.
- Nilai secret ditulis `"***"`. Field yang berisi nama env (seperti `api_key_env`) tidak di-redact.
- `--dump-config=json` menulis `{ "profile", "layers": [...], "config": {...}, "origins": { "<path key>": "<lapisan>" } }`.

| Gagal | Exit | Pesan stderr |
|---|---:|---|
| Flag tidak dikenal atau nilai salah | 2 | `error: <pesan clap>` |
| File `--patch` tidak ada | 2 | `error: file patch tidak ditemukan: <path>` |
| Profile tidak dikenal | 2 | `error: profile tidak dikenal: <nama> (tersedia: ...)` |

## API-CLI-002: `lana chat`

```text
lana chat [--agent <nama>] [--continue | --resume <id>] [--yolo]
```

| Flag | Tipe | Wajib | Arti |
|---|---|---:|---|
| `--agent <nama>` | string | Tidak | Agent session. Default dari route `cli` pertama |
| `--continue`, `-c` | bool | Tidak | Buka session CLI terakhir di cwd |
| `--resume <id>`, `-r <id>` | ULID atau prefix | Tidak | Buka session tertentu di cwd |
| `--yolo` | bool | Tidak | Policy session `trust` |

Aturan:

- `--continue` dan `--resume` tidak boleh dipakai bersamaan.
- `--agent` bersama `--continue` atau `--resume` mengganti agent session yang dibuka, sama seperti `/agent <nama>`.
- Workspace = `std::env::current_dir()` yang sudah di-canonicalize.
- Saat `--continue` tidak menemukan session, baris info `tidak ada session sebelumnya di <cwd>, membuat session baru` dicetak lalu chat dimulai.

| Kondisi | Exit | Pesan stderr |
|---|---:|---|
| Keluar normal (`/exit`, `Ctrl+D`, `Ctrl+C` dua kali) | 0 | — |
| stdin atau stdout bukan TTY | 2 | `error: lana chat butuh terminal interaktif; pakai 'lana run' untuk script` |
| `LANA_HOME` belum ada | 2 | `error: konfigurasi belum ada di <path>; jalankan 'lana init'` |
| Agent tidak terdaftar | 2 | `error: agent tidak dikenal: <nama> (tersedia: ...)` |
| Tidak ada route `cli` dan tanpa `--agent` | 2 | `error: tidak ada agent default untuk CLI; pakai --agent <nama> atau tambah [[routes]] channel = "cli"` |
| `--continue` dan `--resume` bersamaan | 2 | `error: --continue dan --resume tidak bisa dipakai bersamaan` |
| ID tidak ditemukan | 2 | `error: session tidak ditemukan: <id>` |
| Prefix ID ambigu | 2 | `error: prefix <id> cocok dengan beberapa session:` lalu satu ID per baris |
| Session dari cwd lain | 2 | `error: session <id> milik direktori <cwd asal>; jalankan dari direktori itu` |
| Config tidak valid | 2 | Sama dengan `API-CLI-009` |
| Env API key provider tidak ada | 3 | `error: env <NAMA_ENV> belum diisi untuk provider <provider>` |

Error provider di tengah chat tidak menghentikan proses. Pesan tampil di scrollback (lihat `API-CLI-003`).

## API-CLI-003: Layout TUI dan status bar

TUI inline (ratatui `Viewport::Inline`). Riwayat ditulis ke scrollback di atas viewport.

```text
lana · default · embedded · ask · openai:gpt-5-mini         ← header, dicetak sekali saat session dibuka
session 01JBX7Q3KM · ~/coding/x

> jalankan test

Saya jalankan cargo test.

⏺ bash  cargo test
  ✓ sukses · 2.3s

Semua test lulus.
───────────────────────────────────────────────────────────── ← viewport mulai di sini
> _
default · embedded · ask · gpt-5-mini · 12.3k tok
```

Elemen scrollback:

| Elemen | Format |
|---|---|
| Pesan user | `> ` diikuti teks; baris lanjutan diindentasi 2 spasi |
| Teks model | Markdown dirender: heading, list, bold, italic, inline code, code block dengan bahasa. Tabel dan gambar dicetak apa adanya |
| Tool call ringkas | `⏺ <nama>  <argumen utama>` lalu `  <ikon> <status> · <durasi>` |
| Ikon status | `…` berjalan, `✓` sukses, `✗` error, `⊘` ditolak. Teks status selalu ditulis: `berjalan`, `sukses`, `error`, `ditolak` |
| Tool call verbose | Seperti ringkas, ditambah blok `input:` (JSON rapi) dan `output:` (teks lengkap), diindentasi 2 spasi |
| Error provider | `✗ error provider: <pesan>`; turn ditutup, chat tetap terbuka |
| Info | Baris diawali `· ` (contoh `· pindah ke session 01JBX…`) |

Argumen utama per tool:

| Tool | Argumen utama |
|---|---|
| `bash` | `command` |
| `read_file`, `write_file`, `edit_file` | `path` relatif terhadap workspace |
| `glob`, `grep` | `pattern` |
| `web_fetch` | `url` |
| `task` | `agent` dan `title` |
| `mcp__*` dan lainnya | 60 karakter pertama dari input JSON |

Status bar (baris terakhir viewport), dipisah ` · `:

| Posisi | Isi |
|---|---|
| 1 | Nama agent |
| 2 | `embedded` |
| 3 | Policy: `trust`, `ask`, atau `never` |
| 4 | Nama model tanpa provider |
| 5 | Token session (input + output), format `12.3k tok` |
| 6 | `verbose` jika mode verbose menyala |
| 7 | `turn berjalan · ctrl+c batal` saat turn berjalan |

Saat policy `trust`, baris peringatan tampil di atas input selama policy itu aktif:

```text
⚠ YOLO: semua tool diizinkan tanpa approval (rule deny tetap berlaku)
```

## API-CLI-004: Tombol

| Tombol | Konteks | Aksi |
|---|---|---|
| `Enter` | Input | Kirim pesan. Input kosong tidak dikirim |
| `Shift+Enter` | Input, terminal dengan keyboard protocol (kitty, WezTerm, Ghostty, iTerm2 dengan CSI u) | Baris baru |
| `Alt+Enter`, `Option+Enter` | Input | Baris baru |
| `Ctrl+J` | Input | Baris baru |
| `\` lalu `Enter` | Input, `\` di akhir baris | `\` dihapus, baris baru |
| Paste | Input | Teks masuk input tanpa terkirim (bracketed paste) |
| `↑` / `↓` | Input, kursor di baris pertama/terakhir | Riwayat sebelumnya/berikutnya |
| `Ctrl+R` | Input | Cari riwayat secara incremental. `Enter` pilih, `Esc` batal |
| `Ctrl+O` | Kapan saja | Nyalakan atau matikan mode verbose. Saat dinyalakan, tool call terakhir di session dicetak ulang lengkap |
| `Ctrl+C` | Turn berjalan atau menunggu approval | Batalkan turn |
| `Ctrl+C` | Idle, input berisi | Kosongkan input |
| `Ctrl+C` | Idle, input kosong | Cetak `· tekan ctrl+c lagi untuk keluar`; tekan lagi dalam 2 detik untuk keluar `0` |
| `Ctrl+D` | Input kosong | Keluar `0` |
| `Esc` | Picker atau pencarian riwayat | Tutup |

Pesan yang dikirim saat turn berjalan dicetak di scrollback dengan penanda `> ` dan label `(antre)`.

## API-CLI-005: Prompt approval

Tampil di viewport, di atas input:

```text
? Izinkan bash: cargo test
  [y] ya sekali   [a] selalu untuk session ini   [n] tolak
```

| Tombol | Outcome |
|---|---|
| `y` atau `Y` | `allowed_once` |
| `a` atau `A` | `allowed_session` |
| `n` atau `N` | `rejected`, lalu tampil `alasan (opsional, Enter untuk lewati): ` |
| `Ctrl+C` | `cancelled`, turn dibatalkan |
| Tombol lain | Diabaikan |
| Tidak ada jawaban 10 menit | `unavailable`, cetak `· approval kedaluwarsa` |

- Argumen yang ditampilkan diambil dari `tool/call` lewat `call_id` (ADR 0004).
- Jika mode verbose menyala, input JSON lengkap ditampilkan di atas pilihan.
- Approval dari subagent diberi label: `? [explorer] Izinkan read_file: src/main.rs`.
- Setelah dijawab, baris hasil dicetak ke scrollback: `  ✓ diizinkan sekali`, `  ✓ diizinkan untuk session`, `  ⊘ ditolak`, `  ⊘ dibatalkan`.

## API-CLI-006: Slash command

Parsing: input yang diawali `/` di karakter pertama. `//teks` dikirim ke model sebagai `/teks`.

| Perintah | Argumen | Saat turn berjalan | Output |
|---|---|---|---|
| `/agent` | — | Boleh | Agent aktif, lalu daftar `nama · provider:model · policy` |
| `/agent <nama>` | nama agent | Ditolak | `· agent: <nama>` |
| `/new` | — | Ditolak | `· session baru <id>` |
| `/sessions` | — | Ditolak | Picker (lihat di bawah) |
| `/permission` | — | Boleh | `· policy: <nilai>` |
| `/permission <trust\|ask\|never>` | nilai | Boleh | `· policy: <nilai>` |
| `/yolo` | — | Boleh | Sama dengan `/permission trust` |
| `/model` | — | Boleh | `· model: <provider>:<model>` |
| `/model <provider>:<model>` | model | Ditolak | `· model: <provider>:<model>` |
| `/compact` | — | Ditolak | `· context diringkas (<token sebelum> → <token sesudah> tok)` |
| `/fork` | — | Ditolak | `· fork dari <id lama> → session <id baru>` |
| `/clear` | — | Boleh | Layar dibersihkan, header dicetak ulang |
| `/help` | — | Boleh | Daftar perintah dan tombol |
| `/exit` | — | Boleh, membatalkan turn dulu | Keluar `0` |

Picker `/sessions` di viewport:

```text
Session di ~/coding/x (↑↓ pilih · enter buka · esc batal)
› 01JBX7Q3KM  perbaiki test parser      default  5 turn  2 menit lalu   (aktif)
  01JBW2A9PD  tambah flag --output      default  12 turn 3 jam lalu
```

- Maksimal 20 session, urut `updated_at` terbaru.
- Judul dari projection `sessions.title`. Session tanpa judul memakai 40 karakter pertama pesan user pertama.

| Gagal | Output |
|---|---|
| Perintah tidak dikenal | `✗ perintah tidak dikenal: /<x> · ketik /help` |
| Argumen salah | `✗ <perintah>: <alasan> · contoh: <contoh>` |
| Agent atau provider tidak terdaftar | `✗ agent tidak dikenal: <nama>` atau `✗ provider tidak dikenal: <nama>` |
| Ditolak saat turn berjalan | `✗ tunggu turn selesai atau tekan ctrl+c dulu` |
| `/fork` tanpa turn selesai | `✗ belum ada turn selesai untuk di-fork` |

## API-CLI-007: `lana run`

```text
lana run [--agent <nama>] [--output text|json|stream-json] [--ask] [prompt...]
```

| Input | Tipe | Wajib | Arti |
|---|---|---:|---|
| `prompt...` | string, sisa argumen digabung dengan spasi | Tidak | Prompt |
| stdin | teks UTF-8 | Tidak | Dibaca jika stdin bukan TTY. Ditambahkan setelah prompt argumen dengan dua baris baru |
| `--agent <nama>` | string | Tidak | Default dari route `cli` pertama |
| `--output`, `-o` | enum | Tidak | Default `text` |
| `--ask` | bool | Tidak | Policy `ask`, approval lewat `/dev/tty` |

`--output text`: stdout berisi teks jawaban final ditambah satu baris baru.

`--output json`: stdout berisi tepat satu objek:

```json
{
  "type": "result",
  "session_id": "01JBX7Q3KM8Z4T2V6N0P1R5S9W",
  "agent": "default",
  "model": "openai:gpt-5-mini",
  "cwd": "/Users/ridho/coding/x",
  "stop_reason": "end_turn",
  "text": "Jawaban final",
  "usage": { "input_tokens": 1200, "output_tokens": 340 },
  "duration_ms": 4210,
  "exit_code": 0,
  "error": null
}
```

| Field | Tipe | Arti |
|---|---|---|
| `stop_reason` | `end_turn` \| `max_steps` \| `cancelled` \| `error` | Alasan turn berhenti |
| `text` | string | Teks jawaban final; string kosong jika tidak ada |
| `error` | `null` atau `{ "class": "provider" \| "runtime", "message": string }` | Terisi jika `stop_reason = error` |
| `exit_code` | integer | Sama dengan exit code proses |

`--output stream-json`: JSON Lines. Baris terakhir selalu `type: "result"` dengan bentuk yang sama
seperti `--output json`.

| `type` | Field |
|---|---|
| `session` | `session_id`, `agent`, `model`, `cwd` |
| `text_delta` | `text` |
| `tool_call` | `call_id`, `name`, `input` |
| `approval` | `call_id`, `outcome` (`allowed_once`, `allowed_session`, `rejected`, `cancelled`, `unavailable`) |
| `tool_result` | `call_id`, `status` (`success`, `error`, `rejected`), `output` |
| `result` | lihat `--output json` |

Tipe baru bisa ditambah di versi berikutnya. Consumer harus mengabaikan `type` yang tidak dikenal.

| Kondisi | Exit | stdout (`json`/`stream-json`) | stderr |
|---|---:|---|---|
| Sukses | 0 | `result` | — |
| Tanpa prompt dan stdin TTY | 2 | kosong | `error: prompt kosong; berikan argumen atau pipe lewat stdin` |
| Agent atau config salah | 2 | kosong | Sama dengan `API-CLI-002` |
| Env API key tidak ada | 3 | kosong | `error: env <NAMA_ENV> belum diisi untuk provider <provider>` |
| Provider gagal setelah retry dan fallback | 3 | `result` dengan `error.class = "provider"` | `error: <pesan>` |
| `max_steps` | 4 | `result` dengan `stop_reason = "max_steps"` | `error: turn berhenti karena max_steps (<n>)` |
| `Ctrl+C` atau SIGINT | 130 | `result` dengan `stop_reason = "cancelled"` | — |
| Error lain | 1 | `result` dengan `error.class = "runtime"` jika session sudah dibuat | `error: <pesan>` |

Prompt approval dengan `--ask` memakai format `API-CLI-005` dalam mode baris (tanpa TUI) di `/dev/tty`.

## API-CLI-008: `lana init`

```text
lana init [--force] [--yes]
```

Pertanyaan wizard (urut):

| Pertanyaan | Pilihan | Default |
|---|---|---|
| Provider | `openai`, `anthropic`, `openrouter` | `openai` |
| Nama env API key | string | `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `OPENROUTER_API_KEY` |
| Model default | string | `gpt-5-mini` / `claude-sonnet-5-5` / `openai/gpt-5-mini` |

`--yes` memakai semua default tanpa bertanya. File yang dibuat untuk default:

`$LANA_HOME/config.toml`

```toml
[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"

[agents.default]
provider = "openai"
model = "gpt-5-mini"
prompt_file = "agents/default.md"
tools = ["read_file", "write_file", "edit_file", "glob", "grep", "bash"]
permission = "ask"

[[routes]]
channel = "cli"
agent = "default"
```

Untuk `anthropic`: `kind = "anthropic"` tanpa `base_url`. Untuk `openrouter`:
`base_url = "https://openrouter.ai/api/v1"`.

`$LANA_HOME/agents/default.md`: system prompt umum untuk asisten coding dan personal (isi ditentukan di design).

`$LANA_HOME/secrets.env` (permission `0600`):

```text
# Isi API key di sini, atau set sebagai env di shell.
# OPENAI_API_KEY=
```

Output sukses (stdout):

```text
✓ dibuat ~/.lana/config.toml
✓ dibuat ~/.lana/agents/default.md
✓ dibuat ~/.lana/secrets.env (0600)
· isi OPENAI_API_KEY di ~/.lana/secrets.env atau env shell, lalu jalankan: lana chat
```

| Kondisi | Exit | Output |
|---|---:|---|
| Sukses | 0 | Seperti di atas |
| File sudah ada, tanpa `--force` | 2 | stderr `error: file sudah ada: <path>; pakai --force untuk menimpa`. Tidak ada file yang diubah |
| Dengan `--force` | 0 | Baris `✓ ditimpa <path>` untuk file yang sudah ada |
| Tanpa TTY dan tanpa `--yes` | 2 | stderr `error: lana init butuh terminal; pakai --yes untuk nilai default` |
| `Ctrl+C` di tengah wizard | 130 | Tidak ada file yang dibuat |

## API-CLI-009: `lana config check`

```text
lana config check
```

| Kondisi | Exit | Output |
|---|---:|---|
| Valid | 0 | stdout `✓ config valid (<n> lapisan, <m> agent, <k> provider)` |
| Tidak valid | 2 | stderr satu baris per error: `error: <file>:<baris>: <key>: <pesan>`. Baris dihilangkan jika tidak diketahui |

Contoh:

```text
error: ~/.lana/config.toml:12: agents.coder.provider: provider 'anthropik' tidak terdaftar (tersedia: openai)
```

Semua error dilaporkan sekaligus, tidak berhenti di error pertama.

## API-CLI-010: `lana config path`

```text
lana config path
```

Output stdout, exit `0`:

```text
LANA_HOME  /Users/ridho/.lana
✓ /Users/ridho/.lana/config.toml
· /Users/ridho/.lana/profiles/minimal.toml (tidak ada)
✓ /Users/ridho/.lana/secrets.env
✓ /Users/ridho/.lana/lana.db
✓ /Users/ridho/.lana/logs/lana.log
```

Profile yang ditampilkan mengikuti `--profile` (default `minimal`).

## API-CLI-011: `lana config edit`

```text
lana config edit
```

- Menjalankan `$VISUAL`, atau `$EDITOR` jika `$VISUAL` kosong, dengan argumen `$LANA_HOME/config.toml`. Nilai env dipecah seperti shell (mendukung `code --wait`).
- Setelah editor keluar, menjalankan `API-CLI-009` dan memakai exit code-nya.

| Kondisi | Exit | Output |
|---|---:|---|
| `$VISUAL` dan `$EDITOR` kosong | 2 | `error: set $EDITOR untuk memakai lana config edit` |
| Editor keluar dengan kode non-zero | 1 | `error: editor keluar dengan kode <n>`; check tidak dijalankan |
| `config.toml` belum ada | 2 | `error: konfigurasi belum ada di <path>; jalankan 'lana init'` |

## Traceability contract

| Operasi | Requirements | Komponen design | Verifikasi |
|---|---|---|---|
| `API-CLI-001` | `REQ-CONF-001`, `REQ-EXIT-001` | Ditetapkan di design | Test CLI (`assert_cmd`) |
| `API-CLI-002` | `REQ-CHAT-001`, `REQ-CHAT-002`, `REQ-CHAT-010`, `REQ-CHAT-011`, `REQ-SESS-001`, `REQ-APPR-002` | Ditetapkan di design | Test CLI dan test terminal semu |
| `API-CLI-003` | `REQ-CHAT-003`, `REQ-CHAT-004`, `REQ-CHAT-005` | Ditetapkan di design | Snapshot render |
| `API-CLI-004` | `REQ-CHAT-006` sampai `REQ-CHAT-009` | Ditetapkan di design | Test input handler |
| `API-CLI-005` | `REQ-APPR-001` | Ditetapkan di design | Test penjawab approval |
| `API-CLI-006` | `REQ-CMD-001`, `REQ-APPR-002` | Ditetapkan di design | Test parser dan dispatcher perintah |
| `API-CLI-007` | `REQ-RUN-001` sampai `REQ-RUN-003`, `REQ-EXIT-001` | Ditetapkan di design | Test CLI dengan `MockLlm` |
| `API-CLI-008` | `REQ-INIT-001` | Ditetapkan di design | Test CLI di `LANA_HOME` sementara |
| `API-CLI-009` | `REQ-CONF-001` | Ditetapkan di design | Test CLI dengan config salah |
| `API-CLI-010` | `REQ-CONF-001` | Ditetapkan di design | Test CLI |
| `API-CLI-011` | `REQ-CONF-001` | Ditetapkan di design | Test CLI dengan `EDITOR` palsu |

## Blocking Contract Questions

| ID | Pertanyaan | Mengendalikan | Pemilik | Status | Resolusi |
|---|---|---|---|---|---|
| `CQ-001` | Cara membuka detail tool call di TUI inline | `API-CLI-003`, `API-CLI-004` | Ridho | Resolved | Mode verbose dengan `Ctrl+O` |
| `CQ-002` | Agent `lana run` tanpa `--agent` | `API-CLI-007` | Ridho | Resolved | Sama seperti chat |
| `CQ-003` | Format `json` dan `stream-json` | `API-CLI-007` | Ridho | Resolved | Objek `result` dan event `type` per baris |
| `CQ-004` | Format ID session | Konvensi bersama | Ridho | Resolved | ULID, prefix unik minimal 6 karakter |
| `CQ-005` | Default `lana init --yes` | `API-CLI-008` | Ridho | Resolved | `openai`, `OPENAI_API_KEY`, `gpt-5-mini` |
| `CQ-006` | Log selama chat | Konvensi bersama | Ridho | Resolved | File log, `LANA_LOG`, `-v` |
| `CQ-007` | Format `--dump-config` | `API-CLI-001` | Ridho | Resolved | TOML dengan komentar asal, opsi JSON |
| `CQ-008` | Tampilan `/sessions` | `API-CLI-006` | Ridho | Resolved | Picker di viewport |

None yang masih Open.

Detail yang dipilih agent tanpa pertanyaan terpisah (mohon dicek saat review): teks pesan error dan
info, ikon status, `//` untuk mengirim teks diawali `/`, label `(antre)`, alias `-c`/`-r`/`-o`,
`$VISUAL` sebelum `$EDITOR`, isi `tools` agent `default`, dan `/exit` yang membatalkan turn dulu.

## Approval

- [x] Requirements sudah Approved dan revisinya dicatat di atas.
- [x] Keberlakuan contract sudah ditentukan.
- [x] Setiap operasi punya ID stabil dan tertaut ke requirement dan acceptance criteria.
- [x] Input, sukses, gagal, otorisasi, dan kompatibilitas eksplisit.
- [x] Tidak ada perilaku yang hanya ada di file ini.
- [x] Blocking Contract Questions bernilai `None`.
- **Di-approve oleh**: —
- **Di-approve pada**: —
