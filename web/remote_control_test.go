package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/workbench"
)

// remoteControlRequest is a remote API request carrying an allowed Tailscale
// login, the remote https Origin, and an optional cookie and CSRF header.
func remoteControlRequest(method, target, body string, cookie *http.Cookie, csrf string) *http.Request {
	req := remoteHostRequest(method, target, body, remoteTestLogin)
	req.Header.Set("Origin", "https://"+remoteTestHost)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	return req
}

// remoteTokenLogin signs in over Tailscale with the legacy token and returns
// the session cookie and its CSRF value.
func remoteTokenLogin(t *testing.T, s *Server, token string) (*http.Cookie, string) {
	t.Helper()
	rr := serveAuth(s, remoteHostRequest(http.MethodPost, "/v1/auth/login", tokenLoginBody(t, token), remoteTestLogin))
	csrf := loginCSRF(t, rr)
	return authCookie(t, rr), csrf
}

// remotePasswordLogin signs in over Tailscale with the Lana password and
// returns the session cookie and its CSRF value.
func remotePasswordLogin(t *testing.T, s *Server, password string) (*http.Cookie, string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"password": password})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, remoteHostRequest(http.MethodPost, "/v1/auth/login", string(body), remoteTestLogin))
	csrf := loginCSRF(t, rr)
	return authCookie(t, rr), csrf
}

// remoteControlSession mints a remote browser session without the login flow,
// with control locked unless until is non-zero.
func remoteControlSession(t *testing.T, s *Server, until time.Time) *http.Cookie {
	t.Helper()
	value, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.sessions.addRemote(value, "test", remoteIdentity{
		Login:  remoteTestLogin,
		Device: remoteTestDevice,
		IP:     remoteTestIP,
	}, s.nowTime())
	if err != nil {
		t.Fatal(err)
	}
	if !until.IsZero() {
		if err := s.sessions.setControlUntil(sess.key, until); err != nil {
			t.Fatal(err)
		}
	}
	return &http.Cookie{Name: sessionCookieName, Value: value}
}

// dialRemoteAttach opens the Workbench attach of a remote browser: it carries
// the Serve headers, the remote https Origin, and the session cookie.
func dialRemoteAttach(t *testing.T, addr, id, mode string, cookie *http.Cookie) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	header := http.Header{}
	header.Set("Cookie", cookie.Name+"="+cookie.Value)
	header.Set(remoteForwardedForHeader, remoteTestIP)
	header.Set(remoteForwardedHostHeader, remoteTestHost)
	header.Set(remoteUserLoginHeader, remoteTestLogin)
	header.Set("Origin", "https://"+remoteTestHost)
	url := "ws://" + addr + "/v1/workbench/sessions/" + id + "/attach?mode=" + mode
	return websocket.Dial(context.Background(), url, &websocket.DialOptions{HTTPHeader: header})
}

// postRemoteControlLock sends POST /v1/auth/control/lock as the remote session
// and asserts 200.
func postRemoteControlLock(t *testing.T, addr string, cookie *http.Cookie) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/auth/control/lock", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = remoteTestHost
	req.Header.Set(remoteForwardedForHeader, remoteTestIP)
	req.Header.Set(remoteUserLoginHeader, remoteTestLogin)
	req.Header.Set("Origin", "https://"+remoteTestHost)
	req.Header.Set(csrfHeaderName, csrfValue(cookie.Value))
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("control lock status = %d, want 200", resp.StatusCode)
	}
}

// controlCodeFromMessage returns the 6-digit code of the REQ-RA-033 owner
// message.
func controlCodeFromMessage(t *testing.T, message string) string {
	t.Helper()
	const prefix = "Lana control code: "
	i := strings.Index(message, prefix)
	if i < 0 {
		t.Fatalf("message %q has no code", message)
	}
	code := message[i+len(prefix):]
	if j := strings.IndexByte(code, '\n'); j >= 0 {
		code = code[:j]
	}
	if len(code) != 6 {
		t.Fatalf("code = %q, want 6 digits", code)
	}
	return code
}

