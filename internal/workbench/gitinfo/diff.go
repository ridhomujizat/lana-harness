package gitinfo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// diffContext is the unified diff context of REQ-WG-006.
const diffContext = "-U3"

// maxDiffLines is how many diff lines are kept before the diff is cut and
// truncated is set (REQ-WG-006).
const maxDiffLines = 5000

// FileDiff is the answer of REQ-WG-006. Hunks is never null; Binary and
// Truncated tell the viewer to say so instead.
type FileDiff struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
	Hunks     []Hunk `json:"hunks"`
}

// Hunk is one @@ hunk of a diff (REQ-WG-006).
type Hunk struct {
	Header   string `json:"header"`
	OldStart int    `json:"old_start"`
	NewStart int    `json:"new_start"`
	Lines    []Line `json:"lines"`
}

// Line is one line of a hunk (REQ-WG-006). Kind is "ctx", "add" or "del"; a
// context line carries both line numbers, an add only new, a del only old.
type Line struct {
	Kind      string `json:"kind"`
	Old       int    `json:"old,omitempty"`
	New       int    `json:"new,omitempty"`
	Text      string `json:"text"`
	NoNewline bool   `json:"no_newline,omitempty"`
}

// hunkHeaderRe reads the old and new start of a "@@ -412,9 +412,14 @@ ..." line.
var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// Diff returns the diff of one file of a worktree for one scope (REQ-WG-006).
// The path is relative to the worktree; an absolute path or one that climbs out
// with ".." is ErrForbiddenPath, as is a joined path the policy refuses. An
// unknown commit scope is ErrNotFound.
func (r *Runner) Diff(ctx context.Context, worktree, path string, scope Scope, base string) (FileDiff, error) {
	if err := r.CheckPath(worktree); err != nil {
		return FileDiff{}, err
	}
	rel, ok := relativePath(path)
	if !ok {
		return FileDiff{}, ErrForbiddenPath
	}
	if err := r.CheckPath(resolvedLeaf(filepath.Join(worktree, rel))); err != nil {
		return FileDiff{}, err
	}

	fd := FileDiff{Path: rel, Hunks: []Hunk{}}

	var (
		rangeArgs []string
		untracked bool
	)
	switch {
	case scope == ScopeUncommitted:
		untracked = r.isUntracked(ctx, worktree, rel)
		rangeArgs = []string{"-M", "HEAD"}
	case scope == ScopeBranch:
		_, mergeBase, _, err := r.resolveMergeBase(ctx, worktree, base)
		if err != nil {
			return FileDiff{}, err
		}
		untracked = r.isUntracked(ctx, worktree, rel)
		rangeArgs = []string{"-M", mergeBase}
	case scope == ScopeLastCommit:
		args, err := r.commitRange(ctx, worktree, "HEAD")
		if err != nil {
			return FileDiff{}, err
		}
		rangeArgs = args
	default:
		sha, ok := scope.commitSHA()
		if !ok {
			return FileDiff{}, fmt.Errorf("gitinfo: invalid scope %q", scope)
		}
		args, err := r.commitRange(ctx, worktree, sha)
		if err != nil {
			return FileDiff{}, err
		}
		rangeArgs = args
	}

	var (
		raw []byte
		err error
	)
	if untracked {
		fd.Status = statusUntracked
		// git diff --no-index exits 1 when the two files differ, which is the
		// normal case here (REQ-WG-006).
		raw, err = r.gitAllowExit(ctx, worktree, 1, "diff", "--no-color", "--no-index", "--", os.DevNull, rel)
	} else {
		args := append([]string{"diff", "--no-color", "--no-ext-diff", diffContext}, rangeArgs...)
		args = append(args, "--", rel)
		raw, err = r.Git(ctx, worktree, args...)
		if err == nil {
			fd.Status = r.diffStatus(ctx, worktree, rangeArgs, rel)
		}
	}
	if err != nil {
		return FileDiff{}, err
	}

	hunks, binary, truncated := ParseUnifiedDiff(string(raw), maxDiffLines)
	if hunks == nil {
		hunks = []Hunk{}
	}
	fd.Hunks = hunks
	fd.Binary = binary
	fd.Truncated = truncated
	return fd, nil
}

