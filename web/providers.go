package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/internal/models"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// providerBodyLimit is the largest accepted provider form or test body.
const providerBodyLimit = 64 << 10

// providerNameRE is the provider name rule of API-WEB-020.
var providerNameRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// providerEnvRE is the env name rule of API-WEB-019 and API-WEB-020.
var providerEnvRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// providerKinds are the kinds a provider form may save (API-RTR-001).
var providerKinds = map[string]bool{
	"openai": true, "deepseek": true, "openrouter": true, "anthropic": true, "openai_compat": true,
	core.ClaudeCodeKind: true,
}

// providerKindsMessage is the error text for a kind outside providerKinds.
const providerKindsMessage = "kind must be one of openai, deepseek, openrouter, anthropic, openai_compat, claude-code"

// claudeTestModel is the model of a Claude Code account test without one: the
// cheapest alias.
const claudeTestModel = "haiku"

// ProviderTest is one connection test. APIKey carries an inline key for this
// call only: the handler never writes it and the type prints "***" when it is
// formatted or logged.
type ProviderTest struct {
	Kind      string
	BaseURL   string
	Model     string
	APIKeyEnv string
	APIKey    core.Secret
}

// TestResult is the outcome of a provider test. The HTTP status is the
// provider's status, not the status of the web call; zero means no answer.
type TestResult struct {
	OK        bool
	LatencyMS int64
	Status    int
	Message   string
}

// ProviderTester is the daemon port for API-WEB-019. The adapter in cmd/lana
// implements it with the same provider package the runtime uses.
type ProviderTester interface {
	TestProvider(ctx context.Context, test ProviderTest) (TestResult, error)
}

// registerProviderRoutes installs the provider routes. The tester is read from
// the backend, so SetBackend installs it together with the other ports.
func (s *Server) registerProviderRoutes() {
	s.Handle("GET /v1/providers", http.HandlerFunc(s.handleListProviders))
	s.Handle("POST /v1/providers/test", http.HandlerFunc(s.handleTestProvider))
	s.Handle("PUT /v1/providers/{name}", http.HandlerFunc(s.handlePutProvider))
	s.Handle("DELETE /v1/providers/{name}", http.HandlerFunc(s.handleDeleteProvider))
}

type providerJSON struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	// DefaultBaseURL is the preset base URL of the kind, "" for
	// openai_compat (API-RTR-006).
	DefaultBaseURL string `json:"default_base_url"`
	// APIKeyEnv is the effective key env: api_key_env, or <NAME>_API_KEY
	// for a preset kind.
	APIKeyEnv string   `json:"api_key_env"`
	KeyState  string   `json:"key_state"`
	UsedBy    []string `json:"used_by"`
	// ModelsCount is the number of models the router lists for the provider.
	ModelsCount int `json:"models_count"`
	// Disabled is the disabled key of the entry (API-RTR-006 revision 2).
	Disabled bool `json:"disabled"`
	// Builtin marks the built-in Claude Code account "claude-code", whose
	// optional table holds only its settings (API-CCT-010, API-CCT-022).
	Builtin bool `json:"builtin"`
	// ConfigDir is the CLAUDE_CONFIG_DIR of a Claude Code account; "" for the
	// built-in one (Claude Code's own default, ~/.claude) and other kinds.
	ConfigDir string `json:"config_dir"`
	// AllowScheduled is the allow_scheduled key of a Claude Code account.
	AllowScheduled bool `json:"allow_scheduled"`
	// LoginCommand signs a Claude Code account in from a terminal.
	LoginCommand string `json:"login_command,omitempty"`
	// Label is the display label of a Claude Code account (REQ-CCT-012).
	Label string `json:"label,omitempty"`
}

type listProvidersResponse struct {
	Version         string         `json:"version"`
	Providers       []providerJSON `json:"providers"`
	RestartRequired bool           `json:"restart_required"`
}

