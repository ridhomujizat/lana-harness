# Contract: Fondasi (workspace, profile loader, Context/modul/hook)

- **Issue**: [`#2`](https://github.com/ridhomujizat/lana-harness/issues/2)
- **Requirements**: [requirements.md](requirements.md)
- **Revisi requirements**: 4
- **Status requirements**: Approved
- **Revisi contract**: 2 (rekonsiliasi lintas spec #2 sampai #4, lihat `CQ-016` sampai `CQ-032`)
- **Berlaku untuk**: format file config dan profile (TOML), format pesan error config, API Go publik package `config` dan `core`. Tidak ada subcommand CLI di spec ini (CLI ada di spec #1).
- **Status**: Approved

File ini menetapkan bentuk persis format config dan API Go publik. Perilaku dimiliki oleh `requirements.md`.

## Konvensi bersama

| Hal | Keputusan |
|---|---|
| Module Go | `github.com/ridhomujizat/lana-harness` (ADR 0013) |
| Versi Go | Direktif `go 1.24` di `go.mod`. Toolchain dari `flake.lock`, `GOTOOLCHAIN=local` |
| Build | `CGO_ENABLED=0`, tanpa cgo |
| Encoding | UTF-8. TOML v1.0 |
| Nama key | `snake_case` di TOML dan JSON. Nama identifier Go mengikuti aturan Go |
| Waktu | Field `created_at` dan `updated_at` dalam milidetik Unix (`int64`) |
| `LANA_HOME` | Default `~/.lana`. Env `LANA_HOME` mengganti lokasi |
| Secret | Tipe `core.Secret`. Tampil `***` di semua output (`fmt`, `slog`, JSON, TOML) |
| Stabilitas | Sebelum `v1.0.0`, perubahan API publik dicatat di CHANGELOG (ADR 0014) |
| Package public | `config` dan `core`. Package lain di module tidak dijamin stabil |

## Index operasi

| ID operasi | Jenis | Subjek | Requirements | Acceptance criteria |
|---|---|---|---|---|
| `API-CONF-001` | File | Lokasi file dan urutan lapisan | `REQ-CONF-001`, `REQ-CONF-006` | `AC-CONF-001`, `AC-CONF-002`, `AC-CONF-015` |
| `API-CONF-002` | File | Skema tingkat atas dan `version` | `REQ-CONF-003`, `REQ-CONF-008` | `AC-CONF-009`, `AC-CONF-021` |
| `API-CONF-003` | File | `[providers.<nama>]` | `REQ-CONF-003` | `AC-CONF-010`, `AC-CONF-023` |
| `API-CONF-004` | File | `[agents.<nama>]` dan `rules` | `REQ-CONF-002`, `REQ-CONF-003` | `AC-CONF-006`, `AC-CONF-016` |
| `API-CONF-005` | File | `[[routes]]` | `REQ-CONF-002`, `REQ-CONF-003` | `AC-CONF-007` |
| `API-CONF-006` | File | `[mcp.<nama>]` | `REQ-CONF-003`, `REQ-CONF-007` | `AC-CONF-018`, `AC-CONF-020` |
| `API-CONF-007` | File | `[channels.<nama>]` | `REQ-CONF-002` | Tidak ada AC khusus; aturan merge umum `AC-CONF-006` |
| `API-CONF-008` | File | `[[modules]]`, posisi, dan `disabled` | `REQ-CONF-002`, `REQ-MOD-001` | `AC-CONF-003`, `AC-CONF-004`, `AC-CONF-005`, `AC-CONF-008` |
| `API-CONF-009` | File | Profile bawaan dan file profile | `REQ-PROF-001`, `REQ-CONF-001` | `AC-PROF-001`, `AC-PROF-002`, `AC-CONF-002` |
| `API-CONF-010` | File | `secrets.env` dan urutan lookup | `REQ-CONF-005` | `AC-CONF-012`, `AC-CONF-017` |
| `API-CONF-011` | File | Interpolasi `${NAMA}` | `REQ-CONF-007` | `AC-CONF-018`, `AC-CONF-019`, `AC-CONF-020` |
| `API-CONF-012` | Output | Format pesan error config | `REQ-CONF-003` | `AC-CONF-009`, `AC-CONF-010`, `AC-CONF-016` |
| `API-CONF-013` | Output | Peta asal (origins) | `REQ-CONF-004` | `AC-CONF-011` |
| `API-CONF-014` | Go | `config.Load` dan tipe pendukung | `REQ-CONF-001` sampai `REQ-CONF-009` | `AC-CONF-022`, `AC-CONF-023` dan acuan di atas |
| `API-CORE-001` | Go | `Module`, `ModuleConfig`, `RegisterModule` | `REQ-MOD-001`, `REQ-MOD-004` | `AC-MOD-001`, `AC-MOD-007` |
| `API-CORE-002` | Go | `Builder`, `Boot`, `Shutdown` | `REQ-MOD-001` sampai `REQ-MOD-004`, `REQ-HOOK-004` | `AC-MOD-001` sampai `AC-MOD-006`, `AC-HOOK-009` |
| `API-CORE-003` | Go | `Context`, `ServiceKey` | `REQ-MOD-001`, `REQ-MOD-004` | `AC-MOD-007` |
| `API-CORE-004` | Go | Hook generik `Waterfall`, `Serial`, `Observe` | `REQ-HOOK-001` sampai `REQ-HOOK-004` | `AC-HOOK-001` sampai `AC-HOOK-008`, `AC-HOOK-010` |
| `API-CORE-005` | Go | Hook bernama dan tipe input-output | `REQ-HOOK-001`, `REQ-HOOK-002`, `REQ-HOOK-003` | Turunan dari `AC-HOOK-*`; daftar nama sesuai ADR 0002. Tipe I dan O di `API-CORE-015` |
| `API-CORE-006` | Go | Pesan, `ChatRequest`, `ChatEvent`, `Provider` | Tidak berlaku | Tipe untuk spec #3 |
| `API-CORE-007` | Go | `Tool`, `Risk`, `ToolRegistry` | Tidak berlaku | Tipe untuk spec #3 |
| `API-CORE-008` | Go | `SessionEvent`, kind, dan payload | Tidak berlaku | Event ADR 0009, tipe untuk spec #3 dan #4 |
| `API-CORE-009` | Go | `SessionStore`, `SessionInfo`, `NewSession`, `SessionFilter` | Tidak berlaku | Interface untuk spec #4 (dengan `Info`, `ChainEvents`, `DeriveMessages`) |
| `API-CORE-010` | Go | `AgentRegistry`, `LiveFrame` | Tidak berlaku | Interface untuk spec #3 |
| `API-CORE-011` | Go | `ApprovalService`, `Policy`, `ApprovalOutcome` | Tidak berlaku | Interface untuk spec #4 |
| `API-CORE-012` | Go | `Secret`, `Secrets` | `REQ-CONF-005` | `AC-CONF-012`, `AC-CONF-013`, `AC-CONF-014` |
| `API-CORE-013` | Go | Tipe schema `Config` | `REQ-CONF-003` | `AC-CONF-009`, `AC-CONF-010` |
| `API-CORE-014` | Go | Error sentinel dan `LeaseHeldError` | `REQ-MOD-002`, `REQ-HOOK-001` | `AC-MOD-003`, `AC-MOD-004`, `AC-HOOK-005` |
| `API-CORE-015` | Go | Daftar tipe gabungan `core` (sumber tunggal): tipe hook I/O, tipe tambahan dari spec #3 dan #4, `DeriveMessages`, `ResolvePrefix` | Tidak berlaku | Kolom `Kebutuhan dari spec #N` di tabel API-CORE-015 |

---

## API-CONF-001: Lokasi file dan urutan lapisan

- **Tujuan**: menetapkan file yang dibaca dan urutan lapisan yang membentuk config efektif.
- **Stabilitas**: Baru

| Urutan | Nama lapisan (origin) | Sumber | Jika tidak ada |
|---:|---|---|---|
| 1 | `base` | Baris bawaan (`API-CONF-009`) | Selalu ada |
| 2 | `profile:<nama>` | Profile bawaan (`API-CONF-009`) | Nama tidak dikenal: error (`AC-PROF-002`) |
| 3 | `<LANA_HOME>/profiles/<nama>.toml` | File | Dilewati (`AC-CONF-002`) |
| 4 | `<LANA_HOME>/config.toml` | File | Dilewati |
| 5 | Setiap `--patch <file>`, berurutan | File | Error: `file patch tidak ditemukan: <path>` |
| 6 | Setiap `Layers`, berurutan | Memori | Tidak berlaku |

- Path `LANA_HOME` dipakai hanya jika `Options.Home` terisi (`API-CONF-014`).
- Path relatif di config (misalnya `prompt_file`) dihitung dari `LANA_HOME` (`AC-CONF-015`).
- Nama lapisan file untuk origin dan error adalah path absolut. Nama lapisan memori adalah `Layer.Name`.

## API-CONF-002: Skema tingkat atas dan `version`

Tabel tingkat atas yang dikenal. Key lain adalah error `Tidak dikenal`.

| Key | Tipe TOML | Wajib | Arti |
|---|---|---:|---|
| `version` | integer | Tidak | Versi format. Tanpa field, versi dianggap `1`. Versi > `1` ditolak (`AC-CONF-021`) |
| `providers` | tabel | Tidak | Lihat `API-CONF-003` |
| `agents` | tabel | Tidak | Lihat `API-CONF-004` |
| `routes` | array tabel | Tidak | Lihat `API-CONF-005` |
| `mcp` | tabel | Tidak | Lihat `API-CONF-006` |
| `channels` | tabel | Tidak | Lihat `API-CONF-007` |
| `modules` | array tabel | Tidak | Lihat `API-CONF-008` |

- `version` hanya di tingkat atas. Nilai bukan integer adalah error tipe.
- Error `version` menyebut versi file dan versi yang didukung: `versi config 2 lebih tinggi dari yang didukung (1)`.

## API-CONF-003: `[providers.<nama>]`

```toml
[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
```

| Key | Tipe | Wajib | Nilai | Aturan |
|---|---|---:|---|---|
| `kind` | string | Ya | `openai_compat`, `anthropic` | Enum. Nilai lain adalah error `REQ-CONF-003` |
| `base_url` | string | Tidak | URL | Tidak divalidasi formatnya di spec ini |
| `api_key_env` | string | Tidak | Nama env | Nama saja. Nilai dibaca lewat `core.Secrets` (`API-CONF-010`) |
| `headers` | tabel string ke string | Tidak | Nilai literal | Tidak diinterpolasi. Owner spec #3 (`API-PROV-001`) |
| `timeout` | string durasi Go | Tidak | Contoh `300s` | Divalidasi `time.ParseDuration`. Default di spec #3 (`API-PROV-001`) |

- Nama env di `api_key_env` tidak dicek keberadaannya saat load. Pengecekan saat run ada di spec #1 (exit 3).
- Tabel `providers` digabung per key (`API-CONF-013`).

## API-CONF-004: `[agents.<nama>]` dan `rules`

```toml
[agents.coder]
provider = "anthropic"
model = "claude-sonnet-5-5"
fallback = ["openrouter:anthropic/claude-haiku-5-5"]
prompt_file = "agents/coder.md"
workspace = "~/coding"
tools = ["read_file", "write_file", "edit_file", "glob", "grep", "bash", "task"]
subagents = ["explorer"]
mcp = ["github"]
skills = ["git-workflow"]
permission = "ask"

[agents.coder.rules]
allow = ["bash(git status*)", "bash(go test*)"]
deny = ["bash(rm -rf*)", "bash(sudo*)"]
```

| Key | Tipe | Wajib | Validasi di spec ini |
|---|---|---:|---|
| `provider` | string | Tidak | Harus ada di `providers` (`REQ-CONF-003`) |
| `model` | string | Tidak | Tipe string |
| `fallback` | array string | Tidak | Tipe array string. Isi `provider:model` tidak diperiksa di spec ini |
| `prompt_file` | string | Tidak | Tipe string. Path relatif dari `LANA_HOME` |
| `workspace` | string | Tidak | Tipe string |
| `tools` | array string | Tidak | Tipe array string. Nama tool diperiksa spec #3 |
| `subagents` | array string | Tidak | Setiap nilai harus ada di `agents` |
| `mcp` | array string | Tidak | Setiap nilai harus ada di `mcp` |
| `skills` | array string | Tidak | Tipe array string |
| `permission` | string | Tidak | `trust`, `ask`, atau `never`. Default `ask` jika tidak ditulis |
| `max_steps` | integer | Tidak | 1 sampai 200. Default 25 di spec #3 (`API-AGENT-007`) |
| `context_window` | integer | Tidak | Lebih dari 0. Default 128000 di spec #4 (`API-PERM-002`) |
| `workspace` | string | Tidak | Tipe string. Owner spec #3 |
| `rules.allow` | array string | Tidak | Tipe array string. Isi divalidasi pola setelah spec #4 selesai (`CQ-030`) |
| `rules.deny` | array string | Tidak | Tipe array string. Sama seperti `allow` |

- Key lain di dalam `agents.<nama>` adalah error `Tidak dikenal`, dengan saran key terdekat.
- Pola `rules.allow` dan `rules.deny` divalidasi dengan `internal/pattern.ParsePattern` (grammar dari spec #4 `API-PERM-001`, `CQ-030`). Paket itu dipakai `config` dan `tools/permission`. Sebelum spec #4 selesai, nilainya hanya array string.
- Key di bawah ini dikenal oleh spec ini. Validasi isi dimiliki spec pemilik:

| Key | Owner | Validasi di spec #2 |
|---|---|---|
| `agents.<nama>.max_steps` | Spec #3 (`API-AGENT-007`) | Integer 1 sampai 200 (`REQ-CONF-003`) |
| `agents.<nama>.context_window` | Spec #4 (`API-PERM-002`) | Integer lebih dari 0 |
| `agents.<nama>.permission` | Spec #4 (`API-PERM-002`) | Enum `trust`, `ask`, `never` |
| `agents.<nama>.rules.allow`, `.deny` | Spec #4 (`API-PERM-002`) | Array string. Pola di spec #4 setelah `CQ-030` |
| `agents.<nama>.workspace` | Spec #3 (`API-AGENT-007`) | Tipe string |
| `providers.<nama>.headers` | Spec #3 (`API-PROV-001`) | Tabel string ke string |
| `providers.<nama>.timeout` | Spec #3 (`API-PROV-001`) | Durasi Go |
| `[[modules]]` `store`, `config.path` | Spec #4 (`API-STORE-001`) | Tidak kosong |
| `[[routes]]`, `channels.*`, `module.config` | Spec #1, modul channel | Opaque (`CQ-012`) |
- Field `model` dan `provider` tidak wajib di tingkat config. Pemeriksaan saat run ada di spec #3.

## API-CONF-005: `[[routes]]`

```toml
[[routes]]
channel = "telegram"
match = { chat_id = 123456789 }
agent = "asisten"

[[routes]]
channel = "cli"
agent = "coder"
```

| Key | Tipe | Wajib | Aturan |
|---|---|---:|---|
| `channel` | string | Ya | Nama channel. Tidak diperiksa terhadap daftar channel di spec ini |
| `agent` | string | Ya | Harus ada di `agents` (`REQ-CONF-003`) |
| `match` | tabel | Tidak | Opaque di spec ini. Validasi isi ada di modul channel |

- Urutan `[[routes]]` dipertahankan. Route `cli` pertama adalah default `lana chat` (spec #1).
- Daftar `routes` dari lapisan yang paling akhir menulisnya menggantikan seluruh daftar (`AC-CONF-007`).

## API-CONF-006: `[mcp.<nama>]`

```toml
[mcp.github]
command = "github-mcp-server"
args = ["stdio"]
env = { GITHUB_TOKEN = "${GITHUB_TOKEN}" }

[mcp.notion]
url = "https://mcp.notion.com/mcp"
headers = { Authorization = "Bearer ${NOTION_TOKEN}" }
```

| Key | Tipe | Wajib | Aturan |
|---|---|---:|---|
| `command` | string | Salah satu `command` atau `url` | Server stdio |
| `args` | array string | Tidak | Tidak diinterpolasi |
| `url` | string | Salah satu `command` atau `url` | Server streamable HTTP |
| `env` | tabel string | Tidak | Nilai diinterpolasi (`API-CONF-011`). Semua nilai bertipe `core.Secret` |
| `headers` | tabel string | Tidak | Sama seperti `env` |

- Memiliki `command` dan `url` sekaligus adalah error validasi.
- Tidak memiliki keduanya adalah error validasi.
- Setiap nama di `agents.<nama>.mcp` harus ada di `mcp`.

## API-CONF-007: `[channels.<nama>]`

- Setiap `[channels.<nama>]` harus berupa tabel. Isinya opaque di spec ini; modul channel memvalidasi key-nya.
- Tabel digabung per key secara rekursif (`API-CONF-013`).

```toml
[channels.telegram]
token_env = "TELEGRAM_BOT_TOKEN"
```

## API-CONF-008: `[[modules]]`, posisi, dan `disabled`

```toml
[[modules]]
id = "store"
module = "sqlite_store"
config = { path = "lana.db" }

[[modules]]
id = "channel.telegram"
module = "telegram"
after = "store"
config = { poll_timeout_secs = 30 }

[[modules]]
id = "scheduler"
disabled = true
```

| Key | Tipe | Wajib | Aturan |
|---|---|---:|---|
| `id` | string | Ya | Tidak kosong. Unik per lapisan. Duplikat adalah error `REQ-CONF-003` |
| `module` | string | Ya untuk baris baru | Harus nama modul yang terdaftar di binary (`REQ-CONF-003`) |
| `disabled` | bool | Tidak | Default `false`. `true` membuat baris tidak di-install (`AC-CONF-004`) |
| `after` | string | Tidak | `id` lain. Harus ada (`AC-CONF-008`) |
| `before` | string | Tidak | `id` lain. Harus ada |
| `config` | tabel | Tidak | Opaque. Divalidasi modul saat install lewat `ModuleConfig.Decode` |

Aturan merge baris:

| Kasus | Hasil |
|---|---|
| `id` belum ada | Baris ditambahkan. Di akhir, atau tepat setelah `after`, atau tepat sebelum `before` |
| `id` sudah ada | Seluruh `config` lama diganti (`AC-CONF-003`). `module` dan `disabled` yang ditulis menimpa nilai lama |
| `after` atau `before` untuk `id` yang sudah ada | Diabaikan. Nilai tetap divalidasi sebagai `id` yang harus ada |
| `after` dan `before` ditulis bersamaan | Error validasi |
| `disabled = true` | Baris tetap ada di config efektif dengan `disabled = true`, tidak di-install |

- Config efektif menyimpan semua baris termasuk yang `disabled`, sehingga `--dump-config` bisa menampilkannya.

## API-CONF-009: Profile bawaan dan file profile

Profile bawaan adalah daftar baris `[[modules]]` yang dikompilasi ke binary.

| Profile | Lapisan | Baris |
|---|---|---|
| `minimal` | `profile:minimal` | Kosong. Hanya lapisan `base` (`CQ-031`) |
| `headless` | `profile:headless` | `{ id = "runner", module = "cli_runner" }`. Base plus runner one-shot (modul milik spec #1) |

Lapisan `base` (dikompilasi ke binary). Dipakai `chat` dan `run`:

| `id` | `module` | `config` | Diimplementasi di |
|---|---|---|---|
| `llm` | `llm` | `{}` | Spec #3 (`API-PROV-001`) |
| `tools` | `tools` | `{}` | Spec #3 (`API-AGENT-009`) |
| `store` | `sqlite_store` | `{ path = "lana.db" }` | Spec #4 (`API-STORE-001`) |
| `agent` | `agent` | `{}` | Spec #3 (`API-AGENT-007`) |
| `permission` | `permission` | `{}` | Spec #4 (`API-PERM-003`) |
| `approval` | `approval` | `{}` | Spec #4 (`API-APPR-001`) |
| `compaction` | `compaction` | `{}` | Spec #4 (`API-COMP-002`) |

- Baris `prompt` yang sebelumnya ada dihapus. `prompt_file` dibaca modul `agent` (spec #3).
- `store` memakai `sqlite_store`. Library tanpa `Home` memakai `memstore` (spec #3 `API-LIB-001`). Opsi `Store` memakai `provided_store`. Lihat design `DQ-016`.
- Nama modul di atas diimplementasi di spec #3 dan #4. Sebelum itu, config bawaan gagal validasi `module` tidak dikenal (lihat `CQ-032`). Test memakai modul palsu dengan nama yang sama.
- Profile di luar daftar bawaan (`daemon`, `acp`, `sdk`, `mcp`) ditambah oleh spec yang membawa modulnya.
- File `<LANA_HOME>/profiles/<nama>.toml` memakai skema yang sama dengan `config.toml`.
- Profile tidak dikenal: error `profile tidak dikenal: <nama> (tersedia: <daftar, diurutkan>)`.

## API-CONF-010: `secrets.env` dan urutan lookup

```text
# Isi API key di sini, atau set sebagai env di shell.
OPENAI_API_KEY=sk-...
```

| Aturan | Nilai |
|---|---|
| Lokasi | `<LANA_HOME>/secrets.env` |
| Format | `NAMA=nilai` per baris. Baris kosong dan `#` di awal baris diabaikan |
| Kutip | Pembungkus `"..."` atau `'...'` di nilai dibuang |
| Baris lain | Error validasi dengan nomor baris |
| Urutan lookup | Env proses, lalu `secrets.env`. Env proses menang (`AC-CONF-012`) |
| Nilai kosong | Dianggap tidak ada, baik di env maupun di file |
| Permission | Jika mode bukan `0600`, peringatan (`AC-CONF-017`) |

Pesan peringatan permission:

```text
secrets.env punya permission 0644 (seharusnya 0600); jalankan: chmod 600 /Users/ridho/.lana/secrets.env
```

- File tetap dibaca setelah peringatan.
- Peringatan masuk `Result.Warnings` dan dicatat lewat `slog` level `warn`.

## API-CONF-011: Interpolasi `${NAMA}`

| Aturan | Nilai |
|---|---|
| Lokasi berlaku | Nilai `mcp.<nama>.env` dan `mcp.<nama>.headers` saja |
| Pola nama | `${NAMA}` dengan `NAMA` berawalan huruf atau `_`, berisi huruf, angka, atau `_` |
| Sumber | Env proses, lalu `secrets.env` |
| Nama tidak ditemukan | Error: `variabel ${NAMA} tidak ditemukan` dengan key `mcp.<nama>.env.<KEY>` |
| Hasil | Bertipe `core.Secret` |
| Di field lain | Teks biasa. `${HOME}` tetap `${HOME}` |
| `${}` kosong atau nama tidak valid | Error validasi |
| Escape | Tidak ada. `${` selalu dianggap variabel di lokasi berlaku |

## API-CONF-012: Format pesan error config

Setiap error adalah `config.Error` (`API-CONF-014`). Format `Error()`:

| Kondisi | Format |
|---|---|
| `File` dan `Line` terisi, `Key` terisi | `<file>:<baris>: <key>: <pesan>` |
| `File` dan `Line` terisi, `Key` kosong | `<file>:<baris>: <pesan>` |
| `File` terisi, `Line` nol, `Key` terisi | `<file>: <key>: <pesan>` |
| `File` kosong | `<key>: <pesan>`, atau `<pesan>` jika `Key` kosong |

Contoh (sesuai `API-CLI-009` spec #1):

```text
/Users/ridho/.lana/config.toml:12: agents.coder.provider: provider 'anthropik' tidak terdaftar (tersedia: openai)
/Users/ridho/.lana/config.toml:4: agents.coder.permision: key tidak dikenal (maksud Anda: permission?)
profile tidak dikenal: xyz (tersedia: headless, minimal)
```

Pesan validasi yang wajib (teks tetap):

| Kondisi | Pesan |
|---|---|
| Sintaks TOML | Pesan parser TOML, dengan baris dan kolom |
| Tipe salah | `harus <tipe>, dapat <tipe>` |
| Key tidak dikenal | `key tidak dikenal` dan, jika ada, `(maksud Anda: <key>?)` |
| Provider tidak ada | `provider '<nama>' tidak terdaftar (tersedia: <daftar>)` |
| Route agent tidak ada | `agent '<nama>' tidak terdaftar (tersedia: <daftar>)` |
| Subagent tidak ada | `subagent '<nama>' tidak terdaftar` |
| MCP tidak ada | `server mcp '<nama>' tidak terdaftar` |
| Modul tidak dikenal | `modul '<nama>' tidak dikenal (tersedia: <daftar>)` |
| `id` duplikat di satu lapisan | `id '<id>' sudah dipakai di baris <baris>` |
| Enum tidak valid | `nilai '<nilai>' tidak valid (pilihan: <daftar>)` |
| `after` atau `before` tidak ada | `id '<id>' tidak ada` |
| Versi lebih tinggi | `versi config <n> lebih tinggi dari yang didukung (1)` |
| `${}` tidak ditemukan | `variabel ${<NAMA>} tidak ditemukan` |

- Daftar `<daftar>` diurutkan alfabet. Saran key memakai satu key dengan jarak edit terdekat ≤ 2 di tabel yang sama.
- Semua error dari semua lapisan dikembalikan sekaligus (`AC-CONF-009`).

## API-CONF-013: Peta asal (origins)

- Setiap path key berikut ada di peta:

| Jenis path key | Contoh | Nilai |
|---|---|---|
| Tabel | `providers.openai` | Nama lapisan yang menulis tabel itu |
| Key daun | `agents.x.model` | Nama lapisan yang terakhir menulisnya |
| Baris modul | `modules.store` | Nama lapisan yang menulis atau mengubah baris |
| Daftar `routes` | `routes` | Nama lapisan yang terakhir menulisnya |
| Key di dalam map secret | `mcp.github.env.GITHUB_TOKEN` | Nama lapisan |
| Versi | `version` | Nama lapisan |

- Nilai asal adalah nama lapisan (`API-CONF-001`).
- Peta asal menyimpan nilai dari lapisan yang terakhir menulisnya.

## API-CONF-014: Go API `config`

```go
package config

import (
	"context"

	"github.com/ridhomujizat/lana-harness/core"
)

// Options mengatur pembacaan config. Field kosong memakai default.
type Options struct {
	Home    string                            // "" = tanpa file dari LANA_HOME
	Profile string                            // "" = "minimal"
	Patches []string                          // path file --patch, berurutan
	Layers  []Layer                          // lapisan memori, setelah Patches
	Env     func(name string) (string, bool) // nil = os.LookupEnv
}

// Layer adalah lapisan dari memori. Isi TOML atau Value, tepat satu.
type Layer struct {
	Name  string // wajib; dipakai sebagai origin dan lokasi error
	TOML  []byte // teks TOML
	Value any    // struct Go; di-marshal ke TOML lalu diproses sama seperti TOML
}

// Load membangun config efektif. Error validasi dikembalikan sebagai Errors.
// Error lain (misal ctx dibatalkan) dikembalikan apa adanya.
func Load(ctx context.Context, opts Options) (*Result, error)

// Result adalah hasil Load yang valid.
type Result struct {
	Profile  string
	Home     string           // "" jika Options.Home kosong
	Config   core.Config      // config efektif
	Origins  Origins
	Layers   []LayerInfo      // semua lapisan dalam urutan, termasuk yang tidak ada
	Warnings []Warning
	Secrets  core.Secrets
}

// LayerInfo menjelaskan satu lapisan.
type LayerInfo struct {
	Name    string // "base", "profile:<nama>", path absolut, atau Layer.Name
	Path    string // "" untuk lapisan tanpa file
	Present bool
}

// Origins memetakan path key ke nama lapisan (API-CONF-013).
type Origins map[string]string

// Of mengembalikan nama lapisan untuk path key.
func (o Origins) Of(key string) (layer string, ok bool)

// Warning adalah peringatan yang tidak menggagalkan Load.
type Warning struct {
	File string
	Msg  string
}

// Error adalah satu kesalahan validasi (API-CONF-012).
type Error struct {
	File string
	Line int // 0 jika tidak diketahui
	Col  int // 0 jika tidak diketahui
	Key  string
	Msg  string
}

func (e Error) Error() string

// Errors adalah kumpulan semua error dari semua lapisan, urut lapisan lalu baris.
type Errors []Error

func (e Errors) Error() string // baris dipisah "\n"

// HomeDir mengembalikan LANA_HOME atau ~/.lana.
func HomeDir(env func(name string) (string, bool)) (string, error)

// ProfileNames mengembalikan nama profile bawaan, diurutkan alfabet.
func ProfileNames() []string
```

Aturan:

- `Load` tidak menulis ke stdout atau stderr. Peringatan dikembalikan di `Result.Warnings`. Caller (spec #1) mencetaknya ke stderr.
- `Load` tidak membaca file jika `Home` kosong, kecuali `Patches`.
- Patch yang tidak ada: `Errors` berisi `Error{Msg: "file patch tidak ditemukan: <path>"}`.

## API-CORE-001: `Module`, `ModuleConfig`, `RegisterModule`

```go
package core

// Module adalah unit fitur yang dikompilasi ke binary.
type Module interface {
	ID() string                 // nama implementasi, sama dengan nama yang didaftarkan
	Requires() []ServiceKey     // service yang harus sudah dipasang sebelum Install
	Provides() []ServiceKey     // service yang dipasang modul ini
	Install(b *Builder, cfg ModuleConfig) error
	Stop(ctx context.Context) error
}

// ModuleConfig adalah isi satu baris [[modules]] setelah merge.
type ModuleConfig struct {
	ID     string         // id baris, unik di config efektif
	Module string         // nama implementasi
	Values map[string]any // tabel config baris
}

// Decode menyalin Values ke struct tujuan lewat TOML.
func (c ModuleConfig) Decode(into any) error

// RegisterModule mendaftarkan factory modul. Nama duplikat memicu panic.
// Dipanggil dari init() atau sebelum Boot.
func RegisterModule(name string, factory func() Module)
```

- Factory dipanggil sekali per baris aktif. Instance dipakai untuk satu boot.
- `ID()` mengembalikan nama implementasi. Pesan error memakai `id` baris (`ModuleConfig.ID`).

## API-CORE-002: `Builder`, `Boot`, `Shutdown`

```go
// BootInput adalah masukan Boot.
type BootInput struct {
	Config  Config   // config efektif dari config.Load
	Secrets Secrets  // dari config.Result.Secrets
	Profile string
}

// Boot membuat Context. Tidak ada modul yang di-install jika rencana boot gagal.
func Boot(ctx context.Context, in BootInput) (*Context, error)

// ShutdownTimeout adalah batas total Shutdown.
const ShutdownTimeout = 30 * time.Second

// Shutdown menghentikan modul dalam urutan kebalikan install.
func (c *Context) Shutdown(ctx context.Context) error

// Builder dipakai modul saat Install. Tidak dipakai setelah Boot selesai.
type Builder struct{ /* tidak diekspor */ }

// Provide memasang service. Error jika key sudah dipasang modul lain.
func (b *Builder) Provide(key ServiceKey, svc any) error

// Lookup mengambil service yang sudah dipasang modul sebelumnya.
func (b *Builder) Lookup(key ServiceKey) (any, bool)

// Own mendaftarkan fungsi release (dari Use hook). Dipanggil saat modul berhenti.
func (b *Builder) Own(release func())

// Hooks mengembalikan hook yang dipakai bersama.
func (b *Builder) Hooks() *Hooks

// Config mengembalikan salinan config efektif (baca saja).
func (b *Builder) Config() Config
```

Urutan boot:

1. Baca baris aktif (`Disabled = false`). Buat instance modul lewat factory.
2. Validasi rencana: nama modul dikenal, service tiap `Requires` dipasok, tidak ada `Provides` ganda, tidak ada siklus.
3. Urutkan topologi, stabil menurut urutan baris (`AC-MOD-001`, `AC-MOD-002`).
4. Install setiap modul berurutan. Jika `Install` gagal, modul yang sudah di-install di-`Stop` terbalik, lalu Boot gagal.
5. Bangun `Context`. Field service inti diisi dari `Provide`.

Error boot (teks tetap, menyebut modul dan service):

| Kondisi | Pesan |
|---|---|
| Modul tidak dikenal | `modul '<nama>' (baris <id>) tidak dikenal (tersedia: <daftar>)` |
| Service tidak dipasang | `modul <id> membutuhkan service '<key>' yang tidak dipasang modul aktif` |
| Service ganda | `service '<key>' dipasang lebih dari satu modul: <id1>, <id2>` |
| Siklus | `siklus dependency: <id1> → <id2> → <id1>` |
| `Install` gagal | `modul <id> gagal dipasang: <err>` |

Shutdown:

- Modul di-`Stop` dalam urutan kebalikan install.
- Batas total = `ShutdownTimeout` atau sisa deadline `ctx`, mana yang lebih kecil.
- Modul yang belum selesai saat batas habis dicatat sebagai peringatan `slog` dan ditinggalkan.
- Modul berikutnya tetap dipanggil `Stop`, dengan `ctx` yang sudah habis. Pemanggilan itu tidak ditunggu.
- Setelah semua modul berhenti, listener hook yang masih terdaftar dilepas dan `Observe` ditutup.

## API-CORE-003: `Context` dan `ServiceKey`

```go
// ServiceKey adalah nama service di registry Builder.
type ServiceKey string

const (
	ServiceLLM      ServiceKey = "llm"
	ServiceTools    ServiceKey = "tools"
	ServiceSessions ServiceKey = "sessions"
	ServiceAgents   ServiceKey = "agents"
	ServiceApproval ServiceKey = "approval"
	ServiceCompaction ServiceKey = "compaction" // dipasang modul compaction (spec #4)
)

// Context adalah wiring hasil boot. Tidak bisa diubah setelah Boot.
type Context struct {
	LLM      LLMRegistry      // nil jika tidak ada modul yang memasangnya
	Tools    ToolRegistry
	Sessions SessionStore
	Agents   AgentRegistry
	Approval ApprovalService
	Hooks    *Hooks
	Config   Config           // salinan config efektif
	Secrets  Secrets
	Profile  string
}

// Lookup mengambil service apa pun (termasuk key di luar lima field di atas).
func (c *Context) Lookup(key ServiceKey) (any, bool)
```

- Service selain lima field di atas (misalnya `compaction`) dipasang dengan `Provide` dan diambil dengan `Lookup`. Key `compaction` ditetapkan di `ServiceCompaction` (`CQ-027`). Key lain ditetapkan spec #3 dan #4.

## API-CORE-004: Hook generik

```go
// Next meneruskan ke listener berikutnya, atau ke handler default di ujung rantai.
type Next[I, O any] func(ctx context.Context, in I) (O, error)

// WaterfallListener menerima input dan next.
type WaterfallListener[I, O any] func(ctx context.Context, in I, next Next[I, O]) (O, error)

// SerialListener menerima input dan mengembalikan satu hasil.
type SerialListener[I, O any] func(ctx context.Context, in I) (O, error)

// UseOption mengatur registrasi listener.
type UseOption func(*useOptions)

// WithPrepend mendaftarkan listener sebelum listener biasa.
func WithPrepend() UseOption

// Waterfall adalah middleware berurutan dengan handler default.
type Waterfall[I, O any] struct{ /* tidak diekspor */ }

func NewWaterfall[I, O any]() *Waterfall[I, O]

// SetDefault mengganti handler default. Tanpa default, Call mengembalikan ErrNoDefault.
func (w *Waterfall[I, O]) SetDefault(h func(ctx context.Context, in I) (O, error))

// Use mendaftarkan listener. Release menghapus listener itu.
func (w *Waterfall[I, O]) Use(l WaterfallListener[I, O], opts ...UseOption) (release func())

// Call menjalankan rantai. Hasil listener pertama yang tidak memanggil next dikembalikan.
func (w *Waterfall[I, O]) Call(ctx context.Context, in I) (O, error)

// Serial memanggil semua listener berurutan dan mengumpulkan hasilnya.
type Serial[I, O any] struct{ /* tidak diekspor */ }

func NewSerial[I, O any]() *Serial[I, O]

// Use mendaftarkan listener. Release menghapus listener itu.
func (s *Serial[I, O]) Use(l SerialListener[I, O]) (release func())

// Call mengembalikan hasil per listener sesuai urutan. Error pertama menghentikan rantai.
func (s *Serial[I, O]) Call(ctx context.Context, in I) ([]O, error)

// Observe mengirim event ke semua subscriber tanpa menunggu.
type Observe[T any] struct{ /* tidak diekspor */ }

func NewObserve[T any]() *Observe[T]

// Publish mengirim v ke setiap subscriber. Tidak pernah memblokir.
func (o *Observe[T]) Publish(v T)

// Subscribe membuat subscriber dengan buffer ObserveBuffer.
func (o *Observe[T]) Subscribe() *Subscription[T]

// Close menutup semua subscriber.
func (o *Observe[T]) Close()

// ObserveBuffer adalah jumlah event per subscriber.
const ObserveBuffer = 1024

// Received adalah hasil Recv. Lagged > 0 berarti Value belum terisi; panggil Recv lagi.
type Received[T any] struct {
	Lagged int // jumlah event yang terbuang sebelum event berikutnya
	Value  T
}

// Subscription adalah pembaca untuk satu subscriber.
type Subscription[T any] struct{ /* tidak diekspor */ }

// Recv menunggu event berikutnya. Error ErrClosed setelah Close, atau ctx.Err().
func (s *Subscription[T]) Recv(ctx context.Context) (Received[T], error)

// Close melepas subscriber.
func (s *Subscription[T]) Close()
```

- `Use` dan `Subscribe` aman dipanggil dari banyak goroutine. Daftar listener disalin saat `Call` dimulai.
- `Observe` mengurangi buffer dengan membuang event tertua. Hitungan buang disimpan sampai `Recv` membacanya sebagai `Lagged`.

## API-CORE-005: Hook bernama dan tipe input-output

Daftar hook di `Hooks` (ADR 0002). Tipe I dan O di bawah adalah tipe final. Definisi tiap tipe ada
di `API-CORE-015` (tipe gabungan). Perilaku default dimiliki spec #3 dan #4.

| Field | Tipe | Mode | Input (I) | Output (O) | Default |
|---|---|---|---|---|---|
| `AgentPreStep` | `*Waterfall[PreStepInput, PreStepDecision]` | waterfall | `PreStepInput` | `PreStepDecision` | Spec #3 |
| `AgentRequest` | `*Waterfall[RequestControl, RequestControl]` | waterfall | `RequestControl` | `RequestControl` | Spec #3 |
| `LLMStream` | `*Waterfall[LLMStreamInput, ChatStream]` | waterfall | `LLMStreamInput` | `ChatStream` | Provider (spec #3). Retry (spec #3), compaction dan retry `ErrContextTooLong` (spec #4) |
| `ToolsPreExecute` | `*Waterfall[ToolsPreExecuteInput, ToolDecision]` | waterfall | `ToolsPreExecuteInput` | `ToolDecision` | Spec #3 (fail-closed). Permission di spec #4 |
| `ToolsExecute` | `*Waterfall[ToolsExecuteInput, ToolOutput]` | waterfall | `ToolsExecuteInput` | `ToolOutput` | Spec #3 |
| `ToolsPostExecute` | `*Waterfall[ToolsPostExecuteInput, ToolOutput]` | waterfall | `ToolsPostExecuteInput` | `ToolOutput` | Spec #3 |
| `ApprovalRequest` | `*Waterfall[ApprovalRequest, ApprovalOutcome]` | waterfall | `ApprovalRequest` | `ApprovalOutcome` | `unavailable` (fail-closed). Penjawab di spec #4 |
| `AgentTurnStopping` | `*Serial[TurnStopInput, StopVote]` | serial | `TurnStopInput` | `StopVote` | Tidak ada |
| `SessionEvent` | `*Observe[SessionEvent]` | observe | `SessionEvent` | — | Tidak ada |

```go
// Hooks adalah kumpulan hook bertipe. Dibuat oleh Boot.
type Hooks struct {
	AgentPreStep      *Waterfall[PreStepInput, PreStepDecision]
	AgentRequest      *Waterfall[RequestControl, RequestControl]
	LLMStream         *Waterfall[LLMStreamInput, ChatStream]
	ToolsPreExecute   *Waterfall[ToolsPreExecuteInput, ToolDecision]
	ToolsExecute      *Waterfall[ToolsExecuteInput, ToolOutput]
	ToolsPostExecute  *Waterfall[ToolsPostExecuteInput, ToolOutput]
	ApprovalRequest   *Waterfall[ApprovalRequest, ApprovalOutcome]
	AgentTurnStopping *Serial[TurnStopInput, StopVote]
	SessionEvent      *Observe[SessionEvent]
}
```

- Tool yang ditolak listener `ToolsPreExecute` mengembalikan `ToolDecision{Allowed: false, Status: ToolStatusRejected}`. Tidak ada error sentinel untuk penolakan.
- Tipe `ChatStream`, `TurnStopInput`, `StopVote`, dan semua tipe input hook lain ada di `API-CORE-015`.


## API-CORE-006: Pesan, `ChatRequest`, `ChatEvent`, `Provider`

```go
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message adalah satu pesan. Hasil tool dikirim sebagai blok tool_result di pesan user.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

type BlockKind string

const (
	BlockText       BlockKind = "text"
	BlockToolUse    BlockKind = "tool_use"
	BlockToolResult BlockKind = "tool_result"
	BlockImage      BlockKind = "image"
)

// ContentBlock adalah satu blok isi. Field yang tidak relevan untuk Kind kosong.
type ContentBlock struct {
	Kind      BlockKind       `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use: call id; tool_result: tool_use_id
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // tool_use
	Output    string          `json:"output,omitempty"`      // tool_result
	IsError   bool            `json:"is_error,omitempty"`    // tool_result
	MediaType string          `json:"media_type,omitempty"`  // image
	Data      []byte          `json:"data,omitempty"`        // image, base64 di JSON
}

// Usage adalah jumlah token.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// ChatRequest adalah permintaan ke provider.
type ChatRequest struct {
	Model       string     // "<nama model>" tanpa prefix provider
	System      string
	Messages    []Message
	Tools       []ToolSpec
	MaxTokens   int
	Temperature *float64
}

type ChatEventKind string

const (
	ChatTextDelta ChatEventKind = "text_delta"
	ChatBlock     ChatEventKind = "block"  // blok lengkap (tool_use atau text)
	ChatUsage     ChatEventKind = "usage"
	ChatStop      ChatEventKind = "stop"
)

// ChatEvent adalah satu potongan stream. Field sesuai Kind.
type ChatEvent struct {
	Kind       ChatEventKind
	Text       string       // text_delta
	Block      ContentBlock // block
	Usage      Usage        // usage
	StopReason string       // stop: end_turn, tool_use, max_tokens, atau nilai provider
}

// Provider adalah adapter model. Kesalahan sebelum stream dimulai dikembalikan dari Stream.
// Kesalahan di tengah stream dikirim sebagai error di iterator.
type Provider interface {
	Stream(ctx context.Context, req ChatRequest) (ChatStream, error)
}

// LLMRegistry menyimpan provider berdasarkan nama di [providers.<nama>].
type LLMRegistry interface {
	Provider(name string) (Provider, bool)
	Names() []string
}
```

## API-CORE-007: `Tool`, `Risk`, `ToolRegistry`

```go
// Risk adalah kelas risiko tool.
type Risk string

const (
	RiskRead    Risk = "read"
	RiskWrite   Risk = "write"
	RiskExec    Risk = "exec"
	RiskNetwork Risk = "network"
)

// ToolSpec adalah deskripsi tool untuk model.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolCtx adalah konteks satu pemanggilan tool.
type ToolCtx struct {
	SessionID string
	TurnID    string
	CallID    string
	AgentName string
	Workspace string
}

// ToolStatus adalah status hasil tool (sama dengan status tool/result di spec #1).
type ToolStatus string

const (
	ToolStatusSuccess  ToolStatus = "success"
	ToolStatusError    ToolStatus = "error"
	ToolStatusRejected ToolStatus = "rejected"
)

// ToolOutput adalah hasil tool. Status error berarti tool gagal, rejected berarti ditolak gerbang.
type ToolOutput struct {
	Status ToolStatus
	Output string // teks untuk model; dipotong sesuai batas tool
}

// ToolCall adalah satu pemanggilan tool. Risk diisi sebelum hook ToolsPreExecute.
type ToolCall struct {
	SessionID string
	TurnID    string
	CallID    string
	Name      string
	Input     json.RawMessage
	Risk      Risk
}

// Tool adalah tool yang bisa dipanggil model.
type Tool interface {
	Spec() ToolSpec
	Risk(input json.RawMessage) Risk
	Call(ctx context.Context, tc ToolCtx, input json.RawMessage) (ToolOutput, error)
}

// ToolRegistry menyimpan tool berdasarkan nama.
type ToolRegistry interface {
	Register(t Tool) error                          // error jika nama sudah ada
	Get(name string) (Tool, bool)
	Specs(names []string) ([]ToolSpec, error)       // error jika ada nama tidak terdaftar
}
```

## API-CORE-008: `SessionEvent`, kind, dan payload

```go
// PayloadVersion adalah nilai field v di setiap payload (ADR 0009).
const PayloadVersion = 1

// SessionEvent adalah satu baris session_events.
type SessionEvent struct {
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	TurnID    string          `json:"turn_id,omitempty"`
	CreatedAt int64           `json:"created_at"` // unix ms
}

// EventDraft adalah event sebelum ditulis. Payload di-marshal ke JSON.
type EventDraft struct {
	Kind    string
	TurnID  string
	Payload any
}

const (
	KindSessionCreated   = "session/created"
	KindTurnStart        = "turn/start"
	KindTurnEnd          = "turn/end"
	KindStepStart        = "step/start"
	KindStepEnd          = "step/end"
	KindSystemMessage    = "system/message"
	KindUserMessage      = "user/message"
	KindAssistantMessage = "assistant/message"
	KindAssistantAttempt = "assistant/attempt"
	KindToolCall         = "tool/call"
	KindToolResult       = "tool/result"
	KindApprovalAsked    = "approval/asked"
	KindApprovalDecided  = "approval/decided"
	KindApprovalPolicy   = "approval/policy"
	KindModelSelection   = "model/selection"
	KindCompactionSummary = "compaction/summary"
)
```

Payload per kind (semua field JSON wajib, `v` = `PayloadVersion`):

| Kind | Struct | Field JSON |
|---|---|---|
| `session/created` | `SessionCreated` | `v`, `agent`, `channel`, `address`, `workspace`, `parent_session_id`, `parent_call_id`, `inherited_seq` |
| `turn/start` | `TurnStart` | `v`, `turn_id` |
| `turn/end` | `TurnEnd` | `v`, `turn_id`, `reason` (`end_turn`, `max_steps`, `cancelled`, `interrupted`, `error`, `budget`; `budget` dipakai mulai spec #5, ADR 0015), `error` (`PayloadError`, `null` jika tidak ada) |
| `step/start` | `StepStart` | `v`, `step`, `provider`, `model` |
| `step/end` | `StepEnd` | `v`, `step`, `provider`, `model`, `usage` (`Usage`) |
| `system/message` | `SystemMessage` | `v`, `text` |
| `user/message` | `UserMessage` | `v`, `content` (`[]ContentBlock`) |
| `assistant/message` | `AssistantMessage` | `v`, `content` (`[]ContentBlock`), `stop_reason` |
| `assistant/attempt` | `AssistantAttempt` | `v`, `step`, `attempt`, `provider`, `model`, `error` (`PayloadError`) |
| `tool/call` | `ToolCallPayload` | `v`, `call_id`, `name`, `input` |
| `tool/result` | `ToolResultPayload` | `v`, `call_id`, `status` (`success`, `error`, `rejected`), `output` |
| `approval/asked` | `ApprovalAsked` | `v`, `request_id`, `call_id`, `tool`, `risk`, `label` (opsional). Tanpa argumen |
| `approval/decided` | `ApprovalDecided` | `v`, `request_id`, `call_id`, `outcome`, `reason` (opsional) |
| `approval/policy` | `ApprovalPolicy` | `v`, `policy` |
| `model/selection` | `ModelSelection` | `v`, `provider`, `model`, `agent` (opsional, hanya dari `SetAgent`) |
| `compaction/summary` | `CompactionSummary` | `v`, `summary`, `upto_seq`, `tokens_before`, `tokens_after`, `model` |

- Tabel ini adalah sumber tunggal field payload. Spec #3 (`API-AGENT-008`) dan spec #4 (tabel event) merujuk ke sini (`CQ-029`).
- Field `error` memakai `PayloadError` (`API-CORE-015`).
- Payload `approval/*` tidak membawa argumen tool (ADR 0004). Argumen dibaca dari `tool/call` lewat `call_id`.
- Nama struct Go memakai akhiran `Payload` jika bentrok dengan tipe lain (`ToolCallPayload`, `ToolResultPayload`).
- Field `v` di-set ke `PayloadVersion` saat menulis. Migrasi versi lama dilakukan saat decode (ADR 0009).

## API-CORE-009: `SessionStore`, `SessionInfo`, `NewSession`, `SessionFilter`

```go
// SessionInfo adalah proyeksi session.
type SessionInfo struct {
	ID              string
	Agent           string
	Channel         string
	Address         string
	Workspace       string // absolut, sudah filepath.EvalSymlinks
	Title           string
	Status          string // "idle" atau "running"
	ParentSessionID string
	InheritedSeq    int64 // 0 jika bukan fork
	LastSeq         int64
	TurnCount       int64
	CreatedAt       int64 // unix ms
	UpdatedAt       int64 // unix ms
}

// NewSession adalah input Create.
type NewSession struct {
	Agent           string
	Channel         string
	Address         string
	Workspace       string // absolut; wajib
	ParentSessionID string // kosong jika bukan child
	ParentCallID    string // kosong jika bukan child subagent
	InheritedSeq    int64
}

// SessionFilter membatasi List.
type SessionFilter struct {
	Channel string
	Address string
	Limit   int // 0 = tanpa batas; urut updated_at terbaru
}

// SessionStore adalah event log append-only (ADR 0009).
type SessionStore interface {
	Create(ctx context.Context, s NewSession) (SessionInfo, error)
	Open(ctx context.Context, id string) (SessionInfo, error) // dengan resume repair, butuh lease
	Info(ctx context.Context, id string) (SessionInfo, error) // baca saja, tanpa repair
	Append(ctx context.Context, id string, drafts []EventDraft) ([]SessionEvent, error)
	Events(ctx context.Context, id string, fromSeq int64) iter.Seq2[SessionEvent, error]
	ChainEvents(ctx context.Context, id string) ([]SessionEvent, error) // rantai induk dipotong di inherited_seq
	DeriveMessages(ctx context.Context, id string) ([]Message, error)   // ChainEvents lalu DeriveMessages (API-CORE-015)
	List(ctx context.Context, f SessionFilter) ([]SessionInfo, error)
	FindPrefix(ctx context.Context, prefix string) ([]SessionInfo, error) // tidak peka huruf besar-kecil
	Fork(ctx context.Context, parentID string, atSeq int64) (SessionInfo, error)
	// TryLease mengambil kunci session. release melepasnya. LeaseHeldError jika dipegang proses lain.
	TryLease(ctx context.Context, id string) (release func() error, err error)
}
```

- `Events` mengembalikan event dengan `seq >= fromSeq`.
- `Append` menulis drafts dalam satu transaksi dan mengembalikan event yang sudah diberi `Seq`.
- `Fork` mengisi `Agent`, `Channel`, `Address`, dan `Workspace` dari induk. `ParentSessionID` diisi induk dan `InheritedSeq` = `atSeq`. Fork tidak mengganti agent.
- `ResolvePrefix` (`API-CORE-015`) menerapkan aturan prefix: minimal 6 karakter dan tepat satu cocok.
- `Info`, `ChainEvents`, dan `DeriveMessages` ditambahkan spec #4 (`CQ-022`, `CQ-028`).


## API-CORE-010: `AgentRegistry`, `LiveFrame`

```go
// LiveFrame adalah frame sementara (streaming), tidak ditulis ke log.
type LiveFrame struct {
	SessionID string
	TurnID    string
	Kind      string // "text_delta"
	Text      string
}

// AgentRegistry mengatur agent hidup dan inbox-nya.
type AgentRegistry interface {
	Send(ctx context.Context, sessionID string, text string) error
	Cancel(ctx context.Context, sessionID string) error
	SetAgent(ctx context.Context, sessionID string, agent string) error
	SetModel(ctx context.Context, sessionID string, model string) error // "<provider>:<model>"
	SubscribeLive(sessionID string) (*Subscription[LiveFrame], error)
}
```

## API-CORE-011: `ApprovalService`, `Policy`, `ApprovalOutcome`

```go
// Policy adalah mode approval session.
type Policy string

const (
	PolicyTrust Policy = "trust"
	PolicyAsk   Policy = "ask"
	PolicyNever Policy = "never"
)

// ApprovalOutcome adalah jawaban approval. Selain allowed_once dan allowed_session, tool ditolak (fail-closed).
type ApprovalOutcome string

const (
	OutcomeAllowedOnce    ApprovalOutcome = "allowed_once"
	OutcomeAllowedSession ApprovalOutcome = "allowed_session"
	OutcomeRejected       ApprovalOutcome = "rejected"
	OutcomeCancelled      ApprovalOutcome = "cancelled"
	OutcomeUnavailable    ApprovalOutcome = "unavailable"
)

// ApprovalRequest adalah permintaan approval untuk satu pemanggilan tool. Tidak membawa argumen (ADR 0004).
type ApprovalRequest struct {
	SessionID string
	TurnID    string
	CallID    string // rujukan ke tool/call
	AgentName string
	ToolName  string
	Risk      Risk
	Label     string // nama agent child jika session punya induk, kosong jika bukan
}

// ApprovalService menjalankan approval lewat hook ApprovalRequest.
type ApprovalService interface {
	Request(ctx context.Context, req ApprovalRequest) (ApprovalOutcome, error)
	SetPolicy(ctx context.Context, sessionID string, p Policy) error
	EffectivePolicy(ctx context.Context, sessionID string) (Policy, error)
}
```

## API-CORE-012: `Secret` dan `Secrets`

```go
// Secret menyimpan nilai rahasia. Semua format dan encoder menulis "***".
type Secret struct{ /* v tidak diekspor */ }

func NewSecret(v string) Secret

// Reveal mengembalikan nilai asli. Hanya dipakai saat mengirim ke pihak yang berhak (misal header HTTP provider).
func (s Secret) Reveal() string

func (s Secret) String() string                // "***"
func (s Secret) GoString() string              // "***"
func (s Secret) Format(f fmt.State, verb rune) // "***" untuk semua verb
func (s Secret) LogValue() slog.Value          // "***"
func (s Secret) MarshalJSON() ([]byte, error)  // "\"***\""
func (s Secret) MarshalText() ([]byte, error)  // "***"

// Secrets mengambil nilai secret berdasarkan nama env.
type Secrets interface {
	Lookup(name string) (Secret, bool)
}
```

## API-CORE-013: Tipe schema `Config`

```go
// Config adalah config efektif. Nilai secret bertipe Secret.
type Config struct {
	Version   int                       `toml:"version"`
	Providers map[string]ProviderConfig `toml:"providers"`
	Agents    map[string]AgentConfig    `toml:"agents"`
	Routes    []RouteConfig             `toml:"routes"`
	MCP       map[string]MCPConfig      `toml:"mcp"`
	Channels  map[string]map[string]any `toml:"channels"`
	Modules   []ModuleRow               `toml:"modules"`
}

type ProviderConfig struct {
	Kind      string `toml:"kind"`
	BaseURL   string `toml:"base_url"`
	APIKeyEnv string `toml:"api_key_env"`
}

type AgentConfig struct {
	Provider   string      `toml:"provider"`
	Model      string      `toml:"model"`
	Fallback   []string    `toml:"fallback"`
	PromptFile string      `toml:"prompt_file"`
	Workspace  string      `toml:"workspace"`
	Tools      []string    `toml:"tools"`
	Subagents  []string    `toml:"subagents"`
	MCP        []string    `toml:"mcp"`
	Skills     []string    `toml:"skills"`
	Permission string      `toml:"permission"`
	Rules      AgentRules  `toml:"rules"`
}

type AgentRules struct {
	Allow []string `toml:"allow"`
	Deny  []string `toml:"deny"`
}

type RouteConfig struct {
	Channel string         `toml:"channel"`
	Agent   string         `toml:"agent"`
	Match   map[string]any `toml:"match"`
}

type MCPConfig struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	URL     string            `toml:"url"`
	Env     map[string]Secret `toml:"env"`
	Headers map[string]Secret `toml:"headers"`
}

// ModuleRow adalah baris [[modules]] efektif.
type ModuleRow struct {
	ID       string         `toml:"id"`
	Module   string         `toml:"module"`
	Disabled bool           `toml:"disabled"`
	Config   map[string]any `toml:"config"`
}
```

- Tag `toml` dipakai untuk marshal config efektif (`--dump-config` di spec #1).
- Field `Env` dan `Headers` semua bertipe `Secret`. Nilai literal juga tampil `***` (lihat `Rekomendasi agent` di design).

## API-CORE-014: Error sentinel

```go
var (
	ErrNotFound              = errors.New("tidak ditemukan")
	ErrClosed                = errors.New("subscription ditutup") // lana.ErrClosed (spec #3) berbeda
	ErrNoDefault             = errors.New("handler default belum diset")
	ErrContextTooLong        = errors.New("context terlalu panjang")
	ErrPrefixTooShort        = errors.New("prefix session minimal 6 karakter")
	ErrAmbiguousPrefix       = errors.New("prefix session cocok lebih dari satu")
	ErrUnknownPayloadVersion = errors.New("versi payload tidak dikenal")
)

// LeaseHeldError dikembalikan TryLease saat session dipakai proses lain.
type LeaseHeldError struct {
	SessionID string
	PID       int
	Host      string
}

func (e *LeaseHeldError) Error() string
```

- `ErrContextTooLong` dibungkus `ProviderError` dengan class `context_length` (`API-CORE-015`).
- `ErrRejected` (versi sebelum revisi 2) dihapus. Penolakan tool memakai `ToolDecision` (`CQ-020`).

## API-CORE-015: Daftar tipe gabungan `core` (sumber tunggal)

Daftar ini memuat semua tipe `core` yang dipakai spec #2, #3, dan #4. Nama dan signature di sini
menang atas nama di spec lain. Spec #3 dan #4 merujuk ke ID ini dan tidak mendefinisikan ulang.

| Nama | Jenis | Bagian | Kebutuhan dari spec #2 | Kebutuhan dari spec #3 | Kebutuhan dari spec #4 |
|---|---|---|---|---|---|
| `Message`, `ContentBlock`, `BlockKind`, `Role`, `Usage` | Tipe | `API-CORE-006` | Ya | Ya | Ya |
| `ChatRequest`, `ChatEvent`, `ChatEventKind`, `Provider`, `LLMRegistry` | Tipe | `API-CORE-006` | Ya | Ya | — |
| `ChatStream` | Alias | `API-CORE-015` | Ya | Ya | — |
| `LLMTarget`, `LLMStreamInput` | Tipe | `API-CORE-015` | — | Ya | — |
| `RequestControl` | Tipe | `API-CORE-015` | — | Ya | — |
| `PreStepInput`, `PreStepDecision` | Tipe | `API-CORE-015` | — | Ya (menggantikan `StepInput`) | — |
| `TurnStopInput`, `StopVote` | Tipe | `API-CORE-015` | Ya | Ya (menggantikan `AgentTurnStopInput`, `TurnStopVote`) | — |
| `ToolSpec`, `ToolCtx`, `ToolCall`, `Tool`, `Risk`, `ToolRegistry` | Tipe | `API-CORE-007` | Ya | Ya | Ya |
| `ToolStatus` | Tipe dan konstanta | `API-CORE-007` | — | Ya | — |
| `ToolOutput` | Tipe | `API-CORE-007` | Ya (dengan `Status`) | Ya | — |
| `ToolDecision` | Tipe | `API-CORE-015` | — | Ya | Ya (gabungan, `CQ-020`) |
| `ToolsPreExecuteInput`, `ToolsExecuteInput`, `ToolsPostExecuteInput` | Tipe | `API-CORE-015` | — | Ya (`ToolPostInput` #2 diganti, `CQ-017`) | Ya (input listener permission) |
| `ToolPostInput` | Tipe | Dihapus | Ya (diganti) | — | — |
| `SessionEvent`, `EventDraft`, konstanta kind, payload struct, `PayloadVersion` | Tipe | `API-CORE-008` | Ya | Ya | Ya |
| `PayloadError` | Tipe | `API-CORE-015` | — | Ya | Ya |
| `SessionInfo`, `NewSession`, `SessionFilter`, `SessionStore` | Tipe dan interface | `API-CORE-009` | Ya | Ya | Ya (`Info`, `ChainEvents`, `DeriveMessages`) |
| `AgentRegistry`, `LiveFrame` | Interface dan tipe | `API-CORE-010` | Ya | Ya | Ya (`SubscribeLive`, `Cancel`) |
| `Policy`, `ApprovalOutcome`, `ApprovalRequest`, `ApprovalService` | Tipe dan interface | `API-CORE-011` | Ya | Ya | Ya |
| `CompactionResult`, `Compactor` | Tipe dan interface | `API-CORE-015` | — | — | Ya |
| `ProviderError` | Tipe | `API-CORE-015` | — | Ya | Ya |
| `Secret`, `Secrets`, `Config` dan turunannya | Tipe | `API-CORE-012`, `API-CORE-013` | Ya | Ya | — |
| `ServiceKey` (tambah `ServiceCompaction`) | Konstanta | `API-CORE-003` | Ya | — | Ya |
| `DeriveMessages` | Fungsi murni | `API-CORE-015` | — | Ya (subset tanpa compaction) | Ya (compaction, `CQ-028`) |
| `ResolvePrefix`, `AmbiguousPrefixError` | Fungsi dan tipe | `API-CORE-015` | — | — | Ya (`CQ-023`) |
| Sentinel error | Variabel | `API-CORE-014` | Ya | Ya | Ya |

```go
// ---- Input dan output hook ----

// PreStepInput adalah masukan AgentPreStep: satu pesan yang di-claim pada satu step.
type PreStepInput struct {
	SessionID string
	TurnID    string
	AgentName string
	Step      int
	Input     Message
}

// PreStepDecision adalah hasil AgentPreStep. Input = pesan hasil ubah listener (default: Input asli).
type PreStepDecision struct {
	Input  Message
	Reject bool
	Reason string
}

// RequestControl adalah kontrol request yang bisa diubah AgentRequest.
type RequestControl struct {
	MaxTokens   int
	Temperature *float64
}

// LLMTarget adalah satu target provider. Primary di indeks 0, lalu fallback berurutan.
type LLMTarget struct {
	Provider string
	Model    string
}

// LLMStreamInput adalah masukan LLMStream.
type LLMStreamInput struct {
	SessionID string
	TurnID    string
	Step      int
	AgentName string
	Targets   []LLMTarget
	Request   ChatRequest
}

// ChatStream adalah hasil stream provider.
type ChatStream = iter.Seq2[ChatEvent, error]

// ToolsPreExecuteInput adalah masukan ToolsPreExecute. Risk ada di Call.Risk.
type ToolsPreExecuteInput struct {
	AgentName string
	Call      ToolCall
}

// ToolDecision adalah keputusan ToolsPreExecute.
// Allowed false berarti Status = ToolStatusRejected. Outcome kosong jika tidak ada approval.
type ToolDecision struct {
	Allowed bool
	Outcome ApprovalOutcome
	Status  ToolStatus
	Reason  string // teks yang dibaca model
}

// ToolsExecuteInput adalah masukan ToolsExecute.
type ToolsExecuteInput struct {
	Ctx  ToolCtx
	Call ToolCall
	Tool Tool
}

// ToolsPostExecuteInput adalah masukan ToolsPostExecute.
type ToolsPostExecuteInput struct {
	Call   ToolCall
	Output ToolOutput
}

// TurnStopInput adalah masukan AgentTurnStopping.
type TurnStopInput struct {
	SessionID  string
	TurnID     string
	AgentName  string
	Steps      int
	StopReason string // alasan yang akan dipakai jika turn berhenti
}

// StopVote adalah suara satu listener AgentTurnStopping.
type StopVote struct {
	Continue bool
	Reason   string
}

// ---- Error provider ----

// ProviderError adalah error provider. Class: auth, rate_limit, server, network,
// context_length, client, missing_secret.
type ProviderError struct {
	Class   string
	Status  int // kode HTTP, 0 jika tidak ada
	Message string
}

func (e *ProviderError) Error() string

// Is membuat errors.Is(err, ErrContextTooLong) benar jika Class = context_length.
func (e *ProviderError) Is(target error) bool

// PayloadError adalah error di payload turn/end dan assistant/attempt.
type PayloadError struct {
	Class   string `json:"class"`
	Status  int    `json:"status,omitempty"`
	Message string `json:"message"`
}

// ---- Compaction ----

// CompactionResult adalah hasil CompactNow.
type CompactionResult struct {
	UptoSeq      int64
	TokensBefore int64
	TokensAfter  int64
}

// Compactor meringkas riwayat session saat diminta. Dipasang modul compaction (spec #4)
// dengan key ServiceCompaction.
type Compactor interface {
	CompactNow(ctx context.Context, sessionID string) (CompactionResult, error)
}

// ---- Sesi dan prefix ----

// AmbiguousPrefixError dikembalikan ResolvePrefix jika prefix cocok lebih dari satu session.
type AmbiguousPrefixError struct {
	Candidates []string // urut naik
}

func (e *AmbiguousPrefixError) Error() string

// Is membuat errors.Is(err, ErrAmbiguousPrefix) benar.
func (e *AmbiguousPrefixError) Is(target error) bool

// ResolvePrefix memilih satu session dari hasil FindPrefix. Prefix kurang dari 6 karakter
// menghasilkan ErrPrefixTooShort. Tanpa cocok, ErrNotFound. Lebih dari satu, *AmbiguousPrefixError.
func ResolvePrefix(matches []SessionInfo, prefix string) (SessionInfo, error)

// ---- Riwayat model ----

// DeriveMessages menyusun riwayat model dari rantai event. Fungsi murni tanpa I/O.
// Mengabaikan system/message yang bukan terakhir, assistant/attempt, dan tool/call.
// Spec #3 mengimplementasikan subset tanpa compaction. Spec #4 menambah compaction/summary.
func DeriveMessages(events []SessionEvent) ([]Message, error)
```


---

## Traceability contract

| Operasi | Requirements | Komponen design | Verifikasi |
|---|---|---|---|
| `API-CONF-001` | `REQ-CONF-001`, `REQ-CONF-006` | `config` loader (layer) | Test unit `config` dengan `Home` sementara |
| `API-CONF-002` | `REQ-CONF-003`, `REQ-CONF-008` | `config` validator | Test unit tabel error |
| `API-CONF-003` | `REQ-CONF-003` | `config` validator | Test unit |
| `API-CONF-004` | `REQ-CONF-002`, `REQ-CONF-003` | `config` merge dan validator | Test unit |
| `API-CONF-005` | `REQ-CONF-002`, `REQ-CONF-003` | `config` merge dan validator | Test unit |
| `API-CONF-006` | `REQ-CONF-003`, `REQ-CONF-007` | `config` interpolasi | Test unit |
| `API-CONF-007` | `REQ-CONF-002` | `config` merge | Test unit |
| `API-CONF-008` | `REQ-CONF-002`, `REQ-MOD-001` | `config` merge, `core` plan | Test unit |
| `API-CONF-009` | `REQ-PROF-001`, `REQ-CONF-001` | `config` profile table | Test unit |
| `API-CONF-010` | `REQ-CONF-005` | `config` secrets | Test unit |
| `API-CONF-011` | `REQ-CONF-007` | `config` interpolasi | Test unit |
| `API-CONF-012` | `REQ-CONF-003` | `config.Error` | Test unit |
| `API-CONF-013` | `REQ-CONF-004` | `config` origins | Test unit |
| `API-CONF-014` | `REQ-CONF-009` | `config.Load` | Test unit dan integrasi |
| `API-CORE-001` | `REQ-MOD-001`, `REQ-MOD-004` | `core` registry | Test unit |
| `API-CORE-002` | `REQ-MOD-001` sampai `REQ-MOD-004` | `core` boot dan shutdown | Test unit dengan modul palsu |
| `API-CORE-003` | `REQ-MOD-001`, `REQ-MOD-004` | `core` Context | Test unit |
| `API-CORE-004` | `REQ-HOOK-001` sampai `REQ-HOOK-004` | `core` hook generik | Test unit dan race |
| `API-CORE-012` | `REQ-CONF-005` | `core.Secret` | Test unit format |
| `API-CORE-013` | `REQ-CONF-003` | `core.Config` | Test unit |
| `API-CORE-014` | `REQ-MOD-002`, `REQ-HOOK-001` | `core` error | Test unit |
| `API-CORE-015` | Tidak berlaku (tipe) | `core` tipe gabungan, `DeriveMessages`, `ResolvePrefix` | Test kompilasi dan test unit di spec #3 dan #4 (`TestDeriveGolden`, `TestResolvePrefix`) |

## Blocking Contract Questions

| ID | Pertanyaan | Mengendalikan | Pemilik | Status | Resolusi |
|---|---|---|---|---|---|
| `CQ-001` | Posisi lapisan memori terhadap `--patch` | `API-CONF-001`, `API-CONF-014` | Ridho | Resolved | Rekomendasi agent: lapisan memori diterapkan setelah semua `Patches`, sesuai urutan slice `Layers`. |
| `CQ-002` | Arti `Options.Home` kosong | `API-CONF-014` | Ridho | Resolved | Rekomendasi agent: tidak ada file dari home yang dibaca. CLI memanggil `HomeDir` lebih dulu. Ini memenuhi `REQ-CONF-009`. |
| `CQ-003` | Profile kosong | `API-CONF-014` | Ridho | Resolved | Rekomendasi agent: sama dengan `minimal`. |
| `CQ-004` | Isi lapisan `base` dan profile bawaan | `API-CONF-009` | Ridho | Resolved | Rekomendasi agent: tabel di `API-CONF-009`. `minimal` tanpa baris tambahan, `headless` menambah `runner`. Nama modul mengikuti spec #3 dan #4. |
| `CQ-005` | Env yang ada tetapi kosong | `API-CONF-010`, `API-CONF-011` | Ridho | Resolved | Rekomendasi agent: nilai kosong dianggap tidak ada. Lookup lanjut ke `secrets.env`. |
| `CQ-006` | Secret untuk nilai literal di `mcp.*.env` | `API-CORE-013`, `API-CONF-006` | Ridho | Resolved | Rekomendasi agent: semua nilai `env` dan `headers` bertipe `Secret`. Nilai literal juga tampil `***`. Pilihan ini paling sederhana dan aman. |
| `CQ-007` | Siapa yang mencetak peringatan permission `secrets.env` | `API-CONF-010`, `API-CONF-014` | Ridho | Resolved | Rekomendasi agent: `Load` mengembalikan `Result.Warnings` dan mencatat `slog` warn. Spec #1 mencetak ke stderr. Library tidak menulis stderr. |
| `CQ-008` | Saran key terdekat | `API-CONF-012` | Ridho | Resolved | Rekomendasi agent: satu saran dengan jarak edit ≤ 2 di tabel yang sama. Jika tidak ada, tanpa saran. |
| `CQ-009` | Default `permission` agent | `API-CONF-004` | Ridho | Resolved | Rekomendasi agent: `ask`, sesuai prinsip fail-closed (ADR 0004). |
| `CQ-010` | Baris `secrets.env` yang tidak valid | `API-CONF-010` | Ridho | Resolved | Rekomendasi agent: error validasi dengan nomor baris. Baris lain tetap diproses. |
| `CQ-011` | `after` atau `before` untuk `id` yang sudah ada | `API-CONF-008` | Ridho | Resolved | Rekomendasi agent: diabaikan dan tetap divalidasi. `after` dan `before` bersamaan adalah error. |
| `CQ-012` | Isi `channels.*`, `routes.*.match`, dan `module.config` | `API-CONF-005`, `API-CONF-007`, `API-CONF-008` | Ridho | Resolved | Rekomendasi agent: opaque di spec ini. Validasi isinya ada di modul pemilik. |
| `CQ-013` | `fallback` dan `prompt_file` | `API-CONF-004` | Ridho | Resolved | Rekomendasi agent: hanya tipe array string dan string. Provider di `fallback` dan keberadaan file prompt dicek spec #3. |
| `CQ-014` | Escape untuk `${` literal | `API-CONF-011` | Ridho | Resolved | Rekomendasi agent: tidak ada escape. Di lokasi yang berlaku, `${` selalu variabel. |
| `CQ-015` | `--patch` yang tidak ada: dilewati (`REQ-CONF-001`) atau error (`API-CLI-001` spec #1) | `API-CONF-001`, `API-CONF-014` | Ridho | Resolved | Rekomendasi agent: error. Spec #1 sudah menetapkan exit 2 dan pesan `file patch tidak ditemukan`. Aturan "dilewati" di `REQ-CONF-001` dibaca untuk profile dan `config.toml`. Catatan ini masuk design. |
| `CQ-016` | Daftar tipe `core` gabungan: sumber tunggal dan nama | `API-CORE-005` sampai `API-CORE-015` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `API-CORE-015` adalah daftar gabungan. Untuk tipe yang sama, nama spec #2 dipakai. Signature spec #2 menang (lihat `CQ-022`). Kolom kebutuhan per spec mencatat pemakai. |
| `CQ-017` | Nama input hook tool | `API-CORE-005`, `API-CORE-015` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `ToolsPreExecuteInput`, `ToolsExecuteInput`, `ToolsPostExecuteInput`. `ToolPostInput` (spec #2) diganti agar tiga input tool seragam. Isi struct sama. |
| `CQ-018` | Input dan output hook step dan stop | `API-CORE-005`, `API-CORE-015` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `PreStepInput` dan `PreStepDecision` menggantikan `StepInput`, karena perlu `Reject` (spec #3 `Q-008`). `TurnStopInput` dan `StopVote` dari spec #2 dipakai. `AgentTurnStopInput` dan `TurnStopVote` dihapus. |
| `CQ-019` | Bentuk `ChatEvent` dan `ToolCall` | `API-CORE-006`, `API-CORE-007` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `ChatEvent` memakai bentuk spec #2 (`ChatBlock` membawa `Block ContentBlock`, `Usage` berupa struct). Kind `tool_use` dari spec #3 menjadi `ChatBlock` dengan `Block.Kind = tool_use`. `ToolCall` memakai field spec #2 (`SessionID`, `TurnID`, `CallID`, `Name`, `Input`, `Risk`). |
| `CQ-020` | Status tool dan `ToolDecision` | `API-CORE-007`, `API-CORE-015` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `ToolOutput{Status ToolStatus, Output}` menggantikan `IsError`. `ToolDecision{Allowed, Outcome, Status, Reason}` menggabungkan `Allow` (spec #3) dan `Allowed` (spec #4). Nama `Allowed` dipakai. `ErrRejected` dihapus, penolakan lewat `ToolDecision`. |
| `CQ-021` | Isi `ApprovalRequest` | `API-CORE-011` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): field spec #2 tanpa `Input` dan `Summary`, ditambah `Label` (spec #4). Nama tool memakai `ToolName`. Tanpa argumen (ADR 0004). |
| `CQ-022` | Signature `SessionStore` | `API-CORE-009` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): signature spec #2 menang. `Create` dan `Open` mengembalikan `SessionInfo`. `Events` mengembalikan iterator. `List` memakai `SessionFilter` dengan `Limit` 0 = tanpa batas. `FindPrefix` mengembalikan daftar. `Fork` tanpa argumen `in`: child mewarisi agent, channel, address, dan workspace induk. Ditambah dari spec #4: `Info`, `ChainEvents`, `DeriveMessages`. |
| `CQ-023` | `Lease` dan aturan prefix | `API-CORE-009`, `API-CORE-014`, `API-CORE-015` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `TryLease` mengembalikan `release func() error`. `core.Lease` dihapus. `LeaseHeldError` dari spec #2 dipakai dengan `errors.As`. Aturan prefix (minimal 6 karakter, tepat satu) di `core.ResolvePrefix` dengan `ErrPrefixTooShort`, `ErrNotFound`, dan `AmbiguousPrefixError`. |
| `CQ-024` | Tipe argumen `Append` | `API-CORE-009` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `EventDraft` dari spec #2 dipakai. `NewEvent` dari spec #4 dihapus. |
| `CQ-025` | Bentuk I/O hook dan bentuk minimum | `API-CORE-005` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): tipe I/O di `API-CORE-005` dinaikkan dari bentuk minimum ke tipe final. Spec #2 menyebut spec #3 dan #4 boleh mengisi perilaku. Perubahan tipe ini tercatat di sini dan di CHANGELOG sebelum v1.0.0 (ADR 0014). |
| `CQ-026` | `ProviderError` dan konteks terlalu panjang | `API-CORE-014`, `API-CORE-015` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `core.ProviderError{Class, Status, Message}` dan sentinel `core.ErrContextTooLong`. `Is(ErrContextTooLong)` benar untuk class `context_length`. Adapter spec #3 membuatnya. Compaction spec #4 memakai `errors.Is`. `lana.ProviderError` di spec #3 menjadi alias. |
| `CQ-027` | Compaction di `core` | `API-CORE-003`, `API-CORE-015` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `Compactor` dan `CompactionResult` di `core`. `ServiceCompaction = "compaction"` ditambah di `API-CORE-003`. `AgentRegistry` (spec #3) tidak punya `CompactNow`. |
| `CQ-028` | Derive riwayat di `core` | `API-CORE-009`, `API-CORE-015` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `core.DeriveMessages(events) ([]Message, error)` adalah fungsi murni tanpa I/O. Store membungkusnya: `ChainEvents` lalu `DeriveMessages`. Spec #3 mengimplementasikan subset tanpa compaction. Spec #4 menambah compaction dan tidak mengubah hasil log tanpa compaction. |
| `CQ-029` | Pemilik field payload event | `API-CORE-008` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `API-CORE-008` adalah tabel tunggal field payload. Spec #3 (`API-AGENT-008`) dan spec #4 (tabel event) merujuk ke sini. Penambahan: `session/created.workspace` dan `parent_call_id`, `model/selection.agent`, `turn/end.error`, `assistant/attempt.model`, `approval/*.request_id`, `compaction/summary.summary`. `system/message` memakai `text`. |
| `CQ-030` | Key config tambahan dan validasi pola `rules` | `API-CONF-003`, `API-CONF-004` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): key milik spec #3 dan #4 terdaftar di tabel "Key di bawah ini dikenal" pada `API-CONF-004`. Pola `rules` divalidasi lewat `internal/pattern.ParsePattern` setelah spec #4 selesai. Sebelum itu, hanya array string. |
| `CQ-031` | Profile `base` dan nama modul | `API-CONF-009` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): `base` berisi `llm`, `tools`, `store` (`sqlite_store`), `agent`, `permission`, `approval`, dan `compaction`. `minimal` sama dengan `base`. `headless` sama dengan `base` ditambah `cli_runner` (spec #1). Baris `prompt` dihapus. Nama modul memakai nama spec #3 dan #4. |
| `CQ-032` | Profile sebelum modul ada | `API-CONF-009` | Ridho | Resolved | Rekomendasi agent (rekonsiliasi): ini catatan urutan build, bukan profile baru. Sampai spec #4 dan `cli_runner` ada, profile bawaan gagal validasi `module`. Test spec #3 memakai `Layers` tanpa profile bawaan (lihat design spec #3). |

None yang masih Open.

## Approval

- [x] Requirements sudah Approved dan revisinya dicatat di atas.
- [x] Keberlakuan contract sudah ditentukan.
- [x] Setiap operasi punya ID stabil dan tertaut ke requirement dan acceptance criteria.
- [x] Input, sukses, gagal, otorisasi, dan kompatibilitas eksplisit.
- [x] Tidak ada perilaku yang hanya ada di file ini. Perilaku yang belum ada di requirements dicatat di `design.md` bagian Catatan untuk requirements.
- [x] Blocking Contract Questions bernilai `None`.
- **Di-approve oleh**: Ridho (gate dilepas 2026-10-29)
- **Di-approve pada**: 2026-10-29 (revisi 1). Revisi 2 rekonsiliasi `CQ-016` sampai `CQ-032`, oleh agent atas nama pemilik (gate dilepas 2026-10-29).
