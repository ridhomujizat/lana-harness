package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	authTestAddr   = "127.0.0.1:18300"
	authTestOrigin = "http://127.0.0.1:18300"
	// testAuthToken is the legacy owner token the token-based tests use.
	testAuthToken = "test-owner-token-0123456789abcdef"
	// testPassword is a valid 8+ character dashboard password.
	testPassword = "correct horse battery"
)

func newAuthServer(t *testing.T, home string, stderr io.Writer) *Server {
	t.Helper()
	if stderr == nil {
		stderr = io.Discard
	}
	s, err := New(Config{Addr: authTestAddr, Enabled: true, Home: home, Stderr: stderr})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// writeTokenFile writes a version 1 web.json holding token's SHA-256.
func writeTokenFile(t *testing.T, home, token string) {
	t.Helper()
	sum := sha256.Sum256([]byte(token))
	body := `{"version":1,"token_sha256":"` + hex.EncodeToString(sum[:]) + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, credentialFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// authTokenFor writes a token file into home and returns the token.
func authTokenFor(t *testing.T, home string) string {
	t.Helper()
	writeTokenFile(t, home, testAuthToken)
	return testAuthToken
}

func newAuthServerWithToken(t *testing.T) (*Server, string) {
	t.Helper()
	home := t.TempDir()
	token := authTokenFor(t, home)
	return newAuthServer(t, home, io.Discard), token
}

func authRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = authTestAddr
	return req
}

func serveAuth(s *Server, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}

func doAuthLogin(t *testing.T, s *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	return serveAuth(s, authRequest(http.MethodPost, "/v1/auth/login", string(body)))
}

func doAuthPasswordLogin(t *testing.T, s *Server, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"password": password})
	if err != nil {
		t.Fatal(err)
	}
	return serveAuth(s, authRequest(http.MethodPost, "/v1/auth/login", string(body)))
}

func doAuthLogout(s *Server, cookie *http.Cookie, origin, csrf string) *httptest.ResponseRecorder {
	req := authRequest(http.MethodPost, "/v1/auth/logout", "")
	req.AddCookie(cookie)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	return serveAuth(s, req)
}

func getAuthSession(s *Server, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := authRequest(http.MethodGet, "/v1/auth/session", "")
	req.AddCookie(cookie)
	return serveAuth(s, req)
}

func authCookie(t *testing.T, rr *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range rr.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie
		}
	}
	t.Fatalf("no %s cookie in %q", sessionCookieName, rr.Header().Get("Set-Cookie"))
	return nil
}

// loginCSRF reads the csrf value of a successful login response.
func loginCSRF(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("login body %q: %v", rr.Body.String(), err)
	}
	if body.CSRF == "" {
		t.Fatal("login body has no csrf value")
	}
	return body.CSRF
}

// loginAndSession logs in with the legacy token and returns the cookie and csrf.
func loginAndSession(t *testing.T, s *Server, token string) (*http.Cookie, string) {
	t.Helper()
	rr := doAuthLogin(t, s, token)
	csrf := loginCSRF(t, rr)
	return authCookie(t, rr), csrf
}

func assertAuthError(t *testing.T, rr *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d; body = %q", rr.Code, status, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("error body %q: %v", rr.Body.String(), err)
	}
	if env.Error.Code != code {
		t.Fatalf("error code = %q, want %q; body = %q", env.Error.Code, code, rr.Body.String())
	}
	if env.Error.Message == "" {
		t.Fatal("error message is empty")
	}
}

// codeFromLink returns the code of a sign-in link.
func codeFromLink(t *testing.T, link string) string {
	t.Helper()
	i := strings.Index(link, "code=")
	if i < 0 {
		t.Fatalf("link %q has no code", link)
	}
	return link[i+len("code="):]
}

// setPasswordViaLink creates a sign-in link, claims it, and returns the session
// cookie and csrf value.
func setPasswordViaLink(t *testing.T, s *Server, password string) (*http.Cookie, string) {
	t.Helper()
	link, err := s.SignInLink()
	if err != nil {
		t.Fatalf("SignInLink: %v", err)
	}
	body, err := json.Marshal(map[string]string{"code": codeFromLink(t, link), "password": password})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/claim", string(body)))
	csrf := loginCSRF(t, rr)
	return authCookie(t, rr), csrf
}

// fakeTelegramReset is a web.TelegramResetSender for tests.
type fakeTelegramReset struct {
	available bool
	sentTo    int
	err       error
	codes     []string
}

