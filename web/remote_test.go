package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/tailscale"
)

const (
	// remoteTestPeer and outsideTestPeer are the loopback and non-loopback TCP
	// peers the classification of REQ-RA-001 is checked with.
	remoteTestPeer  = "127.0.0.1:5555"
	outsideTestPeer = "192.168.1.5:5555"

	// remoteTestHost is the configured ts.net name and remoteTestLogin the
	// allowed Tailscale login.
	remoteTestHost  = "ridho-mbp.tail3f2a1.ts.net"
	remoteTestLogin = "ridho@github"
	// remoteTestIP is the X-Forwarded-For client IP and remoteTestDevice the
	// machine name the fake whois returns for it.
	remoteTestIP     = "100.64.0.1"
	remoteTestDevice = "iPhone 15"
)

// fakeTailscale is a web.tailscaleClient that never runs the real CLI
// (REQ-RA-054).
type fakeTailscale struct {
	peers map[string]tailscale.Peer
}

func newFakeTailscale() *fakeTailscale {
	return &fakeTailscale{peers: map[string]tailscale.Peer{
		remoteTestIP: {Device: remoteTestDevice, Login: remoteTestLogin},
	}}
}

func (f *fakeTailscale) Status(context.Context, string) (tailscale.Status, error) {
	return tailscale.Status{Installed: true, Running: true}, nil
}

func (f *fakeTailscale) Serve(context.Context, string) error { return nil }

func (f *fakeTailscale) Unserve(context.Context) error { return nil }

func (f *fakeTailscale) Whois(_ context.Context, ip string) (tailscale.Peer, error) {
	if peer, ok := f.peers[ip]; ok {
		return peer, nil
	}
	return tailscale.Peer{}, nil
}

// newRemoteTestServer returns a server whose credential store holds the legacy
// token, plus a fake Tailscale client.
func newRemoteTestServer(t *testing.T, cfg core.WebRemoteConfig) (*Server, string, *fakeTailscale) {
	t.Helper()
	home := t.TempDir()
	token := authTokenFor(t, home)
	s := newAuthServer(t, home, io.Discard)
	fake := newFakeTailscale()
	s.SetTailscaleClient(fake)
	s.SetRemoteConfig(cfg)
	return s, token, fake
}

// remoteEnabledConfig is an enabled remote configuration that allows
// remoteTestLogin.
func remoteEnabledConfig() core.WebRemoteConfig {
	cfg := core.DefaultWebRemoteConfig()
	cfg.Enabled = true
	cfg.Host = remoteTestHost
	cfg.AllowedLogins = []string{remoteTestLogin}
	return cfg
}

// localHostRequest is a request from a loopback peer to the bind address.
func localHostRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = remoteTestPeer
	req.Host = authTestAddr
	return req
}

// remoteHostRequest is a Serve-style request: a loopback peer, the ts.net host,
// and X-Forwarded-For. login is carried in Tailscale-User-Login when non-empty.
func remoteHostRequest(method, target, body, login string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = remoteTestPeer
	req.Host = remoteTestHost
	req.Header.Set(remoteForwardedForHeader, remoteTestIP)
	if login != "" {
		req.Header.Set(remoteUserLoginHeader, login)
	}
	return req
}

type remoteStateResponse struct {
	PasswordSet   bool   `json:"password_set"`
	TelegramReset bool   `json:"telegram_reset"`
	LockedUntil   *int64 `json:"locked_until"`
	Remote        bool   `json:"remote"`
	Login         string `json:"login"`
	Device        string `json:"device"`
}

func decodeRemoteState(t *testing.T, rr *httptest.ResponseRecorder) remoteStateResponse {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("state status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var state remoteStateResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &state); err != nil {
		t.Fatalf("state body %q: %v", rr.Body.String(), err)
	}
	return state
}

