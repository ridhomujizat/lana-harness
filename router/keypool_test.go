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

// The fakekey kind builds one fake adapter per API key: its behavior is
// looked up by base URL and key env, so a test scripts each key alone.
func init() {
	RegisterKind("fakekey", func(name string, cfg core.ProviderConfig, deps Deps) (Adapter, error) {
		fakesMu.Lock()
		b, ok := fakes[cfg.BaseURL+"#"+cfg.APIKeyEnv]
		fakesMu.Unlock()
		if !ok {
			return nil, errors.New("no fake behavior for " + cfg.BaseURL + "#" + cfg.APIKeyEnv)
		}
		if len(cfg.APIKeyEnvs) > 0 {
			return nil, errors.New("a key client got api_key_envs")
		}
		return &fakeAdapter{name: name, b: b, deps: deps}, nil
	})
}

// keyProvider registers one behavior per key env and returns a provider
// config with api_key_env = envs[0] and api_key_envs = envs[1:].
func keyProvider(t *testing.T, id string, keys map[string]*fakeBehavior, envs ...string) core.ProviderConfig {
	t.Helper()
	url := "fakekey://" + t.Name() + "/" + id
	fakesMu.Lock()
	for env, b := range keys {
		fakes[url+"#"+env] = b
	}
	fakesMu.Unlock()
	t.Cleanup(func() {
		fakesMu.Lock()
		for env := range keys {
			delete(fakes, url+"#"+env)
		}
		fakesMu.Unlock()
	})
	return core.ProviderConfig{Kind: "fakekey", BaseURL: url, APIKeyEnv: envs[0], APIKeyEnvs: envs[1:]}
}

func outcomes(recs []CallRecord) []string {
	out := make([]string, len(recs))
	for i, rec := range recs {
		out[i] = rec.Provider + ":" + rec.Key + ":" + rec.Outcome
	}
	return out
}

// AC-PAF-005 (router side): a key out of quota moves the call to the next
// key of the same target at once; the records name each key; the spent key
// is skipped on the next call until its cooldown ends.
func TestKeyPoolQuotaMovesToNextKey(t *testing.T) {
	k1 := &fakeBehavior{always: pe("client", 402, "HTTP 402: Insufficient Balance")}
	k2 := &fakeBehavior{}
	ds := keyProvider(t, "ds", map[string]*fakeBehavior{"K1": k1, "K2": k2}, "K1", "K2")
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"ds": ds}},
		WithSecrets(mapSecrets{"K1": "sk-one", "K2": "sk-two"}))
	var attempts []Attempt
	stream, target, err := h.r.StreamChain(context.Background(), chain("ds/m"), core.ChatRequest{}, CallMeta{}, func(a Attempt) { attempts = append(attempts, a) })
	if err != nil {
		t.Fatal(err)
	}
	if text, err := drain(stream); err != nil || text != "hi from ds" || target.Provider != "ds" {
		t.Fatalf("text %q err %v target %v", text, err, target)
	}
	if got, want := outcomes(h.rec.all()), []string{"ds:K1:quota", "ds:K2:ok"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %v, want %v", got, want)
	}
	if recs := h.rec.all(); recs[0].Attempt != 1 || recs[1].Attempt != 2 {
		t.Fatalf("attempt numbers = %d, %d", recs[0].Attempt, recs[1].Attempt)
	}
	if len(attempts) != 1 || attempts[0].Err.Class != "client" {
		t.Fatalf("attempts = %+v", attempts)
	}
	if len(h.sleep.delays) != 0 {
		t.Fatalf("slept %v before the next key", h.sleep.delays)
	}
	// The target itself is not cooling down: a key was left.
	if cooling := h.r.Cooling(); len(cooling) != 0 {
		t.Fatalf("cooling targets = %v", cooling)
	}
	until, ok := h.r.KeyCooling("ds")["K1"]
	if !ok || !until.Equal(h.clock.Now().Add(time.Hour)) {
		t.Fatalf("K1 cooling = %v %v", until, ok)
	}

	// The next call skips the cooling key.
	if _, _, err := h.r.StreamChain(context.Background(), chain("ds/m"), core.ChatRequest{}, CallMeta{}, nil); err != nil {
		t.Fatal(err)
	}
	if k1.callCount() != 1 || k2.callCount() != 2 {
		t.Fatalf("calls K1 %d K2 %d", k1.callCount(), k2.callCount())
	}

	// After the cooldown K1 is first again.
	h.clock.Advance(time.Hour + time.Second)
	k1.mu.Lock()
	k1.always = nil
	k1.mu.Unlock()
	if _, _, err := h.r.StreamChain(context.Background(), chain("ds/m"), core.ChatRequest{}, CallMeta{}, nil); err != nil {
		t.Fatal(err)
	}
	if k1.callCount() != 2 || k2.callCount() != 2 {
		t.Fatalf("after cooldown calls K1 %d K2 %d", k1.callCount(), k2.callCount())
	}
	if len(h.r.KeyCooling("ds")) != 0 {
		t.Fatalf("key cooling = %v", h.r.KeyCooling("ds"))
	}
}

