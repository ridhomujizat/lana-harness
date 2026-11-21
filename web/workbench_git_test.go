package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
	"github.com/ridhomujizat/lana-harness/internal/workbench/gitinfo"
	"github.com/ridhomujizat/lana-harness/internal/workbench/preview"
)

// newGitEnv returns a logged-in server whose preview policy allows a resolved
// temp tree, so the git routes can read the temporary repository. backend is
// installed when it is not nil; root is that tree.
func newGitEnv(t *testing.T, backend WorkbenchBackend) (*Server, *http.Cookie, string, string) {
	t.Helper()
	s, cookie, csrf := newWorkbenchServer(t)
	if backend != nil {
		s.SetWorkbench(backend)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	s.previewHome = home
	s.previewTemps = []string{root}
	return s, cookie, csrf, root
}

// gitRun runs git in dir with a fixed identity and fails the test on a non-zero
// exit.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// gitRepo makes a repository on main with one commit.
func gitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "init")
}

// gitAddWorktree adds a worktree of repo on a new branch from main.
func gitAddWorktree(t *testing.T, repo, path, branch string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "worktree", "add", "-b", branch, path, "main")
}

// gitWriteFile writes a file, creating its parent folder.
func gitWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitTarget builds a route target with its query values.
func gitTarget(path string, params map[string]string) string {
	q := url.Values{}
	for key, value := range params {
		q.Set(key, value)
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// gitGet sends an authenticated GET to a git route.
func gitGet(s *Server, cookie *http.Cookie, target string) *httptest.ResponseRecorder {
	return workbenchRequest(s, http.MethodGet, target, "", cookie, "")
}

func TestWorkbenchGitUnavailable(t *testing.T) {
	s, cookie, _, root := newGitEnv(t, nil)
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)
	target := gitTarget("/v1/workbench/git/worktrees", map[string]string{"repo": repo})
	assertAuthError(t, gitGet(s, cookie, target), http.StatusServiceUnavailable, "workbench_unavailable")
}

func TestWorkbenchGitResolve(t *testing.T) {
	s, cookie, csrf, root := newGitEnv(t, newFakeWorkbench())
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	forbidden := t.TempDir()

	body := `{"dirs":[` + strconvQuote(repo) + `,` + strconvQuote(plain) + `,` + strconvQuote(forbidden) + `]}`
	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/git/resolve", body, cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var resp struct {
		Dirs map[string]struct {
			Repo     *string `json:"repo"`
			Worktree string  `json:"worktree"`
			Error    string  `json:"error"`
		} `json:"dirs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	got := resp.Dirs[repo]
	if got.Repo == nil || got.Worktree != repo {
		t.Fatalf("repo dir = %+v", got)
	}
	if filepath.Base(*got.Repo) != ".git" {
		t.Fatalf("repo id = %q, want a .git path", *got.Repo)
	}
	if resp.Dirs[plain].Repo != nil || resp.Dirs[plain].Error != "" {
		t.Fatalf("plain dir = %+v, want repo null", resp.Dirs[plain])
	}
	if resp.Dirs[forbidden].Error != "forbidden_path" {
		t.Fatalf("forbidden dir = %+v", resp.Dirs[forbidden])
	}
}

func TestWorkbenchGitStatus(t *testing.T) {
	s, cookie, _, root := newGitEnv(t, newFakeWorkbench())
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)
	gitWriteFile(t, filepath.Join(repo, "a.txt"), []byte("hi\n"))

	target := gitTarget("/v1/workbench/git/status", map[string]string{"worktree": repo, "scope": "uncommitted"})
	rr := gitGet(s, cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	var status gitinfo.Status
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Worktree != repo || status.Scope != gitinfo.ScopeUncommitted {
		t.Fatalf("status = %+v", status)
	}
	if len(status.Files) != 1 || status.Files[0].Path != "a.txt" || status.Files[0].Status != "U" {
		t.Fatalf("files = %+v", status.Files)
	}

	req := authRequest(http.MethodGet, target, "")
	req.AddCookie(cookie)
	req.Header.Set("If-None-Match", etag)
	rr304 := serveAuth(s, req)
	if rr304.Code != http.StatusNotModified {
		t.Fatalf("304 status = %d, body = %q", rr304.Code, rr304.Body.String())
	}
	if rr304.Body.Len() != 0 {
		t.Fatalf("304 body = %q", rr304.Body.String())
	}

	bad := gitTarget("/v1/workbench/git/status", map[string]string{"worktree": repo, "scope": "bogus"})
	assertAuthError(t, gitGet(s, cookie, bad), http.StatusBadRequest, "invalid_request")
}

func TestWorkbenchGitDiffTreeSearch(t *testing.T) {
	s, cookie, _, root := newGitEnv(t, newFakeWorkbench())
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)
	gitWriteFile(t, filepath.Join(repo, "a.txt"), []byte("one\ntwo\n"))
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "add a")
	gitWriteFile(t, filepath.Join(repo, "a.txt"), []byte("one\nTWO\n"))

	// Diff.
	target := gitTarget("/v1/workbench/git/diff", map[string]string{"worktree": repo, "path": "a.txt", "scope": "uncommitted"})
	rr := gitGet(s, cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("diff = %d, body = %q", rr.Code, rr.Body.String())
	}
	var diff gitinfo.FileDiff
	if err := json.Unmarshal(rr.Body.Bytes(), &diff); err != nil {
		t.Fatal(err)
	}
	if diff.Path != "a.txt" || diff.Status != "M" || len(diff.Hunks) == 0 {
		t.Fatalf("diff = %+v", diff)
	}

	// Tree.
	target = gitTarget("/v1/workbench/git/tree", map[string]string{"worktree": repo})
	rr = gitGet(s, cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("tree = %d, body = %q", rr.Code, rr.Body.String())
	}
	var tree gitinfo.TreeList
	if err := json.Unmarshal(rr.Body.Bytes(), &tree); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range tree.Entries {
		if entry.Name == "a.txt" {
			found = true
			if entry.Status != "M" {
				t.Fatalf("a.txt status = %q, want M", entry.Status)
			}
		}
	}
	if !found {
		t.Fatalf("tree = %+v", tree.Entries)
	}

	// Search.
	target = gitTarget("/v1/workbench/git/search", map[string]string{"worktree": repo, "q": "a.txt"})
	rr = gitGet(s, cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("search = %d, body = %q", rr.Code, rr.Body.String())
	}
	var search struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	hasFile := false
	for _, file := range search.Files {
		if file == "a.txt" {
			hasFile = true
		}
	}
	if !hasFile {
		t.Fatalf("search = %v", search.Files)
	}
}

func TestWorkbenchGitFile(t *testing.T) {
	s, cookie, _, root := newGitEnv(t, newFakeWorkbench())
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)
	gitWriteFile(t, filepath.Join(repo, "a.txt"), []byte("hello\nworld\n"))
	gitWriteFile(t, filepath.Join(repo, "bin.dat"), []byte{0x00, 0x01, 0x02, 0x03})

	// A text file is the FileContent JSON.
	target := gitTarget("/v1/workbench/git/file", map[string]string{"worktree": repo, "path": "a.txt"})
	rr := gitGet(s, cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("text = %d, body = %q", rr.Code, rr.Body.String())
	}
	var content gitinfo.FileContent
	if err := json.Unmarshal(rr.Body.Bytes(), &content); err != nil {
		t.Fatal(err)
	}
	if content.Kind != "text" || content.Text != "hello\nworld\n" || content.Lines != 2 {
		t.Fatalf("content = %+v", content)
	}

	// Any other kind is the spec 61 preview response.
	target = gitTarget("/v1/workbench/git/file", map[string]string{"worktree": repo, "path": "bin.dat"})
	rr = gitGet(s, cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("binary = %d, body = %q", rr.Code, rr.Body.String())
	}
	var resp workbenchPreviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Kind != "binary" || resp.URL == "" || resp.RawURL == "" {
		t.Fatalf("preview resp = %+v", resp)
	}
	if want := preview.Resolve(filepath.Join(repo, "bin.dat")); resp.Path != want {
		t.Fatalf("path = %q, want %q", resp.Path, want)
	}
}

func TestWorkbenchGitWorktrees(t *testing.T) {
	s, cookie, _, root := newGitEnv(t, newFakeWorkbench())
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)

	// The repo id is the absolute git common dir (".../.git").
	target := gitTarget("/v1/workbench/git/worktrees", map[string]string{"repo": filepath.Join(repo, ".git")})
	rr := gitGet(s, cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("worktrees = %d, body = %q", rr.Code, rr.Body.String())
	}
	var list gitinfo.WorktreeList
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(list.Repo) != ".git" {
		t.Fatalf("repo = %q, want a .git path", list.Repo)
	}
	if len(list.Worktrees) != 1 || list.Worktrees[0].Path != repo || !list.Worktrees[0].Main {
		t.Fatalf("worktrees = %+v", list.Worktrees)
	}
}

func TestWorkbenchGitWorktreeCreate(t *testing.T) {
	s, cookie, csrf, root := newGitEnv(t, newFakeWorkbench())
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)
	path := filepath.Join(root, "repo-worktree", "feature-x")

	body := `{"repo":` + strconvQuote(repo) + `,"branch":"feature/x","new_branch":true,"base":"main","path":` + strconvQuote(path) + `}`
	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/git/worktrees", body, cookie, csrf)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create = %d, body = %q", rr.Code, rr.Body.String())
	}
	var wt gitinfo.Worktree
	if err := json.Unmarshal(rr.Body.Bytes(), &wt); err != nil {
		t.Fatal(err)
	}
	if wt.Path != path || wt.Branch != "feature/x" || wt.Main {
		t.Fatalf("worktree = %+v", wt)
	}

	// The same path with a different new branch is path_exists.
	body = `{"repo":` + strconvQuote(repo) + `,"branch":"feature/y","new_branch":true,"base":"main","path":` + strconvQuote(path) + `}`
	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/git/worktrees", body, cookie, csrf)
	assertAuthError(t, rr, http.StatusConflict, "path_exists")
}

func TestWorkbenchGitWorktreeRemoveHasWork(t *testing.T) {
	s, cookie, csrf, root := newGitEnv(t, newFakeWorkbench())
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)
	path := filepath.Join(root, "repo-worktree", "feature-x")
	gitAddWorktree(t, repo, path, "feature/x")
	gitWriteFile(t, filepath.Join(path, "dirty.txt"), []byte("x\n"))

	body := `{"path":` + strconvQuote(path) + `}`
	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/git/worktrees/remove", body, cookie, csrf)
	if rr.Code != http.StatusConflict {
		t.Fatalf("remove = %d, body = %q", rr.Code, rr.Body.String())
	}
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "has_work" {
		t.Fatalf("code = %q, want has_work", env.Error.Code)
	}
	if len(env.Error.Details) == 0 {
		t.Fatal("no has_work details")
	}
	if n, ok := env.Error.Details[0]["dirty"].(float64); !ok || n <= 0 {
		t.Fatalf("details = %+v", env.Error.Details)
	}

	// Force with a wrong confirm is confirm_mismatch.
	body = `{"path":` + strconvQuote(path) + `,"force":true,"confirm":"nope"}`
	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/git/worktrees/remove", body, cookie, csrf)
	assertAuthError(t, rr, http.StatusBadRequest, "confirm_mismatch")
}

func TestWorkbenchGitWorktreeRemoveInUse(t *testing.T) {
	fake := newFakeWorkbench()
	s, cookie, csrf, root := newGitEnv(t, fake)
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)
	path := filepath.Join(root, "repo-worktree", "feature-x")
	gitAddWorktree(t, repo, path, "feature/x")
	fake.sessions = []workbench.Session{{ID: "s1", Cwd: filepath.Join(path, "sub"), State: "running"}}

	body := `{"path":` + strconvQuote(path) + `,"force":true,"confirm":"feature-x"}`
	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/git/worktrees/remove", body, cookie, csrf)
	if rr.Code != http.StatusConflict {
		t.Fatalf("remove = %d, body = %q", rr.Code, rr.Body.String())
	}
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "in_use" {
		t.Fatalf("code = %q, want in_use", env.Error.Code)
	}
	if len(env.Error.Details) == 0 {
		t.Fatal("no in_use details")
	}
	sessions, ok := env.Error.Details[0]["sessions"].([]any)
	if !ok || len(sessions) != 1 || sessions[0] != "s1" {
		t.Fatalf("sessions = %+v", env.Error.Details)
	}
}

func TestWorkbenchGitSettings(t *testing.T) {
	s, cookie, csrf, root := newGitEnv(t, newFakeWorkbench())
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)
	target := gitTarget("/v1/workbench/git/settings", map[string]string{"repo": repo})

	rr := gitGet(s, cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("get = %d, body = %q", rr.Code, rr.Body.String())
	}
	var defaults gitinfo.Settings
	if err := json.Unmarshal(rr.Body.Bytes(), &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.Base != "main" || !defaults.WarnShared {
		t.Fatalf("defaults = %+v", defaults)
	}
	if defaults.WorktreeRoot != gitinfo.WorktreeRoot(repo) {
		t.Fatalf("worktree_root = %q, want %q", defaults.WorktreeRoot, gitinfo.WorktreeRoot(repo))
	}

	newRoot := filepath.Join(root, "wt-root")
	body := `{"base":"main","worktree_root":` + strconvQuote(newRoot) + `,"warn_shared":false}`
	rr = workbenchRequest(s, http.MethodPut, target, body, cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("put = %d, body = %q", rr.Code, rr.Body.String())
	}
	var put gitinfo.Settings
	if err := json.Unmarshal(rr.Body.Bytes(), &put); err != nil {
		t.Fatal(err)
	}
	if put.Base != "main" || put.WorktreeRoot != newRoot || put.WarnShared {
		t.Fatalf("put = %+v", put)
	}

	rr = gitGet(s, cookie, target)
	var got gitinfo.Settings
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != put {
		t.Fatalf("round trip = %+v, want %+v", got, put)
	}

	// An empty base is refused.
	rr = workbenchRequest(s, http.MethodPut, target, `{"base":""}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")

	// A worktree_root outside the path policy is refused.
	outside := t.TempDir()
	body = `{"worktree_root":` + strconvQuote(outside) + `}`
	rr = workbenchRequest(s, http.MethodPut, target, body, cookie, csrf)
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}

func TestWorkbenchGitForbiddenPath(t *testing.T) {
	s, cookie, _, _ := newGitEnv(t, newFakeWorkbench())
	outside := t.TempDir()
	target := gitTarget("/v1/workbench/git/status", map[string]string{"worktree": outside, "scope": "uncommitted"})
	assertAuthError(t, gitGet(s, cookie, target), http.StatusForbidden, "forbidden_path")
}
