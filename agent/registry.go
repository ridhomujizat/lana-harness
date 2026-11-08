package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
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

// finishedTurnCap is the number of finished turns kept with their result, so a
// Wait shortly after a turn ends still finds it. Older finished records are
// dropped; a running turn is never dropped.
const finishedTurnCap = 256

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
	compaction core.Compactor
	memory     core.MemoryCapturer
	titler     core.SessionTitler
	knowledge  core.Knowledge
	config     core.Config
	secrets    core.Secrets
	home       string
	defaultWS  string

	mu      sync.Mutex
	runners map[string]*Runner
	turns   map[string]*turnState
	// turnOrder is the turn ids finished in finish order, oldest first.
	// finishTurn trims the oldest finished records beyond finishedTurnCap so
	// the turns map cannot grow without bound over a long session.
	turnOrder      []string
	activeChildren map[string]int
	closed         bool
	wg             sync.WaitGroup
	hub            *Hub
	pipeline       *ToolRunner

	// jobs is the background-job registry, set once at boot (REQ-JOB-010). Nil
	// when the runtime has no job registry.
	jobs JobService

	// mcpMu guards the MCP manager, its admin flag, and the per-agent MCP
	// overlay. The overlay replaces agents.<name>.mcp in memory until restart.
	mcpMu       sync.Mutex
	mcpCanAdmin bool
	mcpServers  map[string][]string
	// mcpApplied holds the MCP definitions the registry applied, seeded from
	// the boot config. The diff in ApplyConfig reads it instead of the config
	// snapshot, so a server mcp_manage already put is not applied twice.
	mcpApplied map[string]core.MCPConfig

	// cfgMu guards the agents and teams snapshot. ApplyConfig swaps both maps
	// as a unit; reads copy the map header under the lock and use it unlocked.
	cfgMu sync.RWMutex
	// applyMu serializes ApplyConfig so two saves cannot race on the MCP diff.
	applyMu sync.Mutex

	// runtimeMu guards the external agent runtimes (ADR 0018), built on first
	// use by name, and the hooks their turns report usage and limits through.
	runtimeMu      sync.Mutex
	runtimes       map[string]core.AgentRuntime
	runtimesClosed bool
	externalHooks  ExternalHooks
	// toolHost serves lana's tools to Claude Code turns (ADR 0020).
	toolHost core.RuntimeToolHost
	// scheduled counts the scheduled-run marks per session (REQ-CCT-008).
	scheduled map[string]int
}

// NewRegistry creates a registry.
func NewRegistry(store core.SessionStore, llm core.LLMRegistry, toolReg core.ToolRegistry, hooks *core.Hooks, cfg core.Config, secrets core.Secrets, home, defaultWorkspace string) *Registry {
	root, cancel := context.WithCancel(context.Background())
	// API-CCT-002: a legacy runtime agent is its claude-code address.
	cfg.Agents = llmcall.NormalizeAgents(cfg.Agents)
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
		mcpApplied:     make(map[string]core.MCPConfig, len(cfg.MCP)),
		hub:            NewHub(),
	}
	for name, serverCfg := range cfg.MCP {
		r.mcpApplied[name] = serverCfg
	}
	r.pipeline = NewToolRunner(hooks, toolReg)
	installDefaults(hooks, llm, store)
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

// SetCompactor installs the compaction service used by turns. It is set at
// boot, before any turn starts; a nil service disables compaction.
func (r *Registry) SetCompactor(c core.Compactor) { r.compaction = c }

// compactor returns the compaction service, or nil when none is installed.
func (r *Registry) compactor() core.Compactor { return r.compaction }

// SetJobs installs the background-job registry used by turns and the job
// tools. It is set once at boot, before any turn starts (REQ-JOB-010).
func (r *Registry) SetJobs(j JobService) { r.jobs = j }

// SetMemoryCapturer installs the memory-capture service used at turn end. It is
// set at boot, before any turn starts; a nil service disables capture.
func (r *Registry) SetMemoryCapturer(c core.MemoryCapturer) { r.memory = c }

// memoryCapturer returns the memory-capture service, or nil when none is
// installed.
func (r *Registry) memoryCapturer() core.MemoryCapturer { return r.memory }

// SetSessionTitler installs the session title service used at turn end
// (spec 40). It is set at boot, before any turn starts; nil disables titles.
func (r *Registry) SetSessionTitler(t core.SessionTitler) { r.titler = t }

