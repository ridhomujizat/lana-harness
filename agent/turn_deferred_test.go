package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/tools"
)

// deferTool is a fake MCP tool of the deferral tests: a fixed description and
// JSON input schema, and a read risk so the default gate allows it.
type deferTool struct {
	name   string
	desc   string
	schema string
}

func (t deferTool) Spec() core.ToolSpec {
	schema := t.schema
	if schema == "" {
		schema = `{"type":"object"}`
	}
	desc := t.desc
	if desc == "" {
		desc = "A fake MCP tool."
	}
	return core.ToolSpec{Name: t.name, Description: desc, InputSchema: json.RawMessage(schema)}
}
func (t deferTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }
func (t deferTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "pong"}, nil
}

func simpleTools(server string, n int) []deferTool {
	names := mcpToolNames(server, n)
	out := make([]deferTool, 0, len(names))
	for _, name := range names {
		out = append(out, deferTool{name: name})
	}
	return out
}

func namedTools(names ...string) []deferTool {
	out := make([]deferTool, 0, len(names))
	for _, name := range names {
		out = append(out, deferTool{name: name})
	}
	return out
}

// deferMCPManager serves the fake tools of each server and records the applied
// MCP config so the defer flag can be read back.
type deferMCPManager struct {
	registry core.ToolRegistry
	servers  map[string][]deferTool
}

func (m *deferMCPManager) Ensure(_ context.Context, servers []string) ([]string, map[string]string) {
	var names []string
	for _, server := range servers {
		for _, tool := range m.servers[server] {
			_ = m.registry.Register(tool)
			names = append(names, tool.name)
		}
	}
	return names, nil
}
func (m *deferMCPManager) Status([]string) []core.MCPServerStatus { return nil }
func (m *deferMCPManager) TestServer(context.Context, core.MCPConfig) (int, error) {
	return 0, nil
}
func (m *deferMCPManager) Put(string, core.MCPConfig) {}
func (m *deferMCPManager) Remove(string)              {}

// stepRecorder wraps the scripted provider and stores every request, so a test
// can name the tool list of each step.
type stepRecorder struct {
	inner core.Provider
	mu    sync.Mutex
	reqs  []core.ChatRequest
}

func (p *stepRecorder) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	p.mu.Unlock()
	return p.inner.Stream(ctx, req)
}

func (p *stepRecorder) tools(t *testing.T, step int) []string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if step >= len(p.reqs) {
		t.Fatalf("recorded %d requests, want at least %d", len(p.reqs), step+1)
	}
	return toolSpecNames(p.reqs[step].Tools)
}

func newDeferHarness(t *testing.T, script mock.Script, cfg core.AgentConfig, servers map[string][]deferTool) (*harness, *stepRecorder) {
	t.Helper()
	h := newHarness(t, script, map[string]core.AgentConfig{"assistant": cfg})
	if err := h.tools.Register(tools.NewToolSearch()); err != nil {
		t.Fatal(err)
	}
	h.reg.SetMCP(&deferMCPManager{registry: h.tools, servers: servers})
	rec := &stepRecorder{inner: h.llm.providers["mock"]}
	h.llm.providers["mock"] = rec
	return h, rec
}

func userMessageTexts(t *testing.T, h *harness) []string {
	t.Helper()
	var out []string
	for _, event := range h.events(t) {
		if event.Kind != core.KindUserMessage {
			continue
		}
		var payload core.UserMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, userText(payload))
	}
	return out
}

// AC-DTL-001: an agent above the threshold sends no MCP schema on its first step
// and is offered tool_search (REQ-DTL-001, REQ-DTL-004).
func TestDeferralHidesSchemasWhenMany(t *testing.T) {
	cfg := core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}, MCP: []string{"srv"}}
	h, rec := newDeferHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}},
		cfg, map[string][]deferTool{"srv": simpleTools("srv", 16)})

	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	names := rec.tools(t, 0)
	for _, name := range names {
		if strings.HasPrefix(name, "mcp__") {
			t.Fatalf("first step tools=%v, a deferred schema was sent", names)
		}
	}
	if !containsString(names, "tool_search") {
		t.Fatalf("first step tools=%v, tool_search missing", names)
	}
	if prompt := h.systemPrompts(t)[0]; !strings.Contains(prompt, "# Deferred tools") || !strings.Contains(prompt, "mcp__srv__t00") {
		t.Fatalf("prompt lacks the deferred tools section:\n%s", prompt)
	}
}

