package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
)

// fakeWorkbench is an in-memory WorkbenchBackend for the /v1/workbench tests.
type fakeWorkbench struct {
	mu          sync.Mutex
	sessions    []workbench.Session
	layout      json.RawMessage
	created     []workbench.CreateOptions
	killed      []string
	restarted   []string
	resizes     []workbenchResizeCall
	attachErr   error
	listErr     error
	stream      *fakeWorkbenchStream
	streams     map[string]*fakeWorkbenchStream
	controlHook func([]byte)
	onCreate    func(workbench.CreateOptions)
	// switches records each SwitchAccount call; switchErr, when set, is its
	// answer, and onSwitch runs before it.
	switches   []workbenchSwitchCall
	switchErr  error
	onSwitch   func(id, configDir string)
	attachSeen []string
	subscribe  chan workbench.Event
}

type workbenchResizeCall struct {
	id         string
	cols, rows int
}

func newFakeWorkbench() *fakeWorkbench {
	return &fakeWorkbench{subscribe: make(chan workbench.Event, 8)}
}

func (f *fakeWorkbench) has(id string) bool {
	for _, s := range f.sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}

func (f *fakeWorkbench) List(context.Context) ([]workbench.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]workbench.Session(nil), f.sessions...), nil
}

func (f *fakeWorkbench) Get(_ context.Context, id string) (workbench.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		if s.ID == id {
			return s, nil
		}
	}
	return workbench.Session{}, workbench.ErrNotFound
}

func (f *fakeWorkbench) Create(_ context.Context, opts workbench.CreateOptions) (workbench.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, opts)
	if f.onCreate != nil {
		f.onCreate(opts)
	}
	sess := workbench.Session{ID: "created", Title: opts.Title, Dir: opts.Dir, Command: opts.Command, Cols: opts.Cols, Rows: opts.Rows, Labels: opts.Labels}
	f.sessions = append(f.sessions, sess)
	return sess, nil
}

func (f *fakeWorkbench) Kill(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has(id) {
		return workbench.ErrNotFound
	}
	f.killed = append(f.killed, id)
	return nil
}

func (f *fakeWorkbench) Restart(_ context.Context, id string) (workbench.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		if s.ID == id {
			f.restarted = append(f.restarted, id)
			return s, nil
		}
	}
	return workbench.Session{}, workbench.ErrNotFound
}

type workbenchSwitchCall struct {
	id, agent, configDir string
}

func (f *fakeWorkbench) SwitchAccount(_ context.Context, id, agent, configDir string) (workbench.Session, error) {
	f.mu.Lock()
	hook := f.onSwitch
	f.mu.Unlock()
	if hook != nil {
		hook(id, configDir)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		if s.ID == id {
			f.switches = append(f.switches, workbenchSwitchCall{id: id, agent: agent, configDir: configDir})
			if f.switchErr != nil {
				return workbench.Session{}, f.switchErr
			}
			s.AgentConfigDir = configDir
			return s, nil
		}
	}
	return workbench.Session{}, workbench.ErrNotFound
}

func (f *fakeWorkbench) Resize(_ context.Context, id string, cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has(id) {
		return workbench.ErrNotFound
	}
	f.resizes = append(f.resizes, workbenchResizeCall{id: id, cols: cols, rows: rows})
	return nil
}

func (f *fakeWorkbench) Layout(context.Context) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.layout, nil
}

func (f *fakeWorkbench) SetLayout(_ context.Context, layout json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.layout = layout
	return nil
}

func (f *fakeWorkbench) Attach(_ context.Context, id, mode string) (io.ReadWriteCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attachSeen = append(f.attachSeen, id+"/"+mode)
	if f.attachErr != nil {
		return nil, f.attachErr
	}
	stream := newFakeWorkbenchStream()
	if mode == workbenchModeControl {
		stream.hook = f.controlHook
	}
	if f.streams == nil {
		f.streams = map[string]*fakeWorkbenchStream{}
	}
	f.streams[id+"/"+mode] = stream
	f.stream = stream
	return stream, nil
}

// streamFor returns the most recent stream of one session and mode, or nil.
func (f *fakeWorkbench) streamFor(id, mode string) *fakeWorkbenchStream {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.streams[id+"/"+mode]
}

// pushView feeds output into a session's view stream, as a pane would.
func (f *fakeWorkbench) pushView(id string, data []byte) {
	if s := f.streamFor(id, workbenchModeView); s != nil {
		s.push(data)
	}
}

