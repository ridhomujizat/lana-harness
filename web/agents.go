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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/avatar"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// agentBodyLimit is the largest accepted POST/PUT agent body.
const agentBodyLimit = 64 << 10

// maxAgentDescription mirrors the config validation limit (REQ-WEB-046).
const maxAgentDescription = 120

// maxAgentTitle mirrors the config validation limit (REQ-ROOM-009, API-ROOM-006).
const maxAgentTitle = 60

// agentNameRE is the create and duplicate name rule of API-WEB-025.
var agentNameRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// agentKeys are the keys a PUT /v1/agents/{name} body may carry. They match
// the [agents.<name>] table of core.AgentConfig.
var agentKeys = map[string]bool{
	"runtime":              true,
	"auth":                 true,
	"api_key_env":          true,
	"binary":               true,
	"allow_scheduled":      true,
	"provider":             true,
	"model":                true,
	"fallback":             true,
	"prompt_file":          true,
	"workspace":            true,
	"tools":                true,
	"disabled_tools":       true,
	"subagents":            true,
	"mcp":                  true,
	"skills":               true,
	"permission":           true,
	"max_steps":            true,
	"context_window":       true,
	"reasoning_effort":     true,
	"compaction_headroom":  true,
	"compaction_threshold": true,
	"compaction_retain":    true,
	"memory_capture":       true,
	"workspace_only":       true,
	"vision":               true,
	"description":          true,
	"title":                true,
	"color":                true,
	"rules":                true,
}

// putAgentKeys are the fields accepted by PUT /v1/agents/{name}.
var putAgentKeys = func() map[string]bool {
	keys := make(map[string]bool, len(agentKeys)+1)
	for key := range agentKeys {
		keys[key] = true
	}
	keys["expected_version"] = true
	return keys
}()

// createAgentKeys are the fields accepted by POST /v1/agents. A lana agent
// starts from a template; a Claude Agent (runtime "claude-code") has none and
// takes its runtime keys instead (API-CCR-004).
var createAgentKeys = map[string]bool{
	"name":             true,
	"template":         true,
	"workspace":        true,
	"permission":       true,
	"description":      true,
	"title":            true,
	"color":            true,
	"rules":            true,
	"runtime":          true,
	"model":            true,
	"reasoning_effort": true,
	"auth":             true,
	"api_key_env":      true,
	"binary":           true,
	"allow_scheduled":  true,
}

// claudeCodeRuntime is the runtime key of a Claude Agent (API-CCR-001).
const claudeCodeRuntime = "claude-code"

// Engine labels of the agent rows (API-CCR-004).
const (
	engineLabelLana   = "lana"
	engineLabelClaude = "Claude Agent"
)

// runtimeAgentEfforts are the reasoning_effort values of a claude-code agent,
// passed to --effort (API-CCR-001).
var runtimeAgentEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// duplicateAgentKeys are the fields accepted by the duplicate endpoint.
var duplicateAgentKeys = map[string]bool{"name": true}

func (s *Server) registerAgentRoutes() {
	s.Handle("GET /v1/agents", http.HandlerFunc(s.handleAgents))
	s.Handle("POST /v1/agents", http.HandlerFunc(s.handleCreateAgent))
	s.Handle("PUT /v1/agents/{name}", http.HandlerFunc(s.handlePutAgent))
	s.Handle("POST /v1/agents/{name}/duplicate", http.HandlerFunc(s.handleDuplicateAgent))
	// The model catalog shares the agent settings surface (REQ-MDL-020).
	s.registerModelRoutes()
}

type mcpStatusJSON struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Error string `json:"error"`
}

type agentJSON struct {
	Name string `json:"name"`
	// Kind is "agent" or "team" (API-TEAM-006).
	Kind string `json:"kind"`
	// Model is null for a team: each agent uses its own model.
	Model       *string         `json:"model"`
	Policy      string          `json:"policy"`
	Lead        string          `json:"lead,omitempty"`
	Members     []string        `json:"members,omitempty"`
	Skills      []string        `json:"skills"`
	MCP         []string        `json:"mcp"`
	MCPStatus   []mcpStatusJSON `json:"mcp_status"`
	Description string          `json:"description"`
	// Title is the agent role, "" when unset (API-ROOM-007).
	Title string `json:"title"`
	// Color is the resolved avatar color, never empty (API-ROOM-007): a
	// palette token or a #RRGGBB hex color (spec 43).
	Color string `json:"color"`
	// AvatarSVG is the avatar the row shows, never empty: the saved one or
	// the default seeded from the name (spec 43, API-AVT-005). AvatarSource
	// is "saved" or "default"; a team is always "default".
	AvatarSVG       string   `json:"avatar_svg"`
	AvatarSource    string   `json:"avatar_source"`
	Default         bool     `json:"default"`
	Provider        string   `json:"provider"`
	Permission      string   `json:"permission"`
	Workspace       string   `json:"workspace"`
	DisabledTools   []string `json:"disabled_tools"`
	ReasoningEffort string   `json:"reasoning_effort"`
	ToolsCount      int      `json:"tools_count"`
	SessionsCount   int      `json:"sessions_count"`
	LastActiveAt    *int64   `json:"last_active_at"`
	// MemoryCapture is the background capture switch; an absent key means on
	// (REQ-MEM-010).
	MemoryCapture bool `json:"memory_capture"`
	// WorkspaceOnly keeps the file tools inside the workspace; an absent key
	// means off.
	WorkspaceOnly bool `json:"workspace_only"`
	// Vision overrides the model's catalog vision flag (API-ATT-005). nil means
	// the key is absent and the catalog decides.
	Vision *bool `json:"vision,omitempty"`
	// Runtime is the deprecated engine key: "claude-code" only for a legacy
	// file, "" after a dashboard save (API-CCT-006). EngineLabel is kept for
	// old clients; the UI no longer uses it.
	Runtime     string `json:"runtime"`
	EngineLabel string `json:"engine_label"`
	// ClaudeCode is true when the agent's first target is a claude-code
	// address (API-CCT-006).
	ClaudeCode bool `json:"claude_code"`
	// Auth, APIKeyEnv, Binary, and AllowScheduled are the Claude-only keys of
	// an agent that uses Claude Code; they are omitted otherwise. Auth is
	// never empty for such an agent ("subscription" when the key is absent).
	Auth           string `json:"auth,omitempty"`
	APIKeyEnv      string `json:"api_key_env,omitempty"`
	Binary         string `json:"binary,omitempty"`
	AllowScheduled *bool  `json:"allow_scheduled,omitempty"`
}