// AC-DTL-002: a small server is sent in full and tool_search is not offered
// (REQ-DTL-001).
func TestDeferralSendsAllWhenFew(t *testing.T) {
	cfg := core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}, MCP: []string{"srv"}}
	h, rec := newDeferHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}},
		cfg, map[string][]deferTool{"srv": simpleTools("srv", 5)})

	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	names := rec.tools(t, 0)
	for _, want := range mcpToolNames("srv", 5) {
		if !containsString(names, want) {
			t.Fatalf("first step tools=%v, %s missing", names, want)
		}
	}
	if containsString(names, "tool_search") {
		t.Fatalf("first step tools=%v, tool_search offered without deferral", names)
	}
}

// AC-DTL-003: [mcp.kerjan] defer = false keeps its tools out of the deferral:
// they are sent and do not count toward the threshold (REQ-DTL-001).
func TestDeferralServerOptOut(t *testing.T) {
	cfg := core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}, MCP: []string{"kerjan", "dbx"}}
	h, rec := newDeferHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, cfg,
		map[string][]deferTool{"kerjan": simpleTools("kerjan", 16), "dbx": simpleTools("dbx", 16)})
	h.reg.PutServer("kerjan", core.MCPConfig{Defer: boolPtr(false)})

	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	names := rec.tools(t, 0)
	for _, want := range mcpToolNames("kerjan", 16) {
		if !containsString(names, want) {
			t.Fatalf("first step tools=%v, kerjan tool %s missing", names, want)
		}
	}
	for _, name := range mcpToolNames("dbx", 16) {
		if containsString(names, name) {
			t.Fatalf("first step tools=%v, deferred %s was sent", names, name)
		}
	}
	if !containsString(names, "tool_search") {
		t.Fatalf("first step tools=%v, tool_search missing", names)
	}
}

// AC-DTL-005: tool_search loads b then a and the next step's list ends with b, a
// (REQ-DTL-002).
func TestDeferralLoadOrder(t *testing.T) {
	on := true
	cfg := core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}, MCP: []string{"srv"}, DeferTools: &on}
	script := mock.Script{Responses: []mock.Response{
		toolResponse("c1", "tool_search", `{"query":"select:mcp__srv__b"}`),
		toolResponse("c2", "tool_search", `{"query":"select:mcp__srv__a"}`),
		textResponse("done"),
	}}
	h, rec := newDeferHarness(t, script, cfg, map[string][]deferTool{"srv": namedTools("mcp__srv__a", "mcp__srv__b")})

	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	afterB := rec.tools(t, 1)
	if !containsString(afterB, "mcp__srv__b") || containsString(afterB, "mcp__srv__a") {
		t.Fatalf("after loading b tools=%v want b and not a", afterB)
	}
	final := rec.tools(t, 2)
	if len(final) < 2 || final[len(final)-2] != "mcp__srv__b" || final[len(final)-1] != "mcp__srv__a" {
		t.Fatalf("final tools=%v want it to end with b, a", final)
	}
}

// AC-DTL-006: two turns without a load send an identical tool list
// (REQ-DTL-002).
func TestDeferralIdenticalAcrossLoadFreeTurns(t *testing.T) {
	on := true
	cfg := core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}, MCP: []string{"srv"}, DeferTools: &on}
	h, rec := newDeferHarness(t, mock.Script{Responses: []mock.Response{textResponse("one"), textResponse("two")}},
		cfg, map[string][]deferTool{"srv": namedTools("mcp__srv__a", "mcp__srv__b")})

	h.run(t, "first")
	h.run(t, "second")
	if first, second := rec.tools(t, 0), rec.tools(t, 1); !reflect.DeepEqual(first, second) {
		t.Fatalf("first=%v second=%v want identical", first, second)
	}
}

// AC-DTL-008: loading a tool mid-session adds no <system-update> because the
// deferred_tools section never changes (REQ-DTL-003).
func TestDeferralLoadAddsNoSystemUpdate(t *testing.T) {
	on := true
	cfg := core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}, MCP: []string{"srv"}, DeferTools: &on}
	script := mock.Script{Responses: []mock.Response{
		toolResponse("c1", "mcp__srv__a", `{}`),
		textResponse("loaded"),
		textResponse("again"),
	}}
	h, _ := newDeferHarness(t, script, cfg, map[string][]deferTool{"srv": namedTools("mcp__srv__a")})

	h.run(t, "first")
	h.run(t, "second")
	for _, text := range userMessageTexts(t, h) {
		if strings.Contains(text, "<system-update>") {
			t.Fatalf("a load produced a system-update:\n%s", text)
		}
	}
}

