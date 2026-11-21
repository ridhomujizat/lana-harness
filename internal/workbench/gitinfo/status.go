package gitinfo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// emptyTree is the well-known object id of git's empty tree (REQ-WG-004). It is
// the left side of a diff for a root commit, which has no parent.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// maxUntrackedSize caps how many bytes of an untracked file are read to count
// its lines (REQ-WG-003): a bigger file keeps add 0.
const maxUntrackedSize = 1 << 20

// binarySniffSize is how many leading bytes of an untracked file are checked for
// a NUL byte to call it binary (REQ-WG-003).
const binarySniffSize = 8 * 1024

// maxCommits caps the commit list of the branch scope (REQ-WG-004).
const maxCommits = 200

// commitPrefix is the scope form that names one commit (REQ-WG-004).
const commitPrefix = "commit:"

// statusModified and friends are the status letters of the contract: M, A, D,
// R (with an old path), U (untracked) and C (a conflict).
const (
	statusModified  = "M"
	statusAdded     = "A"
	statusDeleted   = "D"
	statusRenamed   = "R"
	statusUntracked = "U"
	statusConflict  = "C"
)

// commitSHARe matches the short or full object id of a commit scope (REQ-WG-004).
var commitSHARe = regexp.MustCompile(`^[0-9a-f]{4,40}$`)

// ErrNotFound is returned for a scope or file git does not know, so the HTTP
// layer can answer 404 not_found (api-contract).
var ErrNotFound = errors.New("gitinfo: not found")

// Scope is the comparison a Status or Diff asks for (REQ-WG-004). It is the
// wire value of the "scope" field.
type Scope string

// The scopes of REQ-WG-004.
const (
	ScopeUncommitted Scope = "uncommitted"
	ScopeBranch      Scope = "branch"
	ScopeLastCommit  Scope = "last_commit"
)

// ParseScope turns the scope query value into a Scope (REQ-WG-004). It accepts
// "uncommitted", "branch", "last_commit" and "commit:<sha>" where sha is 4 to 40
// lowercase hex digits; anything else is an error the HTTP layer maps to 400
// invalid_request.
func ParseScope(s string) (Scope, error) {
	switch Scope(s) {
	case ScopeUncommitted, ScopeBranch, ScopeLastCommit:
		return Scope(s), nil
	}
	if sha, ok := strings.CutPrefix(s, commitPrefix); ok && commitSHARe.MatchString(sha) {
		return Scope(s), nil
	}
	return "", fmt.Errorf("gitinfo: invalid scope %q", s)
}

// commitSHA returns the object id of a "commit:<sha>" scope.
func (s Scope) commitSHA() (string, bool) {
	return strings.CutPrefix(string(s), commitPrefix)
}

// FileChange is one changed file of a status answer (REQ-WG-003). OldPath is
// set only for a rename; Add, Del and Binary are the line counts and binary flag
// of the change.
type FileChange struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"`
	Status  string `json:"status"`
	Add     int    `json:"add"`
	Del     int    `json:"del"`
	Binary  bool   `json:"binary"`
}

// Commit is one commit of the branch scope (REQ-WG-004).
type Commit struct {
	SHA     string    `json:"sha"`
	Subject string    `json:"subject"`
	Time    time.Time `json:"time"`
}

// Totals sums the files of a status answer (REQ-WG-003).
type Totals struct {
	Files int `json:"files"`
	Add   int `json:"add"`
	Del   int `json:"del"`
}

// Status is the answer of REQ-WG-003. Base, MergeBase and Commits are filled for
// the branch scope only; Files is never null.
type Status struct {
	Worktree  string       `json:"worktree"`
	Branch    string       `json:"branch"`
	Upstream  string       `json:"upstream,omitempty"`
	Ahead     int          `json:"ahead"`
	Behind    int          `json:"behind"`
	Scope     Scope        `json:"scope"`
	Base      string       `json:"base,omitempty"`
	MergeBase string       `json:"merge_base,omitempty"`
	Commits   []Commit     `json:"commits,omitempty"`
	Files     []FileChange `json:"files"`
	Totals    Totals       `json:"totals"`
}

