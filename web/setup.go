package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/internal/models"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// setupBodyLimit is the largest accepted POST /v1/setup body.
const setupBodyLimit = 64 << 10

// setupComboName is the combo the setup writes when the owner picks a
// fallback model: [main, fallback]. The agent then uses model = "default".
const setupComboName = "default"

// setupMaxProviders caps the providers of one setup save.
const setupMaxProviders = 16

// SetupStatus is the read model of API-WEB-018. Models and ClaudeCodeModels
// are the wizard's model picker catalog: the built-in models of each preset
// kind and the Claude Code aliases. They need no configured provider, so the
// picker works before anything is saved.
type SetupStatus struct {
	Needed           bool                    `json:"needed"`
	Reasons          []string                `json:"reasons"`
	ProviderCount    int                     `json:"provider_count"`
	AgentCount       int                     `json:"agent_count"`
	Models           map[string][]SetupModel `json:"models"`
	ClaudeCodeModels []SetupModel            `json:"claude_code_models"`
}

// SetupModel is one model of the setup picker catalog.
type SetupModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// setupConfigDocument is the part of config.toml the setup handlers count.
type setupConfigDocument struct {
	Providers map[string]any `toml:"providers"`
	Agents    map[string]any `toml:"agents"`
	Combos    map[string]any `toml:"combos"`
	Router    map[string]any `toml:"router"`
}

// setupProviderPreset is one provider card of the legacy setup body.
type setupProviderPreset struct {
	name   string
	kind   string
	keyEnv string
}

// setupProviderPresets maps the legacy setup provider names to config
// provider tables. Each card uses its preset kind (API-RTR-001), so the table
// needs no base_url: the router knows the URL of every preset kind.
var setupProviderPresets = map[string]setupProviderPreset{
	"openai":     {name: "openai", kind: "openai", keyEnv: "OPENAI_API_KEY"},
	"anthropic":  {name: "anthropic", kind: "anthropic", keyEnv: "ANTHROPIC_API_KEY"},
	"deepseek":   {name: "deepseek", kind: "deepseek", keyEnv: "DEEPSEEK_API_KEY"},
	"openrouter": {name: "openrouter", kind: "openrouter", keyEnv: "OPENROUTER_API_KEY"},
}

// setupKinds are the provider kinds a setup save accepts. Claude Code needs
// no provider table: the built-in claude-code account is always there.
var setupKinds = []string{"openai", "anthropic", "deepseek", "openrouter", "openai_compat"}

// setupBaseURL returns the base_url a setup card writes: "" when the typed
// URL is the kind's preset URL (or empty), the typed URL otherwise. The old
// OpenAI-compatible Anthropic URL (…/v1) counts as the preset, since the
// native adapter adds /v1 itself.
func setupBaseURL(kind, typed string) string {
	typed = strings.TrimRight(strings.TrimSpace(typed), "/")
	preset := strings.TrimRight(models.DefaultBaseURL(kind), "/")
	if typed == "" || strings.EqualFold(typed, preset) {
		return ""
	}
	if kind == "anthropic" && strings.EqualFold(typed, preset+"/v1") {
		return ""
	}
	return typed
}

// setupKeyEnv is the default key variable of a provider: <NAME>_API_KEY, the
// same name the Router pages use (OPENAI_API_KEY for openai).
func setupKeyEnv(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_API_KEY"
}

// setupRequest is the POST /v1/setup body of API-WEB-021. Revision 3 (the
// Router onboarding) sends providers, model, fallback, and background_model;
// the revision 2 body with one provider (provider.name, provider.model) is
// still accepted and saved in the same router form.
type setupRequest struct {
	Provider        *setupProviderRequest `json:"provider"`
	Providers       []setupProviderEntry  `json:"providers"`
	Model           string                `json:"model"`
	Fallback        string                `json:"fallback"`
	BackgroundModel string                `json:"background_model"`
	Agent           setupAgentRequest     `json:"agent"`
	Persona         setupPersonaRequest   `json:"persona"`
	ExpectedVersion *string               `json:"expected_version"`
}

