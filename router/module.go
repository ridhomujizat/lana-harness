package router

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("llm", func() core.Module { return &Module{} })
}

// Module is the "llm" module: it builds the router from core.Config and
// provides it as core.ServiceLLM (both core.LLMRegistry and core.LLMRouter).
//
// Module values:
//   - "_providers" map[string]core.Provider: custom providers (tests and
//     embedders), winning over config entries of the same name;
//   - "_recorder" Recorder: the call recorder;
//   - "_limit_store" LimitStore: the limit snapshot store.
type Module struct {
	router *Router
}

func (m *Module) ID() string                  { return "llm" }
func (m *Module) Requires() []core.ServiceKey { return nil }
func (m *Module) Provides() []core.ServiceKey { return []core.ServiceKey{core.ServiceLLM} }

func (m *Module) Stop(context.Context) error {
	if m.router == nil {
		return nil
	}
	return m.router.Close()
}

func (m *Module) Install(b *core.Builder, cfg core.ModuleConfig) error {
	config := b.Config()
	rcfg, err := ConfigFromCore(config)
	if err != nil {
		return err
	}
	opts := []Option{WithSecrets(b.Secrets())}
	if custom, ok := cfg.Values["_providers"].(map[string]core.Provider); ok && len(custom) > 0 {
		opts = append(opts, WithProviders(custom))
	}
	if rec, ok := cfg.Values["_recorder"].(Recorder); ok && rec != nil {
		opts = append(opts, WithRecorder(rec))
	}
	if store, ok := cfg.Values["_limit_store"].(LimitStore); ok && store != nil {
		opts = append(opts, WithLimitStore(store))
	}
	r, err := New(rcfg, opts...)
	if err != nil {
		return err
	}
	if err := checkSecrets(config, b.Secrets()); err != nil {
		_ = r.Close()
		return err
	}
	m.router = r
	return b.Provide(core.ServiceLLM, r)
}

// ConfigFromCore builds a router Config from lana's config: [providers],
// [combos], and [router] cooldown ("" means the default, "0s" turns
// cooldown off).
func ConfigFromCore(config core.Config) (Config, error) {
	out := Config{Providers: config.Providers}
	if len(config.Combos) > 0 {
		out.Combos = make(map[string]Combo, len(config.Combos))
		for name, combo := range config.Combos {
			out.Combos[name] = Combo{Description: combo.Description, Models: combo.Models}
		}
	}
	if raw := strings.TrimSpace(config.Router.Cooldown); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			return Config{}, fmt.Errorf("router.cooldown %q is not a valid duration", config.Router.Cooldown)
		}
		if d == 0 {
			d = -1
		}
		out.Cooldown = d
	}
	return out, nil
}

// checkSecrets fails boot when a provider in use has an empty API key env.
func checkSecrets(config core.Config, secrets core.Secrets) error {
	used := usedProviders(config)
	for _, name := range sortedKeys(config.Providers) {
		if !used[name] {
			continue
		}
		provider := config.Providers[name]
		if provider.APIKeyEnv == "" {
			continue
		}
		secret, ok := core.NewSecret(""), false
		if secrets != nil {
			secret, ok = secrets.Lookup(provider.APIKeyEnv)
		}
		if !ok || secret.Reveal() == "" {
			return &core.ProviderError{
				Class:   "missing_secret",
				Message: fmt.Sprintf("env %s for provider %s is empty", provider.APIKeyEnv, name),
			}
		}
	}
	return nil
}

// usedProviders returns the providers an agent, a fallback, a combo used by
// an agent, the embedding provider, or the knowledge agent uses.
func usedProviders(config core.Config) map[string]bool {
	used := map[string]bool{}
	markAddress := func(value string) {
		if target, ok := ParseAddress(value); ok {
			used[target.Provider] = true
		} else if value != "" {
			used[value] = true
		}
	}
	markModel := func(agent core.AgentConfig) {
		if agent.Provider != "" {
			used[agent.Provider] = true
			return
		}
		if combo, ok := config.Combos[agent.Model]; ok {
			for _, model := range combo.Models {
				markAddress(model)
			}
			return
		}
		if target, ok := ParseAddress(agent.Model); ok {
			used[target.Provider] = true
		}
	}
	for _, agent := range config.Agents {
		markModel(agent)
		for _, fallback := range agent.Fallback {
			markAddress(fallback)
		}
	}
	// The embedding provider needs its key checked at boot too (API-KNW-004).
	if config.Knowledge.EmbeddingProvider != "" {
		used[config.Knowledge.EmbeddingProvider] = true
	}
	// The knowledge upkeep agent runs with its own provider; mark it
	// explicitly so its key is checked even if the agent list above ever
	// changes (API-KNU-001).
	if agent, ok := config.Agents[config.Knowledge.Agent]; ok {
		markModel(agent)
	}
	return used
}
