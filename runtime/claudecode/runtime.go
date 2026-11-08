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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	// TempDir holds the --mcp-config files (API-CCT-014); "" is
	// os.TempDir().
	TempDir string
	// SignInTimeout and SignOutTimeout bound `claude auth login` and
	// `claude auth logout` (REQ-CCT-012).
	SignInTimeout  time.Duration
	SignOutTimeout time.Duration
}

// Runtime is the claude-code AgentRuntime.
type Runtime struct {
	deps core.RuntimeDeps
	log  *slog.Logger
	opts Options
	sem  *fifoSem

	mu        sync.Mutex
	procs     map[string]*process  // live processes by lana session ID
	starting  map[string]bool      // sessions whose process is starting
	claudeIDs map[string]claudeRef // Claude session by lana session ID
	closed    bool
	// mcpFiles are the --mcp-config files of live processes (API-CCT-014).
	mcpFiles map[string]bool
	// accounts are the running and queued turns per account (API-CCT-011).
	accounts map[string]*accountCounts
	// authRuns cancel the running sign-in or sign-out per account
	// (REQ-CCT-012).
	authRuns map[string]context.CancelFunc

	accountStatusMu sync.Mutex
	accountStatus   map[string]cachedAccountStatus

	versionMu sync.Mutex
	versions  map[string]versionInfo

	statusMu   sync.Mutex
	status     core.RuntimeStatus
	statusAt   time.Time
	lastBinary string

	// models is the model list of the last initialize response (nil: none
	// yet). Only the models part is read; the account part is never kept.
	modelsMu sync.Mutex
	models   []core.RuntimeModel
}

var (
	_ core.AgentRuntime       = (*Runtime)(nil)
	_ core.RuntimeModelLister = (*Runtime)(nil)
)

// EarlyError is the error RunTurn returns when a turn failed before its first
// content event (REQ-CCT-004).
type EarlyError = core.RuntimeEarlyError

// Early failure classes (router outcome classes of the call record).
const (
	classNotInstalled  = core.ErrorClassNotFound
	classVersion       = "client"
	classAuth          = "auth"
	classMissingSecret = "missing_secret"
	classRateLimit     = "rate_limit"
	classRuntime       = "runtime"
)

// Models returns the models the last initialize response listed (the scope
// addition to spec 39); ok is false before the first process started.
func (r *Runtime) Models() ([]core.RuntimeModel, bool) {
	r.modelsMu.Lock()
	defer r.modelsMu.Unlock()
	if r.models == nil {
		return nil, false
	}
	return append([]core.RuntimeModel(nil), r.models...), true
}

// setModels stores the models of an initialize response and reports them
// through RuntimeDeps.OnModels when they changed.
func (r *Runtime) setModels(list []core.RuntimeModel) {
	if list == nil {
		return
	}
	r.modelsMu.Lock()
	same := len(list) == len(r.models)
	for i := 0; same && i < len(list); i++ {
		same = list[i] == r.models[i]
	}
	r.models = append([]core.RuntimeModel{}, list...)
	r.modelsMu.Unlock()
	if !same && r.deps.OnModels != nil {
		r.deps.OnModels(append([]core.RuntimeModel(nil), list...))
	}
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
	if opts.SignOutTimeout <= 0 {
		opts.SignOutTimeout = DefaultSignOutTimeout
	}
	log := deps.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Runtime{
		deps: deps, log: log, opts: opts, sem: newFIFOSem(opts.MaxConcurrent),
		procs: map[string]*process{}, starting: map[string]bool{}, claudeIDs: map[string]claudeRef{},
		versions: map[string]versionInfo{}, mcpFiles: map[string]bool{}, accounts: map[string]*accountCounts{},
		accountStatus: map[string]cachedAccountStatus{}, authRuns: map[string]context.CancelFunc{},
	}
}

// claudeRef is the Claude session a lana session ran last, with its account:
// a session of one account is never resumed on another (REQ-CCT-010).
type claudeRef struct {
	id      string
	account string
}

// accountCounts are the turns of one account that run or wait for a slot.
type accountCounts struct {
	running, queued int
}

// accountName is the account of a turn config: "" is the built-in one.
func accountName(cfg core.RuntimeConfig) string {
	if cfg.Account == "" {
		return core.ClaudeCodeProvider
	}
	return cfg.Account
}

// countTurn moves the account's counts by the given amounts.
func (r *Runtime) countTurn(account string, running, queued int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.accounts[account]
	if c == nil {
		c = &accountCounts{}
		r.accounts[account] = c
	}
	c.running += running
	c.queued += queued
}