// setupProviderRequest is the revision 2 provider of the setup body.
type setupProviderRequest struct {
	Name    string `json:"name"`
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

// setupProviderEntry is one connected provider of the revision 3 body.
type setupProviderEntry struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	APIKey    string `json:"api_key"`
	BaseURL   string `json:"base_url"`
	APIKeyEnv string `json:"api_key_env"`
}

type setupAgentRequest struct {
	Name        string `json:"name"`
	Template    string `json:"template"`
	Workspace   string `json:"workspace"`
	Permission  string `json:"permission"`
	DisplayName string `json:"display_name"`
}

type setupPersonaRequest struct {
	AgentMD string `json:"agent_md"`
	UserMD  string `json:"user_md"`
}

type setupSaveResponse struct {
	Restart string `json:"restart"`
	PID     int    `json:"pid"`
}

// setupProviderPlan is one validated provider of a setup save.
type setupProviderPlan struct {
	name      string
	kind      string
	baseURL   string
	keyEnv    string
	apiKey    string
	testModel string
}

// setupPlan is a validated setup request. It carries only values that are safe
// to write; the inline keys are never logged or returned.
type setupPlan struct {
	providers   []setupProviderPlan
	model       string
	fallback    string
	background  string
	agentName   string
	displayName string
	workspace   string
	permission  string
	template    agenthome.Template
	agentMD     string
	userMD      string
}

// agentModel is the model key of the setup agent: the default combo when a
// fallback is set, else the main model address.
func (p *setupPlan) agentModel() string {
	if p.fallback != "" {
		return setupComboName
	}
	return p.model
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
	status.Models = setupModelCatalog()
	status.ClaudeCodeModels = setupClaudeCodeModels()
	writeJSON(w, http.StatusOK, status)
}

// setupModelCatalog returns the built-in picker models of each preset kind.
// openrouter lists no built-in models, so it gets its test model; the
// OpenAI-compatible presets are named by the page.
func setupModelCatalog() map[string][]SetupModel {
	out := map[string][]SetupModel{}
	for _, kind := range []string{"openai", "anthropic", "deepseek", "openrouter"} {
		list := []SetupModel{}
		for _, model := range models.ForKind(kind) {
			if model.Legacy {
				continue
			}
			list = append(list, SetupModel{ID: model.ID, Name: model.Name})
		}
		if len(list) == 0 {
			if id := models.TestModel(kind); id != "" {
				name := id
				if known, ok := models.Lookup(id); ok && known.Name != "" {
					name = known.Name
				}
				list = append(list, SetupModel{ID: id, Name: name})
			}
		}
		out[kind] = list
	}
	return out
}

// setupClaudeCodeModels returns the Claude Code aliases (API-CCT-004).
func setupClaudeCodeModels() []SetupModel {
	out := make([]SetupModel, 0, len(llmcall.ClaudeCodeAliases))
	for _, alias := range llmcall.ClaudeCodeAliases {
		out = append(out, SetupModel{ID: alias, Name: strings.ToUpper(alias[:1]) + alias[1:]})
	}
	return out
}

// setupState reads config.toml at call time. Setup is needed when the file is
// missing, has no [agents.*] table, or has no model source: no
// [providers.*] table and no agent or combo on the built-in Claude Code.
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
	claudeCode := false
	if len(bytes.TrimSpace(file.Raw)) > 0 {
		var document setupConfigDocument
		if err := toml.Unmarshal(file.Raw, &document); err != nil {
			return SetupStatus{}, err
		}
		status.ProviderCount = len(document.Providers)
		status.AgentCount = len(document.Agents)
		claudeCode = setupUsesClaudeCode(document)
	}
	if status.ProviderCount == 0 && !claudeCode {
		status.Reasons = append(status.Reasons, "no_provider")
	}
	if status.AgentCount == 0 {
		status.Reasons = append(status.Reasons, "no_agent")
	}
	status.Needed = len(status.Reasons) > 0
	return status, nil
}

