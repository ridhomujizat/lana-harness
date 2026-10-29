# 0001. System Context Dan Roadmap

- **ADR reference**: [0001. Gunakan Rust single binary daemon](../adr/0001-gunakan-rust-single-binary-daemon.md), [0002. Core dari modul, service, dan hook](../adr/0002-core-dari-modul-service-dan-hook.md), [0008. Interface](../adr/0008-interface-cli-channel-http-dan-web-dashboard.md), [0012. ACP dan SDK](../adr/0012-acp-dan-sdk-json-rpc.md)
- **Related diagram**: [0002. Agent Loop Dan Approval](0002-agent-loop-dan-approval.md), [0003. Data Model Dan Scheduler](0003-data-model-dan-scheduler.md), [0004. Profile, Fork, Subagent, Dan Protokol](0004-profile-fork-subagent-dan-protokol.md)
- **Purpose**: system context, peta service dan hook, dependency antar crate, topologi runtime, dan roadmap implementasi.

Diagram ini menunjukkan batas proses `lana`, apa yang ada di dalamnya, dan sistem luar yang
dihubungi. Untuk alur satu turn agent, lihat diagram 0002.

> Jaga diagram ini sinkron dengan ADR terkait. Jika ADR dan diagram berbeda, perbarui keduanya dalam satu perubahan.

## System Context

```mermaid
flowchart LR
  subgraph Users["User dan client"]
    Term["Terminal - lana chat"]
    TG["Telegram app"]
    WA["WhatsApp app"]
    Browser["Browser - dashboard"]
    Editor["Editor ACP - Zed"]
    Script["Script atau program - SDK"]
    Ext["Sistem lain - webhook"]
  end

  subgraph Lana["Proses lana - satu binary, profile dipilih saat boot"]
    Doors["Pintu: channel, HTTP, ACP, SDK"]
    Agents["ctx.agents dan agent loop"]
    Hooks["Hooks: pre_step, llm.stream, tools, approval"]
    Tools["ctx.tools: builtin, MCP, skill, task"]
    Sched["Scheduler cron"]
    Store[("lana.db: session event log")]
  end

  subgraph External["Layanan luar"]
    Anthropic["Anthropic API"]
    OpenAI["OpenAI API"]
    OpenRouter["OpenRouter API"]
    TGAPI["Telegram Bot API"]
    WAAPI["WhatsApp Cloud API"]
    MCPS["MCP servers"]
  end

  FS[("Workspace dan ~/.lana")]

  Term -->|HTTP SSE| Doors
  Browser -->|HTTP SSE| Doors
  Ext -->|webhook| Doors
  Editor -->|ACP stdio| Doors
  Script -->|JSON-RPC| Doors
  TG --> TGAPI
  WA --> WAAPI
  Doors <-->|long polling| TGAPI
  Doors <-->|webhook dan Graph API| WAAPI

  Doors --> Agents
  Sched --> Agents
  Agents --> Hooks
  Hooks --> Tools
  Agents <--> Store
  Sched <--> Store
  Hooks --> Anthropic
  Hooks --> OpenAI
  Hooks --> OpenRouter
  Tools <-->|stdio atau HTTP| MCPS
  Tools --> FS
```

## Peta Service Dan Hook

Mengikuti pola deepseek-harness: modul mendaftarkan service ke `Context`, dan kebijakan dipasang
sebagai listener hook.

```mermaid
flowchart TD
  subgraph Services["Service di Context"]
    Llm["ctx.llm"]
    ToolsS["ctx.tools"]
    Sess["ctx.sessions"]
    AgentsS["ctx.agents"]
    Appr["ctx.approval"]
    Prompt["ctx.prompt"]
  end

  subgraph Listeners["Modul yang memasang listener"]
    Perm["permission"] -->|tools.pre_execute| ToolsS
    Spill["spill output"] -->|tools.post_execute| ToolsS
    Retry["retry dan fallback"] -->|llm.stream| Llm
    Compact["compaction"] -->|agent.pre_step| AgentsS
    ChAns["channel"] -->|approval.request| Appr
    AcpAns["acp dan sdk"] -->|approval.request| Appr
    Web["web, channel, protokol"] -->|session.event| Sess
  end
```

## Dependency Antar Crate

Arah panah = "bergantung pada". `lana-core` tidak bergantung pada crate lain di workspace.

```mermaid
flowchart TD
  Bin["lana - binary dan profile"]
  Core["lana-core"]
  Agent["lana-agent"]
  Store["lana-store"]
  Prov["lana-providers"]
  Tool["lana-tools"]
  Mcp["lana-mcp"]
  Chan["lana-channels"]
  Proto["lana-protocol"]
  Web["lana-web"]

  Bin --> Agent
  Bin --> Store
  Bin --> Prov
  Bin --> Tool
  Bin --> Mcp
  Bin --> Chan
  Bin --> Proto
  Bin --> Web
  Agent --> Core
  Store --> Core
  Prov --> Core
  Tool --> Core
  Mcp --> Core
  Chan --> Core
  Proto --> Core
  Web --> Core
```

