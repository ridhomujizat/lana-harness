// Package claudecode runs the user's installed Claude Code as a lana agent
// runtime over headless stream-json (ADR 0018, spec 38).
//
// lana starts the unmodified `claude` binary, one process per lana session,
// and translates its stream into core.RuntimeEvent values. lana never reads,
// copies, or stores Claude credentials and never calls Anthropic endpoints.
package claudecode

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Name is the runtime name agents use: runtime = "claude-code".
const Name = "claude-code"

// Defaults of Options (ADR 0018, spec 38 Q-001, Q-002).
const (
	DefaultMaxConcurrent  = 2
	DefaultIdleTimeout    = 10 * time.Minute
	DefaultInterruptGrace = 5 * time.Second
	DefaultKillGrace      = 2 * time.Second
	DefaultStatusTTL      = 60 * time.Second
	initTimeout           = 60 * time.Second
)

func init() {
	core.RegisterRuntime(Name, func(deps core.RuntimeDeps) (core.AgentRuntime, error) {
		return New(deps, Options{}), nil
	})
}

// Options tune a Runtime. Zero values use the defaults.
type Options struct {
	// MaxConcurrent caps the turns running at once across the daemon; more
	// turns wait in arrival order.
	MaxConcurrent int
	// IdleTimeout stops a process after a turn when no new turn arrives.
	IdleTimeout time.Duration
	// InterruptGrace is the wait after an interrupt request before SIGTERM.
	InterruptGrace time.Duration
	// KillGrace is the wait after SIGTERM before SIGKILL.
	KillGrace time.Duration
	// StatusTTL caches Status probes and version checks.
	StatusTTL time.Duration
	// LookPath finds the binary when RuntimeConfig.Binary is empty.
	LookPath func(string) (string, error)
	// Environ returns the daemon environment the child env is built from.
	Environ func() []string
}

// Runtime is the claude-code AgentRuntime.
type Runtime struct {
	deps core.RuntimeDeps
	log  *slog.Logger
	opts Options
	sem  *fifoSem

	mu        sync.Mutex
	procs     map[string]*process // live processes by lana session ID
	starting  map[string]bool     // sessions whose process is starting
	claudeIDs map[string]string   // Claude session UUID by lana session ID
	closed    bool

	versionMu sync.Mutex
	versions  map[string]versionInfo

	statusMu   sync.Mutex
	status     core.RuntimeStatus
	statusAt   time.Time
	lastBinary string
}

var _ core.AgentRuntime = (*Runtime)(nil)

// New returns a Runtime.
func New(deps core.RuntimeDeps, opts Options) *Runtime {
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = DefaultMaxConcurrent
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = DefaultIdleTimeout
	}
	if opts.InterruptGrace <= 0 {
		opts.InterruptGrace = DefaultInterruptGrace
	}
	if opts.KillGrace <= 0 {
		opts.KillGrace = DefaultKillGrace
	}
	if opts.StatusTTL <= 0 {
		opts.StatusTTL = DefaultStatusTTL
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.Environ == nil {
		opts.Environ = os.Environ
	}
	log := deps.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Runtime{
		deps: deps, log: log, opts: opts, sem: newFIFOSem(opts.MaxConcurrent),
		procs: map[string]*process{}, starting: map[string]bool{}, claudeIDs: map[string]string{},
		versions: map[string]versionInfo{},
	}
}

// TurnError is the error RunTurn returns for a failed turn.
type TurnError struct {
	Message string
}

func (e *TurnError) Error() string { return e.Message }

// userError returns an error whose text is shown to the owner as is. The text
// names the product, so it starts with a capital letter.
func userError(format string, args ...any) error {
	if len(args) == 0 {
		return &TurnError{Message: format}
	}
	return &TurnError{Message: fmt.Sprintf(format, args...)}
}

// errNotInstalled is the turn error when the binary is missing.
const errNotInstalled = "Claude Code is not installed or not on PATH"

