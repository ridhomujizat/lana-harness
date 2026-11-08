package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/tools"
)

// claudeHarness is a harness whose agents may use claude-code targets, with a
// scripted Claude Code runtime.
func claudeHarness(t *testing.T, script mock.Script, agents map[string]core.AgentConfig, run func(context.Context, *fakeRuntime, core.RuntimeTurn) error) (*harness, *fakeRuntime) {
	t.Helper()
	h := newHarness(t, script, agents)
	rt := newFakeRuntime(run)
	h.reg.SetRuntime("claude-code", rt)
	return h, rt
}

// helloScript is a Claude turn that answers "hello from claude".
func helloScript(_ context.Context, _ *fakeRuntime, turn core.RuntimeTurn) error {
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeSessionMeta, ClaudeSessionID: fakeClaudeID, Model: "claude-sonnet-5-5"})
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeAssistantMessage, Text: "hello from claude"})
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeUsage, Usage: core.Usage{InputTokens: 5, OutputTokens: 2}})
	return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopEndTurn})
}

// notLoggedInScript fails before any output, as the runtime does when Claude
// Code is not logged in (REQ-CCT-004).
func notLoggedInScript(_ context.Context, _ *fakeRuntime, turn core.RuntimeTurn) error {
	const msg = "Claude Code is not logged in. Run `claude`, then `/login`."
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeSessionMeta, ClaudeSessionID: fakeClaudeID, Model: "claude-opus-5-5"})
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: core.RuntimeStopError, IsError: true, ErrorText: msg})
	return &core.RuntimeEarlyError{Class: "auth", Message: msg}
}

func kindCounts(events []core.SessionEvent) map[string]int {
	out := map[string]int{}
	for _, e := range events {
		out[e.Kind]++
	}
	return out
}

// AC-CCT-001 (agent side): a claude-code address runs the turn in Claude Code
// with the model from the address.
func TestClaudeCodeAddressRunsInClaude(t *testing.T) {
	h, rt := claudeHarness(t, mock.Script{}, map[string]core.AgentConfig{"assistant": {Model: "claude-code/opus", ReasoningEffort: "off"}}, helloScript)
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "hello from claude" {
		t.Fatalf("result = %+v", result)
	}
	turns := rt.recorded()
	if len(turns) != 1 || turns[0].Config.Model != "opus" || turns[0].Config.Effort != "" {
		t.Fatalf("turns = %+v", turns)
	}
}