// titleTurnEnd asks the title service to name a session after a completed
// root turn that stored an owner message (REQ-TTL-001). The service returns
// at once and skips a session that already has a title.
func (r *Registry) titleTurnEnd(reason string, child, owner bool, req core.TitleRequest) {
	if r.titler == nil || child || !owner {
		return
	}
	if reason != "end_turn" && reason != "max_steps" && reason != "loop" {
		return
	}
	r.titler.TurnEnded(req)
}

// SetKnowledge installs the knowledge service used by the knowledge tools and
// per-turn recall. It is set at boot, before any turn starts; a nil service
// disables knowledge.
func (r *Registry) SetKnowledge(k core.Knowledge) { r.knowledge = k }

// knowledgeService returns the knowledge service, or nil when none is
// installed.
func (r *Registry) knowledgeService() core.Knowledge { return r.knowledge }

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

// configSnapshot returns the loaded agent and team maps. ApplyConfig replaces
// the maps as a unit and never changes them in place, so the result stays valid
// after the lock is released.
func (r *Registry) configSnapshot() (map[string]core.AgentConfig, map[string]core.TeamConfig) {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	return r.config.Agents, r.config.Teams
}

// AgentConfig returns the live config of one agent and whether it exists.
// ApplyConfig replaces the map, so this reads the current snapshot; the
// compaction service uses it so context_window and compaction_* edits apply
// without a restart (REQ-LTC-011).
func (r *Registry) AgentConfig(name string) (core.AgentConfig, bool) {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	agent, ok := r.config.Agents[name]
	return agent, ok
}

// providersConfig returns the loaded provider map for validation lookups.
// modelConfig returns the live providers and combos.
func (r *Registry) modelConfig() (map[string]core.ProviderConfig, map[string]core.ComboConfig) {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	return r.config.Providers, r.config.Combos
}

