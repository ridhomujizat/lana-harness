# 0005. Agentic Tool Integration

- **ADR reference**: [0014. Provide lana as an agentic tool](../adr/0014-integration-as-agentic-tool.md), [0013. Use Go](../adr/0013-use-go-as-single-binary.md)
- **Related diagram**: [0001. System Context and Roadmap](0001-system-context-and-roadmap.md), [0002. Agent Loop and Approval](0002-agent-loop-and-approval.md)
- **Purpose**: the four integration surfaces, the Go library embedding flow, the MCP server flow, and the approval mapping per surface.

For people integrating `lana` into other services. The agent loop, event log, and permissions
are the same for all surfaces; this diagram only shows the entry points.

> Keep this diagram in sync with the related ADRs. If an ADR and this diagram disagree, update both in the same change.

## Four Surfaces

```mermaid
flowchart LR
  subgraph Callers["Callers"]
    GoSvc["Go service"]
    Harness["Claude Code, Cursor, other harnesses"]
    AnySvc["Service in any language"]
    Agent2["Other agent"]
  end

  subgraph Lana["lana"]
    Lib["package lana - in-process"]
    McpS["MCP server - stdio or HTTP"]
    Rest["REST + SSE - /v1"]
    A2A["A2A - agent card and tasks"]
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

## Embedding as a Go Library

```mermaid
sequenceDiagram
  autonumber
  participant Svc as Go service
  participant Rt as lana Runtime
  participant AgentLoop as Agent loop
  participant Tool as Service-owned tool
  participant Appr as Service-owned responder

  Svc->>Rt: lana.New with Options, Tools, Approval
  Rt-->>Svc: Runtime ready
  Svc->>Rt: Run with agent and prompt
  Rt->>AgentLoop: Create session with channel library
  AgentLoop->>Tool: tool_use lookup_order
  opt Tool needs approval
    AgentLoop->>Appr: approval.request
    Appr-->>AgentLoop: allowed_once or rejected
  end
  Tool-->>AgentLoop: Result
  AgentLoop-->>Rt: turn/end
  Rt-->>Svc: Result: text, session_id, usage
  Svc->>Rt: Close
```

## Called Through MCP

```mermaid
sequenceDiagram
  autonumber
  participant Client as MCP client
  participant Srv as lana mcp
  participant AgentLoop as Agent loop

  Client->>Srv: initialize
  Srv-->>Client: tools lana_run, lana_agents, lana_sessions
  Client->>Srv: tools/call lana_run with agent and prompt
  Srv->>AgentLoop: New session with channel mcp
  AgentLoop-->>Srv: turn events
  Srv-->>Client: progress notifications
  opt Approval and client supports elicitation
    Srv->>Client: elicitation/create
    Client-->>Srv: User's answer
  end
  AgentLoop-->>Srv: turn/end
  Srv-->>Client: Tool result: text, session_id, stop_reason
```

## Approval Mapping

```mermaid
flowchart TD
  Req["approval.request"] --> Ch{"Session channel"}
  Ch -- "library" --> L{"Service provides a responder?"}
  L -- "Yes" --> LA["Service's responder function"]
  L -- "No" --> Un["unavailable"]
  Ch -- "mcp" --> M{"Client supports elicitation?"}
  M -- "Yes" --> MA["elicitation/create"]
  M -- "No" --> Nv["policy never: rejected"]
  Ch -- "api" --> RA["SSE approval event, answer via POST /v1/approvals/id"]
  Ch -- "a2a" --> AA["Task input-required"]
```

Rules:

- Integration surfaces cannot raise a policy above the agent's policy.
- An agent outside a surface's allowlist cannot be called through that surface.
- The workspace for network surfaces always comes from the agent's config.
