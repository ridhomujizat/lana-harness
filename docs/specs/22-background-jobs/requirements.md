# Requirements: Background jobs

- **Issue**: —
- **Status**: Implemented
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: — (tool inputs, two tools, two web endpoints; listed below)
- **Related ADR**: —

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-31 | "bisa ga kamu buat app react todo list. dan jalankan di laptop ini" (session `06GRWP55TNRKSACZKTR40BCPRM`), then "ya boleh implement" | goal |
| Incident 2026-10-31 | Seq 109–134 of that session: `cd … && nohup npm run dev … > dev.log 2>&1 &` returned `timed out after 60 seconds`; the timeout's `killGroup` (SIGKILL to the process group, `tools/bash.go`) killed vite; retries with `setsid` (`sh: setsid: command not found` on macOS) and `disown` wasted 10+ steps. Cause: the backgrounded AND-list runs in a subshell whose stdout is the tool's output pipe, so `cmd.Wait` blocks until the timeout. | problem |
| deepseek-harness | `packages/jobs/jobs/README.md` (job ids `<kind>-<short-uuid>`, owner-scoped, output ring), `packages/jobs/jobs-local/README.md` (in-process registry, max 10 running per owner, jobs die with the harness), `packages/jobs/tool-jobs/README.md` (`job_output`, `job_list`, `job_kill`; reads return output since the previous read and end with `[status: …]`) | reference design |

## Requirements

### Foreground commands

- **REQ-JOB-001** A foreground `bash` call returns when its shell exits. Output pipes still held by
  processes the command left behind are closed 2 s after the shell exits, and those processes are
  not killed. The timeout still kills the whole process group of a shell that has not exited.

### Background jobs

- **REQ-JOB-010** `bash` accepts `run_in_background: true`. The command starts in a new session
  (own process group, no controlling terminal), stdin `/dev/null`, stdout and stderr appended to
  a private log file (`<home>/jobs/<sha256(session)[:16]>/<job-id>.log`, dir 0700, file 0600).
  The call waits up to 3 s, then returns the job id, the pid, the log path, the status, and the
  output so far (tail, at most 4 000 characters). If the command already exited, the exit code is
  included.
- **REQ-JOB-011** Job ids are `bash-<16 lowercase hex>`. Jobs belong to the session that started
  them; another session cannot read or stop them. At most 10 running jobs per session; the 11th
  start fails with a message telling the model to stop a job first.
- **REQ-JOB-012** `job_output(job_id, wait?, timeout_secs?)` returns the output written since the
  previous `job_output` read of that job (at most 16 000 characters, older part dropped with a
  notice) and ends with `[status: running|exited <code>|killed]`. With `wait: true` it waits up to
  `timeout_secs` (default 30, max 600) for the job to end.
- **REQ-JOB-013** `job_list()` lists the session's jobs, one per line:
  `<id> <status> pid=<pid> <age> — <first 120 chars of the command>`.
- **REQ-JOB-014** `job_kill(job_id)` sends SIGTERM to the job's process group, then SIGKILL after
  5 s, and returns the final status.
- **REQ-JOB-015** The job tools are available to every agent that has `bash`, without listing them
  in `tools`. Risk: `job_output`/`job_list` read; `job_kill` same risk class as `bash`.
- **REQ-JOB-016** The `bash` tool description tells the model: use `run_in_background: true` for
  servers, watchers, and anything that does not exit; never use `&`, `nohup`, `setsid`, or
  `disown` for that.
- **REQ-JOB-017** The turn's `<context>` block lists the session's jobs when there are any:
  running jobs, and jobs that ended since the last turn with their exit status.
- **REQ-JOB-018** Jobs live in the daemon process: daemon shutdown (including graceful restart)
  kills every job's process group. lana has no session delete yet; `KillSession` is ready for it. Jobs are not restored after
  a restart; their log files stay until the session is deleted.

### Web

- **REQ-JOB-020** The session page shows the session's jobs above the composer when there is at
  least one running job: command (truncated), status, age, a "Logs" toggle with the last 4 000
  characters, and a "Stop" button. Endpoints: `GET /v1/sessions/{id}/jobs`,
  `POST /v1/sessions/{id}/jobs/{job}/stop`.

## Out of scope

- Waking an idle agent when a job ends (deepseek-harness completion notices). Status reaches the
  model through `job_output`, `job_list`, and the next turn's `<context>`.
- PTY jobs, durable jobs across restarts, and job control from Telegram.
