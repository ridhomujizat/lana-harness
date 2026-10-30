package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/agent/compaction"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
	"github.com/ridhomujizat/lana-harness/providers/openai"
	"github.com/ridhomujizat/lana-harness/tools/permission"
	"github.com/ridhomujizat/lana-harness/web"
)

// webBackend adapts *lana.Runtime to the web port. It is the only place that
// knows both the web package and the runtime.
type webBackend struct {
	rt   *lana.Runtime
	home string
	// leases is the daemon lease set shared with the Telegram channel.
	leases *leaseSet
	// loadedSHA256 is the sha256 of config.toml when the daemon started. A
	// save never updates it; the running runtime keeps the loaded config.
	loadedSHA256 string
	// startedAt is the start time of this process image. An in-place restart
	// builds a new backend, so GET /v1/daemon reports a new value.
	startedAt time.Time
	// restart schedules the in-place restart; nil means it is disabled.
	restart *restartController

	mu sync.Mutex
	// root is the daemon root context. Turns run on it, never on a request
	// context, so closing the browser does not cancel them.
	root context.Context
	// active holds the sessions whose turn this process started.
	active map[string]struct{}
}

var _ web.Backend = (*webBackend)(nil)
var _ web.ConfigStore = (*webBackend)(nil)
var _ web.SecretStore = (*webBackend)(nil)
var _ web.AgentFileStore = (*webBackend)(nil)
var _ web.SkillStore = (*webBackend)(nil)
var _ web.ProviderTester = (*webBackend)(nil)
var _ web.MCPBackend = (*webBackend)(nil)
var _ web.ToolCatalog = (*webBackend)(nil)

const (
	// providerTestTimeout is the 15-second limit of API-WEB-019.
	providerTestTimeout = 15 * time.Second
	// providerTestKeyEnv is the internal env name that carries an inline key
	// to the adapter. The value never leaves this process.
	providerTestKeyEnv = "LANA_PROVIDER_TEST_KEY"
	// providerTestPrompt is the smallest possible chat message.
	providerTestPrompt = "ping"
)

// TestProvider implements web.ProviderTester with the same adapter the runtime
// uses (providers/openai). It sends exactly one chat request with one output
// token and a 15-second timeout. The key of the request is only passed to the
// adapter; it is never written, logged, or returned.
func (b *webBackend) TestProvider(ctx context.Context, test web.ProviderTest) (web.TestResult, error) {
	key := test.APIKey.Reveal()
	envName := strings.TrimSpace(test.APIKeyEnv)
	if envName == "" {
		envName = providerTestKeyEnv
	}
	secrets := providerTestSecrets{
		home:       b.home,
		inlineName: envName,
		inlineKey:  core.NewSecret(key),
	}
	if key == "" {
		if _, ok := secrets.Lookup(envName); !ok {
			return web.TestResult{
				OK:      false,
				Status:  0,
				Message: "env " + envName + " is not set",
			}, nil
		}
	}
	adapter, err := openai.NewAdapter("provider-test", openai.Config{
		BaseURL:   test.BaseURL,
		APIKeyEnv: envName,
		Timeout:   providerTestTimeout.String(),
	}, secrets)
	if err != nil {
		return web.TestResult{OK: false, Message: err.Error()}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, providerTestTimeout)
	defer cancel()
	start := time.Now()
	stream, err := adapter.Stream(ctx, core.ChatRequest{
		Model: test.Model,
		Messages: []core.Message{{
			Role: core.RoleUser,
			Content: []core.ContentBlock{{
				Kind: core.BlockText,
				Text: providerTestPrompt,
			}},
		}},
		MaxTokens: 1,
	})
	if err != nil {
		return providerTestFailure(err, test.BaseURL, key), nil
	}
	for _, streamErr := range stream {
		if streamErr != nil {
			return providerTestFailure(streamErr, test.BaseURL, key), nil
		}
	}
	return web.TestResult{OK: true, LatencyMS: time.Since(start).Milliseconds()}, nil
}

// providerTestSecrets looks an env name up in the inline key, the process
// environment, and then secrets.env, in the same order config.Load uses.
type providerTestSecrets struct {
	home       string
	inlineName string
	inlineKey  core.Secret
}

func (s providerTestSecrets) Lookup(name string) (core.Secret, bool) {
	if name == s.inlineName && s.inlineKey.Reveal() != "" {
		return s.inlineKey, true
	}
	if value := os.Getenv(name); value != "" {
		return core.NewSecret(value), true
	}
	if s.home == "" {
		return core.Secret{}, false
	}
	values, err := webfiles.ReadSecrets(filepath.Join(s.home, "secrets.env"))
	if err != nil {
		return core.Secret{}, false
	}
	if value := values[name]; value != "" {
		return core.NewSecret(value), true
	}
	return core.Secret{}, false
}

