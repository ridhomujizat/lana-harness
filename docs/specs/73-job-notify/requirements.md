# Requirements: Background job notices

- **Issue**: —
- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: — (one payload field, no new endpoints)
- **Related ADR**: —
- **Extends**: [22-background-jobs](../22-background-jobs/requirements.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-11-25 | "agent lana belum bisa handle background task/service dan nerima kalau service itu mati kaya claude code. dan memberi indicator ada background task yang jalan" | goal |
| Interview 2026-11-25 | Busy turn: insert the notice at the next step. Indicator: "ada indicator message background task jalan, kaya ui claude code chat ini aja". Daemon restart: keep as now (jobs die, no notice). Wake question unanswered: recommended default, wake on every end. | decisions |
| Spec 22 | "Out of scope: Waking an idle agent when a job ends" | gap |
| Spec 22 web strip | Loads the job list once on open and polls only while a job runs, so a job started during the visit does not show until reload. | gap |
| Claude Code | `<task-notification>` user turn when a background Bash task ends; footer pill "N background tasks"; transcript line "Background command … completed (exit code N)". | reference UX |

## Requirements

### Notice (Go)

- **REQ-JBN-001** When a background job ends (exit, or killed by a signal it did not get from the
  agent's own `job_kill`), lana sends one job notice to the session that owns the job. No notice
  is sent for a job ended by that session's `job_kill` call (the call already returned the
  status) or by daemon shutdown (REQ-JOB-018 stays as is). A job stopped from the web Stop button
  does get a notice.
- **REQ-JBN-002** The notice text the model sees is:

  ```
  <task-notification>
  job: <id>
  command: <command, first 200 chars>
  status: exited <code> | killed | killed (stopped by the owner from the web)
  runtime: <age label, e.g. 4m>
  output tail:
  <last ≤ 2000 chars of the log>
  </task-notification>
  Background job <id> ended. Read more with job_output if needed, then tell the owner what happened and whether anything must be restarted.
  ```

  The tail is read without moving the `job_output` cursor. Sending the notice marks the job
  announced, so the next turn's `<context>` does not report it again (REQ-JOB-017).
- **REQ-JBN-003** Idle session: the notice starts a new turn (the agent wakes). Busy session: the
  notice is queued in the session inbox and the turn takes it at its next step, the same way an
  owner message sent mid-turn is taken. Several notices are taken one per step in order.
- **REQ-JBN-004** The notice is stored as a `user/message` with `runtime: true` and a `job` field:
  `{"id","command","status","exit_code","tail","stopped_by"}`. `stopped_by` is `"owner"` when the
  owner stopped the job from the web, and is omitted otherwise. It is not an owner message: memory capture,
  session titles, and the loop guard's owner-message rules treat it like other runtime notes.
- **REQ-JBN-005** A notice for a session whose runner was closed or whose session no longer
  exists is dropped without error. Sessions on the Claude Code runtime have no lana jobs and get
  no notices.

### Indicator (web)

- **REQ-JBN-020** Transcript: a `user/message` with `job` renders as one compact system line in
  the turn, not an owner bubble and not hidden: status icon, "Background task finished" (exit 0),
  "Background task failed · exit N" (exit ≠ 0), or "Background task stopped" (killed), then the
  command in mono, truncated. Clicking the line toggles the stored tail. Same rendering in the
  session page and the floating agent room view, and in `lib/format.ts` text exports.
- **REQ-JBN-021** Background pill: the strip above the composer is replaced by a compact pill in
  Claude Code style, shown while at least one job of the session runs: a pulsing dot and
  "1 background task" / "N background tasks". Clicking it opens a panel listing the jobs (running
  first, then ended, newest first): command, status, age, "Logs" toggle (tail), "Stop" for running
  jobs. Ended jobs stay in the panel until the page reloads; the pill itself hides when nothing
  runs.
- **REQ-JBN-022** The job list refreshes every 3 s while a job runs, and also whenever the session
  stream delivers a `tool/result` event or a job notice, so a job started mid-visit appears
  without reload.
- **REQ-JBN-023** A `bash` tool card whose input has `run_in_background: true` shows a
  "background" tag next to the tool name.

## Out of scope

- Delivering wake-turn replies to Telegram or other channels; macOS/PWA notifications for jobs.
- Keeping jobs alive across daemon restart, or notices after a restart.
- Watching job output for patterns (Claude Code Monitor).