// AC-PAF-006: when every key answers 429 past its retries, the chain moves
// to its next target; each key gets its own retries.
func TestKeyPoolRateLimitAllKeysMovesOn(t *testing.T) {
	k1 := &fakeBehavior{always: pe("rate_limit", 429, "slow down")}
	k2 := &fakeBehavior{always: pe("rate_limit", 429, "slow down")}
	ds := keyProvider(t, "ds", map[string]*fakeBehavior{"K1": k1, "K2": k2}, "K1", "K2")
	b := &fakeBehavior{}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"ds": ds, "b": fakeProvider(t, "b", b)}},
		WithSecrets(mapSecrets{"K1": "sk-one", "K2": "sk-two"}))
	stream, target, err := h.r.StreamChain(context.Background(), chain("ds/m", "b/y"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = drain(stream)
	if target.Provider != "b" {
		t.Fatalf("target = %v", target)
	}
	if k1.callCount() != retriesBeforeNext+1 || k2.callCount() != retriesBeforeNext+1 {
		t.Fatalf("calls K1 %d K2 %d", k1.callCount(), k2.callCount())
	}
	want := []time.Duration{2 * time.Second, 4 * time.Second, 2 * time.Second, 4 * time.Second}
	if !reflect.DeepEqual(h.sleep.delays, want) {
		t.Fatalf("delays = %v, want %v", h.sleep.delays, want)
	}
	recs := h.rec.all()
	if got := outcomes(recs); !reflect.DeepEqual(got, []string{
		"ds:K1:rate_limit", "ds:K1:rate_limit", "ds:K1:rate_limit",
		"ds:K2:rate_limit", "ds:K2:rate_limit", "ds:K2:rate_limit", "b::ok",
	}) {
		t.Fatalf("records = %v", got)
	}
	if recs[5].Attempt != 6 {
		t.Fatalf("last ds attempt = %d", recs[5].Attempt)
	}
	// Both keys and the target cool down.
	if cooling := h.r.KeyCooling("ds"); len(cooling) != 2 {
		t.Fatalf("key cooling = %v", cooling)
	}
	if _, ok := h.r.Cooling()[Target{Provider: "ds", Model: "m"}]; !ok {
		t.Fatalf("ds/m not cooling: %v", h.r.Cooling())
	}
}

// A refused key cools down for 5 minutes and the next key answers; a key
// whose secret is empty is skipped without a call.
func TestKeyPoolAuthAndMissingKey(t *testing.T) {
	k1 := &fakeBehavior{always: pe("auth", 401, "bad key")}
	k2 := &fakeBehavior{}
	k3 := &fakeBehavior{}
	ds := keyProvider(t, "ds", map[string]*fakeBehavior{"K1": k1, "K2": k2, "K3": k3}, "K1", "K2", "K3")
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"ds": ds}},
		WithSecrets(mapSecrets{"K1": "sk-one", "K3": "sk-three"}))
	stream, _, err := h.r.StreamChain(context.Background(), chain("ds/m"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = drain(stream)
	if k1.callCount() != 1 || k2.callCount() != 0 || k3.callCount() != 1 {
		t.Fatalf("calls K1 %d K2 %d K3 %d", k1.callCount(), k2.callCount(), k3.callCount())
	}
	if got := outcomes(h.rec.all()); !reflect.DeepEqual(got, []string{"ds:K1:auth", "ds:K3:ok"}) {
		t.Fatalf("records = %v", got)
	}
	if until := h.r.KeyCooling("ds")["K1"]; !until.Equal(h.clock.Now().Add(5 * time.Minute)) {
		t.Fatalf("K1 cooling until %v", until)
	}
}

// A rate-limited key cools down for max(cooldown, Retry-After), or a minute
// when both are zero.
func TestKeyPoolRateLimitCooldown(t *testing.T) {
	limited := &core.ProviderError{Class: "rate_limit", Status: 429, Message: "wait", RetryAfter: 10 * time.Minute}
	k1 := &fakeBehavior{errs: []error{limited}}
	k2 := &fakeBehavior{}
	ds := keyProvider(t, "ds", map[string]*fakeBehavior{"K1": k1, "K2": k2}, "K1", "K2")
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"ds": ds}, Cooldown: -1},
		WithSecrets(mapSecrets{"K1": "sk-one", "K2": "sk-two"}))
	// A long Retry-After moves to the next key at once, even on the last
	// target.
	if _, _, err := h.r.StreamChain(context.Background(), chain("ds/m"), core.ChatRequest{}, CallMeta{}, nil); err != nil {
		t.Fatal(err)
	}
	if k1.callCount() != 1 || k2.callCount() != 1 || len(h.sleep.delays) != 0 {
		t.Fatalf("calls K1 %d K2 %d delays %v", k1.callCount(), k2.callCount(), h.sleep.delays)
	}
	if until := h.r.KeyCooling("ds")["K1"]; !until.Equal(h.clock.Now().Add(10 * time.Minute)) {
		t.Fatalf("K1 cooling until %v", until)
	}

	h.clock.Advance(11 * time.Minute)
	k1.mu.Lock()
	k1.always = pe("rate_limit", 429, "slow")
	k1.mu.Unlock()
	if _, _, err := h.r.StreamChain(context.Background(), chain("ds/m"), core.ChatRequest{}, CallMeta{}, nil); err != nil {
		t.Fatal(err)
	}
	// Cooldown off and no Retry-After: one minute.
	if until := h.r.KeyCooling("ds")["K1"]; !until.Equal(h.clock.Now().Add(time.Minute)) {
		t.Fatalf("K1 cooling until %v", until)
	}
}

