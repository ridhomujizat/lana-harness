package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/claudemcp"
	"github.com/ridhomujizat/lana-harness/internal/claudeshare"
)

// Spec 77 REQ-WRA-005: the Workbench default Claude Code account. Every
// Workbench terminal created afterwards gets the account's CLAUDE_CONFIG_DIR and
// CLAUDE_SECURESTORAGE_CONFIG_DIR, so typing `claude` there runs on it. The
// built-in account "claude-code" (the initial value) sets neither. The choice is
// stored by the daemon in <LANA_HOME>/workbench/claude-account.json, next to
// the agent hook switch.

// workbenchClaudeAccountBodyLimit bounds the PUT /v1/workbench/claude-account
// body.
const workbenchClaudeAccountBodyLimit = 4 << 10

// workbenchClaudeAccountMu serialises the read-modify-write of the setting
// file.
var workbenchClaudeAccountMu sync.Mutex

// claudeDefaultDirs holds the default Claude Code folder of each server (spec
// 77 REQ-WRA-001): the folder account folders share history with. Like
// agentHooks it is kept outside the Server struct.
var (
	claudeDefaultDirsMu sync.Mutex
	claudeDefaultDirs   = map[*Server]string{}
)

// SetClaudeDefaultDir sets the default Claude Code folder (the daemon's
// CLAUDE_CONFIG_DIR, else ~/.claude) that a saved Claude Code account links its
// history, projects, settings, CLAUDE.md, skills, agents and commands to (spec
// 77 REQ-WRA-001). Unset, an account save links nothing.
func (s *Server) SetClaudeDefaultDir(dir string) {
	claudeDefaultDirsMu.Lock()
	defer claudeDefaultDirsMu.Unlock()
	claudeDefaultDirs[s] = dir
}

// claudeDefaultDir returns the folder SetClaudeDefaultDir set, or "".
func (s *Server) claudeDefaultDir() string {
	claudeDefaultDirsMu.Lock()
	defer claudeDefaultDirsMu.Unlock()
	return claudeDefaultDirs[s]
}

// claudeDefaultJSONs holds the default account's .claude.json of each server
// (spec 77 REQ-WRA-012), kept like claudeDefaultDirs.
var (
	claudeDefaultJSONsMu sync.Mutex
	claudeDefaultJSONs   = map[*Server]string{}
)

// SetClaudeDefaultJSON sets the default account's .claude.json (the daemon's
// $CLAUDE_CONFIG_DIR/.claude.json, else ~/.claude.json) that a saved Claude
// Code account that never signed in gets a copy of, without its login keys
// (spec 77 REQ-WRA-012). Unset, an account save writes no .claude.json.
func (s *Server) SetClaudeDefaultJSON(path string) {
	claudeDefaultJSONsMu.Lock()
	defer claudeDefaultJSONsMu.Unlock()
	claudeDefaultJSONs[s] = path
}

// claudeDefaultJSON returns the file SetClaudeDefaultJSON set, or "".
func (s *Server) claudeDefaultJSON() string {
	claudeDefaultJSONsMu.Lock()
	defer claudeDefaultJSONsMu.Unlock()
	return claudeDefaultJSONs[s]
}

// shareClaudeAccount links an account folder to the default folder (spec 77
// REQ-WRA-001) and, when the account never signed in, writes its .claude.json
// from the default's (REQ-WRA-012). A failure is only logged: it never fails
// the save.
func (s *Server) shareClaudeAccount(name, dir string) {
	if dir == "" {
		return
	}
	if defaultDir := s.claudeDefaultDir(); defaultDir != "" {
		report, err := claudeshare.Link(dir, defaultDir, s.nowTime())
		if err != nil {
			s.logger.Warn("web: share Claude Code history", "account", name, "dir", dir, "error", err)
		}
		if report.Changed() {
			s.logger.Info("web: shared Claude Code history", "account", name, "dir", dir)
		}
	}
	if defaultJSON := s.claudeDefaultJSON(); defaultJSON != "" {
		wrote, err := claudeshare.SeedClaudeJSON(dir, defaultJSON)
		if err != nil {
			s.logger.Warn("web: copy Claude Code settings", "account", name, "dir", dir, "error", err)
		}
		if wrote {
			s.logger.Info("web: copied Claude Code settings", "account", name, "dir", dir)
		}
	}
}

// registerWorkbenchClaudeAccountRoutes registers the routes of REQ-WRA-005.
// They go through Handle, so the host, origin, cookie and CSRF checks apply
// like the other /v1/workbench routes.
func (s *Server) registerWorkbenchClaudeAccountRoutes() {
	s.Handle("GET /v1/workbench/claude-account", http.HandlerFunc(s.handleWorkbenchClaudeAccountGet))
	s.Handle("PUT /v1/workbench/claude-account", http.HandlerFunc(s.handleWorkbenchClaudeAccountPut))
}

// workbenchClaudeAccountResponse is the GET and PUT answer: the account name
// and its config folder ("" for the built-in account).
type workbenchClaudeAccountResponse struct {
	Account   string `json:"account"`
	ConfigDir string `json:"config_dir"`
}

// handleWorkbenchClaudeAccountGet answers GET /v1/workbench/claude-account. A
// stored account that no longer exists reads as the built-in one.
func (s *Server) handleWorkbenchClaudeAccountGet(w http.ResponseWriter, r *http.Request) {
	account, dir := s.workbenchClaudeAccount(r.Context())
	writeJSON(w, http.StatusOK, workbenchClaudeAccountResponse{Account: account, ConfigDir: dir})
}

