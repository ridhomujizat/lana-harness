// Package lana is the embeddable lana-harness runtime.
package lana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ridhomujizat/lana-harness/agent"
	_ "github.com/ridhomujizat/lana-harness/agent/compaction"
	_ "github.com/ridhomujizat/lana-harness/agent/memcapture"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/pattern"
	"github.com/ridhomujizat/lana-harness/internal/question"
	_ "github.com/ridhomujizat/lana-harness/mcp"
	_ "github.com/ridhomujizat/lana-harness/providers/openai"
	_ "github.com/ridhomujizat/lana-harness/store"
	"github.com/ridhomujizat/lana-harness/store/memstore"
	_ "github.com/ridhomujizat/lana-harness/tools"
	_ "github.com/ridhomujizat/lana-harness/tools/permission"
)

// ApprovalMode is the runtime's initial approval option.
type ApprovalMode int

const (
	PolicyNever ApprovalMode = iota
	PolicyTrust
)

// Options is the input to New.
type Options struct {
	Home      string
	Layers    []config.Layer
	Profile   string
	Tools     []core.Tool
	Approval  ApprovalMode
	Store     core.SessionStore
	Providers map[string]core.Provider
}

// Option is an additional option for New.
type Option func(*runtimeOptions)

type runtimeOptions struct {
	approve         ApproveFunc
	approveChannels map[string]bool
	ask             AskFunc
	askChannels     map[string]bool
}

// ApproveFunc answers approvals for the library channel.
type ApproveFunc = func(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome

// ApproveWith installs an approval responder.
func ApproveWith(fn ApproveFunc) Option {
	return func(o *runtimeOptions) { o.approve = fn }
}

// ApproveChannels sets the session channels the ApproveWith responder answers.
// The default is "library" and "cli"; other channels go to the next answerer.
func ApproveChannels(channels ...string) Option {
	return func(o *runtimeOptions) {
		o.approveChannels = map[string]bool{}
		for _, channel := range channels {
			o.approveChannels[channel] = true
		}
	}
}

// AskFunc answers questions for a session channel.
type AskFunc = func(ctx context.Context, req core.QuestionRequest) core.QuestionOutcome

// AskWith installs a question responder.
func AskWith(fn AskFunc) Option {
	return func(o *runtimeOptions) { o.ask = fn }
}

// AskChannels sets the session channels the AskWith responder answers.
// The default is "library" and "cli"; other channels go to the next answerer.
func AskChannels(channels ...string) Option {
	return func(o *runtimeOptions) {
		o.askChannels = map[string]bool{}
		for _, channel := range channels {
			o.askChannels[channel] = true
		}
	}
}

var (
	ErrEmptyPrompt     = errors.New("prompt is empty")
	ErrNoRoute         = errors.New("library route not found")
	ErrUnknownAgent    = agent.ErrUnknownAgent
	ErrSessionBusy     = agent.ErrSessionBusy
	ErrSessionNotFound = agent.ErrSessionNotFound
	ErrClosed          = agent.ErrClosed
	ErrUnknownProvider = agent.ErrUnknownProvider
	ErrEmptyModel      = agent.ErrEmptyModel
	ErrInvalidModel    = agent.ErrInvalidModel
	ErrTeamModel       = agent.ErrTeamModel
	ErrUnknownMCP      = errors.New("mcp server is not configured")
	// ErrDraining refuses a new turn while the daemon shuts down
	// (REQ-TFB-002, API-TFB-008). Its text is the user-facing message.
	ErrDraining = errors.New("lana is restarting. Try again in a moment.") //nolint:staticcheck // ST1005: the contract fixes this user-facing text.
	// ErrTurnRunning refuses Continue while a turn runs (API-TFB-003).
	ErrTurnRunning = errors.New("a turn is running")
	// ErrNothingToContinue refuses Continue when the last turn ended normally
	// (API-TFB-003).
	ErrNothingToContinue = errors.New("the last turn ended normally")
)

// ProviderError is an alias for core.ProviderError.
type ProviderError = core.ProviderError

// ToolEntry is one tool row of API-WEB-026.
type ToolEntry struct {
	Name      string `json:"name"`
	Risk      string `json:"risk"`
	AsksFirst bool   `json:"asks_first"`
	Enabled   bool   `json:"enabled"`
	Source    string `json:"source"`
}

// ToolGroup is one group of the tool catalog of API-WEB-026.
type ToolGroup struct {
	ID    string      `json:"id"`
	Label string      `json:"label"`
	Tools []ToolEntry `json:"tools"`
}

// toolCatalogGroupOrder is the built-in group order of API-WEB-026.
var toolCatalogGroupOrder = []struct {
	ID    string
	Label string
	Names []string
}{
	{ID: "files", Label: "Files", Names: []string{"read_file", "write_file", "edit_file", "glob", "grep"}},
	{ID: "shell", Label: "Shell", Names: []string{"bash"}},
	{ID: "web", Label: "Web", Names: []string{"web_fetch"}},
	{ID: "memory", Label: "Memory", Names: []string{"memory_read", "memory_write"}},
	{ID: "skills", Label: "Skills", Names: []string{"skill_load", "skill_write"}},
	{ID: "mcp-admin", Label: "MCP", Names: []string{"mcp_manage"}},
	{ID: "chat", Label: "Chat", Names: []string{"task", "task_continue", "ask_user"}},
}

// Runtime is the library runtime.
type Runtime struct {
	ctx      context.Context
	cancel   context.CancelFunc
	coreCtx  *core.Context
	result   *config.Result
	registry *agent.Registry
	store    core.SessionStore
	closed   atomic.Bool
	// cfgMu guards the live-applied sections of result.Config: Agents, Teams,
	// and MCP. Providers, routes, channels, web, and secrets stay fixed.
	cfgMu sync.RWMutex
	// draining refuses new turns while the daemon waits for running turns
	// (REQ-TFB-002).
	draining atomic.Bool
	closeMu  sync.Mutex
	closeErr error
	approve  ApproveFunc
	home     string
}

func init() {
	core.RegisterModule("memstore", func() core.Module { return &memstoreModule{} })
	core.RegisterModule("provided_store", func() core.Module { return &providedStoreModule{} })
}

type memstoreModule struct{}

func (m *memstoreModule) ID() string                  { return "memstore" }
func (m *memstoreModule) Requires() []core.ServiceKey { return nil }
func (m *memstoreModule) Provides() []core.ServiceKey { return []core.ServiceKey{core.ServiceSessions} }
func (m *memstoreModule) Install(b *core.Builder, cfg core.ModuleConfig) error {
	return b.Provide(core.ServiceSessions, memstore.New())
}
func (m *memstoreModule) Stop(context.Context) error { return nil }

type providedStoreModule struct{}

func (m *providedStoreModule) ID() string                  { return "provided_store" }
func (m *providedStoreModule) Requires() []core.ServiceKey { return nil }
func (m *providedStoreModule) Provides() []core.ServiceKey {
	return []core.ServiceKey{core.ServiceSessions}
}
func (m *providedStoreModule) Install(b *core.Builder, cfg core.ModuleConfig) error {
	raw, ok := cfg.Values["_store"]
	if !ok {
		return fmt.Errorf("provided_store without _store")
	}
	store, ok := raw.(core.SessionStore)
	if !ok || store == nil {
		return fmt.Errorf("_store is not a SessionStore")
	}
	return b.Provide(core.ServiceSessions, store)
}
func (m *providedStoreModule) Stop(context.Context) error { return nil }

// migrateUser runs the API-HOME-003 migration for a runtime open.
func migrateUser(home string, cfg core.Config) {
	if home == "" {
		return
	}
	candidates := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		candidates = append(candidates, name)
	}
	sort.Strings(candidates)
	defaultAgent := ""
	for _, route := range cfg.Routes {
		if route.Channel == "cli" {
			defaultAgent = route.Agent
			break
		}
	}
	migration, err := agenthome.MigrateUser(home, candidates, defaultAgent)
	if err != nil {
		slog.Warn("cannot migrate user file", "error", err)
		return
	}
	if migration.Source != "" {
		slog.Info("migrated user file from agent " + migration.Source)
		return
	}
	if warning := migration.Warning(home); warning != "" {
		slog.Warn(warning)
	}
}

