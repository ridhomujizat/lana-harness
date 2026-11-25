package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Antigravity accounts on the Router page (spec 74 API-AGY-020..027). The
// daemon starts the official `agy acp --login` with the account's HOME; agy
// prints the Google link and reads the code on stdin. Answers carry only the
// state, the link while agy waits for the code, and a short, URL-stripped
// error line. The code is written once to agy and never stored or logged.

// AntigravityMinVersion is the oldest agy lana runs (REQ-AGY-002).
const AntigravityMinVersion = "1.5.17"

// antigravityChoiceGroup is the picker group of Antigravity models
// (API-AGY-026); with several enabled accounts each has its own group
// "Antigravity · <display name>".
const antigravityChoiceGroup = "Antigravity"

// Antigravity sign-in states (API-AGY-020).
const (
	AntigravitySignInIdle        = "idle"
	AntigravitySignInWaitingCode = "waiting_code"
	AntigravitySignInFailed      = "failed"
)

var (
	// ErrAntigravitySignInRunning is returned by an AntigravityBackend while
	// a sign-in of the account runs (409 sign_in_running).
	ErrAntigravitySignInRunning = errors.New("a sign-in of this account is already running")
	// ErrAntigravityBuiltin is returned for the built-in account, which signs
	// in with agy in a terminal (409 builtin).
	ErrAntigravityBuiltin = errors.New("the built-in account signs in with agy in a terminal")
	// ErrAntigravityNotWaiting is returned for a code sent while the
	// sign-in does not wait for one (409 not_waiting).
	ErrAntigravityNotWaiting = errors.New("the sign-in is not waiting for a code")
	// ErrAntigravityBadCode is returned for a code that is empty, longer
	// than 2048 characters, or not one line (422 bad_code).
	ErrAntigravityBadCode = errors.New("the code must be 1 to 2048 characters on one line")
)

// AntigravityAccountRef names an Antigravity account for a sign-in: Home ""
// is filled by the backend from the running config.
type AntigravityAccountRef struct {
	Name   string
	Home   string
	Binary string
}

