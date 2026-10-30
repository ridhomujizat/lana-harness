package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

// TurnResult is the result of one turn.
type TurnResult struct {
	SessionID  string
	TurnID     string
	StopReason string
	Text       string
	Usage      core.Usage
	Err        *core.ProviderError
}

type turnState struct {
	done   chan struct{}
	result TurnResult
}

// maxActiveChildren is the number of child turns one parent may run at once.
const maxActiveChildren = 3

// mcpAdmin is core.MCPManager plus the hot-apply operations the registry
// forwards to core.MCPAdmin.
type mcpAdmin interface {
	core.MCPManager
	TestServer(ctx context.Context, cfg core.MCPConfig) (int, error)
	Put(name string, cfg core.MCPConfig)
	Remove(name string)
}

// mcpManagerOnly adapts a manager without hot apply. Its admin methods are
// never reached because mcpAdminFor reports no admin service for it.
type mcpManagerOnly struct{ core.MCPManager }

func (mcpManagerOnly) TestServer(context.Context, core.MCPConfig) (int, error) {
	return 0, fmt.Errorf("mcp admin is not available")
}
func (mcpManagerOnly) Put(string, core.MCPConfig) {}
func (mcpManagerOnly) Remove(string)              {}

// Registry manages live sessions and their inboxes.
type Registry struct {
	root       context.Context
	cancelRoot context.CancelFunc
	store      core.SessionStore
	llm        core.LLMRegistry
	tools      core.ToolRegistry
	mcp        mcpAdmin
	hooks      *core.Hooks
	questions  core.QuestionService
	config     core.Config
	secrets    core.Secrets
	home       string
	defaultWS  string

	mu             sync.Mutex
	runners        map[string]*Runner
	turns          map[string]*turnState
	activeChildren map[string]int
	closed         bool
	wg             sync.WaitGroup
	hub            *Hub
	pipeline       *ToolRunner

	// mcpMu guards the MCP manager, its admin flag, and the per-agent MCP
	// overlay. The overlay replaces agents.<name>.mcp in memory until restart.
	mcpMu       sync.Mutex
	mcpCanAdmin bool
	mcpServers  map[string][]string
}

// NewRegistry creates a registry.
func NewRegistry(store core.SessionStore, llm core.LLMRegistry, toolReg core.ToolRegistry, hooks *core.Hooks, cfg core.Config, secrets core.Secrets, home, defaultWorkspace string) *Registry {
	root, cancel := context.WithCancel(context.Background())
	if hooks == nil {
		hooks = core.NewHooks()
	}
	r := &Registry{
		root:           root,
		cancelRoot:     cancel,
		store:          store,
		llm:            llm,
		tools:          toolReg,
		hooks:          hooks,
		config:         cfg,
		secrets:        secrets,
		home:           home,
		defaultWS:      defaultWorkspace,
		runners:        map[string]*Runner{},
		turns:          map[string]*turnState{},
		activeChildren: map[string]int{},
		mcpServers:     map[string][]string{},
		hub:            NewHub(),
	}
	r.pipeline = NewToolRunner(hooks, toolReg)
	installDefaults(hooks, llm)
	return r
}

// SetMCP installs the MCP manager used by turns. A manager that also implements
// the hot-apply methods backs core.MCPAdmin as well; a plain core.MCPManager
// keeps connecting servers but exposes no admin service.
func (r *Registry) SetMCP(m core.MCPManager) {
	r.mcpMu.Lock()
	defer r.mcpMu.Unlock()
	if m == nil {
		r.mcp = nil
		r.mcpCanAdmin = false
		return
	}
	if admin, ok := m.(mcpAdmin); ok {
		r.mcp = admin
		r.mcpCanAdmin = true
		return
	}
	r.mcp = mcpManagerOnly{MCPManager: m}
	r.mcpCanAdmin = false
}

