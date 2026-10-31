package compaction

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/agent"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store/memstore"
	"github.com/ridhomujizat/lana-harness/tools"
)

// bigTool answers every call with a fixed-size output.
type bigTool struct{ chars int }

func (t bigTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "big", Description: "big output", InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (t bigTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }
func (t bigTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: strings.Repeat("x", t.chars)}, nil
}

// turnProvider answers conversation steps with one tool call until limit steps,
// then a final text. Summary requests get a short summary.
type turnProvider struct {
	mu        sync.Mutex
	steps     int
	summaries int
	limit     int
}

func isSummaryRequest(req core.ChatRequest) bool {
	if len(req.Messages) == 0 {
		return false
	}
	for _, block := range req.Messages[len(req.Messages)-1].Content {
		if block.Kind == core.BlockText && strings.HasPrefix(block.Text, "You are now acting as a compaction engine") {
			return true
		}
	}
	return false
}

func (p *turnProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	if isSummaryRequest(req) {
		p.mu.Lock()
		p.summaries++
		p.mu.Unlock()
		return textStream("condensed summary"), nil
	}
	p.mu.Lock()
	p.steps++
	n := p.steps
	p.mu.Unlock()
	if n > p.limit {
		return textStream("done"), nil
	}
	call := fmt.Sprintf("c%d", n)
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockToolUse, ID: call, Name: "big", Input: json.RawMessage(`{}`)}}, nil)
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "tool_use"}, nil)
	}, nil
}

func textStream(text string) core.ChatStream {
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockText, Text: text}}, nil)
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}
}

// (a) a turn of sixty tool steps of 6000 characters each, under a 32000 window,
// finishes; compaction runs inside the turn and the derived history is a framed
// summary followed by a tail that starts at a unit boundary.
func TestTurnFinishesWithCompaction(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	ws := t.TempDir()
	home := t.TempDir()
	info, err := store.Create(ctx, core.NewSession{Agent: "assistant", Channel: "library", Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	provider := &turnProvider{limit: 60}
	llm := fakeLLM{provider: provider}
	hooks := core.NewHooks()
	toolReg := tools.NewRegistry()
	if err := toolReg.Register(bigTool{chars: 6000}); err != nil {
		t.Fatal(err)
	}
	cfg := core.Config{
		Providers: map[string]core.ProviderConfig{"mock": {Kind: "openai_compat"}},
		Agents: map[string]core.AgentConfig{
			"assistant": {Provider: "mock", Model: "m", Tools: []string{"big"}, ContextWindow: 32000, MaxSteps: 100},
		},
	}
	reg := agent.NewRegistry(store, llm, toolReg, hooks, cfg, nil, home, ws)
	tools.NewPipeline(hooks)
	service := NewService(store, llm, cfg)
	service.Install(hooks)
	reg.SetCompactor(service)

	turnID, err := reg.Start(ctx, info.ID, "go")
	if err != nil {
		t.Fatal(err)
	}
	result, err := reg.Wait(ctx, turnID)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "end_turn" || result.Err != nil {
		t.Fatalf("turn did not finish: %+v", result)
	}
	provider.mu.Lock()
	summaries := provider.summaries
	provider.mu.Unlock()
	if summaries == 0 {
		t.Fatal("no summary request was made inside the turn")
	}
	if count, _ := summaryCount(t, store, info.ID); count == 0 {
		t.Fatal("no compaction/summary event")
	}
	messages, err := store.DeriveMessages(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) < 2 || messages[0].Role != core.RoleUser || !strings.Contains(messages[0].Content[0].Text, "<compacted-summary>") {
		t.Fatalf("derived history does not start with a framed summary: %+v", messages)
	}
	if messages[1].Role != core.RoleAssistant {
		t.Fatalf("retained tail does not start at a unit boundary: %+v", messages[1])
	}
}
