package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakeclaude")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fakeBin = filepath.Join(dir, "claude")
	cmd := exec.Command("go", "build", "-o", fakeBin, "./testdata/fakeclaude")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build fakeclaude: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type mapSecrets map[string]string

func (s mapSecrets) Lookup(name string) (core.Secret, bool) {
	v, ok := s[name]
	return core.NewSecret(v), ok
}

// harness runs the runtime against the fake claude.
type harness struct {
	t       *testing.T
	rt      *Runtime
	logPath string
	ws      string
	fixture atomic.Value // string
	mu      sync.Mutex
	env     []string
}

func newHarness(t *testing.T, fixture string, opts Options, env ...string) *harness {
	t.Helper()
	h := &harness{t: t, logPath: filepath.Join(t.TempDir(), "log.jsonl"), ws: t.TempDir(), env: env}
	h.setFixture(fixture)
	if opts.Environ == nil {
		opts.Environ = func() []string {
			h.mu.Lock()
			defer h.mu.Unlock()
			base := append([]string{}, os.Environ()...)
			base = append(base, "FAKECLAUDE_LOG="+h.logPath, "FAKECLAUDE_FIXTURE="+h.fixture.Load().(string))
			return append(base, h.env...)
		}
	}
	if opts.LookPath == nil {
		opts.LookPath = func(string) (string, error) { return fakeBin, nil }
	}
	h.rt = New(core.RuntimeDeps{Secrets: mapSecrets{"MY_CLAUDE_KEY": "sk-ant-test-key"}}, opts)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.rt.Shutdown(ctx)
	})
	return h
}

func (h *harness) setFixture(name string) {
	abs, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		h.t.Fatal(err)
	}
	h.fixture.Store(abs)
}

func (h *harness) setEnv(env ...string) {
	h.mu.Lock()
	h.env = env
	h.mu.Unlock()
}

// recorder collects emitted events.
type recorder struct {
	mu     sync.Mutex
	events []core.RuntimeEvent
	notify chan core.RuntimeEvent
}

func newRecorder() *recorder { return &recorder{notify: make(chan core.RuntimeEvent, 1024)} }

func (r *recorder) emit(ev core.RuntimeEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
	r.notify <- ev
}

func (r *recorder) all() []core.RuntimeEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.RuntimeEvent(nil), r.events...)
}

func (r *recorder) waitKind(t *testing.T, kind core.RuntimeEventKind) core.RuntimeEvent {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-r.notify:
			if ev.Kind == kind {
				return ev
			}
		case <-timeout:
			t.Fatalf("no %s event", kind)
		}
	}
}

func kinds(events []core.RuntimeEvent) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = string(ev.Kind)
	}
	return out
}

func (h *harness) turn(sid string) core.RuntimeTurn {
	return core.RuntimeTurn{
		SessionID: sid, TurnID: "turn-1", AgentName: "claude",
		Config: core.RuntimeConfig{Workspace: h.ws, Model: "haiku"},
		Input:  []core.ContentBlock{{Kind: core.BlockText, Text: "hi"}},
	}
}

func (h *harness) run(ctx context.Context, turn core.RuntimeTurn) ([]core.RuntimeEvent, error) {
	rec := newRecorder()
	turn.Emit = rec.emit
	err := h.rt.RunTurn(ctx, turn)
	return rec.all(), err
}

type logEntry struct {
	Kind string   `json:"kind"`
	Argv []string `json:"argv"`
	// MCPConfig and MCPConfigMode are the --mcp-config file's content and
	// permission bits.
	MCPConfig     string          `json:"mcp_config"`
	MCPConfigMode string          `json:"mcp_config_mode"`
	Env           []string        `json:"env"`
	Cwd           string          `json:"cwd"`
	Line          json.RawMessage `json:"line"`
	Pid           int             `json:"pid"`
	// StdinEOF is set by `-p /usage`: stdin was at EOF.
	StdinEOF bool `json:"stdin_eof"`
}