// New builds the library runtime.
func New(ctx context.Context, opts Options, options ...Option) (*Runtime, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var runtimeOpts runtimeOptions
	for _, option := range options {
		option(&runtimeOpts)
	}
	result, err := config.Load(ctx, config.Options{Home: opts.Home, Layers: opts.Layers, Profile: opts.Profile})
	if err != nil {
		return nil, err
	}
	migrateUser(opts.Home, result.Config)
	setCustomTools(result.Config.Modules, opts.Tools)
	setProviders(result.Config.Modules, opts.Providers)
	if opts.Store != nil {
		if err := replaceModule(result.Config.Modules, "store", "provided_store", map[string]any{"_store": opts.Store}); err != nil {
			return nil, err
		}
	} else if opts.Home == "" {
		if err := replaceModule(result.Config.Modules, "store", "memstore", nil); err != nil {
			return nil, err
		}
	}
	coreCtx, err := core.Boot(ctx, core.BootInput{Config: result.Config, Secrets: result.Secrets, Profile: result.Profile, Home: opts.Home})
	if err != nil {
		return nil, err
	}
	registry, _ := coreCtx.Agents.(*agent.Registry)
	if registry == nil {
		_ = coreCtx.Shutdown(ctx)
		return nil, fmt.Errorf("service agents not available")
	}
	questionService := question.New(coreCtx.Sessions, coreCtx.Hooks)
	coreCtx.Questions = questionService
	registry.SetQuestions(questionService)
	if opts.Approval == PolicyTrust {
		coreCtx.Hooks.ToolsPreExecute.Use(func(context.Context, core.ToolsPreExecuteInput, core.Next[core.ToolsPreExecuteInput, core.ToolDecision]) (core.ToolDecision, error) {
			return core.ToolDecision{Allowed: true}, nil
		}, core.WithPrepend())
	}
	if runtimeOpts.approve != nil {
		fn := runtimeOpts.approve
		channels := runtimeOpts.approveChannels
		if channels == nil {
			channels = map[string]bool{"library": true, "cli": true}
		}
		coreCtx.Hooks.ApprovalRequest.Use(func(ctx context.Context, req core.ApprovalRequest, next core.Next[core.ApprovalRequest, core.ApprovalOutcome]) (outcome core.ApprovalOutcome, err error) {
			info, infoErr := coreCtx.Sessions.Info(ctx, req.SessionID)
			if infoErr != nil || !channels[info.Channel] {
				return next(ctx, req)
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					outcome = core.OutcomeUnavailable
					err = nil
				}
			}()
			outcome = fn(ctx, req)
			if !validApprovalOutcome(outcome) {
				outcome = core.OutcomeUnavailable
			}
			return outcome, nil
		})
	}
	if runtimeOpts.ask != nil {
		fn := runtimeOpts.ask
		channels := runtimeOpts.askChannels
		if channels == nil {
			channels = map[string]bool{"library": true, "cli": true}
		}
		coreCtx.Hooks.QuestionAsk.Use(func(ctx context.Context, req core.QuestionRequest, next core.Next[core.QuestionRequest, core.QuestionOutcome]) (outcome core.QuestionOutcome, err error) {
			info, infoErr := coreCtx.Sessions.Info(ctx, req.SessionID)
			if infoErr != nil || !channels[info.Channel] {
				return next(ctx, req)
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					outcome = questionNoResponder()
					err = nil
				}
			}()
			outcome = fn(ctx, req)
			if !validQuestionOutcome(outcome) {
				outcome = questionNoResponder()
			}
			return outcome, nil
		})
	}
	root, cancel := context.WithCancel(context.Background())
	rt := &Runtime{
		ctx: root, cancel: cancel, coreCtx: coreCtx, result: result,
		registry: registry, store: coreCtx.Sessions, approve: runtimeOpts.approve, home: opts.Home,
	}
	return rt, nil
}