type providerSaveResponse struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	RestartRequired bool   `json:"restart_required"`
	// ConfigDir and LoginCommand answer a Claude Code account save
	// (API-CCT-010); ConfigDir and Message a Claude Code account delete.
	ConfigDir    string `json:"config_dir,omitempty"`
	LoginCommand string `json:"login_command,omitempty"`
	Message      string `json:"message,omitempty"`
	// FolderRemoved answers a delete with remove_folder (REQ-CCT-012).
	FolderRemoved bool `json:"folder_removed,omitempty"`
}

type providerTestRequest struct {
	// Name is the provider being edited, optional. With an empty Model, the
	// model an agent or combo already uses with it is tested.
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key"`
	APIKeyEnv string `json:"api_key_env"`
}

type providerTestOKResponse struct {
	OK        bool  `json:"ok"`
	LatencyMS int64 `json:"latency_ms"`
}

type providerTestFailResponse struct {
	OK      bool   `json:"ok"`
	Status  int    `json:"status"`
	Message string `json:"message"`
}

// providerRecord is the [providers.<name>] table as the form edits it.
// Timeout stays raw: the form never sends it, so a save keeps the current value.
type providerRecord struct {
	Kind              string
	BaseURL           string
	APIKeyEnv         string
	Headers           map[string]string
	Timeout           string
	StreamIdleTimeout string
	ThinkingFormat    string
	Disabled          bool

	// Claude Code account keys (kind "claude-code", API-CCT-010).
	ConfigDir      string
	Binary         string
	Auth           string
	AllowScheduled bool
	Label          string
}

// providerValidationError carries config.Check issues through the section
// writer, the same way agentValidationError does.
type providerValidationError struct {
	issues []ConfigIssue
}

func (e *providerValidationError) Error() string { return "config.toml is not valid" }

// providerDocument is the part of config.toml the provider and router
// handlers read.
type providerDocument struct {
	Providers map[string]map[string]any `toml:"providers"`
	Agents    map[string]map[string]any `toml:"agents"`
	Combos    map[string]map[string]any `toml:"combos"`
	Knowledge map[string]any            `toml:"knowledge"`
	Router    map[string]any            `toml:"router"`
	// Builtin is the optional [providers.claude-code] table of the built-in
	// Claude Code account (API-CCT-022). parseProviderDocument moves it out
	// of Providers, so it is never listed as a configured provider.
	Builtin map[string]any `toml:"-"`
}

func (s *Server) providerTesterOrError(w http.ResponseWriter) ProviderTester {
	if s.backend != nil {
		if tester, ok := s.backend.(ProviderTester); ok {
			return tester
		}
	}
	s.logger.Error("web: provider tester is not configured")
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	return nil
}

// providerHome returns the LANA_HOME used for secrets.env.
func (s *Server) providerHome() string {
	if store, ok := s.configStore.(interface{ SettingsHome() string }); ok {
		if home := store.SettingsHome(); home != "" {
			return home
		}
	}
	return s.cfg.Home
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config for provider list", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	response := listProvidersResponse{
		Version:         file.Version,
		Providers:       []providerJSON{},
		RestartRequired: file.Version != store.ConfigLoadedVersion() || s.secretsRestartRequired(),
	}
	// API-CCT-010: the built-in Claude Code account is always listed.
	builtin := providerJSON{
		Name: core.ClaudeCodeProvider, Kind: core.ClaudeCodeKind, Builtin: true,
		KeyState: "unset", UsedBy: []string{}, LoginCommand: llmcall.ClaudeLoginCommand(""),
	}
	if !file.Exists {
		response.Providers = append(response.Providers, builtin)
		writeJSON(w, http.StatusOK, response)
		return
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config for provider list", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	builtin.UsedBy = document.providerUsedBy(core.ClaudeCodeProvider)
	// API-CCT-022: the built-in row shows its stored settings.
	if stored := providerRecordFromMap(document.Builtin); stored.Kind == core.ClaudeCodeKind {
		builtin.AllowScheduled = stored.AllowScheduled
		builtin.Disabled = stored.Disabled
	}
	secretValues := map[string]string{}
	if home := s.providerHome(); home != "" {
		values, readErr := webfiles.ReadSecrets(secretsPath(home))
		if readErr != nil {
			// The list is still useful; an unreadable secrets.env only makes
			// key_state fall back to the environment check.
			s.logger.Warn("web: read secrets for provider list", "error", readErr)
		} else {
			secretValues = values
		}
	}
	names := sortedMapKeys(document.Providers)
	modelLists := s.routerProviderModels(r.Context(), document, append(append([]string{}, names...), core.ClaudeCodeProvider))
	builtin.ModelsCount = len(modelLists[core.ClaudeCodeProvider])
	for _, name := range names {
		record := providerRecordFromMap(document.Providers[name])
		keyEnv := providerEffectiveKeyEnv(record, name)
		row := providerJSON{
			Name:           name,
			Kind:           record.Kind,
			BaseURL:        record.BaseURL,
			DefaultBaseURL: models.DefaultBaseURL(record.Kind),
			APIKeyEnv:      keyEnv,
			KeyState:       providerKeyState(keyEnv, secretValues),
			UsedBy:         document.providerUsedBy(name),
			ModelsCount:    len(modelLists[name]),
			Disabled:       record.Disabled,
		}
		if record.Kind == core.ClaudeCodeKind {
			row.ConfigDir = s.claudeConfigDir(name, record)
			row.AllowScheduled = record.AllowScheduled
			row.LoginCommand = llmcall.ClaudeLoginCommand(row.ConfigDir)
			row.Label = record.Label
		}
		response.Providers = append(response.Providers, row)
	}
	// The built-in account comes after the tables.
	response.Providers = append(response.Providers, builtin)
	writeJSON(w, http.StatusOK, response)
}

