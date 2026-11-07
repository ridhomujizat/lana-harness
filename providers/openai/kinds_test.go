package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

// roundTripFunc is an http.RoundTripper over a func, for requests to the
// public default URLs.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const doneSSE = "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

// recorded is one request a fake transport saw.
type recorded struct {
	url  string
	auth string
	body map[string]any
}

// recordingClient answers every request with status, headers, and body and
// records the requests.
func recordingClient(status int, header http.Header, body string) (*http.Client, func() []recorded) {
	var mu sync.Mutex
	var seen []recorded
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rec := recorded{url: r.URL.String(), auth: r.Header.Get("Authorization")}
		if r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &rec.body)
		}
		mu.Lock()
		seen = append(seen, rec)
		mu.Unlock()
		h := http.Header{}
		for k, v := range header {
			h[k] = v
		}
		return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	return client, func() []recorded {
		mu.Lock()
		defer mu.Unlock()
		return append([]recorded(nil), seen...)
	}
}

func drain(t *testing.T, stream core.ChatStream) {
	t.Helper()
	for _, err := range stream {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
	}
}

func newRouter(t *testing.T, providers map[string]core.ProviderConfig, opts ...router.Option) *router.Router {
	t.Helper()
	r, err := router.New(router.Config{Providers: providers}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestKindsRegistered(t *testing.T) {
	kinds := router.Kinds()
	for _, kind := range []string{"openai", "deepseek", "openrouter", "openai_compat"} {
		if !slices.Contains(kinds, kind) {
			t.Fatalf("kind %q not registered: %v", kind, kinds)
		}
	}
}

// AC-RTR-001: kind deepseek without base_url calls
// https://api.deepseek.com/v1/chat/completions with the key from DS_API_KEY,
// and the deepseek thinking format applies by kind.
func TestDeepSeekKindDefaults(t *testing.T) {
	client, seen := recordingClient(200, http.Header{"Content-Type": {"text/event-stream"}}, doneSSE)
	r := newRouter(t, map[string]core.ProviderConfig{"ds": {Kind: "deepseek"}},
		router.WithSecrets(mapSecrets{"DS_API_KEY": "sk-ds"}), router.WithHTTPClient(client))
	p, ok := r.Provider("ds")
	if !ok {
		t.Fatal("provider ds missing")
	}
	stream, err := p.Stream(context.Background(), core.ChatRequest{Model: "deepseek-flash", ReasoningEffort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, stream)
	got := seen()
	if len(got) != 1 {
		t.Fatalf("requests=%d", len(got))
	}
	if got[0].url != "https://api.deepseek.com/v1/chat/completions" {
		t.Fatalf("url=%q", got[0].url)
	}
	if got[0].auth != "Bearer sk-ds" {
		t.Fatalf("auth=%q", got[0].auth)
	}
	thinking, _ := got[0].body["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || got[0].body["reasoning_effort"] != "high" {
		t.Fatalf("body=%v", got[0].body)
	}
	if _, ok := p.(core.Embedder); !ok {
		t.Fatal("deepseek adapter lost Embed")
	}
}

func TestPresetKindBaseURLs(t *testing.T) {
	cases := map[string]string{
		"openai":     "https://api.openai.com/v1/chat/completions",
		"openrouter": "https://openrouter.ai/api/v1/chat/completions",
	}
	for kind, want := range cases {
		t.Run(kind, func(t *testing.T) {
			client, seen := recordingClient(200, nil, doneSSE)
			r := newRouter(t, map[string]core.ProviderConfig{"my-" + kind: {Kind: kind}},
				router.WithSecrets(mapSecrets{"MY_" + strings.ToUpper(kind) + "_API_KEY": "k"}), router.WithHTTPClient(client))
			p, _ := r.Provider("my-" + kind)
			stream, err := p.Stream(context.Background(), core.ChatRequest{Model: "m"})
			if err != nil {
				t.Fatal(err)
			}
			drain(t, stream)
			if got := seen(); len(got) != 1 || got[0].url != want || got[0].auth != "Bearer k" {
				t.Fatalf("seen=%+v", got)
			}
			if _, ok := p.(core.Embedder); !ok {
				t.Fatal("adapter lost Embed")
			}
		})
	}
}

func TestOpenAICompatNeedsBaseURL(t *testing.T) {
	_, err := router.New(router.Config{Providers: map[string]core.ProviderConfig{"local": {Kind: "openai_compat"}}})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("err=%v", err)
	}
}

// openai_compat keeps an empty api_key_env: no default key goes to a custom
// host.
func TestOpenAICompatNoDefaultKey(t *testing.T) {
	client, seen := recordingClient(200, nil, doneSSE)
	r := newRouter(t, map[string]core.ProviderConfig{"local": {Kind: "openai_compat", BaseURL: "http://localhost:1/v1"}},
		router.WithSecrets(mapSecrets{"LOCAL_API_KEY": "secret"}), router.WithHTTPClient(client))
	p, _ := r.Provider("local")
	stream, err := p.Stream(context.Background(), core.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, stream)
	if got := seen(); len(got) != 1 || got[0].auth != "" {
		t.Fatalf("seen=%+v", got)
	}
}

func TestDefaultKeyEnv(t *testing.T) {
	if got := router.DefaultKeyEnv("my-open-router"); got != "MY_OPEN_ROUTER_API_KEY" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveThinkingFormatByKind(t *testing.T) {
	if got := resolveThinkingFormat(core.ProviderConfig{Kind: "deepseek", BaseURL: "https://proxy.example/v1"}); got != formatDeepSeek {
		t.Fatalf("format=%q", got)
	}
	if got := resolveThinkingFormat(core.ProviderConfig{Kind: "deepseek", ThinkingFormat: "none"}); got != formatNone {
		t.Fatalf("format=%q", got)
	}
}

func kindBody(t *testing.T, kind string, req core.ChatRequest) map[string]any {
	t.Helper()
	body, err := buildKindRequestBody(req, kind, formatNone)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestOpenAIEffortBodies(t *testing.T) {
	temp := 0.2
	cases := []struct {
		name       string
		req        core.ChatRequest
		wantEffort any // nil = absent
		wantTemp   bool
	}{
		{"catalog thinking high", core.ChatRequest{Model: "gpt-5-mini", ReasoningEffort: "high"}, "high", false},
		{"catalog thinking low", core.ChatRequest{Model: "gpt-5", ReasoningEffort: "low"}, "low", false},
		{"catalog thinking off", core.ChatRequest{Model: "gpt-5", ReasoningEffort: "off"}, "minimal", false},
		{"catalog thinking max", core.ChatRequest{Model: "gpt-5.1", ReasoningEffort: "max"}, "xhigh", false},
		{"catalog thinking default", core.ChatRequest{Model: "gpt-5"}, nil, false},
		{"catalog non-thinking", core.ChatRequest{Model: "gpt-4.1", ReasoningEffort: "high"}, nil, true},
		{"unknown with effort", core.ChatRequest{Model: "o9-new", ReasoningEffort: "high"}, "high", true},
		{"unknown without effort", core.ChatRequest{Model: "o9-new"}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.MaxTokens = 100
			tc.req.Temperature = &temp
			got := kindBody(t, "openai", tc.req)
			if got["reasoning_effort"] != tc.wantEffort {
				t.Fatalf("reasoning_effort=%v want %v", got["reasoning_effort"], tc.wantEffort)
			}
			if got["max_completion_tokens"] != float64(100) {
				t.Fatalf("max_completion_tokens=%v", got["max_completion_tokens"])
			}
			if _, ok := got["max_tokens"]; ok {
				t.Fatalf("max_tokens sent: %v", got)
			}
			if _, ok := got["temperature"]; ok != tc.wantTemp {
				t.Fatalf("temperature=%v want present=%v", got["temperature"], tc.wantTemp)
			}
			for _, key := range []string{"thinking", "reasoning"} {
				if _, ok := got[key]; ok {
					t.Fatalf("%s sent: %v", key, got)
				}
			}
		})
	}
}

func TestOpenRouterEffortBodies(t *testing.T) {
	cases := []struct {
		effort string
		want   map[string]any
	}{
		{"", nil},
		{"off", map[string]any{"enabled": false}},
		{"low", map[string]any{"effort": "low"}},
		{"high", map[string]any{"effort": "high"}},
		{"max", map[string]any{"effort": "high"}},
	}
	for _, tc := range cases {
		t.Run("effort "+tc.effort, func(t *testing.T) {
			got := kindBody(t, "openrouter", core.ChatRequest{Model: "openai/gpt-5-mini", ReasoningEffort: tc.effort, MaxTokens: 50})
			reasoning, _ := got["reasoning"].(map[string]any)
			if tc.want == nil {
				if _, ok := got["reasoning"]; ok {
					t.Fatalf("reasoning sent: %v", got["reasoning"])
				}
			} else if len(reasoning) != len(tc.want) {
				t.Fatalf("reasoning=%v want %v", reasoning, tc.want)
			}
			for k, v := range tc.want {
				if reasoning[k] != v {
					t.Fatalf("reasoning=%v want %v", reasoning, tc.want)
				}
			}
			if got["max_tokens"] != float64(50) {
				t.Fatalf("max_tokens=%v", got["max_tokens"])
			}
			for _, key := range []string{"reasoning_effort", "thinking", "max_completion_tokens"} {
				if _, ok := got[key]; ok {
					t.Fatalf("%s sent: %v", key, got)
				}
			}
		})
	}
}

// OpenRouter streams delta.reasoning and repeats it in reasoning_details;
// the text counts once. A chunk with only reasoning_details is read too.
func TestOpenRouterReasoningDeltas(t *testing.T) {
	body := `data: {"choices":[{"delta":{"reasoning":"think ","reasoning_details":[{"type":"reasoning.text","text":"think "}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","text":"more"},{"type":"reasoning.encrypted","data":"xx"}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	adapter, err := NewAdapter("or", Config{Kind: "openrouter", BaseURL: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Stream(context.Background(), core.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var deltas, text strings.Builder
	var block string
	for _, ev := range collect(t, stream) {
		switch ev.Kind {
		case core.ChatThinkingDelta:
			deltas.WriteString(ev.Text)
		case core.ChatTextDelta:
			text.WriteString(ev.Text)
		case core.ChatBlock:
			if ev.Block.Kind == core.BlockThinking {
				block = ev.Block.Text
			}
		}
	}
	if deltas.String() != "think more" || block != "think more" || text.String() != "answer" {
		t.Fatalf("deltas=%q block=%q text=%q", deltas.String(), block, text.String())
	}
}

func rateHeaders() http.Header {
	h := http.Header{}
	h.Set("x-ratelimit-limit-requests", "500")
	h.Set("x-ratelimit-remaining-requests", "499")
	h.Set("x-ratelimit-reset-requests", "120ms")
	h.Set("x-ratelimit-limit-tokens", "150000")
	h.Set("x-ratelimit-remaining-tokens", "149000")
	h.Set("x-ratelimit-reset-tokens", "6m0s")
	return h
}

// AC-RTR-016 data: the x-ratelimit-* headers of a response, success or
// error, land in the provider's limit snapshot.
func TestRateLimitHeadersToSnapshot(t *testing.T) {
	for _, status := range []int{200, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client, _ := recordingClient(status, rateHeaders(), doneSSE)
			r := newRouter(t, map[string]core.ProviderConfig{"openai": {Kind: "openai"}},
				router.WithSecrets(mapSecrets{"OPENAI_API_KEY": "k"}), router.WithHTTPClient(client))
			p, _ := r.Provider("openai")
			before := time.Now()
			stream, err := p.Stream(context.Background(), core.ChatRequest{Model: "gpt-5-mini"})
			if status == 200 {
				if err != nil {
					t.Fatal(err)
				}
				drain(t, stream)
			} else {
				var pe *core.ProviderError
				if !errors.As(err, &pe) || pe.Class != "rate_limit" {
					t.Fatalf("err=%v", err)
				}
			}
			snaps := r.Limits(context.Background(), false)
			if len(snaps) != 1 || snaps[0].Provider != "openai" || snaps[0].Kind != "openai" || snaps[0].Rate == nil {
				t.Fatalf("snaps=%+v", snaps)
			}
			tokens, requests := snaps[0].Rate.Tokens, snaps[0].Rate.Requests
			if tokens == nil || tokens.Limit != 150000 || tokens.Remaining != 149000 || tokens.ResetAt == nil {
				t.Fatalf("tokens=%+v", tokens)
			}
			if d := tokens.ResetAt.Sub(before); d < 6*time.Minute-time.Second || d > 6*time.Minute+5*time.Second {
				t.Fatalf("tokens reset in %s", d)
			}
			if requests == nil || requests.Limit != 500 || requests.Remaining != 499 || requests.ResetAt == nil {
				t.Fatalf("requests=%+v", requests)
			}
		})
	}
}

func TestParseRateLimits(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	if got := parseRateLimits(http.Header{}, now); got != nil {
		t.Fatalf("empty headers: %+v", got)
	}
	h := http.Header{}
	h.Set("x-ratelimit-remaining-tokens", "12")
	h.Set("x-ratelimit-reset-tokens", "garbage")
	got := parseRateLimits(h, now)
	if got == nil || got.Tokens == nil || got.Tokens.Remaining != 12 || got.Tokens.ResetAt != nil || got.Requests != nil {
		t.Fatalf("got=%+v", got)
	}
	// OpenRouter's plain headers count requests; reset is Unix milliseconds.
	h = http.Header{}
	h.Set("X-RateLimit-Limit", "20")
	h.Set("X-RateLimit-Remaining", "19")
	h.Set("X-RateLimit-Reset", "1794139200000")
	got = parseRateLimits(h, now)
	if got == nil || got.Requests == nil || got.Requests.Limit != 20 || got.Requests.ResetAt == nil ||
		!got.Requests.ResetAt.Equal(time.UnixMilli(1794139200000)) {
		t.Fatalf("got=%+v", got)
	}
	resets := map[string]time.Duration{"1s": time.Second, "20ms": 20 * time.Millisecond, "6m0s": 6 * time.Minute, "2": 2 * time.Second}
	for value, want := range resets {
		at, ok := parseReset(value, now)
		if !ok || at.Sub(now) != want {
			t.Fatalf("reset %q: %v %v", value, at, ok)
		}
	}
}

const openRouterModels = `{"data":[
 {"id":"anthropic/claude-sonnet-4.6","name":"Anthropic: Claude Sonnet 4.6","context_length":1000000,
  "architecture":{"input_modalities":["text","image"],"output_modalities":["text"]},
  "pricing":{"prompt":"0.000003","completion":"0.000015","input_cache_read":"0.0000003"},
  "top_provider":{"context_length":1000000,"max_completion_tokens":64000},
  "supported_parameters":["max_tokens","reasoning","tools"]},
 {"id":"meta/llama-free","name":"Llama free","context_length":0,
  "architecture":{"input_modalities":["text"]},
  "pricing":{"prompt":"0","completion":"0"},
  "top_provider":{"context_length":8192,"max_completion_tokens":null},
  "supported_parameters":["tools"]},
 {"id":"openrouter/auto","name":"Auto Router","context_length":2000000,
  "pricing":{"prompt":"-1","completion":"-1"}},
 {"id":""}
]}`

func TestOpenRouterListModels(t *testing.T) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(openRouterModels))
	}))
	defer srv.Close()
	adapter, err := NewAdapter("or", Config{Kind: "openrouter", BaseURL: srv.URL + "/api/v1", APIKeyEnv: "OR_KEY"}, mapSecrets{"OR_KEY": "sk-or"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := adapter.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if path != "/api/v1/models" || auth != "Bearer sk-or" {
		t.Fatalf("path=%q auth=%q", path, auth)
	}
	if len(got) != 3 {
		t.Fatalf("models=%+v", got)
	}
	sonnet := got[0]
	if sonnet.ID != "anthropic/claude-sonnet-4.6" || sonnet.Name != "Anthropic: Claude Sonnet 4.6" ||
		sonnet.ContextWindow != 1000000 || sonnet.MaxOutput != 64000 || !sonnet.Vision || !sonnet.Thinking {
		t.Fatalf("sonnet=%+v", sonnet)
	}
	if p := sonnet.Price; p == nil || p.Input != 3 || p.Output != 15 || p.CachedInput != 0.3 {
		t.Fatalf("price=%+v", sonnet.Price)
	}
	free := got[1]
	if free.ContextWindow != 8192 || free.MaxOutput != 0 || free.Vision || free.Thinking {
		t.Fatalf("free=%+v", free)
	}
	if p := free.Price; p == nil || p.Input != 0 || p.Output != 0 {
		t.Fatalf("free price=%+v", free.Price)
	}
	if got[2].Price != nil {
		t.Fatalf("auto price=%+v", got[2].Price)
	}
}

func TestPerMillion(t *testing.T) {
	for perToken, want := range map[float64]float64{0.000003: 3, 0.00000125: 1.25, 0.0000000375: 0.0375, 0.000015: 15} {
		if got := perMillion(perToken); got != want {
			t.Fatalf("perMillion(%v)=%v want %v", perToken, got, want)
		}
	}
}

func TestListModelsIDsAnd404(t *testing.T) {
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 200 {
			http.Error(w, `{"error":"nope"}`, status)
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-flash","object":"model","owned_by":"deepseek"}]}`))
	}))
	defer srv.Close()
	ds, _ := NewAdapter("ds", Config{Kind: "deepseek", BaseURL: srv.URL}, nil)
	got, err := ds.ListModels(context.Background())
	if err != nil || len(got) != 1 || got[0].ID != "deepseek-flash" || got[0].Price != nil {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	status = 404
	compat, _ := NewAdapter("c", Config{Kind: "openai_compat", BaseURL: srv.URL}, nil)
	if _, err := compat.ListModels(context.Background()); !errors.Is(err, router.ErrNotSupported) {
		t.Fatalf("compat err=%v", err)
	}
	oa, _ := NewAdapter("o", Config{Kind: "openai", BaseURL: srv.URL}, nil)
	var pe *core.ProviderError
	if _, err := oa.ListModels(context.Background()); !errors.As(err, &pe) || pe.Class != core.ErrorClassNotFound {
		t.Fatalf("openai err=%v", err)
	}
}

