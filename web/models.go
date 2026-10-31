package web

import (
	"net/http"

	"github.com/ridhomujizat/lana-harness/internal/models"
)

// registerModelRoutes installs the model catalog endpoint of REQ-MDL-020. Every
// route goes through Handle, so it shares the owner cookie and CSRF middleware
// of the other API routes. The /api path is the name in the spec contract; the
// /v1 path matches the dashboard's proxy and the rest of the API.
func (s *Server) registerModelRoutes() {
	s.Handle("GET /v1/models", http.HandlerFunc(s.handleModels))
	s.Handle("GET /api/models", http.HandlerFunc(s.handleModels))
}

// modelJSON is one catalog entry of GET /api/models.
type modelJSON struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	ContextWindow int    `json:"context_window"`
	Legacy        bool   `json:"legacy"`
	Replacement   string `json:"replacement"`
	Thinking      string `json:"thinking"`
	DefaultEffort string `json:"default_effort"`
}

// effortJSON is one reasoning effort with its description.
type effortJSON struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}

type modelsResponse struct {
	Models  []modelJSON  `json:"models"`
	Efforts []effortJSON `json:"efforts"`
}

// handleModels returns the catalog of the named provider. The provider's base
// URL decides the catalog through models.ForProviderKind. An unknown provider
// is 404 not_found; a known provider with no catalog gets an empty list.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config for model catalog", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config for model catalog", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	name := r.URL.Query().Get("provider")
	table, ok := document.Providers[name]
	if name == "" || !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "provider is not configured")
		return
	}

	catalog := models.ForProviderKind(mapProviderString(table, "base_url"))
	efforts := models.Efforts()
	response := modelsResponse{
		Models:  make([]modelJSON, 0, len(catalog)),
		Efforts: make([]effortJSON, 0, len(efforts)),
	}
	for _, model := range catalog {
		response.Models = append(response.Models, modelJSON{
			ID:            model.ID,
			Name:          model.Name,
			Description:   model.Description,
			ContextWindow: model.ContextWindow,
			Legacy:        model.Legacy,
			Replacement:   model.Replacement,
			Thinking:      model.Thinking,
			DefaultEffort: model.DefaultEffort,
		})
	}
	for _, effort := range efforts {
		response.Efforts = append(response.Efforts, effortJSON{
			Value:       effort.ID,
			Description: effort.Description,
		})
	}
	writeJSON(w, http.StatusOK, response)
}
