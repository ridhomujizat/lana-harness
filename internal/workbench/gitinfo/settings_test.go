package gitinfo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ptrString and ptrBool build the pointer fields of a SettingsPatch.
func ptrString(s string) *string { return &s }
func ptrBool(b bool) *bool       { return &b }

func TestDefaultSettingsDevelopment(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	runGit(t, repo, "branch", "development")

	r := NewRunner(allowPolicy(root))
	s := r.DefaultSettings(context.Background(), repo)
	if s.Base != "development" {
		t.Errorf("base = %q, want development", s.Base)
	}
	if s.WorktreeRoot != WorktreeRoot(repo) {
		t.Errorf("worktree_root = %q, want %q", s.WorktreeRoot, WorktreeRoot(repo))
	}
	if !s.WarnShared {
		t.Errorf("warn_shared = false, want true")
	}
}

func TestDefaultSettingsMain(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)

	r := NewRunner(allowPolicy(root))
	s := r.DefaultSettings(context.Background(), repo)
	if s.Base != "main" {
		t.Errorf("base = %q, want main", s.Base)
	}
}

func TestDefaultSettingsOriginHead(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	sha := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "update-ref", "refs/remotes/origin/trunk", sha)
	runGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")

	r := NewRunner(allowPolicy(root))
	s := r.DefaultSettings(context.Background(), repo)
	if s.Base != "trunk" {
		t.Errorf("base = %q, want trunk", s.Base)
	}
}

func TestSettingsStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbench-git.json")
	store := NewSettingsStore(path)
	repo := "/repo/a"
	defaults := Settings{Base: "main", WorktreeRoot: "/root", WarnShared: true}

	if got := store.Get(repo, defaults); got != defaults {
		t.Fatalf("empty Get = %+v, want %+v", got, defaults)
	}

	put, err := store.Put(repo, SettingsPatch{Base: ptrString("development")})
	if err != nil {
		t.Fatal(err)
	}
	if put.Base != "development" || !put.WarnShared {
		t.Errorf("Put = %+v, want base development and warn_shared true", put)
	}

	got := store.Get(repo, defaults)
	if got.Base != "development" || got.WorktreeRoot != "/root" || !got.WarnShared {
		t.Errorf("Get after Put = %+v, want base development, root /root, warn true", got)
	}

	if _, err := store.Put(repo, SettingsPatch{WarnShared: ptrBool(false)}); err != nil {
		t.Fatal(err)
	}
	got = store.Get(repo, defaults)
	if got.WarnShared {
		t.Errorf("warn_shared = true, want false after the patch")
	}
	if got.Base != "development" {
		t.Errorf("base = %q, want development kept", got.Base)
	}

	// A new store on the same file reads what was written.
	again := NewSettingsStore(path)
	got = again.Get(repo, defaults)
	if got.Base != "development" || got.WarnShared {
		t.Errorf("reloaded = %+v, want base development and warn_shared false", got)
	}
}

func TestSettingsStoreDefaultsWarnShared(t *testing.T) {
	store := NewSettingsStore(filepath.Join(t.TempDir(), "x.json"))
	if got := store.Get("/repo", Settings{}); !got.WarnShared {
		t.Errorf("warn_shared = false, want true with no entry")
	}
}

func TestSettingsStoreFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	store := NewSettingsStore(path)
	if _, err := store.Put("/repo", SettingsPatch{Base: ptrString("main")}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %o, want 600", got)
	}
}
