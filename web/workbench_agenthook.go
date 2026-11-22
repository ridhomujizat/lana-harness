package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/ridhomujizat/lana-harness/internal/workbench/agenthook"
)

// AgentHookFolder is one Claude Code config folder the daemon installs the
// Workbench SessionStart hook into (spec 69 REQ-WA-013).
type AgentHookFolder struct {
	// Dir is the folder that holds settings.json.
	Dir string
	// Account is the Claude Code account the folder belongs to; "claude-code"
	// is the daemon's own folder.
	Account string
}

// AgentHookConfig configures the Workbench agent hook routes (spec 69). The
// daemon sets it with SetAgentHook.
type AgentHookConfig struct {
	// Binary is the absolute path of the lana binary the hook command runs.
	Binary string
	// Folders returns the Claude Code config folders to manage. It is called on
	// every request, so an account added in the dashboard is picked up the next
	// time the switch is shown.
	Folders func() []AgentHookFolder
	// Allowed is false for a daemon that must never touch the owner's Claude
	// settings: a dev, scratch or test daemon.
	Allowed bool
}

// agentHookBodyLimit bounds the PUT /v1/workbench/agent-hook body.
const agentHookBodyLimit = 4 << 10

// agentHookNotAllowed is the per-folder error, and the PUT message, when this
// daemon may not manage the hook.
const agentHookNotAllowed = "only the main Lana daemon manages the hook"

// agentHooks holds the agent hook settings of each server. The daemon builds
// one server, so this keeps the settings without adding a field to the Server
// struct.
var (
	agentHooksMu sync.Mutex
	agentHooks   = map[*Server]AgentHookConfig{}
)

// SetAgentHook installs the Workbench agent hook settings (spec 69 REQ-WA-013).
// With Allowed and the switch on, it also installs into every folder in the
// background; errors are logged. A daemon that is not allowed, or whose switch
// is off, waits for the owner to use the routes.
func (s *Server) SetAgentHook(cfg AgentHookConfig) {
	agentHooksMu.Lock()
	agentHooks[s] = cfg
	agentHooksMu.Unlock()
	if !cfg.Allowed {
		return
	}
	go s.installAgentHook(cfg)
}

// agentHookConfig returns the settings of this server; ok is false when
// SetAgentHook was never called.
func (s *Server) agentHookConfig() (AgentHookConfig, bool) {
	agentHooksMu.Lock()
	defer agentHooksMu.Unlock()
	cfg, ok := agentHooks[s]
	return cfg, ok
}

// installAgentHook installs the hook into every folder while the switch is on.
// It runs in the background, so a failure is only logged.
func (s *Server) installAgentHook(cfg AgentHookConfig) {
	enabled, err := s.agentHookEnabled()
	if err != nil {
		s.logger.Warn("web: read agent hook setting", "error", err)
		return
	}
	if !enabled {
		return
	}
	for _, folder := range agentHookFolders(cfg) {
		if _, err := agenthook.Install(agentHookSettingsPath(folder.Dir), cfg.Binary); err != nil {
			s.logger.Warn("web: install Claude Code SessionStart hook", "dir", folder.Dir, "error", err)
		}
	}
}

// registerWorkbenchAgentHookRoutes registers the Workbench settings switch of
// spec 69 REQ-WA-014. They go through Handle, so the host, origin, cookie and
// CSRF checks apply like the other /v1/workbench routes.
func (s *Server) registerWorkbenchAgentHookRoutes() {
	s.Handle("GET /v1/workbench/agent-hook", http.HandlerFunc(s.handleAgentHookGet))
	s.Handle("PUT /v1/workbench/agent-hook", http.HandlerFunc(s.handleAgentHookPut))
}

// agentHookFolderView is one folder of the route answer.
type agentHookFolderView struct {
	Dir       string `json:"dir"`
	Account   string `json:"account"`
	Installed bool   `json:"installed"`
	Error     string `json:"error,omitempty"`
}

// agentHookResponse is the GET and PUT answer of the hook setting.
type agentHookResponse struct {
	Enabled bool                  `json:"enabled"`
	Folders []agentHookFolderView `json:"folders"`
}