// Status reports the changed files of a worktree for one scope (REQ-WG-003,
// REQ-WG-004). The path policy is checked first (ErrForbiddenPath) and a
// directory outside any repository is ErrNotARepo. An unknown commit scope is
// ErrNotFound.
func (r *Runner) Status(ctx context.Context, worktree string, scope Scope, base string) (Status, error) {
	if err := r.CheckPath(worktree); err != nil {
		return Status{}, err
	}
	out, err := r.Git(ctx, worktree, "status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all")
	if err != nil {
		if isNotARepo(err) {
			return Status{}, ErrNotARepo
		}
		return Status{}, err
	}
	ps := parsePorcelainV2(out)
	status := Status{
		Worktree: filepath.Clean(worktree),
		Branch:   ps.branch,
		Upstream: ps.upstream,
		Ahead:    ps.ahead,
		Behind:   ps.behind,
		Scope:    scope,
		Files:    []FileChange{},
	}

	switch {
	case scope == ScopeUncommitted:
		counts, err := r.uncommittedCounts(ctx, worktree)
		if err != nil {
			return Status{}, err
		}
		files := buildChanges(ps.changes, counts)
		files = append(files, r.untrackedChanges(worktree, ps.untracked)...)
		status.Files = sortChanges(files)
	case scope == ScopeBranch:
		ref, mergeBase, short, err := r.resolveMergeBase(ctx, worktree, base)
		if err != nil {
			return Status{}, err
		}
		status.Base = ref
		status.MergeBase = short
		files, err := r.rangeChanges(ctx, worktree, []string{"-M", mergeBase})
		if err != nil {
			return Status{}, err
		}
		files = append(files, r.untrackedChanges(worktree, ps.untracked)...)
		status.Files = sortChanges(files)
		commits, err := r.commits(ctx, worktree, mergeBase)
		if err != nil {
			return Status{}, err
		}
		status.Commits = commits
	case scope == ScopeLastCommit:
		rangeArgs, err := r.commitRange(ctx, worktree, "HEAD")
		if err != nil {
			return Status{}, err
		}
		files, err := r.rangeChanges(ctx, worktree, rangeArgs)
		if err != nil {
			return Status{}, err
		}
		status.Files = files
	default:
		sha, ok := scope.commitSHA()
		if !ok {
			return Status{}, fmt.Errorf("gitinfo: invalid scope %q", scope)
		}
		rangeArgs, err := r.commitRange(ctx, worktree, sha)
		if err != nil {
			return Status{}, err
		}
		files, err := r.rangeChanges(ctx, worktree, rangeArgs)
		if err != nil {
			return Status{}, err
		}
		status.Files = files
	}

	status.Totals = totalsOf(status.Files)
	return status, nil
}

// namedChange is a path and status letter as git reports them, with the old path
// of a rename.
type namedChange struct {
	path    string
	oldPath string
	status  string
}

// porcelainInfo is the part of `git status --porcelain=v2` the status uses
// (REQ-WG-003).
type porcelainInfo struct {
	branch    string
	upstream  string
	ahead     int
	behind    int
	changes   []namedChange
	untracked []string
}