func (f *fakeTelegramReset) ResetAvailable() bool { return f.available }

func (f *fakeTelegramReset) SendOwnerCode(_ context.Context, code string) (int, error) {
	f.codes = append(f.codes, code)
	if f.err != nil {
		return 0, f.err
	}
	return f.sentTo, nil
}

func TestLoginTable(t *testing.T) {
	t.Run("session without cookie", func(t *testing.T) {
		s, _ := newAuthServerWithToken(t)
		rr := serveAuth(s, authRequest(http.MethodGet, "/v1/auth/session", ""))
		assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
		if got := rr.Header().Get("Set-Cookie"); got != "" {
			t.Fatalf("unexpected Set-Cookie = %q", got)
		}
	})

	t.Run("registered route without cookie", func(t *testing.T) {
		s, _ := newAuthServerWithToken(t)
		called := false
		s.Handle("GET /v1/things", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusNoContent)
		}))
		rr := serveAuth(s, authRequest(http.MethodGet, "/v1/things", ""))
		assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
		if called {
			t.Fatal("handler ran without a cookie")
		}
	})

	t.Run("correct token", func(t *testing.T) {
		s, token := newAuthServerWithToken(t)
		rr := doAuthLogin(t, s, token)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %q", rr.Code, rr.Body.String())
		}
		if csrf := loginCSRF(t, rr); csrf == "" {
			t.Fatal("login did not return csrf")
		}
		cookie := authCookie(t, rr)
		if cookie.Value == "" {
			t.Fatal("cookie value is empty")
		}
		if cookie.Path != "/" {
			t.Fatalf("cookie Path = %q", cookie.Path)
		}
		if !cookie.HttpOnly {
			t.Fatal("cookie is not HttpOnly")
		}
		if cookie.SameSite != http.SameSiteStrictMode {
			t.Fatalf("cookie SameSite = %v", cookie.SameSite)
		}
		if cookie.Secure {
			t.Fatal("cookie is Secure on a loopback http origin")
		}
		raw := rr.Header().Get("Set-Cookie")
		for _, want := range []string{"lana_session=", "Path=/", "HttpOnly", "SameSite=Strict"} {
			if !strings.Contains(raw, want) {
				t.Fatalf("Set-Cookie = %q, want %q", raw, want)
			}
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		s, _ := newAuthServerWithToken(t)
		rr := doAuthLogin(t, s, "not-the-owner-token")
		assertAuthError(t, rr, http.StatusUnauthorized, "invalid_password")
		if got := rr.Header().Get("Set-Cookie"); got != "" {
			t.Fatalf("unexpected Set-Cookie = %q", got)
		}
	})

	t.Run("empty token", func(t *testing.T) {
		s, _ := newAuthServerWithToken(t)
		rr := doAuthLogin(t, s, "")
		assertAuthError(t, rr, http.StatusUnauthorized, "invalid_password")
	})

	t.Run("no credential store fails closed", func(t *testing.T) {
		s := newAuthServer(t, "", nil)
		rr := doAuthLogin(t, s, "anything")
		assertAuthError(t, rr, http.StatusUnauthorized, "invalid_password")
		if got := rr.Header().Get("Set-Cookie"); got != "" {
			t.Fatalf("unexpected Set-Cookie = %q", got)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		s, _ := newAuthServerWithToken(t)
		rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/login", "{not json"))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	})

	t.Run("unknown body shape", func(t *testing.T) {
		s, _ := newAuthServerWithToken(t)
		rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/login", `{"other":"value"}`))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	})

	t.Run("missing body", func(t *testing.T) {
		s, _ := newAuthServerWithToken(t)
		rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/login", ""))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	})

	t.Run("both password and token", func(t *testing.T) {
		s, token := newAuthServerWithToken(t)
		body, err := json.Marshal(map[string]string{"token": token, "password": "whatever"})
		if err != nil {
			t.Fatal(err)
		}
		rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/login", string(body)))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	})
}

// TestPasswordLoginAndTokenRetired covers AC-WLG-005 and AC-WLG-013: the
// password logs in, the legacy token stops working once a password is set.
func TestPasswordLoginAndTokenRetired(t *testing.T) {
	home := t.TempDir()
	token := authTokenFor(t, home)
	s := newAuthServer(t, home, io.Discard)

	if rr := doAuthLogin(t, s, token); rr.Code != http.StatusOK {
		t.Fatalf("token login before password status = %d, body = %q", rr.Code, rr.Body.String())
	}

	setPasswordViaLink(t, s, testPassword)

	if rr := doAuthPasswordLogin(t, s, testPassword); rr.Code != http.StatusOK {
		t.Fatalf("password login status = %d, body = %q", rr.Code, rr.Body.String())
	} else {
		// AC-WLG-005: the right password sets a session cookie.
		_ = authCookie(t, rr)
	}
	if rr := doAuthPasswordLogin(t, s, "wrong password here"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, body = %q", rr.Code, rr.Body.String())
	} else {
		assertAuthError(t, rr, http.StatusUnauthorized, "invalid_password")
	}
	// The legacy token no longer works (AC-WLG-013).
	assertAuthError(t, doAuthLogin(t, s, token), http.StatusUnauthorized, "invalid_password")
}

// TestLoginLockout covers AC-WLG-006: five wrong tries lock login for five
// minutes, even for the right password.
func TestLoginLockout(t *testing.T) {
	home := t.TempDir()
	s := newAuthServer(t, home, io.Discard)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	setPasswordViaLink(t, s, testPassword)

	for i := 0; i < loginMaxFails; i++ {
		rr := doAuthPasswordLogin(t, s, "wrong")
		assertAuthError(t, rr, http.StatusUnauthorized, "invalid_password")
	}
	rr := doAuthPasswordLogin(t, s, testPassword)
	assertAuthError(t, rr, http.StatusTooManyRequests, "too_many_attempts")

	now = now.Add(loginLockDuration + time.Second)
	if rr := doAuthPasswordLogin(t, s, testPassword); rr.Code != http.StatusOK {
		t.Fatalf("login after lockout status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestClaimCoversACWLG003 covers AC-WLG-003: a valid code sets an argon2id
// password, signs in, and is rejected afterwards.
func TestClaimCoversACWLG003(t *testing.T) {
	home := t.TempDir()
	s := newAuthServer(t, home, io.Discard)

	link, err := s.SignInLink()
	if err != nil {
		t.Fatal(err)
	}
	code := codeFromLink(t, link)
	body, err := json.Marshal(map[string]string{"code": code, "password": testPassword})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/claim", string(body)))
	if csrf := loginCSRF(t, rr); csrf == "" {
		t.Fatal("claim did not return csrf")
	}
	_ = authCookie(t, rr)

	stored := readCredentialFile(t, home)
	if stored.Version != credentialVersionV2 {
		t.Fatalf("version = %d, want 2", stored.Version)
	}
	if !strings.HasPrefix(stored.PasswordHash, "$argon2id$") {
		t.Fatalf("password_hash = %q, want an argon2id hash", stored.PasswordHash)
	}
	if stored.PasswordSetAt == 0 {
		t.Fatal("password_set_at is not set")
	}
	if stored.TokenSHA256 != "" {
		t.Fatalf("token_sha256 = %q, want empty after a password is set", stored.TokenSHA256)
	}

	// The same code is single use (AC-WLG-003).
	rr = serveAuth(s, authRequest(http.MethodPost, "/v1/auth/claim", string(body)))
	assertAuthError(t, rr, http.StatusGone, "link_expired")
}

// TestClaimExpiredCode covers AC-WLG-004.
func TestClaimExpiredCode(t *testing.T) {
	home := t.TempDir()
	s := newAuthServer(t, home, io.Discard)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	link, err := s.SignInLink()
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(signInCodeTTL + time.Minute)
	body, err := json.Marshal(map[string]string{"code": codeFromLink(t, link), "password": testPassword})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/claim", string(body)))
	assertAuthError(t, rr, http.StatusGone, "link_expired")
}

// TestClaimShortPassword covers the 400 invalid_request of API-WLG-002.
func TestClaimShortPassword(t *testing.T) {
	home := t.TempDir()
	s := newAuthServer(t, home, io.Discard)
	link, err := s.SignInLink()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"code": codeFromLink(t, link), "password": "short"})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/claim", string(body)))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}

// TestResetTelegramCoversACWLG007 covers AC-WLG-007: a Telegram reset sends a
// code, and the code plus a new password resets it.
func TestResetTelegramCoversACWLG007(t *testing.T) {
	home := t.TempDir()
	s := newAuthServer(t, home, io.Discard)
	fake := &fakeTelegramReset{available: true, sentTo: 1}
	s.SetTelegramResetSender(fake)

	rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/reset/telegram", "{}"))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("reset telegram status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var sent struct {
		SentTo int `json:"sent_to"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.SentTo != 1 {
		t.Fatalf("sent_to = %d, want 1", sent.SentTo)
	}
	if len(fake.codes) != 1 {
		t.Fatalf("fake sender got %d codes, want 1", len(fake.codes))
	}

	body, err := json.Marshal(map[string]string{"code": fake.codes[0], "password": testPassword})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, authRequest(http.MethodPost, "/v1/auth/reset/verify", string(body)))
	if csrf := loginCSRF(t, rr); csrf == "" {
		t.Fatal("reset verify did not return csrf")
	}
	_ = authCookie(t, rr)
	if rr := doAuthPasswordLogin(t, s, testPassword); rr.Code != http.StatusOK {
		t.Fatalf("login after reset status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestResetWrongCodesCoversACWLG008 covers AC-WLG-008.
func TestResetWrongCodesCoversACWLG008(t *testing.T) {
	home := t.TempDir()
	s := newAuthServer(t, home, io.Discard)
	fake := &fakeTelegramReset{available: true, sentTo: 1}
	s.SetTelegramResetSender(fake)
	if rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/reset/telegram", "{}")); rr.Code != http.StatusAccepted {
		t.Fatalf("reset telegram status = %d", rr.Code)
	}
	code := fake.codes[0]

	body := `{"code":"000000","password":"` + testPassword + `"}`
	for i := 0; i < resetMaxTries-1; i++ {
		rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/reset/verify", body))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("try %d status = %d, want 401", i+1, rr.Code)
		}
	}
	// The last wrong try voids the code.
	rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/reset/verify", body))
	assertAuthError(t, rr, http.StatusGone, "code_expired")
	// AC-WLG-008: the page says "Ask for a new code.".
	if !strings.Contains(rr.Body.String(), codeExpiredMessage) {
		t.Fatalf("code_expired body = %q, want %q", rr.Body.String(), codeExpiredMessage)
	}

	// Even the right code is now void.
	right, err := json.Marshal(map[string]string{"code": code, "password": testPassword})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, authRequest(http.MethodPost, "/v1/auth/reset/verify", string(right)))
	assertAuthError(t, rr, http.StatusGone, "code_expired")
}

// TestResetTelegramUnavailableCoversACWLG009 covers AC-WLG-009.
func TestResetTelegramUnavailableCoversACWLG009(t *testing.T) {
	s := newAuthServer(t, t.TempDir(), io.Discard)

	// state reports telegram_reset false with no sender.
	state := decodeAuthState(t, serveAuth(s, authRequest(http.MethodGet, "/v1/auth/state", "")))
	if state.TelegramReset {
		t.Fatal("telegram_reset = true with no sender")
	}
	assertAuthError(t, serveAuth(s, authRequest(http.MethodPost, "/v1/auth/reset/telegram", "{}")), http.StatusConflict, "telegram_unavailable")

	// A sender that reports unavailable is the same 409.
	s.SetTelegramResetSender(&fakeTelegramReset{available: false})
	assertAuthError(t, serveAuth(s, authRequest(http.MethodPost, "/v1/auth/reset/telegram", "{}")), http.StatusConflict, "telegram_unavailable")
}

// TestResetTelegramRateLimit covers the one-per-minute limit of API-WLG-004.
func TestResetTelegramRateLimit(t *testing.T) {
	home := t.TempDir()
	s := newAuthServer(t, home, io.Discard)
	fake := &fakeTelegramReset{available: true, sentTo: 1}
	s.SetTelegramResetSender(fake)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	if rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/reset/telegram", "{}")); rr.Code != http.StatusAccepted {
		t.Fatalf("first send status = %d", rr.Code)
	}
	assertAuthError(t, serveAuth(s, authRequest(http.MethodPost, "/v1/auth/reset/telegram", "{}")), http.StatusTooManyRequests, "too_many_attempts")

	now = now.Add(resetSendInterval)
	if rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/reset/telegram", "{}")); rr.Code != http.StatusAccepted {
		t.Fatalf("send after a minute status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestResetRestartAndOpenCoversACWLG010 covers AC-WLG-010 and REQ-WLG-001/005.
func TestResetRestartAndOpenCoversACWLG010(t *testing.T) {
	home := t.TempDir()
	var stderr bytes.Buffer
	s := newAuthServer(t, home, &stderr)
	s.cfg.OpenBrowser = true
	setPasswordViaLink(t, s, testPassword)

	if rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/reset/restart", "{}")); rr.Code != http.StatusAccepted {
		t.Fatalf("reset restart status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if !s.credentials.ResetRequested() {
		t.Fatal("reset_requested was not set")
	}

	// A restart with reset_requested opens a sign-in link.
	var opened []string
	open := func(url string) error {
		opened = append(opened, url)
		return nil
	}
	s.OpenOnStart(open)
	if len(opened) != 1 {
		t.Fatalf("opener called %d times, want 1", len(opened))
	}
	if !strings.HasPrefix(opened[0], "http://"+authTestAddr+"/#/welcome?code=") {
		t.Fatalf("opened url = %q", opened[0])
	}

	// Setting the new password through that link clears the flag.
	body, err := json.Marshal(map[string]string{"code": codeFromLink(t, opened[0]), "password": "a brand new secret"})
	if err != nil {
		t.Fatal(err)
	}
	if rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/claim", string(body))); rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if s.credentials.ResetRequested() {
		t.Fatal("reset_requested was not cleared after the password was set")
	}
	if stored := readCredentialFile(t, home); stored.ResetRequested {
		t.Fatal("web.json still has reset_requested true")
	}
}

// TestOpenOnStartCoversACWLG001And002 covers AC-WLG-001 and AC-WLG-002.
func TestOpenOnStartCoversACWLG001And002(t *testing.T) {
	t.Run("new home opens once", func(t *testing.T) {
		home := t.TempDir()
		var stderr bytes.Buffer
		s := newAuthServer(t, home, &stderr)
		s.cfg.OpenBrowser = true

		var opened []string
		s.OpenOnStart(func(url string) error {
			opened = append(opened, url)
			return nil
		})
		if len(opened) != 1 {
			t.Fatalf("opener called %d times, want 1", len(opened))
		}
		link := opened[0]
		if !strings.HasPrefix(link, "http://"+authTestAddr+"/#/welcome?code=") {
			t.Fatalf("link = %q", link)
		}
		if len(codeFromLink(t, link)) != 43 {
			t.Fatalf("code length = %d, want 43", len(codeFromLink(t, link)))
		}
		if !strings.Contains(stderr.String(), "web: open this link to set your password: "+link) {
			t.Fatalf("stderr = %q, want the sign-in link", stderr.String())
		}
		// A new home has no token file (spec #18, "no token is created").
		if _, err := os.Stat(filepath.Join(home, credentialFileName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("web.json exists after a fresh start: %v", err)
		}
	})

	t.Run("open_browser false keeps the link on stderr", func(t *testing.T) {
		home := t.TempDir()
		var stderr bytes.Buffer
		s := newAuthServer(t, home, &stderr)
		s.cfg.OpenBrowser = false

		calls := 0
		s.OpenOnStart(func(string) error {
			calls++
			return nil
		})
		if calls != 0 {
			t.Fatalf("opener called %d times with open_browser false", calls)
		}
		if !strings.Contains(stderr.String(), "web: open this link to set your password: ") {
			t.Fatalf("stderr = %q, want the sign-in link", stderr.String())
		}
	})

	t.Run("set password does not open again", func(t *testing.T) {
		home := t.TempDir()
		s := newAuthServer(t, home, io.Discard)
		s.cfg.OpenBrowser = true
		setPasswordViaLink(t, s, testPassword)

		calls := 0
		s.OpenOnStart(func(string) error {
			calls++
			return nil
		})
		if calls != 0 {
			t.Fatalf("opener called %d times after a password was set", calls)
		}
	})
}

// TestOpenOnStartTokenOnlyCoversACWLG013 covers the first half of AC-WLG-013: a
// token-only web.json has no password, so the daemon opens a sign-in link.
func TestOpenOnStartTokenOnlyCoversACWLG013(t *testing.T) {
	home := t.TempDir()
	authTokenFor(t, home)
	var stderr bytes.Buffer
	s := newAuthServer(t, home, &stderr)
	s.cfg.OpenBrowser = true

	var opened []string
	s.OpenOnStart(func(url string) error {
		opened = append(opened, url)
		return nil
	})
	if len(opened) != 1 {
		t.Fatalf("opener called %d times, want 1", len(opened))
	}
	if !strings.HasPrefix(opened[0], "http://"+authTestAddr+"/#/welcome?code=") {
		t.Fatalf("link = %q, want a sign-in link", opened[0])
	}
}

// TestChangePasswordEndsOtherSessions covers AC-WLG-012.
func TestChangePasswordEndsOtherSessions(t *testing.T) {
	home := t.TempDir()
	s := newAuthServer(t, home, io.Discard)
	cookieA, _ := setPasswordViaLink(t, s, testPassword)
	rr := doAuthPasswordLogin(t, s, testPassword)
	csrfB := loginCSRF(t, rr)
	cookieB := authCookie(t, rr)

	body, err := json.Marshal(map[string]string{"current": testPassword, "password": "a different secret"})
	if err != nil {
		t.Fatal(err)
	}
	change := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/auth/password", string(body), cookieB, csrfB))
	if change.Code != http.StatusOK {
		t.Fatalf("change password status = %d, body = %q", change.Code, change.Body.String())
	}

	assertAuthError(t, getAuthSession(s, cookieA), http.StatusUnauthorized, "unauthenticated")
	if rr := getAuthSession(s, cookieB); rr.Code != http.StatusOK {
		t.Fatalf("current session status = %d, body = %q", rr.Code, rr.Body.String())
	}
	// The new password works; the old one does not.
	if rr := doAuthPasswordLogin(t, s, "a different secret"); rr.Code != http.StatusOK {
		t.Fatalf("new password login status = %d", rr.Code)
	}
	assertAuthError(t, doAuthPasswordLogin(t, s, testPassword), http.StatusUnauthorized, "invalid_password")
}

// TestChangePasswordGuards covers the 400 and 401 of API-WLG-007.
func TestChangePasswordGuards(t *testing.T) {
	home := t.TempDir()
	s := newAuthServer(t, home, io.Discard)
	cookie, csrf := setPasswordViaLink(t, s, testPassword)

	short := `{"current":"` + testPassword + `","password":"short"}`
	assertAuthError(t, serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/auth/password", short, cookie, csrf)), http.StatusBadRequest, "invalid_request")

	wrong := `{"current":"nope","password":"a valid secret"}`
	assertAuthError(t, serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/auth/password", wrong, cookie, csrf)), http.StatusUnauthorized, "invalid_password")

	// No CSRF header is refused.
	assertAuthError(t, serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/auth/password", wrong, cookie, "")), http.StatusForbidden, "csrf_invalid")
}

// TestAuthState covers GET /v1/auth/state (API-WLG-001).
func TestAuthState(t *testing.T) {
	home := t.TempDir()
	s := newAuthServer(t, home, io.Discard)
	fake := &fakeTelegramReset{available: true}
	s.SetTelegramResetSender(fake)

	state := decodeAuthState(t, serveAuth(s, authRequest(http.MethodGet, "/v1/auth/state", "")))
	if state.PasswordSet || !state.TelegramReset || state.LockedUntil != nil {
		t.Fatalf("state = %+v", state)
	}

	setPasswordViaLink(t, s, testPassword)
	state = decodeAuthState(t, serveAuth(s, authRequest(http.MethodGet, "/v1/auth/state", "")))
	if !state.PasswordSet {
		t.Fatal("password_set = false after a password was set")
	}

	// Five wrong tries lock login and state reports locked_until.
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	for i := 0; i < loginMaxFails; i++ {
		doAuthPasswordLogin(t, s, "wrong")
	}
	state = decodeAuthState(t, serveAuth(s, authRequest(http.MethodGet, "/v1/auth/state", "")))
	if state.LockedUntil == nil || *state.LockedUntil != now.Add(loginLockDuration).UnixMilli() {
		t.Fatalf("locked_until = %v", state.LockedUntil)
	}
}

func decodeAuthState(t *testing.T, rr *httptest.ResponseRecorder) struct {
	PasswordSet   bool   `json:"password_set"`
	TelegramReset bool   `json:"telegram_reset"`
	LockedUntil   *int64 `json:"locked_until"`
} {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("state status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var state struct {
		PasswordSet   bool   `json:"password_set"`
		TelegramReset bool   `json:"telegram_reset"`
		LockedUntil   *int64 `json:"locked_until"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &state); err != nil {
		t.Fatalf("state body %q: %v", rr.Body.String(), err)
	}
	return state
}

func TestOriginHostCSRF(t *testing.T) {
	t.Run("login bad origin", func(t *testing.T) {
		s, _ := newAuthServerWithToken(t)
		req := authRequest(http.MethodPost, "/v1/auth/login", `{"token":"irrelevant"}`)
		req.Header.Set("Origin", "https://evil.example")
		rr := serveAuth(s, req)
		assertAuthError(t, rr, http.StatusForbidden, "forbidden_origin")
		if got := rr.Header().Get("Set-Cookie"); got != "" {
			t.Fatalf("unexpected Set-Cookie = %q", got)
		}
	})

	t.Run("logout without csrf keeps session", func(t *testing.T) {
		s, token := newAuthServerWithToken(t)
		cookie, _ := loginAndSession(t, s, token)
		rr := doAuthLogout(s, cookie, authTestOrigin, "")
		assertAuthError(t, rr, http.StatusForbidden, "csrf_invalid")
		if again := getAuthSession(s, cookie); again.Code != http.StatusOK {
			t.Fatalf("session after rejected logout = %d, body = %q", again.Code, again.Body.String())
		}
	})

	t.Run("logout with wrong csrf", func(t *testing.T) {
		s, token := newAuthServerWithToken(t)
		cookie, _ := loginAndSession(t, s, token)
		rr := doAuthLogout(s, cookie, authTestOrigin, "not-the-csrf")
		assertAuthError(t, rr, http.StatusForbidden, "csrf_invalid")
		if again := getAuthSession(s, cookie); again.Code != http.StatusOK {
			t.Fatalf("session after rejected logout = %d", again.Code)
		}
	})

	t.Run("host mismatch", func(t *testing.T) {
		s, token := newAuthServerWithToken(t)
		body, err := json.Marshal(map[string]string{"token": token})
		if err != nil {
			t.Fatal(err)
		}
		req := authRequest(http.MethodPost, "/v1/auth/login", string(body))
		req.Host = "evil.example"
		assertAuthError(t, serveAuth(s, req), http.StatusForbidden, "forbidden_host")

		cookie, _ := loginAndSession(t, s, token)
		req = authRequest(http.MethodGet, "/v1/auth/session", "")
		req.Host = "evil.example"
		req.AddCookie(cookie)
		assertAuthError(t, serveAuth(s, req), http.StatusForbidden, "forbidden_host")
	})

	t.Run("matching origin allowed", func(t *testing.T) {
		s, token := newAuthServerWithToken(t)
		body, err := json.Marshal(map[string]string{"token": token})
		if err != nil {
			t.Fatal(err)
		}
		req := authRequest(http.MethodPost, "/v1/auth/login", string(body))
		req.Header.Set("Origin", authTestOrigin)
		rr := serveAuth(s, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
		}
	})

	t.Run("session without origin allowed", func(t *testing.T) {
		s, token := newAuthServerWithToken(t)
		cookie, _ := loginAndSession(t, s, token)
		req := authRequest(http.MethodGet, "/v1/auth/session", "")
		req.AddCookie(cookie)
		if rr := serveAuth(s, req); rr.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
		}
	})

	t.Run("logout clears cookie", func(t *testing.T) {
		s, token := newAuthServerWithToken(t)
		cookie, csrf := loginAndSession(t, s, token)
		rr := doAuthLogout(s, cookie, authTestOrigin, csrf)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
		}
		raw := rr.Header().Get("Set-Cookie")
		for _, want := range []string{"lana_session=;", "Path=/", "Max-Age=0", "HttpOnly", "SameSite=Strict"} {
			if !strings.Contains(raw, want) {
				t.Fatalf("Set-Cookie = %q, want %q", raw, want)
			}
		}
		assertAuthError(t, getAuthSession(s, cookie), http.StatusUnauthorized, "unauthenticated")
	})
}

func TestIdleExpiry(t *testing.T) {
	s, token := newAuthServerWithToken(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	cookie, _ := loginAndSession(t, s, token)

	now = now.Add(11*time.Hour + 59*time.Minute)
	rr := getAuthSession(s, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("session before expiry status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body struct {
		ExpiresAt int64 `json:"expires_at"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("session body %q: %v", rr.Body.String(), err)
	}
	want := now.Add(sessionIdleTTL).UnixMilli()
	if body.ExpiresAt != want {
		t.Fatalf("expires_at = %d, want %d", body.ExpiresAt, want)
	}

	now = now.Add(12 * time.Hour)
	assertAuthError(t, getAuthSession(s, cookie), http.StatusUnauthorized, "unauthenticated")
}

// readCredentialFile reads and decodes web.json for assertions.
func readCredentialFile(t *testing.T, home string) credentialFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, credentialFileName))
	if err != nil {
		t.Fatal(err)
	}
	var stored credentialFile
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("web.json %q: %v", data, err)
	}
	return stored
}

// TestCredentialFileModeAndRead covers the v1 read and the 0600 mode.
func TestCredentialFileModeAndRead(t *testing.T) {
	home := t.TempDir()
	token := authTokenFor(t, home)

	path := filepath.Join(home, credentialFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != credentialFileMode {
		t.Fatalf("mode = %o, want %o", got, credentialFileMode)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// A read does not rewrite the v1 file, and the token still logs in.
	s := newAuthServer(t, home, io.Discard)
	if rr := doAuthLogin(t, s, token); rr.Code != http.StatusOK {
		t.Fatalf("token login after reload status = %d, body = %q", rr.Code, rr.Body.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("v1 file changed on read: %q -> %q", before, after)
	}
}

func TestCredentialFileInvalid(t *testing.T) {
	const goodHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tests := []struct {
		name string
		body string
	}{
		{name: "garbage", body: "not json"},
		{name: "wrong version", body: `{"version":3,"token_sha256":"` + goodHash + `"}`},
		{name: "short hash", body: `{"version":1,"token_sha256":"abc"}`},
		{name: "bad password hash", body: `{"version":2,"password_hash":"not-a-hash"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, credentialFileName), []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := New(Config{Addr: authTestAddr, Enabled: true, Home: home})
			if err == nil {
				t.Fatal("New succeeded with an invalid credential file")
			}
			text := err.Error()
			if !strings.HasPrefix(text, "web: credential file") {
				t.Fatalf("error = %q, want a web: credential file prefix", text)
			}
			if !strings.Contains(text, credentialFileName) {
				t.Fatalf("error = %q, want the file name", text)
			}
		})
	}
}

func TestAddrValidatedBeforeCredential(t *testing.T) {
	home := t.TempDir()
	_, err := New(Config{Addr: "0.0.0.0:18300", Enabled: true, Home: home})
	if !errors.Is(err, ErrNotLoopback) {
		t.Fatalf("New error = %v, want ErrNotLoopback", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, credentialFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("%s exists after an address error: %v", credentialFileName, statErr)
	}
}

// TestSecretsNeverLeak checks that no password, token, or hash reaches a
// response, a log line, or the stderr stream.
func TestSecretsNeverLeak(t *testing.T) {
	var logBuf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	home := t.TempDir()
	var stderr bytes.Buffer
	s := newAuthServer(t, home, &stderr)
	s.cfg.OpenBrowser = true

	var responses []*httptest.ResponseRecorder
	opened := ""
	s.OpenOnStart(func(url string) error {
		opened = url
		return nil
	})
	code := codeFromLink(t, opened)
	body, err := json.Marshal(map[string]string{"code": code, "password": testPassword})
	if err != nil {
		t.Fatal(err)
	}
	claim := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/claim", string(body)))
	responses = append(responses, claim)
	if claim.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body = %q", claim.Code, claim.Body.String())
	}
	cookie := authCookie(t, claim)

	failed := doAuthPasswordLogin(t, s, "wrong password")
	responses = append(responses, failed)

	stored := readCredentialFile(t, home)
	secrets := []string{testPassword, code, stored.PasswordHash}
	if stored.TokenSHA256 != "" {
		secrets = append(secrets, stored.TokenSHA256)
	}

	for i, rr := range responses {
		for _, secret := range secrets {
			if secret == "" {
				continue
			}
			if strings.Contains(rr.Body.String(), secret) {
				t.Fatalf("response %d leaks a secret: %q", i, rr.Body.String())
			}
			for name, values := range rr.Header() {
				for _, value := range values {
					if strings.Contains(value, secret) {
						t.Fatalf("response %d header %s leaks a secret: %q", i, name, value)
					}
				}
			}
		}
	}
	if strings.Contains(logBuf.String(), testPassword) || strings.Contains(logBuf.String(), stored.PasswordHash) {
		t.Fatalf("slog output leaks a secret: %q", logBuf.String())
	}
	if strings.Count(stderr.String(), testPassword) != 0 {
		t.Fatalf("stderr leaks the password: %q", stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(home, credentialFileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(testPassword)) {
		t.Fatalf("%s leaks the plain password: %q", credentialFileName, data)
	}
	_ = cookie
}
