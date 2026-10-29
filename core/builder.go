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

// Builder dipakai modul saat Install. Tidak dipakai setelah Boot selesai.
type Builder struct {
	mu        sync.Mutex
	cfg       Config
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

// Provide memasang service. Error jika key sudah dipasang modul lain.
func (b *Builder) Provide(key ServiceKey, svc any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if old, ok := b.services[key]; ok {
		return fmt.Errorf("service '%s' sudah dipasang modul %s", key, old.provider)
	}
	b.services[key] = serviceEntry{provider: b.currentID, value: svc}
	return nil
}

// Lookup mengambil service yang sudah dipasang modul sebelumnya.
func (b *Builder) Lookup(key ServiceKey) (any, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.services[key]
	return entry.value, ok
}

// Own mendaftarkan fungsi release (dari Use hook). Dipanggil saat modul berhenti.
func (b *Builder) Own(release func()) {
	if release == nil {
		return
	}
	b.mu.Lock()
	b.owned[b.currentID] = append(b.owned[b.currentID], release)
	b.mu.Unlock()
}

// Hooks mengembalikan hook yang dipakai bersama.
func (b *Builder) Hooks() *Hooks { return b.hooks }

// Config mengembalikan salinan config efektif (baca saja).
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

// ModuleNamesSorted mengembalikan daftar modul terdaftar untuk pesan error.
func ModuleNamesSorted() []string {
	names := ModuleNames()
	sort.Strings(names)
	return names
}
