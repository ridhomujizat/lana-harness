package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// mcpBodyLimit is the largest accepted MCP form or test body.
const mcpBodyLimit = 64 << 10

// mcpNameRE is the MCP server name rule of API-WEB-027.
var mcpNameRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// MCPServerRow is one server of GET /v1/mcp (API-WEB-027). EnvKeys and
// HeaderKeys hold names only; values are never returned.
type MCPServerRow struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Command      string   `json:"command"`
	Args         []string `json:"args"`
	URL          string   `json:"url"`
	EnvKeys      []string `json:"env_keys"`
	HeaderKeys   []string `json:"header_keys"`
	ToolsCount   int      `json:"tools_count"`
	Agents       []string `json:"agents"`
	State        string   `json:"state"`
	Status       string   `json:"status"`
	Error        string   `json:"error"`
	RunningForMS int64    `json:"running_for_ms"`
}

// MCPDefinition is the definition a form or a test sends. Env and Header
// values are inline; the handler never writes or returns them.
type MCPDefinition struct {
	Type    string
	Command string
	Args    []string
	URL     string
	Env     map[string]string
	Headers map[string]string
}

// MCPTestResult is the outcome of POST /v1/mcp/test. Message is set when OK is
// false and never contains an inline value.
type MCPTestResult struct {
	OK        bool
	ToolCount int
	Message   string
}

// MCPState is the outcome of POST /v1/mcp/{name}/restart.
type MCPState struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Error string `json:"error"`
}

// MCPBackend is the daemon port for API-WEB-027. The adapter in cmd/lana
// implements it on top of the runtime. It stays separate from Backend so the
// existing session fakes do not grow MCP methods.
type MCPBackend interface {
	MCPServers() []MCPServerRow
	MCPTest(ctx context.Context, def MCPDefinition) MCPTestResult
	MCPRestart(ctx context.Context, name string) MCPState
}

// registerMCPRoutes installs the MCP routes. The backend and config store are
// read at call time, so SetBackend installs them together with the other ports.
func (s *Server) registerMCPRoutes() {
	s.Handle("GET /v1/mcp", http.HandlerFunc(s.handleListMCP))
	s.Handle("PUT /v1/mcp/{name}", http.HandlerFunc(s.handlePutMCP))
	s.Handle("DELETE /v1/mcp/{name}", http.HandlerFunc(s.handleDeleteMCP))
	s.Handle("POST /v1/mcp/test", http.HandlerFunc(s.handleTestMCP))
	s.Handle("POST /v1/mcp/{name}/restart", http.HandlerFunc(s.handleRestartMCP))
}

type mcpListResponse struct {
	Version string         `json:"version"`
	Servers []MCPServerRow `json:"servers"`
}

type mcpSaveResponse struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	RestartRequired bool   `json:"restart_required"`
}

type mcpTestOKResponse struct {
	OK        bool `json:"ok"`
	ToolCount int  `json:"tool_count"`
}

type mcpTestFailResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// mcpDocument is the part of config.toml the MCP handlers read.
type mcpDocument struct {
	MCP    map[string]map[string]any `toml:"mcp"`
	Agents map[string]map[string]any `toml:"agents"`
}

// mcpValidationError carries config.Check issues through the section writer.
type mcpValidationError struct {
	issues []ConfigIssue
}

func (e *mcpValidationError) Error() string { return "config.toml is not valid" }

func (s *Server) mcpBackendOrError(w http.ResponseWriter) MCPBackend {
	if s.backend != nil {
		if backend, ok := s.backend.(MCPBackend); ok {
			return backend
		}
	}
	s.logger.Error("web: MCP backend is not configured")
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	return nil
}

