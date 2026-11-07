package router

import (
	"context"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// A host whose store opens after the router attaches the recorder and the
// limit store later: records reach the new recorder, stored snapshots load
// under the ones already reported, and Close saves them.
func TestAttachStores(t *testing.T) {
	ok := &fakeBehavior{}
	r, err := New(Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", ok)}})
	if err != nil {
		t.Fatal(err)
	}
	r.UpdateLimits(LimitUpdate{Provider: "a", Rate: &RateLimits{Tokens: &RateLimit{Limit: 10, Remaining: 9}}})

	rec := &recorder{}
	store := &memLimitStore{loaded: []LimitSnapshot{
		{Provider: "a", Kind: "fake"},
		{Provider: "claude-agent", Kind: "claude-code"},
	}}
	r.AttachStores(rec, store)

	stream, _, err := r.StreamChain(context.Background(), chain("a/x"), core.ChatRequest{}, CallMeta{Source: "turn", Requested: "coding"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drain(stream); err != nil {
		t.Fatal(err)
	}
	records := rec.all()
	if len(records) != 1 || records[0].Requested != "coding" || records[0].Outcome != "ok" {
		t.Fatalf("records = %+v", records)
	}

	got := r.Limits(context.Background(), false)
	if len(got) != 2 || got[0].Provider != "a" || got[0].Rate == nil || got[0].Rate.Tokens.Remaining != 9 || got[1].Provider != "claude-agent" {
		t.Fatalf("limits = %+v", got)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) == 0 || len(store.saved[len(store.saved)-1]) != 2 {
		t.Fatalf("saved = %+v", store.saved)
	}
}

// StreamChain records meta.Requested when set, else the first address.
func TestStreamChainRequested(t *testing.T) {
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", &fakeBehavior{})}})
	for _, tc := range []struct{ requested, want string }{{"", "a/x"}, {"duo", "duo"}} {
		stream, _, err := h.r.StreamChain(context.Background(), chain("a/x"), core.ChatRequest{}, CallMeta{Source: "test", Requested: tc.requested}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := drain(stream); err != nil {
			t.Fatal(err)
		}
		records := h.rec.all()
		if got := records[len(records)-1].Requested; got != tc.want {
			t.Fatalf("requested %q recorded %q, want %q", tc.requested, got, tc.want)
		}
	}
}

// API-RTR-001: the preset kinds default api_key_env to <NAME>_API_KEY;
// openai_compat and custom providers have none unless set.
func TestEffectiveKeyEnv(t *testing.T) {
	cases := []struct {
		cfg  core.ProviderConfig
		name string
		want string
	}{
		{core.ProviderConfig{Kind: "deepseek"}, "ds", "DS_API_KEY"},
		{core.ProviderConfig{Kind: "anthropic"}, "my-claude", "MY_CLAUDE_API_KEY"},
		{core.ProviderConfig{Kind: "openrouter", APIKeyEnv: "OR_KEY"}, "openrouter", "OR_KEY"},
		{core.ProviderConfig{Kind: "openai_compat"}, "local", ""},
		{core.ProviderConfig{Kind: "openai_compat", APIKeyEnv: "LOCAL_KEY"}, "local", "LOCAL_KEY"},
		{core.ProviderConfig{}, "custom", ""},
	}
	for _, tc := range cases {
		if got := EffectiveKeyEnv(tc.cfg, tc.name); got != tc.want {
			t.Fatalf("%s %+v: %q, want %q", tc.name, tc.cfg, got, tc.want)
		}
	}
}

// The boot key check uses the default env of a preset kind.
func TestModuleMissingDefaultKeyEnv(t *testing.T) {
	err := checkSecrets(core.Config{
		Providers: map[string]core.ProviderConfig{"ds": {Kind: "deepseek"}},
		Agents:    map[string]core.AgentConfig{"a": {Model: "ds/deepseek-flash"}},
	}, mapSecrets{})
	if err == nil || !strings.Contains(err.Error(), "DS_API_KEY") {
		t.Fatalf("err = %v", err)
	}
	if err := checkSecrets(core.Config{
		Providers: map[string]core.ProviderConfig{"ds": {Kind: "deepseek"}},
		Agents:    map[string]core.AgentConfig{"a": {Model: "ds/deepseek-flash"}},
	}, mapSecrets{"DS_API_KEY": "k"}); err != nil {
		t.Fatalf("err = %v", err)
	}
}
