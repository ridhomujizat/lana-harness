# Tasks: Background jobs

Test command: `nix develop -c go vet ./... && nix develop -c go test -count=1 ./...`; the web
task also `cd web/ui && env -u NODE_ENV npx vitest run && npm run build`.

## Wave 1

### T-001 Foreground fix, registry, tools, context line, shutdown

- Status: Done 2026-10-31. Evidence: go vet clean, go test all ok. Session delete is not wired: lana has no session delete path yet.

- Files: `tools/bash.go`, new `internal/jobs/`, new `tools/jobs.go`, `tools/module.go`, `core/tool.go` (`ToolCtx.Jobs`, `JobService`), `agent/toolrunner.go`, `agent/turn.go` (companion tools, context line), `internal/agenthome/prompt.go` (`PromptInput.Jobs`), `lana.go` (create, wire, shutdown), session delete path, tests.
- Covers: REQ-JOB-001, REQ-JOB-010–018. Design §1–§4, §6 (Go).

## Wave 2

### T-002 Web jobs strip

- Files: web session routes (new `web/jobs.go`), runtime backend method(s) in `cmd/lana/web.go`, `web/ui/src/api/sessions.ts` (or new `api/jobs.ts`), `web/ui/src/views/session.tsx`, `web/ui/src/styles/app.css`, tests, `web/dist`.
- Covers: REQ-JOB-020. Design §5.
