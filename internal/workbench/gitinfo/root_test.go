package gitinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestDetectRoot(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	initRepo(t, main)
	wt := filepath.Join(root, "wt")
	runGit(t, main, "worktree", "add", "-b", "feature/x", wt)

	r := NewRunner(allowPolicy(root))
	info, err := r.DetectRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != filepath.Clean(root) {
		t.Errorf("Path = %q, want %q", info.Path, filepath.Clean(root))
	}
	if info.Name != filepath.Base(root) {
		t.Errorf("Name = %q, want %q", info.Name, filepath.Base(root))
	}
	// "main" holds a .git folder and "wt" a .git file (a linked worktree).
	if want := []string{"main", "wt"}; !slices.Equal(info.Repos, want) {
		t.Errorf("Repos = %v, want %v", info.Repos, want)
	}
}

func TestDetectRootNoChildRepos(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "CLAUDE.md", "#\n")
	r := NewRunner(allowPolicy(root))

	if _, err := r.DetectRoot(context.Background(), root); !errors.Is(err, ErrNotARoot) {
		t.Fatalf("DetectRoot = %v, want ErrNotARoot", err)
	}
}

func TestDetectRootInsideRepo(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(allowPolicy(root))

	for _, dir := range []string{repo, sub} {
		if _, err := r.DetectRoot(context.Background(), dir); !errors.Is(err, ErrNotARoot) {
			t.Errorf("DetectRoot(%s) = %v, want ErrNotARoot", dir, err)
		}
	}
}

func TestDetectRootHiddenChildSkipped(t *testing.T) {
	root := t.TempDir()
	initRepo(t, filepath.Join(root, ".hidden"))
	initRepo(t, filepath.Join(root, "visible"))
	r := NewRunner(allowPolicy(root))

	info, err := r.DetectRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"visible"}; !slices.Equal(info.Repos, want) {
		t.Errorf("Repos = %v, want %v (hidden child skipped)", info.Repos, want)
	}
}

func TestDetectRootCache(t *testing.T) {
	root := t.TempDir()
	initRepo(t, filepath.Join(root, "a"))
	initRepo(t, filepath.Join(root, "b"))
	r := NewRunner(allowPolicy(root))

	now := time.Now()
	r.now = func() time.Time { return now }

	first, err := r.DetectRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b"}; !slices.Equal(first.Repos, want) {
		t.Fatalf("Repos = %v, want %v", first.Repos, want)
	}

	// A new repo appears, but inside the window the cached root comes back.
	initRepo(t, filepath.Join(root, "c"))
	cached, err := r.DetectRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b"}; !slices.Equal(cached.Repos, want) {
		t.Errorf("cached Repos = %v, want %v", cached.Repos, want)
	}

	// Past 5 s the folder is detected again.
	now = now.Add(cacheTTL + time.Second)
	fresh, err := r.DetectRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c"}; !slices.Equal(fresh.Repos, want) {
		t.Errorf("fresh Repos = %v, want %v", fresh.Repos, want)
	}
}

func TestDetectRootForbiddenPath(t *testing.T) {
	r := NewRunner(allowPolicy(t.TempDir()))

	if _, err := r.DetectRoot(context.Background(), t.TempDir()); !errors.Is(err, ErrForbiddenPath) {
		t.Fatalf("DetectRoot(forbidden) = %v, want ErrForbiddenPath", err)
	}
}

func TestRepoDisplayName(t *testing.T) {
	cases := []struct{ root, folder, want string }{
		{"sxored", "sxored-be-ocrengine", "be-ocrengine"},
		{"sxored", "sxored-be-ocrai-worktree", "be-ocrai-worktree"},
		{"sxored", "sxored", "sxored"},
		{"sxored", "sxored-", "sxored-"},
		{"sxored", "other-repo", "other-repo"},
		{"rdms", "be-rdms", "be-rdms"},
		{"sxored", "rf-sxored-be", "rf-sxored-be"},
	}
	for _, c := range cases {
		if got := RepoDisplayName(c.root, c.folder); got != c.want {
			t.Errorf("RepoDisplayName(%q, %q) = %q, want %q", c.root, c.folder, got, c.want)
		}
	}
}