// ApplyConfig swaps the agent, team, provider, and combo config and applies
// the [mcp.*] diff to the running manager. The router applies providers and
// combos itself (REQ-RTR-008); the registry keeps them to check session model
// switches. Routes, channels, web, and secrets are not applied here
// (REQ-TFB-006).
func (r *Registry) ApplyConfig(cfg core.Config) {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()

	agents := make(map[string]core.AgentConfig, len(cfg.Agents))
	for name, agentCfg := range cfg.Agents {
		agents[name] = llmcall.NormalizeAgent(agentCfg)
	}
	teams := make(map[string]core.TeamConfig, len(cfg.Teams))
	for name, teamCfg := range cfg.Teams {
		teams[name] = teamCfg
	}
	r.cfgMu.Lock()
	r.config.Agents = agents
	r.config.Teams = teams
	r.config.Providers = cfg.Providers
	r.config.Combos = cfg.Combos
	r.cfgMu.Unlock()

	// The config now owns these agents, so its mcp list wins over the overlay
	// that mcp_manage keeps for a running turn.
	r.mcpMu.Lock()
	for name := range agents {
		delete(r.mcpServers, name)
	}
	applied := make(map[string]core.MCPConfig, len(r.mcpApplied))
	for name, serverCfg := range r.mcpApplied {
		applied[name] = serverCfg
	}
	r.mcpMu.Unlock()

	for name, serverCfg := range cfg.MCP {
		if prev, ok := applied[name]; ok && reflect.DeepEqual(prev, serverCfg) {
			continue
		}
		r.PutServer(name, serverCfg)
	}
	for name := range applied {
		if _, ok := cfg.MCP[name]; !ok {
			r.RemoveServer(name)
		}
	}
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

// PutServer adds or replaces an MCP server in the running manager and records
// the applied definition.
func (r *Registry) PutServer(name string, cfg core.MCPConfig) {
	admin, ok := r.adminManager()
	if !ok {
		return
	}
	admin.Put(name, cfg)
	r.mcpMu.Lock()
	r.mcpApplied[name] = cfg
	r.mcpMu.Unlock()
}

// RemoveServer stops and forgets an MCP server in the running manager.
func (r *Registry) RemoveServer(name string) {
	admin, ok := r.adminManager()
	if !ok {
		return
	}
	admin.Remove(name)
	r.mcpMu.Lock()
	delete(r.mcpApplied, name)
	r.mcpMu.Unlock()
}

// ServerStatus reports the runtime state of one MCP server. known is false when
// the registry never applied the server (REQ-TFB-007, API-TFB-007).
func (r *Registry) ServerStatus(name string) (state string, tools int, reason string, known bool) {
	r.mcpMu.Lock()
	_, known = r.mcpApplied[name]
	manager := r.mcp
	r.mcpMu.Unlock()
	if !known || manager == nil {
		return "", 0, "", false
	}
	statuses := manager.Status([]string{name})
	if len(statuses) == 0 {
		return "", 0, "", false
	}
	status := statuses[0]
	return status.State, status.Tools, status.Reason, true
}

func installDefaults(hooks *core.Hooks, llm core.LLMRegistry, store core.SessionStore) {
	hooks.AgentPreStep.SetDefault(func(_ context.Context, in core.PreStepInput) (core.PreStepDecision, error) {
		return core.PreStepDecision{Input: in.Input}, nil
	})
	hooks.AgentRequest.SetDefault(func(_ context.Context, in core.RequestControl) (core.RequestControl, error) {
		return in, nil
	})
	// REQ-RTR-005, REQ-RTR-007: the router owns retry, backoff, fallback,
	// and cooldown. Each failed attempt becomes an assistant/attempt event.
	hooks.LLMStream.SetDefault(func(ctx context.Context, in core.LLMStreamInput) (core.ChatStream, error) {
		meta := core.LLMCallMeta{
			Source: llmcall.SourceTurn, Agent: in.AgentName,
			SessionID: in.SessionID, TurnID: in.TurnID, Requested: in.Requested,
		}
		stream, served, err := llmcall.Stream(ctx, llm, in.Targets, in.Request, meta, func(attempt core.LLMAttempt) {
			writeAttempt(ctx, store, in, attempt)
		})
		if err != nil {
			return nil, err
		}
		if in.OnServed != nil {
			in.OnServed(served)
		}
		return stream, nil
	})
}

// writeAttempt records one failed attempt of a step before the router
// retries or moves to the next target.
func writeAttempt(ctx context.Context, store core.SessionStore, in core.LLMStreamInput, attempt core.LLMAttempt) {
	if store == nil || attempt.Err == nil {
		return
	}
	events := &Events{Store: store, SessionID: in.SessionID}
	pe := attempt.Err
	payload := core.AssistantAttempt{
		V: core.PayloadVersion, Step: in.Step, Attempt: attempt.N,
		Provider: attempt.Target.Provider, Model: attempt.Target.Model,
		Error: core.PayloadError{Class: pe.Class, Status: pe.Status, Message: pe.Message},
	}
	_, _ = events.Append(context.WithoutCancel(ctx), core.EventDraft{Kind: core.KindAssistantAttempt, TurnID: in.TurnID, Payload: payload})
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
	if state := r.turns[turnID]; state != nil {
		state.result = result
		close(state.done)
		// Remember the finish order and drop the oldest finished records once
		// the cap is passed. Only finished turns are tracked, so a running turn
		// is never deleted.
		r.turnOrder = append(r.turnOrder, turnID)
		for len(r.turnOrder) > finishedTurnCap {
			oldest := r.turnOrder[0]
			r.turnOrder = r.turnOrder[1:]
			delete(r.turns, oldest)
		}
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

// Start starts a new turn. attachments are workspace-relative paths of the
// owner message (REQ-ATT-002).
func (r *Registry) Start(ctx context.Context, sessionID, text string, attachments ...string) (string, error) {
	return r.start(ctx, sessionID, text, attachments, "")
}

// StartContinue starts a new turn whose first user message is marked as a
// continue; reason is the previous turn/end reason (API-TFB-004, REQ-TFB-005).
func (r *Registry) StartContinue(ctx context.Context, sessionID, text, reason string) (string, error) {
	return r.start(ctx, sessionID, text, nil, reason)
}

// start starts a new turn. continueReason is non-empty only for a continue turn
// and marks the turn's first user message.
func (r *Registry) start(ctx context.Context, sessionID, text string, attachments []string, continueReason string) (string, error) {
	if strings.TrimSpace(text) == "" && len(attachments) == 0 {
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
		runner.runChain(turnCtx, inboxItem{text: text, attachments: append([]string(nil), attachments...)}, turnID, continueReason)
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
	turnCtx, cancel := context.WithCancelCause(parent)
	runner.running = true
	runner.turnID = turnID
	runner.cancel = cancel
	r.turns[turnID] = &turnState{done: make(chan struct{})}
	return turnID, turnCtx
}

func (r *Registry) startTurnAsync(runner *Runner, item inboxItem) {
	turnCtx, cancel := context.WithCancelCause(r.root)
	turnID := id.New()
	runner.running = true
	runner.turnID = turnID
	runner.cancel = cancel
	r.turns[turnID] = &turnState{done: make(chan struct{})}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		runner.runChain(turnCtx, item, turnID, "")
	}()
}

// Send sends a message; if the session is idle it starts a turn, otherwise it goes to the inbox.
func (r *Registry) Send(ctx context.Context, sessionID, text string) error {
	return r.SendWithAttachments(ctx, sessionID, text)
}

// SendWithAttachments sends a message with attachments (workspace-relative
// paths); if the session is idle it starts a turn, otherwise it goes to the
// inbox (REQ-ATT-002, REQ-ATT-008).
func (r *Registry) SendWithAttachments(ctx context.Context, sessionID, text string, attachments ...string) error {
	if strings.TrimSpace(text) == "" && len(attachments) == 0 {
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
	item := inboxItem{text: text, attachments: append([]string(nil), attachments...)}
	runner.mu.Lock()
	if runner.running {
		runner.inbox = append(runner.inbox, item)
		runner.mu.Unlock()
		r.mu.Unlock()
		return nil
	}
	r.startTurnAsync(runner, item)
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
		// A nil cause keeps context.Cause at context.Canceled, so the turn
		// stays "cancelled" and is not an interrupt (API-TFB-001).
		cancel(nil)
	}
	// REQ-CCR-006: an external runtime turn gets its interrupt at once.
	runner.interruptExternal()
	return nil
}

// Interrupt cancels the running turn with cause. A shutdown cause becomes the
// "interrupted" turn/end reason (REQ-TFB-001, API-TFB-001).
func (r *Registry) Interrupt(ctx context.Context, sessionID string, cause error) error {
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
		cancel(cause)
	}
	runner.interruptExternal()
	return nil
}

// InterruptAll cancels every turn running in this process with cause and
// returns how many turns it interrupted (REQ-TFB-002).
func (r *Registry) InterruptAll(cause error) int {
	r.mu.Lock()
	runners := make([]*Runner, 0, len(r.runners))
	for _, runner := range r.runners {
		runners = append(runners, runner)
	}
	r.mu.Unlock()
	count := 0
	for _, runner := range runners {
		runner.mu.Lock()
		running, cancel := runner.running, runner.cancel
		runner.mu.Unlock()
		if !running || cancel == nil {
			continue
		}
		cancel(cause)
		count++
	}
	return count
}

// RunningTurnCount reports the number of turns running in this process.
func (r *Registry) RunningTurnCount() int {
	r.mu.Lock()
	runners := make([]*Runner, 0, len(r.runners))
	for _, runner := range r.runners {
		runners = append(runners, runner)
	}
	r.mu.Unlock()
	count := 0
	for _, runner := range runners {
		runner.mu.Lock()
		if runner.running {
			count++
		}
		runner.mu.Unlock()
	}
	return count
}

// SetAgent selects the agent or team for the session.
func (r *Registry) SetAgent(ctx context.Context, sessionID, agentName string) error {
	if err := r.sessionExists(ctx, sessionID); err != nil {
		return err
	}
	agents, teams := r.configSnapshot()
	if team, ok := teams[agentName]; ok {
		if _, ok := agents[team.Lead]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownAgent, team.Lead)
		}
		// No provider or model: resolve uses the agent's config model, so a
		// later web model change reaches an existing session (AC-TFB-014).
		payload := core.ModelSelection{V: core.PayloadVersion, Agent: agentName}
		return r.appendEvent(ctx, sessionID, core.KindModelSelection, "", payload)
	}
	if _, ok := agents[agentName]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAgent, agentName)
	}
	payload := core.ModelSelection{V: core.PayloadVersion, Agent: agentName}
	return r.appendEvent(ctx, sessionID, core.KindModelSelection, "", payload)
}

