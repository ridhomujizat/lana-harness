package router

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Retry policy (REQ-RTR-005), ported from agent/retry.go.
const (
	// retriesBeforeNext is the retry limit on a target when later targets
	// remain.
	retriesBeforeNext = 2
	// retriesOnLast is the retry limit on the last target.
	retriesOnLast = 5
	// moveOnRetryAfter is the Retry-After above which a rate-limited target
	// is left at once when later targets remain.
	moveOnRetryAfter = 20 * time.Second
	// maxRetryAfter caps a provider-requested wait.
	maxRetryAfter = 120 * time.Second
)

// backoffDelays are the base waits before retry 1..5, scaled by the jitter.
var backoffDelays = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second}

// Stream resolves address (a combo, an address, or "provider:model") and
// streams through its chain. The record's requested field is address.
func (r *Router) Stream(ctx context.Context, address string, req core.ChatRequest, meta CallMeta) (core.ChatStream, error) {
	snap := r.snap.Load()
	chain, err := snap.resolve(address)
	if err != nil {
		return nil, err
	}
	stream, _, err := r.streamChain(ctx, snap, address, chain, req, meta, nil)
	return stream, err
}

// StreamChain tries the targets of chain in order and returns the stream of
// the first one that produced an event, with that target (REQ-RTR-005).
// Targets cooling down are skipped unless all of them are (REQ-RTR-006).
// onAttempt, when set, gets every failed attempt before the router retries
// or moves on. The record's requested field is meta.Requested, or the first
// target's address when that is empty.
func (r *Router) StreamChain(ctx context.Context, chain []Target, req core.ChatRequest, meta CallMeta, onAttempt func(Attempt)) (core.ChatStream, Target, error) {
	requested := meta.Requested
	if requested == "" && len(chain) > 0 {
		requested = Address(chain[0])
	}
	return r.streamChain(ctx, r.snap.Load(), requested, chain, req, meta, onAttempt)
}

func (r *Router) streamChain(ctx context.Context, snap *snapshot, requested string, chain []Target, req core.ChatRequest, meta CallMeta, onAttempt func(Attempt)) (core.ChatStream, Target, error) {
	if len(chain) == 0 {
		return nil, Target{}, &core.ProviderError{Class: "client", Message: "provider target is empty"}
	}
	active := r.cool.filter(chain, r.now())
	var lastErr error
targets:
	for i, target := range active {
		retries := retriesBeforeNext
		last := i == len(active)-1
		if last {
			retries = retriesOnLast
		}
		for n := 1; ; n++ {
			start := r.now()
			stream, err := r.attempt(ctx, snap, requested, target, n, start, req, meta)
			if err == nil {
				r.cool.clear(target)
				return stream, target, nil
			}
			if ctx.Err() != nil {
				return nil, Target{}, ctx.Err()
			}
			pe := snap.redactError(r.opts.secrets, target.Provider, asProviderError(err))
			r.recordFailure(snap, requested, target, n, start, meta, pe)
			if onAttempt != nil {
				onAttempt(Attempt{Target: target, N: n, Err: pe})
			}
			lastErr = pe
			switch class := effectiveClass(pe); class {
			case "rate_limit", "server", "network":
				if class == "rate_limit" && pe.RetryAfter > moveOnRetryAfter && !last {
					r.coolDown(snap, target, pe)
					continue targets
				}
				if n > retries {
					if class == "rate_limit" {
						r.coolDown(snap, target, pe)
					}
					continue targets
				}
				if err := r.sleep(ctx, r.backoff(n, pe)); err != nil {
					return nil, Target{}, err
				}
			case "auth", "missing_secret", core.ErrorClassNotFound:
				if class == "auth" {
					r.coolDown(snap, target, pe)
				}
				continue targets
			default:
				// client, context_length, and unknown classes stop the chain.
				return nil, Target{}, pe
			}
		}
	}
	return nil, Target{}, lastErr
}