func TestScanRepoClean(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "sxored-be-ocrai")
	initRepo(t, repo)
	writeFile(t, repo, "a.txt", "one\ntwo\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "a")

	r := NewRunner(allowPolicy(root))
	s := r.ScanRepo(context.Background(), "sxored", repo)
	if s.Error != "" {
		t.Fatalf("Error = %q, want empty", s.Error)
	}
	if s.Name != "sxored-be-ocrai" || s.Display != "be-ocrai" {
		t.Errorf("Name/Display = %q/%q, want sxored-be-ocrai/be-ocrai", s.Name, s.Display)
	}
	if s.Worktree != filepath.Clean(repo) {
		t.Errorf("Worktree = %q, want %q", s.Worktree, filepath.Clean(repo))
	}
	if s.Branch != "main" || s.Short != "main" {
		t.Errorf("branch = %q/%q, want main/main", s.Branch, s.Short)
	}
	if s.Detached {
		t.Errorf("Detached = true, want false")
	}
	if s.Head == "" {
		t.Errorf("Head is empty, want the short HEAD")
	}
	if s.Color != Color(filepath.Clean(repo)) {
		t.Errorf("Color = %q, want %q", s.Color, Color(filepath.Clean(repo)))
	}
	if s.Changed != 0 || s.Additions != 0 || s.Deletions != 0 {
		t.Errorf("changed/add/del = %d/%d/%d, want 0/0/0", s.Changed, s.Additions, s.Deletions)
	}
	if s.Etag == "" {
		t.Errorf("Etag is empty")
	}
}

func TestScanRepoChanges(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "a.txt", "one\ntwo\nthree\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "a")

	writeFile(t, repo, "a.txt", "one\nTWO\nthree\n") // modified
	writeFile(t, repo, "new/f1.txt", "1\n")          // an untracked folder
	writeFile(t, repo, "new/f2.txt", "2\n")
	writeFile(t, repo, "new/f3.txt", "3\n")

	r := NewRunner(allowPolicy(root))
	s := r.ScanRepo(context.Background(), "repo", repo)
	if s.Error != "" {
		t.Fatalf("Error = %q, want empty", s.Error)
	}
	// The modified file is one entry and the whole untracked folder is one more
	// with --untracked-files=normal.
	if s.Changed != 2 {
		t.Errorf("Changed = %d, want 2", s.Changed)
	}
}

func TestScanRepoNumstat(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "a.txt", "one\ntwo\nthree\n")
	writeFile(t, repo, "b.txt", "x\ny\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "a")

	writeFile(t, repo, "a.txt", "one\ntwo\nthree\nfour\nfive\n")    // +2
	if err := os.Remove(filepath.Join(repo, "b.txt")); err != nil { // -2
		t.Fatal(err)
	}

	r := NewRunner(allowPolicy(root))
	s := r.ScanRepo(context.Background(), "repo", repo)
	if s.Changed != 2 {
		t.Errorf("Changed = %d, want 2", s.Changed)
	}
	if s.Additions != 2 || s.Deletions != 2 {
		t.Errorf("add/del = %d/%d, want 2/2", s.Additions, s.Deletions)
	}
}

func TestScanRepoDetached(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "a.txt", "one\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "a")
	runGit(t, repo, "checkout", "-q", "--detach")

	r := NewRunner(allowPolicy(root))
	s := r.ScanRepo(context.Background(), "repo", repo)
	if !s.Detached {
		t.Errorf("Detached = false, want true")
	}
	if s.Branch != "detached" || s.Short != "detached" {
		t.Errorf("branch = %q/%q, want detached/detached", s.Branch, s.Short)
	}
	if s.Head == "" {
		t.Errorf("Head is empty")
	}
}

func TestScanRepoNoCommits(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	writeFile(t, repo, "f.txt", "one\n")
	runGit(t, repo, "add", "f.txt")

	r := NewRunner(allowPolicy(root))
	s := r.ScanRepo(context.Background(), "repo", repo)
	if s.Error != "" {
		t.Fatalf("Error = %q, want empty", s.Error)
	}
	if s.Head != "" {
		t.Errorf("Head = %q, want empty for a repo with no commits", s.Head)
	}
	if s.Branch != "main" {
		t.Errorf("Branch = %q, want main", s.Branch)
	}
	if s.Changed < 1 {
		t.Errorf("Changed = %d, want at least the staged file", s.Changed)
	}
	// The numstat pass is skipped without a commit.
	if s.Additions != 0 || s.Deletions != 0 {
		t.Errorf("add/del = %d/%d, want 0/0", s.Additions, s.Deletions)
	}
}

func TestScanRepoEtag(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, "a.txt", "one\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "a")

	r := NewRunner(allowPolicy(root))
	first := r.ScanRepo(context.Background(), "repo", repo)
	again := r.ScanRepo(context.Background(), "repo", repo)
	if first.Etag == "" {
		t.Fatal("Etag is empty")
	}
	if first.Etag != again.Etag {
		t.Errorf("etag changed without a change: %q vs %q", first.Etag, again.Etag)
	}

	writeFile(t, repo, "a.txt", "one\ntwo\n")
	changed := r.ScanRepo(context.Background(), "repo", repo)
	if changed.Etag == first.Etag {
		t.Errorf("etag = %q after a change, want a new value", changed.Etag)
	}
}

func TestListFolderOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "Alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "zebra.txt", "z\n")
	writeFile(t, root, "Apple.txt", "a\n")
	writeFile(t, root, ".env", "SECRET=1\n")

	r := NewRunner(allowPolicy(root))
	listing, err := r.ListFolder(context.Background(), root, func(string) (RepoSummary, bool) {
		return RepoSummary{}, false
	})
	if err != nil {
		t.Fatal(err)
	}
	if listing.Dir != filepath.Clean(root) {
		t.Errorf("Dir = %q, want %q", listing.Dir, filepath.Clean(root))
	}
	if listing.Truncated {
		t.Errorf("Truncated = true, want false")
	}
	names := make([]string, len(listing.Entries))
	for i, e := range listing.Entries {
		names[i] = e.Name
	}
	// Folders first then files, each case-insensitively sorted; the secret
	// folder .ssh and the .env file are refused by the policy.
	want := []string{"Alpha", "beta", "Apple.txt", "zebra.txt"}
	if !slices.Equal(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	if kind := listing.Entries[0].Kind; kind != treeDirKind {
		t.Errorf("first entry kind = %q, want %q", kind, treeDirKind)
	}
	if kind := listing.Entries[2].Kind; kind != treeFileKind {
		t.Errorf("first file kind = %q, want %q", kind, treeFileKind)
	}
	if listing.Entries[2].Size == 0 {
		t.Errorf("file size = 0, want Apple.txt's size")
	}
	if listing.Entries[2].Modified == "" {
		t.Errorf("file modified is empty")
	}
}

func TestListFolderRepoMarks(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "sxored-be-ocrai")
	initRepo(t, repo)

	var calls []string
	r := NewRunner(allowPolicy(root))
	listing, err := r.ListFolder(context.Background(), root, func(child string) (RepoSummary, bool) {
		calls = append(calls, child)
		return RepoSummary{Short: "ocr-v2", Color: "#E0805B", Changed: 9}, child == "sxored-be-ocrai"
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"sxored-be-ocrai"}; !slices.Equal(calls, want) {
		t.Errorf("callback children = %v, want %v", calls, want)
	}
	if len(listing.Entries) != 1 {
		t.Fatalf("entries = %v, want one child repo", listing.Entries)
	}
	entry := listing.Entries[0]
	if !entry.Repo || entry.Kind != treeDirKind {
		t.Errorf("entry = %+v, want a repo dir", entry)
	}
	if entry.Short != "ocr-v2" || entry.Color != "#E0805B" || entry.Changed != 9 {
		t.Errorf("repo fields = %+v, want ocr-v2/#E0805B/9", entry)
	}
	if entry.Name == ".git" {
		t.Errorf(".git listed")
	}
}

func TestListFolderSkipsGit(t *testing.T) {
	root := t.TempDir()
	// An empty .git folder is not a valid repository, so git reports "not a git
	// repository" and the folder is listed; ".git" itself is never a row.
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "readme.md", "#\n")

	r := NewRunner(allowPolicy(root))
	listing, err := r.ListFolder(context.Background(), root, func(string) (RepoSummary, bool) {
		return RepoSummary{}, false
	})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(listing.Entries))
	for i, e := range listing.Entries {
		names[i] = e.Name
	}
	if slices.Contains(names, ".git") {
		t.Errorf("entries = %v, want .git skipped", names)
	}
	if !slices.Contains(names, "readme.md") {
		t.Errorf("entries = %v, want readme.md", names)
	}
}

func TestListFolderTruncated(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < folderMaxEntries+1; i++ {
		name := filepath.Join(root, fmt.Sprintf("f%04d", i))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRunner(allowPolicy(root))
	listing, err := r.ListFolder(context.Background(), root, func(string) (RepoSummary, bool) {
		return RepoSummary{}, false
	})
	if err != nil {
		t.Fatal(err)
	}
	if !listing.Truncated {
		t.Errorf("Truncated = false, want true")
	}
	if len(listing.Entries) != folderMaxEntries {
		t.Errorf("entries = %d, want %d", len(listing.Entries), folderMaxEntries)
	}
}

func TestListFolderInsideRepo(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))

	if _, err := r.ListFolder(context.Background(), repo, nil); !errors.Is(err, ErrInsideRepo) {
		t.Fatalf("ListFolder(repo) = %v, want ErrInsideRepo", err)
	}
}

func TestListFolderForbiddenPath(t *testing.T) {
	r := NewRunner(allowPolicy(t.TempDir()))

	if _, err := r.ListFolder(context.Background(), t.TempDir(), nil); !errors.Is(err, ErrForbiddenPath) {
		t.Fatalf("ListFolder(forbidden) = %v, want ErrForbiddenPath", err)
	}
}
