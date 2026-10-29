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
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("dari asisten2")}}, map[string]core.AgentConfig{
		"asisten":  {Provider: "mock", Model: "m1"},
		"asisten2": {Provider: "mock", Model: "m2"},
	})
	if err := h.reg.SetAgent(context.Background(), h.info.ID, "asisten2"); err != nil {
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
	if payload.Agent != "asisten2" || payload.Provider != "mock" || payload.Model != "m2" {
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
		textResponse("selesai"),
	}}, map[string]core.AgentConfig{"asisten": {Provider: "mock", Model: "m1", Tools: []string{"bash"}}})
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

func TestSetModelUnknownProvider(t *testing.T) {
	h := newHarness(t, mock.Script{}, nil)
	before := len(h.events(t))
	err := h.reg.SetModel(context.Background(), h.info.ID, "tidakada:m")
	if err == nil || !strings.Contains(err.Error(), "tidakada") {
		t.Fatalf("err=%v", err)
	}
	if after := len(h.events(t)); after != before {
		t.Fatalf("event ditulis: %d -> %d", before, after)
	}
}

func TestWorkspacePrecedence(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{ToolUse: &mock.ToolUse{ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"x.txt"}`)}},
			{Stop: strptr("tool_use")},
		}},
		textResponse("selesai"),
	}}, map[string]core.AgentConfig{"asisten": {Provider: "mock", Model: "m", Tools: []string{"read_file"}, Workspace: t.TempDir()}})
	if err := os.WriteFile(filepath.Join(h.ws, "x.txt"), []byte("isi"), 0o644); err != nil {
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
