# Tasks: Session search

- **Status**: Completed
- **Revision**: 1

Each task is one `dsh-coder` run. Test command for every task:
`nix develop -c go vet ./... && nix develop -c go test -count=1 ./...`.

## Wave 1

### T-001 Index and store search API

- Status: Done 2026-11-01. Evidence: `go vet ./...` clean, `go test -count=1 ./...` all ok; store tests cover AC-SSR-001, AC-SSR-002, ranking, subagent exclusion, fork inclusion, caller upto rule, `TextWindow`, MATCH safety.
- Files: new `store/migrations/0004_session_text.sql`, `store/store.go`, new `core/sessionsearch.go`,
  `internal/agenthome/memory.go` (export `IsStopWord`), `store/store_test.go`.
- Covers: REQ-SSR-001–003, store side of REQ-SSR-011–015. Design §1, §2.
- Acceptance: AC-SSR-001, AC-SSR-002, store cases of design §4.

## Wave 2

### T-002 `session_search` tool and wiring

- Status: Done 2026-11-01. Evidence: `go vet ./...` clean, `go test -count=1 ./...` all ok (tools, agent, archtest). Smoke test on a copy of `~/.lana/lana.db`: migration 3→4 backfilled 166 rows (82 user, 84 assistant); `SearchText` for "boss", "telegram token", "memory" returns ranked hits with snippets.
- Files: new `tools/session_search.go`, new `tools/session_search_test.go`, `tools/module.go`,
  `core/tool.go` (`ToolCtx.Sessions`), `agent/turn.go` (set it), `agent/loopguard.go`, `lana.go`
  (catalog), tests.
- Covers: REQ-SSR-010–018. Design §3.
- Acceptance: AC-SSR-003, AC-SSR-004, AC-SSR-005.
