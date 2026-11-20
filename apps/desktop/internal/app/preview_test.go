package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
)

// previewSessionCookie is the daemon's session cookie the fake daemon sets
// (web/auth.go).
const previewSessionCookie = "lana_session"

// previewDaemon is a fake daemon the preview tests run against (REQ-WP-052):
// it signs the app in like the local daemon and answers the preview POST and
// the token GET routes, so the app's whole preview flow runs over HTTP.
type previewDaemon struct {
	server *httptest.Server

	mu       sync.Mutex
	status   int
	body     string
	lastBody []byte
	gets     []string
}

func newPreviewDaemon(t *testing.T) *previewDaemon {
	t.Helper()
	d := &previewDaemon{status: http.StatusOK}
	d.server = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.server.Close)
	return d
}

// addr is the daemon address as $LANA_HOME/daemon.secret holds it.
func (d *previewDaemon) addr() string { return strings.TrimPrefix(d.server.URL, "http://") }

// base is the daemon origin, the value of the Origin header.
func (d *previewDaemon) base() string { return d.server.URL }

// answer sets the preview POST answer: the status and the raw JSON body.
func (d *previewDaemon) answer(status int, body string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status, d.body = status, body
}

// request is the body of the last preview POST.
func (d *previewDaemon) request() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return string(d.lastBody)
}

func (d *previewDaemon) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/local/app-link":
		writeJSONTest(w, http.StatusOK, map[string]string{"link": d.base() + "/v1/auth/app?code=1"})
	case r.URL.Path == "/v1/auth/app":
		http.SetCookie(w, &http.Cookie{Name: previewSessionCookie, Value: "sess", Path: "/"})
		http.Redirect(w, r, "/#/chats", http.StatusSeeOther)
	case r.URL.Path == "/v1/auth/session":
		writeJSONTest(w, http.StatusOK, map[string]any{"csrf": "csrf-1", "expires_at": time.Now().Add(time.Hour).UnixMilli()})
	case r.URL.Path == lana.PreviewPath && r.Method == http.MethodPost:
		d.handlePreview(w, r)
	case strings.HasPrefix(r.URL.Path, lana.PreviewPath+"/"):
		d.handlePreviewFile(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handlePreview records the body and answers the chosen status and body.
func (d *previewDaemon) handlePreview(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	d.mu.Lock()
	d.lastBody = body
	status, out := d.status, d.body
	d.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, out)
}

// handlePreviewFile serves a token GET with no cookie, as REQ-WP-004 requires.
func (d *previewDaemon) handlePreviewFile(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	d.gets = append(d.gets, r.URL.Path)
	d.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "file body")
}

