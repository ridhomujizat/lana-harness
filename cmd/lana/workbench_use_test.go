package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/workbench/gitinfo"
	"github.com/ridhomujizat/lana-harness/internal/workbench/preview"
	"github.com/ridhomujizat/lana-harness/web"
)

// stubWorkbenchUsePane makes the in-pane check of `use` answer id and ok.
func stubWorkbenchUsePane(t *testing.T, id string, ok bool) {
	t.Helper()
	old := workbenchUseInPane
	workbenchUseInPane = func() (string, bool) { return id, ok }
	t.Cleanup(func() { workbenchUseInPane = old })
}

// stubWorkbenchUseRunner makes the local git runner allow dirs. The first dir
// is also the runner's home, so the resolved paths of all of them pass.
func stubWorkbenchUseRunner(t *testing.T, dirs ...string) {
	t.Helper()
	old := workbenchUseRunner
	workbenchUseRunner = func() *gitinfo.Runner {
		return gitinfo.NewRunner(preview.NewPolicy(dirs[0], dirs))
	}
	t.Cleanup(func() { workbenchUseRunner = old })
}

// useGit runs git in dir with a fixed committer identity and fails the test on
// a non-zero exit.
func useGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// initUseRepo makes a repository on main with one commit, a development branch
// and the ignored files the create copies by default (REQ-WG-032).
func initUseRepo(t *testing.T, repo string) {
	t.Helper()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	useGit(t, repo, "init", "-b", "main")
	useGit(t, repo, "commit", "--allow-empty", "-m", "init")
	useGit(t, repo, "branch", "development")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("*.local\n.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("SECRET=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".claude", "agent.local"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resolved returns dir with its symlinks resolved, so macOS /var matches the
// physical /private/var git and the path policy report.
func resolved(t *testing.T, dir string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWorkbenchUseOutsidePane(t *testing.T) {
	stubWorkbenchUsePane(t, "", false)
	stderr, code := captureStderr(t, func() int { return cmdWorkbenchUse([]string{"some/path"}) })
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "not in a Lana Workbench pane") {
		t.Fatalf("stderr = %q, want the pane message", stderr)
	}
}

func TestWorkbenchUseDispatchedByWorkbench(t *testing.T) {
	stubWorkbenchUsePane(t, "", false)
	stderr, code := captureStderr(t, func() int { return cmdWorkbench([]string{"use"}) })
	if code != 2 || !strings.Contains(stderr, "not in a Lana Workbench pane") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestWorkbenchUseReportsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	dir := t.TempDir()
	t.Chdir(dir)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	var gotPath, gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.Header.Get(web.LocalSecretHeader)
		if r.Method != http.MethodPut || r.URL.Path != "/v1/local/workbench/sessions/ws-12/worktree" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotPath = body.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session": "ws-12",
			"worktree": map[string]any{
				"short":    "fig",
				"worktree": "/repo/figcraft-viewer",
			},
		})
	}))
	defer srv.Close()
	writeDaemonSecretJSON(t, home, strings.TrimPrefix(srv.URL, "http://"), "s3cr3t")
	stubWorkbenchUsePane(t, "ws-12", true)

	out, code := captureStdout(t, func() int { return cmdWorkbenchUse([]string{"sub"}) })
	if code != 0 {
		t.Fatalf("exit = %d, out = %q", code, out)
	}
	if want := filepath.Join(cwd, "sub"); gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
	if gotSecret != "s3cr3t" {
		t.Fatalf("secret = %q, want s3cr3t", gotSecret)
	}
	if want := "Workbench: fig (/repo/figcraft-viewer)\n"; out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

func TestWorkbenchUseClearDeletes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)

	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	writeDaemonSecretJSON(t, home, strings.TrimPrefix(srv.URL, "http://"), "s")
	stubWorkbenchUsePane(t, "ws-9", true)

	out, code := captureStdout(t, func() int { return cmdWorkbenchUse([]string{"--clear"}) })
	if code != 0 {
		t.Fatalf("exit = %d, out = %q", code, out)
	}
	if method != http.MethodDelete || path != "/v1/local/workbench/sessions/ws-9/worktree" {
		t.Fatalf("request = %s %s, want DELETE on the report route", method, path)
	}
	if want := "Workbench: following the pane's own folder again\n"; out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

// daemonDeleter is a fake daemon that records the report request and answers a
// DELETE with 204 and a PUT with a fixed worktree.
type daemonDeleter struct {
	method string
	path   string
	body   string
}

func (d *daemonDeleter) serve(t *testing.T, home string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.method, d.path = r.Method, r.URL.Path
		data, _ := io.ReadAll(r.Body)
		d.body = string(data)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"worktree": map[string]any{"short": "fig", "worktree": "/repo/fig"},
		})
	}))
	t.Cleanup(srv.Close)
	writeDaemonSecretJSON(t, home, strings.TrimPrefix(srv.URL, "http://"), "s")
	return srv
}

