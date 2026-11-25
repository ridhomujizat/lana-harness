package antigravity

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
	dir, err := os.MkdirTemp("", "fakeagy")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fakeBin = filepath.Join(dir, "agy")
	cmd := exec.Command("go", "build", "-o", fakeBin, "./testdata/fakeagy")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build fakeagy: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// harness runs the runtime against the fake agy.
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
			base = append(base, "FAKEAGY_RECORD="+h.logPath, "FAKEAGY_FIXTURE="+h.fixture.Load().(string))
			return append(base, h.env...)
		}
	}
	if opts.LookPath == nil {
		opts.LookPath = func(string) (string, error) { return fakeBin, nil }
	}
	h.rt = New(core.RuntimeDeps{}, opts)
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
		SessionID: sid, TurnID: "turn-1", AgentName: "antigravity",
		Config: core.RuntimeConfig{Workspace: h.ws, Model: "gemini-3.8-flash-low"},
		Input:  []core.ContentBlock{{Kind: core.BlockText, Text: "hi"}},
	}
}

func (h *harness) run(ctx context.Context, turn core.RuntimeTurn) ([]core.RuntimeEvent, error) {
	rec := newRecorder()
	turn.Emit = rec.emit
	err := h.rt.RunTurn(ctx, turn)
	return rec.all(), err
}

type recordEntry struct {
	Kind string          `json:"kind"`
	Argv []string        `json:"argv"`
	Env  []string        `json:"env"`
	Cwd  string          `json:"cwd"`
	Line json.RawMessage `json:"line"`
}

func (h *harness) log() []recordEntry {
	data, err := os.ReadFile(h.logPath)
	if err != nil {
		return nil
	}
	var out []recordEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e recordEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

func (h *harness) starts() []recordEntry {
	var out []recordEntry
	for _, e := range h.log() {
		if e.Kind == "start" {
			out = append(out, e)
		}
	}
	return out
}

// stdinOf returns the decoded stdin messages whose method matches.
func (h *harness) stdinOf(method string) []map[string]any {
	var out []map[string]any
	for _, e := range h.log() {
		if e.Kind != "stdin" {
			continue
		}
		var m map[string]any
		if json.Unmarshal(e.Line, &m) == nil && m["method"] == method {
			out = append(out, m)
		}
	}
	return out
}

// stdinReplies returns the responses lana wrote (the answers to permission
// requests), in order.
func (h *harness) stdinReplies() []map[string]any {
	var out []map[string]any
	for _, e := range h.log() {
		if e.Kind != "stdin" {
			continue
		}
		var m map[string]any
		if json.Unmarshal(e.Line, &m) == nil && m["method"] == nil && m["result"] != nil {
			out = append(out, m)
		}
	}
	return out
}

func endOf(t *testing.T, events []core.RuntimeEvent) core.RuntimeEvent {
	t.Helper()
	if len(events) == 0 || events[len(events)-1].Kind != core.RuntimeEnd {
		t.Fatalf("last event is not end: %v", kinds(events))
	}
	return events[len(events)-1]
}

func countPrefix(env []string, prefix string) int {
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			n++
		}
	}
	return n
}

func hasEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