// ApplyConfig replaces the live sections of the loaded config: agents, teams,
// and MCP. Providers, routes, channels, web, and secrets are not applied
// (REQ-TFB-006).
func (rt *Runtime) ApplyConfig(cfg core.Config) {
	agents := make(map[string]core.AgentConfig, len(cfg.Agents))
	for name, agentCfg := range cfg.Agents {
		agents[name] = agentCfg
	}
	teams := make(map[string]core.TeamConfig, len(cfg.Teams))
	for name, teamCfg := range cfg.Teams {
		teams[name] = teamCfg
	}
	mcpCfg := make(map[string]core.MCPConfig, len(cfg.MCP))
	for name, serverCfg := range cfg.MCP {
		mcpCfg[name] = serverCfg
	}
	rt.cfgMu.Lock()
	rt.result.Config.Agents = agents
	rt.result.Config.Teams = teams
	rt.result.Config.MCP = mcpCfg
	rt.cfgMu.Unlock()
	if rt.registry != nil {
		rt.registry.ApplyConfig(cfg)
	}
}

// NonLiveConfig returns the loaded config with the live-applied sections
// cleared. The web backend compares a save against it to tell a live change
// from a restart-only one (REQ-TFB-006).
func (rt *Runtime) NonLiveConfig() core.Config {
	rt.cfgMu.RLock()
	defer rt.cfgMu.RUnlock()
	cfg := rt.result.Config
	cfg.Agents = nil
	cfg.Teams = nil
	cfg.MCP = nil
	return cfg
}

// agents returns the loaded agent map. The map itself is never changed in
// place, so the snapshot stays valid after the lock is released.
func (rt *Runtime) agents() map[string]core.AgentConfig {
	rt.cfgMu.RLock()
	defer rt.cfgMu.RUnlock()
	return rt.result.Config.Agents
}

// teams returns the loaded team map.
func (rt *Runtime) teams() map[string]core.TeamConfig {
	rt.cfgMu.RLock()
	defer rt.cfgMu.RUnlock()
	return rt.result.Config.Teams
}

// routes returns the loaded route list.
func (rt *Runtime) routes() []core.RouteConfig {
	rt.cfgMu.RLock()
	defer rt.cfgMu.RUnlock()
	return rt.result.Config.Routes
}

// mcpConfig returns the loaded MCP server map.
func (rt *Runtime) mcpConfig() map[string]core.MCPConfig {
	rt.cfgMu.RLock()
	defer rt.cfgMu.RUnlock()
	return rt.result.Config.MCP
}

func replaceModule(rows []core.ModuleRow, id, module string, values map[string]any) error {
	for i := range rows {
		if rows[i].ID != id {
			continue
		}
		rows[i].Module = module
		if values != nil {
			if rows[i].Config == nil {
				rows[i].Config = map[string]any{}
			}
			for key, value := range values {
				rows[i].Config[key] = value
			}
		}
		return nil
	}
	return fmt.Errorf("module row '%s' not found", id)
}

func setProviders(rows []core.ModuleRow, providers map[string]core.Provider) {
	if len(providers) == 0 {
		return
	}
	for i := range rows {
		if rows[i].ID != "llm" {
			continue
		}
		if rows[i].Config == nil {
			rows[i].Config = map[string]any{}
		}
		rows[i].Config["_providers"] = providers
	}
}

func setCustomTools(rows []core.ModuleRow, custom []core.Tool) {
	if len(custom) == 0 {
		return
	}
	for i := range rows {
		if rows[i].ID != "tools" {
			continue
		}
		if rows[i].Config == nil {
			rows[i].Config = map[string]any{}
		}
		rows[i].Config["_custom_tools"] = custom
	}
}

func (rt *Runtime) closedError() error {
	if rt.closed.Load() {
		return ErrClosed
	}
	return nil
}

// SetDraining refuses new turns from now on. The daemon sets it at the start of
// a drain and clears it again when a pending restart is cancelled
// (REQ-TFB-002, API-TFB-008).
func (rt *Runtime) SetDraining(draining bool) {
	rt.draining.Store(draining)
}

// Draining reports whether the runtime refuses new turns.
func (rt *Runtime) Draining() bool {
	return rt.draining.Load()
}

// drainError returns ErrDraining while the runtime drains.
func (rt *Runtime) drainError() error {
	if rt.draining.Load() {
		return ErrDraining
	}
	return nil
}

// RunningTurnCount reports the number of turns owned by this process.
func (rt *Runtime) RunningTurnCount() int {
	if rt.registry == nil {
		return 0
	}
	return rt.registry.RunningTurnCount()
}

// InterruptAll cancels every turn running in this process with cause and
// returns how many turns it interrupted (REQ-TFB-002). The cause selects the
// turn/end message in agent/turn.go (API-TFB-001).
func (rt *Runtime) InterruptAll(cause error) int {
	if rt.registry == nil {
		return 0
	}
	return rt.registry.InterruptAll(cause)
}

func (rt *Runtime) routeAgent(agentName string) (string, error) {
	if agentName != "" {
		if rt.IsTeam(agentName) {
			return agentName, nil
		}
		if _, ok := rt.agents()[agentName]; !ok {
			return "", fmt.Errorf("%w: %s", ErrUnknownAgent, agentName)
		}
		return agentName, nil
	}
	for _, route := range rt.routes() {
		if route.Channel == "library" {
			return route.Agent, nil
		}
	}
	return "", ErrNoRoute
}