// tokenLoginBody is a legacy-token login body.
func tokenLoginBody(t *testing.T, token string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestRemoteLocalUnchanged covers REQ-RA-001: a loopback request to the bind
// address is local and keeps today's behavior even with remote access off.
func TestRemoteLocalUnchanged(t *testing.T) {
	s, _, _ := newRemoteTestServer(t, core.DefaultWebRemoteConfig())

	rr := serveAuth(s, localHostRequest(http.MethodGet, "/", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<title>lana</title>") {
		t.Fatalf("GET / body is not the index: %q", rr.Body.String())
	}

	state := decodeRemoteState(t, serveAuth(s, localHostRequest(http.MethodGet, "/v1/auth/state", "")))
	if state.Remote || state.Login != "" || state.Device != "" {
		t.Fatalf("local state = %+v, want remote false", state)
	}
}

// TestRemoteSpoofedHeadersFromNonLoopbackIgnored covers REQ-RA-001: the
// forwarded and Tailscale headers are read only from a loopback peer.
func TestRemoteSpoofedHeadersFromNonLoopbackIgnored(t *testing.T) {
	s, _, _ := newRemoteTestServer(t, core.DefaultWebRemoteConfig())

	req := localHostRequest(http.MethodGet, "/", "")
	req.RemoteAddr = outsideTestPeer
	req.Header.Set(remoteForwardedForHeader, remoteTestIP)
	req.Header.Set(remoteUserLoginHeader, remoteTestLogin)
	rr := serveAuth(s, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, body = %q", rr.Code, rr.Body.String())
	}

	stateReq := localHostRequest(http.MethodGet, "/v1/auth/state", "")
	stateReq.RemoteAddr = outsideTestPeer
	stateReq.Header.Set(remoteUserLoginHeader, remoteTestLogin)
	state := decodeRemoteState(t, serveAuth(s, stateReq))
	if state.Remote || state.Login != "" {
		t.Fatalf("spoofed peer state = %+v, want remote false", state)
	}
}

// TestRemoteGateRefusals covers REQ-RA-003.
func TestRemoteGateRefusals(t *testing.T) {
	disabled := core.DefaultWebRemoteConfig()

	wrongHost := remoteEnabledConfig()
	loginNotAllowed := remoteEnabledConfig()
	loginNotAllowed.AllowedLogins = []string{"someone@else"}

	cases := []struct {
		name  string
		cfg   core.WebRemoteConfig
		build func() *http.Request
		isAPI bool
	}{
		{
			name: "disabled page",
			cfg:  disabled,
			build: func() *http.Request {
				return remoteHostRequest(http.MethodGet, "/", "", remoteTestLogin)
			},
		},
		{
			name:  "disabled api",
			cfg:   disabled,
			isAPI: true,
			build: func() *http.Request {
				return remoteHostRequest(http.MethodGet, "/v1/auth/state", "", remoteTestLogin)
			},
		},
		{
			name: "wrong host",
			cfg:  wrongHost,
			build: func() *http.Request {
				req := remoteHostRequest(http.MethodGet, "/", "", remoteTestLogin)
				req.Host = "other.ts.net"
				return req
			},
		},
		{
			name: "login not allowed",
			cfg:  loginNotAllowed,
			build: func() *http.Request {
				return remoteHostRequest(http.MethodGet, "/", "", remoteTestLogin)
			},
		},
		{
			name: "login missing with require_identity",
			cfg:  remoteEnabledConfig(),
			build: func() *http.Request {
				return remoteHostRequest(http.MethodGet, "/", "", "")
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newRemoteTestServer(t, tt.cfg)
			rr := serveAuth(s, tt.build())
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body = %q", rr.Code, rr.Body.String())
			}
			if tt.isAPI {
				assertAuthError(t, rr, http.StatusForbidden, "remote_disabled")
				return
			}
			if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
				t.Fatalf("Content-Type = %q, want text/plain", got)
			}
			if !strings.Contains(rr.Body.String(), remoteDisabledMessage) {
				t.Fatalf("body = %q, want %q", rr.Body.String(), remoteDisabledMessage)
			}
		})
	}
}

