package openaiproxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

// The fake kind looks up its provider by base URL, so each test owns its
// providers.
func init() {
	router.RegisterKind("proxyfake", func(name string, cfg core.ProviderConfig, _ router.Deps) (router.Adapter, error) {
		fakesMu.Lock()
		defer fakesMu.Unlock()
		p, ok := fakes[cfg.BaseURL]
		if !ok {
			return nil, errors.New("no fake provider for " + cfg.BaseURL)
		}
		return p, nil
	})
}

var (
	fakesMu sync.Mutex
	fakes   = map[string]*fakeProvider{}
)

type fakeProvider struct {
	mu sync.Mutex
	// err is returned by every Stream call.
	err error
	// events are the stream; midErr is yielded after them.
	events []core.ChatEvent
	midErr error

	models  []router.ModelInfo
	listErr error

	calls int
	last  core.ChatRequest
}

func (p *fakeProvider) Stream(_ context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.last = req
	if p.err != nil {
		return nil, p.err
	}
	events := append([]core.ChatEvent(nil), p.events...)
	midErr := p.midErr
	return func(yield func(core.ChatEvent, error) bool) {
		for _, e := range events {
			if !yield(e, nil) {
				return
			}
		}
		if midErr != nil {
			yield(core.ChatEvent{}, midErr)
		}
	}, nil
}

func (p *fakeProvider) ListModels(context.Context) ([]router.ModelInfo, error) {
	return p.models, p.listErr
}

func (p *fakeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *fakeProvider) lastRequest() core.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

type recorder struct {
	mu   sync.Mutex
	recs []router.CallRecord
}

func (r *recorder) Record(rec router.CallRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec)
}

func (r *recorder) records() []router.CallRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]router.CallRecord(nil), r.recs...)
}

const testKey = "lrk_test"

func testAuth(bearer string) (string, bool) {
	if bearer == testKey {
		return "k1", true
	}
	return "", false
}

type env struct {
	router *router.Router
	rec    *recorder
	srv    *httptest.Server
	used   []string
	mu     sync.Mutex
}

