package router

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// AC-RTR-004: a combo name, an address split on the first "/", and the legacy
// "provider:model" value resolve to their chains.
func TestResolve(t *testing.T) {
	cfg := Config{
		Providers: map[string]core.ProviderConfig{
			"a":          fakeProvider(t, "a", &fakeBehavior{}),
			"b":          fakeProvider(t, "b", &fakeBehavior{}),
			"openrouter": fakeProvider(t, "or", &fakeBehavior{}),
		},
		Combos: map[string]Combo{"coding": {Models: []string{"a/x", "openrouter/openai/gpt-5-mini", "b/y"}}},
	}
	h := newHarness(t, cfg)
	tests := []struct {
		in   string
		want []Target
	}{
		{"coding", chain("a/x", "openrouter/openai/gpt-5-mini", "b/y")},
		{"openrouter/openai/gpt-5-mini", []Target{{Provider: "openrouter", Model: "openai/gpt-5-mini"}}},
		{"openrouter:openai/gpt-5-mini", []Target{{Provider: "openrouter", Model: "openai/gpt-5-mini"}}},
		{"a/llama3:8b", []Target{{Provider: "a", Model: "llama3:8b"}}},
	}
	for _, tc := range tests {
		got, err := h.r.Resolve(tc.in)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Resolve(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	for _, in := range []string{"nope/x", "nope", "", "a/", "/x", "coding2"} {
		if _, err := h.r.Resolve(in); !errors.Is(err, ErrUnknownAddress) {
			t.Fatalf("Resolve(%q) err = %v, want ErrUnknownAddress", in, err)
		}
	}
}

// AC-RTR-006 (router side) and REQ-RTR-004: invalid combos and unknown kinds
// are rejected.
func TestNewRejectsInvalidConfig(t *testing.T) {
	providers := map[string]core.ProviderConfig{"a": fakeProvider(t, "a", &fakeBehavior{})}
	tests := []struct {
		name   string
		combos map[string]Combo
		want   string
	}{
		{"combo in combo", map[string]Combo{"coding": {Models: []string{"a/x", "coding2"}}, "coding2": {Models: []string{"a/y"}}}, "combos.coding.models[1]"},
		{"name equals provider", map[string]Combo{"a": {Models: []string{"a/x"}}}, "provider name"},
		{"name with slash", map[string]Combo{"x/y": {Models: []string{"a/x"}}}, "must not contain"},
		{"empty", map[string]Combo{"c": {}}, "combos.c.models"},
		{"unknown provider", map[string]Combo{"c": {Models: []string{"zz/x"}}}, "unknown provider"},
		{"legacy form", map[string]Combo{"c": {Models: []string{"a:x"}}}, "not a provider/model address"},
		{"duplicate", map[string]Combo{"c": {Models: []string{"a/x", "a/x"}}}, "duplicate"},
	}
	for _, tc := range tests {
		_, err := New(Config{Providers: providers, Combos: tc.combos})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	_, err := New(Config{Providers: map[string]core.ProviderConfig{"x": {Kind: "zzz"}}})
	if err == nil || err.Error() != "provider 'x' kind 'zzz' is not supported in this build" {
		t.Fatalf("unknown kind err = %v", err)
	}
}

// AC-RTR-007: an auth error moves to the next target at once; the attempt is
// reported and the served target returned.
func TestFallbackOnAuth(t *testing.T) {
	a := &fakeBehavior{always: pe("auth", 401, "HTTP 401: bad key")}
	b := &fakeBehavior{}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", a), "b": fakeProvider(t, "b", b)}})
	var attempts []Attempt
	stream, served, err := h.r.StreamChain(context.Background(), chain("a/x", "b/y"), core.ChatRequest{}, CallMeta{Source: "turn", Agent: "lana"}, func(at Attempt) {
		attempts = append(attempts, at)
	})
	if err != nil {
		t.Fatal(err)
	}
	text, err := drain(stream)
	if err != nil || text != "hi from b" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if served != (Target{Provider: "b", Model: "y"}) {
		t.Fatalf("served = %v", served)
	}
	if len(attempts) != 1 || attempts[0].Target != (Target{Provider: "a", Model: "x"}) || attempts[0].N != 1 || attempts[0].Err.Class != "auth" {
		t.Fatalf("attempts = %+v", attempts)
	}
	if a.callCount() != 1 || len(h.sleep.delays) != 0 {
		t.Fatalf("a calls=%d sleeps=%v", a.callCount(), h.sleep.delays)
	}
	if b.calls[0] != "y" {
		t.Fatalf("b got model %q", b.calls[0])
	}
	recs := h.rec.all()
	if len(recs) != 2 || recs[0].Outcome != "auth" || recs[0].Provider != "a" || recs[1].Outcome != "ok" || recs[1].Provider != "b" {
		t.Fatalf("records = %+v", recs)
	}
	for _, rec := range recs {
		if rec.Source != "turn" || rec.Agent != "lana" || rec.Requested != "a/x" {
			t.Fatalf("record meta = %+v", rec)
		}
	}
	// auth cools the target down.
	if _, ok := h.r.Cooling()[Target{Provider: "a", Model: "x"}]; !ok {
		t.Fatalf("a/x is not cooling: %v", h.r.Cooling())
	}
}

// AC-RTR-008: client and context_length errors stop the chain.
func TestNoFallbackOnClient(t *testing.T) {
	for _, class := range []string{"client", "context_length"} {
		t.Run(class, func(t *testing.T) {
			a := &fakeBehavior{always: pe(class, 400, "bad request")}
			b := &fakeBehavior{}
			h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", a), "b": fakeProvider(t, "b", b)}})
			_, _, err := h.r.StreamChain(context.Background(), chain("a/x", "b/y"), core.ChatRequest{}, CallMeta{}, nil)
			var got *core.ProviderError
			if !errors.As(err, &got) || got.Class != class {
				t.Fatalf("err = %v", err)
			}
			if b.callCount() != 0 || a.callCount() != 1 {
				t.Fatalf("a=%d b=%d", a.callCount(), b.callCount())
			}
			if len(h.r.Cooling()) != 0 {
				t.Fatalf("cooling = %v", h.r.Cooling())
			}
		})
	}
}

// REQ-RTR-005: 2 retries when later targets remain, 5 on the last target,
// with the backoff of agent/retry.go.
func TestRetryCounts(t *testing.T) {
	a := &fakeBehavior{always: pe("server", 500, "boom")}
	b := &fakeBehavior{always: pe("server", 503, "boom")}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", a), "b": fakeProvider(t, "b", b)}})
	var attempts []Attempt
	_, _, err := h.r.StreamChain(context.Background(), chain("a/x", "b/y"), core.ChatRequest{}, CallMeta{}, func(at Attempt) { attempts = append(attempts, at) })
	var got *core.ProviderError
	if !errors.As(err, &got) || got.Status != 503 {
		t.Fatalf("err = %v", err)
	}
	if a.callCount() != 3 || b.callCount() != 6 {
		t.Fatalf("a=%d b=%d, want 3 and 6", a.callCount(), b.callCount())
	}
	s := time.Second
	want := []time.Duration{2 * s, 4 * s, 2 * s, 4 * s, 8 * s, 16 * s, 32 * s}
	if !reflect.DeepEqual(h.sleep.delays, want) {
		t.Fatalf("delays = %v, want %v", h.sleep.delays, want)
	}
	if len(attempts) != 9 || attempts[2].N != 3 || attempts[3].N != 1 || attempts[8].N != 6 {
		t.Fatalf("attempts = %+v", attempts)
	}
	if len(h.rec.all()) != 9 {
		t.Fatalf("records = %d", len(h.rec.all()))
	}
	// server errors do not cool down (SC-001).
	if len(h.r.Cooling()) != 0 {
		t.Fatalf("cooling = %v", h.r.Cooling())
	}
}

// REQ-RTR-005: a Retry-After over 20s moves on when a target remains; on the
// last target the wait is capped at 120s. A 404 moves on.
func TestRetryAfterAndNotFound(t *testing.T) {
	limited := pe("rate_limit", 429, "slow down")
	limited.RetryAfter = 30 * time.Second
	a := &fakeBehavior{always: limited}
	b := &fakeBehavior{}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", a), "b": fakeProvider(t, "b", b)}})
	_, served, err := h.r.StreamChain(context.Background(), chain("a/x", "b/y"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil || served.Provider != "b" || a.callCount() != 1 || len(h.sleep.delays) != 0 {
		t.Fatalf("served=%v err=%v a=%d sleeps=%v", served, err, a.callCount(), h.sleep.delays)
	}
	if until := h.r.Cooling()[Target{Provider: "a", Model: "x"}]; !until.Equal(h.clock.Now().Add(5 * time.Minute)) {
		t.Fatalf("cooldown until %v", until)
	}

	long := pe("rate_limit", 429, "slow down")
	long.RetryAfter = 10 * time.Minute
	c := &fakeBehavior{errs: []error{long}}
	h2 := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"c": fakeProvider(t, "c", c)}})
	_, _, err = h2.r.StreamChain(context.Background(), chain("c/z"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil || c.callCount() != 2 || !reflect.DeepEqual(h2.sleep.delays, []time.Duration{120 * time.Second}) {
		t.Fatalf("err=%v c=%d sleeps=%v", err, c.callCount(), h2.sleep.delays)
	}

	d := &fakeBehavior{always: pe("client", 404, "model not found")}
	e := &fakeBehavior{}
	h3 := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"d": fakeProvider(t, "d", d), "e": fakeProvider(t, "e", e)}})
	_, served, err = h3.r.StreamChain(context.Background(), chain("d/x", "e/y", "zz/q"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil || served.Provider != "e" {
		t.Fatalf("served=%v err=%v", served, err)
	}
	if recs := h3.rec.all(); recs[0].Outcome != core.ErrorClassNotFound {
		t.Fatalf("records = %+v", recs)
	}
	// An unknown provider in a chain is not_found and moves on.
	_, served, err = h3.r.StreamChain(context.Background(), chain("zz/q", "e/y"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil || served.Provider != "e" {
		t.Fatalf("served=%v err=%v", served, err)
	}
}

// AC-RTR-009: a 429 at 10:00 cools a/x for 5 minutes; at 10:02 a is skipped,
// at 10:06 a is tried first again.
func TestCooldownTimeline(t *testing.T) {
	limited := pe("rate_limit", 429, "slow down")
	a := &fakeBehavior{errs: []error{limited, limited, limited}}
	b := &fakeBehavior{}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", a), "b": fakeProvider(t, "b", b)}})
	call := func() Target {
		t.Helper()
		stream, served, err := h.r.StreamChain(context.Background(), chain("a/x", "b/y"), core.ChatRequest{}, CallMeta{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := drain(stream); err != nil {
			t.Fatal(err)
		}
		return served
	}
	start := h.clock.Now()
	if served := call(); served.Provider != "b" || a.callCount() != 3 {
		t.Fatalf("10:00 served=%v a=%d", served, a.callCount())
	}
	h.clock.Set(start.Add(2 * time.Minute))
	if served := call(); served.Provider != "b" || a.callCount() != 3 {
		t.Fatalf("10:02 served=%v a=%d", served, a.callCount())
	}
	h.clock.Set(start.Add(6 * time.Minute))
	if served := call(); served.Provider != "a" || a.callCount() != 4 {
		t.Fatalf("10:06 served=%v a=%d", served, a.callCount())
	}
	if len(h.r.Cooling()) != 0 {
		t.Fatalf("cooling = %v", h.r.Cooling())
	}

	// A negative cooldown turns it off.
	c := &fakeBehavior{errs: []error{pe("auth", 401, "no")}}
	h2 := newHarness(t, Config{Cooldown: -1, Providers: map[string]core.ProviderConfig{"c": fakeProvider(t, "c", c), "d": fakeProvider(t, "d", &fakeBehavior{})}})
	if _, _, err := h2.r.StreamChain(context.Background(), chain("c/x", "d/y"), core.ChatRequest{}, CallMeta{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(h2.r.Cooling()) != 0 {
		t.Fatalf("cooling with cooldown off = %v", h2.r.Cooling())
	}
}

// REQ-RTR-006: when every target is cooling down, the full chain is tried.
func TestAllCoolingTriesFullChain(t *testing.T) {
	a := &fakeBehavior{errs: []error{pe("auth", 401, "no")}}
	b := &fakeBehavior{errs: []error{pe("auth", 403, "no")}}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", a), "b": fakeProvider(t, "b", b)}})
	_, _, err := h.r.StreamChain(context.Background(), chain("a/x", "b/y"), core.ChatRequest{}, CallMeta{}, nil)
	var got *core.ProviderError
	if !errors.As(err, &got) || got.Status != 403 {
		t.Fatalf("err = %v, want the last error", err)
	}
	if len(h.r.Cooling()) != 2 {
		t.Fatalf("cooling = %v", h.r.Cooling())
	}
	_, served, err := h.r.StreamChain(context.Background(), chain("a/x", "b/y"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil || served.Provider != "a" {
		t.Fatalf("served=%v err=%v", served, err)
	}
	// Success clears the cooldown of the served target.
	if _, ok := h.r.Cooling()[Target{Provider: "a", Model: "x"}]; ok {
		t.Fatal("a/x still cooling after success")
	}
}

// REQ-RTR-009, REQ-RTR-010: cost math, unknown price → nil.
func TestCost(t *testing.T) {
	usage := core.Usage{InputTokens: 1000, CachedInputTokens: 400, OutputTokens: 200}
	got := Cost(&Price{Input: 1, CachedInput: 0.1, Output: 2}, usage)
	if got == nil || !near(*got, (600*1+400*0.1+200*2)/1e6) {
		t.Fatalf("cost = %v", got)
	}
	// A zero cached price falls back to the input price.
	got = Cost(&Price{Input: 1, Output: 2}, usage)
	if got == nil || !near(*got, (1000*1+200*2)/1e6) {
		t.Fatalf("cost without cached price = %v", got)
	}
	if Cost(nil, usage) != nil {
		t.Fatal("unknown price must give nil cost")
	}
}

func near(a, b float64) bool { return a-b < 1e-12 && b-a < 1e-12 }

// AC-RTR-012 (data side), AC-RTR-014: an ok record carries tokens, catalog
// cost, TTFT, and duration; an unknown model has no cost.
func TestRecordOK(t *testing.T) {
	usage := &core.Usage{InputTokens: 1000, CachedInputTokens: 400, OutputTokens: 200}
	h := newHarness(t, Config{})
	block := make(chan struct{})
	a := &fakeBehavior{usage: usage, block: block, clock: h.clock, advance: 100 * time.Millisecond}
	if err := h.r.Apply(Config{
		Providers: map[string]core.ProviderConfig{"oa": fakeProvider(t, "oa", a)},
		Combos:    map[string]Combo{"fast": {Models: []string{"oa/gpt-5-mini"}}},
	}); err != nil {
		t.Fatal(err)
	}
	stream, err := h.r.Stream(context.Background(), "fast", core.ChatRequest{}, CallMeta{Source: "turn", Agent: "lana", SessionID: "s1", TurnID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	close(block)
	if _, err := drain(stream); err != nil {
		t.Fatal(err)
	}
	recs := h.rec.all()
	if len(recs) != 1 {
		t.Fatalf("records = %+v", recs)
	}
	rec := recs[0]
	if rec.Outcome != "ok" || rec.Requested != "fast" || rec.Provider != "oa" || rec.Model != "gpt-5-mini" || rec.Attempt != 1 ||
		rec.SessionID != "s1" || rec.TurnID != "t1" || rec.InputTokens != 1000 || rec.CachedTokens != 400 || rec.OutputTokens != 200 {
		t.Fatalf("record = %+v", rec)
	}
	if rec.CostUSD == nil || !near(*rec.CostUSD, (600*0.25+400*0.025+200*2)/1e6) {
		t.Fatalf("cost = %v", rec.CostUSD)
	}
	if rec.TTFTMs != 100 || rec.DurationMs != 200 {
		t.Fatalf("ttft=%d duration=%d", rec.TTFTMs, rec.DurationMs)
	}

	b := &fakeBehavior{usage: usage}
	h2 := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"x": fakeProvider(t, "x", b)}})
	stream, err = h2.r.Stream(context.Background(), "x/mystery-model", core.ChatRequest{}, CallMeta{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = drain(stream)
	if recs := h2.rec.all(); len(recs) != 1 || recs[0].CostUSD != nil || recs[0].InputTokens != 1000 {
		t.Fatalf("unknown price record = %+v", recs)
	}
}

// A stream that fails after its first event records the error class.
func TestRecordMidStreamError(t *testing.T) {
	a := &fakeBehavior{midErr: pe("network", 0, "connection reset")}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", a)}})
	stream, err := h.r.Stream(context.Background(), "a/x", core.ChatRequest{}, CallMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drain(stream); err == nil {
		t.Fatal("want the mid-stream error")
	}
	if recs := h.rec.all(); len(recs) != 1 || recs[0].Outcome != "network" || !strings.Contains(recs[0].Error, "connection reset") {
		t.Fatalf("records = %+v", recs)
	}
}

// AC-RTR-013: the API key never reaches the record or the attempt, and the
// stored message is at most 500 characters.
func TestRedaction(t *testing.T) {
	key := "sk-secret-123456"
	a := &fakeBehavior{errs: []error{pe("auth", 401, "HTTP 401: invalid key "+key+" "+strings.Repeat("é", 900))}}
	cfg := fakeProvider(t, "a", a)
	cfg.APIKeyEnv = "A_KEY"
	b := &fakeBehavior{}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": cfg, "b": fakeProvider(t, "b", b)}}, WithSecrets(mapSecrets{"A_KEY": key}))
	var attempt Attempt
	if _, _, err := h.r.StreamChain(context.Background(), chain("a/x", "b/y"), core.ChatRequest{}, CallMeta{}, func(at Attempt) { attempt = at }); err != nil {
		t.Fatal(err)
	}
	rec := h.rec.all()[0]
	if strings.Contains(rec.Error, key) || !strings.Contains(rec.Error, "***") {
		t.Fatalf("record error = %q", rec.Error)
	}
	if n := len([]rune(rec.Error)); n > 500 {
		t.Fatalf("record error has %d characters", n)
	}
	if strings.Contains(attempt.Err.Message, key) {
		t.Fatalf("attempt error = %q", attempt.Err.Message)
	}
	// The returned error of a failed chain is redacted too.
	a.errs = []error{pe("auth", 401, "bad "+key)}
	_, _, err := h.r.StreamChain(context.Background(), chain("a/x"), core.ChatRequest{}, CallMeta{}, nil)
	if err == nil || strings.Contains(err.Error(), key) {
		t.Fatalf("err = %v", err)
	}
	if got := Redact("a "+key+" b "+key, key, ""); got != "a *** b ***" {
		t.Fatalf("Redact = %q", got)
	}
}

// REQ-RTR-008: Apply swaps atomically; an in-flight call finishes on the old
// snapshot; unchanged providers keep their adapter; a failed Apply keeps the
// old config.
func TestApplyAtomic(t *testing.T) {
	block := make(chan struct{})
	old := &fakeBehavior{text: "old", block: block}
	keep := &fakeBehavior{}
	cfg1 := Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "old", old), "k": fakeProvider(t, "k", keep)}}
	h := newHarness(t, cfg1)
	kBefore, _ := h.r.Provider("k")
	aBefore, _ := h.r.Provider("a")

	stream, err := h.r.Stream(context.Background(), "a/x", core.ChatRequest{}, CallMeta{})
	if err != nil {
		t.Fatal(err)
	}
	next := &fakeBehavior{text: "new"}
	cfg2 := Config{
		Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "new", next), "k": cfg1.Providers["k"]},
		Combos:    map[string]Combo{"c": {Models: []string{"a/x"}}},
	}
	if err := h.r.Apply(cfg2); err != nil {
		t.Fatal(err)
	}
	close(block)
	if text, err := drain(stream); err != nil || text != "old" {
		t.Fatalf("in-flight text=%q err=%v", text, err)
	}
	stream, err = h.r.Stream(context.Background(), "c", core.ChatRequest{}, CallMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := drain(stream); text != "new" {
		t.Fatalf("new call text = %q", text)
	}
	kAfter, _ := h.r.Provider("k")
	aAfter, _ := h.r.Provider("a")
	if kAfter != kBefore {
		t.Fatal("unchanged provider k got a new adapter")
	}
	if aAfter == aBefore {
		t.Fatal("changed provider a kept its adapter")
	}

	bad := Config{Providers: map[string]core.ProviderConfig{"z": {Kind: "zzz"}}}
	if err := h.r.Apply(bad); err == nil {
		t.Fatal("Apply with an unknown kind succeeded")
	}
	if names := h.r.Names(); !reflect.DeepEqual(names, []string{"a", "k"}) {
		t.Fatalf("names after failed Apply = %v", names)
	}
	if _, err := h.r.Resolve("c"); err != nil {
		t.Fatalf("combo lost after failed Apply: %v", err)
	}
}

type memLimitStore struct {
	loaded []LimitSnapshot
	saved  [][]LimitSnapshot
}

func (s *memLimitStore) Load() []LimitSnapshot  { return s.loaded }
func (s *memLimitStore) Save(v []LimitSnapshot) { s.saved = append(s.saved, v) }

// REQ-RTR-012: updates merge per field, balances refresh at most every 5
// minutes, and snapshots load at New and save on Close.
func TestLimits(t *testing.T) {
	bal := &fakeBehavior{balance: &Balance{Currency: "USD", Total: 4.2, ToppedUp: 4.2}}
	store := &memLimitStore{loaded: []LimitSnapshot{{Provider: "claude-agent", Kind: "claude-code", Windows: []Window{{Type: "five_hour", Status: "allowed", Utilization: 0.42}}}}}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{
		"ds": fakeProvider(t, "ds", bal),
	}}, WithLimitStore(store), WithProviders(map[string]core.Provider{"plain": providerFunc(nil)}))

	h.r.UpdateLimits(LimitUpdate{Provider: "ds", Rate: &RateLimits{Tokens: &RateLimit{Limit: 150000, Remaining: 149000}}})
	h.r.UpdateLimits(LimitUpdate{Provider: "ds", Rate: &RateLimits{Requests: &RateLimit{Limit: 500, Remaining: 499}}})
	got := h.r.Limits(context.Background(), false)
	if len(got) != 3 || got[0].Provider != "ds" || got[1].Provider != "plain" || got[2].Provider != "claude-agent" {
		t.Fatalf("limits = %+v", got)
	}
	ds := got[0]
	if ds.Kind != "fake" || ds.Rate.Tokens.Remaining != 149000 || ds.Rate.Requests.Remaining != 499 || ds.Balance != nil {
		t.Fatalf("ds = %+v", ds)
	}
	if got[1].Kind != "custom" || got[1].Rate != nil {
		t.Fatalf("plain = %+v", got[1])
	}
	if bal.balances != 0 {
		t.Fatal("balance fetched without refresh")
	}

	got = h.r.Limits(context.Background(), true)
	if bal.balances != 1 || got[0].Balance == nil || got[0].Balance.Total != 4.2 || got[0].Rate.Tokens == nil {
		t.Fatalf("after refresh balances=%d ds=%+v", bal.balances, got[0])
	}
	h.clock.Advance(time.Minute)
	h.r.Limits(context.Background(), true)
	if bal.balances != 1 {
		t.Fatalf("refresh within 5 minutes fetched again (%d)", bal.balances)
	}
	h.clock.Advance(5 * time.Minute)
	bal.balErr = errors.New("balance endpoint down")
	got = h.r.Limits(context.Background(), true)
	if bal.balances != 2 || got[0].Error != "balance endpoint down" || got[0].Balance == nil {
		t.Fatalf("after failed refresh balances=%d ds=%+v", bal.balances, got[0])
	}
	h.clock.Advance(6 * time.Minute)
	bal.balErr = nil
	if got = h.r.Limits(context.Background(), true); got[0].Error != "" {
		t.Fatalf("error not cleared: %+v", got[0])
	}

	// Adapters push header limits through Deps.Limits.
	h.r.snap.Load().providers["ds"].adapter.(*fakeAdapter).deps.Limits(LimitUpdate{Rate: &RateLimits{InputTokens: &RateLimit{Limit: 10}}})
	if got = h.r.Limits(context.Background(), false); got[0].Rate.InputTokens == nil || got[0].Kind != "fake" {
		t.Fatalf("deps update = %+v", got[0])
	}

	if err := h.r.Close(); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 || len(store.saved[0]) != 2 {
		t.Fatalf("saved = %+v", store.saved)
	}
}

type providerFunc func(context.Context, core.ChatRequest) (core.ChatStream, error)

func (f providerFunc) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	return f(ctx, req)
}

// REQ-RTR-013: Models merges the catalog of the kind with the provider list,
// caches it, and tolerates ErrNotSupported and fetch errors.
func TestModels(t *testing.T) {
	b := &fakeBehavior{models: []ModelInfo{
		{ID: "gpt-5-mini", Price: &Price{Input: 9, CachedInput: 9, Output: 9}},
		{ID: "custom-x", ContextWindow: 4096},
	}}
	adapter := &fakeAdapter{name: "oa", b: b}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"oa": {Kind: "openai"}}},
		WithProviders(map[string]core.Provider{"oa": adapter, "plain": providerFunc(nil)}))
	list, err := h.r.Models(context.Background(), "oa", false)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ModelInfo{}
	for _, m := range list {
		byID[m.ID] = m
	}
	if m := byID["gpt-5.1"]; m.Source != "catalog" || m.Address != "oa/gpt-5.1" || m.Price == nil || !m.Thinking {
		t.Fatalf("gpt-5.1 = %+v", m)
	}
	if m := byID["gpt-5-mini"]; m.Source != "both" || m.Price.Input != 9 || m.ContextWindow != 400000 {
		t.Fatalf("gpt-5-mini = %+v", m)
	}
	if m := byID["custom-x"]; m.Source != "provider" || m.Name != "custom-x" || m.Price != nil || m.ContextWindow != 4096 {
		t.Fatalf("custom-x = %+v", m)
	}
	if _, _ = h.r.Models(context.Background(), "oa", false); b.lists != 1 {
		t.Fatalf("cached list fetched again: %d", b.lists)
	}
	if _, _ = h.r.Models(context.Background(), "oa", true); b.lists != 2 {
		t.Fatalf("refresh did not fetch: %d", b.lists)
	}
	// The provider price wins for cost once listed.
	b.usage = &core.Usage{InputTokens: 1000000}
	stream, err := h.r.Stream(context.Background(), "oa/gpt-5-mini", core.ChatRequest{}, CallMeta{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = drain(stream)
	if rec := h.rec.all()[0]; rec.CostUSD == nil || !near(*rec.CostUSD, 9) {
		t.Fatalf("cost = %v", rec.CostUSD)
	}

	if at, ok := h.r.ModelsFetchedAt("oa"); !ok || at.IsZero() {
		t.Fatalf("ModelsFetchedAt(oa) = %v, %v", at, ok)
	}
	if _, ok := h.r.ModelsFetchedAt("nope"); ok {
		t.Fatal("ModelsFetchedAt(nope) reported a fetch")
	}

	plain, err := h.r.Models(context.Background(), "plain", false)
	if err != nil || len(plain) != 0 {
		t.Fatalf("plain = %+v, %v", plain, err)
	}
	b.listErr = errors.New("models endpoint down")
	list, err = h.r.Models(context.Background(), "oa", true)
	if err == nil || len(list) == 0 || list[0].Source != "catalog" {
		t.Fatalf("on fetch error list=%d err=%v", len(list), err)
	}
	if _, err := h.r.Models(context.Background(), "nope", false); !errors.Is(err, ErrUnknownAddress) {
		t.Fatalf("unknown provider err = %v", err)
	}
}

// REQ-RTR-007: Embed uses the adapter's embedder and records the source.
func TestEmbed(t *testing.T) {
	b := &fakeBehavior{}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", b)}},
		WithProviders(map[string]core.Provider{"plain": providerFunc(nil)}))
	vecs, err := h.r.Embed(context.Background(), "a/emb", []string{"x", "y"}, CallMeta{Source: "knowledge"})
	if err != nil || len(vecs) != 2 {
		t.Fatalf("vecs=%v err=%v", vecs, err)
	}
	b.embedErr = pe("server", 500, "boom")
	if _, err := h.r.Embed(context.Background(), "a/emb", []string{"x"}, CallMeta{}); err == nil {
		t.Fatal("want error")
	}
	recs := h.rec.all()
	if len(recs) != 7 || recs[0].Source != "knowledge" || recs[0].Outcome != "ok" || recs[1].Source != "embedding" || recs[6].Outcome != "server" {
		t.Fatalf("records = %+v", recs)
	}
	var pErr *core.ProviderError
	if _, err := h.r.Embed(context.Background(), "plain/x", []string{"x"}, CallMeta{}); !errors.As(err, &pErr) || pErr.Class != "client" {
		t.Fatalf("plain embed err = %v", err)
	}
}

// A recorder that panics does not break the stream.
func TestRecorderPanicIsContained(t *testing.T) {
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", &fakeBehavior{})}},
		WithRecorder(panicRecorder{}))
	stream, err := h.r.Stream(context.Background(), "a/x", core.ChatRequest{}, CallMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if text, err := drain(stream); err != nil || text != "hi from a" {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

type panicRecorder struct{}

func (panicRecorder) Record(CallRecord) { panic("boom") }

// A cancelled context stops the chain without more attempts.
func TestContextCancelStopsChain(t *testing.T) {
	a := &fakeBehavior{always: pe("server", 500, "boom")}
	b := &fakeBehavior{}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", a), "b": fakeProvider(t, "b", b)}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := h.r.StreamChain(ctx, chain("a/x", "b/y"), core.ChatRequest{}, CallMeta{}, nil)
	if !errors.Is(err, context.Canceled) || b.callCount() != 0 {
		t.Fatalf("err=%v b=%d", err, b.callCount())
	}
	if _, _, err := h.r.StreamChain(context.Background(), nil, core.ChatRequest{}, CallMeta{}, nil); err == nil {
		t.Fatal("empty chain succeeded")
	}
}

// Custom providers (even non-comparable ones) survive Apply.
func TestApplyKeepsCustomProviders(t *testing.T) {
	h := newHarness(t, Config{}, WithProviders(map[string]core.Provider{"plain": providerFunc(nil)}))
	if err := h.r.Apply(Config{Providers: map[string]core.ProviderConfig{"plain": {Kind: "openai_compat", BaseURL: "mock://x"}}}); err != nil {
		t.Fatal(err)
	}
	if err := h.r.Apply(Config{Providers: map[string]core.ProviderConfig{"plain": {Kind: "openai_compat", BaseURL: "mock://x"}}}); err != nil {
		t.Fatal(err)
	}
	if kind, _ := h.r.Kind("plain"); kind != "openai_compat" {
		t.Fatalf("kind = %q", kind)
	}
	if names := h.r.Names(); len(names) != 1 || names[0] != "plain" {
		t.Fatalf("names = %v", names)
	}
}

// Spec 37 failure table: a provider whose API key env is empty fails with
// missing_secret without being called, and the chain moves on; a key that
// appears later (secrets.env written from the dashboard) is used at once.
func TestMissingKeyMovesOn(t *testing.T) {
	a := &fakeBehavior{}
	cfg := fakeProvider(t, "a", a)
	cfg.APIKeyEnv = "A_KEY"
	b := &fakeBehavior{text: "from b"}
	secrets := mapSecrets{}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": cfg, "b": fakeProvider(t, "b", b)}}, WithSecrets(secrets))
	var attempts []Attempt
	stream, served, err := h.r.StreamChain(context.Background(), chain("a/x", "b/y"), core.ChatRequest{}, CallMeta{}, func(at Attempt) { attempts = append(attempts, at) })
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := drain(stream); text != "from b" || served.Provider != "b" {
		t.Fatalf("served = %v, text = %q", served, text)
	}
	if len(attempts) != 1 || attempts[0].Err.Class != "missing_secret" || !strings.Contains(attempts[0].Err.Message, "A_KEY") {
		t.Fatalf("attempts = %+v", attempts)
	}
	if a.callCount() != 0 {
		t.Fatalf("provider a was called %d times without a key", a.callCount())
	}
	secrets["A_KEY"] = "k"
	stream, served, err = h.r.StreamChain(context.Background(), chain("a/x", "b/y"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil || served.Provider != "a" {
		t.Fatalf("served = %v, err = %v", served, err)
	}
	_, _ = drain(stream)
}
