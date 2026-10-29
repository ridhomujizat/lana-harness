# Spesifikasi Fitur

Folder ini berisi spesifikasi fitur yang siap diimplementasi. Satu spec adalah satu paket yang
bisa di-review, berisi requirements, contract antarmuka (jika ada), design, dan task:

```text
docs/specs/<issue>-<slug-fitur>/
├── requirements.md
├── api-contract.md       # wajib jika CLI, config, HTTP, JSON-RPC, atau event berubah
├── design.md
└── tasks.md
```

`<issue>` adalah nomor GitHub issue tanpa `#`, misalnya `12-cli-tui`. GitHub issue adalah tracker
untuk status dan diskusi. Spec di repository adalah source of truth yang ber-versi dan dibaca
bersama kode.

## Mulai cepat

Mulai dari index di bawah, lalu baca hanya artefak yang dibutuhkan untuk keputusan saat ini:

| Tujuan | Baca |
|---|---|
| Memahami scope, aturan, atau acceptance | `requirements.md` |
| Review perintah CLI, flag, config, endpoint, atau payload | `requirements.md` + operasi terkait di `api-contract.md` |
| Implementasi | requirement yang ditugaskan + operasi contract + komponen terkait di `design.md` |
| Mengerjakan atau memverifikasi pekerjaan | entri terkait di `tasks.md` |
| Memahami keputusan atau alur yang tahan lama | [ADR](../adr/README.md) atau [diagram](../diagram/README.md) yang ditautkan dari `design.md` |

Cara membuat paket baru:

1. Buat GitHub issue untuk fitur.
2. Salin `0000-feature-name/` ke `<issue>-<slug-fitur>/` dan tambahkan baris di index.
3. Tulis dan approve `requirements.md`. Pertanyaan yang belum terjawab tetap memblokir.
4. Tulis dan approve `api-contract.md` jika antarmuka yang terlihat dari luar berubah. Jika tidak,
   tandai `Tidak diperlukan` dan hapus bagian operasi.
5. Tulis dan approve `design.md` berdasarkan requirements dan contract yang sudah di-approve.
6. Tulis `tasks.md`, implementasi satu slice, lalu catat bukti verifikasi yang sebenarnya.

Jangan baca semua artefak secara default. Ikuti link dan ID stabil untuk tugas saat ini:
`REQ-*` → `AC-*` → `API-*` (jika ada) → komponen design → `T-*` → bukti.

## Tanggung jawab artefak

| Artefak | Memiliki | Tidak boleh memiliki |
|---|---|---|
| `requirements.md` | perilaku yang terlihat, aturan, scope, acceptance criteria, open question | layout kode, langkah implementasi |
| `api-contract.md` | bentuk persis perintah CLI, flag, exit code, key config, endpoint HTTP, method JSON-RPC, event | alasan bisnis, pemetaan internal |
| `design.md` | solusi spesifik repository, pemetaan internal, data model, failure handling, strategi verifikasi | requirement produk, duplikasi schema contract, progres task |
| `tasks.md` | slice implementasi berurutan, cakupan requirement, bukti penyelesaian | perilaku baru atau keputusan arsitektur |
| [`docs/adr/`](../adr/README.md) | keputusan lintas fitur dan tradeoff | daftar task fitur |
| [`docs/diagram/`](../diagram/README.md) | tampilan visual alur, batas, topologi, data model yang tahan lama | duplikasi prosa spec |

## Lifecycle

Setiap file punya gate approval sendiri:

```text
requirements: Draft → Approved
api-contract: Draft → Approved (jika berlaku)
design:       Draft → Approved
tasks:        Draft → Approved → In Progress → Completed
```

Urutan kerja: approve requirements, lalu contract (jika berlaku), lalu design, lalu tasks.
Implementasi tidak boleh mulai selama masih ada open question yang memblokir atau artefak
sebelumnya belum di-approve.

Agent wajib menanyakan setiap pertanyaan yang memblokir ke pemilik dan menunggu jawaban. Mencatat
pertanyaan di file tidak berarti design atau implementasi boleh lanjut. Pertanyaan sebaiknya
meminta referensi: issue, screenshot, contoh output, implementasi yang sudah ada, atau arah yang disukai.

Artefak arsitektur diperiksa di antara requirements dan design:

```text
Issue → requirements Approved → contract Approved → scan ADR/diagram → design Approved → tasks Approved → kode
```

ADR berstatus Accepted dan diagram yang berlaku adalah input untuk requirements dan design. Setiap
design wajib menyelesaikan Visual Architecture Check. ADR yang dibutuhkan harus Accepted sebelum
design di-approve.

## Traceability

- ID requirement: `REQ-<KAPABILITAS>-NNN`.
- ID acceptance criteria: `AC-<KAPABILITAS>-NNN`.
- ID task: `T-NNN`, dengan nomor GitHub issue atau PR jika ada.
- ID operasi contract: `API-<KAPABILITAS>-NNN`, ditautkan ke `REQ-*` dan `AC-*`.
- Setiap acceptance criterion punya tepat satu pemilik bukti.
- Setiap task yang selesai mencatat bukti test atau runtime.
- Requirements, contract (jika berlaku), dan design punya nomor revisi eksplisit sebelum tasks di-approve.

## Aturan tidak mengarang

Agent tidak boleh mengarang aturan perilaku, flag, field, default, permission, transisi state,
exit code, error code, perilaku retry, atau jaminan kompatibilitas. Perilaku yang hilang atau
bertentangan masuk ke `requirements.md` di bagian Blocking Open Questions dan menghentikan design
atau implementasi.

Agent boleh memilih detail implementasi privat paling sederhana yang sesuai repository, selama
bisa dibalik dan tidak mengubah perilaku yang terlihat.

## Hubungan dengan ADR dan diagram

- Buat ADR hanya untuk keputusan lintas fitur, kontraktual, atau sulit dibalik.
- Diagram wajib untuk batas baru, aktor atau sistem luar, alur multi-komponen, lifecycle atau
  transisi state, relasi entity, alur konkurensi/retry/gagal, arah dependency, atau topologi runtime.
- Diagram tidak wajib untuk field, validasi, label, refactor lokal, atau perilaku satu langkah.
- `design.md` menautkan ADR dan diagram, tidak menyalin isinya.
- Diagram yang hanya menjelaskan satu fitur tetap di `design.md`. Pindahkan ke `docs/diagram/` jika dipakai ulang.
- Tolak diagram dekoratif: diagram berguna punya minimal dua node/state/entity dan satu relasi.

## Index

| Issue | Fitur | Requirements | Contract | Design | Tasks |
|---|---|---|---|---|---|
| — | Template paket | [Template](0000-feature-name/requirements.md) | [Template](0000-feature-name/api-contract.md) | [Template](0000-feature-name/design.md) | [Template](0000-feature-name/tasks.md) |
| [#1](https://github.com/ridhomujizat/lana-harness/issues/1) | CLI dan TUI chat | [Approved r4](1-cli-tui/requirements.md) | [Approved r1](1-cli-tui/api-contract.md) | Belum dibuat | Belum dibuat |
