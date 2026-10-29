// Package lana is the embeddable lana-harness runtime.
package lana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ridhomujizat/lana-harness/agent"
	_ "github.com/ridhomujizat/lana-harness/agent/compaction"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
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
	approve ApproveFunc
}

// ApproveFunc answers approvals for the library channel.
type ApproveFunc = func(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome

// ApproveWith installs an approval responder.
func ApproveWith(fn ApproveFunc) Option {
	return func(o *runtimeOptions) { o.approve = fn }
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
)

// ProviderError is an alias for core.ProviderError.
type ProviderError = core.ProviderError

// Runtime is the library runtime.
type Runtime struct {
	ctx      context.Context
	cancel   context.CancelFunc
	coreCtx  *core.Context
	result   *config.Result
	registry *agent.Registry
	store    core.SessionStore
	closed   atomic.Bool
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
	if opts.Approval == PolicyTrust {
		coreCtx.Hooks.ToolsPreExecute.Use(func(context.Context, core.ToolsPreExecuteInput, core.Next[core.ToolsPreExecuteInput, core.ToolDecision]) (core.ToolDecision, error) {
			return core.ToolDecision{Allowed: true}, nil
		}, core.WithPrepend())
	}
	if runtimeOpts.approve != nil {
		fn := runtimeOpts.approve
		coreCtx.Hooks.ApprovalRequest.Use(func(ctx context.Context, req core.ApprovalRequest, next core.Next[core.ApprovalRequest, core.ApprovalOutcome]) (outcome core.ApprovalOutcome, err error) {
			info, infoErr := coreCtx.Sessions.Info(ctx, req.SessionID)
			if infoErr != nil || (info.Channel != "library" && info.Channel != "cli") {
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
	root, cancel := context.WithCancel(context.Background())
	rt := &Runtime{
		ctx: root, cancel: cancel, coreCtx: coreCtx, result: result,
		registry: registry, store: coreCtx.Sessions, approve: runtimeOpts.approve, home: opts.Home,
	}
	return rt, nil
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

func (rt *Runtime) routeAgent(agentName string) (string, error) {
	if agentName != "" {
		if _, ok := rt.result.Config.Agents[agentName]; !ok {
			return "", fmt.Errorf("%w: %s", ErrUnknownAgent, agentName)
		}
		return agentName, nil
	}
	for _, route := range rt.result.Config.Routes {
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
	if agentCfg, ok := rt.result.Config.Agents[agentName]; ok && agentCfg.Workspace != "" {
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
		model := ""
		if agentCfg, ok := rt.result.Config.Agents[agentName]; ok {
			model = agentCfg.Provider + ":" + agentCfg.Model
		}
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
	for _, route := range rt.result.Config.Routes {
		if route.Channel == channel {
			return route.Agent
		}
	}
	return ""
}

// AgentNames returns the registered agent names, sorted.
func (rt *Runtime) AgentNames() []string {
	names := make([]string, 0, len(rt.result.Config.Agents))
	for name := range rt.result.Config.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// AgentModel returns the agent's "provider:model".
func (rt *Runtime) AgentModel(agentName string) string {
	if agentCfg, ok := rt.result.Config.Agents[agentName]; ok {
		return agentCfg.Provider + ":" + agentCfg.Model
	}
	return ""
}

// AgentPolicy returns the agent's initial policy.
func (rt *Runtime) AgentPolicy(agentName string) string {
	if agentCfg, ok := rt.result.Config.Agents[agentName]; ok {
		return string(agentCfg.Permission)
	}
	return ""
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

// Send puts a message into the session's inbox.
func (rt *Runtime) Send(ctx context.Context, sessionID, text string) error {
	if err := rt.closedError(); err != nil {
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