// mcpManager returns the installed MCP manager, or nil when none is set.
func (r *Registry) mcpManager() core.MCPManager {
	r.mcpMu.Lock()
	defer r.mcpMu.Unlock()
	if r.mcp == nil {
		return nil
	}
	return r.mcp
}

// mcpAdminFor returns the registry as core.MCPAdmin only when the installed
// manager supports hot apply.
func (r *Registry) mcpAdminFor() core.MCPAdmin {
	r.mcpMu.Lock()
	defer r.mcpMu.Unlock()
	if r.mcp == nil || !r.mcpCanAdmin {
		return nil
	}
	return r
}

// SetAgentServers replaces the in-memory MCP list of one agent. The copy keeps
// the caller's slice from changing under a running turn.
func (r *Registry) SetAgentServers(agent string, servers []string) {
	r.mcpMu.Lock()
	defer r.mcpMu.Unlock()
	r.mcpServers[agent] = append([]string(nil), servers...)
}

// agentServers returns the overlay list of one agent and whether it is set.
func (r *Registry) agentServers(agent string) ([]string, bool) {
	r.mcpMu.Lock()
	defer r.mcpMu.Unlock()
	servers, ok := r.mcpServers[agent]
	if !ok {
		return nil, false
	}
	return append([]string(nil), servers...), true
}

// adminManager returns the installed admin-capable MCP manager.
func (r *Registry) adminManager() (mcpAdmin, bool) {
	r.mcpMu.Lock()
	defer r.mcpMu.Unlock()
	if r.mcp == nil || !r.mcpCanAdmin {
		return nil, false
	}
	return r.mcp, true
}

// TestServer tests an MCP definition through the admin manager.
func (r *Registry) TestServer(ctx context.Context, cfg core.MCPConfig) (int, error) {
	admin, ok := r.adminManager()
	if !ok {
		return 0, fmt.Errorf("mcp admin is not available")
	}
	return admin.TestServer(ctx, cfg)
}

// PutServer adds or replaces an MCP server in the running manager.
func (r *Registry) PutServer(name string, cfg core.MCPConfig) {
	admin, ok := r.adminManager()
	if !ok {
		return
	}
	admin.Put(name, cfg)
}

// RemoveServer stops and forgets an MCP server in the running manager.
func (r *Registry) RemoveServer(name string) {
	admin, ok := r.adminManager()
	if !ok {
		return
	}
	admin.Remove(name)
}

func installDefaults(hooks *core.Hooks, llm core.LLMRegistry) {
	hooks.AgentPreStep.SetDefault(func(_ context.Context, in core.PreStepInput) (core.PreStepDecision, error) {
		return core.PreStepDecision{Input: in.Input}, nil
	})
	hooks.AgentRequest.SetDefault(func(_ context.Context, in core.RequestControl) (core.RequestControl, error) {
		return in, nil
	})
	hooks.LLMStream.SetDefault(func(ctx context.Context, in core.LLMStreamInput) (core.ChatStream, error) {
		if len(in.Targets) == 0 {
			return nil, &core.ProviderError{Class: "client", Message: "provider target is empty"}
		}
		target := in.Targets[0]
		provider, ok := llm.Provider(target.Provider)
		if !ok {
			return nil, &core.ProviderError{Class: "client", Message: "unknown provider: " + target.Provider}
		}
		req := in.Request
		req.Model = target.Model
		return provider.Stream(ctx, req)
	})
}

func (r *Registry) runnerLocked(sessionID string) *Runner {
	if existing := r.runners[sessionID]; existing != nil {
		return existing
	}
	runner := &Runner{reg: r, sessionID: sessionID}
	r.runners[sessionID] = runner
	return runner
}

// acquireChild reserves a running child slot for parentID. It returns the
// number of active children and false when the limit is already reached.
func (r *Registry) acquireChild(parentID string) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := r.activeChildren[parentID]
	if count >= maxActiveChildren {
		return count, false
	}
	r.activeChildren[parentID] = count + 1
	return count + 1, true
}