func (h *harness) log() []logEntry {
	data, err := os.ReadFile(h.logPath)
	if err != nil {
		return nil
	}
	var out []logEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e logEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

func (h *harness) starts() []logEntry {
	var out []logEntry
	for _, e := range h.log() {
		if e.Kind == "start" {
			out = append(out, e)
		}
	}
	return out
}

// stdinOf returns the decoded stdin lines whose type matches.
func (h *harness) stdinOf(typ string) []map[string]any {
	var out []map[string]any
	for _, e := range h.log() {
		if e.Kind != "stdin" {
			continue
		}
		var m map[string]any
		if json.Unmarshal(e.Line, &m) == nil && m["type"] == typ {
			out = append(out, m)
		}
	}
	return out
}

func argValue(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func endOf(t *testing.T, events []core.RuntimeEvent) core.RuntimeEvent {
	t.Helper()
	if len(events) == 0 || events[len(events)-1].Kind != core.RuntimeEnd {
		t.Fatalf("last event is not end: %v", kinds(events))
	}
	return events[len(events)-1]
}

func TestRegistered(t *testing.T) {
	f, ok := core.LookupRuntime(Name)
	if !ok {
		t.Fatal("claude-code is not registered")
	}
	rt, err := f(core.RuntimeDeps{})
	if err != nil || rt == nil {
		t.Fatalf("factory: %v", err)
	}
}

// AC-CCR-002 (runtime side): the first turn passes --session-id <uuid>, the
// same process serves later turns, and a new runtime resumes with --resume.
func TestSessionIDThenResume(t *testing.T) {
	h := newHarness(t, "twoturn.jsonl", Options{})
	ctx := context.Background()
	events, err := h.run(ctx, h.turn("s1"))
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	starts := h.starts()
	if len(starts) != 1 {
		t.Fatalf("starts = %d", len(starts))
	}
	argv := starts[0].Argv
	id := argValue(argv, "--session-id")
	if !isUUID(id) || argValue(argv, "--resume") != "" {
		t.Fatalf("argv = %v", argv)
	}
	if got := argValue(argv, "--model"); got != "haiku" {
		t.Fatalf("--model = %q", got)
	}
	resolved, _ := filepath.EvalSymlinks(h.ws)
	if cwd, _ := filepath.EvalSymlinks(starts[0].Cwd); cwd != resolved {
		t.Fatalf("cwd = %q, want %q", starts[0].Cwd, h.ws)
	}
	if events[0].Kind != core.RuntimeSessionMeta || events[0].ClaudeSessionID != id || events[0].Version != "2.1.319" {
		t.Fatalf("first event = %+v", events[0])
	}
	if events[0].Model != "claude-haiku-5-5" || len(events[0].Tools) == 0 {
		t.Fatalf("meta = %+v", events[0])
	}

	events2, err := h.run(ctx, h.turn("s1"))
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if n := len(h.starts()); n != 1 {
		t.Fatalf("second turn started a new process: %d starts", n)
	}
	for _, ev := range events2 {
		if ev.Kind == core.RuntimeSessionMeta {
			t.Fatal("session_meta sent twice for one process")
		}
	}
	var texts []string
	for _, ev := range events2 {
		if ev.Kind == core.RuntimeAssistantMessage {
			texts = append(texts, ev.Text)
		}
	}
	if strings.Join(texts, "|") != "two" {
		t.Fatalf("turn 2 replies = %v", texts)
	}

	// A daemon restart: a new runtime gets the stored ID as ResumeID.
	h2 := newHarness(t, "hi.jsonl", Options{})
	turn := h2.turn("s1")
	turn.ResumeID = id
	events3, err := h2.run(ctx, turn)
	if err != nil {
		t.Fatalf("resumed turn: %v", err)
	}
	starts = h2.starts()
	if len(starts) != 1 || argValue(starts[0].Argv, "--resume") != id || argValue(starts[0].Argv, "--session-id") != "" {
		t.Fatalf("resumed argv = %v", starts[0].Argv)
	}
	if events3[0].ClaudeSessionID != id {
		t.Fatalf("resumed meta = %+v", events3[0])
	}
}

func TestBuildArgs(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555555"
	args, err := buildArgs(core.RuntimeConfig{Model: "opus", Effort: "high"}, id, false, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "-p --input-format stream-json --output-format stream-json --verbose --include-partial-messages " +
		"--permission-prompts host --permission-prompt-tool stdio --model opus --effort high --session-id " + id
	if got := strings.Join(args, " "); got != want {
		t.Fatalf("args = %s", got)
	}
	args, _ = buildArgs(core.RuntimeConfig{AppendSystemPrompt: "-You are Bima"}, id, true, `{"mcpServers":{}}`)
	if got := strings.Join(args, "|"); !strings.Contains(got, "|--append-system-prompt|-You are Bima|--mcp-config|{\"mcpServers\":{}}|") || strings.Contains(got, "strict-mcp-config") {
		t.Fatalf("prompt and mcp args = %v", args)
	}
	if got := strings.Join(args[len(args)-2:], " "); got != "--resume "+id {
		t.Fatalf("resume args = %v", args)
	}
	if _, err := buildArgs(core.RuntimeConfig{}, "--dangerously", true, ""); err == nil {
		t.Fatal("non-UUID session ID accepted")
	}
	if _, err := buildArgs(core.RuntimeConfig{Model: "--x"}, id, false, ""); err == nil {
		t.Fatal("flag-like model accepted")
	}
}

// AC-CCR-003: subscription auth drops the four variables from the child env.
func TestEnvScrubbing(t *testing.T) {
	scrubbed := []string{"ANTHROPIC_API_KEY=leak1", "ANTHROPIC_AUTH_TOKEN=leak2", "CLAUDE_CODE_OAUTH_TOKEN=leak3", "ANTHROPIC_BASE_URL=http://leak4"}
	h := newHarness(t, "hi.jsonl", Options{}, scrubbed...)
	if _, err := h.run(context.Background(), h.turn("s1")); err != nil {
		t.Fatal(err)
	}
	env := strings.Join(h.starts()[0].Env, "\n")
	for _, name := range scrubbedEnv {
		if strings.Contains(env, name+"=") {
			t.Fatalf("child env has %s", name)
		}
	}
	if !strings.Contains(env, "FAKECLAUDE_LOG=") {
		t.Fatal("child env lost other variables")
	}

	// api_key auth: same scrub, then the key from the secrets service.
	h2 := newHarness(t, "hi.jsonl", Options{}, scrubbed...)
	turn := h2.turn("s1")
	turn.Config.Auth, turn.Config.APIKeyEnv = core.RuntimeAuthAPIKey, "MY_CLAUDE_KEY"
	if _, err := h2.run(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, kv := range h2.starts()[0].Env {
		for _, name := range scrubbedEnv {
			if strings.HasPrefix(kv, name+"=") {
				keys = append(keys, kv)
			}
		}
	}
	if len(keys) != 1 || keys[0] != "ANTHROPIC_API_KEY=sk-ant-test-key" {
		t.Fatalf("api_key env = %v", keys)
	}

	turn.SessionID = "s2"
	turn.Config.APIKeyEnv = "MISSING_KEY"
	events, err := h2.run(context.Background(), turn)
	if err == nil || !strings.Contains(err.Error(), "MISSING_KEY is not set") || !endOf(t, events).IsError {
		t.Fatalf("missing secret: err = %v", err)
	}
}

// AC-CCR-004 (runtime side): the recorded "say hi" transcript maps to the
// expected event sequence.
func TestHiFixtureEvents(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{})
	turn := h.turn("s1")
	turn.Input = append(turn.Input, core.ContentBlock{Kind: core.BlockImage, MediaType: "image/png", Data: []byte("PNG")})
	events, err := h.run(context.Background(), turn)
	if err != nil {
		t.Fatal(err)
	}
	want := "session_meta text_delta text_delta text_delta text_delta assistant_message limits usage end"
	if got := strings.Join(kinds(events), " "); got != want {
		t.Fatalf("events = %s", got)
	}
	var deltas string
	for _, ev := range events {
		if ev.Kind == core.RuntimeTextDelta {
			deltas += ev.Text
		}
	}
	if deltas != "Hello there, friend!" || events[5].Text != "Hello there, friend!" {
		t.Fatalf("text = %q / %q", deltas, events[5].Text)
	}
	limits := events[6].Windows
	if len(limits) != 2 || limits[0].Type != "five_hour" || *limits[0].Utilization != 0.08 ||
		limits[0].ResetsAt.Unix() != 1794088200 || limits[1].Type != "seven_day" || limits[0].Status != "allowed" {
		t.Fatalf("limits = %+v", limits)
	}
	u := events[7]
	if u.Usage.InputTokens != 2+16599+12932 || u.Usage.CachedInputTokens != 12932 || u.Usage.OutputTokens != 10 ||
		u.CostUSD != 0.00345432 || u.Model != "claude-haiku-5-5" || u.DurationMS != 2382 {
		t.Fatalf("usage = %+v", u)
	}
	if end := events[8]; end.StopReason != "end_turn" || end.IsError {
		t.Fatalf("end = %+v", end)
	}
	// The user line carries the text and a base64 image block.
	users := h.stdinOf("user")
	if len(users) != 1 {
		t.Fatalf("user lines = %d", len(users))
	}
	content := users[0]["message"].(map[string]any)["content"].([]any)
	img := content[1].(map[string]any)
	src := img["source"].(map[string]any)
	if img["type"] != "image" || src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "UE5H" {
		t.Fatalf("image block = %v", img)
	}
	if inits := h.stdinOf("control_request"); len(inits) != 1 ||
		inits[0]["request"].(map[string]any)["subtype"] != "initialize" {
		t.Fatalf("initialize not sent first: %v", inits)
	}
}

// The recorded Bash transcript: approval allow round-trip and tool events.
func TestBashApprovalAllow(t *testing.T) {
	h := newHarness(t, "bash.jsonl", Options{})
	turn := h.turn("s1")
	var got []core.RuntimeApproval
	var mu sync.Mutex
	turn.Approve = func(ctx context.Context, req core.RuntimeApproval) (bool, string) {
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		return true, ""
	}
	events, err := h.run(context.Background(), turn)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("approvals = %d", len(got))
	}
	req := got[0]
	if req.ToolName != "Bash" || req.Risk != core.RiskExec || req.Rule != "bash(ls)" || req.PermTool != "bash" ||
		req.CallID != "toolu_01RJJQ1VZJQPDhmWbcEFFEQv" || string(req.PermInput) != `{"command":"ls"}` {
		t.Fatalf("approval = %+v", req)
	}
	var seq []string
	for _, ev := range events {
		switch ev.Kind {
		case core.RuntimeToolCall:
			if ev.Name != "Bash" || ev.ID != "toolu_01RJJQ1VZJQPDhmWbcEFFEQv" || ev.Risk != core.RiskExec || !strings.Contains(string(ev.Input), `"command":"ls"`) {
				t.Fatalf("tool_call = %+v", ev)
			}
			seq = append(seq, "call")
		case core.RuntimeToolResult:
			if ev.ID != "toolu_01RJJQ1VZJQPDhmWbcEFFEQv" || ev.Output != "notes.txt  6B" || ev.IsError {
				t.Fatalf("tool_result = %+v", ev)
			}
			seq = append(seq, "result")
		case core.RuntimeAssistantMessage:
			seq = append(seq, "message")
		case core.RuntimeUsage, core.RuntimeEnd:
			seq = append(seq, string(ev.Kind))
		}
	}
	if got := strings.Join(seq, " "); got != "call result message usage end" {
		t.Fatalf("sequence = %s", got)
	}
	responses := h.stdinOf("control_response")
	if len(responses) != 1 {
		t.Fatalf("control responses = %d", len(responses))
	}
	resp := responses[0]["response"].(map[string]any)
	inner := resp["response"].(map[string]any)
	if resp["subtype"] != "success" || resp["request_id"] != "f7ff0bd9-e4e3-4f5d-bdd7-7fa039a5fa95" ||
		inner["behavior"] != "allow" || inner["updatedInput"].(map[string]any)["command"] != "ls" {
		t.Fatalf("response = %v", resp)
	}
}

func TestBashApprovalDeny(t *testing.T) {
	for _, tc := range []struct {
		name    string
		approve func(context.Context, core.RuntimeApproval) (bool, string)
		message string
	}{
		{"deny", func(context.Context, core.RuntimeApproval) (bool, string) { return false, "deny rule: bash(ls)" }, "deny rule: bash(ls)"},
		{"no approver", nil, "no approver is available"},
		{"panic", func(context.Context, core.RuntimeApproval) (bool, string) { panic("boom") }, "approval failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "bash.jsonl", Options{})
			turn := h.turn("s1")
			turn.Approve = tc.approve
			if _, err := h.run(context.Background(), turn); err != nil {
				t.Fatal(err)
			}
			responses := h.stdinOf("control_response")
			if len(responses) != 1 {
				t.Fatalf("control responses = %d", len(responses))
			}
			inner := responses[0]["response"].(map[string]any)["response"].(map[string]any)
			if inner["behavior"] != "deny" || inner["message"] != tc.message {
				t.Fatalf("response = %v", inner)
			}
		})
	}
}

// AC-CCR-006: the fake ignores interrupt; the turn still ends cancelled within
// 6 seconds (interrupt, then SIGTERM after 5 s).
func TestCancelIgnoredInterrupt(t *testing.T) {
	h := newHarness(t, "hang.jsonl", Options{}, "FAKECLAUDE_IGNORE_INTERRUPT=1")
	rec := newRecorder()
	turn := h.turn("s1")
	turn.Emit = rec.emit
	errc := make(chan error, 1)
	go func() { errc <- h.rt.RunTurn(context.Background(), turn) }()
	rec.waitKind(t, core.RuntimeTextDelta)
	start := time.Now()
	if err := h.rt.Interrupt("s1"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("turn did not end within 6 s")
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Fatalf("took %v", d)
	}
	if end := endOf(t, rec.all()); end.StopReason != core.RuntimeStopCancelled || end.IsError {
		t.Fatalf("end = %+v", end)
	}
	var sawInterrupt bool
	for _, m := range h.stdinOf("control_request") {
		if m["request"].(map[string]any)["subtype"] == "interrupt" {
			sawInterrupt = true
		}
	}
	if !sawInterrupt {
		t.Fatal("fake did not receive interrupt")
	}
}

// A context cancel interrupts; an honoring process ends the turn at once and
// stays alive for the next turn.
func TestCancelByContextKeepsProcess(t *testing.T) {
	h := newHarness(t, "hang.jsonl", Options{})
	rec := newRecorder()
	turn := h.turn("s1")
	turn.Emit = rec.emit
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- h.rt.RunTurn(ctx, turn) }()
	rec.waitKind(t, core.RuntimeTextDelta)
	start := time.Now()
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("turn did not end")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("honored interrupt took %v", d)
	}
	if end := endOf(t, rec.all()); end.StopReason != core.RuntimeStopCancelled {
		t.Fatalf("end = %+v", end)
	}
	h.rt.mu.Lock()
	p := h.rt.procs["s1"]
	h.rt.mu.Unlock()
	if p == nil || p.exited() {
		t.Fatal("process did not survive the interrupt")
	}
}

