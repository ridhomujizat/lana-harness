# 0010. Susun runtime dengan profile dan config patch

- **Status**: Accepted (detail implementasi Rust diganti oleh [0013](0013-gunakan-go-sebagai-single-binary.md); keputusan arsitekturnya tetap)
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0002](0002-core-dari-modul-service-dan-hook.md), [0012](0012-acp-dan-sdk-json-rpc.md)

## Context

Binary `lana` dipakai dengan beberapa bentuk: daemon dengan semua channel, one-shot di script,
server ACP untuk editor, dan server SDK untuk program lain. Tiap bentuk butuh kumpulan modul
berbeda. Contoh: one-shot tidak butuh Telegram dan scheduler; ACP tidak butuh web dashboard.

`dsh` menyelesaikan ini dengan **profile** (komposisi bernama) yang menumpuk **bundle**, lalu
**patch** yang menargetkan baris berdasarkan `id`. Perintah `dsh --profile web --dump-config`
menampilkan pohon akhir. Pemilik meminta pola profile dan patch masuk v1.

## Decision

Konfigurasi efektif dibangun dari **lapisan berurutan**. Lapisan belakang menimpa lapisan depan.

1. **Base** — dikompilasi ke binary. Modul bersama: provider, tools, store, approval, prompt, compaction.
2. **Profile bawaan** — dikompilasi ke binary: `daemon`, `headless`, `acp`, `sdk`, `minimal`.
3. **Patch profile user** — `~/.lana/profiles/<profile>.toml`.
4. **Config user** — `~/.lana/config.toml` (provider, agent, route, MCP, channel).
5. **Overlay CLI** — `--patch <file>`, bisa diulang.

Bentuk baris modul:

```toml
[[modules]]
id = "channel.telegram"     # identitas baris, target patch
module = "telegram"         # implementasi Module yang dikompilasi
disabled = false
config = { poll_timeout_secs = 30 }
```

Aturan merge:

- `[[modules]]` digabung berdasarkan `id`. Baris dengan `id` yang sama **mengganti seluruh**
  `config` baris itu, mengikuti `dsh`. `disabled = true` mematikan baris. Baris dengan `id` baru ditambahkan.
- Field `after = "<id>"` atau `before = "<id>"` mengatur posisi baris baru.
- Tabel lain (`providers`, `agents`, `mcp`, `channels`) digabung per key secara rekursif.
- `[[routes]]` tidak digabung. Lapisan yang paling akhir mendefinisikan `routes` menggantikan seluruh daftar.

Profile bawaan:

| Profile | Subcommand | Modul tambahan di atas base |
|---|---|---|
| `daemon` | `lana daemon` | http, web, telegram, whatsapp, scheduler, router |
| `headless` | `lana run` | runner one-shot, output ke stdout. Approval policy `never` kecuali `--ask` |
| `acp` | `lana acp` | server ACP di stdio (lihat [0012](0012-acp-dan-sdk-json-rpc.md)) |
| `sdk` | `lana sdk` | server SDK JSON-RPC di stdio atau unix socket |
| `minimal` | `lana --profile minimal` | hanya provider, store, dan agent loop. Untuk test dan embedding |

`lana chat` memakai profile `daemon` jika daemon sudah jalan (attach lewat HTTP), atau `minimal`
ditambah channel CLI jika daemon mati.

Alat bantu:

- `lana --profile <p> --dump-config` mencetak konfigurasi efektif beserta asal tiap baris.
- `lana config check` memvalidasi semua lapisan dan melaporkan `id` yang tidak dikenal.
- Modul tidak dikenal atau dependency service yang hilang membuat boot gagal dengan pesan jelas.
  Tidak ada fallback diam-diam.

## Consequences

**Positive**
- Satu binary, banyak peran, tanpa flag yang saling bertabrakan.
- Pengguna bisa mematikan atau mengganti satu modul tanpa menyalin seluruh config.
- `--dump-config` membuat komposisi bisa diperiksa.

**Negative / tradeoffs**
- Lima lapisan merge lebih sulit dipahami daripada satu file. Dimitigasi dengan `--dump-config`.
- "Patch mengganti seluruh `config` baris" bisa mengejutkan pengguna yang mengira deep merge.

**Neutral**
- Profile custom milik user (bukan hanya patch) bisa ditambah nanti sebagai file `profiles/<nama>.toml` dengan `extends = "daemon"`.

## Alternatives considered

- **Satu `config.toml` tanpa profile** — paling sederhana. Ditolak karena ACP dan headless butuh modul berbeda.
- **Feature flag Cargo saja** — mengecilkan binary, tapi tidak bisa memilih saat runtime. Tetap dipakai
  untuk modul besar (WhatsApp, TUI), dan profile memilih di antara modul yang tersedia.
- **YAML dengan ekspresi `!!js` seperti `dsh`** — ditolak. TOML statis cukup dan lebih aman.