// TestRemoteAllowedIndexAndState covers REQ-RA-004 and REQ-RA-027: an allowed
// remote request serves the index and the state carries the identity chip.
func TestRemoteAllowedIndexAndState(t *testing.T) {
	s, _, _ := newRemoteTestServer(t, remoteEnabledConfig())

	rr := serveAuth(s, remoteHostRequest(http.MethodGet, "/", "", remoteTestLogin))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<title>lana</title>") {
		t.Fatalf("GET / body is not the index: %q", rr.Body.String())
	}

	state := decodeRemoteState(t, serveAuth(s, remoteHostRequest(http.MethodGet, "/v1/auth/state", "", remoteTestLogin)))
	if !state.Remote || state.Login != remoteTestLogin || state.Device != remoteTestDevice {
		t.Fatalf("remote state = %+v, want remote true with the identity", state)
	}
}

// TestRemoteOrigin covers REQ-RA-004: a remote request accepts only
// https://<host>, a local request only http://<addr>.
func TestRemoteOrigin(t *testing.T) {
	s, token, _ := newRemoteTestServer(t, remoteEnabledConfig())
	body := tokenLoginBody(t, token)

	accepted := remoteHostRequest(http.MethodPost, "/v1/auth/login", body, remoteTestLogin)
	accepted.Header.Set("Origin", "https://"+remoteTestHost)
	if rr := serveAuth(s, accepted); rr.Code != http.StatusOK {
		t.Fatalf("remote origin https status = %d, body = %q", rr.Code, rr.Body.String())
	}

	rejected := remoteHostRequest(http.MethodPost, "/v1/auth/login", body, remoteTestLogin)
	rejected.Header.Set("Origin", "http://"+authTestAddr)
	assertAuthError(t, serveAuth(s, rejected), http.StatusForbidden, "forbidden_origin")

	local := localHostRequest(http.MethodPost, "/v1/auth/login", body)
	local.Header.Set("Origin", authTestOrigin)
	if rr := serveAuth(s, local); rr.Code != http.StatusOK {
		t.Fatalf("local origin http status = %d, body = %q", rr.Code, rr.Body.String())
	}

	localRejected := localHostRequest(http.MethodPost, "/v1/auth/login", body)
	localRejected.Header.Set("Origin", "https://"+remoteTestHost)
	assertAuthError(t, serveAuth(s, localRejected), http.StatusForbidden, "forbidden_origin")
}

// listSessions decodes the session rows of a response.
func listSessions(t *testing.T, rr *httptest.ResponseRecorder) []browserSessionInfo {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("sessions status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body struct {
		Sessions []browserSessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("sessions body %q: %v", rr.Body.String(), err)
	}
	return body.Sessions
}

