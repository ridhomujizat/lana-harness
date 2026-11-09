package router

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/ridhomujizat/lana-harness/core"
)

// ErrNotSupported is returned by an optional adapter operation the provider
// does not offer, such as ListModels.
var ErrNotSupported = errors.New("router: not supported by this provider")

// Adapter is a provider adapter of one kind (API-RTR-005).
type Adapter interface {
	core.Provider
	// ListModels returns the models the provider's models endpoint lists.
	// It may return ErrNotSupported.
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// BalanceFetcher is an optional adapter interface for providers that report
// a credit or balance through an API (REQ-RTR-012).
type BalanceFetcher interface {
	FetchBalance(ctx context.Context) (LimitUpdate, error)
}

// Deps are the dependencies a Factory gets.
type Deps struct {
	Secrets core.Secrets
	// HTTP is the client set with WithHTTPClient; nil means the adapter
	// builds its own.
	HTTP *http.Client
	// Limits pushes limit values read from response headers. Provider and
	// Kind are filled in by the router when empty.
	Limits func(LimitUpdate)
}

// DefaultKeyEnv returns the default api_key_env of a provider: its name in
// upper case with "-" replaced by "_", plus "_API_KEY" (API-RTR-001).
func DefaultKeyEnv(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_API_KEY"
}

// presetKinds are the kinds whose api_key_env defaults to DefaultKeyEnv.
// openai_compat has no default: a custom host must never get a key the owner
// did not name for it (API-RTR-001).
var presetKinds = map[string]bool{"openai": true, "deepseek": true, "openrouter": true, "anthropic": true}

// EffectiveKeyEnv returns the env name that holds the API key of provider
// name: the explicit api_key_env, else DefaultKeyEnv(name) for the preset
// kinds openai, deepseek, openrouter, and anthropic, else "" (no key).
func EffectiveKeyEnv(cfg core.ProviderConfig, name string) string {
	if cfg.APIKeyEnv != "" {
		return cfg.APIKeyEnv
	}
	if presetKinds[cfg.Kind] {
		return DefaultKeyEnv(name)
	}
	return ""
}

// Factory builds the adapter of one provider.
type Factory func(name string, cfg core.ProviderConfig, deps Deps) (Adapter, error)

var (
	kindsMu sync.RWMutex
	kinds   = map[string]Factory{}
)

// RegisterKind registers the factory of a provider kind. Adapters call it
// from init(). An empty kind, a nil factory, or a duplicate kind panics.
func RegisterKind(kind string, f Factory) {
	if kind == "" || f == nil {
		panic("router: RegisterKind requires a kind and a factory")
	}
	kindsMu.Lock()
	defer kindsMu.Unlock()
	if _, ok := kinds[kind]; ok {
		panic("router: kind '" + kind + "' is already registered")
	}
	kinds[kind] = f
}

// Kinds returns the registered kinds, sorted.
func Kinds() []string {
	kindsMu.RLock()
	defer kindsMu.RUnlock()
	names := make([]string, 0, len(kinds))
	for name := range kinds {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func kindFactory(kind string) (Factory, bool) {
	kindsMu.RLock()
	defer kindsMu.RUnlock()
	f, ok := kinds[kind]
	return f, ok
}

// WrapProvider turns a plain core.Provider into an Adapter whose ListModels
// returns ErrNotSupported. An Adapter is returned unchanged.
func WrapProvider(p core.Provider) Adapter {
	if a, ok := p.(Adapter); ok {
		return a
	}
	return providerAdapter{Provider: p}
}

type providerAdapter struct {
	core.Provider
}

func (providerAdapter) ListModels(context.Context) ([]ModelInfo, error) {
	return nil, ErrNotSupported
}
