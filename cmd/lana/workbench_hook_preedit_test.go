package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// preEditRequest is one request the fake daemon saw.
type preEditRequest struct {
	method string
	path   string
	body   string
}

// preEditDaemon records the hook's report requests and answers a PUT with the
// reported path as the worktree root (as the daemon's report route does) and a
// DELETE with 204.
type preEditDaemon struct {
	mu   sync.Mutex
	reqs []preEditRequest
}

func (d *preEditDaemon) handle(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	d.mu.Lock()
	d.reqs = append(d.reqs, preEditRequest{method: r.Method, path: r.URL.Path, body: string(data)})
	d.mu.Unlock()
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(data, &body)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"worktree": map[string]any{"short": "wt", "worktree": body.Path},
	})
}

func (d *preEditDaemon) requests() []preEditRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]preEditRequest(nil), d.reqs...)
}

func (d *preEditDaemon) reset() {
	d.mu.Lock()
	d.reqs = nil
	d.mu.Unlock()
}

// servePreEditDaemon starts a fake daemon and points daemon.secret at it.
func servePreEditDaemon(t *testing.T, home string) *preEditDaemon {
	t.Helper()
	d := &preEditDaemon{}
	srv := httptest.NewServer(http.HandlerFunc(d.handle))
	t.Cleanup(srv.Close)
	writeDaemonSecretJSON(t, home, strings.TrimPrefix(srv.URL, "http://"), "s")
	return d
}

// stubHookPane makes the in-pane check of the hooks answer id and ok.
func stubHookPane(t *testing.T, id string, ok bool) {
	t.Helper()
	old := hookInPane
	hookInPane = func() (string, bool) { return id, ok }
	t.Cleanup(func() { hookInPane = old })
}

// countHookGit wraps the git runner with a counter, so a test can assert the
// hook ran no git at all.
func countHookGit(t *testing.T) *int32 {
	t.Helper()
	old := hookGitRunner
	var n int32
	hookGitRunner = func(dir string, args ...string) (string, bool) {
		atomic.AddInt32(&n, 1)
		return hookGitRun(dir, args...)
	}
	t.Cleanup(func() { hookGitRunner = old })
	return &n
}

// preEditJSON builds the hook stdin JSON for an edit.
func preEditJSON(cwd, toolPathKey, path, agent string) string {
	body := map[string]any{
		"cwd":        cwd,
		"tool_name":  "Edit",
		"tool_input": map[string]string{toolPathKey: path},
	}
	if agent != "" {
		body["agent_id"] = agent
	}
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// runPreEdit runs the hook entry and returns what it wrote to stdout and its
// exit code.
func runPreEdit(t *testing.T, input string) (string, int) {
	t.Helper()
	return captureStdout(t, func() int { return workbenchPreEdit(strings.NewReader(input)) })
}

// preEditReportPath is the request body's path.
func preEditReportPath(t *testing.T, req preEditRequest) string {
	t.Helper()
	var body struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(req.body), &body); err != nil {
		t.Fatalf("request body %q: %v", req.body, err)
	}
	return body.Path
}

// makePreEditRepo makes a repo with a sibling worktree and one nested under
// <repo>/.claude/worktrees/x, and returns their resolved roots.
func makePreEditRepo(t *testing.T) (repo, sibling, nested string) {
	t.Helper()
	root := resolved(t, t.TempDir())
	repo = filepath.Join(root, "repo")
	initUseRepo(t, repo)
	sibling = filepath.Join(root, "sib")
	useGit(t, repo, "worktree", "add", "-q", sibling, "-b", "sib")
	if err := os.MkdirAll(filepath.Join(repo, ".claude", "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested = filepath.Join(repo, ".claude", "worktrees", "x")
	useGit(t, repo, "worktree", "add", "-q", nested, "-b", "nested")
	return repo, sibling, nested
}

func TestHookPreEditOutsidePane(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "", false)
	d := servePreEditDaemon(t, home)

	out, code := runPreEdit(t, preEditJSON("/repo", "file_path", "/repo/a.go", ""))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	if got := d.requests(); len(got) != 0 {
		t.Fatalf("requests = %+v, want none", got)
	}
}

func TestHookPreEditSubagent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)

	out, code := runPreEdit(t, preEditJSON("/repo", "file_path", "/repo/a.go", "sub-7"))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	if got := d.requests(); len(got) != 0 {
		t.Fatalf("requests = %+v, want none for a subagent", got)
	}
}

func TestHookPreEditHomeRootNoRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)
	repo, _, _ := makePreEditRepo(t)

	out, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(repo, "main.go"), ""))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	if got := d.requests(); len(got) != 0 {
		t.Fatalf("requests = %+v, want none in the home root", got)
	}
}