## Topologi Runtime Tokio

Topologi untuk profile `daemon`. Profile lain menjalankan subset task ini.

```mermaid
flowchart LR
  Main["main - susun profile"] --> Axum["axum server task"]
  Main --> TGPoll["telegram poll task"]
  Main --> SchedT["scheduler task"]
  Main --> Writer["store writer task"]
  Axum --> Reg["ctx.agents"]
  TGPoll --> Reg
  SchedT --> Reg
  Reg --> A1["agent task session A dan inbox"]
  Reg --> A2["agent task session B dan inbox"]
  A2 --> Sub["agent task child session"]
  A1 --> Writer
  A2 --> Writer
  Sub --> Writer
  Writer --> Bus["broadcast session.event"]
  Bus --> Axum
  Bus --> TGPoll
```

Aturan:

- Satu agent task per session aktif, dengan satu inbox. Session idle 15 menit dihentikan; state tetap di log.
- Semua tulis ke SQLite lewat satu store writer task.
- MCP server child process hanya hidup saat dibutuhkan.

## KISS Default Path

```mermaid
flowchart TD
  A["Pesan masuk dari pintu mana pun"] --> B["Cocokkan route ke agent"]
  B --> C["Ambil atau buat session"]
  C --> D["Masukkan ke inbox, agent loop jalan"]
  D --> E["Event log di-broadcast ke pintu asal"]

  X["Escape hatch: perintah /agent"] -.-> B
  Y["Escape hatch: --yolo di CLI"] -.-> D
  Z["Escape hatch: --patch saat boot"] -.-> A
```

KISS defaults:

- Satu binary, satu file DB. Modul dikompilasi ke binary; tidak ada plugin runtime di v1.
- Telegram long polling, bukan webhook.
- Memory FTS5, bukan vektor.
- Tanpa sandbox OS. Batas workspace dan approval fail-closed adalah garis pertahanan v1.

## Roadmap Implementasi

```mermaid
flowchart LR
  M0["M0 Fondasi"] --> M1["M1 Core dan agent loop"]
  M1 --> M2["M2 Event log dan permission"]
  M2 --> M3["M3 Daemon, HTTP, Telegram"]
  M3 --> M4["M4 MCP, skills, subagent"]
  M4 --> M5["M5 Cron, memory, fork"]
  M5 --> M6["M6 ACP, SDK, web"]
  M6 --> M7["M7 WhatsApp dan rilis"]
```

## Implementation Checklist

1. **M0 Fondasi** — `flake.nix` (Rust stable, `cargo-nextest`, `sqlite`), Cargo workspace, profil release kecil, CI build `aarch64-darwin` dan `x86_64-linux-musl`. Loader profile berlapis dengan merge `[[modules]]` per `id`, `--dump-config`, `lana config check`.
2. **M1 Core dan agent loop** — `Context`, `Module`, `Hooks` waterfall dan observe. Adapter `anthropic` dan `openai_compat` (OpenAI, OpenRouter) dengan streaming. Agent loop turn dan step dengan inbox. Builtin tools file dan `bash`. Profile `minimal` dan `headless` (`lana run`).
3. **M2 Event log dan permission** — `session_events` append-only, projection `sessions`, `derive_messages`, resume setelah crash. Modul `permission` di `tools.pre_execute`, `ctx.approval` dengan outcome fail-closed, `approval/policy`. Compaction lewat `compaction/summary`.
4. **M3 Daemon, HTTP, Telegram** — profile `daemon`, axum API dan SSE, channel Telegram long polling dengan inline keyboard, routing multi-agent, `/agent`, `lana chat` attach ke daemon.
5. **M4 MCP, skills, subagent** — `rmcp` stdio dan HTTP dengan lazy start, `skill_load`, tool `task` dan `task_continue` sebagai child session.
6. **M5 Cron, memory, fork** — scheduler dengan policy `never`, tool `cron_*`, `lana cron`, memory FTS5, `fork` di batas turn, `lana session export`.
7. **M6 ACP, SDK, web** — profile `acp` dengan crate `agent-client-protocol`, profile `sdk` JSON-RPC di stdio dan unix socket, dashboard Preact + `htm` embedded.
8. **M7 WhatsApp dan rilis** — channel WhatsApp Cloud API, `lana service install`, Docker image `distroless`. Ukur target: binary < 15 MB, RAM idle < 20 MB.
9. **Test di setiap milestone** — `MockLlm` untuk agent loop, test golden `derive_messages` dari event, test resume dari log terpotong, test permission matcher, test kontrak format OpenAI, test merge profile.
10. **Observability** — `tracing` dengan output JSON opsional. Redaksi API key dan token di log dan payload event.