// setupUsesClaudeCode reports whether an agent or combo uses the built-in
// Claude Code account, which needs no provider table (API-CCT-001).
func setupUsesClaudeCode(document setupConfigDocument) bool {
	prefix := core.ClaudeCodeProvider + "/"
	for _, raw := range document.Agents {
		agent, _ := raw.(map[string]any)
		if runtime, _ := agent["runtime"].(string); runtime == core.ClaudeCodeProvider {
			return true
		}
		if model, _ := agent["model"].(string); strings.HasPrefix(model, prefix) {
			return true
		}
	}
	for _, raw := range document.Combos {
		combo, _ := raw.(map[string]any)
		list, _ := combo["models"].([]any)
		for _, item := range list {
			if model, _ := item.(string); strings.HasPrefix(model, prefix) {
				return true
			}
		}
	}
	return false
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

	raw := file.Raw
	if !file.Exists {
		raw = setupConfigSeed(s.Addr())
	}
	// The config is built and checked before any provider call, so a model
	// or combo that config.Check rejects costs no request.
	candidate, changed, err := renderSetupConfig(r.Context(), agentChecker(store), raw, plan)
	if err != nil {
		s.writeSetupSaveError(w, "config", err)
		return
	}

	// The ports are read only after the cheap refusals, so a backend without
	// them still reports setup_not_needed and validation failures correctly.
	var tester ProviderTester
	if len(plan.providers) > 0 {
		if tester = s.providerTesterOrError(w); tester == nil {
			return
		}
	}
	daemon := s.daemonStoreOrError(w)
	if daemon == nil {
		return
	}

	for _, provider := range plan.providers {
		if provider.testModel == "" {
			// An OpenAI-compatible URL with no known model and no picked
			// model has nothing to test; the page tested it already.
			continue
		}
		result, err := tester.TestProvider(r.Context(), ProviderTest{
			Kind:      provider.kind,
			BaseURL:   provider.baseURL,
			Model:     provider.testModel,
			APIKeyEnv: provider.keyEnv,
			APIKey:    core.NewSecret(provider.apiKey),
		})
		if err != nil {
			s.logger.Error("web: setup provider test", "provider", provider.name, "error", redactProviderKey(err.Error(), provider.apiKey))
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		if !result.OK {
			message := redactProviderKey(result.Message, provider.apiKey)
			if message == "" {
				message = "provider connection test failed"
			}
			if len(plan.providers) > 1 {
				message = provider.name + ": " + message
			}
			writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "connection_failed", message, []map[string]any{
				{"provider": provider.name},
			})
			return
		}
	}

	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		s.writeSetupStepError(w, "config", err)
		return
	}
	wroteSecret := false
	for _, provider := range plan.providers {
		if provider.apiKey == "" {
			continue
		}
		if err := webfiles.SetSecret(secretsPath(home), provider.keyEnv, provider.apiKey); err != nil {
			s.writeSetupStepError(w, "secrets", err)
			return
		}
		if err := webfiles.SetSecretMeta(secretsMetaPath(home), provider.keyEnv, s.nowTime().UnixMilli()); err != nil {
			// The key itself is written; a meta failure only loses the update time.
			s.logger.Warn("web: write setup secrets meta", "error", err)
		}
		wroteSecret = true
	}
	if wroteSecret {
		s.setSecretsChanged()
	}
	if err := agenthome.WriteFile(paths.Agent, plan.agentMD); err != nil {
		s.writeSetupStepError(w, "agent_home", err)
		return
	}
	if err := writeSetupUserFile(home, plan.userMD); err != nil {
		s.writeSetupStepError(w, "user_md", err)
		return
	}

	s.auditSave("setup.save", setupConfigPath(file, home), plan.agentName, changed)

	info := daemon.DaemonInfo()
	// Setup saves restart immediately; no turn should be running in this flow
	// (API-TFB-002).
	if _, err := daemon.Restart(r.Context(), false); err != nil {
		s.logger.Error("web: schedule setup restart", "error", err)
		writeAPIErrorDetails(w, http.StatusInternalServerError, "restart_failed", "restart could not be prepared", []map[string]any{
			{"step": "restart"},
		})
		return
	}
	writeJSON(w, http.StatusAccepted, setupSaveResponse{Restart: "scheduled", PID: info.PID})
}