// AC-CCT-004: a session switch to a claude-code address runs the next turn in
// Claude Code; switching back runs lana's loop; switching to Claude Code again
// starts a new Claude session (REQ-CCT-003).
func TestSessionSwitchesEngine(t *testing.T) {
	h, rt := claudeHarness(t, mock.Script{Responses: []mock.Response{textResponse("native one"), textResponse("native two")}},
		map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m"}}, helloScript)
	ctx := context.Background()
	if r := h.run(t, "one"); r.Text != "native one" || len(rt.recorded()) != 0 {
		t.Fatalf("native turn = %+v", r)
	}
	if err := h.reg.SetModel(ctx, h.info.ID, "claude-code/sonnet"); err != nil {
		t.Fatal(err)
	}
	if r := h.run(t, "two"); r.Text != "hello from claude" {
		t.Fatalf("claude turn = %+v", r)
	}
	turns := rt.recorded()
	if len(turns) != 1 || turns[0].Config.Model != "sonnet" || turns[0].ResumeID != "" {
		t.Fatalf("claude turns = %+v", turns)
	}
	if err := h.reg.SetModel(ctx, h.info.ID, "mock:m"); err != nil {
		t.Fatal(err)
	}
	if r := h.run(t, "three"); r.Text != "native two" {
		t.Fatalf("native turn after switch = %+v", r)
	}
	if err := h.reg.SetModel(ctx, h.info.ID, "claude-code/sonnet"); err != nil {
		t.Fatal(err)
	}
	h.run(t, "four")
	turns = rt.recorded()
	if len(turns) != 2 || turns[1].ResumeID != "" {
		t.Fatalf("an engine switch resumed the old Claude session: %+v", turns)
	}
	if closes := rt.closedSessions(); len(closes) != 1 || closes[0] != h.info.ID {
		t.Fatalf("closes = %v", closes)
	}
	if err := h.reg.SetModel(ctx, h.info.ID, "claude-code/-x"); err == nil {
		t.Fatal("a flag-like claude-code model was accepted")
	}
}

// AC-CCT-005 (agent side): Claude Code fails before output, so the same turn
// continues in lana's loop with the next target; the attempt and a failed
// claude-code record are kept.
func TestClaudeEarlyFailureFallsBack(t *testing.T) {
	h, rt := claudeHarness(t, mock.Script{Responses: []mock.Response{textResponse("deepseek answer")}},
		map[string]core.AgentConfig{"assistant": {Model: "claude-code/opus", Fallback: []string{"mock:m"}}}, notLoggedInScript)
	var mu sync.Mutex
	var usages []ExternalUsage
	h.reg.SetExternalHooks(ExternalHooks{Usage: func(u ExternalUsage) { mu.Lock(); usages = append(usages, u); mu.Unlock() }})
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "deepseek answer" || len(rt.recorded()) != 1 {
		t.Fatalf("result = %+v", result)
	}
	events := h.events(t)
	k := kindCounts(events)
	if k[core.KindTurnStart] != 1 || k[core.KindTurnEnd] != 1 || k[core.KindUserMessage] != 1 || k[core.KindAssistantAttempt] != 1 {
		t.Fatalf("kinds = %v", k)
	}
	for _, e := range events {
		switch e.Kind {
		case core.KindAssistantAttempt:
			a := payloadOf[core.AssistantAttempt](t, e)
			if a.Provider != "claude-code" || a.Model != "opus" || a.Error.Class != "auth" {
				t.Fatalf("attempt = %+v", a)
			}
		case core.KindStepStart:
			if s := payloadOf[core.StepStart](t, e); s.Provider != "mock" {
				t.Fatalf("step/start = %+v", s)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(usages) != 1 || usages[0].Provider != "claude-code" || usages[0].Outcome != "auth" || usages[0].Requested != "claude-code/opus" {
		t.Fatalf("usages = %+v", usages)
	}
}

// Failure table: Claude Code fails before output and no target is left, so
// the turn fails with the Claude error.
func TestClaudeEarlyFailureWithoutFallback(t *testing.T) {
	h, _ := claudeHarness(t, mock.Script{}, map[string]core.AgentConfig{"assistant": {Model: "claude-code/opus"}}, notLoggedInScript)
	result := h.run(t, "hi")
	if result.StopReason != "error" || result.Err == nil || !strings.Contains(result.Err.Message, "not logged in") {
		t.Fatalf("result = %+v", result)
	}
}

// AC-CCT-006: the Claude process gets lana's prompt head: persona, memory,
// and the note on lana's MCP tools.
func TestClaudePromptHead(t *testing.T) {
	h, rt := claudeHarness(t, mock.Script{}, map[string]core.AgentConfig{"assistant": {Model: "claude-code/opus", Tools: []string{"memory_write"}}}, helloScript)
	if err := h.tools.Register(tools.NewMemoryWrite()); err != nil {
		t.Fatal(err)
	}
	home := agenthome.Paths(h.home, "assistant")
	if err := os.MkdirAll(home.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home.Agent, []byte("You are Bima"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home.Memory, []byte("The owner drinks green tea.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.reg.SetToolHost(&fakeToolHost{})
	h.run(t, "hi")
	turns := rt.recorded()
	if len(turns) != 1 {
		t.Fatalf("turns = %d", len(turns))
	}
	prompt := turns[0].Config.AppendSystemPrompt
	for _, want := range []string{"You are Bima", "green tea", "mcp__lana__"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt has no %q:\n%s", want, prompt)
		}
	}
}

// fakeToolHost records Bind and runs calls the test makes.
type fakeToolHost struct {
	mu      sync.Mutex
	tools   []core.ToolSpec
	call    core.RuntimeToolFunc
	opened  int
	bindErr error
}

func (f *fakeToolHost) Bind(_ string, specs []core.ToolSpec, call core.RuntimeToolFunc) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bindErr != nil {
		return f.bindErr
	}
	f.tools, f.call = specs, call
	return nil
}

func (f *fakeToolHost) Unbind(string) {
	f.mu.Lock()
	f.call = nil
	f.mu.Unlock()
}

func (f *fakeToolHost) Open(string) (string, func(), error) {
	f.mu.Lock()
	f.opened++
	f.mu.Unlock()
	return `{"mcpServers":{}}`, func() {}, nil
}

func (f *fakeToolHost) Close() error { return nil }

func (f *fakeToolHost) bound() core.RuntimeToolFunc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.call
}

type recordCapturer struct {
	mu   sync.Mutex
	reqs []core.CaptureRequest
}

func (c *recordCapturer) Capture(req core.CaptureRequest) {
	c.mu.Lock()
	c.reqs = append(c.reqs, req)
	c.mu.Unlock()
}

// AC-CCT-007 (agent side): Claude calls lana's memory tool over MCP; the call
// runs through lana's tool runner, the memory has the fact, the log shows a
// lana tool call, and memory capture runs after the turn (AC-CCT-009).
func TestClaudeCallsLanaTool(t *testing.T) {
	host := &fakeToolHost{}
	var callOut core.ToolOutput
	h, rt := claudeHarness(t, mock.Script{}, map[string]core.AgentConfig{"assistant": {
		Model: "claude-code/opus", Permission: "trust", Tools: []string{"memory_write", "read_file", "bash"},
	}}, func(ctx context.Context, _ *fakeRuntime, turn core.RuntimeTurn) error {
		if turn.OpenMCP == nil {
			return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopError, IsError: true, ErrorText: "no MCP"})
		}
		if _, _, err := turn.OpenMCP(); err != nil {
			return err
		}
		allow, _ := turn.Approve(ctx, core.RuntimeApproval{CallID: "toolu_1", ToolName: "mcp__lana__memory_write", Risk: core.RiskExec})
		if !allow {
			return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopError, IsError: true, ErrorText: "denied"})
		}
		input := json.RawMessage(`{"target":"memory","action":"add","content":"The owner likes green tea."}`)
		turn.Emit(core.RuntimeEvent{Kind: core.RuntimeToolCall, ID: "toolu_1", Name: "mcp__lana__memory_write", Input: input, Risk: core.RiskExec})
		callOut = host.bound()(ctx, "toolu_1", "memory_write", input)
		turn.Emit(core.RuntimeEvent{Kind: core.RuntimeToolResult, ID: "toolu_1", Output: callOut.Output, IsError: callOut.Status != core.ToolStatusSuccess})
		turn.Emit(core.RuntimeEvent{Kind: core.RuntimeAssistantMessage, Text: "Saved."})
		return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopEndTurn})
	})
	if err := h.tools.Register(tools.NewMemoryWrite()); err != nil {
		t.Fatal(err)
	}
	h.reg.SetToolHost(host)
	capturer := &recordCapturer{}
	h.reg.SetMemoryCapturer(capturer)
	result := h.run(t, "remember that I like green tea")
	if result.StopReason != "end_turn" || callOut.Status != core.ToolStatusSuccess {
		t.Fatalf("result = %+v, call = %+v", result, callOut)
	}
	if len(host.tools) != 1 || host.tools[0].Name != "memory_write" || host.opened != 1 {
		t.Fatalf("host tools = %+v opened = %d", host.tools, host.opened)
	}
	if turns := rt.recorded(); turns[0].Config.MCPKey != "memory_write" {
		t.Fatalf("mcp key = %q", turns[0].Config.MCPKey)
	}
	data, err := os.ReadFile(filepath.Join(h.home, "agents", "assistant", "MEMORY.md"))
	if err != nil || !strings.Contains(string(data), "green tea") {
		t.Fatalf("memory = %q, %v", data, err)
	}
	found := false
	for _, e := range h.events(t) {
		if e.Kind == core.KindToolCall {
			call := payloadOf[core.ToolCallPayload](t, e)
			found = call.Name == "memory_write" && !call.External && call.CallID == "toolu_1"
		}
	}
	if !found {
		t.Fatal("no lana tool/call for memory_write")
	}
	capturer.mu.Lock()
	defer capturer.mu.Unlock()
	if len(capturer.reqs) != 1 || capturer.reqs[0].Targets[0].Provider != "claude-code" || !hasToolSpec(capturer.reqs[0].Tools, "memory_write") {
		t.Fatalf("capture = %+v", capturer.reqs)
	}
}

