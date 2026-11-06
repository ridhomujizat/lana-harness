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
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// providerBodyLimit is the largest accepted provider form or test body.
const providerBodyLimit = 64 << 10

// providerNameRE is the provider name rule of API-WEB-020.
var providerNameRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// providerEnvRE is the env name rule of API-WEB-019 and API-WEB-020.
var providerEnvRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// providerKinds are the kinds a provider form may save.
var providerKinds = map[string]bool{"openai_compat": true, "anthropic": true}

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
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	BaseURL   string   `json:"base_url"`
	APIKeyEnv string   `json:"api_key_env"`
	KeyState  string   `json:"key_state"`
	UsedBy    []string `json:"used_by"`
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
}

type providerTestRequest struct {
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
	Kind      string
	BaseURL   string
	APIKeyEnv string
	Headers   map[string]string
	Timeout   string
}

// providerValidationError carries config.Check issues through the section
// writer, the same way agentValidationError does.
type providerValidationError struct {
	issues []ConfigIssue
}

func (e *providerValidationError) Error() string { return "config.toml is not valid" }

// providerDocument is the part of config.toml the provider handlers read.
type providerDocument struct {
	Providers map[string]map[string]any `toml:"providers"`
	Agents    map[string]map[string]any `toml:"agents"`
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
	if !file.Exists {
		writeJSON(w, http.StatusOK, response)
		return
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config for provider list", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
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
	usedBy := providerUsage(document.Agents)

	names := make([]string, 0, len(document.Providers))
	for name := range document.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		record := providerRecordFromMap(document.Providers[name])
		used := usedBy[name]
		if used == nil {
			used = []string{}
		}
		response.Providers = append(response.Providers, providerJSON{
			Name:      name,
			Kind:      record.Kind,
			BaseURL:   record.BaseURL,
			APIKeyEnv: record.APIKeyEnv,
			KeyState:  providerKeyState(record.APIKeyEnv, secretValues),
			UsedBy:    used,
		})
	}
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

// providerUsage maps each provider name to the sorted agents that use it.
func providerUsage(agents map[string]map[string]any) map[string][]string {
	usage := map[string][]string{}
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		provider := mapProviderString(agents[name], "provider")
		if provider == "" {
			continue
		}
		usage[provider] = append(usage[provider], "agents."+name)
	}
	return usage
}

func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	var body providerTestRequest
	if !decodeJSONObject(w, r, providerBodyLimit, &body) {
		return
	}
	if !providerKinds[body.Kind] {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "kind must be openai_compat or anthropic")
		return
	}
	if err := validateProviderBaseURL(body.BaseURL); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(body.Model) == "" || strings.ContainsAny(body.Model, "\r\n") {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "model is required and must be one line")
		return
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
		"host", providerHost(body.BaseURL),
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
		writeProviderIssue(w, "providers."+name+".kind", "kind must be openai_compat or anthropic")
		return
	}
	baseURL, present, err := providerStringField(body, "base_url")
	if err != nil || !present || baseURL == "" {
		writeProviderIssue(w, "providers."+name+".base_url", "base_url is required")
		return
	}
	if err := validateProviderBaseURL(baseURL); err != nil {
		writeProviderIssue(w, "providers."+name+".base_url", err.Error())
		return
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
		result, err := tester.TestProvider(r.Context(), ProviderTest{
			Kind:      record.Kind,
			BaseURL:   record.BaseURL,
			Model:     providerTestModel(name, record.Kind, document.Agents),
			APIKeyEnv: apiKeyEnv,
			APIKey:    core.NewSecret(apiKey),
		})
		if err != nil {
			s.logger.Error("web: provider test before save", "error", redactProviderKey(err.Error(), apiKey))
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		if !result.OK {
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
		s.setSecretsChanged()
	}
	if configChanged || apiKey != "" {
		auditKeys := changed
		if apiKey != "" {
			auditKeys = append(auditKeys, "secrets."+apiKeyEnv)
		}
		s.auditSave("provider.save", providerConfigPath(file, s.providerHome()), name, auditKeys)
	}
	version := webfiles.ConfigVersion(candidate)
	writeJSON(w, http.StatusOK, providerSaveResponse{
		Name:            name,
		Version:         version,
		RestartRequired: true,
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
	if used := providerUsage(document.Agents)[name]; len(used) > 0 {
		issues := make([]ConfigIssue, 0, len(used))
		for _, agent := range used {
			issues = append(issues, ConfigIssue{
				Key:     agent + ".provider",
				Message: "provider '" + name + "' is used by " + agent,
			})
		}
		writeProviderIssues(w, issues)
		return
	}
	candidate, changed, err := webfiles.RewriteTable(r.Context(), providerChecker(store), file.Raw, "[providers."+name+"]", "")
	if err != nil {
		s.writeProviderSaveError(w, "delete provider", err)
		return
	}
	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		s.logger.Error("web: write config for provider delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	version := webfiles.ConfigVersion(candidate)
	s.auditSave("provider.delete", providerConfigPath(file, s.providerHome()), name, changed)
	writeJSON(w, http.StatusOK, providerSaveResponse{
		Name:            name,
		Version:         version,
		RestartRequired: true,
	})
}

func parseProviderDocument(raw []byte) (providerDocument, error) {
	document := providerDocument{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return document, nil
	}
	if err := toml.Unmarshal(raw, &document); err != nil {
		return providerDocument{}, err
	}
	return document, nil
}

func providerRecordFromMap(table map[string]any) providerRecord {
	record := providerRecord{
		Kind:      mapProviderString(table, "kind"),
		BaseURL:   mapProviderString(table, "base_url"),
		APIKeyEnv: mapProviderString(table, "api_key_env"),
		Timeout:   mapProviderString(table, "timeout"),
	}
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
// provider form has no model field, so it uses the model of an agent that
// already points at the provider and otherwise the provider's default model.
func providerTestModel(name, kind string, agents map[string]map[string]any) string {
	names := make([]string, 0, len(agents))
	for agent := range agents {
		names = append(names, agent)
	}
	sort.Strings(names)
	for _, agent := range names {
		table := agents[agent]
		if mapProviderString(table, "provider") != name {
			continue
		}
		if model := mapProviderString(table, "model"); model != "" {
			return model
		}
	}
	switch name {
	case "openai":
		return "gpt-5-mini"
	case "openrouter":
		return "openai/gpt-5-mini"
	case "deepseek":
		return "deepseek-flash"
	case "anthropic":
		return "claude-sonnet-4-5"
	}
	if kind == "anthropic" {
		return "claude-sonnet-4-5"
	}
	return "gpt-5-mini"
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
		"expected_version": true,
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
