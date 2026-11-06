package memcapture

import (
	"context"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("memcapture", func() core.Module { return &Module{} })
}

// Module installs the memory-capture service.
type Module struct {
	service *Service
	cancel  context.CancelFunc
}

// capturerSink is the part of the agent registry the module needs. The registry
// is looked up through an interface so this package does not import the agent
// package (internal/archtest).
type capturerSink interface {
	SetMemoryCapturer(core.MemoryCapturer)
	AgentConfig(name string) (core.AgentConfig, bool)
}

func (m *Module) ID() string { return "memcapture" }
func (m *Module) Requires() []core.ServiceKey {
	return []core.ServiceKey{core.ServiceSessions, core.ServiceLLM, core.ServiceAgents}
}

// Provides returns no service key: the capture pass is reached through the
// agent registry sink, not a new service.
func (m *Module) Provides() []core.ServiceKey { return nil }

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
	base, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.service = NewService(base, store, llm)
	if agentsAny, ok := b.Lookup(core.ServiceAgents); ok {
		if sink, ok := agentsAny.(capturerSink); ok {
			sink.SetMemoryCapturer(m.service)
			m.service.LookupAgent = sink.AgentConfig
		}
	}
	return nil
}

// Stop cancels the base context so in-flight passes end (REQ-MEM-015).
func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	return nil
}
