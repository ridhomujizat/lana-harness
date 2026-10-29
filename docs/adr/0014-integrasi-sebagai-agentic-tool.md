# 0014. Sediakan lana sebagai agentic tool untuk service lain

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0004](0004-permission-mode-per-agent.md), [0008](0008-interface-cli-channel-http-dan-web-dashboard.md), [0009](0009-session-sebagai-event-log-append-only.md), [0012](0012-acp-dan-sdk-json-rpc.md), [0013](0013-gunakan-go-sebagai-single-binary.md)

## Context

Pemilik ingin `lana` tidak hanya dipakai langsung, tetapi juga dipanggil oleh service lain
sebagai **agentic tool**: service mengirim tugas, `lana` menjalankan agent dengan tool dan
permission-nya, lalu mengembalikan hasil. Pemakai yang dibayangkan:

- Service Go milik pemilik yang ingin agent di dalam prosesnya sendiri.
- Agent harness lain (Claude Code, Cursor, agent kustom) yang memanggil tool lewat MCP.
- Service dalam bahasa apa pun lewat HTTP.
- Agent lain yang memakai protokol Agent2Agent (A2A).

ADR 0012 sudah menyediakan ACP (untuk editor) dan SDK JSON-RPC (stdio dan unix socket). Keduanya
tidak menjangkau pemakai di atas.

## Decision

`lana` menyediakan **empat permukaan integrasi** di atas `Context` yang sama. Tidak ada jalur
eksekusi terpisah; semua permukaan memakai agent loop, event log, dan permission yang sama.

| Urutan | Permukaan | Bentuk | Profile |
|---|---|---|---|
| 1 | **Go library** | Package publik `lana` dan `core` | — (dipakai di proses service) |
| 2 | **MCP server** | `lana mcp`, transport stdio dan streamable HTTP | `mcp` |
| 3 | **REST + SSE API** | Bagian dari HTTP server daemon (ADR 0008) | `daemon` |
| 4 | **A2A** | Agent card dan endpoint A2A | `daemon` + build tag `a2a` |

**1. Go library.**

```go
rt, err := lana.New(ctx, lana.Options{
    Home:    "/var/lib/myservice/lana",   // atau Config langsung tanpa file
    Profile: "headless",
    Tools:   []core.Tool{myservice.LookupOrderTool()},  // tool milik service
    Approval: lana.ApproveWith(myApprover),            // atau lana.PolicyNever
})
defer rt.Close()

res, err := rt.Run(ctx, lana.Request{Agent: "ops", Prompt: "cek order 123"})
// res.Text, res.SessionID, res.Usage, res.StopReason

stream, err := rt.Stream(ctx, lana.Request{...})  // iterator event
```

- API publik: package `lana` (fasad) dan `core` (tipe `Tool`, `Module`, hook, event). Package lain
  boleh di-import tetapi tidak dijamin stabil sebelum v1.0.0.
- Service bisa mendaftarkan tool dan modul sendiri; tool itu melewati permission engine yang sama.
- Approval di library adalah fungsi penjawab (`approval.request`) milik service. Tanpa penjawab, hasilnya `unavailable` (fail-closed).
- Versi mengikuti semver lewat tag Git. Sebelum `v1.0.0`, perubahan API publik dicatat di CHANGELOG.

**2. MCP server.** Memakai `modelcontextprotocol/go-sdk`.

| Tool MCP | Input | Output |
|---|---|---|
| `lana_run` | `agent?`, `prompt`, `session_id?` | Teks jawaban final, `session_id`, `stop_reason` |
| `lana_agents` | — | Daftar agent dan deskripsinya |
| `lana_sessions` | `limit?` | Daftar session milik client MCP ini |

- Progress dikirim sebagai notifikasi progress MCP selama turn berjalan.
- Approval: jika client mendukung **elicitation**, approval dikirim sebagai elicitation. Jika tidak, policy session `never`.
- Agent yang boleh dipanggil lewat MCP dibatasi `[protocol.mcpserver] agents = [...]`.
- Transport streamable HTTP mewajibkan bearer token, sama seperti HTTP API.

**3. REST + SSE API.** Menggantikan dan memperluas tabel HTTP di ADR 0008:

| Method | Path | Fungsi |
|---|---|---|
| `POST` | `/v1/runs` | Jalankan satu turn; `?wait=true` menunggu hasil, default mengembalikan `session_id` |
| `POST` | `/v1/sessions` | Buat session |
| `POST` | `/v1/sessions/{id}/messages` | Kirim pesan |
| `GET` | `/v1/sessions/{id}/events?from_seq=` | SSE event session |
| `POST` | `/v1/sessions/{id}/cancel` | Batalkan turn |
| `POST` | `/v1/approvals/{id}` | Jawab approval |
| `GET` | `/v1/agents` | Daftar agent |

- Spesifikasi OpenAPI 3.1 ditulis tangan di `docs/specs/` dan menjadi contract. Handler diuji terhadapnya.
- Autentikasi bearer token per client (`[[api.tokens]]` dengan nama dan daftar agent yang diizinkan).
- Method SDK JSON-RPC (ADR 0012) dipetakan ke handler yang sama.

**4. A2A.** `lana` sebagai A2A server: agent card di `/.well-known/agent-card.json` (satu skill per
agent yang diekspos), task dipetakan ke session, status task ke `turn/*`, artifact ke jawaban final.
Approval dipetakan ke status `input-required`. `lana` sebagai A2A client (memanggil agent lain)
ditambah setelahnya sebagai tool `a2a_call`.

**Aturan bersama:**

- Setiap session dari permukaan integrasi mencatat `channel` (`library`, `mcp`, `api`, `a2a`) dan
  identitas client di `session/created`.
- Agent yang tidak tercantum dalam allowlist permukaan tidak bisa dipanggil lewat permukaan itu.
- Workspace untuk permukaan jaringan diambil dari `workspace` agent, bukan dari client.
- Tidak ada permukaan yang bisa menaikkan policy di atas policy agent.

## Consequences

**Positive**
- Satu agent bisa dipakai dari terminal, chat, editor, service Go, harness lain, dan agent lain.
- MCP membuat `lana` langsung bisa dipakai oleh Claude Code dan harness lain yang mendukung MCP.
- Event log memudahkan audit tindakan yang dipicu service lain.

**Negative / tradeoffs**
- Empat permukaan berarti empat contract yang harus dijaga.
- Library publik membatasi kebebasan refactor package `lana` dan `core`.
- A2A dan elicitation MCP masih berkembang; upgrade library bisa memaksa perubahan.

**Neutral**
- ACP dan SDK JSON-RPC dari ADR 0012 tetap ada untuk editor dan proses lokal.

## Alternatives considered

- **ConnectRPC/gRPC** — satu definisi untuk gRPC dan HTTP+JSON. Ditolak pemilik; REST + SSE cukup dan lebih sederhana.
- **Hanya MCP** — paling cepat. Ditolak karena service Go ingin embed dan service lain butuh HTTP.
- **Hanya HTTP API** — universal. Ditolak karena harness yang mendukung MCP dan A2A akan butuh adapter sendiri.
