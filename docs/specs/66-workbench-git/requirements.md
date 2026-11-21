# Spec 66: Workbench files, git changes and worktrees

Status: Approved r1, 2026-11-21. Owner: design reviewed in chat, model A chosen, defaults D1–D5 accepted. Design: FigCraft pages "Workbench · Files & Changes" and "Workbench · Worktrees" in `design/lana-design-system.figcraft`, PNGs `design/export/workbench-files-*.png` and `design/export/worktrees-*.png`.

## Problem

Coding agents run in the Workbench, but the owner cannot see what they changed without typing `git status` and `git diff` in a terminal. The Workbench also has no view of a space's files. The owner runs several Claude Code sessions at once, each in its own git worktree, often split inside one tab. Worktrees pile up: on 2026-11-21 the repo had 27, of which 17 were merged into `development` and 2 had lost their folder. They sit next to the repo as `../lana-harness-<feature>` and clutter the parent folder.

## Decisions (owner, 2026-11-21)

1. A right-side panel in the Workbench with the tabs **Files**, **Changes** and **Worktrees**. It replaces the spec 61 preview panel: ⌘+click on a path opens Files with that file selected.
2. One **Changes** button (git-compare icon + count badge) at the right end of the tab strip opens the panel. No second panel icon. The panel slides in from the right, like the sidebar.
3. Pane headers keep their tools (split right, split down, zoom, close). The new controls do not go into pane headers.
4. **Model A: a space is a repo, and a worktree belongs to each session.** A space can hold sessions in different worktrees, also as split panes in one tab. The worktree is derived from the session's working directory.
5. Tab titles do not show the worktree. The worktree shows in the pane header chip, in the panel's worktree picker, in the Agents list and in a tab hover card.
6. New worktrees go into `../<repo>-worktree/<name>`, not next to the repo. The Workbench offers to move the existing sibling worktrees there.
7. On a phone, the Workbench header gets the Changes button. Files and Changes open as a bottom sheet, and a diff opens full screen.

## Defaults (accepted by the owner, 2026-11-21)

- **D1** The panel is read-only: no stage, discard or commit. Agents and terminals still do that.
- **D2** The base branch for "Branch vs base" is `development` when the repo has it, else the remote's default branch. It can be changed per repo.
- **D3** On a phone, the A−/A+ text size buttons move from the header into the ⋯ space menu, to make room for the Changes button.
- **D4** Clean up offers "Delete remote branches on origin", off by default.
- **D5** Claude agent worktrees (`.claude/worktrees/agent-*`) are listed with an "Claude agent" chip, not hidden.

## Requirements

### Daemon: git data (Go)

- **REQ-WG-001** A new package `internal/workbench/gitinfo` runs `git` with `exec.CommandContext`, a 10 s timeout, `GIT_OPTIONAL_LOCKS=0`, `GIT_TERMINAL_PROMPT=0`, `LC_ALL=C`, and never through a shell. Every path it accepts goes through the spec 61 path policy (`internal/workbench/preview`, REQ-WP-003): home and temp folders only, the secret folders and `.env` files refused.
- **REQ-WG-002** Resolve: for a directory, return the worktree root (`git rev-parse --show-toplevel`), the repo id (the absolute `--git-common-dir`), the branch (`git symbolic-ref --short -q HEAD`, else `detached` and the short HEAD), whether it is the main worktree, and the worktree colour (REQ-WG-061). A directory outside any repo returns `{repo: null}`. Results are cached per directory for 5 s.
- **REQ-WG-003** Status of a worktree: `git status --porcelain=v2 -z --branch --untracked-files=all` plus `git diff --numstat -z HEAD` (untracked files count their lines, up to 1 MB). The result has the branch, upstream ahead/behind, and per file the status (`M`, `A`, `D`, `R` with the old path, `U` for untracked, `C` for a conflict), additions, deletions and a binary flag. The response carries an ETag (a hash of the output). The same ETag returns 304.
- **REQ-WG-004** Scopes for status and diff:
  - `uncommitted`: the working tree and index against `HEAD`;
  - `branch`: the working tree against `git merge-base <base> HEAD` (REQ-WG-005), so it includes uncommitted work; it also returns the commits `<merge-base>..HEAD` (short sha, subject, author time);
  - `last_commit`: `HEAD~1..HEAD`;
  - `commit:<sha>`: that commit alone, chosen from the branch scope's commit list.
