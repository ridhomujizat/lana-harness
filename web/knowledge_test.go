package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// fakeKnowledgeBackend adds the knowledge port to the in-memory backend.
type fakeKnowledgeBackend struct {
	*fakeWebBackend
	status           core.KnowledgeStatus
	statusErr        error
	report           KnowledgeReport
	reportErr        error
	uploadErr        error
	consolidateErr   error
	consolidateCalls int
	uploaded         []knowledgeUploaded
}

type knowledgeUploaded struct {
	name string
	data string
}

func newFakeKnowledgeBackend() *fakeKnowledgeBackend {
	return &fakeKnowledgeBackend{fakeWebBackend: newFakeWebBackend()}
}

func (b *fakeKnowledgeBackend) KnowledgeStatus(context.Context) (core.KnowledgeStatus, error) {
	return b.status, b.statusErr
}

func (b *fakeKnowledgeBackend) KnowledgeReport(context.Context) (KnowledgeReport, error) {
	return b.report, b.reportErr
}

func (b *fakeKnowledgeBackend) KnowledgeUpload(_ context.Context, name string, data []byte) (string, error) {
	b.uploaded = append(b.uploaded, knowledgeUploaded{name: name, data: string(data)})
	if b.uploadErr != nil {
		return "", b.uploadErr
	}
	return "inbox/" + name, nil
}

func (b *fakeKnowledgeBackend) KnowledgeConsolidate(context.Context) error {
	b.consolidateCalls++
	return b.consolidateErr
}

// sampleKnowledgeStatus is a fully populated GET /v1/knowledge body.
func sampleKnowledgeStatus() core.KnowledgeStatus {
	return core.KnowledgeStatus{
		Available:        true,
		Path:             "/Users/ridho/.lana/knowledge",
		Exists:           true,
		Counts:           core.KnowledgeCounts{Pages: 42, Sources: 10, JournalDays: 7},
		SourcesPending:   []string{"sources/2026-11-01 Rapat Q4.pdf"},
		SourcesFailed:    []core.KnowledgeSourceError{{Path: "sources/x.md", Error: "invalid JSON from model"}},
		InboxUnsupported: []string{"inbox/notes.docx"},
		Embedding:        core.KnowledgeEmbeddingStatus{Provider: "openai", Model: "text-embedding-3-small", Chunks: 900, Vectors: 880},
		Agent:            "lana",
		AgentError:       "",
		Consolidation:    core.KnowledgeConsolidation{Running: false, LastAt: 1761958800000, NextAt: 1762045200000, Report: "reports/lint-2026-11-01.md"},
		Log:              []core.KnowledgeLogEntry{{Heading: "2026-11-01 03:00 consolidation", Lines: []string{"- created [[Proyek X]]"}}},
	}
}

// TestKnowledgeStatusShape covers the API-KNU-003 response of GET
// /v1/knowledge.
func TestKnowledgeStatusShape(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.status = sampleKnowledgeStatus()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got core.KnowledgeStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !reflect.DeepEqual(got, backend.status) {
		t.Fatalf("status = %+v, want %+v", got, backend.status)
	}
	for _, key := range []string{`"available":true`, `"counts":{"pages":42`, `"sources_pending":[`, `"sources_failed":[`, `"embedding":{"provider":"openai"`, `"consolidation":{"running":false`, `"log":[`} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
}

// TestKnowledgeStatusListsNeverNull covers API-KNU-003: an empty vault status
// still sends empty lists, never null.
func TestKnowledgeStatusListsNeverNull(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.status = core.KnowledgeStatus{
		SourcesPending:   []string{},
		SourcesFailed:    []core.KnowledgeSourceError{},
		InboxUnsupported: []string{},
		Log:              []core.KnowledgeLogEntry{},
	}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	for _, key := range []string{`"sources_pending":[]`, `"sources_failed":[]`, `"inbox_unsupported":[]`, `"log":[]`} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
}

// TestKnowledgeStatusBackendErrorIs500 covers a status failure.
func TestKnowledgeStatusBackendErrorIs500(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.statusErr = errors.New("boom")
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge", "", cookie, ""))
	assertAuthError(t, rr, http.StatusInternalServerError, "internal_error")
}

// TestKnowledgeReportShape covers the API-KNU-004 success body.
func TestKnowledgeReportShape(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.report = KnowledgeReport{Path: "reports/lint-2026-11-01.md", Markdown: "# Knowledge lint\n"}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/report", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got KnowledgeReport
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if got != backend.report {
		t.Fatalf("report = %+v, want %+v", got, backend.report)
	}
}