// AC-CCR-007: three sessions start a turn; two processes run and the third
// starts when one ends.
func TestConcurrencyCap(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	h := newHarness(t, "gate.jsonl", Options{}, "FAKECLAUDE_GATE="+gate)
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = h.run(context.Background(), h.turn(fmt.Sprintf("s%d", i)))
		}(i)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(h.starts()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(h.starts()); n != 2 {
		t.Fatalf("starts before gate = %d", n)
	}
	st := h.rt.Status(context.Background())
	if st.Running != 2 || st.Queued != 1 || st.MaxConcurrent != 2 {
		t.Fatalf("status = %+v", st)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	if n := len(h.starts()); n != 3 {
		t.Fatalf("starts = %d", n)
	}
	// The idle process of an earlier turn was stopped to keep at most 2 alive.
	h.rt.mu.Lock()
	live := 0
	for _, p := range h.rt.procs {
		if !p.exited() {
			live++
		}
	}
	h.rt.mu.Unlock()
	if live > 2 {
		t.Fatalf("live processes = %d", live)
	}
}

func TestFIFOSemaphore(t *testing.T) {
	s := newFIFOSem(1)
	if err := s.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	var order []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	waitQueued := func(n int) {
		for {
			if _, q := s.Counts(); q == n {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	// A waiter whose context ends leaves the queue.
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() { cancelled <- s.Acquire(ctx) }()
	waitQueued(1)
	for i, name := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Acquire(context.Background()); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			s.Release()
		}()
		waitQueued(i + 2)
	}
	cancel()
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	waitQueued(3)
	s.Release()
	wg.Wait()
	if got := strings.Join(order, ""); got != "abc" {
		t.Fatalf("order = %s", got)
	}
	if r, q := s.Counts(); r != 0 || q != 0 {
		t.Fatalf("counts = %d, %d", r, q)
	}
}

// A process that exits mid-turn fails the turn with the redacted last stderr
// line; the next turn starts a new process with --resume.
func TestCrashMidTurn(t *testing.T) {
	h := newHarness(t, "crash.jsonl", Options{})
	events, err := h.run(context.Background(), h.turn("s1"))
	if err == nil {
		t.Fatal("crash did not fail the turn")
	}
	end := endOf(t, events)
	if !end.IsError || !strings.Contains(end.ErrorText, "upstream connection reset") || end.ErrorText != err.Error() {
		t.Fatalf("end = %+v", end)
	}
	for _, secret := range []string{"abcdef123456", "SECRETSECRET"} {
		if strings.Contains(end.ErrorText, secret) {
			t.Fatalf("error text leaks %s: %s", secret, end.ErrorText)
		}
	}
	if len(end.ErrorText) > len("Claude Code exited: ")+300 {
		t.Fatalf("error text too long: %d", len(end.ErrorText))
	}
	first := argValue(h.starts()[0].Argv, "--session-id")

	h.setFixture("hi.jsonl")
	if _, err := h.run(context.Background(), h.turn("s1")); err != nil {
		t.Fatal(err)
	}
	starts := h.starts()
	if len(starts) != 2 || argValue(starts[1].Argv, "--resume") != first {
		t.Fatalf("restart argv = %v", starts[len(starts)-1].Argv)
	}
}

func TestNotLoggedIn(t *testing.T) {
	h := newHarness(t, "notloggedin.jsonl", Options{})
	events, err := h.run(context.Background(), h.turn("s1"))
	if err == nil || err.Error() != notLoggedInMessage {
		t.Fatalf("err = %v", err)
	}
	for _, ev := range events {
		if ev.Kind == core.RuntimeAssistantMessage {
			t.Fatalf("synthetic login message shown as reply: %q", ev.Text)
		}
	}
	if end := endOf(t, events); !end.IsError || end.ErrorText != notLoggedInMessage {
		t.Fatalf("end = %+v", end)
	}
}

func TestNotInstalledAndOldVersion(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{LookPath: func(string) (string, error) { return "", exec.ErrNotFound }})
	if _, err := h.run(context.Background(), h.turn("s1")); err == nil || err.Error() != errNotInstalled {
		t.Fatalf("err = %v", err)
	}
	if st := h.rt.Status(context.Background()); st.Error != errNotInstalled {
		t.Fatalf("status = %+v", st)
	}
	turn := h.turn("s1")
	turn.Config.Binary = filepath.Join(t.TempDir(), "nope")
	if _, err := h.run(context.Background(), turn); err == nil || err.Error() != errNotInstalled {
		t.Fatalf("missing configured binary: %v", err)
	}

	old := newHarness(t, "hi.jsonl", Options{}, "FAKECLAUDE_VERSION=2.0.9 (Claude Code)")
	_, err := old.run(context.Background(), old.turn("s1"))
	if err == nil || !strings.Contains(err.Error(), "2.0.9 is older than the minimum 2.1.0") {
		t.Fatalf("old version: %v", err)
	}
	if len(old.starts()) != 0 {
		t.Fatal("old version started a stream process")
	}
}

func TestStatus(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{})
	st := h.rt.Status(context.Background())
	if st.Binary != fakeBin || st.Version != "2.1.319" || !st.VersionOK || !st.LoggedIn || st.AuthMethod != "claude.ai" ||
		st.Error != "" || st.MaxConcurrent != 2 {
		t.Fatalf("status = %+v", st)
	}
	// Cached: a changed auth state is not seen until the TTL passes.
	h.setEnv(`FAKECLAUDE_AUTH={"loggedIn":false,"authMethod":"none"}`, "FAKECLAUDE_AUTH_EXIT=1")
	if st := h.rt.Status(context.Background()); !st.LoggedIn {
		t.Fatal("status not cached")
	}

	h2 := newHarness(t, "hi.jsonl", Options{}, `FAKECLAUDE_AUTH={"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`, "FAKECLAUDE_AUTH_EXIT=1")
	st = h2.rt.Status(context.Background())
	if st.LoggedIn || st.AuthMethod != "" || st.Error != "" || !st.VersionOK {
		t.Fatalf("logged-out status = %+v", st)
	}
}