- **REQ-WG-005** The base branch per repo is D2. It is read from the Workbench git settings (REQ-WG-050), and `origin/<base>` is used when it exists. Comparing never fetches; fetching happens only when creating a worktree (REQ-WG-031).
- **REQ-WG-006** Diff of one file: `git diff --no-color --no-ext-diff -U3` for the scope, or `git diff --no-index /dev/null <file>` for an untracked file. The daemon parses it into hunks: header, old and new start, and lines with kind (`ctx`, `add`, `del`), old and new line numbers and text. A diff over 5,000 lines is cut, with `truncated: true`. A binary file returns `binary: true` and no hunks.
- **REQ-WG-007** File tree: list one folder of a worktree. Folders come first, then files, case-insensitive. Each entry has a name, a kind, its git status (from REQ-WG-003, uncommitted scope), `ignored` (from `git ls-files --others --ignored --exclude-standard --directory`, cached per worktree) and, for folders, `changed` when anything below has a status. `.git` is never listed. A folder with over 2,000 entries returns the first 2,000 and `truncated: true`.
- **REQ-WG-008** File search for the ⌘P filter: `git ls-files -co --exclude-standard` cached per worktree for 10 s. The daemon returns the best 50 fuzzy matches on the path (subsequence match, file name hits ranked first).
- **REQ-WG-009** File content: text files up to 1 MB return their text and the line count. Other kinds (image, pdf, html, markdown, binary, over 1 MB) return the spec 61 preview response (REQ-WP-002), so the viewer reuses that iframe page.
- **REQ-WG-010** Worktree list for a repo: `git worktree list --porcelain`. For each worktree: path, branch or detached sha, HEAD time, `main`, `locked`, `prunable` (folder missing), dirty count, ahead count against the base, `merged` (the branch is an ancestor of the base: `git branch --merged <base>`, run once per list), `agent` (the path is under `.claude/worktrees/`), and `location`: `main`, `container` (under the repo's worktree folder, REQ-WG-030), `sibling` (next to the repo) or `other`. Status per worktree runs at most 4 at a time and is cached for 30 s. A worktree whose status fails shows `error` and stays in the list.

### Daemon: HTTP (Go, `web` package)

- **REQ-WG-020** New authenticated routes under `/v1/workbench/git/…`, listed in [api-contract.md](api-contract.md). They return 503 `workbench_unavailable` like the other Workbench routes when the Workbench is off. Errors use the daemon envelope. A path outside the policy is 403 `forbidden_path`. A path that is not in a git repo is 404 `not_a_repo`.
- **REQ-WG-021** The client refreshes status by polling every 3 s while the panel is open and the page is visible, using the ETag. It also refreshes when a session in that worktree goes from working to idle, and on the panel's refresh button. There is no new core event and no core protocol change.

### Daemon: worktree actions (Go)

- **REQ-WG-030** The worktree folder of a repo is `<parent of the main worktree>/<main worktree name>-worktree`, for example `~/coding/project/self-tool/lana-harness-worktree`. It can be changed per repo (REQ-WG-050). The folder name for a branch is the branch without its type prefix (`feature/router` → `router`). When that folder exists, the name is the whole branch with `/` replaced by `-` (`fix-router`). When that exists too, a number is appended.
- **REQ-WG-031** Create: body `{repo, branch, new_branch, base, fetch, path, copy[]}`. With `fetch`, the daemon runs `git fetch origin` first and reports a failure without creating anything. Then `git worktree add [-b <branch>] <path> <base or branch>`. Then it copies each `copy` file from the main worktree to the same relative path. The response is the new worktree (REQ-WG-010). A branch that already exists with `new_branch: true` is 409 `branch_exists`. A path that exists is 409 `path_exists`.
- **REQ-WG-032** Copy candidates (`GET …/worktrees/copy-candidates`): files in the main worktree that are git-ignored, up to 2 levels deep and 1 MB each, skipping `node_modules`, `dist`, `build`, `.venv` and `vendor` folders. Checked by default: `.claude/*.local` and `.env`, `.env.*`. The daemon never returns file contents, only paths and sizes. Copying `.env` files is allowed because the daemon copies them on disk and nothing is shown.
- **REQ-WG-033** Remove: body `{path, force, delete_branch, confirm}`. The daemon re-checks before acting:
  - a live Workbench session whose `cwd` or `dir` is inside the worktree → 409 `in_use` with the session ids;
  - a dirty worktree, or commits not on any remote (`git rev-list --count <branch> --not --remotes`), without `force` → 409 `has_work` with the counts;
  - `force` requires `confirm` equal to the worktree folder name, else 400 `confirm_mismatch`;
  - the main worktree → 400 `main_worktree`.

  Then `git worktree remove [--force] <path>`, and with `delete_branch`, `git branch -d <branch>` (`-D` only with `force`).
- **REQ-WG-034** Clean up: body `{paths[], delete_branch, delete_remote}`. It removes each path as in REQ-WG-033 without `force`, and with `delete_remote`, `git push origin --delete <branch>`. It returns one result per path (`removed`, or `skipped` with the reason). One failure does not stop the others.
- **REQ-WG-035** Move: body `{moves: [{path, to}]}`. It runs `git worktree move <path> <to>` per item, skipping `in_use` and `locked` worktrees with a reason. It returns one result per item.
- **REQ-WG-036** Prune (`git worktree prune`) and lock/unlock (`git worktree lock|unlock`) are separate actions. Prune only removes entries whose folder is missing.
- **REQ-WG-037** Every action is logged at info level with the repo, the action and the paths.

### Settings

- **REQ-WG-050** Workbench git settings are kept in `<LANA_HOME>/workbench-git.json`, keyed by repo id: `{base, worktree_root, warn_shared}`. `GET` and `PUT /v1/workbench/git/settings?repo=` read and write one repo's entry. Missing values use the defaults (D2, REQ-WG-030, `true`).

### Web: panel and toggle (TypeScript)

- **REQ-WG-060** The tab strip's right end, after Accounts and Attached, has one Changes button: git-compare icon and a badge with the focused worktree's changed file count (hidden at 0). Click toggles the panel. ⇧⌘E opens Files, ⇧⌘G opens Changes, and Esc closes the panel when focus is not in a terminal. The tooltip is "Files & changes ⇧⌘E" and the count with the branch.
- **REQ-WG-061** Worktree colour: the daemon computes it as FNV-1a of the worktree path modulo the seven `SPACE_COLORS` values (`web/ui/src/views/workbench-model.ts`, copied into Go), so every client shows the same colour.
- **REQ-WG-062** The panel takes the spec 61 preview slot: right of the panes, a draggable left edge, width kept per browser (the spec 61 key `lana.workbench.previewWidth`), and it slides in and out in 220 ms with the terminal refit after the slide (`app/layout-motion.ts`). Its header has the segmented tabs Files, Changes (count) and Worktrees, the worktree picker (REQ-WG-070), refresh, maximize (the panel fills the workspace, like pane zoom) and close. Open/closed, the tab and the width are kept per browser, not in the shared layout document.
- **REQ-WG-062a** Opening the panel on a desktop width slides the Spaces/Agents sidebar closed to give the terminals its room, and closing the panel brings it back (owner, 2026-11-21). A sidebar toggled by hand while the panel is open stays as the owner left it. The automatic hide is not saved as the owner's sidebar preference. A page loaded with the panel open hides the sidebar the same way.
- **REQ-WG-062b** Focus mode (owner, 2026-11-21), on by default and switched by a button in the panel header: while the panel is open, the active tab shows only its focused pane next to the panel. The pane header gets a `‹ n/N ›` switcher to step through the tab's other panes (the panel follows, REQ-WG-072). It is a local view kept per browser: the shared layout and other clients do not change, and the hidden terminals stay alive.
- **REQ-WG-063** ⌘+click on a path in a terminal opens the panel on Files, switched to that pane's worktree, with the file selected and scrolled to its `:line`. A path outside the worktree opens the spec 61 preview inside the viewer area.

### Web: Files

- **REQ-WG-064** The list column (268 px) has the filter (⌘P, REQ-WG-008) and the lazy tree (REQ-WG-007). Rows: chevron, folder or file icon, name, and on the right a status letter (`M` amber, `U`/`A` green, `D` red, `R` blue) or a dot on changed folders. Ignored files are dimmed. Folders with a single child folder collapse into one row (`web/ui/src/views`).
- **REQ-WG-065** The viewer shows the file read-only with line numbers, light syntax colouring and a git gutter: a green bar for added lines, amber for changed, a red mark where lines were removed (from the uncommitted diff). The header has the name, folder, status, a File/Diff toggle for a changed file, and ⋯ (copy path, reveal in Finder for a local daemon, open in the spec 61 preview). The footer shows the language, line count, size and "+n −m since HEAD". Other kinds use REQ-WG-009.

### Web: Changes

- **REQ-WG-066** The scope picker offers Uncommitted, Branch vs `<base>` and Last commit, each with its file count, and at the bottom "Base branch: `<base>` ›" (REQ-WG-050). In the branch scope, the list starts with the commits; clicking one filters the files and the diff to that commit.
- **REQ-WG-067** Rows show the status letter, the file name, its folder, and `+n −m`. A deleted file is struck through. The summary line shows the file count and total `+/−`.
- **REQ-WG-068** The diff viewer shows hunks with old and new line numbers, `+`/`−` signs, green and red row backgrounds, hunk headers, Unified or Split (kept per browser), next and previous hunk (↑/↓ buttons, `]`/`[` keys), and "Open file" to switch to Files. A truncated or binary diff says so.

### Web: worktree per session (model A)

- **REQ-WG-070** The client resolves each session's `cwd` (else `dir`) with REQ-WG-002, batched and debounced 300 ms, again when the `cwd` changes. Sessions with the same repo id are one repo.
- **REQ-WG-071** Pane header: after the title, a chip with the worktree colour dot and the short branch (branch without the type prefix). A pane narrower than 500 px shows the program tile, the chip and a state dot, and hides the title and the state chip. The pane border keeps its normal focus style; it does not take the worktree colour (owner, 2026-11-21). Tab titles do not change.
- **REQ-WG-072** The panel follows the focused pane's worktree. The worktree picker lists "In this space" (worktrees used by this space's sessions, with the tabs and panes that use them and their change counts), then "Other worktrees" with search. "Pin panel to this worktree" stops it following focus until unpinned. "Manage worktrees…" opens the Worktrees tab.
- **REQ-WG-073** Hovering a tab whose panes use more than one worktree shows a card: "N panes · M worktrees", then per worktree its dot, short branch, and the programs with their state.
- **REQ-WG-074** The Agents list shows each agent's worktree dot and short branch under its name. The space row stays one line: the worktree dots sit before the tab count, and "N worktrees · M terminals" is its tooltip (owner, 2026-11-21).
- **REQ-WG-075** When two sessions with a working agent (`agent` set) share one worktree, both pane chips turn amber with "shared with Tab n · <program>". The chip opens: "Move this agent to a new worktree…" (the New worktree dialog, then a new tab there), "Go to the other pane", "Don't warn for this worktree" (REQ-WG-050 `warn_shared`).
- **REQ-WG-076** The new-tab `+` button gets a menu: New terminal (⌘T), Claude Code here (in the focused worktree), Claude Code in worktree ▸ (worktrees in this space, then one more and "All N worktrees…"), New worktree… (⇧⌘N).

