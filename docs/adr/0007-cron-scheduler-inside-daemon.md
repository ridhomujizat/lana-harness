# 0007. Run the cron scheduler inside the daemon

- **Status**: Accepted (Rust implementation details replaced by [0013](0013-use-go-as-single-binary.md); the architectural decision stands)
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0004](0004-permission-mode-per-agent.md), [0005](0005-sqlite-as-single-storage.md)

## Context

A personal assistant needs reminders ("remind me at 9 in the morning"). Automation/ops needs recurring tasks
("check the VPS disk every hour, report to Telegram if > 80%"). nouride calls this feature
scheduled work and missions. Using the OS `crontab` separates state from the daemon and is hard for an agent to create.

## Decision

The daemon runs **one scheduler task** that reads `cron_jobs` from SQLite.

- Schedule format: a 5-field cron expression (`0 9 * * *`), or a one-shot ISO 8601 timestamp (`at = "2026-11-01T09:00:00+07:00"`).
  Parsing uses the `cron` crate. Timezone per job, defaulting to the local timezone.
- Loop: compute the nearest `next_run_at`, `sleep_until`, run the jobs that are due, recompute.
  Job changes (create/update/delete) wake the loop through `Notify`.
- Each run creates a new session `(channel = "cron", address = job_id, agent)` and runs one agent turn
  with the job's `prompt`. The final result is sent to `target_channel` and `target_address`.
- Runs missed while the daemon was down: executed **once** at boot if still within the `catchup` window
  (default 1 hour); otherwise recorded as `skipped`.
- A job must not overlap with itself. If the previous run has not finished, the new run is `skipped`.
- Cron sessions use the approval policy `never` because no human is present. Tools that require approval are rejected automatically (see [0004](0004-permission-mode-per-agent.md)).

Jobs are created through three paths:

1. The agent, via the `cron_create`, `cron_list`, `cron_delete` tools (risk `Write`).
2. The CLI: `lana cron add --agent ops --schedule "0 * * * *" --prompt "..." --to telegram:123`.
3. The web dashboard.

## Consequences

**Positive**
- No dependency on `crontab` or `launchd` per job.
- Run history is stored in `cron_runs` and can be viewed in the dashboard.

**Negative / tradeoffs**
- Jobs only run while the daemon is alive. For reliability, the daemon is installed as a service (`lana service install`).
- Schedule accuracy is at the level of seconds, not real-time.

**Neutral**
- Long-running "missions" (multi-step, spanning days) are not part of v1.

## Alternatives considered

- **crontab/launchd per job** — rejected because state is scattered and system access is needed.
- **`tokio-cron-scheduler` crate** — full-featured, but keeps its own state. Rejected so that SQLite remains the single source of truth.
