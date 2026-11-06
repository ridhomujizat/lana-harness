package compaction

import (
	"context"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("compaction", func() core.Module { return &Module{} })
}

// Module installs the compaction service and listeners.
type Module struct {
	service *Service
}

// compactorSink is the part of the agent registry the module needs. The
// registry is looked up through an interface so this package does not import
// the agent package (internal/archtest).
type compactorSink interface {
	SetCompactor(core.Compactor)
	AgentConfig(name string) (core.AgentConfig, bool)
}

func (m *Module) ID() string { return "compaction" }
func (m *Module) Requires() []core.ServiceKey {
	return []core.ServiceKey{core.ServiceSessions, core.ServiceLLM, core.ServiceAgents}
}
func (m *Module) Provides() []core.ServiceKey { return []core.ServiceKey{core.ServiceCompaction} }
func (m *Module) Install(b *core.Builder, cfg core.ModuleConfig) error {
	storeAny, ok := b.Lookup(core.ServiceSessions)
	if !ok {
		return fmt.Errorf("service sessions is not available")
	}
	llmAny, ok := b.Lookup(core.ServiceLLM)
	if !ok {
		return fmt.Errorf("service llm is not available")
	}
	store, ok := storeAny.(core.SessionStore)
	if !ok {
		return fmt.Errorf("service sessions is not a SessionStore")
	}
	llm, ok := llmAny.(core.LLMRegistry)
	if !ok {
		return fmt.Errorf("service llm is not an LLMRegistry")
	}
	m.service = NewService(store, llm, b.Config())
	m.service.Install(b.Hooks())
	if agentsAny, ok := b.Lookup(core.ServiceAgents); ok {
		if sink, ok := agentsAny.(compactorSink); ok {
			sink.SetCompactor(m.service)
			m.service.LookupAgent = sink.AgentConfig
		}
	}
	return b.Provide(core.ServiceCompaction, m.service)
}
func (m *Module) Stop(context.Context) error { return nil }