- **REQ-WG-077** Pane sizes are draggable (owner, 2026-11-21). The gap between two panes of a split is a 10 px handle: dragging it moves that border, with each pane kept at least 160 px; double-click resets the split to equal shares. The shares are stored on the split node of the shared layout (`sizes`, summing to 1; absent = equal), saved once on release, kept in proportion when a pane closes, and halved for a new pane split from an existing one. Terminals hold their size during the drag and refit once at the end (`holdLayout`).

### Web: Worktrees tab and dialogs

- **REQ-WG-080** The Worktrees tab lists the repo's worktrees (REQ-WG-010) with the filter, the segments All, Active, Merged and Prunable with counts, and New worktree. Groups: In this space, Active, Merged into `<base>` (checkboxes, Select all, the first rows and "Show n more"), and Prunable (Prune all). Rows show the colour dot or a folder icon, the folder name, chips (main checkout, n changed, ↑n, merged, Claude agent, folder missing, n tabs/panes), the branch and the last commit age. On hover: Claude Code here, Open in new tab, ⋯ (Reveal in Finder, Copy path, Lock/Unlock, Remove…).
- **REQ-WG-081** Banners: "N worktrees are merged into `<base>` and have no local changes" with Clean up N…, and "N worktrees sit next to the repo. Move them into ../`<repo>`-worktree/" with Move N…. A banner hides when its count is 0.
- **REQ-WG-082** New worktree dialog: New branch / Existing branch, branch name (with the hint "feature/, fix/, perf/, chore/"), base (default D2, from `origin`), "git fetch origin" (on), folder (REQ-WG-030, editable), copy candidates (REQ-WG-032), "Start Claude Code (or another program, or nothing) in New tab in `<space>` | New space", and a preview of the git commands. Create runs REQ-WG-031 and then starts the session.
- **REQ-WG-083** Clean up dialog: the merged, clean worktrees, all checked except those in use (warned and unchecked) and those outside the repo's folders (warned, checked). Options: Delete local branches (`git branch -d`, on) and Delete remote branches on origin (D4). The button says how many it removes. Results show per row.
- **REQ-WG-084** Remove dialog for a worktree with work: what would be lost (uncommitted count, unpushed commits with the newest sha and age), the safe actions first (Review changes, Push branch, which opens a terminal in that worktree with `git push -u origin <branch>` typed but not run), then typing the folder name to enable Remove anyway, and "Also delete branch".
- **REQ-WG-085** Move dialog: each sibling worktree with its target name (REQ-WG-030), in-use ones skipped with the reason, "Remove the N merged ones instead of moving them" (on) and "New worktrees go into `<repo>`-worktree/ from now on" (on, REQ-WG-050). The button sums up ("Move 2, remove 13").

