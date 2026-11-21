package gitinfo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/workbench/preview"
)

// testGitEnv is the environment the test repositories use, so commits never
// depend on the machine's git configuration.
func testGitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
}

// runGit runs git in dir and fails the test when it exits non-zero.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = testGitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// initRepo makes a repository on main with one commit.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
}

// allowPolicy is a path policy that allows the given folders: the first is both
// the home folder and a temp folder, so their resolved paths pass
// (REQ-WG-001).
func allowPolicy(dirs ...string) preview.Policy {
	return preview.NewPolicy(dirs[0], dirs)
}

func TestGitFailed(t *testing.T) {
	r := NewRunner(allowPolicy(t.TempDir()))
	_, err := r.Git(context.Background(), t.TempDir(), "rev-parse", "HEAD")
	var ge *GitError
	if !errors.As(err, &ge) {
		t.Fatalf("Git error = %v, want *GitError", err)
	}
	if ge.Code != "git_failed" || ge.ExitCode == 0 || ge.Message == "" {
		t.Fatalf("GitError = %#v", ge)
	}
}

func TestGitSuccess(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))

	out, err := r.Git(context.Background(), repo, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "true" {
		t.Fatalf("rev-parse = %q, want true", got)
	}
}

func TestCheckPath(t *testing.T) {
	allowed := t.TempDir()
	r := NewRunner(allowPolicy(allowed))

	if err := r.CheckPath(allowed); err != nil {
		t.Fatalf("CheckPath(allowed) = %v", err)
	}
	if err := r.CheckPath(t.TempDir()); !errors.Is(err, ErrForbiddenPath) {
		t.Fatalf("CheckPath(other) = %v, want ErrForbiddenPath", err)
	}
	if err := r.CheckPath(filepath.Join(allowed, ".env")); !errors.Is(err, ErrForbiddenPath) {
		t.Fatalf("CheckPath(.env) = %v, want ErrForbiddenPath", err)
	}
}
