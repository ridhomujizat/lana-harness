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

// agentHookState is the three-state hook setting: the owner was never asked
// ("unset"), consented ("on"), or declined ("off").
type agentHookState string

const (
	// agentHookUnset is the switch the owner never answered.
	agentHookUnset agentHookState = "unset"
	// agentHookOn is the owner's yes: the daemon installs and keeps the hooks.
	agentHookOn agentHookState = "on"
	// agentHookOff is the owner's no: the daemon removes and never re-adds them.
	agentHookOff agentHookState = "off"
)

// Agent hook folder kinds (spec 77 REQ-WRA-009).
const (
	// AgentHookClaude is a Claude Code config folder (settings.json).
	AgentHookClaude = "claude"
	// AgentHookAntigravity is an agy config folder, <HOME>/.gemini/config
	// (hooks.json).
	AgentHookAntigravity = "antigravity"
)

// AgentHookFolder is one config folder the daemon installs the Workbench hooks
// into (spec 69 REQ-WA-013, REQ-WA-014; spec 77 REQ-WRA-009 for agy). The
// hooks are installed only by the owner's action.
type AgentHookFolder struct {
	// Dir is the folder that holds settings.json (Claude Code) or hooks.json
	// (Antigravity).
	Dir string
	// Account is the account the folder belongs to; "claude-code" is the
	// daemon's own Claude Code folder and "antigravity" the owner's agy one.
	Account string
	// Kind is AgentHookClaude ("" too) or AgentHookAntigravity.
	Kind string
}

// kind is the folder's kind, AgentHookClaude when unset.
func (f AgentHookFolder) kind() string {
	if f.Kind == AgentHookAntigravity {
		return AgentHookAntigravity
	}
	return AgentHookClaude
}

// install adds Lana's hooks to the folder's file.
func (f AgentHookFolder) install(binary string) error {
	var err error
	if f.kind() == AgentHookAntigravity {
		_, err = agenthook.InstallAgy(f.path(), binary)
	} else {
		_, err = agenthook.Install(f.path(), binary)
	}
	return err
}

// remove takes Lana's hooks out of the folder's file.
func (f AgentHookFolder) remove() error {
	var err error
	if f.kind() == AgentHookAntigravity {
		_, err = agenthook.RemoveAgy(f.path())
	} else {
		_, err = agenthook.Remove(f.path())
	}
	return err
}

// installed reports whether the folder's file holds Lana's hooks for binary.
func (f AgentHookFolder) installed(binary string) (bool, error) {
	if f.kind() == AgentHookAntigravity {
		return agenthook.InstalledAgy(f.path(), binary)
	}
	return agenthook.Installed(f.path(), binary)
}

// path is the hook file of the folder.
func (f AgentHookFolder) path() string {
	if f.kind() == AgentHookAntigravity {
		return filepath.Join(f.Dir, agenthook.AgyHooksFile)
	}
	return agentHookSettingsPath(f.Dir)
}

