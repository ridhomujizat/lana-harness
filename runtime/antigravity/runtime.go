// Package antigravity runs the user's installed Antigravity CLI (`agy`) as a
// lana agent runtime over the Agent Client Protocol (ADR 0022, spec 74).
//
// lana starts the unmodified `agy acp` binary, one process per lana session,
// and translates its ACP messages into core.RuntimeEvent values. lana never
// reads, copies, or stores Google credentials and never calls a Google
// endpoint: an account is only its own HOME folder.
package antigravity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// Name is the runtime name agents use: runtime = "antigravity".
const Name = "antigravity"

// Defaults of Options.
const (
	DefaultMaxConcurrent  = 2
	DefaultIdleTimeout    = 10 * time.Minute
	DefaultInterruptGrace = 15 * time.Second
	DefaultKillGrace      = 5 * time.Second
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
	// InterruptGrace is the wait after a cancel request before killing the
	// process group.
	InterruptGrace time.Duration
	// KillGrace is the wait after SIGTERM before SIGKILL.
	KillGrace time.Duration
	// StatusTTL caches the version probe.
	StatusTTL time.Duration
	// LookPath finds the binary when RuntimeConfig.Binary is empty.
	LookPath func(string) (string, error)
	// Environ returns the daemon environment the child env is built from.
	Environ func() []string
	// SignInTimeout bounds `agy acp --login` (REQ-AGY-010); 15 minutes when
	// unset.
	SignInTimeout time.Duration
}

// Runtime is the antigravity AgentRuntime.
type Runtime struct {
	deps core.RuntimeDeps
	log  *slog.Logger
	opts Options
	sem  *fifoSem

	mu       sync.Mutex
	procs    map[string]*process // live processes by lana session ID
	starting map[string]bool     // sessions whose process is starting
	acpIDs   map[string]acpRef   // ACP session by lana session ID
	closed   bool

	// agy caches the per-account status and model probes and holds the
	// running sign-in and usage runs (REQ-AGY-009, REQ-AGY-010).
	agy *agyState

	versionMu sync.Mutex
	versions  map[string]versionInfo
}

var _ core.AgentRuntime = (*Runtime)(nil)

// EarlyError is the error RunTurn returns when a turn failed before its first
// content event (REQ-AGY-008).
type EarlyError = core.RuntimeEarlyError

// Early failure classes (router outcome classes of the call record).
const (
	classNotInstalled = core.ErrorClassNotFound
	classVersion      = "client"
	classAuth         = "auth"
	classRuntime      = "runtime"
)

// TurnError is the error RunTurn returns for a failed turn.
type TurnError struct {
	Message string
}

func (e *TurnError) Error() string { return e.Message }

// errBusy is returned when the session already has a running Antigravity turn.
var errBusy = errors.New("this session already has a running Antigravity turn")

// acpRef is the ACP session a lana session ran last, with its account: a
// session of one account is never resumed on another (REQ-AGY-004).
type acpRef struct {
	id      string
	account string
}

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
	if opts.SignInTimeout <= 0 {
		opts.SignInTimeout = DefaultSignInTimeout
	}
	log := deps.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Runtime{
		deps: deps, log: log, opts: opts, sem: newFIFOSem(opts.MaxConcurrent),
		procs: map[string]*process{}, starting: map[string]bool{}, acpIDs: map[string]acpRef{},
		agy: newAgyState(), versions: map[string]versionInfo{},
	}
}

// accountName is the account of a turn config: "" is the built-in one.
func accountName(cfg core.RuntimeConfig) string {
	if cfg.Account == "" {
		return core.AntigravityProvider
	}
	return cfg.Account
}

