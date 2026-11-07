package tools

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestWebSearchResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"web":{"results":[{"title":"Go <strong>1.25</strong>","url":"https://go.dev","description":"Fast &amp; <strong>safe</strong>","age":"2 days ago"},{"title":"The Go Blog","url":"https://go.dev/blog","description":"News","age":"1 week ago"}]}}`))
	}))
	defer srv.Close()
	old := webSearchBaseURL
	webSearchBaseURL = srv.URL
	defer func() { webSearchBaseURL = old }()
	t.Setenv("BRAVE_API_KEY", "test-key")

	out := callTool(t, NewWebSearch(), t.TempDir(), `{"query":"golang"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	for _, want := range []string{"1. Go 1.25", "   https://go.dev\n", "Fast & safe (2 days ago)", "2. The Go Blog", "News (1 week ago)"} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("output lacks %q:\n%s", want, out.Output)
		}
	}
	if strings.Contains(out.Output, "<strong>") || strings.Contains(out.Output, "&amp;") {
		t.Fatalf("HTML was not stripped: %q", out.Output)
	}
}

func TestWebSearchRequest(t *testing.T) {
	queries := make(chan url.Values, 1)
	headers := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.Query()
		headers <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()
	old := webSearchBaseURL
	webSearchBaseURL = srv.URL
	defer func() { webSearchBaseURL = old }()
	t.Setenv("BRAVE_API_KEY", "test-key")

	out := callTool(t, NewWebSearch(), t.TempDir(), `{"query":"golang generics","count":3,"freshness":"pw","country":"US"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if out.Output != "No results for golang generics" {
		t.Fatalf("output=%q", out.Output)
	}
	query := <-queries
	if got := query.Get("q"); got != "golang generics" {
		t.Fatalf("q=%q", got)
	}
	if got := query.Get("count"); got != "3" {
		t.Fatalf("count=%q", got)
	}
	if got := query.Get("freshness"); got != "pw" {
		t.Fatalf("freshness=%q", got)
	}
	if got := query.Get("country"); got != "US" {
		t.Fatalf("country=%q", got)
	}
	header := <-headers
	if got := header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept=%q", got)
	}
	if got := header.Get("X-Subscription-Token"); got != "test-key" {
		t.Fatalf("X-Subscription-Token=%q", got)
	}
}

func TestWebSearchDefaultCount(t *testing.T) {
	queries := make(chan url.Values, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()
	old := webSearchBaseURL
	webSearchBaseURL = srv.URL
	defer func() { webSearchBaseURL = old }()
	t.Setenv("BRAVE_API_KEY", "test-key")

	_ = callTool(t, NewWebSearch(), t.TempDir(), `{"query":"golang"}`)
	if got := (<-queries).Get("count"); got != "8" {
		t.Fatalf("count=%q, want the default 8", got)
	}
}

func TestWebSearchMissingKey(t *testing.T) {
	t.Setenv("BRAVE_API_KEY", "")
	out := callTool(t, NewWebSearch(), t.TempDir(), `{"query":"golang"}`)
	if out.Status != core.ToolStatusError {
		t.Fatalf("out=%+v", out)
	}
	if out.Output != "web_search is not configured: set BRAVE_API_KEY in the daemon environment" {
		t.Fatalf("output=%q", out.Output)
	}
}

func TestWebSearchRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow down"))
	}))
	defer srv.Close()
	old := webSearchBaseURL
	webSearchBaseURL = srv.URL
	defer func() { webSearchBaseURL = old }()
	t.Setenv("BRAVE_API_KEY", "test-key")

	out := callTool(t, NewWebSearch(), t.TempDir(), `{"query":"golang"}`)
	if out.Status != core.ToolStatusError || out.Output != "Brave rate limit reached, retry later" {
		t.Fatalf("out=%+v", out)
	}
}

func TestWebSearchHTTPErrors(t *testing.T) {
	for _, tc := range []struct {
		status  int
		body    string
		want    string
		notWant string
	}{
		{http.StatusUnauthorized, "nope", "Brave rejected the API key", "nope"},
		{http.StatusForbidden, "nope", "Brave rejected the API key", "nope"},
		{http.StatusInternalServerError, "upstream broke", "Brave returned HTTP 500: upstream broke", ""},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		old := webSearchBaseURL
		webSearchBaseURL = srv.URL
		t.Setenv("BRAVE_API_KEY", "test-key")
		out := callTool(t, NewWebSearch(), t.TempDir(), `{"query":"golang"}`)
		srv.Close()
		webSearchBaseURL = old
		if out.Status != core.ToolStatusError || !strings.Contains(out.Output, tc.want) {
			t.Fatalf("status %d out=%+v", tc.status, out)
		}
		if tc.notWant != "" && strings.Contains(out.Output, tc.notWant) {
			t.Fatalf("status %d output leaks body: %q", tc.status, out.Output)
		}
	}
}

func TestWebSearchInvalidInput(t *testing.T) {
	for _, input := range []string{
		`{"query":""}`,
		`{"query":"golang","count":50}`,
		`{"query":"golang","count":0}`,
		`{"query":"golang","freshness":"xx"}`,
		`{"query":"golang","country":"USA"}`,
	} {
		out := callTool(t, NewWebSearch(), t.TempDir(), input)
		if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "invalid input") {
			t.Fatalf("input %s out=%+v", input, out)
		}
	}
}
