package agent

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// stubTool is a minimal core.Tool used to register a name after a turn.
type stubTool struct{ name string }

func (s stubTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: s.name, InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (s stubTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }
func (s stubTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "ok"}, nil
}

// REQ-TB-004, REQ-TB-008: an agent whose tools list names a tool nothing has
// registered still runs a turn; the missing tool is not offered, and a later
// registration makes it available on the next turn without a restart.
func TestTurnSkipsUnregisteredAgentTool(t *testing.T) {
	h := newHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file", "ghost_tool"}},
	})
	rec := &recordingProvider{}
	h.llm.providers["mock"] = rec

	if result := h.run(t, "first"); result.StopReason != "end_turn" || result.Err != nil {
		t.Fatalf("first turn result=%+v", result)
	}
	names := toolSpecNames(rec.last.Tools)
	if slices.Contains(names, "ghost_tool") {
		t.Fatalf("ghost_tool offered while unregistered: %v", names)
	}
	if !slices.Contains(names, "read_file") {
		t.Fatalf("read_file missing from %v", names)
	}

	if err := h.tools.Register(stubTool{name: "ghost_tool"}); err != nil {
		t.Fatal(err)
	}
	if result := h.run(t, "second"); result.StopReason != "end_turn" || result.Err != nil {
		t.Fatalf("second turn result=%+v", result)
	}
	if names := toolSpecNames(rec.last.Tools); !slices.Contains(names, "ghost_tool") {
		t.Fatalf("ghost_tool not offered after registration: %v", names)
	}
}
