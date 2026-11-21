package gitinfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initWithOrigin makes a repository with a bare "origin" remote it pushed main
// to, so its branches can be seen as merged into a remote.
func initWithOrigin(t *testing.T, root, repo string) string {
	t.Helper()
	initRepo(t, repo)
	runGit(t, root, "init", "--bare", "origin.git")
	bare := filepath.Join(root, "origin.git")
	runGit(t, repo, "remote", "add", "origin", bare)
	runGit(t, repo, "push", "-u", "origin", "main")
	return bare
}

// addWorktree adds a worktree on a new branch from HEAD, creating its parent
// folder first.
func addWorktree(t *testing.T, repo, path, branch string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "worktree", "add", "-b", branch, path)
}

// pushedWorktree adds a worktree on a new branch and pushes the branch to
// origin, so it has no unpushed commits.
func pushedWorktree(t *testing.T, repo, path, branch string) {
	t.Helper()
	addWorktree(t, repo, path, branch)
	runGit(t, path, "push", "-u", "origin", branch)
}

func TestCreateWorktreeNewBranch(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, ".gitignore", ".env\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "ignore")
	writeFile(t, repo, ".env", "SECRET=1\n")

	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "thing")
	wt, err := r.CreateWorktree(context.Background(), CreateInput{
		Repo:      repo,
		Branch:    "feature/thing",
		NewBranch: true,
		Base:      "main",
		Path:      path,
		Copy:      []string{".env"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if wt.Path != path || wt.Branch != "feature/thing" || wt.Main {
		t.Fatalf("worktree = %+v, want %s on feature/thing", wt, path)
	}
	if filepath.Dir(path) != WorktreeRoot(repo) {
		t.Errorf("container = %q, want %q", filepath.Dir(path), WorktreeRoot(repo))
	}
	if got := strings.TrimSpace(runGit(t, path, "symbolic-ref", "--short", "HEAD")); got != "feature/thing" {
		t.Errorf("branch = %q, want feature/thing", got)
	}
	data, err := os.ReadFile(filepath.Join(path, ".env"))
	if err != nil {
		t.Fatalf("copied .env: %v", err)
	}
	if string(data) != "SECRET=1\n" {
		t.Errorf(".env = %q, want SECRET=1", data)
	}
}

func TestCreateWorktreeExistingBranch(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	runGit(t, repo, "branch", "existing")

	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "existing")
	wt, err := r.CreateWorktree(context.Background(), CreateInput{
		Repo: repo, Branch: "existing", NewBranch: false, Path: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if wt.Path != path || wt.Branch != "existing" {
		t.Fatalf("worktree = %+v, want existing at %s", wt, path)
	}
}

func TestCreateWorktreeRemoteOnlyBranch(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initWithOrigin(t, root, repo)
	// A branch pushed by another clone exists only as origin/<b> here.
	runGit(t, repo, "push", "origin", "main:refs/heads/remote-one")
	runGit(t, repo, "fetch", "origin")

	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "remote-one")
	if _, err := r.CreateWorktree(context.Background(), CreateInput{
		Repo: repo, Branch: "remote-one", NewBranch: false, Path: path,
	}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(runGit(t, path, "symbolic-ref", "--short", "HEAD")); got != "remote-one" {
		t.Errorf("branch = %q, want remote-one", got)
	}
}

func TestCreateWorktreeBranchExists(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	runGit(t, repo, "branch", "taken")

	r := NewRunner(allowPolicy(root))
	_, err := r.CreateWorktree(context.Background(), CreateInput{
		Repo: repo, Branch: "taken", NewBranch: true, Base: "main",
		Path: filepath.Join(WorktreeRoot(repo), "taken"),
	})
	if !errors.Is(err, ErrBranchExists) {
		t.Fatalf("error = %v, want ErrBranchExists", err)
	}
}

func TestCreateWorktreeMissingBranch(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)

	r := NewRunner(allowPolicy(root))
	_, err := r.CreateWorktree(context.Background(), CreateInput{
		Repo: repo, Branch: "nope", NewBranch: false,
		Path: filepath.Join(WorktreeRoot(repo), "nope"),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestCreateWorktreePathExists(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)

	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "new")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := r.CreateWorktree(context.Background(), CreateInput{
		Repo: repo, Branch: "brand-new", NewBranch: true, Base: "main", Path: path,
	})
	if !errors.Is(err, ErrPathExists) {
		t.Fatalf("error = %v, want ErrPathExists", err)
	}
}

func TestCreateWorktreeFetchError(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	runGit(t, repo, "remote", "add", "origin", filepath.Join(root, "no-such-origin.git"))

	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "fetched")
	_, err := r.CreateWorktree(context.Background(), CreateInput{
		Repo: repo, Branch: "feature/fetched", NewBranch: true, Base: "main",
		Fetch: true, Path: path,
	})
	var fe *FetchError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v, want *FetchError", err)
	}
	if fe.Message == "" {
		t.Errorf("FetchError message is empty")
	}
	if exists(path) {
		t.Errorf("worktree folder was created")
	}
	if exists(WorktreeRoot(repo)) {
		t.Errorf("worktree container was created")
	}
}

func TestCopyCandidates(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, ".gitignore", strings.Join([]string{
		"*.local", ".env", ".env.*", "node_modules/", "dist/", "build/", ".venv/", "vendor/",
	}, "\n")+"\n")

	writeFile(t, repo, ".env", "SECRET=1\n")
	writeFile(t, repo, ".env.local", "SECRET=2\n")
	writeFile(t, repo, ".claude/agent.local", "x\n")
	writeFile(t, repo, ".claude/notes.txt", "x\n")
	writeFile(t, repo, "readme.md", "x\n")
	writeFile(t, repo, "node_modules/pkg.js", "x\n")
	writeFile(t, repo, "dist/out.js", "x\n")
	writeFile(t, repo, "build/b.js", "x\n")
	writeFile(t, repo, ".venv/v.txt", "x\n")
	writeFile(t, repo, "vendor/v.txt", "x\n")
	writeFile(t, repo, "deep/nested/x.local", "x\n")
	writeBytes(t, repo, "big.local", make([]byte, copyMaxSize+1))

	r := NewRunner(allowPolicy(root))
	got, err := r.CopyCandidates(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".claude/agent.local", ".env", ".env.local"}
	if len(got) != len(want) {
		t.Fatalf("candidates = %+v, want %v", got, want)
	}
	for i, c := range got {
		if c.Path != want[i] {
			t.Errorf("candidate %d = %q, want %q", i, c.Path, want[i])
		}
		if !c.Default {
			t.Errorf("candidate %q default = false, want true", c.Path)
		}
		if c.Size == 0 {
			t.Errorf("candidate %q size = 0", c.Path)
		}
	}
}

