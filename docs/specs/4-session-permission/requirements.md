# Requirements: Session event log, permission, approval

- **Issue**: [`#4`](https://github.com/ridhomujizat/lana-harness/issues/4)
- **Status**: Approved
- **Revisi**: 2 (REQ-APPR-007 dan AC-APPR-017 ditambah; `Q-027` sampai `Q-032`; nama error mengikuti `core`)
- **Pemilik**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Diperlukan (kind dan payload event, DDL, API Go package `store`, `core`, permission, approval, compaction, key config)
- **ADR terkait**: [Index ADR](../../adr/README.md) — [0004](../../adr/0004-permission-mode-per-agent.md), [0005](../../adr/0005-sqlite-sebagai-storage-tunggal.md), [0009](../../adr/0009-session-sebagai-event-log-append-only.md), [0011](../../adr/0011-subagent-sebagai-child-session.md), [0013](../../adr/0013-gunakan-go-sebagai-single-binary.md), [0014](../../adr/0014-integrasi-sebagai-agentic-tool.md)
- **Diagram terkait**: [Index diagram](../../diagram/README.md) — [0002](../../diagram/0002-agent-loop-dan-approval.md), [0003](../../diagram/0003-data-model-dan-scheduler.md), [0004](../../diagram/0004-profile-fork-subagent-dan-protokol.md)

## Sumber

| Sumber | Referensi | Dipakai untuk |
|---|---|---|
| Issue | [`#4`](https://github.com/ridhomujizat/lana-harness/issues/4) | scope spec #4 |
| ADR 0004 | Permission dan approval | urutan evaluasi, policy, outcome tertutup, audit, rantai penjawab |
| ADR 0005 | SQLite | satu file, WAL, writer tunggal, migrasi, secret tidak masuk DB |
| ADR 0009 | Event log | `session_events`, `sessions`, fork, resume, compaction, versi payload |
| ADR 0011 | Subagent | batas izin child (irisan induk dan child) |
| ADR 0013 | Go | `modernc.org/sqlite`, layout package, `syscall.Kill` untuk cek PID |
| ADR 0014 | Integrasi | approval tanpa penjawab = `unavailable` |
| Spec #1 | `REQ-SESS-001`, `REQ-SESS-002`, `REQ-APPR-001`, `REQ-APPR-002`, `REQ-CMD-001`, design "Lock session" | perilaku session CLI, lease, policy, fork, compaction yang dipanggil CLI |
| Spec #2 | Tipe dan hook di package `core` | `core.SessionEvent`, `core.SessionStore`, hook waterfall dan observe |
| Spec #3 | Agent loop (sedang ditulis) | pemanggil hook `tools.pre_execute`, `agent.pre_step`, `llm.stream`; default fail-closed |
| Diagram 0002, 0003, 0004 | Alur turn, data model, derive, resume | urutan event, derive, resume repair |
| Wawancara 2026-10-29 | Pemilik melepas gate approval | keputusan di bagian Resolusi pertanyaan memakai `Rekomendasi agent:` |

## Tujuan

Pemilik punya riwayat session yang lengkap dan bisa dipercaya. Setiap pesan, tool call, dan
keputusan izin tercatat di satu log. Session bisa dilanjutkan setelah proses berhenti mendadak,
di-fork di batas turn, dan diringkas saat konteks hampir penuh. Setiap tool call dinilai oleh aturan
izin dan approval yang tertutup dan fail-closed sebelum berjalan.

## Aktor

- **Pemilik** — menulis `allow` dan `deny` di config, mengubah policy session, dan menjawab approval lewat channel. Tidak bisa melewati rule `deny`.
- **Agent loop** (spec #3) — memanggil store, permission, dan compaction lewat service dan hook.
- **Penjawab approval** — listener hook `approval.request` milik channel (CLI di spec #1, channel lain nanti). Hanya menjawab session miliknya.
- **Provider** — dipanggil untuk membuat ringkasan compaction lewat adapter langsung.

## Definisi

- **Event** — satu baris `session_events`: `session_id`, `seq`, `kind`, `payload`, `turn_id`, `created_at`.
- **Projection** — baris tabel `sessions`. Bisa dibangun ulang dari event.
- **Turn yatim** — turn dengan `turn/start` yang belum punya `turn/end` pasangan.
- **Call yatim** — `tool/call` atau `tool_use` yang belum punya `tool/result`.
- **Rantai session** — session dan induknya sampai root. Event induk dipotong di `inherited_seq`.
- **Policy** — `trust`, `ask`, atau `never`. Nilai efektif = `approval/policy` terakhir di rantai, atau `permission` agent jika belum ada.
- **Pola** — teks `tool` atau `tool(arg-glob)` di rule `allow` dan `deny`.
- **Argumen pola** — nilai input tool yang dicocokkan dengan pola (aturan di `REQ-PERM-001`).
- **Grant sesi** — izin dari jawaban `allowed_session`. Dihitung dari `tool/call` yang dirujuk.
- **Penjawab** — listener hook `approval.request` yang bisa menjawab.
- **Outcome** — salah satu dari lima nilai tertutup: `allowed_once`, `allowed_session`, `rejected`, `cancelled`, `unavailable`.
- **Lease** — kunci satu session untuk satu proses. Disimpan di `session_leases`.
- **Compaction** — event `compaction/summary` yang menggantikan riwayat sampai `upto_seq` untuk model. Event lama tetap ada.
- **Context window** — batas token model. Dibaca dari config agent.
- **Prefix ULID** — awalan ID session minimal 6 karakter, tidak peka huruf besar-kecil.

## Scope

### Termasuk

- Penyimpanan SQLite satu file: pragma, migrasi, writer tunggal, read pool.
- Event log append-only, projection `sessions`, dan index.
- `Create`, `Open` dengan resume repair, `Append`, `Events`, `List`, `FindPrefix`, `Fork`, `TryLease`, `Prune`, derive riwayat model, dan rebuild projection.
- Permission: pola, evaluasi, batas workspace, dan grant sesi.
- Approval: policy, permintaan, audit, rantai penjawab, timeout, cancel, dan fungsi batas izin subagent.
- Compaction: otomatis, manual, dan retry saat konteks terlalu panjang.
- Modul `sqlite_store`, `permission`, `approval`, dan `compaction`.

### Tidak termasuk

- Export session ke JSONL, `lana session`, dan `lana db prune` (CLI). Hanya fungsi `Prune` di store.
- Memory FTS5 dan tabel cron (M5).
- Subcommand CLI, TUI, dan prompt approval (spec #1).
- Agent loop, provider, tool builtin, dan tool `task` (spec #3 dan spec subagent, M4).
- MCP, SDK, HTTP, ACP, dan A2A.
- Sandbox level OS (ADR 0004).
- Retensi data dan rotasi log.

## Asumsi

- Satu file `lana.db` bisa dibuka lebih dari satu proses. Lease di `session_leases` mencegah dua proses memakai session yang sama. — Sumber: ADR 0005, spec #1 `REQ-SESS-002`.
- Tool yang menyentuh file punya argumen `path`. Tool `bash` punya argumen `command`. — Sumber: spec #1 `API-CLI-003` (argumen utama per tool).
- Waktu disimpan dalam milidetik Unix. — Sumber: spec #1 konvensi serialisasi.
- Hook `tools.pre_execute` dan `approval.request` berjalan sebagai waterfall. Hook `session.event` berjalan sebagai observe. — Sumber: spec #2 `REQ-HOOK-001`, `REQ-HOOK-003`.
- Agent loop menulis `tool/result` untuk setiap `tool/call`, termasuk saat ditolak. — Sumber: ADR 0009, ARCHITECTURE §5.
- Tipe `core.Message`, `core.ContentBlock`, `core.SessionEvent`, konstanta kind, struct payload, dan `core.Risk` dimiliki spec #2. — Sumber: pembagian spec core (wawancara 2026-10-29).

## Aturan perilaku

### Penyimpanan

#### REQ-STORE-001: Satu file SQLite

Sistem harus menyimpan state session di satu file SQLite dengan mode WAL, `synchronous = NORMAL`,
`foreign_keys = ON`, dan `busy_timeout` 5000 ms. Driver harus pure Go tanpa cgo.

- `AC-STORE-001`: Given `LANA_HOME` belum punya `lana.db`, when store dibuka, then file dibuat dengan mode `0600`, `journal_mode` bernilai `wal`, `synchronous` bernilai `1`, dan `foreign_keys` bernilai `1`.
- `AC-STORE-002`: Given path `data/lana.db` di config dan folder `data` ada di `LANA_HOME`, when store dibuka, then file berada di `$LANA_HOME/data/lana.db`.

#### REQ-STORE-002: Migrasi bernomor

Sistem harus menjalankan migrasi SQL yang di-embed secara berurutan saat store dibuka. Setiap
migrasi berjalan dalam satu transaksi dan menaikkan `PRAGMA user_version`. Database yang
`user_version`-nya lebih tinggi dari migrasi tertinggi di binary ditolak tanpa diubah.

- `AC-STORE-003`: Given database kosong, when store dibuka dua kali, then `user_version` sama dengan jumlah migrasi dan migrasi tidak dijalankan ulang.
- `AC-STORE-004`: Given `user_version` lebih tinggi dari migrasi tertinggi, when store dibuka, then store menolak dengan `ErrSchemaTooNew` dan isi database tidak berubah.

#### REQ-STORE-003: Writer tunggal dan read pool

Sistem harus mengirim semua tulis ke satu goroutine writer per proses. Setiap transaksi tulis
memakai `BEGIN IMMEDIATE`, sehingga dua proses tidak bisa menulis bersamaan. Baca memakai pool
koneksi read-only.

- `AC-STORE-005`: Given 50 goroutine menulis event ke 10 session bersamaan, when semua selesai, then tidak ada error `SQLITE_BUSY` dan seq setiap session berurutan tanpa celah.
- `AC-STORE-006`: Given dua instance store membuka file yang sama dari dua proses, when keduanya menulis ke session yang sama, then setiap seq hanya terpakai sekali dan tidak ada event yang hilang.

#### REQ-STORE-004: Event log append-only

Sistem harus menolak `UPDATE` dan `DELETE` pada `session_events`, kecuali jalur prune session utuh
(`REQ-STORE-014`).

- `AC-STORE-007`: Given event tersimpan, when `UPDATE session_events` dijalankan langsung lewat SQL, then SQLite menolak dengan pesan `session_events is append-only` dan baris tidak berubah.
- `AC-STORE-008`: Given event tersimpan, when `DELETE FROM session_events` dijalankan langsung lewat SQL, then SQLite menolak dengan pesan yang sama dan baris tetap ada.

#### REQ-STORE-005: Append atomik, seq, dan turn tunggal

Sistem harus menulis satu batch event dalam satu transaksi. Seq naik 1 per session mulai dari 1.
Sistem harus menolak `turn/start` jika session masih punya turn yang belum ditutup.

- `AC-STORE-009`: Given session baru tanpa event, when `Append` menulis tiga event, then seq event itu 1, 2, dan 3 dan `last_seq` projection bernilai 3.
- `AC-STORE-010`: Given batch dua event dan event kedua tidak valid, when `Append` dipanggil, then tidak ada event dari batch itu yang tersimpan.
- `AC-STORE-011`: Given turn masih berjalan, when `Append` menulis `turn/start` baru, then store menolak dengan `ErrTurnActive`.

#### REQ-STORE-006: Create dan Open dengan resume repair

Sistem harus menulis `session/created` saat `Create`. Saat `Open` dipanggil oleh proses yang
memegang lease, sistem harus menutup turn yatim: `approval/decided` dengan outcome `unavailable`
untuk approval yang belum dijawab, `tool/result` dengan status `error` untuk setiap call yatim,
lalu `turn/end` dengan reason `interrupted`. Session yang sudah idle tidak menerima event baru.

- `AC-STORE-012`: Given `Create` dengan agent, channel, address, dan workspace, when selesai, then event pertama berseq 1 berkind `session/created` berisi keempat nilai itu, dan status projection `idle`.
- `AC-STORE-013`: Given turn berjalan dengan call `c1` yang sudah punya `tool/result` dan call `c2` yang belum, when `Open`, then tersimpan `tool/result` error untuk `c2`, lalu `turn/end` reason `interrupted`, dan status projection `idle`.
- `AC-STORE-014`: Given turn berjalan dengan `approval/asked` tanpa `approval/decided`, when `Open`, then tersimpan `approval/decided` outcome `unavailable` reason `interrupted` sebelum `turn/end`.
- `AC-STORE-015`: Given session sudah idle, when `Open` dipanggil dua kali, then tidak ada event baru.
- `AC-STORE-016`: Given proses tidak memegang lease session, when `Open` dipanggil, then store menolak dengan `ErrLeaseNotHeld` dan tidak menulis event.

#### REQ-STORE-007: Fork di batas turn

Sistem harus membuat session baru dari `Fork(induk, at_seq)` dengan `parent_session_id` dan
`inherited_seq = at_seq`, tanpa menyalin event induk. `at_seq` harus menunjuk `turn/end` milik induk.

- `AC-STORE-017`: Given session `A` punya `turn/end` di seq 20 dan event sampai seq 30, when `Fork(A, 20)`, then session `B` tercatat dengan `parent_session_id = A` dan `inherited_seq = 20`, `A` tetap punya 30 event, dan `B` punya nol event.
- `AC-STORE-018`: Given `at_seq` menunjuk `user/message`, when `Fork` dipanggil, then store menolak dengan `ErrForkNotAtTurnBoundary` dan tidak membuat session.

#### REQ-STORE-008: Baca event dan daftar session

Sistem harus mengembalikan event session dengan `seq >= from` diurutkan naik. `List(channel,
address, limit)` harus mengembalikan session urut `updated_at` menurun dengan jumlah maksimal `limit`.

- `AC-STORE-019`: Given session dengan 10 event, when `Events(id, 5)`, then hasil berisi seq 5 sampai 10 berurutan.
- `AC-STORE-020`: Given 25 session dengan `channel = cli` dan `address` yang sama, when `List(cli, address, 20)`, then hasil berisi 20 session terbaru urut `updated_at` menurun.

#### REQ-STORE-009: Pencarian session dengan prefix

Sistem harus mencari session dengan prefix ULID minimal 6 karakter tanpa peka huruf besar-kecil.
Prefix yang cocok dengan lebih dari satu session harus ditolak dengan daftar kandidat.

- `AC-STORE-021`: Given session `01JBX7Q3KM8Z4T2V6N0P1R5S9W`, when `FindPrefix("01jbx7")`, then session itu dikembalikan.
- `AC-STORE-022`: Given dua session `01JBX7Q3KM...` dan `01JBX7Q9AA...`, when `FindPrefix("01jbx7")`, then store menolak dengan `core.ErrAmbiguousPrefix` yang memuat kedua ID urut naik.
- `AC-STORE-023`: Given prefix 5 karakter, when `FindPrefix`, then store menolak dengan `core.ErrPrefixTooShort`; given prefix 6 karakter yang tidak cocok, then store menolak dengan `ErrSessionNotFound`.

#### REQ-STORE-010: Lease sesi

Sistem harus memberi satu session ke satu proses lewat `session_leases`. `TryLease` berhasil jika
belum ada lease, atau pemegangnya berada di host yang sama dan PID-nya sudah mati. Pemegang di host
lain dianggap hidup. `Release` melepas lease.

- `AC-STORE-024`: Given session tanpa lease, when `TryLease`, then baris `session_leases` berisi `pid` proses, `host`, dan `acquired_at`.
- `AC-STORE-025`: Given lease dipegang PID yang masih hidup di host yang sama, when proses lain memanggil `TryLease`, then store menolak dengan `*core.LeaseHeldError` yang memuat PID pemegang.
- `AC-STORE-026`: Given lease dipegang PID yang sudah mati di host yang sama, when `TryLease`, then lease diganti dengan PID proses sekarang.
- `AC-STORE-027`: Given lease dipegang host lain, when `TryLease`, then store menolak dengan `*core.LeaseHeldError`.

#### REQ-STORE-011: Derive riwayat model

Sistem harus menyusun riwayat model dengan fungsi murni `derive_messages` dari rantai session.
Event yang masuk riwayat adalah event konten model (lihat contract). Ringkasan terakhir
menggantikan riwayat sampai `upto_seq`. `assistant/attempt`, `tool/call`, event lifecycle, dan
event approval tidak masuk riwayat.

- `AC-STORE-028`: Given session dengan turn lengkap (user, assistant dengan `tool_use`, tool result, satu `assistant/attempt`, dan event approval), when riwayat diderive, then hasil sama dengan golden file.
- `AC-STORE-029`: Given fork `B` dari `A` di seq 20 dan `A` punya event sampai seq 30, when riwayat `B` diderive, then riwayat berisi event `A` sampai seq 20 dan event `B` saja.
- `AC-STORE-030`: Given `compaction/summary` dengan `upto_seq = 12` di tengah session, when riwayat diderive, then riwayat berisi ringkasan lalu event dengan seq lebih besar dari 12, sedangkan event lama tetap ada di log.

#### REQ-STORE-012: Projection dapat dibangun ulang

Sistem harus membangun ulang tabel `sessions` dari `session_events`. Hasil rebuild harus sama
dengan projection yang dibangun saat append.

- `AC-STORE-031`: Given tiga session dengan event, when baris `sessions` dihapus lalu projection dibangun ulang, then isi `sessions` sama dengan sebelum penghapusan.

#### REQ-STORE-013: Versi payload

Setiap payload harus berisi `v`. Sistem harus men-decode payload versi lama ke versi sekarang di
memori dan tidak menulis ulang baris lama. Versi yang tidak dikenal ditolak.

- `AC-STORE-032`: Given payload dengan `v` 99, when di-decode, then store mengembalikan `ErrUnknownPayloadVersion`.
- `AC-STORE-033`: Given baris dengan `v` 0 (fixture uji dengan migrasi payload 0 ke 1), when event dibaca, then payload hasil bernilai versi 1 dan isi baris di database tidak berubah.

#### REQ-STORE-014: Prune sesi utuh

Sistem harus menghapus satu session beserta semua event, baris projection, dan lease-nya dalam
satu transaksi. Sistem tidak boleh memotong sebagian log. Session yang masih punya child ditolak.

- `AC-STORE-034`: Given session `A` tanpa child, when `Prune(A)`, then event, baris `sessions`, dan lease `A` hilang, dan event session lain tidak berubah.
- `AC-STORE-035`: Given session `A` punya session fork `B`, when `Prune(A)`, then store menolak dengan `ErrSessionHasChildren` dan tidak ada yang terhapus.

#### REQ-STORE-015: Broadcast setelah commit

Sistem harus mengirim event ke hook `session.event` hanya setelah transaksi commit berhasil.

- `AC-STORE-036`: Given `Append` gagal karena constraint, when subscriber membaca hook `session.event`, then subscriber tidak menerima event dari batch itu.
- `AC-STORE-037`: Given `Append` berhasil, when subscriber membaca, then subscriber menerima event dengan seq dan kind yang sama dengan hasil append.

#### REQ-STORE-016: Modul store

Modul `sqlite_store` harus memasang service `sessions` berisi `core.SessionStore`. Path database
dibaca dari baris `[[modules]]` dengan `id = "store"` dan key `config.path`. Default `lana.db`
relatif ke `LANA_HOME`.

- `AC-STORE-038`: Given baris `store` tanpa `config.path`, when boot, then service `sessions` tersedia dan database berada di `$LANA_HOME/lana.db`.

### Permission

#### REQ-PERM-001: Pola aturan

Sistem harus memakai pola `tool` atau `tool(arg-glob)`. Nama tool cocok persis. Wildcard `*`
mencocokkan urutan karakter apa pun. `?` mencocokkan satu karakter. `**` mencocokkan urutan
karakter termasuk `/`, dan hanya berlaku pada argumen path. Pada argumen `command`, `*` dan `?`
juga mencocokkan `/`. Pada argumen path, `*` dan `?` tidak melewati `/`. `\` membuat karakter
berikutnya literal. Pola tidak valid ditolak saat config dimuat.

Argumen pola per tool:

| Tool | Argumen pola |
|---|---|
| `bash` | `command` |
| `read_file`, `write_file`, `edit_file`, `glob`, `grep` | `path`, relatif terhadap workspace jika tidak absolut. Kosong berarti workspace |
| `web_fetch` | `url` |
| `task` | `agent` |
| Lainnya (termasuk `mcp__*`) | seluruh input JSON dalam bentuk compact |

- `AC-PERM-001`: Given rule `bash(git status*)`, when `command` bernilai `git status --short`, then pola cocok; when `command` bernilai `git push`, then pola tidak cocok.
- `AC-PERM-002`: Given rule `write_file(~/.ssh/**)` dan home `/Users/ridho`, when path `~/.ssh/keys/id` diselesaikan, then pola cocok.
- `AC-PERM-003`: Given rule `read_file(src/*)`, when path `src/a/b.go`, then pola tidak cocok; given rule `read_file(src/**)`, then pola cocok.
- `AC-PERM-004`: Given `agents.coder.rules.allow` berisi `bash(git`, when config dimuat, then validasi gagal dan error menyebut key `agents.coder.rules.allow`, nilai pola, dan alasan tanda kurung tidak tertutup.

#### REQ-PERM-002: Urutan evaluasi

Sistem harus mengevaluasi setiap panggilan `tools.pre_execute` dengan urutan berikut dan berhenti
pada keputusan pertama: (1) rule `deny` cocok, tolak; (2) argumen path di luar workspace, tolak;
(3) rule `allow` atau grant sesi cocok, izinkan; (4) policy `trust`, izinkan; (5) risk `Read`,
izinkan; (6) policy `never`, tolak; (7) policy `ask`, minta approval.

- `AC-PERM-005`: Given policy `trust` dan rule `deny` `bash(rm -rf*)`, when model memanggil `bash` dengan `rm -rf build`, then tool ditolak tanpa approval.
- `AC-PERM-006`: Given rule `allow` `read_file` dan path di luar workspace, when `read_file` dipanggil, then tool ditolak karena workspace, bukan diizinkan karena rule allow.
- `AC-PERM-007`: Given policy `never` dan rule `allow` `bash(git status*)`, when `git status` dipanggil, then tool berjalan tanpa approval.
- `AC-PERM-008`: Given policy `never` dan tanpa rule allow, when `write_file` dipanggil, then tool ditolak dan tidak ada `approval/asked`.
- `AC-PERM-009`: Given policy `ask`, when `read_file` dalam workspace dipanggil, then tool berjalan tanpa `approval/asked`.
- `AC-PERM-010`: Given policy `ask` dan `bash` tanpa rule allow atau grant, when dipanggil, then sistem membuat approval dan menunggu outcome.

#### REQ-PERM-003: Batas workspace

Sistem harus menyelesaikan argumen path relatif terhadap workspace session, lalu membandingkannya
setelah `filepath.EvalSymlinks`. Path di luar workspace ditolak, termasuk symlink yang menunjuk
keluar. Path yang belum ada diselesaikan dari induk terdekat yang ada. Tool tanpa argumen path
(`bash`, `web_fetch`, `task`, dan tool MCP) tidak dicek batas ini.

- `AC-PERM-011`: Given workspace `~/coding/x` dan symlink `~/coding/x/link` yang menunjuk `/etc`, when `read_file` dengan path `link/hosts` dipanggil, then tool ditolak.
- `AC-PERM-012`: Given workspace `~/coding/x`, when `write_file` dengan path `../y.txt` dipanggil, then tool ditolak.
- `AC-PERM-013`: Given workspace `~/coding/x` dan file `new.txt` belum ada, when `write_file` dengan path `new.txt` dipanggil, then batas workspace tidak menolak tool.

#### REQ-PERM-004: Grant sesi

Sistem harus menyimpan jawaban `allowed_session` sebagai `approval/decided` yang merujuk `call_id`.
Grant berbentuk pola literal `tool(argumen utama)`, atau `tool` jika tool tidak punya argumen pola.
Grant dihitung dari `tool/call` yang dirujuk. Grant berlaku untuk call berikutnya di session itu
dan di session turunan fork yang mewarisi event tersebut. Grant tidak ditulis ke config.

- `AC-PERM-014`: Given jawaban `allowed_session` untuk `bash` dengan `command` `cargo test`, when `bash` dengan `cargo test` dipanggil lagi di session yang sama, then tidak ada `approval/asked` baru.
- `AC-PERM-015`: Given grant dibuat di `A` lalu `B` di-fork dari `A` setelah grant itu, when `cargo test` dipanggil di `B`, then tidak ada `approval/asked`.
- `AC-PERM-016`: Given grant `bash(cargo test)`, when `bash` dengan `command` `cargo build` dipanggil, then approval diminta lagi.

#### REQ-PERM-005: Listener menggantikan default

Modul `permission` harus memasang listener di hook `tools.pre_execute`. Listener mengembalikan
`ToolDecision` tanpa memanggil `next`. Jika modul tidak aktif, default fail-closed dari spec #3
tetap menolak semua tool.

- `AC-PERM-017`: Given modul `permission` aktif dan tool diizinkan, when hook dipanggil, then `ToolDecision.Allowed` bernilai `true` dan default spec #3 tidak berjalan.
- `AC-PERM-018`: Given modul `permission` tidak aktif, when tool dipanggil, then tool ditolak oleh default fail-closed.

#### REQ-PERM-006: Hasil penolakan

Setiap penolakan harus mengembalikan `ToolDecision` dengan `Allowed = false`, alasan yang bisa
dibaca model, dan `Status = rejected`. Agent loop menulis `tool/result` dengan status itu.

- `AC-PERM-019`: Given `write_file` ditolak oleh policy `never`, when hasil dibaca, then `Allowed` bernilai `false`, `Status` bernilai `rejected`, dan `Reason` tidak kosong.

### Approval

#### REQ-APPR-001: Policy session

Policy efektif session adalah nilai `approval/policy` terakhir di rantai session. Jika belum ada,
policy efektif adalah `permission` agent. Nilai valid hanya `trust`, `ask`, dan `never`. Satu-satunya
jalur tulis policy adalah `SetPolicy`, yang menulis event `approval/policy`.

- `AC-APPR-001`: Given session tanpa `approval/policy` dan agent `permission = "ask"`, when `EffectivePolicy` dipanggil, then hasilnya `ask`.
- `AC-APPR-002`: Given `SetPolicy(trust)` lalu `SetPolicy(never)`, when `EffectivePolicy` dipanggil, then hasilnya `never` dan kedua event tersimpan.
- `AC-APPR-003`: Given `SetPolicy` dengan nilai `yolo`, when dipanggil, then store menolak dengan `ErrInvalidPolicy` dan tidak menulis event.
- `AC-APPR-004`: Given turn berjalan dengan policy `ask`, when `SetPolicy(never)` dipanggil di tengah turn, then evaluasi tool berikutnya memakai `never`.

#### REQ-APPR-002: Permintaan approval dan audit

Setiap permintaan approval harus memakai `request_id` ULID baru. Sistem menulis `approval/asked`,
memanggil rantai penjawab, lalu menulis `approval/decided`. `approval/asked` tidak boleh menyalin
argumen tool, hanya `call_id`. Setiap permintaan menghasilkan tepat satu `approval/decided`.

- `AC-APPR-005`: Given permintaan approval, when selesai, then `approval/asked` berisi `request_id`, `call_id`, nama tool, dan label tanpa field argumen, dan tepat satu `approval/decided` dengan `request_id` yang sama tersimpan.
- `AC-APPR-006`: Given outcome `allowed_once`, when hasil dikembalikan, then `ToolDecision.Allowed` bernilai `true`; given outcome `rejected` dengan alasan dari penjawab, then `Allowed` bernilai `false` dan alasan itu masuk ke `Reason`.

#### REQ-APPR-003: Rantai penjawab

Hook `approval.request` berjalan sebagai waterfall. Penjawab pertama yang mengembalikan outcome
menjawab. Jika tidak ada penjawab yang menjawab, hasilnya `unavailable`. Penjawab yang error atau
mengembalikan nilai di luar outcome tertutup menghasilkan `unavailable`.

- `AC-APPR-007`: Given penjawab A tidak memiliki session dan memanggil `next`, dan penjawab B memiliki session, when permintaan dibuat, then B menjawab dan outcome dari B dipakai.
- `AC-APPR-008`: Given tidak ada penjawab, when permintaan dibuat, then outcome `unavailable`.
- `AC-APPR-009`: Given penjawab A mengembalikan error, when permintaan dibuat, then outcome `unavailable` dan penjawab B tidak berjalan.
- `AC-APPR-010`: Given penjawab mengembalikan nilai `ya`, when permintaan dibuat, then outcome `unavailable`.

#### REQ-APPR-004: Batas waktu dan pembatalan

Jika tidak ada jawaban dalam 10 menit, outcome `unavailable`. Jika context turn dibatalkan saat
menunggu jawaban, outcome `cancelled`.

- `AC-APPR-011`: Given tidak ada jawaban, when waktu maju 10 menit (jam uji), then outcome `unavailable` dan `approval/decided` tersimpan dengan reason `timeout`.
- `AC-APPR-012`: Given turn dibatalkan saat menunggu jawaban, when context dibatalkan, then outcome `cancelled` dan `approval/decided` tersimpan.

#### REQ-APPR-005: Audit fail-closed

Jika `approval/asked` tidak bisa ditulis, penjawab tidak boleh dipanggil dan outcome `unavailable`.
Jika `approval/decided` tidak bisa ditulis, tool tidak boleh berjalan.

- `AC-APPR-013`: Given penulisan `approval/asked` gagal, when permintaan dibuat, then penjawab tidak dipanggil dan outcome `unavailable`.
- `AC-APPR-014`: Given penjawab mengembalikan `allowed_once` tetapi penulisan `approval/decided` gagal, when permintaan selesai, then `ToolDecision.Allowed` bernilai `false`.

#### REQ-APPR-006: Batas izin subagent

Permission child tidak boleh lebih longgar dari induknya. Urutan dari paling ketat ke paling
longgar: `never`, `ask`, `trust`. Policy child adalah nilai yang lebih ketat dari policy induk dan
policy agent child. Rule `deny` dari induk dan child berlaku bersama. Tool `task` dibuat di spec
subagent (M4). Spec ini menetapkan fungsi batas yang dipakai tool itu.

- `AC-APPR-015`: Given induk `trust` dan child `ask`, when policy child dihitung, then hasilnya `ask`; given induk `never` dan child `trust`, then hasilnya `never`.
- `AC-APPR-016`: Given rule `deny` `bash(sudo*)` hanya di induk, when child memanggil `bash` dengan `sudo ls`, then tool ditolak.

#### REQ-APPR-007: Penjawab library

Session dengan channel `library` dapat dijawab oleh fungsi yang dipasang dengan `lana.ApproveWith`.
Penjawab ini memakai hook `approval.request` (REQ-APPR-003) dan hanya menjawab session channel
`library`. Session channel lain diteruskan ke penjawab berikutnya dengan `next`.

- `AC-APPR-017`: Given `lana.ApproveWith(fn)` dipasang dan `fn` mengembalikan `allowed_once`, when tool meminta approval dari session channel `library`, then outcome `allowed_once` dipakai. Given session channel `cli`, then `fn` tidak dipanggil.

### Compaction

#### REQ-COMP-001: Compaction otomatis

Sebelum step dimulai, jika token konteks sesi lebih dari 70% `context_window` agent, sistem harus
menjalankan compaction. Token konteks adalah `input_tokens` pada `step/end` terakhir di rantai
session. Jika `context_window` tidak diisi, nilainya 128000.

- `AC-COMP-001`: Given `context_window = 10000` dan `input_tokens` step terakhir 7001, when turn memulai step berikutnya, then `compaction/summary` tertulis sebelum `step/start`.
- `AC-COMP-002`: Given `input_tokens` step terakhir tepat 7000, when turn memulai step berikutnya, then tidak ada `compaction/summary`.
- `AC-COMP-003`: Given `context_window` tidak diisi, when ambang dihitung, then ambang memakai 128000 sehingga batasnya 89600 token.

#### REQ-COMP-002: Compaction manual

`CompactNow(session)` harus memakai provider dan model efektif agent. Hasilnya berupa
`compaction/summary` dengan `upto_seq` sama dengan seq event terakhir sebelum summary. Event lama
tidak dihapus. Pemanggilan saat turn berjalan atau session tanpa event ditolak.

- `AC-COMP-004`: Given session dengan riwayat dan tanpa turn berjalan, when `CompactNow` dipanggil, then `compaction/summary` tertulis dengan `upto_seq` seq terakhir sebelumnya, `tokens_before`, dan `tokens_after`, dan jumlah event lama tidak berkurang.
- `AC-COMP-005`: Given turn berjalan, when `CompactNow` dipanggil, then ditolak dengan `ErrTurnActive`; given session tanpa event, then ditolak dengan `ErrNothingToCompact`.

#### REQ-COMP-003: Konteks terlalu panjang

Jika provider mengembalikan `core.ErrContextTooLong`, sistem harus menjalankan compaction dengan
`upto_seq` tepat sebelum `step/start` step yang sedang berjalan, lalu mengulang request satu kali
dengan riwayat yang diderive ulang. Jika ulangan juga gagal, turn berakhir dengan `turn/end`
reason `error`.

- `AC-COMP-006`: Given request pertama mengembalikan `ErrContextTooLong` dan compaction berhasil, when request diulang, then request kedua berhasil dan turn berlanjut.
- `AC-COMP-007`: Given request ulang juga mengembalikan `ErrContextTooLong`, when proses selesai, then turn berakhir dengan reason `error` dan tidak ada percobaan ketiga.

## Komitmen contract

| Requirement | Komitmen yang terlihat | Operasi contract |
|---|---|---|
| `REQ-STORE-001`, `REQ-STORE-002`, `REQ-STORE-004` | File database, pragma, migrasi, dan trigger append-only | `API-STORE-001` |
| `REQ-STORE-003`, `REQ-STORE-005`, `REQ-STORE-015` | Append atomik, seq, turn tunggal, broadcast setelah commit | `API-STORE-004` |
| `REQ-STORE-006` | Create dan Open dengan resume repair | `API-STORE-002`, `API-STORE-003` |
| `REQ-STORE-007` | Fork di batas turn | `API-STORE-008` |
| `REQ-STORE-008`, `REQ-STORE-009` | Baca event, daftar session, prefix | `API-STORE-005` sampai `API-STORE-007` |
| `REQ-STORE-010` | Lease sesi | `API-STORE-009` |
| `REQ-STORE-011` | Riwayat model | `API-STORE-010` |
| `REQ-STORE-012`, `REQ-STORE-013`, `REQ-STORE-014` | Rebuild projection, versi payload, prune | `API-STORE-011` sampai `API-STORE-013` |
| `REQ-STORE-016` | Modul `sqlite_store` dan path database | `API-STORE-001` |
| `REQ-PERM-001` | Pola aturan dan validasi config | `API-PERM-001`, `API-PERM-002` |
| `REQ-PERM-002` to `REQ-PERM-006` | Evaluasi tool, workspace, grant, hasil tolak | `API-PERM-003` |
| `REQ-APPR-001` | Policy session | `API-APPR-001` |
| `REQ-APPR-002`, `REQ-APPR-004`, `REQ-APPR-005` | Audit, timeout, cancel, fail-closed | `API-APPR-002` |
| `REQ-APPR-003` | Rantai penjawab | `API-APPR-003` |
| `REQ-APPR-006` | Batas izin subagent | `API-APPR-004` |
| `REQ-APPR-007` | Penjawab library | `API-APPR-005` |
| `REQ-COMP-001`, `REQ-COMP-003` | Compaction otomatis dan retry | `API-COMP-002` |
| `REQ-COMP-002` | Compaction manual | `API-COMP-001` |

## Matriks state dan operasi

| State saat ini | Operasi | Hasil | Boleh |
|---|---|---|---:|
| Session idle | `Append` `turn/start` | Status `running` | Ya |
| Session running | `Append` `turn/start` | `ErrTurnActive` | Tidak |
| Session running | `Append` `turn/end` untuk turn aktif | Status `idle` | Ya |
| Session tanpa lease | `Open` | `ErrLeaseNotHeld` | Tidak |
| Session dengan turn yatim, lease dipegang | `Open` | Resume repair, lalu status `idle` | Ya |
| Session idle atau running | `Fork` di `turn/end` yang sudah ada | Session baru, tanpa perubahan induk | Ya |
| Session idle atau running | `Fork` di seq selain `turn/end` | `ErrForkNotAtTurnBoundary` | Tidak |
| Session running | `CompactNow` | `ErrTurnActive` | Tidak |
| Session idle | `CompactNow` | `compaction/summary` tertulis | Ya |
| Session tanpa child | `Prune` | Session dan event hilang | Ya |
| Session dengan child | `Prune` | `ErrSessionHasChildren` | Tidak |
| Approval menunggu jawaban | Penjawab mengembalikan outcome | `approval/decided` tertulis | Ya |
| Approval menunggu jawaban | Context dibatalkan | `approval/decided` `cancelled` | Ya |

## Kasus gagal

| Kondisi | Perilaku yang diharapkan | Kode hasil |
|---|---|---|
| Database tidak bisa dibuka | Store tidak dipasang, boot gagal dengan path | `error` dengan path |
| `user_version` lebih tinggi | Database tidak diubah | `ErrSchemaTooNew` |
| `SQLITE_BUSY` lebih dari 5 detik | Transaksi di-rollback, tidak ada event parsial | error ke pemanggil |
| Store ditutup saat ada tulis tertunda | Tulis tertunda gagal, tidak ada event parsial | `ErrStoreClosed` |
| Lease dipegang proses hidup | Lease baru ditolak | `*core.LeaseHeldError` |
| Prefix terlalu pendek atau ambigu | Pencarian ditolak | `core.ErrPrefixTooShort`, `core.ErrAmbiguousPrefix` |
| Fork di seq bukan turn/end | Session tidak dibuat | `ErrForkNotAtTurnBoundary` |
| Pola rule tidak valid | Config ditolak saat dimuat | error validasi config |
| `EvalSymlinks` gagal selain path belum ada | Tool ditolak | alasan di `ToolDecision.Reason` |
| `approval/asked` gagal ditulis | Penjawab tidak dipanggil | outcome `unavailable` |
| `approval/decided` gagal ditulis | Tool tidak berjalan | `ToolDecision.Allowed = false` |
| Penjawab error atau outcome tidak dikenal | Permintaan selesai sebagai `unavailable` | outcome `unavailable` |
| Ringkasan gagal saat compaction otomatis | Turn lanjut tanpa compaction dan log warn | tidak ada error ke user |
| Ringkasan gagal saat `CompactNow` manual | Permintaan gagal, session tidak berubah | error ke pemanggil |
| Request ulang juga `ErrContextTooLong` | Turn berakhir | `turn/end` reason `error` |

## Otorisasi dan keamanan

- Store hanya dibuka oleh proses pemilik `LANA_HOME`. Tidak ada autentikasi tambahan. File database mode `0600`.
- Penjawab approval hanya menjawab session miliknya. Session dari channel lain tidak boleh dijawab.
- Session tidak bisa menaikkan policy di atas `permission` agent tanpa event `approval/policy` yang tercatat.
- Rule `deny` berlaku di semua policy, termasuk `trust`.
- `approval/asked` dan `approval/decided` tidak memuat argumen tool dan tidak masuk log level `info`.
- Argumen tool tetap tersimpan di `tool/call`. Redaksi secret di argumen tool adalah tanggung jawab spec #3.

## Integritas data dan konkurensi

- Seq unik dan berurutan per session. Tidak ada celah.
- `session_events` hanya bertambah. Pengecualian hanya prune session utuh.
- Paling banyak satu turn terbuka per session.
- Setiap `tool/call` berakhir dengan satu `tool/result`, termasuk saat proses berhenti (lewat resume repair).
- Setiap `approval/asked` berpasangan dengan tepat satu `approval/decided`.
- Dua proses tidak memegang lease yang sama untuk session yang sama.
- Tulis di satu database tidak boleh terjadi bersamaan (`BEGIN IMMEDIATE`).

## Aksesibilitas

Tidak berlaku. Spec ini tidak menambah antarmuka pengguna. Tampilan approval ada di spec #1.

## Skenario

### SC-001: Approval sekali, lalu session yang sama

**Given** agent `coder` dengan `permission = "ask"` dan workspace `~/coding/x`
**When** model memanggil `bash` dengan `cargo test`, lalu pemilik menjawab `allowed_session`, lalu model memanggil `cargo test` lagi di session yang sama
**Then** panggilan pertama membuat `approval/asked` dan `approval/decided`, panggilan kedua berjalan tanpa approval baru

### SC-002: Proses berhenti saat menunggu approval

**Given** turn berjalan dan menunggu jawaban approval untuk call `c1`
**When** proses mati, lalu session dibuka lagi oleh proses lain yang memegang lease
**Then** store menulis `approval/decided` `unavailable` dengan reason `interrupted`, `tool/result` error untuk `c1`, dan `turn/end` `interrupted`; session menerima pesan baru

### SC-003: Fork setelah compaction

**Given** session `A` punya tiga turn, dan `compaction/summary` ditulis di akhir turn kedua
**When** pemilik membuat fork `B` dari `turn/end` terakhir `A`, lalu riwayat `B` diderive
**Then** riwayat berisi ringkasan dari `A`, event `A` setelah `upto_seq`, dan event milik `B`

## Resolusi pertanyaan

Semua pertanyaan di bawah diputuskan oleh agent karena gate dilepas pemilik (2026-10-29). Setiap
keputusan memilih opsi paling sederhana yang sesuai ADR. Pemilik bisa membatalkannya lewat revisi.

| ID | Pertanyaan | Resolusi |
|---|---|---|
| `Q-001` | Isi grant `allowed_session`: tool saja atau tool dengan argumen | Rekomendasi agent: tool dengan argumen utama literal, misalnya `bash(cargo test)`, dihitung dari `tool/call` yang dirujuk. Ini paling sempit dan tidak perlu menyalin argumen ke `approval/asked`. |
| `Q-002` | Semantik wildcard pada argumen | Rekomendasi agent: argumen `command` bebas wildcard. Argumen path: `*` dan `?` tidak melewati `/`, `**` melewati `/`. |
| `Q-003` | Tool mana yang dibatasi workspace | Rekomendasi agent: tool dengan argumen `path`. `bash` tidak dicek, sesuai ADR 0004 yang tidak punya sandbox di v1. |
| `Q-004` | Kapan resume repair berjalan | Rekomendasi agent: saat ada `turn/start` tanpa `turn/end`. Session idle tidak menerima event baru. |
| `Q-005` | Approval yang belum dijawab saat resume | Rekomendasi agent: tulis `approval/decided` outcome `unavailable` reason `interrupted`, supaya pasangan audit lengkap. |
| `Q-006` | Lokasi `context_window`: per agent atau per model | Rekomendasi agent: per agent, `[agents.<nama>] context_window`, default 128000. Schema config spec #2 tidak punya tabel per model. `/model` tidak mengubah nilai ini. |
| `Q-007` | Ukuran konteks untuk ambang 70% | Rekomendasi agent: `input_tokens` dari `step/end` terakhir di rantai session. |
| `Q-008` | Isi `tokens_before` dan `tokens_after` pada `compaction/summary` | Rekomendasi agent: `tokens_before` memakai nilai Q-007. `tokens_after` memakai `output_tokens` panggilan ringkasan. |
| `Q-009` | Compaction saat `ErrContextTooLong` | Rekomendasi agent: listener `llm.stream` dipasang di depan. `upto_seq` = seq tepat sebelum `step/start` step aktif. Ringkasan memakai adapter provider langsung, tanpa hook, supaya tidak rekursif. |
| `Q-010` | Export JSONL dan prune | Rekomendasi agent: `Prune` ada di store (ADR 0009, ADR 0005). `Export` tidak dibuat karena tidak ada consumer di scope. Bisa dibuka lagi di spec CLI. |
| `Q-011` | Status projection dan judul session | Rekomendasi agent: `status` bernilai `idle` atau `running`. `title` berisi 40 rune pertama `user/message` pertama jika kosong. |
| `Q-012` | Apakah `tool/call` masuk riwayat model | Rekomendasi agent: tidak. Blok `tool_use` sudah ada di `assistant/message`. |
| `Q-013` | Bentuk `compaction/summary` di riwayat | Rekomendasi agent: pesan role `user` berisi teks ringkasan dengan label tetap di awal. |
| `Q-014` | Policy dibaca dari log atau cache | Rekomendasi agent: dibaca dari log pada setiap evaluasi. Ini sederhana dan benar untuk `AC-APPR-004`. |
| `Q-015` | Peran `Create` terhadap lease | Rekomendasi agent: `Create` tidak mengambil lease. Caller memanggil `TryLease`, lalu `Open`. `Open` menolak tanpa lease. |
| `Q-016` | `approval/decided` gagal ditulis setelah izin | Rekomendasi agent: tool tidak berjalan (fail-closed), sesuai ADR 0004. |
| `Q-017` | Batas izin subagent untuk policy dan rule | Rekomendasi agent: policy = nilai paling ketat. `deny` = gabungan induk dan child. Rule `allow` child tetap dievaluasi di session child. |
| `Q-018` | Di mana workspace disimpan | Rekomendasi agent: di `session/created` sebagai field `workspace` (absolut, sudah `EvalSymlinks`), dan di kolom projection `workspace`. |
| `Q-019` | Mode file database | Rekomendasi agent: `lana.db` dibuat `0600`. File `-wal` dan `-shm` juga di-chmod `0600` jika ada. |
| `Q-020` | Turn ganda dalam satu session | Rekomendasi agent: store menolak `turn/start` kedua dengan `ErrTurnActive`. |
| `Q-021` | Status `tool/result` untuk penolakan | Rekomendasi agent: `rejected` untuk penolakan policy, workspace, dan approval. `error` untuk error runtime tool dan untuk resume repair. |
| `Q-022` | `turn/end` mana yang valid sebagai batas fork | Rekomendasi agent: semua `turn/end` valid, termasuk reason `error`, `cancelled`, dan `interrupted`. |
| `Q-023` | Modul apa yang masuk profile | Rekomendasi agent: `sqlite_store`, `permission`, `approval`, dan `compaction` masuk base profile yang dipakai `chat` dan `run`. Profile `minimal` di ARCHITECTURE §3 perlu direvisi, lihat contract. Direvisi: `minimal` = base (`Q-031`). |
| `Q-024` | Kegagalan ringkasan saat compaction otomatis | Rekomendasi agent: log level warn, turn lanjut tanpa compaction. Kegagalan `CompactNow` manual dikembalikan sebagai error. |
| `Q-025` | Default `permission` jika tidak diisi | Rekomendasi agent: `ask`, sesuai contoh `[agents.default]` di spec #1. |
| `Q-026` | Pesan penolakan untuk model | Rekomendasi agent: satu kalimat bahasa Indonesia. Alasan spesifik (pola, outcome) ikut disebut. Teks tepatnya dimiliki contract. |
| `Q-027` | Bentuk `SessionStore` dan `TryLease` | Rekomendasi agent (rekonsiliasi): signature spec #2 menang (`CQ-022` spec #2). `Create` dan `Open` mengembalikan `SessionInfo`. `TryLease` mengembalikan `release func() error`. Penolakan lease memakai `*core.LeaseHeldError`. Keputusan `Q-015` tetap. |
| `Q-028` | `system/message` di riwayat model | Rekomendasi agent (rekonsiliasi): `system/message` tidak masuk riwayat `DeriveMessages`. Pesan terakhirnya dipakai sebagai `ChatRequest.System` (spec #3 `Q-028`). |
| `Q-029` | Opsi approval library | Rekomendasi agent (rekonsiliasi): `lana.ApproveWith` disediakan spec ini (`REQ-APPR-007`, `API-APPR-005`). Dipasang sebagai opsi `lana.New` (spec #3 `Q-030`). |
| `Q-030` | Lokasi grammar pola `rules` | Rekomendasi agent (rekonsiliasi): paket `internal/pattern`, dipakai `config` (validasi saat load, `CQ-030` spec #2) dan `tools/permission`. Grammar tidak berubah. |
| `Q-031` | Isi profile base | Rekomendasi agent (rekonsiliasi): `sqlite_store`, `permission`, `approval`, dan `compaction` ada di base (`API-CONF-009` spec #2, `CQ-031`). `minimal` sama dengan base. Keputusan `Q-023` berlaku. |
| `Q-032` | Pemilik field payload event | Rekomendasi agent (rekonsiliasi): tabel field di `API-CORE-008` (spec #2, `CQ-029`). `session/created.workspace` dan `model/selection.agent` disetujui (spec #3 `Q-017` dan `Q-022`). |

## Blocking Open Questions

None.

## Cakupan requirement

| Requirement | Acceptance criteria | Sumber |
|---|---|---|
| `REQ-STORE-001` | `AC-STORE-001`, `AC-STORE-002` | ADR 0005, ADR 0013, `Q-019` |
| `REQ-STORE-002` | `AC-STORE-003`, `AC-STORE-004` | ADR 0005 |
| `REQ-STORE-003` | `AC-STORE-005`, `AC-STORE-006` | ADR 0005, spec #1 design (Lock session) |
| `REQ-STORE-004` | `AC-STORE-007`, `AC-STORE-008` | ADR 0009 |
| `REQ-STORE-005` | `AC-STORE-009`, `AC-STORE-010`, `AC-STORE-011` | ADR 0009, ARCHITECTURE §5, `Q-020` |
| `REQ-STORE-006` | `AC-STORE-012`, `AC-STORE-013`, `AC-STORE-014`, `AC-STORE-015`, `AC-STORE-016` | ADR 0009, diagram 0004 (resume), `Q-004`, `Q-005`, `Q-015`, spec #1 `AC-SESS-005` |
| `REQ-STORE-007` | `AC-STORE-017`, `AC-STORE-018` | ADR 0009, spec #1 `AC-CMD-009`, `AC-CMD-010` |
| `REQ-STORE-008` | `AC-STORE-019`, `AC-STORE-020` | ADR 0009, spec #1 `AC-SESS-002`, `AC-CMD-004` |
| `REQ-STORE-009` | `AC-STORE-021`, `AC-STORE-022`, `AC-STORE-023` | spec #1 API-CLI konvensi (ID session) |
| `REQ-STORE-010` | `AC-STORE-024`, `AC-STORE-025`, `AC-STORE-026`, `AC-STORE-027` | spec #1 `REQ-SESS-002`, `DQ-002` |
| `REQ-STORE-011` | `AC-STORE-028`, `AC-STORE-029`, `AC-STORE-030` | ADR 0009, diagram 0003, `Q-012` |
| `REQ-STORE-012` | `AC-STORE-031` | ADR 0005, ADR 0009 |
| `REQ-STORE-013` | `AC-STORE-032`, `AC-STORE-033` | ADR 0009 (versi format) |
| `REQ-STORE-014` | `AC-STORE-034`, `AC-STORE-035` | ADR 0009, ADR 0005, `Q-010` |
| `REQ-STORE-015` | `AC-STORE-036`, `AC-STORE-037` | ADR 0009, spec #2 `REQ-HOOK-003` |
| `REQ-STORE-016` | `AC-STORE-038` | spec #2 `REQ-CONF-002`, ARCHITECTURE §3 |
| `REQ-PERM-001` | `AC-PERM-001`, `AC-PERM-002`, `AC-PERM-003`, `AC-PERM-004` | ADR 0004, `Q-002` |
| `REQ-PERM-002` | `AC-PERM-005`, `AC-PERM-006`, `AC-PERM-007`, `AC-PERM-008`, `AC-PERM-009`, `AC-PERM-010` | ADR 0004 (urutan 1-7) |
| `REQ-PERM-003` | `AC-PERM-011`, `AC-PERM-012`, `AC-PERM-013` | ADR 0004, spec #1 `AC-CHAT-022`, `Q-003` |
| `REQ-PERM-004` | `AC-PERM-014`, `AC-PERM-015`, `AC-PERM-016` | ADR 0004, spec #1 `AC-APPR-002`, `Q-001` |
| `REQ-PERM-005` | `AC-PERM-017`, `AC-PERM-018` | spec #3 (fail-closed default), ADR 0002 |
| `REQ-PERM-006` | `AC-PERM-019` | ADR 0004, `Q-021` |
| `REQ-APPR-001` | `AC-APPR-001`, `AC-APPR-002`, `AC-APPR-003`, `AC-APPR-004` | ADR 0004, spec #1 `AC-CMD-005`, `AC-CMD-014` |
| `REQ-APPR-002` | `AC-APPR-005`, `AC-APPR-006` | ADR 0004 (audit), spec #1 `AC-APPR-001`, `AC-APPR-003` |
| `REQ-APPR-003` | `AC-APPR-007`, `AC-APPR-008`, `AC-APPR-009`, `AC-APPR-010` | ADR 0004, ADR 0014 |
| `REQ-APPR-004` | `AC-APPR-011`, `AC-APPR-012` | spec #1 `AC-APPR-004`, `AC-APPR-005`, ADR 0004 |
| `REQ-APPR-005` | `AC-APPR-013`, `AC-APPR-014` | ADR 0004 (fail-closed) |
| `REQ-APPR-006` | `AC-APPR-015`, `AC-APPR-016` | ADR 0011, `Q-017` |
| `REQ-APPR-007` | `AC-APPR-017` | ADR 0014, `Q-029` |
| `REQ-COMP-001` | `AC-COMP-001`, `AC-COMP-002`, `AC-COMP-003` | ADR 0005, ADR 0009, diagram 0002 (failure handling), `Q-006`, `Q-007` |
| `REQ-COMP-002` | `AC-COMP-004`, `AC-COMP-005` | spec #1 `AC-CMD-008`, `Q-008` |
| `REQ-COMP-003` | `AC-COMP-006`, `AC-COMP-007` | diagram 0002 (failure handling), `Q-009` |

Setiap requirement dan acceptance criterion muncul tepat satu kali.

## Approval

- [x] Scope dan pengecualian eksplisit.
- [x] Perilaku sukses dan gagal terukur.
- [x] Otorisasi eksplisit.
- [x] Transisi state dan efek destruktif eksplisit.
- [x] Default, urutan, retry, dan idempotensi eksplisit jika berlaku.
- [x] Setiap requirement dan acceptance criterion punya ID stabil dan sumber.
- [x] Keberlakuan contract eksplisit.
- [x] Blocking Open Questions bernilai `None`.
- **Di-approve oleh**: Ridho (gate dilepas 2026-10-29)
- **Di-approve pada**: 2026-10-29 (revisi 1). Revisi 2 rekonsiliasi oleh agent atas nama pemilik (gate dilepas 2026-10-29).