func TestParseAuthStatus(t *testing.T) {
	st, err := parseAuthStatus([]byte(`{"loggedIn":true,"authMethod":"claude.ai","email":"x@example.com","subscriptionType":"max"}`))
	if err != nil || !st.LoggedIn || st.AuthMethod != "claude.ai" {
		t.Fatalf("st = %+v, %v", st, err)
	}
	if _, err := parseAuthStatus([]byte("Not logged in. Run claude auth login to authenticate.")); err == nil {
		t.Fatal("text output parsed as JSON")
	}
	if _, v, ok := parseVersion("2.1.319 (Claude Code)"); !ok || v != [3]int{2, 1, 319} {
		t.Fatalf("version = %v", v)
	}
	if versionAtLeast([3]int{2, 0, 99}, [3]int{2, 1, 0}) || !versionAtLeast([3]int{2, 1, 0}, [3]int{2, 1, 0}) {
		t.Fatal("versionAtLeast")
	}
}

// Subagent lines (non-null parent_tool_use_id) are not part of the main reply;
// unsupported control requests get an error response; unknown types are ignored.
func TestSubagentAndUnknownLines(t *testing.T) {
	h := newHarness(t, "subagent.jsonl", Options{})
	events, err := h.run(context.Background(), h.turn("s1"))
	if err != nil {
		t.Fatal(err)
	}
	want := "session_meta assistant_message tool_call tool_result assistant_message usage end"
	if got := strings.Join(kinds(events), " "); got != want {
		t.Fatalf("events = %s", got)
	}
	for _, ev := range events {
		if strings.Contains(ev.Text, "subagent") || ev.ID == "toolu_sub_read" {
			t.Fatalf("subagent line leaked: %+v", ev)
		}
	}
	if ev := events[3]; ev.ID != "toolu_task" || ev.Output != "notes say hello" {
		t.Fatalf("task result = %+v", ev)
	}
	responses := h.stdinOf("control_response")
	if len(responses) != 1 {
		t.Fatalf("control responses = %d", len(responses))
	}
	if resp := responses[0]["response"].(map[string]any); resp["subtype"] != "error" || resp["request_id"] != "hook-1" {
		t.Fatalf("response = %v", resp)
	}
}