func TestCopyCandidatesSkipsNotIgnored(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	writeFile(t, repo, ".env", "SECRET=1\n") // not ignored here

	r := NewRunner(allowPolicy(root))
	got, err := r.CopyCandidates(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("candidates = %+v, want none", got)
	}
}

func TestSuggestPath(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))

	got, err := r.SuggestPath(context.Background(), repo, "feature/foo", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(WorktreeRoot(repo), "foo"); got != want {
		t.Fatalf("SuggestPath = %q, want %q", got, want)
	}

	// The short folder is taken: the whole branch name is used.
	if err := os.MkdirAll(got, 0o755); err != nil {
		t.Fatal(err)
	}
	clash, err := r.SuggestPath(context.Background(), repo, "feature/foo", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(WorktreeRoot(repo), "feature-foo"); clash != want {
		t.Fatalf("SuggestPath clash = %q, want %q", clash, want)
	}
}

func TestRemoveWorktreeClean(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initWithOrigin(t, root, repo)
	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "one")
	pushedWorktree(t, repo, path, "feature/one")

	res, err := r.RemoveWorktree(context.Background(), RemoveInput{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Removed || res.BranchDeleted {
		t.Errorf("result = %+v, want removed without branch delete", res)
	}
	if exists(path) {
		t.Errorf("worktree folder still there")
	}
}

func TestRemoveWorktreeDirtyNoForce(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initWithOrigin(t, root, repo)
	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "dirty")
	pushedWorktree(t, repo, path, "feature/dirty")
	writeFile(t, path, "dirty.txt", "x\n")

	_, err := r.RemoveWorktree(context.Background(), RemoveInput{Path: path})
	var hw *HasWorkError
	if !errors.As(err, &hw) {
		t.Fatalf("error = %v, want *HasWorkError", err)
	}
	if hw.Dirty != 1 || hw.Unpushed != 0 {
		t.Errorf("HasWorkError = %+v, want dirty 1 unpushed 0", hw)
	}
	if !exists(path) {
		t.Errorf("worktree was removed")
	}
}

func TestRemoveWorktreeUnpushed(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initWithOrigin(t, root, repo)
	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "unpushed")
	addWorktree(t, repo, path, "feature/unpushed")
	writeFile(t, path, "c.txt", "x\n")
	runGit(t, path, "add", "c.txt")
	runGit(t, path, "commit", "-qm", "c")

	_, err := r.RemoveWorktree(context.Background(), RemoveInput{Path: path})
	var hw *HasWorkError
	if !errors.As(err, &hw) {
		t.Fatalf("error = %v, want *HasWorkError", err)
	}
	if hw.Dirty != 0 || hw.Unpushed != 1 {
		t.Errorf("HasWorkError = %+v, want dirty 0 unpushed 1", hw)
	}
}

