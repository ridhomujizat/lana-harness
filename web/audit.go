package web

import (
	"net/http"
	"strconv"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

func (s *Server) registerAuditRoutes() {
	s.Handle("GET /v1/audit", http.HandlerFunc(s.handleGetAudit))
}

// handleGetAudit lists the audit file through API-WEB-030: newest first, with
// the optional before cursor and the limit page size.
func (s *Server) handleGetAudit(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var before int64
	if raw := query.Get("before"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "before must be a non-negative integer")
			return
		}
		before = value
	}
	limit := webfiles.AuditDefaultLimit
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > webfiles.AuditMaxLimit {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 100")
			return
		}
		limit = value
	}

	home := s.auditHome()
	if home == "" {
		s.logger.Error("web: audit home is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	page, err := webfiles.ReadAudit(webfiles.AuditPath(home), before, limit)
	if err != nil {
		s.logger.Error("web: read audit file", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// auditHome returns the LANA_HOME that holds logs/audit.jsonl.
func (s *Server) auditHome() string {
	if store, ok := s.configStore.(interface{ SettingsHome() string }); ok {
		if home := store.SettingsHome(); home != "" {
			return home
		}
	}
	return s.cfg.Home
}