// parsePorcelainV2 reads the NUL-separated records of `git status
// --porcelain=v2 -z` (REQ-WG-003): the branch headers, the ordinary "1" records,
// the rename "2" records (the old path is the record that follows), the
// unmerged "u" records and the untracked "?" records.
func parsePorcelainV2(out []byte) porcelainInfo {
	var ps porcelainInfo
	tokens := splitNUL(out)
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case strings.HasPrefix(token, "# "):
			parsePorcelainHeader(&ps, token)
		case strings.HasPrefix(token, "1 "):
			if fields := strings.SplitN(token, " ", 9); len(fields) == 9 {
				ps.changes = append(ps.changes, namedChange{path: fields[8], status: ordinaryStatus(fields[1])})
			}
		case strings.HasPrefix(token, "2 "):
			if fields := strings.SplitN(token, " ", 10); len(fields) == 10 && i+1 < len(tokens) {
				i++
				ps.changes = append(ps.changes, namedChange{path: fields[9], oldPath: tokens[i], status: statusRenamed})
			}
		case strings.HasPrefix(token, "u "):
			if fields := strings.SplitN(token, " ", 11); len(fields) == 11 {
				ps.changes = append(ps.changes, namedChange{path: fields[10], status: statusConflict})
			}
		case strings.HasPrefix(token, "? "):
			ps.untracked = append(ps.untracked, token[2:])
		}
	}
	return ps
}

// parsePorcelainHeader fills the branch fields of a line like
// "# branch.head main", "# branch.upstream origin/main" or
// "# branch.ab +2 -0" (REQ-WG-003).
func parsePorcelainHeader(ps *porcelainInfo, line string) {
	switch {
	case strings.HasPrefix(line, "# branch.head "):
		ps.branch = strings.TrimPrefix(line, "# branch.head ")
		if ps.branch == "(detached)" {
			ps.branch = "detached"
		}
	case strings.HasPrefix(line, "# branch.upstream "):
		ps.upstream = strings.TrimPrefix(line, "# branch.upstream ")
	case strings.HasPrefix(line, "# branch.ab "):
		for _, field := range strings.Fields(strings.TrimPrefix(line, "# branch.ab ")) {
			if len(field) < 2 {
				continue
			}
			switch field[0] {
			case '+':
				ps.ahead, _ = strconv.Atoi(field[1:])
			case '-':
				ps.behind, _ = strconv.Atoi(field[1:])
			}
		}
	}
}

// ordinaryStatus is the status letter of an ordinary "1" record (REQ-WG-003): a
// D on either side is D, an A in the index is A, else M.
func ordinaryStatus(xy string) string {
	if len(xy) < 2 {
		return statusModified
	}
	switch {
	case xy[0] == 'D' || xy[1] == 'D':
		return statusDeleted
	case xy[0] == 'A':
		return statusAdded
	default:
		return statusModified
	}
}

// fileStat is the line counts of one file from `git diff --numstat`.
type fileStat struct {
	add    int
	del    int
	binary bool
}

// counts runs `git diff --numstat -z` over the given diff arguments and maps
// each file to its line counts (REQ-WG-003).
func (r *Runner) counts(ctx context.Context, dir string, args ...string) (map[string]fileStat, error) {
	out, err := r.Git(ctx, dir, append([]string{"diff", "--numstat", "-z"}, args...)...)
	if err != nil {
		return nil, err
	}
	return parseNumstat(out), nil
}

// parseNumstat reads the NUL-separated `git diff --numstat -z` records: "add TAB
// del TAB path" for a normal file and "add TAB del TAB NUL oldPath NUL newPath"
// for a rename, where "-\t-" marks a binary file.
func parseNumstat(out []byte) map[string]fileStat {
	tokens := splitNUL(out)
	stats := make(map[string]fileStat)
	for i := 0; i < len(tokens); i++ {
		parts := strings.SplitN(tokens[i], "\t", 3)
		if len(parts) < 3 {
			continue
		}
		stat := fileStat{}
		if parts[0] == "-" || parts[1] == "-" {
			stat.binary = true
		} else {
			stat.add, _ = strconv.Atoi(parts[0])
			stat.del, _ = strconv.Atoi(parts[1])
		}
		if parts[2] != "" {
			stats[parts[2]] = stat
			continue
		}
		if i+2 < len(tokens) {
			stats[tokens[i+2]] = stat
			i += 2
		}
	}
	return stats
}

