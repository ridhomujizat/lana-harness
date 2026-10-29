# 0005. Integrasi Agentic Tool

- **ADR reference**: [0014. Sediakan lana sebagai agentic tool](../adr/0014-integrasi-sebagai-agentic-tool.md), [0013. Gunakan Go](../adr/0013-gunakan-go-sebagai-single-binary.md)
- **Related diagram**: [0001. System Context Dan Roadmap](0001-system-context-dan-roadmap.md), [0002. Agent Loop Dan Approval](0002-agent-loop-dan-approval.md)
- **Purpose**: empat permukaan integrasi, alur embed Go library, alur MCP server, dan pemetaan approval per permukaan.

Untuk orang yang mengintegrasikan `lana` ke service lain. Agent loop, event log, dan permission
sama untuk semua permukaan; diagram ini hanya menunjukkan pintunya.

> Jaga diagram ini sinkron dengan ADR terkait. Jika ADR dan diagram berbeda, perbarui keduanya dalam satu perubahan.

## Empat Permukaan

```mermaid
flowchart LR
  subgraph Callers["Pemanggil"]
    GoSvc["Service Go"]
    Harness["Claude Code, Cursor, harness lain"]
    AnySvc["Service bahasa apa pun"]
    Agent2["Agent lain"]
  end

  subgraph Lana["lana"]
    Lib["package lana - in-process"]
    McpS["MCP server - stdio atau HTTP"]
    Rest["REST + SSE - /v1"]
    A2A["A2A - agent card dan task"]
    Ctx["Context: agent loop, event log, permission"]
  end

  GoSvc -->|import| Lib
  Harness -->|MCP| McpS
  AnySvc -->|HTTP| Rest
  Agent2 -->|A2A| A2A
  Lib --> Ctx
  McpS --> Ctx
  Rest --> Ctx
  A2A --> Ctx
```

## Embed Sebagai Go Library

```mermaid
sequenceDiagram
  autonumber
  participant Svc as Service Go
  participant Rt as lana Runtime
  participant AgentLoop as Agent loop
  participant Tool as Tool milik service
  participant Appr as Penjawab milik service

  Svc->>Rt: lana.New dengan Options, Tools, Approval
  Rt-->>Svc: Runtime siap
  Svc->>Rt: Run dengan agent dan prompt
  Rt->>AgentLoop: Buat session channel library
  AgentLoop->>Tool: tool_use lookup_order
  opt Tool butuh approval
    AgentLoop->>Appr: approval.request
    Appr-->>AgentLoop: allowed_once atau rejected
  end
  Tool-->>AgentLoop: hasil
  AgentLoop-->>Rt: turn/end
  Rt-->>Svc: Result: text, session_id, usage
  Svc->>Rt: Close
```

## Dipanggil Lewat MCP

```mermaid
sequenceDiagram
  autonumber
  participant Client as MCP client
  participant Srv as lana mcp
  participant AgentLoop as Agent loop

  Client->>Srv: initialize
  Srv-->>Client: tools lana_run, lana_agents, lana_sessions
  Client->>Srv: tools/call lana_run dengan agent dan prompt
  Srv->>AgentLoop: Session baru channel mcp
  AgentLoop-->>Srv: event turn
  Srv-->>Client: notifikasi progress
  opt Approval dan client mendukung elicitation
    Srv->>Client: elicitation/create
    Client-->>Srv: jawaban user
  end
  AgentLoop-->>Srv: turn/end
  Srv-->>Client: hasil tool: teks, session_id, stop_reason
```

## Pemetaan Approval

```mermaid
flowchart TD
  Req["approval.request"] --> Ch{"Channel session"}
  Ch -- "library" --> L{"Service memberi penjawab?"}
  L -- "Ya" --> LA["Fungsi penjawab service"]
  L -- "Tidak" --> Un["unavailable"]
  Ch -- "mcp" --> M{"Client mendukung elicitation?"}
  M -- "Ya" --> MA["elicitation/create"]
  M -- "Tidak" --> Nv["policy never: rejected"]
  Ch -- "api" --> RA["Event SSE approval, jawab lewat POST /v1/approvals/id"]
  Ch -- "a2a" --> AA["Task input-required"]
```

Aturan:

- Permukaan integrasi tidak bisa menaikkan policy di atas policy agent.
- Agent di luar allowlist permukaan tidak bisa dipanggil lewat permukaan itu.
- Workspace untuk permukaan jaringan selalu dari config agent.
