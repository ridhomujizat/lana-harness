# Tasks: FigCraft viewer in Workbench Files

- **Requirements**: [requirements.md](requirements.md) (revision 1)
- **Contract**: [api-contract.md](api-contract.md) (revision 1)
- **Design**: FigCraft page "Workbench · FigCraft viewer" in `design/lana-design-system.figcraft`; PNGs `design/export/figcraft-viewer-*.png`
- **Status**: Done, merged into development 2026-11-22
- **Branch**: `feature/figcraft-viewer`, worktree `../lana-harness-worktree/figcraft-viewer`, from origin/development 5637bd5

Go code goes through the `dsh-coder` implementer, one task per run. Web code (`web/ui`) is written in the main session.

## Coverage

| Requirements | Tasks | Verification |
|---|---|---|
| REQ-FV-001…008, 050 (types) | T-001 | `go test -race ./internal/workbench/figcraft/` |
| REQ-FV-010…013 | T-002 | `go test -race ./internal/workbench/figcraft/` |
| REQ-FV-020…024, 050, API contract | T-003 | `go test ./web/ -run WorkbenchFigcraft` |
| REQ-FV-060…069 | T-004 | vitest, `tsc`, build, preview screenshots |
| REQ-FV-100…102 | T-001…T-004, T-005 | `go test ./...`, `npm test`, live check against the real file and app |

## Tasks

- [x] `T-001` `internal/workbench/figcraft`: CLI, file mode, cache, Service
  - Requirements: REQ-FV-001…008, the Settings type of REQ-FV-050
  - Scope: new `internal/workbench/figcraft/{figcraft.go,cli.go,file.go,cache.go}` and tests
  - Evidence: 2026-11-22, 1 implementer run; `go test -race ./internal/workbench/figcraft/` passes.
- [x] `T-002` Live mode over the FigCraft control port
  - Depends on: T-001
  - Requirements: REQ-FV-010…013
  - Scope: `internal/workbench/figcraft/{live.go,discover.go}`, the source choice in `figcraft.go`, tests
  - Evidence: 2026-11-22, 1 implementer run; fake control server tests pass; live-checked against FigCraft 0.18.1 on :47810.
- [x] `T-003` HTTP routes and settings store
  - Depends on: T-001
  - Requirements: REQ-FV-020…024, 050
  - Scope: new `web/workbench_figcraft.go`, `web/workbench_figcraft_test.go`, route registration
  - Evidence: 2026-11-22, 1 implementer run; `go test ./web/` passes. The shell CSP gained `img-src 'self' blob: data:` so the viewer can show its renders.
- [x] `T-004` Web viewer
  - Requirements: REQ-FV-060…069, 102
  - Scope: `web/ui/src/api/workbench.ts`, new `web/ui/src/views/workbench-figcraft.tsx`, `workbench-figcraft-model.ts` (+ test), `workbench-files.tsx`, styles
  - Evidence: 2026-11-22, written in the main session; vitest 798 pass, `tsc` and eslint clean, build passes.
- [x] `T-005` Live check
  - The real `design/lana-design-system.figcraft`, in file mode and in live mode against FigCraft on :47810, on a scratch daemon. Desktop and phone width screenshots.
  - Evidence: 2026-11-22, scratch daemon ~/.lana-fv :18477 behind `tailscale serve` :8444; file and live mode, a half-written file (stale fallback), desktop and phone width screenshots in the real Workbench. figcraft-cli updated to 0.18.1 on the Mac.
