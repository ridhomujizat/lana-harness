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

	// scoped-knowledge port (API-DKN-004–008).
	pages          []core.KnowledgePageInfo
	pagesTotal     int
	pagesErr       error
	pagesScope     string
	pagesQuery     string
	pagesLimit     int
	pageDoc        core.KnowledgeDoc
	pageMarkdown   string
	pageErr        error
	reviewItems    []core.ReviewItem
	reviewErr      error
	decideStatus   string
	decideErr      error
	decideCalls    []reviewDecision
	knowledgeAvail bool
	knowledgeStats core.KnowledgeDomainStats
	knowledgeErr   error
}

type knowledgeUploaded struct {
	name string
	data string
}

type reviewDecision struct {
	id      string
	approve bool
}

func (b *fakeKnowledgeBackend) KnowledgePages(_ context.Context, scope, q string, limit int) ([]core.KnowledgePageInfo, int, error) {
	b.pagesScope, b.pagesQuery, b.pagesLimit = scope, q, limit
	if b.pagesErr != nil {
		return nil, 0, b.pagesErr
	}
	return b.pages, b.pagesTotal, nil
}

func (b *fakeKnowledgeBackend) KnowledgePage(_ context.Context, _ string) (core.KnowledgeDoc, string, error) {
	if b.pageErr != nil {
		return core.KnowledgeDoc{}, "", b.pageErr
	}
	return b.pageDoc, b.pageMarkdown, nil
}

func (b *fakeKnowledgeBackend) ReviewItems(context.Context) ([]core.ReviewItem, error) {
	if b.reviewErr != nil {
		return nil, b.reviewErr
	}
	return b.reviewItems, nil
}

func (b *fakeKnowledgeBackend) ReviewDecide(_ context.Context, id string, approve bool) (string, error) {
	b.decideCalls = append(b.decideCalls, reviewDecision{id: id, approve: approve})
	if b.decideErr != nil {
		return "", b.decideErr
	}
	return b.decideStatus, nil
}

func (b *fakeKnowledgeBackend) AgentKnowledge(_ context.Context, _ string) (bool, core.KnowledgeDomainStats, error) {
	if b.knowledgeErr != nil {
		return false, core.KnowledgeDomainStats{}, b.knowledgeErr
	}
	return b.knowledgeAvail, b.knowledgeStats, nil
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

// sampleKnowledgePage is a fully populated API-DKN-004 row.
func sampleKnowledgePage() core.KnowledgePageInfo {
	return core.KnowledgePageInfo{
		Path: "agents/dana/wiki/Gaji.md", Title: "Gaji", Scope: "dana", Type: "topic",
		Description: "Aturan gaji tim", Updated: "2026-11-10", Mtime: 1762041600000,
	}
}

// TestKnowledgeStatusScopesAndReviewPending covers the API-DKN-003 additions.
func TestKnowledgeStatusScopesAndReviewPending(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.status = core.KnowledgeStatus{
		Available:     true,
		Scopes:        []core.KnowledgeScopeStat{{Scope: "shared", Pages: 42, JournalDays: 7, UpdatedAt: 1762045200000}},
		ReviewPending: 3,
	}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	for _, key := range []string{`"scopes":[{"scope":"shared","pages":42`, `"review_pending":3`} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
}

// TestKnowledgePagesShape covers the API-DKN-004 success body.
func TestKnowledgePagesShape(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.pages = []core.KnowledgePageInfo{sampleKnowledgePage()}
	backend.pagesTotal = 54
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/pages", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got knowledgePagesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if got.Total != 54 || len(got.Pages) != 1 || !reflect.DeepEqual(got.Pages[0], sampleKnowledgePage()) {
		t.Fatalf("pages = %+v, total = %d", got.Pages, got.Total)
	}
	for _, key := range []string{`"pages":[`, `"total":54`, `"path":"agents/dana/wiki/Gaji.md"`, `"scope":"dana"`, `"mtime":1762041600000`} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
	// API-DKN-004: the default scope is all and the default limit is 200.
	if backend.pagesScope != "all" || backend.pagesLimit != 200 {
		t.Fatalf("scope = %q, limit = %d, want all, 200", backend.pagesScope, backend.pagesLimit)
	}
}