// handleWorkbenchClaudeAccountPut answers PUT /v1/workbench/claude-account
// with body {"account": "<name>"}: the built-in "claude-code" or a configured
// Claude Code account. An unknown name is 400 invalid_request. Existing
// terminals keep their environment.
func (s *Server) handleWorkbenchClaudeAccountPut(w http.ResponseWriter, r *http.Request) {
	if !s.controlAllowed(r) {
		refuseControlLocked(w)
		return
	}
	var body struct {
		Account string `json:"account"`
	}
	if !decodeJSONObject(w, r, workbenchClaudeAccountBodyLimit, &body) {
		return
	}
	account := strings.TrimSpace(body.Account)
	dirs := s.claudeAccountDirs(r.Context())
	dir, ok := dirs[account]
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "account must be claude-code or a Claude Code account of the Router")
		return
	}
	if err := s.saveWorkbenchClaudeAccount(account); err != nil {
		s.logger.Error("web: save workbench claude account", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	browser, _ := sessionFromContext(r.Context())
	s.auditWorkbench("claude-account", account, browser)
	writeJSON(w, http.StatusOK, workbenchClaudeAccountResponse{Account: account, ConfigDir: dir})
}

// workbenchClaudeAccount returns the stored Workbench account and its config
// folder. A missing or unreadable setting, or an account that is no longer
// configured, is the built-in account with no folder.
func (s *Server) workbenchClaudeAccount(ctx context.Context) (string, string) {
	stored, err := s.storedWorkbenchClaudeAccount()
	if err != nil {
		s.logger.Warn("web: read workbench claude account", "error", err)
	}
	if stored == "" || stored == core.ClaudeCodeProvider {
		return core.ClaudeCodeProvider, ""
	}
	if dir, ok := s.claudeAccountDirs(ctx)[stored]; ok {
		return stored, dir
	}
	return core.ClaudeCodeProvider, ""
}

// workbenchClaudeEnv is the environment a new Workbench terminal gets for the
// default account: CLAUDE_CONFIG_DIR and CLAUDE_SECURESTORAGE_CONFIG_DIR of a
// Router account, nothing for the built-in one.
func (s *Server) workbenchClaudeEnv(ctx context.Context) []string {
	_, dir := s.workbenchClaudeAccount(ctx)
	if dir == "" {
		return nil
	}
	return []string{"CLAUDE_CONFIG_DIR=" + dir, "CLAUDE_SECURESTORAGE_CONFIG_DIR=" + dir}
}

// mirrorClaudeMCP mirrors the built-in account's MCP servers ($HOME/.claude.json,
// user scope) into the named Claude Code account before a Workbench pane
// launches it (spec 81 REQ-SMC-004, REQ-SMC-005). The built-in account, an
// unknown name, or any failure leaves the account as it was: it never fails or
// blocks the create beyond the mirror's lock wait. Only a short reason is
// logged, never a server entry.
func (s *Server) mirrorClaudeMCP(ctx context.Context, name string) {
	if name == "" || name == core.ClaudeCodeProvider {
		return
	}
	dir, ok := s.claudeAccountDirs(ctx)[name]
	if !ok || dir == "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	if _, err := claudemcp.Mirror(home, dir); err != nil {
		s.logger.Info("web: claude MCP mirror skipped", "account", name, "reason", err.Error())
	}
}

// claudeAccountDirs maps every Claude Code account of the stored config to its
// config folder: the built-in "claude-code" to "", each kind=claude-code
// provider to its folder. A config that cannot be read leaves only the
// built-in account.
func (s *Server) claudeAccountDirs(ctx context.Context) map[string]string {
	out := map[string]string{core.ClaudeCodeProvider: ""}
	if s.configStore == nil {
		return out
	}
	file, err := s.configStore.ConfigFile(ctx)
	if err != nil || !file.Exists {
		return out
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		return out
	}
	for name, table := range document.Providers {
		if name == core.ClaudeCodeProvider || mapProviderString(table, "kind") != core.ClaudeCodeKind {
			continue
		}
		out[name] = s.claudeConfigDir(name, providerRecordFromMap(table))
	}
	return out
}

// workbenchClaudeAccountPath is the setting file:
// <LANA_HOME>/workbench/claude-account.json.
func (s *Server) workbenchClaudeAccountPath() string {
	if s.cfg.Home == "" {
		return ""
	}
	return filepath.Join(s.cfg.Home, "workbench", "claude-account.json")
}

// storedWorkbenchClaudeAccount reads the stored account name; a missing file,
// an empty Home path or JSON without a string account is "".
func (s *Server) storedWorkbenchClaudeAccount() (string, error) {
	path := s.workbenchClaudeAccountPath()
	if path == "" {
		return "", nil
	}
	workbenchClaudeAccountMu.Lock()
	data, err := os.ReadFile(path)
	workbenchClaudeAccountMu.Unlock()
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var doc struct {
		Account string `json:"account"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", nil
	}
	return strings.TrimSpace(doc.Account), nil
}

// saveWorkbenchClaudeAccount writes the setting file.
func (s *Server) saveWorkbenchClaudeAccount(account string) error {
	path := s.workbenchClaudeAccountPath()
	if path == "" {
		return nil
	}
	data, err := json.Marshal(struct {
		Account string `json:"account"`
	}{account})
	if err != nil {
		return err
	}
	workbenchClaudeAccountMu.Lock()
	defer workbenchClaudeAccountMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
