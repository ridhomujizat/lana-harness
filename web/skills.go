package web

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// skillBodyLimit is the largest accepted PUT /v1/skills body.
const skillBodyLimit = 64 << 10

// SkillStore supplies the home directory and the configured agent names.
type SkillStore interface {
	SettingsHome() string
	AgentNames() []string
}

// SetSkillStore installs the skills port directly. SetBackend also calls it
// when the backend implements SkillStore.
func (s *Server) SetSkillStore(store SkillStore) {
	s.skillStore = store
}

func (s *Server) registerSkillRoutes() {
	s.Handle("GET /v1/skills", http.HandlerFunc(s.handleListSkills))
	s.Handle("GET /v1/skills/{scope}/{name}", http.HandlerFunc(s.handleGetSkill))
	s.Handle("PUT /v1/skills/{scope}/{name}", http.HandlerFunc(s.handlePutSkill))
	s.Handle("DELETE /v1/skills/{scope}/{name}", http.HandlerFunc(s.handleDeleteSkill))
}

func (s *Server) skillStoreOrError(w http.ResponseWriter) SkillStore {
	if s.skillStore == nil {
		s.logger.Error("web: skill store is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return nil
	}
	return s.skillStore
}

type skillJSON struct {
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	Description string `json:"description"`
	Valid       bool   `json:"valid"`
	Error       string `json:"error"`
	Size        int    `json:"size"`
	UpdatedAt   int64  `json:"updated_at"`
}

type listSkillsResponse struct {
	Skills  []skillJSON `json:"skills"`
	Invalid []skillJSON `json:"invalid"`
}

type skillContentResponse struct {
	Content string `json:"content"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

type putSkillRequest struct {
	Content         string `json:"content"`
	ExpectedVersion string `json:"expected_version"`
}

type putSkillResponse struct {
	Version string `json:"version"`
	Path    string `json:"path"`
}

type deleteSkillRequest struct {
	ExpectedVersion string `json:"expected_version"`
}

type deleteSkillResponse struct {
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	Deleted bool   `json:"deleted"`
}

func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	store := s.skillStoreOrError(w)
	if store == nil {
		return
	}
	scope := r.URL.Query().Get("scope")
	skills := make([]skillJSON, 0)
	invalid := make([]skillJSON, 0)
	add := func(agent, label string) {
		valid, bad := readSkillScope(store.SettingsHome(), agent, label)
		skills = append(skills, valid...)
		invalid = append(invalid, bad...)
	}
	if scope == "" {
		add("", "global")
		agents := append([]string(nil), store.AgentNames()...)
		sort.Strings(agents)
		for _, agent := range agents {
			add(agent, "agent:"+agent)
		}
	} else {
		agent, err := resolveSkillScope(store, scope)
		if err != nil {
			writeSkillScopeError(w, err)
			return
		}
		add(agent, scope)
	}
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Scope != skills[j].Scope {
			return skills[i].Scope < skills[j].Scope
		}
		return skills[i].Name < skills[j].Name
	})
	sort.Slice(invalid, func(i, j int) bool {
		if invalid[i].Scope != invalid[j].Scope {
			return invalid[i].Scope < invalid[j].Scope
		}
		return invalid[i].Name < invalid[j].Name
	})
	writeJSON(w, http.StatusOK, listSkillsResponse{Skills: skills, Invalid: invalid})
}

func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	store := s.skillStoreOrError(w)
	if store == nil {
		return
	}
	scope := r.PathValue("scope")
	agent, err := resolveSkillScope(store, scope)
	if err != nil {
		writeSkillScopeError(w, err)
		return
	}
	name := r.PathValue("name")
	if !agenthome.ValidSkillName(name) {
		writeAPIError(w, http.StatusNotFound, "not_found", "skill is not available")
		return
	}
	path := skillFilePath(store.SettingsHome(), agent, name)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		writeAPIError(w, http.StatusNotFound, "not_found", "skill is not available")
		return
	}
	if err != nil {
		s.logger.Error("web: read skill", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, skillContentResponse{
		Content: string(data),
		Version: fileVersion(data),
		Path:    path,
	})
}

func (s *Server) handlePutSkill(w http.ResponseWriter, r *http.Request) {
	store := s.skillStoreOrError(w)
	if store == nil {
		return
	}
	scope := r.PathValue("scope")
	agent, err := resolveSkillScope(store, scope)
	if err != nil {
		writeSkillScopeError(w, err)
		return
	}
	name := r.PathValue("name")
	if !agenthome.ValidSkillName(name) {
		writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "skill is not valid", []map[string]any{
			{"field": "name", "message": "name must match [a-z][a-z0-9_-]{0,63}"},
		})
		return
	}
	var body putSkillRequest
	if !decodeJSONObject(w, r, skillBodyLimit, &body) {
		return
	}
	if err := validateSkillContent(name, body.Content); err != nil {
		writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "skill is not valid", []map[string]any{
			skillValidationDetail(err),
		})
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	path := skillFilePath(store.SettingsHome(), agent, name)
	current, readErr := os.ReadFile(path)
	exists := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		s.logger.Error("web: read skill before save", "error", readErr)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if exists {
		if body.ExpectedVersion == "" || body.ExpectedVersion != fileVersion(current) {
			writeAPIError(w, http.StatusConflict, "stale_version", "skill changed since it was loaded")
			return
		}
		if bytes.Equal(current, []byte(body.Content)) {
			writeJSON(w, http.StatusOK, putSkillResponse{Version: fileVersion(current), Path: path})
			return
		}
	} else if body.ExpectedVersion != "" {
		writeAPIError(w, http.StatusConflict, "stale_version", "skill changed since it was loaded")
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		s.logger.Error("web: create skill directory", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if err := webfiles.AtomicWriteMode(path, []byte(body.Content), 0o600); err != nil {
		s.logger.Error("web: write skill", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.auditSave("skill", path, name, nil)
	status := http.StatusOK
	if !exists {
		status = http.StatusCreated
	}
	writeJSON(w, status, putSkillResponse{Version: fileVersion([]byte(body.Content)), Path: path})
}

// handleDeleteSkill removes one skill folder. It never edits agent config or
// agent files: a name an agent still lists is skipped by the runtime (Q-026).
func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	store := s.skillStoreOrError(w)
	if store == nil {
		return
	}
	scope := r.PathValue("scope")
	agent, err := resolveSkillScope(store, scope)
	if err != nil {
		writeSkillScopeError(w, err)
		return
	}
	name := r.PathValue("name")
	if !agenthome.ValidSkillName(name) {
		writeAPIError(w, http.StatusNotFound, "not_found", "skill is not available")
		return
	}
	var body deleteSkillRequest
	if !decodeJSONObject(w, r, skillBodyLimit, &body) {
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	path := skillFilePath(store.SettingsHome(), agent, name)
	current, readErr := os.ReadFile(path)
	if errors.Is(readErr, os.ErrNotExist) {
		writeAPIError(w, http.StatusNotFound, "not_found", "skill is not available")
		return
	}
	if readErr != nil {
		s.logger.Error("web: read skill before delete", "error", readErr)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if body.ExpectedVersion == "" || body.ExpectedVersion != fileVersion(current) {
		writeAPIError(w, http.StatusConflict, "stale_version", "skill changed since it was loaded")
		return
	}
	dir := filepath.Dir(path)
	if err := os.RemoveAll(dir); err != nil {
		s.logger.Error("web: delete skill", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.auditSave("skill.delete", dir, name, nil)
	writeJSON(w, http.StatusOK, deleteSkillResponse{Name: name, Scope: scope, Deleted: true})
}

func readSkillScope(home, agent, scope string) ([]skillJSON, []skillJSON) {
	dir := agenthome.GlobalSkillsDir(home)
	if agent != "" {
		dir = agenthome.AgentSkillsDir(home, agent)
	}
	skills, invalid := agenthome.ReadSkills(dir, scope)
	valid := make([]skillJSON, 0, len(skills))
	for _, skill := range skills {
		size, updatedAt := skillFileMeta(skill.Dir)
		valid = append(valid, skillJSON{
			Name:        skill.Name,
			Scope:       scope,
			Description: skill.Description,
			Valid:       true,
			Size:        size,
			UpdatedAt:   updatedAt,
		})
	}
	bad := make([]skillJSON, 0, len(invalid))
	for _, item := range invalid {
		size, updatedAt := skillFileMeta(item.Path)
		bad = append(bad, skillJSON{
			Name:      filepath.Base(item.Path),
			Scope:     scope,
			Valid:     false,
			Error:     item.Err.Error(),
			Size:      size,
			UpdatedAt: updatedAt,
		})
	}
	return valid, bad
}

// skillFileMeta returns the character count and modification time of the
// SKILL.md inside dir. A file that cannot be read contributes 0/0; ReadSkills
// has already reported that case as an invalid skill.
func skillFileMeta(dir string) (int, int64) {
	path := filepath.Join(dir, "SKILL.md")
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	return utf8.RuneCountInString(string(data)), info.ModTime().UnixMilli()
}

type skillScopeError struct {
	status int
	code   string
	msg    string
}

func (e *skillScopeError) Error() string { return e.msg }

func resolveSkillScope(store SkillStore, scope string) (string, error) {
	if scope == "global" {
		return "", nil
	}
	if strings.HasPrefix(scope, "agent:") {
		name := strings.TrimPrefix(scope, "agent:")
		if agenthome.ValidName(name) && containsString(store.AgentNames(), name) {
			return name, nil
		}
		return "", &skillScopeError{status: http.StatusNotFound, code: "not_found", msg: "agent is not configured"}
	}
	return "", &skillScopeError{status: http.StatusBadRequest, code: "invalid_request", msg: "scope must be global or agent:<name>"}
}

func writeSkillScopeError(w http.ResponseWriter, err error) {
	var scopeErr *skillScopeError
	if errors.As(err, &scopeErr) {
		writeAPIError(w, scopeErr.status, scopeErr.code, scopeErr.msg)
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func skillFilePath(home, agent, name string) string {
	dir := agenthome.GlobalSkillsDir(home)
	if agent != "" {
		dir = agenthome.AgentSkillsDir(home, agent)
	}
	return filepath.Join(dir, name, "SKILL.md")
}

// validateSkillContent parses the candidate in a temporary directory named
// after the skill, because ParseSkill checks that the frontmatter name matches
// the folder name. The temporary directory is always removed.
func validateSkillContent(name, content string) error {
	root, err := os.MkdirTemp("", "lana-skill-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
		return err
	}
	_, err = agenthome.ParseSkill(dir)
	return err
}

func skillValidationDetail(err error) map[string]any {
	message := err.Error()
	field := "frontmatter"
	switch {
	case strings.Contains(message, "description"):
		field = "description"
	case strings.Contains(message, "body"):
		field = "body"
	case strings.Contains(message, "name"):
		field = "name"
	}
	return map[string]any{"field": field, "message": message}
}
