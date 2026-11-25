# Tasks: Background job notices

Test command: `nix develop -c go vet ./... && nix develop -c go test -count=1 ./...`; the web
task also `cd web/ui && env -u NODE_ENV npx vitest run && npm run build`.

## Wave 1 (parallel)

### T-001 Registry hook, notice, wake

- Status: Done 2026-11-25. Evidence: go vet + go test all ok; vitest 866/867 (message-files preview test fails on development too); live-tested on scratch daemon :18488 (idle wake, mid-turn notice, web Stop, job_kill silent).
- Files: `internal/jobs/jobs.go` (+ test), `core/event.go`, `agent/toolrunner.go`,
  `agent/turn.go`, `agent/registry.go`, new `agent/jobnotice.go` (+ tests), `lana.go`.
- Covers: REQ-JBN-001..005. Design §1–§2.

### T-002 Web pill, notice line, background tag

- Status: Done 2026-11-25. Evidence: go vet + go test all ok; vitest 866/867 (message-files preview test fails on development too); live-tested on scratch daemon :18488 (idle wake, mid-turn notice, web Stop, job_kill silent).
- Files: `web/ui/src/api/sessions.ts`, new `web/ui/src/views/job-notice.tsx`, new
  `web/ui/src/views/jobs-pill.tsx`, `web/ui/src/views/session.tsx`, `web/ui/src/views/turn.tsx`,
  `web/ui/src/views/room-timeline.tsx`, `web/ui/src/lib/format.ts`, styles, tests, `web/dist`.
- Covers: REQ-JBN-020..023. Design §3.