// Per-turn cost comes from model_usage_since_last_result, not the cumulative
// total_cost_usd.
func TestTwoTurnCosts(t *testing.T) {
	h := newHarness(t, "twoturn.jsonl", Options{})
	var costs []float64
	for i := 0; i < 2; i++ {
		events, err := h.run(context.Background(), h.turn("s1"))
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range events {
			if ev.Kind == core.RuntimeUsage {
				costs = append(costs, ev.CostUSD)
			}
		}
	}
	if len(costs) != 2 || costs[0] != 0.0029658700000000002 || costs[1] != 0.00313429 {
		t.Fatalf("costs = %v", costs)
	}
}

func TestIdleExitThenResume(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{IdleTimeout: 100 * time.Millisecond})
	if _, err := h.run(context.Background(), h.turn("s1")); err != nil {
		t.Fatal(err)
	}
	h.rt.mu.Lock()
	p := h.rt.procs["s1"]
	h.rt.mu.Unlock()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("idle process did not exit")
	}
	if _, err := h.run(context.Background(), h.turn("s1")); err != nil {
		t.Fatal(err)
	}
	starts := h.starts()
	if len(starts) != 2 || argValue(starts[1].Argv, "--resume") != argValue(starts[0].Argv, "--session-id") {
		t.Fatalf("starts = %d, argv = %v", len(starts), starts[len(starts)-1].Argv)
	}
}

