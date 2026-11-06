package tools

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestWebFetchHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><style>body{color:red}</style><script>alert(1)</script></head><body><h1>Title</h1><p>Body</p></body></html>`))
	}))
	defer srv.Close()
	out := callTool(t, NewWebFetch(&Guard{}), t.TempDir(), `{"url":"`+srv.URL+`"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if !strings.Contains(out.Output, "Title") || !strings.Contains(out.Output, "Body") {
		t.Fatalf("markdown=%q", out.Output)
	}
	if strings.Contains(out.Output, "alert") || strings.Contains(out.Output, "color:red") {
		t.Fatalf("script/style not removed: %q", out.Output)
	}
}

func TestWebFetchContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"a":1}`))
			return
		}
		if r.URL.Path == "/img" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png"))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain text"))
	}))
	defer srv.Close()
	for _, tc := range []struct{ path, want string }{
		{"/txt", "plain text"},
		{"/json", `{"a":1}`},
	} {
		out := callTool(t, NewWebFetch(&Guard{}), t.TempDir(), `{"url":"`+srv.URL+tc.path+`"}`)
		if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, tc.want) {
			t.Fatalf("%s out=%+v", tc.path, out)
		}
	}
	out := callTool(t, NewWebFetch(&Guard{}), t.TempDir(), `{"url":"`+srv.URL+`/img"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "unsupported content type") {
		t.Fatalf("img out=%+v", out)
	}
}

func TestWebFetchLimits(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		switch r.URL.Path {
		case "/large":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(strings.Repeat("x", webFetchLimit+1)))
		case "/error":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	out := callTool(t, NewWebFetch(&Guard{}), t.TempDir(), `{"url":"ftp://example.com/x"}`)
	if out.Status != core.ToolStatusError || hit {
		t.Fatalf("url non-http out=%+v hit=%v", out, hit)
	}
	out = callTool(t, NewWebFetch(&Guard{}), t.TempDir(), `{"url":"`+srv.URL+`/large"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "response too large") {
		t.Fatalf("large out=%+v", out)
	}
	out = callTool(t, NewWebFetch(&Guard{}), t.TempDir(), `{"url":"`+srv.URL+`/error"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "HTTP 500") {
		t.Fatalf("error out=%+v", out)
	}
}

func TestWebFetchTimeout(t *testing.T) {
	old := webFetchTimeout
	webFetchTimeout = 50 * time.Millisecond
	defer func() { webFetchTimeout = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()
	out := callTool(t, NewWebFetch(&Guard{}), t.TempDir(), `{"url":"`+srv.URL+`"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "timed out after 30 seconds") {
		t.Fatalf("out=%+v", out)
	}
}

// TestBlockedAddress checks the address guard of web_fetch: cloud metadata,
// link-local, multicast, and the unspecified address are refused; loopback and
// private ranges stay reachable.
func TestBlockedAddress(t *testing.T) {
	blocked := []string{
		"169.254.169.254", // AWS/GCP/Azure metadata
		"169.254.0.1",     // link-local unicast
		"fe80::1",         // IPv6 link-local unicast
		"224.0.0.1",       // multicast (link-local)
		"239.1.2.3",       // multicast
		"ff02::1",         // IPv6 link-local multicast
		"ff00::1",         // IPv6 multicast
		"0.0.0.0",         // unspecified
		"::",              // unspecified
		"100.100.100.200", // Alibaba metadata
	}
	for _, s := range blocked {
		if err := blockedAddress(net.ParseIP(s)); err == nil {
			t.Errorf("blockedAddress(%s) = nil, want an error", s)
		} else if !strings.Contains(err.Error(), "blocked address") {
			t.Errorf("blockedAddress(%s) error = %q", s, err)
		}
	}
	allowed := []string{
		"127.0.0.1", "::1", "10.0.0.1", "172.16.5.4", "192.168.1.1", "8.8.8.8",
	}
	for _, s := range allowed {
		if err := blockedAddress(net.ParseIP(s)); err != nil {
			t.Errorf("blockedAddress(%s) = %v, want nil", s, err)
		}
	}
}

// TestWebFetchBlocksMetadataURL checks that a fetch of a cloud metadata address
// is refused by the dialer, so no request leaves the process.
func TestWebFetchBlocksMetadataURL(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	_, _, err := FetchMarkdown(context.Background(), "http://169.254.169.254/")
	if err == nil || !strings.Contains(err.Error(), "blocked address") {
		t.Fatalf("err=%v want blocked address", err)
	}
}