type listAgentsResponse struct {
	Agents []agentJSON `json:"agents"`
}

type agentCreatedResponse struct {
	Name            string `json:"name"`
	RestartRequired bool   `json:"restart_required"`
}

type putAgentResponse struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	RestartRequired bool   `json:"restart_required"`
}

// agentRecord is the [agents.<name>] table as a form can edit it. Empty values
// are omitted when the table is rendered.
type agentRecord struct {
	// Runtime, Auth, APIKeyEnv, Binary, and AllowScheduled are the runtime
	// keys of a Claude Agent (API-CCR-001).
	Runtime             string
	Auth                string
	APIKeyEnv           string
	Binary              string
	AllowScheduled      bool
	Provider            string
	Model               string
	Fallback            []string
	PromptFile          string
	Workspace           string
	Tools               []string
	DisabledTools       []string
	Subagents           []string
	MCP                 []string
	Skills              []string
	Permission          string
	MaxSteps            int
	ContextWindow       int
	ReasoningEffort     string
	CompactionHeadroom  int
	CompactionThreshold float64
	CompactionRetain    float64
	// MemoryCapture is nil when the key is absent; nil means on (REQ-MEM-010).
	MemoryCapture *bool
	// WorkspaceOnly keeps the file tools inside the workspace; an absent key
	// means off.
	WorkspaceOnly bool
	// Vision is nil when the key is absent; the model catalog decides then
	// (API-ATT-005).
	Vision      *bool
	Description string
	// Title is the agent role, "" when the key is absent (API-ROOM-007).
	Title string
	// Color is the avatar color, "" when absent and derived from the name
	// (API-ROOM-007).
	Color string
	Rules agentRules
}

type agentRules struct {
	Allow []string
	Deny  []string
}

// agentValidationError carries config.Check issues through the section writer.
type agentValidationError struct {
	issues []ConfigIssue
}

func (e *agentValidationError) Error() string { return "config.toml is not valid" }