// accountTurns returns the running and queued turns of an account.
func (r *Runtime) accountTurns(account string) (running, queued int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.accounts[account]; c != nil {
		return c.running, c.queued
	}
	return 0, 0
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

// RunTurn runs one turn on the session's process, starting it when needed. A
// failure before the turn's first content event returns an *EarlyError
// (REQ-CCT-004).
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
		return fail(classRuntime, "Claude Code runtime is shut down")
	}
	account := accountName(turn.Config)
	bin, err := r.resolveBinary(turn.Config.Binary, account == core.ClaudeCodeProvider)
	if err != nil {
		return fail(classNotInstalled, err.Error())
	}
	if _, err := r.checkVersion(ctx, bin); err != nil {
		if ctx.Err() != nil {
			return cancelled()
		}
		return fail(classVersion, err.Error())
	}
	env, apiKey, err := buildEnv(r.opts.Environ(), turn.Config, r.deps.Secrets)
	if err != nil {
		return fail(classMissingSecret, err.Error())
	}
	dir, err := workspaceDir(turn.Config.Workspace)
	if err != nil {
		return fail("client", err.Error())
	}
	r.countTurn(account, 0, 1)
	if err := r.sem.Acquire(ctx); err != nil {
		r.countTurn(account, 0, -1)
		return cancelled()
	}
	r.countTurn(account, 1, -1)
	defer func() {
		r.sem.Release()
		r.countTurn(account, -1, 0)
	}()

	// A change of the prompt head or the lana tool set starts a new process
	// that resumes the Claude session (REQ-CCT-005). The account is part of
	// the key, so a process never serves two accounts (REQ-CCT-010).
	promptSum := sha256.Sum256([]byte(turn.Config.AppendSystemPrompt))
	key := strings.Join([]string{account, turn.Config.ConfigDir, bin, dir, turn.Config.Auth, turn.Config.APIKeyEnv, turn.Config.Model, turn.Config.Effort,
		hex.EncodeToString(promptSum[:8]), turn.Config.MCPKey}, "\x00")
	p, err := r.acquireProcess(ctx, turn, launch{bin: bin, env: env, dir: dir, key: key, apiKey: apiKey})
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

// errBusy is returned when the session already has a running Claude turn.
var errBusy = errors.New("this session already has a running Claude turn")

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
			p.stop(r.opts.KillGrace, r.opts.KillGrace)
		default:
			r.beginTurn(p)
			r.mu.Unlock()
			return p, nil
		}
	}
	r.evictIdleLocked()
	account := accountName(turn.Config)
	claudeID, resume := turn.ResumeID, true
	if !isUUID(claudeID) {
		if claudeID != "" {
			r.log.Warn("claude: ignoring invalid resume ID", "session", sid)
		}
		claudeID = ""
		// REQ-CCT-010: a Claude session of another account is not resumed.
		if ref, ok := r.claudeIDs[sid]; ok && ref.account == account {
			claudeID = ref.id
		}
	}
	if claudeID == "" {
		claudeID, resume = newUUID(), false
	}
	r.claudeIDs[sid] = claudeRef{id: claudeID, account: account}
	r.starting[sid] = true
	r.mu.Unlock()

	p, err := r.startSessionProcess(ctx, turn, l, sid, claudeID, resume)

	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.starting, sid)
	if err != nil {
		return nil, err
	}
	if r.closed {
		p.stop(0, r.opts.KillGrace)
		return nil, &TurnError{Message: "Claude Code runtime is shut down"}
	}
	r.procs[sid] = p
	r.beginTurn(p)
	return p, nil
}

