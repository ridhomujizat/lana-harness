# Tasks: Project context files

- **Requirements**: [requirements.md](requirements.md) (revision 1)
- **Contract**: [api-contract.md](api-contract.md) (revision 1)
- **Design**: [design.md](design.md) (revision 1)
- **Status**: Draft

## Coverage

| Acceptance criterion | Evidence owner | Verification |
|---|---|---|
| AC-PCX-001, 003 | T-001 | `go test ./config/ ./core/` |
| AC-PCX-002, 004, 006, 007, 008, 009, 010, 011, 012 | T-002, T-003 | `go test ./internal/projectctx/ ./agent/ -race` |
| AC-PCX-005 | T-003 | `go test ./agent/ -run SystemUpdate` |

## Tasks

- [x] `T-001` Add the `context_files` config setting
  - Requirements: REQ-PCX-001; API-PCX-001
  - Scope: `core/config.go`, `config/validate.go`, `config/load.go` if needed, and tests
  - Verification: `nix develop -c go test ./config/ ./core/`
  - Evidence: 2026-11-21, commit 3ed7c5d — 1 implementer run; the web agent editor keeps the absent/empty
    distinction. `go test ./config/ ./core/ ./web/` pass.

- [x] `T-002` Add the `internal/projectctx` package
  - Depends on: T-001
  - Requirements: REQ-PCX-002 (render), 003 (Note), 004 (SeenFromEvents), 005, 006
  - Scope: new `internal/projectctx/*.go` with tests
  - Verification: `nix develop -c go test -race ./internal/projectctx/`
  - Evidence: 2026-11-21, commit 67f2931 — 1 implementer run; `-race` tests pass.

- [x] `T-003` Wire the prompt section and the tool-result notes
  - Depends on: T-002
  - Requirements: REQ-PCX-002, 003, 004
  - Scope: `internal/agenthome/prompt.go`, `agent/turn.go`, `agent/toolrunner.go`, and tests
  - Verification: `nix develop -c go test -race ./internal/agenthome/ ./agent/`
  - Evidence: 2026-11-21 — 1 implementer run plus an orchestrator one-line fix: the Claude Code runtime
    (`agent/external.go`) builds its prompt without the project section. Full `go test ./...` passes.

## Final verification

- [x] `nix develop -c go build ./...` and `nix develop -c go test ./...` pass.
- [ ] A live check with the dev daemon: a Lana turn that reads a file in a repository shows its
      `CLAUDE.md` once.
