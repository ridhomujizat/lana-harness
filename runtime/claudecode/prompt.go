package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Router prompt runs (spec 78, REQ-RPR-002): one `claude -p` process per
// router call of a Claude Code account, with no tools, no MCP, and no session
// saved for /resume. The prompt goes in on stdin; the stream-json answer is
// mapped with the same mapper agent turns use.

// promptBaseArgs are the flags of every prompt run (REQ-RPR-002), before
// --model. --tools "" turns every built-in tool off, --strict-mcp-config
// without --mcp-config loads no MCP server, --setting-sources project,local
// keeps the owner's user settings (hooks, plugins) out as agent turns do
// (API-CCT-029), and --no-session-persistence keeps the run out of /resume.
var promptBaseArgs = []string{
	"-p",
	"--output-format", "stream-json",
	"--verbose",
	"--include-partial-messages",
}

// promptTailArgs follow --model.
var promptTailArgs = []string{
	"--tools", "",
	"--strict-mcp-config",
	"--setting-sources", "project,local",
	"--no-session-persistence",
}

// classUnknownModel is the class of a run Claude Code refused for its model
// (REQ-RPR-005: client).
const classUnknownModel = "client"

// buildPromptArgs returns the argv after the binary of a prompt run: the
// flags of REQ-RPR-002, then --system-prompt when system is set and --effort
// when the config has one. Each value is one argv element, so it is never
// read as a flag; a model or effort starting with "-" is refused.
func buildPromptArgs(cfg core.RuntimeConfig, system string) ([]string, error) {
	args := append([]string(nil), promptBaseArgs...)
	if cfg.Model != "" {
		if strings.HasPrefix(cfg.Model, "-") {
			return nil, fmt.Errorf("invalid model %q", cfg.Model)
		}
		args = append(args, "--model", cfg.Model)
	}
	args = append(args, promptTailArgs...)
	if system != "" {
		args = append(args, "--system-prompt", system)
	}
	if cfg.Effort != "" {
		if strings.HasPrefix(cfg.Effort, "-") {
			return nil, fmt.Errorf("invalid reasoning_effort %q", cfg.Effort)
		}
		args = append(args, "--effort", cfg.Effort)
	}
	return args, nil
}

