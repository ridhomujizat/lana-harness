package tools

import (
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
