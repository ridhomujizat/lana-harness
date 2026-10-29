package core

import (
	"context"
	"sort"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// Module adalah unit fitur yang dikompilasi ke binary.
type Module interface {
	ID() string
	Requires() []ServiceKey
	Provides() []ServiceKey
	Install(b *Builder, cfg ModuleConfig) error
	Stop(ctx context.Context) error
}

// ModuleConfig adalah isi satu baris [[modules]] setelah merge.
type ModuleConfig struct {
	ID     string
	Module string
	Values map[string]any
}

// Decode menyalin Values ke struct tujuan lewat TOML.
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

// RegisterModule mendaftarkan factory modul. Nama duplikat memicu panic.
func RegisterModule(name string, factory func() Module) {
	if name == "" || factory == nil {
		panic("core: RegisterModule membutuhkan nama dan factory")
	}
	moduleMu.Lock()
	defer moduleMu.Unlock()
	if _, ok := moduleRegistry[name]; ok {
		panic("core: modul '" + name + "' sudah terdaftar")
	}
	moduleRegistry[name] = factory
}

// ModuleNames mengembalikan nama modul terdaftar, diurutkan alfabet.
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