// providerTestFailure turns an adapter error into the contract result. The
// message names the provider host for an HTTP status, as AC-WEB-036 requires.
func providerTestFailure(err error, baseURL, key string) web.TestResult {
	status := 0
	message := err.Error()
	var providerErr *core.ProviderError
	if errors.As(err, &providerErr) {
		status = providerErr.Status
		if status == 0 {
			message = providerErr.Message
		} else {
			message = providerTestStatusMessage(status, baseURL)
		}
	}
	return web.TestResult{
		OK:      false,
		Status:  status,
		Message: redactProviderTestKey(message, key),
	}
}

func providerTestStatusMessage(status int, baseURL string) string {
	text := http.StatusText(status)
	if text == "" {
		return fmt.Sprintf("HTTP %d from %s", status, providerTestHost(baseURL))
	}
	return fmt.Sprintf("%d %s from %s", status, text, providerTestHost(baseURL))
}

func providerTestHost(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		return baseURL
	}
	return parsed.Hostname()
}

// redactProviderTestKey keeps a provider message from echoing the key.
func redactProviderTestKey(message, key string) string {
	if key == "" {
		return message
	}
	return strings.ReplaceAll(message, key, "***")
}

// newWebBackend builds the adapter with the daemon root context.
func newWebBackend(rt *lana.Runtime, home string, leases *leaseSet, root context.Context) *webBackend {
	backend := &webBackend{
		rt:           rt,
		home:         home,
		leases:       leases,
		loadedSHA256: configFileSHA(home),
		startedAt:    time.Now(),
		active:       map[string]struct{}{},
	}
	backend.SetRootContext(root)
	return backend
}

// SetRestartController installs the in-place restart scheduler (API-WEB-023).
func (b *webBackend) SetRestartController(controller *restartController) {
	b.restart = controller
}

// DaemonInfo implements web.DaemonStore (API-WEB-022).
func (b *webBackend) DaemonInfo() web.DaemonInfo {
	info := web.DaemonInfo{
		PID:              os.Getpid(),
		StartedAt:        b.startedAt.UnixMilli(),
		Home:             b.home,
		Version:          version,
		GoVersion:        runtime.Version(),
		OS:               runtime.GOOS,
		Arch:             runtime.GOARCH,
		RestartSupported: b.restart != nil && b.restart.supported(),
		RunningTurns:     []web.DaemonTurn{},
	}
	if b.rt == nil {
		return info
	}
	sessions, err := b.rt.ListAllSessions(context.Background(), core.SessionFilter{})
	if err != nil {
		return info
	}
	for _, session := range sessions {
		// Only turns this process will cancel are listed; a turn held by
		// another process keeps running through the restart.
		if !b.rt.TurnActive(session.ID) && !b.marked(session.ID) {
			continue
		}
		info.RunningTurns = append(info.RunningTurns, web.DaemonTurn{
			SessionID: session.ID,
			Title:     session.Title,
			Agent:     session.Agent,
		})
	}
	return info
}

// Restart implements web.DaemonStore. It preflights and schedules the restart;
// the daemon replaces its process image after the handler writes 202.
func (b *webBackend) Restart(context.Context) error {
	if b.restart == nil {
		return errors.New("restart is not available")
	}
	return b.restart.schedule()
}

// configFileSHA returns the sha256 of $LANA_HOME/config.toml, or "" when the
// file cannot be read. The daemon calls newWebBackend after config.Load, so
// this is the version of the file the runtime was built from.
func configFileSHA(home string) string {
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SetRootContext installs the daemon root context. web.Server.Start calls it
// with the context the daemon was started with.
func (b *webBackend) SetRootContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	b.root = ctx
	b.mu.Unlock()
}

func (b *webBackend) rootContext() context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.root == nil {
		return context.Background()
	}
	return b.root
}

// Sessions lists every channel, following the contract paging rule. The store
// has no before cursor, so a past-the-first page is filtered in memory.
func (b *webBackend) Sessions(ctx context.Context, filter web.SessionFilter) (web.SessionPage, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	storeFilter := core.SessionFilter{Channel: filter.Channel, TopLevelOnly: filter.TopLevelOnly}
	if filter.Before == 0 {
		storeFilter.Limit = limit + 1
	}
	list, err := b.rt.ListAllSessions(ctx, storeFilter)
	if err != nil {
		return web.SessionPage{}, err
	}
	if filter.Before > 0 {
		filtered := make([]core.SessionInfo, 0, len(list))
		for _, info := range list {
			if info.UpdatedAt < filter.Before {
				filtered = append(filtered, info)
			}
		}
		list = filtered
	}
	var next *int64
	if len(list) > limit {
		at := list[limit-1].UpdatedAt
		next = &at
		list = list[:limit]
	}
	sessions := make([]web.Session, 0, len(list))
	for _, info := range list {
		sessions = append(sessions, web.Session{SessionInfo: info, Team: b.teamForAgent(info.Agent)})
	}
	return web.SessionPage{Sessions: sessions, NextBefore: next}, nil
}

