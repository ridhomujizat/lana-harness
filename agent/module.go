package agent

import (
	"context"
	"fmt"
	"os"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("agent", func() core.Module { return &Module{} })
}

// Module memasang AgentRegistry.
type Module struct {
	registry *Registry
}

func (m *Module) ID() string { return "agent" }
func (m *Module) Requires() []core.ServiceKey {
	return []core.ServiceKey{core.ServiceLLM, core.ServiceTools, core.ServiceSessions}
}
func (m *Module) Provides() []core.ServiceKey { return []core.ServiceKey{core.ServiceAgents} }

func (m *Module) Install(b *core.Builder, cfg core.ModuleConfig) error {
	llmAny, ok := b.Lookup(core.ServiceLLM)
	if !ok {
		return fmt.Errorf("service llm tidak tersedia")
	}
	llm, ok := llmAny.(core.LLMRegistry)
	if !ok {
		return fmt.Errorf("service llm bukan LLMRegistry")
	}
	toolsAny, ok := b.Lookup(core.ServiceTools)
	if !ok {
		return fmt.Errorf("service tools tidak tersedia")
	}
	toolReg, ok := toolsAny.(core.ToolRegistry)
	if !ok {
		return fmt.Errorf("service tools bukan ToolRegistry")
	}
	storeAny, ok := b.Lookup(core.ServiceSessions)
	if !ok {
		return fmt.Errorf("service sessions tidak tersedia")
	}
	store, ok := storeAny.(core.SessionStore)
	if !ok {
		return fmt.Errorf("service sessions bukan SessionStore")
	}
	config := b.Config()
	for name, agentCfg := range config.Agents {
		for _, toolName := range agentCfg.Tools {
			if _, ok := toolReg.Get(toolName); !ok {
				return fmt.Errorf("agent '%s' memakai tool '%s' yang tidak terdaftar", name, toolName)
			}
		}
		if agentCfg.Provider != "" {
			if _, ok := config.Providers[agentCfg.Provider]; !ok {
				return fmt.Errorf("agent '%s' memakai provider '%s' yang tidak terdaftar", name, agentCfg.Provider)
			}
		}
		for _, fallback := range agentCfg.Fallback {
			provider := fallback
			if idx := stringsIndexByte(fallback, ':'); idx >= 0 {
				provider = fallback[:idx]
			}
			if _, ok := config.Providers[provider]; !ok {
				return fmt.Errorf("agent '%s' memakai provider fallback '%s' yang tidak terdaftar", name, provider)
			}
			if _, ok := llm.Provider(provider); !ok {
				return fmt.Errorf("agent '%s' memakai provider fallback '%s' yang tidak dikenal", name, provider)
			}
		}
	}
	workspace, err := os.Getwd()
	if err != nil {
		return err
	}
	registry := NewRegistry(store, llm, toolReg, b.Hooks(), config, b.Secrets(), b.Home(), workspace)
	retry := NewRetry(store)
	retry.Install(b.Hooks())
	m.registry = registry
	return b.Provide(core.ServiceAgents, registry)
}

func (m *Module) Stop(ctx context.Context) error {
	if m.registry == nil {
		return nil
	}
	return m.registry.Close(ctx)
}

func stringsIndexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
