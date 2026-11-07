package openai

import (
	"context"
	"net/url"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

func init() {
	router.RegisterKind("openai_compat", newKindAdapter)
}

// newKindAdapter is the router factory of the openai_compat kind.
func newKindAdapter(name string, provider core.ProviderConfig, deps router.Deps) (router.Adapter, error) {
	return NewAdapter(name, Config{
		BaseURL:           provider.BaseURL,
		APIKeyEnv:         provider.APIKeyEnv,
		Headers:           provider.Headers,
		Timeout:           provider.Timeout,
		StreamIdleTimeout: provider.StreamIdleTimeout,
		ThinkingFormat:    resolveThinkingFormat(provider),
	}, deps.Secrets)
}

// ListModels is not implemented yet for this kind (T-RTR-02).
func (a *Adapter) ListModels(context.Context) ([]router.ModelInfo, error) {
	return nil, router.ErrNotSupported
}

// resolveThinkingFormat picks the provider thinking format: the configured
// thinking_format when set, else deepseek for the api.deepseek.com host, else
// none (REQ-MDL-002).
func resolveThinkingFormat(provider core.ProviderConfig) string {
	if provider.ThinkingFormat != "" {
		return provider.ThinkingFormat
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

var _ router.Adapter = (*Adapter)(nil)