// When every key is cooling down, the first one is tried anyway.
func TestKeyPoolAllCoolingTriesFirst(t *testing.T) {
	k1 := &fakeBehavior{errs: []error{pe("auth", 401, "no")}}
	k2 := &fakeBehavior{errs: []error{pe("auth", 401, "no")}}
	ds := keyProvider(t, "ds", map[string]*fakeBehavior{"K1": k1, "K2": k2}, "K1", "K2")
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"ds": ds}},
		WithSecrets(mapSecrets{"K1": "sk-one", "K2": "sk-two"}))
	if _, _, err := h.r.StreamChain(context.Background(), chain("ds/m"), core.ChatRequest{}, CallMeta{}, nil); err == nil {
		t.Fatal("want an error")
	}
	if len(h.r.KeyCooling("ds")) != 2 {
		t.Fatalf("key cooling = %v", h.r.KeyCooling("ds"))
	}
	if _, _, err := h.r.StreamChain(context.Background(), chain("ds/m"), core.ChatRequest{}, CallMeta{}, nil); err != nil {
		t.Fatal(err)
	}
	if k1.callCount() != 2 || k2.callCount() != 1 {
		t.Fatalf("calls K1 %d K2 %d", k1.callCount(), k2.callCount())
	}
	if _, cooling := h.r.KeyCooling("ds")["K1"]; cooling {
		t.Fatal("K1 still cooling after a success")
	}
}

