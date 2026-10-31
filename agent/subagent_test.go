package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/tools"
	"github.com/ridhomujizat/lana-harness/tools/permission"
)

// newSubagentHarness registers the task tools and allows exec tools.
func newSubagentHarness(t *testing.T, script mock.Script, agents map[string]core.AgentConfig) *harness {
	t.Helper()
	h := newHarness(t, script, agents)
	for _, tool := range []core.Tool{tools.NewTask(), tools.NewTaskContinue()} {
		if err := h.tools.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	h.hooks.ToolsPreExecute.SetDefault(func(context.Context, core.ToolsPreExecuteInput) (core.ToolDecision, error) {
		return core.ToolDecision{Allowed: true}, nil
	})
	return h
}

// newSubagentParent replaces the harness session with a session of agent.
func newSubagentParent(t *testing.T, h *harness, agentName string) core.SessionInfo {
	t.Helper()
	info, err := h.store.Create(context.Background(), core.NewSession{
		Agent: agentName, Channel: "library", Address: "chat-1", Workspace: h.ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.info = info
	return info
}

func subagentChildren(t *testing.T, h *harness, parentID string) []core.SessionInfo {
	t.Helper()
	all, err := h.store.List(context.Background(), core.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var children []core.SessionInfo
	for _, info := range all {
		if info.ParentSessionID == parentID {
			children = append(children, info)
		}
	}
	return children
}

func TestSubagentPlainAgentDelegation(t *testing.T) {
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("call_task", "task", `{"agent":"explorer","prompt":"Find the notes","title":"Notes"}`),
		textResponse("child answer"),
		textResponse("parent done"),
	}}, map[string]core.AgentConfig{
		"coder":    {Provider: "mock", Model: "m", Tools: []string{"task", "read_file"}, Subagents: []string{"explorer"}},
		"explorer": {Provider: "mock", Model: "child-model"},
	})
	parent := newSubagentParent(t, h, "coder")
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "parent done" || result.Err != nil {
		t.Fatalf("result=%+v", result)
	}
	payload := toolResultPayload(t, h, "call_task")
	if payload.Status != core.ToolStatusSuccess {
		t.Fatalf("tool result=%+v", payload)
	}
	children := subagentChildren(t, h, parent.ID)
	if len(children) != 1 || children[0].Agent != "explorer" {
		t.Fatalf("children=%+v", children)
	}
	var output taskResult
	if err := json.Unmarshal([]byte(payload.Output), &output); err != nil {
		t.Fatal(err)
	}
	if output.Agent != "explorer" || output.Answer != "child answer" || output.SessionID != children[0].ID {
		t.Fatalf("output=%+v", output)
	}
}

func TestSubagentNoDelegates(t *testing.T) {
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("call_task", "task", `{"agent":"explorer","prompt":"Find the notes"}`),
		textResponse("turn continues"),
	}}, map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "m", Tools: []string{"task"}},
	})
	parent := newSubagentParent(t, h, "coder")
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "turn continues" {
		t.Fatalf("result=%+v", result)
	}
	payload := toolResultPayload(t, h, "call_task")
	if payload.Status != core.ToolStatusError || payload.Output != noSubagentsMessage {
		t.Fatalf("tool result=%+v", payload)
	}
	if children := subagentChildren(t, h, parent.ID); len(children) != 0 {
		t.Fatalf("children=%+v", children)
	}
}