// attempt makes one call to one target and waits for its first event.
func (r *Router) attempt(ctx context.Context, snap *snapshot, requested string, target Target, n int, start time.Time, req core.ChatRequest, meta CallMeta) (core.ChatStream, error) {
	if snap.isClaude(target.Provider) {
		// ADR 0020: a Claude Code target runs in the Claude Code runtime; the
		// agent dispatches it before the router, so this is never reached.
		return nil, &core.ProviderError{Class: core.ErrorClassNotFound, Message: "claude-code targets run in the Claude Code runtime, not through the router"}
	}
	entry, ok := snap.providers[target.Provider]
	if !ok {
		return nil, &core.ProviderError{Class: core.ErrorClassNotFound, Message: fmt.Sprintf("provider %q is not configured", target.Provider)}
	}
	// A disabled provider is treated as not configured: the chain moves on
	// (API-RTR-001 revision 2).
	if entry.cfg.Disabled {
		return nil, &core.ProviderError{Class: core.ErrorClassNotFound, Message: fmt.Sprintf("provider %q is disabled", target.Provider)}
	}
	if pe := r.missingKey(entry); pe != nil {
		return nil, pe
	}
	one := req
	one.Model = target.Model
	stream, err := entry.adapter.Stream(ctx, one)
	if err != nil {
		return nil, err
	}
	// A provider that returns no stream and no error is a broken adapter;
	// treat it like a network failure so retry and fallback apply.
	if stream == nil {
		return nil, &core.ProviderError{Class: "network", Message: "provider returned no stream"}
	}
	first, pump, err := pumpStream(ctx, stream)
	if err != nil {
		return nil, err
	}
	base := r.baseRecord(requested, target, n, start, meta)
	base.TTFTMs = r.now().Sub(start).Milliseconds()
	return r.recordedStream(snap, first, pump, base, start), nil
}

// missingKey returns a missing_secret error when a configured provider has an
// API key env whose value is empty, so the chain moves on without calling
// the provider (spec 37 failure table). Providers added with WithProviders
// and routers without a secret source are not checked.
func (r *Router) missingKey(entry *providerEntry) *core.ProviderError {
	if entry.custom || r.opts.secrets == nil {
		return nil
	}
	env := EffectiveKeyEnv(entry.cfg, entry.name)
	if env == "" {
		return nil
	}
	if secret, ok := r.opts.secrets.Lookup(env); ok && secret.Reveal() != "" {
		return nil
	}
	return &core.ProviderError{
		Class:   "missing_secret",
		Message: fmt.Sprintf("env %s for provider %s is empty", env, entry.name),
	}
}

func (r *Router) backoff(n int, pe *core.ProviderError) time.Duration {
	if pe.RetryAfter > 0 {
		return min(pe.RetryAfter, maxRetryAfter)
	}
	delay := backoffDelays[min(len(backoffDelays), n)-1]
	return time.Duration(float64(delay) * r.opts.jitter())
}

func (r *Router) coolDown(snap *snapshot, target Target, pe *core.ProviderError) {
	if snap.cooldown <= 0 {
		return
	}
	d := max(snap.cooldown, pe.RetryAfter)
	r.cool.set(target, r.now().Add(d))
}

// effectiveClass maps an HTTP 404 that an adapter classed as client to
// not_found.
func effectiveClass(pe *core.ProviderError) string {
	if pe.Class == "client" && pe.Status == 404 {
		return core.ErrorClassNotFound
	}
	return pe.Class
}

func asProviderError(err error) *core.ProviderError {
	var pe *core.ProviderError
	if errors.As(err, &pe) && pe != nil {
		return pe
	}
	return &core.ProviderError{Class: "network", Message: err.Error()}
}

type streamPump struct {
	ch     chan streamResult
	cancel context.CancelFunc
}

type streamResult struct {
	event core.ChatEvent
	err   error
}

// pumpStream runs the stream in a goroutine and waits for its first event.
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
