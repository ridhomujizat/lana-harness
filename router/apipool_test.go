package router

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// Spec 80: the API connections of one kind and effective base URL are one
// ordered pool. The fakekey kind looks its behavior up by base URL and key
// env, so connections that share a base URL get their own behavior through
// their own key.

// connPool returns two connections cc (key K1) and cc-2 (key K2) of kind
// fakekey with the same base URL, and the secrets they read.
func connPool(t *testing.T, cc, cc2 *fakeBehavior) (core.ProviderConfig, core.ProviderConfig, mapSecrets) {
	t.Helper()
	first := keyProvider(t, "cc", map[string]*fakeBehavior{"K1": cc, "K2": cc2}, "K1")
	second := first
	second.APIKeyEnv = "K2"
	return first, second, mapSecrets{"K1": "sk-one", "K2": "sk-two"}
}

func intPtr(n int) *int { return &n }

func boolPtr(b bool) *bool { return &b }

// AC-ACP-001: cc answers 402 (quota); a call on cc/m is answered by cc-2,
// and the records name cc, then cc-2.
func TestAPIPoolQuotaMovesToNextConnection(t *testing.T) {
	b1 := &fakeBehavior{always: pe("client", 402, "HTTP 402: Insufficient Balance")}
	b2 := &fakeBehavior{}
	cc, cc2, secrets := connPool(t, b1, b2)
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"cc": cc, "cc-2": cc2}}, WithSecrets(secrets))
	if got := h.r.APIPool("cc"); !reflect.DeepEqual(got, []string{"cc", "cc-2"}) {
		t.Fatalf("pool = %v", got)
	}
	stream, err := h.r.Stream(context.Background(), "cc/m", core.ChatRequest{}, CallMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if text, err := drain(stream); err != nil || text != "hi from cc-2" {
		t.Fatalf("text %q err %v", text, err)
	}
	if got, want := outcomes(h.rec.all()), []string{"cc::quota", "cc-2::ok"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %v, want %v", got, want)
	}
	if recs := h.rec.all(); recs[1].Requested != "cc/m" || recs[1].Model != "m" {
		t.Fatalf("record = %+v", recs[1])
	}
	if len(h.sleep.delays) != 0 {
		t.Fatalf("slept %v before the next connection", h.sleep.delays)
	}

	// cc cools down; the next call starts at cc-2.
	if _, _, err := h.r.StreamChain(context.Background(), chain("cc/m"), core.ChatRequest{}, CallMeta{}, nil); err != nil {
		t.Fatal(err)
	}
	if b1.callCount() != 1 || b2.callCount() != 2 {
		t.Fatalf("calls cc %d cc-2 %d", b1.callCount(), b2.callCount())
	}
}

// AC-ACP-002: cc-2 with priority 10 comes before cc (default 100): a call on
// cc/m never calls cc.
func TestAPIPoolPriorityOrdersConnections(t *testing.T) {
	b1, b2 := &fakeBehavior{}, &fakeBehavior{}
	cc, cc2, secrets := connPool(t, b1, b2)
	cc2.Priority = intPtr(10)
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"cc": cc, "cc-2": cc2}}, WithSecrets(secrets))
	_, target, err := h.r.StreamChain(context.Background(), chain("cc/m"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if target != (Target{Provider: "cc-2", Model: "m"}) || b1.callCount() != 0 || b2.callCount() != 1 {
		t.Fatalf("target %v calls cc %d cc-2 %d", target, b1.callCount(), b2.callCount())
	}
	if got := h.r.APIPool("cc-2"); !reflect.DeepEqual(got, []string{"cc-2", "cc"}) {
		t.Fatalf("pool = %v", got)
	}
}

// AC-ACP-003: a failure that is not the connection's own skips the rest of
// the pool. A 400 (client) stops the chain as before (AC-RTR-008) without
// calling cc-2; a 404 (not_found) and a 5xx past its retries move the chain
// to its next target, also without calling cc-2.
func TestAPIPoolOtherFailureSkipsPool(t *testing.T) {
	cases := []struct {
		name     string
		err      *core.ProviderError
		answered bool
		calls    int
	}{
		{"client", pe("client", 400, "bad request"), false, 1},
		{"not_found", pe("client", 404, "no such model"), true, 1},
		{"server", pe("server", 500, "boom"), true, retriesBeforeNext + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b1 := &fakeBehavior{always: tc.err}
			b2 := &fakeBehavior{}
			other := &fakeBehavior{}
			cc, cc2, secrets := connPool(t, b1, b2)
			h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{
				"cc": cc, "cc-2": cc2, "openai": fakeProvider(t, "openai", other),
			}}, WithSecrets(secrets))
			stream, target, err := h.r.StreamChain(context.Background(), chain("cc/m", "openai/x"), core.ChatRequest{}, CallMeta{}, nil)
			if tc.answered {
				if err != nil {
					t.Fatal(err)
				}
				_, _ = drain(stream)
				if target.Provider != "openai" || other.callCount() != 1 {
					t.Fatalf("target %v openai calls %d", target, other.callCount())
				}
			} else {
				var got *core.ProviderError
				if !errors.As(err, &got) || got.Status != 400 || other.callCount() != 0 {
					t.Fatalf("err %v openai calls %d", err, other.callCount())
				}
			}
			if b1.callCount() != tc.calls || b2.callCount() != 0 {
				t.Fatalf("calls cc %d cc-2 %d", b1.callCount(), b2.callCount())
			}
		})
	}
}

