package commandcode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

type secrets map[string]string

func (s secrets) Lookup(name string) (core.Secret, bool) {
	v, ok := s[name]
	return core.NewSecret(v), ok
}

const testKey = "cc-test-key-123"

func newTestAdapter(t *testing.T, url string) *Adapter {
	t.Helper()
	a, err := NewAdapter("cmc", Config{BaseURL: url}, secrets{DefaultAPIKeyEnv: testKey}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func collect(t *testing.T, stream core.ChatStream) ([]core.ChatEvent, error) {
	t.Helper()
	var out []core.ChatEvent
	for ev, err := range stream {
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
	return out, nil
}

func ndjson(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func TestStreamRequestAndEvents(t *testing.T) {
	var gotBody map[string]any
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alpha/generate" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		gotHeader = r.Header.Clone()
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &gotBody); err != nil {
			t.Errorf("body: %v", err)
		}
		_, _ = io.WriteString(w, ndjson(
			`{"type":"start"}`,
			`{"type":"reasoning-delta","text":"think"}`,
			`{"type":"text-delta","text":"Hel"}`,
			`data: {"type":"text-delta","delta":"lo"}`,
			`{"type":"tool-input-start","id":"call_1","toolName":"read"}`,
			`{"type":"tool-input-delta","id":"call_1","delta":"{\"path\":"}`,
			`{"type":"tool-input-delta","id":"call_1","delta":"\"a.go\"}"}`,
			`{"type":"tool-call","toolCallId":"call_2","toolName":"ls","input":{"dir":"."}}`,
			`{"type":"finish-step","finishReason":"tool-calls","usage":{"inputTokens":10,"outputTokens":5}}`,
			`{"type":"finish","finishReason":"tool-calls","totalUsage":{"inputTokens":12,"outputTokens":6,"cachedInputTokens":4}}`,
		))
	}))
	defer srv.Close()

	temp := 0.7
	req := core.ChatRequest{
		Model:           "deepseek/deepseek-v4-flash",
		System:          "be brief",
		MaxTokens:       500000,
		Temperature:     &temp,
		ReasoningEffort: "max",
		Tools:           []core.ToolSpec{{Name: "read", Description: "read a file", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hi"}}},
			{Role: core.RoleAssistant, Content: []core.ContentBlock{
				{Kind: core.BlockText, Text: "reading"},
				{Kind: core.BlockToolUse, ID: "t1", Name: "read", Input: json.RawMessage(`{"path":"x"}`)},
			}},
			{Role: core.RoleUser, Content: []core.ContentBlock{
				{Kind: core.BlockToolResult, ID: "t1", Output: "file body"},
				{Kind: core.BlockText, Text: "continue"},
			}},
		},
	}
	events, err := collect(t, mustStream(t, newTestAdapter(t, srv.URL), req))
	if err != nil {
		t.Fatal(err)
	}

	if got := gotHeader.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q", got)
	}
	if gotHeader.Get("x-command-code-version") == "" || gotHeader.Get("x-project-slug") == "" {
		t.Errorf("identity headers missing: %v", gotHeader)
	}
	if gotHeader.Get("x-session-id") != gotBody["threadId"] {
		t.Errorf("x-session-id %q != threadId %v", gotHeader.Get("x-session-id"), gotBody["threadId"])
	}
	params := gotBody["params"].(map[string]any)
	if params["model"] != "deepseek/deepseek-v4-flash" || params["system"] != "be brief" || params["stream"] != true {
		t.Errorf("params = %v", params)
	}
	if params["max_tokens"] != float64(maxTokensLimit) {
		t.Errorf("max_tokens = %v, want clamp %d", params["max_tokens"], maxTokensLimit)
	}
	if params["temperature"] != 0.7 || params["reasoning_effort"] != "high" {
		t.Errorf("temperature/effort = %v/%v", params["temperature"], params["reasoning_effort"])
	}
	tools := params["tools"].([]any)
	if tool := tools[0].(map[string]any); tool["name"] != "read" || tool["input_schema"] == nil {
		t.Errorf("tools = %v", tools)
	}
	msgs := params["messages"].([]any)
	roles := []string{}
	for _, m := range msgs {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "user,assistant,tool,user" {
		t.Fatalf("roles = %v", roles)
	}
	assistant := msgs[1].(map[string]any)["content"].([]any)
	if assistant[0].(map[string]any)["type"] != "reasoning" || assistant[2].(map[string]any)["type"] != "tool-call" {
		t.Errorf("assistant blocks = %v", assistant)
	}
	if input := assistant[2].(map[string]any)["input"].(map[string]any); input["path"] != "x" {
		t.Errorf("tool-call input = %v", input)
	}
	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool-result" || result["toolCallId"] != "t1" || result["toolName"] != "read" {
		t.Errorf("tool result = %v", result)
	}
	if out := result["output"].(map[string]any); out["type"] != "text" || out["value"] != "file body" {
		t.Errorf("tool output = %v", out)
	}

	var text, thinking strings.Builder
	var blocks []core.ContentBlock
	var usage core.Usage
	stop := ""
	for _, ev := range events {
		switch ev.Kind {
		case core.ChatTextDelta:
			text.WriteString(ev.Text)
		case core.ChatThinkingDelta:
			thinking.WriteString(ev.Text)
		case core.ChatBlock:
			blocks = append(blocks, ev.Block)
		case core.ChatUsage:
			usage = ev.Usage
		case core.ChatStop:
			stop = ev.StopReason
		}
	}
	if text.String() != "Hello" || thinking.String() != "think" {
		t.Errorf("text %q thinking %q", text.String(), thinking.String())
	}
	if len(blocks) != 3 || blocks[0].Kind != core.BlockThinking {
		t.Fatalf("blocks = %+v", blocks)
	}
	if blocks[1].ID != "call_1" || blocks[1].Name != "read" || string(blocks[1].Input) != `{"path":"a.go"}` {
		t.Errorf("tool 1 = %+v (%s)", blocks[1], blocks[1].Input)
	}
	if blocks[2].ID != "call_2" || blocks[2].Name != "ls" || string(blocks[2].Input) != `{"dir":"."}` {
		t.Errorf("tool 2 = %+v (%s)", blocks[2], blocks[2].Input)
	}
	if usage != (core.Usage{InputTokens: 12, OutputTokens: 6, CachedInputTokens: 4}) {
		t.Errorf("usage = %+v", usage)
	}
	if stop != "tool_use" {
		t.Errorf("stop = %q", stop)
	}
}

