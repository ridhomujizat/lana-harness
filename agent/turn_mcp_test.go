package agent

import (
	"context"
	"slices"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// fakeMCPAdminManager is a hot-apply manager for turn tests. It embeds the
// plain fake so Ensure and Status keep their existing behavior.
type fakeMCPAdminManager struct{ fakeMCPManager }

func (m *fakeMCPAdminManager) TestServer(context.Context, core.MCPConfig) (int, error) {
	return 0, nil
}
func (m *fakeMCPAdminManager) Put(string, core.MCPConfig) {}
func (m *fakeMCPAdminManager) Remove(string)              {}

func toolSpecNames(specs []core.ToolSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

// AC-AMCP-001: a root turn offers mcp_manage when the tool is registered.
func TestRootTurnOffersMCPManage(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}},
		map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	if err := h.tools.Register(fakeMCPTool{name: "mcp_manage"}); err != nil {
		t.Fatal(err)
	}
	recording := &recordingProvider{}
	h.llm.providers["mock"] = recording

	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	names := toolSpecNames(recording.last.Tools)
	if !slices.Contains(names, "mcp_manage") {
		t.Fatalf("offered tools=%v, mcp_manage missing", names)
	}
	if !slices.Contains(names, "read_file") {
		t.Fatalf("offered tools=%v, read_file missing", names)
	}
}

// AC-AMCP-002: disabled_tools and child sessions hide mcp_manage.
func TestMCPManageHiddenWhenDisabledOrChild(t *testing.T) {
	t.Run("disabled_tools", func(t *testing.T) {
		h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}},
			map[string]core.AgentConfig{"assistant": {
				Provider: "mock", Model: "m", Tools: []string{"read_file"}, DisabledTools: []string{"mcp_manage"},
			}})
		if err := h.tools.Register(fakeMCPTool{name: "mcp_manage"}); err != nil {
			t.Fatal(err)
		}
		recording := &recordingProvider{}
		h.llm.providers["mock"] = recording

		if result := h.run(t, "hi"); result.StopReason != "end_turn" {
			t.Fatalf("result=%+v", result)
		}
		if names := toolSpecNames(recording.last.Tools); slices.Contains(names, "mcp_manage") {
			t.Fatalf("offered tools=%v, mcp_manage should be disabled", names)
		}
	})

	t.Run("child session", func(t *testing.T) {
		h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}},
			map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
		if err := h.tools.Register(fakeMCPTool{name: "mcp_manage"}); err != nil {
			t.Fatal(err)
		}
		child, err := h.store.Create(context.Background(), core.NewSession{
			Agent: "assistant", Channel: "library", Workspace: h.ws, ParentSessionID: h.info.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		h.info = child
		recording := &recordingProvider{}
		h.llm.providers["mock"] = recording

		if result := h.run(t, "hi"); result.StopReason != "end_turn" {
			t.Fatalf("result=%+v", result)
		}
		if names := toolSpecNames(recording.last.Tools); slices.Contains(names, "mcp_manage") {
			t.Fatalf("offered tools=%v, a child session must not get mcp_manage", names)
		}
	})
}

// AC-AMCP-010 and AC-AMCP-011: the agent overlay replaces cfg.MCP on the next
// turn, so added server tools appear and removed ones disappear.
func TestAgentMCPOverlayHotAppliesTools(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}},
		map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	manager := &fakeMCPAdminManager{fakeMCPManager: fakeMCPManager{
		registry: h.tools,
		toolName: "mcp__fake__ping",
	}}
	h.reg.SetMCP(manager)
	recording := &recordingProvider{}
	h.llm.providers["mock"] = recording

	if result := h.run(t, "before"); result.StopReason != "end_turn" {
		t.Fatalf("before result=%+v", result)
	}
	if names := toolSpecNames(recording.last.Tools); slices.Contains(names, "mcp__fake__ping") {
		t.Fatalf("offered tools=%v, MCP tool offered before the overlay was set", names)
	}
	if got := manager.calls.Load(); got != 0 {
		t.Fatalf("Ensure calls=%d, want 0", got)
	}

	h.reg.SetAgentServers("assistant", []string{"fake"})
	if result := h.run(t, "added"); result.StopReason != "end_turn" {
		t.Fatalf("added result=%+v", result)
	}
	if names := toolSpecNames(recording.last.Tools); !slices.Contains(names, "mcp__fake__ping") {
		t.Fatalf("offered tools=%v, added MCP tool missing", names)
	}
	if got := manager.calls.Load(); got != 1 {
		t.Fatalf("Ensure calls=%d, want 1", got)
	}

	h.reg.SetAgentServers("assistant", []string{})
	if result := h.run(t, "removed"); result.StopReason != "end_turn" {
		t.Fatalf("removed result=%+v", result)
	}
	if names := toolSpecNames(recording.last.Tools); slices.Contains(names, "mcp__fake__ping") {
		t.Fatalf("offered tools=%v, removed MCP tool is still offered", names)
	}
}

// A manager that only implements core.MCPManager keeps connecting servers but
// exposes no admin service; an admin-capable manager exposes the registry.
func TestMCPAdminForRequiresHotApplyManager(t *testing.T) {
	h := newHarness(t, mock.Script{}, nil)

	h.reg.SetMCP(&fakeMCPManager{})
	if h.reg.mcpManager() == nil {
		t.Fatal("plain manager is not installed")
	}
	if h.reg.mcpAdminFor() != nil {
		t.Fatal("plain manager exposed an admin service")
	}

	h.reg.SetMCP(&fakeMCPAdminManager{})
	if h.reg.mcpAdminFor() == nil {
		t.Fatal("admin-capable manager did not expose an admin service")
	}
}