func (rt *Runtime) openSession(ctx context.Context, req Request, agentName string) (core.SessionInfo, error) {
	if req.SessionID != "" {
		matches, err := rt.store.FindPrefix(ctx, req.SessionID)
		if err != nil {
			return core.SessionInfo{}, err
		}
		info, err := core.ResolvePrefix(matches, req.SessionID)
		if err != nil {
			return core.SessionInfo{}, err
		}
		return info, nil
	}
	workspace, err := os.Getwd()
	if err != nil {
		return core.SessionInfo{}, err
	}
	if agentCfg, ok := rt.agentConfig(agentName); ok && agentCfg.Workspace != "" {
		workspace = agentCfg.Workspace
	}
	workspace, err = normalizeWorkspace(workspace)
	if err != nil {
		return core.SessionInfo{}, err
	}
	return rt.store.Create(ctx, core.NewSession{Agent: agentName, Channel: "library", Workspace: workspace})
}

func normalizeWorkspace(path string) (string, error) {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// Request is the input to Run or Stream.
type Request struct {
	Agent     string
	Prompt    string
	SessionID string
}

// Usage is the token count of one turn.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Result is the outcome of one turn.
type Result struct {
	Text       string
	SessionID  string
	StopReason string
	Usage      Usage
}

const (
	StopEndTurn   = "end_turn"
	StopMaxSteps  = "max_steps"
	StopCancelled = "cancelled"
	StopError     = "error"
)

// Run executes one turn.
func (rt *Runtime) Run(ctx context.Context, req Request) (Result, error) {
	if err := rt.closedError(); err != nil {
		return Result{}, err
	}
	if err := rt.drainError(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return Result{}, ErrEmptyPrompt
	}
	agentName, err := rt.agentFor(ctx, req)
	if err != nil {
		return Result{}, err
	}
	info, err := rt.openSession(ctx, req, agentName)
	if err != nil {
		return Result{}, err
	}
	turnID, err := rt.registry.Start(ctx, info.ID, req.Prompt)
	if err != nil {
		return Result{}, err
	}
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = rt.registry.Cancel(context.Background(), info.ID)
		case <-stopWatch:
		}
	}()
	turnResult, err := rt.registry.Wait(context.Background(), turnID)
	close(stopWatch)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Text: turnResult.Text, SessionID: info.ID, StopReason: turnResult.StopReason,
		Usage: Usage{InputTokens: int(turnResult.Usage.InputTokens), OutputTokens: int(turnResult.Usage.OutputTokens)},
	}
	if turnResult.Err != nil {
		return result, turnResult.Err
	}
	return result, nil
}

// CanContinue reports why a continue turn may not start: ErrDraining while the
// daemon drains, ErrTurnRunning while a turn runs, or ErrNothingToContinue when
// the session's last turn ended normally (API-TFB-003, REQ-TFB-005).
func (rt *Runtime) CanContinue(ctx context.Context, sessionID string) error {
	if err := rt.closedError(); err != nil {
		return err
	}
	if err := rt.drainError(); err != nil {
		return err
	}
	info, err := rt.SessionInfo(ctx, sessionID)
	if err != nil {
		return err
	}
	if rt.TurnActive(info.ID) {
		return ErrTurnRunning
	}
	end, _, ok, err := rt.lastTurn(ctx, info.ID)
	if err != nil {
		return err
	}
	if !ok || end.Reason == StopEndTurn {
		return ErrNothingToContinue
	}
	return nil
}

// Continue starts a turn that resumes the session's last turn. The new turn's
// user message carries continue=true and continue_reason, and its text asks the
// model to carry on from where the turn stopped (API-TFB-003, API-TFB-004,
// REQ-TFB-005).
func (rt *Runtime) Continue(ctx context.Context, sessionID string) (Result, error) {
	if err := rt.CanContinue(ctx, sessionID); err != nil {
		return Result{}, err
	}
	info, err := rt.SessionInfo(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	end, steps, _, err := rt.lastTurn(ctx, info.ID)
	if err != nil {
		return Result{}, err
	}
	text := "Continue the previous task from where it stopped. It ended because: " + continueReasonText(end, steps)
	turnID, err := rt.registry.StartContinue(ctx, info.ID, text, end.Reason)
	if err != nil {
		return Result{}, err
	}
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = rt.registry.Cancel(context.Background(), info.ID)
		case <-stopWatch:
		}
	}()
	turnResult, err := rt.registry.Wait(context.Background(), turnID)
	close(stopWatch)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Text: turnResult.Text, SessionID: info.ID, StopReason: turnResult.StopReason,
		Usage: Usage{InputTokens: int(turnResult.Usage.InputTokens), OutputTokens: int(turnResult.Usage.OutputTokens)},
	}
	if turnResult.Err != nil {
		return result, turnResult.Err
	}
	return result, nil
}

// lastTurn returns the session's last turn/end, the number of steps that turn
// ran, and whether a turn/end exists (API-TFB-003).
func (rt *Runtime) lastTurn(ctx context.Context, sessionID string) (core.TurnEnd, int, bool, error) {
	events, err := rt.store.ChainEvents(ctx, sessionID)
	if err != nil {
		return core.TurnEnd{}, 0, false, err
	}
	var end core.TurnEnd
	turnID := ""
	found := false
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != core.KindTurnEnd {
			continue
		}
		if err := json.Unmarshal(events[i].Payload, &end); err != nil {
			return core.TurnEnd{}, 0, false, err
		}
		turnID = events[i].TurnID
		if turnID == "" {
			turnID = end.TurnID
		}
		found = true
		break
	}
	if !found {
		return core.TurnEnd{}, 0, false, nil
	}
	steps := 0
	for _, event := range events {
		if event.Kind == core.KindStepStart && event.TurnID == turnID {
			steps++
		}
	}
	return end, steps, true, nil
}

// continueReasonText renders the plain reason line of a continue request
// (REQ-TFB-004, REQ-TFB-005).
func continueReasonText(end core.TurnEnd, steps int) string {
	switch end.Reason {
	case StopCancelled:
		return "Stopped by you."
	case "interrupted":
		return "lana restarted while working."
	case StopError:
		message := ""
		if end.Error != nil {
			message = end.Error.Message
		}
		return "Stopped by an error: " + message
	case StopMaxSteps:
		return fmt.Sprintf("Stopped after the step limit (%d steps).", steps)
	default:
		return ""
	}
}

