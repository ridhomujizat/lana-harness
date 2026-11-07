package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

const testKey = "sk-ant-test-secret-key"

type mapSecrets map[string]string

func (m mapSecrets) Lookup(name string) (core.Secret, bool) {
	v, ok := m[name]
	return core.NewSecret(v), ok
}

// fakeAPI is an httptest Anthropic API. handler answers /v1/messages; the
// last request body and headers are recorded.
type fakeAPI struct {
	mu      sync.Mutex
	body    []byte
	headers http.Header
	path    string
	server  *httptest.Server
}

func newFakeAPI(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.body, f.headers, f.path = body, r.Header.Clone(), r.URL.Path
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func sseHandler(t *testing.T, fixture string) func(http.ResponseWriter, *http.Request) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	}
}

func newTestAdapter(t *testing.T, base string, limits func(router.LimitUpdate)) *Adapter {
	t.Helper()
	a, err := NewAdapter("claude", Config{BaseURL: base, APIKeyEnv: "ANTHROPIC_API_KEY", Headers: map[string]string{"X-Extra": "1"}, StreamIdleTimeout: "200ms"},
		mapSecrets{"ANTHROPIC_API_KEY": testKey}, nil, limits)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func collect(t *testing.T, stream core.ChatStream) ([]core.ChatEvent, error) {
	t.Helper()
	var events []core.ChatEvent
	for ev, err := range stream {
		if err != nil {
			return events, err
		}
		events = append(events, ev)
	}
	return events, nil
}

func TestKindRegistered(t *testing.T) {
	if !slices.Contains(router.Kinds(), Kind) {
		t.Fatalf("kinds=%v", router.Kinds())
	}
}

// AC-RTR-002: thinking deltas, text deltas, one tool_use block, usage with
// cached input tokens, and stop reason tool_use.
func TestStreamThinkingTextToolUsage(t *testing.T) {
	api := newFakeAPI(t, sseHandler(t, "thinking_tool.sse"))
	a := newTestAdapter(t, api.server.URL+"/v1/", nil)
	stream, err := a.Stream(context.Background(), core.ChatRequest{Model: "claude-opus-5-5", Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hi"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	events, err := collect(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.ChatEvent{
		{Kind: core.ChatThinkingDelta, Text: "Let me look "},
		{Kind: core.ChatThinkingDelta, Text: "at the file."},
		{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockThinking, Text: "Let me look at the file.", Signature: "EqQBCgIYAhIM1gbcDa9GJwZA2b3h"}},
		{Kind: core.ChatTextDelta, Text: "Reading "},
		{Kind: core.ChatTextDelta, Text: "it now."},
		{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockToolUse, ID: "toolu_01A", Name: "read_file", Input: json.RawMessage(`{"path": "a.txt"}`)}},
		{Kind: core.ChatUsage, Usage: core.Usage{InputTokens: 550, OutputTokens: 200, CachedInputTokens: 400}},
		{Kind: core.ChatStop, StopReason: "tool_use"},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events:\n%+v\nwant:\n%+v", events, want)
	}
	if api.path != "/v1/messages" {
		t.Fatalf("path=%q", api.path)
	}
	for name, value := range map[string]string{"X-Api-Key": testKey, "Anthropic-Version": "2023-06-01", "Content-Type": "application/json", "X-Extra": "1"} {
		if got := api.headers.Get(name); got != value {
			t.Fatalf("header %s=%q, want %q", name, got, value)
		}
	}
	if api.headers.Get("Authorization") != "" {
		t.Fatalf("authorization header sent")
	}
}