// TestRemoteLoginCreatesRemoteSession covers REQ-RA-020, REQ-RA-023, and
// REQ-RA-024: a remote login records the identity, sets a Secure cookie, and
// lists the session as remote.
func TestRemoteLoginCreatesRemoteSession(t *testing.T) {
	s, token, _ := newRemoteTestServer(t, remoteEnabledConfig())

	rr := serveAuth(s, remoteHostRequest(http.MethodPost, "/v1/auth/login", tokenLoginBody(t, token), remoteTestLogin))
	cookie := authCookie(t, rr)
	if !cookie.Secure {
		t.Fatal("remote session cookie is not Secure")
	}

	req := remoteHostRequest(http.MethodGet, "/v1/auth/sessions", "", remoteTestLogin)
	req.AddCookie(cookie)
	rows := listSessions(t, serveAuth(s, req))
	if len(rows) != 1 {
		t.Fatalf("session rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if !row.Remote || row.Login != remoteTestLogin || row.Device != remoteTestDevice || row.IP != remoteTestIP {
		t.Fatalf("session row = %+v, want the remote identity", row)
	}
}

// TestRemoteSessionRules covers REQ-RA-022: a remote cookie used locally and a
// login mismatch are both 401 unauthenticated.
func TestRemoteSessionRules(t *testing.T) {
	cfg := remoteEnabledConfig()
	cfg.AllowedLogins = []string{remoteTestLogin, "other@github"}
	s, token, _ := newRemoteTestServer(t, cfg)

	rr := serveAuth(s, remoteHostRequest(http.MethodPost, "/v1/auth/login", tokenLoginBody(t, token), remoteTestLogin))
	cookie := authCookie(t, rr)

	local := localHostRequest(http.MethodGet, "/v1/auth/session", "")
	local.AddCookie(cookie)
	assertAuthError(t, serveAuth(s, local), http.StatusUnauthorized, "unauthenticated")

	mismatch := remoteHostRequest(http.MethodGet, "/v1/auth/session", "", "other@github")
	mismatch.AddCookie(cookie)
	assertAuthError(t, serveAuth(s, mismatch), http.StatusUnauthorized, "unauthenticated")

	// The matching remote request is accepted.
	match := remoteHostRequest(http.MethodGet, "/v1/auth/session", "", remoteTestLogin)
	match.AddCookie(cookie)
	if rr := serveAuth(s, match); rr.Code != http.StatusOK {
		t.Fatalf("matching remote session status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestRemoteForbiddenRoutes covers REQ-RA-040: a remote request cannot change
// the settings that gate remote access.
func TestRemoteForbiddenRoutes(t *testing.T) {
	s, token, _ := newRemoteTestServer(t, remoteEnabledConfig())
	rr := serveAuth(s, remoteHostRequest(http.MethodPost, "/v1/auth/login", tokenLoginBody(t, token), remoteTestLogin))
	cookie := authCookie(t, rr)
	csrf := loginCSRF(t, rr)

	cases := []struct {
		method string
		target string
		body   string
	}{
		{http.MethodPut, "/v1/web", "{}"},
		{http.MethodPost, "/v1/auth/password", "{}"},
		{http.MethodPost, "/v1/auth/sessions/revoke-others", ""},
	}
	for _, tt := range cases {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			req := remoteHostRequest(tt.method, tt.target, tt.body, remoteTestLogin)
			req.AddCookie(cookie)
			req.Header.Set(csrfHeaderName, csrf)
			assertAuthError(t, serveAuth(s, req), http.StatusForbidden, "remote_forbidden")
		})
	}
}

// TestRemoteRevokeSessions covers REQ-RA-025: revoke one session by its display
// id, and revoke every remote session.
func TestRemoteRevokeSessions(t *testing.T) {
	s, token, _ := newRemoteTestServer(t, remoteEnabledConfig())

	localCookie, localCSRF := loginAndSession(t, s, token)

	remoteOne := authCookie(t, serveAuth(s, remoteHostRequest(http.MethodPost, "/v1/auth/login", tokenLoginBody(t, token), remoteTestLogin)))
	remoteTwo := authCookie(t, serveAuth(s, remoteHostRequest(http.MethodPost, "/v1/auth/login", tokenLoginBody(t, token), remoteTestLogin)))

	// remoteOne and remoteTwo are created in the same millisecond, so the
	// session list orders them by their random display id. The row marked
	// current for remoteOne's own cookie identifies it without that order.
	remoteOneID := currentRemoteSessionID(t, s, remoteOne)

	// Revoke one remote session from the local session.
	revoke := localHostRequest(http.MethodPost, "/v1/auth/sessions/"+remoteOneID+"/revoke", "")
	revoke.AddCookie(localCookie)
	revoke.Header.Set(csrfHeaderName, localCSRF)
	if rr := serveAuth(s, revoke); rr.Code != http.StatusOK {
		t.Fatalf("revoke one status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if rr := serveAuth(s, remoteSessionRequest(remoteOne)); rr.Code != http.StatusUnauthorized {
		t.Fatalf("revoked remote session status = %d, want 401", rr.Code)
	}
	// Only remoteOne was revoked: remoteTwo still authenticates.
	if rr := serveAuth(s, remoteSessionRequest(remoteTwo)); rr.Code != http.StatusOK {
		t.Fatalf("other remote session status = %d, want 200", rr.Code)
	}

	// An unknown display id is 404.
	missing := localHostRequest(http.MethodPost, "/v1/auth/sessions/0123456789ab/revoke", "")
	missing.AddCookie(localCookie)
	missing.Header.Set(csrfHeaderName, localCSRF)
	assertAuthError(t, serveAuth(s, missing), http.StatusNotFound, "not_found")

	// Revoke every remote session.
	revokeRemote := localHostRequest(http.MethodPost, "/v1/auth/sessions/revoke-remote", "")
	revokeRemote.AddCookie(localCookie)
	revokeRemote.Header.Set(csrfHeaderName, localCSRF)
	rr := serveAuth(s, revokeRemote)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke remote status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body struct {
		Revoked int `json:"revoked"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("revoke remote body %q: %v", rr.Body.String(), err)
	}
	if body.Revoked != 1 {
		t.Fatalf("revoked = %d, want 1", body.Revoked)
	}
	if rr := serveAuth(s, remoteSessionRequest(remoteTwo)); rr.Code != http.StatusUnauthorized {
		t.Fatalf("second revoked remote session status = %d, want 401", rr.Code)
	}
}

// TestRemoteRevokeOwnSessionClearsCookie covers REQ-RA-025: revoking the
// caller's own session clears its cookie.
func TestRemoteRevokeOwnSessionClearsCookie(t *testing.T) {
	s, token, _ := newRemoteTestServer(t, remoteEnabledConfig())
	cookie, csrf := loginAndSession(t, s, token)

	rows, _ := getBrowserSessionList(t, s, cookie)
	if len(rows) != 1 {
		t.Fatalf("session rows = %d, want 1", len(rows))
	}

	revoke := localHostRequest(http.MethodPost, "/v1/auth/sessions/"+rows[0].ID+"/revoke", "")
	revoke.AddCookie(cookie)
	revoke.Header.Set(csrfHeaderName, csrf)
	rr := serveAuth(s, revoke)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke own status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if raw := rr.Header().Get("Set-Cookie"); !strings.Contains(raw, "lana_session=;") || !strings.Contains(raw, "Max-Age=0") {
		t.Fatalf("Set-Cookie = %q, want a cleared session cookie", raw)
	}
}

// remoteSessionRequest is a remote GET /v1/auth/session with a cookie.
func remoteSessionRequest(cookie *http.Cookie) *http.Request {
	req := remoteHostRequest(http.MethodGet, "/v1/auth/session", "", remoteTestLogin)
	req.AddCookie(cookie)
	return req
}

// currentRemoteSessionID returns the display id the sessions list marks current
// for a remote cookie. Listing through the cookie's own remote request avoids
// depending on the order of sessions created in the same millisecond.
func currentRemoteSessionID(t *testing.T, s *Server, cookie *http.Cookie) string {
	t.Helper()
	req := remoteHostRequest(http.MethodGet, "/v1/auth/sessions", "", remoteTestLogin)
	req.AddCookie(cookie)
	for _, row := range listSessions(t, serveAuth(s, req)) {
		if row.Current {
			return row.ID
		}
	}
	t.Fatal("no current session row for the remote cookie")
	return ""
}

// fakeOwnerMessenger records the plain-text owner messages of REQ-RA-070.
type fakeOwnerMessenger struct {
	sent []string
}

func (f *fakeOwnerMessenger) SendOwnerMessage(_ context.Context, text string) (int, error) {
	f.sent = append(f.sent, text)
	return 1, nil
}

// TestTelegramOwnerMessengerAccessor covers SetTelegramOwnerMessenger and its
// getter, the Telegram port REQ-RA-070 fills.
func TestTelegramOwnerMessengerAccessor(t *testing.T) {
	s := newAuthServer(t, t.TempDir(), io.Discard)
	if s.telegramOwnerMessenger() != nil {
		t.Fatal("messenger is set before SetTelegramOwnerMessenger")
	}
	fake := &fakeOwnerMessenger{}
	s.SetTelegramOwnerMessenger(fake)
	if s.telegramOwnerMessenger() != fake {
		t.Fatal("SetTelegramOwnerMessenger did not store the messenger")
	}
	if sent, err := s.telegramOwnerMessenger().SendOwnerMessage(context.Background(), "hi"); err != nil || sent != 1 {
		t.Fatalf("SendOwnerMessage = %d, %v, want 1, nil", sent, err)
	}
	if len(fake.sent) != 1 || fake.sent[0] != "hi" {
		t.Fatalf("sent = %v, want [hi]", fake.sent)
	}
}
