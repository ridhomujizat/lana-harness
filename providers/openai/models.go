package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

// maxJSONBody caps a JSON response read by get. OpenRouter's model list is
// a few megabytes.
const maxJSONBody = 32 << 20

// get sends an authenticated GET and returns the body of a 2xx answer. A
// non-2xx answer is a classified *core.ProviderError.
func (a *Adapter) get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	a.setHeaders(req)
	req.Header.Del("Content-Type")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, classifyNetwork(err)
	}
	defer func() { _ = resp.Body.Close() }()
	a.reportLimits(resp.Header)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		pe := classifyHTTP(resp.StatusCode, data)
		pe.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, pe
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxJSONBody))
}

// wireModel is one entry of GET /models. OpenAI and DeepSeek send only the
// id; OpenRouter adds the name, context length, prices, modalities, and
// supported parameters.
type wireModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
	TopProvider   struct {
		ContextLength       int  `json:"context_length"`
		MaxCompletionTokens *int `json:"max_completion_tokens"`
	} `json:"top_provider"`
	Pricing *struct {
		Prompt         flexNumber `json:"prompt"`
		Completion     flexNumber `json:"completion"`
		InputCacheRead flexNumber `json:"input_cache_read"`
	} `json:"pricing"`
	Architecture struct {
		InputModalities []string `json:"input_modalities"`
	} `json:"architecture"`
	SupportedParameters []string `json:"supported_parameters"`
}

// ListModels returns the models of GET {base_url}/models (REQ-RTR-013). The
// router merges them with the catalog. An openai_compat server without the
// endpoint (404) returns router.ErrNotSupported.
func (a *Adapter) ListModels(ctx context.Context) ([]router.ModelInfo, error) {
	data, err := a.get(ctx, strings.TrimRight(a.cfg.BaseURL, "/")+"/models")
	if err != nil {
		var pe *core.ProviderError
		if a.kind == kindCompat && errors.As(err, &pe) && pe.Status == http.StatusNotFound {
			return nil, router.ErrNotSupported
		}
		return nil, err
	}
	var payload struct {
		Data []wireModel `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("models list of provider '%s': %w", a.Name, err)
	}
	out := make([]router.ModelInfo, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID == "" {
			continue
		}
		out = append(out, m.info())
	}
	return out, nil
}

// info converts a wire model. Prices are USD per token on the wire and per
// one million tokens in lana; a negative price (OpenRouter's routers) is
// unknown.
func (m wireModel) info() router.ModelInfo {
	info := router.ModelInfo{
		ID:            m.ID,
		Name:          m.Name,
		ContextWindow: m.ContextLength,
		Vision:        slices.Contains(m.Architecture.InputModalities, "image"),
		Thinking:      slices.Contains(m.SupportedParameters, "reasoning"),
	}
	if info.ContextWindow == 0 {
		info.ContextWindow = m.TopProvider.ContextLength
	}
	if m.TopProvider.MaxCompletionTokens != nil {
		info.MaxOutput = *m.TopProvider.MaxCompletionTokens
	}
	if p := m.Pricing; p != nil && (p.Prompt.set || p.Completion.set) {
		input, output, cached := p.Prompt.value, p.Completion.value, p.InputCacheRead.value
		if input >= 0 && output >= 0 && cached >= 0 {
			info.Price = &router.Price{
				Input:       perMillion(input),
				CachedInput: perMillion(cached),
				Output:      perMillion(output),
			}
		}
	}
	return info
}

// perMillion converts a per-token price to a per-million price, rounded to
// drop float noise ("0.000003" → 3, not 2.9999999999999996).
func perMillion(perToken float64) float64 {
	return math.Round(perToken*1e6*1e9) / 1e9
}

// flexNumber is a JSON number sent as a number or a decimal string.
type flexNumber struct {
	value float64
	set   bool
}

func (n *flexNumber) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "null" {
		return nil
	}
	if unquoted, err := strconv.Unquote(raw); err == nil {
		raw = strings.TrimSpace(unquoted)
	}
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fmt.Errorf("invalid number %s", string(data))
	}
	n.value, n.set = v, true
	return nil
}
