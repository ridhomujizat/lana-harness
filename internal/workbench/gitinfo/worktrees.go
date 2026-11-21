package gitinfo

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// worktreeStatusConcurrency is how many worktrees have their status checked at
// once (REQ-WG-010).
const worktreeStatusConcurrency = 4

// forbiddenPathError is the Error a listed worktree carries when its folder is
// outside the spec 61 path policy, so the client can tell it apart from a git
// failure (REQ-WG-010).
const forbiddenPathError = "forbidden_path"

// Worktree is one worktree of a repository (REQ-WG-010). Branch is empty for a
// detached worktree; Dirty is the number of changed entries, Ahead the commits
// the branch has over the base, Merged whether the branch is an ancestor of the
// base, Agent whether it is a Claude Code agent worktree (D5) and Location one
// of main, container, sibling or other. Error carries the first line of a
// failed status command, or "forbidden_path" when its folder is refused.
type Worktree struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Branch   string    `json:"branch"`
	Head     string    `json:"head"`
	HeadTime time.Time `json:"head_time"`
	Main     bool      `json:"main"`
	Locked   bool      `json:"locked"`
	Prunable bool      `json:"prunable"`
	Dirty    int       `json:"dirty"`
	Ahead    int       `json:"ahead"`
	Merged   bool      `json:"merged"`
	Agent    bool      `json:"agent"`
	Location string    `json:"location"`
	Color    string    `json:"color"`
	Error    string    `json:"error"`
}

// WorktreeList is the answer of REQ-WG-010. Worktrees is never null.
type WorktreeList struct {
	Repo      string     `json:"repo"`
	Base      string     `json:"base"`
	Root      string     `json:"root"`
	Worktrees []Worktree `json:"worktrees"`
}

// Worktrees lists the worktrees of the repository that holds repoDir
// (REQ-WG-010). repoDir is any path inside the repository; base is the branch
// the ahead and merged counts compare against and root is the folder container
// worktrees sit under ("" uses WorktreeRoot of the main worktree). The per
// worktree status runs at most worktreeStatusConcurrency at a time and the whole
// list is cached per repository, base and root for worktreesTTL. A worktree
// whose folder is outside the path policy stays in the list with
// Error "forbidden_path".
func (r *Runner) Worktrees(ctx context.Context, repoDir, base, root string) (WorktreeList, error) {
	info, err := r.Resolve(ctx, repoDir)
	if err != nil {
		return WorktreeList{}, err
	}
	repo := info.Repo

	key := worktreesKey(repo, base, root)
	if list, ok := r.caches.worktrees.get(key, r.clock()); ok {
		return list, nil
	}

	mainPath, entries, err := r.readWorktrees(ctx, repoDir)
	if err != nil {
		return WorktreeList{}, err
	}
	if mainPath == "" {
		mainPath = filepath.Clean(repoDir)
	}
	if root == "" {
		root = WorktreeRoot(mainPath)
	} else {
		root = filepath.Clean(root)
	}

	ref, resultBase := r.baseRef(ctx, mainPath, base)
	merged := r.mergedBranches(ctx, mainPath, ref)

	list := WorktreeList{Repo: repo, Base: resultBase, Root: root, Worktrees: []Worktree{}}
	var heads []string
	var forbidden []bool
	first := true
	for _, entry := range entries {
		if entry.bare {
			continue
		}
		path := filepath.Clean(entry.path)
		wt := Worktree{
			Path:     path,
			Name:     filepath.Base(path),
			Branch:   entry.branch,
			Head:     shortSHA(entry.head),
			Main:     first,
			Locked:   entry.locked,
			Prunable: entry.prunable,
			Agent:    agentWorktree(path),
			Location: worktreeLocation(path, mainPath, root, first),
			Color:    Color(path),
		}
		if ref != "" && wt.Branch != "" {
			wt.Merged = merged[wt.Branch]
		}
		list.Worktrees = append(list.Worktrees, wt)
		heads = append(heads, entry.head)
		forbidden = append(forbidden, r.CheckPath(path) != nil)
		first = false
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, worktreeStatusConcurrency)
	for i := range list.Worktrees {
		if forbidden[i] {
			list.Worktrees[i].Error = forbiddenPathError
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r.worktreeStatus(ctx, &list.Worktrees[i], mainPath, ref, heads[i])
		}(i)
	}
	wg.Wait()

	r.caches.worktrees.put(key, repo, list, r.clock())
	return list, nil
}

