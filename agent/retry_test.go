package agent

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

type failingProvider struct {
	failures   int32
	class      string
	retryAfter time.Duration
	calls      atomic.Int32
}

func (p *failingProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	n := p.calls.Add(1)
	if n <= p.failures {
		return nil, &core.ProviderError{Class: p.class, Message: "failed", RetryAfter: p.retryAfter}
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
			return nil, &core.ProviderError{Class: "client", Message: "provider not found"}
		}
		return provider.Stream(ctx, in.Request)
	})
	retry := NewRetry(store)
	retry.Delays = []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 15 * time.Millisecond}
	retry.Jitter = func() float64 { return 1 }
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
		t.Fatalf("backoff did not happen: %v", elapsed)
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
	if primary.calls.Load() != 6 {
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

func TestNewRetryDefaultDelays(t *testing.T) {
	retry := NewRetry(nil)
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second}
	if !reflect.DeepEqual(retry.Delays, want) {
		t.Fatalf("delays=%v", retry.Delays)
	}
}

// newRecordingHarness builds a harness whose Retry records the delays it waits
// through Sleep, so tests never spend wall-clock time.
func newRecordingHarness(t *testing.T, provider core.Provider, jitter func() float64) (*core.Hooks, *Retry, *[]time.Duration, string) {
	t.Helper()
	store := memstore.New()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	hooks := core.NewHooks()
	hooks.LLMStream.SetDefault(func(ctx context.Context, in core.LLMStreamInput) (core.ChatStream, error) {
		return provider.Stream(ctx, in.Request)
	})
	slept := &[]time.Duration{}
	retry := NewRetry(store)
	if jitter != nil {
		retry.Jitter = jitter
	}
	retry.Sleep = func(ctx context.Context, d time.Duration) error {
		*slept = append(*slept, d)
		return nil
	}
	retry.Install(hooks)
	return hooks, retry, slept, info.ID
}

func TestRetryHonorsRetryAfter(t *testing.T) {
	provider := &failingProvider{failures: 1, class: "rate_limit", retryAfter: 3 * time.Second}
	hooks, _, slept, sessionID := newRecordingHarness(t, provider, func() float64 { return 1 })
	if err := callStream(t, hooks, sessionID, []core.LLMTarget{{Provider: "p", Model: "m"}}); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("calls=%d", provider.calls.Load())
	}
	if len(*slept) != 1 || (*slept)[0] != 3*time.Second {
		t.Fatalf("slept=%v", *slept)
	}
}

func TestRetryAfterCapped(t *testing.T) {
	provider := &failingProvider{failures: 1, class: "rate_limit", retryAfter: 200 * time.Second}
	hooks, _, slept, sessionID := newRecordingHarness(t, provider, func() float64 { return 1 })
	if err := callStream(t, hooks, sessionID, []core.LLMTarget{{Provider: "p", Model: "m"}}); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 1 || (*slept)[0] != 120*time.Second {
		t.Fatalf("slept=%v", *slept)
	}
}

func TestRetryJitterRange(t *testing.T) {
	provider := &failingProvider{failures: 2, class: "server"}
	hooks, retry, slept, sessionID := newRecordingHarness(t, provider, nil)
	retry.Delays = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond}
	if err := callStream(t, hooks, sessionID, []core.LLMTarget{{Provider: "p", Model: "m"}}); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 2 {
		t.Fatalf("slept=%v", *slept)
	}
	for i, d := range *slept {
		base := retry.Delays[i]
		lo := time.Duration(float64(base) * 0.8)
		hi := time.Duration(float64(base) * 1.2)
		if d < lo || d > hi {
			t.Fatalf("delay %d = %v not in [%v,%v]", i, d, lo, hi)
		}
	}
}

// nilStreamProvider returns no stream and no error, the broken adapter case.
type nilStreamProvider struct {
	calls atomic.Int32
}

func (p *nilStreamProvider) Stream(context.Context, core.ChatRequest) (core.ChatStream, error) {
	p.calls.Add(1)
	return nil, nil
}

// TestRetryNilStream covers that a provider returning (nil, nil) is treated as
// a retryable network failure instead of dereferencing a nil iterator.
func TestRetryNilStream(t *testing.T) {
	provider := &nilStreamProvider{}
	hooks, _, sessionID := retryHarness(t, map[string]core.Provider{"p": provider}, nil)
	err := callStream(t, hooks, sessionID, []core.LLMTarget{{Provider: "p", Model: "m"}})
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "network" {
		t.Fatalf("err=%v want a network ProviderError", err)
	}
	if got := provider.calls.Load(); got != maxRetries+1 {
		t.Fatalf("calls=%d want %d", got, maxRetries+1)
	}
}