// Event is an event from Stream.
type Event interface{ isEvent() }

type Session struct {
	SessionID string
	Agent     string
	Model     string
}
type TextDelta struct{ Text string }
type ToolCall struct {
	CallID string
	Name   string
	Input  json.RawMessage
}
type ToolResult struct {
	CallID string
	Status string
	Output string
}
type Done struct{ Result Result }

func (Session) isEvent()    {}
func (TextDelta) isEvent()  {}
func (ToolCall) isEvent()   {}
func (ToolResult) isEvent() {}
func (Done) isEvent()       {}

// Stream yields the turn's events as an iterator.
func (rt *Runtime) Stream(ctx context.Context, req Request) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		if err := rt.closedError(); err != nil {
			yield(nil, err)
			return
		}
		if err := rt.drainError(); err != nil {
			yield(nil, err)
			return
		}
		if strings.TrimSpace(req.Prompt) == "" {
			yield(nil, ErrEmptyPrompt)
			return
		}
		agentName, err := rt.agentFor(ctx, req)
		if err != nil {
			yield(nil, err)
			return
		}
		info, err := rt.openSession(ctx, req, agentName)
		if err != nil {
			yield(nil, err)
			return
		}
		turnID, err := rt.registry.Start(ctx, info.ID, req.Prompt)
		if err != nil {
			yield(nil, err)
			return
		}
		stopWatch := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = rt.registry.Cancel(context.Background(), info.ID)
			case <-stopWatch:
			}
		}()
		turnResult, err := rt.registry.Wait(context.Background(), turnID)
		close(stopWatch)
		if err != nil {
			yield(nil, err)
			return
		}
		model := rt.AgentModel(agentName)
		if !yield(Session{SessionID: info.ID, Agent: agentName, Model: model}, nil) {
			return
		}
		events, err := rt.store.ChainEvents(context.Background(), info.ID)
		if err != nil {
			yield(nil, err)
			return
		}
		for _, event := range events {
			if event.TurnID != turnID {
				continue
			}
			switch event.Kind {
			case core.KindAssistantMessage:
				var payload core.AssistantMessage
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					yield(nil, err)
					return
				}
				for _, block := range payload.Content {
					if block.Kind == core.BlockText && block.Text != "" {
						if !yield(TextDelta{Text: block.Text}, nil) {
							return
						}
					}
				}
			case core.KindToolCall:
				var payload core.ToolCallPayload
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					yield(nil, err)
					return
				}
				if !yield(ToolCall{CallID: payload.CallID, Name: payload.Name, Input: payload.Input}, nil) {
					return
				}
			case core.KindToolResult:
				var payload core.ToolResultPayload
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					yield(nil, err)
					return
				}
				if !yield(ToolResult{CallID: payload.CallID, Status: string(payload.Status), Output: payload.Output}, nil) {
					return
				}
			}
		}
		result := Result{
			Text: turnResult.Text, SessionID: info.ID, StopReason: turnResult.StopReason,
			Usage: Usage{InputTokens: int(turnResult.Usage.InputTokens), OutputTokens: int(turnResult.Usage.OutputTokens)},
		}
		if !yield(Done{Result: result}, nil) {
			return
		}
		if turnResult.Err != nil {
			yield(nil, turnResult.Err)
		}
	}
}

// Close shuts down the runtime.
func (rt *Runtime) Close() error {
	rt.closeMu.Lock()
	defer rt.closeMu.Unlock()
	if rt.closed.Load() {
		return rt.closeErr
	}
	rt.closed.Store(true)
	rt.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.registry.Close(ctx); err != nil {
		rt.closeErr = err
	}
	if err := rt.coreCtx.Shutdown(context.Background()); err != nil && rt.closeErr == nil {
		rt.closeErr = err
	}
	return rt.closeErr
}

// CreateSession creates a cli-channel session for the current working directory.
func (rt *Runtime) CreateSession(ctx context.Context, agentName string) (core.SessionInfo, error) {
	if err := rt.closedError(); err != nil {
		return core.SessionInfo{}, err
	}
	if agentName == "" {
		agentName = rt.channelAgent("cli")
	}
	if _, err := rt.routeAgent(agentName); err != nil {
		return core.SessionInfo{}, err
	}
	workspace, err := normalizeWorkspace(".")
	if err != nil {
		return core.SessionInfo{}, err
	}
	return rt.store.Create(ctx, core.NewSession{Agent: agentName, Channel: "cli", Address: workspace, Workspace: workspace})
}

// channelAgent returns the agent of the first route for the channel, or "".
func (rt *Runtime) channelAgent(channel string) string {
	for _, route := range rt.routes() {
		if route.Channel == channel {
			return route.Agent
		}
	}
	return ""
}

