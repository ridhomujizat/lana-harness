package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	// localTestSecret is the configured daemon secret for the local tests.
	localTestSecret = "local-control-secret-value"
	// localTestAddr is a loopback RemoteAddr.
	localTestAddr = "127.0.0.1:53123"
	// localTestOutside is a non-loopback RemoteAddr.
	localTestOutside = "10.0.0.5:53123"
)

// localTestRequest builds a request for the local control path. httptest sets a
// non-loopback RemoteAddr by default, so every call names one.
func localTestRequest(method, secret, remoteAddr string) *http.Request {
	req := httptest.NewRequest(method, LocalSignInPath, nil)
	req.Host = authTestAddr
	req.RemoteAddr = remoteAddr
	if secret != "" {
		req.Header.Set(LocalSecretHeader, secret)
	}
	return req
}

// TestLocalSignInLinkCoversACWLG011 covers AC-WLG-011: the local endpoint
// returns a sign-in link that works once.
func TestLocalSignInLinkCoversACWLG011(t *testing.T) {
	home := t.TempDir()
	s := newAuthServer(t, home, io.Discard)
	s.SetLocalSecret(localTestSecret)

	rr := serveAuth(s, localTestRequest(http.MethodPost, localTestSecret, localTestAddr))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var payload struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !strings.Contains(payload.Link, "/#/welcome?code=") {
		t.Fatalf("link = %q, want a sign-in link", payload.Link)
	}

	// The code works once: the first claim succeeds, the second is gone.
	body, err := json.Marshal(map[string]string{"code": codeFromLink(t, payload.Link), "password": testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/claim", string(body))); rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body = %q", rr.Code, rr.Body.String())
	}
	assertAuthError(t, serveAuth(s, authRequest(http.MethodPost, "/v1/auth/claim", string(body))), http.StatusGone, "link_expired")
}

// TestLocalSignInLinkRequiresSecret covers the 401 cases: a missing or wrong
// header, and an empty configured secret.
func TestLocalSignInLinkRequiresSecret(t *testing.T) {
	s := newAuthServer(t, t.TempDir(), io.Discard)
	s.SetLocalSecret(localTestSecret)

	t.Run("missing header", func(t *testing.T) {
		assertAuthError(t, serveAuth(s, localTestRequest(http.MethodPost, "", localTestAddr)), http.StatusUnauthorized, "unauthorized")
	})
	t.Run("wrong header", func(t *testing.T) {
		assertAuthError(t, serveAuth(s, localTestRequest(http.MethodPost, "not-the-secret", localTestAddr)), http.StatusUnauthorized, "unauthorized")
	})
	t.Run("not reachable without the header", func(t *testing.T) {
		// A request with no header never reaches the handler.
		if rr := serveAuth(s, localTestRequest(http.MethodPost, "", localTestAddr)); rr.Code == http.StatusOK {
			t.Fatal("the endpoint answered 200 without the header")
		}
	})
	t.Run("empty configured secret rejects every request", func(t *testing.T) {
		empty := newAuthServer(t, t.TempDir(), io.Discard)
		empty.SetLocalSecret("")
		assertAuthError(t, serveAuth(empty, localTestRequest(http.MethodPost, "", localTestAddr)), http.StatusUnauthorized, "unauthorized")
		assertAuthError(t, serveAuth(empty, localTestRequest(http.MethodPost, "anything", localTestAddr)), http.StatusUnauthorized, "unauthorized")
	})
}

// TestLocalSignInLinkMethodNotAllowed covers the 405 the mux answers for a
// method other than POST.
func TestLocalSignInLinkMethodNotAllowed(t *testing.T) {
	s := newAuthServer(t, t.TempDir(), io.Discard)
	s.SetLocalSecret(localTestSecret)

	rr := serveAuth(s, localTestRequest(http.MethodGet, localTestSecret, localTestAddr))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rr.Code)
	}
}

// TestLocalSignInLinkNonLoopback covers the 403 for a non-loopback caller with
// the correct secret.
func TestLocalSignInLinkNonLoopback(t *testing.T) {
	s := newAuthServer(t, t.TempDir(), io.Discard)
	s.SetLocalSecret(localTestSecret)

	rr := serveAuth(s, localTestRequest(http.MethodPost, localTestSecret, localTestOutside))
	assertAuthError(t, rr, http.StatusForbidden, "forbidden_remote")
}