// AntigravitySignInStatus is the last sign-in of one account (API-AGY-020).
type AntigravitySignInStatus struct {
	// State is idle, starting, waiting_code, running, succeeded, failed, or
	// cancelled.
	State string `json:"state"`
	// URL is the Google sign-in link, set only for waiting_code.
	URL string `json:"url"`
	// Error is set for failed only; it never holds a URL or code.
	Error      string     `json:"error"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// AntigravityWindow is one quota window of a model group (API-AGY-020):
// Type is "5h" or "weekly"; Remaining is the fraction left, null when not
// reported.
type AntigravityWindow struct {
	Group     string     `json:"group"`
	Type      string     `json:"type"`
	Remaining *float64   `json:"remaining"`
	ResetsAt  *time.Time `json:"resets_at"`
	Source    string     `json:"source,omitempty"`
}

// AntigravityUsage is an account's use in the last 24 hours.
type AntigravityUsage struct {
	Calls        int64 `json:"calls"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// AntigravityAccountStatus is one account row of GET /v1/router/antigravity
// (API-AGY-020, spec 75 additions).
type AntigravityAccountStatus struct {
	Name             string              `json:"name"`
	Builtin          bool                `json:"builtin"`
	Label            string              `json:"label"`
	DisplayName      string              `json:"display_name"`
	Home             string              `json:"home"`
	Binary           string              `json:"binary"`
	Disabled         bool                `json:"disabled"`
	AllowScheduled   bool                `json:"allow_scheduled"`
	SignedIn         bool                `json:"signed_in"`
	Error            string              `json:"error"`
	Tier             string              `json:"tier"`
	ModelsCount      int                 `json:"models_count"`
	Running          int                 `json:"running"`
	Queued           int                 `json:"queued"`
	Windows          []AntigravityWindow `json:"windows"`
	WindowsUpdatedAt *time.Time          `json:"windows_updated_at"`
	WindowsError     string              `json:"windows_error"`
	Usage24h         AntigravityUsage    `json:"usage_24h"`
	Agents           []string            `json:"agents"`
	Combos           []string            `json:"combos"`
	// SignIn is filled by the handler.
	SignIn       *AntigravitySignInStatus `json:"sign_in"`
	Priority     int                      `json:"priority"`
	Pool         bool                     `json:"pool"`
	CoolingUntil *time.Time               `json:"cooling_until"`
}

// AntigravityStatus is the answer of GET /v1/router/antigravity
// (API-AGY-020): the built-in account's binary and version, and every
// account in pool order.
type AntigravityStatus struct {
	Installed     bool                       `json:"installed"`
	Binary        string                     `json:"binary"`
	Version       string                     `json:"version"`
	VersionOK     bool                       `json:"version_ok"`
	MinVersion    string                     `json:"min_version"`
	Error         string                     `json:"error"`
	Running       int                        `json:"running"`
	Queued        int                        `json:"queued"`
	MaxConcurrent int                        `json:"max_concurrent"`
	Accounts      []AntigravityAccountStatus `json:"accounts"`
	// PoolWarning is true with more than one pooled personal account
	// (spec 75 REQ-PAF-006).
	PoolWarning bool `json:"pool_warning"`
}

// AntigravityBackend is the optional backend part for Antigravity accounts
// (spec 74). RouterModels and RouterTest of such a backend accept the
// Antigravity accounts and "<account>/<model>" addresses.
type AntigravityBackend interface {
	// AntigravityInstalled reports whether the built-in account's binary is
	// found; the pickers list Antigravity models only then (API-AGY-026).
	AntigravityInstalled(ctx context.Context) bool
	// AntigravityStatus reports the runtime and every account.
	AntigravityStatus(ctx context.Context) (AntigravityStatus, error)
	// StartAntigravitySignIn starts `agy acp --login` in the background.
	StartAntigravitySignIn(account AntigravityAccountRef) (AntigravitySignInStatus, error)
	// AntigravitySignInState returns the last sign-in; idle when none.
	AntigravitySignInState(name string) AntigravitySignInStatus
	// SubmitAntigravityCode hands the pasted code to the waiting sign-in.
	SubmitAntigravityCode(name, code string) (AntigravitySignInStatus, error)
	// CancelAntigravitySignIn stops a running sign-in and returns the state.
	CancelAntigravitySignIn(name string) AntigravitySignInStatus
	// ForgetAntigravityAccount cancels a running sign-in and drops what the
	// daemon keeps about a removed account.
	ForgetAntigravityAccount(name string)
}

// agyCodeMaxRunes is the longest accepted sign-in code (API-AGY-023).
const agyCodeMaxRunes = 2048

func (s *Server) registerAntigravityRoutes() {
	s.Handle("GET /v1/router/antigravity", http.HandlerFunc(s.handleAntigravityStatus))
	s.Handle("POST /v1/router/antigravity/accounts", http.HandlerFunc(s.handleCreateAntigravityAccount))
	s.Handle("POST /v1/router/antigravity/accounts/{name}/sign-in", http.HandlerFunc(s.handleStartAntigravitySignIn))
	s.Handle("GET /v1/router/antigravity/accounts/{name}/sign-in", http.HandlerFunc(s.handleGetAntigravitySignIn))
	s.Handle("DELETE /v1/router/antigravity/accounts/{name}/sign-in", http.HandlerFunc(s.handleCancelAntigravitySignIn))
	s.Handle("POST /v1/router/antigravity/accounts/{name}/sign-in/code", http.HandlerFunc(s.handleAntigravitySignInCode))
	s.Handle("PUT /v1/router/antigravity/accounts/{name}/label", http.HandlerFunc(s.handlePutAntigravityLabel))
}

// agyPort returns the Antigravity backend, or nil.
func (s *Server) agyPort() AntigravityBackend {
	if port := s.routerPort(); port != nil {
		if agy, ok := port.(AntigravityBackend); ok {
			return agy
		}
	}
	if s.backend != nil {
		if agy, ok := s.backend.(AntigravityBackend); ok {
			return agy
		}
	}
	return nil
}

func (s *Server) agyPortOrError(w http.ResponseWriter) AntigravityBackend {
	agy := s.agyPort()
	if agy == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "sign_in_unavailable", "Antigravity is not available in this daemon")
	}
	return agy
}

// antigravityHome is the HOME of a named Antigravity account as the runtime
// sets it; "" for the built-in one.
func (s *Server) antigravityHome(name string, record providerRecord) string {
	return llmcall.AntigravityHomeDir(s.providerHome(), name, core.ProviderConfig{Home: record.Home})
}