// TestKnowledgePagesEmptyNeverNull covers API-DKN-004: an empty list is [].
func TestKnowledgePagesEmptyNeverNull(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/pages", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"pages":[]`) {
		t.Fatalf("body has no empty pages: %s", rr.Body.String())
	}
}

// TestKnowledgePagesScopeAndLimit covers the API-DKN-004 query parameters.
func TestKnowledgePagesScopeAndLimit(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/pages?scope=dana&q=gaji&limit=10", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if backend.pagesScope != "dana" || backend.pagesQuery != "gaji" || backend.pagesLimit != 10 {
		t.Fatalf("scope = %q, q = %q, limit = %d", backend.pagesScope, backend.pagesQuery, backend.pagesLimit)
	}
}

// TestKnowledgePagesInvalidQueryIs400 covers the API-DKN-004 validation.
func TestKnowledgePagesInvalidQueryIs400(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	for _, target := range []string{
		"/v1/knowledge/pages?scope=Dana",
		"/v1/knowledge/pages?scope=bad!",
		"/v1/knowledge/pages?limit=0",
		"/v1/knowledge/pages?limit=501",
		"/v1/knowledge/pages?limit=abc",
	} {
		rr := serveAuth(s, sessionTestRequest(http.MethodGet, target, "", cookie, ""))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	}
}

// TestKnowledgePagesUnavailableIs503 covers API-DKN-004 without a service.
func TestKnowledgePagesUnavailableIs503(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.pagesErr = ErrKnowledgeUnavailable
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/pages", "", cookie, ""))
	assertAuthError(t, rr, http.StatusServiceUnavailable, "unavailable")
}

// TestKnowledgePageShape covers the API-DKN-005 success body.
func TestKnowledgePageShape(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.pageDoc = core.KnowledgeDoc{Path: "agents/dana/wiki/Gaji.md", Title: "Gaji", Scope: "dana"}
	backend.pageMarkdown = "# Gaji\n"
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/page?path=agents/dana/wiki/Gaji.md", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got knowledgePageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if got.Path != "agents/dana/wiki/Gaji.md" || got.Title != "Gaji" || got.Scope != "dana" || got.Markdown != "# Gaji\n" {
		t.Fatalf("page = %+v", got)
	}
}

// TestKnowledgePageInvalidPathIs400 covers the API-DKN-005 path validation.
func TestKnowledgePageInvalidPathIs400(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	for _, path := range []string{
		"",
		"../etc/passwd",
		"wiki/../x.md",
		"sources/x.md",
		"wiki/x.txt",
		"agents/dana/index.md",
	} {
		rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/page?path="+path, "", cookie, ""))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	}
}

// TestKnowledgePageMissingIs404 covers API-DKN-005: a missing file is
// not_found.
func TestKnowledgePageMissingIs404(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.pageErr = fmt.Errorf("knowledge page: %w", fs.ErrNotExist)
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/page?path=wiki/Missing.md", "", cookie, ""))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

// TestKnowledgePageUnavailableIs503 covers API-DKN-005 without a service.
func TestKnowledgePageUnavailableIs503(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.pageErr = ErrKnowledgeUnavailable
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/page?path=wiki/a.md", "", cookie, ""))
	assertAuthError(t, rr, http.StatusServiceUnavailable, "unavailable")
}

// TestReviewListShape covers the API-DKN-006 success body.
func TestReviewListShape(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.reviewItems = []core.ReviewItem{
		{ID: "p_1", Kind: "knowledge", Agent: "rio", From: "dana", Title: "Gaji", Action: "append", Body: "gaji naik", OwnerTurn: true, SessionID: "06GS", CreatedAt: 1762041600000},
		{ID: "s_dana.payroll-check", Kind: "skill", Agent: "dana", From: "dana", Title: "payroll-check", Action: "update", Description: "When payroll runs", Body: "steps", OwnerTurn: true, SessionID: "06GS", CreatedAt: 1762041000000},
	}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/review", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got reviewListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !reflect.DeepEqual(got.Items, backend.reviewItems) {
		t.Fatalf("items = %+v, want %+v", got.Items, backend.reviewItems)
	}
	for _, key := range []string{`"kind":"knowledge"`, `"kind":"skill"`, `"id":"s_dana.payroll-check"`, `"description":"When payroll runs"`, `"owner_turn":true`} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
}

// TestReviewListEmptyNeverNull covers API-DKN-006: an empty queue is [].
func TestReviewListEmptyNeverNull(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/review", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"items":[]`) {
		t.Fatalf("body has no empty items: %s", rr.Body.String())
	}
}