// A rate limit past its retries moves to the next connection.
func TestAPIPoolRateLimitMovesToNextConnection(t *testing.T) {
	b1 := &fakeBehavior{always: pe("rate_limit", 429, "slow down")}
	b2 := &fakeBehavior{}
	cc, cc2, secrets := connPool(t, b1, b2)
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"cc": cc, "cc-2": cc2}}, WithSecrets(secrets))
	_, target, err := h.r.StreamChain(context.Background(), chain("cc/m"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if target.Provider != "cc-2" || b1.callCount() != retriesBeforeNext+1 || b2.callCount() != 1 {
		t.Fatalf("target %v calls cc %d cc-2 %d", target, b1.callCount(), b2.callCount())
	}
}

// AC-ACP-004: cc-2 with pool = false is never used by cc/m; cc-2/m uses
// cc-2 only. A disabled connection is left out the same way.
func TestAPIPoolUnpooledAndDisabled(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*core.ProviderConfig)
	}{
		{"pool false", func(pc *core.ProviderConfig) { pc.Pool = boolPtr(false) }},
		{"disabled", func(pc *core.ProviderConfig) { pc.Disabled = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b1 := &fakeBehavior{always: pe("client", 402, "Insufficient Balance")}
			b2 := &fakeBehavior{}
			cc, cc2, secrets := connPool(t, b1, b2)
			tc.set(&cc2)
			h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"cc": cc, "cc-2": cc2}}, WithSecrets(secrets))
			if pool := h.r.APIPool("cc"); pool != nil {
				t.Fatalf("pool = %v", pool)
			}
			if _, _, err := h.r.StreamChain(context.Background(), chain("cc/m"), core.ChatRequest{}, CallMeta{}, nil); err == nil {
				t.Fatal("cc/m answered")
			}
			if b2.callCount() != 0 {
				t.Fatalf("cc-2 called %d times", b2.callCount())
			}
			if tc.name != "pool false" {
				return
			}
			b1.mu.Lock()
			b1.always = nil
			b1.mu.Unlock()
			_, target, err := h.r.StreamChain(context.Background(), chain("cc-2/m"), core.ChatRequest{}, CallMeta{}, nil)
			if err != nil || target.Provider != "cc-2" || b1.callCount() != 1 {
				t.Fatalf("target %v err %v cc calls %d", target, err, b1.callCount())
			}
		})
	}
}