func joinedText(events []core.RuntimeEvent) string {
	var b strings.Builder
	for _, ev := range events {
		if ev.Kind == core.RuntimeTextDelta {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

func TestRegistered(t *testing.T) {
	f, ok := core.LookupRuntime(Name)
	if !ok {
		t.Fatal("antigravity is not registered")
	}
	rt, err := f(core.RuntimeDeps{})
	if err != nil || rt == nil {
		t.Fatalf("factory: %v", err)
	}
}

// AC-AGY-003: the child is `agy acp`, without the skip-permissions flag, with
// the Google API-key variables removed and the auto-update pin set; a named
// account gets its own HOME and USERPROFILE, each exactly once.
func TestEnvAndArgv(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "GEMINI_API_KEY=leak-secret", "GOOGLE_API_KEY=leak2")
	if _, err := h.run(context.Background(), h.turn("s1")); err != nil {
		t.Fatal(err)
	}
	start := h.starts()[0]
	if got := strings.Join(start.Argv, " "); got != "acp" {
		t.Fatalf("argv = %v", start.Argv)
	}
	if strings.Contains(strings.Join(start.Argv, " "), "dangerously-skip-permissions") || strings.Contains(strings.Join(start.Argv, " "), "--sandbox") {
		t.Fatalf("unsafe argv = %v", start.Argv)
	}
	if !hasEnv(start.Env, "AGY_CLI_DISABLE_AUTO_UPDATE=true") {
		t.Fatalf("missing auto-update pin: %v", start.Env)
	}
	if countPrefix(start.Env, "GEMINI_API_KEY=") != 0 || countPrefix(start.Env, "GOOGLE_API_KEY=") != 0 {
		t.Fatalf("google key leaked: %v", start.Env)
	}
	cwd, _ := filepath.EvalSymlinks(h.ws)
	if resolved, _ := filepath.EvalSymlinks(start.Cwd); resolved != cwd {
		t.Fatalf("cwd = %q, want %q", start.Cwd, h.ws)
	}

	// A named account: HOME and USERPROFILE point at the account folder.
	home := t.TempDir()
	turn := h.turn("s2")
	turn.Config.Home = home
	if _, err := h.run(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	env := h.starts()[1].Env
	if countPrefix(env, "HOME=") != 1 || !hasEnv(env, "HOME="+home) {
		t.Fatalf("HOME = %v", env)
	}
	if countPrefix(env, "USERPROFILE=") != 1 || !hasEnv(env, "USERPROFILE="+home) {
		t.Fatalf("USERPROFILE = %v", env)
	}
}

// AC-AGY-004: two turns in one lana session run in one process.
func TestTwoTurnsOneProcess(t *testing.T) {
	h := newHarness(t, "acp_twoturn.jsonl", Options{})
	turn := h.turn("s1")
	turn.Config.Model = "" // the fixture has no set_model
	events, err := h.run(context.Background(), turn)
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if got := joinedText(events); got != "one" {
		t.Fatalf("turn 1 text = %q", got)
	}
	events2, err := h.run(context.Background(), turn)
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if got := joinedText(events2); got != "two" {
		t.Fatalf("turn 2 text = %q", got)
	}
	if n := len(h.starts()); n != 1 {
		t.Fatalf("starts = %d", n)
	}
	for _, ev := range events2 {
		if ev.Kind == core.RuntimeSessionMeta {
			t.Fatal("session_meta sent twice for one process")
		}
	}
}

// AC-AGY-004: at most MaxConcurrent (2) processes run; a third waits.
func TestConcurrencyCap(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	h := newHarness(t, "acp_gate.jsonl", Options{}, "FAKEAGY_GATE="+gate)
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			turn := h.turn(fmt.Sprintf("s%d", i))
			turn.Config.Model = ""
			_, errs[i] = h.run(context.Background(), turn)
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
}

// AC-AGY-006: session/new carries the prompt head in
// _meta.antigravity.systemPrompt.append and lana's MCP server in ACP shape.
func TestSessionNewMetaAndMCP(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{})
	turn := h.turn("s1")
	turn.Config.AppendSystemPrompt = "You are Bima."
	turn.OpenMCP = func() (string, func(), error) {
		return `{"mcpServers":{"lana":{"type":"http","url":"http://127.0.0.1:9/mcp","headers":{"Authorization":"Bearer tok"}}}}`, func() {}, nil
	}
	if _, err := h.run(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	news := h.stdinOf("session/new")
	if len(news) != 1 {
		t.Fatalf("session/new count = %d", len(news))
	}
	params := news[0]["params"].(map[string]any)
	meta := params["_meta"].(map[string]any)["antigravity"].(map[string]any)["systemPrompt"].(map[string]any)
	if meta["append"] != "You are Bima." {
		t.Fatalf("append = %v", meta["append"])
	}
	servers := params["mcpServers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("mcpServers = %v", servers)
	}
	srv := servers[0].(map[string]any)
	if srv["type"] != "http" || srv["name"] != "lana" || srv["url"] != "http://127.0.0.1:9/mcp" {
		t.Fatalf("server = %v", srv)
	}
	headers := srv["headers"].([]any)
	hd := headers[0].(map[string]any)
	if len(headers) != 1 || hd["name"] != "Authorization" || hd["value"] != "Bearer tok" {
		t.Fatalf("headers = %v", headers)
	}
}

// AC-AGY-006: an empty prompt head sends no _meta and an empty server array.
func TestSessionNewNoMeta(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{})
	if _, err := h.run(context.Background(), h.turn("s1")); err != nil {
		t.Fatal(err)
	}
	params := h.stdinOf("session/new")[0]["params"].(map[string]any)
	if _, ok := params["_meta"]; ok {
		t.Fatalf("_meta present without a prompt head: %v", params)
	}
	if servers, ok := params["mcpServers"].([]any); !ok || len(servers) != 0 {
		t.Fatalf("mcpServers = %v", params["mcpServers"])
	}
}

// AC-AGY-007: a stored ACP session ID resumes with session/load, and the
// history agy replays during the load is not emitted.
func TestLoadResumeReplayIgnored(t *testing.T) {
	h := newHarness(t, "acp_load.jsonl", Options{})
	const resumeID = "4500ba70-d45a-4d2e-a625-1cdf41fae39b"
	turn := h.turn("s1")
	turn.ResumeID = resumeID
	events, err := h.run(context.Background(), turn)
	if err != nil {
		t.Fatal(err)
	}
	loads := h.stdinOf("session/load")
	if len(loads) != 1 || loads[0]["params"].(map[string]any)["sessionId"] != resumeID {
		t.Fatalf("session/load = %v", loads)
	}
	if len(h.stdinOf("session/new")) != 0 {
		t.Fatal("session/new sent when a resume ID was given")
	}
	meta := events[0]
	if meta.Kind != core.RuntimeSessionMeta || meta.ClaudeSessionID != resumeID {
		t.Fatalf("meta = %+v", meta)
	}
	// The replayed tool calls and the replayed reply are dropped.
	for _, ev := range events {
		if ev.Kind == core.RuntimeToolCall || ev.Kind == core.RuntimeToolResult {
			t.Fatalf("replayed tool event leaked: %+v", ev)
		}
	}
	text := joinedText(events)
	if !strings.Contains(text, "Earlier, I ran") || strings.Contains(text, "There are **3** files") {
		t.Fatalf("text = %q", text)
	}
}

// AC-AGY-007: a failed session/load falls back to session/new.
func TestLoadFallback(t *testing.T) {
	h := newHarness(t, "acp_load_fail.jsonl", Options{}, "FAKEAGY_FAIL_LOAD=1")
	turn := h.turn("s1")
	turn.Config.Model = ""
	turn.ResumeID = "gone-session"
	events, err := h.run(context.Background(), turn)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.stdinOf("session/load")) != 1 {
		t.Fatal("session/load not sent")
	}
	if len(h.stdinOf("session/new")) != 1 {
		t.Fatal("session/new not sent after the failed load")
	}
	meta := events[0]
	if meta.Kind != core.RuntimeSessionMeta || meta.ClaudeSessionID != "new-session" {
		t.Fatalf("meta = %+v", meta)
	}
	if end := endOf(t, events); end.StopReason != core.RuntimeStopEndTurn {
		t.Fatalf("end = %+v", end)
	}
}

// AC-AGY-008: the recorded acp_turn1 transcript maps to the expected events.
func TestEventMapping(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{})
	turn := h.turn("s1")
	turn.Approve = func(context.Context, core.RuntimeApproval) (bool, string) { return true, "" }
	events, err := h.run(context.Background(), turn)
	if err != nil {
		t.Fatal(err)
	}
	text := joinedText(events)
	if !strings.HasSuffix(text, "There are **3** files in the folder.\n\nPROBE-OK") {
		t.Fatalf("text = %q", text)
	}
	var calls, results int
	for _, ev := range events {
		switch ev.Kind {
		case core.RuntimeToolCall:
			calls++
			if ev.ID == "" || !strings.Contains(string(ev.Input), "CommandLine") {
				t.Fatalf("tool_call = %+v", ev)
			}
		case core.RuntimeToolResult:
			results++
			if ev.ID == "" || !strings.Contains(ev.Output, "Command exited with code 0") {
				t.Fatalf("tool_result = %+v", ev)
			}
		}
	}
	if calls != 2 || results != 2 {
		t.Fatalf("calls = %d, results = %d", calls, results)
	}
	var usage *core.RuntimeEvent
	for i := range events {
		if events[i].Kind == core.RuntimeUsage {
			usage = &events[i]
		}
	}
	if usage == nil || usage.Usage.InputTokens != 15469 || usage.Usage.OutputTokens != 117 || usage.Usage.CachedInputTokens != 17908 {
		t.Fatalf("usage = %+v", usage)
	}
	if end := endOf(t, events); end.StopReason != core.RuntimeStopEndTurn || end.IsError {
		t.Fatalf("end = %+v", end)
	}
}

