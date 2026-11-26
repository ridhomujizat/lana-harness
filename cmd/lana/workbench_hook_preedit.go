package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Tunables of `lana workbench hook pre-edit` (spec 69).
const (
	// hookPreEditTimeout bounds the hook's report PUT and DELETE. It is short
	// so the hook returns quickly and never delays the tool call.
	hookPreEditTimeout = 2 * time.Second
)

// hookPreEditInput is the part of the Claude Code PreToolUse hook JSON the
// pre-edit hook reads.
type hookPreEditInput struct {
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
	AgentID string `json:"agent_id"`
}

// hookGitRunner runs a git command for the pre-edit hook and returns its
// stdout; ok is false when git failed. It is a variable so tests can count and
// stub the git runs.
var hookGitRunner = hookGitRun

// hookGitRun runs `git -C dir <args>` without a shell and with a short timeout
// (spec 69).
func hookGitRun(dir string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), hookGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// workbenchPreEdit is the Claude Code PreToolUse hook program that reports the
// worktree an edit lands in: it finds the worktree of the edited path and
// reports it to the daemon, so the chip follows edits the agent makes in
// another worktree of the pane's repo (spec 69). It never blocks or fails the
// tool call: it always exits 0, writes nothing to stdout and swallows errors.
func workbenchPreEdit(in io.Reader) int {
	// A hook must never fail the tool call, so a panic is swallowed too.
	defer func() { _ = recover() }()

	var input hookPreEditInput
	// A hook JSON the hook does not understand is ignored: there is then no
	// target path and the hook exits.
	_ = json.NewDecoder(io.LimitReader(in, hookInputMax)).Decode(&input)

	id, ok := hookInPane()
	if !ok {
		return 0
	}
	if input.AgentID != "" {
		// A subagent never moves the chip (D8).
		return 0
	}
	target := input.ToolInput.FilePath
	if target == "" {
		target = input.ToolInput.NotebookPath
	}
	return workbenchReportEdit(id, input.Cwd, target)
}

// workbenchReportEdit reports the worktree of an edited path for the pane
// session id: the shared part of the Claude Code pre-edit hook and the agy
// post-edit hook (spec 69, spec 77 REQ-WRA-009). cwd is the agent's working
// folder ("" for the process's). It never fails: it always returns 0.
func workbenchReportEdit(id, cwd, target string) int {
	if target == "" {
		return 0
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(cwd, target)
	}
	target = hookResolvedPath(target)
	cwd = hookResolvedPath(cwd)

	home, err := homeDir()
	if err != nil {
		return 0
	}
	stored, ok := workbenchUseSecret(home)
	if !ok {
		// The daemon is not running: nothing can be reported.
		return 0
	}
	statePath := hookPreEditStatePath(home, id)

	state, ok := hookPreEditReadState(statePath)
	if !ok || !hookPreEditStateMatches(state, cwd) {
		state, ok = hookPreEditBuildState(cwd)
		if !ok {
			// The pane's folder is not in a git repo: nothing to follow.
			return 0
		}
		hookPreEditWriteState(statePath, state)
	}

	// The target's nearest existing folder is the only place the fast path and
	// the refresh path need: it is also where git is asked from when the path
	// itself does not exist yet (Write may create new folders).
	dir := hookPreEditNearestDir(target)

	// The stored worktrees only know the roots they held when the state was
	// saved, so a worktree nested below a matched root stays invisible. A few
	// cheap stats of the folders between the matched root and dir tell whether
	// one may have appeared; if so the hook falls through to the git refresh
	// instead of reporting the outer root.
	if found, ok := hookPreEditWorktreeRoot(target, state.Worktrees); ok && !hookPreEditHasNestedGit(found, dir) {
		return hookPreEditApply(stored, id, statePath, &state, found)
	}
	if dir == "" {
		return 0
	}

	// The path is not in a known worktree, or a worktree nested below one
	// appeared since the state was saved: it may be a new worktree of the same
	// repo, or another repo.
	_, commonDir, ok := hookPreEditRepoInfo(dir)
	if !ok || commonDir != state.CommonDir {
		// Not a repo, or another repo: ignored.
		return 0
	}
	roots, ok := hookPreEditWorktrees(dir)
	if !ok {
		return 0
	}
	state.Worktrees = roots
	found, ok := hookPreEditWorktreeRoot(target, roots)
	if !ok {
		return 0
	}
	return hookPreEditApply(stored, id, statePath, &state, found)
}