func TestSubagentChildSessionFields(t *testing.T) {
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{textResponse("child answer")}},
		map[string]core.AgentConfig{
			"coder":    {Provider: "mock", Model: "m", Tools: []string{"task"}, Subagents: []string{"explorer"}},
			"explorer": {Provider: "mock", Model: "child-model"},
		})
	parent := newSubagentParent(t, h, "coder")
	runner := &subagentRunner{reg: h.reg, allowed: []string{"explorer"}}
	out, err := tools.NewTask().Call(context.Background(), core.ToolCtx{
		SessionID:     parent.ID,
		CallID:        "call_42",
		AgentName:     "coder",
		Workspace:     h.ws,
		Subagents:     runner,
		AllowedAgents: []string{"explorer"},
	}, json.RawMessage(`{"agent":"explorer","prompt":"List the notes","title":"Notes"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("output=%+v", out)
	}
	children := subagentChildren(t, h, parent.ID)
	if len(children) != 1 {
		t.Fatalf("children=%+v", children)
	}
	child := children[0]
	if child.Agent != "explorer" || child.Channel != parent.Channel || child.Address != parent.Address || child.Workspace != parent.Workspace {
		t.Fatalf("child=%+v parent=%+v", child, parent)
	}
	events, err := h.store.ChainEvents(context.Background(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 4 || events[0].Kind != core.KindSessionCreated {
		t.Fatalf("events=%v", kinds(events))
	}
	var created core.SessionCreated
	if err := json.Unmarshal(events[0].Payload, &created); err != nil {
		t.Fatal(err)
	}
	if created.Agent != "explorer" || created.Channel != parent.Channel || created.Address != parent.Address || created.Workspace != parent.Workspace {
		t.Fatalf("session/created=%+v", created)
	}
	if created.ParentSessionID != parent.ID || created.ParentCallID != "call_42" || created.InheritedSeq != 0 {
		t.Fatalf("parent fields=%+v", created)
	}
	systemIndex, userIndex := -1, -1
	for i, event := range events {
		switch event.Kind {
		case core.KindSystemMessage:
			systemIndex = i
		case core.KindUserMessage:
			userIndex = i
			var message core.UserMessage
			if err := json.Unmarshal(event.Payload, &message); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(userText(message), "List the notes") {
				t.Fatalf("user message=%+v", message)
			}
		}
	}
	if systemIndex == -1 || userIndex == -1 || systemIndex > userIndex {
		t.Fatalf("system=%d user=%d events=%v", systemIndex, userIndex, kinds(events))
	}
	var output taskResult
	if err := json.Unmarshal([]byte(out.Output), &output); err != nil {
		t.Fatal(err)
	}
	if output.SessionID != child.ID || output.Answer != "child answer" {
		t.Fatalf("output=%+v", output)
	}
}

func TestSubagentResultJSON(t *testing.T) {
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("call_task", "task", `{"agent":"explorer","prompt":"List the notes"}`),
		textResponse("Three findings: a, b, c"),
		textResponse("done"),
	}}, map[string]core.AgentConfig{
		"coder":    {Provider: "mock", Model: "m", Tools: []string{"task"}, Subagents: []string{"explorer"}},
		"explorer": {Provider: "mock", Model: "child-model"},
	})
	newSubagentParent(t, h, "coder")
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "done" {
		t.Fatalf("result=%+v", result)
	}
	payload := toolResultPayload(t, h, "call_task")
	if payload.Status != core.ToolStatusSuccess {
		t.Fatalf("tool result=%+v", payload)
	}
	var output taskResult
	if err := json.Unmarshal([]byte(payload.Output), &output); err != nil {
		t.Fatal(err)
	}
	if output.Agent != "explorer" || output.Status != "end_turn" || output.Error != "" {
		t.Fatalf("output=%+v", output)
	}
	if output.Answer != "Three findings: a, b, c" {
		t.Fatalf("answer=%q", output.Answer)
	}
	if output.Steps != 1 || output.ToolCalls != 0 {
		t.Fatalf("steps=%d tool_calls=%d", output.Steps, output.ToolCalls)
	}
	if output.DurationMS < 0 {
		t.Fatalf("duration_ms=%d", output.DurationMS)
	}
}

func TestSubagentNotDelegated(t *testing.T) {
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("call_task", "task", `{"agent":"writer","prompt":"Write it"}`),
		textResponse("turn continues"),
	}}, map[string]core.AgentConfig{
		"coder":    {Provider: "mock", Model: "m", Tools: []string{"task"}, Subagents: []string{"explorer"}},
		"explorer": {Provider: "mock", Model: "child-model"},
		"writer":   {Provider: "mock", Model: "child-model"},
	})
	parent := newSubagentParent(t, h, "coder")
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "turn continues" {
		t.Fatalf("result=%+v", result)
	}
	payload := toolResultPayload(t, h, "call_task")
	if payload.Status != core.ToolStatusError {
		t.Fatalf("tool result=%+v", payload)
	}
	for _, want := range []string{"agent 'writer' is not a subagent of this agent", "(allowed: explorer)"} {
		if !strings.Contains(payload.Output, want) {
			t.Fatalf("output=%q lacks %q", payload.Output, want)
		}
	}
	if children := subagentChildren(t, h, parent.ID); len(children) != 0 {
		t.Fatalf("children=%+v", children)
	}
}

func intptr(v int) *int { return &v }

func decodeTaskOutput(t *testing.T, out core.ToolOutput) taskResult {
	t.Helper()
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("output=%+v", out)
	}
	var result taskResult
	if err := json.Unmarshal([]byte(out.Output), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSubagentContinueKeepsHistory(t *testing.T) {
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		textResponse("first answer"),
		textResponse("second answer"),
	}}, map[string]core.AgentConfig{
		"coder":    {Provider: "mock", Model: "m", Tools: []string{"task", "task_continue"}, Subagents: []string{"explorer"}},
		"explorer": {Provider: "mock", Model: "child-model"},
	})
	parent := newSubagentParent(t, h, "coder")
	runner := &subagentRunner{reg: h.reg, allowed: []string{"explorer"}}
	first, err := runner.Spawn(context.Background(), core.SpawnRequest{
		ParentSessionID: parent.ID, ParentCallID: "call_1", Agent: "explorer", Prompt: "List the notes",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstResult := decodeTaskOutput(t, first)
	if firstResult.Answer != "first answer" {
		t.Fatalf("first=%+v", firstResult)
	}

	input := fmt.Sprintf(`{"session_id":%q,"prompt":"Also check X"}`, firstResult.SessionID)
	out, err := tools.NewTaskContinue().Call(context.Background(), core.ToolCtx{
		SessionID: parent.ID, Subagents: runner, AllowedAgents: []string{"explorer"},
	}, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	secondResult := decodeTaskOutput(t, out)
	if secondResult.SessionID != firstResult.SessionID || secondResult.Answer != "second answer" {
		t.Fatalf("second=%+v", secondResult)
	}

	events, err := h.store.ChainEvents(context.Background(), firstResult.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	turns, users := 0, 0
	hasFollowUp := false
	for _, event := range events {
		switch event.Kind {
		case core.KindTurnStart:
			turns++
		case core.KindUserMessage:
			users++
			var message core.UserMessage
			if err := json.Unmarshal(event.Payload, &message); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(userText(message), "Also check X") {
				hasFollowUp = true
			}
		}
	}
	if turns != 2 || users != 2 || !hasFollowUp {
		t.Fatalf("turns=%d users=%d follow_up=%v events=%v", turns, users, hasFollowUp, kinds(events))
	}
}

func TestSubagentContinueNotChild(t *testing.T) {
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{textResponse("unused")}},
		map[string]core.AgentConfig{
			"coder":    {Provider: "mock", Model: "m", Tools: []string{"task", "task_continue"}, Subagents: []string{"explorer"}},
			"explorer": {Provider: "mock", Model: "child-model"},
		})
	parent := newSubagentParent(t, h, "coder")
	other := newSubagentParent(t, h, "coder")
	child, err := h.store.Create(context.Background(), core.NewSession{
		Agent: "explorer", Channel: other.Channel, Address: other.Address, Workspace: other.Workspace, ParentSessionID: other.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := h.store.ChainEvents(context.Background(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner := &subagentRunner{reg: h.reg, allowed: []string{"explorer"}}
	out, err := runner.Continue(context.Background(), core.ContinueRequest{
		ParentSessionID: parent.ID, SessionID: child.ID, Prompt: "Also check X",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("session %s is not a subagent of this session", child.ID)
	if out.Status != core.ToolStatusError || out.Output != want {
		t.Fatalf("output=%+v want %q", out, want)
	}
	after, err := h.store.ChainEvents(context.Background(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("events before=%d after=%d", len(before), len(after))
	}
}

func TestSubagentContinueBusy(t *testing.T) {
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{DelayMS: intptr(10000)}}},
	}}, map[string]core.AgentConfig{
		"coder":    {Provider: "mock", Model: "m", Tools: []string{"task", "task_continue"}, Subagents: []string{"explorer"}},
		"explorer": {Provider: "mock", Model: "child-model"},
	})
	parent := newSubagentParent(t, h, "coder")
	child, err := h.store.Create(context.Background(), core.NewSession{
		Agent: "explorer", Channel: parent.Channel, Address: parent.Address, Workspace: parent.Workspace, ParentSessionID: parent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	turnID, err := h.reg.Start(context.Background(), child.ID, "long run")
	if err != nil {
		t.Fatal(err)
	}
	runner := &subagentRunner{reg: h.reg, allowed: []string{"explorer"}}
	out, err := runner.Continue(context.Background(), core.ContinueRequest{
		ParentSessionID: parent.ID, SessionID: child.ID, Prompt: "Also check X",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusError || out.Output != busySubagentMessage {
		t.Fatalf("output=%+v", out)
	}
	if err := h.reg.Cancel(context.Background(), child.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reg.Wait(context.Background(), turnID); err != nil {
		t.Fatal(err)
	}
	events, err := h.store.ChainEvents(context.Background(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	turns, followUps := 0, 0
	for _, event := range events {
		switch event.Kind {
		case core.KindTurnStart:
			turns++
		case core.KindUserMessage:
			var message core.UserMessage
			if err := json.Unmarshal(event.Payload, &message); err != nil {
				t.Fatal(err)
			}
			if len(message.Content) > 0 && message.Content[0].Text == "Also check X" {
				followUps++
			}
		}
	}
	if turns != 1 || followUps != 0 {
		t.Fatalf("turns=%d follow_ups=%d events=%v", turns, followUps, kinds(events))
	}
}

func TestSubagentDepthChain(t *testing.T) {
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		textResponse("child answer"),
		textResponse("grandchild answer"),
	}}, map[string]core.AgentConfig{
		"coder":    {Provider: "mock", Model: "m", Tools: []string{"task"}, Subagents: []string{"explorer"}},
		"explorer": {Provider: "mock", Model: "child-model"},
	})
	root := newSubagentParent(t, h, "coder")
	runner := &subagentRunner{reg: h.reg, allowed: []string{"explorer"}}
	childOut, err := runner.Spawn(context.Background(), core.SpawnRequest{
		ParentSessionID: root.ID, ParentCallID: "call_child", Agent: "explorer", Prompt: "child task",
	})
	if err != nil {
		t.Fatal(err)
	}
	childResult := decodeTaskOutput(t, childOut)
	child, err := h.store.Info(context.Background(), childResult.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	grandchildOut, err := runner.Spawn(context.Background(), core.SpawnRequest{
		ParentSessionID: child.ID, ParentCallID: "call_grandchild", Agent: "explorer", Prompt: "grandchild task",
	})
	if err != nil {
		t.Fatal(err)
	}
	grandchildResult := decodeTaskOutput(t, grandchildOut)
	if grandchildResult.Answer != "grandchild answer" {
		t.Fatalf("grandchild=%+v", grandchildResult)
	}
	grandchild, err := h.store.Info(context.Background(), grandchildResult.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	refused, err := runner.Spawn(context.Background(), core.SpawnRequest{
		ParentSessionID: grandchild.ID, ParentCallID: "call_deep", Agent: "explorer", Prompt: "too deep",
	})
	if err != nil {
		t.Fatal(err)
	}
	if refused.Status != core.ToolStatusError || refused.Output != depthLimitMessage {
		t.Fatalf("output=%+v", refused)
	}
	if children := subagentChildren(t, h, grandchild.ID); len(children) != 0 {
		t.Fatalf("children=%+v", children)
	}
}

func TestSubagentActiveChildLimit(t *testing.T) {
	delayed := func(text string) mock.Response {
		return mock.Response{Events: []mock.Event{
			{DelayMS: intptr(300)}, {Text: &text}, {Stop: strptr("end_turn")},
		}}
	}
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		delayed("one"), delayed("two"), delayed("three"), textResponse("later"),
	}}, map[string]core.AgentConfig{
		"coder":    {Provider: "mock", Model: "m", Tools: []string{"task"}, Subagents: []string{"explorer"}},
		"explorer": {Provider: "mock", Model: "child-model"},
	})
	parent := newSubagentParent(t, h, "coder")
	runner := &subagentRunner{reg: h.reg, allowed: []string{"explorer"}}
	tool := tools.NewTask()
	type callResult struct {
		out core.ToolOutput
		err error
	}
	results := make(chan callResult, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := tool.Call(context.Background(), core.ToolCtx{
				SessionID: parent.ID, Subagents: runner, AllowedAgents: []string{"explorer"},
			}, json.RawMessage(`{"agent":"explorer","prompt":"work"}`))
			results <- callResult{out: out, err: err}
		}()
	}
	wg.Wait()
	close(results)
	successes, limited := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		switch result.out.Status {
		case core.ToolStatusSuccess:
			successes++
		case core.ToolStatusError:
			if result.out.Output != "3 subagents are already running" {
				t.Fatalf("output=%q", result.out.Output)
			}
			limited++
		default:
			t.Fatalf("output=%+v", result.out)
		}
	}
	if successes != 3 || limited != 1 {
		t.Fatalf("successes=%d limited=%d", successes, limited)
	}
	if children := subagentChildren(t, h, parent.ID); len(children) != 3 {
		t.Fatalf("children=%+v", children)
	}
	h.reg.mu.Lock()
	active := h.reg.activeChildren[parent.ID]
	h.reg.mu.Unlock()
	if active != 0 {
		t.Fatalf("active children=%d", active)
	}
	out, err := runner.Spawn(context.Background(), core.SpawnRequest{
		ParentSessionID: parent.ID, ParentCallID: "call_later", Agent: "explorer", Prompt: "later",
	})
	if err != nil {
		t.Fatal(err)
	}
	decodeTaskOutput(t, out)
	if children := subagentChildren(t, h, parent.ID); len(children) != 4 {
		t.Fatalf("children after release=%+v", children)
	}
}

// REQ-LTC-040: a child with a configured cap stops at cap+1 steps (the cap plus
// one report step); a child with no cap runs until the model stops.
func TestSubagentMaxStepsLimit(t *testing.T) {
	tests := []struct {
		name     string
		maxSteps int
		tools    int
		want     int
		wantStop string
	}{
		{"capped", 5, 5, 6, "max_steps"},
		{"unset", 0, 20, 21, "end_turn"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			responses := make([]mock.Response, 0, test.tools+1)
			for i := 0; i < test.tools; i++ {
				// Each call reads a distinct line, so its result digest differs
				// and the loop guard's read-only detectors stay quiet; the test
				// is about the step cap, not about a looping model.
				responses = append(responses, toolResponse(fmt.Sprintf("call_%d", i), "read_file", fmt.Sprintf(`{"path":"a.txt","offset":%d,"limit":1}`, i+1)))
			}
			responses = append(responses, textResponse("done"))
			h := newSubagentHarness(t, mock.Script{Responses: responses}, map[string]core.AgentConfig{
				"coder":  {Provider: "mock", Model: "m", Tools: []string{"task"}, Subagents: []string{"worker"}},
				"worker": {Provider: "mock", Model: "child-model", Tools: []string{"read_file"}, MaxSteps: test.maxSteps},
			})
			var content strings.Builder
			for i := 1; i <= test.tools; i++ {
				fmt.Fprintf(&content, "line %d\n", i)
			}
			if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte(content.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			parent := newSubagentParent(t, h, "coder")
			out, err := (&subagentRunner{reg: h.reg, allowed: []string{"worker"}}).Spawn(context.Background(), core.SpawnRequest{
				ParentSessionID: parent.ID, ParentCallID: "call_worker", Agent: "worker", Prompt: "keep working",
			})
			if err != nil {
				t.Fatal(err)
			}
			result := decodeTaskOutput(t, out)
			if result.Status != test.wantStop || result.Steps != test.want {
				t.Fatalf("output=%+v want %s with %d steps", result, test.wantStop, test.want)
			}
		})
	}
}

// countingTool records how many times the model invoked it.
type countingTool struct {
	calls atomic.Int32
}

func (c *countingTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        "count_call",
		Description: "Counts the calls made by a child agent.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}
}
func (c *countingTool) Risk(json.RawMessage) core.Risk { return core.RiskWrite }
func (c *countingTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	c.calls.Add(1)
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "counted"}, nil
}

// TestSubagentApprovalAllowedOnce covers AC-TEAM-026: an answered child
// approval lets the child's call run once, and allowed_once is recorded in
// the child's log only.
func TestSubagentApprovalAllowedOnce(t *testing.T) {
	tool := &countingTool{}
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("call_task", "task", `{"agent":"researcher","prompt":"Count the call"}`),
		toolResponse("call_count", "count_call", `{}`),
		textResponse("child answer"),
		textResponse("parent done"),
	}}, map[string]core.AgentConfig{
		"coder":      {Provider: "mock", Model: "m", Permission: "trust", Tools: []string{"task"}, Subagents: []string{"researcher"}},
		"researcher": {Provider: "mock", Model: "child-model", Permission: "ask", Tools: []string{"count_call"}},
	})
	if err := h.tools.Register(tool); err != nil {
		t.Fatal(err)
	}
	parent := newSubagentParent(t, h, "coder")

	h.hooks.ApprovalRequest.SetDefault(func(context.Context, core.ApprovalRequest) (core.ApprovalOutcome, error) {
		return core.OutcomeAllowedOnce, nil
	})
	service := permission.NewApprovalService(h.store, h.hooks, h.reg.config)
	h.hooks.ToolsPreExecute.Use((&permission.Engine{Store: h.store, Config: h.reg.config, Approval: service}).Handle)

	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "parent done" || result.Err != nil {
		t.Fatalf("result=%+v", result)
	}
	if got := tool.calls.Load(); got != 1 {
		t.Fatalf("child tool calls=%d want 1", got)
	}
	payload := toolResultPayload(t, h, "call_task")
	if payload.Status != core.ToolStatusSuccess {
		t.Fatalf("task result=%+v", payload)
	}

	children := subagentChildren(t, h, parent.ID)
	if len(children) != 1 || children[0].Agent != "researcher" {
		t.Fatalf("children=%+v", children)
	}
	child := children[0]
	events, err := h.store.ChainEvents(context.Background(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	var asked []core.ApprovalAsked
	var decided []core.ApprovalDecided
	countResults := 0
	for _, event := range events {
		switch event.Kind {
		case core.KindApprovalAsked:
			var payload core.ApprovalAsked
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			asked = append(asked, payload)
		case core.KindApprovalDecided:
			var payload core.ApprovalDecided
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			decided = append(decided, payload)
		case core.KindToolResult:
			var payload core.ToolResultPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.CallID == "call_count" {
				countResults++
				if payload.Status != core.ToolStatusSuccess {
					t.Fatalf("child tool result=%+v", payload)
				}
			}
		}
	}
	if len(asked) != 1 || asked[0].Label != "researcher" {
		t.Fatalf("child approvals=%+v want label %q", asked, "researcher")
	}
	if len(decided) != 1 || decided[0].Outcome != core.OutcomeAllowedOnce || decided[0].RequestID != asked[0].RequestID {
		t.Fatalf("child decisions=%+v asked=%+v", decided, asked)
	}
	if countResults != 1 {
		t.Fatalf("child count_call results=%d want 1", countResults)
	}

	// The answer is recorded in the child's log only.
	parentEvents, err := h.store.ChainEvents(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range parentEvents {
		if event.Kind == core.KindApprovalAsked || event.Kind == core.KindApprovalDecided {
			t.Fatalf("parent log carries a child approval: %s", event.Kind)
		}
	}
}

// startedChildren returns the children of parentID that have written turn/start.
func startedChildren(t *testing.T, h *harness, parentID string) []core.SessionInfo {
	t.Helper()
	var started []core.SessionInfo
	for _, child := range subagentChildren(t, h, parentID) {
		events, err := h.store.ChainEvents(context.Background(), child.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Kind == core.KindTurnStart {
				started = append(started, child)
				break
			}
		}
	}
	return started
}

// turnEndReason returns the reason of the last turn/end of the session.
func turnEndReason(t *testing.T, h *harness, sessionID string) string {
	t.Helper()
	events, err := h.store.ChainEvents(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	reason := ""
	for _, event := range events {
		if event.Kind != core.KindTurnEnd {
			continue
		}
		var payload core.TurnEnd
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		reason = payload.Reason
	}
	return reason
}

func twoTaskStep() mock.Response {
	return mock.Response{Events: []mock.Event{
		{ToolUse: &mock.ToolUse{ID: "call_a", Name: "task", Input: json.RawMessage(`{"agent":"explorer","prompt":"first"}`)}},
		{ToolUse: &mock.ToolUse{ID: "call_b", Name: "task", Input: json.RawMessage(`{"agent":"explorer","prompt":"second"}`)}},
		{Stop: strptr("tool_use")},
	}}
}

// TestSubagentParentCancelCascade covers AC-TEAM-027: cancelling the parent
// turn cancels every running child, and no child goroutine is left behind.
func TestSubagentParentCancelCascade(t *testing.T) {
	delay := 30000
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		twoTaskStep(),
		{Events: []mock.Event{{DelayMS: &delay}}},
		{Events: []mock.Event{{DelayMS: &delay}}},
	}}, map[string]core.AgentConfig{
		"coder":    {Provider: "mock", Model: "m", Tools: []string{"task"}, Subagents: []string{"explorer"}},
		"explorer": {Provider: "mock", Model: "child-model"},
	})
	parent := newSubagentParent(t, h, "coder")
	before := runtime.NumGoroutine()
	turnID, err := h.reg.Start(context.Background(), parent.ID, "hi")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(startedChildren(t, h, parent.ID)) == 2 })
	if err := h.reg.Cancel(context.Background(), parent.ID); err != nil {
		t.Fatal(err)
	}
	result, err := h.reg.Wait(context.Background(), turnID)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "cancelled" {
		t.Fatalf("parent result=%+v", result)
	}
	children := subagentChildren(t, h, parent.ID)
	if len(children) != 2 {
		t.Fatalf("children=%+v", children)
	}
	waitFor(t, func() bool {
		for _, child := range children {
			if turnEndReason(t, h, child.ID) == "" {
				return false
			}
		}
		return true
	})
	for _, child := range children {
		if reason := turnEndReason(t, h, child.ID); reason != "cancelled" {
			t.Fatalf("child %s turn/end=%q want cancelled", child.ID, reason)
		}
	}
	for _, callID := range []string{"call_a", "call_b"} {
		payload := toolResultPayload(t, h, callID)
		if payload.Status != core.ToolStatusError || payload.Output != "cancelled" {
			t.Fatalf("%s result=%+v", callID, payload)
		}
	}
	if err := h.reg.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("leaked goroutines: before=%d after=%d", before, runtime.NumGoroutine())
		}
		time.Sleep(time.Millisecond)
	}
}

// TestSubagentChildCancelOnly covers AC-TEAM-028: cancelling one child ends
// that child only, and the parent turn continues to its next step.
func TestSubagentChildCancelOnly(t *testing.T) {
	delay := 800
	answer := "child answer"
	childResponse := mock.Response{Events: []mock.Event{
		{DelayMS: &delay}, {Text: &answer}, {Stop: strptr("end_turn")},
	}}
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		twoTaskStep(),
		childResponse,
		childResponse,
		textResponse("parent done"),
	}}, map[string]core.AgentConfig{
		"coder":    {Provider: "mock", Model: "m", Tools: []string{"task"}, Subagents: []string{"explorer"}},
		"explorer": {Provider: "mock", Model: "child-model"},
	})
	parent := newSubagentParent(t, h, "coder")
	turnID, err := h.reg.Start(context.Background(), parent.ID, "hi")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(startedChildren(t, h, parent.ID)) == 2 })
	children := subagentChildren(t, h, parent.ID)
	if len(children) != 2 {
		t.Fatalf("children=%+v", children)
	}
	cancelledID, continuedID := children[0].ID, children[1].ID
	if err := h.reg.Cancel(context.Background(), cancelledID); err != nil {
		t.Fatal(err)
	}
	result, err := h.reg.Wait(context.Background(), turnID)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "end_turn" || result.Text != "parent done" {
		t.Fatalf("parent result=%+v", result)
	}
	waitFor(t, func() bool { return turnEndReason(t, h, cancelledID) != "" })
	if reason := turnEndReason(t, h, cancelledID); reason != "cancelled" {
		t.Fatalf("cancelled child turn/end=%q want cancelled", reason)
	}
	if reason := turnEndReason(t, h, continuedID); reason != "end_turn" {
		t.Fatalf("continued child turn/end=%q want end_turn", reason)
	}
	cancelledCalls, successCalls := 0, 0
	for _, callID := range []string{"call_a", "call_b"} {
		payload := toolResultPayload(t, h, callID)
		switch payload.Status {
		case core.ToolStatusError:
			if payload.Output != "cancelled" {
				t.Fatalf("%s result=%+v", callID, payload)
			}
			cancelledCalls++
		case core.ToolStatusSuccess:
			successCalls++
		default:
			t.Fatalf("%s result=%+v", callID, payload)
		}
	}
	if cancelledCalls != 1 || successCalls != 1 {
		t.Fatalf("cancelled calls=%d successful calls=%d", cancelledCalls, successCalls)
	}
}

// TestSubagentSpawnLinksChildToParent covers that Spawn links the child session
// so its live frames reach the parent's subscribers.
func TestSubagentSpawnLinksChildToParent(t *testing.T) {
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{textResponse("child answer")}},
		map[string]core.AgentConfig{
			"coder":    {Provider: "mock", Model: "m", Tools: []string{"task"}, Subagents: []string{"explorer"}},
			"explorer": {Provider: "mock", Model: "child-model"},
		})
	parent := newSubagentParent(t, h, "coder")
	sub, err := h.reg.SubscribeLive(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	runner := &subagentRunner{reg: h.reg, allowed: []string{"explorer"}}
	out, err := runner.Spawn(context.Background(), core.SpawnRequest{
		ParentSessionID: parent.ID, ParentCallID: "call_link", Agent: "explorer", Prompt: "child task",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := decodeTaskOutput(t, out)
	frame := recvHubFrame(t, sub)
	if frame.SessionID != result.SessionID || frame.Kind != "text_delta" || frame.Text != "child answer" {
		t.Fatalf("frame=%+v want child %s", frame, result.SessionID)
	}
}
