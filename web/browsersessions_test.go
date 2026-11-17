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

// newTestBrowserSessions returns a persisted store that starts with no records.
func newTestBrowserSessions(t *testing.T) (*browserSessions, string) {
	t.Helper()
	home := t.TempDir()
	st, err := loadBrowserSessions(home, time.Now())
	if err != nil {
		t.Fatalf("loadBrowserSessions: %v", err)
	}
	return st, home
}

// TestBrowserSessionsRemoteFields covers REQ-RA-020 and REQ-RA-024: a remote
// add, touch, list, and stored record carry the Tailscale identity.
func TestBrowserSessionsRemoteFields(t *testing.T) {
	st, home := newTestBrowserSessions(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	sess, err := st.addRemote("remote-cookie", "Safari on iPhone", remoteIdentity{Login: "ridho@github", Device: "iPhone 15", IP: "100.64.0.1"}, now)
	if err != nil {
		t.Fatalf("addRemote: %v", err)
	}
	if !sess.remote || sess.login != "ridho@github" || sess.device != "iPhone 15" {
		t.Fatalf("added session = %+v, want the remote identity", sess)
	}
	if !sess.controlUntil.IsZero() {
		t.Fatalf("added controlUntil = %v, want the zero time while locked", sess.controlUntil)
	}
	if want := now.Add(remoteIdleDefault); !sess.expiresAt.Equal(want) {
		t.Fatalf("expiresAt = %v, want %v", sess.expiresAt, want)
	}

	touched, ok, err := st.touch("remote-cookie", now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("touch = %v, %v, want ok", ok, err)
	}
	if !touched.remote || touched.login != "ridho@github" || touched.device != "iPhone 15" {
		t.Fatalf("touched session = %+v, want the remote identity", touched)
	}

	infos := st.list(sess.key, now.Add(2*time.Minute))
	if len(infos) != 1 {
		t.Fatalf("listed sessions = %d, want 1", len(infos))
	}
	info := infos[0]
	if !info.Remote || info.Login != "ridho@github" || info.Device != "iPhone 15" || info.IP != "100.64.0.1" {
		t.Fatalf("listed row = %+v, want the remote identity", info)
	}
	if !info.Current {
		t.Fatalf("listed row is not current: %+v", info)
	}
	if info.ControlUntil != 0 {
		t.Fatalf("control_until = %d, want 0 while locked", info.ControlUntil)
	}

	file := readSessionsFileForTest(t, home)
	if len(file.Sessions) != 1 {
		t.Fatalf("stored sessions = %d, want 1", len(file.Sessions))
	}
	stored := file.Sessions[0]
	if !stored.Remote || stored.Login != "ridho@github" || stored.Device != "iPhone 15" || stored.IP != "100.64.0.1" {
		t.Fatalf("stored record = %+v, want the remote identity", stored)
	}
}

// TestBrowserSessionsLocalStaysLocal covers REQ-RA-020: a local record keeps
// every remote field empty.
func TestBrowserSessionsLocalStaysLocal(t *testing.T) {
	st, home := newTestBrowserSessions(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	sess, err := st.add("local-cookie", "Local browser", now)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if sess.remote || sess.login != "" || sess.device != "" || !sess.controlUntil.IsZero() {
		t.Fatalf("local session = %+v, want the remote fields empty", sess)
	}
	infos := st.list(sess.key, now)
	if len(infos) != 1 || infos[0].Remote || infos[0].Login != "" || infos[0].ControlUntil != 0 {
		t.Fatalf("listed rows = %+v, want one local row", infos)
	}
	if stored := readSessionsFileForTest(t, home).Sessions; len(stored) != 1 || stored[0].Remote {
		t.Fatalf("stored records = %+v, want one local record", stored)
	}
}

// TestBrowserSessionsRemoteIdleExpiry covers REQ-RA-021: a remote session ends
// after the remote idle limit.
func TestBrowserSessionsRemoteIdleExpiry(t *testing.T) {
	st, home := newTestBrowserSessions(t)
	st.setRemoteLimits(30*time.Minute, 168*time.Hour)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	if _, err := st.addRemote("c", "ua", remoteIdentity{Login: "l"}, now); err != nil {
		t.Fatalf("addRemote: %v", err)
	}
	if _, ok, _ := st.touch("c", now.Add(29*time.Minute)); !ok {
		t.Fatal("remote session expired before the idle limit")
	}
	if _, ok, _ := st.touch("c", now.Add(time.Hour)); ok {
		t.Fatal("remote session survived past the idle limit")
	}
	if got := len(readSessionsFileForTest(t, home).Sessions); got != 0 {
		t.Fatalf("stored sessions after expiry = %d, want 0", got)
	}
}

// TestBrowserSessionsRemoteMaxAgeExpiry covers REQ-RA-021: max age ends a
// remote session even while it is in active use.
func TestBrowserSessionsRemoteMaxAgeExpiry(t *testing.T) {
	st, _ := newTestBrowserSessions(t)
	st.setRemoteLimits(24*time.Hour, 2*time.Hour)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	if _, err := st.addRemote("c", "ua", remoteIdentity{Login: "l"}, now); err != nil {
		t.Fatalf("addRemote: %v", err)
	}
	if _, ok, _ := st.touch("c", now.Add(2*time.Hour-time.Minute)); !ok {
		t.Fatal("remote session expired before max age")
	}
	// Still inside the idle window, but past max age.
	if _, ok, _ := st.touch("c", now.Add(2*time.Hour+time.Minute)); ok {
		t.Fatal("remote session survived past max age")
	}
}

// TestBrowserSessionsLocalKeepsIdleTTL covers REQ-RA-021: the remote limits do
// not shorten a local session's 12-hour idle limit.
func TestBrowserSessionsLocalKeepsIdleTTL(t *testing.T) {
	st, _ := newTestBrowserSessions(t)
	st.setRemoteLimits(30*time.Minute, time.Hour)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	if _, err := st.add("local-cookie", "ua", now); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Six hours of idle is fine for a local session, though the remote idle
	// limit is 30 minutes.
	if _, ok, _ := st.touch("local-cookie", now.Add(6*time.Hour)); !ok {
		t.Fatal("local session expired before sessionIdleTTL")
	}
	if _, ok, _ := st.touch("local-cookie", now.Add(6*time.Hour+sessionIdleTTL)); ok {
		t.Fatal("local session survived past sessionIdleTTL")
	}
}

// TestBrowserSessionsRevokeByID covers REQ-RA-025: one session is removed by
// its 12-hex display id.
func TestBrowserSessionsRevokeByID(t *testing.T) {
	st, home := newTestBrowserSessions(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	sess, err := st.addRemote("c", "ua", remoteIdentity{Login: "l"}, now)
	if err != nil {
		t.Fatalf("addRemote: %v", err)
	}
	display := hex.EncodeToString(sess.key[:])[:sessionIDDisplayHex]

	if _, ok, err := st.revokeByID("000000000000"); err != nil || ok {
		t.Fatalf("revokeByID(unknown) = %v, %v, want no match", ok, err)
	}
	if _, ok, err := st.revokeByID("abc"); err != nil || ok {
		t.Fatalf("revokeByID(short) = %v, %v, want no match", ok, err)
	}

	key, ok, err := st.revokeByID(display)
	if err != nil || !ok {
		t.Fatalf("revokeByID(%q) = %v, %v, want a match", display, ok, err)
	}
	if key != sess.key {
		t.Fatalf("revokeByID key = %x, want %x", key, sess.key)
	}
	if len(st.records) != 0 {
		t.Fatalf("records after revoke = %d, want 0", len(st.records))
	}
	if got := len(readSessionsFileForTest(t, home).Sessions); got != 0 {
		t.Fatalf("stored sessions after revoke = %d, want 0", got)
	}
}

// TestBrowserSessionsRevokeRemoteLeavesLocal covers REQ-RA-025: revoke-remote
// ends every remote session and keeps the local ones.
func TestBrowserSessionsRevokeRemoteLeavesLocal(t *testing.T) {
	st, home := newTestBrowserSessions(t)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	local, err := st.add("local-cookie", "Local", now)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	for _, login := range []string{"a@github", "b@github"} {
		if _, err := st.addRemote("remote-"+login, "ua", remoteIdentity{Login: login, Device: "phone", IP: "100.64.0.9"}, now); err != nil {
			t.Fatalf("addRemote: %v", err)
		}
	}
	if got := len(st.remoteKeys()); got != 2 {
		t.Fatalf("remoteKeys = %d, want 2", got)
	}
	revoked, err := st.revokeRemote()
	if err != nil {
		t.Fatalf("revokeRemote: %v", err)
	}
	if revoked != 2 {
		t.Fatalf("revokeRemote = %d, want 2", revoked)
	}
	if got := len(st.remoteKeys()); got != 0 {
		t.Fatalf("remoteKeys after revoke = %d, want 0", got)
	}
	if _, ok := st.records[local.key]; !ok {
		t.Fatal("local record was revoked")
	}
	if _, ok, _ := st.touch("local-cookie", now.Add(time.Minute)); !ok {
		t.Fatal("local session was revoked")
	}
	stored := readSessionsFileForTest(t, home).Sessions
	if len(stored) != 1 || stored[0].Remote {
		t.Fatalf("stored records after revoke = %+v, want one local record", stored)
	}
	if got, err := st.revokeRemote(); err != nil || got != 0 {
		t.Fatalf("second revokeRemote = %d, %v, want 0, nil", got, err)
	}
}

// TestBrowserSessionsControlUntilPersists covers REQ-RA-032 and REQ-RA-034:
// the control deadline survives a reload and clearing it relocks control.
func TestBrowserSessionsControlUntilPersists(t *testing.T) {
	st, home := newTestBrowserSessions(t)
	st.setRemoteLimits(time.Hour, 168*time.Hour)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	sess, err := st.addRemote("c", "ua", remoteIdentity{Login: "l"}, now)
	if err != nil {
		t.Fatalf("addRemote: %v", err)
	}
	until := now.Add(15 * time.Minute)
	if err := st.setControlUntil(sess.key, until); err != nil {
		t.Fatalf("setControlUntil: %v", err)
	}
	infos := st.list(sess.key, now)
	if len(infos) != 1 || infos[0].ControlUntil != until.UnixMilli() {
		t.Fatalf("listed rows = %+v, want control_until %d", infos, until.UnixMilli())
	}
	if got := readSessionsFileForTest(t, home).Sessions[0].ControlUntil; got != until.UnixMilli() {
		t.Fatalf("stored control_until = %d, want %d", got, until.UnixMilli())
	}

	reloaded, err := loadBrowserSessions(home, now)
	if err != nil {
		t.Fatalf("loadBrowserSessions: %v", err)
	}
	touched, ok, err := reloaded.touch("c", now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("touch after reload = %v, %v, want ok", ok, err)
	}
	if !touched.controlUntil.Equal(until) {
		t.Fatalf("touched controlUntil = %v, want %v", touched.controlUntil, until)
	}
	if rows := reloaded.list(touched.key, now.Add(time.Minute)); len(rows) != 1 || rows[0].ControlUntil != until.UnixMilli() {
		t.Fatalf("reloaded rows = %+v, want control_until %d", rows, until.UnixMilli())
	}

	if err := reloaded.setControlUntil(touched.key, time.Time{}); err != nil {
		t.Fatalf("setControlUntil(zero): %v", err)
	}
	if got := readSessionsFileForTest(t, home).Sessions[0].ControlUntil; got != 0 {
		t.Fatalf("stored control_until after lock = %d, want 0", got)
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
