package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// thinkingProvider scripts a two-step turn: step one thinks and calls a tool,
// step two answers. It records every request.
type thinkingProvider struct {
	mu       sync.Mutex
	requests []core.ChatRequest
}

func (p *thinkingProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	step := len(p.requests)
	p.mu.Unlock()
	return func(yield func(core.ChatEvent, error) bool) {
		if step == 1 {
			if !yield(core.ChatEvent{Kind: core.ChatThinkingDelta, Text: "deep "}, nil) {
				return
			}
			if !yield(core.ChatEvent{Kind: core.ChatThinkingDelta, Text: "thought"}, nil) {
				return
			}
			if !yield(core.ChatEvent{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockThinking, Text: "deep thought"}}, nil) {
				return
			}
			if !yield(core.ChatEvent{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a.txt"}`)}}, nil) {
				return
			}
			yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "tool_use"}, nil)
			return
		}
		if !yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: "done"}, nil) {
			return
		}
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

// REQ-MDL-003, REQ-MDL-004: the stored assistant message has the thinking block
// first, the next step request carries it back, the system prompt and earlier
// messages stay byte-identical, and a thinking_delta live frame is published.
func TestTurnThinkingRoundTrip(t *testing.T) {
	h := newHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}},
	})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := &thinkingProvider{}
	h.llm.providers["mock"] = provider
	sub := h.reg.hub.Subscribe(h.info.ID)
	defer sub.Close()

	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "done" {
		t.Fatalf("result=%+v", result)
	}

	var assistant []core.AssistantMessage
	for _, event := range h.events(t) {
		if event.Kind != core.KindAssistantMessage {
			continue
		}
		var payload core.AssistantMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		assistant = append(assistant, payload)
	}
	if len(assistant) != 2 {
		t.Fatalf("assistant messages=%d", len(assistant))
	}
	first := assistant[0].Content
	if len(first) != 2 || first[0].Kind != core.BlockThinking || first[0].Text != "deep thought" || first[1].Kind != core.BlockToolUse {
		t.Fatalf("first assistant content=%+v", first)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("requests=%d", len(provider.requests))
	}
	second := provider.requests[1]
	if second.System != provider.requests[0].System {
		t.Fatalf("system prompt changed between steps")
	}
	prefix := provider.requests[0].Messages
	if len(second.Messages) < len(prefix) || !reflect.DeepEqual(second.Messages[:len(prefix)], prefix) {
		t.Fatalf("earlier messages changed between steps:\n%+v\n%+v", prefix, second.Messages)
	}
	found := false
	for _, message := range second.Messages {
		if message.Role != core.RoleAssistant {
			continue
		}
		for _, block := range message.Content {
			if block.Kind == core.BlockThinking && block.Text == "deep thought" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("thinking block not carried into the next step request: %+v", second.Messages)
	}

	frame := recvHubFrame(t, sub)
	if frame.Kind != "thinking_delta" || frame.Text != "deep " {
		t.Fatalf("first live frame=%+v", frame)
	}
}

// REQ-MDL-001: the step request carries the agent's reasoning_effort, else the
// catalog default, else "".
func TestTurnReasoningEffort(t *testing.T) {
	cases := []struct {
		name        string
		model       string
		agentEffort string
		want        string
	}{
		{"agent value wins", "deepseek-flash", "low", "low"},
		{"agent off is kept", "deepseek-flash", "off", "off"},
		{"catalog default", "deepseek-flash", "", "high"},
		{"unknown model has no default", "custom-model", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, map[string]core.AgentConfig{
				"assistant": {Provider: "mock", Model: tc.model, ReasoningEffort: tc.agentEffort},
			})
			recording := &recordingProvider{}
			h.llm.providers["mock"] = recording
			if result := h.run(t, "hi"); result.StopReason != "end_turn" {
				t.Fatalf("result=%+v", result)
			}
			if recording.last.ReasoningEffort != tc.want {
				t.Fatalf("reasoning_effort=%q want %q", recording.last.ReasoningEffort, tc.want)
			}
		})
	}
}
