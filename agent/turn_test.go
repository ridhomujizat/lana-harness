package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/tools"
)

// AC-ASK-002: ask_user is offered only when the agent's tools list contains it
// and disabled_tools removes it from the offered list.
func TestAskUserOfferedOnlyWhenEnabled(t *testing.T) {
	cases := []struct {
		name     string
		config   core.AgentConfig
		expected bool
	}{
		{
			name:     "listed in tools",
			config:   core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file", "ask_user"}},
			expected: true,
		},
		{
			name:     "absent from tools",
			config:   core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}},
			expected: false,
		},
		{
			name:     "disabled_tools removes it",
			config:   core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file", "ask_user"}, DisabledTools: []string{"ask_user"}},
			expected: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}},
				map[string]core.AgentConfig{"assistant": tc.config})
			if err := h.tools.Register(tools.NewAskUser()); err != nil {
				t.Fatal(err)
			}
			recording := &recordingProvider{}
			h.llm.providers["mock"] = recording
			if result := h.run(t, "hi"); result.StopReason != "end_turn" {
				t.Fatalf("result=%+v", result)
			}
			names := make([]string, 0, len(recording.last.Tools))
			for _, spec := range recording.last.Tools {
				names = append(names, spec.Name)
			}
			if got := slices.Contains(names, "ask_user"); got != tc.expected {
				t.Fatalf("offered tools=%v, ask_user expected %v", names, tc.expected)
			}
			if !slices.Contains(names, "read_file") {
				t.Fatalf("offered tools=%v, read_file missing", names)
			}
		})
	}
}

// A call to a tool that is not in the agent's list keeps the runner error and
// writes no question event.
func TestAskUserNotEnabled(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "ask_user", `{"questions":[{"question":"Pick","options":[{"label":"a"},{"label":"b"}]}]}`),
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	if err := h.tools.Register(tools.NewAskUser()); err != nil {
		t.Fatal(err)
	}
	h.run(t, "hi")
	found := false
	for _, event := range h.events(t) {
		if event.Kind == core.KindQuestionAsked {
			t.Fatal("question/asked written for a disabled tool")
		}
		if event.Kind != core.KindToolResult {
			continue
		}
		var payload core.ToolResultPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.CallID == "c1" && payload.Status == core.ToolStatusError && strings.Contains(payload.Output, "tool is not enabled") {
			found = true
		}
	}
	if !found {
		t.Fatal("tool/result not-enabled error not found")
	}
}
