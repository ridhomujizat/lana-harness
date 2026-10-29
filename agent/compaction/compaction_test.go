package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

type fakeLLM struct{ provider core.Provider }

func (f fakeLLM) Provider(string) (core.Provider, bool) { return f.provider, true }
func (f fakeLLM) Names() []string                       { return []string{"mock"} }

type summaryProvider struct {
	failures int32
	calls    atomic.Int32
	text     string
}

func (p *summaryProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	n := p.calls.Add(1)
	if n <= p.failures {
		return nil, core.ErrContextTooLong
	}
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: p.text}, nil)
		yield(core.ChatEvent{Kind: core.ChatUsage, Usage: core.Usage{InputTokens: 100, OutputTokens: 10}}, nil)
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

func setup(t *testing.T, provider *summaryProvider) (*memstore.Store, core.SessionInfo, core.Config, *Service) {
	t.Helper()
	store := memstore.New()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "coder", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	cfg := core.Config{Agents: map[string]core.AgentConfig{"coder": {Provider: "mock", Model: "m"}}}
	service := NewService(store, fakeLLM{provider: provider}, cfg)
	return store, info, cfg, service
}

func seed(t *testing.T, store *memstore.Store, id string) {
	t.Helper()
	if _, err := store.Append(context.Background(), id, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hello"}}}},
		{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hi"}}}},
		{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCompactNowSummary(t *testing.T) {
	provider := &summaryProvider{text: "summary"}
	store, info, _, service := setup(t, provider)
	seed(t, store, info.ID)
	result, err := service.CompactNow(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.UptoSeq != 5 || result.TokensAfter == 0 {
		t.Fatalf("result=%+v", result)
	}
	events, _ := store.ChainEvents(context.Background(), info.ID)
	var payload core.CompactionSummary
	found := false
	for _, event := range events {
		if event.Kind == core.KindCompactionSummary {
			found = true
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found || payload.Summary != "summary" || payload.UptoSeq != result.UptoSeq {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestCompactNowRejects(t *testing.T) {
	provider := &summaryProvider{text: "x"}
	store, info, _, service := setup(t, provider)
	if _, err := service.CompactNow(context.Background(), info.ID); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err=%v", err)
	}
	seed(t, store, info.ID)
	if _, err := store.Append(context.Background(), info.ID, []core.EventDraft{{Kind: core.KindTurnStart, TurnID: "t2", Payload: core.TurnStart{V: 1, TurnID: "t2"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompactNow(context.Background(), info.ID); !errors.Is(err, ErrTurnActive) {
		t.Fatalf("err=%v", err)
	}
}

func TestAutoCompactThreshold(t *testing.T) {
	provider := &summaryProvider{text: "summary"}
	store, info, cfg, service := setup(t, provider)
	cfg.Agents["coder"] = core.AgentConfig{Provider: "mock", Model: "m", ContextWindow: 10000}
	service.Config = cfg
	seed(t, store, info.ID)
	if _, err := store.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t2", Payload: core.TurnStart{V: 1, TurnID: "t2"}},
		{Kind: core.KindStepStart, TurnID: "t2", Payload: core.StepStart{V: 1, Step: 1, Provider: "mock", Model: "m"}},
		{Kind: core.KindStepEnd, TurnID: "t2", Payload: core.StepEnd{V: 1, Step: 1, Provider: "mock", Model: "m", Usage: core.Usage{InputTokens: 7001}}},
	}); err != nil {
		t.Fatal(err)
	}
	hooks := core.NewHooks()
	hooks.AgentPreStep.SetDefault(func(_ context.Context, in core.PreStepInput) (core.PreStepDecision, error) {
		return core.PreStepDecision{Input: in.Input}, nil
	})
	service.Install(hooks)
	if _, err := hooks.AgentPreStep.Call(context.Background(), core.PreStepInput{SessionID: info.ID, AgentName: "coder", Input: core.Message{Role: core.RoleUser}}); err != nil {
		t.Fatal(err)
	}
	events, _ := store.ChainEvents(context.Background(), info.ID)
	found := false
	for _, event := range events {
		if event.Kind == core.KindCompactionSummary {
			found = true
		}
	}
	if !found {
		t.Fatal("summary not written")
	}
}

func TestDefaultContextWindow(t *testing.T) {
	provider := &summaryProvider{text: "summary"}
	store, info, cfg, service := setup(t, provider)
	seed(t, store, info.ID)
	_ = store.Append
	if _, err := store.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t2", Payload: core.TurnStart{V: 1, TurnID: "t2"}},
		{Kind: core.KindStepStart, TurnID: "t2", Payload: core.StepStart{V: 1, Step: 1, Provider: "mock", Model: "m"}},
		{Kind: core.KindStepEnd, TurnID: "t2", Payload: core.StepEnd{V: 1, Step: 1, Provider: "mock", Model: "m", Usage: core.Usage{InputTokens: 89600}}},
	}); err != nil {
		t.Fatal(err)
	}
	hooks := core.NewHooks()
	hooks.AgentPreStep.SetDefault(func(_ context.Context, in core.PreStepInput) (core.PreStepDecision, error) {
		return core.PreStepDecision{Input: in.Input}, nil
	})
	service.Install(hooks)
	if _, err := hooks.AgentPreStep.Call(context.Background(), core.PreStepInput{SessionID: info.ID, AgentName: "coder", Input: core.Message{Role: core.RoleUser}}); err != nil {
		t.Fatal(err)
	}
	events, _ := store.ChainEvents(context.Background(), info.ID)
	for _, event := range events {
		if event.Kind == core.KindCompactionSummary {
			t.Fatal("should not compact at exactly 70%")
		}
	}
	if cfg.Agents["coder"].ContextWindow != 0 {
		t.Fatal("unexpected")
	}
}

func TestContextTooLongRetryOnce(t *testing.T) {
	provider := &summaryProvider{text: "summary", failures: 1}
	store, info, _, service := setup(t, provider)
	seed(t, store, info.ID)
	hooks := core.NewHooks()
	hooks.LLMStream.SetDefault(func(ctx context.Context, in core.LLMStreamInput) (core.ChatStream, error) {
		return provider.Stream(ctx, in.Request)
	})
	service.Install(hooks)
	stream, err := hooks.LLMStream.Call(context.Background(), core.LLMStreamInput{SessionID: info.ID, TurnID: "t", Step: 1, Targets: []core.LLMTarget{{Provider: "mock", Model: "m"}}})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for event, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == core.ChatTextDelta {
			text.WriteString(event.Text)
		}
	}
	if text.String() != "summary" || provider.calls.Load() != 3 {
		t.Fatalf("text=%q calls=%d", text.String(), provider.calls.Load())
	}
}

func TestContextTooLongTwiceEndsTurn(t *testing.T) {
	provider := &summaryProvider{text: "summary", failures: 2}
	store, info, _, service := setup(t, provider)
	seed(t, store, info.ID)
	hooks := core.NewHooks()
	hooks.LLMStream.SetDefault(func(ctx context.Context, in core.LLMStreamInput) (core.ChatStream, error) {
		return provider.Stream(ctx, in.Request)
	})
	service.Install(hooks)
	_, err := hooks.LLMStream.Call(context.Background(), core.LLMStreamInput{SessionID: info.ID, TurnID: "t", Step: 1, Targets: []core.LLMTarget{{Provider: "mock", Model: "m"}}})
	if !errors.Is(err, core.ErrContextTooLong) {
		t.Fatalf("err=%v", err)
	}
}
