package agent

import (
	"context"
	"errors"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Retry is the llm.stream listener for retries and fallback.
type Retry struct {
	Store  core.SessionStore
	Delays []time.Duration
}

// NewRetry creates a retry listener.
func NewRetry(store core.SessionStore) *Retry {
	return &Retry{Store: store, Delays: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}}
}

// Install installs the retry listener.
func (r *Retry) Install(hooks *core.Hooks) {
	hooks.LLMStream.Use(r.handle)
}

func (r *Retry) handle(ctx context.Context, in core.LLMStreamInput, next core.Next[core.LLMStreamInput, core.ChatStream]) (core.ChatStream, error) {
	if len(in.Targets) == 0 {
		return nil, &core.ProviderError{Class: "client", Message: "provider target is empty"}
	}
	var lastErr error
	for _, target := range in.Targets {
		attempt := 0
		for {
			attempt++
			one := in
			one.Targets = []core.LLMTarget{target}
			stream, err := next(ctx, one)
			if err == nil {
				var first core.ChatEvent
				var rest *streamPump
				first, rest, err = pumpStream(ctx, stream)
				if err == nil {
					return replayStream(first, rest), nil
				}
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			pe := asProviderError(err)
			r.writeAttempt(ctx, in, target, attempt, pe)
			if !retryable(pe.Class) {
				return nil, lastErr
			}
			if attempt > 3 {
				break
			}
			delay := r.Delays[min(len(r.Delays), attempt)-1]
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	if lastErr == nil {
		lastErr = &core.ProviderError{Class: "client", Message: "no target available"}
	}
	return nil, lastErr
}

func retryable(class string) bool {
	return class == "rate_limit" || class == "server" || class == "network"
}

func asProviderError(err error) *core.ProviderError {
	var pe *core.ProviderError
	if errors.As(err, &pe) {
		return pe
	}
	return &core.ProviderError{Class: "network", Message: err.Error()}
}

func (r *Retry) writeAttempt(ctx context.Context, in core.LLMStreamInput, target core.LLMTarget, attempt int, pe *core.ProviderError) {
	if r.Store == nil {
		return
	}
	events := &Events{Store: r.Store, SessionID: in.SessionID}
	payload := core.AssistantAttempt{
		V: core.PayloadVersion, Step: in.Step, Attempt: attempt,
		Provider: target.Provider, Model: target.Model,
		Error: core.PayloadError{Class: pe.Class, Status: pe.Status, Message: pe.Message},
	}
	_, _ = events.Append(context.WithoutCancel(ctx), core.EventDraft{Kind: core.KindAssistantAttempt, TurnID: in.TurnID, Payload: payload})
}

type streamPump struct {
	ch     chan streamResult
	cancel context.CancelFunc
}

type streamResult struct {
	event core.ChatEvent
	err   error
}

func pumpStream(ctx context.Context, stream core.ChatStream) (core.ChatEvent, *streamPump, error) {
	pumpCtx, cancel := context.WithCancel(ctx)
	pump := &streamPump{ch: make(chan streamResult, 64), cancel: cancel}
	go func() {
		defer close(pump.ch)
		stream(func(event core.ChatEvent, err error) bool {
			select {
			case pump.ch <- streamResult{event: event, err: err}:
				return true
			case <-pumpCtx.Done():
				return false
			}
		})
	}()
	select {
	case result, ok := <-pump.ch:
		if !ok {
			cancel()
			return core.ChatEvent{}, nil, &core.ProviderError{Class: "network", Message: "stream is empty"}
		}
		if result.err != nil {
			cancel()
			return core.ChatEvent{}, nil, result.err
		}
		return result.event, pump, nil
	case <-ctx.Done():
		cancel()
		return core.ChatEvent{}, nil, ctx.Err()
	}
}

func replayStream(first core.ChatEvent, pump *streamPump) core.ChatStream {
	return func(yield func(core.ChatEvent, error) bool) {
		defer pump.cancel()
		if !yield(first, nil) {
			return
		}
		for result := range pump.ch {
			if !yield(result.event, result.err) {
				return
			}
			if result.err != nil {
				return
			}
		}
	}
}
