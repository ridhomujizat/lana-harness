package mcphost

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ridhomujizat/lana-harness/core"
)

type headerTransport map[string]string

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range t {
		req.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// connect opens an MCP client session with the server of an --mcp-config.
func connect(t *testing.T, config string) (*mcp.ClientSession, error) {
	t.Helper()
	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(config), &cfg); err != nil {
		t.Fatal(err)
	}
	srv := cfg.MCPServers[ServerName]
	if srv.Type != "http" || !strings.HasPrefix(srv.URL, "http://127.0.0.1:") || !strings.HasPrefix(srv.Headers["Authorization"], "Bearer ") {
		t.Fatalf("config = %s", config)
	}
	if token := strings.TrimPrefix(srv.Headers["Authorization"], "Bearer "); len(token) != 2*tokenBytes {
		t.Fatalf("token length = %d", len(token))
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: srv.URL, HTTPClient: &http.Client{Transport: headerTransport(srv.Headers)}, MaxRetries: -1, DisableStandaloneSSE: true,
	}, nil)
}

func post(t *testing.T, url, auth string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

var memorySpec = core.ToolSpec{Name: "memory_write", Description: "Save facts.", InputSchema: json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}}}`)}

// REQ-CCT-006: the session's tools are listed and a call runs through the
// bound handler with Claude's tool use ID.
func TestHostListsAndCalls(t *testing.T) {
	h := New(Options{})
	t.Cleanup(func() { _ = h.Close() })
	var mu sync.Mutex
	var got []string
	call := func(_ context.Context, callID, name string, input json.RawMessage) core.ToolOutput {
		mu.Lock()
		got = append(got, callID+" "+name+" "+string(input))
		mu.Unlock()
		return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "saved"}
	}
	if err := h.Bind("s1", []core.ToolSpec{memorySpec, {Name: "calendar", Description: "Calendar."}}, call); err != nil {
		t.Fatal(err)
	}
	config, release, err := h.Open("s1")
	if err != nil {
		t.Fatal(err)
	}
	cs, err := connect(t, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 2 || tools.Tools[0].Name != "calendar" || tools.Tools[1].Name != "memory_write" {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "memory_write", Arguments: map[string]any{"content": "x"}, Meta: mcp.Meta{"claudecode/toolUseId": "toolu_1"},
	})
	if err != nil || res.IsError || res.Content[0].(*mcp.TextContent).Text != "saved" {
		t.Fatalf("call = %+v, %v", res, err)
	}
	if len(got) != 1 || got[0] != `toolu_1 memory_write {"content":"x"}` {
		t.Fatalf("calls = %v", got)
	}

	// A rebind replaces the tool set; an unbound session refuses calls.
	if err := h.Bind("s1", []core.ToolSpec{memorySpec}, call); err != nil {
		t.Fatal(err)
	}
	tools, _ = cs.ListTools(context.Background(), nil)
	if len(tools.Tools) != 1 {
		t.Fatalf("rebind tools = %+v", tools.Tools)
	}
	h.Unbind("s1")
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "memory_write", Arguments: map[string]any{}})
	if err != nil || !res.IsError {
		t.Fatalf("unbound call = %+v, %v", res, err)
	}
	release()
	release()
	if _, err := cs.ListTools(context.Background(), nil); err == nil {
		t.Fatal("a released token still works")
	}
}

// AC-CCT-008: a request without a valid token gets 401.
func TestHostRejectsMissingToken(t *testing.T) {
	h := New(Options{})
	t.Cleanup(func() { _ = h.Close() })
	if err := h.Bind("s1", []core.ToolSpec{memorySpec}, nil); err != nil {
		t.Fatal(err)
	}
	config, release, err := h.Open("s1")
	if err != nil {
		t.Fatal(err)
	}
	url := h.URL()
	for _, auth := range []string{"", "Bearer nope", "Basic abc", "Bearer "} {
		if code := post(t, url, auth); code != http.StatusUnauthorized {
			t.Fatalf("auth %q: status %d", auth, code)
		}
	}
	var cfg struct {
		MCPServers map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	_ = json.Unmarshal([]byte(config), &cfg)
	auth := cfg.MCPServers[ServerName].Headers["Authorization"]
	if code := post(t, url, auth); code == http.StatusUnauthorized {
		t.Fatal("valid token rejected")
	}
	release()
	if code := post(t, url, auth); code != http.StatusUnauthorized {
		t.Fatalf("revoked token: status %d", code)
	}
	_ = h.Close()
	if _, _, err := h.Open("s1"); err == nil {
		t.Fatal("open after close")
	}
}

func TestObjectSchema(t *testing.T) {
	if s := objectSchema(nil); s["type"] != "object" {
		t.Fatalf("empty schema = %v", s)
	}
	if s := objectSchema(json.RawMessage(`{"properties":{}}`)); s["type"] != "object" || s["properties"] == nil {
		t.Fatalf("schema = %v", s)
	}
	out := toolResult(core.ToolOutput{Status: core.ToolStatusError})
	if !out.IsError || out.Content[0].(*mcp.TextContent).Text == "" {
		t.Fatalf("error result = %+v", out)
	}
}
