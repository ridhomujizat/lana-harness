package openai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Config is the configuration of one openai_compat provider.
type Config struct {
	BaseURL   string
	APIKeyEnv string
	Headers   map[string]string
	Timeout   string
}

// Adapter is a core.Provider for one provider.
type Adapter struct {
	Name    string
	cfg     Config
	secrets core.Secrets
	client  *http.Client
}

// NewAdapter creates an adapter.
func NewAdapter(name string, cfg Config, secrets core.Secrets) (*Adapter, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("base_url of provider '%s' is invalid", name)
	}
	timeout := 300 * time.Second
	if cfg.Timeout != "" {
		parsed, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("timeout of provider '%s' is invalid", name)
		}
		timeout = parsed
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("cross-host redirect rejected")
			}
			return nil
		},
	}
	return &Adapter{Name: name, cfg: cfg, secrets: secrets, client: client}, nil
}

// Stream sends the request and returns an event iterator.
func (a *Adapter) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	body, err := buildRequestBody(req)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(a.cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	for key, value := range a.cfg.Headers {
		httpReq.Header.Set(key, value)
	}
	if a.cfg.APIKeyEnv != "" && a.secrets != nil {
		if secret, ok := a.secrets.Lookup(a.cfg.APIKeyEnv); ok && secret.Reveal() != "" {
			httpReq.Header.Set("Authorization", "Bearer "+secret.Reveal())
		}
	}
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, classifyNetwork(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, classifyHTTP(resp.StatusCode, data)
	}
	return a.readSSE(ctx, resp.Body), nil
}

var _ core.Provider = (*Adapter)(nil)
