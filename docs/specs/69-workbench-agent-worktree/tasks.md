# Tasks: Agents report the worktree they work in

- **Requirements**: [requirements.md](requirements.md) (draft r1)
- **Contract**: [api-contract.md](api-contract.md) (draft r1)
- **Depends on**: spec 66 (merged into `development`), spec 55, spec 56 (Claude Code accounts), spec 60 (restore)
- **Status**: Not started. Implementation later (owner, 2026-11-22).
- **Branch**: to be created from `development` when work starts; spec drafted on `docs/workbench-agent-worktree`

Go code goes through the `dsh-coder` implementer, one task per run. Web code (`web/ui`) is written in the main session.

## Coverage

| Requirements | Tasks | Verification |
|---|---|---|
| REQ-WA-001, 002 | T-001 | `go test ./internal/workbench/` |
| REQ-WA-010, 011, 012 | T-002 | `go test ./cmd/lana/ -run Hook` |
| REQ-WA-013, 014 | T-003 | `go test ./internal/workbench/agenthook/` |
| REQ-WA-030…034 | T-004 | `go test ./web/ -run WorkbenchWorktreeReport` |
| REQ-WA-020…023 | T-005 | `go test ./cmd/lana/ -run WorkbenchUse` |
| REQ-WA-015, 040…043 | T-006 | vitest, `tsc`, build, preview screenshots |
| REQ-WA-051, 052 | T-001…T-006, T-007 | `go test ./...`, `npm test`, live check |
| REQ-WA-060…062 | T-008 | `go test ./tools/... ./agent/...` |
| REQ-WA-070, 071 | T-009 | vitest, preview screenshots |

## Phase 1 tasks

- [ ] `T-001` Core: `LANA_WORKBENCH_TTY` and the in-pane check
  - Requirements: REQ-WA-001, 002
  - Scope: `internal/workbench/session.go` (`sessionEnv`), a small `InPane()` helper usable by the CLI, tests

- [ ] `T-002` CLI: `lana workbench hook session-start`
  - Depends on: T-001
  - Requirements: REQ-WA-010, 011, 012
  - Scope: `cmd/lana/workbench.go`, new `cmd/lana/workbench_hook.go`, tests (inside, outside, nested tty, each source)

- [ ] `T-003` Hook install into Claude Code settings
  - Depends on: T-002
  - Requirements: REQ-WA-013, 014
  - Scope: new `internal/workbench/agenthook` (merge, remove, backup, folder list from accounts), daemon start hook-up, `GET/PUT /v1/workbench/agent-hook`, tests

- [ ] `T-004` Daemon: report store and routes
  - Requirements: REQ-WA-030…034
  - Scope: `web/workbench_report.go` (store, `PUT/DELETE …/worktree`, `work_dir` on sessions and events, end rules from session events), tests

- [ ] `T-005` CLI: `lana workbench use`
  - Depends on: T-001, T-004
  - Requirements: REQ-WA-020…023
  - Scope: new `cmd/lana/workbench_use.go` (daemon client with `daemon.secret`, `--new` through the spec 66 create route), tests against a test server

- [ ] `T-006` Web: worktree from `work_dir`, reported chip, picker, toast, hook switch
  - Depends on: T-004
  - Requirements: REQ-WA-015, 040…043, 014 (switch UI)
  - Scope: `web/ui/src/views/workbench-*`, Workbench settings

- [ ] `T-007` Live check
  - Requirements: REQ-WA-052
  - Two Claude Code sessions in two worktrees on a scratch daemon; Ghostty and a nested herdr show no context.

## Phase 2 tasks (after phase 1 is live)

- [ ] `T-008` Lana tool `use_worktree`, prompt head line, session record field
  - Requirements: REQ-WA-060, 061, 062

- [ ] `T-009` Chat header chip and Changes panel in chat; git routes without the core
  - Requirements: REQ-WA-070, 071
