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
	"time"

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
	graph          core.KnowledgeGraph
	graphErr       error
	graphScope     string
	graphJournals  bool
	graphSources   bool
	activity       core.KnowledgeActivityPage
	activityErr    error
	activityLimit  int
	activityBefore time.Time
	activityKind   string
	health         core.KnowledgeHealth
	healthErr      error
	reviewItems    []core.ReviewItem
	reviewErr      error
	reviewStatus   string
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

func (b *fakeKnowledgeBackend) KnowledgeGraph(_ context.Context, scope string, journals, sources bool) (core.KnowledgeGraph, error) {
	b.graphScope, b.graphJournals, b.graphSources = scope, journals, sources
	if b.graphErr != nil {
		return core.KnowledgeGraph{}, b.graphErr
	}
	return b.graph, nil
}

func (b *fakeKnowledgeBackend) KnowledgeActivity(_ context.Context, limit int, before time.Time, kind string) (core.KnowledgeActivityPage, error) {
	b.activityLimit, b.activityBefore, b.activityKind = limit, before, kind
	if b.activityErr != nil {
		return core.KnowledgeActivityPage{}, b.activityErr
	}
	return b.activity, nil
}

func (b *fakeKnowledgeBackend) KnowledgeHealth(context.Context) (core.KnowledgeHealth, error) {
	if b.healthErr != nil {
		return core.KnowledgeHealth{}, b.healthErr
	}
	return b.health, nil
}

func (b *fakeKnowledgeBackend) ReviewItems(_ context.Context, status string) ([]core.ReviewItem, error) {
	b.reviewStatus = status
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
		{http.MethodGet, "/v1/knowledge/graph"},
		{http.MethodGet, "/v1/knowledge/activity"},
		{http.MethodGet, "/v1/knowledge/health"},
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

	for _, target := range []string{
		"/v1/knowledge/pages", "/v1/knowledge/page?path=wiki/a.md", "/v1/review",
		"/v1/knowledge/graph", "/v1/knowledge/activity", "/v1/knowledge/health",
	} {
		rr := serveAuth(s, authRequest(http.MethodGet, target, ""))
		assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
	}
}

// sampleKnowledgeGraph is a populated API-KGR-001 body.
func sampleKnowledgeGraph() core.KnowledgeGraph {
	return core.KnowledgeGraph{
		Nodes: []core.KnowledgeGraphNode{
			{ID: "wiki/A.md", Title: "A", Scope: "shared", Kind: "page", Type: "topic", Updated: "2026-11-11", Degree: 1},
		},
		Hubs:   []core.KnowledgeGraphHub{{Scope: "shared", Pages: 1}},
		Edges:  []core.KnowledgeGraphEdge{{Source: "wiki/A.md", Target: "wiki/B.md"}},
		Counts: core.KnowledgeGraphCounts{Pages: 1, Links: 1, Broken: 0},
	}
}

// TestKnowledgeGraphShape covers the API-KGR-001 success body and query.
func TestKnowledgeGraphShape(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.graph = sampleKnowledgeGraph()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/graph?scope=dana&journals=1&sources=1", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got core.KnowledgeGraph
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !reflect.DeepEqual(got, backend.graph) {
		t.Fatalf("graph = %+v, want %+v", got, backend.graph)
	}
	if backend.graphScope != "dana" || !backend.graphJournals || !backend.graphSources {
		t.Fatalf("arguments = scope %q journals %v sources %v", backend.graphScope, backend.graphJournals, backend.graphSources)
	}
	for _, key := range []string{`"nodes":[`, `"hubs":[`, `"edges":[`, `"counts":{"pages":1,"links":1,"broken":0}`, `"truncated":false`} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
}

// TestKnowledgeGraphDefaultsAndEmpty covers the default scope and a never-null
// empty graph.
func TestKnowledgeGraphDefaultsAndEmpty(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/graph", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if backend.graphScope != "all" || backend.graphJournals || backend.graphSources {
		t.Fatalf("arguments = scope %q journals %v sources %v", backend.graphScope, backend.graphJournals, backend.graphSources)
	}
	for _, key := range []string{`"nodes":[]`, `"hubs":[]`, `"edges":[]`} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
}