// A chat call to a missing model (HTTP 404) is classified not_found.
func TestStream404NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"model not found"}}`, http.StatusNotFound)
	}))
	defer srv.Close()
	adapter, _ := NewAdapter("p", Config{BaseURL: srv.URL}, nil)
	_, err := adapter.Stream(context.Background(), core.ChatRequest{Model: "nope"})
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != core.ErrorClassNotFound || pe.Status != 404 {
		t.Fatalf("err=%v", err)
	}
}

func TestDeepSeekBalance(t *testing.T) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[
			{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"},
			{"currency":"USD","total_balance":"4.20","granted_balance":"0.00","topped_up_balance":"4.20"}]}`))
	}))
	defer srv.Close()
	r := newRouter(t, map[string]core.ProviderConfig{"deepseek": {Kind: "deepseek", BaseURL: srv.URL + "/v1"}},
		router.WithSecrets(mapSecrets{"DEEPSEEK_API_KEY": "sk-ds"}))
	snaps := r.Limits(context.Background(), true)
	if path != "/user/balance" || auth != "Bearer sk-ds" {
		t.Fatalf("path=%q auth=%q", path, auth)
	}
	if len(snaps) != 1 || snaps[0].Error != "" {
		t.Fatalf("snaps=%+v", snaps)
	}
	b := snaps[0].Balance
	if b == nil || b.Currency != "USD" || b.Total != 4.2 || b.Granted != 0 || b.ToppedUp != 4.2 {
		t.Fatalf("balance=%+v", b)
	}
}

