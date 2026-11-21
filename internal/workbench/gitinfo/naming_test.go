package gitinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestColor(t *testing.T) {
	for _, path := range []string{"/a/b", "/Users/me/code/lana-harness", "/x"} {
		c := Color(path)
		if c != Color(path) {
			t.Errorf("Color(%q) is not stable", path)
		}
		found := false
		for _, p := range Palette {
			if p == c {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Color(%q) = %q, not in Palette", path, c)
		}
	}
	if Color("/a/b/") != Color("/a/b") {
		t.Errorf("Color does not ignore a trailing slash")
	}
}

func TestShortBranch(t *testing.T) {
	cases := map[string]string{
		"feature/router":  "router",
		"dev/integration": "integration",
		"main":            "main",
		"detached":        "detached",
		"feature/x/y":     "x/y",
	}
	for in, want := range cases {
		if got := ShortBranch(in); got != want {
			t.Errorf("ShortBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorktreeRoot(t *testing.T) {
	if got, want := WorktreeRoot("/Users/me/code/lana-harness"), "/Users/me/code/lana-harness-worktree"; got != want {
		t.Errorf("WorktreeRoot = %q, want %q", got, want)
	}
}

func TestFolderName(t *testing.T) {
	root := t.TempDir()
	mkdir := func(name string) {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if got, want := FolderName(root, "main"), filepath.Join(root, "main"); got != want {
		t.Fatalf("FolderName(main) = %q, want %q", got, want)
	}
	if got, want := FolderName(root, "feature/router"), filepath.Join(root, "router"); got != want {
		t.Fatalf("FolderName(feature/router) = %q, want %q", got, want)
	}

	// The short name is taken: the whole branch, with "/" as "-".
	mkdir("router")
	if got, want := FolderName(root, "feature/router"), filepath.Join(root, "feature-router"); got != want {
		t.Fatalf("FolderName with a taken short name = %q, want %q", got, want)
	}

	// Both are taken: a number is appended until the path is free.
	mkdir("feature-router")
	if got, want := FolderName(root, "feature/router"), filepath.Join(root, "feature-router-2"); got != want {
		t.Fatalf("FolderName with both taken = %q, want %q", got, want)
	}
	mkdir("feature-router-2")
	if got, want := FolderName(root, "feature/router"), filepath.Join(root, "feature-router-3"); got != want {
		t.Fatalf("FolderName with both taken twice = %q, want %q", got, want)
	}
}