// connect signs the app in to the fake daemon, as the local connection does.
func (d *previewDaemon) connect(t *testing.T) *lana.Local {
	t.Helper()
	home := t.TempDir()
	secret := fmt.Sprintf("{\"addr\":%q,\"secret\":%q}\n", d.addr(), "local-secret")
	if err := os.WriteFile(filepath.Join(home, lana.SecretFileName), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	conn := lana.NewLocal(home)
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return conn
}

// previewApp is the test app talking to the fake daemon (REQ-WP-052). The
// pane terminals take a fake attachment, so no pane dials the daemon.
func previewApp(t *testing.T) (*App, *previewDaemon) {
	t.Helper()
	d := newPreviewDaemon(t)
	a := testApp(t)
	a.conn = d.connect(t)
	a.attach = func(context.Context, string) (paneAttachment, error) { return &fakeAttachment{}, nil }
	return a, d
}

// openPreview runs OpenPreview, waits for its background call to post and runs
// the posted work on the test's thread, restoring the app's post seam
// afterwards. It reports whether the link was handled.
func openPreview(t *testing.T, a *App, session, link, cwd string) bool {
	t.Helper()
	posted := make(chan func(), 1)
	prev := a.post
	a.post = func(fn func()) { posted <- fn }
	handled := a.OpenPreview(session, link, cwd)
	if !handled {
		a.post = prev
		return false
	}
	select {
	case fn := <-posted:
		fn()
	case <-time.After(2 * time.Second):
		a.post = prev
		t.Fatal("OpenPreview did not finish")
	}
	a.post = prev
	return true
}

// writeJSONTest writes a JSON answer, for the fake daemon.
func writeJSONTest(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// TestOpenPreviewSetsThePreview is REQ-DW-080/REQ-WP-040: ⌘+click on a file
// link has the daemon resolve it, and the panel shows the absolute URL of the
// page.
func TestOpenPreviewSetsThePreview(t *testing.T) {
	a, d := previewApp(t)
	d.answer(http.StatusOK, `{"path":"/tmp/work/main.go","name":"main.go","dir":"/tmp/work","kind":"text","size":13,"line":12,"col":0,"url":"/v1/workbench/preview/tok1/main.go?view=1#L12","raw_url":"/v1/workbench/preview/tok1/main.go"}`)

	if !openPreview(t, a, "s1", "main.go:12", "/tmp/work/api") {
		t.Fatal("OpenPreview did not handle the link")
	}
	if a.preview == nil {
		t.Fatal("OpenPreview set no preview")
	}
	if a.preview.path != "/tmp/work/main.go" {
		t.Errorf("preview path = %q, want %q", a.preview.path, "/tmp/work/main.go")
	}
	if a.preview.line != 12 {
		t.Errorf("preview line = %d, want 12", a.preview.line)
	}
	if a.preview.kind != previewKindText {
		t.Errorf("preview kind = %q, want %q", a.preview.kind, previewKindText)
	}
	want := d.base() + "/v1/workbench/preview/tok1/main.go?view=1#L12"
	if a.preview.url != want {
		t.Errorf("preview url = %q, want %q", a.preview.url, want)
	}
	// The daemon got the pane's session and the active space's folder.
	body := d.request()
	for _, sub := range []string{`"session":"s1"`, `"link":"main.go:12"`, `"dir":"/tmp/work"`} {
		if !strings.Contains(body, sub) {
			t.Errorf("preview request %q does not hold %q", body, sub)
		}
	}
}

// TestOpenPreviewReplacesThePreview is REQ-DW-081: a new ⌘+click replaces the
// file the panel shows.
func TestOpenPreviewReplacesThePreview(t *testing.T) {
	a, d := previewApp(t)
	d.answer(http.StatusOK, `{"path":"/tmp/work/a.md","name":"a.md","dir":"/tmp/work","kind":"markdown","size":4,"url":"/v1/workbench/preview/t/a.md?view=1","raw_url":"/v1/workbench/preview/t/a.md"}`)
	openPreview(t, a, "s1", "/tmp/work/a.md", "/tmp/work")
	d.answer(http.StatusOK, `{"path":"/tmp/work/b.html","name":"b.html","dir":"/tmp/work","kind":"html","size":10,"url":"/v1/workbench/preview/t/b.html","raw_url":"/v1/workbench/preview/t/b.html"}`)
	openPreview(t, a, "s1", "/tmp/work/b.html", "/tmp/work")

	if a.preview == nil || a.preview.path != "/tmp/work/b.html" {
		t.Fatalf("the second preview did not replace the first: %+v", a.preview)
	}
	if a.preview.kind != previewKindHTML {
		t.Errorf("preview kind = %q, want %q", a.preview.kind, previewKindHTML)
	}
}

// TestOpenPreviewMissingFileNotices is REQ-DW-080/REQ-WP-040: a 404 from the
// daemon opens nothing and shows the daemon's message.
func TestOpenPreviewMissingFileNotices(t *testing.T) {
	a, d := previewApp(t)
	d.answer(http.StatusNotFound, `{"error":"no such file: /tmp/work/nope.go"}`)
	if !openPreview(t, a, "s1", "nope.go", "/tmp/work") {
		t.Fatal("OpenPreview did not handle the missing link")
	}
	if a.preview != nil {
		t.Error("a missing file opened a preview")
	}
	if !strings.Contains(a.notice, "no such file") {
		t.Errorf("the notice is %q, want it to name the missing file", a.notice)
	}
}

// TestOpenPreviewForbiddenNotices is REQ-WP-040: a 403 from the daemon opens
// nothing and shows the daemon's message.
func TestOpenPreviewForbiddenNotices(t *testing.T) {
	a, d := previewApp(t)
	d.answer(http.StatusForbidden, `{"error":"the path is not allowed"}`)
	if !openPreview(t, a, "s1", "~/.ssh/id_ed25519", "") {
		t.Fatal("OpenPreview did not handle the refused link")
	}
	if a.preview != nil {
		t.Error("a refused path opened a preview")
	}
	if !strings.Contains(a.notice, "not allowed") {
		t.Errorf("the notice is %q, want the daemon's message", a.notice)
	}
}

// TestOpenPreviewLeavesURLsToTheSystem is REQ-DW-080: an http(s) link is not a
// file, so OpenPreview leaves it to the system browser.
func TestOpenPreviewLeavesURLsToTheSystem(t *testing.T) {
	a := testApp(t)
	for _, link := range []string{"https://example.com", "http://example.com/x.go"} {
		if a.OpenPreview("s1", link, "/tmp") {
			t.Errorf("OpenPreview handled the URL %q", link)
		}
	}
	if a.preview != nil {
		t.Error("a URL opened a preview")
	}
}

// TestClosePreviewClearsIt is REQ-DW-081: the close function clears the
// preview and its focus.
func TestClosePreviewClearsIt(t *testing.T) {
	a, d := previewApp(t)
	d.answer(http.StatusOK, `{"path":"/tmp/work/a.go","name":"a.go","dir":"/tmp/work","kind":"text","size":2,"url":"/v1/workbench/preview/t/a.go?view=1","raw_url":"/v1/workbench/preview/t/a.go"}`)
	openPreview(t, a, "s1", "a.go", "/tmp/work")

	if a.preview == nil || !a.previewFocused {
		t.Fatal("the preview did not open focused")
	}
	a.ClosePreview()
	if a.preview != nil {
		t.Error("ClosePreview left the preview")
	}
	if a.previewFocused {
		t.Error("ClosePreview left the panel focused")
	}
}

// TestPreviewPanelShowsABinaryMessage is REQ-DW-083: a file the panel cannot
// show gets a native message with its name and size instead of a web view.
func TestPreviewPanelShowsABinaryMessage(t *testing.T) {
	a, d := previewApp(t)
	d.answer(http.StatusOK, `{"path":"/tmp/work/blob.bin","name":"blob.bin","dir":"/tmp/work","kind":"binary","size":4,"url":"/v1/workbench/preview/t/blob.bin","raw_url":"/v1/workbench/preview/t/blob.bin"}`)
	openPreview(t, a, "s1", "blob.bin", "/tmp/work")

	if a.preview == nil || a.preview.kind != previewKindBinary {
		t.Fatalf("the binary file did not open as binary: %+v", a.preview)
	}
	tt := ui.NewTester(func(c *ui.Context) { a.workspace(c, &light) }, 1200, 700)
	if !tt.HasText("4 B") {
		t.Errorf("the binary message does not show the size; it shows %q", tt.Texts())
	}
}

// TestPreviewTokenGETWorks is REQ-WP-004: the token URL the app shows serves
// the file with no cookie.
func TestPreviewTokenGETWorks(t *testing.T) {
	a, d := previewApp(t)
	d.answer(http.StatusOK, `{"path":"/tmp/work/a.txt","name":"a.txt","dir":"/tmp/work","kind":"text","size":9,"url":"/v1/workbench/preview/tok/a.txt?view=1","raw_url":"/v1/workbench/preview/tok/a.txt"}`)
	openPreview(t, a, "s1", "a.txt", "/tmp/work")
	if a.preview == nil {
		t.Fatal("the preview did not open")
	}

	resp, err := http.Get(d.base() + "/v1/workbench/preview/tok/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the token GET got %d, want 200", resp.StatusCode)
	}
}

// TestPreviewActionsAreLocalOnly is REQ-WP-040: Open in default app and Reveal
// in Finder show for a local daemon and are hidden for a remote one, whose
// files are on another machine.
func TestPreviewActionsAreLocalOnly(t *testing.T) {
	local := testApp(t)
	local.preview = &previewState{path: "/tmp/x.md", name: "x.md", dir: "/tmp", kind: previewKindMarkdown, size: 10}
	tt := ui.NewTester(func(c *ui.Context) { local.workspace(c, &light) }, 1200, 700)
	if !tt.HasText("Open in default app") {
		t.Error("a local daemon does not offer Open in default app")
	}
	if !tt.HasText("Reveal in Finder") {
		t.Error("a local daemon does not offer Reveal in Finder")
	}

	remote := testApp(t)
	remote.conn = &fakeConn{base: "https://box.example:8443"}
	remote.preview = &previewState{path: "/tmp/x.md", name: "x.md", dir: "/tmp", kind: previewKindMarkdown, size: 10}
	ttRemote := ui.NewTester(func(c *ui.Context) { remote.workspace(c, &light) }, 1200, 700)
	if ttRemote.HasText("Open in default app") {
		t.Error("a remote daemon still offers Open in default app")
	}
	if ttRemote.HasText("Reveal in Finder") {
		t.Error("a remote daemon still offers Reveal in Finder")
	}
}

// TestPreviewActionOnRemoteNotices is REQ-WP-040: an Open or Reveal invoked
// against a remote daemon is a no-op with a notice.
func TestPreviewActionOnRemoteNotices(t *testing.T) {
	a := testApp(t)
	a.conn = &fakeConn{base: "https://box.example:8443"}
	a.openPreviewInApp("/tmp/x.md")
	if !strings.Contains(a.notice, "remote") {
		t.Errorf("the notice is %q, want it to name the remote daemon", a.notice)
	}
}
