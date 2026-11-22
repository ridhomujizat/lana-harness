package web

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
)

// reportGit runs git in dir and fails the test when it exits non-zero, with an
// environment that does not depend on the machine's git configuration.
func reportGit(t *testing.T, dir string, args ...string) {
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

// initReportRepo makes a repository on main with one commit.
func initReportRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	reportGit(t, dir, "init", "-b", "main")
	reportGit(t, dir, "commit", "--allow-empty", "-m", "init")
}

// reportBody is the {"path": ...} body of the report route.
func reportBody(t *testing.T, path string) string {
	t.Helper()
	data, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// reportFixture is a server with a fake Workbench, a temp Lana home and a git
// repository the report path policy allows.
type reportFixture struct {
	s      *Server
	home   string
	root   string
	repo   string
	fake   *fakeWorkbench
	cookie *http.Cookie
	csrf   string
}

// newReportFixture builds the fixture and installs the given sessions.
func newReportFixture(t *testing.T, sessions ...workbench.Session) *reportFixture {
	t.Helper()
	home := t.TempDir()
	token := authTokenFor(t, home)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initReportRepo(t, repo)
	s := newAuthServer(t, home, io.Discard)
	// The path policy allows the whole temp root, like a Lana home.
	s.previewHome = root
	s.previewTemps = []string{root}
	cookie, csrf := loginAndSession(t, s, token)
	fake := newFakeWorkbench()
	fake.sessions = sessions
	s.SetWorkbench(fake)
	return &reportFixture{s: s, home: home, root: root, repo: repo, fake: fake, cookie: cookie, csrf: csrf}
}

// reportList is the GET /v1/workbench/sessions answer.
type reportList struct {
	Sessions []workbench.Session `json:"sessions"`
}

func listWorkbenchSessions(t *testing.T, s *Server, cookie *http.Cookie, csrf string) reportList {
	t.Helper()
	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions", "", cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var list reportList
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	return list
}

func TestWorkbenchReportPut(t *testing.T) {
	fx := newReportFixture(t, workbench.Session{ID: "abc", Agent: "claude"})

	rr := workbenchRequest(fx.s, http.MethodPut, "/v1/workbench/sessions/abc/worktree", reportBody(t, fx.repo), fx.cookie, fx.csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var answer workbenchReportAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Session != "abc" || answer.Agent != "claude" {
		t.Fatalf("answer = %+v", answer)
	}
	if answer.WorkDir == "" || answer.Worktree.Worktree != answer.WorkDir {
		t.Fatalf("work_dir = %q, worktree = %+v", answer.WorkDir, answer.Worktree)
	}
	if answer.Worktree.Branch != "main" || answer.Worktree.Short != "main" || answer.Worktree.Repo == "" {
		t.Fatalf("worktree = %+v", answer.Worktree)
	}
	if answer.ReportedAt.IsZero() {
		t.Fatal("reported_at is zero")
	}

	// The file holds the report keyed by the session.
	body, err := os.ReadFile(filepath.Join(fx.home, "workbench", "worktree-reports.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]workbenchReport
	if err := json.Unmarshal(body, &stored); err != nil {
		t.Fatalf("stored file %q: %v", body, err)
	}
	rep, ok := stored["abc"]
	if !ok || rep.WorkDir != answer.WorkDir || rep.Agent != "claude" {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestWorkbenchReportUnknownSession(t *testing.T) {
	fx := newReportFixture(t)

	rr := workbenchRequest(fx.s, http.MethodPut, "/v1/workbench/sessions/missing/worktree", reportBody(t, fx.repo), fx.cookie, fx.csrf)
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

func TestWorkbenchReportNotARepo(t *testing.T) {
	fx := newReportFixture(t, workbench.Session{ID: "abc", Agent: "claude"})
	plain := filepath.Join(fx.root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	rr := workbenchRequest(fx.s, http.MethodPut, "/v1/workbench/sessions/abc/worktree", reportBody(t, plain), fx.cookie, fx.csrf)
	assertAuthError(t, rr, http.StatusNotFound, "not_a_repo")
}

func TestWorkbenchReportForbiddenPath(t *testing.T) {
	fx := newReportFixture(t, workbench.Session{ID: "abc", Agent: "claude"})
	// A repository outside the policy's home.
	other := filepath.Join(t.TempDir(), "repo")
	initReportRepo(t, other)

	rr := workbenchRequest(fx.s, http.MethodPut, "/v1/workbench/sessions/abc/worktree", reportBody(t, other), fx.cookie, fx.csrf)
	assertAuthError(t, rr, http.StatusForbidden, "forbidden_path")
}

func TestWorkbenchReportDelete(t *testing.T) {
	fx := newReportFixture(t, workbench.Session{ID: "abc", Agent: "claude"})

	rr := workbenchRequest(fx.s, http.MethodPut, "/v1/workbench/sessions/abc/worktree", reportBody(t, fx.repo), fx.cookie, fx.csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("put = %d, body = %q", rr.Code, rr.Body.String())
	}

	rr = workbenchRequest(fx.s, http.MethodDelete, "/v1/workbench/sessions/abc/worktree", "", fx.cookie, fx.csrf)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, body = %q", rr.Code, rr.Body.String())
	}
	if _, ok := fx.s.workbenchReports.get("abc"); ok {
		t.Fatal("the report was not removed")
	}

	// A second delete is still 204.
	rr = workbenchRequest(fx.s, http.MethodDelete, "/v1/workbench/sessions/abc/worktree", "", fx.cookie, fx.csrf)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("second delete = %d", rr.Code)
	}
}

func TestWorkbenchListCarriesWorkDir(t *testing.T) {
	fx := newReportFixture(t, workbench.Session{ID: "abc", Agent: "claude"})

	rr := workbenchRequest(fx.s, http.MethodPut, "/v1/workbench/sessions/abc/worktree", reportBody(t, fx.repo), fx.cookie, fx.csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("put = %d, body = %q", rr.Code, rr.Body.String())
	}
	var answer workbenchReportAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}

	list := listWorkbenchSessions(t, fx.s, fx.cookie, fx.csrf)
	if len(list.Sessions) != 1 {
		t.Fatalf("sessions = %+v", list.Sessions)
	}
	got := list.Sessions[0]
	if got.WorkDir != answer.WorkDir || got.WorkDirAt == nil {
		t.Fatalf("session = %+v, want work_dir %q", got, answer.WorkDir)
	}
}

func TestWorkbenchReportEndRules(t *testing.T) {
	restored := time.Unix(1000, 0)
	cases := []struct {
		name     string
		session  workbench.Session
		wantKeep bool
	}{
		{"exited", workbench.Session{ID: "abc", Agent: "claude", State: workbenchExitedState}, false},
		{"another agent", workbench.Session{ID: "abc", Agent: "codex"}, false},
		{"agent gone without restore", workbench.Session{ID: "abc"}, false},
		{"restored resumes the agent", workbench.Session{ID: "abc", Restored: &restored, ResumedAgent: "claude"}, true},
		{"restored and the agent runs again", workbench.Session{ID: "abc", Agent: "claude", Restored: &restored, ResumedAgent: "claude"}, true},
		{"missing from the list", workbench.Session{ID: "xyz", Agent: "claude"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newReportFixture(t, workbench.Session{ID: "abc", Agent: "claude"})
			rr := workbenchRequest(fx.s, http.MethodPut, "/v1/workbench/sessions/abc/worktree", reportBody(t, fx.repo), fx.cookie, fx.csrf)
			if rr.Code != http.StatusOK {
				t.Fatalf("put = %d, body = %q", rr.Code, rr.Body.String())
			}

			fx.fake.mu.Lock()
			fx.fake.sessions = []workbench.Session{tc.session}
			fx.fake.mu.Unlock()

			list := listWorkbenchSessions(t, fx.s, fx.cookie, fx.csrf)
			kept := false
			for _, sess := range list.Sessions {
				if sess.ID == tc.session.ID && sess.WorkDir != "" {
					kept = true
				}
			}
			if kept != tc.wantKeep {
				t.Fatalf("work_dir kept = %v, want %v; sessions = %+v", kept, tc.wantKeep, list.Sessions)
			}
			if _, ok := fx.s.workbenchReports.get("abc"); ok != tc.wantKeep {
				t.Fatalf("stored report present = %v, want %v", ok, tc.wantKeep)
			}
		})
	}
}

func TestWorkbenchReportPersistReload(t *testing.T) {
	fx := newReportFixture(t, workbench.Session{ID: "abc", Agent: "claude"})
	rr := workbenchRequest(fx.s, http.MethodPut, "/v1/workbench/sessions/abc/worktree", reportBody(t, fx.repo), fx.cookie, fx.csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("put = %d, body = %q", rr.Code, rr.Body.String())
	}
	var answer workbenchReportAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}

	// A new server on the same home loads the report from disk.
	s2 := newAuthServer(t, fx.home, io.Discard)
	s2.previewHome = fx.root
	s2.previewTemps = []string{fx.root}
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude"}}
	s2.SetWorkbench(fake)
	cookie, csrf := loginAndSession(t, s2, testAuthToken)

	list := listWorkbenchSessions(t, s2, cookie, csrf)
	if len(list.Sessions) != 1 || list.Sessions[0].WorkDir != answer.WorkDir {
		t.Fatalf("reloaded sessions = %+v, want work_dir %q", list.Sessions, answer.WorkDir)
	}
}

func TestWorkbenchLocalReportRoutes(t *testing.T) {
	fx := newReportFixture(t, workbench.Session{ID: "abc", Agent: "claude"})
	fx.s.SetLocalSecret(localTestSecret)

	put := func(secret string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/v1/local/workbench/sessions/abc/worktree", strings.NewReader(reportBody(t, fx.repo)))
		req.Host = authTestAddr
		req.RemoteAddr = localTestAddr
		if secret != "" {
			req.Header.Set(LocalSecretHeader, secret)
		}
		return serveAuth(fx.s, req)
	}

	// Without the daemon secret the route is refused.
	assertAuthError(t, put(""), http.StatusUnauthorized, "unauthorized")

	// With it the report goes through, like the browser route.
	rr := put(localTestSecret)
	if rr.Code != http.StatusOK {
		t.Fatalf("local put = %d, body = %q", rr.Code, rr.Body.String())
	}
	var answer workbenchReportAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Session != "abc" || answer.WorkDir == "" {
		t.Fatalf("answer = %+v", answer)
	}

	// The local DELETE removes it.
	req := httptest.NewRequest(http.MethodDelete, "/v1/local/workbench/sessions/abc/worktree", nil)
	req.Host = authTestAddr
	req.RemoteAddr = localTestAddr
	req.Header.Set(LocalSecretHeader, localTestSecret)
	if rr := serveAuth(fx.s, req); rr.Code != http.StatusNoContent {
		t.Fatalf("local delete = %d, body = %q", rr.Code, rr.Body.String())
	}

	// The local create route requires the secret too.
	create := httptest.NewRequest(http.MethodPost, "/v1/local/workbench/git/worktrees", strings.NewReader("{}"))
	create.Host = authTestAddr
	create.RemoteAddr = localTestAddr
	assertAuthError(t, serveAuth(fx.s, create), http.StatusUnauthorized, "unauthorized")
}

func TestWorkbenchReportEventBus(t *testing.T) {
	s, addr := newWorkbenchLiveServer(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initReportRepo(t, repo)
	s.previewHome = root
	s.previewTemps = []string{root}
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude"}}
	s.SetWorkbench(fake)
	s.SetLocalSecret(localTestSecret)

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

	// A report change publishes session.updated on the bus.
	put, err := http.NewRequest(http.MethodPut, "http://"+addr+"/v1/local/workbench/sessions/abc/worktree", strings.NewReader(reportBody(t, repo)))
	if err != nil {
		t.Fatal(err)
	}
	put.Header.Set(LocalSecretHeader, localTestSecret)
	presp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = presp.Body.Close() }()
	if presp.StatusCode != http.StatusOK {
		t.Fatalf("local put = %d", presp.StatusCode)
	}

	select {
	case got := <-events:
		if got.name != "session.updated" {
			t.Fatalf("event = %q, want session.updated", got.name)
		}
		var ev struct {
			Session workbench.Session `json:"session"`
		}
		if err := json.Unmarshal([]byte(got.data), &ev); err != nil {
			t.Fatalf("data %q: %v", got.data, err)
		}
		if ev.Session.WorkDir == "" || ev.Session.WorkDirAt == nil {
			t.Fatalf("event session = %+v", ev.Session)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no bus event received")
	}
}