// RunTurn runs one turn on the session's process, starting it when needed. A
// failure before the turn's first content event returns an *EarlyError
// (REQ-AGY-008).
func (r *Runtime) RunTurn(ctx context.Context, turn core.RuntimeTurn) error {
	emit := turn.Emit
	if emit == nil {
		emit = func(core.RuntimeEvent) {}
	}
	content := false
	tracked := func(ev core.RuntimeEvent) {
		switch ev.Kind {
		case core.RuntimeTextDelta, core.RuntimeThinkingDelta, core.RuntimeAssistantMessage, core.RuntimeToolCall, core.RuntimeToolResult:
			content = true
		}
		emit(ev)
	}
	class := classRuntime
	end := func(ev core.RuntimeEvent) error {
		ev.Kind = core.RuntimeEnd
		emit(ev)
		if !ev.IsError {
			return nil
		}
		if !content {
			return &EarlyError{Class: class, Message: ev.ErrorText}
		}
		return &TurnError{Message: ev.ErrorText}
	}
	fail := func(c, msg string) error {
		class = c
		return end(core.RuntimeEvent{StopReason: core.RuntimeStopError, IsError: true, ErrorText: msg})
	}
	cancelled := func() error { return end(core.RuntimeEvent{StopReason: core.RuntimeStopCancelled}) }

	if turn.SessionID == "" {
		return fail(classRuntime, "runtime turn has no session ID")
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return fail(classRuntime, "Antigravity runtime is shut down")
	}
	bin, err := r.resolveBinary(turn.Config.Binary)
	if err != nil {
		return fail(classNotInstalled, err.Error())
	}
	if _, err := r.checkVersion(ctx, bin); err != nil {
		if ctx.Err() != nil {
			return cancelled()
		}
		return fail(classVersion, err.Error())
	}
	env := buildEnv(r.opts.Environ(), turn.Config)
	dir, err := workspaceDir(turn.Config.Workspace)
	if err != nil {
		return fail("client", err.Error())
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return fail(classRuntime, "Antigravity runtime is shut down")
	}
	r.mu.Unlock()
	if err := r.sem.Acquire(ctx); err != nil {
		return cancelled()
	}
	defer r.sem.Release()

	// A change of the prompt head starts a new process that resumes the ACP
	// session; the account and HOME are part of the key, so a process never
	// serves two accounts (REQ-AGY-002, REQ-AGY-004).
	account := accountName(turn.Config)
	promptSum := sha256.Sum256([]byte(turn.Config.AppendSystemPrompt))
	key := strings.Join([]string{account, turn.Config.Home, bin, dir, hex.EncodeToString(promptSum[:8])}, "\x00")
	p, err := r.acquireProcess(ctx, turn, launch{bin: bin, env: env, dir: dir, key: key, account: account})
	if err != nil {
		if ctx.Err() != nil {
			return cancelled()
		}
		if errors.Is(err, errBusy) {
			content = true // not an early failure: another turn owns the session
		}
		return fail(classRuntime, err.Error())
	}
	ev, evClass := r.serveTurn(ctx, p, turn, tracked)
	if evClass != "" {
		class = evClass
	}
	return end(ev)
}

// launch is what a new process for a turn needs.
type launch struct {
	bin     string
	env     []string
	dir     string
	key     string // config key; a change starts a new process
	account string
}

// acquireProcess returns the session's live process with a new active turn,
// starting a process when there is none or its config changed.
func (r *Runtime) acquireProcess(ctx context.Context, turn core.RuntimeTurn, l launch) (*process, error) {
	sid := turn.SessionID
	r.mu.Lock()
	if r.starting[sid] {
		r.mu.Unlock()
		return nil, errBusy
	}
	if p := r.procs[sid]; p != nil {
		p.mu.Lock()
		busy, stopping := p.turn != nil, p.stopping
		p.mu.Unlock()
		switch {
		case busy:
			r.mu.Unlock()
			return nil, errBusy
		case p.exited() || stopping || p.key != l.key:
			delete(r.procs, sid)
			p.stop(r.opts.KillGrace)
		default:
			r.beginTurn(p)
			r.mu.Unlock()
			return p, nil
		}
	}
	r.evictIdleLocked()
	r.starting[sid] = true
	r.mu.Unlock()

	p, err := r.startSessionProcess(ctx, turn, l, sid)

	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.starting, sid)
	if err != nil {
		return nil, err
	}
	if r.closed {
		p.stop(r.opts.KillGrace)
		return nil, &TurnError{Message: "Antigravity runtime is shut down"}
	}
	r.procs[sid] = p
	r.beginTurn(p)
	return p, nil
}

