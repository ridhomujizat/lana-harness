package gitinfo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes a text file inside a test repository, creating its folders.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	writeBytes(t, dir, name, []byte(content))
}

// writeBytes writes a file inside a test repository, creating its folders.
func writeBytes(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// removeFile deletes a file inside a test repository.
func removeFile(dir, name string) error {
	return os.Remove(filepath.Join(dir, name))
}

// gitTry runs git and returns its output and error, so a test can expect a
// failure (a merge that conflicts) without failing itself.
func gitTry(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = testGitEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// changesByPath indexes a status file list by path.
func changesByPath(files []FileChange) map[string]FileChange {
	byPath := make(map[string]FileChange, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}
	return byPath
}

func TestStatusUncommitted(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "a.txt", "one\ntwo\nthree\n")
	writeFile(t, repo, "b.txt", "keep one\nkeep two\nkeep three\n")
	writeFile(t, repo, "d.txt", "delete me\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "files")

	writeFile(t, repo, "a.txt", "one\nTWO\nthree\n") // modified
	writeFile(t, repo, "added.txt", "added\n")       // staged add
	runGit(t, repo, "add", "added.txt")
	if err := os.Remove(filepath.Join(repo, "d.txt")); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "mv", "b.txt", "moved.txt")        // staged rename
	writeFile(t, repo, "unt.txt", "line1\nline2\n")    // untracked text
	writeBytes(t, repo, "bin.dat", []byte{0, 1, 2, 3}) // untracked binary

	r := NewRunner(allowPolicy(root))
	status, err := r.Status(context.Background(), repo, ScopeUncommitted, "")
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != "main" {
		t.Errorf("branch = %q, want main", status.Branch)
	}
	if status.Scope != ScopeUncommitted {
		t.Errorf("scope = %q, want uncommitted", status.Scope)
	}
	if status.Files == nil {
		t.Fatal("Files is nil, want []")
	}

	byPath := changesByPath(status.Files)
	want := map[string]string{
		"a.txt":     statusModified,
		"added.txt": statusAdded,
		"d.txt":     statusDeleted,
		"moved.txt": statusRenamed,
		"unt.txt":   statusUntracked,
		"bin.dat":   statusUntracked,
	}
	if len(status.Files) != len(want) {
		t.Fatalf("files = %v, want %d entries", status.Files, len(want))
	}
	for path, want := range want {
		got, ok := byPath[path]
		if !ok {
			t.Errorf("%s missing from files", path)
			continue
		}
		if got.Status != want {
			t.Errorf("%s status = %q, want %q", path, got.Status, want)
		}
	}
	if old := byPath["moved.txt"].OldPath; old != "b.txt" {
		t.Errorf("moved.txt old_path = %q, want b.txt", old)
	}
	if unt := byPath["unt.txt"]; unt.Add != 2 || unt.Binary {
		t.Errorf("unt.txt = %+v, want add 2 and not binary", unt)
	}
	if bin := byPath["bin.dat"]; !bin.Binary || bin.Add != 0 {
		t.Errorf("bin.dat = %+v, want binary with add 0", bin)
	}
	if status.Totals.Files != len(want) {
		t.Errorf("totals.files = %d, want %d", status.Totals.Files, len(want))
	}
	// The list is sorted by path.
	for i := 1; i < len(status.Files); i++ {
		if status.Files[i-1].Path > status.Files[i].Path {
			t.Fatalf("files not sorted: %v", status.Files)
		}
	}
}

func TestStatusUncommittedNoHEAD(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	writeFile(t, repo, "f.txt", "one\ntwo\n")
	runGit(t, repo, "add", "f.txt")

	r := NewRunner(allowPolicy(root))
	status, err := r.Status(context.Background(), repo, ScopeUncommitted, "")
	if err != nil {
		t.Fatal(err)
	}
	f, ok := changesByPath(status.Files)["f.txt"]
	if !ok {
		t.Fatalf("f.txt missing from files: %v", status.Files)
	}
	if f.Status != statusAdded || f.Add != 2 {
		t.Errorf("f.txt = %+v, want A with add 2", f)
	}
}

func TestStatusConflict(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "f.txt", "base\n")
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-qm", "base")
	runGit(t, repo, "checkout", "-q", "-b", "other")
	writeFile(t, repo, "f.txt", "other\n")
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-qm", "other")
	runGit(t, repo, "checkout", "-q", "main")
	writeFile(t, repo, "f.txt", "main\n")
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-qm", "main")
	if _, err := gitTry(repo, "merge", "other"); err == nil {
		t.Skip("merge did not conflict")
	}

	r := NewRunner(allowPolicy(root))
	status, err := r.Status(context.Background(), repo, ScopeUncommitted, "")
	if err != nil {
		t.Fatal(err)
	}
	f, ok := changesByPath(status.Files)["f.txt"]
	if !ok {
		t.Fatalf("f.txt missing from files: %v", status.Files)
	}
	if f.Status != statusConflict {
		t.Errorf("conflict status = %q, want %q", f.Status, statusConflict)
	}
}

func TestStatusAheadBehind(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "f.txt", "1\n")
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-qm", "c2")
	runGit(t, repo, "checkout", "-q", "-b", "feature", "main~1")
	runGit(t, repo, "branch", "--set-upstream-to=main", "feature")
	writeFile(t, repo, "g.txt", "2\n")
	runGit(t, repo, "add", "g.txt")
	runGit(t, repo, "commit", "-qm", "c3")

	r := NewRunner(allowPolicy(root))
	status, err := r.Status(context.Background(), repo, ScopeUncommitted, "")
	if err != nil {
		t.Fatal(err)
	}
	if status.Upstream == "" {
		t.Errorf("upstream is empty, want the local upstream")
	}
	if status.Ahead != 1 || status.Behind != 1 {
		t.Errorf("ahead/behind = %d/%d, want 1/1", status.Ahead, status.Behind)
	}
}