// TestKnowledgeGraphInvalidIs400 covers the API-KGR-001 validation (AC-KGR-005).
func TestKnowledgeGraphInvalidIs400(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	for _, target := range []string{
		"/v1/knowledge/graph?scope=Bad",
		"/v1/knowledge/graph?scope=bad!",
		"/v1/knowledge/graph?journals=2",
		"/v1/knowledge/graph?journals=x",
		"/v1/knowledge/graph?sources=yes",
	} {
		rr := serveAuth(s, sessionTestRequest(http.MethodGet, target, "", cookie, ""))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	}
}

// TestKnowledgeGraphUnavailableIs503 covers API-KGR-001 without a service.
func TestKnowledgeGraphUnavailableIs503(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.graphErr = ErrKnowledgeUnavailable
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/graph", "", cookie, ""))
	assertAuthError(t, rr, http.StatusServiceUnavailable, "unavailable")
}

// sampleKnowledgeActivity is a populated API-KGR-004 body.
func sampleKnowledgeActivity() core.KnowledgeActivityPage {
	return core.KnowledgeActivityPage{
		Entries: []core.KnowledgeActivityEntry{
			{At: "2026-11-11T06:44:00+07:00", Heading: "consolidation", Kind: "run", Actor: "", Lines: []string{"journaled 11 sessions"}},
		},
		NextBefore: "2026-11-10T13:42:00+07:00",
	}
}

// TestKnowledgeActivityShape covers the API-KGR-004 success body and query.
func TestKnowledgeActivityShape(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.activity = sampleKnowledgeActivity()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/activity?limit=10&kind=writes&before=2026-11-11T06:00", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got core.KnowledgeActivityPage
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !reflect.DeepEqual(got, backend.activity) {
		t.Fatalf("activity = %+v, want %+v", got, backend.activity)
	}
	if backend.activityLimit != 10 || backend.activityKind != "writes" {
		t.Fatalf("arguments = limit %d kind %q", backend.activityLimit, backend.activityKind)
	}
	if !backend.activityBefore.Equal(time.Date(2026, 11, 11, 6, 0, 0, 0, time.Local)) {
		t.Fatalf("before = %v", backend.activityBefore)
	}
	for _, key := range []string{`"entries":[`, `"at":"2026-11-11T06:44:00+07:00"`, `"kind":"run"`, `"actor":""`, `"next_before":"2026-11-10T13:42:00+07:00"`} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
}

// TestKnowledgeActivityDefaults covers the default limit and an empty,
// never-null entries list.
func TestKnowledgeActivityDefaults(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/activity", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if backend.activityLimit != 50 || backend.activityKind != "" || !backend.activityBefore.IsZero() {
		t.Fatalf("arguments = limit %d kind %q before %v", backend.activityLimit, backend.activityKind, backend.activityBefore)
	}
	if !strings.Contains(rr.Body.String(), `"entries":[]`) {
		t.Fatalf("body has no empty entries: %s", rr.Body.String())
	}
}

// TestKnowledgeActivityRFC3339Before covers the RFC 3339 before form.
func TestKnowledgeActivityRFC3339Before(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/activity?before=2026-11-11T06:00:00%2B07:00", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	want, _ := time.Parse(time.RFC3339, "2026-11-11T06:00:00+07:00")
	if !backend.activityBefore.Equal(want) {
		t.Fatalf("before = %v, want %v", backend.activityBefore, want)
	}
}

// TestKnowledgeActivityInvalidIs400 covers the API-KGR-004 validation
// (AC-KGR-005).
func TestKnowledgeActivityInvalidIs400(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	for _, target := range []string{
		"/v1/knowledge/activity?limit=0",
		"/v1/knowledge/activity?limit=201",
		"/v1/knowledge/activity?limit=abc",
		"/v1/knowledge/activity?before=2026-11-11",
		"/v1/knowledge/activity?before=garbage",
		"/v1/knowledge/activity?kind=bogus",
	} {
		rr := serveAuth(s, sessionTestRequest(http.MethodGet, target, "", cookie, ""))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	}
}