// Every key value of a pool is redacted from errors and records.
func TestKeyPoolRedaction(t *testing.T) {
	one, two := "sk-first-111111", "sk-second-222222"
	k1 := &fakeBehavior{always: pe("auth", 401, "bad "+one+" and "+two)}
	k2 := &fakeBehavior{always: pe("client", 400, "nope "+two+" "+one)}
	ds := keyProvider(t, "ds", map[string]*fakeBehavior{"K1": k1, "K2": k2}, "K1", "K2")
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"ds": ds}},
		WithSecrets(mapSecrets{"K1": one, "K2": two}))
	var attempts []Attempt
	_, _, err := h.r.StreamChain(context.Background(), chain("ds/m"), core.ChatRequest{}, CallMeta{}, func(a Attempt) { attempts = append(attempts, a) })
	if err == nil || strings.Contains(err.Error(), one) || strings.Contains(err.Error(), two) {
		t.Fatalf("err = %v", err)
	}
	for _, rec := range h.rec.all() {
		if strings.Contains(rec.Error, one) || strings.Contains(rec.Error, two) || !strings.Contains(rec.Error, "***") {
			t.Fatalf("record error = %q", rec.Error)
		}
	}
	for _, a := range attempts {
		if strings.Contains(a.Err.Message, one) || strings.Contains(a.Err.Message, two) {
			t.Fatalf("attempt error = %q", a.Err.Message)
		}
	}
}

// A provider with one key keeps no key in its records; a quota error from it
// moves the chain like auth.
func TestSingleKeyQuotaMovesOn(t *testing.T) {
	a := &fakeBehavior{always: pe("client", 400, `{"error":{"message":"Your credit balance is too low"}}`)}
	cfg := fakeProvider(t, "a", a)
	cfg.APIKeyEnv = "A_KEY"
	b := &fakeBehavior{}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": cfg, "b": fakeProvider(t, "b", b)}},
		WithSecrets(mapSecrets{"A_KEY": "sk-a"}))
	stream, target, err := h.r.StreamChain(context.Background(), chain("a/x", "b/y"), core.ChatRequest{}, CallMeta{}, nil)
	if err != nil || target.Provider != "b" {
		t.Fatalf("target %v err %v", target, err)
	}
	_, _ = drain(stream)
	if got := outcomes(h.rec.all()); !reflect.DeepEqual(got, []string{"a::quota", "b::ok"}) {
		t.Fatalf("records = %v", got)
	}
	if a.callCount() != 1 {
		t.Fatalf("a called %d times", a.callCount())
	}
	if _, ok := h.r.Cooling()[Target{Provider: "a", Model: "x"}]; !ok {
		t.Fatal("a/x not cooling")
	}
}

func TestKeyEnvsAndQuotaPhrases(t *testing.T) {
	if got := KeyEnvs(core.ProviderConfig{Kind: "deepseek", APIKeyEnvs: []string{"K2", "DS_API_KEY", "", "K2"}}, "ds"); !reflect.DeepEqual(got, []string{"DS_API_KEY", "K2"}) {
		t.Fatalf("KeyEnvs = %v", got)
	}
	if got := KeyEnvs(core.ProviderConfig{Kind: "openai_compat", APIKeyEnvs: []string{"K2"}}, "x"); !reflect.DeepEqual(got, []string{"K2"}) {
		t.Fatalf("KeyEnvs compat = %v", got)
	}
	if got := KeyEnvs(core.ProviderConfig{Kind: "openai_compat"}, "x"); len(got) != 0 {
		t.Fatalf("KeyEnvs none = %v", got)
	}
	for _, tc := range []struct {
		status int
		msg    string
		want   bool
	}{
		{402, "", true},
		{400, "Insufficient Balance", true},
		{429, `{"error":{"code":"insufficient_quota"}}`, true},
		{403, "You exceeded your current quota", true},
		{400, "Quota exceeded for model", true},
		{400, "credit balance is too low", true},
		{429, "slow down", false},
		{500, "insufficient balance", false},
		{401, "insufficient balance", false},
	} {
		if got := IsQuotaError(tc.status, tc.msg); got != tc.want {
			t.Errorf("IsQuotaError(%d, %q) = %v", tc.status, tc.msg, got)
		}
	}
}

