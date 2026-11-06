# Tasks: Tool reliability and the eval runner

Test command: `nix develop -c go vet ./... && nix develop -c go test -count=1 ./...`.

The three tasks touch separate files and run in parallel worktrees. T-003 runs first against the
base commit to record a baseline report.

## Wave 1

### T-001 Tool descriptions, read numbering, edit and write improvements

- Status: Done 2026-10-31. Evidence: go vet clean, go test all ok, lana-eval run.
- Files: `tools/files.go`, new `tools/edithint.go`, `tools/search.go`, `tools/bash.go`,
  `tools/webfetch.go`, `tools/jobs.go`, their tests, agent tests that assert raw `read_file` output.
- Covers: REQ-TRL-001–008. Design §1–§3.

### T-002 Loop guard

- Status: Done 2026-10-31. Evidence: go vet clean, go test all ok, lana-eval run.
- Files: new `agent/loopguard.go`, `agent/loopguard_test.go`, `agent/turn.go`, a turn test.
- Covers: REQ-TRL-010–014. Design §4.

### T-003 Eval runner

- Status: Done 2026-10-31. Evidence: go vet clean, go test all ok, lana-eval run.
- Files: new `cmd/lana-eval/` (`main.go`, `tasks.go`, `run.go`, `report.go`, tests for report and
  metrics).
- Covers: REQ-TRL-020–025. Design §5.
