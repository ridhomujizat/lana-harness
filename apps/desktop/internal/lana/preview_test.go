package lana

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// previewErrorAnswer writes the flat {"error": "..."} answer the preview route
// used to answer (REQ-WP-001).
func previewErrorAnswer(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// previewEnvelopeAnswer writes the standard {"error": {"code", "message"}}
// envelope the daemon's routes answer (REQ-DW-013).
func previewEnvelopeAnswer(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

// TestPreviewReturnsTheFile is REQ-WP-001/002: the client posts the session,
// the link and the space folder, and decodes the file's path, kind and URLs.
func TestPreviewReturnsTheFile(t *testing.T) {
	d := newFakeDaemon(t)
	d.preview = func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.previewBody = body
		d.mu.Unlock()
		writeJSON(w, http.StatusOK, PreviewFile{
			Path:   "/tmp/work/main.go",
			Name:   "main.go",
			Dir:    "/tmp/work",
			Kind:   "text",
			Size:   13,
			Line:   12,
			URL:    PreviewPath + "/tok/main.go?view=1#L12",
			RawURL: PreviewPath + "/tok/main.go",
		})
	}
	conn := d.connect(t)

	file, err := conn.Preview(context.Background(), "sess-1", "main.go:12", "/tmp/work")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if file.Path != "/tmp/work/main.go" || file.Name != "main.go" || file.Kind != "text" || file.Line != 12 {
		t.Fatalf("preview file = %+v", file)
	}
	if file.URL != PreviewPath+"/tok/main.go?view=1#L12" || file.RawURL != PreviewPath+"/tok/main.go" {
		t.Fatalf("preview urls = %q, %q", file.URL, file.RawURL)
	}

	d.mu.Lock()
	body := string(d.previewBody)
	d.mu.Unlock()
	for _, want := range []string{`"session":"sess-1"`, `"link":"main.go:12"`, `"dir":"/tmp/work"`} {
		if !strings.Contains(body, want) {
			t.Errorf("preview body %q does not hold %q", body, want)
		}
	}
}

// TestPreviewNotFoundCarriesTheMessage is REQ-WP-001: a 404 is a preview error
// whose text is the daemon's own "no such file: ..." message.
func TestPreviewNotFoundCarriesTheMessage(t *testing.T) {
	d := newFakeDaemon(t)
	d.preview = func(w http.ResponseWriter, _ *http.Request) {
		previewErrorAnswer(w, http.StatusNotFound, "no such file: /tmp/work/nope.go")
	}
	conn := d.connect(t)

	_, err := conn.Preview(context.Background(), "sess-1", "nope.go", "/tmp/work")
	if !IsPreviewNotFound(err) {
		t.Fatalf("error = %v, want a 404 preview error", err)
	}
	if !strings.Contains(err.Error(), "no such file: /tmp/work/nope.go") {
		t.Fatalf("error = %q, want the daemon's message", err)
	}
}

// TestPreviewForbiddenCarriesTheMessage is REQ-WP-001: a 403 is a preview
// error whose text is the daemon's "the path is not allowed" message.
func TestPreviewForbiddenCarriesTheMessage(t *testing.T) {
	d := newFakeDaemon(t)
	d.preview = func(w http.ResponseWriter, _ *http.Request) {
		previewErrorAnswer(w, http.StatusForbidden, "the path is not allowed")
	}
	conn := d.connect(t)

	_, err := conn.Preview(context.Background(), "sess-1", "~/.ssh/id_ed25519", "")
	if !IsPreviewForbidden(err) {
		t.Fatalf("error = %v, want a 403 preview error", err)
	}
	if !strings.Contains(err.Error(), "the path is not allowed") {
		t.Fatalf("error = %q, want the daemon's message", err)
	}
}

// TestPreviewBadLinkCarriesTheMessage is REQ-WP-001: a 400 is a preview error
// whose text is the daemon's own message.
func TestPreviewBadLinkCarriesTheMessage(t *testing.T) {
	d := newFakeDaemon(t)
	d.preview = func(w http.ResponseWriter, _ *http.Request) {
		previewErrorAnswer(w, http.StatusBadRequest, "the link must name a local file")
	}
	conn := d.connect(t)

	_, err := conn.Preview(context.Background(), "sess-1", "https://example.com", "")
	if !IsPreviewBadLink(err) {
		t.Fatalf("error = %v, want a 400 preview error", err)
	}
	if !strings.Contains(err.Error(), "the link must name a local file") {
		t.Fatalf("error = %q, want the daemon's message", err)
	}
}

// TestPreviewNotFoundEnvelopeCarriesTheMessage is REQ-WP-001: a 404 envelope
// answer carries the daemon's error.message and stays a not-found preview.
func TestPreviewNotFoundEnvelopeCarriesTheMessage(t *testing.T) {
	d := newFakeDaemon(t)
	d.preview = func(w http.ResponseWriter, _ *http.Request) {
		previewEnvelopeAnswer(w, http.StatusNotFound, "not_found", "no such file: /x")
	}
	conn := d.connect(t)

	_, err := conn.Preview(context.Background(), "sess-1", "x.go", "/x")
	if !IsPreviewNotFound(err) {
		t.Fatalf("error = %v, want a 404 preview error", err)
	}
	var previewErr *PreviewError
	if !errors.As(err, &previewErr) {
		t.Fatalf("error = %v, want a *PreviewError", err)
	}
	if previewErr.Message != "no such file: /x" {
		t.Fatalf("message = %q, want %q", previewErr.Message, "no such file: /x")
	}
}

// TestPreviewForbiddenEnvelopeCarriesTheMessage is REQ-WP-001: a 403 envelope
// answer carries the daemon's error.message and stays a forbidden preview.
func TestPreviewForbiddenEnvelopeCarriesTheMessage(t *testing.T) {
	d := newFakeDaemon(t)
	d.preview = func(w http.ResponseWriter, _ *http.Request) {
		previewEnvelopeAnswer(w, http.StatusForbidden, "forbidden_path", "the path is not allowed")
	}
	conn := d.connect(t)

	_, err := conn.Preview(context.Background(), "sess-1", "~/.ssh/id_ed25519", "")
	if !IsPreviewForbidden(err) {
		t.Fatalf("error = %v, want a 403 preview error", err)
	}
	var previewErr *PreviewError
	if !errors.As(err, &previewErr) {
		t.Fatalf("error = %v, want a *PreviewError", err)
	}
	if previewErr.Message != "the path is not allowed" {
		t.Fatalf("message = %q, want %q", previewErr.Message, "the path is not allowed")
	}
}

// TestPreviewBadLinkEnvelopeCarriesTheMessage is REQ-WP-001: a 400 envelope
// answer carries the daemon's error.message and stays a bad-link preview.
func TestPreviewBadLinkEnvelopeCarriesTheMessage(t *testing.T) {
	d := newFakeDaemon(t)
	d.preview = func(w http.ResponseWriter, _ *http.Request) {
		previewEnvelopeAnswer(w, http.StatusBadRequest, "invalid_link", "the link must name a local file")
	}
	conn := d.connect(t)

	_, err := conn.Preview(context.Background(), "sess-1", "https://example.com", "")
	if !IsPreviewBadLink(err) {
		t.Fatalf("error = %v, want a 400 preview error", err)
	}
	var previewErr *PreviewError
	if !errors.As(err, &previewErr) {
		t.Fatalf("error = %v, want a *PreviewError", err)
	}
	if previewErr.Message != "the link must name a local file" {
		t.Fatalf("message = %q, want %q", previewErr.Message, "the link must name a local file")
	}
}
