// Package commandcode is the adapter of Command Code (commandcode.ai), the
// router kind "commandcode" (spec 51). It talks the wire the official
// command-code CLI uses: POST {base}/alpha/generate with a
// {threadId, memory, config, params} envelope, answered with NDJSON stream
// events, and GET {base}/provider/v1/models for the model list. The port
// follows github.com/luqman-v1/9router-go (internal/proxy/executor
// commandcode_*.go).
package commandcode

import (
	"bytes"
	"context"
	"encoding/json"
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
	// Kind is the router kind this package registers.
	Kind = "commandcode"
	// DefaultBaseURL is the base URL when the provider sets none.
	DefaultBaseURL = "https://api.commandcode.ai"
	// DefaultAPIKeyEnv is the key variable when the provider sets none.
	DefaultAPIKeyEnv = "COMMANDCODE_API_KEY"
	// cliVersion is the command-code CLI version the identity headers
	// present (x-command-code-version). A provider header overrides it.
	cliVersion = "1.88.0"
)

// defaultResponseHeaderTimeout is the provider [timeout] default: the wait
// for the response headers, not a total request limit.
const defaultResponseHeaderTimeout = 300 * time.Second

// defaultStreamIdleTimeout is the [stream_idle_timeout] default: the longest
// gap between two body reads before the stream is cut.
const defaultStreamIdleTimeout = 120 * time.Second

func init() {
	router.RegisterKind(Kind, newKindAdapter)
}

// newKindAdapter is the router factory of the commandcode kind.
func newKindAdapter(name string, provider core.ProviderConfig, deps router.Deps) (router.Adapter, error) {
	return NewAdapter(name, Config{
		BaseURL:           provider.BaseURL,
		APIKeyEnv:         router.EffectiveKeyEnv(provider, name),
		Headers:           provider.Headers,
		Timeout:           provider.Timeout,
		StreamIdleTimeout: provider.StreamIdleTimeout,
	}, deps.Secrets, deps.HTTP)
}

// Config is the configuration of one commandcode provider.
type Config struct {
	BaseURL           string
	APIKeyEnv         string
	Headers           map[string]string
	Timeout           string
	StreamIdleTimeout string
}

// Adapter is the router.Adapter of one commandcode provider.
type Adapter struct {
	Name       string
	cfg        Config
	base       string
	secrets    core.Secrets
	client     *http.Client
	streamIdle time.Duration
}

// NewAdapter creates an adapter. httpClient may be nil; the adapter then
// builds its own client.
func NewAdapter(name string, cfg Config, secrets core.Secrets, httpClient *http.Client) (*Adapter, error) {
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
	}
	return &Adapter{
		Name:       name,
		cfg:        cfg,
		base:       strings.TrimRight(cfg.BaseURL, "/"),
		secrets:    secrets,
		client:     client,
		streamIdle: streamIdle,
	}, nil
}

// Stream sends the request to /alpha/generate and returns an event iterator.
func (a *Adapter) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	env := buildEnvelope(req, time.Now())
	body, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	httpReq, key, err := a.newRequest(ctx, http.MethodPost, "/alpha/generate", body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	// x-session-id mirrors the body's threadId, as the official CLI does.
	httpReq.Header.Set("x-session-id", env.ThreadID)
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, classifyNetwork(err, key)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError(resp, key)
	}
	return a.readStream(ctx, resp.Body), nil
}

// ListModels returns the models of GET {base}/provider/v1/models
// (REQ-RTR-013). The list is public; the key is sent when one is set.
func (a *Adapter) ListModels(ctx context.Context) ([]router.ModelInfo, error) {
	req, key, err := a.newRequest(ctx, http.MethodGet, "/provider/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, classifyNetwork(err, key)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError(resp, key)
	}
	defer func() { _ = resp.Body.Close() }()
	var payload struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&payload); err != nil {
		return nil, &core.ProviderError{Class: "server", Message: "invalid models response"}
	}
	out := make([]router.ModelInfo, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID == "" {
			continue
		}
		out = append(out, router.ModelInfo{ID: m.ID, Name: m.Name, ContextWindow: m.ContextLength})
	}
	return out, nil
}

// newRequest builds a request with the CLI identity headers, the provider
// headers, and the bearer key. It returns the key so error messages can
// redact it.
func (a *Adapter) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, string, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, reader)
	if err != nil {
		return nil, "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Identity headers of the command-code CLI; a provider header wins.
	req.Header.Set("User-Agent", "cli")
	req.Header.Set("x-command-code-version", cliVersion)
	req.Header.Set("x-cli-environment", "production")
	req.Header.Set("x-cli-surface", "cli")
	req.Header.Set("x-taste-learning", "false")
	req.Header.Set("x-project-slug", projectSlug)
	req.Header.Set("Accept-Encoding", "identity")
	for k, v := range a.cfg.Headers {
		req.Header.Set(k, v)
	}
	key := ""
	if a.secrets != nil {
		if secret, ok := a.secrets.Lookup(a.cfg.APIKeyEnv); ok {
			key = secret.Reveal()
		}
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, key, nil
}

var _ router.Adapter = (*Adapter)(nil)
