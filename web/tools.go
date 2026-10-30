package web

import (
	"errors"
	"net/http"
	"strings"
)

// ToolEntry is one tool row of API-WEB-026.
type ToolEntry struct {
	Name      string `json:"name"`
	Risk      string `json:"risk"`
	AsksFirst bool   `json:"asks_first"`
	Enabled   bool   `json:"enabled"`
}

// ToolGroup is one group of API-WEB-026.
type ToolGroup struct {
	ID    string      `json:"id"`
	Label string      `json:"label"`
	Tools []ToolEntry `json:"tools"`
}

// ToolCatalog is the daemon port for API-WEB-026. The adapter in cmd/lana
// implements it through the runtime and the web package never imports lana.
type ToolCatalog interface {
	ToolCatalog(agent string) ([]ToolGroup, error)
}

// registerToolRoutes installs the tool catalog route. It must be mounted from
// web/server.go with s.registerToolRoutes().
func (s *Server) registerToolRoutes() {
	s.Handle("GET /v1/tools", http.HandlerFunc(s.handleToolCatalog))
}

type toolCatalogResponse struct {
	Agent  string      `json:"agent"`
	Groups []ToolGroup `json:"groups"`
}

// handleToolCatalog serves API-WEB-026. A missing or unknown agent is 404
// not_found; the failure table treats both the same way.
func (s *Server) handleToolCatalog(w http.ResponseWriter, r *http.Request) {
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	if agent == "" {
		writeAPIError(w, http.StatusNotFound, "not_found", "agent is not configured")
		return
	}
	catalog := s.toolCatalogOrError(w)
	if catalog == nil {
		return
	}
	groups, err := catalog.ToolCatalog(agent)
	if err != nil {
		if errors.Is(err, ErrUnknownAgent) {
			writeAPIError(w, http.StatusNotFound, "not_found", "agent is not configured")
			return
		}
		s.logger.Error("web: tool catalog", "agent", agent, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, toolCatalogResponse{Agent: agent, Groups: nonNilToolGroups(groups)})
}

// toolCatalogOrError returns the catalog port from the backend. It writes the
// error response and returns nil when the port is not configured.
func (s *Server) toolCatalogOrError(w http.ResponseWriter) ToolCatalog {
	if s.backend != nil {
		if catalog, ok := s.backend.(ToolCatalog); ok {
			return catalog
		}
	}
	s.logger.Error("web: tool catalog is not configured")
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	return nil
}

// nonNilToolGroups keeps the groups and tools arrays in the response as [] even
// when the runtime returns nothing.
func nonNilToolGroups(groups []ToolGroup) []ToolGroup {
	if groups == nil {
		groups = []ToolGroup{}
	}
	for i := range groups {
		if groups[i].Tools == nil {
			groups[i].Tools = []ToolEntry{}
		}
	}
	return groups
}
