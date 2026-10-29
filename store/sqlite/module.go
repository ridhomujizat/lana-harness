// Package sqlite akan menyediakan store SQLite pada spec #4.
package sqlite

import (
	"context"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("sqlite_store", func() core.Module { return &Module{} })
}

// Module adalah modul sqlite_store.
type Module struct{}

func (m *Module) ID() string                  { return "sqlite_store" }
func (m *Module) Requires() []core.ServiceKey { return nil }
func (m *Module) Provides() []core.ServiceKey { return []core.ServiceKey{core.ServiceSessions} }
func (m *Module) Install(*core.Builder, core.ModuleConfig) error {
	return fmt.Errorf("sqlite_store belum tersedia pada milestone ini")
}
func (m *Module) Stop(context.Context) error { return nil }
