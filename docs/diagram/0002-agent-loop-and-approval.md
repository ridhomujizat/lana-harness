# 0002. Agent Loop and Approval

- **ADR reference**: [0002. Core from modules, services, and hooks](../adr/0002-core-from-modules-services-and-hooks.md), [0004. Permissions and approval](../adr/0004-permission-mode-per-agent.md), [0009. Session event log](../adr/0009-session-as-append-only-event-log.md)
- **Related diagram**: [0001. System Context and Roadmap](0001-system-context-and-roadmap.md), [0004. Profile, Fork, Subagent, and Protocols](0004-profile-fork-subagent-and-protocols.md)
- **Purpose**: model of turns and steps, hook positions, the sequence of a single turn, the approval chain, and failure handling.

This diagram is for people implementing the `agent` package. The turn, step, and hook pattern follows
deepseek-harness (`dsh`).

> Keep this diagram in sync with the related ADRs. If an ADR and this diagram disagree, update both in the same change.

## Turns and Steps

- **Step** = one request to the model plus the tools it calls.
- **Turn** = zero or more steps. A turn opens before the first input is claimed and closes when nothing is left to answer.

```text
turn/start
  claim input from inbox (initial message, plus one queued message per step)
  hook agent.pre_step         accept, modify, or reject input
    rejected in the first step -> turn/end without a step
  step/start
    assemble system prompt and tool schemas
    hook agent.request        modify request controls
    write system/message if changed, write user/message
    derive history from log, freeze request
    hook llm.stream           retry, fallback, metering
      temporary stream frames to channel
      assistant/message | assistant/attempt
    for each tool_use:
      tool/call
      hook tools.pre_execute  permission and approval
      hook tools.execute
      hook tools.post_execute truncate or spill output
      tool/result
  step/end
  still a tool_result or new inbox message -> next step
  hook agent.turn_stopping
turn/end
```

Names with `/` are durable events in the session log. Names with `.` are live hooks.

## Turn Sequence

```mermaid
sequenceDiagram
  autonumber
  participant User
  participant Channel
  participant Inbox
  participant AgentLoop as Agent loop
  participant Log as Session log
  participant LLM as Provider
  participant Tools as Tool pipeline
  participant Appr as Approval

  User->>Channel: Send message
  Channel->>Inbox: Enqueue message
  AgentLoop->>Log: turn/start
  AgentLoop->>Inbox: Claim input
  AgentLoop->>AgentLoop: Hook agent.pre_step
  AgentLoop->>Log: step/start, user/message
  AgentLoop->>Log: Derive history
  AgentLoop->>LLM: Stream request through hook llm.stream
  LLM-->>AgentLoop: Text deltas and tool_use
  AgentLoop-->>Channel: Temporary stream frames
  AgentLoop->>Log: assistant/message
  opt tool_use present
    AgentLoop->>Log: tool/call
    AgentLoop->>Tools: Hook tools.pre_execute
    opt Policy ask and risky tool
      Tools->>Appr: request
      Appr->>Log: approval/asked
      Appr->>Channel: Show approval with call_id
      Channel->>User: Allow or deny buttons
      User-->>Channel: Answer
      Channel-->>Appr: Outcome
      Appr->>Log: approval/decided
      Appr-->>Tools: Outcome
    end
    Tools->>Tools: Hook tools.execute and tools.post_execute
    Tools-->>AgentLoop: Output or error
    AgentLoop->>Log: tool/result
  end
  AgentLoop->>Log: step/end
  AgentLoop->>AgentLoop: Hook agent.turn_stopping
  AgentLoop->>Log: turn/end
  Log-->>Channel: session.event
  Channel-->>User: Final answer
```

Invariants:

- *Model-visible means logged*: every model request can be reconstructed from the log.
- Every `tool/call` always has a `tool/result`, including when rejected, cancelled, or errored.
- A session has only one active turn. New messages enter the inbox and are claimed at the next step.
- `max_steps` defaults to 25 per turn, and to 15 for subagents.
- Independent `Read` tools may run in parallel. `Write` and `Exec` tools run sequentially.

## Permission and Approval Decision

```mermaid
flowchart TD
  A["tools.pre_execute"] --> B{"Matches deny rule?"}
  B -- "Yes" --> X["Reject: tool/result error"]
  B -- "No" --> W{"Path inside workspace?"}
  W -- "No" --> X
  W -- "Yes" --> C{"Matches allow or session grant?"}
  C -- "Yes" --> OK["Continue to tools.execute"]
  C -- "No" --> D{"Session policy"}
  D -- "trust" --> OK
  D -- "never" --> E1{"Risk Read?"}
  E1 -- "Yes" --> OK
  E1 -- "No" --> X
  D -- "ask" --> E2{"Risk Read?"}
  E2 -- "Yes" --> OK
  E2 -- "No" --> G["ctx.approval.request"]
  G --> H{"Outcome"}
  H -- "allowed_once" --> OK
  H -- "allowed_session" --> R["Store grant in log"] --> OK
  H -- "rejected" --> X
  H -- "cancelled" --> X
  H -- "unavailable" --> X
```

## Approval Responder Chain

The `approval.request` hook is a waterfall. The first responder that owns the session answers. If no one
answers, the outcome is `unavailable` (fail-closed).

```mermaid
flowchart LR
  Req["approval.request"] --> Ch{"Session owner's channel connected?"}
  Ch -- "Yes" --> Human["CLI, Telegram, WhatsApp, or web answers"]
  Ch -- "No" --> Acp{"Session owned by ACP or SDK client?"}
  Acp -- "Yes" --> Client["session/request_permission or approval.respond"]
  Acp -- "No" --> Un["unavailable"]
  Human --> Out["Outcome"]
  Client --> Out
  Un --> Out
```

## Failure Handling

```mermaid
flowchart TD
  A["Error in turn"] --> B{"Error class"}
  B -- "Provider 429 or 5xx" --> C["llm.stream retry with backoff, max 3 times"]
  C --> D{"Still failing?"}
  D -- "Yes" --> E["Switch to fallback provider"]
  E --> F{"Fallbacks exhausted?"}
  F -- "Yes" --> G["turn/end reason error, message sent to channel"]
  B -- "Provider 4xx" --> G
  B -- "Context too long" --> H["Write compaction/summary, then retry once"]
  B -- "Tool error or timeout" --> I["tool/result is_error to model"]
  B -- "Process dies mid-turn" --> J["On resume: close orphaned tool/call, turn/end interrupted"]
  A2["Every failed attempt"] --> K["assistant/attempt in log"]
  C --> A2
```
