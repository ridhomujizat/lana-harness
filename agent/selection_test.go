package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

func TestSetAgentEvent(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("from assistant2")}}, map[string]core.AgentConfig{
		"assistant":  {Provider: "mock", Model: "m1"},
		"assistant2": {Provider: "mock", Model: "m2"},
	})
	if err := h.reg.SetAgent(context.Background(), h.info.ID, "assistant2"); err != nil {
		t.Fatal(err)
	}
	var payload core.ModelSelection
	for _, event := range h.events(t) {
		if event.Kind == core.KindModelSelection {
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
		}
	}
	if payload.Agent != "assistant2" || payload.Provider != "" || payload.Model != "" {
		t.Fatalf("payload=%+v", payload)
	}
	h.run(t, "hi")
	var model string
	for _, event := range h.events(t) {
		if event.Kind == core.KindStepStart {
			var step core.StepStart
			_ = json.Unmarshal(event.Payload, &step)
			model = step.Model
		}
	}
	if model != "m2" {
		t.Fatalf("model=%q", model)
	}
}

func TestSetModelMidTurn(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{ToolUse: &mock.ToolUse{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"sleep 0.2"}`)}},
			{Stop: strptr("tool_use")},
		}},
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m1", Tools: []string{"bash"}}})
	h.hooks.ToolsPreExecute.Use(func(ctx context.Context, in core.ToolsPreExecuteInput, next core.Next[core.ToolsPreExecuteInput, core.ToolDecision]) (core.ToolDecision, error) {
		return core.ToolDecision{Allowed: true}, nil
	})
	turnID, err := h.reg.Start(context.Background(), h.info.ID, "hi")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return hasEvent(h, core.KindToolCall) })
	if err := h.reg.SetModel(context.Background(), h.info.ID, "mock:m2"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reg.Wait(context.Background(), turnID); err != nil {
		t.Fatal(err)
	}
	var models []string
	for _, event := range h.events(t) {
		if event.Kind == core.KindStepStart {
			var step core.StepStart
			_ = json.Unmarshal(event.Payload, &step)
			models = append(models, step.Model)
		}
	}
	if len(models) != 2 || models[0] != "m1" || models[1] != "m2" {
		t.Fatalf("models=%v", models)
	}
}

// REQ-MDL-001: SetReasoningEffort writes the session effort; a model change
// keeps it, and an empty value clears it back to the model default.
func TestSetReasoningEffortEvent(t *testing.T) {
	h := newHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m1"},
	})
	ctx := context.Background()
	if err := h.reg.SetModel(ctx, h.info.ID, "mock:m2"); err != nil {
		t.Fatal(err)
	}
	if err := h.reg.SetReasoningEffort(ctx, h.info.ID, "low"); err != nil {
		t.Fatal(err)
	}
	selection := lastSelection(t, h)
	if selection.Provider != "mock" || selection.Model != "m2" || selection.ReasoningEffort != "low" {
		t.Fatalf("selection=%+v", selection)
	}

	// A model change keeps the session effort.
	if err := h.reg.SetModel(ctx, h.info.ID, "mock:m3"); err != nil {
		t.Fatal(err)
	}
	selection = lastSelection(t, h)
	if selection.Provider != "mock" || selection.Model != "m3" || selection.ReasoningEffort != "low" {
		t.Fatalf("after model change selection=%+v", selection)
	}

	// "Model default" stores an empty effort and keeps the model.
	if err := h.reg.SetReasoningEffort(ctx, h.info.ID, ""); err != nil {
		t.Fatal(err)
	}
	selection = lastSelection(t, h)
	if selection.Provider != "mock" || selection.Model != "m3" || selection.ReasoningEffort != "" {
		t.Fatalf("after clear selection=%+v", selection)
	}
}

// lastSelection returns the last model/selection payload of the harness session.
func lastSelection(t *testing.T, h *harness) core.ModelSelection {
	t.Helper()
	var selection core.ModelSelection
	for _, event := range h.events(t) {
		if event.Kind != core.KindModelSelection {
			continue
		}
		var payload core.ModelSelection
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		selection = payload
	}
	return selection
}

func TestSetModelUnknownProvider(t *testing.T) {
	h := newHarness(t, mock.Script{}, nil)
	before := len(h.events(t))
	err := h.reg.SetModel(context.Background(), h.info.ID, "nosuch:m")
	if err == nil || !strings.Contains(err.Error(), "nosuch") {
		t.Fatalf("err=%v", err)
	}
	if after := len(h.events(t)); after != before {
		t.Fatalf("event written: %d -> %d", before, after)
	}
}

func TestWorkspacePrecedence(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{ToolUse: &mock.ToolUse{ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"x.txt"}`)}},
			{Stop: strptr("tool_use")},
		}},
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}, Workspace: t.TempDir()}})
	if err := os.WriteFile(filepath.Join(h.ws, "x.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.run(t, "hi")
	var result core.ToolResultPayload
	for _, event := range h.events(t) {
		if event.Kind == core.KindToolResult {
			_ = json.Unmarshal(event.Payload, &result)
		}
	}
	if result.Status != core.ToolStatusSuccess {
		t.Fatalf("tool result=%+v", result)
	}
}
