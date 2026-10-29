// Package compaction menyediakan modul compaction (spec #4).
package compaction

import (
	"context"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("compaction", func() core.Module { return &Module{} })
}

// Module adalah modul compaction.
type Module struct{}

func (m *Module) ID() string                                     { return "compaction" }
func (m *Module) Requires() []core.ServiceKey                    { return nil }
func (m *Module) Provides() []core.ServiceKey                    { return nil }
func (m *Module) Install(*core.Builder, core.ModuleConfig) error { return nil }
func (m *Module) Stop(context.Context) error                     { return nil }
