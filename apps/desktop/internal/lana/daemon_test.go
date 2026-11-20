package lana

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
)

// sessionCookieName is the daemon's session cookie (web/auth.go).
const sessionCookieName = "lana_session"

// fakeDaemon imitates the parts of the Lana daemon the client uses: the local
// app link, its sign-in cookie and redirect, the session CSRF value, and the
// workbench routes.
type fakeDaemon struct {
	server *httptest.Server
	secret string

	// events, attach and preview handle those routes; a test replaces them.
	events  http.HandlerFunc
	attach  http.HandlerFunc
	preview http.HandlerFunc

	mu          sync.Mutex
	appLinks    int
	pending     string
	pendingCSRF string
	cookie      string
	csrf        string
	origins     []string
	csrfHeaders []string
	layout      []byte
	createBody  []byte
	resizeBody  []byte
	previewBody []byte
	errStatus   int
	errCode     string
	errMessage  string
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{secret: "local-secret"}
	d.events = func(w http.ResponseWriter, _ *http.Request) {
		d.writeError(w, http.StatusNotFound, "not_found", "no events route")
	}
	d.attach = func(w http.ResponseWriter, _ *http.Request) {
		d.writeError(w, http.StatusNotFound, "not_found", "no attach route")
	}
	d.preview = func(w http.ResponseWriter, _ *http.Request) {
		d.writeError(w, http.StatusNotFound, "not_found", "no preview route")
	}
	d.server = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.server.Close)
	return d
}

// addr is the daemon address as $LANA_HOME/daemon.secret holds it.
func (d *fakeDaemon) addr() string { return strings.TrimPrefix(d.server.URL, "http://") }

// base is the daemon origin, the value of the Origin header.
func (d *fakeDaemon) base() string { return "http://" + d.addr() }

func (d *fakeDaemon) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case appLinkPath:
		d.handleAppLink(w, r)
	case "/v1/auth/app":
		d.handleAppSignIn(w, r)
	case authSessionPath:
		d.handleSession(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, "/v1/workbench/") {
			d.handleWorkbench(w, r)
			return
		}
		d.writeError(w, http.StatusNotFound, "not_found", "no such route")
	}
}

// handleAppLink answers a one-time app link after checking the local secret.
func (d *fakeDaemon) handleAppLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		d.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}
	if r.Header.Get(headerLocalSecret) != d.secret {
		d.writeError(w, http.StatusUnauthorized, "unauthorized", "local control secret is missing or invalid")
		return
	}
	d.mu.Lock()
	d.appLinks++
	code := d.appLinks
	d.pending = fmt.Sprintf("session-%d", code)
	d.pendingCSRF = fmt.Sprintf("csrf-%d", code)
	d.mu.Unlock()
	link := fmt.Sprintf("%s/v1/auth/app?code=code-%d&next=%%23/chats", d.base(), code)
	writeJSON(w, http.StatusOK, map[string]string{"link": link})
}

// handleAppSignIn sets the session cookie and redirects, as the daemon does.
func (d *fakeDaemon) handleAppSignIn(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	cookie, csrf := d.pending, d.pendingCSRF
	if cookie != "" {
		d.cookie, d.csrf = cookie, csrf
	}
	d.mu.Unlock()
	if cookie == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: cookie, Path: "/", HttpOnly: true})
	http.Redirect(w, r, "/#/chats", http.StatusSeeOther)
}

// handleSession answers the CSRF value of the cookie's session.
func (d *fakeDaemon) handleSession(w http.ResponseWriter, r *http.Request) {
	if !d.authorized(r) {
		d.writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	d.mu.Lock()
	csrf := d.csrf
	d.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"csrf":       csrf,
		"expires_at": time.Now().Add(time.Hour).UnixMilli(),
	})
}

// handleWorkbench applies the daemon's Origin and CSRF checks, records what the
// client sent, and dispatches to the route handler.
func (d *fakeDaemon) handleWorkbench(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	d.origins = append(d.origins, r.Header.Get(headerOrigin))
	d.csrfHeaders = append(d.csrfHeaders, r.Header.Get(headerCSRF))
	status, code, message := d.errStatus, d.errCode, d.errMessage
	d.mu.Unlock()

	if status != 0 {
		d.writeError(w, status, code, message)
		return
	}
	if !d.authorized(r) {
		d.writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if r.Header.Get(headerOrigin) != d.base() {
		d.writeError(w, http.StatusForbidden, "forbidden_origin", "origin is not allowed")
		return
	}
	if isUnsafe(r.Method) && r.Header.Get(headerCSRF) != d.csrfValue() {
		d.writeError(w, http.StatusForbidden, "csrf_invalid", "CSRF token is missing or invalid")
		return
	}
	switch {
	case r.URL.Path == EventsPath:
		d.events(w, r)
	case strings.HasSuffix(r.URL.Path, "/attach"):
		d.attach(w, r)
	case r.URL.Path == LayoutPath:
		d.handleLayout(w, r)
	case r.URL.Path == PreviewPath:
		d.preview(w, r)
	default:
		d.handleSessions(w, r)
	}
}

func (d *fakeDaemon) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == SessionsPath:
		writeJSON(w, http.StatusOK, map[string]any{"sessions": []Session{}})
	case r.Method == http.MethodPost && r.URL.Path == SessionsPath:
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.createBody = body
		d.mu.Unlock()
		writeJSON(w, http.StatusOK, Session{ID: "sess-1", Title: "shell", State: "running", Cols: 80, Rows: 24})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/resize"):
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.resizeBody = body
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/restart"):
		writeJSON(w, http.StatusOK, Session{ID: "sess-1", State: "running"})
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		d.writeError(w, http.StatusNotFound, "not_found", "no such route")
	}
}

func (d *fakeDaemon) handleLayout(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		d.mu.Lock()
		layout := d.layout
		d.mu.Unlock()
		if len(layout) == 0 {
			layout = []byte("{}")
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(layout)
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		if !json.Valid(body) {
			d.writeError(w, http.StatusBadRequest, "invalid_request", "the layout must be valid JSON")
			return
		}
		d.mu.Lock()
		d.layout = body
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		d.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "no")
	}
}

// authorized reports whether the request carries the current session cookie.
func (d *fakeDaemon) authorized(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cookie != "" && cookie.Value == d.cookie
}

// revoke invalidates the session, as an expiry or a password change would.
func (d *fakeDaemon) revoke() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cookie = ""
}

func (d *fakeDaemon) csrfValue() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.csrf
}

func (d *fakeDaemon) appLinkCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.appLinks
}

func (d *fakeDaemon) writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// connect writes the daemon secret for this server and signs in.
func (d *fakeDaemon) connect(t *testing.T) *Local {
	t.Helper()
	home := t.TempDir()
	writeSecret(t, home, d.addr(), d.secret)
	conn := NewLocal(home)
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return conn
}

func writeSecret(t *testing.T, home, addr, secret string) {
	t.Helper()
	body := fmt.Sprintf("{\"addr\":%q,\"secret\":%q}\n", addr, secret)
	if err := os.WriteFile(filepath.Join(home, SecretFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