// letters runs `git diff --name-status -z` over the given diff arguments
// (REQ-WG-004).
func (r *Runner) letters(ctx context.Context, dir string, args ...string) ([]namedChange, error) {
	out, err := r.Git(ctx, dir, append([]string{"diff", "--name-status", "-z"}, args...)...)
	if err != nil {
		return nil, err
	}
	return parseNameStatus(out), nil
}

// parseNameStatus reads the NUL-separated `git diff --name-status -z` records:
// "status NUL path" for a normal file and "status NUL oldPath NUL newPath" for a
// rename or copy.
func parseNameStatus(out []byte) []namedChange {
	tokens := splitNUL(out)
	var changes []namedChange
	for i := 0; i < len(tokens); i++ {
		status := tokens[i]
		if status == "" {
			continue
		}
		letter := statusLetter(status)
		if status[0] == 'R' || status[0] == 'C' {
			if i+2 < len(tokens) {
				changes = append(changes, namedChange{path: tokens[i+2], oldPath: tokens[i+1], status: letter})
				i += 2
			}
			continue
		}
		if i+1 < len(tokens) {
			changes = append(changes, namedChange{path: tokens[i+1], status: letter})
			i++
		}
	}
	return changes
}

// statusLetter maps a name-status code (like "M", "R100" or "C075") to the
// status letter of the contract (REQ-WG-003).
func statusLetter(code string) string {
	switch {
	case code == "":
		return statusModified
	case code[0] == 'A':
		return statusAdded
	case code[0] == 'D':
		return statusDeleted
	case code[0] == 'R' || code[0] == 'C':
		return statusRenamed
	case code[0] == 'U':
		return statusConflict
	default:
		return statusModified
	}
}