// RunTurn runs one turn on the session's process, starting it when needed.
func (r *Runtime) RunTurn(ctx context.Context, turn core.RuntimeTurn) error {
	emit := turn.Emit
	if emit == nil {
		emit = func(core.RuntimeEvent) {}
	}
	end := func(ev core.RuntimeEvent) error {
		ev.Kind = core.RuntimeEnd
		emit(ev)
		if ev.IsError {
			return &TurnError{Message: ev.ErrorText}
		}
		return nil
	}
	fail := func(msg string) error {
		return end(core.RuntimeEvent{StopReason: core.RuntimeStopError, IsError: true, ErrorText: msg})
	}
	cancelled := func() error { return end(core.RuntimeEvent{StopReason: core.RuntimeStopCancelled}) }

	if turn.SessionID == "" {
		return fail("runtime turn has no session ID")
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return fail("Claude Agent runtime is shut down")
	}
	bin, err := r.resolveBinary(turn.Config.Binary)
	if err != nil {
		return fail(err.Error())
	}
	if _, err := r.checkVersion(ctx, bin); err != nil {
		if ctx.Err() != nil {
			return cancelled()
		}
		return fail(err.Error())
	}
	env, apiKey, err := buildEnv(r.opts.Environ(), turn.Config, r.deps.Secrets)
	if err != nil {
		return fail(err.Error())
	}
	dir, err := workspaceDir(turn.Config.Workspace)
	if err != nil {
		return fail(err.Error())
	}
	if err := r.sem.Acquire(ctx); err != nil {
		return cancelled()
	}
	defer r.sem.Release()

	key := strings.Join([]string{bin, dir, turn.Config.Auth, turn.Config.APIKeyEnv, turn.Config.Model, turn.Config.Effort}, "\x00")
	p, err := r.acquireProcess(ctx, turn, launch{bin: bin, env: env, dir: dir, key: key, apiKey: apiKey})
	if err != nil {
		if ctx.Err() != nil {
			return cancelled()
		}
		return fail(err.Error())
	}
	ev := r.serveTurn(ctx, p, turn, emit)
	return end(ev)
}

// acquireProcess returns the session's live process with a new active turn,
// starting a process when there is none or its config changed.
// launch is what a new process for a turn needs.
type launch struct {
	bin    string
	env    []string
	dir    string
	key    string // config key; a change starts a new process
	apiKey string
}

func (r *Runtime) acquireProcess(ctx context.Context, turn core.RuntimeTurn, l launch) (*process, error) {
	sid := turn.SessionID
	r.mu.Lock()
	if r.starting[sid] {
		r.mu.Unlock()
		return nil, errors.New("this session already has a running Claude turn")
	}
	if p := r.procs[sid]; p != nil {
		p.mu.Lock()
		busy, stopping := p.turn != nil, p.stopping
		p.mu.Unlock()
		switch {
		case busy:
			r.mu.Unlock()
			return nil, errors.New("this session already has a running Claude turn")
		case p.exited() || stopping || p.key != l.key:
			delete(r.procs, sid)
			p.stop(r.opts.KillGrace, r.opts.KillGrace)
		default:
			r.beginTurn(p)
			r.mu.Unlock()
			return p, nil
		}
	}
	r.evictIdleLocked()
	claudeID, resume := turn.ResumeID, true
	if !isUUID(claudeID) {
		if claudeID != "" {
			r.log.Warn("claude: ignoring invalid resume ID", "session", sid)
		}
		claudeID = r.claudeIDs[sid]
	}
	if claudeID == "" {
		claudeID, resume = newUUID(), false
	}
	r.claudeIDs[sid] = claudeID
	r.starting[sid] = true
	r.mu.Unlock()

	p, err := r.startSessionProcess(ctx, turn.Config, l, sid, claudeID, resume)

	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.starting, sid)
	if err != nil {
		return nil, err
	}
	if r.closed {
		p.stop(0, r.opts.KillGrace)
		return nil, &TurnError{Message: "Claude Agent runtime is shut down"}
	}
	r.procs[sid] = p
	r.beginTurn(p)
	return p, nil
}

func (r *Runtime) startSessionProcess(ctx context.Context, cfg core.RuntimeConfig, l launch, sid, claudeID string, resume bool) (*process, error) {
	args, err := buildArgs(cfg, claudeID, resume)
	if err != nil {
		return nil, err
	}
	p, err := startProcess(l.bin, args, l.env, l.dir, sid, l.key, l.apiKey, r.onExit)
	if err != nil {
		return nil, err
	}
	p.state.claudeID = claudeID
	ictx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()
	if _, err := p.request(ictx, map[string]any{"subtype": "initialize"}); err != nil {
		p.stop(0, r.opts.KillGrace)
		if errors.Is(err, errProcessExited) {
			<-p.done
			return nil, errors.New(p.crashText())
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, userError("Claude Code did not initialize: %v", err)
	}
	r.log.Debug("claude: process started", "session", sid, "resume", resume)
	return p, nil
}

// beginTurn marks p busy with a new turn. r.mu is held.
func (r *Runtime) beginTurn(p *process) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idle != nil {
		p.idle.Stop()
		p.idle = nil
	}
	p.turn = &activeTurn{lines: make(chan []byte, 256), done: make(chan struct{})}
	p.lastUsed = time.Now()
}

