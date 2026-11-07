package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// embedRequest is the POST /embeddings body (API-KNW-004).
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedResponse is the subset of the embeddings reply lana reads.
type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed sends one batch of inputs to <base_url>/embeddings and returns the
// vectors in input order, regardless of the order of the response entries
// (API-KNW-004). It uses the same auth, headers, timeout, and error
// classification as Stream. Splitting a large batch is the caller's job.
func (a *Adapter) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embedRequest{Model: model, Input: inputs})
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(a.cfg.BaseURL, "/") + "/embeddings"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	a.setHeaders(httpReq)
	resp, err := a.client.Do(httpReq)
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
	var payload embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if len(payload.Data) != len(inputs) {
		return nil, fmt.Errorf("embeddings: expected %d vectors, got %d", len(inputs), len(payload.Data))
	}
	out := make([][]float32, len(inputs))
	for _, item := range payload.Data {
		if item.Index < 0 || item.Index >= len(inputs) {
			return nil, fmt.Errorf("embeddings: index %d out of range", item.Index)
		}
		out[item.Index] = item.Embedding
	}
	for i, vec := range out {
		if vec == nil {
			return nil, fmt.Errorf("embeddings: response has no vector for index %d", i)
		}
	}
	return out, nil
}

// setHeaders applies the provider headers and API key to a request, matching
// the streaming path.
func (a *Adapter) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	for key, value := range a.cfg.Headers {
		req.Header.Set(key, value)
	}
	if a.cfg.APIKeyEnv != "" && a.secrets != nil {
		if secret, ok := a.secrets.Lookup(a.cfg.APIKeyEnv); ok && secret.Reveal() != "" {
			req.Header.Set("Authorization", "Bearer "+secret.Reveal())
		}
	}
}

var _ core.Embedder = (*Adapter)(nil)
