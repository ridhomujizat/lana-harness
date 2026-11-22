# Tasks: Workbench panel for a folder of several repos

- **Requirements**: [requirements.md](requirements.md) (draft r1)
- **Contract**: [api-contract.md](api-contract.md) (draft r1)
- **Depends on**: spec 66 (merged into `development`); spec 69 for the report parts (REQ-WM-010 report chip, 030 order, 035)
- **Design**: to do — FigCraft page "Workbench · Multi-repo root" (pane chip, picker, grouped Changes, root Files, phone sheet), owner approval before code
- **Status**: Not started. Implementation later (owner, 2026-11-22).

Go code goes through the `dsh-coder` implementer, one task per run. Web code (`web/ui`) is written in the main session.

## Coverage

| Requirements | Tasks | Verification |
|---|---|---|
| REQ-WM-001, 002, 003 | T-001 | `go test ./internal/workbench/gitinfo/` |
| REQ-WM-004, 005, 006 | T-002 | `go test ./web/ -run WorkbenchFiles` |
| design | T-003 | owner approval |
| REQ-WM-010, 011, 020, 021 | T-004 | vitest, preview screenshots |
| REQ-WM-030…036 | T-005 | vitest, preview screenshots |
| REQ-WM-040…042, 050 | T-006 | vitest, preview screenshots |
| REQ-WM-060 | T-007 | phone preview screenshots |
| REQ-WM-070, 071 | T-001…T-007, T-008 | `go test ./...`, `npm test`, live check on `~/coding/sxored` |

## Tasks

- [ ] `T-001` gitinfo: root detection, resolve field, root summary with cache and ETag
- [ ] `T-002` Daemon routes: root summary, folder listing and file read outside a repo
- [ ] `T-003` FigCraft design: chip, picker, grouped Changes, root Files, phone sheet
- [ ] `T-004` Web: root chip, badge, picker narrowing and pin
- [ ] `T-005` Web: grouped Changes, clean repos row, scope on a root, polling
- [ ] `T-006` Web: root Files tree, ⌘P on a root, ⌘+click, Worktrees on a root
- [ ] `T-007` Web: phone sheet
- [ ] `T-008` Live check on `~/coding/sxored` (27 repos) and `~/coding/infomedia/rdms` with a scratch daemon
