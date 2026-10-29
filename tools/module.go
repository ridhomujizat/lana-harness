package tools

import (
	"context"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("tools", func() core.Module { return &Module{} })
}

// Module installs the ToolRegistry with built-in and custom tools.
type Module struct{}

func (m *Module) ID() string                  { return "tools" }
func (m *Module) Requires() []core.ServiceKey { return nil }
func (m *Module) Provides() []core.ServiceKey { return []core.ServiceKey{core.ServiceTools} }
func (m *Module) Stop(context.Context) error  { return nil }

func (m *Module) Install(b *core.Builder, cfg core.ModuleConfig) error {
	NewPipeline(b.Hooks())
	registry := NewRegistry()
	guard := &Guard{}
	builtins := []core.Tool{
		NewReadFile(guard),
		NewWriteFile(guard),
		NewEditFile(guard),
		NewGlob(guard),
		NewGrep(guard),
		NewBash(guard),
		NewWebFetch(guard),
		NewMemoryRead(),
		NewMemoryWrite(),
	}
	for _, tool := range builtins {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	if raw, ok := cfg.Values["_custom_tools"]; ok {
		custom, ok := raw.([]core.Tool)
		if !ok {
			return fmt.Errorf("_custom_tools is not []core.Tool")
		}
		for _, tool := range custom {
			if err := registry.Register(tool); err != nil {
				return err
			}
		}
	}
	return b.Provide(core.ServiceTools, registry)
}
