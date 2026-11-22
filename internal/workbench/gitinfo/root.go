package gitinfo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// scanTimeout is how long one summary git run may take (REQ-WM-082): the status
// scan and the numstat pass each get 3 s, not the runner's 10 s default.
const scanTimeout = 3 * time.Second

// folderMaxEntries is how many entries ListFolder returns before it cuts the
// list and sets Truncated (REQ-WM-004).
const folderMaxEntries = 2000

// shortHeadLen is how many hex digits of a HEAD object id a summary keeps, the
// shape of `git rev-parse --short HEAD` for a normal repository (REQ-WM-003).
const shortHeadLen = 7

// etagLen is how many hex digits of the summary hash the ETag keeps
// (REQ-WM-003).
const etagLen = 12

// initialOID is the porcelain v2 branch.oid of a repository whose branch has no
// commit yet.
const initialOID = "(initial)"

// ErrNotARoot is returned for a folder that is inside a git worktree or that
// holds no direct child repository (REQ-WM-001).
var ErrNotARoot = errors.New("gitinfo: not a workbench root")

// ErrInsideRepo is returned by ListFolder for a folder inside a git worktree, so
// the caller uses the worktree file tree of REQ-WG-007 instead (REQ-WM-004).
var ErrInsideRepo = errors.New("gitinfo: folder is inside a git repository")

// RootInfo is a workbench root (REQ-WM-001): a folder outside any worktree that
// holds several repositories. Path is the cleaned folder, Name its base name and
// Repos the names of its direct child repositories, sorted case-insensitively.
type RootInfo struct {
	Path  string   `json:"path"`
	Name  string   `json:"name"`
	Repos []string `json:"repos"`
}

// RepoSummary is the cheap per-repo scan a root summary is built from
// (REQ-WM-003, REQ-WM-082). Error carries a git failure code ("git_timeout" or
// "git_failed") and is empty for a healthy scan, so one broken repository never
// hides the others.
type RepoSummary struct {
	Name      string `json:"name"`
	Display   string `json:"display"`
	Worktree  string `json:"worktree"`
	Branch    string `json:"branch"`
	Short     string `json:"short"`
	Detached  bool   `json:"detached"`
	Head      string `json:"head"`
	Color     string `json:"color"`
	Changed   int    `json:"changed"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	Error     string `json:"error,omitempty"`
	Slow      bool   `json:"slow,omitempty"`
	Etag      string `json:"etag"`
}

// FolderEntry is one row of a folder listing outside a repo (REQ-WM-004). Kind
// is "dir" or "file"; Repo, Short, Color and Changed are set for a child folder
// that is a repository; Size is set for files only.
type FolderEntry struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Repo     bool   `json:"repo,omitempty"`
	Short    string `json:"short,omitempty"`
	Color    string `json:"color,omitempty"`
	Changed  int    `json:"changed,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Modified string `json:"modified,omitempty"`
}

// FolderListing is the answer of REQ-WM-004. Entries is never null.
type FolderListing struct {
	Dir       string        `json:"dir"`
	Entries   []FolderEntry `json:"entries"`
	Truncated bool          `json:"truncated"`
}

// rootCache caches a detected RootInfo per cleaned folder with the 5 s window of
// the resolve cache (REQ-WM-001). It is safe for concurrent use.
type rootCache struct {
	mu      sync.Mutex
	entries map[string]rootEntry
}

// rootEntry is a detected root and the time it was stored.
type rootEntry struct {
	info RootInfo
	at   time.Time
}

// get returns a cached RootInfo for key while it is younger than cacheTTL.
func (c *rootCache) get(key string, now time.Time) (RootInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return RootInfo{}, false
	}
	if now.Sub(entry.at) >= cacheTTL {
		delete(c.entries, key)
		return RootInfo{}, false
	}
	return entry.info, true
}

// put caches info for key at now.
func (c *rootCache) put(key string, info RootInfo, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]rootEntry)
	}
	c.entries[key] = rootEntry{info: info, at: now}
}

