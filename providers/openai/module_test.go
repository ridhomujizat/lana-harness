package openai

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

type emptySecrets struct{}

func (emptySecrets) Lookup(string) (core.Secret, bool) { return core.Secret{}, false }

func bootLLM(t *testing.T, config core.Config, secrets core.Secrets) (*core.Context, error) {
	t.Helper()
	config.Modules = []core.ModuleRow{{ID: "llm", Module: "llm", Config: map[string]any{}}}
	return core.Boot(context.Background(), core.BootInput{Config: config, Secrets: secrets, Profile: "test"})
}

// A kind no adapter registered is rejected at boot. (The anthropic kind is
// registered by providers/anthropic, which this test binary does not link.)
func TestUnregisteredKindRejected(t *testing.T) {
	cfg := core.Config{
		Providers: map[string]core.ProviderConfig{"other": {Kind: "unregistered_kind", BaseURL: "https://example.com"}},
		Agents:    map[string]core.AgentConfig{"a": {Provider: "other", Model: "m"}},
	}
	_, err := bootLLM(t, cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "unregistered_kind") || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("err=%v", err)
	}
}

// A provider in use with an empty key no longer fails boot (spec 37 failure
// table): the router reports missing_secret for its target instead.
func TestMissingSecretAtBoot(t *testing.T) {
	cfg := core.Config{
		Providers: map[string]core.ProviderConfig{"openai": {Kind: "openai_compat", BaseURL: "https://example.com", APIKeyEnv: "OPENAI_API_KEY"}},
		Agents:    map[string]core.AgentConfig{"a": {Provider: "openai", Model: "m"}},
	}
	ctx, err := bootLLM(t, cfg, emptySecrets{})
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	llm, ok := ctx.LLM.(core.LLMRouter)
	if !ok {
		t.Fatalf("LLM is %T", ctx.LLM)
	}
	_, _, err = llm.StreamChain(context.Background(), []core.LLMTarget{{Provider: "openai", Model: "m"}}, core.ChatRequest{}, core.LLMCallMeta{Source: "test"}, nil)
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "missing_secret" {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(pe.Message, "OPENAI_API_KEY") || !strings.Contains(pe.Message, "openai") {
		t.Fatalf("message=%q", pe.Message)
	}
}

func TestLLMRegistryInstalled(t *testing.T) {
	cfg := core.Config{
		Providers: map[string]core.ProviderConfig{"openai": {Kind: "openai_compat", BaseURL: "https://example.com"}},
		Agents:    map[string]core.AgentConfig{"a": {Provider: "openai", Model: "m"}},
	}
	ctx, err := bootLLM(t, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.LLM == nil || len(ctx.LLM.Names()) != 1 || ctx.LLM.Names()[0] != "openai" {
		t.Fatalf("registry=%v", ctx.LLM)
	}
}
