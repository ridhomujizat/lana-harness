package lana

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// newDrainTestRuntime builds a runtime whose mock answers after delayMS.
func newDrainTestRuntime(t *testing.T, delayMS int) *Runtime {
	t.Helper()
	text, stop := "hello", "end_turn"
	rt, err := New(context.Background(), Options{
		Home: t.TempDir(),
		Layers: []config.Layer{{Name: "test", TOML: []byte(`
[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.assistant]
provider = "mock"
model = "m"
`)}},
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{Responses: []mock.Response{
			{Events: []mock.Event{{DelayMS: &delayMS}, {Text: &text}, {Stop: &stop}}},
		}})},
	})
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// TestDrainingRefusesNewTurns covers AC-TFB-004: Run, Stream, Send, and session
// creation refuse before starting anything while the gate is set.
func TestDrainingRefusesNewTurns(t *testing.T) {
	rt := newDrainTestRuntime(t, 0)
	ctx := context.Background()
	info, err := rt.CreateChannelSession(ctx, "web", "owner", "assistant")
	if err != nil {
		t.Fatal(err)
	}

	rt.SetDraining(true)
	if !rt.Draining() {
		t.Fatal("Draining() = false after SetDraining(true)")
	}

	if _, err := rt.Run(ctx, Request{Prompt: "hi", SessionID: info.ID}); !errors.Is(err, ErrDraining) {
		t.Fatalf("Run error = %v, want ErrDraining", err)
	}
	if err := rt.Send(ctx, info.ID, "hi"); !errors.Is(err, ErrDraining) {
		t.Fatalf("Send error = %v, want ErrDraining", err)
	}
	if _, err := rt.CreateChannelSession(ctx, "web", "owner-2", "assistant"); !errors.Is(err, ErrDraining) {
		t.Fatalf("CreateChannelSession error = %v, want ErrDraining", err)
	}
	for _, err := range rt.Stream(ctx, Request{Prompt: "hi", SessionID: info.ID}) {
		if !errors.Is(err, ErrDraining) {
			t.Fatalf("Stream error = %v, want ErrDraining", err)
		}
		break
	}
	if rt.RunningTurnCount() != 0 {
		t.Fatalf("running turns = %d, want 0", rt.RunningTurnCount())
	}

	rt.SetDraining(false)
	if rt.Draining() {
		t.Fatal("Draining() = true after SetDraining(false)")
	}
	if _, err := rt.Run(ctx, Request{Prompt: "hi", SessionID: info.ID}); err != nil {
		t.Fatalf("Run after the gate lifted: %v", err)
	}
}

// TestRunningTurnCountAndInterrupt covers the count the drain polls and the
// interrupt path that ends a turn "interrupted".
func TestRunningTurnCountAndInterrupt(t *testing.T) {
	rt := newDrainTestRuntime(t, 5000)
	ctx := context.Background()
	info, err := rt.CreateChannelSession(ctx, "web", "owner", "assistant")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan Result, 1)
	go func() {
		result, _ := rt.Run(ctx, Request{Prompt: "hi", SessionID: info.ID})
		done <- result
	}()
	deadline := time.Now().Add(3 * time.Second)
	for rt.RunningTurnCount() == 0 {
		if !time.Now().Before(deadline) {
			t.Fatal("running turn count stayed 0")
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitForTurnStep(t, rt, info.ID)
	if interrupted := rt.InterruptAll(core.ErrInterruptedStop); interrupted != 1 {
		t.Fatalf("InterruptAll = %d, want 1", interrupted)
	}
	select {
	case result := <-done:
		if result.StopReason != "interrupted" {
			t.Fatalf("stop reason = %q, want interrupted", result.StopReason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted turn did not finish")
	}
	for rt.RunningTurnCount() != 0 {
		if !time.Now().Before(deadline) {
			t.Fatal("running turn count did not return to 0")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitForTurnStep waits until the turn wrote step/start, so the interrupt cuts
// the provider stream instead of the setup phase.
func waitForTurnStep(t *testing.T, rt *Runtime, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		events, err := rt.History(context.Background(), sessionID)
		if err == nil {
			for _, event := range events {
				if event.Kind == core.KindStepStart {
					return
				}
			}
		}
		if !time.Now().Before(deadline) {
			t.Fatal("turn did not reach step/start")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