// AC-RTR-017 data: a key with limit $10 and usage $2.50 has $7.50 left.
func TestOpenRouterCredit(t *testing.T) {
	answer := `{"data":{"label":"k","limit":10,"usage":2.5,"is_free_tier":false}}`
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	r := newRouter(t, map[string]core.ProviderConfig{"openrouter": {Kind: "openrouter", BaseURL: srv.URL + "/api/v1"}},
		router.WithSecrets(mapSecrets{"OPENROUTER_API_KEY": "sk-or"}))
	snaps := r.Limits(context.Background(), true)
	if path != "/api/v1/key" {
		t.Fatalf("path=%q", path)
	}
	c := snaps[0].Credit
	if c == nil || c.LimitUSD == nil || *c.LimitUSD != 10 || c.UsedUSD != 2.5 || c.RemainingUSD == nil || *c.RemainingUSD != 7.5 {
		t.Fatalf("credit=%+v", c)
	}

	answer = `{"data":{"limit":null,"usage":1.25,"limit_remaining":null}}`
	adapter, _ := NewAdapter("or", Config{Kind: "openrouter", BaseURL: srv.URL + "/api/v1"}, nil)
	u, err := balanceAdapter{adapter}.FetchBalance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if u.Credit == nil || u.Credit.LimitUSD != nil || u.Credit.RemainingUSD != nil || u.Credit.UsedUSD != 1.25 {
		t.Fatalf("credit=%+v", u.Credit)
	}

	answer = `{"data":{"limit":20,"usage":5,"limit_remaining":12}}`
	u, err = balanceAdapter{adapter}.FetchBalance(context.Background())
	if err != nil || *u.Credit.RemainingUSD != 12 {
		t.Fatalf("credit=%+v err=%v", u.Credit, err)
	}
}

// Kinds without a balance API are not balance fetchers.
func TestBalanceOnlyForDeepSeekAndOpenRouter(t *testing.T) {
	r := newRouter(t, map[string]core.ProviderConfig{
		"oa":     {Kind: "openai"},
		"compat": {Kind: "openai_compat", BaseURL: "http://localhost:1"},
		"ds":     {Kind: "deepseek"},
		"or":     {Kind: "openrouter"},
	})
	for name, want := range map[string]bool{"oa": false, "compat": false, "ds": true, "or": true} {
		p, _ := r.Provider(name)
		if _, ok := p.(router.BalanceFetcher); ok != want {
			t.Fatalf("%s balance fetcher=%v want %v", name, ok, want)
		}
	}
}