// handleAgents lists agents. The config file is the source of the rows, so a
// save shows up before the daemon restarts (Q-008); the runtime backend still
// supplies MCP status and session counts when it is available.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	store := s.configStore
	if store == nil && s.backend == nil {
		s.logger.Error("web: agent list has no config store or backend")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	backendRows := map[string]Agent{}
	if s.backend != nil {
		for _, agent := range s.backend.Agents() {
			backendRows[agent.Name] = agent
		}
	}

	tables := map[string]map[string]any{}
	// comboFirst is the first model of each combo, for the claude_code flag
	// (API-CCT-006).
	comboFirst := map[string]string{}
	// accounts are the Claude Code accounts (REQ-CCT-010).
	accounts := map[string]bool{claudeCodeRuntime: true}
	defaultAgent := ""
	hasCLIRoute := false
	if store != nil {
		file, err := store.ConfigFile(r.Context())
		if err != nil {
			s.logger.Error("web: read config for agent list", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		if file.Exists {
			tables, err = parseAgentTables(file.Raw)
			if err != nil {
				s.logger.Error("web: parse config for agent list", "error", err)
				writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				return
			}
			if document, err := parseProviderDocument(file.Raw); err == nil {
				accounts = document.claudeAccounts()
				for name, table := range document.Combos {
					if models := mapProviderStrings(table, "models"); len(models) > 0 {
						comboFirst[name] = models[0]
					}
				}
			}
			routeAgent, ok, err := parseCLIRouteAgent(file.Raw)
			if err != nil {
				s.logger.Error("web: parse routes for agent list", "error", err)
				writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				return
			}
			if ok {
				defaultAgent, hasCLIRoute = routeAgent, true
			}
		}
	}

	names := make([]string, 0, len(tables)+len(backendRows))
	seen := map[string]bool{}
	for name := range tables {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for name := range backendRows {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if !hasCLIRoute && seen["default"] {
		defaultAgent = "default"
	}

	stats := s.agentSessionStats(r.Context())
	response := listAgentsResponse{Agents: make([]agentJSON, 0, len(names))}
	for _, name := range names {
		row := agentJSON{
			Name:          name,
			Kind:          "agent",
			Skills:        []string{},
			MCP:           []string{},
			MCPStatus:     []mcpStatusJSON{},
			DisabledTools: []string{},
			MemoryCapture: true,
			Color:         core.ResolveAgentColor(name, ""),
			EngineLabel:   engineLabelLana,
		}
		model := ""
		configTools := 0
		if agent, ok := backendRows[name]; ok {
			if agent.Kind != "" {
				row.Kind = agent.Kind
			}
			if agent.Kind == "team" {
				// A team row owns no model, skills, or MCP servers; its
				// policy is the lead's (API-TEAM-006).
				row.Policy = agent.Policy
				row.Lead = agent.Lead
				row.Members = nonNilStrings(agent.Members)
				row.Description = agent.Description
				// A team row shows the team description as its title and the
				// brand color (API-ROOM-007).
				row.Title = agent.Description
				row.Color = "brand"
				row.AvatarSVG, row.AvatarSource = avatar.DefaultFor(name), avatar.Default
				if stat, ok := stats[name]; ok && stat.count > 0 {
					row.SessionsCount = stat.count
					lastActive := stat.lastActive
					row.LastActiveAt = &lastActive
				}
				response.Agents = append(response.Agents, row)
				continue
			}
			model = agent.Model
			row.Policy = agent.Policy
			row.Description = agent.Description
			row.Skills = nonNilStrings(agent.Skills)
			row.MCP = nonNilStrings(agent.MCP)
			for _, status := range agent.MCPStatus {
				row.MCPStatus = append(row.MCPStatus, mcpStatusJSON(status))
			}
		}
		if table, ok := tables[name]; ok {
			record := agentRecordFromMap(table)
			row.Description = record.Description
			row.Title = record.Title
			row.Color = core.ResolveAgentColor(name, record.Color)
			row.Provider = record.Provider
			row.Permission = record.Permission
			row.Workspace = record.Workspace
			row.DisabledTools = nonNilStrings(record.DisabledTools)
			if record.MemoryCapture != nil {
				row.MemoryCapture = *record.MemoryCapture
			}
			row.WorkspaceOnly = record.WorkspaceOnly
			if record.Vision != nil {
				vision := *record.Vision
				row.Vision = &vision
			}
			row.ReasoningEffort = record.ReasoningEffort
			// API-CCT-006: runtime stays only for a legacy file; its model
			// shows as the claude-code address it means.
			row.Runtime = record.Runtime
			legacy := record.Runtime != ""
			migrateLegacyRuntime(&record)
			if legacy || isClaudeAccountModel(record.Model, accounts) || isClaudeAccountModel(comboFirst[record.Model], accounts) {
				row.EngineLabel = engineLabelFor(claudeCodeRuntime)
				row.Auth = record.Auth
				if row.Auth == "" {
					row.Auth = core.RuntimeAuthSubscription
				}
				row.APIKeyEnv = record.APIKeyEnv
				row.Binary = record.Binary
				allow := record.AllowScheduled
				row.AllowScheduled = &allow
			}
			configTools = len(record.Tools)
			if len(row.Skills) == 0 && len(record.Skills) > 0 {
				row.Skills = nonNilStrings(record.Skills)
			}
			if len(row.MCP) == 0 && len(record.MCP) > 0 {
				row.MCP = nonNilStrings(record.MCP)
			}
			switch {
			case record.Provider != "" && record.Model != "":
				model = record.Provider + ":" + record.Model
			case record.Model != "":
				model = record.Model
			}
			if record.Permission != "" {
				row.Policy = record.Permission
			}
		}
		if row.Provider == "" {
			// "provider:model" (legacy) or a "provider/model" address; a combo
			// name has no single provider (API-RTR-003).
			if i := strings.IndexAny(model, "/:"); i > 0 {
				row.Provider = model[:i]
			}
		}
		// API-CCT-006: the agent's first target runs in Claude Code.
		row.ClaudeCode = row.Kind == "agent" && (isClaudeAccountModel(model, accounts) || isClaudeAccountModel(comboFirst[model], accounts))
		if row.Permission == "" {
			row.Permission = row.Policy
		}
		if model != "" {
			row.Model = &model
		}
		if count, ok := s.agentToolCount(name); ok {
			row.ToolsCount = count
		} else {
			row.ToolsCount = configTools
		}
		row.AvatarSVG, row.AvatarSource = s.agentAvatar(name)
		row.Default = defaultAgent != "" && name == defaultAgent
		if stat, ok := stats[name]; ok && stat.count > 0 {
			row.SessionsCount = stat.count
			lastActive := stat.lastActive
			row.LastActiveAt = &lastActive
		}
		response.Agents = append(response.Agents, row)
	}
	writeJSON(w, http.StatusOK, response)
}

type agentSessionStat struct {
	count      int
	lastActive int64
}

// agentSessionStats walks the session pages once and aggregates the count and
// last activity per agent. A backend error is not fatal for the agent list;
// the counts stay at zero and the error is logged.
func (s *Server) agentSessionStats(ctx context.Context) map[string]agentSessionStat {
	stats := map[string]agentSessionStat{}
	if s.backend == nil {
		return stats
	}
	filter := SessionFilter{Limit: defaultSessionLimit}
	for {
		page, err := s.backend.Sessions(ctx, filter)
		if err != nil {
			s.logger.Debug("web: list sessions for agents", "error", err)
			return stats
		}
		for _, session := range page.Sessions {
			info := session.SessionInfo
			stat := stats[info.Agent]
			stat.count++
			if info.UpdatedAt > stat.lastActive {
				stat.lastActive = info.UpdatedAt
			}
			stats[info.Agent] = stat
		}
		if page.NextBefore == nil {
			break
		}
		next := *page.NextBefore
		if next <= 0 || (filter.Before > 0 && next >= filter.Before) {
			break
		}
		filter.Before = next
	}
	return stats
}

// agentToolCount returns the number of enabled tools the catalog offers the
// agent. The second result is false when no catalog is available.
func (s *Server) agentToolCount(agent string) (int, bool) {
	catalog, ok := s.backend.(ToolCatalog)
	if !ok {
		return 0, false
	}
	groups, err := catalog.ToolCatalog(agent)
	if err != nil {
		s.logger.Debug("web: tool catalog for agent list", "agent", agent, "error", err)
		return 0, false
	}
	count := 0
	for _, group := range groups {
		for _, tool := range group.Tools {
			if tool.Enabled {
				count++
			}
		}
	}
	return count, true
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	home := s.agentHome()
	if home == "" {
		s.logger.Error("web: agent home is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	var body map[string]json.RawMessage
	if !decodeJSONObject(w, r, agentBodyLimit, &body) {
		return
	}
	if issues := unknownAgentKeys(body, createAgentKeys); len(issues) > 0 {
		writeAgentIssues(w, issues)
		return
	}
	name, present, err := agentStringField(body, "name")
	if err != nil || !present || name == "" {
		writeAgentIssue(w, "name", "name is required")
		return
	}
	if !agentNameRE.MatchString(name) {
		writeAgentIssue(w, "name", "name must match [a-z0-9-]+")
		return
	}
	// API-CCR-004: runtime "claude-code" creates a Claude Agent, which has no
	// template, provider, or lana tools; "" or "lana" creates a lana agent
	// from a template.
	var record agentRecord
	if raw, ok := body["runtime"]; ok {
		if err := applyAgentField(&record, "runtime", raw); err != nil {
			writeAgentIssue(w, "runtime", "runtime must be a string")
			return
		}
	}
	var tpl agenthome.Template
	permission := "ask"
	if record.Runtime == "" {
		template, present, err := agentStringField(body, "template")
		if err != nil || !present || template == "" {
			writeAgentIssue(w, "template", "template is required")
			return
		}
		found, ok := agenthome.LookupTemplate(template)
		if !ok {
			writeAgentIssue(w, "template", "template must be assistant, coder, or worker")
			return
		}
		tpl, permission = found, found.Permission
	} else if _, set := body["template"]; set {
		writeAgentIssue(w, "template", "a "+record.Runtime+" agent has no template")
		return
	}
	for _, key := range []string{"allow_scheduled", "api_key_env", "auth", "binary", "color", "model", "reasoning_effort", "rules", "title"} {
		raw, ok := body[key]
		if !ok {
			continue
		}
		if err := applyAgentField(&record, key, raw); err != nil {
			writeAgentIssue(w, "agents."+name+"."+key, err.Error())
			return
		}
	}
	if problem := checkAgentEffort(record, s.claudeAccountSet(r.Context())); problem != "" {
		writeAgentIssue(w, "agents."+name+".reasoning_effort", problem)
		return
	}
	workspace, _, err := agentStringField(body, "workspace")
	if err != nil {
		writeAgentIssue(w, "workspace", "workspace must be a string")
		return
	}
	description, _, err := agentStringField(body, "description")
	if err != nil {
		writeAgentIssue(w, "description", "description must be a string")
		return
	}
	if problem := validateAgentDescription(description); problem != "" {
		writeAgentIssue(w, "agents."+name+".description", problem)
		return
	}
	if value, present, err := agentStringField(body, "permission"); err != nil {
		writeAgentIssue(w, "permission", "permission must be a string")
		return
	} else if present {
		if !validAgentPermission(value) {
			writeAgentIssue(w, "permission", "permission must be one of ask, trust, never")
			return
		}
		permission = value
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before agent create", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	raw := file.Raw
	if !file.Exists {
		raw = []byte("version = 1\n")
	}
	tables, err := parseAgentTables(raw)
	if err != nil {
		s.logger.Error("web: parse config before agent create", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if _, exists := tables[name]; exists {
		writeAgentExists(w)
		return
	}
	paths := agenthome.Paths(home, name)
	if exists, err := pathExists(paths.Dir); err != nil {
		s.logger.Error("web: stat agent home", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	} else if exists {
		writeAgentExists(w)
		return
	}

	record.Workspace = workspace
	record.Permission = permission
	record.Description = description
	persona := ""
	legacyBody := record.Runtime != ""
	// API-CCT-002: the dashboard writes only the claude-code address.
	migrateLegacyRuntime(&record)
	if !legacyBody {
		persona, err = tpl.Persona(name)
		if err != nil {
			s.logger.Error("web: render agent template", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		// A lana agent without a model starts on the first lana agent's
		// model.
		if record.Model == "" {
			defaults := defaultAgentRecord(tables)
			record.Provider, record.Model = defaults.Provider, defaults.Model
		}
		record.Tools = append([]string(nil), tpl.Tools...)
	}
	table := renderAgentTable(name, record)
	candidate, changed, err := webfiles.RewriteTable(r.Context(), agentChecker(store), raw, "[agents."+name+"]", table)
	if err != nil {
		s.writeAgentSaveError(w, "create agent", err)
		return
	}
	// A legacy Claude Agent body has no template, so no persona file.
	if !legacyBody {
		if err := agenthome.WriteFile(paths.Agent, persona); err != nil {
			_ = os.RemoveAll(paths.Dir)
			s.logger.Error("web: write agent home", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
	}
	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		_ = os.RemoveAll(paths.Dir)
		s.logger.Error("web: write config for agent create", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	applied := s.applyLiveConfig(r.Context(), store, candidate)
	s.auditSave("agent.create", agentConfigPath(file, home), name, changed)
	writeJSON(w, http.StatusCreated, agentCreatedResponse{
		Name:            name,
		RestartRequired: !applied,
	})
}

func (s *Server) handlePutAgent(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	if !agenthome.ValidName(name) {
		writeAPIError(w, http.StatusNotFound, "not_found", "agent is not configured")
		return
	}
	var body map[string]json.RawMessage
	if !decodeJSONObject(w, r, agentBodyLimit, &body) {
		return
	}
	if issues := unknownAgentKeys(body, putAgentKeys); len(issues) > 0 {
		for i := range issues {
			issues[i].Key = "agents." + name + "." + issues[i].Key
		}
		writeAgentIssues(w, issues)
		return
	}
	expected, present, err := agentStringField(body, "expected_version")
	if err != nil || !present {
		writeAgentIssue(w, "expected_version", "expected_version is required")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before agent save", "error", err)
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
	tables, err := parseAgentTables(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before agent save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	source, exists := tables[name]
	if !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "agent is not configured")
		return
	}
	record := agentRecordFromMap(source)
	previousRuntime := record.Runtime
	keys := make([]string, 0, len(agentKeys))
	for key := range agentKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		raw, ok := body[key]
		if !ok {
			continue
		}
		if err := applyAgentField(&record, key, raw); err != nil {
			writeAgentIssue(w, "agents."+name+"."+key, err.Error())
			return
		}
	}
	switchAgentRuntime(&record, previousRuntime, body)
	// API-CCT-002: a save writes the claude-code address and no runtime.
	migrateLegacyRuntime(&record)
	if problem := checkAgentEffort(record, claudeAccountsOf(file.Raw)); problem != "" {
		writeAgentIssue(w, "agents."+name+".reasoning_effort", problem)
		return
	}
	table := renderAgentTable(name, record)
	candidate, changed, err := webfiles.RewriteTable(r.Context(), agentChecker(store), file.Raw, "[agents."+name+"]", table)
	if err != nil {
		s.writeAgentSaveError(w, "save agent", err)
		return
	}
	if !bytes.Equal(candidate, file.Raw) {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for agent save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		s.auditSave("agent.save", agentConfigPath(file, s.agentHome()), name, changed)
	}
	applied := s.applyLiveConfig(r.Context(), store, candidate)
	version := webfiles.ConfigVersion(candidate)
	writeJSON(w, http.StatusOK, putAgentResponse{
		Name:            name,
		Version:         version,
		RestartRequired: !applied,
	})
}

func (s *Server) handleDuplicateAgent(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	home := s.agentHome()
	if home == "" {
		s.logger.Error("web: agent home is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	sourceName := r.PathValue("name")
	var body map[string]json.RawMessage
	if !decodeJSONObject(w, r, agentBodyLimit, &body) {
		return
	}
	if issues := unknownAgentKeys(body, duplicateAgentKeys); len(issues) > 0 {
		writeAgentIssues(w, issues)
		return
	}
	name, present, err := agentStringField(body, "name")
	if err != nil || !present || name == "" {
		writeAgentIssue(w, "name", "name is required")
		return
	}
	if !agentNameRE.MatchString(name) {
		writeAgentIssue(w, "name", "name must match [a-z0-9-]+")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before agent duplicate", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "agent is not configured")
		return
	}
	tables, err := parseAgentTables(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before agent duplicate", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	source, exists := tables[sourceName]
	if !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "agent is not configured")
		return
	}
	if _, exists := tables[name]; exists {
		writeAgentExists(w)
		return
	}
	paths := agenthome.Paths(home, name)
	if exists, err := pathExists(paths.Dir); err != nil {
		s.logger.Error("web: stat agent home", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	} else if exists {
		writeAgentExists(w)
		return
	}

	sourcePaths := agenthome.Paths(home, sourceName)
	persona, err := os.ReadFile(sourcePaths.Agent)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Error("web: read agent persona for duplicate", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	record := agentRecordFromMap(source)
	table := renderAgentTable(name, record)
	candidate, changed, err := webfiles.RewriteTable(r.Context(), agentChecker(store), file.Raw, "[agents."+name+"]", table)
	if err != nil {
		s.writeAgentSaveError(w, "duplicate agent", err)
		return
	}
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		s.logger.Error("web: create agent home for duplicate", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if err := webfiles.AtomicWriteMode(paths.Agent, persona, 0o600); err != nil {
		_ = os.RemoveAll(paths.Dir)
		s.logger.Error("web: write agent persona for duplicate", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		_ = os.RemoveAll(paths.Dir)
		s.logger.Error("web: write config for agent duplicate", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	applied := s.applyLiveConfig(r.Context(), store, candidate)
	s.auditSave("agent.duplicate", agentConfigPath(file, home), name, changed)
	writeJSON(w, http.StatusCreated, agentCreatedResponse{
		Name:            name,
		RestartRequired: !applied,
	})
}

// agentChecker adapts the config port to the section writer's check callback.
func agentChecker(store ConfigStore) func(context.Context, []byte) error {
	return func(ctx context.Context, candidate []byte) error {
		issues, err := store.ConfigCheck(ctx, candidate)
		if err != nil {
			return err
		}
		if len(issues) > 0 {
			return &agentValidationError{issues: issues}
		}
		return nil
	}
}

func (s *Server) writeAgentSaveError(w http.ResponseWriter, operation string, err error) {
	var validation *agentValidationError
	if errors.As(err, &validation) {
		writeAgentIssues(w, validation.issues)
		return
	}
	s.logger.Error("web: "+operation, "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

// agentHome returns the LANA_HOME used for agent folders.
func (s *Server) agentHome() string {
	if store, ok := s.configStore.(interface{ SettingsHome() string }); ok {
		if home := store.SettingsHome(); home != "" {
			return home
		}
	}
	return s.cfg.Home
}

func unknownAgentKeys(body map[string]json.RawMessage, allowed map[string]bool) []ConfigIssue {
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

func agentStringField(body map[string]json.RawMessage, key string) (string, bool, error) {
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

func applyAgentField(record *agentRecord, key string, raw json.RawMessage) error {
	switch key {
	case "runtime":
		// "" or "lana" is lana's own loop; any other name is checked by the
		// config validation (API-CCR-001).
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		value = strings.TrimSpace(value)
		if value == engineLabelLana {
			value = ""
		}
		record.Runtime = value
		return nil
	case "auth":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if value != "" && value != core.RuntimeAuthSubscription && value != core.RuntimeAuthAPIKey {
			return errors.New("auth must be one of subscription, api_key")
		}
		record.Auth = value
		return nil
	case "api_key_env":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		value = strings.TrimSpace(value)
		if value != "" && !providerEnvRE.MatchString(value) {
			return errors.New("api_key_env must match [A-Za-z_][A-Za-z0-9_]*")
		}
		record.APIKeyEnv = value
		return nil
	case "binary":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if strings.ContainsAny(value, "\r\n") {
			return errors.New("must be one line")
		}
		record.Binary = strings.TrimSpace(value)
		return nil
	case "allow_scheduled":
		return json.Unmarshal(raw, &record.AllowScheduled)
	case "provider":
		return json.Unmarshal(raw, &record.Provider)
	case "model":
		return json.Unmarshal(raw, &record.Model)
	case "fallback":
		return json.Unmarshal(raw, &record.Fallback)
	case "prompt_file":
		return json.Unmarshal(raw, &record.PromptFile)
	case "workspace":
		return json.Unmarshal(raw, &record.Workspace)
	case "tools":
		return json.Unmarshal(raw, &record.Tools)
	case "disabled_tools":
		return json.Unmarshal(raw, &record.DisabledTools)
	case "subagents":
		return json.Unmarshal(raw, &record.Subagents)
	case "mcp":
		return json.Unmarshal(raw, &record.MCP)
	case "skills":
		return json.Unmarshal(raw, &record.Skills)
	case "permission":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if value != "" && !validAgentPermission(value) {
			return errors.New("permission must be one of ask, trust, never")
		}
		record.Permission = value
		return nil
	case "max_steps":
		var value int
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		// REQ-LTC-040: max_steps is opt-in; 0 means no limit and there is no
		// upper bound.
		if value < 0 {
			return errors.New("max_steps must be 0 or greater")
		}
		record.MaxSteps = value
		return nil
	case "context_window":
		var value int
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if value <= 0 {
			return errors.New("context_window must be greater than 0")
		}
		record.ContextWindow = value
		return nil
	case "reasoning_effort":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		// REQ-MDL-001: an empty value uses the model's catalog default. The
		// value is checked against the agent's final runtime once every field
		// is applied (checkAgentEffort).
		if value != "" && !validReasoningEffort(value) && !containsString(runtimeAgentEfforts, value) {
			return errors.New("reasoning_effort must be one of off, low, medium, high, xhigh, max")
		}
		record.ReasoningEffort = value
		return nil
	case "compaction_headroom":
		var value int
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if value <= 0 {
			return errors.New("compaction_headroom must be greater than 0")
		}
		record.CompactionHeadroom = value
		return nil
	case "compaction_threshold", "compaction_retain":
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if value <= 0 || value >= 1 {
			return errors.New(key + " must be between 0 and 1")
		}
		if key == "compaction_threshold" {
			record.CompactionThreshold = value
		} else {
			record.CompactionRetain = value
		}
		return nil
	case "memory_capture":
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		record.MemoryCapture = &value
		return nil
	case "workspace_only":
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		record.WorkspaceOnly = value
		return nil
	case "vision":
		// API-ATT-005: a boolean that overrides the catalog vision flag.
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		record.Vision = &value
		return nil
	case "description":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if problem := validateAgentDescription(value); problem != "" {
			return errors.New(problem)
		}
		record.Description = value
		return nil
	case "title":
		// REQ-ROOM-009, API-ROOM-007: an empty title removes the key.
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if problem := validateAgentTitle(value); problem != "" {
			return errors.New(problem)
		}
		record.Title = value
		return nil
	case "color":
		// REQ-ROOM-009, API-ROOM-007: an empty color removes the key.
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if !core.ValidAgentColor(value) {
			return errors.New(core.AgentColorMessage)
		}
		record.Color = value
		return nil
	case "rules":
		var rules struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		}
		if err := json.Unmarshal(raw, &rules); err != nil {
			return err
		}
		record.Rules = agentRules{Allow: rules.Allow, Deny: rules.Deny}
		return nil
	default:
		return fmt.Errorf("unknown key")
	}
}

// engineLabelFor returns the engine name of a runtime (API-CCR-004).
func engineLabelFor(runtime string) string {
	switch runtime {
	case "":
		return engineLabelLana
	case claudeCodeRuntime:
		return engineLabelClaude
	default:
		return runtime
	}
}

// switchAgentRuntime handles a body that still sends the deprecated engine
// key (API-CCT-002). Going from Claude Code back to lana ("" or "lana") on a
// legacy agent clears a model and reasoning effort the body does not set:
// Claude Code's model names do not carry over to lana's router. The
// Claude-only keys and lana's keys are kept on every agent (ADR 0020).
func switchAgentRuntime(record *agentRecord, previous string, body map[string]json.RawMessage) {
	if previous == "" || record.Runtime != "" {
		return
	}
	if _, set := body["runtime"]; !set {
		return
	}
	for _, key := range []string{"model", "reasoning_effort"} {
		if _, set := body[key]; !set {
			clearAgentField(record, key)
		}
	}
}

// migrateLegacyRuntime rewrites runtime = "claude-code" with model "M" as
// model "claude-code/M" ("default" when M is empty), with no runtime or
// provider (API-CCT-002).
func migrateLegacyRuntime(record *agentRecord) {
	if record.Runtime != claudeCodeRuntime {
		return
	}
	model := record.Model
	if model == "" {
		model = "default"
	}
	if !isClaudeCodeModel(model) {
		model = claudeCodeRuntime + "/" + model
	}
	record.Model = model
	record.Provider = ""
	record.Runtime = ""
}

// isClaudeCodeModel reports whether model is a "claude-code/<model>" address.
func isClaudeCodeModel(model string) bool {
	rest, ok := strings.CutPrefix(model, claudeCodeRuntime+"/")
	return ok && rest != ""
}

// clearAgentField removes one key from the record, so renderAgentTable leaves
// it out of the table.
func clearAgentField(record *agentRecord, key string) {
	switch key {
	case "auth":
		record.Auth = ""
	case "api_key_env":
		record.APIKeyEnv = ""
	case "binary":
		record.Binary = ""
	case "allow_scheduled":
		record.AllowScheduled = false
	case "provider":
		record.Provider = ""
	case "model":
		record.Model = ""
	case "reasoning_effort":
		record.ReasoningEffort = ""
	case "fallback":
		record.Fallback = nil
	case "tools":
		record.Tools = nil
	case "disabled_tools":
		record.DisabledTools = nil
	case "mcp":
		record.MCP = nil
	case "skills":
		record.Skills = nil
	case "subagents":
		record.Subagents = nil
	case "max_steps":
		record.MaxSteps = 0
	case "compaction_headroom":
		record.CompactionHeadroom = 0
	case "compaction_threshold":
		record.CompactionThreshold = 0
	case "compaction_retain":
		record.CompactionRetain = 0
	case "memory_capture":
		record.MemoryCapture = nil
	case "context_window":
		record.ContextWindow = 0
	case "vision":
		record.Vision = nil
	}
}

// checkAgentEffort checks reasoning_effort against the agent's runtime: off,
// low, high, max for lana (REQ-MDL-001); low, medium, high, xhigh, max for a
// Claude Agent (API-CCR-001). It returns "" when the value is valid.
func checkAgentEffort(record agentRecord, accounts map[string]bool) string {
	effort := record.ReasoningEffort
	if effort == "" {
		return ""
	}
	if record.Runtime == claudeCodeRuntime {
		if !containsString(runtimeAgentEfforts, effort) {
			return "reasoning_effort must be one of low, medium, high, xhigh, max"
		}
		return ""
	}
	// An agent on a Claude Code address (any account) takes Claude Code's
	// efforts and lana's.
	if isClaudeAccountModel(record.Model, accounts) {
		if !validReasoningEffort(effort) && !containsString(runtimeAgentEfforts, effort) {
			return "reasoning_effort must be one of off, low, medium, high, xhigh, max"
		}
		return ""
	}
	if !validReasoningEffort(effort) {
		return "reasoning_effort must be one of off, low, high, max"
	}
	return ""
}

func validateAgentDescription(value string) string {
	if strings.ContainsAny(value, "\n\r") {
		return "must be one line"
	}
	if n := utf8.RuneCountInString(value); n > maxAgentDescription {
		return fmt.Sprintf("must be at most %d characters (got %d)", maxAgentDescription, n)
	}
	return ""
}

// validateAgentTitle mirrors the config rule of API-ROOM-006: a single line of
// at most 60 characters.
func validateAgentTitle(value string) string {
	if strings.ContainsAny(value, "\n\r") {
		return "must be one line"
	}
	if n := utf8.RuneCountInString(value); n > maxAgentTitle {
		return fmt.Sprintf("must be at most %d characters (got %d)", maxAgentTitle, n)
	}
	return ""
}

func validAgentPermission(value string) bool {
	for _, allowed := range []string{"ask", "trust", "never"} {
		if value == allowed {
			return true
		}
	}
	return false
}

// validReasoningEffort reports whether value is one of the four efforts of
// REQ-MDL-001.
func validReasoningEffort(value string) bool {
	for _, allowed := range []string{"off", "low", "high", "max"} {
		if value == allowed {
			return true
		}
	}
	return false
}

func writeAgentIssue(w http.ResponseWriter, key, message string) {
	writeAgentIssues(w, []ConfigIssue{{Key: key, Message: message}})
}

func writeAgentIssues(w http.ResponseWriter, issues []ConfigIssue) {
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
	writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "agent is not valid", details)
}

func writeAgentExists(w http.ResponseWriter) {
	writeAPIError(w, http.StatusConflict, "invalid_state", "agent already exists")
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

func parseAgentTables(raw []byte) (map[string]map[string]any, error) {
	tables := map[string]map[string]any{}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return tables, nil
	}
	var root map[string]any
	if err := toml.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	agents, ok := root["agents"].(map[string]any)
	if !ok {
		return tables, nil
	}
	for name, value := range agents {
		table, ok := value.(map[string]any)
		if !ok {
			continue
		}
		tables[name] = table
	}
	return tables, nil
}

// parseCLIRouteAgent returns the agent of the first [[routes]] entry whose
// channel is "cli". The second result reports whether that route exists.
func parseCLIRouteAgent(raw []byte) (string, bool, error) {
	var root struct {
		Routes []core.RouteConfig `toml:"routes"`
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return "", false, nil
	}
	if err := toml.Unmarshal(raw, &root); err != nil {
		return "", false, err
	}
	for _, route := range root.Routes {
		if route.Channel == "cli" {
			return route.Agent, true, nil
		}
	}
	return "", false, nil
}

// defaultAgentRecord returns the record of the first lana agent by name; a
// runtime agent's model is not a lana model and is skipped.
func defaultAgentRecord(tables map[string]map[string]any) agentRecord {
	names := make([]string, 0, len(tables))
	for name, table := range tables {
		if mapAgentString(table, "runtime") != "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return agentRecord{}
	}
	sort.Strings(names)
	return agentRecordFromMap(tables[names[0]])
}

func agentRecordFromMap(table map[string]any) agentRecord {
	record := agentRecord{
		Runtime:             mapAgentString(table, "runtime"),
		Auth:                mapAgentString(table, "auth"),
		APIKeyEnv:           mapAgentString(table, "api_key_env"),
		Binary:              mapAgentString(table, "binary"),
		AllowScheduled:      mapAgentBool(table, "allow_scheduled"),
		Provider:            mapAgentString(table, "provider"),
		Model:               mapAgentString(table, "model"),
		Fallback:            mapAgentStrings(table, "fallback"),
		PromptFile:          mapAgentString(table, "prompt_file"),
		Workspace:           mapAgentString(table, "workspace"),
		Tools:               mapAgentStrings(table, "tools"),
		DisabledTools:       mapAgentStrings(table, "disabled_tools"),
		Subagents:           mapAgentStrings(table, "subagents"),
		MCP:                 mapAgentStrings(table, "mcp"),
		Skills:              mapAgentStrings(table, "skills"),
		Permission:          mapAgentString(table, "permission"),
		MaxSteps:            mapAgentInt(table, "max_steps"),
		ContextWindow:       mapAgentInt(table, "context_window"),
		ReasoningEffort:     mapAgentString(table, "reasoning_effort"),
		CompactionHeadroom:  mapAgentInt(table, "compaction_headroom"),
		CompactionThreshold: mapAgentFloat(table, "compaction_threshold"),
		CompactionRetain:    mapAgentFloat(table, "compaction_retain"),
		MemoryCapture:       mapAgentBoolPtr(table, "memory_capture"),
		WorkspaceOnly:       mapAgentBool(table, "workspace_only"),
		Vision:              mapAgentBoolPtr(table, "vision"),
		Description:         mapAgentString(table, "description"),
		Title:               mapAgentString(table, "title"),
		Color:               mapAgentString(table, "color"),
	}
	if rules, ok := table["rules"].(map[string]any); ok {
		record.Rules = agentRules{
			Allow: mapAgentStrings(rules, "allow"),
			Deny:  mapAgentStrings(rules, "deny"),
		}
	}
	return record
}

func mapAgentString(table map[string]any, key string) string {
	value, _ := table[key].(string)
	return value
}

func mapAgentStrings(table map[string]any, key string) []string {
	list, ok := table[key].([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(list))
	for _, item := range list {
		if value, ok := item.(string); ok {
			values = append(values, value)
		}
	}
	return values
}

func mapAgentInt(table map[string]any, key string) int {
	switch value := table[key].(type) {
	case int64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}

func mapAgentFloat(table map[string]any, key string) float64 {
	switch value := table[key].(type) {
	case float64:
		return value
	case int64:
		return float64(value)
	case int:
		return float64(value)
	default:
		return 0
	}
}

// mapAgentBoolPtr returns nil when the key is absent. A config with no
// memory_capture key means on (REQ-MEM-010).
func mapAgentBoolPtr(table map[string]any, key string) *bool {
	value, ok := table[key].(bool)
	if !ok {
		return nil
	}
	return &value
}

// mapAgentBool returns false when the key is absent. A config with no
// workspace_only key leaves the guard open.
func mapAgentBool(table map[string]any, key string) bool {
	value, _ := table[key].(bool)
	return value
}

func renderAgentTable(name string, record agentRecord) string {
	// API-CCT-002: every dashboard write uses the claude-code address.
	migrateLegacyRuntime(&record)
	var b strings.Builder
	fmt.Fprintf(&b, "[agents.%s]\n", name)
	writeAgentString(&b, "runtime", record.Runtime)
	writeAgentString(&b, "provider", record.Provider)
	writeAgentString(&b, "model", record.Model)
	writeAgentString(&b, "auth", record.Auth)
	writeAgentString(&b, "api_key_env", record.APIKeyEnv)
	writeAgentString(&b, "binary", record.Binary)
	writeAgentBool(&b, "allow_scheduled", record.AllowScheduled)
	writeAgentStrings(&b, "fallback", record.Fallback)
	writeAgentString(&b, "prompt_file", record.PromptFile)
	writeAgentString(&b, "workspace", record.Workspace)
	writeAgentStrings(&b, "tools", record.Tools)
	writeAgentStrings(&b, "disabled_tools", record.DisabledTools)
	writeAgentStrings(&b, "subagents", record.Subagents)
	writeAgentStrings(&b, "mcp", record.MCP)
	writeAgentStrings(&b, "skills", record.Skills)
	writeAgentString(&b, "permission", record.Permission)
	writeAgentInt(&b, "max_steps", record.MaxSteps)
	writeAgentInt(&b, "context_window", record.ContextWindow)
	writeAgentString(&b, "reasoning_effort", record.ReasoningEffort)
	writeAgentInt(&b, "compaction_headroom", record.CompactionHeadroom)
	writeAgentFloat(&b, "compaction_threshold", record.CompactionThreshold)
	writeAgentFloat(&b, "compaction_retain", record.CompactionRetain)
	writeAgentBoolPtr(&b, "memory_capture", record.MemoryCapture)
	writeAgentBool(&b, "workspace_only", record.WorkspaceOnly)
	writeAgentBoolPtr(&b, "vision", record.Vision)
	writeAgentString(&b, "description", record.Description)
	writeAgentString(&b, "title", record.Title)
	writeAgentString(&b, "color", record.Color)
	if len(record.Rules.Allow) > 0 || len(record.Rules.Deny) > 0 {
		parts := make([]string, 0, 2)
		if len(record.Rules.Allow) > 0 {
			parts = append(parts, "allow = "+agentStringArray(record.Rules.Allow))
		}
		if len(record.Rules.Deny) > 0 {
			parts = append(parts, "deny = "+agentStringArray(record.Rules.Deny))
		}
		fmt.Fprintf(&b, "rules = { %s }\n", strings.Join(parts, ", "))
	}
	return b.String()
}

func writeAgentString(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "%s = %s\n", key, agentTOMLString(value))
}

func writeAgentStrings(b *strings.Builder, key string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "%s = %s\n", key, agentStringArray(values))
}

func writeAgentInt(b *strings.Builder, key string, value int) {
	if value == 0 {
		return
	}
	fmt.Fprintf(b, "%s = %d\n", key, value)
}

func writeAgentFloat(b *strings.Builder, key string, value float64) {
	if value == 0 {
		return
	}
	fmt.Fprintf(b, "%s = %s\n", key, strconv.FormatFloat(value, 'g', -1, 64))
}

// writeAgentBoolPtr writes the key only when it is present; nil means the
// default and is left out of the table.
func writeAgentBoolPtr(b *strings.Builder, key string, value *bool) {
	if value == nil {
		return
	}
	fmt.Fprintf(b, "%s = %t\n", key, *value)
}

// writeAgentBool writes the key only when it is true; false is the default and
// is left out of the table.
func writeAgentBool(b *strings.Builder, key string, value bool) {
	if !value {
		return
	}
	fmt.Fprintf(b, "%s = %t\n", key, value)
}

func agentStringArray(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, agentTOMLString(value))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// agentTOMLString encodes one string as a TOML basic string through the same
// marshaller the raw config writer uses.
func agentTOMLString(value string) string {
	data, err := toml.Marshal(value)
	if err != nil {
		return `""`
	}
	return strings.TrimSuffix(string(data), "\n")
}

// agentConfigPath returns the config path for an audit line when the store did
// not supply one.
func agentConfigPath(file ConfigFile, home string) string {
	if file.Path != "" {
		return file.Path
	}
	return filepath.Join(home, "config.toml")
}
