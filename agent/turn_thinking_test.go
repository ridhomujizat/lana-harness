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

// REQ-MDL-001: the session's reasoning_effort wins over the agent and the
// catalog default; an empty session effort falls back to the agent, then the
// catalog.
func TestTurnReasoningEffortPrecedence(t *testing.T) {
	cases := []struct {
		name        string
		model       string
		agentEffort string
		selection   string
		want        string
	}{
		{"selection wins over agent", "deepseek-flash", "low", "max", "max"},
		{"selection wins over catalog", "deepseek-flash", "", "off", "off"},
		{"agent wins when selection empty", "deepseek-flash", "low", "", "low"},
		{"catalog default when both empty", "deepseek-flash", "", "", "high"},
		{"unknown model has no default", "custom-model", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, map[string]core.AgentConfig{
				"assistant": {Provider: "mock", Model: tc.model, ReasoningEffort: tc.agentEffort},
			})
			if tc.selection != "" {
				if err := h.reg.SetReasoningEffort(context.Background(), h.info.ID, tc.selection); err != nil {
					t.Fatal(err)
				}
			}
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

// signedThinkingProvider scripts a two-step turn whose first step streams
// signed thinking blocks around text and tool calls, as the Anthropic adapter
// does.
type signedThinkingProvider struct {
	mu       sync.Mutex
	requests []core.ChatRequest
}

func (p *signedThinkingProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	step := len(p.requests)
	p.mu.Unlock()
	return func(yield func(core.ChatEvent, error) bool) {
		var events []core.ChatEvent
		if step == 1 {
			events = []core.ChatEvent{
				{Kind: core.ChatThinkingDelta, Text: "plan"},
				{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockThinking, Text: "plan", Signature: "s1"}},
				{Kind: core.ChatTextDelta, Text: "Read"},
				{Kind: core.ChatTextDelta, Text: "ing"},
				{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a.txt"}`)}},
				{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockThinking, Signature: "s2"}},
				{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockToolUse, ID: "c2", Name: "read_file", Input: json.RawMessage(`{"path":"a.txt"}`)}},
				{Kind: core.ChatStop, StopReason: "tool_use"},
			}
		} else {
			events = []core.ChatEvent{{Kind: core.ChatTextDelta, Text: "done"}, {Kind: core.ChatStop, StopReason: "end_turn"}}
		}
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	}, nil
}

// Signed thinking (spec 37, Anthropic) is stored in response order with its
// signature and sent back unchanged on the next step.
func TestTurnSignedThinkingKeepsOrder(t *testing.T) {
	h := newHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}},
	})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := &signedThinkingProvider{}
	h.llm.providers["mock"] = provider
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "done" {
		t.Fatalf("result=%+v", result)
	}
	want := []core.ContentBlock{
		{Kind: core.BlockThinking, Text: "plan", Signature: "s1"},
		{Kind: core.BlockText, Text: "Reading"},
		{Kind: core.BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a.txt"}`)},
		{Kind: core.BlockThinking, Signature: "s2"},
		{Kind: core.BlockToolUse, ID: "c2", Name: "read_file", Input: json.RawMessage(`{"path":"a.txt"}`)},
	}
	var first *core.AssistantMessage
	for _, event := range h.events(t) {
		if event.Kind != core.KindAssistantMessage {
			continue
		}
		var payload core.AssistantMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		first = &payload
		break
	}
	if first == nil || !reflect.DeepEqual(first.Content, want) {
		t.Fatalf("first assistant content=%+v", first)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("requests=%d", len(provider.requests))
	}
	var sent []core.ContentBlock
	for _, message := range provider.requests[1].Messages {
		if message.Role == core.RoleAssistant {
			sent = message.Content
		}
	}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("assistant content sent back=%+v", sent)
	}
}
