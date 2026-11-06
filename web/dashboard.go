package web

import (
	"context"
	"net/http"

	"github.com/ridhomujizat/lana-harness/internal/stats"
)

// defaultDashboardRange is the range used when the query omits range
// (API-DSH-001).
const defaultDashboardRange = "24h"

// maxDashboardAgentLen is the longest agent filter the endpoint accepts
// (API-DSH-001 r2).
const maxDashboardAgentLen = 128

// DashboardBackend is the optional port of the monitoring dashboard endpoint
// (API-DSH-001). A Backend that also implements it installs the port in
// SetBackend. It is not part of the Backend interface so other implementations
// stay unchanged.
type DashboardBackend interface {
	// Dashboard returns the aggregated dashboard for rangeName, one of 24h,
	// 7d, or 30d. An empty agent means all agents; otherwise only that
	// agent's events are counted (API-DSH-001 r2).
	Dashboard(ctx context.Context, rangeName, agent string) (stats.Dashboard, error)
}

// registerDashboardRoutes registers GET /v1/dashboard through Handle so the
// Host, Origin, cookie, and CSRF checks apply (API-DSH-001).
func (s *Server) registerDashboardRoutes() {
	s.Handle("GET /v1/dashboard", http.HandlerFunc(s.handleDashboard))
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if s.dashboardBackend == nil {
		s.logger.Error("web: dashboard backend is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	rangeName := r.URL.Query().Get("range")
	if rangeName == "" {
		rangeName = defaultDashboardRange
	}
	// API-DSH-001: any range outside the three names is invalid_request.
	if !stats.ValidRange(rangeName) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "range must be one of 24h, 7d, 30d")
		return
	}
	// API-DSH-001 r2: an agent filter over 128 bytes is invalid_request; an
	// empty value means all agents.
	agent := r.URL.Query().Get("agent")
	if len(agent) > maxDashboardAgentLen {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "agent must be at most 128 bytes")
		return
	}
	dashboard, err := s.dashboardBackend.Dashboard(r.Context(), rangeName, agent)
	if err != nil {
		s.logger.Error("web: dashboard", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, dashboard)
}