func (s *Server) handleListMCP(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config for MCP list", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	response := mcpListResponse{Version: file.Version, Servers: []MCPServerRow{}}
	if !file.Exists {
		writeJSON(w, http.StatusOK, response)
		return
	}
	document, err := parseMCPDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config for MCP list", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	runtimeRows := map[string]MCPServerRow{}
	if backend, ok := s.backend.(MCPBackend); ok {
		for _, row := range backend.MCPServers() {
			runtimeRows[row.Name] = row
		}
	}
	usage := mcpServerUsage(document.Agents)

	names := make([]string, 0, len(document.MCP))
	for name := range document.MCP {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		def := mcpDefinitionFromMap(document.MCP[name])
		row := MCPServerRow{
			Name:       name,
			Type:       mcpDefinitionType(def),
			Command:    def.Command,
			Args:       nonNilStrings(def.Args),
			URL:        def.URL,
			EnvKeys:    sortedMCPKeys(def.Env),
			HeaderKeys: sortedMCPKeys(def.Headers),
			Agents:     nonNilStrings(usage[name]),
		}
		if runtimeRow, ok := runtimeRows[name]; ok {
			row.State = runtimeRow.State
			row.ToolsCount = runtimeRow.ToolsCount
			row.Error = runtimeRow.Error
			row.RunningForMS = runtimeRow.RunningForMS
		}
		if row.State == "" {
			row.State = "stopped"
		}
		row.Status = mcpStatusForState(row.State)
		response.Servers = append(response.Servers, row)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handlePutMCP(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	if !mcpNameRE.MatchString(name) {
		writeMCPIssue(w, "name", "name must match [a-z0-9-]+")
		return
	}
	var body map[string]json.RawMessage
	if !decodeJSONObject(w, r, mcpBodyLimit, &body) {
		return
	}
	if issues := unknownMCPKeys(body, false); len(issues) > 0 {
		for i := range issues {
			issues[i].Key = "mcp." + name + "." + issues[i].Key
		}
		writeMCPIssues(w, issues)
		return
	}
	prefix := "mcp." + name + "."
	def, issues := mcpDefinitionFromBody(body, prefix)
	if len(issues) > 0 {
		writeMCPIssues(w, issues)
		return
	}
	expected, present, err := mcpStringField(body, "expected_version")
	if err != nil || !present || expected == "" {
		writeMCPIssue(w, prefix+"expected_version", "expected_version is required")
		return
	}
	_, envPresent := body["env"]
	_, headersPresent := body["headers"]

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before MCP save", "error", err)
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
	document, err := parseMCPDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before MCP save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	stored := mcpDefinitionFromMap(document.MCP[name])
	if !envPresent {
		def.Env = stored.Env
	} else {
		def.Env, issues = mergeMCPValues(stored.Env, def.Env, prefix+"env.")
		if len(issues) > 0 {
			writeMCPIssues(w, issues)
			return
		}
	}
	if !headersPresent {
		def.Headers = stored.Headers
	} else {
		def.Headers, issues = mergeMCPValues(stored.Headers, def.Headers, prefix+"headers.")
		if len(issues) > 0 {
			writeMCPIssues(w, issues)
			return
		}
	}
	normalizeMCPDefinition(&def)

	table := renderMCPTable(name, def)
	candidate, changed, err := webfiles.RewriteTable(r.Context(), mcpChecker(store), file.Raw, "[mcp."+name+"]", table)
	if err != nil {
		s.writeMCPSaveError(w, "save mcp server", err)
		return
	}
	if !bytes.Equal(candidate, file.Raw) {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for MCP save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
	}
	version := webfiles.ConfigVersion(candidate)
	s.auditSave("mcp.save", providerConfigPath(file, s.providerHome()), name, changed)
	writeJSON(w, http.StatusOK, mcpSaveResponse{
		Name:            name,
		Version:         version,
		RestartRequired: true,
	})
}

func (s *Server) handleDeleteMCP(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	if !mcpNameRE.MatchString(name) {
		writeMCPIssue(w, "name", "name must match [a-z0-9-]+")
		return
	}
	var body struct {
		ExpectedVersion string `json:"expected_version"`
	}
	if !decodeJSONObject(w, r, mcpBodyLimit, &body) {
		return
	}
	if body.ExpectedVersion == "" {
		writeMCPIssue(w, "mcp."+name+".expected_version", "expected_version is required")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before MCP delete", "error", err)
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
	document, err := parseMCPDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before MCP delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if _, exists := document.MCP[name]; !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "MCP server is not configured")
		return
	}
	if used := mcpServerUsage(document.Agents)[name]; len(used) > 0 {
		issues := make([]ConfigIssue, 0, len(used))
		for _, agent := range used {
			issues = append(issues, ConfigIssue{
				Key:     "agents." + agent + ".mcp",
				Message: "mcp server '" + name + "' is used by agents." + agent,
			})
		}
		writeMCPIssues(w, issues)
		return
	}
	candidate, changed, err := webfiles.RewriteTable(r.Context(), mcpChecker(store), file.Raw, "[mcp."+name+"]", "")
	if err != nil {
		s.writeMCPSaveError(w, "delete mcp server", err)
		return
	}
	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		s.logger.Error("web: write config for MCP delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	version := webfiles.ConfigVersion(candidate)
	s.auditSave("mcp.delete", providerConfigPath(file, s.providerHome()), name, changed)
	writeJSON(w, http.StatusOK, mcpSaveResponse{
		Name:            name,
		Version:         version,
		RestartRequired: true,
	})
}

func (s *Server) handleTestMCP(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if !decodeJSONObject(w, r, mcpBodyLimit, &body) {
		return
	}
	if issues := unknownMCPKeys(body, true); len(issues) > 0 {
		for i := range issues {
			issues[i].Key = "mcp." + issues[i].Key
		}
		writeMCPIssues(w, issues)
		return
	}
	def, issues := mcpDefinitionFromBody(body, "mcp.")
	if len(issues) > 0 {
		writeMCPIssues(w, issues)
		return
	}
	backend := s.mcpBackendOrError(w)
	if backend == nil {
		return
	}
	result := backend.MCPTest(r.Context(), def)
	if result.OK {
		writeJSON(w, http.StatusOK, mcpTestOKResponse{OK: true, ToolCount: result.ToolCount})
		return
	}
	writeJSON(w, http.StatusOK, mcpTestFailResponse{
		OK:      false,
		Message: redactMCPValues(result.Message, def.Env, def.Headers),
	})
}

func (s *Server) handleRestartMCP(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	if !mcpNameRE.MatchString(name) {
		writeMCPIssue(w, "name", "name must match [a-z0-9-]+")
		return
	}
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before MCP restart", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	configured := false
	if file.Exists {
		document, err := parseMCPDocument(file.Raw)
		if err != nil {
			s.logger.Error("web: parse config before MCP restart", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		_, configured = document.MCP[name]
	}
	if !configured {
		writeAPIError(w, http.StatusNotFound, "not_found", "MCP server is not configured")
		return
	}
	backend := s.mcpBackendOrError(w)
	if backend == nil {
		return
	}
	state := backend.MCPRestart(r.Context(), name)
	if state.Name == "" {
		state.Name = name
	}
	writeJSON(w, http.StatusOK, state)
}

func parseMCPDocument(raw []byte) (mcpDocument, error) {
	document := mcpDocument{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return document, nil
	}
	if err := toml.Unmarshal(raw, &document); err != nil {
		return mcpDocument{}, err
	}
	return document, nil
}

func mcpDefinitionFromMap(table map[string]any) MCPDefinition {
	def := MCPDefinition{
		Command: mapProviderString(table, "command"),
		Args:    mapMCPArgs(table["args"]),
		URL:     mapProviderString(table, "url"),
		Env:     mapMCPValues(table["env"]),
		Headers: mapMCPValues(table["headers"]),
	}
	def.Type = mcpDefinitionType(def)
	return def
}

func mapMCPArgs(value any) []string {
	switch list := value.(type) {
	case []string:
		return append([]string(nil), list...)
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func mapMCPValues(value any) map[string]string {
	table, ok := value.(map[string]any)
	if !ok || len(table) == 0 {
		return nil
	}
	out := make(map[string]string, len(table))
	for key, item := range table {
		if text, ok := item.(string); ok {
			out[key] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mcpDefinitionType(def MCPDefinition) string {
	if def.URL != "" {
		return "url"
	}
	return "command"
}

func mcpDefinitionFromBody(body map[string]json.RawMessage, prefix string) (MCPDefinition, []ConfigIssue) {
	def := MCPDefinition{}
	var issues []ConfigIssue
	add := func(key, message string) {
		issues = append(issues, ConfigIssue{Key: prefix + key, Message: message})
	}

	typ, present, err := mcpStringField(body, "type")
	if err != nil || !present || typ == "" {
		add("type", "type is required")
	} else if typ != "command" && typ != "url" {
		add("type", "type must be command or url")
	}
	def.Type = typ

	command, _, err := mcpStringField(body, "command")
	if err != nil {
		add("command", "command must be a string")
	}
	def.Command = command

	args, _, err := mcpStringsField(body, "args")
	if err != nil {
		add("args", "args must be a list of strings")
	}
	def.Args = args

	rawURL, _, err := mcpStringField(body, "url")
	if err != nil {
		add("url", "url must be a string")
	}
	def.URL = rawURL

	env, _, err := mcpStringMapField(body, "env")
	if err != nil {
		add("env", "env must be an object of strings")
	}
	def.Env = env

	headers, _, err := mcpStringMapField(body, "headers")
	if err != nil {
		add("headers", "headers must be an object of strings")
	}
	def.Headers = headers

	if len(issues) > 0 {
		return def, issues
	}
	switch def.Type {
	case "command":
		if strings.TrimSpace(def.Command) == "" {
			add("command", "command is required")
		}
		if def.URL != "" {
			add("url", "url is not allowed for a command server")
		}
		if len(def.Headers) > 0 {
			add("headers", "headers are only for a URL server")
		}
	case "url":
		if def.URL == "" {
			add("url", "url is required")
		} else if err := validateMCPURL(def.URL); err != nil {
			add("url", err.Error())
		}
		if def.Command != "" {
			add("command", "command is only for a URL server")
		}
		if len(def.Args) > 0 {
			add("args", "args are only for a URL server")
		}
		if len(def.Env) > 0 {
			add("env", "env is only for a command server")
		}
	}
	return def, issues
}

func normalizeMCPDefinition(def *MCPDefinition) {
	if def.Type == "command" {
		def.URL = ""
		def.Headers = nil
		return
	}
	def.Command = ""
	def.Args = nil
	def.Env = nil
}

func validateMCPURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("url must be an http:// or https:// URL")
	}
	return nil
}

// mergeMCPValues keeps a stored value for "***" and refuses a mask that has no
// stored value (API-WEB-027).
func mergeMCPValues(stored, candidate map[string]string, prefix string) (map[string]string, []ConfigIssue) {
	if len(candidate) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(candidate))
	var issues []ConfigIssue
	for key, value := range candidate {
		if value == "***" {
			kept, ok := stored[key]
			if !ok {
				issues = append(issues, ConfigIssue{
					Key:     prefix + key,
					Message: "masked value has no stored value",
				})
				continue
			}
			out[key] = kept
			continue
		}
		out[key] = value
	}
	return out, issues
}

func renderMCPTable(name string, def MCPDefinition) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[mcp.%s]\n", providerTOMLKey(name))
	writeProviderString(&b, "command", def.Command)
	if len(def.Args) > 0 {
		fmt.Fprintf(&b, "args = %s\n", mcpTOMLArray(def.Args))
	}
	writeProviderString(&b, "url", def.URL)
	writeMCPValueTable(&b, "env", def.Env)
	writeMCPValueTable(&b, "headers", def.Headers)
	return b.String()
}

func writeMCPValueTable(b *strings.Builder, name string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	keys := sortedMCPKeys(values)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, providerTOMLKey(key)+" = "+providerTOMLString(values[key]))
	}
	fmt.Fprintf(b, "%s = { %s }\n", name, strings.Join(parts, ", "))
}

func mcpTOMLArray(values []string) string {
	data, err := toml.Marshal(values)
	if err != nil {
		return "[]"
	}
	return strings.TrimSuffix(string(data), "\n")
}

func sortedMCPKeys(values map[string]string) []string {
	if len(values) == 0 {
		return []string{}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// mcpServerUsage maps each server name to the sorted agents that list it.
func mcpServerUsage(agents map[string]map[string]any) map[string][]string {
	usage := map[string][]string{}
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		list, ok := agents[name]["mcp"].([]any)
		if !ok {
			continue
		}
		for _, item := range list {
			server, ok := item.(string)
			if !ok {
				continue
			}
			usage[server] = append(usage[server], name)
		}
	}
	return usage
}

func mcpStatusForState(state string) string {
	switch state {
	case "running":
		return "connected"
	case "unavailable":
		return "unreachable"
	default:
		return "idle"
	}
}

// redactMCPValues is a last line of defense for a test error that echoes an
// inline env or header value.
func redactMCPValues(message string, values ...map[string]string) string {
	for _, table := range values {
		for _, value := range table {
			if value == "" || value == "***" {
				continue
			}
			message = strings.ReplaceAll(message, value, "***")
		}
	}
	return message
}

func unknownMCPKeys(body map[string]json.RawMessage, test bool) []ConfigIssue {
	allowed := map[string]bool{
		"type":    true,
		"command": true,
		"args":    true,
		"url":     true,
		"env":     true,
		"headers": true,
	}
	if !test {
		allowed["expected_version"] = true
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

func mcpStringField(body map[string]json.RawMessage, key string) (string, bool, error) {
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

func mcpStringsField(body map[string]json.RawMessage, key string) ([]string, bool, error) {
	raw, ok := body[key]
	if !ok {
		return nil, false, nil
	}
	var value []string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, true, err
	}
	return value, true, nil
}

func mcpStringMapField(body map[string]json.RawMessage, key string) (map[string]string, bool, error) {
	raw, ok := body[key]
	if !ok {
		return nil, false, nil
	}
	var value map[string]string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, true, err
	}
	return value, true, nil
}

func mcpChecker(store ConfigStore) func(context.Context, []byte) error {
	return func(ctx context.Context, candidate []byte) error {
		issues, err := store.ConfigCheck(ctx, candidate)
		if err != nil {
			return err
		}
		if len(issues) > 0 {
			return &mcpValidationError{issues: issues}
		}
		return nil
	}
}

func (s *Server) writeMCPSaveError(w http.ResponseWriter, operation string, err error) {
	var validation *mcpValidationError
	if errors.As(err, &validation) {
		writeMCPIssues(w, validation.issues)
		return
	}
	s.logger.Error("web: "+operation, "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeMCPIssue(w http.ResponseWriter, key, message string) {
	writeMCPIssues(w, []ConfigIssue{{Key: key, Message: message}})
}

func writeMCPIssues(w http.ResponseWriter, issues []ConfigIssue) {
	details := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		details = append(details, map[string]any{
			"file":    issue.File,
			"key":     issue.Key,
			"field":   issue.Key,
			"message": issue.Message,
		})
	}
	writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "MCP server is not valid", details)
}
