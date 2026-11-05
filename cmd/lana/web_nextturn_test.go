package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/web"
)

// promptCaptureProvider delegates to the mock LLM and keeps the chat requests
// so a test can inspect the system prompt.
type promptCaptureProvider struct {
	llm *mock.Llm

	mu       sync.Mutex
	requests []core.ChatRequest
}

func (p *promptCaptureProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return p.llm.Stream(ctx, req)
}

func (p *promptCaptureProvider) lastSystem() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return ""
	}
	return p.requests[len(p.requests)-1].System
}

func (p *promptCaptureProvider) lastTools() []core.ToolSpec {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return nil
	}
	return p.requests[len(p.requests)-1].Tools
}

// catalogTestTool is a named tool with an explicit risk for the catalog and
// disabled_tools tests. The name can use the MCP "mcp__server__tool" form.
type catalogTestTool struct {
	name string
	risk core.Risk
}

func (t catalogTestTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: t.name, InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (t catalogTestTool) Risk(json.RawMessage) core.Risk { return t.risk }
func (t catalogTestTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusSuccess}, nil
}

// newDisabledToolsRuntime builds a runtime whose coder agent lists the given
// tools and disabled_tools. The mock provider returns one plain text answer.
func newDisabledToolsRuntime(t *testing.T, agentTable string, tools []core.Tool) (*lana.Runtime, *promptCaptureProvider) {
	t.Helper()
	text, end := "ok", "end_turn"
	capture := &promptCaptureProvider{llm: mock.New(mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
	}})}
	layer := `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

` + agentTable + `
[[routes]]
channel = "web"
agent = "coder"
`
	rt, err := lana.New(context.Background(), lana.Options{
		Home:      t.TempDir(),
		Layers:    []config.Layer{{Name: "test", TOML: []byte(layer)}},
		Providers: map[string]core.Provider{"mock": capture},
		Tools:     tools,
	})
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt, capture
}

