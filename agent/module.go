package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
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
			// REQ-TB-004, REQ-TB-008: a command of a package that is not
			// installed is not registered yet. A missing tool must never stop
			// lana; the turn simply does not offer it until it is registered.
			if _, ok := toolReg.Get(toolName); !ok {
				slog.Warn("agent uses a tool which is not registered", "agent", name, "tool", toolName)
			}
		}
		// API-CCT-002: a legacy runtime agent is its claude-code address.
		agentCfg = llmcall.NormalizeAgent(agentCfg)
		if agentCfg.Provider != "" {
			if _, ok := config.Providers[agentCfg.Provider]; !ok {
				return fmt.Errorf("agent '%s' uses provider '%s' which is not registered", name, agentCfg.Provider)
			}
		}
		for _, fallback := range agentCfg.Fallback {
			provider := fallback
			if target, ok := llmcall.ParseTarget(fallback); ok {
				provider = target.Provider
			}
			// ADR 0020, REQ-CCT-010: a Claude Code account has no router
			// provider.
			if llmcall.IsClaudeCodeProvider(config.Providers, provider) {
				continue
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
	if svc, ok := b.Lookup(core.ServiceMCP); ok {
		if m, ok := svc.(core.MCPManager); ok {
			registry.SetMCP(m)
		}
	}
	m.registry = registry
	return b.Provide(core.ServiceAgents, registry)
}

func (m *Module) Stop(ctx context.Context) error {
	if m.registry == nil {
		return nil
	}
	return m.registry.Close(ctx)
}
