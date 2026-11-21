package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// decodeGzip decodes a gzip body and returns it as a string.
func decodeGzip(t *testing.T, data []byte) string {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()
	decoded, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	return string(decoded)
}

// compressWrap installs the gzip middleware on top of next. It needs no other
// Server state.
func compressWrap(next http.Handler) http.Handler {
	return (&Server{}).compress(next)
}

func TestCompressGzipsJSONResponse(t *testing.T) {
	body := `{"events":[` + strings.Repeat(`"x",`, 500) + `"end"]}`
	h := compressWrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/1/events", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("Vary = %q, want Accept-Encoding", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length = %q, want empty", got)
	}
	if got := decodeGzip(t, rec.Body.Bytes()); got != body {
		t.Fatalf("decoded body = %d bytes, want %d", len(got), len(body))
	}
}

func TestCompressSkipsWithoutAcceptEncoding(t *testing.T) {
	body := `{"events":[` + strings.Repeat(`"x",`, 500) + `"end"]}`
	h := compressWrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/1/events", nil)
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty", got)
	}
	if got := rec.Body.String(); got != body {
		t.Fatalf("body was rewritten: got %d bytes, want %d", len(got), len(body))
	}
}

func TestCompressHonorsAcceptEncodingQuality(t *testing.T) {
	h := compressWrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/1/events", nil)
	req.Header.Set("Accept-Encoding", "gzip;q=0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for gzip;q=0", got)
	}
}

func TestCompressGzipsWildcardAccepted(t *testing.T) {
	h := compressWrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/1/events", nil)
	req.Header.Set("Accept-Encoding", "br, *")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip for wildcard", got)
	}
}

func TestCompressSkipsEventStreamAndFlushes(t *testing.T) {
	const event = "event: status\ndata: {\"holder\":\"\"}\n\n"
	h := compressWrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, event); err != nil {
			t.Errorf("write event: %v", err)
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush: %v", err)
		}
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/1/stream", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for event stream", got)
	}
	if !rec.Flushed {
		t.Fatal("Flush did not reach the underlying writer")
	}
	if got := rec.Body.String(); got != event {
		t.Fatalf("body = %q, want %q", got, event)
	}
}

func TestCompressSkipsRangeResponse(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 300)
	h := compressWrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.ServeContent(w, r, "body.json", time.Time{}, bytes.NewReader(body))
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/1/events", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Range", "bytes=0-3")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for a range response", got)
	}
	if got := rec.Header().Get("Content-Range"); got == "" {
		t.Fatal("Content-Range is empty")
	}
	if got := rec.Body.String(); got != "aaaa" {
		t.Fatalf("body = %q, want %q", got, "aaaa")
	}
}

func TestCompressSkipsUpgrade(t *testing.T) {
	done := make(chan struct{})
	var hijackable, wrapped bool
	h := compressWrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, hijackable = w.(http.Hijacker)
		_, wrapped = w.(*compressWriter)
		w.WriteHeader(http.StatusNoContent)
		close(done)
	}))
	ts := httptest.NewServer(h)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	<-done

	if !hijackable {
		t.Fatal("upgrade request did not receive a hijackable ResponseWriter")
	}
	if wrapped {
		t.Fatal("upgrade request received the compress wrapper")
	}
}

func TestCompressSkipsHeadRequests(t *testing.T) {
	h := compressWrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/v1/sessions/1/events", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for HEAD", got)
	}
}
