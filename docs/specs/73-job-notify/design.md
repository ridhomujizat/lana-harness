# Design: Background job notices

- **Status**: Approved
- **Requirements**: [requirements.md](requirements.md)

## 1. Registry end hook (REQ-JBN-001)

`internal/jobs/jobs.go`:

- `Registry` gains `OnEnd func(Job)` (set before the first `Start`) and a `closing bool`.
- `Job` gains `silent bool`. New `KillSilent(sessionID, id string) (Job, error)`: sets `silent`
  under `mu`, then does what `Kill` does. `Kill` (used by the web Stop route) stays notifying.
- `Shutdown()` sets `closing = true` under `mu` before it kills.
- `reap`: after recording the final state, decide `notify := OnEnd != nil && !closing && !job.silent`;
  when `notify`, set `job.announced = true` (REQ-JBN-002, so `<context>` skips it). Take the
  snapshot, unlock, close `done`, then call `OnEnd(snapshot)` **outside the lock** (the hook
  reads the tail and talks to the agent registry).
- Keep the existing defer structure correct: `done` must still be closed and deleted exactly once.

## 2. Agent side (REQ-JBN-002..005)

- `core/event.go`: `UserMessage` gains `Job *JobNotice \`json:"job,omitempty"\``;
  `type JobNotice struct { ID, Command, Status string; ExitCode int; Tail string }` with JSON
  names `id, command, status, exit_code, tail`.
- `agent/toolrunner.go`: `jobAdapter.Kill` calls `reg.KillSilent` (the agent's own `job_kill`).
- `agent/turn.go`: `inboxItem` gains `job *core.JobNotice`. When the claimed item (initial or
  popped) has `job != nil`:
  - it is a runtime note for display, memory capture, titles, and owner-message logic
    (`runtimeNote`-like: payload `Runtime: true`, plus `Job: item.job`);
  - but it keeps inbox-claim semantics: if the step cap or a halt returns the claim, it goes back
    to the inbox (`pushFrontInbox`) like an owner message, so a notice is never lost;
  - it resets the loop guard like an owner message (new information arrived).
  Find every branch that tests `runtimeNote` and decide per branch; add a small helper rather
  than sprinkling `item.job != nil`.
- `agent/registry.go`: `func (r *Registry) SendJobNotice(sessionID string, n core.JobNotice, text string) error`
  — same as `SendReply` (idle: `startTurnAsync`; busy: append to inbox) with
  `inboxItem{text: text, job: &n}`. Returns nil (drops) when the registry is closed or the
  session does not exist (REQ-JBN-005). Use `context.Background()` for the existence check.
- `agent/jobnotice.go` (new): `func JobNoticeText(n core.JobNotice, runtime string) string`
  building the REQ-JBN-002 text (command cut to 200 runes, tail to the last 2000 chars of
  rune-safe text).
- `lana.go` near `jobs.New`: set `jobsRegistry.OnEnd` to a func that reads
  `jobsRegistry.Tail(job.SessionID, job.ID, 2000)`, builds the notice (status `exited`/`killed`,
  exit code, runtime label from `StartedAt`..`EndedAt`), and calls `registry.SendJobNotice`.
  Log and ignore errors. Set it before the registry is used.

## 3. Web (REQ-JBN-020..023)

- `web/ui/src/api/sessions.ts`: add `job?: {id; command; status; exit_code; tail}` to the user
  message payload type.
- `web/ui/src/views/turn.tsx` (session turns), the floating agent room timeline
  (`room-timeline.tsx`), and `web/ui/src/lib/format.ts`: before the `runtime === true` skip,
  handle `payload.job` → a `job` item rendered as `JobNoticeLine` (new small component in
  `web/ui/src/views/job-notice.tsx`): icon (check / x-circle / square), label per REQ-JBN-020,
  mono command truncated with ellipsis, click toggles a `<pre>` with the tail. Muted system-line
  styling consistent with the existing system/info rows of the turn view (Gr8r tokens).
- `web/ui/src/views/session.tsx`: replace `JobsStrip` with `JobsPill` (move to
  `web/ui/src/views/jobs-pill.tsx`): pill with pulsing dot + count while ≥ 1 running; click opens a
  popover/panel above it with the existing rows (command, status, age, Logs, Stop). Keep
  `listSessionJobs`/`stopSessionJob`. Refresh: 3 s poll while running; plus a `refreshKey` prop
  the session page bumps whenever its stream delivers a `tool/result` or a `user/message` with
  `job` (REQ-JBN-022). Mount the same pill above the floating agent composer when the room is a
  session (if the floating room view has a composer for it).
- `bash` tool card (where the tool name renders in `turn.tsx`): when the call input has
  `run_in_background: true`, show a small "background" tag (REQ-JBN-023).
- Update `jobs-strip.test.tsx` → `jobs-pill.test.tsx`; add tests for the notice line and the
  refresh-on-event behaviour. Rebuild `web/dist`.