// endTurn clears p's turn and arms the idle timer.
func (r *Runtime) endTurn(p *process) {
	p.mu.Lock()
	t := p.turn
	p.turn = nil
	p.lastUsed = time.Now()
	if t != nil {
		for _, timer := range t.timers {
			timer.Stop()
		}
		close(t.done)
	}
	if !p.stopping && !p.exited() {
		p.idle = time.AfterFunc(r.opts.IdleTimeout, func() { r.expire(p) })
	}
	p.mu.Unlock()
}

// expire stops an idle process.
func (r *Runtime) expire(p *process) {
	r.mu.Lock()
	p.mu.Lock()
	idle := p.turn == nil
	p.mu.Unlock()
	if !idle || r.procs[p.sessionID] != p {
		r.mu.Unlock()
		return
	}
	delete(r.procs, p.sessionID)
	r.mu.Unlock()
	r.log.Debug("claude: idle process stopped", "session", p.sessionID)
	p.stop(r.opts.KillGrace, r.opts.KillGrace)
}

// evictIdleLocked stops idle processes, oldest first, until a new process fits
// under MaxConcurrent. r.mu is held.
func (r *Runtime) evictIdleLocked() {
	for {
		live := 0
		var oldest *process
		var oldestUsed time.Time
		for _, p := range r.procs {
			if p.exited() {
				continue
			}
			live++
			p.mu.Lock()
			idle, used := p.turn == nil, p.lastUsed
			p.mu.Unlock()
			if idle && (oldest == nil || used.Before(oldestUsed)) {
				oldest, oldestUsed = p, used
			}
		}
		if live+len(r.starting) < r.opts.MaxConcurrent || oldest == nil {
			return
		}
		delete(r.procs, oldest.sessionID)
		oldest.stop(0, r.opts.KillGrace)
	}
}

// onExit forgets a process that exited.
func (r *Runtime) onExit(p *process) {
	r.mu.Lock()
	if r.procs[p.sessionID] == p {
		delete(r.procs, p.sessionID)
	}
	r.mu.Unlock()
	p.mu.Lock()
	if p.idle != nil {
		p.idle.Stop()
	}
	p.mu.Unlock()
}

// serveTurn writes the user message and maps the stream until the turn's
// result, a cancel, or the process exit. It returns the end event.
func (r *Runtime) serveTurn(ctx context.Context, p *process, turn core.RuntimeTurn, emit func(core.RuntimeEvent)) core.RuntimeEvent {
	p.mu.Lock()
	t := p.turn
	p.mu.Unlock()
	askCtx, cancelAsks := context.WithCancel(ctx)
	p.mu.Lock()
	t.cancelAsks = cancelAsks
	p.mu.Unlock()
	asks := map[string]context.CancelFunc{}
	defer func() {
		cancelAsks()
		if id := p.state.claudeID; id != "" {
			r.mu.Lock()
			if r.procs[p.sessionID] == p || r.procs[p.sessionID] == nil {
				r.claudeIDs[p.sessionID] = id
			}
			r.mu.Unlock()
		}
		r.endTurn(p)
	}()

	m := &mapper{emit: emit, log: r.log, proc: &p.state}
	isInterrupted := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return t.interrupted
	}
	finish := func(res *resultLine) core.RuntimeEvent {
		if res == nil {
			if isInterrupted() {
				return core.RuntimeEvent{StopReason: core.RuntimeStopCancelled}
			}
			<-p.done
			return core.RuntimeEvent{StopReason: core.RuntimeStopError, IsError: true, ErrorText: p.crashText()}
		}
		emit(m.usageEvent(res))
		return m.endEvent(res, isInterrupted())
	}
	handle := func(line []byte) *resultLine {
		lr := m.handle(line)
		switch {
		case lr.result != nil:
			return lr.result
		case lr.canUseTool != nil:
			r.answerPermission(askCtx, p, turn, lr.controlID, *lr.canUseTool, asks)
		case lr.otherControl != "":
			r.log.Debug("claude: unsupported control request", "subtype", lr.otherControl)
			_ = p.write(controlErrorLine(lr.controlID, "unsupported control request: "+lr.otherControl))
		case lr.cancelControl != "":
			if cancel := asks[lr.cancelControl]; cancel != nil {
				cancel()
			}
		}
		return nil
	}

	if err := p.write(userMessageLine(turn.Input)); err != nil {
		p.stop(0, r.opts.KillGrace)
		<-p.done
		return core.RuntimeEvent{StopReason: core.RuntimeStopError, IsError: true, ErrorText: p.crashText()}
	}
	ctxDone := ctx.Done()
	for {
		select {
		case line := <-t.lines:
			if res := handle(line); res != nil {
				return finish(res)
			}
		case <-p.done:
			// The reader queued every line before the exit; drain them.
			for {
				select {
				case line := <-t.lines:
					if res := handle(line); res != nil {
						return finish(res)
					}
				default:
					return finish(nil)
				}
			}
		case <-ctxDone:
			ctxDone = nil
			r.interrupt(p)
		}
	}
}

