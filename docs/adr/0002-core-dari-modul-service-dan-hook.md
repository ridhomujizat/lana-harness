# 0002. Bangun core dari modul, service, dan hook dalam Cargo workspace

- **Status**: Proposed
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0001](0001-gunakan-rust-single-binary-daemon.md), [0009](0009-session-sebagai-event-log-append-only.md), [0010](0010-profile-dan-config-patch.md)

## Context

Harness perlu mendukung beberapa provider LLM, beberapa channel, beberapa sumber tool, dan
kebijakan seperti approval, compaction, dan audit. Tanpa batas yang jelas, tiap fitur bocor ke
agent loop dan kode cepat kusut.

Referensi cara kerja adalah **deepseek-harness (`dsh`)**. Di `dsh` semua bagian adalah plugin
Cordis: model adapter, tool registry, session log, bahkan agent loop. Plugin mendaftarkan
*service* ke context bersama (`ctx.tools`, `ctx.llm`, `ctx.sessions`) dan berkomunikasi lewat
*event* bertipe. Event `waterfall` (middleware dengan `next()`) menjadi titik extension untuk
kebijakan, misalnya `agent/pre-step`, `tools/pre-execute`, dan `approval/request`. Tidak ada core
istimewa yang perlu di-patch.

Constraint `lana`:

- Rust, binary kecil. Loading plugin saat runtime (dynamic library, WASM) **tidak** masuk v1.
- Hot reload (HMR) tidak dibutuhkan untuk daemon pribadi.

## Decision

Kita mengadopsi pola `dsh` dalam bentuk **compile-time**:

1. **Modul.** Setiap fitur adalah implementasi `Module` yang dikompilasi ke binary. Profile
   memilih modul mana yang aktif dan konfigurasinya (lihat [0010](0010-profile-dan-config-patch.md)).
2. **Service.** Modul mendaftarkan service bertipe ke `Context`. Modul lain memakai service lewat
   trait, bukan lewat tipe konkret.
3. **Hook.** Titik intercept adalah hook bertipe dengan mode `waterfall` (middleware dengan
   `next`) atau `observe` (hanya mendengar). Kebijakan seperti permission dan compaction adalah
   listener hook, bukan kode di dalam agent loop.
4. **Event durable.** Fakta yang harus bertahan setelah restart ditulis ke session event log
   (lihat [0009](0009-session-sebagai-event-log-append-only.md)) lalu di-broadcast ke observer.

```rust
pub trait Module: Send + Sync {
    fn id(&self) -> &'static str;
    fn requires(&self) -> &'static [ServiceKey];   // urutan boot dari dependency
    fn install(&self, cx: &mut ContextBuilder, cfg: &toml::Value) -> Result<()>;
}

pub struct Context {
    pub llm: Arc<dyn LlmRegistry>,        // ctx.llm      adapter provider
    pub tools: Arc<ToolRegistry>,         // ctx.tools    registry dan pipeline eksekusi
    pub sessions: Arc<dyn SessionStore>,  // ctx.sessions event log
    pub agents: Arc<AgentRegistry>,       // ctx.agents   agent hidup dan inbox
    pub approval: Arc<ApprovalService>,   // ctx.approval
    pub prompt: Arc<SystemPrompt>,        // ctx.prompt   section system prompt
    pub hooks: Hooks,                     // semua hook bertipe
    pub events: broadcast::Sender<SessionEventEnvelope>,
}

#[async_trait]
pub trait Waterfall<I: Send, O: Send>: Send + Sync {
    async fn call(&self, cx: &Context, input: I, next: Next<'_, I, O>) -> Result<O>;
}
```

Hook v1:

| Hook | Mode | Fungsi |
|---|---|---|
| `agent.pre_step` | waterfall | Terima, ubah, atau tolak input yang di-claim sebelum step |
| `agent.request` | waterfall | Ubah kontrol request (max tokens, temperature) sebelum dikirim |
| `llm.stream` | waterfall | Bungkus stream provider (retry, fallback, metering token) |
| `tools.pre_execute` | waterfall | Validasi dan permission sebelum tool jalan |
| `tools.execute` | waterfall | Eksekusi tool (bisa dibungkus timeout, sandbox nanti) |
| `tools.post_execute` | waterfall | Ubah atau potong output tool (spill output besar ke file) |
| `approval.request` | waterfall | Rantai penjawab approval: channel, ACP client, atau `unavailable` |
| `agent.turn_stopping` | serial | Putuskan apakah turn benar-benar berhenti |
| `session.event` | observe | Broadcast event durable ke channel, web, dan protokol |

Layout workspace:

```text
crates/
  lana-core/       Context, Module, Hooks, tipe pesan dan event, trait service. Tanpa I/O.
  lana-agent/      Agent loop (turn dan step), inbox, subagent, compaction
  lana-store/      SQLite: session event log, projection, memory, cron, audit
  lana-providers/  anthropic, openai_compat (OpenAI, OpenRouter, Ollama)
  lana-tools/      builtin tools dan permission engine
  lana-mcp/        MCP client yang membungkus server MCP sebagai tool
  lana-channels/   cli, telegram, whatsapp
  lana-protocol/   server ACP dan SDK JSON-RPC
  lana-web/        axum HTTP API, SSE, dashboard embedded
  lana/            binary: subcommand, profile, loader config
```

Tipe pesan internal (`Message`, `ContentBlock::{Text, ToolUse, ToolResult, Image}`) mengikuti bentuk
Anthropic Messages API. Adapter `openai_compat` mengonversi dari dan ke format OpenAI.

## Consequences

**Positive**
- Agent loop sendiri bisa diganti atau diuji dengan `MockLlm` tanpa jaringan.
- Kebijakan baru (approval, audit, redaksi) cukup menambah listener hook.
- Hook bertipe dicek compiler. Tidak ada event bus berbasis string.
- Profile bisa membuat binary yang sama berperilaku sebagai daemon, one-shot, atau server ACP.

**Negative / tradeoffs**
- Lebih banyak abstraksi dibanding loop sederhana. Butuh dokumentasi hook yang disiplin.
- Tanpa runtime plugin, modul baru butuh rebuild binary.
- `dyn` dan `async_trait` menambah sedikit alokasi. Tidak signifikan dibanding latensi LLM.

**Neutral**
- Plugin pihak ketiga (proses eksternal lewat JSON-RPC, atau WASM) bisa ditambah nanti sebagai
  `Module` yang menjembatani hook ke luar proses.
- Integrasi luar di v1 lewat MCP dan skill (lihat [0006](0006-ekstensi-lewat-mcp-client-dan-skills.md)).

## Alternatives considered

- **Agent loop monolit dengan trait provider, channel, dan tool saja** — lebih sederhana. Ditolak
  karena kebijakan akan menumpuk di dalam loop dan tidak mengikuti cara kerja `dsh`.
- **Event bus dinamis berbasis string seperti Cordis** — fleksibel. Ditolak karena kehilangan
  pengecekan tipe dan lebih lambat di Rust.
- **Plugin runtime (dynamic library atau WASM) sejak v1** — ditunda atas keputusan pemilik.