// InvalidateWorktrees drops every cached worktree list of a repository, so the
// next Worktrees call reads git again (REQ-WG-010). repo is the repository id
// Resolve returned; an unknown repo drops nothing and is not an error.
func (r *Runner) InvalidateWorktrees(repo string) {
	r.caches.worktrees.invalidate(repo)
}

// worktreeEntry is one entry of `git worktree list --porcelain -z`.
type worktreeEntry struct {
	path     string
	head     string
	branch   string
	detached bool
	locked   bool
	prunable bool
	bare     bool
}

// readWorktrees runs `git worktree list --porcelain -z` and returns the path of
// the main worktree and every entry (REQ-WG-010). A directory outside any
// repository is ErrNotARepo.
func (r *Runner) readWorktrees(ctx context.Context, dir string) (string, []worktreeEntry, error) {
	out, err := r.Git(ctx, dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		if isNotARepo(err) {
			return "", nil, ErrNotARepo
		}
		return "", nil, err
	}
	entries := parseWorktreeList(out)
	main := ""
	for _, entry := range entries {
		if !entry.bare {
			main = filepath.Clean(entry.path)
			break
		}
	}
	return main, entries, nil
}

// parseWorktreeList reads the NUL-separated records of `git worktree list
// --porcelain -z` (REQ-WG-010). Attributes are NUL-terminated and an empty
// record separates two entries.
func parseWorktreeList(out []byte) []worktreeEntry {
	var (
		entries []worktreeEntry
		cur     worktreeEntry
		started bool
	)
	flush := func() {
		if started {
			entries = append(entries, cur)
			cur = worktreeEntry{}
			started = false
		}
	}
	for _, field := range strings.Split(string(out), "\x00") {
		if field == "" {
			flush()
			continue
		}
		started = true
		switch {
		case strings.HasPrefix(field, "worktree "):
			cur.path = strings.TrimPrefix(field, "worktree ")
		case strings.HasPrefix(field, "HEAD "):
			cur.head = strings.TrimPrefix(field, "HEAD ")
		case strings.HasPrefix(field, "branch "):
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(field, "branch "), "refs/heads/")
		case field == "detached":
			cur.detached = true
		case strings.HasPrefix(field, "locked"):
			cur.locked = true
		case strings.HasPrefix(field, "prunable"):
			cur.prunable = true
		case field == "bare":
			cur.bare = true
		}
	}
	flush()
	return entries
}

// baseRef resolves the base branch of a repository (REQ-WG-005, REQ-WG-010):
// origin/<base> when that remote-tracking ref exists, else <base> when that
// local ref exists, else no ref. It returns the ref to compare against and the
// value for the result's Base field (base as given when no ref resolves). An
// empty base means no base at all.
func (r *Runner) baseRef(ctx context.Context, dir, base string) (ref, result string) {
	if base == "" {
		return "", ""
	}
	if _, err := r.Git(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+base); err == nil {
		return "origin/" + base, "origin/" + base
	}
	if _, err := r.Git(ctx, dir, "rev-parse", "--verify", "--quiet", base); err == nil {
		return base, base
	}
	return "", base
}

// mergedBranches returns the local branches that are ancestors of ref
// (REQ-WG-010), from one `git branch --merged`. No ref, or a failing command,
// gives an empty set.
func (r *Runner) mergedBranches(ctx context.Context, dir, ref string) map[string]bool {
	set := map[string]bool{}
	if ref == "" {
		return set
	}
	out, err := r.Git(ctx, dir, "branch", "--merged", ref, "--format=%(refname:short)")
	if err != nil {
		return set
	}
	for _, line := range splitLines(out) {
		if name := strings.TrimSpace(line); name != "" {
			set[name] = true
		}
	}
	return set
}

