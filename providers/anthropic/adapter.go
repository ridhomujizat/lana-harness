// Package anthropic is the native adapter of the Anthropic Messages API
// (REQ-RTR-002). It registers the router kind "anthropic" and talks plain
// HTTP: POST {base}/v1/messages with streaming, and GET {base}/v1/models.
package anthropic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

const (
	// DefaultBaseURL is the base URL when the provider sets none.
	DefaultBaseURL = "https://api.anthropic.com"
	// DefaultAPIKeyEnv is the key variable when the provider sets none.
	DefaultAPIKeyEnv = "ANTHROPIC_API_KEY"
	// apiVersion is the anthropic-version header value.
	apiVersion = "2023-06-01"
)

// defaultResponseHeaderTimeout is the provider [timeout] default: the wait
// for the response headers, not a total request limit.
const defaultResponseHeaderTimeout = 300 * time.Second

// defaultStreamIdleTimeout is the [stream_idle_timeout] default: the longest
// gap between two body reads before the stream is cut. The API sends ping
// events while a long thinking block runs, so a quiet stream is a dead one.
const defaultStreamIdleTimeout = 120 * time.Second

// Config is the configuration of one anthropic provider.
type Config struct {
	BaseURL           string
	APIKeyEnv         string
	Headers           map[string]string
	Timeout           string
	StreamIdleTimeout string
}

// Adapter is the router.Adapter of one anthropic provider.
type Adapter struct {
	Name       string
	cfg        Config
	base       string
	secrets    core.Secrets
	client     *http.Client
	streamIdle time.Duration
	limits     func(router.LimitUpdate)
}

// NewAdapter creates an adapter. httpClient may be nil; the adapter then
// builds its own client. limits may be nil.
func NewAdapter(name string, cfg Config, secrets core.Secrets, httpClient *http.Client, limits func(router.LimitUpdate)) (*Adapter, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.APIKeyEnv == "" {
		cfg.APIKeyEnv = DefaultAPIKeyEnv
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("base_url of provider '%s' is invalid", name)
	}
	timeout := defaultResponseHeaderTimeout
	if cfg.Timeout != "" {
		parsed, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("timeout of provider '%s' is invalid", name)
		}
		timeout = parsed
	}
	streamIdle := defaultStreamIdleTimeout
	if cfg.StreamIdleTimeout != "" {
		parsed, err := time.ParseDuration(cfg.StreamIdleTimeout)
		if err != nil {
			return nil, fmt.Errorf("stream_idle_timeout of provider '%s' is invalid", name)
		}
		streamIdle = parsed
	}
	client := httpClient
	if client == nil {
		client = &http.Client{
			// No total timeout: a long stream must only be cut by the idle timer.
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
				TLSHandshakeTimeout:   15 * time.Second,
				ResponseHeaderTimeout: timeout,
				ForceAttemptHTTP2:     true,
			},
			CheckRedirect: checkRedirect,
		}
	}
	return &Adapter{
		Name:       name,
		cfg:        cfg,
		base:       normalizeBase(cfg.BaseURL),
		secrets:    secrets,
		client:     client,
		streamIdle: streamIdle,
		limits:     limits,
	}, nil
}

// checkRedirect allows at most five same-host redirects; a redirect to
// another host would carry the API key away.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("too many redirects")
	}
	if req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("cross-host redirect rejected")
	}
	return nil
}

// normalizeBase trims a trailing "/" and a trailing "/v1", so both
// "https://api.anthropic.com" and "https://api.anthropic.com/v1" work.
func normalizeBase(base string) string {
	base = strings.TrimRight(base, "/")
	base = strings.TrimSuffix(base, "/v1")
	return strings.TrimRight(base, "/")
}

// apiKey returns the configured key, or "" when it is not set.
func (a *Adapter) apiKey() string {
	if a.secrets == nil {
		return ""
	}
	secret, ok := a.secrets.Lookup(a.cfg.APIKeyEnv)
	if !ok {
		return ""
	}
	return secret.Reveal()
}

// newRequest builds a request with the Anthropic headers. It fails with a
// missing_secret error when the key is not set.
func (a *Adapter) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, string, error) {
	key := a.apiKey()
	if key == "" {
		return nil, "", &core.ProviderError{Class: "missing_secret", Message: fmt.Sprintf("provider '%s' needs %s", a.Name, a.cfg.APIKeyEnv)}
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, reader)
	if err != nil {
		return nil, "", err
	}
	for k, v := range a.cfg.Headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("anthropic-version", apiVersion)
	req.Header.Set("x-api-key", key)
	return req, key, nil
}

// Stream sends a streaming Messages request and returns an event iterator.
func (a *Adapter) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	body, err := buildRequestBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, key, err := a.newRequest(ctx, http.MethodPost, "/v1/messages", body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, classifyNetwork(err, key)
	}
	a.pushLimits(resp.Header)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, a.responseError(resp, key)
	}
	return a.readSSE(ctx, resp.Body, key), nil
}

// responseError reads and classifies a non-2xx response and closes its body.
func (a *Adapter) responseError(resp *http.Response, key string) *core.ProviderError {
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	pe := classifyHTTP(resp.StatusCode, data, key)
	pe.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	return pe
}

// pushLimits reports the anthropic-ratelimit-* headers of a response.
func (a *Adapter) pushLimits(h http.Header) {
	if a.limits == nil {
		return
	}
	if rate := parseRateLimits(h); rate != nil {
		a.limits(router.LimitUpdate{Provider: a.Name, Kind: Kind, Rate: rate})
	}
}

var _ router.Adapter = (*Adapter)(nil)
