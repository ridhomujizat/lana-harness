package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

func usagePtr(input, output int64) *core.Usage {
	return &core.Usage{InputTokens: input, OutputTokens: output}
}

// stepUsageFor returns the usage of the step/end event of one session step.
func stepUsageFor(t *testing.T, h *harness, sessionID string, step int) core.Usage {
	t.Helper()
	events, err := h.store.ChainEvents(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind != core.KindStepEnd {
			continue
		}
		var payload core.StepEnd
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Step == step {
			return payload.Usage
		}
	}
	t.Fatalf("no step/end for session %s step %d", sessionID, step)
	return core.Usage{}
}

// TestStepUsageIncludesChildUsage covers AC-TEAM-031: the parent step usage is
// the parent model usage plus the child turn usage; the child step keeps its
// own usage.
func TestStepUsageIncludesChildUsage(t *testing.T) {
	h := newSubagentHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{ToolUse: &mock.ToolUse{ID: "call_task", Name: "task", Input: json.RawMessage(`{"agent":"explorer","prompt":"Find the notes"}`)}},
			{Usage: usagePtr(100, 20)},
			{Stop: strptr("tool_use")},
		}},
		{Events: []mock.Event{
			{Text: strptr("child answer")},
			{Usage: usagePtr(50, 10)},
			{Stop: strptr("end_turn")},
		}},
		textResponse("parent done"),
	}}, map[string]core.AgentConfig{
		"coder":    {Provider: "mock", Model: "m", Tools: []string{"task"}, Subagents: []string{"explorer"}},
		"explorer": {Provider: "mock", Model: "child-model"},
	})
	parent := newSubagentParent(t, h, "coder")
	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	children := subagentChildren(t, h, parent.ID)
	if len(children) != 1 {
		t.Fatalf("children=%+v", children)
	}
	if got := stepUsageFor(t, h, parent.ID, 1); got.InputTokens != 150 || got.OutputTokens != 30 {
		t.Fatalf("parent step usage=%+v want 150/30", got)
	}
	if got := stepUsageFor(t, h, children[0].ID, 1); got.InputTokens != 50 || got.OutputTokens != 10 {
		t.Fatalf("child step usage=%+v want 50/10", got)
	}
}

// nilUsageTool is a tool result without child usage.
type nilUsageTool struct{}

func (nilUsageTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "task", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
}
func (nilUsageTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }
func (nilUsageTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "child result"}, nil
}

// TestStepUsageIgnoresNilChildUsage checks that a tool output without usage
// leaves the step usage at the parent model usage.
func TestStepUsageIgnoresNilChildUsage(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{ToolUse: &mock.ToolUse{ID: "call_task", Name: "task", Input: json.RawMessage(`{}`)}},
			{Usage: usagePtr(100, 20)},
			{Stop: strptr("tool_use")},
		}},
		textResponse("done"),
	}}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"task"}},
	})
	if err := h.tools.Register(nilUsageTool{}); err != nil {
		t.Fatal(err)
	}
	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	if got := stepUsageFor(t, h, h.info.ID, 1); got.InputTokens != 100 || got.OutputTokens != 20 {
		t.Fatalf("step usage=%+v want 100/20", got)
	}
}
