package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// configBodyLimit is the largest accepted PUT /v1/config body.
const configBodyLimit = 4 << 20

// configWriteMu serializes config, secret, and file saves in this daemon. It is
// the same process-wide lock the agent config writer takes.
var configWriteMu = &webfiles.ConfigWriteMu

// ConfigStore is the file and settings port used by the config handlers. It
// stays separate from Backend so the existing session fakes do not grow config
// methods. SetBackend installs a Backend that also implements ConfigStore.
type ConfigStore interface {
	// ConfigFile returns the current config.toml bytes, its sha256, and
	// whether the file exists.
	ConfigFile(ctx context.Context) (ConfigFile, error)
	// ConfigCheck validates a candidate config.toml as the home layer and
	// returns structured issues. An invalid candidate is not an error.
	ConfigCheck(ctx context.Context, candidate []byte) ([]ConfigIssue, error)
	// ConfigWrite replaces config.toml atomically.
	ConfigWrite(ctx context.Context, data []byte) error
	// ConfigLoadedVersion is the version the runtime has loaded. It starts at
	// the startup version and a live apply advances it.
	ConfigLoadedVersion() string
	// ConfigSettings returns the effective settings read from the current
	// file. Secret values are already masked.
	ConfigSettings(ctx context.Context) (ConfigSettings, error)
}

// LiveConfigStore is an optional extension of ConfigStore. A store that
// implements it can apply a written config.toml to the running daemon, so an
// agent, team, or MCP save needs no restart (REQ-TFB-006).
type LiveConfigStore interface {
	ApplyLive(ctx context.Context, candidate []byte) error
}

// ConfigFile is the current config.toml and its content hash.
type ConfigFile struct {
	Path    string
	Raw     []byte
	Version string
	Exists  bool
}

// ConfigIssue is one validation error with a file and key path.
type ConfigIssue struct {
	File    string `json:"file"`
	Key     string `json:"key"`
	Message string `json:"message"`
}

// ConfigSettings is the structured read-only settings view. Values under
// mcp.*.env and mcp.*.headers are "***"; no secret is included.
type ConfigSettings struct {
	Providers map[string]ConfigProvider  `json:"providers"`
	Agents    map[string]ConfigAgent     `json:"agents"`
	Routes    []ConfigRoute              `json:"routes"`
	Channels  map[string]map[string]any  `json:"channels"`
	MCP       map[string]ConfigMCPServer `json:"mcp"`
	Modules   []ConfigModule             `json:"modules"`
}

