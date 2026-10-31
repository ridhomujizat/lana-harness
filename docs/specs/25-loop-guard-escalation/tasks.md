# Tasks: Loop guard escalation and tool-name recovery

- **Status**: Completed

Test command: `nix develop -c go vet ./... && nix develop -c go test -count=1 ./...`.

T-001 and T-003 run in parallel in the main tree (disjoint files). T-002 starts after T-001 is done (it reads
`ToolOutput.Digest` and `ToolOutput.UnknownName`).

## Wave 1

### T-001 Tool-name repair, unknown-name text, result digest

- Status: Done 2026-10-31. Evidence: go vet clean, go test ./... all ok.
- Files: new `agent/toolname.go`, `agent/toolname_test.go`, `agent/toolrunner.go`,
  `agent/toolrunner_test.go`, `core/tool.go` (`Digest`, `UnknownName`).
- Covers: REQ-LPG-020, -021. Design §1, §3.

### T-003 `loop` end reason on every surface

- Status: Done 2026-10-31. Evidence: go vet clean, go test ./... all ok, vitest 124/124, `make web-check` ok.
- Files: `lana.go`, `web/ui/src/views/turn.tsx`, `web/ui/src/views/session.tsx`,
  `web/ui/src/views/team.tsx`, `web/ui/src/lib/format.ts`, `web/ui/src/views/turn.test.tsx`,
  `channels/telegram/chat.go`, `channels/cli/tui/model.go`, their tests.
- Covers: REQ-LPG-010, API-LPG-002, -003. Design §4.

## Wave 2

### T-002 Detectors, block, halt

- Status: Done 2026-11-01. Evidence: go vet clean, gofmt clean, go test ./... all ok (30 packages).
- Files: `agent/loopguard.go`, `agent/loopguard_test.go`, `agent/turn.go`,
  `agent/turn_loopguard_test.go`.
- Covers: REQ-LPG-001–009, -011–013. Design §2.
