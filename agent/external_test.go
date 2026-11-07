package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/tools/permission"
)

// fakeRuntime is a scripted core.AgentRuntime.
type fakeRuntime struct {
	mu         sync.Mutex
	turns      []core.RuntimeTurn
	interrupts []string
	shutdown   bool
	interrupt  chan string
	script     func(ctx context.Context, rt *fakeRuntime, turn core.RuntimeTurn) error
}

func newFakeRuntime(script func(ctx context.Context, rt *fakeRuntime, turn core.RuntimeTurn) error) *fakeRuntime {
	return &fakeRuntime{script: script, interrupt: make(chan string, 16)}
}

func (f *fakeRuntime) RunTurn(ctx context.Context, turn core.RuntimeTurn) error {
	f.mu.Lock()
	f.turns = append(f.turns, turn)
	f.mu.Unlock()
	return f.script(ctx, f, turn)
}

func (f *fakeRuntime) Interrupt(sessionID string) error {
	f.mu.Lock()
	f.interrupts = append(f.interrupts, sessionID)
	f.mu.Unlock()
	select {
	case f.interrupt <- sessionID:
	default:
	}
	return nil
}

func (f *fakeRuntime) Close(string) {}

func (f *fakeRuntime) Shutdown(context.Context) error {
	f.mu.Lock()
	f.shutdown = true
	f.mu.Unlock()
	return nil
}

func (f *fakeRuntime) Status(context.Context) core.RuntimeStatus { return core.RuntimeStatus{} }

func (f *fakeRuntime) recorded() []core.RuntimeTurn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.RuntimeTurn(nil), f.turns...)
}

// end emits the end event and returns the RunTurn error the contract asks for.
func end(turn core.RuntimeTurn, ev core.RuntimeEvent) error {
	ev.Kind = core.RuntimeEnd
	turn.Emit(ev)
	if ev.IsError {
		return errors.New(ev.ErrorText)
	}
	return nil
}

const fakeClaudeID = "11111111-2222-4333-8444-555555555555"

