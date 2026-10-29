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

func TestAnthropicKindRejected(t *testing.T) {
	cfg := core.Config{
		Providers: map[string]core.ProviderConfig{"anthropic": {Kind: "anthropic", BaseURL: "https://example.com"}},
		Agents:    map[string]core.AgentConfig{"a": {Provider: "anthropic", Model: "m"}},
	}
	_, err := bootLLM(t, cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "anthropic") || !strings.Contains(err.Error(), "belum didukung") {
		t.Fatalf("err=%v", err)
	}
}

func TestMissingSecretAtBoot(t *testing.T) {
	cfg := core.Config{
		Providers: map[string]core.ProviderConfig{"openai": {Kind: "openai_compat", BaseURL: "https://example.com", APIKeyEnv: "OPENAI_API_KEY"}},
		Agents:    map[string]core.AgentConfig{"a": {Provider: "openai", Model: "m"}},
	}
	_, err := bootLLM(t, cfg, emptySecrets{})
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "missing_secret" {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(pe.Message, "OPENAI_API_KEY") || !strings.Contains(pe.Message, "openai") {
		t.Fatalf("pesan=%q", pe.Message)
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
