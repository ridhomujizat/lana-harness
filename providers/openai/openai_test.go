package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{401, "", "auth"},
		{403, "", "auth"},
		{429, "", "rate_limit"},
		{500, "", "server"},
		{503, "", "server"},
		{400, `{"error":{"code":"context_length_exceeded"}}`, "context_length"},
		{400, `{"error":{"code":"invalid"}}`, "client"},
		{404, "", "client"},
		{422, "", "client"},
	}
	for _, tc := range cases {
		got := classifyHTTP(tc.status, []byte(tc.body))
		if got.Class != tc.want || got.Status != tc.status {
			t.Fatalf("status %d: %+v want %s", tc.status, got, tc.want)
		}
	}
}

func serveFixture(t *testing.T, name string) *httptest.Server {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	}))
}

func collect(t *testing.T, stream core.ChatStream) []core.ChatEvent {
	t.Helper()
	var events []core.ChatEvent
	for event, err := range stream {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		events = append(events, event)
	}
	return events
}

func TestConfigOpenAICompat(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "text.sse"))
	if err != nil {
		t.Fatal(err)
	}
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	adapter, err := NewAdapter("openrouter", Config{BaseURL: srv.URL + "/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Stream(context.Background(), core.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, stream)
	if path != "/v1/chat/completions" {
		t.Fatalf("path=%q", path)
	}
	var text strings.Builder
	for _, ev := range events {
		if ev.Kind == core.ChatTextDelta {
			text.WriteString(ev.Text)
		}
	}
	if text.String() != "Hello world!" {
		t.Fatalf("text=%q", text.String())
	}
}

func TestNoAuthHeaderWithoutEnv(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
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
	collect(t, stream)
	if auth != "" {
		t.Fatalf("Authorization was sent: %q", auth)
	}
}

func TestSSETextDeltas(t *testing.T) {
	srv := serveFixture(t, "text.sse")
	defer srv.Close()
	adapter, _ := NewAdapter("p", Config{BaseURL: srv.URL}, nil)
	stream, err := adapter.Stream(context.Background(), core.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, stream)
	var deltas []string
	sawStop := false
	for _, ev := range events {
		switch ev.Kind {
		case core.ChatTextDelta:
			deltas = append(deltas, ev.Text)
		case core.ChatStop:
			sawStop = true
			if ev.StopReason != "end_turn" {
				t.Fatalf("stop=%q", ev.StopReason)
			}
		}
	}
	if len(deltas) != 3 || strings.Join(deltas, "") != "Hello world!" || !sawStop {
		t.Fatalf("deltas=%v sawStop=%v", deltas, sawStop)
	}
}

func TestSSEToolCallAccumulate(t *testing.T) {
	srv := serveFixture(t, "tool.sse")
	defer srv.Close()
	adapter, _ := NewAdapter("p", Config{BaseURL: srv.URL}, nil)
	stream, err := adapter.Stream(context.Background(), core.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, stream)
	var block *core.ContentBlock
	for _, ev := range events {
		if ev.Kind == core.ChatBlock {
			b := ev.Block
			block = &b
		}
	}
	if block == nil || block.Kind != core.BlockToolUse || block.ID != "call_1" || block.Name != "read_file" {
		t.Fatalf("block=%+v", block)
	}
	if string(block.Input) != `{"path":"a.txt"}` {
		t.Fatalf("input=%s", block.Input)
	}
}

func TestSSEUsage(t *testing.T) {
	srv := serveFixture(t, "usage.sse")
	defer srv.Close()
	adapter, _ := NewAdapter("p", Config{BaseURL: srv.URL}, nil)
	stream, err := adapter.Stream(context.Background(), core.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, stream)
	var usage *core.Usage
	for _, ev := range events {
		if ev.Kind == core.ChatUsage {
			u := ev.Usage
			usage = &u
		}
	}
	if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 5 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestWireConvert(t *testing.T) {
	temp := 0.2
	req := core.ChatRequest{
		Model:  "gpt",
		System: "sys",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hello"}}},
			{Role: core.RoleAssistant, Content: []core.ContentBlock{
				{Kind: core.BlockText, Text: "ok"},
				{Kind: core.BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a"}`)},
			}},
			{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockToolResult, ID: "c1", Output: "content"}}},
		},
		Tools:       []core.ToolSpec{{Name: "read_file", Description: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		MaxTokens:   100,
		Temperature: &temp,
	}
	body, err := buildRequestBody(req, formatNone)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "gpt" || got["stream"] != true || got["max_tokens"].(float64) != 100 {
		t.Fatalf("body=%v", got)
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("messages=%d %v", len(msgs), msgs)
	}
	system := msgs[0].(map[string]any)
	if system["role"] != "system" || system["content"] != "sys" {
		t.Fatalf("system=%v", system)
	}
	assistant := msgs[2].(map[string]any)
	calls := assistant["tool_calls"].([]any)
	call := calls[0].(map[string]any)
	fn := call["function"].(map[string]any)
	if fn["arguments"] != `{"path":"a"}` {
		t.Fatalf("arguments=%v", fn["arguments"])
	}
	toolMsg := msgs[3].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "c1" || toolMsg["content"] != "content" {
		t.Fatalf("tool=%v", toolMsg)
	}
}

func TestWireImageUnsupported(t *testing.T) {
	req := core.ChatRequest{Model: "m", Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockImage}}}}}
	if _, err := buildRequestBody(req, formatNone); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
}

func TestProviderErrorHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	adapter, _ := NewAdapter("p", Config{BaseURL: srv.URL}, nil)
	_, err := adapter.Stream(context.Background(), core.ChatRequest{Model: "m"})
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "auth" {
		t.Fatalf("err=%v", err)
	}
}
