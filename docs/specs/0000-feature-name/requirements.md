# Requirements: <Nama Fitur>

- **Issue**: `#<nomor>`
- **Status**: Draft
- **Revisi**: <integer, mulai 1>
- **Pemilik**: <nama>
- **Contract**: [api-contract.md](api-contract.md) — Diperlukan / Tidak diperlukan
- **ADR terkait**: [Index ADR](../../adr/README.md) — <link atau `—`>
- **Diagram terkait**: [Index diagram](../../diagram/README.md) — <link atau `—`>

## Sumber

| Sumber | Referensi | Dipakai untuk |
|---|---|---|
| Issue | `#<nomor>` | cerita user, scope |
| ADR | <link atau `—`> | keputusan yang mengikat |
| Referensi luar | <link atau `—`> | perilaku pembanding |

Hal yang tidak didukung sumber atau approval eksplisit masuk ke Blocking Open Questions. Jangan
menyajikan asumsi agent sebagai requirement.

## Tujuan

Satu paragraf tentang hasil untuk user. Jangan jelaskan solusinya.

## Aktor

- **<Aktor>** — <tujuan dan batas izinnya>.

## Definisi

- **<Istilah>** — <satu arti stabil untuk fitur ini>.

## Scope

### Termasuk

- <kapabilitas yang terlihat>

### Tidak termasuk

- <perilaku yang dikecualikan atau fitur lanjutan>

## Asumsi

- <asumsi yang sudah dikonfirmasi> — Sumber: <issue/ADR/keputusan>.

## Aturan perilaku

### REQ-<KAPABILITAS>-001: <Judul aturan>

Sistem harus <perilaku yang terlihat>.

#### Acceptance criteria

- `AC-<KAPABILITAS>-001`: Given <state>, when <aksi>, then <hasil terukur>.

## Komitmen contract

Hapus bagian ini jika fitur tidak mengubah antarmuka luar.

| Requirement | Komitmen yang terlihat | Operasi contract |
|---|---|---|
| `REQ-...` | <perilaku, exit code, atau jaminan kompatibilitas> | `API-...` |

## Matriks state dan operasi

Hapus bagian ini jika tidak ada perilaku yang bergantung pada state.

| State saat ini | Operasi | Hasil | Boleh |
|---|---|---|---:|
| <state> | <operasi> | <state baru atau efek> | Ya/Tidak |

## Kasus gagal

| Kondisi | Perilaku yang diharapkan | Kode hasil |
|---|---|---|
| <kondisi> | <hasil aman yang terlihat> | <exit code, error code, atau pertanyaan> |

## Otorisasi dan keamanan

- <siapa boleh melakukan apa; cara penegakannya>

## Integritas data dan konkurensi

- <invariant yang harus bertahan saat retry, race, atau gagal sebagian>

## Aksesibilitas

- <keyboard, fokus, tanpa ketergantungan warna, atau `Tidak berlaku`>

## Skenario

### SC-001: <Judul>

**Given** <state awal>
**When** <aksi>
**Then** <hasil yang terlihat>

## Blocking Open Questions

| ID | Pertanyaan | Mengendalikan | Referensi yang diminta | Pemilik | Status | Resolusi |
|---|---|---|---|---|---|---|
| `Q-001` | <keputusan yang dibutuhkan> | <perilaku atau contract> | <contoh, screenshot, arah> | <nama> | Open | — |

Tulis `None` hanya jika semua pertanyaan yang memengaruhi perilaku sudah terjawab. Agent wajib
menanyakan setiap pertanyaan Open dan menunggu jawaban sebelum approval atau design.

## Cakupan requirement

| Requirement | Acceptance criteria | Sumber |
|---|---|---|
| `REQ-<KAPABILITAS>-001` | `AC-<KAPABILITAS>-001` | <issue/ADR/keputusan> |

Setiap requirement dan acceptance criterion muncul tepat satu kali.

## Approval

- [ ] Scope dan pengecualian eksplisit.
- [ ] Perilaku sukses dan gagal terukur.
- [ ] Otorisasi eksplisit atau dinyatakan tidak berlaku.
- [ ] Transisi state dan efek destruktif eksplisit atau tidak berlaku.
- [ ] Default, urutan, retry, dan idempotensi eksplisit jika berlaku.
- [ ] Setiap requirement dan acceptance criterion punya ID stabil dan sumber.
- [ ] Keberlakuan contract eksplisit.
- [ ] Blocking Open Questions bernilai `None`.
- **Di-approve oleh**: —
- **Di-approve pada**: —
