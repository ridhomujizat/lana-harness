package web

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// Knowledge page-list bounds (API-DKN-004).
const (
	knowledgePagesDefaultLimit = 200
	knowledgePagesMaxLimit     = 500
)

// knowledgeScopeRE is the agent-name scope pattern of API-DKN-004 (the same
// rule as agenthome.ValidName).
var knowledgeScopeRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Knowledge upload bounds (API-KNU-005): at most 30 MB per file and 100 MB for
// the whole request. They are variables so tests can lower them.
var (
	knowledgeUploadFileLimit  int64 = 30 << 20
	knowledgeUploadTotalLimit int64 = 100 << 20
)

// knowledgeUploadExts are the inbox file types an upload may carry
// (API-KNU-005).
var knowledgeUploadExts = map[string]bool{".md": true, ".txt": true, ".pdf": true, ".html": true}

// ErrKnowledgeUnavailable reports that the runtime has no knowledge service.
// The consolidate handler maps it to 503 unavailable (API-KNU-006).
var ErrKnowledgeUnavailable = errors.New("knowledge is not available")

// ErrReviewInvalid reports a review decision refused because the item no
// longer passes its rules (API-DKN-007). The bridge maps the runtime's
// invalid-input error to it.
var ErrReviewInvalid = errors.New("review item is not valid")

// KnowledgeBackend is the optional port of the Knowledge page
// (API-KNU-003–006, REQ-KNU-040, -041). A Backend that also implements it
// installs the port in SetBackend. It is not part of the Backend interface so
// other implementations stay unchanged.
type KnowledgeBackend interface {
	// KnowledgeStatus reports the vault state (API-KNU-003).
	KnowledgeStatus(ctx context.Context) (core.KnowledgeStatus, error)
	// KnowledgeReport returns the latest lint report; it wraps fs.ErrNotExist
	// when there is none (API-KNU-004).
	KnowledgeReport(ctx context.Context) (KnowledgeReport, error)
	// KnowledgeUpload stores one uploaded file under inbox/ and returns its
	// vault-relative path (API-KNU-005).
	KnowledgeUpload(ctx context.Context, name string, data []byte) (string, error)
	// KnowledgeConsolidate starts one consolidation; it returns
	// core.ErrKnowledgeBusy while one runs and ErrKnowledgeUnavailable when the
	// runtime has no knowledge service (API-KNU-006).
	KnowledgeConsolidate(ctx context.Context) error
}

// KnowledgeDomainBackend is the optional scoped-knowledge port of the page
// list, page read, review queue, and the agent overview block
// (API-DKN-004–008). A Backend that also implements it installs the port in
// SetBackend. It is separate from KnowledgeBackend so implementations that
// only serve the status routes stay unchanged.
type KnowledgeDomainBackend interface {
	// KnowledgePages returns one page of the knowledge list, newest first,
	// with the total before the limit (API-DKN-004).
	KnowledgePages(ctx context.Context, scope, q string, limit int) ([]core.KnowledgePageInfo, int, error)
	// KnowledgePage returns one vault file and its raw markdown (API-DKN-005).
	KnowledgePage(ctx context.Context, path string) (core.KnowledgeDoc, string, error)
	// ReviewItems returns the pending review queue, newest first
	// (API-DKN-006).
	ReviewItems(ctx context.Context) ([]core.ReviewItem, error)
	// ReviewDecide approves or rejects one review item and returns its new
	// status (API-DKN-007).
	ReviewDecide(ctx context.Context, id string, approve bool) (string, error)
	// AgentKnowledge reports the "What it knows" block of one agent
	// (API-DKN-008); available is false when the agent has no domain.
	AgentKnowledge(ctx context.Context, name string) (bool, core.KnowledgeDomainStats, error)
}

// KnowledgeReport is the latest lint report (API-KNU-004).
type KnowledgeReport struct {
	Path     string `json:"path"`
	Markdown string `json:"markdown"`
}

// knowledgeUploadResponse is the POST /v1/knowledge/upload body (API-KNU-005).
type knowledgeUploadResponse struct {
	Saved []string `json:"saved"`
}

// knowledgeConsolidateResponse is the POST /v1/knowledge/consolidate body
// (API-KNU-006).
type knowledgeConsolidateResponse struct {
	Started bool `json:"started"`
}

// knowledgePagesResponse is the GET /v1/knowledge/pages body (API-DKN-004).
type knowledgePagesResponse struct {
	Pages []core.KnowledgePageInfo `json:"pages"`
	Total int                      `json:"total"`
}

// knowledgePageResponse is the GET /v1/knowledge/page body (API-DKN-005).
type knowledgePageResponse struct {
	Path     string `json:"path"`
	Title    string `json:"title"`
	Scope    string `json:"scope"`
	Markdown string `json:"markdown"`
}

