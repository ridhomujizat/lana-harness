package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

func TestWebServerSecurityHeaders(t *testing.T) {
	s, err := New(Config{Addr: "127.0.0.1:0", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != "default-src 'self'; frame-ancestors 'none'" {
		t.Fatalf("CSP = %q", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "" {
		t.Fatalf("dashboard Cache-Control = %q", got)
	}

	asset, err := http.Get(ts.URL + "/assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = asset.Body.Close() }()
	if asset.StatusCode != http.StatusOK {
		t.Fatalf("GET /assets/app.css status = %d", asset.StatusCode)
	}
	if got := asset.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("asset Content-Type = %q", got)
	}
	if got := asset.Header.Get("Content-Security-Policy"); got == "" {
		t.Fatal("asset response has no CSP")
	}

	api, err := http.Get(ts.URL + "/v1/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = api.Body.Close() }()
	if api.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /v1/missing status = %d", api.StatusCode)
	}
	if got := api.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("API Cache-Control = %q", got)
	}
	if got := api.Header.Get("Content-Security-Policy"); got == "" {
		t.Fatal("API response has no CSP")
	}
}

func TestWebServerAssets(t *testing.T) {
	s, err := New(Config{Addr: "127.0.0.1:0", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readBody(t, resp)
	for _, want := range []string{"<title>lana</title>", `href="/assets/app.css"`, `src="/assets/app.js"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("index does not contain %q:\n%s", want, body)
		}
	}

	for _, path := range []string{"/assets/app.css", "/assets/app.js"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body := readBody(t, resp)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || body == "" {
			t.Fatalf("GET %s status = %d, body = %q", path, resp.StatusCode, body)
		}
	}

	for _, path := range []string{"/assets/missing.js", "/assets/"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s status = %d", path, resp.StatusCode)
		}
	}
}

func TestWebAssetsSelfContained(t *testing.T) {
	external := regexp.MustCompile(`(?i)https?://|//`)
	inlineScript := regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>(.+?)</script>`)

	err := fs.WalkDir(assetsFS, "assets", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		// Vendored upstream files keep their own attribution and license
		// URLs. The test below only scans the assets this repository owns.
		if strings.HasPrefix(path, "assets/vendor/") {
			return nil
		}
		data, err := fs.ReadFile(assetsFS, path)
		if err != nil {
			return err
		}
		text := string(data)
		if loc := external.FindString(text); loc != "" {
			t.Fatalf("%s contains an external or protocol-relative URL: %q", path, loc)
		}
		if strings.HasSuffix(path, ".html") {
			for _, match := range inlineScript.FindAllStringSubmatch(text, -1) {
				if strings.TrimSpace(match[1]) != "" {
					t.Fatalf("%s contains an inline script", path)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWebNewLoopbackValidation(t *testing.T) {
	tests := []struct {
		addr    string
		invalid bool
		wantErr error
	}{
		{addr: "127.0.0.1:18300"},
		{addr: "127.42.0.9:18300"},
		{addr: "[::1]:18300"},
		{addr: "::1:18300"},
		{addr: "localhost:18300"},
		{addr: "LOCALHOST:18300"},
		{addr: "0.0.0.0:18300", wantErr: ErrNotLoopback},
		{addr: "192.168.1.2:18300", wantErr: ErrNotLoopback},
		{addr: "example.com:18300", wantErr: ErrNotLoopback},
		{addr: "127.0.0.1", invalid: true},
		{addr: "127.0.0.1:notaport", invalid: true},
		{addr: "127.0.0.1:70000", invalid: true},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			_, err := New(Config{Addr: tt.addr, Enabled: true})
			if tt.invalid {
				if err == nil {
					t.Fatalf("New(%q) succeeded, want an error", tt.addr)
				}
				return
			}
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("New(%q) error = %v", tt.addr, err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("New(%q) error = %v, want %v", tt.addr, err, tt.wantErr)
			}
		})
	}
}

func TestWebServerStartAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := New(Config{Addr: "127.0.0.1:0", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	url := "http://" + s.Addr()
	assertGET(t, url+"/", http.StatusOK)

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := (&http.Client{Timeout: 200 * time.Millisecond}).Get(url + "/")
		if err != nil {
			return
		}
		_ = resp.Body.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server still accepts requests after Shutdown")
}

func TestWebServerStopsOnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, err := New(Config{Addr: "127.0.0.1:0", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	url := "http://" + s.Addr()
	assertGET(t, url+"/", http.StatusOK)
	cancel()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := (&http.Client{Timeout: 200 * time.Millisecond}).Get(url + "/")
		if err != nil {
			return
		}
		_ = resp.Body.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server still accepts requests after context cancellation")
}

func TestWebServerPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	s, err := New(Config{Addr: ln.Addr().String(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Start(context.Background())
	if err == nil {
		t.Fatal("Start succeeded on a port in use")
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("Start error = %v, want address in use", err)
	}
}

func TestWebServerDisabled(t *testing.T) {
	s, err := New(Config{Addr: "127.0.0.1:0", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.Addr(); got != "127.0.0.1:0" {
		t.Fatalf("Addr = %q", got)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func assertGET(t *testing.T, url string, status int) {
	t.Helper()
	resp, err := (&http.Client{Timeout: time.Second}).Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != status {
		t.Fatalf("GET %s status = %d, want %d", url, resp.StatusCode, status)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const webAddrConfigRaw = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "coder"

[web]
enabled = false
addr = "127.0.0.1:18300"
`

// TestWebAddrSaveCoversACWEB057 covers AC-WEB-057: a loopback address is saved
// with restart_required true and the new URL, while a non-loopback address is
// refused with 422 validation_failed naming web.addr and nothing is written.
func TestWebAddrSaveCoversACWEB057(t *testing.T) {
	store := newTestConfigStore(t, webAddrConfigRaw, 0o600, ConfigSettings{})
	s, cookie, csrf := newConfigTestServer(t, store)

	file, err := store.ConfigFile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, map[string]string{
		"addr":             "127.0.0.1:18301",
		"expected_version": file.Version,
	})
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/web", body, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT /v1/web status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var saved putWebResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode PUT /v1/web %q: %v", rr.Body.String(), err)
	}
	if saved.Addr != "127.0.0.1:18301" || saved.URL != "http://127.0.0.1:18301" {
		t.Fatalf("saved address = %+v", saved)
	}
	if !saved.RestartRequired {
		t.Fatal("restart_required is false, want true")
	}
	if saved.Version == "" {
		t.Fatal("version is empty")
	}

	written, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `addr = "127.0.0.1:18301"`) {
		t.Fatalf("config.toml has no new addr:\n%s", written)
	}
	if strings.Contains(string(written), `addr = "127.0.0.1:18300"`) {
		t.Fatalf("config.toml still has the old addr:\n%s", written)
	}
	if !strings.Contains(string(written), "enabled = false") {
		t.Fatalf("[web] enabled was not kept:\n%s", written)
	}

	// The save writes one web.save audit line with the changed key.
	auditData, err := os.ReadFile(webfiles.AuditPath(store.home))
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	if !strings.Contains(string(auditData), `"kind":"web.save"`) || !strings.Contains(string(auditData), `"web.addr"`) {
		t.Fatalf("audit line = %q", auditData)
	}

	// A non-loopback address is refused and the file stays byte for byte.
	current, err := store.ConfigFile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	beforeBad := append([]byte(nil), current.Raw...)
	for _, addr := range []string{"0.0.0.0:18300", "192.168.1.2:18300", "127.0.0.1:notaport"} {
		body = mustJSON(t, map[string]string{"addr": addr, "expected_version": current.Version})
		rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/web", body, cookie, csrf))
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("PUT /v1/web %q status = %d, body = %q", addr, rr.Code, rr.Body.String())
		}
		envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()})
		if envelope.Error.Code != "validation_failed" {
			t.Fatalf("PUT /v1/web %q code = %q", addr, envelope.Error.Code)
		}
		found := false
		for _, detail := range envelope.Error.Details {
			if detail["key"] == "web.addr" {
				found = true
			}
		}
		if !found {
			t.Fatalf("PUT /v1/web %q has no web.addr detail: %s", addr, rr.Body.String())
		}
	}
	afterBad, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterBad) != string(beforeBad) {
		t.Fatalf("invalid address changed config.toml:\n%s", afterBad)
	}

	// A stale expected_version is 409 and writes nothing.
	current, err = store.ConfigFile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	body = mustJSON(t, map[string]string{"addr": "127.0.0.1:18302", "expected_version": store.loaded})
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/web", body, cookie, csrf))
	if rr.Code != http.StatusConflict {
		t.Fatalf("stale PUT /v1/web status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if code := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}); code.Error.Code != "stale_version" {
		t.Fatalf("stale code = %q", code.Error.Code)
	}
	staleData, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(staleData) != string(current.Raw) {
		t.Fatalf("stale version changed config.toml:\n%s", staleData)
	}
}