func TestWorkbenchUseDotClearsWithoutStartDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	t.Chdir(t.TempDir())
	// LANA_WORKBENCH_DIR is unset, so "." means "back to the pane's folder".
	t.Setenv("LANA_WORKBENCH_DIR", "")
	stubWorkbenchUsePane(t, "ws-1", true)

	d := &daemonDeleter{}
	d.serve(t, home)
	out, code := captureStdout(t, func() int { return cmdWorkbenchUse([]string{"."}) })
	if code != 0 {
		t.Fatalf("exit = %d, out = %q", code, out)
	}
	if d.method != http.MethodDelete {
		t.Fatalf("method = %s, want DELETE", d.method)
	}
	if !strings.Contains(out, "following the pane's own folder again") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestWorkbenchUseDotSameWorktreeClears(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	root := resolved(t, t.TempDir())
	repo := filepath.Join(root, "repo")
	initUseRepo(t, repo)
	t.Chdir(repo)
	t.Setenv("LANA_WORKBENCH_DIR", repo)
	stubWorkbenchUsePane(t, "ws-1", true)

	d := &daemonDeleter{}
	d.serve(t, home)
	out, code := captureStdout(t, func() int { return cmdWorkbenchUse([]string{"."}) })
	if code != 0 {
		t.Fatalf("exit = %d, out = %q", code, out)
	}
	if d.method != http.MethodDelete {
		t.Fatalf("method = %s, want DELETE for the same worktree", d.method)
	}
	if !strings.Contains(out, "following the pane's own folder again") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestWorkbenchUseDotOtherWorktreeReports(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	root := resolved(t, t.TempDir())
	repo, other := filepath.Join(root, "repo"), filepath.Join(root, "other")
	initUseRepo(t, repo)
	initUseRepo(t, other)
	t.Chdir(repo)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANA_WORKBENCH_DIR", other)
	stubWorkbenchUsePane(t, "ws-1", true)

	d := &daemonDeleter{}
	d.serve(t, home)
	if _, code := captureStdout(t, func() int { return cmdWorkbenchUse([]string{"."}) }); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if d.method != http.MethodPut {
		t.Fatalf("method = %s, want PUT for another worktree", d.method)
	}
	if !strings.Contains(d.body, cwd) {
		t.Fatalf("body = %q, want the working directory %q", d.body, cwd)
	}
}

func TestWorkbenchUseErrorMapping(t *testing.T) {
	tests := []struct {
		status int
		code   string
		want   string
	}{
		{http.StatusNotFound, "not_a_repo", "/repo/x is not inside a git repository"},
		{http.StatusForbidden, "forbidden_path", "/repo/x is outside the folders the Workbench may read"},
		{http.StatusServiceUnavailable, "workbench_unavailable", "the Workbench is off"},
		{http.StatusNotFound, "not_found", "this pane's session is gone"},
		{http.StatusConflict, "branch_exists", "the branch already exists"},
		{http.StatusConflict, "path_exists", "the worktree folder already exists"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("LANA_HOME", home)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{"code": tt.code, "message": "daemon said no"},
				})
			}))
			defer srv.Close()
			writeDaemonSecretJSON(t, home, strings.TrimPrefix(srv.URL, "http://"), "s")
			stubWorkbenchUsePane(t, "ws-1", true)

			stderr, code := captureStderr(t, func() int { return cmdWorkbenchUse([]string{"/repo/x"}) })
			if code != 1 {
				t.Fatalf("exit = %d, want 1; stderr = %q", code, stderr)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr, tt.want)
			}
		})
	}
}

func TestWorkbenchUseDaemonDown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	// Port 1 is closed, so the report PUT is refused.
	writeDaemonSecretJSON(t, home, "127.0.0.1:1", "s")
	stubWorkbenchUsePane(t, "ws-1", true)

	stderr, code := captureStderr(t, func() int { return cmdWorkbenchUse([]string{"/repo/x"}) })
	if code != 1 || !strings.Contains(stderr, "the Lana daemon is not running") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestWorkbenchUseNoDaemonSecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home) // no daemon.secret
	stubWorkbenchUsePane(t, "ws-1", true)

	stderr, code := captureStderr(t, func() int { return cmdWorkbenchUse([]string{"/repo/x"}) })
	if code != 1 || !strings.Contains(stderr, "the Lana daemon is not running") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestWorkbenchUseBadArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	writeDaemonSecretJSON(t, home, "127.0.0.1:1", "s")
	stubWorkbenchUsePane(t, "ws-1", true)

	for _, args := range [][]string{
		{},
		{"--new"},
		{"--base"},
		{"--clear", "path"},
		{"--new", "b", "path"},
		{"--bogus"},
	} {
		stderr, code := captureStderr(t, func() int { return cmdWorkbenchUse(args) })
		if code != 2 {
			t.Errorf("args %v: exit = %d, want 2; stderr = %q", args, code, stderr)
		}
	}
}

