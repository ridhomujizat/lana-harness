# 0001. System Context Dan Roadmap

- **ADR reference**: [0013. Gunakan Go](../adr/0013-gunakan-go-sebagai-single-binary.md), [0014. Integrasi agentic tool](../adr/0014-integrasi-sebagai-agentic-tool.md), [0015. Runtime embeddable](../adr/0015-runtime-embeddable-untuk-service.md), [0002. Core dari modul, service, dan hook](../adr/0002-core-dari-modul-service-dan-hook.md), [0008. Interface](../adr/0008-interface-cli-channel-http-dan-web-dashboard.md), [0012. ACP dan SDK](../adr/0012-acp-dan-sdk-json-rpc.md)
- **Related diagram**: [0002. Agent Loop Dan Approval](0002-agent-loop-dan-approval.md), [0003. Data Model Dan Scheduler](0003-data-model-dan-scheduler.md), [0004. Profile, Fork, Subagent, Dan Protokol](0004-profile-fork-subagent-dan-protokol.md), [0005. Integrasi Agentic Tool](0005-integrasi-agentic-tool.md)
- **Purpose**: system context, peta service dan hook, dependency antar package, topologi runtime, dan roadmap implementasi.

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
    Svc["Service lain - REST, A2A"]
    Harness["Harness lain - MCP client"]
  end

  subgraph Lana["Proses lana - satu binary, profile dipilih saat boot"]
    Doors["Pintu: channel, REST, ACP, SDK, MCP server, A2A"]
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
  Svc -->|REST SSE atau A2A| Doors
  Harness -->|MCP| Doors
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

## Dependency Antar Package

Arah panah = "meng-import". `core` tidak meng-import package lain di module ini. Service Go yang
meng-embed `lana` hanya meng-import package `lana` dan `core`.

```mermaid
flowchart TD
  Main["cmd/lana"]
  Embed["Service Go lain"]
  Lana["lana - API publik"]
  Core["core"]
  Agent["agent"]
  Store["store"]
  Config["config"]
  Prov["providers"]
  Tool["tools"]
  Mcp["mcp"]
  Chan["channels"]
  Proto["protocol"]
  Web["web"]

  Main --> Lana
  Main --> Chan
  Main --> Proto
  Main --> Web
  Embed --> Lana
  Embed --> Core
  Lana --> Config
  Lana --> Agent
  Lana --> Store
  Lana --> Prov
  Lana --> Tool
  Lana --> Mcp
  Agent --> Core
  Store --> Core
  Config --> Core
  Prov --> Core
  Tool --> Core
  Mcp --> Core
  Chan --> Core
  Proto --> Core
  Web --> Core
```

## Topologi Runtime

Topologi untuk profile `daemon`. Profile lain menjalankan subset goroutine ini.

```mermaid
flowchart LR
  Main["main - susun profile"] --> HTTP["goroutine net/http: REST, SSE, MCP HTTP, A2A"]
  Main --> TGPoll["goroutine telegram poll"]
  Main --> SchedT["goroutine scheduler"]
  Main --> Writer["goroutine store writer"]
  HTTP --> Reg["ctx.agents"]
  TGPoll --> Reg
  SchedT --> Reg
  Reg --> A1["goroutine session A dan inbox"]
  Reg --> A2["goroutine session B dan inbox"]
  A2 --> Sub["goroutine child session"]
  A1 --> Writer
  A2 --> Writer
  Sub --> Writer
  Writer --> Bus["hook observe session.event"]
  Bus --> HTTP
  Bus --> TGPoll
```

Aturan:

- Satu goroutine per session aktif, dengan satu inbox. Session idle 15 menit dihentikan; state tetap di log.
- Semua tulis ke SQLite lewat satu goroutine store writer.
- Semua goroutine menerima `context.Context` dari root dan berhenti saat shutdown.
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
- Service Go yang butuh agent meng-embed package `lana`, bukan menjalankan proses terpisah.
- Telegram long polling, bukan webhook.
- Memory FTS5, bukan vektor.
- Tanpa sandbox OS. Batas workspace dan approval fail-closed adalah garis pertahanan v1.

## Roadmap Implementasi

```mermaid
flowchart LR
  M0["M0 Fondasi"] --> M1["M1 Agent loop dan Go library"]
  M1 --> M2["M2 Session dan permission"]
  M2 --> M25["M2.5 Runtime embeddable"]
  M25 --> M3["M3 CLI dan TUI"]
  M3 --> M4["M4 MCP, skills, subagent, MCP server"]
  M4 --> M5["M5 Daemon dan REST API"]
  M5 --> M6["M6 ACP, SDK, web, A2A"]
  M6 --> M7["M7 WhatsApp dan rilis"]
```

## Implementation Checklist

1. **M0 Fondasi (spec #2)** — `flake.nix` dengan Go dari nixpkgs dan `GOTOOLCHAIN=local`, Go module, layout package, test aturan import, CI GitHub Actions (macOS arm64 dan Linux x86_64). Loader profile berlapis, `core` (Context, Module, hook generik).
2. **M1 Agent loop dan Go library (spec #3)** — adapter `openai_compat` dengan streaming, agent loop turn dan step dengan inbox, builtin tools, `MockLlm`, profile `minimal` dan `headless`. Package `lana` publik: `New`, `Run`, `Stream`, tool kustom.
3. **M2 Session dan permission (spec #4)** — `session_events` append-only di `modernc.org/sqlite`, projection, derive, resume, fork, lease, compaction otomatis dan manual. Permission di `tools.pre_execute`, `ctx.approval` fail-closed.
4. **M2.5 Runtime embeddable (spec #5)** — `store/postgres` (append-only, `pg_try_advisory_lock`), `lana.Agent` di kode, `Budget` per run dengan `turn/end` reason `budget`, `MaxConcurrentRuns`, `observability/otel`. Test kontrak store yang sama untuk SQLite, Postgres, dan memori (ADR 0015).
5. **M3 CLI dan TUI (spec #1)** — `lana chat` (Bubble Tea inline), `lana run`, `lana init`, `lana config`.
6. **M4 MCP, skills, subagent, MCP server** — MCP client `go-sdk` dengan lazy start, `skill_load`, tool `task`, adapter Anthropic, `lana mcp` (stdio dan streamable HTTP, elicitation untuk approval).
7. **M5 Daemon dan REST API** — profile `daemon`, REST + SSE (`/v1/runs`, `/v1/sessions`, token per client, OpenAPI), Telegram, routing, scheduler, memory FTS5.
8. **M6 ACP, SDK, web, A2A** — `lana acp`, `lana sdk`, dashboard (`embed.FS`), A2A server (agent card, task = session).
9. **M7 WhatsApp dan rilis** — channel WhatsApp, `lana service install`, Docker image `distroless/static`. Ukur target: binary < 40 MB, RAM idle < 30 MB.
10. **Test di setiap milestone** — `MockLlm`, golden `derive_messages`, resume dari log terpotong, permission matcher, kontrak format OpenAI, merge profile, `testscript` untuk CLI.
11. **Observability** — `log/slog` dengan handler JSON opsional. Redaksi API key dan token di log dan payload event.