// startSessionProcess starts the child, opens the MCP host for lana's tools,
// and runs `initialize` once (REQ-AGY-004).
func (r *Runtime) startSessionProcess(ctx context.Context, turn core.RuntimeTurn, l launch, sid string) (*process, error) {
	p, err := startProcess(l.bin, buildArgs(), l.env, l.dir, sid, l.key, r.onExit)
	if err != nil {
		return nil, err
	}
	release := func() {}
	if turn.OpenMCP != nil {
		config, rel, err := turn.OpenMCP()
		if rel == nil {
			rel = func() {}
		}
		switch {
		case err != nil:
			rel()
			r.log.Warn("antigravity: lana tools are not available to this process", "session", sid, "error", err)
		default:
			if servers := convertMCP(config); len(servers) > 0 {
				p.state.mcpServers = servers
				release = rel
			} else {
				rel()
			}
		}
	}
	go func() {
		<-p.done
		release()
	}()
	ictx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()
	resp, err := p.call(ictx, "initialize", initParams())
	if err != nil {
		p.stop(r.opts.KillGrace)
		if errors.Is(err, errProcessExited) {
			<-p.done
			return nil, errors.New(p.crashText())
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, userError("Antigravity did not initialize: %v", err)
	}
	p.state.version = agentVersion(resp)
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
	p.turn = &activeTurn{lines: make(chan []byte, 256), done: make(chan struct{}), handlerDone: make(chan struct{})}
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
	r.log.Debug("antigravity: idle process stopped", "session", p.sessionID)
	p.stop(r.opts.KillGrace)
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
		oldest.stop(r.opts.KillGrace)
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

// serveTurn runs the ACP session of one turn and returns the end event and, for
// a failure, its outcome class ("" = runtime).
func (r *Runtime) serveTurn(ctx context.Context, p *process, turn core.RuntimeTurn, emit func(core.RuntimeEvent)) (core.RuntimeEvent, string) {
	p.mu.Lock()
	t := p.turn
	p.mu.Unlock()
	askCtx, cancelAsks := context.WithCancel(ctx)
	p.mu.Lock()
	t.cancelAsks = cancelAsks
	p.mu.Unlock()
	asks := map[string]context.CancelFunc{}

	// RunTurn promises Emit is called from one goroutine at a time, so the
	// handler goroutine and this one share one mutex.
	var emitMu sync.Mutex
	safeEmit := func(ev core.RuntimeEvent) {
		emitMu.Lock()
		emit(ev)
		emitMu.Unlock()
	}
	m := newMapper(safeEmit, r.log, &p.state, turn.Config.Model)

	go func() {
		defer close(t.handlerDone)
		for {
			select {
			case line := <-t.lines:
				r.handleLine(t, m, askCtx, turn, asks, line, p)
			case <-t.done:
				for {
					select {
					case line := <-t.lines:
						r.handleLine(t, m, askCtx, turn, asks, line, p)
					default:
						return
					}
				}
			}
		}
	}()

	out := r.drive(ctx, p, turn, m, t)

	close(t.done)
	<-t.handlerDone
	cancelAsks()
	if id := p.state.acpSessionID; id != "" {
		r.mu.Lock()
		r.acpIDs[turn.SessionID] = acpRef{id: id, account: accountName(turn.Config)}
		r.mu.Unlock()
	}
	r.endTurn(p)

	// The handler goroutine has drained, so the mapper is only touched here.
	switch {
	case out.sessionErr != nil:
		return r.turnFailure(ctx, p, out.sessionErr, turn, m)
	case out.err != nil:
		if out.interrupted || errors.Is(out.err, context.Canceled) || errors.Is(out.err, context.DeadlineExceeded) || ctx.Err() != nil {
			m.flush()
			return core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: core.RuntimeStopCancelled}, ""
		}
		return r.turnFailure(ctx, p, out.err, turn, m)
	}
	return m.result(out.resp, out.interrupted), ""
}

// turnOutcome is what one turn's session and prompt produced.
type turnOutcome struct {
	resp        json.RawMessage
	err         error
	sessionErr  error
	interrupted bool
}

// drive opens the ACP session, sets the model, and runs the prompt. It does not
// touch the mapper: the caller maps the result after the handler drained.
func (r *Runtime) drive(ctx context.Context, p *process, turn core.RuntimeTurn, m *mapper, t *activeTurn) turnOutcome {
	sessionID, err := r.openSession(ctx, p, turn, m)
	if err != nil {
		return turnOutcome{sessionErr: err}
	}
	if model := turn.Config.Model; model != "" && model != "default" {
		if _, err := p.call(ctx, "session/set_model", setModelParams(sessionID, model)); err != nil {
			return turnOutcome{sessionErr: err}
		}
	}
	type promptOutcome struct {
		resp json.RawMessage
		err  error
	}
	done := make(chan promptOutcome, 1)
	go func() {
		resp, err := p.call(ctx, "session/prompt", promptParams(sessionID, turn.Input))
		done <- promptOutcome{resp: resp, err: err}
	}()
	var out promptOutcome
	select {
	case out = <-done:
	case <-ctx.Done():
		r.interrupt(p)
		select {
		case out = <-done:
		case <-time.After(r.opts.InterruptGrace + r.opts.KillGrace + time.Second):
			out = promptOutcome{err: context.Canceled}
		}
	}
	return turnOutcome{resp: out.resp, err: out.err, interrupted: r.turnInterrupted(p, t)}
}

// openSession sends session/load when lana holds an earlier ACP session for
// this lana session and account, else session/new. A failed load falls back to
// session/new. It emits the session_meta event once (REQ-AGY-004).
func (r *Runtime) openSession(ctx context.Context, p *process, turn core.RuntimeTurn, m *mapper) (string, error) {
	if p.state.sessionCreated {
		return p.state.acpSessionID, nil
	}
	account := accountName(turn.Config)
	resumeID := turn.ResumeID
	if resumeID == "" {
		r.mu.Lock()
		ref, ok := r.acpIDs[turn.SessionID]
		r.mu.Unlock()
		if ok && ref.account == account {
			resumeID = ref.id
		}
	}
	if resumeID != "" {
		resp, err := p.call(ctx, "session/load", loadSessionParams(resumeID, p.dir, p.state.mcpServers, turn.Config.AppendSystemPrompt))
		if err != nil {
			r.log.Warn("antigravity: session/load failed; starting a new conversation", "session", turn.SessionID, "error", err)
		} else {
			return r.adoptSession(p, m, resp, resumeID), nil
		}
	}
	resp, err := p.call(ctx, "session/new", newSessionParams(p.dir, p.state.mcpServers, turn.Config.AppendSystemPrompt))
	if err != nil {
		return "", err
	}
	return r.adoptSession(p, m, resp, ""), nil
}

// adoptSession stores the ACP session of a new/load result, marks the process
// ready for updates, and emits the session_meta event.
func (r *Runtime) adoptSession(p *process, m *mapper, resp json.RawMessage, fallback string) string {
	id := sessionIDOf(resp, fallback)
	p.mu.Lock()
	p.state.acpSessionID = id
	p.state.sessionCreated = true
	p.ready = true
	p.mu.Unlock()
	m.sessionMeta(id, tierOf(resp))
	return id
}

// turnFailure maps a turn error to the end event and its outcome class.
func (r *Runtime) turnFailure(ctx context.Context, p *process, err error, turn core.RuntimeTurn, m *mapper) (core.RuntimeEvent, string) {
	m.flush()
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: core.RuntimeStopCancelled}, ""
	}
	if errors.Is(err, errProcessExited) {
		return core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: core.RuntimeStopError, IsError: true, ErrorText: p.crashText()}, classRuntime
	}
	msg := err.Error()
	if looksNotSignedIn(msg) {
		msg = notSignedInMessage(accountName(turn.Config))
		return core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: core.RuntimeStopError, IsError: true, ErrorText: msg}, classAuth
	}
	return core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: core.RuntimeStopError, IsError: true, ErrorText: msg}, classRuntime
}