// sampleKnowledgeHealth is a populated API-KGR-005 body.
func sampleKnowledgeHealth() core.KnowledgeHealth {
	return core.KnowledgeHealth{
		LastRun:       &core.KnowledgeHealthRun{At: "2026-11-11T06:44:00+07:00", Journaled: 11, Applied: 2, Skipped: 0},
		NextAt:        "2026-11-12T03:00:00+07:00",
		Lint:          &core.KnowledgeHealthLint{Date: "2026-11-11", Path: "reports/lint-2026-11-11.md", Pages: 2, Orphans: 1},
		ReviewPending: 0,
		Memory:        []core.KnowledgeHealthMemory{{Agent: "lana", Characters: 5210, Limit: 8000}},
	}
}

// TestKnowledgeHealthShape covers the API-KGR-005 success body.
func TestKnowledgeHealthShape(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.health = sampleKnowledgeHealth()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/health", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got core.KnowledgeHealth
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !reflect.DeepEqual(got, backend.health) {
		t.Fatalf("health = %+v, want %+v", got, backend.health)
	}
	for _, key := range []string{
		`"last_run":{"at":"2026-11-11T06:44:00+07:00"`, `"next_at":"2026-11-12T03:00:00+07:00"`,
		`"lint":{"date":"2026-11-11"`, `"review_pending":0`, `"memory":[{"agent":"lana","characters":5210,"limit":8000}]`,
	} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
}

// TestKnowledgeHealthEmptyNeverNull covers the never-null memory list and the
// null last_run and lint.
func TestKnowledgeHealthEmptyNeverNull(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/health", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	for _, key := range []string{`"last_run":null`, `"lint":null`, `"memory":[]`} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
}

// TestKnowledgeHealthUnavailableIs503 covers API-KGR-005 without a service.
func TestKnowledgeHealthUnavailableIs503(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.healthErr = ErrKnowledgeUnavailable
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/health", "", cookie, ""))
	assertAuthError(t, rr, http.StatusServiceUnavailable, "unavailable")
}

// TestReviewListDefaultPending covers the unchanged default review response.
func TestReviewListDefaultPending(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/review", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if backend.reviewStatus != "pending" {
		t.Fatalf("status = %q, want pending", backend.reviewStatus)
	}
}

// TestReviewListDecided covers API-KGR-003: the decided response and its extra
// fields.
func TestReviewListDecided(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.reviewItems = []core.ReviewItem{
		{ID: "p_1", Kind: "knowledge", Agent: "karin", From: "dana", Title: "Gaji Pasar 2026", Action: "append", Body: "…", Status: "applied", Result: "append Gaji", CreatedAt: 1762800000, DecidedAt: 1762810000, OldText: "gaji lama"},
	}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/review?status=decided", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if backend.reviewStatus != "decided" {
		t.Fatalf("status = %q, want decided", backend.reviewStatus)
	}
	var got reviewListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !reflect.DeepEqual(got.Items, backend.reviewItems) {
		t.Fatalf("items = %+v, want %+v", got.Items, backend.reviewItems)
	}
	for _, key := range []string{`"status":"applied"`, `"result":"append Gaji"`, `"decided_at":1762810000`, `"old_text":"gaji lama"`} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
}

// TestReviewListBadStatusIs400 covers the API-KGR-003 status validation
// (AC-KGR-005).
func TestReviewListBadStatusIs400(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/review?status=weird", "", cookie, ""))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}

// TestKnowledgePageDetailNeverNull covers the API-KGR-002 never-null lists.
func TestKnowledgePageDetailNeverNull(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.pageDoc = core.KnowledgeDoc{Path: "wiki/A.md", Title: "A", Scope: "shared"}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/knowledge/page?path=wiki/A.md", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	for _, key := range []string{`"type":""`, `"sources":[]`, `"links_out":[]`, `"unresolved":[]`, `"links_in":[]`} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
}
