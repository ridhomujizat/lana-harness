package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// agentFileBodyLimit is the largest accepted PUT /v1/agents/{name}/files/{file}
// body. It leaves room for the 4,000 character memory limit in any encoding.
const agentFileBodyLimit = 64 << 10

// AgentFileStore supplies the home directory and the configured agent names.
type AgentFileStore interface {
	SettingsHome() string
	AgentNames() []string
}

// SetAgentFileStore installs the agent-file port directly. SetBackend also
// calls it when the backend implements AgentFileStore.
func (s *Server) SetAgentFileStore(store AgentFileStore) {
	s.agentFileStore = store
}

func (s *Server) registerAgentFileRoutes() {
	s.Handle("GET /v1/agents/{name}/files/{file}", http.HandlerFunc(s.handleGetAgentFile))
	s.Handle("PUT /v1/agents/{name}/files/{file}", http.HandlerFunc(s.handlePutAgentFile))
}

func (s *Server) agentFileStoreOrError(w http.ResponseWriter) AgentFileStore {
	if s.agentFileStore == nil {
		s.logger.Error("web: agent file store is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return nil
	}
	return s.agentFileStore
}

type agentFileResponse struct {
	Content    string `json:"content"`
	Version    string `json:"version"`
	Characters int    `json:"characters"`
	Limit      *int   `json:"limit"`
}

type putAgentFileRequest struct {
	Content         string `json:"content"`
	ExpectedVersion string `json:"expected_version"`
}

type putAgentFileResponse struct {
	Version    string `json:"version"`
	Characters int    `json:"characters"`
}

func (s *Server) handleGetAgentFile(w http.ResponseWriter, r *http.Request) {
	store := s.agentFileStoreOrError(w)
	if store == nil {
		return
	}
	target, ok := agentFileTarget(store, r.PathValue("name"), r.PathValue("file"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "agent file is not available")
		return
	}
	data, err := os.ReadFile(target.path)
	if errors.Is(err, os.ErrNotExist) {
		data = nil
	} else if err != nil {
		s.logger.Error("web: read agent file", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, agentFileResponse{
		Content:    string(data),
		Version:    fileVersion(data),
		Characters: utf8.RuneCountInString(string(data)),
		Limit:      target.limit,
	})
}

func (s *Server) handlePutAgentFile(w http.ResponseWriter, r *http.Request) {
	store := s.agentFileStoreOrError(w)
	if store == nil {
		return
	}
	target, ok := agentFileTarget(store, r.PathValue("name"), r.PathValue("file"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "agent file is not available")
		return
	}
	var body putAgentFileRequest
	if !decodeJSONObject(w, r, agentFileBodyLimit, &body) {
		return
	}
	characters := utf8.RuneCountInString(body.Content)
	if target.limit != nil && characters > *target.limit {
		writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "over_limit", "file is over its limit", []map[string]any{
			{"file": target.name, "characters": characters, "limit": *target.limit},
		})
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	current, err := readAgentFile(target.path)
	if err != nil {
		s.logger.Error("web: read agent file before save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	version := fileVersion(current)
	if body.ExpectedVersion != version {
		writeAPIError(w, http.StatusConflict, "stale_version", target.name+" changed since it was loaded")
		return
	}
	if string(current) == body.Content {
		writeJSON(w, http.StatusOK, putAgentFileResponse{Version: version, Characters: characters})
		return
	}
	if err := os.MkdirAll(filepath.Dir(target.path), 0o700); err != nil {
		s.logger.Error("web: create agent file directory", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if err := webfiles.AtomicWriteMode(target.path, []byte(body.Content), 0o600); err != nil {
		s.logger.Error("web: write agent file", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	version = fileVersion([]byte(body.Content))
	s.auditSave("agent_file", target.path, "", nil)
	writeJSON(w, http.StatusOK, putAgentFileResponse{Version: version, Characters: characters})
}

type agentFileRef struct {
	name  string
	path  string
	limit *int
}

func agentFileTarget(store AgentFileStore, name, file string) (agentFileRef, bool) {
	if !agenthome.ValidName(name) || !containsString(store.AgentNames(), name) {
		return agentFileRef{}, false
	}
	home := agenthome.Paths(store.SettingsHome(), name)
	switch file {
	case "agent":
		return agentFileRef{name: "AGENT.md", path: home.Agent}, true
	case "user":
		limit := agenthome.UserLimit
		return agentFileRef{name: "USER.md", path: agenthome.SharedUser(store.SettingsHome()), limit: &limit}, true
	case "memory":
		limit := agenthome.MemoryLimit
		return agentFileRef{name: "MEMORY.md", path: home.Memory, limit: &limit}, true
	default:
		return agentFileRef{}, false
	}
}

func readAgentFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

func fileVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