func TestShutdownStopsProcesses(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{})
	if _, err := h.run(context.Background(), h.turn("s1")); err != nil {
		t.Fatal(err)
	}
	h.rt.mu.Lock()
	p := h.rt.procs["s1"]
	h.rt.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.rt.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !p.exited() {
		t.Fatal("process still running after Shutdown")
	}
	if _, err := h.run(context.Background(), h.turn("s2")); err == nil {
		t.Fatal("turn after Shutdown succeeded")
	}
}

func TestInterruptedResultMapping(t *testing.T) {
	data, err := os.ReadFile("testdata/interrupted.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, cancelled := range []bool{true, false} {
		var events []core.RuntimeEvent
		m := &mapper{emit: func(ev core.RuntimeEvent) { events = append(events, ev) }, log: New(core.RuntimeDeps{}, Options{}).log, proc: &procState{}}
		var res *resultLine
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if lr := m.handle([]byte(line)); lr.result != nil {
				res = lr.result
			}
		}
		if res == nil {
			t.Fatal("no result line")
		}
		end := m.endEvent(res, cancelled)
		if cancelled && (end.StopReason != core.RuntimeStopCancelled || end.IsError) {
			t.Fatalf("cancelled end = %+v", end)
		}
		if !cancelled && (!end.IsError || !strings.Contains(end.ErrorText, "ede_diagnostic")) {
			t.Fatalf("uncancelled end = %+v", end)
		}
		if u := m.usageEvent(res); u.CostUSD != 0 {
			t.Fatalf("interrupted turn cost = %v", u.CostUSD)
		}
	}
}

func TestRedact(t *testing.T) {
	got := redact("failed: Authorization: Bearer abc.def token=xyz123456 key sk-ant-api03-zzz mail me@example.com MYKEY", "MYKEY")
	for _, leak := range []string{"abc.def", "xyz123456", "sk-ant-api03-zzz", "me@example.com", "MYKEY"} {
		if strings.Contains(got, leak) {
			t.Fatalf("redact leaked %q: %s", leak, got)
		}
	}
}