// answerPermission asks turn.Approve in its own goroutine and writes the
// control response. A missing approver, a panic, or a cancelled context denies.
func (r *Runtime) answerPermission(ctx context.Context, p *process, turn core.RuntimeTurn, requestID string, req canUseTool, asks map[string]context.CancelFunc) {
	actx, cancel := context.WithCancel(ctx)
	asks[requestID] = cancel
	permTool, permInput := PermCall(req.ToolName, req.Input)
	approval := core.RuntimeApproval{
		CallID: req.ToolUseID, ToolName: req.ToolName, Input: req.Input, Risk: ToolRisk(req.ToolName),
		Rule: RuleString(req.ToolName, req.Input), PermTool: permTool, PermInput: permInput,
		Description: req.Description, Reason: req.DecisionReason,
	}
	go func() {
		defer cancel()
		allow, message := false, "no approver is available"
		if turn.Approve != nil {
			func() {
				defer func() {
					if v := recover(); v != nil {
						allow, message = false, "approval failed"
						r.log.Error("claude: approve panicked", "panic", v)
					}
				}()
				allow, message = turn.Approve(actx, approval)
			}()
		}
		if actx.Err() != nil {
			allow = false
			if message == "" {
				message = "the request was cancelled"
			}
		}
		_ = p.write(controlSuccessLine(requestID, permissionResponse(allow, message, req.Input)))
	}()
}

// Interrupt asks the session's running turn to stop.
func (r *Runtime) Interrupt(sessionID string) error {
	r.mu.Lock()
	p := r.procs[sessionID]
	r.mu.Unlock()
	if p != nil {
		r.interrupt(p)
	}
	return nil
}

// interrupt sends an interrupt control request to p's running turn, then
// SIGTERM after InterruptGrace and SIGKILL after KillGrace more if the turn has
// not ended.
func (r *Runtime) interrupt(p *process) {
	p.mu.Lock()
	t := p.turn
	if t == nil || t.interrupted {
		p.mu.Unlock()
		return
	}
	t.interrupted = true
	if t.cancelAsks != nil {
		t.cancelAsks()
	}
	stillRunning := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.turn == t
	}
	t.timers = append(t.timers, time.AfterFunc(r.opts.InterruptGrace, func() {
		if !stillRunning() {
			return
		}
		r.log.Debug("claude: interrupt ignored, sending SIGTERM", "session", p.sessionID)
		p.signal(syscall.SIGTERM)
		time.AfterFunc(r.opts.KillGrace, func() {
			if !p.exited() {
				p.signal(syscall.SIGKILL)
			}
		})
	}))
	p.mu.Unlock()
	if err := p.write(controlRequestLine(p.nextID("interrupt"), map[string]any{"subtype": "interrupt"})); err != nil {
		r.log.Debug("claude: interrupt write failed", "err", err)
	}
}

// Close stops the session's process and forgets its Claude session ID.
func (r *Runtime) Close(sessionID string) {
	r.mu.Lock()
	p := r.procs[sessionID]
	delete(r.procs, sessionID)
	delete(r.claudeIDs, sessionID)
	r.mu.Unlock()
	if p != nil {
		r.interrupt(p)
		p.stop(0, r.opts.KillGrace)
	}
}

// Shutdown stops every process and waits for them to exit or ctx to end.
func (r *Runtime) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	procs := make([]*process, 0, len(r.procs))
	for _, p := range r.procs {
		procs = append(procs, p)
	}
	r.procs = map[string]*process{}
	r.mu.Unlock()
	for _, p := range procs {
		r.interrupt(p)
		p.stop(0, r.opts.KillGrace)
	}
	for _, p := range procs {
		select {
		case <-p.done:
		case <-ctx.Done():
			for _, q := range procs {
				q.signal(syscall.SIGKILL)
			}
			return ctx.Err()
		}
	}
	return nil
}

// resolveBinary returns the binary path: the configured one, or `claude` on PATH.
func (r *Runtime) resolveBinary(configured string) (string, error) {
	bin := configured
	if bin == "" {
		path, err := r.opts.LookPath("claude")
		if err != nil {
			return "", userError(errNotInstalled)
		}
		bin = path
	} else {
		if strings.HasPrefix(bin, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				bin = home + bin[1:]
			}
		}
		info, err := os.Stat(bin)
		if err != nil || info.IsDir() {
			return "", userError(errNotInstalled)
		}
	}
	r.statusMu.Lock()
	if r.lastBinary != bin {
		r.lastBinary = bin
		r.statusAt = time.Time{}
	}
	r.statusMu.Unlock()
	return bin, nil
}

// newUUID returns a random version 4 UUID.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
