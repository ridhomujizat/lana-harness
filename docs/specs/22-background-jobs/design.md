# Design: Background jobs

- **Status**: Approved
- **Requirements**: [requirements.md](requirements.md)

## 1. Foreground fix (REQ-JOB-001)

`tools/bash.go`: set `cmd.WaitDelay = 2 * time.Second` so `cmd.Wait` returns 2 s after the shell
exits even when a leftover process holds the output pipe. On the normal `done` path do not call
`killGroup`. Timeout and cancel paths keep `killGroup`.

## 2. Job registry

New package `internal/jobs` (no import of `agent`):

```go
type Status string // "running", "exited", "killed"
type Job struct {
    ID, SessionID, Command, LogPath string
    PID       int
    StartedAt time.Time
    EndedAt   time.Time
    Status    Status
    ExitCode  int
    readOff   int64 // job_output cursor
    announced bool  // reported as ended in a turn <context>
}
type Registry struct { Root string; MaxPerSession int /* 10 */; ... }
func (r *Registry) Start(sessionID, workspace, command string) (*Job, error)
func (r *Registry) Get(sessionID, id string) (*Job, bool)
func (r *Registry) List(sessionID string) []Job
func (r *Registry) ReadNew(sessionID, id string, max int) (text string, dropped int64, job Job, err error)
func (r *Registry) Tail(sessionID, id string, max int) (string, error)
func (r *Registry) Wait(ctx context.Context, sessionID, id string, d time.Duration) Job
func (r *Registry) Kill(sessionID, id string) (Job, error)    // SIGTERM group, SIGKILL after 5 s
func (r *Registry) KillSession(sessionID string)
func (r *Registry) Shutdown()                                // kill all, used at daemon stop
func (r *Registry) ContextLine(sessionID string) string      // REQ-JOB-017, marks ended jobs announced
```

- Start: `exec.Command("sh", "-c", command)`, `Dir` = workspace, `SysProcAttr{Setsid: true}`,
  stdin nil, stdout/stderr = the opened log file (`O_CREATE|O_APPEND|O_WRONLY`, 0600; dir 0700).
  A goroutine waits and records status/exit code. Ids `bash-` + 16 hex from crypto/rand.
- Log dir: `<home>/jobs/<sha256(session)[:16]>/`, same hashing as `internal/spill`.
- Concurrency: one mutex; the per-session running count enforces the cap.

## 3. Tools (REQ-JOB-010–016)

- `bash` input gains `run_in_background` (bool). Background path: `registry.Start`, wait up to
  3 s or until exit, return:
  `started job <id> (pid <pid>), log <path>\n[status: running]\n<tail ≤ 4000 chars>`.
  The `bash` description adds the REQ-JOB-016 sentence.
- New `tools/jobs.go`: `job_output`, `job_list`, `job_kill`, reading the registry through
  `core.ToolCtx.Jobs` (new field, interface `core.JobService` with the methods the tools need) so
  `tools` does not import the registry implementation directly.
- Companion rule (REQ-JOB-015): where the agent's tool set is resolved (agent/turn.go or the tool
  registry filter), add the three job tools whenever `bash` is present. `disabled_tools` still
  removes them.
- Risk: `job_output`, `job_list` → `RiskRead`; `job_kill` → the bash risk class.

## 4. Wiring

- The registry is created once per runtime (`lana.go`, next to the spill store), `Root` = home.
  It is passed into `ToolCtx.Jobs` by `agent/toolrunner.go` (find where `ToolCtx` is built).
- `<context>` line: `agenthome.PromptInput` gains `Jobs string`; `ContextBlock` appends it when
  non-empty; agent/turn.go fills it from `registry.ContextLine(sessionID)` when building the turn
  prompt. Format:
  `background jobs: bash-ab12… running (npm run dev, 4m); bash-cd34… exited 1 (npm test)`.
- Shutdown: the runtime's stop path calls `registry.Shutdown()`. Session delete calls
  `KillSession` (find the session delete path in agent/registry.go or web).

## 5. Web (REQ-JOB-020)

- `GET /v1/sessions/{id}/jobs` → `[{id, command, status, exit_code, pid, started_at, ended_at, tail}]`
  (tail ≤ 4 000 chars). `POST /v1/sessions/{id}/jobs/{job}/stop` → the job after kill.
  Same auth/CSRF middleware as other session routes. Reached through the runtime backend
  interface the web package already uses for sessions.
- `web/ui/src/views/session.tsx`: a "Background jobs" strip above the composer, polling every 3 s
  while the page is open and at least one job runs; each row: truncated command, status, age,
  "Logs" toggle (pre, tail), "Stop" button.

## 6. Verification

- Foreground: `sh -c 'sleep 30 & echo hi'` returns `hi` in < 3 s and the sleep is still alive
  afterwards; a command that never exits still times out and is killed.
- Background: `run_in_background` of a script that prints "ready" then sleeps returns within ~3 s
  with "ready" in the tail and status running; `job_output` returns only new output; `job_kill`
  stops it (process gone); the 11th running job is refused; another session's id is refused.
- `job_output wait:true` returns when a short job exits, with `[status: exited 0]`.
- Companion rule: an agent with `bash` sees the job tools; without `bash` it does not.
- `<context>` lists a running job; an ended job is listed once.
- `Shutdown` kills all job process groups.
- Web: list and stop endpoints; vitest for the strip.