func TestHookPreEditSiblingWorktreeReportsOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)
	repo, sibling, _ := makePreEditRepo(t)

	out, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(sibling, "a.go"), ""))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	reqs := d.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %+v, want exactly one", reqs)
	}
	if reqs[0].method != http.MethodPut || reqs[0].path != "/v1/local/workbench/sessions/ws-1/worktree" {
		t.Fatalf("request = %+v, want PUT on the report route", reqs[0])
	}
	if got, want := preEditReportPath(t, reqs[0]), resolved(t, sibling); got != want {
		t.Fatalf("reported path = %q, want %q", got, want)
	}
}

func TestHookPreEditSecondEditNoGit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)
	repo, sibling, _ := makePreEditRepo(t)

	// The first edit reports the sibling and saves the state.
	if _, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(sibling, "a.go"), "")); code != 0 {
		t.Fatalf("first edit code = %d", code)
	}
	if got := d.requests(); len(got) != 1 {
		t.Fatalf("first edit requests = %+v, want one", got)
	}
	d.reset()
	count := countHookGit(t)

	out, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(sibling, "b.go"), ""))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	if got := d.requests(); len(got) != 0 {
		t.Fatalf("second edit requests = %+v, want none", got)
	}
	if got := atomic.LoadInt32(count); got != 0 {
		t.Fatalf("second edit ran git %d times, want none", got)
	}
}

func TestHookPreEditNestedWorktreeLongestMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)
	repo, _, nested := makePreEditRepo(t)

	out, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(nested, "deep", "a.go"), ""))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	reqs := d.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodPut {
		t.Fatalf("requests = %+v, want one PUT", reqs)
	}
	if got, want := preEditReportPath(t, reqs[0]), resolved(t, nested); got != want {
		t.Fatalf("reported path = %q, want the nested worktree %q", got, want)
	}
}

func TestHookPreEditBackHomeDeletes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)
	repo, sibling, _ := makePreEditRepo(t)

	if _, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(sibling, "a.go"), "")); code != 0 {
		t.Fatalf("sibling edit code = %d", code)
	}
	d.reset()

	out, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(repo, "main.go"), ""))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	reqs := d.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodDelete {
		t.Fatalf("requests = %+v, want one DELETE", reqs)
	}
	if reqs[0].path != "/v1/local/workbench/sessions/ws-1/worktree" {
		t.Fatalf("path = %q, want the report route", reqs[0].path)
	}
}

func TestHookPreEditNonGitPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)
	repo, _, _ := makePreEditRepo(t)

	target := filepath.Join(t.TempDir(), "note.txt")
	out, code := runPreEdit(t, preEditJSON(repo, "file_path", target, ""))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	if got := d.requests(); len(got) != 0 {
		t.Fatalf("requests = %+v, want none for a non-git path", got)
	}
}

func TestHookPreEditOtherRepoIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)
	repo, _, _ := makePreEditRepo(t)
	other := filepath.Join(resolved(t, t.TempDir()), "other")
	initUseRepo(t, other)

	out, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(other, "a.go"), ""))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	if got := d.requests(); len(got) != 0 {
		t.Fatalf("requests = %+v, want none for another repo", got)
	}
}

func TestHookPreEditNewWorktreeRefreshes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)
	root := resolved(t, t.TempDir())
	repo := filepath.Join(root, "repo")
	initUseRepo(t, repo)

	// Establish the state with only the main worktree.
	if _, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(repo, "a.go"), "")); code != 0 {
		t.Fatalf("home edit code = %d", code)
	}
	// A new worktree created after the state was saved.
	fresh := filepath.Join(root, "fresh")
	useGit(t, repo, "worktree", "add", "-q", fresh, "-b", "fresh")
	d.reset()
	count := countHookGit(t)

	out, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(fresh, "a.go"), ""))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	reqs := d.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodPut {
		t.Fatalf("requests = %+v, want one PUT", reqs)
	}
	if got, want := preEditReportPath(t, reqs[0]), resolved(t, fresh); got != want {
		t.Fatalf("reported path = %q, want %q", got, want)
	}
	if atomic.LoadInt32(count) == 0 {
		t.Fatal("the new worktree did not run git to refresh the list")
	}
}

func TestHookPreEditLateNestedWorktreeRefreshes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)
	repo, _, _ := makePreEditRepo(t)

	// The state is saved by an edit in the home root, so it only knows the
	// worktrees that existed then.
	if _, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(repo, "a.go"), "")); code != 0 {
		t.Fatalf("home edit code = %d", code)
	}
	// A worktree nested under the home root, created after the state was saved.
	late := filepath.Join(repo, ".claude", "worktrees", "late")
	useGit(t, repo, "worktree", "add", "-q", late, "-b", "late")
	d.reset()
	count := countHookGit(t)

	target := filepath.Join(late, "deep", "a.go")
	if out, code := runPreEdit(t, preEditJSON(repo, "file_path", target, "")); code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	reqs := d.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodPut {
		t.Fatalf("requests = %+v, want one PUT", reqs)
	}
	if got, want := preEditReportPath(t, reqs[0]), resolved(t, late); got != want {
		t.Fatalf("reported path = %q, want the late worktree %q", got, want)
	}
	if atomic.LoadInt32(count) == 0 {
		t.Fatal("the nested worktree did not run git to refresh the list")
	}

	// The second edit stays a local comparison: no git and no request.
	d.reset()
	atomic.StoreInt32(count, 0)
	if out, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(late, "b.go"), "")); code != 0 || out != "" {
		t.Fatalf("second edit code = %d, stdout = %q", code, out)
	}
	if got := d.requests(); len(got) != 0 {
		t.Fatalf("second edit requests = %+v, want none", got)
	}
	if got := atomic.LoadInt32(count); got != 0 {
		t.Fatalf("second edit ran git %d times, want none", got)
	}
}

