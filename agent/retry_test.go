package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

// The router owns retry, backoff, fallback, and cooldown (REQ-RTR-005); its
// unit tests cover the policy. These tests cover the agent side of the
// default llm.stream handler: attempts become assistant/attempt events and
// the served target is reported.

type failingProvider struct {
	failures int32
	class    string
	status   int
	calls    atomic.Int32
}

func (p *failingProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	n := p.calls.Add(1)
	if n <= p.failures {
		return nil, &core.ProviderError{Class: p.class, Status: p.status, Message: "failed"}
	}
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: "ok"}, nil)
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

// routedHooks installs the default handlers over a router of providers whose
// backoff waits are skipped.
func routedHooks(t *testing.T, providers map[string]core.Provider) (*core.Hooks, *memstore.Store, string) {
	t.Helper()
	store := memstore.New()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	rtr, err := router.New(router.Config{},
		router.WithProviders(providers),
		router.WithSleep(func(context.Context, time.Duration) error { return nil }),
		router.WithJitter(func() float64 { return 1 }),
	)
	if err != nil {
		t.Fatal(err)
	}
	hooks := core.NewHooks()
	installDefaults(hooks, rtr, store)
	return hooks, store, info.ID
}

func callStream(t *testing.T, hooks *core.Hooks, sessionID string, targets []core.LLMTarget, served *core.LLMTarget) error {
	t.Helper()
	stream, err := hooks.LLMStream.Call(context.Background(), core.LLMStreamInput{
		SessionID: sessionID, TurnID: "t", Step: 1, Targets: targets,
		OnServed: func(target core.LLMTarget) { *served = target },
	})
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

func attemptEvents(t *testing.T, store *memstore.Store, sessionID string) []core.AssistantAttempt {
	t.Helper()
	events, err := store.ChainEvents(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var out []core.AssistantAttempt
	for _, event := range events {
		if event.Kind != core.KindAssistantAttempt {
			continue
		}
		var payload core.AssistantAttempt
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, payload)
	}
	return out
}

// Retries of one target are written as numbered assistant/attempt events.
func TestRoutedRetryWritesAttempts(t *testing.T) {
	provider := &failingProvider{failures: 2, class: "rate_limit"}
	hooks, store, sessionID := routedHooks(t, map[string]core.Provider{"p": provider})
	var served core.LLMTarget
	if err := callStream(t, hooks, sessionID, []core.LLMTarget{{Provider: "p", Model: "m"}}, &served); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 3 {
		t.Fatalf("calls=%d", provider.calls.Load())
	}
	attempts := attemptEvents(t, store, sessionID)
	if len(attempts) != 2 {
		t.Fatalf("attempts=%+v", attempts)
	}
	for i, a := range attempts {
		if a.Attempt != i+1 || a.Step != 1 || a.Provider != "p" || a.Model != "m" || a.Error.Class != "rate_limit" {
			t.Fatalf("attempt %d = %+v", i, a)
		}
	}
	if served != (core.LLMTarget{Provider: "p", Model: "m"}) {
		t.Fatalf("served=%+v", served)
	}
}

// AC-RTR-007 (handler side): an auth failure moves to the next target at once
// and the fallback is reported as served.
func TestRoutedFallbackReportsServedTarget(t *testing.T) {
	primary := &failingProvider{failures: 100, class: "auth", status: 401}
	fallback := &failingProvider{}
	hooks, store, sessionID := routedHooks(t, map[string]core.Provider{"primary": primary, "fallback": fallback})
	var served core.LLMTarget
	err := callStream(t, hooks, sessionID, []core.LLMTarget{{Provider: "primary", Model: "x"}, {Provider: "fallback", Model: "y"}}, &served)
	if err != nil {
		t.Fatal(err)
	}
	if primary.calls.Load() != 1 || fallback.calls.Load() != 1 {
		t.Fatalf("primary=%d fallback=%d", primary.calls.Load(), fallback.calls.Load())
	}
	attempts := attemptEvents(t, store, sessionID)
	if len(attempts) != 1 || attempts[0].Provider != "primary" || attempts[0].Model != "x" || attempts[0].Error.Class != "auth" {
		t.Fatalf("attempts=%+v", attempts)
	}
	if served != (core.LLMTarget{Provider: "fallback", Model: "y"}) {
		t.Fatalf("served=%+v", served)
	}
}

// AC-RTR-008 (handler side): client and context_length errors stop the chain.
func TestRoutedNoFallbackClasses(t *testing.T) {
	for _, class := range []string{"client", "context_length"} {
		primary := &failingProvider{failures: 10, class: class, status: 400}
		fallback := &failingProvider{}
		hooks, _, sessionID := routedHooks(t, map[string]core.Provider{"primary": primary, "fallback": fallback})
		var served core.LLMTarget
		err := callStream(t, hooks, sessionID, []core.LLMTarget{{Provider: "primary", Model: "m"}, {Provider: "fallback", Model: "m"}}, &served)
		var pe *core.ProviderError
		if !errors.As(err, &pe) || pe.Class != class {
			t.Fatalf("class %s err=%v", class, err)
		}
		if primary.calls.Load() != 1 || fallback.calls.Load() != 0 {
			t.Fatalf("class %s primary=%d fallback=%d", class, primary.calls.Load(), fallback.calls.Load())
		}
	}
}

// A plain LLMRegistry (no router) gets one direct call to the first target.
func TestPlainRegistryCallsFirstTarget(t *testing.T) {
	primary := &failingProvider{failures: 1, class: "server"}
	store := memstore.New()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	hooks := core.NewHooks()
	installDefaults(hooks, &testLLM{providers: map[string]core.Provider{"p": primary}}, store)
	var served core.LLMTarget
	err = callStream(t, hooks, info.ID, []core.LLMTarget{{Provider: "p", Model: "m"}}, &served)
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "server" || primary.calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, primary.calls.Load())
	}
}
