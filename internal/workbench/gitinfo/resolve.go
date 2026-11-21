package gitinfo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// cacheTTL is how long a resolved directory is reused (REQ-WG-002).
const cacheTTL = 5 * time.Second

// cacheEntry is a resolved directory and the time it was stored.
type cacheEntry struct {
	info Info
	at   time.Time
}

// Info describes where a directory sits in git (REQ-WG-002). Repo is the
// absolute git common directory, so every worktree of one repository shares it;
// Worktree is the worktree root. Branch is the checked-out branch, or
// "detached" when HEAD is not on a branch. Short is the branch without its type
// prefix (REQ-WG-030) and Color is the worktree colour (REQ-WG-061). Main is
// true for the main worktree of the repository.
type Info struct {
	Repo     string `json:"repo"`
	Worktree string `json:"worktree"`
	Branch   string `json:"branch"`
	Short    string `json:"short"`
	Head     string `json:"head"`
	Color    string `json:"color"`
	Detached bool   `json:"detached"`
	Main     bool   `json:"main"`
}

// Resolve returns where dir sits in git (REQ-WG-002). It checks the path policy
// first: a directory outside it is ErrForbiddenPath and a directory outside any
// repository is ErrNotARepo. Results are cached per cleaned directory for 5 s,
// so a client that asks about the same working directory on every poll runs git
// at most once per window.
func (r *Runner) Resolve(ctx context.Context, dir string) (Info, error) {
	if err := r.CheckPath(dir); err != nil {
		return Info{}, err
	}
	key := filepath.Clean(dir)
	if info, ok := r.lookup(key); ok {
		return info, nil
	}
	info, err := r.read(ctx, dir)
	if err != nil {
		return Info{}, err
	}
	r.store(key, info)
	return info, nil
}

// read runs the git queries that make up an Info.
func (r *Runner) read(ctx context.Context, dir string) (Info, error) {
	out, err := r.Git(ctx, dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir", "--git-dir")
	if err != nil {
		if isNotARepo(err) {
			return Info{}, ErrNotARepo
		}
		return Info{}, err
	}
	lines := splitLines(out)
	if len(lines) < 3 {
		return Info{}, fmt.Errorf("gitinfo: unexpected rev-parse output: %q", strings.TrimSpace(string(out)))
	}
	worktree := filepath.Clean(lines[0])
	commonDir := filepath.Clean(lines[1])
	gitDir := filepath.Clean(lines[2])

	info := Info{
		Repo:     commonDir,
		Worktree: worktree,
		Main:     gitDir == commonDir,
	}
	if branch, err := r.Git(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(string(branch))
	} else {
		info.Detached = true
		info.Branch = "detached"
	}
	// An empty repository has no HEAD commit: git fails and Head stays empty,
	// which is not an error (REQ-WG-002).
	if head, err := r.Git(ctx, dir, "rev-parse", "--short", "HEAD"); err == nil {
		info.Head = strings.TrimSpace(string(head))
	}
	info.Short = ShortBranch(info.Branch)
	info.Color = Color(info.Worktree)
	return info, nil
}

// lookup returns a cached Info for key while it is younger than cacheTTL.
func (r *Runner) lookup(key string) (Info, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[key]
	if !ok {
		return Info{}, false
	}
	if r.clock().Sub(entry.at) >= cacheTTL {
		delete(r.cache, key)
		return Info{}, false
	}
	return entry.info, true
}

// store caches an Info for key at the current clock time.
func (r *Runner) store(key string, info Info) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = make(map[string]cacheEntry)
	}
	r.cache[key] = cacheEntry{info: info, at: r.clock()}
}

// isNotARepo reports whether a failed git run means the directory is outside any
// repository. With LC_ALL=C git prints "not a git repository", which is the
// stable marker of that case.
func isNotARepo(err error) bool {
	var ge *GitError
	if errors.As(err, &ge) {
		return strings.Contains(ge.Message, "not a git repository")
	}
	return false
}

// splitLines splits git output on newlines, dropping a trailing newline and
// reporting no lines for empty output.
func splitLines(out []byte) []string {
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