// antigravityFolderRemovable reports whether home lies inside
// $LANA_HOME/antigravity/, the only place lana deletes an account folder
// (API-AGY-025).
func (s *Server) antigravityFolderRemovable(home string) bool {
	lanaHome := s.providerHome()
	if lanaHome == "" || home == "" || !filepath.IsAbs(home) {
		return false
	}
	root := filepath.Clean(filepath.Join(lanaHome, "antigravity"))
	rel, err := filepath.Rel(root, filepath.Clean(home))
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func writeAntigravityFolderOutside(w http.ResponseWriter, home string) {
	writeAPIError(w, http.StatusConflict, "folder_outside_home",
		"the account folder "+home+" is outside lana's antigravity folder, so lana does not delete it; remove the account without deleting the folder, then delete the folder yourself")
}

// checkAntigravityRemoval checks a DELETE with remove_folder of an
// Antigravity account before anything changes (API-AGY-025): the account is
// not in use and its home lies inside $LANA_HOME/antigravity/. ok false means
// the error answer was written. Other kinds pass unchanged.
func (s *Server) checkAntigravityRemoval(w http.ResponseWriter, r *http.Request, store ConfigStore, name, expected string) bool {
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before antigravity account removal", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return false
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return false
	}
	if file.Version != expected {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return false
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before antigravity account removal", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return false
	}
	table, exists := document.Providers[name]
	if !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "provider is not configured")
		return false
	}
	record := providerRecordFromMap(table)
	if record.Kind != core.AntigravityKind {
		return true
	}
	if message := document.providerInUse(name); message != "" {
		writeAPIError(w, http.StatusConflict, "in_use", message)
		return false
	}
	if home := s.antigravityHome(name, record); !s.antigravityFolderRemovable(home) {
		writeAntigravityFolderOutside(w, home)
		return false
	}
	return true
}