// AC-ACP-005: openai_compat-like connections with different base URLs are
// two pools: a 429 on local/m never calls local-2.
func TestAPIPoolDifferentBaseURLs(t *testing.T) {
	b1 := &fakeBehavior{always: pe("rate_limit", 429, "slow down")}
	b2 := &fakeBehavior{}
	local := keyProvider(t, "8091", map[string]*fakeBehavior{"K1": b1}, "K1")
	local2 := keyProvider(t, "8092", map[string]*fakeBehavior{"K1": b2}, "K1")
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"local": local, "local-2": local2}},
		WithSecrets(mapSecrets{"K1": "sk-one"}))
	if pool := h.r.APIPool("local"); pool != nil {
		t.Fatalf("pool = %v", pool)
	}
	if _, _, err := h.r.StreamChain(context.Background(), chain("local/m"), core.ChatRequest{}, CallMeta{}, nil); err == nil {
		t.Fatal("local/m answered")
	}
	if b2.callCount() != 0 {
		t.Fatalf("local-2 called %d times", b2.callCount())
	}
}

// A combo [cc/x, cc-2/x] tries each connection once.
func TestAPIPoolComboTriesEachConnectionOnce(t *testing.T) {
	b1 := &fakeBehavior{always: pe("client", 402, "Insufficient Balance")}
	b2 := &fakeBehavior{always: pe("auth", 401, "bad key")}
	cc, cc2, secrets := connPool(t, b1, b2)
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"cc": cc, "cc-2": cc2}}, WithSecrets(secrets))
	if _, _, err := h.r.StreamChain(context.Background(), chain("cc/x", "cc-2/x"), core.ChatRequest{}, CallMeta{}, nil); err == nil {
		t.Fatal("answered")
	}
	if b1.callCount() != 1 || b2.callCount() != 1 {
		t.Fatalf("calls cc %d cc-2 %d", b1.callCount(), b2.callCount())
	}
	if got, want := outcomes(h.rec.all()), []string{"cc::quota", "cc-2::auth"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %v, want %v", got, want)
	}
}

// A connection cooling down is skipped; when every connection of the pool
// is cooling down, the pool is tried in order anyway.
func TestAPIPoolCooldown(t *testing.T) {
	b1 := &fakeBehavior{errs: []error{pe("client", 402, "Insufficient Balance")}}
	b2 := &fakeBehavior{errs: []error{pe("client", 402, "Insufficient Balance")}}
	cc, cc2, secrets := connPool(t, b1, b2)
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"cc": cc, "cc-2": cc2}}, WithSecrets(secrets))
	if _, _, err := h.r.StreamChain(context.Background(), chain("cc/m"), core.ChatRequest{}, CallMeta{}, nil); err == nil {
		t.Fatal("answered")
	}
	if cooling := h.r.Cooling(); len(cooling) != 2 {
		t.Fatalf("cooling = %v", cooling)
	}
	// Both cooling: cc is tried first and now answers.
	_, target, err := h.r.StreamChain(context.Background(), chain("cc/m"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil || target.Provider != "cc" {
		t.Fatalf("target %v err %v", target, err)
	}
	if b1.callCount() != 2 || b2.callCount() != 1 {
		t.Fatalf("calls cc %d cc-2 %d", b1.callCount(), b2.callCount())
	}
}

// Within a connection its own keys come first (REQ-PAF-005): cc's K1 out of
// quota moves to cc's K1B before cc-2.
func TestAPIPoolKeysBeforeNextConnection(t *testing.T) {
	k1 := &fakeBehavior{always: pe("client", 402, "Insufficient Balance")}
	k1b := &fakeBehavior{}
	k2 := &fakeBehavior{}
	cc := keyProvider(t, "cc", map[string]*fakeBehavior{"K1": k1, "K1B": k1b, "K2": k2}, "K1", "K1B")
	cc2 := cc
	cc2.APIKeyEnv, cc2.APIKeyEnvs = "K2", nil
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"cc": cc, "cc-2": cc2}},
		WithSecrets(mapSecrets{"K1": "a", "K1B": "b", "K2": "c"}))
	stream, target, err := h.r.StreamChain(context.Background(), chain("cc/m"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil || target.Provider != "cc" || k2.callCount() != 0 {
		t.Fatalf("target %v err %v cc-2 calls %d", target, err, k2.callCount())
	}
	_, _ = drain(stream)
	if got, want := outcomes(h.rec.all()), []string{"cc:K1:quota", "cc:K1B:ok"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %v, want %v", got, want)
	}
}