// AC-DTL-011: an agent without deferred tools is not offered tool_search
// (REQ-DTL-004).
func TestDeferralNoToolSearchWithoutDeferred(t *testing.T) {
	cfg := core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}}
	h, rec := newDeferHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, cfg, nil)

	h.run(t, "hi")
	if names := rec.tools(t, 0); containsString(names, "tool_search") {
		t.Fatalf("tools=%v, tool_search offered without deferred tools", names)
	}
}

// AC-DTL-012: a direct call to a deferred tool with valid input runs and loads
// it, and the next step sends its schema (REQ-DTL-005).
func TestDeferralDirectCallRunsAndLoads(t *testing.T) {
	on := true
	cfg := core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}, MCP: []string{"srv"}, DeferTools: &on}
	script := mock.Script{Responses: []mock.Response{
		toolResponse("c1", "mcp__srv__alpha", `{}`),
		textResponse("done"),
	}}
	h, rec := newDeferHarness(t, script, cfg, map[string][]deferTool{"srv": namedTools("mcp__srv__alpha")})

	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	if out := toolResultPayload(t, h, "c1"); out.Status != core.ToolStatusSuccess || out.Output != "pong" {
		t.Fatalf("direct call result=%+v", out)
	}
	if names := rec.tools(t, 1); !containsString(names, "mcp__srv__alpha") {
		t.Fatalf("next step tools=%v, loaded schema missing", names)
	}
}

// AC-DTL-013: a direct call with a missing required field returns the invalid
// input error plus the schema, and the tool is loaded (REQ-DTL-005).
func TestDeferralDirectCallInvalidInput(t *testing.T) {
	on := true
	schema := `{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`
	cfg := core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}, MCP: []string{"srv"}, DeferTools: &on}
	script := mock.Script{Responses: []mock.Response{
		toolResponse("c1", "mcp__srv__alpha", `{}`),
		textResponse("done"),
	}}
	h, rec := newDeferHarness(t, script, cfg, map[string][]deferTool{
		"srv": {{name: "mcp__srv__alpha", desc: "Creates a ticket.", schema: schema}},
	})

	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	out := toolResultPayload(t, h, "c1")
	if out.Status != core.ToolStatusError || !strings.HasPrefix(out.Output, "invalid input:") {
		t.Fatalf("result=%+v want an invalid input error", out)
	}
	for _, want := range []string{"Creates a ticket.", `"required":["title"]`} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("result text lacks %q:\n%s", want, out.Output)
		}
	}
	if names := rec.tools(t, 1); !containsString(names, "mcp__srv__alpha") {
		t.Fatalf("next step tools=%v, the tool was not loaded", names)
	}
}

// AC-DTL-014: a tool loaded in an earlier turn is still sent in the same
// position on a later turn (REQ-DTL-006).
func TestDeferralLoadedToolKeptNextTurn(t *testing.T) {
	on := true
	cfg := core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}, MCP: []string{"srv"}, DeferTools: &on}
	script := mock.Script{Responses: []mock.Response{
		toolResponse("c1", "mcp__srv__a", `{}`),
		textResponse("done"),
		textResponse("again"),
	}}
	h, rec := newDeferHarness(t, script, cfg, map[string][]deferTool{"srv": namedTools("mcp__srv__a")})

	h.run(t, "first")
	loaded := rec.tools(t, 1)
	h.run(t, "second")
	next := rec.tools(t, 2)
	if !containsString(next, "mcp__srv__a") || !reflect.DeepEqual(next, loaded) {
		t.Fatalf("next turn tools=%v want %v", next, loaded)
	}
}

// AC-DTL-015: after a compaction summary the loaded set is empty until a tool
// is loaded again (REQ-DTL-006).
func TestDeferralLoadedSetResetsAfterCompaction(t *testing.T) {
	on := true
	cfg := core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}, MCP: []string{"srv"}, DeferTools: &on}
	script := mock.Script{Responses: []mock.Response{
		toolResponse("c1", "mcp__srv__a", `{}`),
		textResponse("done"),
		textResponse("again"),
	}}
	h, rec := newDeferHarness(t, script, cfg, map[string][]deferTool{"srv": namedTools("mcp__srv__a")})

	h.run(t, "first")
	if _, err := h.store.Append(context.Background(), h.info.ID, []core.EventDraft{
		{Kind: core.KindCompactionSummary, Payload: core.CompactionSummary{V: core.PayloadVersion, Summary: "sum", FromSeq: 1, UptoSeq: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	h.run(t, "second")
	if names := rec.tools(t, 2); containsString(names, "mcp__srv__a") {
		t.Fatalf("tools=%v, a tool loaded before the compaction was sent", names)
	}
}