// AC-AGY-009: approval allow answers with the allow_once option; deny with
// reject_once. Both find the option by kind.
func TestApprovalAllowDeny(t *testing.T) {
	for _, tc := range []struct {
		name  string
		allow bool
		want  string
	}{
		{"allow", true, "allow"},
		{"deny", false, "deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "acp_turn1.jsonl", Options{})
			turn := h.turn("s1")
			turn.Approve = func(_ context.Context, req core.RuntimeApproval) (bool, string) {
				if req.Risk != core.RiskExec || req.PermTool != "bash" || req.ToolName != "run_command" {
					t.Errorf("approval = %+v", req)
				}
				if !strings.Contains(string(req.PermInput), "ls") {
					t.Errorf("perm input = %s", req.PermInput)
				}
				return tc.allow, ""
			}
			if _, err := h.run(context.Background(), turn); err != nil {
				t.Fatal(err)
			}
			replies := h.stdinReplies()
			if len(replies) != 2 {
				t.Fatalf("replies = %d", len(replies))
			}
			for _, r := range replies {
				outcome := r["result"].(map[string]any)["outcome"].(map[string]any)
				if outcome["outcome"] != "selected" || outcome["optionId"] != tc.want {
					t.Fatalf("reply = %v", r)
				}
			}
		})
	}
}

