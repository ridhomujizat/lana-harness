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
	// Spec 78: a pooled Claude Code or Antigravity target tries every
	// account of its pool (REQ-PAF-001).
	chain, groups := r.runtimePool(chain)
	active := r.cool.filterIndex(chain, r.now())
	var lastErr error
	// skipGroup is the pool whose remaining accounts are skipped after a
	// failure that is not an account's own (REQ-PAF-001); -1 for none.
	skipGroup := -1
targets:
	for i, ci := range active {
		target := chain[ci]
		if groups[ci] == skipGroup {
			continue
		}
		skipGroup = -1
		retries := retriesBeforeNext
		last := i == len(active)-1
		if last {
			retries = retriesOnLast
		}
		_, runtime := snap.runtimeKind(target.Provider)
		// REQ-PAF-005: a provider with several API keys moves to its next key
		// before the chain moves on. tried holds the keys this target used.
		entry := snap.providers[target.Provider]
		key := r.pickKey(entry, nil)
		var tried map[string]bool
		// keyStart is the attempt number of the first call with key; retries
		// and backoff count per key.
		keyStart := 1
		for n := 1; ; n++ {
			k := n - keyStart + 1
			start := r.now()
			stream, err := r.attempt(ctx, snap, requested, target, key, n, start, req, meta)
			if err == nil {
				r.cool.clear(target)
				if entry.pooled() {
					r.keyCool.clear(target.Provider, key.env)
				}
				return stream, target, nil
			}
			if ctx.Err() != nil {
				return nil, Target{}, ctx.Err()
			}
			pe := snap.redactError(r.opts.secrets, target.Provider, asProviderError(err))
			r.recordFailure(snap, requested, target, recordKey(entry, key), n, start, meta, pe)
			if onAttempt != nil {
				onAttempt(Attempt{Target: target, N: n, Err: pe})
			}
			lastErr = pe
			class := effectiveClass(pe)
			if runtime {
				if !poolMoves(class) {
					skipGroup = groups[ci]
				}
				switch {
				case runtimeInputError(pe):
					// REQ-RPR-003: the next target may take tools or images.
					continue targets
				case class == "rate_limit" || class == core.ErrorClassQuota:
					// REQ-RPR-005: a usage limit does not clear in seconds; the
					// account cools down and the chain moves on at once.
					r.coolRuntime(snap, target, pe)
					continue targets
				}
			}
			if entry.pooled() && key != nil && keySpent(class, pe, k, retries) {
				r.coolKey(snap, target.Provider, key.env, class, pe)
				if tried == nil {
					tried = map[string]bool{}
				}
				tried[key.env] = true
				if next := r.pickKey(entry, tried); next != nil {
					// The next key is tried at once, without backoff.
					key, keyStart = next, n+1
					continue
				}
				// No key is left: the target is spent.
				if class == "rate_limit" || class == "auth" || class == core.ErrorClassQuota {
					r.coolDown(snap, target, pe)
				}
				continue targets
			}
			switch class {
			case "rate_limit", "server", "network":
				if class == "rate_limit" && pe.RetryAfter > moveOnRetryAfter && !last {
					r.coolDown(snap, target, pe)
					continue targets
				}
				if k > retries {
					if class == "rate_limit" {
						r.coolDown(snap, target, pe)
					}
					continue targets
				}
				if err := r.sleep(ctx, r.backoff(k, pe)); err != nil {
					return nil, Target{}, err
				}
			case "auth", core.ErrorClassQuota, "missing_secret", core.ErrorClassNotFound:
				// spec 75: quota moves the chain like auth.
				if class == "auth" || class == core.ErrorClassQuota {
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

// keySpent reports whether a failed attempt spends the API key it used, so a
// provider with several keys moves to its next one (REQ-PAF-005): auth and
// quota at once, rate_limit after the usual retries of the key or when the
// provider asks for a long wait.
func keySpent(class string, pe *core.ProviderError, k, retries int) bool {
	switch class {
	case "auth", core.ErrorClassQuota:
		return true
	case "rate_limit":
		return k > retries || pe.RetryAfter > moveOnRetryAfter
	}
	return false
}

// pickKey returns the key a call on entry uses (REQ-PAF-005): the first key
// not in tried that is not cooling down and has a value. With tried nil (the
// first call on a target), when every key is cooling down or empty, it
// returns the first key with a value, else the first key, so the call fails
// as before key pools; with tried set it returns nil when no key is left.
// A provider with one key always gets that key.
func (r *Router) pickKey(entry *providerEntry, tried map[string]bool) *keyClient {
	if entry == nil || len(entry.keys) == 0 {
		return nil
	}
	if len(entry.keys) == 1 {
		if tried[entry.keys[0].env] {
			return nil
		}
		return &entry.keys[0]
	}
	now := r.now()
	var present *keyClient
	for i := range entry.keys {
		key := &entry.keys[i]
		if tried[key.env] || !r.keyPresent(entry, key.env) {
			continue
		}
		if _, cooling := r.keyCool.cooling(entry.name, key.env, now); !cooling {
			return key
		}
		if present == nil {
			present = key
		}
	}
	if tried != nil {
		return nil
	}
	if present != nil {
		return present
	}
	return &entry.keys[0]
}

// keyPresent reports whether the secret env of a provider key has a value.
// Custom providers, keys without an env, and routers without a secret source
// count as present.
func (r *Router) keyPresent(entry *providerEntry, env string) bool {
	if entry.custom || env == "" || r.opts.secrets == nil {
		return true
	}
	secret, ok := r.opts.secrets.Lookup(env)
	return ok && secret.Reveal() != ""
}

// coolKey cools one API key of a provider down (REQ-PAF-005): a rate-limited
// key for max(router cooldown, Retry-After), or a minute when both are zero;
// a refused key for 5 minutes; a key out of quota for an hour.
func (r *Router) coolKey(snap *snapshot, provider, env, class string, pe *core.ProviderError) {
	var d time.Duration
	switch class {
	case "rate_limit":
		d = max(snap.cooldown, pe.RetryAfter)
		if d <= 0 {
			d = keyCooldownRateLimit
		}
	case "auth":
		d = keyCooldownAuth
	case core.ErrorClassQuota:
		d = keyCooldownQuota
	default:
		return
	}
	r.keyCool.set(provider, env, r.now().Add(d))
}

// recordKey is the key name a call record carries: the secret name of the
// key used, only when the provider has more than one key (REQ-PAF-005).
func recordKey(entry *providerEntry, key *keyClient) string {
	if !entry.pooled() || key == nil {
		return ""
	}
	return key.env
}

// KeyCooling returns the API keys of provider that are cooling down, by
// secret name, and until when (REQ-PAF-006).
func (r *Router) KeyCooling(provider string) map[string]time.Time {
	out := map[string]time.Time{}
	entry, ok := r.snap.Load().providers[provider]
	if !ok {
		return out
	}
	now := r.now()
	for _, key := range entry.keys {
		if key.env == "" {
			continue
		}
		if until, cooling := r.keyCool.cooling(provider, key.env, now); cooling {
			out[key.env] = until
		}
	}
	return out
}

// attempt makes one call to one target with one of its keys and waits for
// its first event. key nil uses the provider's first adapter.
func (r *Router) attempt(ctx context.Context, snap *snapshot, requested string, target Target, key *keyClient, n int, start time.Time, req core.ChatRequest, meta CallMeta) (core.ChatStream, error) {
	if kind, ok := snap.runtimeKind(target.Provider); ok {
		// Spec 78, ADR 0023: a Claude Code or Antigravity target runs its
		// account's CLI through the runtime streamer.
		return r.attemptRuntime(ctx, snap, requested, target, kind, n, start, req, meta)
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
	adapter := entry.adapter
	env := EffectiveKeyEnv(entry.cfg, entry.name)
	if key != nil {
		adapter, env = key.adapter, key.env
	}
	if pe := r.missingKey(entry, env); pe != nil {
		return nil, pe
	}
	one := req
	one.Model = target.Model
	stream, err := adapter.Stream(ctx, one)
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
	base.Key = recordKey(entry, key)
	base.TTFTMs = r.now().Sub(start).Milliseconds()
	return r.recordedStream(snap, first, pump, base, start), nil
}

// missingKey returns a missing_secret error when a configured provider has an
// API key env whose value is empty, so the chain moves on without calling
// the provider (spec 37 failure table). Providers added with WithProviders
// and routers without a secret source are not checked.
func (r *Router) missingKey(entry *providerEntry, env string) *core.ProviderError {
	if entry.custom || r.opts.secrets == nil || env == "" {
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
// not_found, and an HTTP error that means an exhausted balance, credit, or
// quota (IsQuotaError) to quota, whatever class the adapter gave it.
func effectiveClass(pe *core.ProviderError) string {
	if pe.Class == "client" && pe.Status == 404 {
		return core.ErrorClassNotFound
	}
	switch pe.Class {
	case "client", "auth", "rate_limit":
		if IsQuotaError(pe.Status, pe.Message) {
			return core.ErrorClassQuota
		}
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
