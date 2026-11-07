package openai

import (
	"fmt"
	"net/url"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

// Provider kinds this package serves (REQ-RTR-001).
const (
	kindOpenAI     = "openai"
	kindDeepSeek   = "deepseek"
	kindOpenRouter = "openrouter"
	kindCompat     = "openai_compat"
)

// defaultBaseURLs are the base URLs of the preset kinds (API-RTR-001).
// openai_compat has none: it needs base_url.
var defaultBaseURLs = map[string]string{
	kindOpenAI:     "https://api.openai.com/v1",
	kindDeepSeek:   "https://api.deepseek.com/v1",
	kindOpenRouter: "https://openrouter.ai/api/v1",
}

func init() {
	for _, kind := range []string{kindOpenAI, kindDeepSeek, kindOpenRouter, kindCompat} {
		router.RegisterKind(kind, kindFactory(kind))
	}
}

// kindFactory returns the router factory of one OpenAI-wire kind.
func kindFactory(kind string) router.Factory {
	return func(name string, provider core.ProviderConfig, deps router.Deps) (router.Adapter, error) {
		provider = withKindDefaults(name, kind, provider)
		if provider.BaseURL == "" {
			return nil, fmt.Errorf("provider '%s' of kind '%s' needs base_url", name, kind)
		}
		adapter, err := NewAdapter(name, Config{
			Kind:              kind,
			BaseURL:           provider.BaseURL,
			APIKeyEnv:         provider.APIKeyEnv,
			Headers:           provider.Headers,
			Timeout:           provider.Timeout,
			StreamIdleTimeout: provider.StreamIdleTimeout,
			ThinkingFormat:    resolveThinkingFormat(provider),
			HTTPClient:        deps.HTTP,
			Limits:            deps.Limits,
		}, deps.Secrets)
		if err != nil {
			return nil, err
		}
		switch kind {
		case kindDeepSeek, kindOpenRouter:
			return balanceAdapter{adapter}, nil
		}
		return adapter, nil
	}
}

// withKindDefaults fills the kind defaults of a provider: the kind, the base
// URL of a preset kind, and api_key_env <NAME>_API_KEY for a preset kind.
// openai_compat keeps an empty api_key_env: a custom host must never get a
// key the owner did not name for it (API-RTR-001).
func withKindDefaults(name, kind string, provider core.ProviderConfig) core.ProviderConfig {
	provider.Kind = kind
	preset, ok := defaultBaseURLs[kind]
	if !ok {
		return provider
	}
	if provider.BaseURL == "" {
		provider.BaseURL = preset
	}
	if provider.APIKeyEnv == "" {
		provider.APIKeyEnv = router.DefaultKeyEnv(name)
	}
	return provider
}

// resolveThinkingFormat picks the provider thinking format: the configured
// thinking_format when set, else deepseek for kind deepseek or the
// api.deepseek.com host, else none (REQ-MDL-002, API-RTR-001).
func resolveThinkingFormat(provider core.ProviderConfig) string {
	if provider.ThinkingFormat != "" {
		return provider.ThinkingFormat
	}
	if provider.Kind == kindDeepSeek {
		return formatDeepSeek
	}
	u, err := url.Parse(provider.BaseURL)
	if err != nil {
		return formatNone
	}
	if u.Hostname() == "api.deepseek.com" {
		return formatDeepSeek
	}
	return formatNone
}

var (
	_ router.Adapter        = (*Adapter)(nil)
	_ router.Adapter        = balanceAdapter{}
	_ router.BalanceFetcher = balanceAdapter{}
	_ core.Embedder         = balanceAdapter{}
)