// hookPreEditApply reports found when it differs from the state, clearing the
// report when found is the pane's own folder, and saves the state only after
// the daemon answered success (spec 69).
func hookPreEditApply(stored daemonSecretFile, id, statePath string, state *hookPreEditState, found string) int {
	switch {
	case found == state.Current:
		return 0
	case state.Current == "" && found == state.HomeRoot:
		return 0
	}
	if found == state.HomeRoot {
		if err := workbenchUseClearReport(stored.Addr, stored.Secret, id, hookPreEditTimeout); err != nil {
			return 0
		}
		state.Current = ""
	} else {
		if _, err := workbenchUseReportTimeout(stored.Addr, stored.Secret, id, found, hookPreEditTimeout); err != nil {
			return 0
		}
		state.Current = found
	}
	hookPreEditWriteState(statePath, *state)
	return 0
}

// hookPreEditBuildState resolves the pane's folder with git: its worktree root
// and the repo's common dir, and every worktree root of the repo. ok is false
// when the folder is not in a git repo.
func hookPreEditBuildState(cwd string) (hookPreEditState, bool) {
	toplevel, commonDir, ok := hookPreEditRepoInfo(cwd)
	if !ok {
		return hookPreEditState{}, false
	}
	roots, ok := hookPreEditWorktrees(cwd)
	if !ok {
		return hookPreEditState{}, false
	}
	if !hookContainsPath(roots, toplevel) {
		roots = append(roots, toplevel)
	}
	return hookPreEditState{CommonDir: commonDir, HomeRoot: toplevel, Worktrees: roots}, true
}

// hookPreEditStateMatches reports whether the pane's folder still is the
// home_root of the state: its root found in the stored worktrees equals
// home_root. A new or a moved folder does not match and rebuilds the state.
func hookPreEditStateMatches(state hookPreEditState, cwd string) bool {
	root, ok := hookPreEditWorktreeRoot(cwd, state.Worktrees)
	return ok && root == state.HomeRoot
}

// hookPreEditRepoInfo runs the rev-parse that gives the worktree root and the
// repo's common dir of dir; the common dir is made absolute against dir and
// both are symlink-resolved, so they compare equal across worktrees.
func hookPreEditRepoInfo(dir string) (toplevel, commonDir string, ok bool) {
	out, ok := hookGitRunner(dir, "rev-parse", "--show-toplevel", "--git-common-dir")
	if !ok {
		return "", "", false
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		return "", "", false
	}
	toplevel = strings.TrimSpace(lines[0])
	commonDir = strings.TrimSpace(lines[1])
	if toplevel == "" || commonDir == "" {
		return "", "", false
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(dir, commonDir)
	}
	return hookResolvedPath(toplevel), hookResolvedPath(filepath.Clean(commonDir)), true
}

// hookPreEditWorktrees runs `git worktree list --porcelain` and returns every
// worktree root, symlink-resolved. ok is false when git failed or there is no
// worktree.
func hookPreEditWorktrees(dir string) ([]string, bool) {
	out, ok := hookGitRunner(dir, "worktree", "list", "--porcelain")
	if !ok {
		return nil, false
	}
	var roots []string
	for _, line := range strings.Split(out, "\n") {
		path, found := strings.CutPrefix(line, "worktree ")
		if !found {
			continue
		}
		if path = strings.TrimSpace(path); path == "" {
			continue
		}
		roots = append(roots, hookResolvedPath(path))
	}
	if len(roots) == 0 {
		return nil, false
	}
	return roots, true
}

// hookPreEditWorktreeRoot returns the longest worktree root that is the path
// itself or a directory prefix of it. Longest matters because the worktrees
// under .claude/worktrees sit inside the main worktree (spec 69).
func hookPreEditWorktreeRoot(path string, roots []string) (string, bool) {
	best := ""
	for _, root := range roots {
		if root == "" {
			continue
		}
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			if len(root) > len(best) {
				best = root
			}
		}
	}
	return best, best != ""
}

// hookPreEditNearestDir is the nearest existing ancestor directory of path; it
// is "" when none exists. Write may create new folders, so the path itself may
// not exist yet.
func hookPreEditNearestDir(path string) string {
	for dir := path; ; {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return hookResolvedPath(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// hookContainsPath reports whether roots holds root.
func hookContainsPath(roots []string, root string) bool {
	for _, r := range roots {
		if r == root {
			return true
		}
	}
	return false
}

// hookResolvedPath is path with its symlinks resolved where it exists (macOS
// /tmp and /var are symlinks). A path that does not exist has its nearest
// existing ancestor resolved and the rest re-attached, so git's resolved paths
// compare equal to the edited path.
func hookResolvedPath(path string) string {
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return resolved
	}
	dir, rest := clean, ""
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return clean
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
	}
}

// hookPreEditHasNestedGit reports whether a folder strictly between root and
// dir (dir itself included, root excluded) holds a `.git` entry, so a worktree
// nested below a matched root reaches the git refresh. dir is the target's
// nearest existing folder and always is root itself or below it; the check
// costs only a few stats and covers a `.git` file or dir alike (spec 69).
func hookPreEditHasNestedGit(root, dir string) bool {
	if root == "" || dir == "" {
		return false
	}
	for dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
	return false
}