// missingSecrets reports each empty key of a pool.
func TestMissingSecretsKeyPool(t *testing.T) {
	config := core.Config{
		Providers: map[string]core.ProviderConfig{"ds": {Kind: "deepseek", APIKeyEnv: "K1", APIKeyEnvs: []string{"K2", "K3"}}},
		Agents:    map[string]core.AgentConfig{"a": {Model: "ds/deepseek-flash"}},
	}
	got := missingSecrets(config, mapSecrets{"K2": "v"})
	want := []missingSecret{{Provider: "ds", Env: "K1"}, {Provider: "ds", Env: "K3"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("missingSecrets = %v, want %v", got, want)
	}
}

func TestPoolAccounts(t *testing.T) {
	prio := func(n int) *int { return &n }
	off := false
	r, err := New(Config{Providers: map[string]core.ProviderConfig{
		"antigravity": {Kind: core.AntigravityKind, Priority: prio(50)},
		"agy-2":       {Kind: core.AntigravityKind},
		"agy-3":       {Kind: core.AntigravityKind, Priority: prio(10)},
		"agy-0":       {Kind: core.AntigravityKind, Priority: prio(10)},
		"agy-off":     {Kind: core.AntigravityKind, Pool: &off},
		"agy-dis":     {Kind: core.AntigravityKind, Disabled: true},
		"cc-b":        {Kind: core.ClaudeCodeKind},
		"cc-a":        {Kind: core.ClaudeCodeKind},
		"cc-first":    {Kind: core.ClaudeCodeKind, Priority: prio(0)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.PoolAccounts(core.AntigravityKind), []string{"agy-0", "agy-3", "antigravity", "agy-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("antigravity pool = %v, want %v", got, want)
	}
	// The built-in account defaults to 0 and ties sort by name.
	if got, want := r.PoolAccounts(core.ClaudeCodeKind), []string{"cc-first", "claude-code", "cc-a", "cc-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("claude-code pool = %v, want %v", got, want)
	}
	if got := r.PoolAccounts("deepseek"); got != nil {
		t.Fatalf("other runtime = %v", got)
	}
	for name, want := range map[string]int{"antigravity": 50, "agy-2": 100, "claude-code": 0, "cc-first": 0, "agy-off": 100} {
		if got, ok := r.AccountPriority(name); !ok || got != want {
			t.Errorf("AccountPriority(%s) = %d %v, want %d", name, got, ok, want)
		}
	}
	if _, ok := r.AccountPriority("nope"); ok {
		t.Fatal("unknown account has a priority")
	}

	// The built-in tables may leave the built-in accounts out.
	if err := r.Apply(Config{Providers: map[string]core.ProviderConfig{
		"claude-code": {Kind: core.ClaudeCodeKind, Pool: &off},
		"antigravity": {Kind: core.AntigravityKind, Disabled: true},
		"agy-2":       {Kind: core.AntigravityKind},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := r.PoolAccounts(core.ClaudeCodeKind); len(got) != 0 {
		t.Fatalf("claude-code pool = %v", got)
	}
	if got := r.PoolAccounts(core.AntigravityKind); !reflect.DeepEqual(got, []string{"agy-2"}) {
		t.Fatalf("antigravity pool = %v", got)
	}
}
