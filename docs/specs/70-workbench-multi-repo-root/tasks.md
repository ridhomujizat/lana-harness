# Tasks: Workbench panel for a folder of several repos

- **Requirements**: [requirements.md](requirements.md) (draft r1)
- **Contract**: [api-contract.md](api-contract.md) (draft r1)
- **Depends on**: spec 66 (merged into `development`); spec 69 for the report parts (REQ-WM-010 report chip, 030 order, 035)
- **Design**: FigCraft page "Workbench · Multi-repo root" (frames 1 Changes per repo, 2 picker, 3 Files on root, 4 agent reported, 5 phone), PNGs `design/export/multi-repo-*.png`; awaiting owner approval before code
- **Status**: Done 2026-11-22 on `feature/workbench-agent-repos`, except REQ-WM-041/085 (⌘P search on a root) and REQ-WM-021 (pinning the root view).

Go code goes through the `dsh-coder` implementer, one task per run. Web code (`web/ui`) is written in the main session.

## Coverage

| Requirements | Tasks | Verification |
|---|---|---|
| REQ-WM-001, 002, 003 | T-001 | `go test ./internal/workbench/gitinfo/` |
| REQ-WM-080…083, 085, 086 | T-001b | `go test -race ./internal/workbench/gitinfo/ -run Watcher`, budget test |
| REQ-WM-084 | T-005 | vitest (virtual rows, no re-render on 304), scroll check on 2,000 rows |
| REQ-WM-004, 005, 006 | T-002 | `go test ./web/ -run WorkbenchFiles` |
| design | T-003 | owner approval |
| REQ-WM-010, 011, 020, 021 | T-004 | vitest, preview screenshots |
| REQ-WM-030…036 | T-005 | vitest, preview screenshots |
| REQ-WM-040…042, 050 | T-006 | vitest, preview screenshots |
| REQ-WM-060 | T-007 | phone preview screenshots |
| REQ-WM-070, 071 | T-001…T-007, T-008 | `go test ./...`, `npm test`, live check on `~/coding/sxored` |

## Tasks

- [x] `T-001` gitinfo: root detection, resolve field, root summary with cache and ETag
- [x] `T-001b` Root watcher: shared per root, activity schedule, HEAD/index stat trigger, global limit of 4 git runs, slow mark, singleflight, subscriptions, `git.root` event, budget tests (REQ-WM-080…086)
- [x] `T-002` Daemon routes: root summary, subscribe, folder listing and file read outside a repo
- [x] `T-003` FigCraft design: chip, picker, grouped Changes, root Files, phone sheet
  - Evidence: 2026-11-22, drawn live in FigCraft (5 frames); waiting for owner approval
- [x] `T-004` Web: root chip, badge, picker narrowing and pin
- [x] `T-005` Web: grouped Changes, clean repos row, scope on a root, polling
- [x] `T-006` Web: root Files tree, ⌘P on a root, ⌘+click, Worktrees on a root
- [x] `T-007` Web: phone sheet
- [x] `T-008` Live check
  - Evidence: 2026-11-22 scratch daemon ~/.lana-ar :18488: resolve returns roots for sxored (27), rdms (4), sxored-hub (2); cold summary of sxored ~0.19 s; grouped Changes, root Files, picker and agent-here checked in headless Chrome. Phone sheet not checked live. on `~/coding/sxored` (27 repos) and `~/coding/infomedia/rdms` with a scratch daemon