// bashScript replays a Claude turn with a Bash call: init, tool call, tool
// result, text, plan window, usage, end (AC-CCR-004).
func bashScript(_ context.Context, _ *fakeRuntime, turn core.RuntimeTurn) error {
	util := 0.42
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeSessionMeta, ClaudeSessionID: fakeClaudeID, Model: "claude-haiku-5-5", Tools: []string{"Bash", "Read"}, Version: "2.1.319"})
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeToolCall, ID: "toolu_1", Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`), Risk: core.RiskExec})
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeToolResult, ID: "toolu_1", Output: "notes.txt"})
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeTextDelta, Text: "The directory has "})
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeTextDelta, Text: "notes.txt."})
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeAssistantMessage, Text: "The directory has notes.txt."})
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeLimits, Windows: []core.RuntimeLimitWindow{{Type: "five_hour", Status: "allowed", Utilization: &util, ResetsAt: time.Unix(1794088200, 0).UTC()}}})
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeUsage, Usage: core.Usage{InputTokens: 100, OutputTokens: 20, CachedInputTokens: 60}, CostUSD: 0.0037, Model: "claude-haiku-5-5", DurationMS: 4428})
	return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopEndTurn})
}

func newExternalHarness(t *testing.T, cfg core.AgentConfig, script func(context.Context, *fakeRuntime, core.RuntimeTurn) error) (*harness, *fakeRuntime) {
	t.Helper()
	cfg.Runtime = "claude-code"
	h := newHarness(t, mock.Script{}, map[string]core.AgentConfig{"assistant": cfg})
	rt := newFakeRuntime(script)
	h.reg.SetRuntime("claude-code", rt)
	return h, rt
}

func payloadOf[T any](t *testing.T, event core.SessionEvent) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(event.Payload, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// subsequence reports whether want appears in got in order.
func subsequence(got, want []string) bool {
	i := 0
	for _, kind := range got {
		if i < len(want) && kind == want[i] {
			i++
		}
	}
	return i == len(want)
}

// AC-CCR-004, API-CCR-002: a replayed Bash turn writes the native event kinds
// with an external tool call, the runtime session meta, and step usage; usage
// and plan windows reach the host hooks (AC-CCR-009 data side).
func TestExternalTurnEvents(t *testing.T) {
	h, rt := newExternalHarness(t, core.AgentConfig{Model: "haiku", ReasoningEffort: "high", Workspace: "/ignored"}, bashScript)
	var mu sync.Mutex
	var usages []ExternalUsage
	var limits [][]core.RuntimeLimitWindow
	var limitProvider, limitKind string
	h.reg.SetExternalHooks(ExternalHooks{
		Usage: func(u ExternalUsage) { mu.Lock(); usages = append(usages, u); mu.Unlock() },
		Limits: func(provider, runtime string, w []core.RuntimeLimitWindow) {
			mu.Lock()
			limits = append(limits, w)
			limitProvider, limitKind = provider, runtime
			mu.Unlock()
		},
	})
	sub, err := h.reg.SubscribeLive(h.info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	result := h.run(t, "list files")
	if result.StopReason != "end_turn" || result.Err != nil || result.Text != "The directory has notes.txt." {
		t.Fatalf("result = %+v", result)
	}
	if result.Usage.InputTokens != 100 || result.Usage.OutputTokens != 20 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	events := h.events(t)
	got := kinds(events)
	want := []string{
		core.KindTurnStart, core.KindUserMessage, core.KindSystemMessage, core.KindStepStart,
		core.KindAssistantMessage, core.KindToolCall, core.KindToolResult, core.KindAssistantMessage,
		core.KindStepEnd, core.KindTurnEnd,
	}
	if !subsequence(got, want) {
		t.Fatalf("kinds = %v", got)
	}
	for _, event := range events {
		switch event.Kind {
		case core.KindSystemMessage:
			meta := payloadOf[core.SystemMessage](t, event)
			if meta.Subtype != "runtime" || meta.Runtime != "claude-code" || meta.ClaudeSessionID != fakeClaudeID || meta.Model != "claude-haiku-5-5" || meta.Version != "2.1.319" || len(meta.Tools) != 2 {
				t.Fatalf("meta = %+v", meta)
			}
			raw := string(event.Payload)
			if !strings.Contains(raw, `"claude_session_id":"`+fakeClaudeID+`"`) || !strings.Contains(raw, `"subtype":"runtime"`) {
				t.Fatalf("meta json = %s", raw)
			}
		case core.KindStepStart:
			step := payloadOf[core.StepStart](t, event)
			if step.Provider != "claude-agent" || step.Model != "claude-haiku-5-5" {
				t.Fatalf("step/start = %+v", step)
			}
		case core.KindToolCall:
			call := payloadOf[core.ToolCallPayload](t, event)
			if !call.External || call.Name != "Bash" || call.CallID != "toolu_1" || !strings.Contains(string(event.Payload), `"external":true`) {
				t.Fatalf("tool/call = %s", event.Payload)
			}
		case core.KindToolResult:
			res := payloadOf[core.ToolResultPayload](t, event)
			if res.Status != core.ToolStatusSuccess || res.Output != "notes.txt" {
				t.Fatalf("tool/result = %+v", res)
			}
		case core.KindStepEnd:
			step := payloadOf[core.StepEnd](t, event)
			if step.Provider != "claude-agent" || step.Usage.InputTokens != 100 || step.Usage.OutputTokens != 20 || step.Usage.CachedInputTokens != 60 {
				t.Fatalf("step/end = %+v", step)
			}
		case core.KindTurnEnd:
			if end := payloadOf[core.TurnEnd](t, event); end.Reason != "end_turn" || end.Error != nil {
				t.Fatalf("turn/end = %+v", end)
			}
		}
	}
	// The first assistant message holds the tool call; the second the text.
	var assistants []core.AssistantMessage
	for _, event := range events {
		if event.Kind == core.KindAssistantMessage {
			assistants = append(assistants, payloadOf[core.AssistantMessage](t, event))
		}
	}
	if len(assistants) != 2 || assistants[0].Content[0].Kind != core.BlockToolUse || assistants[0].StopReason != "tool_use" ||
		assistants[1].Content[0].Text != "The directory has notes.txt." {
		t.Fatalf("assistant messages = %+v", assistants)
	}

	turns := rt.recorded()
	if len(turns) != 1 {
		t.Fatalf("turns = %d", len(turns))
	}
	turn := turns[0]
	ws, err := normalizePath(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	if turn.ResumeID != "" || turn.Config.Model != "haiku" || turn.Config.Effort != "high" || turn.Config.Workspace != ws || turn.AgentName != "assistant" {
		t.Fatalf("turn = %+v", turn)
	}
	if len(turn.Input) != 1 || turn.Input[0].Text != "list files" {
		t.Fatalf("input = %+v", turn.Input)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(usages) != 1 {
		t.Fatalf("usages = %+v", usages)
	}
	u := usages[0]
	if u.Provider != "claude-agent" || u.Runtime != "claude-code" || u.Model != "claude-haiku-5-5" || u.Outcome != "ok" ||
		u.CostUSD != 0.0037 || u.DurationMS != 4428 || u.Usage.InputTokens != 100 || u.Agent != "assistant" || u.SessionID != h.info.ID || u.TurnID != result.TurnID {
		t.Fatalf("usage = %+v", u)
	}
	if len(limits) != 1 || limitProvider != "claude-agent" || limitKind != "claude-code" || limits[0][0].Type != "five_hour" || *limits[0][0].Utilization != 0.42 {
		t.Fatalf("limits = %+v %s %s", limits, limitProvider, limitKind)
	}

	// Text deltas stream through the hub like a native turn.
	var deltas []string
	rctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for len(deltas) < 2 {
		got, err := sub.Recv(rctx)
		if err != nil {
			t.Fatalf("deltas = %v: %v", deltas, err)
		}
		if got.Value.Kind == "text_delta" {
			deltas = append(deltas, got.Value.Text)
		}
	}
	if strings.Join(deltas, "") != "The directory has notes.txt." {
		t.Fatalf("deltas = %v", deltas)
	}
}

// AC-CCR-002: the stored Claude session ID is resumed by a new registry and
// runtime, as after a daemon restart.
func TestExternalTurnResumeAfterRestart(t *testing.T) {
	h, rt := newExternalHarness(t, core.AgentConfig{}, bashScript)
	h.run(t, "first")
	if turns := rt.recorded(); len(turns) != 1 || turns[0].ResumeID != "" {
		t.Fatalf("first turn = %+v", turns)
	}
	if err := h.reg.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	shutdown := rt.shutdown
	rt.mu.Unlock()
	if !shutdown {
		t.Fatal("Close did not shut the runtime down")
	}

	// A new registry over the same store, as after a restart.
	reg := NewRegistry(h.store, h.llm, h.tools, h.hooks, h.reg.config, nil, h.home, h.ws)
	next := newFakeRuntime(func(_ context.Context, _ *fakeRuntime, turn core.RuntimeTurn) error {
		turn.Emit(core.RuntimeEvent{Kind: core.RuntimeAssistantMessage, Text: "again"})
		return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopEndTurn})
	})
	reg.SetRuntime("claude-code", next)
	t.Cleanup(func() { _ = reg.Close(context.Background()) })
	turnID, err := reg.Start(context.Background(), h.info.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	result, err := reg.Wait(context.Background(), turnID)
	if err != nil || result.StopReason != "end_turn" || result.Text != "again" {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	turns := next.recorded()
	if len(turns) != 1 || turns[0].ResumeID != fakeClaudeID {
		t.Fatalf("second turn = %+v", turns)
	}
	// The live process sent no new meta, so the step keeps the stored model.
	for _, event := range h.events(t) {
		if event.Kind == core.KindStepStart && event.TurnID == turnID {
			if step := payloadOf[core.StepStart](t, event); step.Model != "claude-haiku-5-5" {
				t.Fatalf("step/start = %+v", step)
			}
		}
	}
}

// AC-CCR-005: a deny rule denies without asking; an ask call asks the owner
// and allows after approval, with approval/asked and approval/decided events.
func TestExternalTurnApprovals(t *testing.T) {
	type answer struct {
		allow   bool
		message string
	}
	var answers []answer
	script := func(ctx context.Context, _ *fakeRuntime, turn core.RuntimeTurn) error {
		for _, cmd := range []string{"rm -rf x", "ls"} {
			input := json.RawMessage(`{"command":"` + cmd + `"}`)
			allow, message := turn.Approve(ctx, core.RuntimeApproval{
				CallID: "toolu_" + strings.Fields(cmd)[0], ToolName: "Bash", Input: input, Risk: core.RiskExec,
				Rule: "bash(" + cmd + ")", PermTool: "bash", PermInput: input,
			})
			answers = append(answers, answer{allow, message})
		}
		return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopEndTurn})
	}
	h, _ := newExternalHarness(t, core.AgentConfig{Permission: "ask", Rules: core.AgentRules{Deny: []string{"bash(rm *)"}}}, script)
	var asked []core.ApprovalRequest
	h.hooks.ApprovalRequest.SetDefault(func(_ context.Context, req core.ApprovalRequest) (core.ApprovalOutcome, error) {
		asked = append(asked, req)
		return core.OutcomeAllowedOnce, nil
	})
	service := permission.NewApprovalService(h.store, h.hooks, h.reg.config)
	h.hooks.ToolsPreExecute.Use((&permission.Engine{Store: h.store, Config: h.reg.config, Approval: service}).Handle)

	if result := h.run(t, "go"); result.StopReason != "end_turn" {
		t.Fatalf("result = %+v", result)
	}
	if len(answers) != 2 {
		t.Fatalf("answers = %+v", answers)
	}
	if answers[0].allow || !strings.Contains(answers[0].message, "deny rule: bash(rm *)") {
		t.Fatalf("rm answer = %+v", answers[0])
	}
	if !answers[1].allow {
		t.Fatalf("ls answer = %+v", answers[1])
	}
	if len(asked) != 1 || asked[0].ToolName != "bash" || asked[0].CallID != "toolu_ls" || asked[0].AgentName != "assistant" {
		t.Fatalf("asked = %+v", asked)
	}
	var askedEvents, decidedEvents int
	for _, event := range h.events(t) {
		switch event.Kind {
		case core.KindApprovalAsked:
			askedEvents++
			if p := payloadOf[core.ApprovalAsked](t, event); p.CallID != "toolu_ls" {
				t.Fatalf("approval/asked = %+v", p)
			}
		case core.KindApprovalDecided:
			decidedEvents++
			if p := payloadOf[core.ApprovalDecided](t, event); p.Outcome != core.OutcomeAllowedOnce {
				t.Fatalf("approval/decided = %+v", p)
			}
		}
	}
	if askedEvents != 1 || decidedEvents != 1 {
		t.Fatalf("approval events asked=%d decided=%d", askedEvents, decidedEvents)
	}
}

// API-CCR-001: trust allows unless a deny rule matches; never denies unless
// an allow rule matches; no gate at all denies (fail-closed).
func TestExternalTurnApprovalPolicies(t *testing.T) {
	approve := func(t *testing.T, cfg core.AgentConfig, engine bool, cmd string) bool {
		t.Helper()
		var allowed bool
		h, _ := newExternalHarness(t, cfg, func(ctx context.Context, _ *fakeRuntime, turn core.RuntimeTurn) error {
			input := json.RawMessage(`{"command":"` + cmd + `"}`)
			allowed, _ = turn.Approve(ctx, core.RuntimeApproval{CallID: "c1", ToolName: "Bash", Input: input, Risk: core.RiskExec, PermTool: "bash", PermInput: input})
			return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopEndTurn})
		})
		h.hooks.ApprovalRequest.SetDefault(func(context.Context, core.ApprovalRequest) (core.ApprovalOutcome, error) {
			t.Error("the owner was asked")
			return core.OutcomeRejected, nil
		})
		if engine {
			service := permission.NewApprovalService(h.store, h.hooks, h.reg.config)
			h.hooks.ToolsPreExecute.Use((&permission.Engine{Store: h.store, Config: h.reg.config, Approval: service}).Handle)
		}
		h.run(t, "go")
		return allowed
	}
	if !approve(t, core.AgentConfig{Permission: "trust"}, true, "ls") {
		t.Fatal("trust denied ls")
	}
	if approve(t, core.AgentConfig{Permission: "trust", Rules: core.AgentRules{Deny: []string{"bash(rm *)"}}}, true, "rm -rf x") {
		t.Fatal("trust allowed a denied command")
	}
	if approve(t, core.AgentConfig{Permission: "never"}, true, "ls") {
		t.Fatal("never allowed ls")
	}
	if !approve(t, core.AgentConfig{Permission: "never", Rules: core.AgentRules{Allow: []string{"bash(ls)"}}}, true, "ls") {
		t.Fatal("never denied an allowed command")
	}
	// The test pipeline's default gate denies exec calls without an engine.
	if approve(t, core.AgentConfig{Permission: "trust"}, false, "ls") {
		t.Fatal("no permission engine allowed an exec call")
	}
}

// REQ-CCR-006: Cancel interrupts the runtime and the turn ends cancelled.
func TestExternalTurnCancel(t *testing.T) {
	running := make(chan struct{})
	h, rt := newExternalHarness(t, core.AgentConfig{}, func(ctx context.Context, rt *fakeRuntime, turn core.RuntimeTurn) error {
		turn.Emit(core.RuntimeEvent{Kind: core.RuntimeTextDelta, Text: "working"})
		close(running)
		select {
		case <-rt.interrupt:
		case <-time.After(5 * time.Second):
		}
		<-ctx.Done()
		return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopCancelled})
	})
	turnID, err := h.reg.Start(context.Background(), h.info.ID, "long job")
	if err != nil {
		t.Fatal(err)
	}
	<-running
	if err := h.reg.Cancel(context.Background(), h.info.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	result, err := h.reg.Wait(ctx, turnID)
	if err != nil || result.StopReason != "cancelled" || result.Err != nil {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	rt.mu.Lock()
	interrupts := append([]string(nil), rt.interrupts...)
	rt.mu.Unlock()
	if len(interrupts) == 0 || interrupts[0] != h.info.ID {
		t.Fatalf("interrupts = %v", interrupts)
	}
	events := h.events(t)
	last := events[len(events)-1]
	if last.Kind != core.KindTurnEnd || payloadOf[core.TurnEnd](t, last).Reason != "cancelled" {
		t.Fatalf("last event = %s %s", last.Kind, last.Payload)
	}
	if !subsequence(kinds(events), []string{core.KindStepStart, core.KindStepEnd, core.KindTurnEnd}) {
		t.Fatalf("kinds = %v", kinds(events))
	}
}

// REQ-CCR-003: a runtime error ends the turn with reason error and its text,
// and the call record carries the error outcome.
func TestExternalTurnError(t *testing.T) {
	const msg = "Claude Code is not logged in. Run `claude`, then `/login`."
	h, _ := newExternalHarness(t, core.AgentConfig{}, func(_ context.Context, _ *fakeRuntime, turn core.RuntimeTurn) error {
		turn.Emit(core.RuntimeEvent{Kind: core.RuntimeUsage})
		return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopError, IsError: true, ErrorText: msg})
	})
	var usages []ExternalUsage
	h.reg.SetExternalHooks(ExternalHooks{Usage: func(u ExternalUsage) { usages = append(usages, u) }})
	result := h.run(t, "hi")
	if result.StopReason != "error" || result.Err == nil || result.Err.Message != msg || result.Err.Class != "runtime" {
		t.Fatalf("result = %+v", result)
	}
	events := h.events(t)
	end := payloadOf[core.TurnEnd](t, events[len(events)-1])
	if end.Reason != "error" || end.Error == nil || end.Error.Message != msg {
		t.Fatalf("turn/end = %+v", end)
	}
	if len(usages) != 1 || usages[0].Outcome != "runtime" || usages[0].Error != msg {
		t.Fatalf("usages = %+v", usages)
	}
}

// A runtime that is not registered fails the turn with a clear error.
func TestExternalTurnUnknownRuntime(t *testing.T) {
	h := newHarness(t, mock.Script{}, map[string]core.AgentConfig{"assistant": {Runtime: "no-such-runtime"}})
	result := h.run(t, "hi")
	if result.StopReason != "error" || result.Err == nil || !strings.Contains(result.Err.Message, `"no-such-runtime" is not available`) {
		t.Fatalf("result = %+v", result)
	}
}

// ADR 0011, ADR 0018: a lana agent delegates to a Claude Agent through task;
// the child session runs through the external branch.
func TestExternalTurnAsSubagent(t *testing.T) {
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("call_task", "task", `{"agent":"claude","prompt":"Say hi"}`),
		textResponse("parent done"),
	}}, map[string]core.AgentConfig{
		"coder":  {Provider: "mock", Model: "m", Permission: "trust", Tools: []string{"task"}, Subagents: []string{"claude"}},
		"claude": {Runtime: "claude-code", Model: "sonnet"},
	})
	rt := newFakeRuntime(func(_ context.Context, _ *fakeRuntime, turn core.RuntimeTurn) error {
		turn.Emit(core.RuntimeEvent{Kind: core.RuntimeAssistantMessage, Text: "hi from claude"})
		turn.Emit(core.RuntimeEvent{Kind: core.RuntimeUsage, Usage: core.Usage{InputTokens: 7, OutputTokens: 3}})
		return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopEndTurn})
	})
	h.reg.SetRuntime("claude-code", rt)
	parent := newSubagentParent(t, h, "coder")

	result := h.run(t, "delegate")
	if result.StopReason != "end_turn" || result.Text != "parent done" {
		t.Fatalf("result = %+v", result)
	}
	payload := toolResultPayload(t, h, "call_task")
	var out taskResult
	if err := json.Unmarshal([]byte(payload.Output), &out); err != nil {
		t.Fatalf("task output %q: %v", payload.Output, err)
	}
	if out.Agent != "claude" || out.Status != "end_turn" || out.Answer != "hi from claude" || out.Steps != 1 {
		t.Fatalf("task result = %+v", out)
	}
	children := subagentChildren(t, h, parent.ID)
	turns := rt.recorded()
	if len(children) != 1 || len(turns) != 1 || turns[0].SessionID != children[0].ID || turns[0].Config.Model != "sonnet" {
		t.Fatalf("children=%+v turns=%+v", children, turns)
	}
}

// A runtime session meta is not a prompt head: a native turn after an
// agent switch writes its own system prompt.
func TestRuntimeMetaIsNotPromptHead(t *testing.T) {
	h, _ := newExternalHarness(t, core.AgentConfig{}, bashScript)
	h.run(t, "first")
	head, err := (&Runner{reg: h.reg, sessionID: h.info.ID}).promptHead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if head.exists {
		t.Fatalf("head = %+v", head)
	}
}

