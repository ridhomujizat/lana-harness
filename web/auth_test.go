package web

import (
	"bytes"
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

func newAuthServerWithToken(t *testing.T) (*Server, string) {
	t.Helper()
	var stderr bytes.Buffer
	s := newAuthServer(t, t.TempDir(), &stderr)
	return s, tokenFromStderr(t, stderr.String())
}

func tokenFromStderr(t *testing.T, out string) string {
	t.Helper()
	const prefix = "web: owner token created at "
	const marker = "; copy it now, it is shown only once: "
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		i := strings.Index(line, marker)
		if i < 0 {
			t.Fatalf("token creation line has no marker: %q", line)
		}
		return line[i+len(marker):]
	}
	t.Fatalf("no token creation line in stderr: %q", out)
	return ""
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

// loginAndSession logs in and returns the browser cookie and its CSRF value.
func loginAndSession(t *testing.T, s *Server, token string) (*http.Cookie, string) {
	t.Helper()
	rr := doAuthLogin(t, s, token)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, body = %q", rr.Code, rr.Body.String())
	}
	cookie := authCookie(t, rr)
	sessRR := getAuthSession(s, cookie)
	if sessRR.Code != http.StatusOK {
		t.Fatalf("session status = %d, body = %q", sessRR.Code, sessRR.Body.String())
	}
	var body struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(sessRR.Body.Bytes(), &body); err != nil {
		t.Fatalf("session body %q: %v", sessRR.Body.String(), err)
	}
	if body.CSRF == "" {
		t.Fatal("session body has no csrf value")
	}
	return cookie, body.CSRF
}

