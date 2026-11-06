# 0009. Store sessions as an append-only event log in SQLite

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0002](0002-core-from-modules-services-and-hooks.md), [0005](0005-sqlite-as-single-storage.md), [0011](0011-subagent-as-child-session.md)

## Context

`dsh` uses an **append-only session log** as the source of model context. The principle is *model-visible
means logged*: every request to the model must be reconstructable from the log. Message history,
fork, resume, transcript, and telemetry are all derived from events. `dsh` stores the log
as a JSONL file per session.

`lana` already uses a single SQLite file (see [0005](0005-sqlite-as-single-storage.md)).
The owner wants the event log to stay in SQLite. Features that need this log: session forking, resume after
a crash, subagents, and replay in the web dashboard.

## Decision

A session is an **append-only sequence of events** in the `session_events` table. There is no `messages` table.
The history for the model is computed by the pure function `derive_messages(events) -> Vec<Message>`.

```sql
CREATE TABLE session_events (
  session_id TEXT    NOT NULL,
  seq        INTEGER NOT NULL,          -- increments by 1 per event, starting from 1
  kind       TEXT    NOT NULL,          -- example: 'user/message'
  payload    TEXT    NOT NULL,          -- JSON
  turn_id    TEXT,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;
-- BEFORE UPDATE and BEFORE DELETE triggers call RAISE(ABORT): the log cannot be changed
```

Durable events in v1:

| Event | Contents |
|---|---|
| `session/created` | agent, channel, address, `parent_session_id`, `inherited_seq` |
| `turn/start`, `turn/end` | turn id, stop reason (`end_turn`, `max_steps`, `cancelled`, `interrupted`, `error`) |
| `step/start`, `step/end` | step number, provider, model, token usage |
| `system/message` | the system prompt in use. A new event is written only when the prompt changes |
| `user/message` | user input, including context injected by modules |
| `assistant/message` | complete model answer (text and `tool_use`) |
| `assistant/attempt` | a failed or retried attempt. Not part of the model history |
| `tool/call`, `tool/result` | call id, tool name, input, output or error |
| `approval/asked`, `approval/decided` | approval audit pair (see [0004](0004-permission-mode-per-agent.md)) |
| `question/asked`, `question/answered` | questions from the `ask_user` tool and their answers (see [spec #11](../specs/11-ask-user-question/api-contract.md)) |
| `approval/policy` | session policy change |
| `model/selection` | change of provider or model in the middle of a session |
| `compaction/summary` | summary that replaces events up to `upto_seq` |

Rules:

- Events are written by the store writer task, then broadcast through the `session.event` hook.
- Streamed tokens are not written token by token. The live stream is sent to channels as temporary frames;
  the log only stores the final `assistant/message` or `assistant/attempt`.
- The `sessions` table is a **projection** (title, agent, status, `last_seq`, parent) that can be rebuilt
  from events. The source of truth remains `session_events`.
- **Compaction** writes `compaction/summary { upto_seq }`. `derive_messages` uses the latest summary plus the
  events after `upto_seq`. Old events are not deleted.

**Fork.** `fork(session, at_seq)` creates a new session with `parent_session_id` and
`inherited_seq = at_seq`. Parent events are not copied. When deriving, the store reads the parent's events up to
`inherited_seq`, then the child session's own events. Forking is only allowed at a turn boundary (`turn/end`).

**Resume.** When a session is opened and its last event is not `turn/end`, the store appends closing events:
an error `tool/result` for each `tool/call` without a pair, followed by `turn/end { reason: "interrupted" }`.
After that the session can accept new messages.

**Format version.** Payloads use `serde` with a field `v`. Format migration happens at decode time
(old version converted to the current one in memory), not by rewriting old rows.

## Consequences

**Positive**
- Every model request can be reconstructed. Debugging and replay become easy.
- Forking is cheap because no events are copied.
- A crash in the middle of a turn does not corrupt the session.
- The web dashboard, CLI, and ACP read the same source.

**Negative / tradeoffs**
- Deriving history requires reading many events. Mitigated by compaction and the `(session_id, seq)` index.
- Data keeps growing. `lana db prune` only deletes whole sessions and never truncates a log.
- JSON payloads in SQLite are less efficient than a binary format. Sufficient for personal use.

**Neutral**
- Exporting a session to JSONL (`lana session export <id>`) is available for compatibility with the `dsh` pattern.

## Alternatives considered

- **JSONL per session like `dsh`** — easy to read and diff. Rejected because it needs a separate index
  and the owner wants a single SQLite file.
- **A mutable `messages` table** — simple. Rejected because forking, resume, and audit would not be trustworthy.