// rangeChanges joins the letters and line counts of the same diff arguments into
// file changes sorted by path (REQ-WG-004).
func (r *Runner) rangeChanges(ctx context.Context, dir string, args []string) ([]FileChange, error) {
	letters, err := r.letters(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	counts, err := r.counts(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	return sortChanges(buildChanges(letters, counts)), nil
}

// buildChanges pairs the status letters with the line counts of the same file.
func buildChanges(letters []namedChange, counts map[string]fileStat) []FileChange {
	files := make([]FileChange, 0, len(letters))
	for _, c := range letters {
		fc := FileChange{Path: c.path, OldPath: c.oldPath, Status: c.status}
		if st, ok := counts[c.path]; ok {
			fc.Add, fc.Del, fc.Binary = st.add, st.del, st.binary
		}
		files = append(files, fc)
	}
	return files
}

// sortChanges orders files by path and never returns nil, so the JSON list is
// [] and not null.
func sortChanges(files []FileChange) []FileChange {
	if files == nil {
		files = []FileChange{}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

// totalsOf sums the file count, additions and deletions (REQ-WG-003).
func totalsOf(files []FileChange) Totals {
	t := Totals{Files: len(files)}
	for _, f := range files {
		t.Add += f.Add
		t.Del += f.Del
	}
	return t
}

// uncommittedCounts is `git diff --numstat HEAD` (REQ-WG-003). A repository with
// no HEAD commit counts the index against the empty tree instead.
func (r *Runner) uncommittedCounts(ctx context.Context, dir string) (map[string]fileStat, error) {
	if _, err := r.Git(ctx, dir, "rev-parse", "--verify", "-q", "HEAD^{commit}"); err != nil {
		return r.counts(ctx, dir, "--cached", emptyTree)
	}
	return r.counts(ctx, dir, "HEAD")
}

// resolveMergeBase picks the base ref of the branch scope (REQ-WG-005): the
// remote-tracking origin/<base> when it exists, else <base>. It returns the ref
// used, the merge base of that ref and HEAD, and its short form.
func (r *Runner) resolveMergeBase(ctx context.Context, dir, base string) (ref, mergeBase, short string, err error) {
	ref = base
	if _, err := r.Git(ctx, dir, "rev-parse", "--verify", "-q", "origin/"+base+"^{commit}"); err == nil {
		ref = "origin/" + base
	}
	out, err := r.Git(ctx, dir, "merge-base", ref, "HEAD")
	if err != nil {
		return "", "", "", err
	}
	mergeBase = strings.TrimSpace(string(out))
	shortOut, err := r.Git(ctx, dir, "rev-parse", "--short", mergeBase)
	if err != nil {
		return "", "", "", err
	}
	return ref, mergeBase, strings.TrimSpace(string(shortOut)), nil
}

// commitRange returns the two revision arguments that diff one commit against
// its parent, using the empty tree for a root commit (REQ-WG-004). A revision
// git does not know is ErrNotFound.
func (r *Runner) commitRange(ctx context.Context, dir, rev string) ([]string, error) {
	out, err := r.Git(ctx, dir, "rev-parse", "--verify", "-q", rev+"^{commit}")
	if err != nil {
		return nil, ErrNotFound
	}
	sha := strings.TrimSpace(string(out))
	if _, err := r.Git(ctx, dir, "rev-parse", "--verify", "-q", sha+"~1^{commit}"); err != nil {
		return []string{"-M", emptyTree, sha}, nil
	}
	return []string{"-M", sha + "~1", sha}, nil
}

// commits lists the commits of between and HEAD, newest first, at most
// maxCommits (REQ-WG-004). Each record is short sha, subject and committer date,
// NUL-separated.
func (r *Runner) commits(ctx context.Context, dir, between string) ([]Commit, error) {
	out, err := r.Git(ctx, dir, "log", "--format=%h%x00%s%x00%cI", "-z", "-n", strconv.Itoa(maxCommits), between+"..HEAD")
	if err != nil {
		return nil, err
	}
	return parseCommits(out), nil
}

// parseCommits reads the NUL-separated triples of the log format above.
func parseCommits(out []byte) []Commit {
	tokens := splitNUL(out)
	commits := make([]Commit, 0, len(tokens)/3)
	for i := 0; i+2 < len(tokens); i += 3 {
		t, _ := time.Parse(time.RFC3339, tokens[i+2])
		commits = append(commits, Commit{SHA: tokens[i], Subject: tokens[i+1], Time: t})
	}
	return commits
}

// untrackedChanges turns the untracked paths of the porcelain output into U
// files, counting the lines of each (REQ-WG-003).
func (r *Runner) untrackedChanges(worktree string, paths []string) []FileChange {
	if len(paths) == 0 {
		return nil
	}
	changes := make([]FileChange, 0, len(paths))
	for _, rel := range paths {
		changes = append(changes, r.untrackedChange(worktree, rel))
	}
	return changes
}

// untrackedChange is one untracked file: status U and, when the policy allows
// reading it, its line count. A file of over 1 MB keeps add 0 and a file whose
// first 8 KB hold a NUL byte is binary (REQ-WG-003).
func (r *Runner) untrackedChange(worktree, rel string) FileChange {
	fc := FileChange{Path: rel, Status: statusUntracked}
	abs := filepath.Join(worktree, rel)
	if err := r.CheckPath(abs); err != nil {
		return fc
	}
	f, err := os.Open(abs)
	if err != nil {
		return fc
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fc
	}
	head := make([]byte, binarySniffSize)
	n, _ := io.ReadFull(f, head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		fc.Binary = true
		return fc
	}
	if info.Size() > maxUntrackedSize {
		return fc
	}
	rest, err := io.ReadAll(f)
	if err != nil {
		return fc
	}
	fc.Add = bytes.Count(head[:n], []byte{'\n'}) + bytes.Count(rest, []byte{'\n'})
	return fc
}

// splitNUL splits git -z output on NUL bytes, dropping the terminator, and
// reports no records for empty output.
func splitNUL(out []byte) []string {
	s := strings.TrimSuffix(string(out), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}