// handleLine maps one line of the turn: a session/update notification, or an
// agent request. An unknown agent request is answered with error -32601.
func (r *Runtime) handleLine(t *activeTurn, m *mapper, askCtx context.Context, turn core.RuntimeTurn, asks map[string]context.CancelFunc, line []byte, p *process) {
	var env rpcEnvelope
	if json.Unmarshal(line, &env) != nil {
		return
	}
	if env.Method == "" {
		return
	}
	if env.Method == "session/update" {
		m.handleUpdate(env.Params)
		return
	}
	if len(env.ID) == 0 {
		return
	}
	switch env.Method {
	case "session/request_permission":
		r.answerPermission(askCtx, p, turn, env.ID, env.Params, asks)
	default:
		_ = p.write(rpcErrorLine(env.ID, -32601, "method not found: "+env.Method))
	}
}

// turnInterrupted reports whether the turn was interrupted.
func (r *Runtime) turnInterrupted(p *process, t *activeTurn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return t.interrupted
}

// Interrupt asks the session's running turn to stop (REQ-AGY-007).
func (r *Runtime) Interrupt(sessionID string) error {
	r.mu.Lock()
	p := r.procs[sessionID]
	r.mu.Unlock()
	if p != nil {
		r.interrupt(p)
	}
	return nil
}