// relativePath cleans a worktree-relative path and refuses an absolute path or
// one that climbs out of the worktree.
func relativePath(path string) (string, bool) {
	if path == "" || filepath.IsAbs(path) {
		return "", false
	}
	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return clean, true
}

// resolvedLeaf returns a path with the symlinks of its parent folder resolved,
// so the policy can check a file that is not on disk, like a deleted file of a
// diff (REQ-WG-006). preview.Resolve leaves a missing path as it is, which on
// macOS would keep a temp folder under its /var symlink and fail the policy.
func resolvedLeaf(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	dir, base := filepath.Split(filepath.Clean(path))
	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(resolvedDir, base)
	}
	return path
}

// isUntracked reports whether a worktree-relative path is an untracked file, so
// the diff is taken against /dev/null (REQ-WG-006).
func (r *Runner) isUntracked(ctx context.Context, dir, rel string) bool {
	out, err := r.Git(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z", "--", rel)
	if err != nil {
		return false
	}
	return len(strings.Trim(string(out), "\x00")) > 0
}

// diffStatus is the status letter of a path for the same diff arguments, taken
// from the name-status list (REQ-WG-003).
func (r *Runner) diffStatus(ctx context.Context, dir string, rangeArgs []string, rel string) string {
	letters, err := r.letters(ctx, dir, rangeArgs...)
	if err != nil {
		return ""
	}
	for _, c := range letters {
		if c.path == rel {
			return c.status
		}
	}
	return ""
}

// gitAllowExit runs git like Git, but treats the given exit code as success and
// still returns its standard output. It exists for "git diff --no-index", which
// reports a difference with exit code 1 (REQ-WG-006).
func (r *Runner) gitAllowExit(ctx context.Context, dir string, code int, args ...string) ([]byte, error) {
	timeout := r.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return nil, &GitError{Code: "git_timeout", Message: firstLine(stderr.String())}
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() == code {
				return stdout.Bytes(), nil
			}
			return nil, &GitError{Code: "git_failed", Message: firstLine(stderr.String()), ExitCode: exitErr.ExitCode()}
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// ParseUnifiedDiff reads a unified diff into hunks (REQ-WG-006). "Binary files
// ... differ" returns binary true and no hunks. A diff with more than maxLines
// lines stops at maxLines and returns truncated true; maxLines 0 means no limit.
// A "\ No newline at end of file" line sets NoNewline on the line before it.
func ParseUnifiedDiff(text string, maxLines int) (hunks []Hunk, binary, truncated bool) {
	var cur *Hunk
	oldNo, newNo := 0, 0
	total := 0
	for _, raw := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(raw, "Binary files ") && strings.HasSuffix(raw, " differ"):
			return nil, true, false
		case strings.HasPrefix(raw, "@@ "):
			oldStart, newStart, ok := parseHunkHeader(raw)
			if !ok {
				continue
			}
			hunks = append(hunks, Hunk{Header: raw, OldStart: oldStart, NewStart: newStart, Lines: []Line{}})
			cur = &hunks[len(hunks)-1]
			oldNo, newNo = oldStart, newStart
		case raw == "":
		case raw[0] == '\\':
			if cur != nil && len(cur.Lines) > 0 {
				cur.Lines[len(cur.Lines)-1].NoNewline = true
			}
		case cur != nil && (raw[0] == '+' || raw[0] == '-' || raw[0] == ' '):
			if maxLines > 0 && total >= maxLines {
				return hunks, false, true
			}
			total++
			switch raw[0] {
			case '+':
				cur.Lines = append(cur.Lines, Line{Kind: "add", New: newNo, Text: raw[1:]})
				newNo++
			case '-':
				cur.Lines = append(cur.Lines, Line{Kind: "del", Old: oldNo, Text: raw[1:]})
				oldNo++
			default:
				cur.Lines = append(cur.Lines, Line{Kind: "ctx", Old: oldNo, New: newNo, Text: raw[1:]})
				oldNo++
				newNo++
			}
		}
	}
	return hunks, false, false
}

// parseHunkHeader reads the old and new start numbers of a hunk header.
func parseHunkHeader(header string) (int, int, bool) {
	m := hunkHeaderRe.FindStringSubmatch(header)
	if m == nil {
		return 0, 0, false
	}
	oldStart, _ := strconv.Atoi(m[1])
	newStart, _ := strconv.Atoi(m[2])
	return oldStart, newStart, true
}