func (b *webBackend) Session(ctx context.Context, id string) (web.Session, error) {
	info, err := b.rt.SessionInfo(ctx, id)
	if err != nil {
		return web.Session{}, sessionLookupError(err)
	}
	return b.session(ctx, info)
}

func (b *webBackend) Events(ctx context.Context, id string, fromSeq int64) (web.EventPage, error) {
	info, err := b.rt.SessionInfo(ctx, id)
	if err != nil {
		return web.EventPage{}, sessionLookupError(err)
	}
	events, err := b.rt.History(ctx, info.ID)
	if err != nil {
		return web.EventPage{}, err
	}
	page := web.EventPage{Events: make([]core.SessionEvent, 0, len(events))}
	for _, event := range events {
		if event.Seq > page.LastSeq {
			page.LastSeq = event.Seq
		}
		if event.Seq > fromSeq {
			page.Events = append(page.Events, event)
		}
	}
	if page.LastSeq == 0 {
		page.LastSeq = info.LastSeq
	}
	return page, nil
}

func (b *webBackend) CreateSession(ctx context.Context, agent, model string) (web.Session, error) {
	info, err := b.rt.CreateChannelSession(ctx, "web", "owner", agent)
	if err != nil {
		if errors.Is(err, lana.ErrUnknownAgent) {
			return web.Session{}, web.ErrUnknownAgent
		}
		return web.Session{}, err
	}
	if model != "" {
		if err := b.rt.SetModel(ctx, info.ID, model); err != nil {
			return web.Session{}, modelSelectionError(err)
		}
	}
	return b.session(ctx, info)
}

func (b *webBackend) Agents() []web.Agent {
	names := b.rt.AgentNames()
	agents := make([]web.Agent, 0, len(names)+len(b.rt.TeamNames()))
	for _, name := range names {
		statuses := b.rt.MCPStatus(name)
		mcpStatus := make([]web.MCPStatus, 0, len(statuses))
		mcp := make([]string, 0, len(statuses))
		for _, status := range statuses {
			mcpStatus = append(mcpStatus, web.MCPStatus{Name: status.Name, State: status.State, Error: status.Reason})
			mcp = append(mcp, status.Name)
		}
		skills := b.rt.Skills(name)
		skillNames := make([]string, 0, len(skills))
		for _, skill := range skills {
			skillNames = append(skillNames, skill.Name)
		}
		agents = append(agents, web.Agent{
			Name:      name,
			Kind:      "agent",
			Model:     b.rt.AgentModel(name),
			Policy:    b.rt.AgentPolicy(name),
			Skills:    skillNames,
			MCP:       mcp,
			MCPStatus: mcpStatus,
		})
	}
	for _, name := range b.rt.TeamNames() {
		team, ok := b.rt.Team(name)
		if !ok {
			continue
		}
		agents = append(agents, web.Agent{
			Name:        name,
			Kind:        "team",
			Policy:      b.rt.AgentPolicy(name),
			Lead:        team.Lead,
			Members:     append([]string{}, team.Members...),
			Description: team.Description,
		})
	}
	return agents
}

// teamForAgent returns the team information of a session's agent, or nil when
// the agent is not a team. Members are names only (API-TEAM-005).
func (b *webBackend) teamForAgent(name string) *web.Team {
	team, ok := b.rt.Team(name)
	if !ok {
		return nil
	}
	return &web.Team{
		Name:    name,
		Lead:    team.Lead,
		Members: append([]string{}, team.Members...),
	}
}