### Phone

- **REQ-WG-090** The phone Workbench header gets the Changes button with the badge, before New terminal. A−/A+ move into the ⋯ space menu as a "Text size" stepper (D3), which also gets "Files".
- **REQ-WG-091** The panel is a bottom sheet with Files, Changes and Worktrees, closed by swiping down, tapping outside or ×. A file opens full screen: back to the list, the file name and folder, `n/N` with ↑/↓ for the previous and next file, Diff/File, and a unified diff only.

## Tests

- **REQ-WG-100** Go, `internal/workbench/gitinfo`, against temporary repos built in the test: resolve (main and linked worktree, not a repo, detached HEAD), status parsing (all letters, rename, binary, untracked line count), every scope, the diff parser (hunks, no newline at end, truncation), the tree with ignored files and changed folders, the search ranking, the worktree list fields (merged, prunable, agent, location), the folder naming with clashes, and the colour hash.
- **REQ-WG-101** Go, `web`: each route's happy path and errors (403 path policy, 404 not a repo, 409 `in_use`, `has_work`, `branch_exists`, `path_exists`, 400 `confirm_mismatch`, `main_worktree`), the ETag 304, and that create with `fetch` and an unreachable remote creates nothing.
- **REQ-WG-102** Web, vitest: the short branch, the follow-focus and pin logic, the shared-agent detection, the tab hover grouping, and the diff row model (unified and split). `tsc` and the web build pass.

## Out of scope

Staging, discarding and committing (D1). Editing files. Merge conflict resolution. Remotes other than `origin`. Submodules (a worktree with submodules is listed, but move is refused by git and reported). Showing other repos' worktrees in one list.
