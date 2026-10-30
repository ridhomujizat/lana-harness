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
	info           DaemonInfo
	restartErr     error
	restartOutcome DaemonRestartOutcome
	cancelErr      error
	cancel         bool
	restarts       int
	forces         []bool
	cancels        int
}

func (f *fakeDaemonStore) DaemonInfo() DaemonInfo { return f.info }

func (f *fakeDaemonStore) Restart(_ context.Context, force bool) (DaemonRestartOutcome, error) {
	f.restarts++
	f.forces = append(f.forces, force)
	if f.restartErr != nil {
		return DaemonRestartOutcome{}, f.restartErr
	}
	outcome := f.restartOutcome
	if outcome.State == "" {
		outcome = DaemonRestartOutcome{State: DaemonRestartScheduled}
	}
	return outcome, nil
}

func (f *fakeDaemonStore) CancelRestart(context.Context) (bool, error) {
	f.cancels++
	return f.cancel, f.cancelErr
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
	if got.Restart.State != DaemonRestartIdle || got.Restart.Deadline != nil {
		t.Fatalf("restart = %+v, want idle/nil", got.Restart)
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

// TestDaemonStatusDraining covers the restart field of GET /v1/daemon while a
// restart drain waits (API-TFB-002).
func TestDaemonStatusDraining(t *testing.T) {
	deadline := time.Now().Add(time.Minute).UnixMilli()
	store := &fakeDaemonStore{info: DaemonInfo{
		PID:              1,
		RestartSupported: true,
		Restart:          DaemonRestartInfo{State: DaemonRestartDraining, Deadline: &deadline},
	}}
	s, cookie, _ := newDaemonTestServer(t, store)

	rr := serveAuth(s, daemonRequest(http.MethodGet, "/v1/daemon", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got DaemonInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Restart.State != DaemonRestartDraining || got.Restart.Deadline == nil || *got.Restart.Deadline != deadline {
		t.Fatalf("restart = %+v, want draining/%d", got.Restart, deadline)
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

// TestDaemonRestartScheduled covers the immediate 202 response of
// API-TFB-002: no running turns schedule the restart.
func TestDaemonRestartScheduled(t *testing.T) {
	store := &fakeDaemonStore{info: DaemonInfo{
		PID:              1,
		RestartSupported: true,
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
	if body.Restart != "scheduled" || body.RunningTurns != 0 {
		t.Fatalf("body = %+v", body)
	}
	if store.restarts != 1 || len(store.forces) != 1 || store.forces[0] {
		t.Fatalf("restart calls = %d, forces = %v", store.restarts, store.forces)
	}
}

// TestDaemonRestartDraining covers the waiting state of API-TFB-002: a
// repeated POST keeps the original deadline and DELETE cancels the drain.
func TestDaemonRestartDraining(t *testing.T) {
	deadline := time.Now().Add(time.Minute).UnixMilli()
	store := &fakeDaemonStore{
		info: DaemonInfo{
			PID:              1,
			RestartSupported: true,
			RunningTurns:     []DaemonTurn{{SessionID: "s1", Title: "First", Agent: "helper"}},
		},
		restartOutcome: DaemonRestartOutcome{State: DaemonRestartDraining, RunningTurns: 1, Deadline: deadline},
		cancel:         true,
	}
	s, cookie, csrf := newDaemonTestServer(t, store)

	type drainingBody struct {
		Restart      string `json:"restart"`
		RunningTurns int    `json:"running_turns"`
		Deadline     int64  `json:"deadline"`
	}
	for i := 0; i < 2; i++ {
		rr := serveAuth(s, daemonRequest(http.MethodPost, "/v1/daemon/restart", `{"confirm":true}`, cookie, csrf))
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status = %d, body = %q, want 202", rr.Code, rr.Body.String())
		}
		var body drainingBody
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("body %q: %v", rr.Body.String(), err)
		}
		if body.Restart != "draining" || body.RunningTurns != 1 || body.Deadline != deadline {
			t.Fatalf("body = %+v, want the original deadline %d", body, deadline)
		}
	}

	rr := serveAuth(s, daemonRequest(http.MethodDelete, "/v1/daemon/restart", "", cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var cancelled struct {
		Restart string `json:"restart"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &cancelled); err != nil || cancelled.Restart != "cancelled" {
		t.Fatalf("cancel body = %q, err = %v", rr.Body.String(), err)
	}
	if store.cancels != 1 {
		t.Fatalf("cancel called %d times, want 1", store.cancels)
	}
}

// TestDaemonRestartCancelInvalid covers DELETE with no pending restart.
func TestDaemonRestartCancelInvalid(t *testing.T) {
	store := &fakeDaemonStore{info: DaemonInfo{PID: 1, RestartSupported: true}}
	s, cookie, csrf := newDaemonTestServer(t, store)

	rr := serveAuth(s, daemonRequest(http.MethodDelete, "/v1/daemon/restart", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "invalid_state")
}

// TestDaemonRestartForce covers "Restart now": force schedules at once and the
// adapter interrupts the running turns.
func TestDaemonRestartForce(t *testing.T) {
	store := &fakeDaemonStore{
		info: DaemonInfo{
			PID:              1,
			RestartSupported: true,
			RunningTurns:     []DaemonTurn{{SessionID: "s1"}},
		},
		restartOutcome: DaemonRestartOutcome{State: DaemonRestartScheduled, RunningTurns: 1},
	}
	s, cookie, csrf := newDaemonTestServer(t, store)

	rr := serveAuth(s, daemonRequest(http.MethodPost, "/v1/daemon/restart", `{"confirm":true,"force":true}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
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
	if len(store.forces) != 1 || !store.forces[0] {
		t.Fatalf("forces = %v, want [true]", store.forces)
	}
}

// TestDaemonRestartDuringStopDrain covers the documented choice: a SIGTERM stop
// drain refuses the web restart with 409 invalid_state.
func TestDaemonRestartDuringStopDrain(t *testing.T) {
	store := &fakeDaemonStore{
		info:       DaemonInfo{PID: 1, RestartSupported: true},
		restartErr: ErrRestartInvalidState,
	}
	s, cookie, csrf := newDaemonTestServer(t, store)

	rr := serveAuth(s, daemonRequest(http.MethodPost, "/v1/daemon/restart", `{"confirm":true}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "invalid_state")
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