// antigravityAccountRef reads config.toml for the account named in the path:
// the built-in antigravity, or a [providers.<name>] table of kind
// antigravity. It writes the error answer and returns false when there is
// none.
func (s *Server) antigravityAccountRef(w http.ResponseWriter, r *http.Request) (AntigravityAccountRef, providerRecord, bool) {
	name := r.PathValue("name")
	if !providerNameRE.MatchString(name) {
		writeProviderIssue(w, "name", "name must match [a-z0-9-]+")
		return AntigravityAccountRef{}, providerRecord{}, false
	}
	store := s.configStore
	var document providerDocument
	exists := false
	if store != nil {
		file, err := store.ConfigFile(r.Context())
		if err != nil {
			s.logger.Error("web: read config for antigravity account", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return AntigravityAccountRef{}, providerRecord{}, false
		}
		if file.Exists {
			parsed, err := parseProviderDocument(file.Raw)
			if err != nil {
				s.logger.Error("web: parse config for antigravity account", "error", err)
			} else {
				document, exists = parsed, true
			}
		}
	}
	if name == core.AntigravityProvider {
		record := providerRecordFromMap(document.AgyBuiltin)
		record.Kind = core.AntigravityKind
		return AntigravityAccountRef{Name: name, Binary: record.Binary}, record, true
	}
	table, ok := document.Providers[name]
	record := providerRecordFromMap(table)
	if !exists || !ok || record.Kind != core.AntigravityKind {
		writeAPIError(w, http.StatusNotFound, "not_found", "no Antigravity account named "+name)
		return AntigravityAccountRef{}, providerRecord{}, false
	}
	return AntigravityAccountRef{Name: name, Home: s.antigravityHome(name, record), Binary: record.Binary}, record, true
}

// nextAntigravityAccountName is the first free agy-2, agy-3, … name.
func nextAntigravityAccountName(document providerDocument) string {
	for n := 2; ; n++ {
		name := "agy-" + strconv.Itoa(n)
		if _, taken := document.Providers[name]; !taken {
			return name
		}
	}
}

// validAccountLabel trims a label and checks it: one line, at most 60
// characters. It returns the issue message when it is not valid.
func validAccountLabel(label string) (string, string) {
	label = strings.TrimSpace(label)
	if strings.ContainsFunc(label, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", "label must be one line"
	}
	if n := utf8.RuneCountInString(label); n > claudeLabelRunes {
		return "", fmt.Sprintf("label must be at most %d characters (got %d)", claudeLabelRunes, n)
	}
	return label, ""
}

// renderAntigravityAccountTable renders a [providers.<name>] table of kind
// "antigravity" (API-AGY-002). The built-in account's table holds only
// binary, allow_scheduled, disabled, and the pool keys (API-AGY-003).
func renderAntigravityAccountTable(name string, record providerRecord) string {
	builtin := name == core.AntigravityProvider
	var b strings.Builder
	fmt.Fprintf(&b, "[providers.%s]\n", name)
	writeProviderString(&b, "kind", core.AntigravityKind)
	if !builtin {
		writeProviderString(&b, "home", record.Home)
	}
	writeProviderString(&b, "binary", record.Binary)
	if record.AllowScheduled {
		b.WriteString("allow_scheduled = true\n")
	}
	if record.Disabled {
		b.WriteString("disabled = true\n")
	}
	if !builtin {
		writeProviderString(&b, "label", record.Label)
	}
	writeProviderPool(&b, record)
	return b.String()
}

type antigravityAccountCreateResponse struct {
	Name            string                  `json:"name"`
	Home            string                  `json:"home"`
	Version         string                  `json:"version"`
	RestartRequired bool                    `json:"restart_required"`
	SignIn          AntigravitySignInStatus `json:"sign_in"`
}

// handleCreateAntigravityAccount adds an Antigravity account in one step
// (API-AGY-021): it writes the table (name default agy-2, agy-3, …), creates
// the home (0700), and starts the sign-in in the background.
func (s *Server) handleCreateAntigravityAccount(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	var body struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	}
	if !decodeOptionalJSONObject(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name != "" && !providerNameRE.MatchString(name) {
		writeProviderIssue(w, "name", "name must match [a-z0-9-]+")
		return
	}
	if name == core.AntigravityProvider {
		writeAPIError(w, http.StatusConflict, "builtin", "antigravity is built in")
		return
	}
	label, issue := validAccountLabel(body.Label)
	if issue != "" {
		writeProviderIssue(w, "label", issue)
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before antigravity account create", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before antigravity account create", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if name == "" {
		name = nextAntigravityAccountName(document)
	} else if _, taken := document.Providers[name]; taken || name == core.ClaudeCodeProvider {
		writeAPIError(w, http.StatusConflict, "exists", "a provider named "+name+" already exists")
		return
	}
	record := providerRecord{Kind: core.AntigravityKind, Label: label}
	candidate, changed, err := webfiles.RewriteTable(r.Context(), providerChecker(store), file.Raw,
		"[providers."+name+"]", renderAntigravityAccountTable(name, record))
	if err != nil {
		s.writeProviderSaveError(w, "create antigravity account", err)
		return
	}
	home := s.antigravityHome(name, record)
	if err := os.MkdirAll(home, 0o700); err != nil {
		writeProviderIssue(w, "providers."+name+".home", "cannot create the account folder: "+err.Error())
		return
	}
	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		s.logger.Error("web: write config for antigravity account create", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.auditSave("provider.save", providerConfigPath(file, s.providerHome()), name, changed)
	response := antigravityAccountCreateResponse{
		Name: name, Home: home, Version: webfiles.ConfigVersion(candidate),
		RestartRequired: s.applyLiveRestartRequired(r.Context(), store, candidate),
	}
	response.SignIn = s.startAntigravitySignIn(AntigravityAccountRef{Name: name, Home: home})
	writeJSON(w, http.StatusAccepted, response)
}

// startAntigravitySignIn starts a sign-in and reports a start failure as a
// failed state.
func (s *Server) startAntigravitySignIn(ref AntigravityAccountRef) AntigravitySignInStatus {
	agy := s.agyPort()
	if agy == nil {
		return AntigravitySignInStatus{State: AntigravitySignInFailed, Error: "Antigravity sign-in is not available in this daemon."}
	}
	state, err := agy.StartAntigravitySignIn(ref)
	if err != nil {
		if errors.Is(err, ErrAntigravitySignInRunning) {
			return state
		}
		return AntigravitySignInStatus{State: AntigravitySignInFailed, Error: truncateText(stripURLs(err.Error()), claudeErrorRunes)}
	}
	return state
}

// stripURLs replaces every URL in text with "sign-in link".
func stripURLs(text string) string {
	fields := strings.Fields(text)
	for i, field := range fields {
		if strings.Contains(field, "://") {
			fields[i] = "sign-in link"
		}
	}
	return strings.Join(fields, " ")
}

func (s *Server) handleStartAntigravitySignIn(w http.ResponseWriter, r *http.Request) {
	ref, _, ok := s.antigravityAccountRef(w, r)
	if !ok {
		return
	}
	if ref.Name == core.AntigravityProvider {
		writeAPIError(w, http.StatusConflict, "builtin", "the built-in account signs in with `agy` in a terminal")
		return
	}
	agy := s.agyPortOrError(w)
	if agy == nil {
		return
	}
	state, err := agy.StartAntigravitySignIn(ref)
	switch {
	case errors.Is(err, ErrAntigravitySignInRunning):
		writeAPIError(w, http.StatusConflict, "sign_in_running", "a sign-in of "+ref.Name+" is already running")
	case errors.Is(err, ErrAntigravityBuiltin):
		writeAPIError(w, http.StatusConflict, "builtin", "the built-in account signs in with `agy` in a terminal")
	case err != nil:
		writeAPIError(w, http.StatusServiceUnavailable, "sign_in_unavailable", truncateText(stripURLs(err.Error()), claudeErrorRunes))
	default:
		writeJSON(w, http.StatusAccepted, state)
	}
}

func (s *Server) handleGetAntigravitySignIn(w http.ResponseWriter, r *http.Request) {
	ref, _, ok := s.antigravityAccountRef(w, r)
	if !ok {
		return
	}
	agy := s.agyPortOrError(w)
	if agy == nil {
		return
	}
	writeJSON(w, http.StatusOK, agy.AntigravitySignInState(ref.Name))
}

func (s *Server) handleCancelAntigravitySignIn(w http.ResponseWriter, r *http.Request) {
	ref, _, ok := s.antigravityAccountRef(w, r)
	if !ok {
		return
	}
	agy := s.agyPortOrError(w)
	if agy == nil {
		return
	}
	writeJSON(w, http.StatusOK, agy.CancelAntigravitySignIn(ref.Name))
}

// handleAntigravitySignInCode hands the code Google showed to the waiting
// sign-in (API-AGY-023). The code is never logged or kept.
func (s *Server) handleAntigravitySignInCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code *string `json:"code"`
	}
	if !decodeJSONObject(w, r, providerBodyLimit, &body) {
		return
	}
	ref, _, ok := s.antigravityAccountRef(w, r)
	if !ok {
		return
	}
	code := ""
	if body.Code != nil {
		code = strings.TrimSpace(*body.Code)
	}
	if n := utf8.RuneCountInString(code); n == 0 || n > agyCodeMaxRunes ||
		strings.ContainsFunc(code, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		writeAPIError(w, http.StatusUnprocessableEntity, "bad_code", "the code must be 1 to 2048 characters on one line")
		return
	}
	agy := s.agyPortOrError(w)
	if agy == nil {
		return
	}
	state, err := agy.SubmitAntigravityCode(ref.Name, code)
	switch {
	case errors.Is(err, ErrAntigravityBadCode):
		writeAPIError(w, http.StatusUnprocessableEntity, "bad_code", "the code must be 1 to 2048 characters on one line")
	case errors.Is(err, ErrAntigravityNotWaiting):
		writeAPIError(w, http.StatusConflict, "not_waiting", "the sign-in of "+ref.Name+" is not waiting for a code")
	case err != nil:
		writeAPIError(w, http.StatusServiceUnavailable, "sign_in_unavailable", truncateText(stripURLs(err.Error()), claudeErrorRunes))
	default:
		writeJSON(w, http.StatusAccepted, state)
	}
}

// handlePutAntigravityLabel stores the display label of an added account as
// label in its table; "" removes it (API-AGY-024).
func (s *Server) handlePutAntigravityLabel(w http.ResponseWriter, r *http.Request) {
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
	if name == core.AntigravityProvider {
		writeAPIError(w, http.StatusConflict, "builtin", "the built-in account has no label")
		return
	}
	path := "providers." + name + ".label"
	if body.Label == nil {
		writeProviderIssue(w, path, "label is required")
		return
	}
	label, issue := validAccountLabel(*body.Label)
	if issue != "" {
		writeProviderIssue(w, path, issue)
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before antigravity label save", "error", err)
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
		s.logger.Error("web: parse config before antigravity label save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	table, ok := document.Providers[name]
	record := providerRecordFromMap(table)
	if !ok || record.Kind != core.AntigravityKind {
		writeAPIError(w, http.StatusNotFound, "not_found", "no Antigravity account named "+name)
		return
	}
	record.Label = label
	candidate, changed, err := webfiles.RewriteTable(r.Context(), providerChecker(store), file.Raw,
		"[providers."+name+"]", renderAntigravityAccountTable(name, record))
	if err != nil {
		s.writeProviderSaveError(w, "save antigravity label", err)
		return
	}
	restartRequired := !isLiveStore(store)
	if !bytes.Equal(candidate, file.Raw) {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for antigravity label save", "error", err)
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

// handleAntigravityStatus is GET /v1/router/antigravity (API-AGY-020).
func (s *Server) handleAntigravityStatus(w http.ResponseWriter, r *http.Request) {
	agy := s.agyPort()
	if agy == nil {
		if s.routerPortOrError(w) == nil {
			return
		}
		writeAPIError(w, http.StatusServiceUnavailable, "router_unavailable", "Antigravity is not available in this daemon")
		return
	}
	status, err := agy.AntigravityStatus(r.Context())
	if err != nil {
		s.writeRouterError(w, "antigravity status", err)
		return
	}
	if status.MinVersion == "" {
		status.MinVersion = AntigravityMinVersion
	}
	if status.Accounts == nil {
		status.Accounts = []AntigravityAccountStatus{}
	}
	s.decorateAntigravityAccounts(r.Context(), status.Accounts)
	writeJSON(w, http.StatusOK, status)
}

// decorateAntigravityAccounts fills the nil lists, the label and display
// name (the label, else the name), and the sign-in state of account rows.
// Labels come from config.toml, so a label just saved shows at once.
func (s *Server) decorateAntigravityAccounts(ctx context.Context, rows []AntigravityAccountStatus) {
	labels := map[string]string{}
	if s.configStore != nil {
		if file, err := s.configStore.ConfigFile(ctx); err == nil && file.Exists {
			if document, err := parseProviderDocument(file.Raw); err == nil {
				for name, table := range document.Providers {
					if record := providerRecordFromMap(table); record.Kind == core.AntigravityKind {
						labels[name] = record.Label
					}
				}
			}
		}
	}
	agy := s.agyPort()
	for i := range rows {
		row := &rows[i]
		if row.Windows == nil {
			row.Windows = []AntigravityWindow{}
		}
		if row.Agents == nil {
			row.Agents = []string{}
		}
		if row.Combos == nil {
			row.Combos = []string{}
		}
		if label, ok := labels[row.Name]; ok {
			row.Label = label
		}
		row.DisplayName = row.Label
		if row.DisplayName == "" {
			row.DisplayName = row.Name
		}
		state := AntigravitySignInStatus{State: AntigravitySignInIdle}
		if agy != nil {
			state = agy.AntigravitySignInState(row.Name)
		}
		row.SignIn = &state
	}
}

// putAntigravityAccount saves an Antigravity account through PUT
// /v1/providers/{name} (API-AGY-002, API-AGY-003): home, binary, label,
// allow_scheduled, disabled; the built-in account only binary,
// allow_scheduled, and disabled. An absent key keeps its stored value, and
// the pool keys always keep theirs. A named account's home is created (0700).
func (s *Server) putAntigravityAccount(w http.ResponseWriter, r *http.Request, store ConfigStore, name string, body map[string]json.RawMessage) {
	path := "providers." + name
	builtin := name == core.AntigravityProvider
	var issues []ConfigIssue
	for _, key := range []string{"base_url", "headers", "api_key", "api_key_env", "config_dir", "auth"} {
		if _, ok := body[key]; ok {
			issues = append(issues, ConfigIssue{Key: path + "." + key, Message: "not allowed for kind antigravity"})
		}
	}
	if builtin {
		for _, key := range []string{"home", "label"} {
			if _, ok := body[key]; ok {
				issues = append(issues, ConfigIssue{Key: path + "." + key, Message: "antigravity is built in"})
			}
		}
	}
	if len(issues) > 0 {
		writeProviderIssues(w, issues)
		return
	}
	expected, present, err := providerStringField(body, "expected_version")
	if err != nil || !present || expected == "" {
		writeProviderIssue(w, path+".expected_version", "expected_version is required")
		return
	}
	strs := map[string]*string{}
	bools := map[string]*bool{}
	for _, key := range []string{"home", "binary", "label", "allow_scheduled", "disabled"} {
		raw, ok := body[key]
		if !ok {
			continue
		}
		switch key {
		case "allow_scheduled", "disabled":
			var value bool
			if err := json.Unmarshal(raw, &value); err != nil {
				writeProviderIssue(w, path+"."+key, key+" must be a boolean")
				return
			}
			bools[key] = &value
		default:
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				writeProviderIssue(w, path+"."+key, key+" must be a string")
				return
			}
			value = strings.TrimSpace(value)
			if strings.ContainsAny(value, "\x00\r\n") {
				writeProviderIssue(w, path+"."+key, key+" must be one line")
				return
			}
			strs[key] = &value
		}
	}
	if v := strs["label"]; v != nil {
		label, issue := validAccountLabel(*v)
		if issue != "" {
			writeProviderIssue(w, path+".label", issue)
			return
		}
		strs["label"] = &label
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before antigravity account save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return
	}
	if file.Version != expected {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before antigravity account save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	storedTable := document.Providers[name]
	if builtin {
		storedTable = document.AgyBuiltin
	}
	record := providerRecord{}
	if stored := providerRecordFromMap(storedTable); stored.Kind == core.AntigravityKind {
		record = stored
	}
	record.Kind = core.AntigravityKind
	for key, value := range strs {
		switch key {
		case "home":
			record.Home = *value
		case "binary":
			record.Binary = *value
		case "label":
			record.Label = *value
		}
	}
	if v := bools["allow_scheduled"]; v != nil {
		record.AllowScheduled = *v
	}
	if v := bools["disabled"]; v != nil {
		record.Disabled = *v
	}
	candidate, changed, err := webfiles.RewriteTable(r.Context(), providerChecker(store), file.Raw,
		"[providers."+name+"]", renderAntigravityAccountTable(name, record))
	if err != nil {
		s.writeProviderSaveError(w, "save antigravity account", err)
		return
	}
	home := ""
	if !builtin {
		home = s.antigravityHome(name, record)
		if err := os.MkdirAll(home, 0o700); err != nil {
			writeProviderIssue(w, path+".home", "cannot create the account folder: "+err.Error())
			return
		}
	}
	configChanged := !bytes.Equal(candidate, file.Raw)
	restartRequired := !isLiveStore(store)
	if configChanged {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for antigravity account save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		s.auditSave("provider.save", providerConfigPath(file, s.providerHome()), name, changed)
		restartRequired = s.applyLiveRestartRequired(r.Context(), store, candidate)
	}
	writeJSON(w, http.StatusOK, providerSaveResponse{
		Name: name, Version: webfiles.ConfigVersion(candidate), RestartRequired: restartRequired, Home: home,
	})
}

// antigravityChoices returns the picker entries of Antigravity models
// (API-AGY-026): one group "Antigravity" when one account is enabled, else
// one group per enabled account, "Antigravity · <display name>". Nothing
// when the backend has no Antigravity or agy is not found.
func (s *Server) antigravityChoices(ctx context.Context, document providerDocument, accounts []string) []routerChoiceJSON {
	port := s.routerPort()
	agy, ok := port.(AntigravityBackend)
	if !ok || !agy.AntigravityInstalled(ctx) {
		return nil
	}
	enabled := []string{}
	if builtin := providerRecordFromMap(document.AgyBuiltin); !builtin.Disabled {
		enabled = append(enabled, core.AntigravityProvider)
	}
	enabled = append(enabled, accounts...)
	var out []routerChoiceJSON
	for _, account := range enabled {
		list, err := port.RouterModels(ctx, account, false)
		if err != nil {
			s.logger.Warn("web: antigravity models", "account", account, "error", err)
			continue
		}
		group := antigravityChoiceGroup
		if len(enabled) > 1 {
			display := account
			if label := providerRecordFromMap(document.Providers[account]).Label; label != "" {
				display = label
			}
			group = antigravityChoiceGroup + " · " + display
		}
		for _, model := range list.Models {
			label := model.Name
			if label == "" {
				label = "Antigravity · " + model.ID
			}
			out = append(out, modelChoice(model, label, group))
		}
	}
	return out
}
