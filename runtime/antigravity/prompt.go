package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Router prompt runs (spec 78, REQ-RPR-002): one `agy -p` process per router
// call of an Antigravity account, with agy's sandbox on and never
// --dangerously-skip-permissions, so agy's tools get no approval. The prompt
// goes in on stdin as one stream-json input message,
// {"event":"user","message":{"content":"…"}} (agy 1.5.17: `-p` takes the
// next argument as its prompt and an empty one is refused, so a stdin prompt
// needs --input-format stream-json). Slash commands are off so a prompt
// starting with "/" is text. agy's stream-json output is NDJSON events,
// {"event":"init"|"step_update"|"result", "<event>":{…}}; agent_response
// steps carry text_delta, and the result carries the whole response and the
// usage, as the `--output-format json` answer does (checked live 2026-11-26).

// promptArgs are the flags of every prompt run before --model.
var promptArgs = []string{"--input-format", "stream-json", "--output-format", "stream-json", "--disable-slash-commands"}

// promptInput is the stdin of a prompt run: one stream-json user message.
func promptInput(prompt string) string {
	var msg struct {
		Event   string `json:"event"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	msg.Event = "user"
	msg.Message.Content = prompt
	data, _ := json.Marshal(msg)
	return string(data) + "\n"
}

// Classes of a failed prompt run besides the turn classes (REQ-RPR-005).
const (
	// classRateLimit is a usage or rate limit; agy's quota answers map to it
	// too.
	classRateLimit = "rate_limit"
	// classUnknownModel is a model agy refused.
	classUnknownModel = "client"
)

// agyErrorPrefix starts the error line agy prints when a headless run fails.
const agyErrorPrefix = "AGY_ERROR:"

// buildPromptArgs returns the argv after the binary of a prompt run
// (REQ-RPR-002): promptArgs, --model unless the model is
// "" or "default" (agy's current model), --sandbox, and --effort when set and
// not lana's "off". A model or effort starting with "-" is refused.
func buildPromptArgs(cfg core.RuntimeConfig) ([]string, error) {
	args := append([]string(nil), promptArgs...)
	if cfg.Model != "" && cfg.Model != "default" {
		if strings.HasPrefix(cfg.Model, "-") {
			return nil, fmt.Errorf("invalid model %q", cfg.Model)
		}
		args = append(args, "--model", cfg.Model)
	}
	args = append(args, "--sandbox")
	if effort := cfg.Effort; effort != "" && effort != "off" {
		if strings.HasPrefix(effort, "-") {
			return nil, fmt.Errorf("invalid reasoning_effort %q", effort)
		}
		args = append(args, "--effort", effort)
	}
	return args, nil
}

// RunPrompt runs one prompt for a router call (spec 78, REQ-RPR-002): `agy -p`
// with the account's HOME and environment as its agent turns get them (the
// file-token setting of added accounts included), the prompt on stdin, in
// run.Dir, in its own process group, killed when ctx ends. It takes a slot
// of the runtime's concurrency limit (REQ-RPR-006). It never reads a
// credential.
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
		return early(classRuntime, "Antigravity runtime is shut down")
	}
	bin, err := r.resolveBinary(run.Config.Binary)
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
	args, err := buildPromptArgs(run.Config)
	if err != nil {
		return early(classUnknownModel, err.Error())
	}
	env := buildEnv(r.opts.Environ(), run.Config)
	if err := r.sem.Acquire(ctx); err != nil {
		return ctx.Err()
	}
	defer r.sem.Release()

	cmd := exec.Command(bin, args...)
	cmd.Dir = run.Dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(promptInput(run.Prompt))
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
		return early(classNotInstalled, "start Antigravity: "+err.Error())
	}
	// abort stops a run that cannot answer: agy starting its own sign-in.
	abort := make(chan struct{})
	var abortOnce sync.Once
	stopRun := func() { abortOnce.Do(func() { close(abort) }) }
	stderr := &promptStderr{tail: newTailBuffer(8), onLogin: stopRun}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		stderr.readFrom(stderrPipe)
	}()
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

	p := &promptParser{emit: emit}
	br := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, rerr := readLine(br)
		if len(bytes.TrimSpace(line)) > 0 {
			p.handle(line)
		}
		if rerr != nil {
			break
		}
	}
	<-stderrDone
	waitErr := cmd.Wait()
	close(exited)
	<-killed

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if stderr.login() {
		return p.failure(classAuth, notSignedInMessage(accountName(run.Config)))
	}
	if p.res != nil && p.res.ok() && waitErr == nil {
		p.finish()
		return nil
	}
	msg := p.errorText()
	if msg == "" {
		msg = stderr.errorLine()
	}
	switch {
	case msg != "":
		msg = "Antigravity: " + msg
	case waitErr != nil:
		msg = "Antigravity exited unexpectedly (" + waitErr.Error() + ")"
	default:
		msg = "Antigravity answered without a result"
	}
	msg = truncate(msg, 300)
	class, msg := classifyPromptFailure(accountName(run.Config), msg)
	if p.res != nil {
		// A partial answer still reports its usage.
		p.usage()
	}
	return p.failure(class, msg)
}

// classifyPromptFailure maps a failed prompt run to its router class
// (REQ-RPR-005): not signed in is auth (with lana's message), a quota or rate
// limit rate_limit, a refused model client, anything else runtime.
func classifyPromptFailure(account, msg string) (string, string) {
	switch {
	case looksNotSignedIn(msg) || strings.Contains(strings.ToLower(msg), "authentication required"):
		return classAuth, notSignedInMessage(account)
	case looksQuota(msg):
		return classRateLimit, msg
	case looksUnknownModel(msg):
		return classUnknownModel, msg
	}
	return classRuntime, msg
}

// looksUnknownModel reports whether an agy error text refuses the model, such
// as "invalid model selection (--model x --effort y)".
func looksUnknownModel(text string) bool {
	t := strings.ToLower(text)
	for _, s := range []string{"invalid model", "model not found", "unknown model", "no such model", "not a valid model"} {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

// printResult is the terminal "result" event of a stream-json run (the same
// shape as the `--output-format json` answer).
type printResult struct {
	Status         string          `json:"status"`
	Response       string          `json:"response"`
	FinishedReason string          `json:"finished_reason"`
	Error          json.RawMessage `json:"error"`
	Usage          struct {
		InputTokens     int64 `json:"input_tokens"`
		OutputTokens    int64 `json:"output_tokens"`
		CacheReadTokens int64 `json:"cache_read_tokens"`
	} `json:"usage"`
}

// ok reports whether the run finished normally.
func (r *printResult) ok() bool {
	status := strings.ToUpper(strings.TrimSpace(r.Status))
	return (status == "" || status == "SUCCESS") && r.FinishedReason == "" && !hasJSONValue(r.Error)
}

// promptParser maps the stdout lines of one prompt run.
type promptParser struct {
	emit func(core.RuntimeEvent)
	// sawText is set by the first text delta.
	sawText bool
	res     *printResult
	// agyError is the text of an AGY_ERROR line.
	agyError string
	usaged   bool
}

// handle maps one stdout line: a step_update text delta is emitted as it
// comes, a result is kept for finish, an AGY_ERROR line is kept as the error.
func (p *promptParser) handle(line []byte) {
	trimmed := bytes.TrimSpace(line)
	if rest, ok := bytes.CutPrefix(trimmed, []byte(agyErrorPrefix)); ok {
		p.agyError = errorMessage(json.RawMessage(bytes.TrimSpace(rest)))
		return
	}
	var head struct {
		Type   string          `json:"type"`
		Event  string          `json:"event"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(trimmed, &head) != nil {
		return
	}
	kind := head.Event
	if kind == "" {
		kind = head.Type
	}
	switch kind {
	case "step_update":
		p.stepUpdate(trimmed)
	case "result":
		// agy nests the result under "result"; a flat line is read as is.
		body := trimmed
		if len(bytes.TrimSpace(head.Result)) > 0 && bytes.TrimSpace(head.Result)[0] == '{' {
			body = head.Result
		}
		var res printResult
		if json.Unmarshal(body, &res) == nil {
			p.res = &res
		}
	}
}

// stepDelta is the part of a step_update that carries streamed text: the
// update's own text_delta, or the one of a nested step or step_update
// object. A thinking delta is not sent (REQ-RPR-004); a step of the user is
// skipped.
type stepDelta struct {
	Source        string `json:"source"`
	StepType      string `json:"step_type"`
	TextDelta     string `json:"text_delta"`
	TextDeltaAlt  string `json:"textDelta"`
	ThinkingDelta string `json:"thinking_delta"`
}

func (p *promptParser) stepUpdate(line []byte) {
	var outer struct {
		stepDelta
		Step       *stepDelta `json:"step"`
		StepUpdate *stepDelta `json:"step_update"`
	}
	if json.Unmarshal(line, &outer) != nil {
		return
	}
	for _, d := range []*stepDelta{&outer.stepDelta, outer.Step, outer.StepUpdate} {
		if d == nil || strings.Contains(strings.ToLower(d.Source), "user") {
			continue
		}
		// Only the agent's answer is text; tool and user steps are not.
		if d.StepType != "" && d.StepType != "agent_response" && d.StepType != "PLANNER_RESPONSE" {
			continue
		}
		text := d.TextDelta
		if text == "" {
			text = d.TextDeltaAlt
		}
		if text != "" {
			p.sawText = true
			p.emit(core.RuntimeEvent{Kind: core.RuntimeTextDelta, Text: text})
		}
	}
}

// finish emits the whole response when no delta streamed it, then the usage.
func (p *promptParser) finish() {
	if !p.sawText && p.res.Response != "" {
		p.sawText = true
		p.emit(core.RuntimeEvent{Kind: core.RuntimeAssistantMessage, Text: p.res.Response})
	}
	p.usage()
}

// usage emits the result's usage once (REQ-RPR-004): input tokens include
// the cache-read ones, which are also reported as cached.
func (p *promptParser) usage() {
	if p.usaged || p.res == nil {
		return
	}
	p.usaged = true
	u := p.res.Usage
	p.emit(core.RuntimeEvent{Kind: core.RuntimeUsage, Usage: core.Usage{
		InputTokens:       u.InputTokens + u.CacheReadTokens,
		OutputTokens:      u.OutputTokens,
		CachedInputTokens: u.CacheReadTokens,
	}})
}

// errorText is agy's own error of a failed run: the result's error, the
// AGY_ERROR line, or the finished reason.
func (p *promptParser) errorText() string {
	if p.res != nil {
		if msg := errorMessage(p.res.Error); msg != "" {
			return msg
		}
	}
	if p.agyError != "" {
		return p.agyError
	}
	if p.res != nil {
		if p.res.FinishedReason != "" {
			return "the run ended early (" + p.res.FinishedReason + ")"
		}
		if s := strings.TrimSpace(p.res.Status); s != "" && !strings.EqualFold(s, "SUCCESS") {
			return "status " + s
		}
	}
	return ""
}

// failure is the error of a failed run: an *EarlyError before the first
// text, else a *TurnError.
func (p *promptParser) failure(class, msg string) error {
	if p.sawText {
		return &TurnError{Message: msg}
	}
	return &EarlyError{Class: class, Message: msg}
}

// errorMessage reads an error value: a string, or an object's "message",
// cut to one short line.
func errorMessage(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if !hasJSONValue(raw) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return detailLine(s)
	}
	var obj struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		if obj.Message != "" {
			return detailLine(obj.Message)
		}
		if obj.Error != "" {
			return detailLine(obj.Error)
		}
		return ""
	}
	return detailLine(string(raw))
}

