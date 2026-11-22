package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
	"github.com/ridhomujizat/lana-harness/internal/workbench/gitinfo"
)

// rootFixture is a server whose path policy allows a temp tree holding a folder
// of repos, the shape of a spec 70 root (REQ-WM-001).
type rootFixture struct {
	s      *Server
	cookie *http.Cookie
	csrf   string
	root   string // the allowed temp tree
	folder string // the root folder: a folder of repos, not a repo itself
}

// newRootFixture builds the fixture with two child repos, a plain folder and a
// file, and closes the root watcher at the end of the test.
func newRootFixture(t *testing.T, backend WorkbenchBackend) *rootFixture {
	t.Helper()
	s, cookie, csrf, root := newGitEnv(t, backend)
	t.Cleanup(s.closeWorkbenchRootWatchers)
	folder := filepath.Join(root, "multi")
	for _, name := range []string{"alpha", "beta"} {
		gitRepo(t, filepath.Join(folder, name))
	}
	if err := os.MkdirAll(filepath.Join(folder, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitWriteFile(t, filepath.Join(folder, "README.md"), []byte("read me\n"))
	return &rootFixture{s: s, cookie: cookie, csrf: csrf, root: root, folder: folder}
}

// subscribe PUTs a root subscription with the given client and returns the
// response.
func (fx *rootFixture) subscribe(t *testing.T, client string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"root":` + strconvQuote(fx.folder) + `,"client":` + strconvQuote(client) + `}`
	return workbenchRequest(fx.s, http.MethodPut, "/v1/workbench/git/root/subscribe", body, fx.cookie, fx.csrf)
}

func TestWorkbenchRootResolve(t *testing.T) {
	s, cookie, csrf, root := newGitEnv(t, newFakeWorkbench())
	multi := filepath.Join(root, "multi")
	gitRepo(t, filepath.Join(multi, "alpha"))
	gitRepo(t, filepath.Join(multi, "beta"))
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	body := `{"dirs":[` + strconvQuote(multi) + `,` + strconvQuote(plain) + `]}`
	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/git/resolve", body, cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var resp struct {
		Dirs map[string]map[string]json.RawMessage `json:"dirs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	rootAnswer, ok := resp.Dirs[multi]
	if !ok {
		t.Fatalf("no answer for %q: %v", multi, resp.Dirs)
	}
	if got := string(rootAnswer["repo"]); got != "null" {
		t.Fatalf("root repo = %s, want null", got)
	}
	raw, ok := rootAnswer["root"]
	if !ok {
		t.Fatalf("no root field: %v", rootAnswer)
	}
	var info struct {
		Path  string `json:"path"`
		Name  string `json:"name"`
		Repos int    `json:"repos"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	if info.Path != multi || info.Name != "multi" || info.Repos != 2 {
		t.Fatalf("root = %+v, want path %q, name multi, 2 repos", info, multi)
	}

	plainAnswer, ok := resp.Dirs[plain]
	if !ok {
		t.Fatalf("no answer for %q", plain)
	}
	if _, ok := plainAnswer["root"]; ok {
		t.Fatalf("plain dir has a root field: %v", plainAnswer)
	}
	if got := string(plainAnswer["repo"]); got != "null" {
		t.Fatalf("plain repo = %s, want null", got)
	}
}

func TestWorkbenchRootSummary(t *testing.T) {
	fx := newRootFixture(t, newFakeWorkbench())
	if rr := fx.subscribe(t, "c1"); rr.Code != http.StatusNoContent {
		t.Fatalf("subscribe = %d, body = %q", rr.Code, rr.Body.String())
	}

	target := gitTarget("/v1/workbench/git/root", map[string]string{"dir": fx.folder})
	rr := gitGet(fx.s, fx.cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	var summary gitinfo.RootSummary
	if err := json.Unmarshal(rr.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Root != fx.folder || summary.Name != "multi" {
		t.Fatalf("summary = %+v", summary)
	}
	if len(summary.Repos) != 2 || summary.Repos[0].Name != "alpha" || summary.Repos[1].Name != "beta" {
		t.Fatalf("repos = %+v, want alpha and beta", summary.Repos)
	}
	alpha := summary.Repos[0]
	if alpha.Worktree != filepath.Join(fx.folder, "alpha") || alpha.Branch != "main" || alpha.Short != "main" {
		t.Fatalf("alpha = %+v", alpha)
	}
	if alpha.Color == "" {
		t.Fatalf("alpha has no colour: %+v", alpha)
	}
	// The non-repo children are listed (names and kinds only).
	if len(summary.Other) != 2 || summary.Other[0].Name != "docs" || summary.Other[0].Kind != "dir" ||
		summary.Other[1].Name != "README.md" || summary.Other[1].Kind != "file" {
		t.Fatalf("other = %+v", summary.Other)
	}

	// The same ETag returns 304 with no body.
	req := authRequest(http.MethodGet, target, "")
	req.AddCookie(fx.cookie)
	req.Header.Set("If-None-Match", etag)
	rr304 := serveAuth(fx.s, req)
	if rr304.Code != http.StatusNotModified {
		t.Fatalf("304 status = %d, body = %q", rr304.Code, rr304.Body.String())
	}
	if rr304.Body.Len() != 0 {
		t.Fatalf("304 body = %q", rr304.Body.String())
	}
}

func TestWorkbenchRootNotARoot(t *testing.T) {
	s, cookie, _, root := newGitEnv(t, newFakeWorkbench())
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	// No child repos is not a root.
	target := gitTarget("/v1/workbench/git/root", map[string]string{"dir": plain})
	assertAuthError(t, gitGet(s, cookie, target), http.StatusNotFound, "not_a_root")

	// A folder inside a repo is not a root.
	target = gitTarget("/v1/workbench/git/root", map[string]string{"dir": repo})
	assertAuthError(t, gitGet(s, cookie, target), http.StatusNotFound, "not_a_root")

	// A folder the path policy refuses is 403.
	outside := t.TempDir()
	target = gitTarget("/v1/workbench/git/root", map[string]string{"dir": outside})
	assertAuthError(t, gitGet(s, cookie, target), http.StatusForbidden, "forbidden_path")
}

func TestWorkbenchRootSubscribe(t *testing.T) {
	fx := newRootFixture(t, newFakeWorkbench())

	if rr := fx.subscribe(t, "c1"); rr.Code != http.StatusNoContent {
		t.Fatalf("subscribe = %d, body = %q", rr.Code, rr.Body.String())
	}

	// A missing client is 400.
	body := `{"root":` + strconvQuote(fx.folder) + `}`
	rr := workbenchRequest(fx.s, http.MethodPut, "/v1/workbench/git/root/subscribe", body, fx.cookie, fx.csrf)
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")

	// A folder that is not a root is 404.
	plain := filepath.Join(fx.root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	body = `{"root":` + strconvQuote(plain) + `,"client":"c1"}`
	rr = workbenchRequest(fx.s, http.MethodPut, "/v1/workbench/git/root/subscribe", body, fx.cookie, fx.csrf)
	assertAuthError(t, rr, http.StatusNotFound, "not_a_root")

	// The DELETE unsubscribes with 204, also for a client that is not there.
	body = `{"root":` + strconvQuote(fx.folder) + `,"client":"c1"}`
	rr = workbenchRequest(fx.s, http.MethodDelete, "/v1/workbench/git/root/subscribe", body, fx.cookie, fx.csrf)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestWorkbenchRootFilesTree(t *testing.T) {
	fx := newRootFixture(t, newFakeWorkbench())
	gitWriteFile(t, filepath.Join(fx.folder, "Zeta.txt"), []byte("z\n"))

	target := gitTarget("/v1/workbench/files/tree", map[string]string{"dir": fx.folder})

	// Not subscribed: the repo child carries the plain mark only.
	rr := gitGet(fx.s, fx.cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var listing gitinfo.FolderListing
	if err := json.Unmarshal(rr.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	assertRootTreeOrder(t, listing)
	if listing.Entries[0].Repo != true || listing.Entries[0].Short != "" {
		t.Fatalf("alpha mark without a subscription = %+v", listing.Entries[0])
	}

	// Subscribed: the repo details come from the cached summary.
	if rr := fx.subscribe(t, "c1"); rr.Code != http.StatusNoContent {
		t.Fatalf("subscribe = %d, body = %q", rr.Code, rr.Body.String())
	}
	rr = gitGet(fx.s, fx.cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	listing = gitinfo.FolderListing{}
	if err := json.Unmarshal(rr.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	assertRootTreeOrder(t, listing)
	alpha := listing.Entries[0]
	if !alpha.Repo || alpha.Short != "main" || alpha.Color == "" {
		t.Fatalf("alpha mark with a subscription = %+v", alpha)
	}

	// A folder inside a repo is 400 inside_repo with the worktree in the message.
	sub := filepath.Join(fx.folder, "alpha", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	target = gitTarget("/v1/workbench/files/tree", map[string]string{"dir": sub})
	rr = gitGet(fx.s, fx.cookie, target)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("inside repo status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "inside_repo" {
		t.Fatalf("inside repo code = %q, want inside_repo", env.Error.Code)
	}
	if !strings.Contains(env.Error.Message, filepath.Join(fx.folder, "alpha")) {
		t.Fatalf("inside repo message = %q, want the worktree", env.Error.Message)
	}
}

// assertRootTreeOrder checks the fixture listing: folders first then files,
// case-insensitively sorted, with no .git entry.
func assertRootTreeOrder(t *testing.T, listing gitinfo.FolderListing) {
	t.Helper()
	got := make([]string, len(listing.Entries))
	for i, entry := range listing.Entries {
		got[i] = entry.Kind + ":" + entry.Name
		if entry.Name == ".git" {
			t.Fatalf(".git is listed")
		}
	}
	want := []string{"dir:alpha", "dir:beta", "dir:docs", "file:README.md", "file:Zeta.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if listing.Truncated {
		t.Fatalf("listing is truncated")
	}
}

func TestWorkbenchRootFilesFile(t *testing.T) {
	fx := newRootFixture(t, newFakeWorkbench())

	// A text file is the FileContent JSON.
	text := filepath.Join(fx.folder, "notes.txt")
	gitWriteFile(t, text, []byte("hello\nworld\n"))
	target := gitTarget("/v1/workbench/files/file", map[string]string{"path": text})
	rr := gitGet(fx.s, fx.cookie, target)
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
	bin := filepath.Join(fx.folder, "bin.dat")
	gitWriteFile(t, bin, []byte{0x00, 0x01, 0x02, 0x03})
	target = gitTarget("/v1/workbench/files/file", map[string]string{"path": bin})
	rr = gitGet(fx.s, fx.cookie, target)
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

	// A path the policy refuses is 403.
	outside := filepath.Join(t.TempDir(), "x.txt")
	gitWriteFile(t, outside, []byte("x\n"))
	target = gitTarget("/v1/workbench/files/file", map[string]string{"path": outside})
	assertAuthError(t, gitGet(fx.s, fx.cookie, target), http.StatusForbidden, "forbidden_path")
}

// fakeRootClock is a settable clock for the activity cache test.
type fakeRootClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeRootClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeRootClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// countingWorkbench is a fakeWorkbench that counts how often the core session
// list is fetched, so the activity cache of REQ-WM-081 can be checked.
type countingWorkbench struct {
	*fakeWorkbench
	mu    sync.Mutex
	calls int
}

func newCountingWorkbench() *countingWorkbench {
	return &countingWorkbench{fakeWorkbench: newFakeWorkbench()}
}

func (c *countingWorkbench) List(ctx context.Context) ([]workbench.Session, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.fakeWorkbench.List(ctx)
}

func (c *countingWorkbench) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingWorkbench) setSessions(sessions ...workbench.Session) {
	c.fakeWorkbench.mu.Lock()
	c.sessions = sessions
	c.fakeWorkbench.mu.Unlock()
}

func TestWorkbenchRootActivityCache(t *testing.T) {
	backend := newCountingWorkbench()
	s, _, _, root := newGitEnv(t, backend)
	folder := filepath.Join(root, "multi")
	clk := &fakeRootClock{t: time.Now()}
	s.now = clk.now

	out := time.Now().Add(-time.Minute)
	backend.setSessions(
		workbench.Session{ID: "w", Cwd: filepath.Join(folder, "alpha"), AgentState: "working", LastOutput: out},
		workbench.Session{ID: "r", Agent: "claude", Cwd: filepath.Join(root, "elsewhere")},
	)
	// A spec 69 report puts the second session on the root through its
	// work_dir (REQ-WM-010).
	if err := s.workbenchReports.put(workbenchReport{
		Session: "r", WorkDir: filepath.Join(folder, "beta"), ReportedAt: out, Agent: "claude",
	}); err != nil {
		t.Fatal(err)
	}

	act := s.workbenchRootActivity(folder)
	if !act.Working {
		t.Fatalf("activity = %+v, want working", act)
	}
	if !act.LastOutput.Equal(out) {
		t.Fatalf("last output = %v, want %v", act.LastOutput, out)
	}
	if !containsString(act.Dirs, filepath.Join(folder, "alpha")) {
		t.Fatalf("dirs = %v, want the alpha pane", act.Dirs)
	}
	if !containsString(act.Reported, filepath.Join(folder, "beta")) {
		t.Fatalf("reported = %v, want the beta worktree", act.Reported)
	}
	if got := backend.count(); got != 1 {
		t.Fatalf("core list fetches = %d, want 1", got)
	}

	// A second call within the window reuses the cached list.
	s.workbenchRootActivity(folder)
	if got := backend.count(); got != 1 {
		t.Fatalf("core list fetches = %d, want still 1", got)
	}

	// Past the window the list is fetched again.
	clk.add(3 * time.Second)
	s.workbenchRootActivity(folder)
	if got := backend.count(); got != 2 {
		t.Fatalf("core list fetches = %d, want 2 after the window", got)
	}
}

func TestWorkbenchRootEvent(t *testing.T) {
	s, addr := newWorkbenchLiveServer(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, "multi")
	gitRepo(t, filepath.Join(folder, "alpha"))
	s.previewHome = root
	s.previewTemps = []string{root}
	// A working session on the root makes the sweep run every 10 s, so a short
	// tick past it changes the summary without expiring the subscription.
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "s", Cwd: folder, AgentState: "working"}}
	s.SetWorkbench(fake)

	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/workbench/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(workbenchSessionCookie(t, s))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d", resp.StatusCode)
	}

	type sseEvent struct{ name, data string }
	events := make(chan sseEvent, 4)
	go func() {
		reader := bufio.NewReader(resp.Body)
		var cur sseEvent
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				cur.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if cur.name != "" {
					events <- cur
				}
				cur = sseEvent{}
			}
		}
	}()

	if err := s.workbenchRootWatchers().Subscribe(context.Background(), folder, "c1"); err != nil {
		t.Fatal(err)
	}
	// A change in a subscribed repo, then a tick past the busy 10 s sweep.
	gitWriteFile(t, filepath.Join(folder, "alpha", "new.txt"), []byte("x\n"))
	s.workbenchRootWatchers().Tick(time.Now().Add(11 * time.Second))

	select {
	case got := <-events:
		if got.name != "git.root" {
			t.Fatalf("event = %q, want git.root", got.name)
		}
		var ev struct {
			Root  string            `json:"root"`
			Etag  string            `json:"etag"`
			Repos map[string]string `json:"repos"`
		}
		if err := json.Unmarshal([]byte(got.data), &ev); err != nil {
			t.Fatalf("data %q: %v", got.data, err)
		}
		if ev.Root != folder || ev.Etag == "" {
			t.Fatalf("event = %+v", ev)
		}
		if _, ok := ev.Repos["alpha"]; !ok {
			t.Fatalf("event repos = %v, want alpha", ev.Repos)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no git.root event received")
	}
}
