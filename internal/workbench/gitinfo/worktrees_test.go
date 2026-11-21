package gitinfo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// resolveDir makes a temp directory with its symlinks resolved, so a path built
// under it matches what git reports (on macOS a temp folder sits under /var, a
// symlink to /private/var).
func resolveDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWorktrees(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	// .claude is ignored, so the agent worktree below it keeps the main
	// worktree clean.
	writeFile(t, repo, ".gitignore", ".claude/\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "ignore")
	runGit(t, repo, "branch", "development")

	rootDir := filepath.Join(root, "repo-worktree")
	container := filepath.Join(rootDir, "x")
	sibling := filepath.Join(root, "repo-sibling")
	agent := filepath.Join(repo, ".claude", "worktrees", "agent-1")
	ahead := filepath.Join(rootDir, "ahead")
	merged := filepath.Join(rootDir, "merged")
	gone := filepath.Join(rootDir, "gone")

	for _, dir := range []string{rootDir, filepath.Dir(agent)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, repo, "worktree", "add", "-b", "feature/x", container)
	runGit(t, repo, "worktree", "add", "-b", "feature/sib", sibling)
	runGit(t, repo, "worktree", "add", "-b", "agent/one", agent)
	runGit(t, repo, "branch", "merged-branch")
	runGit(t, repo, "worktree", "add", merged, "merged-branch")
	runGit(t, repo, "worktree", "add", "-b", "ahead-branch", ahead)
	runGit(t, repo, "worktree", "add", "-b", "gone-branch", gone)

	// The ahead branch has one commit more than development.
	writeFile(t, ahead, "new.txt", "x\n")
	runGit(t, ahead, "add", "new.txt")
	runGit(t, ahead, "commit", "-qm", "ahead commit")

	// The container worktree has one uncommitted file.
	writeFile(t, container, "dirty.txt", "d\n")

	// A removed folder makes a worktree prunable.
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(allowPolicy(root))
	list, err := r.Worktrees(context.Background(), repo, "development", rootDir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repo, ".git"); list.Repo != want {
		t.Errorf("repo = %q, want %q", list.Repo, want)
	}
	if list.Base != "development" {
		t.Errorf("base = %q, want development (the local ref)", list.Base)
	}
	if list.Root != rootDir {
		t.Errorf("root = %q, want %q", list.Root, rootDir)
	}
	if len(list.Worktrees) != 7 {
		t.Fatalf("worktrees = %d, want 7: %+v", len(list.Worktrees), list.Worktrees)
	}
	byName := map[string]Worktree{}
	for _, w := range list.Worktrees {
		byName[w.Name] = w
		if w.Color != Color(w.Path) {
			t.Errorf("%s color = %q, want %q", w.Name, w.Color, Color(w.Path))
		}
	}

	main := byName["repo"]
	if !main.Main || main.Location != "main" {
		t.Errorf("main = %+v, want Main with location main", main)
	}
	if main.Branch != "main" || main.Dirty != 0 || main.Locked || main.Prunable || main.Agent {
		t.Errorf("main fields = %+v", main)
	}
	if main.Path != repo {
		t.Errorf("main path = %q, want %q", main.Path, repo)
	}
	if len(main.Head) != 7 || main.HeadTime.IsZero() || main.Error != "" {
		t.Errorf("main head/time/error = %+v", main)
	}

	x := byName["x"]
	if x.Location != "container" || x.Main || x.Branch != "feature/x" || x.Dirty != 1 {
		t.Errorf("x = %+v, want container/feature-x/dirty 1", x)
	}
	if !x.Merged || x.Ahead != 0 || x.Error != "" {
		t.Errorf("x merged/ahead/error = %+v", x)
	}
	if x.Path != container {
		t.Errorf("x path = %q, want %q", x.Path, container)
	}

	sib := byName["repo-sibling"]
	if sib.Location != "sibling" || sib.Branch != "feature/sib" {
		t.Errorf("sibling = %+v, want sibling/feature-sib", sib)
	}
	if sib.Path != sibling {
		t.Errorf("sibling path = %q, want %q", sib.Path, sibling)
	}

	ag := byName["agent-1"]
	if !ag.Agent || ag.Location != "other" || ag.Branch != "agent/one" {
		t.Errorf("agent = %+v, want agent/other/agent-one", ag)
	}

	ah := byName["ahead"]
	if ah.Ahead != 1 || ah.Merged || ah.Location != "container" {
		t.Errorf("ahead = %+v, want ahead 1, not merged, container", ah)
	}

	mg := byName["merged"]
	if !mg.Merged || mg.Ahead != 0 {
		t.Errorf("merged = %+v, want merged with ahead 0", mg)
	}

	gn := byName["gone"]
	if !gn.Prunable || gn.Location != "container" {
		t.Errorf("gone = %+v, want prunable container", gn)
	}
	if gn.Dirty != 0 || gn.Ahead != 0 || !gn.HeadTime.IsZero() || gn.Error != "" {
		t.Errorf("gone status = %+v, want skipped status work", gn)
	}
}

func TestWorktreesBasePrefersOrigin(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	runGit(t, repo, "branch", "development")
	sha := strings.TrimSpace(runGit(t, repo, "rev-parse", "development"))
	runGit(t, repo, "update-ref", "refs/remotes/origin/development", sha)

	r := NewRunner(allowPolicy(root))
	list, err := r.Worktrees(context.Background(), repo, "development", "")
	if err != nil {
		t.Fatal(err)
	}
	if list.Base != "origin/development" {
		t.Errorf("base = %q, want origin/development", list.Base)
	}
}

func TestWorktreesForbiddenPath(t *testing.T) {
	allowed := resolveDir(t)
	repo := filepath.Join(allowed, "repo")
	initRepo(t, repo)
	runGit(t, repo, "branch", "development")

	// A worktree outside the allowed folder is listed but not inspected.
	outside := filepath.Join(resolveDir(t), "wt")
	runGit(t, repo, "worktree", "add", "-b", "out-branch", outside)

	r := NewRunner(allowPolicy(allowed))
	list, err := r.Worktrees(context.Background(), repo, "development", "")
	if err != nil {
		t.Fatal(err)
	}
	var found *Worktree
	for i := range list.Worktrees {
		if list.Worktrees[i].Name == "wt" {
			found = &list.Worktrees[i]
		}
	}
	if found == nil {
		t.Fatalf("outside worktree missing: %+v", list.Worktrees)
	}
	if found.Error != "forbidden_path" {
		t.Errorf("error = %q, want forbidden_path", found.Error)
	}
	if found.Dirty != 0 || found.Ahead != 0 || !found.HeadTime.IsZero() {
		t.Errorf("forbidden worktree ran status work: %+v", *found)
	}
	// Merged still comes from the one-time branch list.
	if !found.Merged {
		t.Errorf("merged = false, want true from the branch list")
	}
}

func TestWorktreesCacheAndInvalidate(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))

	now := time.Now()
	r.now = func() time.Time { return now }

	info, err := r.Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	repoID := info.Repo

	first, err := r.Worktrees(context.Background(), repo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := dirtyOf(t, first, repo); got != 0 {
		t.Fatalf("first dirty = %d, want 0", got)
	}

	writeFile(t, repo, "new.txt", "x\n")

	cached, err := r.Worktrees(context.Background(), repo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := dirtyOf(t, cached, repo); got != 0 {
		t.Errorf("cached dirty = %d, want 0 (the change is not seen yet)", got)
	}

	r.InvalidateWorktrees(repoID)
	fresh, err := r.Worktrees(context.Background(), repo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := dirtyOf(t, fresh, repo); got != 1 {
		t.Errorf("fresh dirty = %d, want 1 after InvalidateWorktrees", got)
	}

	// An unknown repository is safe to invalidate.
	r.InvalidateWorktrees("does-not-exist")

	// Past the 30 s window the next call reads git again.
	writeFile(t, repo, "second.txt", "y\n")
	now = now.Add(worktreesTTL + time.Second)
	expired, err := r.Worktrees(context.Background(), repo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := dirtyOf(t, expired, repo); got != 2 {
		t.Errorf("expired dirty = %d, want 2 after the window", got)
	}
}

func TestWorktreeLocation(t *testing.T) {
	main := "/x/repo"
	root := "/x/repo-worktree"
	cases := []struct {
		path string
		want string
	}{
		{"/x/repo", "main"},
		{"/x/repo-worktree/a", "container"},
		{"/x/repo-sibling", "sibling"},
		{"/elsewhere/wt", "other"},
	}
	for _, c := range cases {
		if got := worktreeLocation(c.path, main, root, c.path == main); got != c.want {
			t.Errorf("worktreeLocation(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestAgentWorktree(t *testing.T) {
	if !agentWorktree("/x/repo/.claude/worktrees/agent-1") {
		t.Errorf("agent worktree not detected")
	}
	if agentWorktree("/x/repo/feature") {
		t.Errorf("a plain worktree was detected as an agent worktree")
	}
}

// dirtyOf returns the dirty count of the main worktree of a list.
func dirtyOf(t *testing.T, list WorktreeList, repo string) int {
	t.Helper()
	name := filepath.Base(repo)
	for _, w := range list.Worktrees {
		if w.Name == name {
			return w.Dirty
		}
	}
	t.Fatalf("worktree %q missing from %+v", name, list.Worktrees)
	return 0
}
