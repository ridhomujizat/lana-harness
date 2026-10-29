# Requirements: Fondasi (workspace, profile loader, Context/modul/hook)

- **Issue**: [`#2`](https://github.com/ridhomujizat/lana-harness/issues/2)
- **Status**: Draft
- **Revisi**: 3 (pindah ke Go, ADR 0013)
- **Pemilik**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Diperlukan (format config dan profile, API Go publik package `core` dan `config`)
- **ADR terkait**: [Index ADR](../../adr/README.md) — [0013](../../adr/0013-gunakan-go-sebagai-single-binary.md), [0014](../../adr/0014-integrasi-sebagai-agentic-tool.md), [0002](../../adr/0002-core-dari-modul-service-dan-hook.md), [0010](../../adr/0010-profile-dan-config-patch.md)
- **Diagram terkait**: [Index diagram](../../diagram/README.md) — [0001](../../diagram/0001-system-context-dan-roadmap.md), [0004](../../diagram/0004-profile-fork-subagent-dan-protokol.md)

## Sumber

| Sumber | Referensi | Dipakai untuk |
|---|---|---|
| Issue | [`#2`](https://github.com/ridhomujizat/lana-harness/issues/2) | scope |
| ADR 0013 | Go, single binary dan library | toolchain, layout package, build, target ukuran dan platform |
| ADR 0014 | Integrasi agentic tool | `core` publik, config tanpa file untuk embed |
| ADR 0002 | Modul, service, hook | `Module`, `Context`, mode hook |
| ADR 0010 | Profile dan patch | urutan lapisan, aturan merge, profile bawaan, `--dump-config` |
| Spec #1 | [design: Interface core](../1-cli-tui/design.md#interface-core-yang-dibutuhkan-dari-spec-2) | `ProfileLoader` dengan peta asal dan error ber-span |
| Instruksi mesin | Nix development | `nix develop`, tanpa install global |
| Wawancara 2026-10-29 | Pembagian spec core menjadi #2, #3, #4 | scope |

## Tujuan

Developer (pemilik) punya fondasi yang bisa dibangun dan diuji: lingkungan dev yang
reprodusibel, Go module dengan aturan import antar package yang dijaga, loader konfigurasi berlapis yang
menjelaskan asal setiap nilai dan semua kesalahannya, serta runtime modul dan hook tempat fitur
lain (agent loop, channel, permission) dipasang.

## Aktor

- **Developer** — menjalankan `nix develop`, `go`, dan test.
- **Service Go** — meng-import `core` dan `config` saat meng-embed `lana` (ADR 0014).
- **Spec lain (#1, #3, #4)** — memakai package `core` dan `config` sebagai dependency.
- **Pemilik sebagai user** — menulis `config.toml` dan patch, membaca pesan error config.

## Definisi

- **Lapisan** — satu sumber config: base, profile bawaan, patch profile user, `config.toml`, atau `--patch`.
- **Baris modul** — satu entri `[[modules]]` dengan `id` unik.
- **Config efektif** — hasil merge semua lapisan.
- **Peta asal** — pasangan path key ke lapisan yang terakhir menetapkan nilainya.
- **Modul** — implementasi `Module` yang dikompilasi ke binary.
- **Service** — objek bertipe di `Context` yang dipasang oleh modul.
- **Hook** — titik intercept bertipe dengan mode `waterfall`, `serial`, atau `observe`.

## Scope

### Termasuk

- `flake.nix` dengan dev shell.
- Go module dengan layout package ADR 0013, aturan import, dan build release static.
- Package `core` dan `config` sebagai API publik.
- Profile loader: lapisan, merge, validasi, peta asal, secret.
- `Context`, `Module`, urutan install dari dependency, shutdown.
- Hook `waterfall`, `serial`, dan `observe` beserta semantiknya.
- Profile bawaan `minimal` dan `headless` sebagai daftar modul.

### Tidak termasuk

- Isi modul fitur (agent loop, provider, tools, store, permission, channel). Spec #3 dan #4.
- Subcommand CLI (`config check`, `--dump-config`). Spec #1 memakai loader dari spec ini.
- Profile `daemon`, `acp`, `sdk`. Ditambah oleh spec yang membawa modulnya.
- Plugin runtime (ADR 0002).

## Asumsi

- Developer memakai macOS arm64 atau Linux dengan Nix flakes aktif. — Sumber: instruksi mesin.
- Semua dependency dev disediakan Nix, bukan install global. — Sumber: instruksi mesin.

## Aturan perilaku

### Lingkungan dev dan build

#### REQ-DEV-001: Dev shell Nix

`nix develop` harus menyediakan Go, `gopls`, `golangci-lint`, dan `sqlite`. Versi Go dipin oleh
`flake.lock` (nixpkgs). Dev shell men-set `GOTOOLCHAIN=local` dan `CGO_ENABLED=0`.

- `AC-DEV-001`: Given mesin dengan Nix flakes, when developer menjalankan `nix develop -c go version`, then versi yang tercetak memenuhi direktif `go` di `go.mod`.
- `AC-DEV-002`: Given dev shell aktif, when developer menjalankan `golangci-lint version`, then perintah berhasil.
- `AC-DEV-003`: Given dev shell aktif di macOS arm64, when developer menjalankan `GOOS=linux GOARCH=amd64 go build ./cmd/lana`, then build berhasil tanpa cgo.

#### REQ-CI-001: CI GitHub Actions

Setiap push dan pull request harus menjalankan, di macOS arm64 dan Linux x86_64, lewat `nix develop -c`:
`gofmt -l` (harus kosong), `golangci-lint run ./...`, `go test -race ./...`, dan build release.
Ukuran binary release dicetak di ringkasan job.

- `AC-CI-001`: Given satu test gagal, when CI berjalan, then job gagal.
- `AC-CI-002`: Given semua langkah lulus, when CI selesai, then ringkasan job berisi ukuran binary untuk kedua platform.

#### REQ-BUILD-001: Layout package dan aturan import

Module harus memakai layout package dari ADR 0013. `core` tidak boleh meng-import package lain di
module ini. Package implementasi hanya boleh meng-import `core` dan `internal/...`, kecuali yang
diizinkan ADR 0013 (`cmd/lana` dan package `lana` merakit semuanya).

- `AC-BUILD-001`: Given module bersih, when developer menjalankan `go build ./...`, then build berhasil.
- `AC-BUILD-002`: Given seseorang menambah import dari `core` ke package lain di module ini, when test aturan import dijalankan, then test gagal dan menyebut package yang dilanggar.

#### REQ-BUILD-002: Build release

Build release harus memakai `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=<v>"`
untuk `darwin/arm64`, `linux/amd64`, dan `linux/arm64`, lalu ukuran binary `lana` dicatat. Target
< 40 MB (ADR 0013) dicatat saja dan tidak menggagalkan build.

- `AC-BUILD-003`: Given `make release` (atau script build), when build selesai, then tiga binary ada di `dist/` dan ukurannya tercetak.
- `AC-BUILD-004`: Given binary Linux hasil build, when diperiksa dengan `file`, then binary bertipe statically linked.

### Profile loader

#### REQ-CONF-001: Urutan lapisan

Loader harus membangun config efektif dari lapisan berurutan: base, profile bawaan,
`$LANA_HOME/profiles/<profile>.toml`, `$LANA_HOME/config.toml`, lalu setiap `--patch` sesuai urutan.
Lapisan yang filenya tidak ada dilewati.

- `AC-CONF-001`: Given `config.toml` dan `--patch a.toml` sama-sama menetapkan `agents.x.model`, when config dimuat, then nilai dari `a.toml` yang dipakai.
- `AC-CONF-002`: Given `profiles/minimal.toml` tidak ada, when profile `minimal` dimuat, then loader berhasil dan lapisan itu ditandai tidak ada.

#### REQ-CONF-002: Aturan merge

- `[[modules]]` digabung per `id`. Baris dengan `id` sama mengganti seluruh `config` baris itu.
  Field lain yang ditulis di lapisan atas (`module`, `disabled`) menimpa nilai lama.
- `disabled = true` membuat baris tidak di-install.
- Baris dengan `id` baru ditambahkan di akhir, atau di posisi `after = "<id>"` / `before = "<id>"`.
- Tabel `providers`, `agents`, `mcp`, `channels` digabung rekursif per key.
- `[[routes]]` dari lapisan terakhir yang menulisnya menggantikan seluruh daftar.

- `AC-CONF-003`: Given base punya `id = "store"` dengan `config = { path = "a.db", wal = true }` dan patch menulis `id = "store"` dengan `config = { path = "b.db" }`, when merge, then `config` efektif `{ path = "b.db" }`.
- `AC-CONF-004`: Given patch menulis `id = "store"` dengan `disabled = true`, when boot, then modul `store` tidak di-install.
- `AC-CONF-005`: Given patch menambah `id = "x"` dengan `after = "store"`, when merge, then `x` tepat setelah `store`.
- `AC-CONF-006`: Given base menetapkan `agents.a.model` dan `agents.a.tools`, dan patch hanya `agents.a.model`, when merge, then `tools` tetap dari base.
- `AC-CONF-007`: Given `config.toml` punya dua route dan patch punya satu route, when merge, then hanya satu route dari patch.
- `AC-CONF-008`: Given `after` menunjuk `id` yang tidak ada, when merge, then error validasi menyebut `id` tersebut.

#### REQ-CONF-003: Validasi lengkap dengan lokasi

Loader harus mengumpulkan semua error dari semua lapisan sebelum gagal. Setiap error harus
menyebut file, nomor baris jika diketahui, path key, dan pesan.

Validasi minimal:

- Sintaks TOML.
- Tipe nilai sesuai schema.
- Rujukan: `agents.*.provider` ke `providers`, `routes.*.agent` ke `agents`, `agents.*.subagents` ke `agents`, `agents.*.mcp` ke `mcp`.
- `module` di `[[modules]]` dikenal binary.
- `id` duplikat di satu lapisan.
- Nilai enum (`permission`, `kind`) valid.
- Key yang tidak dikenal schema adalah error, dengan saran key terdekat jika ada.
- `version` lebih tinggi dari yang didukung binary.

- `AC-CONF-009`: Given dua error di `config.toml` dan satu di patch, when config dimuat, then ketiga error dilaporkan.
- `AC-CONF-010`: Given `agents.coder.provider = "x"` di baris 12 dan provider `x` tidak ada, when config dimuat, then error berisi file `config.toml`, baris 12, key `agents.coder.provider`.
- `AC-CONF-016`: Given `agents.coder.permision = "ask"`, when config dimuat, then error menyebut key `agents.coder.permision` dan saran `permission`.

#### REQ-CONF-004: Peta asal

Loader harus menyediakan peta asal untuk setiap tabel, setiap key daun, dan setiap baris modul.

- `AC-CONF-011`: Given `providers.openai` ditulis di `config.toml` dan `model` agent ditimpa di `--patch p.toml`, when peta asal dibaca, then `providers.openai` berasal dari `config.toml` dan `agents.x.model` dari `p.toml`.

#### REQ-CONF-005: Secret

Nilai secret dibaca dari env proses, lalu dari `$LANA_HOME/secrets.env`. Env proses menang.
Nilai secret tidak boleh muncul lewat `fmt` (`%v`, `%+v`, `%#v`, `%s`), `slog`, serialisasi JSON atau TOML config efektif.

- `AC-CONF-012`: Given `OPENAI_API_KEY` ada di env dan di `secrets.env` dengan nilai berbeda, when secret dibaca, then nilai dari env yang dipakai.
- `AC-CONF-013`: Given config efektif diserialisasi, when ada secret, then nilainya `"***"`.
- `AC-CONF-014`: Given nilai secret, when diformat dengan `%#v` atau dicatat dengan `slog`, then hasilnya tidak mengandung nilai asli.
- `AC-CONF-017`: Given `secrets.env` dengan permission `0644`, when config dimuat, then file tetap dibaca dan peringatan berisi permission sekarang dan `chmod 600 <path>` tampil di log dan stderr.

#### REQ-CONF-007: Interpolasi env di MCP

`${NAMA}` di nilai `mcp.*.env` dan `mcp.*.headers` diganti dengan nilai env (env proses, lalu
`secrets.env`). Hasilnya diperlakukan sebagai secret. Di field lain, `${` adalah teks biasa. Nama
env yang tidak ditemukan adalah error validasi.

- `AC-CONF-018`: Given `mcp.github.env.GITHUB_TOKEN = "${GITHUB_TOKEN}"` dan env `GITHUB_TOKEN=abc`, when config dimuat, then nilai efektifnya `abc` dan serialisasinya `"***"`.
- `AC-CONF-019`: Given `agents.x.prompt_file = "${HOME}/p.md"`, when config dimuat, then nilainya tetap `"${HOME}/p.md"`.
- `AC-CONF-020`: Given `${TIDAK_ADA}` di `mcp.x.env` dan env itu tidak ada, when config dimuat, then error menyebut `TIDAK_ADA` dan key-nya.

#### REQ-CONF-008: Versi format config

Setiap lapisan boleh menulis `version = <integer>` di tingkat atas. Tanpa field itu, versi dianggap 1.

- `AC-CONF-021`: Given `version = 2` dan binary hanya mendukung 1, when config dimuat, then error menyebut versi file dan versi yang didukung.

#### REQ-CONF-009: Config tanpa file untuk embed

Loader harus bisa membangun config efektif dari lapisan di memori (teks TOML atau struct Go) tanpa
membaca `LANA_HOME`, dengan aturan merge dan validasi yang sama. Asal lapisan memori dinamai oleh pemanggil.

- `AC-CONF-022`: Given service Go memberi satu lapisan TOML dari memori bernama `myservice`, when config dimuat, then tidak ada file yang dibaca dan peta asal menyebut `myservice`.
- `AC-CONF-023`: Given lapisan memori berisi rujukan provider yang tidak ada, when config dimuat, then error validasi sama seperti lapisan file, dengan nama lapisan sebagai lokasi.

#### REQ-CONF-006: Lokasi data

`LANA_HOME` default `~/.lana`. Env `LANA_HOME` mengganti lokasi. Path relatif di config (misalnya
`prompt_file`) dihitung dari `LANA_HOME`.

- `AC-CONF-015`: Given `LANA_HOME=/tmp/x`, when config dimuat, then `config.toml` dibaca dari `/tmp/x/config.toml`.

### Modul dan Context

#### REQ-MOD-001: Urutan install dari dependency

Boot harus meng-install modul aktif dalam urutan yang memenuhi `requires` setiap modul. Untuk modul
tanpa hubungan dependency, urutan baris di config dipertahankan.

- `AC-MOD-001`: Given modul B membutuhkan service yang dipasang modul A dan A tertulis setelah B, when boot, then A di-install sebelum B.
- `AC-MOD-002`: Given dua modul tanpa dependency, when boot, then urutan install sama dengan urutan baris.

#### REQ-MOD-002: Boot gagal dengan jelas

Boot harus gagal tanpa meng-install modul apa pun jika ada service yang dibutuhkan tetapi tidak
dipasang modul aktif, atau ada siklus dependency. Pesan menyebut modul dan service terkait.

- `AC-MOD-003`: Given modul membutuhkan service `llm` dan tidak ada modul aktif yang memasangnya, when boot, then error menyebut nama modul dan `llm`.
- `AC-MOD-004`: Given A membutuhkan B dan B membutuhkan A, when boot, then error menyebut siklus `A → B → A`.

#### REQ-MOD-003: Shutdown berurutan

Shutdown harus menghentikan modul dalam urutan kebalikan dari install, dengan batas waktu total
30 detik. Modul yang melewati batas dicatat dan dilewati.

- `AC-MOD-005`: Given A di-install sebelum B, when shutdown, then B berhenti sebelum A.
- `AC-MOD-006`: Given satu modul tidak berhenti dalam 30 detik, when shutdown, then modul lain tetap dihentikan dan peringatan dicatat.

#### REQ-MOD-004: Satu pemasang per service

Satu service hanya boleh dipasang oleh satu modul aktif.

- `AC-MOD-007`: Given dua modul aktif memasang service `llm`, when boot, then error menyebut kedua modul.

### Hook

#### REQ-HOOK-001: Waterfall

Listener `waterfall` dipanggil berurutan. Listener menerima input dan `next`. Memanggil `next`
meneruskan ke listener berikutnya dan mengembalikan hasilnya. Tidak memanggil `next` memutus rantai
dengan hasil listener itu. Jika semua listener memanggil `next`, handler default hook dipanggil.
Listener yang didaftarkan dengan `prepend` berjalan sebelum listener biasa.

- `AC-HOOK-001`: Given listener A dan B dan handler default, when hook dipanggil dan keduanya memanggil `next`, then urutannya A, B, default.
- `AC-HOOK-002`: Given A tidak memanggil `next`, when hook dipanggil, then B dan default tidak berjalan dan hasil A dikembalikan.
- `AC-HOOK-003`: Given A mengubah input sebelum `next`, when B berjalan, then B menerima input yang sudah diubah.
- `AC-HOOK-004`: Given C didaftarkan dengan `prepend` setelah A, when hook dipanggil, then C berjalan sebelum A.
- `AC-HOOK-005`: Given A mengembalikan error, when hook dipanggil, then pemanggil menerima error itu dan listener berikutnya tidak berjalan.

#### REQ-HOOK-002: Serial

Listener `serial` dipanggil berurutan, masing-masing ditunggu, tanpa `next`. Hasil semua listener
dikembalikan sesuai urutan.

- `AC-HOOK-006`: Given A dan B pada hook serial, when hook dipanggil, then B mulai setelah A selesai dan hasilnya `[A, B]`.

#### REQ-HOOK-003: Observe

Hook `observe` mengirim event ke semua subscriber tanpa menunggu mereka. Setiap subscriber punya
buffer 1024 event. Saat buffer penuh, event tertua dibuang dan subscriber menerima `Lagged(n)`
dengan jumlah event yang terbuang sebelum event berikutnya.

- `AC-HOOK-007`: Given subscriber yang tidak membaca, when 10.000 event dikirim, then pengirim tidak tertahan.
- `AC-HOOK-010`: Given subscriber tertinggal 1500 event, when subscriber membaca lagi, then ia menerima `Lagged(476)` lalu 1024 event terbaru.

#### REQ-HOOK-004: Registrasi yang bisa dilepas

Setiap registrasi listener mengembalikan handle. Melepas handle menghapus listener. Saat modul
berhenti, semua listener miliknya dilepas.

- `AC-HOOK-008`: Given handle listener dilepas, when hook dipanggil, then listener itu tidak berjalan.
- `AC-HOOK-009`: Given modul berhenti saat shutdown, when hook dipanggil setelahnya, then listener modul itu tidak berjalan.

### Profile bawaan

#### REQ-PROF-001: Profile `minimal` dan `headless`

Binary harus berisi profile bawaan `minimal` dan `headless` sebagai daftar baris modul. Profile
tidak dikenal harus gagal dengan daftar profile yang tersedia.

- `AC-PROF-001`: Given profile `minimal`, when config efektif dibaca, then baris modulnya sama dengan definisi bawaan.
- `AC-PROF-002`: Given profile `xyz`, when dimuat, then error menyebut `xyz` dan daftar profile yang ada.

## Kasus gagal

| Kondisi | Perilaku yang diharapkan |
|---|---|
| File config tidak bisa dibaca (permission) | Error menyebut file dan alasan OS |
| TOML tidak valid | Error dengan baris dan kolom |
| Rujukan rusak | Error per rujukan dengan lokasi |
| Modul tidak dikenal | Error menyebut `module` dan daftar modul yang ada |
| Dependency hilang atau siklus | Boot gagal sebelum install modul apa pun |

## Otorisasi dan keamanan

- Secret tidak pernah tercetak (`REQ-CONF-005`).
- Loader tidak menjalankan kode atau ekspresi dari config (ADR 0010 menolak `!!js`).

## Integritas data dan konkurensi

- Loader murni: hasil hanya bergantung pada isi lapisan dan env.
- `Context` hanya bisa diubah selama boot. Setelah boot, service tidak bisa ditambah atau diganti.
- Hook aman dipanggil dari banyak task bersamaan.

## Aksesibilitas

Tidak berlaku (tidak ada antarmuka user baru).

## Resolusi pertanyaan

| ID | Pertanyaan | Resolusi |
|---|---|---|
| `Q-001` | Pin versi toolchain | Rev 2: `rust-toolchain.toml`. Rev 3: diganti ADR 0013, Go dari nixpkgs dipin `flake.lock`, `GOTOOLCHAIN=local` |
| `Q-002` | CI | GitHub Actions di macOS arm64 dan Linux x86_64 |
| `Q-003` | Key tidak dikenal | Error dengan saran key terdekat |
| `Q-004` | Interpolasi `${VAR}` | Hanya di `mcp.*.env` dan `mcp.*.headers`, nilainya secret |
| `Q-005` | Target ukuran binary | Dicatat saja |
| `Q-006` | Subscriber `observe` tertinggal | Buffer 1024, buang event tertua, `Lagged(n)` |
| `Q-007` | Permission `secrets.env` | Peringatan di log dan stderr, file tetap dibaca |
| `Q-008` | Versi format config | Opsional `version`, default 1 |

## Blocking Open Questions

None.

## Cakupan requirement

| Requirement | Acceptance criteria | Sumber |
|---|---|---|
| `REQ-DEV-001` | `AC-DEV-001`, `AC-DEV-002`, `AC-DEV-003` | Instruksi mesin, ADR 0013 |
| `REQ-BUILD-001` | `AC-BUILD-001`, `AC-BUILD-002` | ADR 0013 |
| `REQ-BUILD-002` | `AC-BUILD-003`, `AC-BUILD-004` | ADR 0013, `Q-005` |
| `REQ-CI-001` | `AC-CI-001`, `AC-CI-002` | `Q-002` |
| `REQ-CONF-001` | `AC-CONF-001`, `AC-CONF-002` | ADR 0010 |
| `REQ-CONF-002` | `AC-CONF-003` sampai `AC-CONF-008` | ADR 0010 |
| `REQ-CONF-003` | `AC-CONF-009`, `AC-CONF-010`, `AC-CONF-016` | ADR 0010, spec #1, `Q-003` |
| `REQ-CONF-004` | `AC-CONF-011` | ADR 0010, spec #1 |
| `REQ-CONF-005` | `AC-CONF-012`, `AC-CONF-013`, `AC-CONF-014`, `AC-CONF-017` | ADR 0005, spec #1 contract, `Q-007` |
| `REQ-CONF-006` | `AC-CONF-015` | ADR 0013, spec #1 contract |
| `REQ-CONF-007` | `AC-CONF-018`, `AC-CONF-019`, `AC-CONF-020` | ADR 0006, `Q-004` |
| `REQ-CONF-008` | `AC-CONF-021` | `Q-008` |
| `REQ-CONF-009` | `AC-CONF-022`, `AC-CONF-023` | ADR 0014 |
| `REQ-MOD-001` | `AC-MOD-001`, `AC-MOD-002` | ADR 0002 |
| `REQ-MOD-002` | `AC-MOD-003`, `AC-MOD-004` | ADR 0010 |
| `REQ-MOD-003` | `AC-MOD-005`, `AC-MOD-006` | ARCHITECTURE §5 |
| `REQ-MOD-004` | `AC-MOD-007` | ADR 0002 |
| `REQ-HOOK-001` | `AC-HOOK-001` sampai `AC-HOOK-005` | ADR 0002 |
| `REQ-HOOK-002` | `AC-HOOK-006` | ADR 0002 |
| `REQ-HOOK-003` | `AC-HOOK-007`, `AC-HOOK-010` | ADR 0002, `Q-006` |
| `REQ-HOOK-004` | `AC-HOOK-008`, `AC-HOOK-009` | ADR 0002 (registrasi bisa dilepas, pola `dsh`) |
| `REQ-PROF-001` | `AC-PROF-001`, `AC-PROF-002` | ADR 0010 |

## Approval

- [x] Scope dan pengecualian eksplisit.
- [x] Perilaku sukses dan gagal terukur.
- [x] Otorisasi eksplisit.
- [x] Transisi state dan efek destruktif eksplisit atau tidak berlaku.
- [x] Default, urutan, retry, dan idempotensi eksplisit jika berlaku.
- [x] Setiap requirement dan acceptance criterion punya ID stabil dan sumber.
- [x] Keberlakuan contract eksplisit.
- [x] Blocking Open Questions bernilai `None`.
- **Di-approve oleh**: —
- **Di-approve pada**: —
