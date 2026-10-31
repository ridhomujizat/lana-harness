package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

func usageStream(t *testing.T, usageJSON string) *core.Usage {
	t.Helper()
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[],\"usage\":" + usageJSON + "}\n\n" +
		"data: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	adapter, err := NewAdapter("p", Config{BaseURL: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Stream(context.Background(), core.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	for ev, err := range stream {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		if ev.Kind == core.ChatUsage {
			u := ev.Usage
			return &u
		}
	}
	t.Fatal("no usage event")
	return nil
}

func TestSSECachedUsageDeepSeek(t *testing.T) {
	usage := usageStream(t, `{"prompt_tokens":100,"completion_tokens":5,"prompt_cache_hit_tokens":80}`)
	if usage.InputTokens != 100 || usage.OutputTokens != 5 || usage.CachedInputTokens != 80 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestSSECachedUsageOpenAI(t *testing.T) {
	usage := usageStream(t, `{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":64}}`)
	if usage.InputTokens != 100 || usage.CachedInputTokens != 64 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestClassifyContextLength(t *testing.T) {
	deepseek := `{"error":{"message":"This model's maximum context length is 131072 tokens. However, you requested 140000 tokens.","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"deepseek 400 message", http.StatusBadRequest, deepseek, "context_length"},
		{"413 too many tokens", http.StatusRequestEntityTooLarge, `{"error":{"message":"too many tokens"}}`, "context_length"},
		{"400 context window", http.StatusBadRequest, `{"error":{"message":"This exceeds the context window"}}`, "context_length"},
		{"400 unmatched", http.StatusBadRequest, `{"error":{"message":"bad request"}}`, "client"},
		{"400 code only", http.StatusBadRequest, `{"error":{"code":"context_length_exceeded"}}`, "context_length"},
	}
	for _, tc := range cases {
		got := classifyHTTP(tc.status, []byte(tc.body))
		if got.Class != tc.want {
			t.Fatalf("%s: class=%s want %s", tc.name, got.Class, tc.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("3"); d != 3*time.Second {
		t.Fatalf("delta-seconds: %v", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Fatalf("empty: %v", d)
	}
	if d := parseRetryAfter("0"); d != 0 {
		t.Fatalf("zero: %v", d)
	}
	future := time.Now().Add(10 * time.Second).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(future); d <= 0 || d > 10*time.Second {
		t.Fatalf("http-date: %v", d)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(past); d != 0 {
		t.Fatalf("past date: %v", d)
	}
}

func TestRetryAfterHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	adapter, err := NewAdapter("p", Config{BaseURL: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Stream(context.Background(), core.ChatRequest{Model: "m"})
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "rate_limit" || pe.RetryAfter != 3*time.Second {
		t.Fatalf("err=%v", err)
	}
}

func TestStreamIdleTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
		flusher.Flush()
		<-release
	}))
	defer srv.Close()
	defer close(release)

	adapter, err := NewAdapter("p", Config{BaseURL: srv.URL, Timeout: "5s", StreamIdleTimeout: "100ms"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Stream(context.Background(), core.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var got error
	for _, err := range stream {
		if err != nil {
			got = err
			break
		}
	}
	var pe *core.ProviderError
	if !errors.As(got, &pe) || pe.Class != "network" || !strings.Contains(pe.Message, "stream idle for 100ms") {
		t.Fatalf("err=%v", got)
	}
}

func TestStreamSteadyOutlivesTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		for i := 0; i < 8; i++ {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"},\"finish_reason\":null}]}\n\n")
			flusher.Flush()
			time.Sleep(40 * time.Millisecond)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	// A short response-header timeout and a long stream: only a total limit
	// would cut this, and there is none.
	adapter, err := NewAdapter("p", Config{BaseURL: srv.URL, Timeout: "100ms", StreamIdleTimeout: "5s"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Stream(context.Background(), core.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, stream)
	var deltas int
	for _, ev := range events {
		if ev.Kind == core.ChatTextDelta {
			deltas++
		}
	}
	if deltas != 8 {
		t.Fatalf("deltas=%d", deltas)
	}
}