// setSession mutates one session under the fake's lock, so a test changing a
// session while a hub polls it is race-free.
func (f *fakeWorkbench) setSession(id string, mutate func(*workbench.Session)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.sessions {
		if f.sessions[i].ID == id {
			mutate(&f.sessions[i])
			return
		}
	}
}

// fakeWorkbenchStream is an io.Pipe-based core connection: it records what the
// handler writes and echoes it back, so a control attach round-trips. The echo
// is written from its own goroutine so a write never blocks a caller with no
// reader (the chat hub does not read its control attachment).
type fakeWorkbenchStream struct {
	mu      sync.Mutex
	written []byte
	closed  bool
	hook    func([]byte)
	outR    *io.PipeReader
	outW    *io.PipeWriter
}

func newFakeWorkbenchStream() *fakeWorkbenchStream {
	r, w := io.Pipe()
	return &fakeWorkbenchStream{outR: r, outW: w}
}

func (s *fakeWorkbenchStream) Read(p []byte) (int, error) { return s.outR.Read(p) }

func (s *fakeWorkbenchStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.written = append(s.written, p...)
	hook := s.hook
	s.mu.Unlock()
	if hook != nil {
		hook(p)
	}
	echo := append([]byte(nil), p...)
	go func() { _, _ = s.outW.Write(echo) }()
	return len(p), nil
}

// push writes bytes into the stream as if the pty produced them.
func (s *fakeWorkbenchStream) push(data []byte) {
	echo := append([]byte(nil), data...)
	go func() { _, _ = s.outW.Write(echo) }()
}

func (s *fakeWorkbenchStream) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	_ = s.outR.CloseWithError(io.EOF)
	_ = s.outW.CloseWithError(io.EOF)
	return nil
}

func (s *fakeWorkbenchStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *fakeWorkbenchStream) input() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.written...)
}

func (f *fakeWorkbench) Subscribe(context.Context) (<-chan workbench.Event, error) {
	return f.subscribe, nil
}

// newWorkbenchServer returns a server with a logged-in owner cookie and its
// CSRF value. The server address is authTestAddr.
func newWorkbenchServer(t *testing.T) (*Server, *http.Cookie, string) {
	t.Helper()
	home := t.TempDir()
	token := authTokenFor(t, home)
	s := newAuthServer(t, home, io.Discard)
	cookie, csrf := loginAndSession(t, s, token)
	return s, cookie, csrf
}

// newWorkbenchLiveServer starts a server on a real listener so the SSE and
// WebSocket routes can be exercised end to end. It returns the bound address.
func newWorkbenchLiveServer(t *testing.T) (*Server, string) {
	t.Helper()
	s, err := New(Config{Addr: "127.0.0.1:0", Enabled: true, Home: t.TempDir(), Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s, s.Addr()
}

// workbenchSessionCookie mints a browser session without the login flow, so a
// live-server test can call the authenticated routes directly.
func workbenchSessionCookie(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	value, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.sessions.add(value, "test", s.nowTime()); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookieName, Value: value}
}

func workbenchRequest(s *Server, method, target, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	req := authRequest(method, target, body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	if isStateChanging(method) {
		req.Header.Set("Origin", authTestOrigin)
	}
	return serveAuth(s, req)
}

func TestWorkbenchUnavailableWithoutBackend(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)

	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusServiceUnavailable, "workbench_unavailable")

	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions", "{}", cookie, csrf)
	assertAuthError(t, rr, http.StatusServiceUnavailable, "workbench_unavailable")
}

// A create passes its input line to the core; a line break or an input over
// the limit is a 400 that creates nothing.
func TestWorkbenchCreateInput(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	fake := newFakeWorkbench()
	s.SetWorkbench(fake)

	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions", `{"input":"env -u CLAUDE_CONFIG_DIR claude"}`, cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if len(fake.created) != 1 || fake.created[0].Input != "env -u CLAUDE_CONFIG_DIR claude" || len(fake.created[0].Command) != 0 {
		t.Fatalf("created = %+v", fake.created)
	}

	for _, body := range []string{
		`{"input":"claude\nrm -rf /"}`,
		`{"input":"claude\r"}`,
		`{"input":"` + strings.Repeat("a", workbench.MaxCreateInput+1) + `"}`,
	} {
		rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions", body, cookie, csrf)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("create %.40q status = %d, want 400", body, rr.Code)
		}
	}
	if len(fake.created) != 1 {
		t.Fatalf("create calls = %d, want 1", len(fake.created))
	}
}

