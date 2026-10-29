package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

// TurnResult adalah hasil satu turn.
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

// Registry mengatur session hidup dan inbox.
type Registry struct {
	root       context.Context
	cancelRoot context.CancelFunc
	store      core.SessionStore
	llm        core.LLMRegistry
	tools      core.ToolRegistry
	hooks      *core.Hooks
	config     core.Config
	secrets    core.Secrets
	home       string
	defaultWS  string

	mu       sync.Mutex
	runners  map[string]*Runner
	turns    map[string]*turnState
	closed   bool
	wg       sync.WaitGroup
	hub      *Hub
	pipeline *ToolRunner
}

// NewRegistry membuat registry.
func NewRegistry(store core.SessionStore, llm core.LLMRegistry, toolReg core.ToolRegistry, hooks *core.Hooks, cfg core.Config, secrets core.Secrets, home, defaultWorkspace string) *Registry {
	root, cancel := context.WithCancel(context.Background())
	if hooks == nil {
		hooks = core.NewHooks()
	}
	r := &Registry{
		root:       root,
		cancelRoot: cancel,
		store:      store,
		llm:        llm,
		tools:      toolReg,
		hooks:      hooks,
		config:     cfg,
		secrets:    secrets,
		home:       home,
		defaultWS:  defaultWorkspace,
		runners:    map[string]*Runner{},
		turns:      map[string]*turnState{},
		hub:        NewHub(),
	}
	r.pipeline = NewToolRunner(hooks, toolReg)
	installDefaults(hooks, llm)
	return r
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
			return nil, &core.ProviderError{Class: "client", Message: "target provider kosong"}
		}
		target := in.Targets[0]
		provider, ok := llm.Provider(target.Provider)
		if !ok {
			return nil, &core.ProviderError{Class: "client", Message: "provider tidak dikenal: " + target.Provider}
		}
		req := in.Request
		req.Model = target.Model
		return provider.Stream(ctx, req)
	})
}

func (r *Registry) runner(sessionID string) *Runner {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runnerLocked(sessionID)
}

func (r *Registry) runnerLocked(sessionID string) *Runner {
	if existing := r.runners[sessionID]; existing != nil {
		return existing
	}
	runner := &Runner{reg: r, sessionID: sessionID}
	r.runners[sessionID] = runner
	return runner
}

func (r *Registry) newTurn(turnID string) *turnState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := &turnState{done: make(chan struct{})}
	r.turns[turnID] = state
	return state
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

// Wait menunggu turn selesai.
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

// Start memulai turn baru.
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
	turnID := id.New()
	turnCtx, cancel := context.WithCancel(r.root)
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

// Send mengirim pesan; idle memulai turn, sibuk masuk inbox.
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

// Cancel membatalkan turn berjalan.
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

// SetAgent memilih agent untuk session.
func (r *Registry) SetAgent(ctx context.Context, sessionID, agentName string) error {
	if err := r.sessionExists(ctx, sessionID); err != nil {
		return err
	}
	agent, ok := r.config.Agents[agentName]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAgent, agentName)
	}
	payload := core.ModelSelection{V: core.PayloadVersion, Provider: agent.Provider, Model: agent.Model, Agent: agentName}
	return r.appendEvent(ctx, sessionID, core.KindModelSelection, "", payload)
}

// SetModel memilih provider:model untuk session.
func (r *Registry) SetModel(ctx context.Context, sessionID, model string) error {
	if err := r.sessionExists(ctx, sessionID); err != nil {
		return err
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

// SubscribeLive berlangganan frame hidup satu session.
func (r *Registry) SubscribeLive(sessionID string) (*core.Subscription[core.LiveFrame], error) {
	return r.hub.Subscribe(sessionID), nil
}

func (r *Registry) appendEvent(ctx context.Context, sessionID, kind, turnID string, payload any) error {
	events := &Events{Store: r.store, SessionID: sessionID}
	_, err := events.Append(ctx, core.EventDraft{Kind: kind, TurnID: turnID, Payload: payload})
	return err
}

// Close membatalkan semua turn dan menunggu selesai.
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
		return fmt.Errorf("shutdown_timeout: turn tidak selesai")
	}
	r.hub.Close()
	return nil
}

// Names mengembalikan agent terdaftar, terurut.
func (r *Registry) AgentNames() []string {
	names := make([]string, 0, len(r.config.Agents))
	for name := range r.config.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

var _ core.AgentRegistry = (*Registry)(nil)
