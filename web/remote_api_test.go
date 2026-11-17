package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/notify"
	"github.com/ridhomujizat/lana-harness/internal/tailscale"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// The remote access settings tests of REQ-RA-060..064 and the sign-in
// notification tests of REQ-RA-070..072.

// remoteAPIBaseConfig is a valid config.toml without a [web.remote] table.
const remoteAPIBaseConfig = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "coder"

[web]
enabled = true
addr = "127.0.0.1:18300"
`

// remoteAPIConfigWithTable is the same config with an existing [web.remote]
// table whose host must be replaced by a save.
const remoteAPIConfigWithTable = remoteAPIBaseConfig + `
[web.remote]
enabled = true
host = "stale.tail3f2a1.ts.net"
`

// recordingTailscale is a web.tailscaleClient that records its calls
// (REQ-RA-054).
type recordingTailscale struct {
	status     tailscale.Status
	statusErr  error
	serveErr   error
	unserveErr error
	whois      map[string]tailscale.Peer

	statusAddr   string
	serveAddr    string
	serveCalls   int
	unserveCalls int
}

func newRecordingTailscale() *recordingTailscale {
	return &recordingTailscale{
		status: tailscale.Status{
			Installed:  true,
			Path:       "/usr/local/bin/tailscale",
			Version:    "1.60.0",
			Running:    true,
			DNSName:    remoteTestHost,
			Login:      remoteTestLogin,
			Tailnet:    "tail3f2a1.ts.net",
			MagicDNS:   true,
			HTTPSCerts: true,
		},
		whois: map[string]tailscale.Peer{},
	}
}

func (f *recordingTailscale) Status(_ context.Context, daemonAddr string) (tailscale.Status, error) {
	f.statusAddr = daemonAddr
	return f.status, f.statusErr
}

func (f *recordingTailscale) Serve(_ context.Context, daemonAddr string) error {
	f.serveCalls++
	f.serveAddr = daemonAddr
	return f.serveErr
}

func (f *recordingTailscale) Unserve(context.Context) error {
	f.unserveCalls++
	return f.unserveErr
}

func (f *recordingTailscale) Whois(_ context.Context, ip string) (tailscale.Peer, error) {
	return f.whois[ip], nil
}

// newRemoteAPIServer builds a server over a temp home with config.toml, a
// logged-in local session, and a recording Tailscale client.
func newRemoteAPIServer(t *testing.T, raw string) (*Server, *testConfigStore, *http.Cookie, string, *recordingTailscale) {
	t.Helper()
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	token := authTokenFor(t, store.home)
	s := newAuthServer(t, store.home, io.Discard)
	s.SetConfigStore(store)
	ts := newRecordingTailscale()
	s.SetTailscaleClient(ts)
	s.SetRemoteConfig(core.DefaultWebRemoteConfig())
	cookie, csrf := loginAndSession(t, s, token)
	return s, store, cookie, csrf, ts
}

// countRemoteSessions counts the remote rows of a session list.
func countRemoteSessions(rows []browserSessionInfo) int {
	count := 0
	for _, row := range rows {
		if row.Remote {
			count++
		}
	}
	return count
}

// auditFile reads the audit file under home.
func auditFile(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(webfiles.AuditPath(home))
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	return string(data)
}

// assertRemoteIssue checks a 422 validation_failed answer that names key.
func assertRemoteIssue(t *testing.T, rr *httptest.ResponseRecorder, key string) {
	t.Helper()
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %q", rr.Code, rr.Body.String())
	}
	envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()})
	if envelope.Error.Code != "validation_failed" {
		t.Fatalf("error code = %q, body = %q", envelope.Error.Code, rr.Body.String())
	}
	for _, detail := range envelope.Error.Details {
		if detail["key"] == key {
			return
		}
	}
	t.Fatalf("no %s detail: %s", key, rr.Body.String())
}

// TestRemoteAPIGetShape covers REQ-RA-060.
func TestRemoteAPIGetShape(t *testing.T) {
	s, store, cookie, _, ts := newRemoteAPIServer(t, remoteAPIConfigWithTable)
	s.SetRemoteConfig(remoteEnabledConfig())

	rr := serveAuth(s, settingsRequest(http.MethodGet, "/v1/web/remote", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/web/remote status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got remoteSettingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	file, err := store.ConfigFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Version != file.Version {
		t.Fatalf("version = %q, want the config version %q", got.Config.Version, file.Version)
	}
	if !got.Config.Enabled || got.Config.Host != remoteTestHost || !got.Config.RequireIdentity {
		t.Fatalf("config = %+v", got.Config)
	}
	if !reflect.DeepEqual(got.Config.AllowedLogins, []string{remoteTestLogin}) {
		t.Fatalf("allowed_logins = %v", got.Config.AllowedLogins)
	}
	if got.Config.Terminal != core.WebRemoteTerminalConfirm || got.Config.ConfirmWith != core.WebRemoteConfirmPasswordOrTelegram {
		t.Fatalf("terminal/confirm = %q/%q", got.Config.Terminal, got.Config.ConfirmWith)
	}
	if got.Config.ControlMinutes != 15 || got.Config.IdleMinutes != 60 || got.Config.MaxAgeHours != 168 || !got.Config.NotifySignIn {
		t.Fatalf("numbers = %+v", got.Config)
	}
	if !got.Status.Installed || got.Status.DNSName != remoteTestHost {
		t.Fatalf("status = %+v", got.Status)
	}
	if got.URL != "https://"+remoteTestHost {
		t.Fatalf("url = %q", got.URL)
	}
	if got.CheckedAt == 0 {
		t.Fatal("checked_at is zero")
	}
	if got.Warning != "" {
		t.Fatalf("warning = %q, want none", got.Warning)
	}
	if ts.statusAddr != s.Addr() {
		t.Fatalf("status address = %q, want %q", ts.statusAddr, s.Addr())
	}
}

// TestRemoteAPIPutSavesAndAppliesLive covers REQ-RA-011 and REQ-RA-061: the
// save writes the table (present or absent), and the new settings apply at once.
func TestRemoteAPIPutSavesAndAppliesLive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		present bool
	}{
		{name: "table absent", raw: remoteAPIBaseConfig},
		{name: "table present", raw: remoteAPIConfigWithTable, present: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, cookie, csrf, _ := newRemoteAPIServer(t, tc.raw)
			s.SetRemoteConfig(remoteEnabledConfig())
			file, err := store.ConfigFile(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			body := mustJSON(t, map[string]any{
				"expected_version": file.Version,
				"require_identity": true,
				"allowed_logins":   []string{" New@GitHub ", "new@github", " "},
				"terminal":         "full",
				"confirm_with":     "password",
				"control_minutes":  30,
				"idle_minutes":     120,
				"max_age_hours":    24,
				"notify_sign_in":   false,
			})
			rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/web/remote", body, cookie, csrf))
			if rr.Code != http.StatusOK {
				t.Fatalf("PUT status = %d, body = %q", rr.Code, rr.Body.String())
			}
			var got remoteSettingsResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode %q: %v", rr.Body.String(), err)
			}
			if !reflect.DeepEqual(got.Config.AllowedLogins, []string{"new@github"}) {
				t.Fatalf("allowed_logins = %v, want the cleaned new login", got.Config.AllowedLogins)
			}
			if got.Config.Terminal != "full" || got.Config.ConfirmWith != "password" || got.Config.NotifySignIn {
				t.Fatalf("config = %+v", got.Config)
			}
			if got.Config.ControlMinutes != 30 || got.Config.IdleMinutes != 120 || got.Config.MaxAgeHours != 24 {
				t.Fatalf("numbers = %+v", got.Config)
			}

			written, err := os.ReadFile(store.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"[web.remote]",
				"enabled = true",
				`host = "` + remoteTestHost + `"`,
				`allowed_logins = ["new@github"]`,
				`terminal = "full"`,
				`confirm_with = "password"`,
				"control_minutes = 30",
			} {
				if !strings.Contains(string(written), want) {
					t.Fatalf("config.toml is missing %q:\n%s", want, written)
				}
			}
			if tc.present && strings.Contains(string(written), "stale.tail3f2a1.ts.net") {
				t.Fatalf("the old table was not replaced:\n%s", written)
			}
			if !strings.Contains(string(written), `addr = "127.0.0.1:18300"`) {
				t.Fatalf("the [web] table changed:\n%s", written)
			}
			audit := auditFile(t, store.home)
			if !strings.Contains(audit, `"kind":"remote.settings"`) || !strings.Contains(audit, `"target":"config.toml"`) {
				t.Fatalf("audit = %q, want a remote.settings line", audit)
			}
			if !strings.Contains(audit, "web.remote.terminal") {
				t.Fatalf("audit = %q, want the changed remote keys", audit)
			}

			// The save applies live: the newly allowed login passes the gate.
			state := decodeRemoteState(t, serveAuth(s, remoteHostRequest(http.MethodGet, "/v1/auth/state", "", "new@github")))
			if !state.Remote || state.Login != "new@github" {
				t.Fatalf("state = %+v, want the new login allowed", state)
			}
			assertAuthError(t, serveAuth(s, remoteHostRequest(http.MethodGet, "/v1/auth/state", "", remoteTestLogin)), http.StatusForbidden, "remote_disabled")
		})
	}
}

// TestRemoteAPIStaleVersion covers the 409 stale_version of REQ-RA-061.
func TestRemoteAPIStaleVersion(t *testing.T) {
	s, store, cookie, csrf, _ := newRemoteAPIServer(t, remoteAPIBaseConfig)
	s.SetRemoteConfig(remoteEnabledConfig())
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, map[string]any{
		"expected_version": "deadbeef",
		"require_identity": true,
		"allowed_logins":   []string{remoteTestLogin},
		"terminal":         "confirm",
		"confirm_with":     "password_or_telegram",
		"control_minutes":  15,
		"idle_minutes":     60,
		"max_age_hours":    168,
		"notify_sign_in":   true,
	})
	assertAuthError(t, serveAuth(s, settingsRequest(http.MethodPut, "/v1/web/remote", body, cookie, csrf)), http.StatusConflict, "stale_version")
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a stale save changed config.toml:\n%s", after)
	}
}

// TestRemoteAPIValidation covers the writeWebIssue keys of REQ-RA-061.
func TestRemoteAPIValidation(t *testing.T) {
	s, store, cookie, csrf, _ := newRemoteAPIServer(t, remoteAPIBaseConfig)
	s.SetRemoteConfig(remoteEnabledConfig())
	file, err := store.ConfigFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	valid := func() map[string]any {
		return map[string]any{
			"expected_version": file.Version,
			"require_identity": true,
			"allowed_logins":   []string{remoteTestLogin},
			"terminal":         "confirm",
			"confirm_with":     "password_or_telegram",
			"control_minutes":  15,
			"idle_minutes":     60,
			"max_age_hours":    168,
			"notify_sign_in":   true,
		}
	}
	cases := []struct {
		name   string
		key    string
		mutate func(map[string]any)
	}{
		{"terminal", "web.remote.terminal", func(m map[string]any) { m["terminal"] = "lock" }},
		{"confirm_with", "web.remote.confirm_with", func(m map[string]any) { m["confirm_with"] = "code" }},
		{"control_minutes", "web.remote.control_minutes", func(m map[string]any) { m["control_minutes"] = 0 }},
		{"idle_minutes", "web.remote.idle_minutes", func(m map[string]any) { m["idle_minutes"] = 4 }},
		{"max_age_hours", "web.remote.max_age_hours", func(m map[string]any) { m["max_age_hours"] = 9999 }},
		{"expected_version", "expected_version", func(m map[string]any) { delete(m, "expected_version") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := valid()
			tc.mutate(body)
			rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/web/remote", mustJSON(t, body), cookie, csrf))
			assertRemoteIssue(t, rr, tc.key)
		})
	}
}

// TestRemoteAPIEnableNotReady covers the 409 tailscale_not_ready of REQ-RA-062.
func TestRemoteAPIEnableNotReady(t *testing.T) {
	cases := []struct {
		name    string
		status  tailscale.Status
		message string
	}{
		{"not installed", tailscale.Status{Installed: false}, remoteNotReadyMessages[0]},
		{"not running", tailscale.Status{Installed: true, Running: false}, remoteNotReadyMessages[1]},
		{"https certificates off", tailscale.Status{Installed: true, Running: true, HTTPSCerts: false}, remoteNotReadyMessages[2]},
		{"serve other", tailscale.Status{Installed: true, Running: true, HTTPSCerts: true, ServeOther: true}, remoteNotReadyMessages[3]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, store, cookie, csrf, ts := newRemoteAPIServer(t, remoteAPIBaseConfig)
			ts.status = tc.status
			file, err := store.ConfigFile(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			body := mustJSON(t, map[string]string{"expected_version": file.Version})
			rr := serveAuth(s, settingsRequest(http.MethodPost, "/v1/web/remote/enable", body, cookie, csrf))
			assertAuthError(t, rr, http.StatusConflict, "tailscale_not_ready")
			if !strings.Contains(rr.Body.String(), tc.message) {
				t.Fatalf("message = %q, want %q", rr.Body.String(), tc.message)
			}
			if ts.serveCalls != 0 {
				t.Fatalf("Serve was called %d times on a not-ready status", ts.serveCalls)
			}
		})
	}
}

// TestRemoteAPIEnableSuccess covers REQ-RA-062: Serve runs for the daemon
// address, and enabled, host, and the login are saved.
func TestRemoteAPIEnableSuccess(t *testing.T) {
	s, store, cookie, csrf, ts := newRemoteAPIServer(t, remoteAPIBaseConfig)
	file, err := store.ConfigFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, map[string]string{"expected_version": file.Version})
	rr := serveAuth(s, settingsRequest(http.MethodPost, "/v1/web/remote/enable", body, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("enable status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if ts.serveCalls != 1 || ts.serveAddr != s.Addr() {
		t.Fatalf("Serve = %d calls for %q, want 1 for %q", ts.serveCalls, ts.serveAddr, s.Addr())
	}
	var got remoteSettingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	if !got.Config.Enabled || got.Config.Host != remoteTestHost {
		t.Fatalf("config = %+v", got.Config)
	}
	if !reflect.DeepEqual(got.Config.AllowedLogins, []string{remoteTestLogin}) {
		t.Fatalf("allowed_logins = %v, want the Tailscale login", got.Config.AllowedLogins)
	}
	written, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[web.remote]", "enabled = true", `host = "` + remoteTestHost + `"`, remoteTestLogin} {
		if !strings.Contains(string(written), want) {
			t.Fatalf("config.toml is missing %q:\n%s", want, written)
		}
	}
	audit := auditFile(t, store.home)
	if !strings.Contains(audit, `"kind":"remote.enable"`) || !strings.Contains(audit, `"target":"`+remoteTestHost+`"`) {
		t.Fatalf("audit = %q, want a remote.enable line for the host", audit)
	}
	cfg := s.remoteConfig()
	if !cfg.Enabled || cfg.Host != remoteTestHost {
		t.Fatalf("in-memory config = %+v, want enabled with the host", cfg)
	}

	// Remote access is now on: an allowed remote request passes the gate.
	state := decodeRemoteState(t, serveAuth(s, remoteHostRequest(http.MethodGet, "/v1/auth/state", "", remoteTestLogin)))
	if !state.Remote {
		t.Fatalf("state = %+v, want remote true", state)
	}
}

// TestRemoteAPIDisable covers REQ-RA-063: Unserve runs, enabled is saved false
// with the host kept, and every remote session ends.
func TestRemoteAPIDisable(t *testing.T) {
	s, store, cookie, csrf, ts := newRemoteAPIServer(t, remoteAPIConfigWithTable)
	s.SetRemoteConfig(remoteEnabledConfig())

	// A remote session to be revoked.
	serveAuth(s, remoteHostRequest(http.MethodPost, "/v1/auth/login", tokenLoginBody(t, testAuthToken), remoteTestLogin))
	if rows, _ := getBrowserSessionList(t, s, cookie); countRemoteSessions(rows) != 1 {
		t.Fatalf("remote sessions before disable = %d, want 1", countRemoteSessions(rows))
	}

	file, err := store.ConfigFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, map[string]string{"expected_version": file.Version})
	rr := serveAuth(s, settingsRequest(http.MethodPost, "/v1/web/remote/disable", body, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if ts.unserveCalls != 1 {
		t.Fatalf("Unserve calls = %d, want 1", ts.unserveCalls)
	}
	var got remoteSettingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	if got.Config.Enabled {
		t.Fatal("enabled is true after disable")
	}
	if got.Config.Host != remoteTestHost {
		t.Fatalf("host = %q, want the host kept", got.Config.Host)
	}
	if got.Warning != "" {
		t.Fatalf("warning = %q, want none", got.Warning)
	}
	written, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "enabled = false") || !strings.Contains(string(written), remoteTestHost) {
		t.Fatalf("config.toml = \n%s", written)
	}
	if audit := auditFile(t, store.home); !strings.Contains(audit, `"kind":"remote.disable"`) {
		t.Fatalf("audit = %q, want a remote.disable line", audit)
	}
	if s.remoteConfig().Enabled {
		t.Fatal("in-memory config is still enabled")
	}
	if rows, _ := getBrowserSessionList(t, s, cookie); countRemoteSessions(rows) != 0 {
		t.Fatalf("remote sessions after disable = %d, want 0", countRemoteSessions(rows))
	}
}

// TestRemoteAPIDisableUnserveWarning covers REQ-RA-063: an Unserve error is a
// warning, not fatal.
func TestRemoteAPIDisableUnserveWarning(t *testing.T) {
	s, store, cookie, csrf, ts := newRemoteAPIServer(t, remoteAPIConfigWithTable)
	s.SetRemoteConfig(remoteEnabledConfig())
	ts.unserveErr = errors.New("tailscale serve: the backend is not running")

	file, err := store.ConfigFile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, map[string]string{"expected_version": file.Version})
	rr := serveAuth(s, settingsRequest(http.MethodPost, "/v1/web/remote/disable", body, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got remoteSettingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	if !strings.Contains(got.Warning, "not running") {
		t.Fatalf("warning = %q, want the Unserve error", got.Warning)
	}
	if got.Config.Enabled {
		t.Fatal("enabled is true after disable")
	}
	written, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "enabled = false") {
		t.Fatalf("config.toml was not saved:\n%s", written)
	}
}

// TestRemoteAPIForbiddenFromRemote covers REQ-RA-040: a remote browser may read
// the settings but not change them.
func TestRemoteAPIForbiddenFromRemote(t *testing.T) {
	s, _, _, _, _ := newRemoteAPIServer(t, remoteAPIConfigWithTable)
	s.SetRemoteConfig(remoteEnabledConfig())
	rr := serveAuth(s, remoteHostRequest(http.MethodPost, "/v1/auth/login", tokenLoginBody(t, testAuthToken), remoteTestLogin))
	cookie := authCookie(t, rr)
	csrf := loginCSRF(t, rr)

	get := remoteHostRequest(http.MethodGet, "/v1/web/remote", "", remoteTestLogin)
	get.AddCookie(cookie)
	if rr := serveAuth(s, get); rr.Code != http.StatusOK {
		t.Fatalf("remote GET status = %d, body = %q", rr.Code, rr.Body.String())
	}

	cases := []struct {
		method string
		target string
	}{
		{http.MethodPut, "/v1/web/remote"},
		{http.MethodPost, "/v1/web/remote/enable"},
		{http.MethodPost, "/v1/web/remote/disable"},
		{http.MethodPost, "/v1/web/remote/open-app"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			req := remoteHostRequest(tc.method, tc.target, "{}", remoteTestLogin)
			req.AddCookie(cookie)
			req.Header.Set(csrfHeaderName, csrf)
			assertAuthError(t, serveAuth(s, req), http.StatusForbidden, "remote_forbidden")
		})
	}
}

// TestRemoteAPIOpenApp covers REQ-RA-064: the runner is called on macOS and the
// path is 501 elsewhere.
func TestRemoteAPIOpenApp(t *testing.T) {
	s, _, cookie, csrf, _ := newRemoteAPIServer(t, remoteAPIBaseConfig)
	called := false
	previous := openTailscaleApp
	openTailscaleApp = func(context.Context) error {
		called = true
		return nil
	}
	t.Cleanup(func() { openTailscaleApp = previous })

	req := localHostRequest(http.MethodPost, "/v1/web/remote/open-app", "")
	req.AddCookie(cookie)
	req.Header.Set(csrfHeaderName, csrf)
	rr := serveAuth(s, req)

	if runtime.GOOS == "darwin" {
		if rr.Code != http.StatusOK {
			t.Fatalf("open-app status = %d, body = %q", rr.Code, rr.Body.String())
		}
		if !called {
			t.Fatal("the open-app runner was not called")
		}
		return
	}
	assertAuthError(t, rr, http.StatusNotImplemented, "unsupported")
	if called {
		t.Fatal("the open-app runner ran off macOS")
	}
}

// channelOwnerMessenger records the owner messages on a channel so the test can
// wait for the announce goroutine (REQ-RA-070).
type channelOwnerMessenger struct {
	messages chan string
}

func newChannelOwnerMessenger() *channelOwnerMessenger {
	return &channelOwnerMessenger{messages: make(chan string, 4)}
}

func (m *channelOwnerMessenger) SendOwnerMessage(_ context.Context, text string) (int, error) {
	m.messages <- text
	return 1, nil
}

// remoteSignInRequest is a request that carries a remote identity and a
// User-Agent.
func remoteSignInRequest(userAgent string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = remoteTestPeer
	req.Header.Set("User-Agent", userAgent)
	return req.WithContext(context.WithValue(req.Context(), remoteContextKey, remoteRequest{
		Login: remoteTestLogin,
		IP:    remoteTestIP,
	}))
}

const safariIPhoneUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"

// TestRemoteAPIAnnounceSignIn covers REQ-RA-070: the bus event, the Telegram
// message, and the audit line of a remote sign-in.
func TestRemoteAPIAnnounceSignIn(t *testing.T) {
	s, store, _, _, _ := newRemoteAPIServer(t, remoteAPIBaseConfig)
	s.SetRemoteConfig(core.DefaultWebRemoteConfig())

	bus := notify.NewBus()
	s.SetNotifications(bus)
	messenger := newChannelOwnerMessenger()
	s.SetTelegramOwnerMessenger(messenger)
	_, events, cancel := bus.Subscribe("")
	defer cancel()

	s.announceRemoteSignIn(remoteSignInRequest(safariIPhoneUserAgent), session{
		remote: true,
		login:  remoteTestLogin,
		device: remoteTestDevice,
	})

	select {
	case event := <-events:
		if event.Kind != notify.KindRemoteSignIn {
			t.Fatalf("kind = %q", event.Kind)
		}
		if event.Title != "New remote sign-in" {
			t.Fatalf("title = %q", event.Title)
		}
		want := "Safari on iPhone on " + remoteTestDevice + " (" + remoteTestLogin + ") signed in over Tailscale."
		if event.Body != want {
			t.Fatalf("body = %q, want %q", event.Body, want)
		}
		if event.URL != remoteSignInURL || event.Thread != remoteSignInThread || !event.TimeSensitive {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("no notification event was published")
	}

	select {
	case text := <-messenger.messages:
		if !strings.Contains(text, "New remote sign-in") || !strings.Contains(text, remoteTestDevice) {
			t.Fatalf("telegram message = %q", text)
		}
	case <-time.After(time.Second):
		t.Fatal("no telegram message was sent")
	}

	audit := auditFile(t, store.home)
	if !strings.Contains(audit, "remote.sign_in") || !strings.Contains(audit, remoteTestLogin+"@"+remoteTestDevice) {
		t.Fatalf("audit line = %q", audit)
	}
}

// TestRemoteAPIAnnounceNotifyOff covers REQ-RA-070: with notify_sign_in off the
// audit line is still written and no event or message is sent.
func TestRemoteAPIAnnounceNotifyOff(t *testing.T) {
	s, store, _, _, _ := newRemoteAPIServer(t, remoteAPIBaseConfig)
	cfg := core.DefaultWebRemoteConfig()
	cfg.NotifySignIn = false
	s.SetRemoteConfig(cfg)

	bus := notify.NewBus()
	s.SetNotifications(bus)
	messenger := newChannelOwnerMessenger()
	s.SetTelegramOwnerMessenger(messenger)

	s.announceRemoteSignIn(remoteSignInRequest(safariIPhoneUserAgent), session{
		remote: true,
		login:  remoteTestLogin,
		device: remoteTestDevice,
	})

	if bus.LastID() != "" {
		t.Fatalf("a notification was published: %q", bus.LastID())
	}
	select {
	case text := <-messenger.messages:
		t.Fatalf("a telegram message was sent: %q", text)
	default:
	}
	audit := auditFile(t, store.home)
	if !strings.Contains(audit, "remote.sign_in") {
		t.Fatalf("audit line = %q, want the sign-in line", audit)
	}
}

// TestRemoteAPIAuditSignInFailed covers REQ-RA-072: the failed sign-in audit
// target is the login.
func TestRemoteAPIAuditSignInFailed(t *testing.T) {
	s, store, _, _, _ := newRemoteAPIServer(t, remoteAPIBaseConfig)
	s.auditRemoteSignInFailed(remoteSignInRequest(safariIPhoneUserAgent))

	audit := auditFile(t, store.home)
	if !strings.Contains(audit, "remote.sign_in_failed") || !strings.Contains(audit, remoteTestLogin) {
		t.Fatalf("audit line = %q", audit)
	}
}

// TestBrowserLabel covers the short browser and platform names of REQ-RA-070.
func TestBrowserLabel(t *testing.T) {
	cases := []struct {
		name string
		ua   string
		want string
	}{
		{"safari iphone", safariIPhoneUserAgent, "Safari on iPhone"},
		{"chrome android", "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36", "Chrome on Android"},
		{"chrome ios", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/120.0.0.0 Mobile/15E148 Safari/604.1", "Chrome on iPhone"},
		{"firefox macos", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:120.0) Gecko/20100101 Firefox/120.0", "Firefox on macOS"},
		{"firefox ios", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/120.0 Mobile/15E148 Safari/605.1.15", "Firefox on iPhone"},
		{"edge windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0", "Edge on Windows"},
		{"safari macos", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15", "Safari on macOS"},
		{"chrome macos", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", "Chrome on macOS"},
		{"chromeos", "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", "Chrome on ChromeOS"},
		{"platform only", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)", "A browser on Windows"},
		{"unknown", "curl/8.4.0", "A browser"},
		{"empty", "", "A browser"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := browserLabel(tc.ua); got != tc.want {
				t.Fatalf("browserLabel(%q) = %q, want %q", tc.ua, got, tc.want)
			}
		})
	}
}

// TestRemoteGateLocalAPIFix covers the gate fix: a /v1/local/ request that
// carries a Serve header from a loopback peer is refused, a plain local request
// still reaches its handler.
func TestRemoteGateLocalAPIFix(t *testing.T) {
	s := newAuthServer(t, t.TempDir(), io.Discard)
	s.SetLocalSecret(localTestSecret)

	t.Run("plain local request passes even with an odd host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, LocalSignInPath, nil)
		req.RemoteAddr = remoteTestPeer
		req.Host = "odd.example"
		// The handler refuses the missing secret; the gate did not turn it
		// into a remote_disabled answer.
		if rr := serveAuth(s, req); rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body = %q", rr.Code, rr.Body.String())
		}
	})

	t.Run("forwarded header from a loopback peer is refused", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, LocalSignInPath, nil)
		req.RemoteAddr = remoteTestPeer
		req.Host = authTestAddr
		req.Header.Set(remoteForwardedForHeader, remoteTestIP)
		assertAuthError(t, serveAuth(s, req), http.StatusForbidden, "remote_disabled")
	})

	t.Run("tailscale login header from a loopback peer is refused", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, LocalSignInPath, nil)
		req.RemoteAddr = remoteTestPeer
		req.Host = authTestAddr
		req.Header.Set(remoteUserLoginHeader, remoteTestLogin)
		assertAuthError(t, serveAuth(s, req), http.StatusForbidden, "remote_disabled")
	})

	t.Run("headers from a non-loopback peer are ignored", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, LocalSignInPath, nil)
		req.RemoteAddr = outsideTestPeer
		req.Host = authTestAddr
		req.Header.Set(remoteForwardedForHeader, remoteTestIP)
		if rr := serveAuth(s, req); rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body = %q", rr.Code, rr.Body.String())
		}
	})
}