// MCPServers implements web.MCPBackend. It returns the runtime state of each
// configured server; the web handler fills in the definition and the agents
// from config.toml so a save shows before a restart (Q-008).
func (b *webBackend) MCPServers() []web.MCPServerRow {
	statuses := map[string]core.MCPServerStatus{}
	for _, agent := range b.rt.AgentNames() {
		for _, status := range b.rt.MCPStatus(agent) {
			if existing, ok := statuses[status.Name]; ok {
				if existing.State == "running" || existing.State == "unavailable" {
					continue
				}
			}
			statuses[status.Name] = status
		}
	}

	names := map[string]struct{}{}
	for name := range statuses {
		names[name] = struct{}{}
	}
	file, err := b.ConfigFile(context.Background())
	if err != nil {
		slog.Warn("web: read config for MCP list", "error", err)
	} else if file.Exists {
		var document struct {
			MCP map[string]map[string]any `toml:"mcp"`
		}
		if err := toml.Unmarshal(file.Raw, &document); err != nil {
			slog.Warn("web: parse config for MCP list", "error", err)
		} else {
			for name := range document.MCP {
				names[name] = struct{}{}
			}
		}
	}

	rows := make([]web.MCPServerRow, 0, len(names))
	for name := range names {
		status := statuses[name]
		state := status.State
		if state == "" {
			state = "stopped"
		}
		rows = append(rows, web.MCPServerRow{
			Name:         name,
			State:        state,
			ToolsCount:   status.Tools,
			Error:        status.Reason,
			RunningForMS: status.RunningForMS,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

// MCPTest implements web.MCPBackend. The definition values are inline for this
// call only; they are never written or logged.
func (b *webBackend) MCPTest(ctx context.Context, def web.MCPDefinition) web.MCPTestResult {
	if err := ctx.Err(); err != nil {
		return web.MCPTestResult{Message: err.Error()}
	}
	count, err := b.rt.TestMCP(core.MCPConfig{
		Command: def.Command,
		Args:    append([]string(nil), def.Args...),
		URL:     def.URL,
		Env:     mcpTestSecrets(def.Env),
		Headers: mcpTestSecrets(def.Headers),
	})
	if err != nil {
		return web.MCPTestResult{Message: err.Error()}
	}
	return web.MCPTestResult{OK: true, ToolCount: count}
}

// MCPRestart implements web.MCPBackend. The runtime restarts one client from
// the loaded definition, not from config.toml.
func (b *webBackend) MCPRestart(ctx context.Context, name string) web.MCPState {
	if err := ctx.Err(); err != nil {
		return web.MCPState{Name: name, Error: err.Error()}
	}
	status, err := b.rt.RestartMCP(name)
	switch {
	case errors.Is(err, lana.ErrUnknownMCP):
		return web.MCPState{Name: name, State: "unavailable", Error: "not configured"}
	case err != nil:
		return web.MCPState{Name: name, State: "unavailable", Error: err.Error()}
	}
	return web.MCPState{Name: status.Name, State: status.State, Error: status.Reason}
}

// mcpTestSecrets wraps inline test values. They are used for the test call and
// are never stored.
func mcpTestSecrets(values map[string]string) map[string]core.Secret {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]core.Secret, len(values))
	for key, value := range values {
		out[key] = core.NewSecret(value)
	}
	return out
}

// ConfigFile implements web.ConfigStore.
func (b *webBackend) ConfigFile(ctx context.Context) (web.ConfigFile, error) {
	if err := ctx.Err(); err != nil {
		return web.ConfigFile{}, err
	}
	path := filepath.Join(b.home, "config.toml")
	file := web.ConfigFile{Path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return web.ConfigFile{}, err
	}
	sum := sha256.Sum256(data)
	file.Raw = data
	file.Version = hex.EncodeToString(sum[:])
	file.Exists = true
	return file, nil
}

// ConfigCheck implements web.ConfigStore.
func (b *webBackend) ConfigCheck(ctx context.Context, candidate []byte) ([]web.ConfigIssue, error) {
	_, err := config.Check(ctx, b.home, candidate)
	if err == nil {
		return nil, nil
	}
	var errs config.Errors
	if !errors.As(err, &errs) {
		return nil, err
	}
	issues := make([]web.ConfigIssue, 0, len(errs))
	for _, item := range errs {
		issues = append(issues, web.ConfigIssue{File: item.File, Key: item.Key, Message: item.Msg})
	}
	return issues, nil
}

// ConfigWrite implements web.ConfigStore.
func (b *webBackend) ConfigWrite(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return webfiles.AtomicWrite(filepath.Join(b.home, "config.toml"), data)
}

// ConfigLoadedVersion implements web.ConfigStore.
func (b *webBackend) ConfigLoadedVersion() string {
	return b.loadedSHA256
}

// SettingsHome implements web.SecretStore, web.AgentFileStore, and
// web.SkillStore.
func (b *webBackend) SettingsHome() string { return b.home }

// AgentNames implements web.AgentFileStore and web.SkillStore.
func (b *webBackend) AgentNames() []string { return b.rt.AgentNames() }

// ToolCatalog implements web.ToolCatalog (API-WEB-026). It maps the runtime
// types to the port types and hides the runtime behind web.ErrUnknownAgent.
func (b *webBackend) ToolCatalog(agent string) ([]web.ToolGroup, error) {
	groups, err := b.rt.ToolCatalog(agent)
	if err != nil {
		if errors.Is(err, lana.ErrUnknownAgent) {
			return nil, web.ErrUnknownAgent
		}
		return nil, err
	}
	out := make([]web.ToolGroup, 0, len(groups))
	for _, group := range groups {
		tools := make([]web.ToolEntry, 0, len(group.Tools))
		for _, entry := range group.Tools {
			tools = append(tools, web.ToolEntry{
				Name:      entry.Name,
				Risk:      entry.Risk,
				AsksFirst: entry.AsksFirst,
				Enabled:   entry.Enabled,
			})
		}
		out = append(out, web.ToolGroup{ID: group.ID, Label: group.Label, Tools: tools})
	}
	return out, nil
}

var secretRefRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ConfigSecretNames implements web.SecretStore. It reads the raw home
// config.toml because config.Load interpolates MCP ${VAR} values, which would
// hide the referenced names. Only names are returned.
func (b *webBackend) ConfigSecretNames(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(b.home, "config.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	names := map[string]struct{}{}
	add := func(name string) {
		if webfiles.ValidateSecretName(name) == nil {
			names[name] = struct{}{}
		}
	}
	if providers, ok := root["providers"].(map[string]any); ok {
		for _, raw := range providers {
			if table, ok := raw.(map[string]any); ok {
				if name, ok := table["api_key_env"].(string); ok {
					add(name)
				}
			}
		}
	}
	if channels, ok := root["channels"].(map[string]any); ok {
		if telegram, ok := channels["telegram"].(map[string]any); ok {
			if name, ok := telegram["token_env"].(string); ok {
				add(name)
			}
		}
	}
	if mcp, ok := root["mcp"].(map[string]any); ok {
		for _, raw := range mcp {
			server, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			for _, field := range []string{"env", "headers"} {
				table, ok := server[field].(map[string]any)
				if !ok {
					continue
				}
				for _, value := range table {
					text, ok := value.(string)
					if !ok {
						continue
					}
					for _, match := range secretRefRE.FindAllStringSubmatch(text, -1) {
						add(match[1])
					}
				}
			}
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// ConfigSettings implements web.ConfigStore.
func (b *webBackend) ConfigSettings(ctx context.Context) (web.ConfigSettings, error) {
	result, err := config.Load(ctx, config.Options{Home: b.home})
	if err != nil {
		return web.ConfigSettings{}, err
	}
	return settingsFromConfig(result.Config), nil
}

// settingsFromConfig builds the read-only view. MCP env and headers become
// "***" so no literal secret can reach the browser.
func settingsFromConfig(cfg core.Config) web.ConfigSettings {
	settings := web.ConfigSettings{
		Providers: make(map[string]web.ConfigProvider, len(cfg.Providers)),
		Agents:    make(map[string]web.ConfigAgent, len(cfg.Agents)),
		Routes:    make([]web.ConfigRoute, 0, len(cfg.Routes)),
		Channels:  make(map[string]map[string]any, len(cfg.Channels)),
		MCP:       make(map[string]web.ConfigMCPServer, len(cfg.MCP)),
		Modules:   make([]web.ConfigModule, 0, len(cfg.Modules)),
	}
	for name, provider := range cfg.Providers {
		settings.Providers[name] = web.ConfigProvider{
			Kind:      provider.Kind,
			BaseURL:   provider.BaseURL,
			APIKeyEnv: provider.APIKeyEnv,
			Headers:   copyStrings(provider.Headers),
			Timeout:   provider.Timeout,
		}
	}
	for name, agent := range cfg.Agents {
		settings.Agents[name] = web.ConfigAgent{
			Provider:      agent.Provider,
			Model:         agent.Model,
			Fallback:      append([]string(nil), agent.Fallback...),
			PromptFile:    agent.PromptFile,
			Workspace:     agent.Workspace,
			Tools:         append([]string(nil), agent.Tools...),
			Subagents:     append([]string(nil), agent.Subagents...),
			MCP:           append([]string(nil), agent.MCP...),
			Skills:        append([]string(nil), agent.Skills...),
			Permission:    agent.Permission,
			MaxSteps:      agent.MaxSteps,
			ContextWindow: agent.ContextWindow,
			Rules: web.ConfigRules{
				Allow: append([]string(nil), agent.Rules.Allow...),
				Deny:  append([]string(nil), agent.Rules.Deny...),
			},
		}
	}
	for _, route := range cfg.Routes {
		settings.Routes = append(settings.Routes, web.ConfigRoute{
			Channel: route.Channel,
			Agent:   route.Agent,
			Match:   copyAnyMap(route.Match),
		})
	}
	for name, channel := range cfg.Channels {
		settings.Channels[name] = channelSettings(channel)
	}
	for name, server := range cfg.MCP {
		settings.MCP[name] = web.ConfigMCPServer{
			Command: server.Command,
			Args:    append([]string(nil), server.Args...),
			URL:     server.URL,
			Env:     maskedKeys(server.Env),
			Headers: maskedKeys(server.Headers),
		}
	}
	for _, module := range cfg.Modules {
		settings.Modules = append(settings.Modules, web.ConfigModule{
			ID:       module.ID,
			Module:   module.Module,
			Disabled: module.Disabled,
			Config:   copyAnyMap(module.Config),
		})
	}
	return settings
}

func copyStrings(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func copyAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func maskedKeys[V any](values map[string]V) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key := range values {
		out[key] = "***"
	}
	return out
}

// channelSettings copies a channel table and hides token-like values. The
// Telegram token_env name and owner_ids stay visible.
func channelSettings(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]any, len(raw))
	for key, value := range raw {
		lower := strings.ToLower(key)
		if key != "token_env" && (strings.Contains(lower, "token") || strings.Contains(lower, "secret")) {
			out[key] = "***"
			continue
		}
		out[key] = value
	}
	return out
}

// Send starts a turn on the daemon root context, or queues the text in the
// session inbox when a turn is already running.
func (b *webBackend) Send(ctx context.Context, id, text string) (web.SendResult, error) {
	info, err := b.resolveSession(ctx, id)
	if err != nil {
		return web.SendResult{}, err
	}
	id = info.ID
	if err := b.ensureLease(ctx, id); err != nil {
		return web.SendResult{}, err
	}
	if b.TurnActive(id) {
		b.waitRunning(id, 2*time.Second)
		if err := b.rt.Send(ctx, id, text); err != nil {
			return web.SendResult{}, turnError(err)
		}
		return web.SendResult{Turn: "queued"}, nil
	}

	// Mark the session first so a message that arrives while the goroutine
	// starts is queued instead of starting a second turn.
	b.mu.Lock()
	if b.active == nil {
		b.active = map[string]struct{}{}
	}
	if _, ok := b.active[id]; ok {
		b.mu.Unlock()
		// Wait for the first turn to reach turn/start so Send lands in its
		// inbox instead of starting a second turn.
		b.waitRunning(id, 2*time.Second)
		if err := b.rt.Send(ctx, id, text); err != nil {
			return web.SendResult{}, turnError(err)
		}
		return web.SendResult{Turn: "queued"}, nil
	}
	b.active[id] = struct{}{}
	root := b.root
	if root == nil {
		root = context.Background()
	}
	beforeSeq := info.LastSeq
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer b.finishTurn(id)
		if _, err := b.rt.Run(root, lana.Request{Prompt: text, SessionID: id}); err != nil {
			slog.Error("web: turn failed", "session", id, "error", err)
		}
	}()
	// The runtime generates the turn id; read it from turn/start so the
	// response carries the id of the turn that the stream reports.
	turnID := b.waitTurnID(id, beforeSeq, done, time.Second)
	return web.SendResult{Turn: "started", TurnID: turnID}, nil
}

// waitTurnID waits briefly for the turn/start event written by the turn.
func (b *webBackend) waitTurnID(id string, afterSeq int64, done <-chan struct{}, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		if turnID := b.turnStartAfter(id, afterSeq); turnID != "" {
			return turnID
		}
		select {
		case <-done:
			return b.turnStartAfter(id, afterSeq)
		default:
		}
		if !time.Now().Before(deadline) {
			return ""
		}
		select {
		case <-done:
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (b *webBackend) turnStartAfter(id string, afterSeq int64) string {
	events, err := b.rt.History(b.rootContext(), id)
	if err != nil {
		return ""
	}
	for _, event := range events {
		if event.SessionID != id || event.Seq <= afterSeq || event.Kind != core.KindTurnStart {
			continue
		}
		if event.TurnID != "" {
			return event.TurnID
		}
		var start core.TurnStart
		if err := json.Unmarshal(event.Payload, &start); err == nil {
			return start.TurnID
		}
	}
	return ""
}

// Cancel stops the running turn and reports whether one was running.
func (b *webBackend) Cancel(ctx context.Context, id string) (bool, error) {
	info, err := b.resolveSession(ctx, id)
	if err != nil {
		return false, err
	}
	id = info.ID
	if err := b.ensureLease(ctx, id); err != nil {
		return false, err
	}
	running := b.TurnActive(id)
	if err := b.rt.Cancel(ctx, id); err != nil {
		return false, turnError(err)
	}
	return running, nil
}

// Compact summarizes the session now.
func (b *webBackend) Compact(ctx context.Context, id string) (web.CompactResult, error) {
	info, err := b.resolveSession(ctx, id)
	if err != nil {
		return web.CompactResult{}, err
	}
	id = info.ID
	if err := b.ensureLease(ctx, id); err != nil {
		return web.CompactResult{}, err
	}
	if b.TurnActive(id) {
		return web.CompactResult{}, web.ErrInvalidState
	}
	result, err := b.rt.Compact(ctx, id)
	if err != nil {
		if errors.Is(err, compaction.ErrTurnActive) || errors.Is(err, compaction.ErrNothingToCompact) {
			return web.CompactResult{}, web.ErrInvalidState
		}
		return web.CompactResult{}, turnError(err)
	}
	return web.CompactResult{TokensBefore: result.TokensBefore, TokensAfter: result.TokensAfter}, nil
}

// Fork branches the session at its last completed turn.
func (b *webBackend) Fork(ctx context.Context, id string) (web.Session, error) {
	info, err := b.resolveSession(ctx, id)
	if err != nil {
		return web.Session{}, err
	}
	id = info.ID
	if err := b.ensureLease(ctx, id); err != nil {
		return web.Session{}, err
	}
	if b.TurnActive(id) {
		return web.Session{}, web.ErrInvalidState
	}
	events, err := b.rt.History(ctx, id)
	if err != nil {
		return web.Session{}, sessionLookupError(err)
	}
	completed := false
	for _, event := range events {
		if event.Kind == core.KindTurnEnd {
			completed = true
		}
	}
	if !completed {
		return web.Session{}, web.ErrInvalidState
	}
	forked, err := b.rt.ForkSession(ctx, id)
	if err != nil {
		return web.Session{}, turnError(err)
	}
	return b.session(ctx, forked)
}

// SetAgent changes the session's agent.
func (b *webBackend) SetAgent(ctx context.Context, id, agent string) error {
	info, err := b.resolveSession(ctx, id)
	if err != nil {
		return err
	}
	id = info.ID
	if err := b.ensureLease(ctx, id); err != nil {
		return err
	}
	if b.TurnActive(id) {
		return web.ErrInvalidState
	}
	currentAgent, err := b.effectiveAgent(ctx, info)
	if err != nil {
		return turnError(err)
	}
	if currentAgent == agent {
		return nil
	}
	if err := b.rt.SetAgent(ctx, id, agent); err != nil {
		if errors.Is(err, lana.ErrUnknownAgent) {
			return web.ErrUnknownAgent
		}
		return turnError(err)
	}
	return nil
}

// SetModel changes the session's model.
func (b *webBackend) SetModel(ctx context.Context, id, model string) error {
	info, err := b.resolveSession(ctx, id)
	if err != nil {
		return err
	}
	id = info.ID
	if err := b.ensureLease(ctx, id); err != nil {
		return err
	}
	if b.TurnActive(id) {
		return web.ErrInvalidState
	}
	currentModel, err := b.sessionModel(ctx, info)
	if err != nil {
		return turnError(err)
	}
	if currentModel == model {
		return nil
	}
	if err := b.rt.SetModel(ctx, id, model); err != nil {
		return modelSelectionError(err)
	}
	return nil
}

// SetPolicy changes the session's effective policy.
func (b *webBackend) SetPolicy(ctx context.Context, id, policy string) error {
	info, err := b.resolveSession(ctx, id)
	if err != nil {
		return err
	}
	id = info.ID
	if err := b.ensureLease(ctx, id); err != nil {
		return err
	}
	if b.TurnActive(id) {
		return web.ErrInvalidState
	}
	policy = strings.TrimSpace(policy)
	currentPolicy, err := b.rt.Policy(ctx, id)
	if err != nil {
		return turnError(err)
	}
	if string(currentPolicy) == policy {
		return nil
	}
	if err := b.rt.SetPolicy(ctx, id, core.Policy(policy)); err != nil {
		if errors.Is(err, permission.ErrInvalidPolicy) {
			return web.ErrInvalidPolicy
		}
		return turnError(err)
	}
	return nil
}

// Subscribe returns the live frames of a turn running in this process.
func (b *webBackend) Subscribe(ctx context.Context, id string) (*core.Subscription[core.LiveFrame], error) {
	return b.rt.SubscribeLive(ctx, id)
}

// TurnActive reports whether a turn is running for the session. The in-process
// marker covers the window before turn/start is written; the registry covers
// turns this process runs; the stored status covers turns started by the
// daemon's other channels.
func (b *webBackend) TurnActive(id string) bool {
	if b.marked(id) {
		return true
	}
	if b.rt.TurnActive(id) {
		return true
	}
	return b.sessionRunning(id)
}

// Holder reports which process holds the session lease without taking it.
func (b *webBackend) Holder(ctx context.Context, id string) (web.Holder, error) {
	if b.leases.holds(id) {
		return web.Holder{State: web.HolderSelf}, nil
	}
	pid, held, err := b.rt.LeaseHolder(ctx, id)
	if err != nil {
		return web.Holder{}, err
	}
	if held {
		return web.Holder{State: web.HolderOther, PID: pid}, nil
	}
	return web.Holder{State: web.HolderNone}, nil
}

// waitRunning waits briefly for a turn to reach turn/start.
func (b *webBackend) waitRunning(id string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b.sessionRunning(id) {
			return
		}
		if !b.marked(id) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (b *webBackend) marked(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.active[id]
	return ok
}

func (b *webBackend) sessionRunning(id string) bool {
	info, err := b.rt.SessionInfo(b.rootContext(), id)
	return err == nil && info.Status == "running"
}

func (b *webBackend) finishTurn(id string) {
	b.mu.Lock()
	delete(b.active, id)
	b.mu.Unlock()
}

// resolveSession accepts a full session ID or a unique prefix.
func (b *webBackend) resolveSession(ctx context.Context, id string) (core.SessionInfo, error) {
	info, err := b.rt.SessionInfo(ctx, id)
	if err != nil {
		return core.SessionInfo{}, sessionLookupError(err)
	}
	return info, nil
}

// ensureLease takes the daemon lease and maps a missing session to the port's
// not-found error.
func (b *webBackend) ensureLease(ctx context.Context, id string) error {
	err := b.leases.ensure(ctx, id)
	if err == nil {
		return nil
	}
	var held *core.LeaseHeldError
	if errors.As(err, &held) {
		return err
	}
	return sessionLookupError(err)
}

// turnError maps runtime errors that the contract names.
func turnError(err error) error {
	switch {
	case errors.Is(err, core.ErrNotFound), errors.Is(err, lana.ErrSessionNotFound):
		return web.ErrSessionNotFound
	case errors.Is(err, lana.ErrUnknownAgent):
		return web.ErrUnknownAgent
	case errors.Is(err, lana.ErrSessionBusy):
		return web.ErrInvalidState
	default:
		return err
	}
}

// modelSelectionError maps the runtime's model errors to the web port. A team
// refuses a model change with invalid_request (REQ-TEAM-022); the error wraps
// ErrInvalidModel, so it must be checked first.
func modelSelectionError(err error) error {
	switch {
	case errors.Is(err, lana.ErrTeamModel):
		return web.ErrTeamModel
	case errors.Is(err, lana.ErrUnknownProvider), errors.Is(err, lana.ErrEmptyModel):
		return web.ErrInvalidModel
	default:
		return turnError(err)
	}
}

// session adds the model, policy, team, and turn state the detail view needs.
func (b *webBackend) session(ctx context.Context, info core.SessionInfo) (web.Session, error) {
	model, err := b.sessionModel(ctx, info)
	if err != nil {
		return web.Session{}, err
	}
	policy, err := b.rt.Policy(ctx, info.ID)
	if err != nil {
		return web.Session{}, err
	}
	agentName, err := b.effectiveAgent(ctx, info)
	if err != nil {
		return web.Session{}, err
	}
	return web.Session{
		SessionInfo:  info,
		Model:        model,
		Policy:       string(policy),
		Team:         b.teamForAgent(agentName),
		TurnRunning:  info.Status == "running",
		InheritedSeq: info.InheritedSeq,
	}, nil
}

// effectiveAgent returns the agent selected by the last model/selection event,
// or the session's creation agent.
func (b *webBackend) effectiveAgent(ctx context.Context, info core.SessionInfo) (string, error) {
	agentName := info.Agent
	events, err := b.rt.History(ctx, info.ID)
	if err != nil {
		return "", err
	}
	for _, event := range events {
		if event.Kind != core.KindModelSelection {
			continue
		}
		var selection core.ModelSelection
		if err := json.Unmarshal(event.Payload, &selection); err != nil {
			return "", err
		}
		if selection.Agent != "" {
			agentName = selection.Agent
		}
	}
	return agentName, nil
}

// sessionModel returns the last model/selection in the history, or the
// agent's configured model when the session did not select one.
func (b *webBackend) sessionModel(ctx context.Context, info core.SessionInfo) (string, error) {
	model := b.rt.AgentModel(info.Agent)
	events, err := b.rt.History(ctx, info.ID)
	if err != nil {
		return "", err
	}
	for _, event := range events {
		if event.Kind != core.KindModelSelection {
			continue
		}
		var selection core.ModelSelection
		if err := json.Unmarshal(event.Payload, &selection); err != nil {
			return "", err
		}
		model = selection.Provider + ":" + selection.Model
	}
	return model, nil
}

// sessionLookupError maps the prefix resolution errors to the port's not-found
// error, as the contract treats an unknown or ambiguous id the same way.
func sessionLookupError(err error) error {
	switch {
	case errors.Is(err, core.ErrNotFound),
		errors.Is(err, core.ErrAmbiguousPrefix),
		errors.Is(err, core.ErrPrefixTooShort):
		return web.ErrSessionNotFound
	default:
		return err
	}
}
