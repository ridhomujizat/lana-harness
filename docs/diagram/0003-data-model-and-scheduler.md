# 0003. Data Model and Scheduler

- **ADR reference**: [0005. SQLite as the single storage](../adr/0005-sqlite-as-single-storage.md), [0009. Session event log](../adr/0009-session-as-append-only-event-log.md), [0007. Cron scheduler inside the daemon](../adr/0007-cron-scheduler-inside-daemon.md)
- **Related diagram**: [0002. Agent Loop and Approval](0002-agent-loop-and-approval.md)
- **Purpose**: SQLite schema, data folder layout, deriving history from the event log, and the cron scheduler flow.

> Keep this diagram in sync with the related ADRs. If an ADR and this diagram disagree, update both in the same change.

## Data Folder Layout

```text
~/.lana/                 (override: LANA_HOME)
  config.toml            providers, agents, routes, mcp, channels, modules
  profiles/<profile>.toml patch per profile
  secrets.env            API keys and bot tokens, permission 0600
  lana.db                SQLite WAL: session event log, memory, cron
  agents/<name>.md       system prompt per agent
  skills/<name>/SKILL.md skills and supporting scripts
  workspaces/<agent>/    default workspace if the agent does not set one
  logs/lana.log          tracing log, daily rotation
```

## Data Model

```mermaid
erDiagram
  SESSIONS ||--o{ SESSION_EVENTS : contains
  SESSIONS ||--o{ SESSIONS : fork_or_subagent
  CRON_JOBS ||--o{ CRON_RUNS : produces
  CRON_RUNS ||--o| SESSIONS : uses

  SESSION_EVENTS {
    TEXT session_id PK
    INTEGER seq PK
    TEXT kind
    TEXT payload
    TEXT turn_id
    INTEGER created_at
  }

  SESSIONS {
    TEXT id PK
    TEXT agent
    TEXT channel
    TEXT address
    TEXT title
    TEXT status
    TEXT parent_session_id
    TEXT parent_call_id
    INTEGER inherited_seq
    INTEGER last_seq
    INTEGER updated_at
  }

  MEMORIES {
    INTEGER id PK
    TEXT scope
    TEXT content
    TEXT tags
    BLOB embedding
    INTEGER created_at
  }

  CRON_JOBS {
    TEXT id PK
    TEXT title
    TEXT agent
    TEXT schedule
    TEXT tz
    TEXT prompt
    TEXT target_channel
    TEXT target_address
    INTEGER next_run_at
    INTEGER enabled
  }

  CRON_RUNS {
    INTEGER id PK
    TEXT job_id FK
    TEXT session_id FK
    TEXT status
    INTEGER started_at
    INTEGER finished_at
    TEXT error
  }
```

Notes:

- `SESSION_EVENTS` is the source of truth. Triggers reject `UPDATE` and `DELETE` (except for `lana db prune`, which deletes whole sessions).
- `SESSIONS` is a projection that can be rebuilt from events. `parent_session_id` is used by forks (with `inherited_seq`) and by subagents (with `parent_call_id`).
- Approval audit = `approval/asked` and `approval/decided` events. An index on `(kind, created_at)` serves the audit page.
- `MEMORIES` has a companion virtual table `memories_fts` (FTS5), kept in sync with triggers. `scope` is `global` or `agent:<name>`.
- `embedding` is empty in v1. It is prepared for later vector search.
- Secrets never enter the DB. The source of truth for secrets is the env or `secrets.env`.

## Deriving Model History

```mermaid
flowchart TD
  A["Open session"] --> B{"Has parent with inherited_seq?"}
  B -- "Yes" --> C["Read parent events up to inherited_seq, recursively"]
  B -- "No" --> D["Start from empty"]
  C --> E["Append the session's own events"]
  D --> E
  E --> F{"Has compaction/summary?"}
  F -- "Yes" --> G["Use latest summary and events after upto_seq"]
  F -- "No" --> H["Use all events"]
  G --> I["derive_messages: system, user, assistant, tool_result"]
  H --> I
  I --> J["History for model request"]
```

## Scheduler Loop

```mermaid
sequenceDiagram
  autonumber
  participant Sched as Scheduler
  participant Store
  participant Agent as Agent runtime
  participant Channel

  Sched->>Store: Fetch enabled jobs with the nearest next_run_at
  Sched->>Sched: sleep_until or wake on Notify
  Sched->>Store: Check for an active run of this job
  alt Active run still exists
    Sched->>Store: Record run as skipped
  else No active run
    Sched->>Store: Create cron_run as running and a new session
    Sched->>Agent: Run turn with job prompt, policy never
    Agent-->>Sched: Final result
    Sched->>Channel: Send result to target
    Sched->>Store: Update cron_run to done or error
  end
  Sched->>Store: Compute and save next_run_at
```

## Catch-up at Boot

```mermaid
flowchart TD
  A["Daemon boots"] --> B["Fetch jobs with next_run_at in the past"]
  B --> C{"Missed by less than 1 hour?"}
  C -- "Yes" --> D["Run once"]
  C -- "No" --> E["Record as skipped"]
  D --> F["Compute next next_run_at"]
  E --> F
  F --> G["Enter scheduler loop"]
```