// TestRemoteControlLocalUnaffected covers REQ-RA-030: a local browser keeps
// full Workbench control without an unlock.
func TestRemoteControlLocalUnaffected(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc"}}
	s.SetWorkbench(fake)

	if rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions", "{}", cookie, csrf); rr.Code != http.StatusOK {
		t.Fatalf("local create status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if rr := workbenchRequest(s, http.MethodDelete, "/v1/workbench/sessions/abc", "", cookie, csrf); rr.Code != http.StatusNoContent {
		t.Fatalf("local kill status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/restart", "", cookie, csrf); rr.Code != http.StatusOK {
		t.Fatalf("local restart status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/resize", `{"cols":80,"rows":24}`, cookie, csrf); rr.Code != http.StatusNoContent {
		t.Fatalf("local resize status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestRemoteControlLockedRefusals covers REQ-RA-031: a remote confirm browser
// with locked control cannot create, restart, kill, or resize a session.
func TestRemoteControlLockedRefusals(t *testing.T) {
	s, token, _ := newRemoteTestServer(t, remoteEnabledConfig())
	cookie, csrf := remoteTokenLogin(t, s, token)
	s.SetWorkbench(newFakeWorkbench())

	cases := []struct {
		method string
		target string
		body   string
	}{
		{http.MethodPost, "/v1/workbench/sessions", "{}"},
		{http.MethodPost, "/v1/workbench/sessions/abc/restart", ""},
		{http.MethodDelete, "/v1/workbench/sessions/abc", ""},
		{http.MethodPost, "/v1/workbench/sessions/abc/resize", `{"cols":80,"rows":24}`},
	}
	for _, tt := range cases {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			rr := serveAuth(s, remoteControlRequest(tt.method, tt.target, tt.body, cookie, csrf))
			assertAuthError(t, rr, http.StatusForbidden, "control_locked")
			if !strings.Contains(rr.Body.String(), controlLockedMessage) {
				t.Fatalf("body = %q, want %q", rr.Body.String(), controlLockedMessage)
			}
		})
	}
}

// TestRemoteControlAttachGuarded covers REQ-RA-031: while control is locked the
// control attach is 403 control_locked and the view attach stays allowed.
func TestRemoteControlAttachGuarded(t *testing.T) {
	s, _, _ := newRemoteTestServer(t, remoteEnabledConfig())
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc"}}
	s.SetWorkbench(fake)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	cookie := remoteControlSession(t, s, time.Time{})

	conn, resp, err := dialRemoteAttach(t, addr, "abc", workbenchModeControl, cookie)
	if err == nil {
		_ = conn.CloseNow()
		t.Fatal("control attach succeeded with locked control")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("control attach status = %v, want 403", resp)
	}
	_ = resp.Body.Close()

	view, _, err := dialRemoteAttach(t, addr, "abc", workbenchModeView, cookie)
	if err != nil {
		t.Fatalf("view attach failed: %v", err)
	}
	_ = view.Close(websocket.StatusNormalClosure, "")
}

// TestRemoteControlUnlockPassword covers REQ-RA-032: the password lifts the
// control lock for control_minutes, a wrong one is 401 invalid_password, and
// control lapses at the deadline.
func TestRemoteControlUnlockPassword(t *testing.T) {
	s, _, _ := newRemoteTestServer(t, remoteEnabledConfig())
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	setPasswordViaLink(t, s, testPassword)
	cookie, csrf := remotePasswordLogin(t, s, testPassword)
	s.SetWorkbench(newFakeWorkbench())

	unlockBody := func(password string) *http.Request {
		return remoteControlRequest(http.MethodPost, "/v1/auth/control/unlock", `{"password":"`+password+`"}`, cookie, csrf)
	}
	assertAuthError(t, serveAuth(s, unlockBody("nope")), http.StatusUnauthorized, "invalid_password")

	rr := serveAuth(s, unlockBody(testPassword))
	if rr.Code != http.StatusOK {
		t.Fatalf("unlock status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body struct {
		ControlUntil int64 `json:"control_until"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	grant := time.Duration(remoteEnabledConfig().ControlMinutes) * time.Minute
	if want := now.Add(grant).UnixMilli(); body.ControlUntil != want {
		t.Fatalf("control_until = %d, want %d", body.ControlUntil, want)
	}

	create := remoteControlRequest(http.MethodPost, "/v1/workbench/sessions", "{}", cookie, csrf)
	if rr := serveAuth(s, create); rr.Code != http.StatusOK {
		t.Fatalf("create after unlock status = %d, body = %q", rr.Code, rr.Body.String())
	}
	now = now.Add(grant + time.Second)
	assertAuthError(t, serveAuth(s, remoteControlRequest(http.MethodPost, "/v1/workbench/sessions", "{}", cookie, csrf)), http.StatusForbidden, "control_locked")
}

// TestRemoteControlUnlockCountsTowardLockout covers REQ-RA-032: five wrong
// unlock passwords lock control and login for five minutes.
func TestRemoteControlUnlockCountsTowardLockout(t *testing.T) {
	s, _, _ := newRemoteTestServer(t, remoteEnabledConfig())
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	setPasswordViaLink(t, s, testPassword)
	cookie, csrf := remotePasswordLogin(t, s, testPassword)

	for i := 0; i < loginMaxFails; i++ {
		rr := serveAuth(s, remoteControlRequest(http.MethodPost, "/v1/auth/control/unlock", `{"password":"wrong"}`, cookie, csrf))
		assertAuthError(t, rr, http.StatusUnauthorized, "invalid_password")
	}
	right := remoteControlRequest(http.MethodPost, "/v1/auth/control/unlock", `{"password":"`+testPassword+`"}`, cookie, csrf)
	assertAuthError(t, serveAuth(s, right), http.StatusTooManyRequests, "too_many_attempts")

	now = now.Add(loginLockDuration + time.Second)
	if rr := serveAuth(s, remoteControlRequest(http.MethodPost, "/v1/auth/control/unlock", `{"password":"`+testPassword+`"}`, cookie, csrf)); rr.Code != http.StatusOK {
		t.Fatalf("unlock after lockout status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestRemoteControlTelegramCode covers REQ-RA-032 and REQ-RA-033: a Telegram
// code unlocks control once, the re-send is rate limited, and a wrong code is
// rejected.
func TestRemoteControlTelegramCode(t *testing.T) {
	s, token, _ := newRemoteTestServer(t, remoteEnabledConfig())
	cookie, csrf := remoteTokenLogin(t, s, token)
	// Install the messenger after the sign-in so the sign-in notification of
	// REQ-RA-070 does not mix with the control code.
	messenger := &fakeOwnerMessenger{}
	s.SetTelegramOwnerMessenger(messenger)
	s.SetTelegramResetSender(&fakeTelegramReset{available: true})

	rr := serveAuth(s, remoteControlRequest(http.MethodPost, "/v1/auth/control/telegram", "", cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("telegram status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var sent struct {
		SentTo int `json:"sent_to"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.SentTo != 1 || len(messenger.sent) != 1 {
		t.Fatalf("sent_to = %d, messages = %d, want 1, 1", sent.SentTo, len(messenger.sent))
	}
	code := controlCodeFromMessage(t, messenger.sent[0])

	// A second send within a minute is rate limited (REQ-RA-033).
	assertAuthError(t, serveAuth(s, remoteControlRequest(http.MethodPost, "/v1/auth/control/telegram", "", cookie, csrf)), http.StatusTooManyRequests, "too_many_attempts")

	wrong := "000000"
	if wrong == code {
		wrong = "000001"
	}
	assertAuthError(t, serveAuth(s, remoteControlRequest(http.MethodPost, "/v1/auth/control/unlock", `{"code":"`+wrong+`"}`, cookie, csrf)), http.StatusUnauthorized, "invalid_code")

	right := remoteControlRequest(http.MethodPost, "/v1/auth/control/unlock", `{"code":"`+code+`"}`, cookie, csrf)
	if rr := serveAuth(s, right); rr.Code != http.StatusOK {
		t.Fatalf("unlock by code status = %d, body = %q", rr.Code, rr.Body.String())
	}
	// The code is single use.
	assertAuthError(t, serveAuth(s, remoteControlRequest(http.MethodPost, "/v1/auth/control/unlock", `{"code":"`+code+`"}`, cookie, csrf)), http.StatusUnauthorized, "invalid_code")
}

// TestRemoteControlViewNotNeeded covers REQ-RA-030 and REQ-RA-032: terminal =
// view never allows control and the unlock routes answer 409 not_needed.
func TestRemoteControlViewNotNeeded(t *testing.T) {
	cfg := remoteEnabledConfig()
	cfg.Terminal = core.WebRemoteTerminalView
	s, token, _ := newRemoteTestServer(t, cfg)
	cookie, csrf := remoteTokenLogin(t, s, token)
	s.SetWorkbench(newFakeWorkbench())

	assertAuthError(t, serveAuth(s, remoteControlRequest(http.MethodPost, "/v1/auth/control/unlock", `{"password":"x"}`, cookie, csrf)), http.StatusConflict, "not_needed")
	assertAuthError(t, serveAuth(s, remoteControlRequest(http.MethodPost, "/v1/auth/control/telegram", "", cookie, csrf)), http.StatusConflict, "not_needed")
	assertAuthError(t, serveAuth(s, remoteControlRequest(http.MethodPost, "/v1/workbench/sessions", "{}", cookie, csrf)), http.StatusForbidden, "control_locked")
}

// TestRemoteControlFullAllows covers REQ-RA-030: terminal = full allows control
// without an unlock.
func TestRemoteControlFullAllows(t *testing.T) {
	cfg := remoteEnabledConfig()
	cfg.Terminal = core.WebRemoteTerminalFull
	s, token, _ := newRemoteTestServer(t, cfg)
	cookie, csrf := remoteTokenLogin(t, s, token)
	s.SetWorkbench(newFakeWorkbench())

	if rr := serveAuth(s, remoteControlRequest(http.MethodPost, "/v1/workbench/sessions", "{}", cookie, csrf)); rr.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// readCloseStatus reads the next message of conn and reports the WebSocket
// close status, so a test can assert the 4403 of REQ-RA-035.
func readCloseStatus(t *testing.T, conn *websocket.Conn) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	return websocket.CloseStatus(err)
}

// TestRemoteControlLockClosesAttachment covers REQ-RA-034 and REQ-RA-035: a
// lock closes a live control attachment with 4403 "control locked".
func TestRemoteControlLockClosesAttachment(t *testing.T) {
	s, _, _ := newRemoteTestServer(t, remoteEnabledConfig())
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc"}}
	s.SetWorkbench(fake)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	cookie := remoteControlSession(t, s, s.nowTime().Add(15*time.Minute))

	conn, _, err := dialRemoteAttach(t, addr, "abc", workbenchModeControl, cookie)
	if err != nil {
		t.Fatalf("control attach failed: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	status := make(chan websocket.StatusCode, 1)
	go func() { status <- readCloseStatus(t, conn) }()

	postRemoteControlLock(t, addr, cookie)

	select {
	case got := <-status:
		if got != workbenchCloseControlLocked {
			t.Fatalf("close status = %v, want 4403", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("control attachment did not close after a lock")
	}
}

// TestRemoteControlAttachExpires covers REQ-RA-035: the watcher closes a control
// attachment once its session's control expires.
func TestRemoteControlAttachExpires(t *testing.T) {
	controlWatchNanos.Store(int64(10 * time.Millisecond))
	t.Cleanup(func() { controlWatchNanos.Store(0) })

	s, _, _ := newRemoteTestServer(t, remoteEnabledConfig())
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc"}}
	s.SetWorkbench(fake)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	// Control holds for a moment, then lapses without a lock, so the poll of
	// the watcher is what closes the attachment.
	cookie := remoteControlSession(t, s, s.nowTime().Add(200*time.Millisecond))

	conn, _, err := dialRemoteAttach(t, addr, "abc", workbenchModeControl, cookie)
	if err != nil {
		t.Fatalf("control attach failed: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	status := make(chan websocket.StatusCode, 1)
	go func() { status <- readCloseStatus(t, conn) }()

	select {
	case got := <-status:
		if got != workbenchCloseControlLocked {
			t.Fatalf("close status = %v, want 4403", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("control attachment did not close after expiry")
	}
}