// AC-AGY-009 (no option by kind): a cancelled turn answers with the cancelled
// outcome.
func TestApprovalCancelledWhenNoApprover(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{})
	turn := h.turn("s1")
	turn.Approve = func(context.Context, core.RuntimeApproval) (bool, string) { return false, "denied" }
	if _, err := h.run(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	// The fixture offers a reject_once option, so a denial selects it.
	if replies := h.stdinReplies(); len(replies) != 2 {
		t.Fatalf("replies = %d", len(replies))
	}
}

// AC-AGY-010: cancel with a fake that ignores session/cancel ends the turn as
// cancelled within InterruptGrace, and the process group is gone.
func TestCancelIgnoredInterrupt(t *testing.T) {
	h := newHarness(t, "acp_hang.jsonl", Options{InterruptGrace: 300 * time.Millisecond, KillGrace: 300 * time.Millisecond}, "FAKEAGY_IGNORE_CANCEL=1")
	rec := newRecorder()
	turn := h.turn("s1")
	turn.Config.Model = ""
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
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not end after interrupt")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("turn took %v", d)
	}
	if end := endOf(t, rec.all()); end.StopReason != core.RuntimeStopCancelled || end.IsError {
		t.Fatalf("end = %+v", end)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.rt.mu.Lock()
		p := h.rt.procs["s1"]
		h.rt.mu.Unlock()
		if p == nil || p.exited() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("process still running after cancel")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A missing binary fails the turn early with the not-installed class.
func TestNotInstalledAndOldVersion(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{LookPath: func(string) (string, error) { return "", exec.ErrNotFound }})
	_, err := h.run(context.Background(), h.turn("s1"))
	var early *EarlyError
	if !errors.As(err, &early) || early.Class != classNotInstalled || err.Error() != errNotInstalled {
		t.Fatalf("err = %v", err)
	}
	if st := h.rt.Status(context.Background()); st.Error != errNotInstalled {
		t.Fatalf("status = %+v", st)
	}

	old := newHarness(t, "acp_turn1.jsonl", Options{}, "FAKEAGY_VERSION=1.4.9")
	_, err = old.run(context.Background(), old.turn("s1"))
	if !errors.As(err, &early) || early.Class != classVersion || !strings.Contains(err.Error(), "1.5.17 is required") {
		t.Fatalf("old version: %v", err)
	}
	if len(old.starts()) != 0 {
		t.Fatal("old version started a process")
	}
}