// Failure table: a host that cannot serve leaves the turn without lana tools
// and writes a note.
func TestClaudeToolHostFailureNote(t *testing.T) {
	h, rt := claudeHarness(t, mock.Script{}, map[string]core.AgentConfig{"assistant": {Model: "claude-code/opus", Tools: []string{"memory_write"}}}, helloScript)
	if err := h.tools.Register(tools.NewMemoryWrite()); err != nil {
		t.Fatal(err)
	}
	h.reg.SetToolHost(&fakeToolHost{bindErr: os.ErrPermission})
	if r := h.run(t, "hi"); r.StopReason != "end_turn" {
		t.Fatalf("result = %+v", r)
	}
	if turns := rt.recorded(); turns[0].OpenMCP != nil {
		t.Fatal("OpenMCP set although the host failed")
	}
	note := false
	for _, e := range h.events(t) {
		if e.Kind == core.KindSystemMessage {
			if m := payloadOf[core.SystemMessage](t, e); m.Subtype == core.SystemSubtypeNote && strings.Contains(m.Text, "not available") {
				note = true
			}
		}
	}
	if !note {
		t.Fatal("no note")
	}
}

// AC-CCT-010 (agent side), REQ-CCT-008: a scheduled run skips Claude Code
// without allow_scheduled, fails with the exact message when nothing else is
// left, and uses Claude Code with allow_scheduled.
func TestScheduledRunSkipsClaude(t *testing.T) {
	h, rt := claudeHarness(t, mock.Script{Responses: []mock.Response{textResponse("deepseek answer")}},
		map[string]core.AgentConfig{"assistant": {Model: "claude-code/opus", Fallback: []string{"mock:m"}}}, helloScript)
	release := h.reg.MarkScheduled(h.info.ID)
	if r := h.run(t, "scheduled"); r.Text != "deepseek answer" || len(rt.recorded()) != 0 {
		t.Fatalf("result = %+v turns = %d", r, len(rt.recorded()))
	}
	release()
	if r := h.run(t, "interactive"); r.Text != "hello from claude" {
		t.Fatalf("interactive = %+v", r)
	}

	h2, rt2 := claudeHarness(t, mock.Script{}, map[string]core.AgentConfig{"assistant": {Model: "claude-code/opus"}}, helloScript)
	defer h2.reg.MarkScheduled(h2.info.ID)()
	r := h2.run(t, "scheduled")
	if r.StopReason != "error" || r.Err == nil || r.Err.Message != "claude-code is not allowed for scheduled runs of agent 'assistant'" || len(rt2.recorded()) != 0 {
		t.Fatalf("result = %+v", r)
	}

	h3, rt3 := claudeHarness(t, mock.Script{}, map[string]core.AgentConfig{"assistant": {Model: "claude-code/opus", AllowScheduled: true}}, helloScript)
	defer h3.reg.MarkScheduled(h3.info.ID)()
	if r := h3.run(t, "scheduled"); r.Text != "hello from claude" || len(rt3.recorded()) != 1 {
		t.Fatalf("allowed = %+v", r)
	}
}