// SetModel selects the session's model: a combo name, a "provider/model"
// address, or the legacy "provider:model" (API-RTR-003). provider:model is
// stored as provider and model as before; a combo or an address is stored
// in model with no provider, so old sessions read the same.
func (r *Registry) SetModel(ctx context.Context, sessionID, model string) error {
	if err := r.sessionExists(ctx, sessionID); err != nil {
		return err
	}
	agentName, err := r.sessionAgent(ctx, sessionID)
	if err != nil {
		return err
	}
	_, teams := r.configSnapshot()
	if _, ok := teams[agentName]; ok {
		return ErrTeamModel
	}
	providerName, modelName, err := r.parseModelChoice(model)
	if err != nil {
		return err
	}
	selection, err := r.sessionSelection(ctx, sessionID)
	if err != nil {
		return err
	}
	// A model change keeps the session's reasoning effort (REQ-MDL-001).
	payload := core.ModelSelection{
		V:               core.PayloadVersion,
		Provider:        providerName,
		Model:           modelName,
		ReasoningEffort: selection.ReasoningEffort,
	}
	return r.appendEvent(ctx, sessionID, core.KindModelSelection, "", payload)
}

// parseModelChoice checks a session model value and returns the selection's
// provider and model: ("p", "m") for "p:m", ("", value) for a combo name or a
// "provider/model" address.
func (r *Registry) parseModelChoice(value string) (string, string, error) {
	providers, combos := r.modelConfig()
	if _, ok := combos[value]; ok {
		return "", value, nil
	}
	// AC-CCT-004: a claude-code address switches the session to Claude Code.
	if llmcall.IsClaudeCodeAddress(r.llm, value) {
		if target, _ := llmcall.ParseTarget(value); !llmcall.ValidClaudeCodeModel(target.Model) {
			return "", "", fmt.Errorf("%w: %s", ErrUnknownProvider, value)
		}
		return "", value, nil
	}
	sep := strings.IndexAny(value, "/:")
	if sep < 0 {
		// Neither a combo in config nor an address: the router may still know
		// it as a combo (an embedder's router config).
		if rtr, ok := r.llm.(core.LLMRouter); ok {
			if _, err := rtr.Resolve(value); err == nil {
				return "", value, nil
			}
		}
		return "", "", fmt.Errorf("%w: %s", ErrUnknownProvider, value)
	}
	providerName, modelName := value[:sep], value[sep+1:]
	if providerName == "" {
		return "", "", fmt.Errorf("%w: %s", ErrUnknownProvider, providerName)
	}
	if modelName == "" {
		return "", "", ErrEmptyModel
	}
	if _, exists := providers[providerName]; !exists {
		if r.llm == nil {
			return "", "", fmt.Errorf("%w: %s", ErrUnknownProvider, providerName)
		}
		if _, known := r.llm.Provider(providerName); !known {
			return "", "", fmt.Errorf("%w: %s", ErrUnknownProvider, providerName)
		}
	}
	if value[sep] == ':' {
		return providerName, modelName, nil
	}
	return "", value, nil
}

