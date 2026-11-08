package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// TestLocalSignInLinkNext covers spec #41: the optional next query parameter
// is added to the link only for a safe in-app hash, and the code still works.
func TestLocalSignInLinkNext(t *testing.T) {
	s := newAuthServer(t, t.TempDir(), io.Discard)
	s.SetLocalSecret(localTestSecret)

	cases := []struct {
		name string
		next string
		want string // the next value in the link, "" when it must be absent
	}{
		{"absent", "", ""},
		{"calendar", "#/calendar", "%23%2Fcalendar"},
		{"chats with query", "#/chats?agent=lana", "%23%2Fchats%3Fagent%3Dlana"},
		{"no hash", "/chats", ""},
		{"absolute url", "https://evil.example/#/chats", ""},
		{"javascript", "javascript:alert(1)", ""},
		{"protocol relative", "#//evil.example", ""},
		{"bare hash", "#/", ""},
		{"welcome loop", "#/welcome?code=x", ""},
		{"login", "#/login", ""},
		{"space", "#/chats x", ""},
		{"quote", "#/chats\"", ""},
		{"too long", "#/" + strings.Repeat("a", 300), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := localTestRequest(http.MethodPost, localTestSecret, localTestAddr)
			req.URL.RawQuery = url.Values{"next": {tc.next}}.Encode()
			rr := serveAuth(s, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
			}
			var payload struct {
				Link string `json:"link"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(payload.Link, "/#/welcome?code=") {
				t.Fatalf("link = %q, want a sign-in link", payload.Link)
			}
			has := strings.Contains(payload.Link, "&next=")
			if tc.want == "" && has {
				t.Fatalf("link = %q, want no next", payload.Link)
			}
			if tc.want != "" && !strings.HasSuffix(payload.Link, "&next="+tc.want) {
				t.Fatalf("link = %q, want next=%s", payload.Link, tc.want)
			}
		})
	}

	// The code of a link with next still claims once.
	req := localTestRequest(http.MethodPost, localTestSecret, localTestAddr)
	req.URL.RawQuery = "next=%23%2Fworkflows"
	rr := serveAuth(s, req)
	var payload struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"code": codeFromLink(t, payload.Link), "password": testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if rr := serveAuth(s, authRequest(http.MethodPost, "/v1/auth/claim", string(body))); rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestSafeNextHash covers the accept rule shared with the dashboard.
func TestSafeNextHash(t *testing.T) {
	for _, ok := range []string{"#/chats", "#/workflows", "#/calendar", "#/router", "#/home", "#/sessions/abc-123", "#/settings/general"} {
		if got, accepted := SafeNextHash(ok); !accepted || got != ok {
			t.Errorf("SafeNextHash(%q) = %q, %v; want accepted", ok, got, accepted)
		}
	}
	for _, bad := range []string{"", "#", "#/", "chats", "/#/chats", "#chats", "#/forgot", "#/welcome", "#/login?x=1", "#/a<b", "#/a\nb", "http://x/#/chats"} {
		if _, accepted := SafeNextHash(bad); accepted {
			t.Errorf("SafeNextHash(%q) accepted; want rejected", bad)
		}
	}
}