// TestReviewListUnavailableIs503 covers API-DKN-006 without a service.
func TestReviewListUnavailableIs503(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.reviewErr = ErrKnowledgeUnavailable
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/review", "", cookie, ""))
	assertAuthError(t, rr, http.StatusServiceUnavailable, "unavailable")
}

// TestReviewApproveReject covers the API-DKN-007 happy paths: approve and
// reject route by id and return the new status (a skill approve is "applied").
func TestReviewApproveReject(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	backend.decideStatus = "approved"
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/review/p_1/approve", "", cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("approve status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got reviewDecideResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if got.ID != "p_1" || got.Status != "approved" {
		t.Fatalf("approve = %+v", got)
	}

	backend.decideStatus = "applied"
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/review/s_dana.payroll-check/approve", "", cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("skill approve status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if got.ID != "s_dana.payroll-check" || got.Status != "applied" {
		t.Fatalf("skill approve = %+v", got)
	}

	backend.decideStatus = "rejected"
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/review/p_1/reject", "", cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("reject status = %d, body = %q", rr.Code, rr.Body.String())
	}
	want := []reviewDecision{{"p_1", true}, {"s_dana.payroll-check", true}, {"p_1", false}}
	if !reflect.DeepEqual(backend.decideCalls, want) {
		t.Fatalf("decisions = %+v, want %+v", backend.decideCalls, want)
	}
}

// TestReviewDecideMissingIs404 covers API-DKN-007: an unknown id is not_found.
func TestReviewDecideMissingIs404(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.decideErr = fmt.Errorf("review item: %w", fs.ErrNotExist)
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/review/p_missing/approve", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

// TestReviewDecideInvalidIs400 covers API-DKN-007: a skill that no longer
// passes its rules is invalid_request.
func TestReviewDecideInvalidIs400(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.decideErr = ErrReviewInvalid
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/review/s_dana.x/approve", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}

// TestKnowledgeDomainWithoutPortIs500 covers a Backend that does not implement
// the optional KnowledgeDomainBackend port on every new route.
func TestKnowledgeDomainWithoutPortIs500(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())

	cases := []struct {
		method string
		target string
	}{
		{http.MethodGet, "/v1/knowledge/pages"},
		{http.MethodGet, "/v1/knowledge/page?path=wiki/a.md"},
		{http.MethodGet, "/v1/review"},
		{http.MethodPost, "/v1/review/p_1/approve"},
	}
	for _, tc := range cases {
		rr := serveAuth(s, sessionTestRequest(tc.method, tc.target, "", cookie, csrf))
		assertAuthError(t, rr, http.StatusInternalServerError, "internal_error")
	}
}

// TestKnowledgeDomainRequiresAuth covers the auth middleware on the new routes.
func TestKnowledgeDomainRequiresAuth(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, _ := newSessionTestServer(t, backend)

	for _, target := range []string{"/v1/knowledge/pages", "/v1/knowledge/page?path=wiki/a.md", "/v1/review"} {
		rr := serveAuth(s, authRequest(http.MethodGet, target, ""))
		assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
	}
}
