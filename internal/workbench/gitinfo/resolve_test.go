package gitinfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench/preview"
)

func TestResolveMainWorktree(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))

	info, err := r.Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Main {
		t.Errorf("Main = false, want true")
	}
	if info.Detached {
		t.Errorf("Detached = true, want false")
	}
	if info.Branch != "main" || info.Short != "main" {
		t.Errorf("branch = %q/%q, want main/main", info.Branch, info.Short)
	}
	if info.Head == "" {
		t.Errorf("Head is empty")
	}
	if want := filepath.Join(preview.Resolve(repo), ".git"); info.Repo != want {
		t.Errorf("Repo = %q, want %q", info.Repo, want)
	}
	if want := preview.Resolve(repo); info.Worktree != want {
		t.Errorf("Worktree = %q, want %q", info.Worktree, want)
	}
	if info.Color != Color(info.Worktree) {
		t.Errorf("Color = %q, want %q", info.Color, Color(info.Worktree))
	}
}

func TestResolveLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	wt := filepath.Join(root, "repo-worktree", "router")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "worktree", "add", "-b", "feature/x", wt)
	r := NewRunner(allowPolicy(root))

	main, err := r.Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	info, err := r.Resolve(context.Background(), wt)
	if err != nil {
		t.Fatal(err)
	}
	if info.Main {
		t.Errorf("Main = true, want false for a linked worktree")
	}
	if info.Repo != main.Repo {
		t.Errorf("Repo = %q, want the main repo id %q", info.Repo, main.Repo)
	}
	if info.Branch != "feature/x" || info.Short != "x" {
		t.Errorf("branch = %q/%q, want feature/x/x", info.Branch, info.Short)
	}
	if want := preview.Resolve(wt); info.Worktree != want {
		t.Errorf("Worktree = %q, want %q", info.Worktree, want)
	}
}

func TestResolveDetached(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	runGit(t, repo, "checkout", "-q", "--detach")
	r := NewRunner(allowPolicy(root))

	info, err := r.Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Detached {
		t.Errorf("Detached = false, want true")
	}
	if info.Branch != "detached" || info.Short != "detached" {
		t.Errorf("branch = %q/%q, want detached/detached", info.Branch, info.Short)
	}
	if info.Head == "" {
		t.Errorf("Head is empty")
	}
}

func TestResolveEmptyRepo(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	r := NewRunner(allowPolicy(root))

	info, err := r.Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if info.Head != "" {
		t.Errorf("Head = %q, want empty for a repository with no commits", info.Head)
	}
	if info.Branch != "main" || !info.Main {
		t.Errorf("branch = %q, Main = %v, want main/true", info.Branch, info.Main)
	}
}

func TestResolveNotARepo(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(allowPolicy(root))

	if _, err := r.Resolve(context.Background(), plain); !errors.Is(err, ErrNotARepo) {
		t.Fatalf("Resolve(plain) = %v, want ErrNotARepo", err)
	}
}

func TestResolveForbiddenPath(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	initRepo(t, repo)
	// The policy's home and temp are another folder, so the repository path is
	// outside it (REQ-WG-001).
	r := NewRunner(allowPolicy(t.TempDir()))

	if _, err := r.Resolve(context.Background(), repo); !errors.Is(err, ErrForbiddenPath) {
		t.Fatalf("Resolve(forbidden) = %v, want ErrForbiddenPath", err)
	}
}

func TestResolveCache(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))

	now := time.Now()
	r.now = func() time.Time { return now }

	first, err := r.Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if first.Branch != "main" {
		t.Fatalf("branch = %q, want main", first.Branch)
	}

	runGit(t, repo, "checkout", "-q", "-b", "other")

	// Inside the window the cached value comes back and git is not run again.
	cached, err := r.Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Branch != "main" {
		t.Errorf("cached branch = %q, want main", cached.Branch)
	}

	// Past 5 s the directory is resolved again.
	now = now.Add(cacheTTL + time.Second)
	fresh, err := r.Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Branch != "other" {
		t.Errorf("fresh branch = %q, want other", fresh.Branch)
	}
}