// view is the route view of the folder, without its install state.
func (f AgentHookFolder) view() agentHookFolderView {
	return agentHookFolderView{Dir: f.Dir, Account: f.Account, Kind: f.kind()}
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

// SetAgentHook installs the Workbench agent hook settings (spec 69 REQ-WA-013,
// REQ-WA-014). The hooks are installed only by the owner's action. With Allowed
// and the switch on, it also installs into every folder in the background;
// errors are logged. A daemon that is not allowed, or whose switch is unset or
// off, waits for the Workbench Install / Not now notice.
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
// It runs in the background, so a failure is only logged. An unset or off
// switch installs nothing: the owner has not asked.
func (s *Server) installAgentHook(cfg AgentHookConfig) {
	state, err := s.agentHookState()
	if err != nil {
		s.logger.Warn("web: read agent hook setting", "error", err)
		return
	}
	if state != agentHookOn {
		return
	}
	for _, folder := range agentHookFolders(cfg) {
		if err := folder.install(cfg.Binary); err != nil {
			s.logger.Warn("web: install Workbench agent hooks", "dir", folder.Dir, "kind", folder.kind(), "error", err)
		}
	}
}

// registerWorkbenchAgentHookRoutes registers the hook routes of spec 69
// REQ-WA-013, REQ-WA-014. They go through Handle, so the host, origin, cookie
// and CSRF checks apply like the other /v1/workbench routes. The Workbench
// reads GET to show the Install / Not now notice, and PUT stores the owner's
// choice.
func (s *Server) registerWorkbenchAgentHookRoutes() {
	s.Handle("GET /v1/workbench/agent-hook", http.HandlerFunc(s.handleAgentHookGet))
	s.Handle("PUT /v1/workbench/agent-hook", http.HandlerFunc(s.handleAgentHookPut))
}

// agentHookFolderView is one folder of the route answer.
type agentHookFolderView struct {
	Dir     string `json:"dir"`
	Account string `json:"account"`
	// Kind is "claude" or "antigravity" (spec 77 REQ-WRA-009).
	Kind      string `json:"kind"`
	Installed bool   `json:"installed"`
	Error     string `json:"error,omitempty"`
}

// agentHookResponse is the GET and PUT answer of the hook setting.
type agentHookResponse struct {
	Enabled      bool                  `json:"enabled"`
	State        agentHookState        `json:"state"`
	NeedsInstall bool                  `json:"needs_install"`
	Folders      []agentHookFolderView `json:"folders"`
}

// handleAgentHookGet answers GET /v1/workbench/agent-hook. It never writes the
// switch. While the switch is on it (re)installs into every folder first, so a
// newly added account gets the hook the next time the switch is shown. It then
// reports each folder and, after the install, whether any of them needs it.
func (s *Server) handleAgentHookGet(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.agentHookConfig()
	if !ok || !cfg.Allowed {
		writeJSON(w, http.StatusOK, agentHookNotAllowedResponse(cfg))
		return
	}
	state, err := s.agentHookState()
	if err != nil {
		s.logger.Error("web: read agent hook setting", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	folders := agentHookFolders(cfg)
	views := make([]agentHookFolderView, 0, len(folders))
	for _, folder := range folders {
		view := folder.view()
		if state == agentHookOn {
			if err := folder.install(cfg.Binary); err != nil {
				view.Error = agentHookErrorText(err)
			}
		}
		view.Installed = agentHookInstalled(folder, cfg.Binary, &view)
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, agentHookAnswerOf(state, views))
}

// handleAgentHookPut answers PUT /v1/workbench/agent-hook. It saves the switch,
// then installs into every folder (on) or removes from every folder (off). It
// works from unset too: Remove is a no-op and the stored off stops the notice.
// A per-folder error goes into the folder's error field and never fails the
// whole call.
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
	state := agentHookOff
	if body.Enabled {
		state = agentHookOn
	}
	folders := agentHookFolders(cfg)
	views := make([]agentHookFolderView, 0, len(folders))
	for _, folder := range folders {
		view := folder.view()
		var err error
		if body.Enabled {
			err = folder.install(cfg.Binary)
		} else {
			err = folder.remove()
		}
		if err != nil {
			view.Error = agentHookErrorText(err)
		}
		view.Installed = agentHookInstalled(folder, cfg.Binary, &view)
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, agentHookAnswerOf(state, views))
}

// agentHookAnswerOf builds the route answer of a state and its folder views.
// needs_install is true when any folder is not installed, whatever the state.
func agentHookAnswerOf(state agentHookState, views []agentHookFolderView) agentHookResponse {
	return agentHookResponse{
		Enabled:      state == agentHookOn,
		State:        state,
		NeedsInstall: agentHookNeedsInstall(views),
		Folders:      views,
	}
}

// agentHookNeedsInstall reports whether any folder is missing a hook.
func agentHookNeedsInstall(views []agentHookFolderView) bool {
	for _, view := range views {
		if !view.Installed {
			return true
		}
	}
	return false
}

// agentHookNotAllowedResponse lists the folders of a daemon that may not manage
// the hook, each with the reason. It reports off and no install need.
func agentHookNotAllowedResponse(cfg AgentHookConfig) agentHookResponse {
	folders := agentHookFolders(cfg)
	views := make([]agentHookFolderView, 0, len(folders))
	for _, folder := range folders {
		view := folder.view()
		view.Error = agentHookNotAllowed
		views = append(views, view)
	}
	return agentHookResponse{Enabled: false, State: agentHookOff, Folders: views}
}

// agentHookInstalled reports whether the hook runs cfg.Binary in the folder,
// folding a read error into the view so it never fails the call.
func agentHookInstalled(folder AgentHookFolder, binary string, view *agentHookFolderView) bool {
	installed, err := folder.installed(binary)
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
	if errors.Is(err, agenthook.ErrInvalidHooks) {
		return "hooks.json is not valid JSON"
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

// agentHookState reads the switch: {"enabled": true} is on, {"enabled": false}
// is off. A missing file, an empty Home path, or JSON without a boolean enabled
// key is unset, so the owner was never asked. Any other read error is returned.
func (s *Server) agentHookState() (agentHookState, error) {
	path := s.agentHookEnabledPath()
	if path == "" {
		return agentHookUnset, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return agentHookUnset, nil
	}
	if err != nil {
		return agentHookUnset, err
	}
	var doc struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Enabled == nil {
		return agentHookUnset, nil
	}
	if *doc.Enabled {
		return agentHookOn, nil
	}
	return agentHookOff, nil
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