func TestRemoveWorktreeForceConfirmMismatch(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initWithOrigin(t, root, repo)
	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "forced")
	pushedWorktree(t, repo, path, "feature/forced")
	writeFile(t, path, "dirty.txt", "x\n")

	_, err := r.RemoveWorktree(context.Background(), RemoveInput{Path: path, Force: true, Confirm: "wrong"})
	if !errors.Is(err, ErrConfirmMismatch) {
		t.Fatalf("error = %v, want ErrConfirmMismatch", err)
	}
	if !exists(path) {
		t.Errorf("worktree was removed on a bad confirm")
	}
}

func TestRemoveWorktreeForce(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initWithOrigin(t, root, repo)
	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "forced")
	pushedWorktree(t, repo, path, "feature/forced")
	writeFile(t, path, "dirty.txt", "x\n")

	res, err := r.RemoveWorktree(context.Background(), RemoveInput{
		Path: path, Force: true, Confirm: filepath.Base(path),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Removed {
		t.Errorf("result = %+v, want removed", res)
	}
	if exists(path) {
		t.Errorf("worktree folder still there")
	}
}

func TestRemoveWorktreeMain(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))

	_, err := r.RemoveWorktree(context.Background(), RemoveInput{Path: repo})
	if !errors.Is(err, ErrMainWorktree) {
		t.Fatalf("error = %v, want ErrMainWorktree", err)
	}
}

func TestRemoveWorktreeInUse(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initWithOrigin(t, root, repo)
	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "busy")
	pushedWorktree(t, repo, path, "feature/busy")

	_, err := r.RemoveWorktree(context.Background(), RemoveInput{Path: path, Sessions: []string{"s-1", "s-2"}})
	var iu *InUseError
	if !errors.As(err, &iu) {
		t.Fatalf("error = %v, want *InUseError", err)
	}
	if strings.Join(iu.Sessions, ",") != "s-1,s-2" {
		t.Errorf("sessions = %v, want s-1,s-2", iu.Sessions)
	}
	if !exists(path) {
		t.Errorf("worktree was removed")
	}
}

func TestRemoveWorktreeDeleteBranch(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initWithOrigin(t, root, repo)
	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "del")
	pushedWorktree(t, repo, path, "feature/del")

	res, err := r.RemoveWorktree(context.Background(), RemoveInput{Path: path, DeleteBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Removed || !res.BranchDeleted {
		t.Fatalf("result = %+v, want removed and branch deleted", res)
	}
	if out := strings.TrimSpace(runGit(t, repo, "branch", "--list", "feature/del")); out != "" {
		t.Errorf("branch still there: %q", out)
	}
}

func TestCleanupMixed(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initWithOrigin(t, root, repo)
	r := NewRunner(allowPolicy(root))

	clean := filepath.Join(WorktreeRoot(repo), "clean")
	dirty := filepath.Join(WorktreeRoot(repo), "dirty")
	busy := filepath.Join(WorktreeRoot(repo), "busy")
	missing := filepath.Join(WorktreeRoot(repo), "missing")
	pushedWorktree(t, repo, clean, "feature/clean")
	pushedWorktree(t, repo, dirty, "feature/dirty")
	writeFile(t, dirty, "x.txt", "x\n")
	pushedWorktree(t, repo, busy, "feature/busy")

	inUse := func(p string) []string {
		if filepath.Clean(p) == busy {
			return []string{"s-1"}
		}
		return nil
	}
	results := r.Cleanup(context.Background(), CleanupInput{
		Paths: []string{clean, dirty, busy, missing},
		InUse: inUse,
	})
	if len(results) != 4 {
		t.Fatalf("results = %+v, want 4", results)
	}
	want := map[string]ItemResult{
		clean:   {Path: clean, Result: resultRemoved},
		dirty:   {Path: dirty, Result: resultSkipped, Reason: reasonHasWork},
		busy:    {Path: busy, Result: resultSkipped, Reason: reasonInUse},
		missing: {Path: missing, Result: resultFailed},
	}
	for _, res := range results {
		w, ok := want[res.Path]
		if !ok {
			t.Errorf("unexpected result %+v", res)
			continue
		}
		if res.Result != w.Result {
			t.Errorf("result for %s = %q, want %q", filepath.Base(res.Path), res.Result, w.Result)
		}
		if res.Result == resultFailed {
			if res.Reason == "" {
				t.Errorf("failed result for %s has no reason", filepath.Base(res.Path))
			}
			continue
		}
		if res.Reason != w.Reason {
			t.Errorf("reason for %s = %q, want %q", filepath.Base(res.Path), res.Reason, w.Reason)
		}
	}
	if exists(clean) {
		t.Errorf("clean worktree still there")
	}
	if !exists(dirty) || !exists(busy) {
		t.Errorf("a skipped worktree was removed")
	}
}

func TestCleanupDeleteRemote(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initWithOrigin(t, root, repo)
	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "remote")
	pushedWorktree(t, repo, path, "feature/remote")

	results := r.Cleanup(context.Background(), CleanupInput{
		Paths:        []string{path},
		DeleteBranch: true,
		DeleteRemote: true,
	})
	if len(results) != 1 || results[0].Result != resultRemoved || results[0].Reason != "" {
		t.Fatalf("results = %+v, want one removed without reason", results)
	}
	if out := strings.TrimSpace(runGit(t, repo, "ls-remote", "--heads", "origin", "feature/remote")); out != "" {
		t.Errorf("remote branch still there: %q", out)
	}
}