// newUseDaemon is a fake daemon that records the create body and the report
// path and answers both routes.
type newUseDaemon struct {
	create workbenchUseCreateBody
	report string
}

func (d *newUseDaemon) serve(t *testing.T, home string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/local/workbench/git/worktrees":
			_ = json.NewDecoder(r.Body).Decode(&d.create)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"path": d.create.Path})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/local/workbench/sessions/"):
			var body struct {
				Path string `json:"path"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			d.report = body.Path
			_ = json.NewEncoder(w).Encode(map[string]any{
				"worktree": map[string]any{"short": gitinfo.ShortBranch(d.create.Branch), "worktree": d.create.Path},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeDaemonSecretJSON(t, home, strings.TrimPrefix(srv.URL, "http://"), "s")
	return srv
}

func TestWorkbenchUseNewCreatesWorktree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	root := resolved(t, t.TempDir())
	repo := filepath.Join(root, "repo")
	initUseRepo(t, repo)
	t.Chdir(repo)
	toplevel := strings.TrimSpace(useGit(t, repo, "rev-parse", "--show-toplevel"))
	stubWorkbenchUseRunner(t, root)
	stubWorkbenchUsePane(t, "ws-1", true)

	d := &newUseDaemon{}
	d.serve(t, home)
	out, code := captureStdout(t, func() int {
		return cmdWorkbenchUse([]string{"--new", "feature/widget"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, out = %q", code, out)
	}

	folder := filepath.Join(gitinfo.WorktreeRoot(toplevel), "widget")
	if d.create.Repo != toplevel {
		t.Errorf("repo = %q, want %q", d.create.Repo, toplevel)
	}
	if d.create.Branch != "feature/widget" || !d.create.NewBranch {
		t.Errorf("branch = %q new_branch = %v, want feature/widget true", d.create.Branch, d.create.NewBranch)
	}
	if d.create.Base != "development" {
		t.Errorf("base = %q, want the default base development", d.create.Base)
	}
	if !d.create.Fetch {
		t.Error("fetch = false, want true by default")
	}
	if d.create.Path != folder {
		t.Errorf("path = %q, want %q", d.create.Path, folder)
	}
	if got, want := strings.Join(d.create.Copy, ","), ".claude/agent.local,.env"; got != want {
		t.Errorf("copy = %q, want %q", got, want)
	}
	if d.report != folder {
		t.Errorf("reported path = %q, want the new worktree %q", d.report, folder)
	}
	want := "Workbench: widget (" + folder + ")\nCreated " + folder + "\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestWorkbenchUseNewExistingBranch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	root := resolved(t, t.TempDir())
	repo := filepath.Join(root, "repo")
	initUseRepo(t, repo)
	useGit(t, repo, "branch", "feature/widget") // exists, but has no worktree
	t.Chdir(repo)
	stubWorkbenchUseRunner(t, root)
	stubWorkbenchUsePane(t, "ws-1", true)

	d := &newUseDaemon{}
	d.serve(t, home)
	if _, code := captureStdout(t, func() int {
		return cmdWorkbenchUse([]string{"--new", "feature/widget", "--no-fetch", "--base", "main"})
	}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if d.create.NewBranch {
		t.Error("new_branch = true for an existing branch, want false")
	}
	if d.create.Fetch {
		t.Error("fetch = true with --no-fetch, want false")
	}
	if d.create.Base != "main" {
		t.Errorf("base = %q, want the --base value main", d.create.Base)
	}
}

func TestWorkbenchUseNewRefusesBranchWithWorktree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	root := resolved(t, t.TempDir())
	repo := filepath.Join(root, "repo")
	initUseRepo(t, repo)
	existing := filepath.Join(root, "existing")
	useGit(t, repo, "worktree", "add", "-b", "feature/widget", existing)
	t.Chdir(repo)
	stubWorkbenchUseRunner(t, root)
	stubWorkbenchUsePane(t, "ws-1", true)
	// The check runs before any request, but the command still needs a daemon.
	writeDaemonSecretJSON(t, home, "127.0.0.1:1", "s")

	stderr, code := captureStderr(t, func() int {
		return cmdWorkbenchUse([]string{"--new", "feature/widget"})
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "already has a worktree") || !strings.Contains(stderr, existing) {
		t.Fatalf("stderr = %q, want the existing worktree path %q", stderr, existing)
	}
}

func TestWorkbenchUseNewNotARepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	t.Chdir(t.TempDir())
	stubWorkbenchUseRunner(t, t.TempDir())
	stubWorkbenchUsePane(t, "ws-1", true)
	writeDaemonSecretJSON(t, home, "127.0.0.1:1", "s")

	stderr, code := captureStderr(t, func() int {
		return cmdWorkbenchUse([]string{"--new", "feature/widget"})
	})
	if code != 1 || !strings.Contains(stderr, "not a git repository") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}