// ConfigProvider is one provider without a key value.
type ConfigProvider struct {
	Kind      string            `json:"kind"`
	BaseURL   string            `json:"base_url,omitempty"`
	APIKeyEnv string            `json:"api_key_env,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Timeout   string            `json:"timeout,omitempty"`
}

// ConfigAgent is the settings view of one agent.
type ConfigAgent struct {
	Provider      string      `json:"provider,omitempty"`
	Model         string      `json:"model,omitempty"`
	Fallback      []string    `json:"fallback,omitempty"`
	PromptFile    string      `json:"prompt_file,omitempty"`
	Workspace     string      `json:"workspace,omitempty"`
	Tools         []string    `json:"tools,omitempty"`
	Subagents     []string    `json:"subagents,omitempty"`
	MCP           []string    `json:"mcp,omitempty"`
	Skills        []string    `json:"skills,omitempty"`
	Permission    string      `json:"permission,omitempty"`
	MaxSteps      int         `json:"max_steps,omitempty"`
	MaxTokens     int         `json:"max_tokens,omitempty"`
	ContextWindow int         `json:"context_window,omitempty"`
	Rules         ConfigRules `json:"rules"`
	// ReasoningEffort and the runtime keys of a Claude Agent (API-CCR-001).
	// Runtime is "claude-code" or "" for lana's own loop.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	Runtime         string `json:"runtime,omitempty"`
	Auth            string `json:"auth,omitempty"`
	APIKeyEnv       string `json:"api_key_env,omitempty"`
	Binary          string `json:"binary,omitempty"`
	AllowScheduled  bool   `json:"allow_scheduled,omitempty"`
}

// ConfigRules is the allow/deny list of an agent.
type ConfigRules struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// ConfigRoute is one route.
type ConfigRoute struct {
	Channel string         `json:"channel,omitempty"`
	Agent   string         `json:"agent,omitempty"`
	Match   map[string]any `json:"match,omitempty"`
}

// ConfigMCPServer is one MCP server. Env and headers are masked.
type ConfigMCPServer struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ConfigModule is one [[modules]] row.
type ConfigModule struct {
	ID       string         `json:"id,omitempty"`
	Module   string         `json:"module,omitempty"`
	Disabled bool           `json:"disabled"`
	Config   map[string]any `json:"config,omitempty"`
}

// SetConfigStore installs the config port directly. SetBackend also calls it
// when the backend implements ConfigStore.
func (s *Server) SetConfigStore(store ConfigStore) {
	s.configStore = store
}

func (s *Server) registerConfigRoutes() {
	s.Handle("GET /v1/config", http.HandlerFunc(s.handleGetConfig))
	s.Handle("PUT /v1/config", http.HandlerFunc(s.handlePutConfig))
}

func (s *Server) configStoreOrError(w http.ResponseWriter) ConfigStore {
	if s.configStore == nil {
		s.logger.Error("web: config store is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return nil
	}
	return s.configStore
}

// applyLiveConfig applies a written candidate to the running daemon. It reports
// false when the store cannot apply live or the apply failed; the write stays
// and the response then asks for a restart (API-TFB-006). Only an apply failure
// is logged.
func (s *Server) applyLiveConfig(ctx context.Context, store ConfigStore, candidate []byte) bool {
	live, ok := store.(LiveConfigStore)
	if !ok {
		return false
	}
	if err := live.ApplyLive(ctx, candidate); err != nil {
		s.logger.Error("web: apply live config", "error", err)
		return false
	}
	return true
}

type configValidation struct {
	OK     bool          `json:"ok"`
	Errors []ConfigIssue `json:"errors"`
}

type getConfigResponse struct {
	Version         string           `json:"version"`
	LoadedVersion   string           `json:"loaded_version"`
	RestartRequired bool             `json:"restart_required"`
	Validation      configValidation `json:"validation"`
	Raw             string           `json:"raw"`
	Settings        ConfigSettings   `json:"settings"`
}

type putConfigResponse struct {
	Version         string `json:"version"`
	RestartRequired bool   `json:"restart_required"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	loaded := store.ConfigLoadedVersion()
	response := getConfigResponse{
		Version:         file.Version,
		LoadedVersion:   loaded,
		RestartRequired: file.Version != loaded,
		Validation:      configValidation{Errors: []ConfigIssue{}},
	}
	if !file.Exists {
		response.Validation.Errors = append(response.Validation.Errors, ConfigIssue{
			File:    file.Path,
			Message: "config.toml does not exist",
		})
		writeJSON(w, http.StatusOK, response)
		return
	}

	issues, err := store.ConfigCheck(r.Context(), file.Raw)
	if err != nil {
		s.logger.Error("web: check config", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if issues == nil {
		issues = []ConfigIssue{}
	}
	response.Validation.OK = len(issues) == 0
	response.Validation.Errors = issues
	if masked, err := maskConfigRaw(file.Raw); err == nil {
		response.Raw = string(masked)
	} else {
		// Never return text that could not be masked; the validation errors
		// still tell the owner what is wrong.
		s.logger.Debug("web: mask config", "error", err)
	}
	if settings, err := store.ConfigSettings(r.Context()); err == nil {
		response.Settings = settings
	} else {
		s.logger.Debug("web: config settings unavailable", "error", err)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	var body struct {
		Raw             string `json:"raw"`
		ExpectedVersion string `json:"expected_version"`
	}
	if !decodeJSONObject(w, r, configBodyLimit, &body) {
		return
	}
	if strings.TrimSpace(body.Raw) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "raw is required")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before save", "error", err)
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

	candidate := []byte(body.Raw)
	unmasked, issues, err := unmaskConfig(file.Raw, candidate, file.Path)
	if err != nil {
		// Let the same validator report the parse error, so the response has
		// the usual file/key details.
		checkIssues, checkErr := store.ConfigCheck(r.Context(), candidate)
		if checkErr != nil {
			s.logger.Error("web: check config candidate", "error", checkErr)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		if len(checkIssues) == 0 {
			checkIssues = []ConfigIssue{{File: file.Path, Message: err.Error()}}
		}
		writeValidationFailed(w, checkIssues)
		return
	}
	if len(issues) > 0 {
		writeValidationFailed(w, issues)
		return
	}

	checkIssues, err := store.ConfigCheck(r.Context(), unmasked)
	if err != nil {
		s.logger.Error("web: check config", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if len(checkIssues) > 0 {
		writeValidationFailed(w, checkIssues)
		return
	}
	if err := store.ConfigWrite(r.Context(), unmasked); err != nil {
		s.logger.Error("web: write config", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	sum := sha256.Sum256(unmasked)
	version := hex.EncodeToString(sum[:])
	changed := changedConfigKeys(file.Raw, unmasked)
	s.auditConfigSave(r.Context(), changed)
	writeJSON(w, http.StatusOK, putConfigResponse{
		Version:         version,
		RestartRequired: version != store.ConfigLoadedVersion(),
	})
}

// auditConfigSave writes the audit line for a successful config save. The
// changed key paths never include values.
func (s *Server) auditConfigSave(_ context.Context, changed []string) {
	s.auditSave("config", "config.toml", "", changed)
}

// auditSave writes the stderr info line for a successful save and appends the
// matching JSON line to $LANA_HOME/logs/audit.jsonl. Callers pass only the
// kind, the file or name, and the changed key paths, so no secret value and no
// file content can reach either output.
func (s *Server) auditSave(kind, file, name string, keys []string) {
	attrs := []any{"kind", kind}
	if file != "" {
		attrs = append(attrs, "file", file)
	}
	if name != "" {
		attrs = append(attrs, "name", name)
	}
	if len(keys) > 0 {
		attrs = append(attrs, "keys", keys)
	}
	s.logger.Info("web: save", attrs...)

	target := file
	if target == "" {
		target = name
	}
	entry := webfiles.AuditEntry{
		Time:        s.nowTime().UnixMilli(),
		Kind:        s.auditKind(kind, name),
		Target:      target,
		ChangedKeys: keys,
	}
	if err := webfiles.AppendAudit(webfiles.AuditPath(s.auditHome()), entry); err != nil {
		// The save itself succeeded. A missing audit line is logged and does
		// not turn the save into an error (design.md, failure handling).
		s.logger.Error("web: append audit line", "error", err)
	}
}

// auditKind maps the short rev 1 stderr kinds to the canonical kinds listed by
// API-WEB-030. The two secret handlers share one stderr kind, so the audit file
// tells set and remove apart by the secrets.env state after the write.
func (s *Server) auditKind(kind, name string) string {
	switch kind {
	case "config":
		return "config.save"
	case "agent_file":
		return "agent.file.save"
	case "skill":
		return "skill.save"
	case "secret":
		if s.secretSet(name) {
			return "secret.set"
		}
		return "secret.remove"
	default:
		return kind
	}
}

// secretSet reports whether name is present in secrets.env after a secret save.
// A missing port or an unreadable file keeps the set kind, so the audit line
// never claims a remove it did not observe.
func (s *Server) secretSet(name string) bool {
	if s.secretStore == nil || name == "" {
		return true
	}
	values, err := webfiles.ReadSecrets(secretsPath(s.secretStore.SettingsHome()))
	if err != nil {
		return true
	}
	_, ok := values[name]
	return ok
}

func writeValidationFailed(w http.ResponseWriter, issues []ConfigIssue) {
	details := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		details = append(details, map[string]any{
			"file":    issue.File,
			"key":     issue.Key,
			"message": issue.Message,
		})
	}
	writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "config.toml is not valid", details)
}

// maskConfigRaw replaces every mcp.*.env and mcp.*.headers value with "***"
// by editing only the value spans in the source text. Comments and layout stay
// as they are.
func maskConfigRaw(data []byte) ([]byte, error) {
	spans, err := secretValueSpans(data)
	if err != nil {
		return nil, err
	}
	replacements := make([]replacement, 0, len(spans))
	for _, span := range spans {
		replacements = append(replacements, replacement{start: span.Start, end: span.End, text: `"***"`})
	}
	return applyReplacements(data, replacements), nil
}

// unmaskConfig restores stored values for "***" under mcp.*.env and
// mcp.*.headers. A masked value with no stored value at the same key is an
// error because the owner could not have seen that key.
func unmaskConfig(stored, candidate []byte, file string) ([]byte, []ConfigIssue, error) {
	spans, err := secretValueSpans(candidate)
	if err != nil {
		return nil, nil, err
	}
	hasMask := false
	for _, span := range spans {
		if span.Value == "***" {
			hasMask = true
			break
		}
	}
	if !hasMask {
		return candidate, nil, nil
	}
	storedSpans, err := secretValueSpans(stored)
	if err != nil {
		return nil, nil, fmt.Errorf("stored config.toml cannot be parsed: %w", err)
	}
	storedValues := make(map[string]string, len(storedSpans))
	for _, span := range storedSpans {
		storedValues[span.key()] = span.Value
	}

	var issues []ConfigIssue
	replacements := make([]replacement, 0, len(spans))
	for _, span := range spans {
		if span.Value != "***" {
			continue
		}
		value, ok := storedValues[span.key()]
		if !ok {
			issues = append(issues, ConfigIssue{
				File:    file,
				Key:     strings.Join(span.Path, "."),
				Message: "masked value has no stored value",
			})
			continue
		}
		encoded, err := marshalTOMLString(value)
		if err != nil {
			return nil, nil, err
		}
		replacements = append(replacements, replacement{start: span.Start, end: span.End, text: encoded})
	}
	if len(issues) > 0 {
		return candidate, issues, nil
	}
	return applyReplacements(candidate, replacements), nil, nil
}

func marshalTOMLString(value string) (string, error) {
	data, err := toml.Marshal(value)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(data), "\n"), nil
}

type replacement struct {
	start int
	end   int
	text  string
}

func applyReplacements(data []byte, replacements []replacement) []byte {
	if len(replacements) == 0 {
		return data
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start < replacements[j].start })
	var out bytes.Buffer
	last := 0
	for _, rep := range replacements {
		if rep.start < last || rep.end > len(data) || rep.start > rep.end {
			continue
		}
		out.Write(data[last:rep.start])
		out.WriteString(rep.text)
		last = rep.end
	}
	out.Write(data[last:])
	return out.Bytes()
}

type secretSpan struct {
	Path  []string
	Start int
	End   int
	Value string
}

func (s secretSpan) key() string {
	return strings.Join(s.Path, "\x00")
}

// secretValueSpans returns the source spans of values under mcp.*.env and
// mcp.*.headers. It handles table sections, dotted keys, and inline tables.
func secretValueSpans(data []byte) ([]secretSpan, error) {
	parser := &unstable.Parser{}
	parser.Reset(data)
	var current []string
	arrayCount := map[string]int{}
	var spans []secretSpan
	for parser.NextExpression() {
		node := parser.Expression()
		switch node.Kind {
		case unstable.Table:
			current = nodeKeyPath(node)
		case unstable.ArrayTable:
			key := nodeKeyPath(node)
			base := strings.Join(key, ".")
			index := arrayCount[base]
			arrayCount[base] = index + 1
			current = append(append([]string{}, key...), strconv.Itoa(index))
		case unstable.KeyValue:
			full := append(append([]string{}, current...), nodeKeyPath(node)...)
			collectSecretSpans(node.Value(), full, &spans)
		}
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	return spans, nil
}

func collectSecretSpans(node *unstable.Node, path []string, spans *[]secretSpan) {
	if node == nil {
		return
	}
	switch node.Kind {
	case unstable.InlineTable:
		iter := node.Children()
		for iter.Next() {
			child := iter.Node()
			if child.Kind != unstable.KeyValue {
				continue
			}
			full := append(append([]string{}, path...), nodeKeyPath(child)...)
			collectSecretSpans(child.Value(), full, spans)
		}
	case unstable.Array:
		// Arrays under env/headers are invalid config; leave them untouched.
	default:
		if isMCPSecretPath(path) {
			*spans = append(*spans, secretSpan{
				Path:  append([]string{}, path...),
				Start: int(node.Raw.Offset),
				End:   int(node.Raw.Offset + node.Raw.Length),
				Value: string(node.Data),
			})
		}
	}
}

func nodeKeyPath(node *unstable.Node) []string {
	var parts []string
	iter := node.Key()
	for iter.Next() {
		parts = append(parts, string(iter.Node().Data))
	}
	return parts
}

func isMCPSecretPath(path []string) bool {
	return len(path) >= 3 && path[0] == "mcp" && (path[2] == "env" || path[2] == "headers")
}

// changedConfigKeys returns the key paths whose values differ between the old
// and new text. It is used only for the audit line, which never prints values.
func changedConfigKeys(old, new []byte) []string {
	before, err := flatConfig(old)
	if err != nil {
		return nil
	}
	after, err := flatConfig(new)
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(before)+len(after))
	seen := map[string]struct{}{}
	for key := range before {
		seen[key] = struct{}{}
	}
	for key := range after {
		seen[key] = struct{}{}
	}
	for key := range seen {
		if before[key] != after[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func flatConfig(data []byte) (map[string]string, error) {
	var value map[string]any
	if err := toml.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	out := map[string]string{}
	flattenConfig("", value, out)
	return out, nil
}

func flattenConfig(prefix string, value any, out map[string]string) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			flattenConfig(joinKey(prefix, key), child, out)
		}
	case []any:
		for i, child := range v {
			flattenConfig(prefix+"."+strconv.Itoa(i), child, out)
		}
	default:
		out[prefix] = fmt.Sprint(v)
	}
}

func joinKey(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
