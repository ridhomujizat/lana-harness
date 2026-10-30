package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// setupBodyLimit is the largest accepted POST /v1/setup body.
const setupBodyLimit = 64 << 10

// SetupStatus is the read model of API-WEB-018.
type SetupStatus struct {
	Needed        bool     `json:"needed"`
	Reasons       []string `json:"reasons"`
	ProviderCount int      `json:"provider_count"`
	AgentCount    int      `json:"agent_count"`
}

// setupConfigDocument is the part of config.toml the setup handlers count.
type setupConfigDocument struct {
	Providers map[string]any `toml:"providers"`
	Agents    map[string]any `toml:"agents"`
}

// setupProviderPreset is one provider card of the setup wizard.
type setupProviderPreset struct {
	name    string
	kind    string
	baseURL string
	keyEnv  string
}

// setupProviderPresets maps the setup provider cards to config provider tables.
// Only the openai_compat provider adapter exists in this repo, so every card
// uses it (owner decision Q-029). Anthropic is reached through its
// OpenAI-compatible endpoint until a native adapter exists.
var setupProviderPresets = map[string]setupProviderPreset{
	"openai": {
		name:    "openai",
		kind:    "openai_compat",
		baseURL: "https://api.openai.com/v1",
		keyEnv:  "OPENAI_API_KEY",
	},
	"anthropic": {
		name:    "anthropic",
		kind:    "openai_compat",
		baseURL: "https://api.anthropic.com/v1",
		keyEnv:  "ANTHROPIC_API_KEY",
	},
	"openrouter": {
		name:    "openrouter",
		kind:    "openai_compat",
		baseURL: "https://openrouter.ai/api/v1",
		keyEnv:  "OPENROUTER_API_KEY",
	},
}

// setupRequest is the POST /v1/setup body of API-WEB-021.
type setupRequest struct {
	Provider        setupProviderRequest `json:"provider"`
	Agent           setupAgentRequest    `json:"agent"`
	Persona         setupPersonaRequest  `json:"persona"`
	ExpectedVersion *string              `json:"expected_version"`
}

