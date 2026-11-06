package core

import (
	"fmt"
	"sort"
	"sync"
)

type serviceEntry struct {
	provider string
	value    any
}

// Builder is used by modules during Install. It is not used after Boot completes.
type Builder struct {
	mu        sync.Mutex
	cfg       Config
	secrets   Secrets
	home      string
	hooks     *Hooks
	services  map[ServiceKey]serviceEntry
	owned     map[string][]func()
	currentID string
}

func newBuilder(cfg Config) *Builder {
	return &Builder{
		cfg:      cfg,
		hooks:    newHooks(),
		services: map[ServiceKey]serviceEntry{},
		owned:    map[string][]func(){},
	}
}

// Provide installs a service. It returns an error if another module already provides the key.
func (b *Builder) Provide(key ServiceKey, svc any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if old, ok := b.services[key]; ok {
		return fmt.Errorf("service '%s' is already provided by module %s", key, old.provider)
	}
	b.services[key] = serviceEntry{provider: b.currentID, value: svc}
	return nil
}

// Lookup returns a service already provided by an earlier module.
func (b *Builder) Lookup(key ServiceKey) (any, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.services[key]
	return entry.value, ok
}

// Own registers a release function (from a Use hook). It is called when the module stops.
func (b *Builder) Own(release func()) {
	if release == nil {
		return
	}
	b.mu.Lock()
	b.owned[b.currentID] = append(b.owned[b.currentID], release)
	b.mu.Unlock()
}

// Hooks returns the shared hooks.
func (b *Builder) Hooks() *Hooks { return b.hooks }

// Secrets returns the process secret source (not exported in the contract).
func (b *Builder) Secrets() Secrets { return b.secrets }

// Home returns the LANA_HOME used by the runtime (empty if none).
func (b *Builder) Home() string { return b.home }

// Config returns a copy of the effective config (read-only).
func (b *Builder) Config() Config {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cfg
}

func (b *Builder) takeOwned(id string) []func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	releases := b.owned[id]
	delete(b.owned, id)
	return releases
}

func (b *Builder) serviceSnapshot() map[ServiceKey]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[ServiceKey]any, len(b.services))
	for key, entry := range b.services {
		out[key] = entry.value
	}
	return out
}

// ModuleNamesSorted returns the registered module names for error messages.
func ModuleNamesSorted() []string {
	names := ModuleNames()
	sort.Strings(names)
	return names
}