// reviewListResponse is the GET /v1/review body (API-DKN-006).
type reviewListResponse struct {
	Items []core.ReviewItem `json:"items"`
}

// reviewDecideResponse is the POST /v1/review/{id}/approve|reject body
// (API-DKN-007).
type reviewDecideResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// registerKnowledgeRoutes registers the knowledge endpoints through Handle so
// the Host, Origin, cookie, and CSRF checks apply (API-KNU-003–006,
// API-DKN-004–007).
func (s *Server) registerKnowledgeRoutes() {
	s.Handle("GET /v1/knowledge", http.HandlerFunc(s.handleKnowledgeStatus))
	s.Handle("GET /v1/knowledge/report", http.HandlerFunc(s.handleKnowledgeReport))
	s.Handle("POST /v1/knowledge/upload", http.HandlerFunc(s.handleKnowledgeUpload))
	s.Handle("POST /v1/knowledge/consolidate", http.HandlerFunc(s.handleKnowledgeConsolidate))
	s.Handle("GET /v1/knowledge/pages", http.HandlerFunc(s.handleKnowledgePages))
	s.Handle("GET /v1/knowledge/page", http.HandlerFunc(s.handleKnowledgePage))
	s.Handle("GET /v1/review", http.HandlerFunc(s.handleReviewList))
	s.Handle("POST /v1/review/{id}/approve", s.handleReviewDecide(true))
	s.Handle("POST /v1/review/{id}/reject", s.handleReviewDecide(false))
}

