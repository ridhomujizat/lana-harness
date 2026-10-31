package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/tools"
)

type fakeMCPTool struct{ name string }

func (t fakeMCPTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: t.name, Description: "A fake MCP tool.", InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (t fakeMCPTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }
func (t fakeMCPTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "pong"}, nil
}

type fakeMCPManager struct {
	calls       atomic.Int32
	registry    core.ToolRegistry
	toolName    string
	unavailable map[string]string
}

func (m *fakeMCPManager) Ensure(context.Context, []string) ([]string, map[string]string) {
	m.calls.Add(1)
	if m.registry != nil && m.toolName != "" {
		_ = m.registry.Register(fakeMCPTool{name: m.toolName})
	}
	// Only report a tool when one was configured: a manager never names an
	// empty server as loaded.
	var names []string
	if m.toolName != "" {
		names = []string{m.toolName}
	}
	return names, m.unavailable
}

func (m *fakeMCPManager) Status([]string) []core.MCPServerStatus { return nil }

func toolResultPayload(t *testing.T, h *harness, callID string) core.ToolResultPayload {
	t.Helper()
	for _, event := range h.events(t) {
		if event.Kind != core.KindToolResult {
			continue
		}
		var payload core.ToolResultPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.CallID == callID {
			return payload
		}
	}
	t.Fatalf("tool result %q not found", callID)
	return core.ToolResultPayload{}
}

func TestTurnMCPToolsAndUnavailable(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "mcp__fake__ping", `{}`),
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", MCP: []string{"fake", "down"}}})
	manager := &fakeMCPManager{
		registry:    h.tools,
		toolName:    "mcp__fake__ping",
		unavailable: map[string]string{"down": "command not found"},
	}
	h.reg.SetMCP(manager)

	result := h.run(t, "ping")
	if result.StopReason != "end_turn" || result.Err != nil {
		t.Fatalf("result=%+v", result)
	}
	if got := manager.calls.Load(); got != 1 {
		t.Fatalf("Ensure calls = %d, want 1", got)
	}
	if prompts := h.systemPrompts(t); len(prompts) != 1 {
		t.Fatalf("prompts = %d, want 1", len(prompts))
	}
	// REQ-LTC-001: the unavailable list moves to the first user message.
	if user := userText(h.firstUserMessage(t)); !strings.Contains(user, "unavailable MCP servers: down (command not found)") {
		t.Fatalf("context lacks the unavailable line:\n%s", user)
	}
	out := toolResultPayload(t, h, "c1")
	if out.Status != core.ToolStatusSuccess || out.Output != "pong" {
		t.Fatalf("mcp tool result=%+v", out)
	}
}

// AC-TFB-019: a server of the agent that the MCP manager does not know is
// listed as "not loaded" in the prompt (REQ-TFB-008 item 2).
func TestTurnMCPUnknownServerIsNotLoaded(t *testing.T) {
	cases := []struct {
		name        string
		unavailable map[string]string
	}{
		{name: "manager reports not configured", unavailable: map[string]string{"ghost": "not configured"}},
		{name: "manager reports nothing", unavailable: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}},
				map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", MCP: []string{"ghost"}}})
			h.reg.SetMCP(&fakeMCPManager{unavailable: tc.unavailable})

			result := h.run(t, "hi")
			if result.StopReason != "end_turn" || result.Err != nil {
				t.Fatalf("result=%+v", result)
			}
			if user := userText(h.firstUserMessage(t)); !strings.Contains(user, "unavailable MCP servers: ghost (not loaded)") {
				t.Fatalf("context lacks the not-loaded line:\n%s", user)
			}
		})
	}
}

func TestTurnWithoutMCPDoesNotEnsure(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, nil)
	manager := &fakeMCPManager{registry: h.tools, toolName: "mcp__fake__ping"}
	h.reg.SetMCP(manager)

	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Err != nil {
		t.Fatalf("result=%+v", result)
	}
	if got := manager.calls.Load(); got != 0 {
		t.Fatalf("Ensure calls = %d, want 0", got)
	}
	if prompt := h.systemPrompts(t)[0]; strings.Contains(prompt, "unavailable MCP servers") {
		t.Fatalf("prompt has unavailable line without mcp:\n%s", prompt)
	}
	if user := userText(h.firstUserMessage(t)); !strings.Contains(user, "<context>") || strings.Contains(user, "unavailable MCP servers") {
		t.Fatalf("context without mcp must hold no unavailable line:\n%s", user)
	}
}

func TestTurnSkillsPromptAndTool(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "skill_load", `{"name":"demo"}`),
		textResponse("done"),
	}}, nil)
	if err := h.tools.Register(tools.NewSkillLoad()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.home, "agents", "assistant", "skills", "demo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill for tests.\n---\nDo the demo.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	result := h.run(t, "use demo")
	if result.StopReason != "end_turn" || result.Err != nil {
		t.Fatalf("result=%+v", result)
	}
	prompt := h.systemPrompts(t)[0]
	for _, want := range []string{
		"# Skills",
		"Call skill_load with the skill name before following a skill.",
		"- demo: Demo skill for tests.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	out := toolResultPayload(t, h, "c1")
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "skill demo (agent)") {
		t.Fatalf("skill_load result=%+v", out)
	}
}
