# Spec 70: Workbench panel for a folder of several repos

Status: Draft r1, 2026-11-22. Not approved yet. Implementation comes later. Extends spec 66 (Workbench files, git changes and worktrees) and works with spec 69 (agents report their worktree). Design: to be drawn in FigCraft before implementation.

## Problem

The owner often starts a session in a project's root folder that is not a git repo itself but holds several repos:

| Root | Repos directly inside | Repos with changes (2026-11-22) |
|---|---|---|
| `~/coding/sxored` | 27 (`sxored-be-*`, `sxored-fe-*`, `sxored-ai`, …) | 4 |
| `~/coding/infomedia/rdms` | 4 (`be-rdms`, `frontend`, `rdms-scanner`, `worker-ai`) | 3 |
| `~/coding/sxored-hub` | 2 (`sxored-be-hub`, `sxored-fe-hub`) | 2 |

These roots also have folders and files outside any repo, such as `docs/`, `CLAUDE.md` and plan files, which the owner needs to read.

Spec 66 resolves such a folder to `{repo: null}`. The pane gets no chip, and the Files and Changes panel has nothing to show. The owner cannot see which repos the agent changed.

## Decisions (owner, 2026-11-22)

1. **Scan the root, not Claude Code's `add-dir`.** A session started in the root can already reach every repo below it. `add-dir` is for the opposite case, and the Workbench cannot read it from outside the Claude process. The scan works for every program: Claude Code, omp, Codex and a plain shell.
2. **Only direct children count as repos.** A folder directly in the root that has a `.git` folder or file is a repo. Deeper repos, such as `rdms/frontend-worktrees/*` or `rdms/inspired/arkon`, are not listed in Changes.
3. **Files shows the whole root,** including folders and files outside any repo, such as `docs/`.
4. **Changes groups the changed files by repo** (view A). Clean repos fold into one "N clean repos" row.
5. **Folders outside git show only in Files.** Changes has no group for them, and there is no diff for them.
6. **A spec 69 report inside the root keeps the root view.** The reported repo moves to the top of Changes, opens, and gets an "agent here" mark. The pane chip reads `sxored › be-ocrai`.

## Requirements

### Daemon (Go)

- **REQ-WM-001** A **root** is a folder that is not inside a git worktree, passes the spec 61 path policy, and has at least one direct child that is a repo: a folder with a `.git` folder or a `.git` file. Only the folder itself is checked; parent folders are not searched, so a pane in `~/coding/sxored/docs` is not a root.
- **REQ-WM-002** Resolve (spec 66 REQ-WG-002) answers a root with `{repo: null, root: {...}}`, where `root` has the root path, its name, and the number of repos. A folder that is neither a repo nor a root still answers `{repo: null}`.
- **REQ-WM-003** Root summary (`GET /v1/workbench/git/root?dir=`): one entry per child repo, sorted by name, with the folder name, the worktree root, the branch and short branch, detached, the colour (REQ-WG-061), the uncommitted changed file count, additions and deletions, ahead/behind its upstream, and `error` when its status failed. It also lists the root's direct child folders and files that are not repos (names only). Status runs at most 4 repos at a time; the summary is cached for 10 s and carries an ETag (a hash of the body), and the same ETag returns 304.
- **REQ-WM-004** Folder listing outside a repo (`GET /v1/workbench/files/tree?dir=`): one folder, folders first then files, case-insensitive, with name, kind, size and modified time, and for each child folder `repo: true` plus its short branch and changed count when it is a repo (one cached status each, REQ-WM-003). It never lists `.git`. Over 2,000 entries returns the first 2,000 and `truncated: true`. The spec 61 path policy applies to every path, so secret folders and `.env` files are refused as in spec 66.
- **REQ-WM-005** File read outside a repo (`GET /v1/workbench/files/file?path=`): text up to 1 MB, or the spec 61 preview for other files, like the spec 66 file route without the git parts.
- **REQ-WM-006** Inside a repo, the existing spec 66 routes (status, diff, tree, search, file) are used as they are, with that repo's worktree.

### Web: pane and tab strip

