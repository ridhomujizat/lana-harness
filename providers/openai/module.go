package openai

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("llm", func() core.Module { return &Module{} })
}

// Module memasang LLMRegistry dari config providers.
type Module struct{}

func (m *Module) ID() string                  { return "llm" }
func (m *Module) Requires() []core.ServiceKey { return nil }
func (m *Module) Provides() []core.ServiceKey { return []core.ServiceKey{core.ServiceLLM} }
func (m *Module) Stop(context.Context) error  { return nil }

func (m *Module) Install(b *core.Builder, cfg core.ModuleConfig) error {
	config := b.Config()
	used := usedProviders(config)
	registry := &llmRegistry{providers: map[string]core.Provider{}}
	if raw, ok := cfg.Values["_providers"]; ok {
		if custom, ok := raw.(map[string]core.Provider); ok {
			for name, provider := range custom {
				registry.providers[name] = provider
			}
		}
	}
	for _, name := range sortedProviderNames(config.Providers) {
		if _, exists := registry.providers[name]; exists {
			continue
		}
		provider := config.Providers[name]
		if provider.Kind != "openai_compat" {
			return fmt.Errorf("provider '%s' kind '%s' belum didukung pada build ini", name, provider.Kind)
		}
		adapter, err := NewAdapter(name, Config{
			BaseURL:   provider.BaseURL,
			APIKeyEnv: provider.APIKeyEnv,
			Headers:   provider.Headers,
			Timeout:   provider.Timeout,
		}, b.Secrets())
		if err != nil {
			return err
		}
		registry.providers[name] = adapter
	}
	for _, name := range sortedProviderNames(config.Providers) {
		if !used[name] {
			continue
		}
		provider := config.Providers[name]
		if provider.APIKeyEnv == "" {
			continue
		}
		secret, ok := core.NewSecret(""), false
		if b.Secrets() != nil {
			secret, ok = b.Secrets().Lookup(provider.APIKeyEnv)
		}
		if !ok || secret.Reveal() == "" {
			return &core.ProviderError{
				Class:   "missing_secret",
				Message: fmt.Sprintf("env %s untuk provider %s kosong", provider.APIKeyEnv, name),
			}
		}
	}
	return b.Provide(core.ServiceLLM, registry)
}

func usedProviders(config core.Config) map[string]bool {
	used := map[string]bool{}
	for _, agent := range config.Agents {
		if agent.Provider != "" {
			used[agent.Provider] = true
		}
		for _, fallback := range agent.Fallback {
			provider, _, ok := strings.Cut(fallback, ":")
			if ok {
				used[provider] = true
			} else {
				used[fallback] = true
			}
		}
	}
	return used
}

func sortedProviderNames(m map[string]core.ProviderConfig) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type llmRegistry struct {
	providers map[string]core.Provider
}

func (r *llmRegistry) Provider(name string) (core.Provider, bool) {
	provider, ok := r.providers[name]
	return provider, ok
}

func (r *llmRegistry) Names() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var _ core.LLMRegistry = (*llmRegistry)(nil)
