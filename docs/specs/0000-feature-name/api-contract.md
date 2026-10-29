# Contract: <Nama Fitur>

- **Issue**: `#<nomor>`
- **Requirements**: [requirements.md](requirements.md)
- **Revisi requirements**: <integer>
- **Status requirements**: Draft
- **Revisi contract**: <integer, mulai 1>
- **Berlaku untuk**: CLI / config / HTTP / JSON-RPC / ACP / event / Tidak diperlukan
- **Status**: Draft

Jangan mendefinisikan perilaku baru di sini. Requirements memiliki perilaku; file ini memiliki
bentuk persis antarmuka. Jangan menulis contract terhadap requirements yang belum di-approve.

Jika fitur tidak mengubah antarmuka luar, set **Berlaku untuk** ke `Tidak diperlukan`, tulis
alasannya, dan hapus bagian operasi.

## Aturan contract

- Dokumentasikan hanya operasi baru atau yang berubah. Tautkan konvensi bersama, jangan salin.
- Setiap operasi punya ID stabil `API-<KAPABILITAS>-NNN`.
- Setiap operasi ditautkan ke minimal satu `REQ-*` dan `AC-*`.
- Tulis input, output sukses, kode gagal, otorisasi, dan kompatibilitas.
- Pakai contoh hanya jika bentuk atau edge case tidak jelas dari tabel.

## Konvensi bersama

| Hal | Keputusan atau otoritas |
|---|---|
| Exit code CLI | <tabel atau `Tidak berlaku`> |
| Output stdout dan stderr | <aturan atau `Tidak berlaku`> |
| Format config | <file dan section atau `Tidak berlaku`> |
| Autentikasi | <token, socket permission, atau `Tidak berlaku`> |
| Versi | <aturan versi atau `Tidak berlaku`> |
| Serialisasi | <JSON, waktu, ID, atau `Konvensi repository`> |

## Index operasi

| ID operasi | Jenis | Perintah/method | Path/flag/topik | Requirements | Acceptance criteria |
|---|---|---|---|---|---|
| `API-<KAPABILITAS>-001` | CLI | `lana <subcommand>` | `--flag` | `REQ-...` | `AC-...` |

## API-<KAPABILITAS>-001: <Judul operasi>

- **Tujuan**: <satu kalimat>
- **Requirements**: `REQ-...`
- **Acceptance criteria**: `AC-...`
- **Stabilitas**: Baru / Sudah ada / Sensitif kompatibilitas
- **Otorisasi**: <izin dan cara penegakan>

### Input

| Lokasi | Nama | Tipe | Wajib | Validasi dan arti |
|---|---|---|---:|---|
| Argumen/flag/env/config/body | `<nama>` | `<tipe>` | Ya/Tidak | <batasan dan arti> |

### Sukses

| Kondisi | Exit code atau status | Output |
|---|---:|---|
| <kondisi> | `<kode>` | <stdout, payload, atau efek> |

### Gagal

| Kondisi | Exit code atau status | Kode stabil | Bisa diulang | Perilaku yang terlihat |
|---|---:|---|---:|---|
| <kondisi> | `<kode>` | `<kode>` | Ya/Tidak | <hasil aman> |

### Efek samping dan jaminan

- **Transisi state**: <perubahan atau `Tidak ada`>
- **Idempotensi**: <perilaku retry atau `Tidak berlaku`>
- **Konkurensi**: <aturan atau `Tidak berlaku`>
- **Paparan data**: <yang disembunyikan atau di-redact>

### Kompatibilitas

- **Breaking change**: Ya/Tidak — <alasan>
- **Deprecation**: <pengganti atau `Tidak berlaku`>

## Traceability contract

| Operasi | Requirements | Acceptance criteria | Komponen design | Verifikasi |
|---|---|---|---|---|
| `API-...` | `REQ-...` | `AC-...` | <komponen> | <test atau probe> |

## Blocking Contract Questions

| ID | Pertanyaan | Mengendalikan | Referensi yang diminta | Pemilik | Status | Resolusi |
|---|---|---|---|---|---|---|
| `CQ-001` | <keputusan antarmuka> | <flag/exit code/format> | <contoh atau arah> | <nama> | Open | — |

Tulis `None` hanya jika contract lengkap atau `Tidak diperlukan`.

## Approval

- [ ] Requirements sudah Approved dan revisinya dicatat di atas.
- [ ] Keberlakuan contract sudah ditentukan.
- [ ] Setiap operasi punya ID stabil dan tertaut ke requirement dan acceptance criteria.
- [ ] Input, sukses, gagal, otorisasi, dan kompatibilitas eksplisit.
- [ ] Tidak ada perilaku yang hanya ada di file ini.
- [ ] Blocking Contract Questions bernilai `None`.
- **Di-approve oleh**: —
- **Di-approve pada**: —