func TestWorkbenchListCreateKillRestart(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Title: "one"}}
	s.SetWorkbench(fake)

	// List.
	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions", "", cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var list struct {
		Sessions []workbench.Session `json:"sessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].ID != "abc" {
		t.Fatalf("list = %+v", list.Sessions)
	}

	// Create with a leading ~ expands to the home directory.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions", `{"dir":"~/proj","cols":80,"rows":24,"title":"t"}`, cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if len(fake.created) != 1 {
		t.Fatalf("create calls = %d", len(fake.created))
	}
	if want := filepath.Join(home, "proj"); fake.created[0].Dir != want {
		t.Fatalf("create dir = %q, want %q", fake.created[0].Dir, want)
	}

	// An empty dir is left alone.
	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions", `{}`, cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("create status = %d", rr.Code)
	}
	if fake.created[1].Dir != "" {
		t.Fatalf("create dir = %q, want empty", fake.created[1].Dir)
	}

	// Restart.
	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/restart", "", cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("restart status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if len(fake.restarted) != 1 || fake.restarted[0] != "abc" {
		t.Fatalf("restarted = %v", fake.restarted)
	}

	// Kill.
	rr = workbenchRequest(s, http.MethodDelete, "/v1/workbench/sessions/abc", "", cookie, csrf)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("kill status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if len(fake.killed) != 1 || fake.killed[0] != "abc" {
		t.Fatalf("killed = %v", fake.killed)
	}
}

func TestWorkbenchNotFoundMapping(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	s.SetWorkbench(newFakeWorkbench())

	rr := workbenchRequest(s, http.MethodDelete, "/v1/workbench/sessions/missing", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusNotFound, "not_found")

	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/missing/restart", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusNotFound, "not_found")

	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/missing/resize", `{"cols":10,"rows":5}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

func TestWorkbenchUnreachableIs503WithReason(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	fake := newFakeWorkbench()
	fake.listErr = errors.New("workbench: starting the core: connection refused")
	s.SetWorkbench(fake)

	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions", "", cookie, csrf)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "workbench_unavailable" || !strings.Contains(env.Error.Message, "connection refused") {
		t.Fatalf("error = %+v", env.Error)
	}
}

func TestWorkbenchResizeValidation(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc"}}
	s.SetWorkbench(fake)

	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/resize", `{"cols":100,"rows":40}`, cookie, csrf)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("resize status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if len(fake.resizes) != 1 || fake.resizes[0].cols != 100 || fake.resizes[0].rows != 40 {
		t.Fatalf("resizes = %+v", fake.resizes)
	}

	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/resize", `{"cols":0,"rows":0}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}

func TestWorkbenchCSRFRequiredOnPost(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	s.SetWorkbench(newFakeWorkbench())

	// The CSRF header is missing, so the create is refused before the backend.
	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions", "{}", cookie, "")
	assertAuthError(t, rr, http.StatusForbidden, "csrf_invalid")

	// With the token the create goes through.
	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions", "{}", cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestWorkbenchLayout(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	fake := newFakeWorkbench()
	s.SetWorkbench(fake)

	// An empty layout is "{}".
	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/layout", "", cookie, csrf)
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "{}" {
		t.Fatalf("empty layout = %d %q", rr.Code, rr.Body.String())
	}

	// A PUT stores the raw document and a GET returns it.
	rr = workbenchRequest(s, http.MethodPut, "/v1/workbench/layout", `{"spaces":[1]}`, cookie, csrf)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("put status = %d, body = %q", rr.Code, rr.Body.String())
	}
	rr = workbenchRequest(s, http.MethodGet, "/v1/workbench/layout", "", cookie, csrf)
	if got := strings.TrimSpace(rr.Body.String()); got != `{"spaces":[1]}` {
		t.Fatalf("layout = %q", got)
	}

	// Invalid JSON is refused.
	rr = workbenchRequest(s, http.MethodPut, "/v1/workbench/layout", `{not json`, cookie, csrf)
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")

	// A too-large document is refused.
	big := make([]byte, workbenchLayoutLimit+1)
	for i := range big {
		big[i] = 'x'
	}
	rr = workbenchRequest(s, http.MethodPut, "/v1/workbench/layout", string(big), cookie, csrf)
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}