// DetectRoot reports whether dir is a workbench root (REQ-WM-001): a folder that
// passes the path policy, sits outside any git worktree, and holds at least one
// direct child repository (a folder with a ".git" folder or file). Only direct
// children are read and hidden children are skipped; a folder inside a worktree
// or one with no child repository is ErrNotARoot and a folder the policy refuses
// is ErrForbiddenPath. Results are cached per cleaned folder for 5 s.
func (r *Runner) DetectRoot(ctx context.Context, dir string) (RootInfo, error) {
	if err := r.CheckPath(dir); err != nil {
		return RootInfo{}, err
	}
	key := filepath.Clean(dir)
	if info, ok := r.caches.roots.get(key, r.clock()); ok {
		return info, nil
	}
	if _, err := r.Resolve(ctx, dir); err == nil {
		return RootInfo{}, ErrNotARoot
	} else if !errors.Is(err, ErrNotARepo) {
		return RootInfo{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return RootInfo{}, ErrNotARoot
	}
	repos := repoChildren(dir, entries)
	if len(repos) == 0 {
		return RootInfo{}, ErrNotARoot
	}
	info := RootInfo{Path: key, Name: filepath.Base(key), Repos: repos}
	r.caches.roots.put(key, info, r.clock())
	return info, nil
}

// repoChildren names the direct child folders of dir that are repositories
// (REQ-WM-001): folders holding a ".git" folder or a ".git" file, hidden folders
// skipped and the names sorted case-insensitively.
func repoChildren(dir string, entries []os.DirEntry) []string {
	repos := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !entry.IsDir() {
			continue
		}
		if hasGit(filepath.Join(dir, name)) {
			repos = append(repos, name)
		}
	}
	sort.Slice(repos, func(i, j int) bool { return lessName(repos[i], repos[j]) })
	return repos
}

