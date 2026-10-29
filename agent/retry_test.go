package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

type failingProvider struct {
	failures int32
	class    string
	calls    atomic.Int32
}

func (p *failingProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	n := p.calls.Add(1)
	if n <= p.failures {
		return nil, &core.ProviderError{Class: p.class, Message: "gagal"}
	}
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: "ok"}, nil)
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

func retryHarness(t *testing.T, providers map[string]core.Provider, targets []core.LLMTarget) (*core.Hooks, *memstore.Store, string) {
	t.Helper()
	store := memstore.New()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	hooks := core.NewHooks()
	hooks.LLMStream.SetDefault(func(ctx context.Context, in core.LLMStreamInput) (core.ChatStream, error) {
		provider := providers[in.Targets[0].Provider]
		if provider == nil {
			return nil, &core.ProviderError{Class: "client", Message: "provider tidak ada"}
		}
		return provider.Stream(ctx, in.Request)
	})
	retry := NewRetry(store)
	retry.Delays = []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 15 * time.Millisecond}
	retry.Install(hooks)
	return hooks, store, info.ID
}

func callStream(t *testing.T, hooks *core.Hooks, sessionID string, targets []core.LLMTarget) error {
	t.Helper()
	stream, err := hooks.LLMStream.Call(context.Background(), core.LLMStreamInput{SessionID: sessionID, TurnID: "t", Step: 1, Targets: targets})
	if err != nil {
		return err
	}
	for _, err := range stream {
		if err != nil {
			return err
		}
	}
	return nil
}

func TestRetryBackoff(t *testing.T) {
	provider := &failingProvider{failures: 2, class: "rate_limit"}
	hooks, store, sessionID := retryHarness(t, map[string]core.Provider{"p": provider}, nil)
	start := time.Now()
	if err := callStream(t, hooks, sessionID, []core.LLMTarget{{Provider: "p", Model: "m"}}); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 3 {
		t.Fatalf("calls=%d", provider.calls.Load())
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("backoff tidak terjadi: %v", elapsed)
	}
	var attempts int
	events, _ := store.ChainEvents(context.Background(), sessionID)
	for _, event := range events {
		if event.Kind == core.KindAssistantAttempt {
			attempts++
		}
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d", attempts)
	}
}

func TestFallbackOrder(t *testing.T) {
	primary := &failingProvider{failures: 100, class: "server"}
	fallback := &failingProvider{failures: 0}
	hooks, _, sessionID := retryHarness(t, map[string]core.Provider{"primary": primary, "fallback": fallback}, nil)
	if err := callStream(t, hooks, sessionID, []core.LLMTarget{{Provider: "primary", Model: "m"}, {Provider: "fallback", Model: "m"}}); err != nil {
		t.Fatal(err)
	}
	if primary.calls.Load() != 4 {
		t.Fatalf("primary calls=%d", primary.calls.Load())
	}
	if fallback.calls.Load() != 1 {
		t.Fatalf("fallback calls=%d", fallback.calls.Load())
	}
}

func TestNoRetryClasses(t *testing.T) {
	for _, class := range []string{"auth", "client", "context_length"} {
		primary := &failingProvider{failures: 10, class: class}
		fallback := &failingProvider{failures: 0}
		hooks, _, sessionID := retryHarness(t, map[string]core.Provider{"primary": primary, "fallback": fallback}, nil)
		err := callStream(t, hooks, sessionID, []core.LLMTarget{{Provider: "primary", Model: "m"}, {Provider: "fallback", Model: "m"}})
		var pe *core.ProviderError
		if !errors.As(err, &pe) || pe.Class != class {
			t.Fatalf("class %s err=%v", class, err)
		}
		if primary.calls.Load() != 1 || fallback.calls.Load() != 0 {
			t.Fatalf("class %s primary=%d fallback=%d", class, primary.calls.Load(), fallback.calls.Load())
		}
	}
}