func TestWorkbenchEventsSSE(t *testing.T) {
	s, addr := newWorkbenchLiveServer(t)
	fake := newFakeWorkbench()
	s.SetWorkbench(fake)
	cookie := workbenchSessionCookie(t, s)

	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/workbench/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d", resp.StatusCode)
	}

	fake.subscribe <- workbench.Event{Type: "session.created", Session: &workbench.Session{ID: "abc"}}

	lines := make(chan string, 2)
	go func() {
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "event: ") {
				lines <- strings.TrimSpace(line)
				return
			}
		}
	}()
	select {
	case got := <-lines:
		if got != "event: session.created" {
			t.Fatalf("event line = %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no SSE event received")
	}
}

func dialWorkbench(t *testing.T, addr, id, mode, origin string, cookie *http.Cookie) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	header := http.Header{}
	if cookie != nil {
		header.Set("Cookie", cookie.Name+"="+cookie.Value)
	}
	if origin != "" {
		header.Set("Origin", origin)
	}
	url := "ws://" + addr + "/v1/workbench/sessions/" + id + "/attach?mode=" + mode
	return websocket.Dial(context.Background(), url, &websocket.DialOptions{HTTPHeader: header})
}

func TestWorkbenchAttachWrongOrigin(t *testing.T) {
	s, addr := newWorkbenchLiveServer(t)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc"}}
	s.SetWorkbench(fake)
	cookie := workbenchSessionCookie(t, s)

	conn, resp, err := dialWorkbench(t, addr, "abc", "view", "http://evil.example", cookie)
	if err == nil {
		_ = conn.CloseNow()
		t.Fatal("attach succeeded with a foreign origin")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestWorkbenchAttachControlEcho(t *testing.T) {
	s, addr := newWorkbenchLiveServer(t)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc"}}
	s.SetWorkbench(fake)
	cookie := workbenchSessionCookie(t, s)

	conn, _, err := dialWorkbench(t, addr, "abc", "control", "http://"+addr, cookie)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageBinary || string(data) != "ping" {
		t.Fatalf("echo = (%v, %q)", typ, data)
	}
}

func TestWorkbenchAttachViewDropsInput(t *testing.T) {
	s, addr := newWorkbenchLiveServer(t)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc"}}
	s.SetWorkbench(fake)
	cookie := workbenchSessionCookie(t, s)

	conn, _, err := dialWorkbench(t, addr, "abc", "view", "http://"+addr, cookie)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":80,"rows":24}`)); err != nil {
		t.Fatal(err)
	}
	// Close completes the handshake, so the server has read and dropped both
	// messages before this returns.
	if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if got := fake.stream.input(); len(got) != 0 {
		t.Fatalf("view mode wrote input %q", got)
	}
	if len(fake.resizes) != 0 {
		t.Fatalf("view mode resized %+v", fake.resizes)
	}
}

func TestWorkbenchAttachResizeTextMessage(t *testing.T) {
	s, addr := newWorkbenchLiveServer(t)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc"}}
	s.SetWorkbench(fake)
	cookie := workbenchSessionCookie(t, s)

	conn, _, err := dialWorkbench(t, addr, "abc", "control", "http://"+addr, cookie)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":123,"rows":45}`)); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.resizes) != 1 || fake.resizes[0].cols != 123 || fake.resizes[0].rows != 45 {
		t.Fatalf("resizes = %+v", fake.resizes)
	}
}

func TestWorkbenchAttachUnknownSession(t *testing.T) {
	s, addr := newWorkbenchLiveServer(t)
	s.SetWorkbench(newFakeWorkbench())
	cookie := workbenchSessionCookie(t, s)

	conn, resp, err := dialWorkbench(t, addr, "missing", "control", "http://"+addr, cookie)
	if err == nil {
		_ = conn.CloseNow()
		t.Fatal("attach succeeded for an unknown session")
	}
	if resp != nil && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestWorkbenchAttachGoneCloseCode(t *testing.T) {
	s, addr := newWorkbenchLiveServer(t)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc"}}
	fake.attachErr = workbench.ErrNotFound
	s.SetWorkbench(fake)
	cookie := workbenchSessionCookie(t, s)

	conn, _, err := dialWorkbench(t, addr, "abc", "control", "http://"+addr, cookie)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != workbenchCloseNotFound {
		t.Fatalf("close status = %v, want 4404", websocket.CloseStatus(err))
	}
}