// releaseChild frees a running child slot for parentID.
func (r *Registry) releaseChild(parentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := r.activeChildren[parentID] - 1
	if count <= 0 {
		delete(r.activeChildren, parentID)
		return
	}
	r.activeChildren[parentID] = count
}

func (r *Registry) finishTurn(turnID string, result TurnResult) {
	r.mu.Lock()
	state := r.turns[turnID]
	if state != nil {
		state.result = result
		close(state.done)
	}
	r.mu.Unlock()
}

// Wait waits for the turn to finish.
func (r *Registry) Wait(ctx context.Context, turnID string) (TurnResult, error) {
	r.mu.Lock()
	state := r.turns[turnID]
	r.mu.Unlock()
	if state == nil {
		return TurnResult{}, fmt.Errorf("%w: turn %s", core.ErrNotFound, turnID)
	}
	select {
	case <-state.done:
		r.mu.Lock()
		result := state.result
		r.mu.Unlock()
		return result, nil
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	}
}

func (r *Registry) sessionExists(ctx context.Context, sessionID string) error {
	if _, err := r.store.Info(ctx, sessionID); err != nil {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	return nil
}

// Start starts a new turn.
func (r *Registry) Start(ctx context.Context, sessionID, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", ErrEmptyText
	}
	if err := r.sessionExists(ctx, sessionID); err != nil {
		return "", err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return "", ErrClosed
	}
	runner := r.runnerLocked(sessionID)
	runner.mu.Lock()
	if runner.running {
		runner.mu.Unlock()
		r.mu.Unlock()
		return "", ErrSessionBusy
	}
	turnID, turnCtx := r.startTurnLocked(runner)
	runner.mu.Unlock()
	r.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		runner.runChain(turnCtx, text, turnID)
	}()
	return turnID, nil
}

func (r *Registry) startTurnLocked(runner *Runner) (string, context.Context) {
	return r.startTurnFrom(r.root, runner)
}

// startTurnFrom starts a turn whose context derives from parent. The caller
// holds r.mu and runner.mu.
func (r *Registry) startTurnFrom(parent context.Context, runner *Runner) (string, context.Context) {
	turnID := id.New()
	turnCtx, cancel := context.WithCancel(parent)
	runner.running = true
	runner.turnID = turnID
	runner.cancel = cancel
	r.turns[turnID] = &turnState{done: make(chan struct{})}
	return turnID, turnCtx
}

func (r *Registry) startTurnAsync(runner *Runner, text string) {
	turnCtx, cancel := context.WithCancel(r.root)
	turnID := id.New()
	runner.running = true
	runner.turnID = turnID
	runner.cancel = cancel
	r.turns[turnID] = &turnState{done: make(chan struct{})}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		runner.runChain(turnCtx, text, turnID)
	}()
}

// Send sends a message; if the session is idle it starts a turn, otherwise it goes to the inbox.
func (r *Registry) Send(ctx context.Context, sessionID, text string) error {
	if strings.TrimSpace(text) == "" {
		return ErrEmptyText
	}
	if err := r.sessionExists(ctx, sessionID); err != nil {
		return err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	runner := r.runnerLocked(sessionID)
	runner.mu.Lock()
	if runner.running {
		runner.inbox = append(runner.inbox, text)
		runner.mu.Unlock()
		r.mu.Unlock()
		return nil
	}
	r.startTurnAsync(runner, text)
	runner.mu.Unlock()
	r.mu.Unlock()
	return nil
}

// Cancel cancels the running turn.
func (r *Registry) Cancel(ctx context.Context, sessionID string) error {
	if err := r.sessionExists(ctx, sessionID); err != nil {
		return err
	}
	r.mu.Lock()
	runner := r.runners[sessionID]
	r.mu.Unlock()
	if runner == nil {
		return nil
	}
	runner.mu.Lock()
	cancel := runner.cancel
	runner.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// SetAgent selects the agent or team for the session.
func (r *Registry) SetAgent(ctx context.Context, sessionID, agentName string) error {
	if err := r.sessionExists(ctx, sessionID); err != nil {
		return err
	}
	if team, ok := r.config.Teams[agentName]; ok {
		lead, ok := r.config.Agents[team.Lead]
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownAgent, team.Lead)
		}
		payload := core.ModelSelection{V: core.PayloadVersion, Provider: lead.Provider, Model: lead.Model, Agent: agentName}
		return r.appendEvent(ctx, sessionID, core.KindModelSelection, "", payload)
	}
	agent, ok := r.config.Agents[agentName]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAgent, agentName)
	}
	payload := core.ModelSelection{V: core.PayloadVersion, Provider: agent.Provider, Model: agent.Model, Agent: agentName}
	return r.appendEvent(ctx, sessionID, core.KindModelSelection, "", payload)
}

