package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// appLink asks the local endpoint for an app link and returns it.
func appLink(t *testing.T, s *Server, next string) string {
	t.Helper()
	target := LocalAppLinkPath
	if next != "" {
		target += "?next=" + url.QueryEscape(next)
	}
	req := httptest.NewRequest(http.MethodPost, target, nil)
	req.Host = authTestAddr
	req.RemoteAddr = localTestAddr
	req.Header.Set(LocalSecretHeader, localTestSecret)
	rr := serveAuth(s, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("app link status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var payload struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	return payload.Link
}

// openAppLink requests the path and query of link, with an optional cookie.
func openAppLink(t *testing.T, s *Server, link string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	req := authRequest(http.MethodGet, parsed.RequestURI(), "")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return serveAuth(s, req)
}

// TestAppLinkSignsInOnce covers AC-DSK-001: the app link starts a session,
// redirects to next, and works once; no password is set.
func TestAppLinkSignsInOnce(t *testing.T) {
	s := newAuthServer(t, t.TempDir(), io.Discard)
	s.SetLocalSecret(localTestSecret)

	link := appLink(t, s, "#/calendar")
	if !strings.HasPrefix(link, "http://"+authTestAddr+AppSignInPath+"?") {
		t.Fatalf("link = %q", link)
	}
	rr := openAppLink(t, s, link, nil)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/#/calendar" {
		t.Fatalf("status = %d, location = %q", rr.Code, rr.Header().Get("Location"))
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	cookie := authCookie(t, rr)
	if got := getAuthSession(s, cookie); got.Code != http.StatusOK {
		t.Fatalf("session status = %d, body = %q", got.Code, got.Body.String())
	}
	if s.credentials != nil && s.credentials.PasswordSet() {
		t.Fatal("the app link set a password")
	}

	// A second use of the same code starts no session.
	again := openAppLink(t, s, link, nil)
	if again.Code != http.StatusSeeOther || again.Header().Get("Location") != "/" {
		t.Fatalf("reuse: status = %d, location = %q", again.Code, again.Header().Get("Location"))
	}
	for _, c := range again.Result().Cookies() {
		if c.Name == sessionCookieName {
			t.Fatalf("reused code set a cookie: %v", c)
		}
	}
}

// TestAppLinkKeepsExistingSession covers AC-DSK-002: a caller that is already
// signed in keeps its session and no new one starts.
func TestAppLinkKeepsExistingSession(t *testing.T) {
	s := newAuthServer(t, t.TempDir(), io.Discard)
	s.SetLocalSecret(localTestSecret)

	first := openAppLink(t, s, appLink(t, s, ""), nil)
	cookie := authCookie(t, first)
	if got := first.Header().Get("Location"); got != "/"+defaultAppNext {
		t.Fatalf("default next: location = %q", got)
	}
	before := len(s.sessions.records)

	rr := openAppLink(t, s, appLink(t, s, "#/workflows"), cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/#/workflows" {
		t.Fatalf("status = %d, location = %q", rr.Code, rr.Header().Get("Location"))
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Fatalf("a signed-in caller got a new cookie: %v", rr.Result().Cookies())
	}
	if after := len(s.sessions.records); after != before {
		t.Fatalf("sessions = %d, want %d", after, before)
	}
	// An expired session with a fresh code starts a new one.
	expired := &http.Cookie{Name: sessionCookieName, Value: "not-a-session"}
	rr = openAppLink(t, s, appLink(t, s, ""), expired)
	authCookie(t, rr)
}

// TestAppLinkExpires covers the two-minute code lifetime and an unsafe next.
func TestAppLinkExpires(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	s := newAuthServer(t, t.TempDir(), io.Discard)
	s.now = func() time.Time { return now }
	s.SetLocalSecret(localTestSecret)

	link := appLink(t, s, "https://example.com/")
	if strings.Contains(link, "next=") {
		t.Fatalf("unsafe next kept: %q", link)
	}
	now = now.Add(appCodeTTL + time.Second)
	rr := openAppLink(t, s, link, nil)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("status = %d, location = %q", rr.Code, rr.Header().Get("Location"))
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Fatalf("expired code set a cookie: %v", rr.Result().Cookies())
	}
}

// TestAppLinkCodeIsNotASignInCode: an app code cannot claim a password and a
// sign-in link code cannot open the app path.
func TestAppLinkCodeIsNotASignInCode(t *testing.T) {
	s := newAuthServer(t, t.TempDir(), io.Discard)
	s.SetLocalSecret(localTestSecret)

	code := codeFromLink(t, appLink(t, s, ""))
	body, err := json.Marshal(map[string]string{"code": code, "password": testPassword})
	if err != nil {
		t.Fatal(err)
	}
	assertAuthError(t, serveAuth(s, authRequest(http.MethodPost, "/v1/auth/claim", string(body))), http.StatusGone, "link_expired")

	signIn, err := s.SignInLink()
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, authRequest(http.MethodGet, AppSignInPath+"?code="+codeFromLink(t, signIn), ""))
	if len(rr.Result().Cookies()) != 0 {
		t.Fatalf("a sign-in link code opened the app path: %v", rr.Result().Cookies())
	}
}

// TestLocalAppLinkChecks covers the local control checks of API-DSK-001.
func TestLocalAppLinkChecks(t *testing.T) {
	s := newAuthServer(t, t.TempDir(), io.Discard)
	s.SetLocalSecret(localTestSecret)
	request := func(method, secret, remote string) *http.Request {
		req := httptest.NewRequest(method, LocalAppLinkPath, nil)
		req.Host = authTestAddr
		req.RemoteAddr = remote
		if secret != "" {
			req.Header.Set(LocalSecretHeader, secret)
		}
		return req
	}
	assertAuthError(t, serveAuth(s, request(http.MethodPost, "", localTestAddr)), http.StatusUnauthorized, "unauthorized")
	assertAuthError(t, serveAuth(s, request(http.MethodPost, "wrong", localTestAddr)), http.StatusUnauthorized, "unauthorized")
	assertAuthError(t, serveAuth(s, request(http.MethodPost, localTestSecret, localTestOutside)), http.StatusForbidden, "forbidden_remote")
	assertAuthError(t, serveAuth(s, request(http.MethodGet, localTestSecret, localTestAddr)), http.StatusMethodNotAllowed, "method_not_allowed")

	// The app path keeps the Host check.
	req := authRequest(http.MethodGet, AppSignInPath+"?code=x", "")
	req.Host = "evil.example:18300"
	assertAuthError(t, serveAuth(s, req), http.StatusForbidden, "forbidden_host")
}
