package store

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("sqlite_store", func() core.Module { return &Module{} })
}

// Module adalah modul sqlite_store.
type Module struct{ store *Store }

func (m *Module) ID() string                  { return "sqlite_store" }
func (m *Module) Requires() []core.ServiceKey { return nil }
func (m *Module) Provides() []core.ServiceKey { return []core.ServiceKey{core.ServiceSessions} }
func (m *Module) Install(b *core.Builder, cfg core.ModuleConfig) error {
	path := "lana.db"
	if raw, ok := cfg.Values["path"]; ok {
		if text, ok := raw.(string); ok && text != "" {
			path = text
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(b.Home(), path)
	}
	store, err := Open(context.Background(), path)
	if err != nil {
		return fmt.Errorf("membuka store %s: %w", path, err)
	}
	store.Hooks = b.Hooks()
	m.store = store
	return b.Provide(core.ServiceSessions, store)
}
func (m *Module) Stop(context.Context) error {
	if m.store == nil {
		return nil
	}
	return m.store.Close()
}