func TestHookPreEditWriteCreatesNewNestedPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)
	repo, sibling, _ := makePreEditRepo(t)

	// The folder newDir does not exist yet, so the path is resolved through its
	// nearest existing ancestor.
	target := filepath.Join(sibling, "newDir", "deep", "a.go")
	out, code := runPreEdit(t, preEditJSON(repo, "file_path", target, ""))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	reqs := d.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodPut {
		t.Fatalf("requests = %+v, want one PUT", reqs)
	}
	if got, want := preEditReportPath(t, reqs[0]), resolved(t, sibling); got != want {
		t.Fatalf("reported path = %q, want %q", got, want)
	}
}

func TestHookPreEditWriteNewWorktreeNewPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)
	root := resolved(t, t.TempDir())
	repo := filepath.Join(root, "repo")
	initUseRepo(t, repo)
	if _, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(repo, "a.go"), "")); code != 0 {
		t.Fatalf("home edit code = %d", code)
	}
	fresh := filepath.Join(root, "fresh")
	useGit(t, repo, "worktree", "add", "-q", fresh, "-b", "fresh")
	d.reset()

	// A not-yet-existing nested path inside the new worktree: git is asked from
	// the nearest existing ancestor (the worktree root).
	target := filepath.Join(fresh, "newDir", "a.go")
	out, code := runPreEdit(t, preEditJSON(repo, "file_path", target, ""))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	reqs := d.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodPut {
		t.Fatalf("requests = %+v, want one PUT", reqs)
	}
	if got, want := preEditReportPath(t, reqs[0]), resolved(t, fresh); got != want {
		t.Fatalf("reported path = %q, want %q", got, want)
	}
}

func TestHookPreEditDaemonDownKeepsState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	d := servePreEditDaemon(t, home)
	repo, sibling, _ := makePreEditRepo(t)

	// Report a sibling so the state holds a current worktree.
	if _, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(sibling, "a.go"), "")); code != 0 {
		t.Fatalf("first edit code = %d", code)
	}
	statePath := hookPreEditStatePath(home, "ws-1")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	d.reset()

	// The daemon goes down, and an edit in another worktree needs a PUT.
	writeDaemonSecretJSON(t, home, "127.0.0.1:1", "s")
	other := filepath.Join(filepath.Dir(repo), "other-wt")
	useGit(t, repo, "worktree", "add", "-q", other, "-b", "other-wt")

	out, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(other, "a.go"), ""))
	if code != 0 || out != "" {
		t.Fatalf("code = %d, stdout = %q, want exit 0 with no stdout", code, out)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("state changed with the daemon down:\n%s\n%s", before, after)
	}
}

func TestHookPreEditStateFileMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	servePreEditDaemon(t, home)
	repo, sibling, _ := makePreEditRepo(t)

	if _, code := runPreEdit(t, preEditJSON(repo, "file_path", filepath.Join(sibling, "a.go"), "")); code != 0 {
		t.Fatalf("code = %d", code)
	}
	statePath := hookPreEditStatePath(home, "ws-1")
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != hookPreEditStateFileMode {
		t.Fatalf("state mode = %o, want %o", got, hookPreEditStateFileMode)
	}
	dirInfo, err := os.Stat(filepath.Dir(statePath))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != hookPreEditStateDirMode {
		t.Fatalf("state dir mode = %o, want %o", got, hookPreEditStateDirMode)
	}
}

func TestHookPreEditAlwaysQuietAndZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	stubHookPane(t, "ws-1", true)
	servePreEditDaemon(t, home)

	for _, input := range []string{
		"not json",
		"{}",
		`{"cwd":"/repo"}`,
		`{"cwd":"/repo","tool_input":{"file_path":""}}`,
		`{"cwd":"/repo","tool_name":"Edit","tool_input":{"notebook_path":"/repo/n.ipynb"}}`,
	} {
		out, code := runPreEdit(t, input)
		if code != 0 {
			t.Errorf("input %q: code = %d, want 0", input, code)
		}
		if out != "" {
			t.Errorf("input %q: stdout = %q, want empty", input, out)
		}
	}
}