// providerKeyState reports api_key_env only, never a value.
func providerKeyState(envName string, fileValues map[string]string) string {
	if envName == "" {
		return "unset"
	}
	if value, ok := os.LookupEnv(envName); ok && value != "" {
		return "overridden_by_env"
	}
	if value, ok := fileValues[envName]; ok && value != "" {
		return "set"
	}
	return "unset"
}

func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	var body providerTestRequest
	if !decodeJSONObject(w, r, providerBodyLimit, &body) {
		return
	}
	if !providerKinds[body.Kind] {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", providerKindsMessage)
		return
	}
	if body.Kind == core.ClaudeCodeKind {
		s.testClaudeAccount(w, r, body)
		return
	}
	// base_url may be omitted for a preset kind; the adapter then uses the
	// kind's public API URL (API-RTR-006).
	if body.BaseURL == "" && models.DefaultBaseURL(body.Kind) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "base_url is required for kind "+body.Kind)
		return
	}
	if body.BaseURL != "" {
		if err := validateProviderBaseURL(body.BaseURL); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	}
	if strings.ContainsAny(body.Model, "\r\n") || strings.ContainsAny(body.Name, "\r\n") {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "model and name must be one line")
		return
	}
	body.Model = strings.TrimSpace(body.Model)
	if body.Model == "" {
		// The form leaves the test model empty unless the owner typed one;
		// the daemon picks a model it knows instead of a guess.
		body.Model = s.defaultProviderTestModel(r.Context(), strings.TrimSpace(body.Name), body.Kind, body.BaseURL)
		if body.Model == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "model is required for this provider: enter a test model")
			return
		}
	}
	hasKey := body.APIKey != ""
	hasEnv := body.APIKeyEnv != ""
	if hasKey == hasEnv {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "give exactly one of api_key or api_key_env")
		return
	}
	if hasEnv && !providerEnvRE.MatchString(body.APIKeyEnv) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "api_key_env must match [A-Za-z_][A-Za-z0-9_]*")
		return
	}
	tester := s.providerTesterOrError(w)
	if tester == nil {
		return
	}
	result, err := tester.TestProvider(r.Context(), ProviderTest{
		Kind:      body.Kind,
		BaseURL:   body.BaseURL,
		Model:     body.Model,
		APIKeyEnv: body.APIKeyEnv,
		APIKey:    core.NewSecret(body.APIKey),
	})
	if err != nil {
		// Keep a provider message from echoing the inline key into the log.
		s.logger.Error("web: provider test", "error", redactProviderKey(err.Error(), body.APIKey))
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	// The log line carries the host and latency only; the inline key never
	// reaches it.
	s.logger.Debug("web: provider test",
		"host", providerHost(providerBaseURLOrDefault(body.Kind, body.BaseURL)),
		"latency_ms", result.LatencyMS,
		"ok", result.OK,
		"status", result.Status,
	)
	if result.OK {
		writeJSON(w, http.StatusOK, providerTestOKResponse{OK: true, LatencyMS: result.LatencyMS})
		return
	}
	writeJSON(w, http.StatusOK, providerTestFailResponse{
		OK:      false,
		Status:  result.Status,
		Message: redactProviderKey(result.Message, body.APIKey),
	})
}