// handleKnowledgeStatus serves GET /v1/knowledge (API-KNU-003).
func (s *Server) handleKnowledgeStatus(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeBackend == nil {
		s.logger.Error("web: knowledge backend is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	status, err := s.knowledgeBackend.KnowledgeStatus(r.Context())
	if err != nil {
		s.logger.Error("web: knowledge status", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleKnowledgeReport serves GET /v1/knowledge/report (API-KNU-004).
func (s *Server) handleKnowledgeReport(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeBackend == nil {
		s.logger.Error("web: knowledge backend is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	report, err := s.knowledgeBackend.KnowledgeReport(r.Context())
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, report)
	case errors.Is(err, fs.ErrNotExist):
		// API-KNU-004: there is no report yet.
		writeAPIError(w, http.StatusNotFound, "not_found", "no lint report yet")
	default:
		s.logger.Error("web: knowledge report", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// handleKnowledgeUpload serves POST /v1/knowledge/upload (API-KNU-005).
func (s *Server) handleKnowledgeUpload(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeBackend == nil {
		s.logger.Error("web: knowledge backend is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	// API-KNU-005: 100 MB for the whole request.
	r.Body = http.MaxBytesReader(w, r.Body, knowledgeUploadTotalLimit)
	reader, err := r.MultipartReader()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request must be multipart/form-data")
		return
	}
	saved := []string{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "could not read the upload")
			return
		}
		name := filepath.Base(strings.TrimSpace(part.FileName()))
		if part.FormName() != "file" || name == "" {
			_ = part.Close()
			continue
		}
		if !knowledgeUploadExts[strings.ToLower(filepath.Ext(name))] {
			// API-KNU-005: name the file whose extension is refused.
			_ = part.Close()
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "unsupported file type: "+name)
			return
		}
		// API-KNU-005: at most 30 MB per file.
		data, err := io.ReadAll(http.MaxBytesReader(w, part, knowledgeUploadFileLimit))
		_ = part.Close()
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "file is larger than 30 MB: "+name)
				return
			}
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "could not read "+name)
			return
		}
		path, err := s.knowledgeBackend.KnowledgeUpload(r.Context(), name, data)
		if err != nil {
			s.logger.Error("web: knowledge upload", "file", name, "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		saved = append(saved, path)
	}
	if len(saved) == 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "field file is required")
		return
	}
	writeJSON(w, http.StatusOK, knowledgeUploadResponse{Saved: saved})
}

// handleKnowledgeConsolidate serves POST /v1/knowledge/consolidate
// (API-KNU-006).
func (s *Server) handleKnowledgeConsolidate(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeBackend == nil {
		s.logger.Error("web: knowledge backend is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	err := s.knowledgeBackend.KnowledgeConsolidate(r.Context())
	switch {
	case err == nil:
		// API-KNU-006: the run started; the response does not wait for it.
		writeJSON(w, http.StatusAccepted, knowledgeConsolidateResponse{Started: true})
	case errors.Is(err, core.ErrKnowledgeBusy):
		writeAPIError(w, http.StatusConflict, "busy", "a consolidation is already running")
	case errors.Is(err, ErrKnowledgeUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "knowledge is not available")
	default:
		s.logger.Error("web: knowledge consolidate", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// handleKnowledgePages serves GET /v1/knowledge/pages (API-DKN-004).
func (s *Server) handleKnowledgePages(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeDomainBackend == nil {
		s.logger.Error("web: knowledge domain backend is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	query := r.URL.Query()
	scope := strings.TrimSpace(query.Get("scope"))
	if scope == "" {
		scope = "all"
	}
	if !validKnowledgeScope(scope) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "scope must be all, shared, or an agent name")
		return
	}
	limit := knowledgePagesDefaultLimit
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > knowledgePagesMaxLimit {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 500")
			return
		}
		limit = value
	}
	pages, total, err := s.knowledgeDomainBackend.KnowledgePages(r.Context(), scope, query.Get("q"), limit)
	if err != nil {
		s.writeKnowledgeDomainError(w, "knowledge pages", err)
		return
	}
	if pages == nil {
		pages = []core.KnowledgePageInfo{}
	}
	writeJSON(w, http.StatusOK, knowledgePagesResponse{Pages: pages, Total: total})
}

// handleKnowledgePage serves GET /v1/knowledge/page (API-DKN-005).
func (s *Server) handleKnowledgePage(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeDomainBackend == nil {
		s.logger.Error("web: knowledge domain backend is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if !validKnowledgePagePath(path) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "path must be a markdown file inside wiki/ or journal/")
		return
	}
	doc, markdown, err := s.knowledgeDomainBackend.KnowledgePage(r.Context(), path)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, knowledgePageResponse{Path: doc.Path, Title: doc.Title, Scope: doc.Scope, Markdown: markdown})
	case errors.Is(err, fs.ErrNotExist):
		writeAPIError(w, http.StatusNotFound, "not_found", "page not found")
	case errors.Is(err, ErrKnowledgeUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "knowledge is not available")
	default:
		s.logger.Error("web: knowledge page", "path", path, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// handleReviewList serves GET /v1/review (API-DKN-006).
func (s *Server) handleReviewList(w http.ResponseWriter, r *http.Request) {
	if s.knowledgeDomainBackend == nil {
		s.logger.Error("web: knowledge domain backend is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	items, err := s.knowledgeDomainBackend.ReviewItems(r.Context())
	if err != nil {
		s.writeKnowledgeDomainError(w, "review list", err)
		return
	}
	if items == nil {
		items = []core.ReviewItem{}
	}
	writeJSON(w, http.StatusOK, reviewListResponse{Items: items})
}

// handleReviewDecide serves POST /v1/review/{id}/approve and /reject
// (API-DKN-007).
func (s *Server) handleReviewDecide(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.knowledgeDomainBackend == nil {
			s.logger.Error("web: knowledge domain backend is not configured")
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		id := r.PathValue("id")
		status, err := s.knowledgeDomainBackend.ReviewDecide(r.Context(), id, approve)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, reviewDecideResponse{ID: id, Status: status})
		case errors.Is(err, fs.ErrNotExist):
			writeAPIError(w, http.StatusNotFound, "not_found", "review item not found")
		case errors.Is(err, ErrReviewInvalid):
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "the skill no longer passes the skill rules")
		case errors.Is(err, ErrKnowledgeUnavailable):
			writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "knowledge is not available")
		default:
			s.logger.Error("web: review decide", "id", id, "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
	}
}

// writeKnowledgeDomainError maps a scoped-knowledge backend failure to the
// contract status: a missing service is 503 unavailable, anything else 500.
func (s *Server) writeKnowledgeDomainError(w http.ResponseWriter, operation string, err error) {
	if errors.Is(err, ErrKnowledgeUnavailable) {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "knowledge is not available")
		return
	}
	s.logger.Error("web: "+operation, "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

// validKnowledgeScope reports whether scope is a valid API-DKN-004 filter:
// "all", "shared", or an agent name.
func validKnowledgeScope(scope string) bool {
	return scope == "all" || scope == "shared" || knowledgeScopeRE.MatchString(scope)
}

// validKnowledgePagePath reports whether ref is a vault-relative markdown path
// inside a wiki or journal folder (API-DKN-005).
func validKnowledgePagePath(ref string) bool {
	if ref == "" || !strings.HasSuffix(ref, ".md") {
		return false
	}
	parts := strings.Split(ref, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	switch parts[0] {
	case "wiki", "journal":
		return true
	case "agents":
		if len(parts) < 4 {
			return false
		}
		return parts[2] == "wiki" || parts[2] == "journal"
	default:
		return false
	}
}