// hasJSONValue reports whether raw holds a value other than null, "", or {}.
func hasJSONValue(raw json.RawMessage) bool {
	t := string(bytes.TrimSpace(raw))
	return t != "" && t != "null" && t != `""` && t != "{}"
}

// promptStderr keeps the last stderr lines of a prompt run and reports agy
// starting its own sign-in ("No valid authentication found … Starting
// login"), which a headless run cannot finish.
type promptStderr struct {
	tail    *tailBuffer
	onLogin func()

	mu       sync.Mutex
	loginHit bool
	agyError string
}

func (s *promptStderr) readFrom(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		s.mu.Lock()
		if rest, ok := strings.CutPrefix(line, agyErrorPrefix); ok {
			s.agyError = errorMessage(json.RawMessage(strings.TrimSpace(rest)))
		}
		login := strings.Contains(lower, "no valid authentication found") || strings.Contains(lower, "starting login")
		if login {
			s.loginHit = true
		}
		s.mu.Unlock()
		if len(line) > 2048 {
			line = line[:2048]
		}
		s.tail.mu.Lock()
		s.tail.lines = append(s.tail.lines, line)
		if len(s.tail.lines) > s.tail.max {
			s.tail.lines = s.tail.lines[len(s.tail.lines)-s.tail.max:]
		}
		s.tail.mu.Unlock()
		if login && s.onLogin != nil {
			s.onLogin()
		}
	}
	_, _ = io.Copy(io.Discard, r)
}

func (s *promptStderr) login() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loginHit
}

// errorLine is the AGY_ERROR text of stderr, else its last line, redacted.
func (s *promptStderr) errorLine() string {
	s.mu.Lock()
	agyError := s.agyError
	s.mu.Unlock()
	if agyError != "" {
		return agyError
	}
	return detailLine(s.tail.lastLine())
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
