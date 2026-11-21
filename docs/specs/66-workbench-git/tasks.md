# Tasks: Workbench files, git changes and worktrees

- **Requirements**: [requirements.md](requirements.md) (revision 1)
- **Contract**: [api-contract.md](api-contract.md) (revision 1)
- **Design**: FigCraft pages "Workbench · Files & Changes" and "Workbench · Worktrees" in `design/lana-design-system.figcraft`; PNGs `design/export/workbench-files-*.png`, `design/export/worktrees-*.png`
- **Status**: In progress
- **Branch**: `feature/workbench-files`, worktree `../lana-harness-worktree/workbench-files`

Go code goes through the `dsh-coder` implementer, one task per run. Web code (`web/ui`) is written in the main session.

## Coverage

| Requirements | Tasks | Verification |
|---|---|---|
| REQ-WG-001, 002, 030, 061 | T-001 | `go test ./internal/workbench/gitinfo/` |
| REQ-WG-003, 004, 005, 006 | T-002 | `go test ./internal/workbench/gitinfo/` |
| REQ-WG-007, 008, 009, 010 | T-003 | `go test ./internal/workbench/gitinfo/` |
| REQ-WG-031…037, 050 | T-004 | `go test ./internal/workbench/gitinfo/` |
| REQ-WG-020, 021, API contract | T-005 | `go test ./web/ -run WorkbenchGit` |
| REQ-WG-060, 062, 063, 064, 065 | T-006 | vitest, `tsc`, build, preview screenshots |
| REQ-WG-066, 067, 068 | T-007 | vitest, preview screenshots |
| REQ-WG-070…076 | T-008 | vitest, preview screenshots |
| REQ-WG-080…085 | T-009 | preview screenshots |
| REQ-WG-090, 091 | T-010 | phone preview screenshots |
| REQ-WG-100…102 | T-001…T-010, T-011 | `go test ./...`, `npm test`, live check |

## Tasks

- [x] `T-001` `internal/workbench/gitinfo`: runner, resolve, colour, worktree folder naming
  - Requirements: REQ-WG-001, 002, 030, 061
  - Scope: new `internal/workbench/gitinfo/{git.go,resolve.go,naming.go}` and tests
  - Evidence: 2026-11-21, commit 569d66e — 1 implementer run; `go test -race ./internal/workbench/gitinfo/` passes.

- [x] `T-002` Status, scopes and the diff parser
  - Depends on: T-001
  - Requirements: REQ-WG-003, 004, 005, 006
  - Scope: `internal/workbench/gitinfo/{status.go,diff.go}` and tests
  - Evidence: 2026-11-21, commit 7bfab9e — 1 implementer run; `-race` tests pass. A deleted file's path is
    checked with its parent folder's symlinks resolved (`resolvedLeaf`), since `/var` is a symlink on macOS.

- [x] `T-003` Tree, search, file content and the worktree list
  - Depends on: T-001
  - Requirements: REQ-WG-007, 008, 009, 010
  - Scope: `internal/workbench/gitinfo/{tree.go,worktrees.go}` and tests
  - Evidence: 2026-11-21, commit a1e2ff2 — 1 implementer run; `-race` tests pass. Lint fix in 59fab24.

- [x] `T-004` Worktree actions and settings
  - Depends on: T-003
  - Requirements: REQ-WG-031…037, 050
  - Scope: `internal/workbench/gitinfo/{actions.go,settings.go}` and tests
  - Evidence: 2026-11-21, commit c41e758 — 1 implementer run; `-race` tests pass (bare origin, fetch failure, confirm, in-use).

- [x] `T-005` HTTP routes
  - Depends on: T-002, T-004
  - Requirements: REQ-WG-020, 021, 033 (in-use check against live sessions), API contract
  - Scope: new `web/workbench_git.go`, route registration in `web/workbench.go`, tests
  - Evidence: 2026-11-21, commit 6f3416f — 1 implementer run; `go test -race ./web/ ./internal/workbench/...` passes. The web sends the repo id (`…/.git`); the handlers use its parent folder.

- [x] `T-006` Web: API client, Changes toggle, sliding panel, Files tab
  - Requirements: REQ-WG-060, 062, 063, 064, 065
  - Scope: `web/ui/src/api/workbench-git.ts`, `web/ui/src/views/workbench-git-panel.tsx`, `workbench-files.tsx`, `workbench.tsx`, `styles/pages-gr8r/workbench-git.css`
  - Evidence: adaed0b, 6a21a3f (virtualised viewers), 5014fb8 (live-check fixes); checked live on a scratch daemon (:18466) against this repo's 30 worktrees.

- [x] `T-007` Web: Changes tab and diff viewer
  - Depends on: T-006
  - Requirements: REQ-WG-066, 067, 068
  - Scope: `web/ui/src/views/workbench-changes.tsx`, `workbench-diff.ts` (row model) and tests
  - Evidence: adaed0b, 6a21a3f; live: branch scope with 10 commits / 61 files, unified diff, 2,193-line file opens in 39 ms with 75 DOM rows.

- [x] `T-008` Web: worktree per session
  - Depends on: T-006
  - Requirements: REQ-WG-070…076
  - Scope: `web/ui/src/views/workbench-worktree-model.ts` and tests, pane header, Agents list, tab hover card, `+` menu
  - Evidence: adaed0b, 5014fb8; live: pane chips, focus colour, picker, tab hover card, + menu.

- [x] `T-009` Web: Worktrees tab and dialogs
  - Depends on: T-008
  - Requirements: REQ-WG-080…085
  - Scope: `web/ui/src/views/workbench-worktrees.tsx`, `workbench-worktree-dialogs.tsx`
  - Evidence: adaed0b, 5014fb8; live: Worktrees tab (banners, groups, chips), New worktree dialog (folder under lana-harness-worktree/).

- [x] `T-010` Web: phone
  - Depends on: T-007, T-009
  - Requirements: REQ-WG-090, 091
  - Evidence: adaed0b, 5014fb8; live at 390×844: sheet with scrim, Changes list, full-screen diff with n/N.

- [x] `T-011` Final verification
  - `nix develop -c go test ./...`, `nix develop -c golangci-lint run`, `npm test`, `npm run build`, web dist rebuilt
  - Live check on a scratch daemon against this repo's real worktrees

## Final verification

2026-11-21: `go build ./...`, `go vet ./...`, `go test ./...` pass; `golangci-lint run ./internal/workbench/... ./web/` 0 issues;
`npx tsc`, `npx eslint src`, `npx vitest run` (771 tests) and `npm run build` pass; web dist rebuilt (8eeedfa).
Live check on a scratch daemon (`~/.lana-wg`, 127.0.0.1:18466) with Vite at :5174: Changes, Files, Worktrees, picker,
+ menu, tab hover card, New worktree dialog and the phone sheet compared with the FigCraft frames. Status polls every
3 s with the panel open and every 15 s closed. No worktree action (create, remove, move, prune) was run against the
real repo.
