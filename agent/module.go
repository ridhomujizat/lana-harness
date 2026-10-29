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

// Module installs the AgentRegistry.
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
		return fmt.Errorf("service llm is not available")
	}
	llm, ok := llmAny.(core.LLMRegistry)
	if !ok {
		return fmt.Errorf("service llm is not an LLMRegistry")
	}
	toolsAny, ok := b.Lookup(core.ServiceTools)
	if !ok {
		return fmt.Errorf("service tools is not available")
	}
	toolReg, ok := toolsAny.(core.ToolRegistry)
	if !ok {
		return fmt.Errorf("service tools is not a ToolRegistry")
	}
	storeAny, ok := b.Lookup(core.ServiceSessions)
	if !ok {
		return fmt.Errorf("service sessions is not available")
	}
	store, ok := storeAny.(core.SessionStore)
	if !ok {
		return fmt.Errorf("service sessions is not a SessionStore")
	}
	config := b.Config()
	for name, agentCfg := range config.Agents {
		for _, toolName := range agentCfg.Tools {
			if _, ok := toolReg.Get(toolName); !ok {
				return fmt.Errorf("agent '%s' uses tool '%s' which is not registered", name, toolName)
			}
		}
		if agentCfg.Provider != "" {
			if _, ok := config.Providers[agentCfg.Provider]; !ok {
				return fmt.Errorf("agent '%s' uses provider '%s' which is not registered", name, agentCfg.Provider)
			}
		}
		for _, fallback := range agentCfg.Fallback {
			provider := fallback
			if idx := stringsIndexByte(fallback, ':'); idx >= 0 {
				provider = fallback[:idx]
			}
			if _, ok := config.Providers[provider]; !ok {
				return fmt.Errorf("agent '%s' uses fallback provider '%s' which is not registered", name, provider)
			}
			if _, ok := llm.Provider(provider); !ok {
				return fmt.Errorf("agent '%s' uses fallback provider '%s' which is unknown", name, provider)
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
