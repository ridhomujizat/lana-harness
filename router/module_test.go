package router

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

func bootLLM(t *testing.T, config core.Config, secrets core.Secrets, values map[string]any) (*core.Context, error) {
	t.Helper()
	if values == nil {
		values = map[string]any{}
	}
	config.Modules = []core.ModuleRow{{ID: "llm", Module: "llm", Config: values}}
	ctx, err := core.Boot(context.Background(), core.BootInput{Config: config, Secrets: secrets, Profile: "test"})
	if err == nil {
		t.Cleanup(func() { _ = ctx.Shutdown(context.Background()) })
	}
	return ctx, err
}

// The llm module provides the router as both LLMRegistry and LLMRouter, with
// combos, custom providers, and the recorder from module values.
func TestModuleProvidesRouter(t *testing.T) {
	rec := &recorder{}
	custom := providerFunc(func(context.Context, core.ChatRequest) (core.ChatStream, error) {
		return func(yield func(core.ChatEvent, error) bool) {
			yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: "custom"}, nil)
		}, nil
	})
	cfg := core.Config{
		Providers: map[string]core.ProviderConfig{
			"a":      fakeProvider(t, "a", &fakeBehavior{}),
			"mocked": {Kind: "openai_compat", BaseURL: "mock://local"},
		},
		Combos: map[string]core.ComboConfig{"coding": {Models: []string{"mocked/m", "a/x"}}},
		Agents: map[string]core.AgentConfig{"lana": {Model: "coding"}},
	}
	ctx, err := bootLLM(t, cfg, nil, map[string]any{
		"_providers": map[string]core.Provider{"mocked": custom},
		"_recorder":  Recorder(rec),
	})
	if err != nil {
		t.Fatal(err)
	}
	llmRouter, ok := ctx.LLM.(core.LLMRouter)
	if !ok {
		t.Fatalf("ctx.LLM is %T, not a core.LLMRouter", ctx.LLM)
	}
	if names := ctx.LLM.Names(); len(names) != 2 || names[0] != "a" || names[1] != "mocked" {
		t.Fatalf("names = %v", names)
	}
	if p, _ := ctx.LLM.Provider("mocked"); p == nil {
		t.Fatal("custom provider missing")
	}
	chain, err := llmRouter.Resolve("coding")
	if err != nil || len(chain) != 2 {
		t.Fatalf("chain=%v err=%v", chain, err)
	}
	stream, served, err := llmRouter.StreamChain(context.Background(), chain, core.ChatRequest{}, core.LLMCallMeta{Source: "turn"}, nil)
	if err != nil || served.Provider != "mocked" {
		t.Fatalf("served=%v err=%v", served, err)
	}
	if text, _ := drain(stream); text != "custom" {
		t.Fatalf("text = %q", text)
	}
	if recs := rec.all(); len(recs) != 1 || recs[0].Source != "turn" {
		t.Fatalf("records = %+v", recs)
	}
}

func TestModuleRejectsUnknownKind(t *testing.T) {
	cfg := core.Config{
		Providers: map[string]core.ProviderConfig{"anthropic": {Kind: "nope", BaseURL: "https://example.com"}},
		Agents:    map[string]core.AgentConfig{"a": {Provider: "anthropic", Model: "m"}},
	}
	_, err := bootLLM(t, cfg, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "kind 'nope' is not supported") {
		t.Fatalf("err = %v", err)
	}
}

// Boot warns, and does not fail, when a provider used through a combo has an
// empty key; the target then fails with missing_secret and the chain moves on
// (spec 37 failure table).
func TestModuleMissingSecretThroughCombo(t *testing.T) {
	b := fakeProvider(t, "b", &fakeBehavior{})
	b.APIKeyEnv = "B_API_KEY"
	unused := fakeProvider(t, "u", &fakeBehavior{})
	unused.APIKeyEnv = "U_API_KEY"
	cfg := core.Config{
		Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", &fakeBehavior{}), "b": b, "u": unused},
		Combos:    map[string]core.ComboConfig{"coding": {Models: []string{"a/x", "b/y"}}, "spare": {Models: []string{"u/z"}}},
		Agents:    map[string]core.AgentConfig{"lana": {Model: "coding"}},
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	if _, err := bootLLM(t, cfg, mapSecrets{}, nil); err != nil {
		t.Fatalf("boot with a missing key failed: %v", err)
	}
	line := logs.String()
	if !strings.Contains(line, "level=WARN") || !strings.Contains(line, "provider=b") || !strings.Contains(line, "env=B_API_KEY") {
		t.Fatalf("missing-key warning = %q", line)
	}
	// The unused combo's provider u is not in use, so it is not reported.
	if strings.Contains(line, "U_API_KEY") {
		t.Fatalf("unused provider reported: %q", line)
	}
	got := missingSecrets(cfg, mapSecrets{})
	if len(got) != 1 || got[0].Provider != "b" || got[0].Env != "B_API_KEY" {
		t.Fatalf("missingSecrets = %+v", got)
	}
	if got := missingSecrets(cfg, mapSecrets{"B_API_KEY": "k"}); len(got) != 0 {
		t.Fatalf("missingSecrets with b's key = %+v", got)
	}
}

func TestUsedProviders(t *testing.T) {
	cfg := core.Config{
		Combos: map[string]core.ComboConfig{"coding": {Models: []string{"c1/x", "c2/y"}}},
		Agents: map[string]core.AgentConfig{
			"legacy":  {Provider: "p", Model: "openai/gpt-5", Fallback: []string{"f1:m", "f2/m", "bare"}},
			"address": {Model: "addr/m"},
			"combo":   {Model: "coding"},
		},
		Knowledge: core.KnowledgeConfig{EmbeddingProvider: "emb"},
	}
	used := usedProviders(cfg)
	for _, name := range []string{"p", "f1", "f2", "bare", "addr", "c1", "c2", "emb"} {
		if !used[name] {
			t.Fatalf("%s not marked used: %v", name, used)
		}
	}
	if used["openai"] {
		t.Fatal("legacy model id marked as provider")
	}
}

func TestConfigFromCore(t *testing.T) {
	tests := []struct {
		cooldown string
		want     time.Duration
		err      bool
	}{
		{"", 0, false},
		{"0s", -1, false},
		{"90s", 90 * time.Second, false},
		{"soon", 0, true},
		{"-1m", 0, true},
	}
	for _, tc := range tests {
		got, err := ConfigFromCore(core.Config{Router: core.RouterConfig{Cooldown: tc.cooldown}})
		if (err != nil) != tc.err || got.Cooldown != tc.want {
			t.Fatalf("cooldown %q = %v, %v", tc.cooldown, got.Cooldown, err)
		}
	}
	got, _ := ConfigFromCore(core.Config{Combos: map[string]core.ComboConfig{"c": {Description: "d", Models: []string{"a/x"}}}})
	if got.Combos["c"].Description != "d" || got.Combos["c"].Models[0] != "a/x" {
		t.Fatalf("combos = %+v", got.Combos)
	}
}