// handleAgentHookGet answers GET /v1/workbench/agent-hook. While the switch is
// on it (re)installs into every folder first, so a newly added account gets the
// hook the next time the switch is shown. It then reports each folder.
func (s *Server) handleAgentHookGet(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.agentHookConfig()
	if !ok || !cfg.Allowed {
		writeJSON(w, http.StatusOK, agentHookNotAllowedResponse(cfg))
		return
	}
	enabled, err := s.agentHookEnabled()
	if err != nil {
		s.logger.Error("web: read agent hook setting", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	folders := agentHookFolders(cfg)
	views := make([]agentHookFolderView, 0, len(folders))
	for _, folder := range folders {
		path := agentHookSettingsPath(folder.Dir)
		view := agentHookFolderView{Dir: folder.Dir, Account: folder.Account}
		if enabled {
			if _, err := agenthook.Install(path, cfg.Binary); err != nil {
				view.Error = agentHookErrorText(err)
			}
		}
		view.Installed = agentHookInstalled(path, cfg.Binary, &view)
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, agentHookResponse{Enabled: enabled, Folders: views})
}

// handleAgentHookPut answers PUT /v1/workbench/agent-hook. It saves the switch,
// then installs into every folder (on) or removes from every folder (off). A
// per-folder error goes into the folder's error field and never fails the whole
// call.
func (s *Server) handleAgentHookPut(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.agentHookConfig()
	if !ok || !cfg.Allowed {
		writeAPIError(w, http.StatusConflict, "not_allowed", agentHookNotAllowed)
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSONObject(w, r, agentHookBodyLimit, &body) {
		return
	}
	if err := s.saveAgentHookEnabled(body.Enabled); err != nil {
		s.logger.Error("web: save agent hook setting", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	folders := agentHookFolders(cfg)
	views := make([]agentHookFolderView, 0, len(folders))
	for _, folder := range folders {
		path := agentHookSettingsPath(folder.Dir)
		view := agentHookFolderView{Dir: folder.Dir, Account: folder.Account}
		var err error
		if body.Enabled {
			_, err = agenthook.Install(path, cfg.Binary)
		} else {
			_, err = agenthook.Remove(path)
		}
		if err != nil {
			view.Error = agentHookErrorText(err)
		}
		view.Installed = agentHookInstalled(path, cfg.Binary, &view)
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, agentHookResponse{Enabled: body.Enabled, Folders: views})
}

// agentHookNotAllowedResponse lists the folders of a daemon that may not manage
// the hook, each with the reason.
func agentHookNotAllowedResponse(cfg AgentHookConfig) agentHookResponse {
	folders := agentHookFolders(cfg)
	views := make([]agentHookFolderView, 0, len(folders))
	for _, folder := range folders {
		views = append(views, agentHookFolderView{
			Dir: folder.Dir, Account: folder.Account, Error: agentHookNotAllowed,
		})
	}
	return agentHookResponse{Enabled: false, Folders: views}
}

// agentHookInstalled reports whether the hook runs cfg.Binary in the folder,
// folding a read error into the view so it never fails the call.
func agentHookInstalled(path, binary string, view *agentHookFolderView) bool {
	installed, err := agenthook.Installed(path, binary)
	if err != nil {
		if view.Error == "" {
			view.Error = agentHookErrorText(err)
		}
		return false
	}
	return installed
}

// agentHookErrorText is the per-folder error text; an invalid settings.json has
// the contract wording.
func agentHookErrorText(err error) string {
	if errors.Is(err, agenthook.ErrInvalidSettings) {
		return "settings.json is not valid JSON"
	}
	return err.Error()
}

// agentHookFolders reads the folders from the config, never nil.
func agentHookFolders(cfg AgentHookConfig) []AgentHookFolder {
	if cfg.Folders == nil {
		return nil
	}
	return cfg.Folders()
}

// agentHookSettingsPath is the settings.json of a Claude Code config folder.
func agentHookSettingsPath(dir string) string {
	return filepath.Join(dir, "settings.json")
}

// agentHookEnabledPath is the switch file:
// <LANA_HOME>/workbench/agent-hook.json.
func (s *Server) agentHookEnabledPath() string {
	if s.cfg.Home == "" {
		return ""
	}
	return filepath.Join(s.cfg.Home, "workbench", "agent-hook.json")
}

// agentHookEnabled reads the switch; a missing file means on.
func (s *Server) agentHookEnabled() (bool, error) {
	path := s.agentHookEnabledPath()
	if path == "" {
		return true, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var doc struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Enabled == nil {
		return true, nil
	}
	return *doc.Enabled, nil
}

// saveAgentHookEnabled writes the switch.
func (s *Server) saveAgentHookEnabled(enabled bool) error {
	path := s.agentHookEnabledPath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Enabled bool `json:"enabled"`
	}{enabled})
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
