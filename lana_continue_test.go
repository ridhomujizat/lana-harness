package lana

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
)

// continueProvider answers the first turn with a provider error, so the turn
// ends with reason "error", and every later turn with text.
type continueProvider struct {
	mu       sync.Mutex
	requests []core.ChatRequest
	turns    int
}

func (p *continueProvider) Stream(_ context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	n := p.turns
	p.turns++
	p.mu.Unlock()
	if n == 0 {
		return func(yield func(core.ChatEvent, error) bool) {
			yield(core.ChatEvent{}, &core.ProviderError{Class: "auth", Message: "boom"})
		}, nil
	}
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: "resumed"}, nil)
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

func (p *continueProvider) lastRequest() core.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return core.ChatRequest{}
	}
	return p.requests[len(p.requests)-1]
}

// textProvider always answers with text and ends the turn normally.
type textProvider struct{ text string }

func (p *textProvider) Stream(_ context.Context, _ core.ChatRequest) (core.ChatStream, error) {
	text := p.text
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: text}, nil)
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

// blockingProvider blocks in Stream until release is closed.
type blockingProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (p *blockingProvider) Stream(_ context.Context, _ core.ChatRequest) (core.ChatStream, error) {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-p.release
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: "done"}, nil)
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

func newContinueRuntime(t *testing.T, provider core.Provider) *Runtime {
	t.Helper()
	cfg := "[providers.local]\nkind = \"openai_compat\"\nbase_url = \"http://127.0.0.1:1\"\n\n[agents.assistant]\nprovider = \"local\"\nmodel = \"m\"\n\n[[routes]]\nchannel = \"library\"\nagent = \"assistant\"\n"
	rt, err := New(context.Background(), Options{
		Layers:    []config.Layer{{Name: "test", TOML: []byte(cfg)}},
		Providers: map[string]core.Provider{"local": provider},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func messageTexts(req core.ChatRequest) []string {
	out := make([]string, 0)
	for _, message := range req.Messages {
		for _, block := range message.Content {
			if block.Text != "" {
				out = append(out, block.Text)
			}
		}
	}
	return out
}

// AC-TFB-011: Continue starts a turn whose user/message payload is marked
// continue with the previous reason, and whose text reaches the model
// (API-TFB-003, API-TFB-004).
func TestContinueStartsMarkedTurn(t *testing.T) {
	provider := &continueProvider{}
	rt := newContinueRuntime(t, provider)

	first, err := rt.Run(context.Background(), Request{Prompt: "do it"})
	if err == nil {
		t.Fatal("first turn did not fail")
	}
	if first.StopReason != StopError {
		t.Fatalf("first stop reason = %q", first.StopReason)
	}

	second, err := rt.Continue(context.Background(), first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if second.StopReason != StopEndTurn || second.Text != "resumed" {
		t.Fatalf("continue result = %+v", second)
	}

	want := "Continue the previous task from where it stopped. It ended because: Stopped by an error: boom"
	events, err := rt.History(context.Background(), first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Kind != core.KindUserMessage {
			continue
		}
		var payload core.UserMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if !payload.Continue {
			continue
		}
		found = true
		if payload.ContinueReason != StopError {
			t.Fatalf("continue_reason = %q want %q", payload.ContinueReason, StopError)
		}
		if len(payload.Content) == 0 || payload.Content[0].Text != want {
			t.Fatalf("continue content = %+v want %q", payload.Content, want)
		}
	}
	if !found {
		t.Fatal("no continue user/message was written")
	}
	if texts := messageTexts(provider.lastRequest()); !containsText(texts, want) {
		t.Fatalf("model input %v lacks the continue text %q", texts, want)
	}
}

func containsText(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

// AC-TFB-012: the runtime refuses Continue while a turn runs, after a normal
// end, and while draining (API-TFB-003).
func TestContinueRefusals(t *testing.T) {
	t.Run("draining", func(t *testing.T) {
		rt := newContinueRuntime(t, &continueProvider{})
		rt.SetDraining(true)
		if err := rt.CanContinue(context.Background(), "any"); !errors.Is(err, ErrDraining) {
			t.Fatalf("err = %v want ErrDraining", err)
		}
	})

	t.Run("nothing_to_continue", func(t *testing.T) {
		rt := newContinueRuntime(t, &textProvider{text: "ok"})
		result, err := rt.Run(context.Background(), Request{Prompt: "hi"})
		if err != nil {
			t.Fatal(err)
		}
		if err := rt.CanContinue(context.Background(), result.SessionID); !errors.Is(err, ErrNothingToContinue) {
			t.Fatalf("err = %v want ErrNothingToContinue", err)
		}
	})

	t.Run("turn_running", func(t *testing.T) {
		provider := &blockingProvider{entered: make(chan struct{}, 1), release: make(chan struct{})}
		rt := newContinueRuntime(t, provider)
		info, err := rt.CreateSession(context.Background(), "assistant")
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			_, _ = rt.Run(context.Background(), Request{Prompt: "hi", SessionID: info.ID})
		}()
		<-provider.entered
		if err := rt.CanContinue(context.Background(), info.ID); !errors.Is(err, ErrTurnRunning) {
			t.Fatalf("err = %v want ErrTurnRunning", err)
		}
		close(provider.release)
	})
}

// The reason texts of a continue request (REQ-TFB-004).
func TestContinueReasonTexts(t *testing.T) {
	cases := []struct {
		end   core.TurnEnd
		steps int
		want  string
	}{
		{core.TurnEnd{Reason: StopCancelled}, 0, "Stopped by you."},
		{core.TurnEnd{Reason: "interrupted"}, 0, "lana restarted while working."},
		{core.TurnEnd{Reason: StopError, Error: &core.PayloadError{Message: "boom"}}, 0, "Stopped by an error: boom"},
		{core.TurnEnd{Reason: StopMaxSteps}, 4, "Stopped after the step limit (4 steps)."},
	}
	for _, tc := range cases {
		if got := continueReasonText(tc.end, tc.steps); got != tc.want {
			t.Errorf("reason %q = %q want %q", tc.end.Reason, got, tc.want)
		}
	}
}
