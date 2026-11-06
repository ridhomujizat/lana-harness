# 0005. Use SQLite as the single storage

- **Status**: Accepted (Rust implementation details replaced by [0013](0013-use-go-as-single-binary.md); the architectural decision stands)
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0003](0003-multi-agent-with-channel-routing.md), [0007](0007-cron-scheduler-inside-daemon.md), [0009](0009-session-as-append-only-event-log.md)

## Context

The harness needs to store sessions and message history, long-term memory, cron jobs, and
permission audit. zeroclaw uses SQLite. nouride keeps all state in one data folder
(`.nouride/`); its public documentation only mentions a `nougate.db` file and does not state the database
engine explicitly.

The lightweight target rules out a separate database server.

## Decision

We use **a single SQLite file** at `~/.lana/lana.db` through `rusqlite` (`bundled` feature).

- `WAL` mode, `synchronous = NORMAL`, `foreign_keys = ON`.
- A single writer task (actor) receives write commands through an `mpsc` channel. Reads use a small pool of
  read-only connections. This avoids `SQLITE_BUSY` without manual locking.
- Schema migrations are numbered SQL files embedded in the binary and run at boot
  (`PRAGMA user_version`).

Memory in v1 uses **FTS5** (full-text search, BM25 ranking). Agents access it through the
`memory_save` and `memory_search` tools. The `embedding BLOB` column is prepared but empty; vector
search (for example `sqlite-vec`) can be added later without a data migration.

Core tables:

| Table | Contents |
|---|---|
| `session_events` | Append-only event log, the source of truth for sessions (see [0009](0009-session-as-append-only-event-log.md)) |
| `sessions` | Projection: `id`, `agent`, `channel`, `address`, `title`, `status`, `parent_session_id`, `inherited_seq`, `last_seq` |
| `memories` + `memories_fts` | `scope` (global/agent), `content`, `tags`, `embedding` |
| `cron_jobs` | `agent`, `schedule`, `prompt`, `target_channel`, `target_address`, `next_run_at`, `enabled` |
| `cron_runs` | `job_id`, `session_id`, `status`, `started_at`, `finished_at`, `error` |

Permission approvals have no table of their own. The `approval/asked` and `approval/decided` event pairs in
`session_events` are their audit trail. An index on `(kind, created_at)` is used for the audit page in the dashboard.

Context window: if a session's tokens exceed the limit (default 70% of the model's context), the compaction
module writes a `compaction/summary` event. Older events are kept.

Secrets (API keys, bot tokens) are **not** stored in the DB. Secrets are read from env vars or from the file
`~/.lana/secrets.env` with `0600` permissions.

## Consequences

**Positive**
- Backup = copy one file. No additional service.
- FTS5 is enough for personal memory without embedding costs.
- A complete history makes replay and debugging easier.

**Negative / tradeoffs**
- `bundled` adds roughly 1-2 MB to the binary.
- A single writer limits write throughput. Enough for personal use.
- FTS5 does not understand synonyms or semantic meaning.

**Neutral**
- History data grows continuously. The command `lana db prune --older-than 90d` is provided.

## Alternatives considered

- **SQLite + embeddings from v1** — semantic search. Deferred because it needs an embedding provider and costs money.
- **Markdown files** — easy to edit by hand. Rejected for sessions and cron, but agent prompts and skills remain files.
- **Postgres** — rejected because it needs a separate server.