type setupProviderRequest struct {
	Name    string `json:"name"`
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

type setupAgentRequest struct {
	Name       string `json:"name"`
	Template   string `json:"template"`
	Workspace  string `json:"workspace"`
	Permission string `json:"permission"`
}

type setupPersonaRequest struct {
	AgentMD string `json:"agent_md"`
	UserMD  string `json:"user_md"`
}

type setupSaveResponse struct {
	Restart string `json:"restart"`
	PID     int    `json:"pid"`
}

// setupPlan is a validated setup request. It carries only values that are safe
// to write; the inline key is never logged or returned.
type setupPlan struct {
	provider   setupProviderPreset
	baseURL    string
	model      string
	apiKey     string
	agentName  string
	workspace  string
	permission string
	template   agenthome.Template
	agentMD    string
	userMD     string
}

// setupInputError is a rejected setup field. code selects validation_failed or
// over_limit; over_limit also carries the size and the limit.
type setupInputError struct {
	code       string
	field      string
	message    string
	characters int
	limit      int
}

func (e *setupInputError) Error() string { return e.message }

func setupFieldError(field, message string) *setupInputError {
	return &setupInputError{code: "validation_failed", field: field, message: message}
}

// registerSetupRoutes registers API-WEB-018 and API-WEB-021 through Handle so
// the owner-cookie and CSRF checks apply.
func (s *Server) registerSetupRoutes() {
	s.Handle("GET /v1/setup", http.HandlerFunc(s.handleGetSetup))
	s.Handle("POST /v1/setup", http.HandlerFunc(s.handleSaveSetup))
}

func (s *Server) handleGetSetup(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	status, err := setupState(r.Context(), store)
	if err != nil {
		s.logger.Error("web: read setup state", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// setupState reads config.toml at call time. Setup is needed when the file is
// missing or has no [providers.*] or [agents.*] table.
func setupState(ctx context.Context, store ConfigStore) (SetupStatus, error) {
	file, err := store.ConfigFile(ctx)
	if err != nil {
		return SetupStatus{}, err
	}
	return setupStateFromFile(file)
}

func setupStateFromFile(file ConfigFile) (SetupStatus, error) {
	status := SetupStatus{Reasons: []string{}}
	if !file.Exists {
		status.Reasons = append(status.Reasons, "config_missing")
	}
	if len(bytes.TrimSpace(file.Raw)) > 0 {
		var document setupConfigDocument
		if err := toml.Unmarshal(file.Raw, &document); err != nil {
			return SetupStatus{}, err
		}
		status.ProviderCount = len(document.Providers)
		status.AgentCount = len(document.Agents)
	}
	if status.ProviderCount == 0 {
		status.Reasons = append(status.Reasons, "no_provider")
	}
	if status.AgentCount == 0 {
		status.Reasons = append(status.Reasons, "no_agent")
	}
	status.Needed = len(status.Reasons) > 0
	return status, nil
}

// handleSaveSetup runs the steps of design.md "Setup save" in order. It stops at
// the first failure; a failure after the config write leaves the earlier files
// in place, as the contract allows.
func (s *Server) handleSaveSetup(w http.ResponseWriter, r *http.Request) {
	var body setupRequest
	if !decodeJSONObject(w, r, setupBodyLimit, &body) {
		return
	}
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before setup save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	status, err := setupStateFromFile(file)
	if err != nil {
		s.logger.Error("web: read setup state before save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !status.Needed {
		writeAPIError(w, http.StatusConflict, "setup_not_needed", "setup is not needed")
		return
	}
	if body.ExpectedVersion == nil {
		writeSetupInputError(w, setupFieldError("expected_version", "expected_version is required"))
		return
	}
	if file.Version != *body.ExpectedVersion {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return
	}

	plan, inputErr := validateSetup(&body)
	if inputErr != nil {
		writeSetupInputError(w, inputErr)
		return
	}
	home := s.agentHome()
	if home == "" {
		s.logger.Error("web: setup save has no home")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	paths := agenthome.Paths(home, plan.agentName)
	tables, err := parseAgentTables(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before setup save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if _, exists := tables[plan.agentName]; exists {
		writeAgentExists(w)
		return
	}
	if exists, err := pathExists(paths.Dir); err != nil {
		s.logger.Error("web: stat agent home for setup", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	} else if exists {
		writeAgentExists(w)
		return
	}

	// The ports are read only after the cheap refusals, so a backend without
	// them still reports setup_not_needed and validation failures correctly.
	tester := s.providerTesterOrError(w)
	if tester == nil {
		return
	}
	daemon := s.daemonStoreOrError(w)
	if daemon == nil {
		return
	}

	result, err := tester.TestProvider(r.Context(), ProviderTest{
		Kind:      plan.provider.kind,
		BaseURL:   plan.baseURL,
		Model:     plan.model,
		APIKeyEnv: plan.provider.keyEnv,
		APIKey:    core.NewSecret(plan.apiKey),
	})
	if err != nil {
		s.logger.Error("web: setup provider test", "error", redactProviderKey(err.Error(), plan.apiKey))
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !result.OK {
		message := redactProviderKey(result.Message, plan.apiKey)
		if message == "" {
			message = "provider connection test failed"
		}
		writeAPIError(w, http.StatusUnprocessableEntity, "connection_failed", message)
		return
	}

	raw := file.Raw
	if !file.Exists {
		raw = setupConfigSeed(s.Addr())
	}
	providerTable := renderProviderTable(plan.provider.name, providerRecord{
		Kind:      plan.provider.kind,
		BaseURL:   plan.baseURL,
		APIKeyEnv: plan.provider.keyEnv,
	})
	candidate, providerKeys, err := webfiles.RewriteTable(r.Context(), agentChecker(store), raw, "[providers."+plan.provider.name+"]", providerTable)
	if err != nil {
		s.writeSetupSaveError(w, "config", err)
		return
	}
	agentTable := renderAgentTable(plan.agentName, agentRecord{
		Provider:   plan.provider.name,
		Model:      plan.model,
		Workspace:  plan.workspace,
		Tools:      append([]string(nil), plan.template.Tools...),
		Permission: plan.permission,
	})
	candidate, agentKeys, err := webfiles.RewriteTable(r.Context(), agentChecker(store), candidate, "[agents."+plan.agentName+"]", agentTable)
	if err != nil {
		s.writeSetupSaveError(w, "config", err)
		return
	}

	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		s.writeSetupStepError(w, "config", err)
		return
	}
	if err := webfiles.SetSecret(secretsPath(home), plan.provider.keyEnv, plan.apiKey); err != nil {
		s.writeSetupStepError(w, "secrets", err)
		return
	}
	if err := webfiles.SetSecretMeta(secretsMetaPath(home), plan.provider.keyEnv, s.nowTime().UnixMilli()); err != nil {
		// The key itself is written; a meta failure only loses the update time.
		s.logger.Warn("web: write setup secrets meta", "error", err)
	}
	s.setSecretsChanged()
	if err := agenthome.WriteFile(paths.Agent, plan.agentMD); err != nil {
		s.writeSetupStepError(w, "agent_home", err)
		return
	}
	if err := writeSetupUserFile(home, plan.userMD); err != nil {
		s.writeSetupStepError(w, "user_md", err)
		return
	}

	changed := append(append([]string{}, providerKeys...), agentKeys...)
	sort.Strings(changed)
	s.auditSave("setup.save", setupConfigPath(file, home), plan.agentName, changed)

	info := daemon.DaemonInfo()
	if err := daemon.Restart(r.Context()); err != nil {
		s.logger.Error("web: schedule setup restart", "error", err)
		writeAPIErrorDetails(w, http.StatusInternalServerError, "restart_failed", "restart could not be prepared", []map[string]any{
			{"step": "restart"},
		})
		return
	}
	writeJSON(w, http.StatusAccepted, setupSaveResponse{Restart: "scheduled", PID: info.PID})
}

// validateSetup checks the body before any provider call or write.
func validateSetup(body *setupRequest) (*setupPlan, *setupInputError) {
	preset, ok := setupProviderPresets[body.Provider.Name]
	if !ok {
		return nil, setupFieldError("provider.name", "name must be openai, anthropic, or openrouter")
	}
	apiKey := strings.TrimSpace(body.Provider.APIKey)
	if apiKey == "" {
		return nil, setupFieldError("provider.api_key", "api_key is required")
	}
	if err := webfiles.ValidateSecretValue(apiKey); err != nil {
		return nil, setupFieldError("provider.api_key", secretValidationMessage(err))
	}
	model := strings.TrimSpace(body.Provider.Model)
	if model == "" || strings.ContainsAny(model, "\r\n") {
		return nil, setupFieldError("provider.model", "model is required and must be one line")
	}
	baseURL := strings.TrimSpace(body.Provider.BaseURL)
	if baseURL == "" {
		baseURL = preset.baseURL
	}
	if err := validateProviderBaseURL(baseURL); err != nil {
		return nil, setupFieldError("provider.base_url", err.Error())
	}

	agentName := body.Agent.Name
	if !agentNameRE.MatchString(agentName) {
		return nil, setupFieldError("agent.name", "name must match [a-z0-9-]+")
	}
	template, ok := agenthome.LookupTemplate(body.Agent.Template)
	if !ok {
		return nil, setupFieldError("agent.template", "template must be assistant, coder, or worker")
	}
	workspace := strings.TrimSpace(body.Agent.Workspace)
	if workspace == "" {
		workspace = "~/lana/workspace"
	}
	if strings.ContainsAny(workspace, "\r\n") {
		return nil, setupFieldError("agent.workspace", "workspace must be one line")
	}
	permission := strings.TrimSpace(body.Agent.Permission)
	if permission == "" {
		permission = "ask"
	}
	if !validAgentPermission(permission) {
		return nil, setupFieldError("agent.permission", "permission must be one of ask, trust, never")
	}
	if strings.TrimSpace(body.Persona.AgentMD) == "" {
		return nil, setupFieldError("persona.agent_md", "agent_md is required")
	}
	characters := utf8.RuneCountInString(body.Persona.UserMD)
	if characters > agenthome.UserLimit {
		return nil, &setupInputError{
			code:       "over_limit",
			field:      "persona.user_md",
			message:    "persona.user_md is over its limit",
			characters: characters,
			limit:      agenthome.UserLimit,
		}
	}
	return &setupPlan{
		provider:   preset,
		baseURL:    baseURL,
		model:      model,
		apiKey:     apiKey,
		agentName:  agentName,
		workspace:  workspace,
		permission: permission,
		template:   template,
		agentMD:    body.Persona.AgentMD,
		userMD:     body.Persona.UserMD,
	}, nil
}

func writeSetupInputError(w http.ResponseWriter, input *setupInputError) {
	if input.code == "over_limit" {
		writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "over_limit", input.message, []map[string]any{
			{"field": input.field, "characters": input.characters, "limit": input.limit},
		})
		return
	}
	writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "setup is not valid", []map[string]any{
		{"key": input.field, "field": input.field, "message": input.message},
	})
}

// writeSetupSaveError maps the section writer's validation error to 422 and
// everything else to a 500 that names the step.
func (s *Server) writeSetupSaveError(w http.ResponseWriter, step string, err error) {
	var validation *agentValidationError
	if errors.As(err, &validation) {
		writeSetupIssues(w, validation.issues)
		return
	}
	s.writeSetupStepError(w, step, err)
}

func writeSetupIssues(w http.ResponseWriter, issues []ConfigIssue) {
	details := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		detail := map[string]any{
			"key":     issue.Key,
			"field":   issue.Key,
			"message": issue.Message,
		}
		if issue.File != "" {
			detail["file"] = issue.File
		}
		details = append(details, detail)
	}
	writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "setup is not valid", details)
}

func (s *Server) writeSetupStepError(w http.ResponseWriter, step string, err error) {
	s.logger.Error("web: setup save", "step", step, "error", err)
	writeAPIErrorDetails(w, http.StatusInternalServerError, "internal_error", "setup save failed", []map[string]any{
		{"step": step},
	})
}

// writeSetupUserFile writes the shared USER.md only when it is missing. An
// existing file is never replaced.
func writeSetupUserFile(home, text string) error {
	if text == "" {
		return nil
	}
	path := agenthome.SharedUser(home)
	exists, err := pathExists(path)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return agenthome.WriteFile(path, text)
}

// setupConfigSeed is the first config.toml of a fresh home. It keeps the web
// view enabled at the address this daemon serves, so the restart listens on the
// same port.
func setupConfigSeed(addr string) []byte {
	var b strings.Builder
	b.WriteString("version = 1\n\n[web]\nenabled = true\n")
	if addr != "" {
		fmt.Fprintf(&b, "addr = %q\n", addr)
	}
	return []byte(b.String())
}

func setupConfigPath(file ConfigFile, home string) string {
	if file.Path != "" {
		return file.Path
	}
	return filepath.Join(home, "config.toml")
}
