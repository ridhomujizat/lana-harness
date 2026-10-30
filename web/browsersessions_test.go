package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// doLoginAs logs in with a specific User-Agent and returns the cookie.
func doLoginAs(t *testing.T, s *Server, token, userAgent string) *http.Cookie {
	t.Helper()
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	req := authRequest(http.MethodPost, "/v1/auth/login", string(body))
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	rr := serveAuth(s, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %q", rr.Code, rr.Body.String())
	}
	return authCookie(t, rr)
}

// csrfForSession returns the CSRF value issued by GET /v1/auth/session.
func csrfForSession(t *testing.T, s *Server, cookie *http.Cookie) string {
	t.Helper()
	rr := getAuthSession(s, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("session status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("session body %q: %v", rr.Body.String(), err)
	}
	if body.CSRF == "" {
		t.Fatal("session body has no csrf value")
	}
	return body.CSRF
}

func getBrowserSessionList(t *testing.T, s *Server, cookie *http.Cookie) ([]browserSessionInfo, *httptest.ResponseRecorder) {
	t.Helper()
	req := authRequest(http.MethodGet, "/v1/auth/sessions", "")
	req.AddCookie(cookie)
	rr := serveAuth(s, req)
	if rr.Code != http.StatusOK {
		return nil, rr
	}
	var body struct {
		Sessions []browserSessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("sessions body %q: %v", rr.Body.String(), err)
	}
	return body.Sessions, rr
}

func doRevokeBrowsers(s *Server, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
	req := authRequest(http.MethodPost, "/v1/auth/sessions/revoke-others", "")
	req.AddCookie(cookie)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	return serveAuth(s, req)
}

func readSessionsFileForTest(t *testing.T, home string) webfiles.SessionsFile {
	t.Helper()
	file, err := webfiles.ReadSessionsFile(filepath.Join(home, webfiles.SessionsFileName))
	if err != nil {
		t.Fatalf("ReadSessionsFile: %v", err)
	}
	return file
}

// TestBrowserSessionsCoversACWEB039 covers AC-WEB-039: two browsers signed in,
// a restart keeps both logins, revoke-others signs B out and keeps A, the
// response counts the revoked record, and the file holds hashes only with mode
// 0600.
func TestBrowserSessionsCoversACWEB039(t *testing.T) {
	home := t.TempDir()
	token := authTokenFor(t, home)
	s := newAuthServer(t, home, io.Discard)

	cookieA := doLoginAs(t, s, token, "Browser A")
	cookieB := doLoginAs(t, s, token, "Browser B")
	csrfA := csrfForSession(t, s, cookieA)
	csrfB := csrfForSession(t, s, cookieB)

	path := filepath.Join(home, webfiles.SessionsFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, raw := range []string{cookieA.Value, cookieB.Value, csrfA, csrfB} {
		if bytes.Contains(data, []byte(raw)) {
			t.Fatalf("%s holds a plain session or CSRF value %q: %s", webfiles.SessionsFileName, raw, data)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != webfiles.SessionsFileMode {
		t.Fatalf("mode = %04o, want %04o", got, webfiles.SessionsFileMode)
	}
	file := readSessionsFileForTest(t, home)
	if len(file.Sessions) != 2 {
		t.Fatalf("stored sessions = %d, want 2", len(file.Sessions))
	}

	sessions, _ := getBrowserSessionList(t, s, cookieA)
	if len(sessions) != 2 {
		t.Fatalf("listed sessions = %d, want 2", len(sessions))
	}
	idRE := regexp.MustCompile(`^[0-9a-f]{12}$`)
	seenCurrent := 0
	for _, item := range sessions {
		if !idRE.MatchString(item.ID) {
			t.Fatalf("id = %q, want 12 lowercase hex characters", item.ID)
		}
		if item.UserAgent != "Browser A" && item.UserAgent != "Browser B" {
			t.Fatalf("user_agent = %q", item.UserAgent)
		}
		if item.CreatedAt == 0 || item.LastUsedAt == 0 {
			t.Fatalf("timestamps missing for %+v", item)
		}
		if item.Current {
			seenCurrent++
			if item.UserAgent != "Browser A" {
				t.Fatalf("current = true for %q, want Browser A", item.UserAgent)
			}
		}
	}
	if seenCurrent != 1 {
		t.Fatalf("current rows = %d, want 1", seenCurrent)
	}

	// A restart from browser A reads the same file; no login is needed.
	restarted := newAuthServer(t, home, nil)
	if rr := getAuthSession(restarted, cookieA); rr.Code != http.StatusOK {
		t.Fatalf("A after restart status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if got := csrfForSession(t, restarted, cookieA); got != csrfA {
		t.Fatalf("CSRF after restart = %q, want %q", got, csrfA)
	}
	if rr := getAuthSession(restarted, cookieB); rr.Code != http.StatusOK {
		t.Fatalf("B after restart status = %d, body = %q", rr.Code, rr.Body.String())
	}

	// "Sign out other browsers" from A revokes B only.
	rr := doRevokeBrowsers(restarted, cookieA, csrfA, authTestOrigin)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var revoked struct {
		Revoked int `json:"revoked"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &revoked); err != nil {
		t.Fatalf("revoke body %q: %v", rr.Body.String(), err)
	}
	if revoked.Revoked != 1 {
		t.Fatalf("revoked = %d, want 1", revoked.Revoked)
	}
	assertAuthError(t, getAuthSession(restarted, cookieB), http.StatusUnauthorized, "unauthenticated")
	if rr := getAuthSession(restarted, cookieA); rr.Code != http.StatusOK {
		t.Fatalf("A after revoke status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if rr := doRevokeBrowsers(restarted, cookieA, csrfA, authTestOrigin); rr.Code != http.StatusOK {
		t.Fatalf("second revoke status = %d, body = %q", rr.Code, rr.Body.String())
	} else if !strings.Contains(rr.Body.String(), `"revoked":0`) {
		t.Fatalf("second revoke body = %q, want revoked 0", rr.Body.String())
	}

	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s after revoke: %v", path, err)
	}
	for _, raw := range []string{cookieA.Value, cookieB.Value, csrfA, csrfB} {
		if bytes.Contains(data, []byte(raw)) {
			t.Fatalf("%s holds a plain session or CSRF value %q after revoke: %s", webfiles.SessionsFileName, raw, data)
		}
	}
	if got := len(readSessionsFileForTest(t, home).Sessions); got != 1 {
		t.Fatalf("stored sessions after revoke = %d, want 1", got)
	}
	sessions, _ = getBrowserSessionList(t, restarted, cookieA)
	if len(sessions) != 1 || !sessions[0].Current {
		t.Fatalf("listed sessions after revoke = %+v, want the current browser only", sessions)
	}
}

func TestBrowserSessionsLastUsedThrottledToOneMinute(t *testing.T) {
	s, token := newAuthServerWithToken(t)
	home := s.cfg.Home
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	cookie := doLoginAs(t, s, token, "Throttled")
	csrfForSession(t, s, cookie)
	file := readSessionsFileForTest(t, home)
	if len(file.Sessions) != 1 {
		t.Fatalf("stored sessions = %d, want 1", len(file.Sessions))
	}
	loginUsed := file.Sessions[0].LastUsedAt

	now = now.Add(30 * time.Second)
	csrfForSession(t, s, cookie)
	file = readSessionsFileForTest(t, home)
	if file.Sessions[0].LastUsedAt != loginUsed {
		t.Fatalf("last_used_at written after 30s: got %d, want %d", file.Sessions[0].LastUsedAt, loginUsed)
	}

	now = now.Add(31 * time.Second)
	csrfForSession(t, s, cookie)
	file = readSessionsFileForTest(t, home)
	if want := now.UnixMilli(); file.Sessions[0].LastUsedAt != want {
		t.Fatalf("last_used_at after 61s = %d, want %d", file.Sessions[0].LastUsedAt, want)
	}
}

func TestBrowserSessionsIdleExpiryPrunesFile(t *testing.T) {
	home := t.TempDir()
	token := authTokenFor(t, home)
	s := newAuthServer(t, home, io.Discard)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	cookie := doLoginAs(t, s, token, "Expiring")
	if got := len(readSessionsFileForTest(t, home).Sessions); got != 1 {
		t.Fatalf("stored sessions = %d, want 1", got)
	}

	now = now.Add(sessionIdleTTL)
	assertAuthError(t, getAuthSession(s, cookie), http.StatusUnauthorized, "unauthenticated")
	if got := len(readSessionsFileForTest(t, home).Sessions); got != 0 {
		t.Fatalf("stored sessions after expiry = %d, want 0", got)
	}
}

func TestBrowserSessionsPruneExpiredOnLoad(t *testing.T) {
	home := t.TempDir()
	idHash := sha256.Sum256([]byte("stale-cookie"))
	csrfHash := sha256.Sum256([]byte("stale-csrf"))
	stale := time.Now().Add(-13 * time.Hour).UnixMilli()
	file := webfiles.SessionsFile{Version: webfiles.SessionsFileVersion, Sessions: []webfiles.SessionRecord{{
		IDHash:     hex.EncodeToString(idHash[:]),
		CSRFHash:   hex.EncodeToString(csrfHash[:]),
		CreatedAt:  stale,
		LastUsedAt: stale,
		UserAgent:  "Stale",
	}}}
	if err := webfiles.WriteSessionsFile(filepath.Join(home, webfiles.SessionsFileName), file); err != nil {
		t.Fatalf("WriteSessionsFile: %v", err)
	}
	var stderr bytes.Buffer
	s := newAuthServer(t, home, &stderr)
	if got := len(s.sessions.records); got != 0 {
		t.Fatalf("records after load = %d, want 0", got)
	}
}

func TestBrowserSessionsCorruptFileStopsStart(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, webfiles.SessionsFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(Config{Addr: authTestAddr, Enabled: true, Home: home, Stderr: io.Discard})
	if err == nil {
		t.Fatal("New succeeded with a corrupt session file")
	}
	if !errors.Is(err, webfiles.ErrSessionFile) {
		t.Fatalf("error = %v, want ErrSessionFile", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %q, want the file path", err)
	}
}

func TestBrowserSessionsRequestRules(t *testing.T) {
	s, token := newAuthServerWithToken(t)
	cookie := doLoginAs(t, s, token, "Rules")
	csrf := csrfForSession(t, s, cookie)

	req := authRequest(http.MethodGet, "/v1/auth/sessions", "")
	assertAuthError(t, serveAuth(s, req), http.StatusUnauthorized, "unauthenticated")

	rr := doRevokeBrowsers(s, cookie, "wrong", authTestOrigin)
	assertAuthError(t, rr, http.StatusForbidden, "csrf_invalid")
	if rr := getAuthSession(s, cookie); rr.Code != http.StatusOK {
		t.Fatalf("session after rejected revoke = %d", rr.Code)
	}

	rr = doRevokeBrowsers(s, cookie, csrf, "https://evil.example")
	assertAuthError(t, rr, http.StatusForbidden, "forbidden_origin")
	if rr := getAuthSession(s, cookie); rr.Code != http.StatusOK {
		t.Fatalf("session after rejected origin = %d", rr.Code)
	}
}

func TestBrowserSessionsUserAgentTruncated(t *testing.T) {
	s, token := newAuthServerWithToken(t)
	long := strings.Repeat("a", 200)
	cookie := doLoginAs(t, s, token, long)
	sessions, rr := getBrowserSessionList(t, s, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if len(sessions) != 1 {
		t.Fatalf("listed sessions = %d, want 1", len(sessions))
	}
	if got := len(sessions[0].UserAgent); got != 120 {
		t.Fatalf("user_agent length = %d, want 120", got)
	}
}
