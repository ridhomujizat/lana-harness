package gitinfo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// fetchTimeout is how long `git fetch origin` (REQ-WG-031) and the remote
// branch delete of REQ-WG-034 may run: a network call needs more than the 10 s
// the runner allows a local git command.
const fetchTimeout = 60 * time.Second

// Sentinel errors of the worktree actions, so the HTTP layer maps them to the
// codes of the contract without reading a message (api-contract).
var (
	// ErrBranchExists is a new branch that already exists (409 branch_exists).
	ErrBranchExists = errors.New("gitinfo: branch exists")
	// ErrPathExists is a worktree path already on disk (409 path_exists).
	ErrPathExists = errors.New("gitinfo: path exists")
	// ErrMainWorktree is the main worktree, which cannot be removed
	// (400 main_worktree).
	ErrMainWorktree = errors.New("gitinfo: main worktree")
	// ErrConfirmMismatch is a forced removal whose confirm does not name the
	// worktree folder (400 confirm_mismatch).
	ErrConfirmMismatch = errors.New("gitinfo: confirm mismatch")
)

// InUseError is a worktree a live Workbench session is inside (409 in_use). It
// carries the session ids so the client can name them.
type InUseError struct {
	Sessions []string
}

// Error renders the failure and the session ids.
func (e *InUseError) Error() string {
	return "gitinfo: in_use: " + strings.Join(e.Sessions, ",")
}

// HasWorkError is a worktree with work not on any remote that a removal without
// force refuses (409 has_work): Dirty changed entries and Unpushed commits.
type HasWorkError struct {
	Dirty    int
	Unpushed int
}

// Error renders the failure and the two counts.
func (e *HasWorkError) Error() string {
	return fmt.Sprintf("gitinfo: has_work: dirty %d, unpushed %d", e.Dirty, e.Unpushed)
}

// FetchError is a failed `git fetch origin` that stopped a create before
// anything was made (500 fetch_failed).
type FetchError struct {
	Message string
}

// Error renders the failure and git's first stderr line.
func (e *FetchError) Error() string {
	if e.Message == "" {
		return "gitinfo: fetch_failed"
	}
	return "gitinfo: fetch_failed: " + e.Message
}

// The result values of an item action (api-contract).
const (
	resultRemoved = "removed"
	resultMoved   = "moved"
	resultSkipped = "skipped"
	resultFailed  = "failed"
)

// The reasons of a skipped item (api-contract).
const (
	reasonInUse        = "in_use"
	reasonHasWork      = "has_work"
	reasonMainWorktree = "main_worktree"
	reasonLocked       = "locked"
)

// copyDepth is how deep CopyCandidates looks for ignored files (REQ-WG-032):
// the root and one folder level, so a path has at most two segments.
const copyDepth = 2

// copyMaxSize is the largest file CopyCandidates offers (REQ-WG-032).
const copyMaxSize = 1 << 20

// copySkipFolders are folder names CopyCandidates never descends into
// (REQ-WG-032).
var copySkipFolders = map[string]bool{
	"node_modules": true,
	"dist":         true,
	"build":        true,
	".venv":        true,
	"vendor":       true,
	".git":         true,
}

// CreateInput is the body of the create action (REQ-WG-031). Repo is any path
// inside the repository. NewBranch makes a branch from Base; an existing branch
// is checked out instead. With Fetch the main worktree fetches origin first.
// Path is the folder of the new worktree and each Copy entry is a
// worktree-relative path copied from the main worktree.
type CreateInput struct {
	Repo      string
	Branch    string
	NewBranch bool
	Base      string
	Fetch     bool
	Path      string
	Copy      []string
}

// CopyCandidate is one file the new-worktree dialog offers to copy
// (REQ-WG-032): its worktree-relative Path, its Size and whether it is checked
// by Default.
type CopyCandidate struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Default bool   `json:"default"`
}

// RemoveInput is the body of the remove action (REQ-WG-033). Sessions are the
// ids of live Workbench sessions inside the worktree, computed by the HTTP
// layer. Force needs Confirm to equal the worktree folder name. DeleteBranch
// also deletes the branch.
type RemoveInput struct {
	Path         string
	Force        bool
	Confirm      string
	DeleteBranch bool
	Sessions     []string
}