// renderSetupConfig writes the setup tables into raw in router form: one
// [providers.<name>] table per provider (preset kinds without base_url), the
// default combo when there is a fallback, the agent with model and no
// provider key, and [router] background_model. Every step is checked with
// config.Check, so the final candidate is a valid config. It returns the
// candidate and the sorted changed key paths.
func renderSetupConfig(ctx context.Context, check func(context.Context, []byte) error, raw []byte, plan *setupPlan) ([]byte, []string, error) {
	candidate := raw
	var changed []string
	rewrite := func(header, table string) error {
		next, keys, err := webfiles.RewriteTable(ctx, check, candidate, header, table)
		if err != nil {
			return err
		}
		candidate = next
		changed = append(changed, keys...)
		return nil
	}
	for _, provider := range plan.providers {
		table := renderProviderTable(provider.name, providerRecord{
			Kind:      provider.kind,
			BaseURL:   provider.baseURL,
			APIKeyEnv: provider.keyEnv,
		})
		if err := rewrite("[providers."+provider.name+"]", table); err != nil {
			return nil, nil, err
		}
	}
	if plan.fallback != "" {
		table := renderComboTable(setupComboName, "Main model, then the fallback", []string{plan.model, plan.fallback})
		if err := rewrite("[combos."+setupComboName+"]", table); err != nil {
			return nil, nil, err
		}
	}
	agentTable := renderAgentTable(plan.agentName, agentRecord{
		Model:       plan.agentModel(),
		Workspace:   plan.workspace,
		Tools:       append([]string(nil), plan.template.Tools...),
		Permission:  plan.permission,
		DisplayName: plan.displayName,
	})
	if err := rewrite("[agents."+plan.agentName+"]", agentTable); err != nil {
		return nil, nil, err
	}
	if plan.background != "" {
		table, err := renderSetupRouterTable(candidate, plan.background)
		if err != nil {
			return nil, nil, err
		}
		if err := rewrite("[router]", table); err != nil {
			return nil, nil, err
		}
	}
	sort.Strings(changed)
	return candidate, dedupeSorted(changed), nil
}

// renderSetupRouterTable renders [router] with background_model set and the
// other keys of an existing [router] table kept.
func renderSetupRouterTable(raw []byte, background string) (string, error) {
	var document setupConfigDocument
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := toml.Unmarshal(raw, &document); err != nil {
			return "", err
		}
	}
	router := map[string]any{}
	for key, value := range document.Router {
		router[key] = value
	}
	router["background_model"] = background
	data, err := toml.Marshal(map[string]any{"router": router})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func dedupeSorted(values []string) []string {
	out := values[:0]
	for i, value := range values {
		if i > 0 && value == values[i-1] {
			continue
		}
		out = append(out, value)
	}
	return out
}

