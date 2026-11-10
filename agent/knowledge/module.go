package knowledge

import (
	"context"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("knowledge", func() core.Module { return &Module{} })
}

// Module installs the knowledge service (REQ-KNW-020–025).
type Module struct {
	service *Service
	cancel  context.CancelFunc
}

// knowledgeSink is the part of the agent registry the module needs. The
// registry is looked up through an interface so this package does not import
// the agent package (internal/archtest).
type knowledgeSink interface {
	SetKnowledge(core.Knowledge)
	AgentConfig(name string) (core.AgentConfig, bool)
}

func (m *Module) ID() string { return "knowledge" }

func (m *Module) Requires() []core.ServiceKey {
	return []core.ServiceKey{core.ServiceSessions, core.ServiceLLM, core.ServiceAgents}
}

func (m *Module) Provides() []core.ServiceKey { return []core.ServiceKey{core.ServiceKnowledge} }

func (m *Module) Install(b *core.Builder, _ core.ModuleConfig) error {
	storeAny, ok := b.Lookup(core.ServiceSessions)
	if !ok {
		return fmt.Errorf("service sessions is not available")
	}
	store, ok := storeAny.(core.SessionStore)
	if !ok {
		return fmt.Errorf("service sessions is not a SessionStore")
	}
	llmAny, ok := b.Lookup(core.ServiceLLM)
	if !ok {
		return fmt.Errorf("service llm is not available")
	}
	llm, ok := llmAny.(core.LLMRegistry)
	if !ok {
		return fmt.Errorf("service llm is not an LLMRegistry")
	}

	base, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.service = NewService(base, b.Home(), b.Config().Knowledge, store, llm)
	m.service.Config = b.Config()
	// REQ-DKN-033: PromoteSoon runs the domain promotion of one agent in the
	// background. Tests may override the hook.
	m.service.promoteDomainFn = func(ctx context.Context, agent string) error {
		_, err := m.service.promoteDomain(ctx, agent)
		return err
	}
	if agentsAny, ok := b.Lookup(core.ServiceAgents); ok {
		if sink, ok := agentsAny.(knowledgeSink); ok {
			sink.SetKnowledge(m.service)
			m.service.LookupAgent = sink.AgentConfig
		}
	}
	// REQ-KNU-003a: journal a session after compaction stores its summary. The
	// observer registers only when the store can read conversation text
	// (design §4).
	if _, ok := storeAny.(core.SessionTextReader); ok {
		if compAny, ok := b.Lookup(core.ServiceCompaction); ok {
			if observer, ok := compAny.(interface{ OnCompacted(func(string, string)) }); ok {
				observer.OnCompacted(m.service.JournalSoon)
			}
		}
	}
	m.service.start()
	return b.Provide(core.ServiceKnowledge, m.service)
}

// Stop cancels the base context so in-flight passes end, then closes the index
// (design §5).
func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	if m.service != nil {
		m.service.drain()
		return m.service.Close()
	}
	return nil
}