func (r *Runtime) startSessionProcess(ctx context.Context, turn core.RuntimeTurn, l launch, sid, claudeID string, resume bool) (*process, error) {
	mcpConfig, release := "", func() {}
	if turn.OpenMCP != nil {
		config, rel, err := turn.OpenMCP()
		if rel == nil {
			rel = func() {}
		}
		switch {
		case err != nil:
			r.log.Warn("claude: lana tools are not available to this process", "session", sid, "error", err)
		default:
			// API-CCT-014: the config (with the session token) goes in a
			// 0600 file that is removed when the process exits.
			path, err := r.writeMCPConfig(config)
			if err != nil {
				rel()
				r.log.Warn("claude: lana tools are not available to this process", "session", sid, "error", err)
				break
			}
			mcpConfig = path
			release = func() {
				r.removeMCPConfig(path)
				rel()
			}
		}
	}
	args, err := buildArgs(turn.Config, claudeID, resume, mcpConfig)
	if err != nil {
		release()
		return nil, err
	}
	p, err := startProcess(l.bin, args, l.env, l.dir, sid, l.key, l.apiKey, r.onExit)
	if err != nil {
		release()
		return nil, err
	}
	// REQ-CCT-006: the session's MCP token lives as long as the process.
	go func() {
		<-p.done
		release()
	}()
	p.state.claudeID = claudeID
	ictx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()
	resp, err := p.request(ictx, map[string]any{"subtype": "initialize"})
	if err != nil {
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
	r.setModels(parseInitModels(resp.Response))
	r.log.Debug("claude: process started", "session", sid, "resume", resume)
	return p, nil
}

// initModel is one entry of the initialize response's models list. The
// response also holds account data, which lana never decodes.
type initModel struct {
	Value       string `json:"value"`
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// parseInitModels reads only the models list of an initialize response. It
// returns nil when the response has none.
func parseInitModels(raw json.RawMessage) []core.RuntimeModel {
	var body struct {
		Models []initModel `json:"models"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &body) != nil || body.Models == nil {
		return nil
	}
	out := make([]core.RuntimeModel, 0, len(body.Models))
	for _, m := range body.Models {
		value := firstNonEmpty(m.Value, m.ID)
		if value == "" || strings.HasPrefix(value, "-") {
			continue
		}
		out = append(out, core.RuntimeModel{
			Value:       value,
			DisplayName: truncate(firstNonEmpty(m.DisplayName, m.Name, value), 80),
			Description: truncate(m.Description, 200),
		})
	}
	return out
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
// result, a cancel, or the process exit. It returns the end event and, for a
// failure, its outcome class ("" = runtime).
func (r *Runtime) serveTurn(ctx context.Context, p *process, turn core.RuntimeTurn, emit func(core.RuntimeEvent)) (core.RuntimeEvent, string) {
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
				r.claudeIDs[p.sessionID] = claudeRef{id: id, account: accountName(turn.Config)}
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
	finish := func(res *resultLine) (core.RuntimeEvent, string) {
		if res == nil {
			if isInterrupted() {
				return core.RuntimeEvent{StopReason: core.RuntimeStopCancelled}, ""
			}
			<-p.done
			return core.RuntimeEvent{StopReason: core.RuntimeStopError, IsError: true, ErrorText: p.crashText()}, classRuntime
		}
		emit(m.usageEvent(res))
		ev := m.endEvent(res, isInterrupted())
		switch {
		case !ev.IsError:
			return ev, ""
		case m.authFailed || ev.ErrorText == notLoggedInMessage:
			return ev, classAuth
		case m.rejected != "":
			return ev, classRateLimit
		}
		return ev, classRuntime
	}
	// rejected ends a turn whose plan window was rejected before any output
	// (REQ-CCT-004): the process is stopped, so a late line of this turn
	// cannot reach the next one, which resumes the Claude session.
	rejected := func() (core.RuntimeEvent, string) {
		p.stop(0, r.opts.KillGrace)
		return core.RuntimeEvent{StopReason: core.RuntimeStopError, IsError: true, ErrorText: m.rejectedText()}, classRateLimit
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
		return core.RuntimeEvent{StopReason: core.RuntimeStopError, IsError: true, ErrorText: p.crashText()}, classRuntime
	}
	ctxDone := ctx.Done()
	for {
		select {
		case line := <-t.lines:
			if res := handle(line); res != nil {
				return finish(res)
			}
			if m.rejectedEarly() {
				return rejected()
			}
		case <-p.done:
			// The reader queued every line before the exit; drain them.
			for {
				select {
				case line := <-t.lines:
					if res := handle(line); res != nil {
						return finish(res)
					}
					if m.rejectedEarly() {
						return rejected()
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

// writeMCPConfig writes an --mcp-config value to a new 0600 file in TempDir
// and returns its path (API-CCT-014).
func (r *Runtime) writeMCPConfig(config string) (string, error) {
	dir := r.opts.TempDir
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "lana-mcp-*.json")
	if err != nil {
		return "", fmt.Errorf("write MCP config: %w", err)
	}
	path := f.Name()
	r.mu.Lock()
	r.mcpFiles[path] = true
	r.mu.Unlock()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		r.removeMCPConfig(path)
		return "", fmt.Errorf("write MCP config: %w", err)
	}
	if _, err := f.WriteString(config); err != nil {
		_ = f.Close()
		r.removeMCPConfig(path)
		return "", fmt.Errorf("write MCP config: %w", err)
	}
	if err := f.Close(); err != nil {
		r.removeMCPConfig(path)
		return "", fmt.Errorf("write MCP config: %w", err)
	}
	return path, nil
}

// removeMCPConfig deletes one --mcp-config file. The file goes before its
// entry, so a concurrent removeMCPConfigs that no longer sees the entry never
// returns while the file is still on disk.
func (r *Runtime) removeMCPConfig(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		r.log.Warn("claude: remove MCP config", "error", err)
	}
	r.mu.Lock()
	delete(r.mcpFiles, path)
	r.mu.Unlock()
}

// removeMCPConfigs deletes every --mcp-config file still on disk.
func (r *Runtime) removeMCPConfigs() {
	r.mu.Lock()
	paths := make([]string, 0, len(r.mcpFiles))
	for path := range r.mcpFiles {
		paths = append(paths, path)
	}
	r.mu.Unlock()
	for _, path := range paths {
		r.removeMCPConfig(path)
	}
}

// Shutdown stops every process and waits for them to exit or ctx to end.
// The --mcp-config files are removed either way.
func (r *Runtime) Shutdown(ctx context.Context) error {
	defer r.removeMCPConfigs()
	r.mu.Lock()
	r.closed = true
	procs := make([]*process, 0, len(r.procs))
	for _, p := range r.procs {
		procs = append(procs, p)
	}
	r.procs = map[string]*process{}
	r.mu.Unlock()
	r.cancelAuthRuns()
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

// resolveBinary returns the binary path: the configured one, or `claude` on
// PATH. builtin marks a turn of the built-in account, whose binary Status
// reports.
func (r *Runtime) resolveBinary(configured string, builtin bool) (string, error) {
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
	if !builtin {
		return bin, nil
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