// TestKnowledgeReportMissingIs404 covers API-KNU-004: no report is not_found.
func TestKnowledgeReportMissingIs404(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.reportErr = fmt.Errorf("knowledge report: %w", fs.ErrNotExist)
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/report", "", cookie, ""))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

// TestKnowledgeReportBackendErrorIs500 covers a report read failure.
func TestKnowledgeReportBackendErrorIs500(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.reportErr = errors.New("boom")
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/report", "", cookie, ""))
	assertAuthError(t, rr, http.StatusInternalServerError, "internal_error")
}

// knowledgeUploadRequest builds a multipart upload request with the given
// files under the "file" field.
func knowledgeUploadRequest(t *testing.T, cookie *http.Cookie, csrf string, files []knowledgeUploaded) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for _, file := range files {
		part, err := writer.CreateFormFile("file", file.name)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := io.WriteString(part, file.data); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/knowledge/upload", &buf)
	req.Host = authTestAddr
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	return req
}

// TestKnowledgeUploadSavesFiles covers the API-KNU-005 success body and that
// every file reaches the backend.
func TestKnowledgeUploadSavesFiles(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, knowledgeUploadRequest(t, cookie, csrf, []knowledgeUploaded{
		{name: "a.md", data: "one"},
		{name: "b.txt", data: "two"},
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got knowledgeUploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !reflect.DeepEqual(got.Saved, []string{"inbox/a.md", "inbox/b.txt"}) {
		t.Fatalf("saved = %v, want [inbox/a.md inbox/b.txt]", got.Saved)
	}
	if len(backend.uploaded) != 2 || backend.uploaded[0].name != "a.md" || backend.uploaded[1].data != "two" {
		t.Fatalf("uploaded = %+v", backend.uploaded)
	}
}

// TestKnowledgeUploadUnsupportedExtIs400 covers API-KNU-005: the refused file
// is named and never reaches the backend.
func TestKnowledgeUploadUnsupportedExtIs400(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, knowledgeUploadRequest(t, cookie, csrf, []knowledgeUploaded{{name: "notes.docx", data: "x"}}))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	if !strings.Contains(rr.Body.String(), "notes.docx") {
		t.Fatalf("body does not name the file: %s", rr.Body.String())
	}
	if len(backend.uploaded) != 0 {
		t.Fatalf("backend upload called %d times", len(backend.uploaded))
	}
}

// TestKnowledgeUploadTooLargeIs400 covers the API-KNU-005 per-file cap.
func TestKnowledgeUploadTooLargeIs400(t *testing.T) {
	old := knowledgeUploadFileLimit
	knowledgeUploadFileLimit = 8
	t.Cleanup(func() { knowledgeUploadFileLimit = old })

	backend := newFakeKnowledgeBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, knowledgeUploadRequest(t, cookie, csrf, []knowledgeUploaded{{name: "big.md", data: strings.Repeat("x", 9)}}))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	if len(backend.uploaded) != 0 {
		t.Fatalf("backend upload called %d times", len(backend.uploaded))
	}
}

// TestKnowledgeUploadNoFileIs400 covers the API-KNU-005 requirement of one or
// more files.
func TestKnowledgeUploadNoFileIs400(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, knowledgeUploadRequest(t, cookie, csrf, nil))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}

// TestKnowledgeUploadNotMultipartIs400 covers a body that is not
// multipart/form-data.
func TestKnowledgeUploadNotMultipartIs400(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/knowledge/upload", "hello", cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}

// TestKnowledgeUploadBackendErrorIs500 covers a storage failure.
func TestKnowledgeUploadBackendErrorIs500(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.uploadErr = errors.New("boom")
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, knowledgeUploadRequest(t, cookie, csrf, []knowledgeUploaded{{name: "a.md", data: "x"}}))
	assertAuthError(t, rr, http.StatusInternalServerError, "internal_error")
}

// TestKnowledgeConsolidateStarts covers the API-KNU-006 202 body.
func TestKnowledgeConsolidateStarts(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/knowledge/consolidate", "", cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got knowledgeConsolidateResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !got.Started {
		t.Fatalf("started = false, want true")
	}
	if backend.consolidateCalls != 1 {
		t.Fatalf("consolidate calls = %d, want 1", backend.consolidateCalls)
	}
}

// TestKnowledgeConsolidateBusyIs409 covers API-KNU-006: a run in progress is
// reported as busy.
func TestKnowledgeConsolidateBusyIs409(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.consolidateErr = core.ErrKnowledgeBusy
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/knowledge/consolidate", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "busy")
}

// TestKnowledgeConsolidateUnavailableIs503 covers API-KNU-006: an unavailable
// knowledge service is 503.
func TestKnowledgeConsolidateUnavailableIs503(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.consolidateErr = ErrKnowledgeUnavailable
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/knowledge/consolidate", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusServiceUnavailable, "unavailable")
}

// TestKnowledgeConsolidateBackendErrorIs500 covers any other failure.
func TestKnowledgeConsolidateBackendErrorIs500(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.consolidateErr = errors.New("boom")
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/knowledge/consolidate", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusInternalServerError, "internal_error")
}

// TestKnowledgeWithoutPortIs500 covers a Backend that does not implement the
// optional KnowledgeBackend port on every route.
func TestKnowledgeWithoutPortIs500(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())

	cases := []struct {
		method string
		target string
		body   string
	}{
		{http.MethodGet, "/v1/knowledge", ""},
		{http.MethodGet, "/v1/knowledge/report", ""},
		{http.MethodPost, "/v1/knowledge/upload", "x"},
		{http.MethodPost, "/v1/knowledge/consolidate", ""},
	}
	for _, tc := range cases {
		rr := serveAuth(s, sessionTestRequest(tc.method, tc.target, tc.body, cookie, csrf))
		assertAuthError(t, rr, http.StatusInternalServerError, "internal_error")
	}
}

// TestKnowledgeRequiresAuth covers the auth middleware on the routes.
func TestKnowledgeRequiresAuth(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, _ := newSessionTestServer(t, backend)

	rr := serveAuth(s, authRequest(http.MethodGet, "/v1/knowledge", ""))
	assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
}