func mustStream(t *testing.T, a *Adapter, req core.ChatRequest) core.ChatStream {
	t.Helper()
	stream, err := a.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func userText(text string) core.Message {
	return core.Message{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: text}}}
}

func TestStreamDefaults(t *testing.T) {
	env := buildEnvelope(core.ChatRequest{Model: "m", Messages: []core.Message{userText("hi")}}, time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC))
	if env.Params.MaxTokens != defaultMaxTokens || env.Params.Temperature != defaultTemperature || env.Params.ReasoningEffort != "" {
		t.Errorf("params = %+v", env.Params)
	}
	if env.Config.Date != "2026-11-11" || env.Config.Environment == "" {
		t.Errorf("config = %+v", env.Config)
	}
	data, _ := json.Marshal(env)
	if !strings.Contains(string(data), `"structure":[]`) || strings.Contains(string(data), `"tools"`) {
		t.Errorf("body = %s", data)
	}
}

func TestThreadIDStableAcrossTurns(t *testing.T) {
	first := core.ChatRequest{System: "sys", Messages: []core.Message{userText("hello")}}
	later := core.ChatRequest{System: "sys", Messages: []core.Message{userText("hello"), {Role: core.RoleAssistant, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hey"}}}, userText("more")}}
	other := core.ChatRequest{System: "sys", Messages: []core.Message{userText("bye")}}
	if threadID(first) != threadID(later) {
		t.Error("threadId changed when a turn was appended")
	}
	if threadID(first) == threadID(other) {
		t.Error("different conversations share a threadId")
	}
	id := threadID(first)
	if len(id) != 36 || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) {
		t.Errorf("threadId %q is not a v4 UUID shape", id)
	}
}

func TestStreamErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, ndjson(`{"type":"error","error":{"message":"Rate limit exceeded","statusCode":429}}`))
	}))
	defer srv.Close()
	_, err := collect(t, mustStream(t, newTestAdapter(t, srv.URL), core.ChatRequest{Model: "m", Messages: []core.Message{userText("hi")}}))
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "rate_limit" || pe.Status != 429 {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamDisconnected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, ndjson(`{"type":"text-delta","text":"partial"}`))
	}))
	defer srv.Close()
	_, err := collect(t, mustStream(t, newTestAdapter(t, srv.URL), core.ChatRequest{Model: "m", Messages: []core.Message{userText("hi")}}))
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "network" {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamHTTPErrorRedactsKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"bad key `+testKey+`"}`)
	}))
	defer srv.Close()
	_, err := newTestAdapter(t, srv.URL).Stream(context.Background(), core.ChatRequest{Model: "m"})
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "auth" || pe.Status != 401 {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(pe.Message, testKey) {
		t.Errorf("message leaks the key: %s", pe.Message)
	}
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/provider/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"deepseek/deepseek-v4-pro","name":"DeepSeek V4 Pro","context_length":1000000},{"id":""}]}`)
	}))
	defer srv.Close()
	models, err := newTestAdapter(t, srv.URL).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "deepseek/deepseek-v4-pro" || models[0].ContextWindow != 1000000 {
		t.Fatalf("models = %+v", models)
	}
}

func TestKindRegisteredWithDefaultKeyEnv(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, ndjson(`{"type":"text-delta","text":"ok"}`, `{"type":"finish","finishReason":"stop"}`))
	}))
	defer srv.Close()
	rt, err := router.New(router.Config{Providers: map[string]core.ProviderConfig{
		"cmd-code": {Kind: Kind, BaseURL: srv.URL},
	}}, router.WithSecrets(secrets{"CMD_CODE_API_KEY": testKey}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	p, ok := rt.Provider("cmd-code")
	if !ok {
		t.Fatal("provider not built")
	}
	stream, err := p.Stream(context.Background(), core.ChatRequest{Model: "m", Messages: []core.Message{userText("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, stream); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want key from CMD_CODE_API_KEY", auth)
	}
}