- **REQ-WM-010** A pane whose worktree (spec 69 REQ-WA-040 order) resolves to a root shows a root chip: a folder-stack icon, the root name and the number of repos with changes (`sxored · 4`). With a spec 69 report on a repo inside the root, the chip reads `sxored › be-ocrai` with that repo's colour dot.
- **REQ-WM-011** The tab strip's Changes badge (REQ-WG-060) shows the total changed file count of all repos in the root when the focused pane is on a root.

### Web: panel header and picker

- **REQ-WM-020** The worktree picker (REQ-WG-072) on a root starts with "All repos in `<root>`", then the changed repos with their counts, then "Clean (N)" folded, with the repo filter. Choosing a repo narrows the panel to that repo with the spec 66 views and a "‹ All repos" link back. The choice is kept per pane in the browser and resets when the pane's root changes.
- **REQ-WM-021** Pin (REQ-WG-072) pins the root view or the narrowed repo, whichever is shown.

### Web: Changes on a root

- **REQ-WM-030** "All repos" Changes lists one group per changed repo: a fold arrow, the colour dot, the repo name, the short branch and the count. Groups are open by default when there are at most 3 changed repos, else folded. Order: the spec 69 reported repo first, then by changed count (high first), then by name.
- **REQ-WM-031** Inside a group, the rows look like the spec 66 file rows (status letter, path relative to the repo, +/−). A group shows its first 10 files and "… n more". Clicking a file opens the spec 66 diff view for that repo and file; ↑/↓ steps through the files of all groups in order.
- **REQ-WM-032** Below the groups, "N clean repos ▸" opens a list of the clean repos with their short branch. Clicking one narrows the panel to it (REQ-WM-020) on Files.
- **REQ-WM-033** The scope picker (REQ-WG-066) on a root offers Uncommitted (default) and Branch vs base. Branch vs base uses each repo's own base (REQ-WG-005). Last commit and single commits are only offered after narrowing to one repo.
- **REQ-WM-034** Refresh: the root summary polls every 10 s while the panel is open and the page is visible, with the ETag. Open groups load their file list with the spec 66 status route and poll it every 3 s (REQ-WG-021). A session going from working to idle refreshes both.
- **REQ-WM-035** The spec 69 reported repo's group has an "agent here" mark and stays open.
- **REQ-WM-036** Folders outside git have no group in Changes (decision 5).

### Web: Files on a root

- **REQ-WM-040** Files shows the root tree from REQ-WM-004. Repo folders carry their colour dot, short branch and a changed dot. Opening a repo folder lists it with the spec 66 tree route, so files inside show their git status; folders outside repos list with REQ-WM-004 and show no status.
- **REQ-WM-041** The ⌘P filter on a root searches the files of the changed repos and the root's non-repo folders, up to 2,000 files outside repos, skipping `node_modules`, `dist`, `build`, `.venv` and `vendor`.
- **REQ-WM-042** ⌘+click on a path in a terminal (REQ-WG-063) whose pane is on a root opens Files on that file, inside a repo or not.

### Web: Worktrees on a root

- **REQ-WM-050** The Worktrees tab on "All repos" asks to choose a repo (the same list as the picker). After narrowing, it is the spec 66 Worktrees tab of that repo.

### Phone

- **REQ-WM-060** The bottom sheet (REQ-WG-091) shows the same root views: grouped Changes, the root tree in Files, and the picker.

### Tests

- **REQ-WM-070** Go, against temporary folders built in the test: root detection (child repo with a `.git` folder, a linked worktree with a `.git` file, no child repos, a folder inside a repo is not a root, path policy), the summary fields and ETag, the order and error rows, the non-repo listing with repo marks and truncation, and the non-repo file read.
- **REQ-WM-071** Web, vitest: group order with and without a report, open-by-default rule, the "… n more" cut, ↑/↓ across groups, the picker narrowing and back, and the chip text. Live check on `~/coding/sxored` with a scratch daemon.

## Out of scope

Repos deeper than one level below the root. Diffs or change lists for folders outside git. Snapshots of non-git folders. Reading Claude Code's `--add-dir` list. Staging, discarding and committing (spec 66 D1). Showing several roots in one panel.
