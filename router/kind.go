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
var presetKinds = map[string]bool{"openai": true, "deepseek": true, "openrouter": true, "anthropic": true, "commandcode": true}

// EffectiveKeyEnv returns the env name that holds the API key of provider
// name: the explicit api_key_env, else DefaultKeyEnv(name) for the preset
// kinds openai, deepseek, openrouter, anthropic, and commandcode, else ""
// (no key).
func EffectiveKeyEnv(cfg core.ProviderConfig, name string) string {
	if cfg.APIKeyEnv != "" {
		return cfg.APIKeyEnv
	}
	if presetKinds[cfg.Kind] {
		return DefaultKeyEnv(name)
	}
	return ""
}

// KeyEnvs returns the API key envs of provider name in pool order
// (REQ-PAF-005): EffectiveKeyEnv first, then each api_key_envs entry, with
// empty and repeated names left out. It is empty for a provider without a
// key.
func KeyEnvs(cfg core.ProviderConfig, name string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(env string) {
		env = strings.TrimSpace(env)
		if env == "" || seen[env] {
			return
		}
		seen[env] = true
		out = append(out, env)
	}
	add(EffectiveKeyEnv(cfg, name))
	for _, env := range cfg.APIKeyEnvs {
		add(env)
	}
	return out
}

// quotaPhrases are the provider messages that mean the balance, credit, or
// quota of the key is used up (REQ-PAF-005).
var quotaPhrases = []string{
	"insufficient balance",
	"insufficient_quota",
	"credit balance is too low",
	"exceeded your current quota",
	"quota exceeded",
}

// IsQuotaError reports whether an HTTP error status and message mean an
// exhausted balance, credit, or quota (core.ErrorClassQuota): HTTP 402, or a
// 400, 403, or 429 whose message holds one of the quota phrases (any case).
// Adapters use it to classify; the router applies it again to every error.
func IsQuotaError(status int, message string) bool {
	switch status {
	case http.StatusPaymentRequired:
		return true
	case http.StatusBadRequest, http.StatusForbidden, http.StatusTooManyRequests:
		lower := strings.ToLower(message)
		for _, phrase := range quotaPhrases {
			if strings.Contains(lower, phrase) {
				return true
			}
		}
	}
	return false
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
