package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// truncatingStep is one scripted model step of the max_tokens guard tests. A
// step can carry a thinking block, an answer text, a tool call, and its stop
// reason, so tests can script a step cut off by the output limit exactly like
// the incident (REQ-TFB-008 item 5).
type truncatingStep struct {
	thinking string
	text     string
	tool     *core.ContentBlock
	stop     string
}

// truncatingProvider plays one truncatingStep per Stream call and records every
// request.
type truncatingProvider struct {
	mu       sync.Mutex
	requests []core.ChatRequest
	steps    []truncatingStep
}

func (p *truncatingProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	index := len(p.requests) - 1
	p.mu.Unlock()
	if index >= len(p.steps) {
		return nil, fmt.Errorf("truncatingProvider: script exhausted")
	}
	step := p.steps[index]
	return func(yield func(core.ChatEvent, error) bool) {
		if step.thinking != "" {
			if !yield(core.ChatEvent{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockThinking, Text: step.thinking}}, nil) {
				return
			}
		}
		if step.text != "" {
			if !yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: step.text}, nil) {
				return
			}
		}
		if step.tool != nil {
			if !yield(core.ChatEvent{Kind: core.ChatBlock, Block: *step.tool}, nil) {
				return
			}
		}
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: step.stop}, nil)
	}, nil
}

// REQ-TFB-008 item 5: a step that stops on max_tokens with only a thinking
// block and no answer runs exactly one extra step carrying maxTokensNote, and
// the turn ends normally when that step answers.
func TestMaxTokensEmptyStepRunsOneExtraStep(t *testing.T) {
	h := newHarness(t, mock.Script{}, nil)
	h.llm.providers["mock"] = &truncatingProvider{steps: []truncatingStep{
		{thinking: "thinking hard", stop: "max_tokens"},
		{text: "here is the answer", stop: "end_turn"},
	}}

	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "here is the answer" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 2 {
		t.Fatalf("steps=%d want 2", steps)
	}
	if got := lastUserText(t, h); got != maxTokensNote {
		t.Fatalf("last user message=%q want %q", got, maxTokensNote)
	}
}

// REQ-TFB-008 item 5: when the extra step is also empty and truncated, the
// turn ends as max_tokens and no third step runs.
func TestMaxTokensEmptyStepEndsMaxTokens(t *testing.T) {
	h := newHarness(t, mock.Script{}, nil)
	h.llm.providers["mock"] = &truncatingProvider{steps: []truncatingStep{
		{thinking: "first", stop: "max_tokens"},
		{thinking: "second", stop: "max_tokens"},
	}}

	result := h.run(t, "hi")
	if result.StopReason != "max_tokens" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 2 {
		t.Fatalf("steps=%d want 2", steps)
	}
	if got := lastUserText(t, h); got != maxTokensNote {
		t.Fatalf("last user message=%q want %q", got, maxTokensNote)
	}
}

// REQ-TFB-008 item 5: a normal end_turn turn is unchanged.
func TestMaxTokensNormalEndUnchanged(t *testing.T) {
	h := newHarness(t, mock.Script{}, nil)
	h.llm.providers["mock"] = &truncatingProvider{steps: []truncatingStep{
		{text: "hello", stop: "end_turn"},
	}}

	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "hello" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 1 {
		t.Fatalf("steps=%d want 1", steps)
	}
}

// REQ-TFB-008 item 5: a final answer that carries text but stopped on
// max_tokens ends the turn as max_tokens.
func TestMaxTokensFinalAnswerCutOff(t *testing.T) {
	h := newHarness(t, mock.Script{}, nil)
	h.llm.providers["mock"] = &truncatingProvider{steps: []truncatingStep{
		{text: "a partial answer", stop: "max_tokens"},
	}}

	result := h.run(t, "hi")
	if result.StopReason != "max_tokens" || result.Text != "a partial answer" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 1 {
		t.Fatalf("steps=%d want 1", steps)
	}
}

// REQ-TFB-008 item 6: an agent's max_tokens is the request MaxTokens of every
// model step.
func TestTurnMaxTokensFromAgentConfig(t *testing.T) {
	h := newHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", MaxTokens: 4096},
	})
	p := &truncatingProvider{steps: []truncatingStep{{text: "hello", stop: "end_turn"}}}
	h.llm.providers["mock"] = p

	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "hello" {
		t.Fatalf("result=%+v", result)
	}
	if len(p.requests) != 1 {
		t.Fatalf("requests=%d want 1", len(p.requests))
	}
	if got := p.requests[0].MaxTokens; got != 4096 {
		t.Fatalf("request MaxTokens=%d want 4096", got)
	}
}

// REQ-TFB-008 item 6: max_tokens 0 (or absent) leaves the request at the
// provider default.
func TestTurnMaxTokensZeroLeavesDefault(t *testing.T) {
	h := newHarness(t, mock.Script{}, nil)
	p := &truncatingProvider{steps: []truncatingStep{{text: "hi", stop: "end_turn"}}}
	h.llm.providers["mock"] = p

	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "hi" {
		t.Fatalf("result=%+v", result)
	}
	if len(p.requests) != 1 {
		t.Fatalf("requests=%d want 1", len(p.requests))
	}
	if got := p.requests[0].MaxTokens; got != 0 {
		t.Fatalf("request MaxTokens=%d want 0", got)
	}
}

// REQ-TFB-008 item 6: a hook that sets max_tokens wins over the agent's value.
func TestTurnMaxTokensHookWins(t *testing.T) {
	h := newHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", MaxTokens: 1024},
	})
	h.hooks.AgentRequest.SetDefault(func(context.Context, core.RequestControl) (core.RequestControl, error) {
		return core.RequestControl{MaxTokens: 2048}, nil
	})
	p := &truncatingProvider{steps: []truncatingStep{{text: "hi", stop: "end_turn"}}}
	h.llm.providers["mock"] = p

	h.run(t, "hi")
	if len(p.requests) != 1 {
		t.Fatalf("requests=%d want 1", len(p.requests))
	}
	if got := p.requests[0].MaxTokens; got != 2048 {
		t.Fatalf("request MaxTokens=%d want 2048", got)
	}
}

// REQ-TFB-008 item 5: a mid-turn max_tokens step that a later normal step
// follows does not change the turn end reason.
func TestMaxTokensMidTurnFollowedByNormalEnd(t *testing.T) {
	h := newHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}},
	})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.llm.providers["mock"] = &truncatingProvider{steps: []truncatingStep{
		{tool: &core.ContentBlock{Kind: core.BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a.txt"}`)}, stop: "max_tokens"},
		{text: "done", stop: "end_turn"},
	}}

	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "done" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 2 {
		t.Fatalf("steps=%d want 2", steps)
	}
}
