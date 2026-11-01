package web

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

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

// registerKnowledgeRoutes registers the knowledge endpoints through Handle so
// the Host, Origin, cookie, and CSRF checks apply (API-KNU-003–006).
func (s *Server) registerKnowledgeRoutes() {
	s.Handle("GET /v1/knowledge", http.HandlerFunc(s.handleKnowledgeStatus))
	s.Handle("GET /v1/knowledge/report", http.HandlerFunc(s.handleKnowledgeReport))
	s.Handle("POST /v1/knowledge/upload", http.HandlerFunc(s.handleKnowledgeUpload))
	s.Handle("POST /v1/knowledge/consolidate", http.HandlerFunc(s.handleKnowledgeConsolidate))
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
