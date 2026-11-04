# Tasks: Workflows

- **Issue**: `—`
- **Requirements**: [requirements.md](requirements.md)
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Design**: [design.md](design.md)
- **Requirements revision**: 1
- **Contract revision**: 1
- **Design revision**: 1
- **Status**: In progress

## Execution rules

- Work happens on branch `feature/workflows` in the worktree `../lana-harness-workflows`. It is not merged into
  `development` without the owner.
- Go code goes through `dsh-coder` (CLAUDE.md). `web/ui` is coded in the main session.
- Waves: T-001 → (T-002 ‖ T-003) → (T-004 ‖ T-005 ‖ T-006) → T-007 → T-008. The web UI chain (T-009–T-011) runs beside
  them against the contract and preview fixtures. T-012 joins.

## Coverage

| Acceptance criterion | Evidence owner | Verification |
|---|---|---|
| `AC-WF-001` | `T-004` | `go test ./internal/workflow -run TestService` |
| `AC-WF-002` | `T-002` | `go test ./internal/workflow -run TestValidate` |
| `AC-WF-003` | `T-004` | `go test ./internal/workflow -run TestScheduler` |
| `AC-WF-004` | `T-005` | `go test ./web -run TestWorkflowWebhook` |
| `AC-WF-005` | `T-005` | `go test ./web -run TestWorkflowCommand` |
| `AC-WF-006`–`AC-WF-015` | `T-004` | `go test ./internal/workflow -run TestEngine` |
| `AC-WF-016` | `T-006` | `go test . -run TestWorkflowPermission` |
| `AC-WF-017`, `AC-WF-018` | `T-004` | `go test ./internal/workflow -run TestAssist` |
| `AC-WF-019`–`AC-WF-021` | `T-011` | `npx vitest run src/views/workflows*` |

## Tasks

- [ ] `T-001` Model, Store and Deps types — `internal/workflow/{model,store,deps}.go`
- [ ] `T-002` Catalog, validation, templates, conditions, cron, lock, MemStore — `internal/workflow/*.go` + tests
- [ ] `T-003` SQLite store — `store/migrations/0006_workflows.sql`, `store/workflow.go`, tests
- [ ] `T-004` Engine, guardian, service, scheduler — `internal/workflow/{engine,guardian,service}.go` + tests
- [ ] `T-005` Web handlers, stream, webhook, chat command — `web/workflows.go`, `web/sessions.go` hook, tests
- [ ] `T-006` Runtime glue — `lana_workflow.go`, `lana.go` (create/start/close), tests
- [ ] `T-007` Telegram — `channels/telegram/workflow.go` (owner buttons, `w:` callbacks, command hook), tests
- [ ] `T-008` Wiring — `cmd/lana/daemon.go`, `cmd/lana/web.go`
- [ ] `T-009` Web API client, routes, sidebar, fixtures
- [ ] `T-010` List and settings pages
- [ ] `T-011` Editor canvas, inspector, assistant, executions
- [ ] `T-012` Build, verify, runtime smoke

## Approval

- **Approved by**: agent on behalf of the owner (gate lifted 2026-11-05)
- **Approved on**: 2026-11-05
