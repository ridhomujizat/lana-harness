package core

import (
	"context"
	"sort"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// Module is a feature unit compiled into the binary.
type Module interface {
	ID() string
	Requires() []ServiceKey
	Provides() []ServiceKey
	Install(b *Builder, cfg ModuleConfig) error
	Stop(ctx context.Context) error
}

// ModuleConfig is the content of one [[modules]] row after merging.
type ModuleConfig struct {
	ID     string
	Module string
	Values map[string]any
}

// Decode copies Values into the destination struct via TOML.
func (c ModuleConfig) Decode(into any) error {
	b, err := toml.Marshal(c.Values)
	if err != nil {
		return err
	}
	return toml.Unmarshal(b, into)
}

var (
	moduleMu       sync.Mutex
	moduleRegistry = map[string]func() Module{}
)

// RegisterModule registers a module factory. A duplicate name panics.
func RegisterModule(name string, factory func() Module) {
	if name == "" || factory == nil {
		panic("core: RegisterModule requires a name and a factory")
	}
	moduleMu.Lock()
	defer moduleMu.Unlock()
	if _, ok := moduleRegistry[name]; ok {
		panic("core: module '" + name + "' is already registered")
	}
	moduleRegistry[name] = factory
}

// ModuleNames returns the registered module names, sorted alphabetically.
func ModuleNames() []string {
	moduleMu.Lock()
	defer moduleMu.Unlock()
	names := make([]string, 0, len(moduleRegistry))
	for name := range moduleRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func moduleFactory(name string) (func() Module, bool) {
	moduleMu.Lock()
	defer moduleMu.Unlock()
	f, ok := moduleRegistry[name]
	return f, ok
}