func TestStreamRedactedThinking(t *testing.T) {
	api := newFakeAPI(t, sseHandler(t, "redacted.sse"))
	a := newTestAdapter(t, api.server.URL, nil)
	stream, err := a.Stream(context.Background(), core.ChatRequest{Model: "claude-opus-4-8"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := collect(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].Kind != core.ChatBlock || events[0].Block.Signature != core.RedactedThinkingPrefix+"EmwKAhgBEgy3va3pzix" || events[0].Block.Text != "" {
		t.Fatalf("events=%+v", events)
	}
	if events[3].StopReason != "end_turn" || events[2].Usage != (core.Usage{InputTokens: 10, OutputTokens: 5}) {
		t.Fatalf("events=%+v", events)
	}
	// The block goes back as redacted_thinking.
	body, err := buildRequestBody(core.ChatRequest{Model: "claude-opus-4-8", Messages: []core.Message{
		{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "q"}}},
		{Role: core.RoleAssistant, Content: []core.ContentBlock{events[0].Block, {Kind: core.BlockText, Text: "Done."}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `{"data":"EmwKAhgBEgy3va3pzix","type":"redacted_thinking"}`) {
		t.Fatalf("body=%s", body)
	}
}

func TestStreamMidStreamError(t *testing.T) {
	api := newFakeAPI(t, sseHandler(t, "midstream_error.sse"))
	a := newTestAdapter(t, api.server.URL, nil)
	stream, err := a.Stream(context.Background(), core.ChatRequest{Model: "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := collect(t, stream)
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "server" || pe.Status != 529 || !strings.Contains(pe.Message, "overloaded_error") {
		t.Fatalf("err=%v", err)
	}
	if len(events) != 1 || events[0].Text != "Partial" {
		t.Fatalf("events=%+v", events)
	}
}

func TestStreamTruncatedIsNetwork(t *testing.T) {
	api := newFakeAPI(t, sseHandler(t, "truncated.sse"))
	a := newTestAdapter(t, api.server.URL, nil)
	stream, err := a.Stream(context.Background(), core.ChatRequest{Model: "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = collect(t, stream)
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "network" {
		t.Fatalf("err=%v", err)
	}
}

func TestStreamIdleTimeoutIsNetwork(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	api := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	a := newTestAdapter(t, api.server.URL, nil)
	stream, err := a.Stream(context.Background(), core.ChatRequest{Model: "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = collect(t, stream)
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "network" || !strings.Contains(pe.Message, "idle") {
		t.Fatalf("err=%v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("idle timeout took %s", time.Since(start))
	}
}

// A tool_use cut by max_tokens has invalid JSON input and is not emitted.
func TestStreamTruncatedToolUseDropped(t *testing.T) {
	api := newFakeAPI(t, sseHandler(t, "max_tokens_tool.sse"))
	a := newTestAdapter(t, api.server.URL, nil)
	stream, err := a.Stream(context.Background(), core.ChatRequest{Model: "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := collect(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Kind == core.ChatBlock {
			t.Fatalf("block emitted: %+v", ev)
		}
	}
	if last := events[len(events)-1]; last.StopReason != "max_tokens" {
		t.Fatalf("stop=%+v", last)
	}
}

// AC-RTR-003: a 429 with retry-after 7 is rate_limit with RetryAfter 7s; a
// 529 overloaded answer is server.
func TestHTTPErrors(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		retryAfter string
		class      string
		wait       time.Duration
	}{
		{"rate limit", 429, `{"type":"error","error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}`, "7", "rate_limit", 7 * time.Second},
		{"overloaded", 529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, "", "server", 0},
		{"internal", 500, `{"type":"error","error":{"type":"api_error","message":"Internal server error"}}`, "", "server", 0},
		{"unauthorized", 401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`, "", "auth", 0},
		{"forbidden", 403, `{"type":"error","error":{"type":"permission_error","message":"no"}}`, "", "auth", 0},
		{"not found", 404, `{"type":"error","error":{"type":"not_found_error","message":"model: claude-nope"}}`, "", core.ErrorClassNotFound, 0},
		{"too large", 413, `{"type":"error","error":{"type":"request_too_large","message":"Request exceeds the maximum allowed number of bytes."}}`, "", "context_length", 0},
		{"prompt too long", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 1000001 tokens > 1000000 maximum"}}`, "", "context_length", 0},
		{"bad request", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"messages: roles must alternate"}}`, "", "client", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			a := newTestAdapter(t, api.server.URL, nil)
			_, err := a.Stream(context.Background(), core.ChatRequest{Model: "claude-opus-5-5"})
			var pe *core.ProviderError
			if !errors.As(err, &pe) {
				t.Fatalf("err=%v", err)
			}
			if pe.Class != tc.class || pe.Status != tc.status || pe.RetryAfter != tc.wait {
				t.Fatalf("got class=%s status=%d wait=%s", pe.Class, pe.Status, pe.RetryAfter)
			}
		})
	}
}

// AC-RTR-013 (adapter side): the key never appears in an error message.
func TestErrorRedactsKey(t *testing.T) {
	api := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key `+testKey+`"}}`)
	})
	a := newTestAdapter(t, api.server.URL, nil)
	_, err := a.Stream(context.Background(), core.ChatRequest{Model: "claude-opus-5-5"})
	if err == nil || strings.Contains(err.Error(), testKey) || !strings.Contains(err.Error(), "***") {
		t.Fatalf("err=%v", err)
	}
}

func TestMissingKey(t *testing.T) {
	a, err := NewAdapter("claude", Config{}, mapSecrets{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Stream(context.Background(), core.ChatRequest{Model: "claude-opus-5-5"})
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "missing_secret" || !strings.Contains(pe.Message, DefaultAPIKeyEnv) {
		t.Fatalf("err=%v", err)
	}
	if a.base != DefaultBaseURL {
		t.Fatalf("base=%q", a.base)
	}
}

func TestRateLimitHeaders(t *testing.T) {
	sse := sseHandler(t, "redacted.sse")
	api := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("anthropic-ratelimit-requests-limit", "4000")
		h.Set("anthropic-ratelimit-requests-remaining", "3999")
		h.Set("anthropic-ratelimit-requests-reset", "2026-11-08T10:00:30Z")
		h.Set("anthropic-ratelimit-tokens-limit", "2000000")
		h.Set("anthropic-ratelimit-tokens-remaining", "1990000")
		h.Set("anthropic-ratelimit-input-tokens-limit", "1600000")
		h.Set("anthropic-ratelimit-input-tokens-remaining", "1595000")
		h.Set("anthropic-ratelimit-input-tokens-reset", "2026-11-08T10:00:01+00:00")
		h.Set("anthropic-ratelimit-output-tokens-limit", "400000")
		h.Set("anthropic-ratelimit-output-tokens-remaining", "395000")
		sse(w, r)
	})
	var got []router.LimitUpdate
	a := newTestAdapter(t, api.server.URL, func(u router.LimitUpdate) { got = append(got, u) })
	stream, err := a.Stream(context.Background(), core.ChatRequest{Model: "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, stream); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Provider != "claude" || got[0].Kind != Kind || got[0].Rate == nil {
		t.Fatalf("updates=%+v", got)
	}
	rate := got[0].Rate
	reset := time.Date(2026, 11, 8, 10, 0, 30, 0, time.UTC)
	if rate.Requests == nil || rate.Requests.Limit != 4000 || rate.Requests.Remaining != 3999 || rate.Requests.ResetAt == nil || !rate.Requests.ResetAt.Equal(reset) {
		t.Fatalf("requests=%+v", rate.Requests)
	}
	if rate.Tokens == nil || rate.Tokens.Limit != 2000000 || rate.Tokens.ResetAt != nil {
		t.Fatalf("tokens=%+v", rate.Tokens)
	}
	if rate.InputTokens == nil || rate.InputTokens.Remaining != 1595000 || rate.InputTokens.ResetAt == nil {
		t.Fatalf("input=%+v", rate.InputTokens)
	}
	if rate.OutputTokens == nil || rate.OutputTokens.Limit != 400000 {
		t.Fatalf("output=%+v", rate.OutputTokens)
	}

	// An error response reports its headers too; no header, no update.
	got = nil
	api2 := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("anthropic-ratelimit-requests-limit", "50")
		w.Header().Set("anthropic-ratelimit-requests-remaining", "0")
		w.WriteHeader(429)
	})
	a2 := newTestAdapter(t, api2.server.URL, func(u router.LimitUpdate) { got = append(got, u) })
	if _, err := a2.Stream(context.Background(), core.ChatRequest{Model: "claude-opus-5-5"}); err == nil {
		t.Fatal("want error")
	}
	if len(got) != 1 || got[0].Rate.Requests.Remaining != 0 || got[0].Rate.Tokens != nil {
		t.Fatalf("updates=%+v", got)
	}
	if parseRateLimits(http.Header{}) != nil {
		t.Fatal("empty headers gave limits")
	}
}

func TestListModelsPagination(t *testing.T) {
	var queries []string
	api := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Path != "/v1/models" || r.Method != http.MethodGet {
			w.WriteHeader(404)
			return
		}
		switch r.URL.Query().Get("after_id") {
		case "":
			_, _ = io.WriteString(w, `{"data":[{"type":"model","id":"claude-opus-5-5","display_name":"Claude Opus 5.5","max_input_tokens":1000000,"max_tokens":128000,"capabilities":{"image_input":{"supported":true},"thinking":{"supported":true}}},{"type":"model","id":"claude-sonnet-5-5","display_name":"Claude Sonnet 5.5"}],"has_more":true,"first_id":"claude-opus-5-5","last_id":"claude-sonnet-5-5"}`)
		case "claude-sonnet-5-5":
			_, _ = io.WriteString(w, `{"data":[{"type":"model","id":"claude-haiku-4-5-20251001","display_name":"Claude Haiku 4.5"}],"has_more":false,"first_id":"claude-haiku-4-5-20251001","last_id":"claude-haiku-4-5-20251001"}`)
		default:
			w.WriteHeader(400)
		}
	})
	a := newTestAdapter(t, api.server.URL+"/v1", nil)
	got, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []router.ModelInfo{
		{ID: "claude-opus-5-5", Name: "Claude Opus 5.5", ContextWindow: 1000000, MaxOutput: 128000, Vision: true, Thinking: true},
		{ID: "claude-sonnet-5-5", Name: "Claude Sonnet 5.5"},
		{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models=%+v", got)
	}
	if len(queries) != 2 || !strings.Contains(queries[1], "after_id=claude-sonnet-5-5") {
		t.Fatalf("queries=%v", queries)
	}
	if api.headers.Get("X-Api-Key") != testKey || api.headers.Get("Anthropic-Version") != apiVersion {
		t.Fatalf("headers=%v", api.headers)
	}
}

func TestListModelsError(t *testing.T) {
	api := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	a := newTestAdapter(t, api.server.URL, nil)
	_, err := a.ListModels(context.Background())
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "auth" {
		t.Fatalf("err=%v", err)
	}
}

func TestStreamErrorClasses(t *testing.T) {
	cases := map[string]string{
		"overloaded_error":      "server",
		"api_error":             "server",
		"rate_limit_error":      "rate_limit",
		"authentication_error":  "auth",
		"permission_error":      "auth",
		"not_found_error":       core.ErrorClassNotFound,
		"request_too_large":     "context_length",
		"invalid_request_error": "client",
		"something_new":         "server",
	}
	for errType, class := range cases {
		if got := classifyStreamError(errType, "m", "").Class; got != class {
			t.Fatalf("%s: class=%s want %s", errType, got, class)
		}
	}
	if got := classifyStreamError("invalid_request_error", "prompt is too long", "").Class; got != "context_length" {
		t.Fatalf("prompt too long class=%s", got)
	}
}
