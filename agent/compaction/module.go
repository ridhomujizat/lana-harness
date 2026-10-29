package compaction

import (
	"context"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("compaction", func() core.Module { return &Module{} })
}

// Module memasang compaction service dan listener.
type Module struct {
	service *Service
}

func (m *Module) ID() string { return "compaction" }
func (m *Module) Requires() []core.ServiceKey {
	return []core.ServiceKey{core.ServiceSessions, core.ServiceLLM}
}
func (m *Module) Provides() []core.ServiceKey { return []core.ServiceKey{core.ServiceCompaction} }
func (m *Module) Install(b *core.Builder, cfg core.ModuleConfig) error {
	storeAny, ok := b.Lookup(core.ServiceSessions)
	if !ok {
		return fmt.Errorf("service sessions tidak tersedia")
	}
	llmAny, ok := b.Lookup(core.ServiceLLM)
	if !ok {
		return fmt.Errorf("service llm tidak tersedia")
	}
	store, ok := storeAny.(core.SessionStore)
	if !ok {
		return fmt.Errorf("service sessions bukan SessionStore")
	}
	llm, ok := llmAny.(core.LLMRegistry)
	if !ok {
		return fmt.Errorf("service llm bukan LLMRegistry")
	}
	m.service = NewService(store, llm, b.Config())
	m.service.Install(b.Hooks())
	return b.Provide(core.ServiceCompaction, m.service)
}
func (m *Module) Stop(context.Context) error { return nil }
