package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// One-click sign-in, sign-out, and removal of Claude Code accounts
// (REQ-CCT-012, contract revision 3). The daemon starts the official
// `claude auth login` / `claude auth logout` with the account's folder; the
// binary opens the browser and owns the credentials. Answers carry only the
// state and a short, URL-stripped error line.

// ClaudeAccountRef names a Claude Code account for a sign-in or sign-out:
// ConfigDir "" is the built-in account (the owner's own ~/.claude).
type ClaudeAccountRef struct {
	Name      string
	ConfigDir string
	Binary    string
}

// ClaudeSignInStatus is the last sign-in of one account (API-CCT-016).
type ClaudeSignInStatus struct {
	// State is idle, running, succeeded, failed, or cancelled.
	State string `json:"state"`
	// Error is set for failed only; it never holds a URL, code, or token.
	Error      string     `json:"error"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// Sign-in states.
const (
	ClaudeSignInIdle    = "idle"
	ClaudeSignInRunning = "running"
	ClaudeSignInFailed  = "failed"
)

// ErrClaudeSignInRunning is returned by a ClaudeAccountAuthBackend while a
// sign-in or sign-out of the account runs.
var ErrClaudeSignInRunning = errors.New("a sign-in or sign-out of this account is already running")

// ClaudeAccountAuthBackend is the optional backend part that signs Claude
// Code accounts in and out (REQ-CCT-012).
type ClaudeAccountAuthBackend interface {
	// StartClaudeSignIn starts `claude auth login` in the background.
	StartClaudeSignIn(account ClaudeAccountRef) (ClaudeSignInStatus, error)
	// CancelClaudeSignIn stops a running sign-in and returns the state.
	CancelClaudeSignIn(name string) ClaudeSignInStatus
	// ClaudeSignInState returns the last sign-in; idle when there was none.
	ClaudeSignInState(name string) ClaudeSignInStatus
	// ForgetClaudeSignIn cancels a running sign-in and drops the state of a
	// removed account.
	ForgetClaudeSignIn(name string)
	// ClaudeSignOut runs `claude auth logout` and waits for it.
	ClaudeSignOut(ctx context.Context, account ClaudeAccountRef) error
}

// claudeLabelRunes is the longest account label (config rule of
// [providers.*] label).
const claudeLabelRunes = 60

// claudeErrorRunes caps an error line in an answer.
const claudeErrorRunes = 240

func (s *Server) registerClaudeSignInRoutes() {
	s.Handle("POST /v1/router/claude-code/accounts", http.HandlerFunc(s.handleCreateClaudeAccount))
	s.Handle("POST /v1/router/claude-code/accounts/{name}/sign-in", http.HandlerFunc(s.handleStartClaudeSignIn))
	s.Handle("GET /v1/router/claude-code/accounts/{name}/sign-in", http.HandlerFunc(s.handleGetClaudeSignIn))
	s.Handle("DELETE /v1/router/claude-code/accounts/{name}/sign-in", http.HandlerFunc(s.handleCancelClaudeSignIn))
	s.Handle("POST /v1/router/claude-code/accounts/{name}/sign-out", http.HandlerFunc(s.handleClaudeSignOut))
	s.Handle("PUT /v1/router/claude-code/accounts/{name}/label", http.HandlerFunc(s.handlePutClaudeLabel))
}

// claudeAuthPort returns the sign-in backend, or nil.
func (s *Server) claudeAuthPort() ClaudeAccountAuthBackend {
	if port := s.routerPort(); port != nil {
		if auth, ok := port.(ClaudeAccountAuthBackend); ok {
			return auth
		}
	}
	if s.backend != nil {
		if auth, ok := s.backend.(ClaudeAccountAuthBackend); ok {
			return auth
		}
	}
	return nil
}

func (s *Server) claudeAuthPortOrError(w http.ResponseWriter) ClaudeAccountAuthBackend {
	auth := s.claudeAuthPort()
	if auth == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "sign_in_unavailable", "Claude Code sign-in is not available in this daemon")
	}
	return auth
}

// decodeOptionalJSONObject decodes a JSON object body; an empty body leaves
// dst as it is.
func decodeOptionalJSONObject(w http.ResponseWriter, r *http.Request, dst any) bool {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, providerBodyLimit))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body is too large")
		return false
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return true
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return false
	}
	return true
}

// claudeAccountRef reads config.toml for the account named in the path: the
// built-in claude-code, or a [providers.<name>] table of kind claude-code.
// It writes the error answer and returns false when there is none.
func (s *Server) claudeAccountRef(w http.ResponseWriter, r *http.Request) (ClaudeAccountRef, providerRecord, bool) {
	name := r.PathValue("name")
	if !providerNameRE.MatchString(name) {
		writeProviderIssue(w, "name", "name must match [a-z0-9-]+")
		return ClaudeAccountRef{}, providerRecord{}, false
	}
	if name == core.ClaudeCodeProvider {
		return ClaudeAccountRef{Name: name}, providerRecord{Kind: core.ClaudeCodeKind}, true
	}
	store := s.configStoreOrError(w)
	if store == nil {
		return ClaudeAccountRef{}, providerRecord{}, false
	}
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config for claude account", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return ClaudeAccountRef{}, providerRecord{}, false
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil || !file.Exists {
		if err != nil {
			s.logger.Error("web: parse config for claude account", "error", err)
		}
		writeAPIError(w, http.StatusNotFound, "not_found", "no Claude Code account named "+name)
		return ClaudeAccountRef{}, providerRecord{}, false
	}
	table, ok := document.Providers[name]
	record := providerRecordFromMap(table)
	if !ok || record.Kind != core.ClaudeCodeKind {
		writeAPIError(w, http.StatusNotFound, "not_found", "no Claude Code account named "+name)
		return ClaudeAccountRef{}, providerRecord{}, false
	}
	return ClaudeAccountRef{Name: name, ConfigDir: s.claudeConfigDir(name, record), Binary: record.Binary}, record, true
}

// nextClaudeAccountName is the first free claude-2, claude-3, … name.
func nextClaudeAccountName(document providerDocument) string {
	for n := 2; ; n++ {
		name := "claude-" + strconv.Itoa(n)
		if _, taken := document.Providers[name]; !taken {
			return name
		}
	}
}

type claudeAccountCreateResponse struct {
	Name            string             `json:"name"`
	ConfigDir       string             `json:"config_dir"`
	Version         string             `json:"version"`
	RestartRequired bool               `json:"restart_required"`
	LoginCommand    string             `json:"login_command"`
	SignIn          ClaudeSignInStatus `json:"sign_in"`
}

// handleCreateClaudeAccount adds a Claude Code account in one step
// (REQ-CCT-012): it writes the table (name default claude-2, claude-3, …),
// creates the folder (0700), and starts the sign-in in the background.
func (s *Server) handleCreateClaudeAccount(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeOptionalJSONObject(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name != "" && !providerNameRE.MatchString(name) {
		writeProviderIssue(w, "name", "name must match [a-z0-9-]+")
		return
	}
	if name == core.ClaudeCodeProvider {
		writeAPIError(w, http.StatusConflict, "builtin", "claude-code is built in")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before claude account create", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before claude account create", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if name == "" {
		name = nextClaudeAccountName(document)
	} else if _, taken := document.Providers[name]; taken {
		writeAPIError(w, http.StatusConflict, "exists", "a provider named "+name+" already exists")
		return
	}
	record := providerRecord{Kind: core.ClaudeCodeKind}
	candidate, changed, err := webfiles.RewriteTable(r.Context(), providerChecker(store), file.Raw,
		"[providers."+name+"]", renderClaudeAccountTable(name, record))
	if err != nil {
		s.writeProviderSaveError(w, "create claude account", err)
		return
	}
	dir := s.claudeConfigDir(name, record)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeProviderIssue(w, "providers."+name+".config_dir", "cannot create the account folder: "+err.Error())
		return
	}
	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		s.logger.Error("web: write config for claude account create", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.auditSave("provider.save", providerConfigPath(file, s.providerHome()), name, changed)
	response := claudeAccountCreateResponse{
		Name: name, ConfigDir: dir, Version: webfiles.ConfigVersion(candidate),
		RestartRequired: s.applyLiveRestartRequired(r.Context(), store, candidate),
		LoginCommand:    llmcall.ClaudeLoginCommand(dir),
	}
	response.SignIn = s.startClaudeSignIn(ClaudeAccountRef{Name: name, ConfigDir: dir})
	writeJSON(w, http.StatusAccepted, response)
}

// startClaudeSignIn starts a sign-in and reports a start failure as a failed
// state.
func (s *Server) startClaudeSignIn(ref ClaudeAccountRef) ClaudeSignInStatus {
	auth := s.claudeAuthPort()
	if auth == nil {
		return ClaudeSignInStatus{State: ClaudeSignInFailed, Error: "Claude Code sign-in is not available in this daemon."}
	}
	state, err := auth.StartClaudeSignIn(ref)
	if err != nil {
		if errors.Is(err, ErrClaudeSignInRunning) {
			return state
		}
		return ClaudeSignInStatus{State: ClaudeSignInFailed, Error: truncateText(err.Error(), claudeErrorRunes)}
	}
	return state
}

func (s *Server) handleStartClaudeSignIn(w http.ResponseWriter, r *http.Request) {
	ref, _, ok := s.claudeAccountRef(w, r)
	if !ok {
		return
	}
	auth := s.claudeAuthPortOrError(w)
	if auth == nil {
		return
	}
	state, err := auth.StartClaudeSignIn(ref)
	switch {
	case errors.Is(err, ErrClaudeSignInRunning):
		writeAPIError(w, http.StatusConflict, "sign_in_running", "a sign-in of "+ref.Name+" is already running")
	case err != nil:
		writeAPIError(w, http.StatusServiceUnavailable, "sign_in_unavailable", truncateText(err.Error(), claudeErrorRunes))
	default:
		writeJSON(w, http.StatusAccepted, state)
	}
}

func (s *Server) handleGetClaudeSignIn(w http.ResponseWriter, r *http.Request) {
	ref, _, ok := s.claudeAccountRef(w, r)
	if !ok {
		return
	}
	auth := s.claudeAuthPortOrError(w)
	if auth == nil {
		return
	}
	writeJSON(w, http.StatusOK, auth.ClaudeSignInState(ref.Name))
}

func (s *Server) handleCancelClaudeSignIn(w http.ResponseWriter, r *http.Request) {
	ref, _, ok := s.claudeAccountRef(w, r)
	if !ok {
		return
	}
	auth := s.claudeAuthPortOrError(w)
	if auth == nil {
		return
	}
	writeJSON(w, http.StatusOK, auth.CancelClaudeSignIn(ref.Name))
}

// handleClaudeSignOut runs `claude auth logout` for the account and answers
// its refreshed row. The built-in account signs the owner out of their own
// Claude Code, so it needs {"confirm": true}.
func (s *Server) handleClaudeSignOut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if !decodeOptionalJSONObject(w, r, &body) {
		return
	}
	ref, _, ok := s.claudeAccountRef(w, r)
	if !ok {
		return
	}
	if ref.Name == core.ClaudeCodeProvider && !body.Confirm {
		writeAPIError(w, http.StatusBadRequest, "confirm_required",
			"signing out the built-in account signs you out of Claude Code on this machine; send {\"confirm\": true}")
		return
	}
	auth := s.claudeAuthPortOrError(w)
	if auth == nil {
		return
	}
	if err := auth.ClaudeSignOut(r.Context(), ref); err != nil {
		if errors.Is(err, ErrClaudeSignInRunning) {
			writeAPIError(w, http.StatusConflict, "sign_in_running", "a sign-in or sign-out of "+ref.Name+" is already running")
			return
		}
		writeAPIError(w, http.StatusUnprocessableEntity, "sign_out_failed", truncateText(err.Error(), claudeErrorRunes))
		return
	}
	s.auditSave("provider.sign_out", "", ref.Name, nil)
	writeJSON(w, http.StatusOK, s.claudeAccountRow(r.Context(), ref.Name))
}

// claudeAccountRow returns one decorated account row of the Claude Code
// status, or a bare row when the status has none.
func (s *Server) claudeAccountRow(ctx context.Context, name string) ClaudeCodeAccountStatus {
	row := ClaudeCodeAccountStatus{Name: name, Builtin: name == core.ClaudeCodeProvider}
	if port := s.routerPort(); port != nil {
		if status, err := port.ClaudeAgentStatus(ctx); err == nil {
			for _, account := range status.Accounts {
				if account.Name == name {
					row = account
					break
				}
			}
		}
	}
	if row.Windows == nil {
		row.Windows = []RouterPlanWindow{}
	}
	if row.Agents == nil {
		row.Agents = []string{}
	}
	if row.Combos == nil {
		row.Combos = []string{}
	}
	rows := []ClaudeCodeAccountStatus{row}
	s.decorateClaudeAccounts(ctx, rows)
	return rows[0]
}

// decorateClaudeAccounts fills the label, display name, and sign-in state
// of account rows (REQ-CCT-012). Labels come from config.toml.
func (s *Server) decorateClaudeAccounts(ctx context.Context, rows []ClaudeCodeAccountStatus) {
	labels := map[string]string{}
	if s.configStore != nil {
		if file, err := s.configStore.ConfigFile(ctx); err == nil && file.Exists {
			if document, err := parseProviderDocument(file.Raw); err == nil {
				for name, table := range document.Providers {
					if record := providerRecordFromMap(table); record.Kind == core.ClaudeCodeKind {
						labels[name] = record.Label
					}
				}
			}
		}
	}
	auth := s.claudeAuthPort()
	for i := range rows {
		row := &rows[i]
		row.Label = labels[row.Name]
		switch {
		case row.Label != "":
			row.DisplayName = row.Label
		case row.Email != "":
			row.DisplayName = row.Email
		default:
			row.DisplayName = row.Name
		}
		state := ClaudeSignInStatus{State: ClaudeSignInIdle}
		if auth != nil {
			state = auth.ClaudeSignInState(row.Name)
		}
		row.SignIn = &state
	}
}

type claudeLabelResponse struct {
	Name            string `json:"name"`
	Label           string `json:"label"`
	Version         string `json:"version"`
	RestartRequired bool   `json:"restart_required"`
}

// handlePutClaudeLabel stores the display label of an added account as
// label in its table; "" removes it.
func (s *Server) handlePutClaudeLabel(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	if !providerNameRE.MatchString(name) {
		writeProviderIssue(w, "name", "name must match [a-z0-9-]+")
		return
	}
	var body struct {
		Label           *string `json:"label"`
		ExpectedVersion string  `json:"expected_version"`
	}
	if !decodeJSONObject(w, r, providerBodyLimit, &body) {
		return
	}
	if name == core.ClaudeCodeProvider {
		writeAPIError(w, http.StatusConflict, "builtin", "the built-in account has no label; it shows its e-mail")
		return
	}
	path := "providers." + name + ".label"
	if body.Label == nil {
		writeProviderIssue(w, path, "label is required")
		return
	}
	label := strings.TrimSpace(*body.Label)
	if strings.ContainsFunc(label, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		writeProviderIssue(w, path, "label must be one line")
		return
	}
	if n := utf8.RuneCountInString(label); n > claudeLabelRunes {
		writeProviderIssue(w, path, fmt.Sprintf("label must be at most %d characters (got %d)", claudeLabelRunes, n))
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before claude label save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return
	}
	if body.ExpectedVersion != "" && file.Version != body.ExpectedVersion {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before claude label save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	table, ok := document.Providers[name]
	record := providerRecordFromMap(table)
	if !ok || record.Kind != core.ClaudeCodeKind {
		writeAPIError(w, http.StatusNotFound, "not_found", "no Claude Code account named "+name)
		return
	}
	record.Label = label
	candidate, changed, err := webfiles.RewriteTable(r.Context(), providerChecker(store), file.Raw,
		"[providers."+name+"]", renderClaudeAccountTable(name, record))
	if err != nil {
		s.writeProviderSaveError(w, "save claude label", err)
		return
	}
	restartRequired := !isLiveStore(store)
	if !bytes.Equal(candidate, file.Raw) {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for claude label save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		s.auditSave("provider.save", providerConfigPath(file, s.providerHome()), name, changed)
		restartRequired = s.applyLiveRestartRequired(r.Context(), store, candidate)
	}
	writeJSON(w, http.StatusOK, claudeLabelResponse{
		Name: name, Label: label, Version: webfiles.ConfigVersion(candidate), RestartRequired: restartRequired,
	})
}

// claudeFolderRemovable reports whether dir lies inside $LANA_HOME/claude/,
// the only place lana deletes an account folder (REQ-CCT-012). A folder the
// owner configured elsewhere is never deleted.
func (s *Server) claudeFolderRemovable(dir string) bool {
	home := s.providerHome()
	if home == "" || dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	root := filepath.Clean(filepath.Join(home, "claude"))
	rel, err := filepath.Rel(root, filepath.Clean(dir))
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func writeClaudeFolderOutside(w http.ResponseWriter, dir string) {
	writeAPIError(w, http.StatusConflict, "folder_outside_home",
		"the account folder "+dir+" is outside lana's claude folder, so lana does not delete it; remove the account without deleting the folder, then delete the folder yourself")
}

// signOutClaudeAccountForRemoval checks a DELETE with remove_folder of a
// Claude Code account (it exists, is not in use, and its folder is removable)
// and signs the account out. note is the sign-out error, if any; ok false
// means the error answer was written. Other kinds pass unchanged.
func (s *Server) signOutClaudeAccountForRemoval(w http.ResponseWriter, r *http.Request, store ConfigStore, name, expected string) (note string, ok bool) {
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before claude account removal", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return "", false
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return "", false
	}
	if file.Version != expected {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return "", false
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before claude account removal", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return "", false
	}
	table, exists := document.Providers[name]
	if !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "provider is not configured")
		return "", false
	}
	record := providerRecordFromMap(table)
	if record.Kind != core.ClaudeCodeKind {
		return "", true
	}
	if message := document.providerInUse(name); message != "" {
		writeAPIError(w, http.StatusConflict, "in_use", message)
		return "", false
	}
	dir := s.claudeConfigDir(name, record)
	if !s.claudeFolderRemovable(dir) {
		writeClaudeFolderOutside(w, dir)
		return "", false
	}
	auth := s.claudeAuthPort()
	if auth == nil {
		return "Claude Code sign-in is not available in this daemon.", true
	}
	if err := auth.ClaudeSignOut(r.Context(), ClaudeAccountRef{Name: name, ConfigDir: dir, Binary: record.Binary}); err != nil {
		s.logger.Warn("web: sign out claude account before removal", "account", name)
		return truncateText(err.Error(), claudeErrorRunes), true
	}
	return "", true
}

// truncateText cuts text to at most n runes.
func truncateText(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	return string([]rune(text)[:n])
}