// AgentNames returns the registered agent names, sorted.
func (rt *Runtime) AgentNames() []string {
	agents := rt.agents()
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TeamNames returns the registered team names, sorted.
func (rt *Runtime) TeamNames() []string {
	teams := rt.teams()
	names := make([]string, 0, len(teams))
	for name := range teams {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsTeam reports whether name is a registered team.
func (rt *Runtime) IsTeam(name string) bool {
	_, ok := rt.teams()[name]
	return ok
}

// Team returns the [teams.<name>] table. It is the public lookup the web
// backend uses to fill team information (API-TEAM-005, API-TEAM-006).
func (rt *Runtime) Team(name string) (core.TeamConfig, bool) {
	team, ok := rt.teams()[name]
	return team, ok
}

// teamLead returns the lead agent config of a team.
func (rt *Runtime) teamLead(name string) (core.AgentConfig, bool) {
	team, ok := rt.teams()[name]
	if !ok {
		return core.AgentConfig{}, false
	}
	lead, ok := rt.agents()[team.Lead]
	return lead, ok
}

// agentConfig returns the config of an agent, or of the lead for a team.
func (rt *Runtime) agentConfig(name string) (core.AgentConfig, bool) {
	if agentCfg, ok := rt.agents()[name]; ok {
		return agentCfg, true
	}
	return rt.teamLead(name)
}

// AgentModel returns the agent's "provider:model".
func (rt *Runtime) AgentModel(agentName string) string {
	if agentCfg, ok := rt.agentConfig(agentName); ok {
		return agentCfg.Provider + ":" + agentCfg.Model
	}
	return ""
}

// AgentPolicy returns the agent's initial policy; for a team it is the lead's.
func (rt *Runtime) AgentPolicy(agentName string) string {
	if agentCfg, ok := rt.agentConfig(agentName); ok {
		return string(agentCfg.Permission)
	}
	return ""
}

// MCPStatus returns the MCP server status of an agent.
func (rt *Runtime) MCPStatus(agentName string) []core.MCPServerStatus {
	agentCfg, ok := rt.agents()[agentName]
	if !ok || len(agentCfg.MCP) == 0 {
		return nil
	}
	service, ok := rt.coreCtx.Lookup(core.ServiceMCP)
	if !ok {
		return nil
	}
	manager, ok := service.(core.MCPManager)
	if !ok {
		return nil
	}
	return manager.Status(agentCfg.MCP)
}

// ToolCatalog returns the tools grouped for API-WEB-026. It reads the loaded
// config and the tool registry; it never starts an MCP server. The built-in
// groups always come first in contract order, then one group per configured MCP
// server. An unknown agent (or team) returns ErrUnknownAgent. It is additive
// (G-29).
func (rt *Runtime) ToolCatalog(agentName string) ([]ToolGroup, error) {
	if err := rt.closedError(); err != nil {
		return nil, err
	}
	agentCfg, ok := rt.agentConfig(agentName)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownAgent, agentName)
	}
	disabled := make(map[string]bool, len(agentCfg.DisabledTools))
	for _, name := range agentCfg.DisabledTools {
		if name != "" {
			disabled[name] = true
		}
	}

	registry := rt.coreCtx.Tools
	mcpCfg := rt.mcpConfig()
	// The catalog describes the root turn, so it builds the same auto-offer
	// state as agent/turn.go. A team runs as its lead: the lead's skills and
	// tool list are the ones the turn resolves.
	inTeam := rt.IsTeam(agentName)
	skillAgent := agentName
	if inTeam {
		if team, ok := rt.Team(agentName); ok {
			skillAgent = team.Lead
		}
	}
	visibleSkills := false
	if rt.home != "" {
		skills, _ := agenthome.VisibleSkills(rt.home, skillAgent, agentCfg.Skills)
		visibleSkills = len(skills) > 0
	}
	_, mcpManageRegistered := registry.Get("mcp_manage")
	autoOffered := agent.AutoOfferedTools(agent.AutoOfferState{
		Root:                true,
		MCPManageRegistered: mcpManageRegistered,
		VisibleSkills:       visibleSkills,
		Team:                inTeam,
	})
	groups := make([]ToolGroup, 0, len(toolCatalogGroupOrder)+len(mcpCfg))
	for _, group := range toolCatalogGroupOrder {
		entries := make([]ToolEntry, 0, len(group.Names))
		for _, name := range group.Names {
			tool, ok := registry.Get(name)
			if !ok {
				continue
			}
			listed := slices.Contains(agentCfg.Tools, name)
			auto := !listed && slices.Contains(autoOffered, name)
			source := "not_listed"
			if listed {
				source = "listed"
			} else if auto {
				source = "auto"
			}
			entries = append(entries, ToolEntry{
				Name:      name,
				Risk:      string(tool.Risk(nil)),
				AsksFirst: toolAsksFirst(tool, agentCfg),
				Enabled:   (listed || auto) && !disabled[name],
				Source:    source,
			})
		}
		groups = append(groups, ToolGroup{ID: group.ID, Label: group.Label, Tools: entries})
	}

	// MCP tools are grouped by server. The agent's own server list carries the
	// config order; any other configured server follows in name order.
	servers := rt.catalogMCPServers(agentCfg, mcpCfg)
	for _, server := range servers {
		entries := make([]ToolEntry, 0)
		for _, name := range rt.catalogToolNames() {
			toolServer, ref, ok := catalogMCPTool(name)
			if !ok || toolServer != server {
				continue
			}
			tool, ok := registry.Get(name)
			if !ok {
				continue
			}
			attached := slices.Contains(agentCfg.MCP, server)
			source := "not_attached"
			if attached {
				source = "attached"
			}
			entries = append(entries, ToolEntry{
				Name:      ref,
				Risk:      string(tool.Risk(nil)),
				AsksFirst: toolAsksFirst(tool, agentCfg),
				Enabled:   attached && !disabled[ref] && !disabled[name],
				Source:    source,
			})
		}
		groups = append(groups, ToolGroup{ID: "mcp:" + server, Label: server, Tools: entries})
	}
	return groups, nil
}

// catalogMCPServers returns the MCP server order of the catalog: the agent's
// list first, then the remaining configured servers, then any server that has
// tools in the registry but is not configured.
func (rt *Runtime) catalogMCPServers(agentCfg core.AgentConfig, mcpCfg map[string]core.MCPConfig) []string {
	seen := make(map[string]bool)
	servers := make([]string, 0, len(agentCfg.MCP)+len(mcpCfg))
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		servers = append(servers, name)
	}
	for _, name := range agentCfg.MCP {
		add(name)
	}
	rest := make([]string, 0, len(mcpCfg))
	for name := range mcpCfg {
		rest = append(rest, name)
	}
	sort.Strings(rest)
	for _, name := range rest {
		add(name)
	}
	var extra []string
	for _, name := range rt.catalogToolNames() {
		if server, _, ok := catalogMCPTool(name); ok && !seen[server] {
			extra = append(extra, server)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		add(name)
	}
	return servers
}

// catalogToolNames returns the registered tool names when the registry can
// enumerate them. A registry without Names has no MCP tools in the catalog.
func (rt *Runtime) catalogToolNames() []string {
	if namer, ok := rt.coreCtx.Tools.(interface{ Names() []string }); ok {
		return namer.Names()
	}
	return nil
}

// catalogMCPTool splits a registry tool name into its server and the
// config-facing "<server>/<tool>" reference. Both the model-facing
// "mcp__<server>__<tool>" form and an explicit "<server>/<tool>" name are
// accepted.
func catalogMCPTool(name string) (server, ref string, ok bool) {
	if rest, found := strings.CutPrefix(name, "mcp__"); found {
		i := strings.Index(rest, "__")
		if i <= 0 || i+2 >= len(rest) {
			return "", "", false
		}
		return rest[:i], rest[:i] + "/" + rest[i+2:], true
	}
	if i := strings.IndexByte(name, '/'); i > 0 && i < len(name)-1 && !strings.Contains(name[i+1:], "/") {
		return name[:i], name, true
	}
	return "", "", false
}

// toolAsksFirst reports whether the evaluator would ask the owner before this
// tool runs. Admin tools always ask; read and memory tools never ask; a trust
// or never policy never asks; a matching argument-less allow rule skips the
// prompt for the other risks (see tools/permission/evaluator.go).
func toolAsksFirst(tool core.Tool, agentCfg core.AgentConfig) bool {
	risk := tool.Risk(nil)
	if risk == core.RiskAdmin {
		return true
	}
	policy := core.PolicyAsk
	if agentCfg.Permission != "" {
		policy = core.Policy(agentCfg.Permission)
	}
	if policy != core.PolicyAsk {
		return false
	}
	switch risk {
	case core.RiskRead, core.RiskMemory:
		return false
	}
	name := tool.Spec().Name
	for _, raw := range agentCfg.Rules.Allow {
		p, err := pattern.ParsePattern(raw)
		if err != nil || p.Arg != nil {
			continue
		}
		if p.Match(name, pattern.Argument{}) {
			return false
		}
	}
	return true
}

// mcpTestTimeout is the limit of TestMCP (API-WEB-027).
const mcpTestTimeout = 15 * time.Second

// mcpOperations is the additive MCP operation set implemented by the MCP
// module's manager. It is separate from core.MCPManager so existing fakes do
// not grow methods.
type mcpOperations interface {
	Restart(ctx context.Context, name string) (core.MCPServerStatus, bool)
	Test(ctx context.Context, def core.MCPConfig) (int, error)
}

func (rt *Runtime) mcpOperations() (mcpOperations, bool) {
	service, ok := rt.coreCtx.Lookup(core.ServiceMCP)
	if !ok {
		return nil, false
	}
	manager, ok := service.(mcpOperations)
	return manager, ok
}

// RestartMCP stops and starts one MCP client from the loaded definition. It
// returns ErrUnknownMCP when the server is not part of the loaded config. It is
// additive (G-22, API-WEB-027).
func (rt *Runtime) RestartMCP(name string) (core.MCPServerStatus, error) {
	if err := rt.closedError(); err != nil {
		return core.MCPServerStatus{}, err
	}
	manager, ok := rt.mcpOperations()
	if !ok {
		return core.MCPServerStatus{}, errors.New("mcp: manager is not available")
	}
	status, found := manager.Restart(rt.ctx, name)
	if !found {
		return status, fmt.Errorf("%w: %s", ErrUnknownMCP, name)
	}
	return status, nil
}

// TestMCP starts a temporary MCP client from def, lists its tools, and stops it
// with a 15-second limit. The client is never added to the runtime. It is
// additive (G-22, API-WEB-027).
func (rt *Runtime) TestMCP(def core.MCPConfig) (int, error) {
	if err := rt.closedError(); err != nil {
		return 0, err
	}
	manager, ok := rt.mcpOperations()
	if !ok {
		return 0, errors.New("mcp: manager is not available")
	}
	ctx, cancel := context.WithTimeout(rt.ctx, mcpTestTimeout)
	defer cancel()
	return manager.Test(ctx, def)
}

// Skills returns the skills visible to an agent.
func (rt *Runtime) Skills(agentName string) []agenthome.Skill {
	if rt.home == "" {
		return nil
	}
	agentCfg, ok := rt.agents()[agentName]
	if !ok {
		return nil
	}
	skills, _ := agenthome.VisibleSkills(rt.home, agentName, agentCfg.Skills)
	return skills
}

// SessionInfo opens a session by id or prefix.
func (rt *Runtime) SessionInfo(ctx context.Context, id string) (core.SessionInfo, error) {
	if id == "" {
		return core.SessionInfo{}, ErrSessionNotFound
	}
	matches, err := rt.store.FindPrefix(ctx, id)
	if err != nil {
		return core.SessionInfo{}, err
	}
	return core.ResolvePrefix(matches, id)
}

// SubscribeLive subscribes to the session's live frames.
func (rt *Runtime) SubscribeLive(ctx context.Context, sessionID string) (*core.Subscription[core.LiveFrame], error) {
	if err := rt.closedError(); err != nil {
		return nil, err
	}
	return rt.coreCtx.Agents.SubscribeLive(sessionID)
}

// TurnActive reports whether a turn is running for the session in this process.
// It reads the in-process registry runners and never touches the store.
func (rt *Runtime) TurnActive(sessionID string) bool {
	if rt.registry == nil {
		return false
	}
	return rt.registry.TurnActive(sessionID)
}

// Send puts a message into the session's inbox. While draining it refuses the
// message before it starts or queues a turn (REQ-TFB-002, API-TFB-008).
func (rt *Runtime) Send(ctx context.Context, sessionID, text string) error {
	if err := rt.closedError(); err != nil {
		return err
	}
	if err := rt.drainError(); err != nil {
		return err
	}
	return rt.coreCtx.Agents.Send(ctx, sessionID, text)
}

// Cancel cancels the turn that is currently running.
func (rt *Runtime) Cancel(ctx context.Context, sessionID string) error {
	if err := rt.closedError(); err != nil {
		return err
	}
	return rt.coreCtx.Agents.Cancel(ctx, sessionID)
}

// Policy returns the session's effective policy.
func (rt *Runtime) Policy(ctx context.Context, sessionID string) (core.Policy, error) {
	if err := rt.closedError(); err != nil {
		return "", err
	}
	return rt.coreCtx.Approval.EffectivePolicy(ctx, sessionID)
}

// SetPolicy changes the session's effective policy.
func (rt *Runtime) SetPolicy(ctx context.Context, sessionID string, policy core.Policy) error {
	if err := rt.closedError(); err != nil {
		return err
	}
	return rt.coreCtx.Approval.SetPolicy(ctx, sessionID, policy)
}

// History returns all events of the session.
func (rt *Runtime) History(ctx context.Context, sessionID string) ([]core.SessionEvent, error) {
	if err := rt.closedError(); err != nil {
		return nil, err
	}
	return rt.store.ChainEvents(ctx, sessionID)
}

// LastSession returns the last cli session for the current workspace.
func (rt *Runtime) LastSession(ctx context.Context) (core.SessionInfo, error) {
	workspace, err := normalizeWorkspace(".")
	if err != nil {
		return core.SessionInfo{}, err
	}
	list, err := rt.store.List(ctx, core.SessionFilter{Channel: "cli", Address: workspace, Limit: 1})
	if err != nil {
		return core.SessionInfo{}, err
	}
	if len(list) == 0 {
		return core.SessionInfo{}, ErrSessionNotFound
	}
	return list[0], nil
}

// ListSessions returns the cli sessions for the current workspace.
func (rt *Runtime) ListSessions(ctx context.Context, limit int) ([]core.SessionInfo, error) {
	if err := rt.closedError(); err != nil {
		return nil, err
	}
	workspace, err := normalizeWorkspace(".")
	if err != nil {
		return nil, err
	}
	return rt.store.List(ctx, core.SessionFilter{Channel: "cli", Address: workspace, Limit: limit})
}

// ListAllSessions returns sessions of every channel that match filter. An
// empty Channel means all channels; other fields follow core.SessionFilter.
func (rt *Runtime) ListAllSessions(ctx context.Context, filter core.SessionFilter) ([]core.SessionInfo, error) {
	if err := rt.closedError(); err != nil {
		return nil, err
	}
	return rt.store.List(ctx, filter)
}

// ForkSession copies the session up to the end of its last turn.
func (rt *Runtime) ForkSession(ctx context.Context, sessionID string) (core.SessionInfo, error) {
	if err := rt.closedError(); err != nil {
		return core.SessionInfo{}, err
	}
	events, err := rt.store.ChainEvents(ctx, sessionID)
	if err != nil {
		return core.SessionInfo{}, err
	}
	var at int64
	for _, event := range events {
		if event.Kind == core.KindTurnEnd {
			at = event.Seq
		}
	}
	if at == 0 {
		return core.SessionInfo{}, errors.New("no completed turn to fork yet")
	}
	return rt.store.Fork(ctx, sessionID, at)
}

// SetAgent changes the session's agent.
func (rt *Runtime) SetAgent(ctx context.Context, sessionID, agentName string) error {
	if err := rt.closedError(); err != nil {
		return err
	}
	return rt.coreCtx.Agents.SetAgent(ctx, sessionID, agentName)
}

// SetModel changes the session's model (provider:model).
func (rt *Runtime) SetModel(ctx context.Context, sessionID, model string) error {
	if err := rt.closedError(); err != nil {
		return err
	}
	return rt.coreCtx.Agents.SetModel(ctx, sessionID, model)
}

// Compact compacts the session's history now.
func (rt *Runtime) Compact(ctx context.Context, sessionID string) (core.CompactionResult, error) {
	if err := rt.closedError(); err != nil {
		return core.CompactionResult{}, err
	}
	service, ok := rt.coreCtx.Lookup(core.ServiceCompaction)
	if !ok {
		return core.CompactionResult{}, errors.New("compaction service not available")
	}
	compactor, ok := service.(core.Compactor)
	if !ok {
		return core.CompactionResult{}, errors.New("compaction service is incompatible")
	}
	return compactor.CompactNow(ctx, sessionID)
}

// agentFor picks the agent from the request, the session, or the default route.
func (rt *Runtime) agentFor(ctx context.Context, req Request) (string, error) {
	if req.Agent != "" {
		return rt.routeAgent(req.Agent)
	}
	if req.SessionID != "" {
		if info, err := rt.SessionInfo(ctx, req.SessionID); err == nil && info.Agent != "" {
			return info.Agent, nil
		}
	}
	return rt.routeAgent("")
}

func validApprovalOutcome(outcome core.ApprovalOutcome) bool {
	switch outcome {
	case core.OutcomeAllowedOnce, core.OutcomeAllowedSession, core.OutcomeRejected, core.OutcomeCancelled, core.OutcomeUnavailable:
		return true
	}
	return false
}

func validQuestionOutcome(outcome core.QuestionOutcome) bool {
	switch outcome.Status {
	case core.QuestionStatusAnswered:
		return outcome.Reason == ""
	case core.QuestionStatusUnavailable:
		switch outcome.Reason {
		case core.QuestionReasonNoResponder, core.QuestionReasonTimeout, core.QuestionReasonCancelled, core.QuestionReasonInterrupted:
			return true
		}
	}
	return false
}

func questionNoResponder() core.QuestionOutcome {
	return core.QuestionOutcome{Status: core.QuestionStatusUnavailable, Reason: core.QuestionReasonNoResponder}
}