// TestNextTurnDisabledToolsNotOffered covers the runtime half of AC-WEB-049: a
// disabled tool is not in the tool list offered to the model at the next turn,
// for both a built-in name and the "<server>/<tool>" MCP form.
func TestNextTurnDisabledToolsNotOffered(t *testing.T) {
	cases := []struct {
		name     string
		disabled string
		absent   []string
		present  []string
	}{
		{
			name:     "built-in bash",
			disabled: `["bash"]`,
			absent:   []string{"bash"},
			present:  []string{"read_file", "web_fetch", "mcp__github__create_issue"},
		},
		{
			name:     "MCP reference",
			disabled: `["github/create_issue"]`,
			absent:   []string{"mcp__github__create_issue"},
			present:  []string{"read_file", "bash", "web_fetch"},
		},
		{
			name:     "unknown name is ignored",
			disabled: `["not_a_tool"]`,
			present:  []string{"read_file", "bash", "web_fetch", "mcp__github__create_issue"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			table := `[agents.coder]
provider = "mock"
model = "m"
permission = "ask"
tools = ["read_file", "bash", "mcp__github__create_issue", "web_fetch"]
disabled_tools = ` + tc.disabled
			rt, capture := newDisabledToolsRuntime(t, table, []core.Tool{
				catalogTestTool{name: "mcp__github__create_issue", risk: core.RiskExec},
			})
			ctx := context.Background()
			info, err := rt.CreateChannelSession(ctx, "web", "owner", "coder")
			if err != nil {
				t.Fatalf("CreateChannelSession: %v", err)
			}
			if _, err := rt.Run(ctx, lana.Request{Prompt: "hello", SessionID: info.ID}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			offered := toolSpecNames(capture.lastTools())
			if len(offered) == 0 {
				t.Fatal("the mock LLM received no tools")
			}
			for _, name := range tc.absent {
				if containsToolName(offered, name) {
					t.Fatalf("disabled tool %q is still offered: %v", name, offered)
				}
			}
			for _, name := range tc.present {
				if !containsToolName(offered, name) {
					t.Fatalf("tool %q is not offered: %v", name, offered)
				}
			}
		})
	}
}

// TestToolCatalog covers API-WEB-026 at the daemon adapter: group order, risk,
// asks_first, enabled, the MCP "<server>/<tool>" name, and the unknown agent
// mapping.
func TestToolCatalog(t *testing.T) {
	// Keep the test independent of the host: a package installed on PATH (for
	// example obscura) would add an "installed" group to the catalog.
	t.Setenv("PATH", "/usr/bin:/bin")
	table := `[agents.coder]
provider = "mock"
model = "m"
permission = "ask"
tools = ["read_file", "bash"]
mcp = ["github"]
disabled_tools = ["bash", "github/create_issue"]

[mcp.github]
command = "github-mcp"
`
	rt, _ := newDisabledToolsRuntime(t, table, []core.Tool{
		catalogTestTool{name: "mcp__github__create_issue", risk: core.RiskExec},
	})
	backend := &webBackend{rt: rt}

	groups, err := backend.ToolCatalog("coder")
	if err != nil {
		t.Fatalf("ToolCatalog: %v", err)
	}
	wantOrder := []string{"files", "shell", "web", "memory", "knowledge", "skills", "mcp-admin", "calendar", "chat", "mcp:github"}
	if len(groups) != len(wantOrder) {
		t.Fatalf("groups = %+v, want %d groups", groups, len(wantOrder))
	}
	for i, id := range wantOrder {
		if groups[i].ID != id {
			t.Fatalf("group %d = %q, want %q", i, groups[i].ID, id)
		}
		if groups[i].Tools == nil {
			t.Fatalf("group %q tools is nil, want []", id)
		}
	}

	read, ok := findToolEntry(groups, "files", "read_file")
	if !ok {
		t.Fatal("read_file is not in the files group")
	}
	if read.Risk != "read" || read.AsksFirst || !read.Enabled {
		t.Fatalf("read_file = %+v", read)
	}
	bash, ok := findToolEntry(groups, "shell", "bash")
	if !ok {
		t.Fatal("bash is not in the shell group")
	}
	if bash.Risk != "exec" || !bash.AsksFirst || bash.Enabled {
		t.Fatalf("bash = %+v", bash)
	}
	issue, ok := findToolEntry(groups, "mcp:github", "github/create_issue")
	if !ok {
		t.Fatal("github/create_issue is not in the mcp:github group")
	}
	if issue.Risk != "exec" || !issue.AsksFirst || issue.Enabled {
		t.Fatalf("github/create_issue = %+v", issue)
	}

	if _, err := backend.ToolCatalog("missing"); !errors.Is(err, web.ErrUnknownAgent) {
		t.Fatalf("unknown agent error = %v, want web.ErrUnknownAgent", err)
	}
}

// TestToolCatalogRealState covers AC-TTS-001..003: enabled is the state the
// root turn really offers, and source names why.
func TestToolCatalogRealState(t *testing.T) {
	// Keep the test independent of the host: a package installed on PATH (for
	// example obscura) would add an "installed" group to the catalog.
	t.Setenv("PATH", "/usr/bin:/bin")
	table := `[agents.coder]
provider = "mock"
model = "m"
permission = "ask"
tools = ["read_file"]

[mcp.github]
command = "github-mcp"
`
	rt, _ := newDisabledToolsRuntime(t, table, []core.Tool{
		catalogTestTool{name: "mcp__github__create_issue", risk: core.RiskExec},
	})
	backend := &webBackend{rt: rt}

	groups, err := backend.ToolCatalog("coder")
	if err != nil {
		t.Fatalf("ToolCatalog: %v", err)
	}

	ask, ok := findToolEntry(groups, "chat", "ask_user")
	if !ok {
		t.Fatal("ask_user is not in the chat group")
	}
	if ask.Enabled || ask.Source != "not_listed" {
		t.Fatalf("ask_user = %+v, want enabled false source not_listed", ask)
	}

	manage, ok := findToolEntry(groups, "mcp-admin", "mcp_manage")
	if !ok {
		t.Fatal("mcp_manage is not in the mcp-admin group")
	}
	if !manage.Enabled || manage.Source != "auto" {
		t.Fatalf("mcp_manage = %+v, want enabled true source auto", manage)
	}

	issue, ok := findToolEntry(groups, "mcp:github", "github/create_issue")
	if !ok {
		t.Fatal("github/create_issue is not in the mcp:github group")
	}
	if issue.Enabled || issue.Source != "not_attached" {
		t.Fatalf("github/create_issue = %+v, want enabled false source not_attached", issue)
	}
}

func toolSpecNames(specs []core.ToolSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

func containsToolName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func findToolEntry(groups []web.ToolGroup, groupID, name string) (web.ToolEntry, bool) {
	for _, group := range groups {
		if group.ID != groupID {
			continue
		}
		for _, entry := range group.Tools {
			if entry.Name == name {
				return entry, true
			}
		}
	}
	return web.ToolEntry{}, false
}

// TestWebNextTurn covers the turn half of AC-WEB-027: an AGENT.md saved through
// the web handler is in the system prompt of the next turn.
func TestWebNextTurn(t *testing.T) {
	text, end := "ok", "end_turn"
	capture := &promptCaptureProvider{llm: mock.New(mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
	}})}
	home := t.TempDir()
	rt, err := lana.New(context.Background(), lana.Options{
		Home:      home,
		Layers:    []config.Layer{{Name: "test", TOML: []byte(webTestLayer)}},
		Providers: map[string]core.Provider{"mock": capture},
	})
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	_, ts, cookie, csrf := webServer(t, rt, home)

	resp, body := webDo(t, http.MethodGet, ts.URL+"/v1/agents/assistant/files/agent", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET agent file status = %d, body = %q", resp.StatusCode, body)
	}
	var current struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &current); err != nil {
		t.Fatal(err)
	}

	const persona = "You are the persona saved through the web view."
	payload, err := json.Marshal(map[string]string{"content": persona, "expected_version": current.Version})
	if err != nil {
		t.Fatal(err)
	}
	resp, body = webDo(t, http.MethodPut, ts.URL+"/v1/agents/assistant/files/agent", string(payload), cookie, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT agent file status = %d, body = %q", resp.StatusCode, body)
	}

	info := webCreateSession(t, rt, context.Background())
	if _, err := rt.Run(context.Background(), lana.Request{Prompt: "hello", SessionID: info.ID}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	system := capture.lastSystem()
	if system == "" {
		t.Fatal("the mock LLM received no request")
	}
	if !strings.Contains(system, persona) {
		t.Fatalf("system prompt does not contain the saved AGENT.md text:\n%s", system)
	}
}