// SetModel selects the provider:model for the session.
func (r *Registry) SetModel(ctx context.Context, sessionID, model string) error {
	if err := r.sessionExists(ctx, sessionID); err != nil {
		return err
	}
	agentName, err := r.sessionAgent(ctx, sessionID)
	if err != nil {
		return err
	}
	if _, ok := r.config.Teams[agentName]; ok {
		return ErrTeamModel
	}
	providerName, modelName, ok := strings.Cut(model, ":")
	if !ok || providerName == "" {
		return fmt.Errorf("%w: %s", ErrUnknownProvider, providerName)
	}
	if modelName == "" {
		return ErrEmptyModel
	}
	if _, exists := r.config.Providers[providerName]; !exists {
		return fmt.Errorf("%w: %s", ErrUnknownProvider, providerName)
	}
	payload := core.ModelSelection{V: core.PayloadVersion, Provider: providerName, Model: modelName}
	return r.appendEvent(ctx, sessionID, core.KindModelSelection, "", payload)
}

// sessionAgent returns the agent or team currently selected by the session.
func (r *Registry) sessionAgent(ctx context.Context, sessionID string) (string, error) {
	info, err := r.store.Info(ctx, sessionID)
	if err != nil {
		return "", err
	}
	name := info.Agent
	chain, err := r.store.ChainEvents(ctx, sessionID)
	if err != nil {
		return "", err
	}
	for _, event := range chain {
		if event.Kind != core.KindModelSelection {
			continue
		}
		var payload core.ModelSelection
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return "", err
		}
		if payload.Agent != "" {
			name = payload.Agent
		}
	}
	return name, nil
}

// SubscribeLive subscribes to the live frames of one session.
func (r *Registry) SubscribeLive(sessionID string) (*core.Subscription[core.LiveFrame], error) {
	return r.hub.Subscribe(sessionID), nil
}

// TurnActive reports whether a turn is running for the session in this process.
func (r *Registry) TurnActive(sessionID string) bool {
	r.mu.Lock()
	runner := r.runners[sessionID]
	r.mu.Unlock()
	if runner == nil {
		return false
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.running
}

func (r *Registry) appendEvent(ctx context.Context, sessionID, kind, turnID string, payload any) error {
	events := &Events{Store: r.store, SessionID: sessionID}
	_, err := events.Append(ctx, core.EventDraft{Kind: kind, TurnID: turnID, Payload: payload})
	return err
}

// Close cancels all turns and waits for them to finish.
func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.cancelRoot()
	r.mu.Unlock()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(30 * time.Second):
		return fmt.Errorf("shutdown_timeout: turn did not finish")
	}
	r.hub.Close()
	return nil
}

// AgentNames returns the registered agents, sorted.
func (r *Registry) AgentNames() []string {
	names := make([]string, 0, len(r.config.Agents))
	for name := range r.config.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var _ core.AgentRegistry = (*Registry)(nil)
var _ core.MCPAdmin = (*Registry)(nil)