// newEnv builds a router over the fake providers and serves the proxy at
// /router/v1/ the way lana's web server mounts it.
func newEnv(t *testing.T, providers map[string]*fakeProvider, combos map[string]router.Combo, opts ...Option) *env {
	t.Helper()
	cfg := router.Config{Providers: map[string]core.ProviderConfig{}, Combos: combos}
	for name, p := range providers {
		url := "fake://" + t.Name() + "/" + name
		fakesMu.Lock()
		fakes[url] = p
		fakesMu.Unlock()
		t.Cleanup(func() {
			fakesMu.Lock()
			delete(fakes, url)
			fakesMu.Unlock()
		})
		cfg.Providers[name] = core.ProviderConfig{Kind: "proxyfake", BaseURL: url}
	}
	rec := &recorder{}
	rt, err := router.New(cfg, router.WithRecorder(rec), router.WithSleep(func(context.Context, time.Duration) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	e := &env{router: rt, rec: rec}
	opts = append([]Option{
		WithOnRequest(func(keyID string) {
			e.mu.Lock()
			e.used = append(e.used, keyID)
			e.mu.Unlock()
		}),
		withClock(func() time.Time { return time.Unix(1700000000, 0) }),
	}, opts...)
	mux := http.NewServeMux()
	mux.Handle("/router/v1/", http.StripPrefix("/router/v1", NewHandler(rt, testAuth, opts...)))
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) post(t *testing.T, body any, headers ...string) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/router/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if len(headers) == 0 {
		headers = []string{"Authorization", "Bearer " + testKey}
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeError(t *testing.T, resp *http.Response) errorDetail {
	t.Helper()
	var body apiError
	if err := json.Unmarshal(readBody(t, resp), &body); err != nil {
		t.Fatalf("error body: %v", err)
	}
	if body.Error.Message == "" || body.Error.Type == "" {
		t.Fatalf("error body is incomplete: %+v", body)
	}
	return body.Error
}

// sseData returns the payloads of the "data:" lines.
func sseData(t *testing.T, body []byte) []string {
	t.Helper()
	var out []string
	for _, event := range strings.Split(strings.TrimSpace(string(body)), "\n\n") {
		payload, ok := strings.CutPrefix(event, "data: ")
		if !ok {
			t.Fatalf("event without data: %q", event)
		}
		out = append(out, payload)
	}
	return out
}

func userMsg(text string) map[string]any { return map[string]any{"role": "user", "content": text} }

func textEvents(parts ...string) []core.ChatEvent {
	var events []core.ChatEvent
	for _, p := range parts {
		events = append(events, core.ChatEvent{Kind: core.ChatTextDelta, Text: p})
	}
	return events
}

// AC-RTR-020: a streamed call by combo name falls back, ends with [DONE],
// names the served target, and is recorded with source proxy.
func TestStreamingCombo(t *testing.T) {
	down := &fakeProvider{err: &core.ProviderError{Class: "auth", Status: 401, Message: "bad key"}}
	events := []core.ChatEvent{{Kind: core.ChatThinkingDelta, Text: "thinking"}}
	events = append(events, textEvents("Hel", "lo")...)
	events = append(events,
		core.ChatEvent{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockThinking, Text: "thinking"}},
		core.ChatEvent{Kind: core.ChatUsage, Usage: core.Usage{InputTokens: 10, OutputTokens: 5, CachedInputTokens: 4}},
		core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"},
	)
	up := &fakeProvider{events: events}
	e := newEnv(t, map[string]*fakeProvider{"a": down, "b": up},
		map[string]router.Combo{"coding": {Models: []string{"a/m1", "b/m2"}}})

	resp := e.post(t, map[string]any{
		"model": "coding", "stream": true, "stream_options": map[string]any{"include_usage": true},
		"messages": []any{userMsg("hi")}, "tool_choice": "auto", "unknown_field": 1,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	if got := resp.Header.Get(HeaderServedBy); got != "b/m2" {
		t.Fatalf("served by = %q", got)
	}
	data := sseData(t, readBody(t, resp))
	if data[len(data)-1] != "[DONE]" {
		t.Fatalf("last event = %q", data[len(data)-1])
	}
	var chunks []chunk
	for _, d := range data[:len(data)-1] {
		var c chunk
		if err := json.Unmarshal([]byte(d), &c); err != nil {
			t.Fatalf("chunk %q: %v", d, err)
		}
		chunks = append(chunks, c)
	}
	first := chunks[0]
	if first.Object != "chat.completion.chunk" || first.Model != "coding" || !strings.HasPrefix(first.ID, "chatcmpl-") || first.Created != 1700000000 {
		t.Fatalf("first chunk = %+v", first)
	}
	if first.Choices[0].Delta.Role != "assistant" {
		t.Fatalf("first delta = %+v", first.Choices[0].Delta)
	}
	var content, reasoning strings.Builder
	var finish string
	var u *usage
	for _, c := range chunks {
		if c.ID != first.ID {
			t.Fatalf("chunk id %q != %q", c.ID, first.ID)
		}
		if c.Usage != nil {
			u = c.Usage
			if len(c.Choices) != 0 {
				t.Fatalf("usage chunk has choices: %+v", c)
			}
			continue
		}
		d := c.Choices[0].Delta
		if d.Content != nil {
			content.WriteString(*d.Content)
		}
		if d.ReasoningContent != nil {
			reasoning.WriteString(*d.ReasoningContent)
		}
		if f := c.Choices[0].FinishReason; f != nil {
			finish = *f
		}
	}
	if content.String() != "Hello" || reasoning.String() != "thinking" || finish != "stop" {
		t.Fatalf("content %q reasoning %q finish %q", content.String(), reasoning.String(), finish)
	}
	if u == nil || u.PromptTokens != 10 || u.CompletionTokens != 5 || u.TotalTokens != 15 || u.PromptTokensDetails.CachedTokens != 4 {
		t.Fatalf("usage = %+v", u)
	}

	var ok *router.CallRecord
	recs := e.rec.records()
	for i := range recs {
		if recs[i].Source != "proxy" || recs[i].Agent != "proxy:k1" || recs[i].TurnID != first.ID {
			t.Fatalf("record meta = %+v", recs[i])
		}
		if recs[i].Outcome == "ok" {
			ok = &recs[i]
		}
	}
	if ok == nil || ok.Provider != "b" || ok.Model != "m2" || ok.InputTokens != 10 {
		t.Fatalf("records = %+v", recs)
	}
	if down.callCount() != 1 || up.callCount() != 1 {
		t.Fatalf("calls a=%d b=%d", down.callCount(), up.callCount())
	}
	if up.lastRequest().Model != "m2" {
		t.Fatalf("upstream model = %q", up.lastRequest().Model)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.used) != 1 || e.used[0] != "k1" {
		t.Fatalf("on request = %v", e.used)
	}
}

// AC-RTR-021: no key or a wrong key gets 401 and nothing is called upstream.
func TestUnauthorized(t *testing.T) {
	p := &fakeProvider{events: textEvents("hi")}
	e := newEnv(t, map[string]*fakeProvider{"a": p}, nil)
	body := map[string]any{"model": "a/m", "messages": []any{userMsg("hi")}}
	for name, headers := range map[string][]string{
		"none":          {"X-None", "1"},
		"wrong bearer":  {"Authorization", "Bearer nope"},
		"not bearer":    {"Authorization", "Basic " + testKey},
		"wrong api key": {"x-api-key", "nope"},
	} {
		t.Run(name, func(t *testing.T) {
			resp := e.post(t, body, headers...)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if got := decodeError(t, resp); got.Code != "invalid_api_key" || got.Type != "invalid_request_error" {
				t.Fatalf("error = %+v", got)
			}
		})
	}
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/router/v1/models", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("models status = %d", resp.StatusCode)
	}
	if p.callCount() != 0 || len(e.rec.records()) != 0 || len(e.used) != 0 {
		t.Fatalf("upstream called: calls=%d records=%d used=%v", p.callCount(), len(e.rec.records()), e.used)
	}
	// x-api-key with the right key works.
	resp = e.post(t, body, "x-api-key", testKey)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("x-api-key status = %d", resp.StatusCode)
	}
}

func TestNonStreamingToolCalls(t *testing.T) {
	p := &fakeProvider{events: []core.ChatEvent{
		{Kind: core.ChatTextDelta, Text: "Let me check."},
		{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockToolUse, ID: "call_1", Name: "weather", Input: json.RawMessage(`{"city":"Paris"}`)}},
		{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockToolUse, ID: "call_2", Name: "time"}},
		{Kind: core.ChatUsage, Usage: core.Usage{InputTokens: 7, OutputTokens: 3}},
		{Kind: core.ChatStop, StopReason: "tool_use"},
	}}
	e := newEnv(t, map[string]*fakeProvider{"a": p}, nil)
	resp := e.post(t, map[string]any{"model": "a/m", "messages": []any{userMsg("weather?")}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	if got := resp.Header.Get(HeaderServedBy); got != "a/m" {
		t.Fatalf("served by = %q", got)
	}
	raw := readBody(t, resp)
	if bytes.Contains(raw, []byte(`"index":0,"id"`)) {
		t.Fatalf("non-streaming tool call carries an index: %s", raw)
	}
	var got completion
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Object != "chat.completion" || got.Model != "a/m" || len(got.Choices) != 1 {
		t.Fatalf("completion = %s", raw)
	}
	choice := got.Choices[0]
	if choice.FinishReason != "tool_calls" || choice.Message.Role != "assistant" || choice.Message.Content == nil || *choice.Message.Content != "Let me check." {
		t.Fatalf("choice = %s", raw)
	}
	calls := choice.Message.ToolCalls
	if len(calls) != 2 || calls[0].ID != "call_1" || calls[0].Type != "function" || calls[0].Function.Name != "weather" ||
		calls[0].Function.Arguments != `{"city":"Paris"}` || calls[1].Function.Arguments != "{}" {
		t.Fatalf("tool calls = %+v", calls)
	}
	if got.Usage == nil || got.Usage.TotalTokens != 10 {
		t.Fatalf("usage = %+v", got.Usage)
	}
}

func TestStreamingToolCalls(t *testing.T) {
	p := &fakeProvider{events: []core.ChatEvent{
		{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockToolUse, ID: "call_1", Name: "a", Input: json.RawMessage(`{}`)}},
		{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockToolUse, ID: "call_2", Name: "b", Input: json.RawMessage(`{"x":1}`)}},
		{Kind: core.ChatStop, StopReason: "tool_use"},
	}}
	e := newEnv(t, map[string]*fakeProvider{"a": p}, nil)
	resp := e.post(t, map[string]any{"model": "a/m", "stream": true, "messages": []any{userMsg("go")}})
	data := sseData(t, readBody(t, resp))
	if data[len(data)-1] != "[DONE]" {
		t.Fatalf("last = %q", data[len(data)-1])
	}
	var calls []toolCall
	finish := ""
	for _, d := range data[:len(data)-1] {
		var c chunk
		if err := json.Unmarshal([]byte(d), &c); err != nil {
			t.Fatal(err)
		}
		if c.Usage != nil {
			t.Fatalf("usage chunk without include_usage: %s", d)
		}
		calls = append(calls, c.Choices[0].Delta.ToolCalls...)
		if c.Choices[0].FinishReason != nil {
			finish = *c.Choices[0].FinishReason
		}
	}
	if len(calls) != 2 || *calls[0].Index != 0 || *calls[1].Index != 1 || calls[1].ID != "call_2" ||
		calls[1].Type != "function" || calls[1].Function.Name != "b" || calls[1].Function.Arguments != `{"x":1}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish = %q", finish)
	}
}

func TestToolRoundTripTranslation(t *testing.T) {
	p := &fakeProvider{events: textEvents("ok")}
	e := newEnv(t, map[string]*fakeProvider{"a": p}, nil)
	resp := e.post(t, map[string]any{
		"model": "a/m",
		"messages": []any{
			map[string]any{"role": "system", "content": "You are terse."},
			map[string]any{"role": "developer", "content": []any{map[string]any{"type": "text", "text": "Use tools."}}},
			userMsg("weather in Paris and time?"),
			map[string]any{"role": "assistant", "content": nil, "reasoning_content": "need tools", "tool_calls": []any{
				map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "weather", "arguments": `{"city":"Paris"}`}},
				map[string]any{"id": "c2", "type": "function", "function": map[string]any{"name": "time", "arguments": ""}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c1", "content": "sunny"},
			map[string]any{"role": "tool", "tool_call_id": "c2", "content": []any{map[string]any{"type": "text", "text": "noon"}}},
			map[string]any{"role": "assistant", "content": "Sunny at noon."},
			userMsg("thanks"),
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "weather", "description": "Weather", "parameters": map[string]any{"type": "object"}}},
			map[string]any{"type": "function", "function": map[string]any{"name": "time"}},
		},
		"max_tokens": 100, "max_completion_tokens": 200, "temperature": 0.5,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	req := p.lastRequest()
	if req.System != "You are terse.\n\nUse tools." {
		t.Fatalf("system = %q", req.System)
	}
	if req.MaxTokens != 200 || req.Temperature == nil || *req.Temperature != 0.5 {
		t.Fatalf("max tokens %d temperature %v", req.MaxTokens, req.Temperature)
	}
	if len(req.Tools) != 2 || req.Tools[0].Name != "weather" || string(req.Tools[0].InputSchema) != `{"type":"object"}` ||
		string(req.Tools[1].InputSchema) != string(emptySchema) {
		t.Fatalf("tools = %+v", req.Tools)
	}
	msgs := req.Messages
	if len(msgs) != 5 {
		t.Fatalf("messages = %+v", msgs)
	}
	if msgs[0].Role != core.RoleUser || msgs[0].Content[0].Text != "weather in Paris and time?" {
		t.Fatalf("message 0 = %+v", msgs[0])
	}
	asst := msgs[1]
	if asst.Role != core.RoleAssistant || len(asst.Content) != 3 ||
		asst.Content[0].Kind != core.BlockThinking || asst.Content[0].Text != "need tools" ||
		asst.Content[1].Kind != core.BlockToolUse || asst.Content[1].ID != "c1" || string(asst.Content[1].Input) != `{"city":"Paris"}` ||
		asst.Content[2].Name != "time" || string(asst.Content[2].Input) != "{}" {
		t.Fatalf("assistant = %+v", asst)
	}
	results := msgs[2]
	if results.Role != core.RoleUser || len(results.Content) != 2 ||
		results.Content[0].Kind != core.BlockToolResult || results.Content[0].ID != "c1" || results.Content[0].Output != "sunny" ||
		results.Content[1].ID != "c2" || results.Content[1].Output != "noon" {
		t.Fatalf("tool results = %+v", results)
	}
	if msgs[3].Role != core.RoleAssistant || msgs[3].Content[0].Text != "Sunny at noon." || msgs[4].Content[0].Text != "thanks" {
		t.Fatalf("tail = %+v", msgs[3:])
	}
}

func TestImages(t *testing.T) {
	p := &fakeProvider{events: textEvents("a cat")}
	e := newEnv(t, map[string]*fakeProvider{"a": p}, nil)
	png := []byte("\x89PNG\r\n\x1a\nfake")
	parts := func(url string) map[string]any {
		return map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "what is this?"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": url, "detail": "high"}},
		}}
	}
	resp := e.post(t, map[string]any{"model": "a/m", "messages": []any{parts("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	blocks := p.lastRequest().Messages[0].Content
	if len(blocks) != 2 || blocks[1].Kind != core.BlockImage || blocks[1].MediaType != "image/png" || !bytes.Equal(blocks[1].Data, png) {
		t.Fatalf("blocks = %+v", blocks)
	}
	calls := p.callCount()
	for url, code := range map[string]string{
		"https://example.com/cat.png": "unsupported_value",
		"data:image/png,notbase64":    "invalid_value",
		"data:text/plain;base64,aGk=": "invalid_value",
	} {
		resp := e.post(t, map[string]any{"model": "a/m", "messages": []any{parts(url)}})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d", url, resp.StatusCode)
		}
		if got := decodeError(t, resp); got.Code != code {
			t.Fatalf("%s: error = %+v", url, got)
		}
	}
	if p.callCount() != calls {
		t.Fatal("a rejected request reached upstream")
	}
}

func TestReasoningEffort(t *testing.T) {
	p := &fakeProvider{events: textEvents("ok")}
	e := newEnv(t, map[string]*fakeProvider{"a": p}, nil)
	for in, want := range map[string]string{
		"": "", "none": "off", "minimal": "low", "low": "low", "medium": "high", "high": "high", "xhigh": "max",
	} {
		resp := e.post(t, map[string]any{"model": "a/m", "reasoning_effort": in, "messages": []any{userMsg("hi")}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%q: status = %d", in, resp.StatusCode)
		}
		if got := p.lastRequest().ReasoningEffort; got != want {
			t.Fatalf("%q: effort = %q, want %q", in, got, want)
		}
	}
	resp := e.post(t, map[string]any{"model": "a/m", "reasoning_effort": "turbo", "messages": []any{userMsg("hi")}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid effort status = %d", resp.StatusCode)
	}
}

func TestBadRequests(t *testing.T) {
	p := &fakeProvider{events: textEvents("ok")}
	e := newEnv(t, map[string]*fakeProvider{"a": p}, nil)
	for name, body := range map[string]map[string]any{
		"n=2":         {"model": "a/m", "n": 2, "messages": []any{userMsg("hi")}},
		"no messages": {"model": "a/m", "messages": []any{}},
		"no model":    {"messages": []any{userMsg("hi")}},
		"bad role":    {"model": "a/m", "messages": []any{map[string]any{"role": "function", "content": "x"}}},
		"bad args": {"model": "a/m", "messages": []any{map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"id": "c", "type": "function", "function": map[string]any{"name": "f", "arguments": "{"}}}}}},
	} {
		resp := e.post(t, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d", name, resp.StatusCode)
		}
		if got := decodeError(t, resp); got.Type != "invalid_request_error" {
			t.Fatalf("%s: error = %+v", name, got)
		}
	}
	if p.callCount() != 0 {
		t.Fatal("a rejected request reached upstream")
	}
}

func TestBodyTooLarge(t *testing.T) {
	e := newEnv(t, map[string]*fakeProvider{"a": {events: textEvents("ok")}}, nil, WithMaxBodyBytes(64))
	resp := e.post(t, map[string]any{"model": "a/m", "messages": []any{userMsg(strings.Repeat("x", 200))}})
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestModelNotFound(t *testing.T) {
	p := &fakeProvider{events: textEvents("ok")}
	e := newEnv(t, map[string]*fakeProvider{"a": p}, nil)
	for _, model := range []string{"nope", "zzz/model"} {
		resp := e.post(t, map[string]any{"model": model, "messages": []any{userMsg("hi")}})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: status = %d", model, resp.StatusCode)
		}
		if got := decodeError(t, resp); got.Code != "model_not_found" {
			t.Fatalf("%s: error = %+v", model, got)
		}
	}
	if p.callCount() != 0 {
		t.Fatal("upstream called")
	}
}

func TestUpstreamErrors(t *testing.T) {
	cases := []struct {
		err    *core.ProviderError
		status int
		code   string
	}{
		{&core.ProviderError{Class: "rate_limit", Status: 429, Message: "slow down", RetryAfter: 3 * time.Second}, 429, "rate_limit"},
		{&core.ProviderError{Class: "auth", Status: 401, Message: "bad key"}, 502, "upstream_auth"},
		{&core.ProviderError{Class: "server", Status: 500, Message: "boom"}, 502, "server"},
		{&core.ProviderError{Class: "network", Message: "read: i/o timeout"}, 504, "timeout"},
		{&core.ProviderError{Class: "client", Status: 400, Message: "bad"}, 400, "client"},
		{&core.ProviderError{Class: "context_length", Status: 400, Message: "too long"}, 400, "context_length_exceeded"},
		{&core.ProviderError{Class: "client", Status: 404, Message: "no such model"}, 404, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.err.Class+"_"+tc.code, func(t *testing.T) {
			p := &fakeProvider{err: tc.err}
			e := newEnv(t, map[string]*fakeProvider{"a": p}, nil)
			resp := e.post(t, map[string]any{"model": "a/m", "stream": true, "messages": []any{userMsg("hi")}})
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("content type = %q", ct)
			}
			if resp.Header.Get(HeaderServedBy) != "" {
				t.Fatal("served-by set on an error")
			}
			if tc.status == 429 && resp.Header.Get("Retry-After") != "3" {
				t.Fatalf("Retry-After = %q", resp.Header.Get("Retry-After"))
			}
			if got := decodeError(t, resp); got.Code != tc.code {
				t.Fatalf("error = %+v", got)
			}
			if p.callCount() == 0 {
				t.Fatal("upstream not called")
			}
		})
	}
}

func TestMidStreamError(t *testing.T) {
	p := &fakeProvider{events: textEvents("partial"), midErr: &core.ProviderError{Class: "server", Message: "stream broke"}}
	e := newEnv(t, map[string]*fakeProvider{"a": p}, nil)
	resp := e.post(t, map[string]any{"model": "a/m", "stream": true, "messages": []any{userMsg("hi")}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	data := sseData(t, readBody(t, resp))
	last := data[len(data)-1]
	var body apiError
	if err := json.Unmarshal([]byte(last), &body); err != nil || body.Error.Code != "server" || !strings.Contains(body.Error.Message, "stream broke") {
		t.Fatalf("last event = %q (%v)", last, err)
	}
	for _, d := range data {
		if d == "[DONE]" {
			t.Fatal("[DONE] after an error")
		}
	}
	recs := e.rec.records()
	if len(recs) != 1 || recs[0].Outcome != "server" {
		t.Fatalf("records = %+v", recs)
	}

	// Non-streaming: the error becomes the response status.
	resp = e.post(t, map[string]any{"model": "a/m", "messages": []any{userMsg("hi")}})
	if resp.StatusCode != http.StatusBadGateway || resp.Header.Get(HeaderServedBy) != "" {
		t.Fatalf("status = %d served-by %q", resp.StatusCode, resp.Header.Get(HeaderServedBy))
	}
	if got := decodeError(t, resp); got.Code != "server" {
		t.Fatalf("error = %+v", got)
	}
}

func TestModelsList(t *testing.T) {
	a := &fakeProvider{models: []router.ModelInfo{{ID: "m1"}, {ID: "org/m2"}}}
	b := &fakeProvider{listErr: errors.New("models endpoint down")}
	c := &fakeProvider{models: []router.ModelInfo{{ID: "x"}}}
	e := newEnv(t, map[string]*fakeProvider{"a": a, "b": b, "c": c},
		map[string]router.Combo{"zeta": {Models: []string{"a/m1"}}, "coding": {Models: []string{"c/x", "a/m1"}}})
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/router/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var list modelList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range list.Data {
		if m.Object != "model" {
			t.Fatalf("object = %q", m.Object)
		}
		got = append(got, m.ID+"@"+m.OwnedBy)
	}
	want := []string{"coding@combo", "zeta@combo", "a/m1@a", "a/org/m2@a", "c/x@c"}
	if strings.Join(got, ",") != strings.Join(want, ",") || list.Object != "list" {
		t.Fatalf("models = %v, want %v", got, want)
	}
}

func TestRouting(t *testing.T) {
	p := &fakeProvider{events: textEvents("ok")}
	rt := newEnv(t, map[string]*fakeProvider{"a": p}, nil).router
	body := `{"model":"a/m","messages":[{"role":"user","content":"hi"}]}`
	do := func(h http.Handler, method, path string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "bearer "+testKey)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	prefixed := NewHandler(rt, testAuth, WithPrefix("/router/v1/"))
	stripped := http.StripPrefix("/router/v1/", NewHandler(rt, testAuth))
	for _, tc := range []struct {
		h      http.Handler
		method string
		path   string
		want   int
	}{
		{prefixed, http.MethodPost, "/router/v1/chat/completions", 200},
		{prefixed, http.MethodGet, "/router/v1/models", 200},
		{prefixed, http.MethodPost, "/chat/completions", 404},
		{prefixed, http.MethodPost, "/router/v1x/chat/completions", 404},
		{prefixed, http.MethodGet, "/router/v1/chat/completions", 405},
		{stripped, http.MethodPost, "/router/v1/chat/completions", 200},
		{stripped, http.MethodGet, "/router/v1/models/", 200},
		{stripped, http.MethodPost, "/router/v1/embeddings", 404},
	} {
		if got := do(tc.h, tc.method, tc.path); got != tc.want {
			t.Fatalf("%s %s = %d, want %d", tc.method, tc.path, got, tc.want)
		}
	}
	// A nil auth rejects everything.
	if got := do(NewHandler(rt, nil), http.MethodGet, "/models"); got != 401 {
		t.Fatalf("nil auth = %d", got)
	}
}