// hasGit reports whether a folder holds a ".git" folder (a main worktree) or a
// ".git" file (a linked worktree), which makes it a repository (REQ-WM-001).
func hasGit(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// RepoDisplayName is the display name of a repo folder (REQ-WM-007): its name
// without the root name and a following "-" when it starts with them, else the
// name unchanged. "sxored-be-ocrengine" in "sxored" is "be-ocrengine".
func RepoDisplayName(rootName, folder string) string {
	prefix := rootName + "-"
	if rest, ok := strings.CutPrefix(folder, prefix); ok && rest != "" {
		return rest
	}
	return folder
}

// ScanRepo runs the cheap summary scan of one repository (REQ-WM-003,
// REQ-WM-082): `git status --porcelain=v2 --untracked-files=normal` and, only
// when there are changes, `git diff --numstat HEAD`, both with the 3 s scan
// timeout. A repository with no commit skips the numstat pass. A git failure is
// reported in Error and never panics, so a list of repos can always be built.
func (r *Runner) ScanRepo(ctx context.Context, rootName, worktree string) RepoSummary {
	dir := filepath.Clean(worktree)
	name := filepath.Base(dir)
	summary := RepoSummary{
		Name:     name,
		Display:  RepoDisplayName(rootName, name),
		Worktree: dir,
		Color:    Color(dir),
	}

	out, err := r.gitTimeout(ctx, dir, scanTimeout, "status", "--porcelain=v2", "-z", "--branch", "--untracked-files=normal")
	if err != nil {
		summary.Error = gitErrorCode(err)
		return summary
	}
	ps := parsePorcelainV2(out)
	summary.Branch = ps.branch
	summary.Short = ShortBranch(ps.branch)
	summary.Detached = ps.detached
	summary.Head = shortHead(ps.oid)
	summary.Ahead = ps.ahead
	summary.Behind = ps.behind
	summary.Changed = len(ps.changes) + len(ps.untracked)

	var numstat []byte
	if summary.Changed > 0 && ps.oid != initialOID {
		numstat, err = r.gitTimeout(ctx, dir, scanTimeout, "diff", "--numstat", "-z", "HEAD")
		if err != nil {
			summary.Error = gitErrorCode(err)
			return summary
		}
		for _, stat := range parseNumstat(numstat) {
			summary.Additions += stat.add
			summary.Deletions += stat.del
		}
	}
	summary.Etag = repoEtag(out, numstat)
	return summary
}

// ListFolder lists one folder outside a repo (REQ-WM-004): folders first, then
// files, each group case-insensitively sorted, with name, kind, size and
// modified time, and for a child folder that is a repository (it holds a ".git")
// repo true plus the short branch, colour and changed count the callback
// returns. ".git" is never listed and a child the path policy refuses is
// skipped. Over folderMaxEntries entries keeps the first folderMaxEntries and
// sets Truncated. A folder inside a worktree is ErrInsideRepo, so the caller
// uses the REQ-WG-007 tree instead.
func (r *Runner) ListFolder(ctx context.Context, dir string, repo func(child string) (RepoSummary, bool)) (FolderListing, error) {
	if err := r.CheckPath(dir); err != nil {
		return FolderListing{}, err
	}
	if _, err := r.Resolve(ctx, dir); err == nil {
		return FolderListing{}, ErrInsideRepo
	} else if !errors.Is(err, ErrNotARepo) {
		return FolderListing{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return FolderListing{}, ErrNotFound
	}

	dirs, files := []FolderEntry{}, []FolderEntry{}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" {
			continue
		}
		child := filepath.Join(dir, name)
		if err := r.CheckPath(child); err != nil {
			continue
		}
		isDir := entry.IsDir()
		fe := FolderEntry{Name: name, Kind: treeFileKind}
		if isDir {
			fe.Kind = treeDirKind
			if repo != nil && hasGit(child) {
				fe.Repo = true
				if summary, ok := repo(name); ok {
					fe.Short = summary.Short
					fe.Color = summary.Color
					fe.Changed = summary.Changed
				}
			}
		}
		if info, err := entry.Info(); err == nil {
			fe.Modified = info.ModTime().Format(time.RFC3339)
			if !isDir {
				fe.Size = info.Size()
			}
		}
		if isDir {
			dirs = append(dirs, fe)
		} else {
			files = append(files, fe)
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return lessName(dirs[i].Name, dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return lessName(files[i].Name, files[j].Name) })
	all := make([]FolderEntry, 0, len(dirs)+len(files))
	all = append(all, dirs...)
	all = append(all, files...)

	listing := FolderListing{Dir: filepath.Clean(dir), Entries: all}
	if len(all) > folderMaxEntries {
		listing.Entries = all[:folderMaxEntries]
		listing.Truncated = true
	}
	return listing, nil
}

// lessName orders two names case-insensitively, with the exact name as the
// tie-break so the order is stable (REQ-WM-004, REQ-WM-007).
func lessName(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}

// shortHead abbreviates the HEAD object id of the porcelain branch header, or ""
// when the repository has no commit yet (REQ-WM-003).
func shortHead(oid string) string {
	if oid == "" || oid == initialOID {
		return ""
	}
	if len(oid) > shortHeadLen {
		return oid[:shortHeadLen]
	}
	return oid
}

// gitErrorCode is the error code a failed summary git run lands in a
// RepoSummary as (REQ-WM-082): "git_timeout" or "git_failed".
func gitErrorCode(err error) string {
	var ge *GitError
	if errors.As(err, &ge) {
		return ge.Code
	}
	return "git_failed"
}

// repoEtag is the summary ETag of a repository (REQ-WM-003): "e-" plus a short
// hash of the raw status output and, when it ran, the raw numstat output, so a
// client reloads a repo only when its state changed.
func repoEtag(status, numstat []byte) string {
	h := sha256.New()
	_, _ = h.Write(status)
	_, _ = h.Write(numstat)
	return "e-" + hex.EncodeToString(h.Sum(nil))[:etagLen]
}