func TestMove(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initWithOrigin(t, root, repo)
	r := NewRunner(allowPolicy(root))
	sib := filepath.Join(root, "wt-sib")
	pushedWorktree(t, repo, sib, "feature/sib")
	to := filepath.Join(WorktreeRoot(repo), "sib")

	results := r.Move(context.Background(), []MoveItem{{Path: sib, To: to}}, nil)
	if len(results) != 1 || results[0].Result != resultMoved {
		t.Fatalf("results = %+v, want one moved", results)
	}
	if exists(sib) {
		t.Errorf("old folder still there")
	}
	if !exists(to) {
		t.Errorf("target folder missing")
	}
}

func TestMoveSkips(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initWithOrigin(t, root, repo)
	r := NewRunner(allowPolicy(root))

	locked := filepath.Join(root, "wt-locked")
	pushedWorktree(t, repo, locked, "feature/locked")
	runGit(t, repo, "worktree", "lock", locked)

	busy := filepath.Join(root, "wt-busy")
	pushedWorktree(t, repo, busy, "feature/busy")

	inUse := func(p string) []string {
		if filepath.Clean(p) == busy {
			return []string{"s-1"}
		}
		return nil
	}

	results := r.Move(context.Background(), []MoveItem{
		{Path: locked, To: filepath.Join(WorktreeRoot(repo), "locked")},
		{Path: busy, To: filepath.Join(WorktreeRoot(repo), "busy")},
	}, inUse)
	if len(results) != 2 {
		t.Fatalf("results = %+v, want 2", results)
	}
	if results[0].Result != resultSkipped || results[0].Reason != reasonLocked {
		t.Errorf("locked result = %+v, want skipped locked", results[0])
	}
	if results[1].Result != resultSkipped || results[1].Reason != reasonInUse {
		t.Errorf("busy result = %+v, want skipped in_use", results[1])
	}
	if !exists(locked) || !exists(busy) {
		t.Errorf("a skipped worktree was moved")
	}
}

func TestPrune(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "gone")
	addWorktree(t, repo, path, "feature/gone")
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}

	pruned, err := r.Prune(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 || pruned[0] != path {
		t.Fatalf("pruned = %v, want %v", pruned, []string{path})
	}
	list, err := r.Worktrees(context.Background(), repo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, wt := range list.Worktrees {
		if wt.Path == path {
			t.Errorf("pruned worktree still listed: %+v", wt)
		}
	}
}

func TestLockUnlock(t *testing.T) {
	root := resolveDir(t)
	repo := filepath.Join(root, "repo")
	initRepo(t, repo)
	r := NewRunner(allowPolicy(root))
	path := filepath.Join(WorktreeRoot(repo), "lockable")
	addWorktree(t, repo, path, "feature/lockable")

	locked, err := r.Lock(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !locked.Locked || locked.Path != path {
		t.Fatalf("locked = %+v, want locked %s", locked, path)
	}

	unlocked, err := r.Lock(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	if unlocked.Locked {
		t.Fatalf("unlocked = %+v, want not locked", unlocked)
	}
}