// SetReasoningEffort sets the session's thinking effort. The provider:model
// selection is kept; an empty value clears it, so the agent or model default
// applies again (REQ-MDL-001).
func (r *Registry) SetReasoningEffort(ctx context.Context, sessionID, effort string) error {
	if err := r.sessionExists(ctx, sessionID); err != nil {
		return err
	}
	selection, err := r.sessionSelection(ctx, sessionID)
	if err != nil {
		return err
	}
	payload := core.ModelSelection{
		V:               core.PayloadVersion,
		Provider:        selection.Provider,
		Model:           selection.Model,
		Agent:           selection.Agent,
		ReasoningEffort: effort,
	}
	return r.appendEvent(ctx, sessionID, core.KindModelSelection, "", payload)
}

// sessionSelection returns the last model/selection payload of the session, or
// the zero value when the session never selected one.
func (r *Registry) sessionSelection(ctx context.Context, sessionID string) (core.ModelSelection, error) {
	var selection core.ModelSelection
	chain, err := r.store.ChainEvents(ctx, sessionID)
	if err != nil {
		return selection, err
	}
	for _, event := range chain {
		if event.Kind != core.KindModelSelection {
			continue
		}
		var payload core.ModelSelection
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return selection, err
		}
		selection = payload
	}
	return selection, nil
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

// WithoutTurn calls fn while no turn for the session runs or can start in this
// process. It returns false without calling fn when a turn for the session is
// running. A Start or Send for the session waits until fn returns. It backs the
// repair of a turn whose turn/end was lost, which must never touch a live turn.
func (r *Registry) WithoutTurn(sessionID string, fn func() error) (bool, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false, ErrClosed
	}
	runner := r.runnerLocked(sessionID)
	runner.mu.Lock()
	r.mu.Unlock()
	defer runner.mu.Unlock()
	if runner.running {
		return false, nil
	}
	return true, fn()
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
	// The external runtime processes stop once the turns ended, or when the
	// wait gives up (ADR 0018: processes exit at daemon shutdown).
	defer func() {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		r.shutdownRuntimes(sctx)
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
	agents, _ := r.configSnapshot()
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var _ core.AgentRegistry = (*Registry)(nil)
var _ core.MCPAdmin = (*Registry)(nil)
