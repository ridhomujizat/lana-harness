# 0004. Profile, Fork, Subagent, and Protocols

- **ADR reference**: [0010. Profiles and config patch](../adr/0010-profile-and-config-patch.md), [0009. Session event log](../adr/0009-session-as-append-only-event-log.md), [0011. Subagent](../adr/0011-subagent-as-child-session.md), [0012. ACP and SDK](../adr/0012-acp-and-sdk-json-rpc.md)
- **Related diagram**: [0002. Agent Loop and Approval](0002-agent-loop-and-approval.md), [0003. Data Model and Scheduler](0003-data-model-and-scheduler.md)
- **Purpose**: profile composition at boot, session fork and resume, subagent flow, and ACP flow.

> Keep this diagram in sync with the related ADRs. If an ADR and this diagram disagree, update both in the same change.

## Profile Composition at Boot

```mermaid
flowchart TD
  L1["1. Base compiled into the binary"] --> M["Merge by row id"]
  L2["2. Built-in profiles: daemon, headless, acp, sdk, minimal"] --> M
  L3["3. ~/.lana/profiles/name.toml"] --> M
  L4["4. ~/.lana/config.toml"] --> M
  L5["5. --patch file"] --> M
  M --> V{"All modules known and service dependencies complete?"}
  V -- "No" --> Fail["Boot fails with a clear message"]
  V -- "Yes" --> Sort["Order modules by service dependencies"]
  Sort --> Install["Module.install into ContextBuilder"]
  Install --> Run["Context ready, goroutines start"]
  M -.-> Dump["--dump-config: print result and origin of each row"]
```

Merge rules:

- `[[modules]]` are merged by `id`. A matching row replaces its entire `config`.
- `disabled = true` turns a row off.
- The tables `providers`, `agents`, `mcp`, and `channels` are merged recursively per key.
- `[[routes]]` from the last layer replaces the entire list.

## Modules per Profile

```mermaid
flowchart LR
  subgraph Base["base"]
    B1["providers"]
    B2["store"]
    B3["agent loop"]
    B4["tools and permission"]
    B5["approval"]
    B6["compaction"]
    B7["mcp and skills"]
  end
  Daemon["daemon"] --> Base
  Daemon --> D1["http and web"]
  Daemon --> D2["telegram and whatsapp"]
  Daemon --> D3["scheduler and router"]
  Headless["headless"] --> B1
  Headless --> B2
  Headless --> B3
  Headless --> B4
  Headless --> B5
  Headless --> B6
  Headless --> H1["cli_runner: one-shot runner to stdout"]
  Acp["acp"] --> Base
  Acp --> A1["ACP server over stdio"]
  Sdk["sdk"] --> Base
  Sdk --> S1["JSON-RPC server over stdio or socket"]
  Minimal["minimal"] --> B1
  Minimal --> B2
  Minimal --> B3
  Minimal --> B4
  Minimal --> B5
  Minimal --> B6
```

## Fork Session

Fork does not copy events. The child session reads the parent's events up to `inherited_seq`, then its own events.

```mermaid
flowchart LR
  subgraph Parent["Session A"]
    P1["seq 1-10: turn 1"] --> P2["seq 11-20: turn 2"] --> P3["seq 21-30: turn 3"]
  end
  subgraph Child["Session B forked from A at seq 20"]
    C0["session/created parent A, inherited_seq 20"] --> C1["seq 1-12: new turn"]
  end
  P2 -. "derive reads seq 1-20 from A" .-> C0
```

Fork rules:

- Fork is only allowed at a turn boundary (the `seq` of a `turn/end`).
- Session approval grants and the policy carry over, because they live in inherited events.

## Resume After a Crash

```mermaid
flowchart TD
  A["Open session"] --> B{"Last event is turn/end?"}
  B -- "Yes" --> Ready["Ready to accept messages"]
  B -- "No" --> C["Find tool/call without tool/result"]
  C --> D["Write error tool/result for each orphaned call"]
  D --> E["Write turn/end with reason interrupted"]
  E --> Ready
```

## Subagent

```mermaid
sequenceDiagram
  autonumber
  participant Parent as Parent loop
  participant Log as Session log
  participant Child as Child loop
  participant LLM as Provider
  participant Ch as Parent channel

  Parent->>Log: tool/call task agent explorer
  Parent->>Log: Create child session with parent_session_id and parent_call_id
  Parent->>Child: Send prompt to child inbox
  loop Until end_turn or max_steps 15
    Child->>LLM: Request with child's tools
    LLM-->>Child: Answer and tool_use
    opt Tool needs approval
      Child->>Ch: Approval labeled explorer
      Ch-->>Child: Outcome
    end
  end
  Child->>Log: turn/end in child session
  Child-->>Parent: Final answer and token usage
  Parent->>Log: tool/result containing child summary
```

Subagent limits:

- Maximum depth 2. At most 3 children running concurrently per parent.
- The child's permission is the intersection of the parent's permission and the child agent's.
- Cancelling the parent cancels all children.

## ACP with an Editor

```mermaid
sequenceDiagram
  autonumber
  participant Ed as ACP editor client
  participant Acp as lana acp
  participant AgentLoop as Agent loop
  participant Log as Session log

  Ed->>Acp: initialize
  Acp-->>Ed: Agent capabilities
  Ed->>Acp: session/new with cwd
  Acp->>Log: session/created, workspace = cwd
  Ed->>Acp: session/prompt
  Acp->>AgentLoop: Put into inbox
  AgentLoop-->>Acp: session.event
  Acp-->>Ed: session/update text and tool_call
  opt Approval
    Acp->>Ed: session/request_permission
    Ed-->>Acp: User's choice
  end
  Acp-->>Ed: Prompt response with stopReason
```
