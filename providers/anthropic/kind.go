package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

// Kind is the router kind this package registers.
const Kind = "anthropic"

// maxModelPages bounds the ListModels pagination.
const maxModelPages = 50

func init() {
	router.RegisterKind(Kind, newKindAdapter)
}

// newKindAdapter is the router factory of the anthropic kind.
func newKindAdapter(name string, provider core.ProviderConfig, deps router.Deps) (router.Adapter, error) {
	return NewAdapter(name, Config{
		BaseURL:           provider.BaseURL,
		APIKeyEnv:         provider.APIKeyEnv,
		Headers:           provider.Headers,
		Timeout:           provider.Timeout,
		StreamIdleTimeout: provider.StreamIdleTimeout,
	}, deps.Secrets, deps.HTTP, deps.Limits)
}

type modelsPage struct {
	Data []struct {
		ID             string `json:"id"`
		DisplayName    string `json:"display_name"`
		MaxInputTokens int    `json:"max_input_tokens"`
		MaxTokens      int    `json:"max_tokens"`
		Capabilities   struct {
			ImageInput struct {
				Supported bool `json:"supported"`
			} `json:"image_input"`
			Thinking struct {
				Supported bool `json:"supported"`
			} `json:"thinking"`
		} `json:"capabilities"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

// ListModels returns the models of GET {base}/v1/models, following the
// after_id pagination (REQ-RTR-013). The router merges the catalog.
func (a *Adapter) ListModels(ctx context.Context) ([]router.ModelInfo, error) {
	var out []router.ModelInfo
	after := ""
	for page := 0; page < maxModelPages; page++ {
		query := url.Values{"limit": {"1000"}}
		if after != "" {
			query.Set("after_id", after)
		}
		req, key, err := a.newRequest(ctx, http.MethodGet, "/v1/models?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := a.client.Do(req)
		if err != nil {
			return nil, classifyNetwork(err, key)
		}
		a.pushLimits(resp.Header)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, a.responseError(resp, key)
		}
		var body modelsPage
		err = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, &core.ProviderError{Class: "server", Message: "invalid models response"}
		}
		for _, m := range body.Data {
			if m.ID == "" {
				continue
			}
			out = append(out, router.ModelInfo{
				ID:            m.ID,
				Name:          m.DisplayName,
				ContextWindow: m.MaxInputTokens,
				MaxOutput:     m.MaxTokens,
				Vision:        m.Capabilities.ImageInput.Supported,
				Thinking:      m.Capabilities.Thinking.Supported,
			})
		}
		if !body.HasMore || body.LastID == "" || body.LastID == after {
			break
		}
		after = body.LastID
	}
	return out, nil
}