// interrupt sends session/cancel and, when the prompt result does not arrive
// within InterruptGrace, kills the process group (REQ-AGY-007).
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
	sessionID := p.state.acpSessionID
	stillRunning := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.turn == t
	}
	t.timers = append(t.timers, time.AfterFunc(r.opts.InterruptGrace, func() {
		if !stillRunning() {
			return
		}
		r.log.Debug("antigravity: interrupt ignored, killing the process group", "session", p.sessionID)
		p.signal(syscall.SIGTERM)
		time.AfterFunc(r.opts.KillGrace, func() {
			if !p.exited() {
				p.signal(syscall.SIGKILL)
			}
		})
	}))
	p.mu.Unlock()
	if sessionID != "" {
		if err := p.notify("session/cancel", cancelParams(sessionID)); err != nil {
			r.log.Debug("antigravity: cancel write failed", "err", err)
		}
	}
}

// Close stops the session's process and forgets its ACP session ID.
func (r *Runtime) Close(sessionID string) {
	r.mu.Lock()
	p := r.procs[sessionID]
	delete(r.procs, sessionID)
	delete(r.acpIDs, sessionID)
	r.mu.Unlock()
	if p != nil {
		r.interrupt(p)
		p.stop(r.opts.KillGrace)
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
	r.cancelProbes()
	for _, p := range procs {
		r.interrupt(p)
		p.stop(r.opts.KillGrace)
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

// resolveBinary returns the binary path: the configured one, or `agy` on PATH.
func (r *Runtime) resolveBinary(configured string) (string, error) {
	bin := configured
	if bin == "" {
		path, err := r.opts.LookPath("agy")
		if err != nil {
			return "", userError(errNotInstalled)
		}
		return path, nil
	}
	if strings.HasPrefix(bin, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			bin = home + bin[1:]
		}
	}
	info, err := os.Stat(bin)
	if err != nil || info.IsDir() {
		return "", userError(errNotInstalled)
	}
	return bin, nil
}

// notSignedInMessage is the turn error for an account that is not signed in.
func notSignedInMessage(account string) string {
	return "Antigravity account " + account + " is not signed in"
}

// looksNotSignedIn reports whether an error text is agy's not-signed-in answer.
func looksNotSignedIn(text string) bool {
	t := strings.ToLower(text)
	for _, s := range []string{"not signed in", "not logged in", "please sign in", "please log in", "sign in to", "authentication failed", "unauthorized", "no credentials"} {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

// clientVersion is the version lana reports in the ACP initialize handshake.
func clientVersion() string { return "dev" }

func initParams() map[string]any {
	return map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo":         map[string]any{"name": "lana", "version": clientVersion()},
	}
}

// sessionMeta returns the _meta of session/new and session/load, or nil when
// there is no prompt head to append.
func sessionMeta(appendPrompt string) map[string]any {
	if appendPrompt == "" {
		return nil
	}
	return map[string]any{"antigravity": map[string]any{"systemPrompt": map[string]any{"append": appendPrompt}}}
}

func newSessionParams(cwd string, servers []acpMCPServer, appendPrompt string) map[string]any {
	params := map[string]any{"cwd": cwd, "mcpServers": mcpServersParam(servers)}
	if meta := sessionMeta(appendPrompt); meta != nil {
		params["_meta"] = meta
	}
	return params
}

func loadSessionParams(sessionID, cwd string, servers []acpMCPServer, appendPrompt string) map[string]any {
	params := newSessionParams(cwd, servers, appendPrompt)
	params["sessionId"] = sessionID
	return params
}

func setModelParams(sessionID, model string) map[string]any {
	return map[string]any{"sessionId": sessionID, "modelId": model}
}

func cancelParams(sessionID string) map[string]any {
	return map[string]any{"sessionId": sessionID}
}

// promptParams renders the user input as ACP prompt blocks: text and image
// blocks (API-AGY-011).
func promptParams(sessionID string, input []core.ContentBlock) map[string]any {
	blocks := make([]map[string]any, 0, len(input))
	for _, b := range input {
		switch b.Kind {
		case core.BlockText:
			if b.Text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": b.Text})
			}
		case core.BlockImage:
			if len(b.Data) == 0 {
				continue
			}
			media := b.MediaType
			if media == "" {
				media = "image/png"
			}
			blocks = append(blocks, map[string]any{"type": "image", "mimeType": media, "data": base64.StdEncoding.EncodeToString(b.Data)})
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": ""})
	}
	return map[string]any{"sessionId": sessionID, "prompt": blocks}
}