// RemoveResult is the answer of the remove action (REQ-WG-033).
type RemoveResult struct {
	Removed       bool `json:"removed"`
	BranchDeleted bool `json:"branch_deleted"`
}

// CleanupInput is the body of the clean up action (REQ-WG-034). DeleteRemote
// deletes the branch on origin after a removal. InUse answers the live session
// ids inside a worktree.
type CleanupInput struct {
	Paths        []string
	DeleteBranch bool
	DeleteRemote bool
	InUse        func(path string) []string
}

// ItemResult is one result of the clean up and move actions (REQ-WG-034,
// REQ-WG-035): Result is "removed", "moved", "skipped" or "failed", with a
// Reason for the last two.
type ItemResult struct {
	Path   string `json:"path"`
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
}

// MoveItem is one move of the move action (REQ-WG-035).
type MoveItem struct {
	Path string
	To   string
}

// CreateWorktree makes one worktree of a repository (REQ-WG-031). Repo is any
// path inside the repository and the object is made from its main worktree. A
// new branch that exists is ErrBranchExists, an existing branch git does not
// know is ErrNotFound and a path already on disk is ErrPathExists. With Fetch
// the main worktree fetches origin first and a failure is a *FetchError with
// nothing created. Each Copy entry is then copied from the main worktree to the
// same relative path, keeping its mode. The object is returned as Worktrees
// lists it afterwards.
func (r *Runner) CreateWorktree(ctx context.Context, in CreateInput) (Worktree, error) {
	info, mainPath, err := r.mainWorktree(ctx, in.Repo)
	if err != nil {
		return Worktree{}, err
	}
	if err := r.CheckPath(resolvedLeaf(in.Path)); err != nil {
		return Worktree{}, err
	}

	if _, err := r.Git(ctx, mainPath, "check-ref-format", "--branch", in.Branch); err != nil {
		return Worktree{}, err
	}

	local := r.refExists(ctx, mainPath, "refs/heads/"+in.Branch)
	switch {
	case in.NewBranch && local:
		return Worktree{}, ErrBranchExists
	case !in.NewBranch && !local && !r.refExists(ctx, mainPath, "refs/remotes/origin/"+in.Branch):
		return Worktree{}, ErrNotFound
	}

	path := filepath.Clean(in.Path)
	if exists(path) {
		return Worktree{}, ErrPathExists
	}

	if in.Fetch {
		if _, err := r.gitTimeout(ctx, mainPath, fetchTimeout, "fetch", "origin"); err != nil {
			return Worktree{}, &FetchError{Message: gitMessage(err)}
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Worktree{}, err
	}

	base := in.Base
	if base == "" {
		base = "HEAD"
	}
	var addArgs []string
	switch {
	case in.NewBranch:
		addArgs = []string{"worktree", "add", "-b", in.Branch, path, base}
	case local:
		addArgs = []string{"worktree", "add", path, in.Branch}
	default:
		addArgs = []string{"worktree", "add", "--track", "-b", in.Branch, path, "origin/" + in.Branch}
	}
	if _, err := r.Git(ctx, mainPath, addArgs...); err != nil {
		return Worktree{}, err
	}

	for _, entry := range in.Copy {
		if err := copyEntry(mainPath, path, entry); err != nil {
			return Worktree{}, err
		}
	}

	r.InvalidateWorktrees(info.Repo)
	list, err := r.Worktrees(ctx, path, base, "")
	if err != nil {
		return Worktree{}, err
	}
	want := filepath.Clean(path)
	for _, wt := range list.Worktrees {
		if wt.Path == want {
			return wt, nil
		}
	}
	return Worktree{}, ErrNotFound
}

// CopyCandidates lists the files of a repository the new-worktree dialog offers
// to copy (REQ-WG-032): the git-ignored regular files of the main worktree up
// to two path segments deep and 1 MB each. copySkipFolders are never descended
// into and no file content is ever read. Default is true for ".claude/*.local",
// ".env" and ".env.*". The list is sorted by path and never nil.
func (r *Runner) CopyCandidates(ctx context.Context, repoDir string) ([]CopyCandidate, error) {
	_, mainPath, err := r.mainWorktree(ctx, repoDir)
	if err != nil {
		return nil, err
	}
	out, err := r.Git(ctx, mainPath, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	candidates := []CopyCandidate{}
	for _, rel := range splitNUL(out) {
		parts := strings.Split(rel, "/")
		if len(parts) > copyDepth || copySkipFolders[parts[0]] {
			continue
		}
		stat, err := os.Lstat(filepath.Join(mainPath, filepath.FromSlash(rel)))
		if err != nil || !stat.Mode().IsRegular() || stat.Size() > copyMaxSize {
			continue
		}
		candidates = append(candidates, CopyCandidate{Path: rel, Size: stat.Size(), Default: defaultCopy(rel)})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	return candidates, nil
}

// SuggestPath is the folder a new worktree of branch would get (REQ-WG-030):
// FolderName under root, or under the repository's worktree root when root is
// empty.
func (r *Runner) SuggestPath(ctx context.Context, repoDir, branch, root string) (string, error) {
	_, mainPath, err := r.mainWorktree(ctx, repoDir)
	if err != nil {
		return "", err
	}
	if root == "" {
		root = WorktreeRoot(mainPath)
	} else {
		root = filepath.Clean(root)
	}
	return FolderName(root, branch), nil
}

// RemoveWorktree removes one worktree (REQ-WG-033). Sessions are the ids of
// live Workbench sessions inside it; a non-empty list is *InUseError. The main
// worktree is ErrMainWorktree. Without Force a worktree with changes or with
// commits not on any remote is *HasWorkError; with Force Confirm must equal the
// worktree folder name, else ErrConfirmMismatch. With DeleteBranch the branch is
// deleted too, and a delete git refuses keeps Removed true and BranchDeleted
// false.
func (r *Runner) RemoveWorktree(ctx context.Context, in RemoveInput) (RemoveResult, error) {
	info, mainPath, err := r.mainWorktree(ctx, in.Path)
	if err != nil {
		return RemoveResult{}, err
	}
	if len(in.Sessions) > 0 {
		return RemoveResult{}, &InUseError{Sessions: append([]string(nil), in.Sessions...)}
	}
	if info.Main {
		return RemoveResult{}, ErrMainWorktree
	}
	if in.Force {
		if in.Confirm != filepath.Base(filepath.Clean(in.Path)) {
			return RemoveResult{}, ErrConfirmMismatch
		}
	} else {
		dirty, unpushed, err := r.workCounts(ctx, info, mainPath)
		if err != nil {
			return RemoveResult{}, err
		}
		if dirty > 0 || unpushed > 0 {
			return RemoveResult{}, &HasWorkError{Dirty: dirty, Unpushed: unpushed}
		}
	}

	path := filepath.Clean(in.Path)
	args := []string{"worktree", "remove"}
	if in.Force {
		args = append(args, "--force")
	}
	args = append(args, path)
	if _, err := r.Git(ctx, mainPath, args...); err != nil {
		return RemoveResult{}, err
	}

	result := RemoveResult{Removed: true}
	if in.DeleteBranch && info.Branch != "" && info.Branch != "detached" {
		branchArgs := []string{"branch", "-d", info.Branch}
		if in.Force {
			branchArgs = []string{"branch", "-D", info.Branch}
		}
		if _, err := r.Git(ctx, mainPath, branchArgs...); err == nil {
			result.BranchDeleted = true
		}
	}
	r.InvalidateWorktrees(info.Repo)
	return result, nil
}

// Cleanup removes a list of worktrees without force (REQ-WG-034). Each path
// becomes one ItemResult: "removed", "skipped" with reason "in_use",
// "has_work" or "main_worktree", or "failed" with the git message. With
// DeleteRemote the branch of a removed worktree is deleted on origin and a
// failure is kept in Reason as "remote: ..." while the result stays "removed".
// One failure never stops the others.
func (r *Runner) Cleanup(ctx context.Context, in CleanupInput) []ItemResult {
	results := make([]ItemResult, 0, len(in.Paths))
	for _, path := range in.Paths {
		results = append(results, r.cleanupOne(ctx, in, path))
	}
	return results
}

// cleanupOne removes one worktree of a cleanup and, with DeleteRemote, its
// branch on origin. The worktree's branch and the main worktree are read before
// the removal, since the folder is gone afterwards.
func (r *Runner) cleanupOne(ctx context.Context, in CleanupInput, path string) ItemResult {
	var sessions []string
	if in.InUse != nil {
		sessions = in.InUse(path)
	}

	var branch, mainPath string
	if info, err := r.Resolve(ctx, path); err == nil {
		branch = info.Branch
		if main, _, err := r.readWorktrees(ctx, path); err == nil {
			mainPath = main
		}
		if mainPath == "" {
			mainPath = info.Worktree
		}
	}

	_, err := r.RemoveWorktree(ctx, RemoveInput{Path: path, DeleteBranch: in.DeleteBranch, Sessions: sessions})
	if err != nil {
		return cleanupFailure(path, err)
	}
	item := ItemResult{Path: path, Result: resultRemoved}
	if in.DeleteRemote && branch != "" && branch != "detached" && mainPath != "" {
		if _, err := r.gitTimeout(ctx, mainPath, fetchTimeout, "push", "origin", "--delete", branch); err != nil {
			item.Reason = "remote: " + gitMessage(err)
		}
	}
	return item
}

// cleanupFailure maps a failed cleanup removal to a skipped item for the known
// reasons and to a failed item otherwise.
func cleanupFailure(path string, err error) ItemResult {
	var inUse *InUseError
	var hasWork *HasWorkError
	switch {
	case errors.As(err, &inUse):
		return ItemResult{Path: path, Result: resultSkipped, Reason: reasonInUse}
	case errors.As(err, &hasWork):
		return ItemResult{Path: path, Result: resultSkipped, Reason: reasonHasWork}
	case errors.Is(err, ErrMainWorktree):
		return ItemResult{Path: path, Result: resultSkipped, Reason: reasonMainWorktree}
	default:
		return ItemResult{Path: path, Result: resultFailed, Reason: gitMessage(err)}
	}
}

// Move moves worktrees with `git worktree move` (REQ-WG-035). A worktree a
// session is inside, or one that is locked, is skipped with reason "in_use" or
// "locked"; the target's parent folder is created first and the move is
// reported "moved" or "failed" with the git message. One failure never stops
// the others.
func (r *Runner) Move(ctx context.Context, moves []MoveItem, inUse func(string) []string) []ItemResult {
	results := make([]ItemResult, 0, len(moves))
	for _, m := range moves {
		results = append(results, r.moveOne(ctx, m, inUse))
	}
	return results
}

// moveOne moves one worktree of a Move.
func (r *Runner) moveOne(ctx context.Context, m MoveItem, inUse func(string) []string) ItemResult {
	path := filepath.Clean(m.Path)
	to := filepath.Clean(m.To)

	if inUse != nil && len(inUse(path)) > 0 {
		return ItemResult{Path: path, Result: resultSkipped, Reason: reasonInUse}
	}
	info, mainPath, err := r.mainWorktree(ctx, path)
	if err != nil {
		return ItemResult{Path: path, Result: resultFailed, Reason: gitMessage(err)}
	}
	if err := r.CheckPath(resolvedLeaf(to)); err != nil {
		return ItemResult{Path: path, Result: resultFailed, Reason: gitMessage(err)}
	}

	locked, err := r.worktreeLocked(ctx, path)
	if err != nil {
		return ItemResult{Path: path, Result: resultFailed, Reason: gitMessage(err)}
	}
	if locked {
		return ItemResult{Path: path, Result: resultSkipped, Reason: reasonLocked}
	}

	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return ItemResult{Path: path, Result: resultFailed, Reason: gitMessage(err)}
	}
	if _, err := r.Git(ctx, mainPath, "worktree", "move", path, to); err != nil {
		return ItemResult{Path: path, Result: resultFailed, Reason: gitMessage(err)}
	}
	r.InvalidateWorktrees(info.Repo)
	return ItemResult{Path: path, Result: resultMoved}
}

// Prune drops the worktree entries whose folder is missing and returns the
// paths it removed (REQ-WG-036).
func (r *Runner) Prune(ctx context.Context, repoDir string) ([]string, error) {
	info, mainPath, err := r.mainWorktree(ctx, repoDir)
	if err != nil {
		return nil, err
	}
	_, entries, err := r.readWorktrees(ctx, repoDir)
	if err != nil {
		return nil, err
	}
	pruned := []string{}
	for _, entry := range entries {
		if entry.prunable {
			pruned = append(pruned, filepath.Clean(entry.path))
		}
	}
	if _, err := r.Git(ctx, mainPath, "worktree", "prune"); err != nil {
		return nil, err
	}
	r.InvalidateWorktrees(info.Repo)
	return pruned, nil
}

// Lock locks or unlocks a worktree and returns it as it is listed afterwards
// (REQ-WG-036).
func (r *Runner) Lock(ctx context.Context, path string, locked bool) (Worktree, error) {
	info, mainPath, err := r.mainWorktree(ctx, path)
	if err != nil {
		return Worktree{}, err
	}
	action := "lock"
	if !locked {
		action = "unlock"
	}
	if _, err := r.Git(ctx, mainPath, "worktree", action, path); err != nil {
		return Worktree{}, err
	}
	r.InvalidateWorktrees(info.Repo)
	list, err := r.Worktrees(ctx, path, "", "")
	if err != nil {
		return Worktree{}, err
	}
	want := filepath.Clean(path)
	for _, wt := range list.Worktrees {
		if wt.Path == want {
			return wt, nil
		}
	}
	return Worktree{}, ErrNotFound
}

// mainWorktree resolves repoDir and returns its Info and the path of the main
// worktree of the repository, which every worktree command runs from.
func (r *Runner) mainWorktree(ctx context.Context, repoDir string) (Info, string, error) {
	info, err := r.Resolve(ctx, repoDir)
	if err != nil {
		return Info{}, "", err
	}
	main, _, err := r.readWorktrees(ctx, repoDir)
	if err != nil {
		return Info{}, "", err
	}
	if main == "" {
		main = info.Worktree
	}
	return info, main, nil
}

// refExists reports whether a ref resolves to an object (REQ-WG-031).
func (r *Runner) refExists(ctx context.Context, dir, ref string) bool {
	_, err := r.Git(ctx, dir, "rev-parse", "--verify", "-q", ref)
	return err == nil
}

// worktreeLocked reports whether a worktree is locked, from the repository's
// worktree list (REQ-WG-035).
func (r *Runner) worktreeLocked(ctx context.Context, path string) (bool, error) {
	_, entries, err := r.readWorktrees(ctx, path)
	if err != nil {
		return false, err
	}
	want := filepath.Clean(path)
	for _, entry := range entries {
		if filepath.Clean(entry.path) == want {
			return entry.locked, nil
		}
	}
	return false, nil
}

// workCounts is the changes and the commits not on any remote of a worktree
// (REQ-WG-033): the dirty count always, the unpushed count when the worktree is
// on a branch git knows.
func (r *Runner) workCounts(ctx context.Context, info Info, mainPath string) (dirty, unpushed int, err error) {
	dirty, err = r.dirtyCount(ctx, info.Worktree)
	if err != nil {
		return 0, 0, err
	}
	if info.Branch != "" && info.Branch != "detached" && r.refExists(ctx, mainPath, "refs/heads/"+info.Branch) {
		out, err := r.Git(ctx, mainPath, "rev-list", "--count", info.Branch, "--not", "--remotes")
		if err != nil {
			return 0, 0, err
		}
		unpushed, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
	return dirty, unpushed, nil
}

// defaultCopy reports whether CopyCandidates checks a path by default
// (REQ-WG-032): ".claude/*.local", ".env" and ".env.*".
func defaultCopy(rel string) bool {
	if rel == ".env" || strings.HasPrefix(rel, ".env.") {
		return true
	}
	if rest, ok := strings.CutPrefix(rel, ".claude/"); ok && !strings.Contains(rest, "/") {
		return strings.HasSuffix(rest, ".local")
	}
	return false
}

// copyEntry copies one worktree-relative path from the main worktree into a new
// worktree (REQ-WG-031). An absolute path or one that climbs out is
// ErrForbiddenPath; a file keeps the mode of its source.
func copyEntry(srcRoot, dstRoot, name string) error {
	rel, ok := relativePath(name)
	if !ok {
		return ErrForbiddenPath
	}
	if rel == "." {
		return nil
	}
	src := filepath.Join(srcRoot, rel)
	dst := filepath.Join(dstRoot, rel)
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.MkdirAll(dst, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyFile(src, dst, info.Mode().Perm())
}

// copyFile copies src to dst with mode, creating dst or replacing it
// (REQ-WG-031).
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// gitMessage is the message of a failed git run for a result reason: the first
// stderr line of a *GitError, else the error text (api-contract).
func gitMessage(err error) string {
	var ge *GitError
	if errors.As(err, &ge) && ge.Message != "" {
		return ge.Message
	}
	return firstLine(err.Error())
}