func (s *Server) handlePutProvider(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	if !providerNameRE.MatchString(name) {
		writeProviderIssue(w, "name", "name must match [a-z0-9-]+")
		return
	}
	var body map[string]json.RawMessage
	if !decodeJSONObject(w, r, providerBodyLimit, &body) {
		return
	}
	if issues := unknownProviderKeys(body); len(issues) > 0 {
		for i := range issues {
			issues[i].Key = "providers." + name + "." + issues[i].Key
		}
		writeProviderIssues(w, issues)
		return
	}
	kind, present, err := providerStringField(body, "kind")
	if err != nil || !present || kind == "" {
		writeProviderIssue(w, "providers."+name+".kind", "kind is required")
		return
	}
	if !providerKinds[kind] {
		writeProviderIssue(w, "providers."+name+".kind", providerKindsMessage)
		return
	}
	if name == core.ClaudeCodeProvider && kind != core.ClaudeCodeKind {
		writeProviderIssue(w, "providers."+name, "claude-code is built in")
		return
	}
	if kind == core.ClaudeCodeKind {
		s.putClaudeAccount(w, r, store, name, body)
		return
	}
	if issues := claudeOnlyProviderKeys(body); len(issues) > 0 {
		for i := range issues {
			issues[i].Key = "providers." + name + "." + issues[i].Key
		}
		writeProviderIssues(w, issues)
		return
	}
	// base_url is optional for the preset kinds; config validation decides
	// per kind (openai_compat needs it).
	baseURL, _, err := providerStringField(body, "base_url")
	if err != nil {
		writeProviderIssue(w, "providers."+name+".base_url", "base_url must be a string")
		return
	}
	if baseURL != "" {
		if err := validateProviderBaseURL(baseURL); err != nil {
			writeProviderIssue(w, "providers."+name+".base_url", err.Error())
			return
		}
	}
	expected, present, err := providerStringField(body, "expected_version")
	if err != nil || !present || expected == "" {
		writeProviderIssue(w, "providers."+name+".expected_version", "expected_version is required")
		return
	}
	apiKeyEnv, present, err := providerStringField(body, "api_key_env")
	if err != nil {
		writeProviderIssue(w, "providers."+name+".api_key_env", "api_key_env must be a string")
		return
	}
	if !present || apiKeyEnv == "" {
		apiKeyEnv = defaultProviderKeyEnv(name)
	}
	if !providerEnvRE.MatchString(apiKeyEnv) {
		writeProviderIssue(w, "providers."+name+".api_key_env", "api_key_env must match [A-Za-z_][A-Za-z0-9_]*")
		return
	}
	apiKey, _, err := providerStringField(body, "api_key")
	if err != nil {
		writeProviderIssue(w, "providers."+name+".api_key", "api_key must be a string")
		return
	}
	if apiKey != "" {
		if err := webfiles.ValidateSecretValue(apiKey); err != nil {
			writeProviderIssue(w, "providers."+name+".api_key", secretValidationMessage(err))
			return
		}
	}
	// disabled is optional: an absent key keeps the stored value, so an edit
	// that does not know the switch never turns a provider back on.
	var disabled *bool
	if raw, ok := body["disabled"]; ok {
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			writeProviderIssue(w, "providers."+name+".disabled", "disabled must be a boolean")
			return
		}
		disabled = &value
	}
	var newHeaders map[string]string
	headersPresent := false
	if raw, ok := body["headers"]; ok {
		headersPresent = true
		if err := json.Unmarshal(raw, &newHeaders); err != nil {
			writeProviderIssue(w, "providers."+name+".headers", "headers must be an object of strings")
			return
		}
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before provider save", "error", err)
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
		s.logger.Error("web: parse config before provider save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	record := providerRecordFromMap(document.Providers[name])
	record.Kind = kind
	record.BaseURL = baseURL
	record.APIKeyEnv = apiKeyEnv
	if disabled != nil {
		record.Disabled = *disabled
	}
	if headersPresent {
		record.Headers, err = mergeProviderHeaders(record.Headers, newHeaders)
		if err != nil {
			writeProviderIssue(w, "providers."+name+".headers", err.Error())
			return
		}
	}

	// A new key is tested before anything is written. The form has no model,
	// so the test uses the model of an agent that uses this provider, or the
	// default model for a known provider name.
	if apiKey != "" {
		tester := s.providerTesterOrError(w)
		if tester == nil {
			return
		}
		guessed := knownProviderTestModel(name, record, document) == ""
		result, err := tester.TestProvider(r.Context(), ProviderTest{
			Kind:      record.Kind,
			BaseURL:   record.BaseURL,
			Model:     providerTestModel(name, record, document),
			APIKeyEnv: apiKeyEnv,
			APIKey:    core.NewSecret(apiKey),
		})
		if err != nil {
			s.logger.Error("web: provider test before save", "error", redactProviderKey(err.Error(), apiKey))
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		// With a guessed model, a 404 means the server took the key and does
		// not serve that model: the key works, so it is saved.
		if !result.OK && (!guessed || result.Status != http.StatusNotFound) {
			message := redactProviderKey(result.Message, apiKey)
			if message == "" {
				message = "provider connection test failed"
			}
			writeAPIError(w, http.StatusUnprocessableEntity, "connection_failed", message)
			return
		}
	}

	table := renderProviderTable(name, record)
	candidate, changed, err := webfiles.RewriteTable(r.Context(), providerChecker(store), file.Raw, "[providers."+name+"]", table)
	if err != nil {
		s.writeProviderSaveError(w, "save provider", err)
		return
	}
	configChanged := !bytes.Equal(candidate, file.Raw)
	if configChanged {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for provider save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
	}
	if apiKey != "" {
		home := s.providerHome()
		if home == "" {
			s.logger.Error("web: provider save has no home")
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		if err := webfiles.SetSecret(secretsPath(home), apiKeyEnv, apiKey); err != nil {
			s.logger.Error("web: save provider secret", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		if err := webfiles.SetSecretMeta(secretsMetaPath(home), apiKeyEnv, s.nowTime().UnixMilli()); err != nil {
			// The secret itself is written; a meta failure only loses the
			// update time.
			s.logger.Warn("web: write secrets meta for provider", "error", err)
		}
		// A live store reads secrets.env on lookup, so the key reaches the
		// running router without a restart (REQ-RTR-008).
		if !isLiveStore(store) {
			s.setSecretsChanged()
		}
	}
	if configChanged || apiKey != "" {
		auditKeys := changed
		if apiKey != "" {
			auditKeys = append(auditKeys, "secrets."+apiKeyEnv)
		}
		s.auditSave("provider.save", providerConfigPath(file, s.providerHome()), name, auditKeys)
	}
	restartRequired := !isLiveStore(store)
	if configChanged {
		restartRequired = s.applyLiveRestartRequired(r.Context(), store, candidate)
	}
	version := webfiles.ConfigVersion(candidate)
	writeJSON(w, http.StatusOK, providerSaveResponse{
		Name:            name,
		Version:         version,
		RestartRequired: restartRequired,
	})
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
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
		ExpectedVersion string `json:"expected_version"`
	}
	if !decodeJSONObject(w, r, providerBodyLimit, &body) {
		return
	}
	if body.ExpectedVersion == "" {
		writeProviderIssue(w, "providers."+name+".expected_version", "expected_version is required")
		return
	}
	if name == core.ClaudeCodeProvider {
		writeAPIError(w, http.StatusConflict, "builtin", "claude-code is built in and cannot be deleted")
		return
	}
	// REQ-CCT-012: ?remove_folder=1 on a Claude Code account signs it out
	// and deletes its folder before the table goes.
	removeFolder := false
	switch r.URL.Query().Get("remove_folder") {
	case "", "0", "false":
	case "1", "true":
		removeFolder = true
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "remove_folder must be 1 or 0")
		return
	}
	var signOutNote string
	if removeFolder {
		var ok bool
		if signOutNote, ok = s.signOutClaudeAccountForRemoval(w, r, store, name, body.ExpectedVersion); !ok {
			return
		}
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before provider delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return
	}
	if file.Version != body.ExpectedVersion {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before provider delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if _, exists := document.Providers[name]; !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "provider is not configured")
		return
	}
	if message := document.providerInUse(name); message != "" {
		writeAPIError(w, http.StatusConflict, "in_use", message)
		return
	}
	record := providerRecordFromMap(document.Providers[name])
	candidate, changed, err := webfiles.RewriteTable(r.Context(), providerChecker(store), file.Raw, "[providers."+name+"]", "")
	if err != nil {
		s.writeProviderSaveError(w, "delete provider", err)
		return
	}
	folderRemoved := false
	if record.Kind == core.ClaudeCodeKind {
		if auth := s.claudeAuthPort(); auth != nil {
			auth.ForgetClaudeSignIn(name)
		}
		if removeFolder {
			dir := s.claudeConfigDir(name, record)
			if !s.claudeFolderRemovable(dir) {
				writeClaudeFolderOutside(w, dir)
				return
			}
			if err := os.RemoveAll(dir); err != nil {
				s.logger.Error("web: remove claude account folder", "error", err)
				writeAPIError(w, http.StatusInternalServerError, "internal_error", "cannot delete the account folder")
				return
			}
			folderRemoved = true
		}
	}
	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		s.logger.Error("web: write config for provider delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	version := webfiles.ConfigVersion(candidate)
	s.auditSave("provider.delete", providerConfigPath(file, s.providerHome()), name, changed)
	response := providerSaveResponse{
		Name:            name,
		Version:         version,
		RestartRequired: s.applyLiveRestartRequired(r.Context(), store, candidate),
	}
	// API-CCT-010: without remove_folder only the table goes; the account's
	// folder (and its login) stays where it is.
	if record.Kind == core.ClaudeCodeKind {
		response.ConfigDir = s.claudeConfigDir(name, record)
		if folderRemoved {
			response.FolderRemoved = true
			response.Message = "The account was signed out and its folder " + response.ConfigDir + " was deleted."
			if signOutNote != "" {
				response.Message = "The account folder " + response.ConfigDir + " was deleted. Sign-out failed: " + signOutNote
			}
		} else {
			response.Message = "The account folder " + response.ConfigDir + " was kept; delete it yourself to remove its login."
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func parseProviderDocument(raw []byte) (providerDocument, error) {
	document := providerDocument{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return document, nil
	}
	if err := toml.Unmarshal(raw, &document); err != nil {
		return providerDocument{}, err
	}
	if table, ok := document.Providers[core.ClaudeCodeProvider]; ok {
		document.Builtin = table
		delete(document.Providers, core.ClaudeCodeProvider)
	}
	return document, nil
}

func providerRecordFromMap(table map[string]any) providerRecord {
	record := providerRecord{
		Kind:      mapProviderString(table, "kind"),
		BaseURL:   mapProviderString(table, "base_url"),
		APIKeyEnv: mapProviderString(table, "api_key_env"),
		Timeout:   mapProviderString(table, "timeout"),

		StreamIdleTimeout: mapProviderString(table, "stream_idle_timeout"),
		ThinkingFormat:    mapProviderString(table, "thinking_format"),
	}
	record.Disabled, _ = table["disabled"].(bool)
	record.ConfigDir = mapProviderString(table, "config_dir")
	record.Binary = mapProviderString(table, "binary")
	record.Auth = mapProviderString(table, "auth")
	record.AllowScheduled, _ = table["allow_scheduled"].(bool)
	record.Label = mapProviderString(table, "label")
	if headers, ok := table["headers"].(map[string]any); ok {
		record.Headers = make(map[string]string, len(headers))
		for key, value := range headers {
			if text, ok := value.(string); ok {
				record.Headers[key] = text
			}
		}
	}
	return record
}

func mapProviderString(table map[string]any, key string) string {
	value, _ := table[key].(string)
	return value
}

// mergeProviderHeaders keeps a stored value for "***" and refuses a mask that
// has no stored value (API-WEB-020).
func mergeProviderHeaders(stored, body map[string]string) (map[string]string, error) {
	if len(body) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(body))
	for key, value := range body {
		if value == "***" {
			kept, ok := stored[key]
			if !ok {
				return nil, fmt.Errorf("header %q has no stored value", key)
			}
			out[key] = kept
			continue
		}
		out[key] = value
	}
	return out, nil
}

func renderProviderTable(name string, record providerRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[providers.%s]\n", name)
	writeProviderString(&b, "kind", record.Kind)
	writeProviderString(&b, "base_url", record.BaseURL)
	writeProviderString(&b, "api_key_env", record.APIKeyEnv)
	if len(record.Headers) > 0 {
		keys := make([]string, 0, len(record.Headers))
		for key := range record.Headers {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, providerTOMLKey(key)+" = "+providerTOMLString(record.Headers[key]))
		}
		fmt.Fprintf(&b, "headers = { %s }\n", strings.Join(parts, ", "))
	}
	writeProviderString(&b, "timeout", record.Timeout)
	writeProviderString(&b, "stream_idle_timeout", record.StreamIdleTimeout)
	writeProviderString(&b, "thinking_format", record.ThinkingFormat)
	if record.Disabled {
		b.WriteString("disabled = true\n")
	}
	return b.String()
}

func writeProviderString(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "%s = %s\n", key, providerTOMLString(value))
}

// providerTOMLKey writes a bare key when it can, otherwise a quoted key.
func providerTOMLKey(key string) string {
	bare := key != ""
	for i := 0; i < len(key); i++ {
		if !isProviderBareKeyChar(key[i]) {
			bare = false
			break
		}
	}
	if bare {
		return key
	}
	data, err := toml.Marshal(key)
	if err != nil {
		return `""`
	}
	return strings.TrimSuffix(string(data), "\n")
}

func isProviderBareKeyChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func providerTOMLString(value string) string {
	data, err := toml.Marshal(value)
	if err != nil {
		return `""`
	}
	return strings.TrimSuffix(string(data), "\n")
}

func defaultProviderKeyEnv(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_API_KEY"
}

// providerTestModel picks the model the save-time connection test uses. The
// provider form has no model field, so it uses the model of an agent or combo
// that already points at the provider, then the kind's cheapest catalog model
// (internal/models.TestModel), then the base URL's catalog, then gpt-5-mini.
func providerTestModel(name string, record providerRecord, document providerDocument) string {
	if model := knownProviderTestModel(name, record, document); model != "" {
		return model
	}
	return "gpt-5-mini"
}

// defaultProviderTestModel picks the model of a connection test whose body
// names none (API-RTR-006): the model an agent or combo already uses with the
// named provider, then the kind's test model, then the base URL's catalog. It
// returns "" when none is known.
func (s *Server) defaultProviderTestModel(ctx context.Context, name, kind, baseURL string) string {
	document := providerDocument{}
	if name != "" && s.configStore != nil {
		if file, err := s.configStore.ConfigFile(ctx); err == nil && file.Exists {
			if parsed, err := parseProviderDocument(file.Raw); err == nil {
				document = parsed
			}
		}
	}
	return knownProviderTestModel(name, providerRecord{Kind: kind, BaseURL: baseURL}, document)
}

// knownProviderTestModel is providerTestModel without the final guess.
func knownProviderTestModel(name string, record providerRecord, document providerDocument) string {
	for _, agent := range sortedMapKeys(document.Agents) {
		table := document.Agents[agent]
		legacy := mapProviderString(table, "provider")
		model := mapProviderString(table, "model")
		if legacy == name && model != "" {
			return model
		}
		if legacy != "" {
			continue
		}
		if _, isCombo := document.Combos[model]; isCombo {
			continue
		}
		if provider, id, ok := parseModelAddress(model); ok && provider == name {
			return id
		}
	}
	for _, combo := range sortedMapKeys(document.Combos) {
		for _, address := range mapProviderStrings(document.Combos[combo], "models") {
			if provider, id, ok := parseModelAddress(address); ok && provider == name {
				return id
			}
		}
	}
	if model := models.TestModel(record.Kind); model != "" {
		return model
	}
	if model := models.TestModelForBaseURL(record.BaseURL); model != "" {
		return model
	}
	if catalog := models.ForProviderKind(record.BaseURL); len(catalog) > 0 {
		return catalog[0].ID
	}
	return ""
}

// providerBaseURLOrDefault returns baseURL, or the kind's preset URL when it
// is empty.
func providerBaseURLOrDefault(kind, baseURL string) string {
	if baseURL != "" {
		return baseURL
	}
	return models.DefaultBaseURL(kind)
}

// providerEffectiveKeyEnv mirrors router.EffectiveKeyEnv, which web cannot
// import: api_key_env, else <NAME>_API_KEY for a preset kind, else "".
func providerEffectiveKeyEnv(record providerRecord, name string) string {
	if record.APIKeyEnv != "" {
		return record.APIKeyEnv
	}
	if models.DefaultBaseURL(record.Kind) != "" {
		return defaultProviderKeyEnv(name)
	}
	return ""
}

func validateProviderBaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("base_url must be an http:// or https:// URL")
	}
	return nil
}

func providerHost(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		return baseURL
	}
	return parsed.Hostname()
}

