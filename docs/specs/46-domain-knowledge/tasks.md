# Tasks: Domain knowledge per agent

- **Status**: In Progress
- **Revision**: 1

Each Go task is one `dsh-coder` run (implementer per `.claude/implementer.local`). UI is coded
directly. Test command for every Go task:
`nix develop -c go vet ./... && nix develop -c go test -count=1 ./<affected>/...`; the full
suite (`go test -race -count=1 ./...`, `golangci-lint run`) runs after each wave.

## Wave 1

### T-001 Vault scopes and supersede (`internal/knowledge` vault side)

- Status: Pending
- Files: new `internal/knowledge/scope.go` + test; `vault.go`, `write.go`, `page.go` + tests.
- Covers: REQ-DKN-001–003, -025. Design §1.
- Acceptance: domain create/append, same title in two scopes, domain `index.md`, vault `index.md`
  domains section, log scope suffix, `AppendJournalIn`, supersede format and errors.

### T-002 Agent memory limit and skill proposals (`internal/agenthome`, config)

- Status: Pending
- Files: `internal/agenthome/agenthome.go`, `skills.go` + tests; `core/config.go`
  (`SkillWrite`), `config/validate.go` + test; `web/agentfiles.go` + test.
- Covers: REQ-DKN-040, -050, -051 (storage side), API-DKN-002, -009. Design §6, §7.
- Acceptance: AC-DKN-006 (agenthome and web parts), skill proposal create/replace/list/approve/
  reject, config validation.

## Wave 2

### T-003 Index schema v3 (`internal/knowledge` index side)

- Status: Pending
- Files: `internal/knowledge/index.go`, `search.go`, `chunk.go` + tests.
- Covers: REQ-DKN-004, -012 (index part), -026. Design §2.
- Acceptance: AC-DKN-001 (index part), scope filter, recall preference, stripped superseded text,
  proposals CRUD, `ScopeStats`, `Pages`, v2→v3 migration.

## Wave 3

### T-004 Core types and scoped service

- Status: Pending
- Files: `core/knowledge.go`, `core/tool.go` (`OwnerTurn`), `agent/knowledge/service.go`,
  `journal.go`, `lint.go`, `agent.go` + tests; callers of `Recall` adjusted to compile.
- Covers: REQ-DKN-010–011 (service), -020–022, -030, -060 (proposal part). Design §3, §4 (not
  4.2/4.3), §4.4.
- Acceptance: AC-DKN-003 (service part), journal routing, `Propose`/`DecideProposal`, `ReadIn`
  order, lint scope.

## Wave 4

### T-005 Domain promotion and nightly order

- Status: Pending
- Files: new `agent/knowledge/promote_domain.go` + test; `upkeep.go`, `promote.go`.
- Covers: REQ-DKN-031–034. Design §4.2, §4.3.
- Acceptance: AC-DKN-005.

### T-006 Tools and turn

- Status: Pending
- Files: `tools/knowledge.go`, `tools/skills.go` + tests; `agent/turn.go` (recall scopes,
  `OwnerTurn`) + test.
- Covers: REQ-DKN-010–012, -020–021, -025, -050, -052. Design §5, §8. API-DKN-001.
- Acceptance: AC-DKN-002, -003 (tool part), -007.

## Wave 5

### T-007 Runtime and web API

- Status: Pending
- Files: `lana.go`, `web/knowledge.go`, `web/overview.go`, `cmd/lana/daemon.go` (or the web
  bridge file) + tests.
- Covers: REQ-DKN-060, -061, API-DKN-003–008. Design §9.
- Acceptance: AC-DKN-008 (Go part).

### T-008 Web UI

- Status: Pending
- Files: `web/ui/src/api/knowledge.ts`, `views/knowledge.tsx`, `views/agent-overview.tsx`, the
  sidebar component, styles, tests; `web/dist`.
- Covers: REQ-DKN-070–072. Design §10.
- Acceptance: AC-DKN-008 (vitest part).

## Wave 6

### T-009 Live smoke

- Status: Pending
- Scratch `LANA_HOME`, real model, two agents with jobs; evidence recorded below.