// worktreeStatus fills the per worktree status of REQ-WG-010 at most
// worktreeStatusConcurrency at a time: the dirty count and the head time for a
// non-prunable worktree, and the ahead count for one with a branch and a base.
// A failure is kept in Error and the worktree stays as it is.
func (r *Runner) worktreeStatus(ctx context.Context, wt *Worktree, mainPath, ref, head string) {
	if !wt.Prunable {
		if n, err := r.dirtyCount(ctx, wt.Path); err != nil {
			wt.setError(err)
		} else {
			wt.Dirty = n
		}
		if head != "" {
			if t, err := r.headTime(ctx, mainPath, head); err != nil {
				wt.setError(err)
			} else {
				wt.HeadTime = t
			}
		}
	}
	if ref != "" && wt.Branch != "" {
		if n, err := r.aheadCount(ctx, mainPath, ref, wt.Branch); err != nil {
			wt.setError(err)
		} else {
			wt.Ahead = n
		}
	}
}

// setError records the first line of a failed status command, keeping the first
// error when more than one fails.
func (w *Worktree) setError(err error) {
	if w.Error == "" {
		w.Error = firstLine(err.Error())
	}
}

// dirtyCount counts the changed entries of a worktree from
// `git status --porcelain=v1 -z --untracked-files=normal` (REQ-WG-010).
func (r *Runner) dirtyCount(ctx context.Context, dir string) (int, error) {
	out, err := r.Git(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return 0, err
	}
	return countStatusRecords(out), nil
}

// countStatusRecords counts the NUL-separated records of `git status
// --porcelain=v1 -z`. A rename or copy record carries the original path in a
// second NUL record, so it counts as one change.
func countStatusRecords(out []byte) int {
	tokens := splitNUL(out)
	n := 0
	for i := 0; i < len(tokens); i++ {
		handle := tokens[i]
		if handle == "" {
			continue
		}
		n++
		if len(handle) >= 2 && (isRenameLetter(handle[0]) || isRenameLetter(handle[1])) {
			i++ // the original path that follows a rename or copy
		}
	}
	return n
}

// isRenameLetter reports whether a porcelain status column letter means a
// rename or copy, whose record is followed by the original path.
func isRenameLetter(letter byte) bool {
	return letter == 'R' || letter == 'C'
}

// headTime runs `git log -1 --format=%cI <head>` from dir and parses it as
// RFC 3339 (REQ-WG-010).
func (r *Runner) headTime(ctx context.Context, dir, head string) (time.Time, error) {
	out, err := r.Git(ctx, dir, "log", "-1", "--format=%cI", head)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
}

// aheadCount is `git rev-list --count ref..branch` (REQ-WG-010).
func (r *Runner) aheadCount(ctx context.Context, dir, ref, branch string) (int, error) {
	out, err := r.Git(ctx, dir, "rev-list", "--count", ref+".."+branch)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// agentWorktree reports whether a worktree path is a Claude Code agent worktree
// (REQ-WG-010, D5): it sits under a ".claude/worktrees/" folder.
func agentWorktree(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/.claude/worktrees/")
}

// worktreeLocation names where a worktree sits (REQ-WG-010): "main" for the main
// worktree, "container" when it is under the repository's worktree folder,
// "sibling" when it is next to the main worktree, else "other".
func worktreeLocation(path, mainPath, root string, main bool) string {
	switch {
	case main:
		return "main"
	case strings.HasPrefix(path, root+string(filepath.Separator)):
		return "container"
	case filepath.Dir(path) == filepath.Dir(mainPath):
		return "sibling"
	default:
		return "other"
	}
}

// shortSHA is the short form of a full object id for a worktree head: git's
// default minimum abbreviation of seven characters (REQ-WG-010).
func shortSHA(sha string) string {
	const short = 7
	if len(sha) > short {
		return sha[:short]
	}
	return sha
}

// worktreesKey is the cache key of a worktree list.
func worktreesKey(repo, base, root string) string {
	return repo + "\x00" + base + "\x00" + root
}
