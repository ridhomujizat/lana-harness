package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

// fakeDaemonStore is an in-memory web.DaemonStore for the handler tests.
type fakeDaemonStore struct {
	info       DaemonInfo
	restartErr error
	restarts   int
}

func (f *fakeDaemonStore) DaemonInfo() DaemonInfo { return f.info }

func (f *fakeDaemonStore) Restart(context.Context) error {
	f.restarts++
	return f.restartErr
}

func newDaemonTestServer(t *testing.T, store DaemonStore) (*Server, *http.Cookie, string) {
	t.Helper()
	s, token := newAuthServerWithToken(t)
	s.SetDaemonStore(store)
	cookie, csrf := loginAndSession(t, s, token)
	return s, cookie, csrf
}

func daemonRequest(method, target, body string, cookie *http.Cookie, csrf string) *http.Request {
	req := authRequest(method, target, body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	return req
}

// TestDaemonStatus covers API-WEB-022: the daemon card reads the process state
// and the running turns of the restart confirmation.
func TestDaemonStatus(t *testing.T) {
	started := time.Now().Add(-5 * time.Second)
	store := &fakeDaemonStore{info: DaemonInfo{
		PID:              4242,
		StartedAt:        started.UnixMilli(),
		Home:             "/home/owner/lana",
		Version:          "test",
		GoVersion:        "go1.25",
		OS:               "darwin",
		Arch:             "arm64",
		RestartSupported: true,
		RunningTurns: []DaemonTurn{
			{SessionID: "s1", Title: "First", Agent: "helper"},
		},
	}}
	s, cookie, _ := newDaemonTestServer(t, store)

	rr := serveAuth(s, daemonRequest(http.MethodGet, "/v1/daemon", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got DaemonInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if got.PID != 4242 || got.StartedAt != started.UnixMilli() {
		t.Fatalf("pid/started_at = %d/%d", got.PID, got.StartedAt)
	}
	if got.Addr != authTestAddr {
		t.Fatalf("addr = %q, want %q", got.Addr, authTestAddr)
	}
	if got.UptimeMS < 4000 {
		t.Fatalf("uptime_ms = %d, want at least 4000", got.UptimeMS)
	}
	if got.BrowsersSignedIn != 1 {
		t.Fatalf("browsers_signed_in = %d, want 1", got.BrowsersSignedIn)
	}
	if !got.RestartSupported {
		t.Fatal("restart_supported = false")
	}
	if len(got.RunningTurns) != 1 || got.RunningTurns[0].SessionID != "s1" ||
		got.RunningTurns[0].Title != "First" || got.RunningTurns[0].Agent != "helper" {
		t.Fatalf("running_turns = %+v", got.RunningTurns)
	}
	if got.Home != "/home/owner/lana" || got.Version != "test" || got.GoVersion != "go1.25" ||
		got.OS != "darwin" || got.Arch != "arm64" {
		t.Fatalf("daemon info = %+v", got)
	}
}

// TestDaemonRestartRequiresConfirm covers the invalid_request rule of
// API-WEB-023.
func TestDaemonRestartRequiresConfirm(t *testing.T) {
	store := &fakeDaemonStore{info: DaemonInfo{PID: 1, RestartSupported: true}}
	s, cookie, csrf := newDaemonTestServer(t, store)

	for _, body := range []string{`{}`, `{"confirm":false}`, `{"confirm":null}`} {
		rr := serveAuth(s, daemonRequest(http.MethodPost, "/v1/daemon/restart", body, cookie, csrf))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	}
	if store.restarts != 0 {
		t.Fatalf("restart called %d times without confirm", store.restarts)
	}
}

// TestDaemonRestartScheduled covers the 202 response of API-WEB-023.
func TestDaemonRestartScheduled(t *testing.T) {
	store := &fakeDaemonStore{info: DaemonInfo{
		PID:              1,
		RestartSupported: true,
		RunningTurns:     []DaemonTurn{{SessionID: "s1", Title: "First", Agent: "helper"}},
	}}
	s, cookie, csrf := newDaemonTestServer(t, store)

	rr := serveAuth(s, daemonRequest(http.MethodPost, "/v1/daemon/restart", `{"confirm":true}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q, want 202", rr.Code, rr.Body.String())
	}
	var body struct {
		Restart      string `json:"restart"`
		RunningTurns int    `json:"running_turns"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if body.Restart != "scheduled" || body.RunningTurns != 1 {
		t.Fatalf("body = %+v", body)
	}
	if store.restarts != 1 {
		t.Fatalf("restart called %d times, want 1", store.restarts)
	}
}

// TestDaemonRestartPreflightFailure covers restart_failed: a failed preflight
// leaves the daemon running.
func TestDaemonRestartPreflightFailure(t *testing.T) {
	store := &fakeDaemonStore{
		info:       DaemonInfo{PID: 1, RestartSupported: true},
		restartErr: errors.New("daemon lock descriptor is lost"),
	}
	s, cookie, csrf := newDaemonTestServer(t, store)

	rr := serveAuth(s, daemonRequest(http.MethodPost, "/v1/daemon/restart", `{"confirm":true}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusInternalServerError, "restart_failed")
	if store.restarts != 1 {
		t.Fatalf("restart called %d times, want 1", store.restarts)
	}

	// The daemon keeps running: the status endpoint still answers.
	status := serveAuth(s, daemonRequest(http.MethodGet, "/v1/daemon", "", cookie, ""))
	if status.Code != http.StatusOK {
		t.Fatalf("status after failed restart = %d, body = %q", status.Code, status.Body.String())
	}
}

// TestDaemonRestartUnsupported covers the Windows path: restart_supported is
// false and a confirmed restart is 409 invalid_state.
func TestDaemonRestartUnsupported(t *testing.T) {
	store := &fakeDaemonStore{info: DaemonInfo{PID: 1, RestartSupported: false}}
	s, cookie, csrf := newDaemonTestServer(t, store)

	status := serveAuth(s, daemonRequest(http.MethodGet, "/v1/daemon", "", cookie, ""))
	if status.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", status.Code, status.Body.String())
	}
	var got DaemonInfo
	if err := json.Unmarshal(status.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.RestartSupported {
		t.Fatal("restart_supported = true on the unsupported path")
	}

	rr := serveAuth(s, daemonRequest(http.MethodPost, "/v1/daemon/restart", `{"confirm":true}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "invalid_state")
	if store.restarts != 0 {
		t.Fatalf("restart called %d times on the unsupported path", store.restarts)
	}
}

// TestDaemonRoutesNeedLogin covers the owner-cookie authorization of both
// operations.
func TestDaemonRoutesNeedLogin(t *testing.T) {
	store := &fakeDaemonStore{info: DaemonInfo{PID: 1, RestartSupported: true}}
	s, _, _ := newDaemonTestServer(t, store)

	rr := serveAuth(s, daemonRequest(http.MethodGet, "/v1/daemon", "", nil, ""))
	assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
	rr = serveAuth(s, daemonRequest(http.MethodPost, "/v1/daemon/restart", `{"confirm":true}`, nil, ""))
	assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
}