func assertAuthError(t *testing.T, rr *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d; body = %q", rr.Code, status, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
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
		if rr.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body = %q", rr.Code, rr.Body.String())
		}
		if rr.Body.Len() != 0 {
			t.Fatalf("body = %q, want empty", rr.Body.String())
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
		if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
			t.Fatalf("cookie has MaxAge=%d Expires=%v, want a session cookie", cookie.MaxAge, cookie.Expires)
		}
		raw := rr.Header().Get("Set-Cookie")
		for _, want := range []string{"lana_session=", "Path=/", "HttpOnly", "SameSite=Strict"} {
			if !strings.Contains(raw, want) {
				t.Fatalf("Set-Cookie = %q, want %q", raw, want)
			}
		}
		if strings.Contains(raw, "Max-Age") {
			t.Fatalf("Set-Cookie = %q, want no Max-Age", raw)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		s, _ := newAuthServerWithToken(t)
		rr := doAuthLogin(t, s, "not-the-owner-token")
		assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
		if got := rr.Header().Get("Set-Cookie"); got != "" {
			t.Fatalf("unexpected Set-Cookie = %q", got)
		}
	})

	t.Run("empty token", func(t *testing.T) {
		s, _ := newAuthServerWithToken(t)
		rr := doAuthLogin(t, s, "")
		assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
		if got := rr.Header().Get("Set-Cookie"); got != "" {
			t.Fatalf("unexpected Set-Cookie = %q", got)
		}
	})

	t.Run("no token store fails closed", func(t *testing.T) {
		s := newAuthServer(t, "", nil)
		rr := doAuthLogin(t, s, "anything")
		assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
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
}

func TestTokenNeverLeaks(t *testing.T) {
	var logBuf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	home := t.TempDir()
	var stderr bytes.Buffer
	s := newAuthServer(t, home, &stderr)
	token := tokenFromStderr(t, stderr.String())

	var responses []*httptest.ResponseRecorder
	login := doAuthLogin(t, s, token)
	responses = append(responses, login)
	if login.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, body = %q", login.Code, login.Body.String())
	}
	cookie := authCookie(t, login)

	failed := doAuthLogin(t, s, "wrong-token")
	responses = append(responses, failed)
	if failed.Code != http.StatusUnauthorized {
		t.Fatalf("failed login status = %d", failed.Code)
	}

	sessRR := getAuthSession(s, cookie)
	responses = append(responses, sessRR)
	var sessBody struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(sessRR.Body.Bytes(), &sessBody); err != nil {
		t.Fatalf("session body %q: %v", sessRR.Body.String(), err)
	}

	logout := doAuthLogout(s, cookie, authTestOrigin, sessBody.CSRF)
	responses = append(responses, logout)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, body = %q", logout.Code, logout.Body.String())
	}

	for i, rr := range responses {
		if body := rr.Body.String(); strings.Contains(body, token) {
			t.Fatalf("response %d body leaks the owner token: %q", i, body)
		}
		for name, values := range rr.Header() {
			for _, value := range values {
				if strings.Contains(value, token) {
					t.Fatalf("response %d header %s leaks the owner token: %q", i, name, value)
				}
			}
		}
	}

	if got := strings.Count(stderr.String(), token); got != 1 {
		t.Fatalf("token appears %d times in stderr, want 1: %q", got, stderr.String())
	}
	if strings.Contains(logBuf.String(), token) {
		t.Fatalf("slog output leaks the owner token: %q", logBuf.String())
	}
	data, err := os.ReadFile(filepath.Join(home, tokenFileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(token)) {
		t.Fatalf("%s leaks the plain owner token: %q", tokenFileName, data)
	}
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
		if rr.Code != http.StatusNoContent {
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

func TestTokenFileModeAndHash(t *testing.T) {
	home := t.TempDir()
	var stderr bytes.Buffer
	newAuthServer(t, home, &stderr)
	token := tokenFromStderr(t, stderr.String())

	path := filepath.Join(home, tokenFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}

	firstData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Version     int    `json:"version"`
		TokenSHA256 string `json:"token_sha256"`
	}
	if err := json.Unmarshal(firstData, &stored); err != nil {
		t.Fatalf("token file %q: %v", firstData, err)
	}
	if stored.Version != 1 {
		t.Fatalf("version = %d, want 1", stored.Version)
	}
	sum := sha256.Sum256([]byte(token))
	if want := hex.EncodeToString(sum[:]); stored.TokenSHA256 != want {
		t.Fatalf("token_sha256 = %q, want %q", stored.TokenSHA256, want)
	}

	var secondStderr bytes.Buffer
	second := newAuthServer(t, home, &secondStderr)
	if secondStderr.Len() != 0 {
		t.Fatalf("second New printed a creation line: %q", secondStderr.String())
	}
	secondData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstData, secondData) {
		t.Fatalf("token file changed between starts:\nfirst  %q\nsecond %q", firstData, secondData)
	}
	if rr := doAuthLogin(t, second, token); rr.Code != http.StatusNoContent {
		t.Fatalf("login after reload status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestTokenFileInvalid(t *testing.T) {
	const goodHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tests := []struct {
		name string
		body string
	}{
		{name: "garbage", body: "not json"},
		{name: "wrong version", body: `{"version":2,"token_sha256":"` + goodHash + `"}`},
		{name: "short hash", body: `{"version":1,"token_sha256":"abc"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, tokenFileName), []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			_, err := New(Config{Addr: authTestAddr, Enabled: true, Home: home, Stderr: &stderr})
			if err == nil {
				t.Fatal("New succeeded with an invalid token file")
			}
			text := err.Error()
			if !strings.HasPrefix(text, "web: token file") {
				t.Fatalf("error = %q, want a web: token file prefix", text)
			}
			if !strings.Contains(text, tokenFileName) {
				t.Fatalf("error = %q, want the file name", text)
			}
			if !strings.Contains(text, "delete it") {
				t.Fatalf("error = %q, want the fix wording", text)
			}
			if stderr.Len() != 0 {
				t.Fatalf("invalid token file printed a creation line: %q", stderr.String())
			}
		})
	}
}

func TestAddrValidatedBeforeToken(t *testing.T) {
	home := t.TempDir()
	_, err := New(Config{Addr: "0.0.0.0:18300", Enabled: true, Home: home})
	if !errors.Is(err, ErrNotLoopback) {
		t.Fatalf("New error = %v, want ErrNotLoopback", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, tokenFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("%s exists after an address error: %v", tokenFileName, statErr)
	}
}