// redactProviderKey is a last line of defense for a provider message that
// echoes the key. The key itself is never written or logged.
func redactProviderKey(message, key string) string {
	if key == "" {
		return message
	}
	return strings.ReplaceAll(message, key, "***")
}

func unknownProviderKeys(body map[string]json.RawMessage) []ConfigIssue {
	allowed := map[string]bool{
		"kind":             true,
		"base_url":         true,
		"api_key_env":      true,
		"api_key":          true,
		"headers":          true,
		"disabled":         true,
		"expected_version": true,
		// Claude Code account keys (API-CCT-010).
		"config_dir":      true,
		"binary":          true,
		"auth":            true,
		"allow_scheduled": true,
	}
	keys := make([]string, 0, len(body))
	for key := range body {
		if !allowed[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	issues := make([]ConfigIssue, 0, len(keys))
	for _, key := range keys {
		issues = append(issues, ConfigIssue{Key: key, Message: "unknown key"})
	}
	return issues
}

func providerStringField(body map[string]json.RawMessage, key string) (string, bool, error) {
	raw, ok := body[key]
	if !ok {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, err
	}
	return value, true, nil
}

func providerChecker(store ConfigStore) func(context.Context, []byte) error {
	return func(ctx context.Context, candidate []byte) error {
		issues, err := store.ConfigCheck(ctx, candidate)
		if err != nil {
			return err
		}
		if len(issues) > 0 {
			return &providerValidationError{issues: issues}
		}
		return nil
	}
}

func (s *Server) writeProviderSaveError(w http.ResponseWriter, operation string, err error) {
	var validation *providerValidationError
	if errors.As(err, &validation) {
		writeProviderIssues(w, validation.issues)
		return
	}
	s.logger.Error("web: "+operation, "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeProviderIssue(w http.ResponseWriter, key, message string) {
	writeProviderIssues(w, []ConfigIssue{{Key: key, Message: message}})
}

func writeProviderIssues(w http.ResponseWriter, issues []ConfigIssue) {
	details := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		details = append(details, map[string]any{
			"file":    issue.File,
			"key":     issue.Key,
			"field":   issue.Key,
			"message": issue.Message,
		})
	}
	writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "provider is not valid", details)
}

func providerConfigPath(file ConfigFile, home string) string {
	if file.Path != "" {
		return file.Path
	}
	if home == "" {
		return "config.toml"
	}
	return filepath.Join(home, "config.toml")
}

// secretValidationMessage keeps the webfiles message without the value.
func secretValidationMessage(err error) string {
	var validation *webfiles.SecretValidationError
	if errors.As(err, &validation) {
		return validation.Message
	}
	return err.Error()
}