// validateSetup checks the body before any provider call or write.
func validateSetup(body *setupRequest) (*setupPlan, *setupInputError) {
	plan := &setupPlan{}
	revision3 := len(body.Providers) > 0 || body.Model != "" || body.Fallback != "" || body.BackgroundModel != ""
	switch {
	case body.Provider != nil && revision3:
		return nil, setupFieldError("provider", "send either provider or providers and model, not both")
	case body.Provider != nil:
		if err := validateLegacySetupProvider(body.Provider, plan); err != nil {
			return nil, err
		}
	default:
		if err := validateSetupModels(body, plan); err != nil {
			return nil, err
		}
	}
	if err := validateSetupAgent(body, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// validateLegacySetupProvider checks the revision 2 provider and turns it into
// one provider and a model address.
func validateLegacySetupProvider(body *setupProviderRequest, plan *setupPlan) *setupInputError {
	preset, ok := setupProviderPresets[body.Name]
	if !ok {
		return setupFieldError("provider.name", "name must be openai, anthropic, deepseek, or openrouter")
	}
	apiKey := strings.TrimSpace(body.APIKey)
	if apiKey == "" {
		return setupFieldError("provider.api_key", "api_key is required")
	}
	if err := webfiles.ValidateSecretValue(apiKey); err != nil {
		return setupFieldError("provider.api_key", secretValidationMessage(err))
	}
	model := strings.TrimSpace(body.Model)
	if model == "" || strings.ContainsAny(model, "\r\n") {
		return setupFieldError("provider.model", "model is required and must be one line")
	}
	baseURL := setupBaseURL(preset.kind, body.BaseURL)
	if baseURL != "" {
		if err := validateProviderBaseURL(baseURL); err != nil {
			return setupFieldError("provider.base_url", err.Error())
		}
	}
	plan.providers = []setupProviderPlan{{
		name:      preset.name,
		kind:      preset.kind,
		baseURL:   baseURL,
		keyEnv:    preset.keyEnv,
		apiKey:    apiKey,
		testModel: model,
	}}
	plan.model = preset.name + "/" + model
	return nil
}

// validateSetupModels checks the revision 3 providers and models.
func validateSetupModels(body *setupRequest, plan *setupPlan) *setupInputError {
	if len(body.Providers) > setupMaxProviders {
		return setupFieldError("providers", fmt.Sprintf("at most %d providers", setupMaxProviders))
	}
	seen := map[string]bool{}
	for i, entry := range body.Providers {
		provider, err := validateSetupProviderEntry(i, entry)
		if err != nil {
			return err
		}
		if seen[provider.name] {
			return setupFieldError(fmt.Sprintf("providers.%d.name", i), fmt.Sprintf("provider %q is listed twice", provider.name))
		}
		seen[provider.name] = true
		plan.providers = append(plan.providers, provider)
	}

	model, err := setupAddress("model", body.Model, true)
	if err != nil {
		return err
	}
	if model == setupComboName {
		return setupFieldError("model", "model must be a provider/model address; a fallback makes the default combo")
	}
	fallback, err := setupAddress("fallback", body.Fallback, false)
	if err != nil {
		return err
	}
	if fallback != "" && fallback == model {
		return setupFieldError("fallback", "fallback must differ from model")
	}
	background, err := setupAddress("background_model", body.BackgroundModel, false)
	if err != nil {
		return err
	}
	if background != "" && setupAddressProvider(background) == core.ClaudeCodeProvider {
		return setupFieldError("background_model", "background passes cannot use claude-code")
	}
	if len(plan.providers) == 0 && setupAddressProvider(model) != core.ClaudeCodeProvider {
		return setupFieldError("providers", "connect a provider or pick a Claude Code model")
	}
	plan.model = model
	plan.fallback = fallback
	plan.background = background

	// Each provider is tested with the model the agent uses on it, else with
	// its kind's test model.
	for i := range plan.providers {
		provider := &plan.providers[i]
		for _, address := range []string{model, fallback, background} {
			if address != "" && setupAddressProvider(address) == provider.name {
				provider.testModel = strings.TrimPrefix(address, provider.name+"/")
				break
			}
		}
	}
	return nil
}

func validateSetupProviderEntry(i int, entry setupProviderEntry) (setupProviderPlan, *setupInputError) {
	field := func(key string) string { return fmt.Sprintf("providers.%d.%s", i, key) }
	name := strings.TrimSpace(entry.Name)
	if !providerNameRE.MatchString(name) {
		return setupProviderPlan{}, setupFieldError(field("name"), "name must match [a-z0-9-]+")
	}
	if name == core.ClaudeCodeProvider {
		return setupProviderPlan{}, setupFieldError(field("name"), "claude-code is built in")
	}
	kind := strings.TrimSpace(entry.Kind)
	if !slices.Contains(setupKinds, kind) {
		return setupProviderPlan{}, setupFieldError(field("kind"), "kind must be one of "+strings.Join(setupKinds, ", "))
	}
	keyEnv := strings.TrimSpace(entry.APIKeyEnv)
	explicitEnv := keyEnv != ""
	if keyEnv == "" {
		keyEnv = setupKeyEnv(name)
	}
	if !providerEnvRE.MatchString(keyEnv) {
		return setupProviderPlan{}, setupFieldError(field("api_key_env"), "api_key_env must match [A-Za-z_][A-Za-z0-9_]*")
	}
	apiKey := strings.TrimSpace(entry.APIKey)
	if apiKey == "" && !explicitEnv {
		return setupProviderPlan{}, setupFieldError(field("api_key"), "api_key is required")
	}
	if apiKey != "" {
		if err := webfiles.ValidateSecretValue(apiKey); err != nil {
			return setupProviderPlan{}, setupFieldError(field("api_key"), secretValidationMessage(err))
		}
	}
	var baseURL, testModel string
	if kind == "openai_compat" {
		baseURL = strings.TrimRight(strings.TrimSpace(entry.BaseURL), "/")
		if baseURL == "" {
			return setupProviderPlan{}, setupFieldError(field("base_url"), "base_url is required for kind openai_compat")
		}
		testModel = models.TestModelForBaseURL(baseURL)
	} else {
		baseURL = setupBaseURL(kind, entry.BaseURL)
		testModel = models.TestModel(kind)
	}
	if baseURL != "" {
		if err := validateProviderBaseURL(baseURL); err != nil {
			return setupProviderPlan{}, setupFieldError(field("base_url"), err.Error())
		}
	}
	return setupProviderPlan{
		name:      name,
		kind:      kind,
		baseURL:   baseURL,
		keyEnv:    keyEnv,
		apiKey:    apiKey,
		testModel: testModel,
	}, nil
}

// setupAddress checks one model field: a provider/model address on one line.
// A Claude Code address must name a valid Claude Code model.
func setupAddress(field, value string, required bool) (string, *setupInputError) {
	value = strings.TrimSpace(value)
	if value == "" {
		if required {
			return "", setupFieldError(field, field+" is required")
		}
		return "", nil
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", setupFieldError(field, field+" must be one line")
	}
	if value == setupComboName {
		return value, nil
	}
	provider := setupAddressProvider(value)
	model := strings.TrimPrefix(value, provider+"/")
	if provider == "" || model == "" || model == value {
		return "", setupFieldError(field, field+" must be a provider/model address")
	}
	if provider == core.ClaudeCodeProvider && !llmcall.ValidClaudeCodeModel(model) {
		return "", setupFieldError(field, fmt.Sprintf("invalid claude-code model %q", model))
	}
	return value, nil
}

// setupAddressProvider returns the provider part of "provider/model".
func setupAddressProvider(address string) string {
	if slash := strings.Index(address, "/"); slash > 0 {
		return address[:slash]
	}
	return ""
}

// validateSetupAgent checks the agent and persona fields.
func validateSetupAgent(body *setupRequest, plan *setupPlan) *setupInputError {
	agentName := body.Agent.Name
	if !agenthome.ValidName(agentName) {
		return setupFieldError("agent.name", "name must match [a-z][a-z0-9_-]{0,31}")
	}
	displayName := strings.TrimSpace(body.Agent.DisplayName)
	if problem := validateAgentDisplayName(displayName); problem != "" {
		return setupFieldError("agent.display_name", problem)
	}
	template, ok := agenthome.LookupTemplate(body.Agent.Template)
	if !ok {
		return setupFieldError("agent.template", "template must be assistant, coder, or worker")
	}
	workspace := strings.TrimSpace(body.Agent.Workspace)
	if workspace == "" {
		workspace = "~/lana/workspace"
	}
	if strings.ContainsAny(workspace, "\r\n") {
		return setupFieldError("agent.workspace", "workspace must be one line")
	}
	permission := strings.TrimSpace(body.Agent.Permission)
	if permission == "" {
		permission = "ask"
	}
	if !validAgentPermission(permission) {
		return setupFieldError("agent.permission", "permission must be one of ask, trust, never")
	}
	if strings.TrimSpace(body.Persona.AgentMD) == "" {
		return setupFieldError("persona.agent_md", "agent_md is required")
	}
	characters := utf8.RuneCountInString(body.Persona.UserMD)
	if characters > agenthome.UserLimit {
		return &setupInputError{
			code:       "over_limit",
			field:      "persona.user_md",
			message:    "persona.user_md is over its limit",
			characters: characters,
			limit:      agenthome.UserLimit,
		}
	}
	plan.agentName = agentName
	plan.displayName = displayName
	plan.workspace = workspace
	plan.permission = permission
	plan.template = template
	plan.agentMD = body.Persona.AgentMD
	plan.userMD = body.Persona.UserMD
	return nil
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
