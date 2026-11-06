package mcp

import (
	"context"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("mcp", func() core.Module { return &Module{} })
}

// Module installs the MCP manager.
type Module struct {
	manager *Manager
	cancel  context.CancelFunc
}

func (m *Module) ID() string                  { return "mcp" }
func (m *Module) Requires() []core.ServiceKey { return []core.ServiceKey{core.ServiceTools} }
func (m *Module) Provides() []core.ServiceKey { return []core.ServiceKey{core.ServiceMCP} }

func (m *Module) Install(b *core.Builder, _ core.ModuleConfig) error {
	raw, ok := b.Lookup(core.ServiceTools)
	if !ok {
		return fmt.Errorf("mcp: tools service is not available")
	}
	registry, ok := raw.(core.ToolRegistry)
	if !ok {
		return fmt.Errorf("mcp: tools service is not a core.ToolRegistry")
	}

	manager := NewManager(b.Config().MCP, b.Home(), registry)
	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx)

	m.manager = manager
	m.cancel = cancel
	if err := b.Provide(core.ServiceMCP, manager); err != nil {
		cancel()
		_ = manager.Close()
		return err
	}
	return nil
}

func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.manager != nil {
		return m.manager.Close()
	}
	return nil
}

var _ core.Module = (*Module)(nil)