// RunPrompt runs one prompt for a router call (spec 78, REQ-RPR-002): `claude
// -p` with the account's environment (as its agent turns get it), the prompt
// on stdin, in run.Dir, in its own process group, killed when ctx ends. It
// takes a slot of the runtime's concurrency limit and counts as a running or
// queued turn of the account (REQ-RPR-006). It never reads a credential.
func (r *Runtime) RunPrompt(ctx context.Context, run core.RuntimePrompt) error {
	emit := run.Emit
	if emit == nil {
		emit = func(core.RuntimeEvent) {}
	}
	early := func(class, msg string) error { return &EarlyError{Class: class, Message: msg} }
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return early(classRuntime, "Claude Code runtime is shut down")
	}
	account := accountName(run.Config)
	bin, err := r.resolveBinary(run.Config.Binary, account == core.ClaudeCodeProvider)
	if err != nil {
		return early(classNotInstalled, err.Error())
	}
	if _, err := r.checkVersion(ctx, bin); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A binary too old to serve the run is treated as missing, so the
		// chain moves on (REQ-RPR-005).
		return early(classNotInstalled, err.Error())
	}
	env, apiKey, err := buildEnv(r.opts.Environ(), run.Config, r.deps.Secrets)
	if err != nil {
		return early(classMissingSecret, err.Error())
	}
	args, err := buildPromptArgs(run.Config, run.System)
	if err != nil {
		return early(classUnknownModel, err.Error())
	}
	r.countTurn(account, 0, 1)
	if err := r.sem.Acquire(ctx); err != nil {
		r.countTurn(account, 0, -1)
		return ctx.Err()
	}
	r.countTurn(account, 1, -1)
	defer func() {
		r.sem.Release()
		r.countTurn(account, -1, 0)
	}()

	cmd := exec.Command(bin, args...)
	cmd.Dir = run.Dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(run.Prompt)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return early(classRuntime, err.Error())
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return early(classRuntime, err.Error())
	}
	if err := cmd.Start(); err != nil {
		return early(classNotInstalled, "start Claude Code: "+err.Error())
	}
	stderr := newTailBuffer(8)
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		stderr.readFrom(stderrPipe)
	}()
	// The process group is killed when ctx ends or the run stops early.
	abort := make(chan struct{})
	exited := make(chan struct{})
	killed := make(chan struct{})
	go func() {
		defer close(killed)
		select {
		case <-ctx.Done():
		case <-abort:
		case <-exited:
			return
		}
		killGroup(cmd.Process.Pid, exited, r.opts.KillGrace)
	}()

	m := &mapper{emit: emit, log: r.log, proc: &procState{}}
	var res *resultLine
	rejected := false
	br := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, rerr := readLine(br)
		if len(bytes.TrimSpace(line)) > 0 && res == nil && !rejected {
			lr := m.handle(line)
			if lr.result != nil {
				res = lr.result
			}
			if m.rejectedEarly() {
				rejected = true
				close(abort)
			}
		}
		if rerr != nil {
			break
		}
	}
	<-stderrDone
	waitErr := cmd.Wait()
	close(exited)
	<-killed

	switch {
	case rejected:
		return &EarlyError{Class: classRateLimit, Message: m.rejectedText(), ResetAt: m.rejectedAt}
	case ctx.Err() != nil:
		return ctx.Err()
	case res == nil:
		msg := redact(stderr.lastLine(), apiKey)
		if msg == "" && m.apiError != "" {
			msg = m.apiError
		}
		switch {
		case msg != "":
			msg = "Claude Code exited: " + truncate(msg, 300)
		case waitErr != nil:
			msg = "Claude Code exited unexpectedly (" + waitErr.Error() + ")"
		default:
			msg = "Claude Code exited unexpectedly"
		}
		return r.promptFailure(m, promptClass(m, msg), msg)
	}
	emit(m.usageEvent(res))
	ev := m.endEvent(res, false)
	if !ev.IsError {
		return nil
	}
	msg := redact(ev.ErrorText, apiKey)
	class := promptClass(m, msg)
	if m.rejected != "" {
		class = classRateLimit
	}
	return r.promptFailure(m, class, truncate(msg, 300))
}

// promptFailure is the error of a failed prompt run: an *EarlyError before
// the first text or tool event, else a *TurnError.
func (r *Runtime) promptFailure(m *mapper, class, msg string) error {
	if m.content {
		return &TurnError{Message: msg}
	}
	resetAt := time.Time{}
	if class == classRateLimit {
		resetAt = m.rejectedAt
	}
	return &EarlyError{Class: class, Message: msg, ResetAt: resetAt}
}

// promptClass maps a failed prompt run to its router class (REQ-RPR-005): not
// signed in is auth, a rejected plan window or a usage limit rate_limit, an
// unknown model client, anything else runtime.
func promptClass(m *mapper, msg string) string {
	switch {
	case m.authFailed || msg == notLoggedInMessage || looksNotLoggedIn(msg):
		return classAuth
	case m.rejected != "" || looksUsageLimit(msg):
		return classRateLimit
	case looksUnknownModel(msg):
		return classUnknownModel
	}
	return classRuntime
}

// looksUsageLimit reports whether a Claude Code error text is a usage or rate
// limit answer.
func looksUsageLimit(text string) bool {
	t := strings.ToLower(text)
	for _, s := range []string{"usage limit", "rate limit", "rate_limit", "limit reached", "too many requests", "overloaded"} {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

// looksUnknownModel reports whether a Claude Code error text refuses the
// model: "There's an issue with the selected model (x). It may not exist or
// you may not have access to it."
func looksUnknownModel(text string) bool {
	t := strings.ToLower(text)
	for _, s := range []string{"issue with the selected model", "may not exist", "model not found", "invalid model", "unknown model", "not_found_error"} {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

// killGroup ends a prompt run's process group: SIGTERM, then SIGKILL after
// grace unless the process exited first.
func killGroup(pid int, exited <-chan struct{}, grace time.Duration) {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && errors.Is(err, syscall.ESRCH) {
		return
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-exited:
	case <-timer.C:
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

var _ core.RuntimePrompter = (*Runtime)(nil)