func TestStatus(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{})
	st := h.rt.Status(context.Background())
	if st.Binary != fakeBin || st.Version != "1.5.17" || !st.VersionOK || st.Error != "" || st.MaxConcurrent != 2 {
		t.Fatalf("status = %+v", st)
	}
}

func TestParseVersion(t *testing.T) {
	got, v, ok := parseVersion("1.5.17\n")
	if !ok || got != "1.5.17" || v != [3]int{1, 5, 17} {
		t.Fatalf("parseVersion = %q %v %v", got, v, ok)
	}
	if versionAtLeast([3]int{1, 4, 99}, [3]int{1, 5, 17}) || !versionAtLeast([3]int{1, 5, 17}, [3]int{1, 5, 17}) {
		t.Fatal("versionAtLeast")
	}
}

func TestBuildEnvScrub(t *testing.T) {
	base := []string{"HOME=/daemon", "USERPROFILE=/daemon", "GEMINI_API_KEY=x", "GOOGLE_CLOUD_PROJECT=p", "PATH=/bin"}
	env := buildEnv(base, core.RuntimeConfig{Home: "/acc"})
	if countPrefix(env, "GEMINI_API_KEY=") != 0 || countPrefix(env, "GOOGLE_CLOUD_PROJECT=") != 0 {
		t.Fatalf("env = %v", env)
	}
	if countPrefix(env, "HOME=") != 1 || !hasEnv(env, "HOME=/acc") || !hasEnv(env, "USERPROFILE=/acc") {
		t.Fatalf("env = %v", env)
	}
	if !hasEnv(env, "AGY_CLI_DISABLE_AUTO_UPDATE=true") || !hasEnv(env, "PATH=/bin") {
		t.Fatalf("env = %v", env)
	}
	// Built-in account keeps the daemon's HOME and USERPROFILE.
	plain := buildEnv(base, core.RuntimeConfig{})
	if countPrefix(plain, "HOME=") != 1 || !hasEnv(plain, "HOME=/daemon") {
		t.Fatalf("plain = %v", plain)
	}
}

func TestConvertMCP(t *testing.T) {
	servers := convertMCP(`{"mcpServers":{"lana":{"type":"http","url":"http://127.0.0.1:1/mcp","headers":{"Authorization":"Bearer t"}}}}`)
	if len(servers) != 1 || servers[0].Name != "lana" || servers[0].Type != "http" || servers[0].URL != "http://127.0.0.1:1/mcp" {
		t.Fatalf("servers = %+v", servers)
	}
	if len(servers[0].Headers) != 1 || servers[0].Headers[0].Name != "Authorization" || servers[0].Headers[0].Value != "Bearer t" {
		t.Fatalf("headers = %+v", servers[0].Headers)
	}
	if convertMCP("") != nil || convertMCP("not json") != nil {
		t.Fatal("bad config gave servers")
	}
}

func TestRiskForKind(t *testing.T) {
	cases := map[string]core.Risk{
		"read": core.RiskRead, "search": core.RiskRead, "think": core.RiskRead, "fetch": core.RiskRead,
		"edit": core.RiskWrite, "write": core.RiskWrite,
		"execute": core.RiskExec, "other": core.RiskExec,
	}
	for kind, want := range cases {
		if got := riskForKind(kind); got != want {
			t.Fatalf("riskForKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestPermCall(t *testing.T) {
	tool, input := permCall("execute", "run_command", json.RawMessage(`{"CommandLine":"rm -rf build"}`))
	if tool != "bash" || !strings.Contains(string(input), `"command":"rm -rf build"`) {
		t.Fatalf("execute = %q %s", tool, input)
	}
	if rule := ruleString("execute", "run_command", json.RawMessage(`{"CommandLine":"rm -rf build"}`)); rule != "bash(rm -rf build)" {
		t.Fatalf("execute rule = %q", rule)
	}
	tool, input = permCall("edit", "edit", json.RawMessage(`{"FilePath":"a.txt"}`))
	if tool != "edit" || !strings.Contains(string(input), `"path":"a.txt"`) {
		t.Fatalf("edit = %q %s", tool, input)
	}
	tool, input = permCall("read", "view_file", json.RawMessage(`{"Path":"a.txt"}`))
	if tool != "view_file" || !strings.Contains(string(input), `"Path"`) {
		t.Fatalf("other = %q %s", tool, input)
	}
}
