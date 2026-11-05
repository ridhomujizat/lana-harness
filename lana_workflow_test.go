package lana

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// countingTool is a fake registered tool with a call counter for the workflow
// tool-gate tests (AC-WF-016).
type countingTool struct {
	name   string
	risk   core.Risk
	output string
	called atomic.Int32
}

func (t *countingTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: t.name, InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
}
func (t *countingTool) Risk(json.RawMessage) core.Risk { return t.risk }
func (t *countingTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	t.called.Add(1)
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: t.output}, nil
}

// newWorkflowRuntime builds a runtime with a scripted mock model and the fake
// tools the tool gate tests call.
func newWorkflowRuntime(t *testing.T, script mock.Script, customTools []core.Tool, options ...Option) *Runtime {
	t.Helper()
	names := make([]string, 0, len(customTools))
	for _, tool := range customTools {
		names = append(names, strconv.Quote(tool.Spec().Name))
	}
	cfg := fmt.Sprintf(`
[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.assistant]
provider = "mock"
model = "m"
tools = [%s]
`, strings.Join(names, ", "))
	rt, err := New(context.Background(), Options{
		Home:      t.TempDir(),
		Layers:    []config.Layer{{Name: "test", TOML: []byte(cfg)}},
		Tools:     customTools,
		Providers: map[string]core.Provider{"mock": mock.New(script)},
	}, options...)
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// toolCallScript scripts a tool call per response, ending with a text reply.
func toolCallScript(calls ...string) mock.Script {
	text, stop := "done", "end_turn"
	responses := make([]mock.Response, 0, len(calls)+1)
	for i, name := range calls {
		use := mock.ToolUse{ID: "c" + string(rune('1'+i)), Name: name, Input: json.RawMessage(`{}`)}
		responses = append(responses, mock.Response{Events: []mock.Event{{ToolUse: &use}}})
	}
	responses = append(responses, mock.Response{Events: []mock.Event{{Text: &text}, {Stop: &stop}}})
	return mock.Script{Responses: responses}
}

// toolResults returns the tool/result outputs of a session in order.
func toolResults(t *testing.T, rt *Runtime, sessionID string) []core.ToolResultPayload {
	t.Helper()
	events, err := rt.History(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var out []core.ToolResultPayload
	for _, event := range events {
		if event.Kind != core.KindToolResult {
			continue
		}
		var payload core.ToolResultPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, payload)
	}
	return out
}

// AC-WF-016: a locked tool runs without asking, an unlocked tool reaches the
// approval path, and the workflow session is live for the whole turn.
func TestWorkflowDepsLockAndApproval(t *testing.T) {
	echo := &countingTool{name: "echo_tool", risk: core.RiskWrite, output: "echo-ok"}
	shell := &countingTool{name: "shell_tool", risk: core.RiskWrite, output: "shell-ok"}
	var approvals atomic.Int32
	rt := newWorkflowRuntime(t, toolCallScript("echo_tool", "shell_tool"), []core.Tool{echo, shell},
		ApproveWith(func(context.Context, core.ApprovalRequest) core.ApprovalOutcome {
			approvals.Add(1)
			return core.OutcomeRejected
		}),
		ApproveChannels("workflow"),
	)

	result, err := rt.WorkflowDeps().RunAgent(context.Background(), workflow.AgentRequest{
		WorkflowID: "wf1", Agent: "assistant", Prompt: "go",
		Lock: []string{"echo_tool"},
	})
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if result.StopReason != "end_turn" || result.SessionID == "" {
		t.Fatalf("result=%+v", result)
	}
	if echo.called.Load() != 1 {
		t.Fatalf("locked echo_tool called %d times, want 1", echo.called.Load())
	}
	if shell.called.Load() != 0 {
		t.Fatalf("unlocked shell_tool ran %d times, want 0", shell.called.Load())
	}
	if approvals.Load() != 1 {
		t.Fatalf("approvals=%d, want 1 for the unlocked tool", approvals.Load())
	}
	if rt.IsWorkflowSession(result.SessionID) {
		t.Fatal("session is still a workflow session after RunAgent returned")
	}
}

// AC-WF-016: a worker tool list rejects a tool that is not in it, before the
// approval path.
func TestWorkflowDepsWorkerToolsGate(t *testing.T) {
	echo := &countingTool{name: "echo_tool", risk: core.RiskWrite, output: "echo-ok"}
	shell := &countingTool{name: "shell_tool", risk: core.RiskWrite, output: "shell-ok"}
	var approvals atomic.Int32
	rt := newWorkflowRuntime(t, toolCallScript("shell_tool"), []core.Tool{echo, shell},
		ApproveWith(func(context.Context, core.ApprovalRequest) core.ApprovalOutcome {
			approvals.Add(1)
			return core.OutcomeAllowedOnce
		}),
		ApproveChannels("workflow"),
	)

	result, err := rt.WorkflowDeps().RunAgent(context.Background(), workflow.AgentRequest{
		WorkflowID: "wf1", Agent: "assistant", Prompt: "go",
		Tools: []string{"echo_tool"},
	})
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if shell.called.Load() != 0 {
		t.Fatalf("shell_tool ran %d times, want 0", shell.called.Load())
	}
	if approvals.Load() != 0 {
		t.Fatalf("approvals=%d, want 0 for a tool outside the worker list", approvals.Load())
	}
	results := toolResults(t, rt, result.SessionID)
	if len(results) != 1 || results[0].Output != "not in this worker's tools" {
		t.Fatalf("tool results=%+v", results)
	}
}

// REQ-WF-009: the step budget rejects tool calls past maxSteps.
func TestWorkflowDepsStepBudget(t *testing.T) {
	echo := &countingTool{name: "echo_tool", risk: core.RiskWrite, output: "echo-ok"}
	shell := &countingTool{name: "shell_tool", risk: core.RiskWrite, output: "shell-ok"}
	rt := newWorkflowRuntime(t, toolCallScript("echo_tool", "echo_tool"), []core.Tool{echo, shell})

	result, err := rt.WorkflowDeps().RunAgent(context.Background(), workflow.AgentRequest{
		WorkflowID: "wf1", Agent: "assistant", Prompt: "go",
		Lock: []string{"echo_tool"}, MaxSteps: 1,
	})
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if echo.called.Load() != 1 {
		t.Fatalf("echo_tool called %d times, want 1", echo.called.Load())
	}
	results := toolResults(t, rt, result.SessionID)
	if len(results) != 2 {
		t.Fatalf("tool results=%+v", results)
	}
	if results[0].Status != core.ToolStatusSuccess || results[1].Output != "step budget used" {
		t.Fatalf("tool results=%+v", results)
	}
}

// REQ-WF-005: Bash returns the exit code and output, and reports a timeout.
func TestWorkflowDepsBash(t *testing.T) {
	rt := newWorkflowRuntime(t, mock.Script{}, nil)
	deps := rt.WorkflowDeps()
	ctx := context.Background()

	ok, err := deps.Bash(ctx, workflow.BashRequest{WorkflowID: "wf1", Command: "echo hi"})
	if err != nil {
		t.Fatalf("Bash echo: %v", err)
	}
	if ok.ExitCode != 0 || !strings.Contains(ok.Output, "hi") {
		t.Fatalf("Bash echo=%+v", ok)
	}

	failed, err := deps.Bash(ctx, workflow.BashRequest{WorkflowID: "wf1", Command: "exit 3"})
	if err != nil {
		t.Fatalf("Bash exit 3: %v", err)
	}
	if failed.ExitCode != 3 {
		t.Fatalf("exit code=%d, want 3", failed.ExitCode)
	}

	_, err = deps.Bash(ctx, workflow.BashRequest{WorkflowID: "wf1", Command: "sleep 3", Timeout: 100 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("timeout error=%v", err)
	}
}

// REQ-WF-005, REQ-TB-006: Browse reports a missing obscura binary before
// starting anything.
func TestWorkflowDepsBrowseMissing(t *testing.T) {
	rt := newWorkflowRuntime(t, mock.Script{}, nil)
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	_, err := rt.WorkflowDeps().Browse(context.Background(), workflow.BrowseRequest{
		WorkflowID: "wf1", URL: "https://example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "Obscura is not installed. Add it on the Tools page.") {
		t.Fatalf("Browse error=%v", err)
	}
}

// REQ-WF-005: the session is a workflow session for the whole RunAgent call and
// is unregistered afterwards.
func TestWorkflowDepsIsWorkflowSessionDuringRun(t *testing.T) {
	delay := 300
	text, stop := "hi", "end_turn"
	script := mock.Script{Responses: []mock.Response{{
		Events: []mock.Event{{DelayMS: &delay}, {Text: &text}, {Stop: &stop}},
	}}}
	rt := newWorkflowRuntime(t, script, nil)
	ctx := context.Background()

	done := make(chan workflow.AgentResult, 1)
	go func() {
		result, _ := rt.WorkflowDeps().RunAgent(ctx, workflow.AgentRequest{
			WorkflowID: "wf1", Agent: "assistant", Prompt: "go",
		})
		done <- result
	}()

	deadline := time.Now().Add(3 * time.Second)
	var sessionID string
	for {
		list, err := rt.store.List(ctx, core.SessionFilter{Channel: "workflow"})
		if err == nil && len(list) > 0 && rt.IsWorkflowSession(list[0].ID) {
			sessionID = list[0].ID
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no live workflow session appeared during RunAgent")
		}
		time.Sleep(5 * time.Millisecond)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunAgent did not return")
	}
	if rt.IsWorkflowSession(sessionID) {
		t.Fatal("session is still registered after RunAgent returned")
	}
}

// REQ-WF-005, REQ-WF-002, REQ-WF-009: CallTool, ToolRisk and AgentExists.
func TestWorkflowDepsToolHelpers(t *testing.T) {
	echo := &countingTool{name: "echo_tool", risk: core.RiskWrite, output: "echo-ok"}
	rt := newWorkflowRuntime(t, mock.Script{}, []core.Tool{echo})
	deps := rt.WorkflowDeps()
	ctx := context.Background()

	out, err := deps.CallTool(ctx, workflow.ToolRequest{WorkflowID: "wf1", Name: "echo_tool", Input: json.RawMessage(`{}`)})
	if err != nil || out != "echo-ok" {
		t.Fatalf("CallTool out=%q err=%v", out, err)
	}
	if _, err := deps.CallTool(ctx, workflow.ToolRequest{WorkflowID: "wf1", Name: "missing"}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("CallTool unknown error=%v", err)
	}

	if deps.ToolRisk("echo_tool") != workflow.RiskWrite {
		t.Fatalf("ToolRisk(echo_tool)=%q", deps.ToolRisk("echo_tool"))
	}
	if deps.ToolRisk("read_file") != workflow.RiskRead {
		t.Fatalf("ToolRisk(read_file)=%q", deps.ToolRisk("read_file"))
	}
	if deps.ToolRisk("missing") != "" {
		t.Fatalf("ToolRisk(missing)=%q", deps.ToolRisk("missing"))
	}

	if !deps.AgentExists("assistant") {
		t.Fatal("AgentExists(assistant)=false")
	}
	if deps.AgentExists("missing") {
		t.Fatal("AgentExists(missing)=true")
	}
}

// REQ-WF-003, REQ-WF-007: the runtime builds a workflow service, StartWorkflows
// is a no-op on the second call, and Close ends it without hanging.
func TestRuntimeWorkflowsLifecycle(t *testing.T) {
	rt := newWorkflowRuntime(t, mock.Script{}, nil)
	if rt.Workflows() == nil {
		t.Fatal("Workflows() is nil after New")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.StartWorkflows(ctx)
	// A second call must be a no-op and must not restart the service.
	rt.StartWorkflows(ctx)

	// Close runs in the helper's cleanup; calling it here proves it returns
	// while the scheduler and event bridge are running.
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// REQ-WF-003: SubscribeSessionEvents passes non-workflow session events to fn
// and skips workflow sessions.
func TestWorkflowSubscribeSessionEvents(t *testing.T) {
	rt := newWorkflowRuntime(t, mock.Script{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type seen struct {
		kind      string
		sessionID string
		agent     string
	}
	events := make(chan seen, 16)
	go rt.SubscribeSessionEvents(ctx, func(kind, sessionID, agent string, _ json.RawMessage) {
		events <- seen{kind: kind, sessionID: sessionID, agent: agent}
	})

	appendEvent := func(sessionID string) {
		t.Helper()
		if _, err := rt.store.Append(context.Background(), sessionID, []core.EventDraft{{
			Kind:    core.KindUserMessage,
			Payload: core.UserMessage{V: core.PayloadVersion, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hi"}}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor := func(sessionID string) seen {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			for len(events) > 0 {
				got := <-events
				if got.sessionID == sessionID {
					return got
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("no event for session %s", sessionID)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	lib, err := rt.store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	wf, err := rt.CreateChannelSession(context.Background(), "workflow", "wf1", "assistant")
	if err != nil {
		t.Fatal(err)
	}

	appendEvent(lib.ID)
	first := waitFor(lib.ID)
	if first.kind != core.KindUserMessage || first.agent != "assistant" {
		t.Fatalf("event=%+v", first)
	}

	// The workflow event sits between two library events, so receiving the
	// second library event proves the workflow one was processed and skipped.
	appendEvent(wf.ID)
	appendEvent(lib.ID)
	waitFor(lib.ID)

	for len(events) > 0 {
		if got := <-events; got.sessionID == wf.ID {
			t.Fatalf("received a workflow session event: %+v", got)
		}
	}
}
