package openai

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

// defaultResponseHeaderTimeout is the provider [timeout] default: the wait
// for the response headers, not a total request limit.
const defaultResponseHeaderTimeout = 300 * time.Second

// defaultStreamIdleTimeout is the [stream_idle_timeout] default: the longest
// gap between two body reads before the stream is cut.
const defaultStreamIdleTimeout = 120 * time.Second

// Config is the configuration of one OpenAI-wire provider.
type Config struct {
	// Kind is the provider kind: openai, deepseek, openrouter, or
	// openai_compat. Empty means openai_compat.
	Kind              string
	BaseURL           string
	APIKeyEnv         string
	Headers           map[string]string
	Timeout           string
	StreamIdleTimeout string
	// ThinkingFormat is the resolved thinking format: deepseek or none.
	ThinkingFormat string
	// HTTPClient replaces the adapter's own client when set (router
	// WithHTTPClient). Timeout then does not apply.
	HTTPClient *http.Client
	// Limits receives the rate limits read from response headers
	// (REQ-RTR-012). Nil drops them.
	Limits func(router.LimitUpdate)
}

// Adapter is a core.Provider for one provider.
type Adapter struct {
	Name           string
	cfg            Config
	secrets        core.Secrets
	client         *http.Client
	streamIdle     time.Duration
	thinkingFormat string
	kind           string
	limits         func(router.LimitUpdate)
}

// NewAdapter creates an adapter.
func NewAdapter(name string, cfg Config, secrets core.Secrets) (*Adapter, error) {
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
	client := &http.Client{
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
	if cfg.HTTPClient != nil {
		client = cfg.HTTPClient
	}
	kind := cfg.Kind
	if kind == "" {
		kind = kindCompat
	}
	return &Adapter{
		Name:           name,
		cfg:            cfg,
		secrets:        secrets,
		client:         client,
		streamIdle:     streamIdle,
		thinkingFormat: cfg.ThinkingFormat,
		kind:           kind,
		limits:         cfg.Limits,
	}, nil
}

// Stream sends the request and returns an event iterator.
func (a *Adapter) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	body, err := buildKindRequestBody(req, a.kind, a.thinkingFormat)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(a.cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	a.setHeaders(httpReq)
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, classifyNetwork(err)
	}
	a.reportLimits(resp.Header)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		pe := classifyHTTP(resp.StatusCode, data)
		pe.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, pe
	}
	return a.readSSE(ctx, resp.Body), nil
}

var _ core.Provider = (*Adapter)(nil)