func TestStatusBranch(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	runGit(t, repo, "branch", "development")
	runGit(t, repo, "checkout", "-q", "-b", "feature")
	writeFile(t, repo, "one.txt", "1\n")
	runGit(t, repo, "add", "one.txt")
	runGit(t, repo, "commit", "-qm", "c2")
	writeFile(t, repo, "two.txt", "2\n")
	runGit(t, repo, "add", "two.txt")
	runGit(t, repo, "commit", "-qm", "c3")
	writeFile(t, repo, "three.txt", "3\n")
	runGit(t, repo, "add", "three.txt") // uncommitted

	r := NewRunner(allowPolicy(root))
	status, err := r.Status(context.Background(), repo, ScopeBranch, "development")
	if err != nil {
		t.Fatal(err)
	}
	if status.Base != "development" {
		t.Errorf("base = %q, want development", status.Base)
	}
	if status.MergeBase == "" {
		t.Errorf("merge_base is empty")
	}
	if len(status.Commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(status.Commits))
	}
	if status.Commits[0].Subject != "c3" || status.Commits[1].Subject != "c2" {
		t.Errorf("commits = %+v, want c3 then c2", status.Commits)
	}
	if status.Commits[0].Time.IsZero() {
		t.Errorf("commit time is zero")
	}
	if _, ok := changesByPath(status.Files)["three.txt"]; !ok {
		t.Errorf("three.txt (uncommitted) missing from files: %v", status.Files)
	}
	for _, f := range status.Files {
		if f.Status != statusAdded {
			t.Errorf("%s status = %q, want A", f.Path, f.Status)
		}
	}
}

func TestStatusBranchPrefersOrigin(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	runGit(t, repo, "branch", "development")
	sha := strings.TrimSpace(runGit(t, repo, "rev-parse", "development"))
	runGit(t, repo, "update-ref", "refs/remotes/origin/development", sha)

	r := NewRunner(allowPolicy(root))
	status, err := r.Status(context.Background(), repo, ScopeBranch, "development")
	if err != nil {
		t.Fatal(err)
	}
	if status.Base != "origin/development" {
		t.Errorf("base = %q, want origin/development", status.Base)
	}
}

func TestStatusLastCommit(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "f.txt", "one\ntwo\n")
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-qm", "c2")

	r := NewRunner(allowPolicy(root))
	status, err := r.Status(context.Background(), repo, ScopeLastCommit, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(status.Files))
	}
	f := status.Files[0]
	if f.Path != "f.txt" || f.Status != statusAdded || f.Add != 2 {
		t.Errorf("f.txt = %+v, want A with add 2", f)
	}
}

func TestStatusLastCommitRoot(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	writeFile(t, repo, "f.txt", "one\n")
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-qm", "root")

	r := NewRunner(allowPolicy(root))
	status, err := r.Status(context.Background(), repo, ScopeLastCommit, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Files) != 1 || status.Files[0].Status != statusAdded {
		t.Errorf("files = %+v, want one A", status.Files)
	}
}

func TestStatusCommitScope(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "f.txt", "one\n")
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-qm", "c2")
	writeFile(t, repo, "g.txt", "two\n")
	runGit(t, repo, "add", "g.txt")
	runGit(t, repo, "commit", "-qm", "c3")

	r := NewRunner(allowPolicy(root))
	sha := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD~1"))
	scope, err := ParseScope("commit:" + sha)
	if err != nil {
		t.Fatal(err)
	}
	status, err := r.Status(context.Background(), repo, scope, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Files) != 1 || status.Files[0].Path != "f.txt" {
		t.Errorf("files = %+v, want f.txt", status.Files)
	}

	unknown, err := ParseScope("commit:deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Status(context.Background(), repo, unknown, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Status(unknown) = %v, want ErrNotFound", err)
	}
}

func TestStatusNotARepo(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(allowPolicy(root))

	if _, err := r.Status(context.Background(), plain, ScopeUncommitted, ""); !errors.Is(err, ErrNotARepo) {
		t.Fatalf("Status(plain) = %v, want ErrNotARepo", err)
	}
}

func TestStatusForbiddenPath(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(t.TempDir()))

	if _, err := r.Status(context.Background(), repo, ScopeUncommitted, ""); !errors.Is(err, ErrForbiddenPath) {
		t.Fatalf("Status(forbidden) = %v, want ErrForbiddenPath", err)
	}
}

func TestStatusInvalidScope(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))

	if _, err := r.Status(context.Background(), repo, Scope("head"), ""); err == nil {
		t.Fatal("Status(unknown scope) = nil error, want error")
	}
}

func TestParseScope(t *testing.T) {
	valid := []string{
		"uncommitted",
		"branch",
		"last_commit",
		"commit:abcd",
		"commit:" + strings.Repeat("a", 40),
	}
	for _, s := range valid {
		if _, err := ParseScope(s); err != nil {
			t.Errorf("ParseScope(%q) = %v, want nil", s, err)
		}
	}
	invalid := []string{
		"",
		"head",
		"commit",
		"commit:",
		"commit:xyz",
		"commit:ABCD",
		"commit:" + strings.Repeat("a", 41),
		"Commit:abcd",
	}
	for _, s := range invalid {
		if _, err := ParseScope(s); err == nil {
			t.Errorf("ParseScope(%q) = nil, want error", s)
		}
	}
}
