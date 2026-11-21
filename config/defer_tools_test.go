package config

import (
	"strings"
	"testing"
)

// API-DTL-001: [agents.<name>] defer_tools is an optional boolean.
func TestAgentDeferToolsValidation(t *testing.T) {
	for _, table := range []string{
		"[agents.coder]\ndefer_tools = true\n",
		"[agents.coder]\ndefer_tools = false\n",
	} {
		if _, err := loadAgentKeys(t, table); err != nil {
			t.Fatalf("unexpected error for %q: %v", table, err)
		}
	}
	errs := agentKeyErrors(t, "[agents.coder]\ndefer_tools = \"yes\"\n")
	found := findError(errs, "agents.coder.defer_tools")
	if found == nil || !strings.Contains(found.Msg, "boolean") {
		t.Fatalf("defer_tools error = %+v", found)
	}
}

// API-DTL-001: defer_tools loads into AgentConfig.DeferTools, nil when absent.
func TestAgentDeferToolsLoaded(t *testing.T) {
	res, err := loadAgentKeys(t, "[agents.coder]\ndefer_tools = false\n")
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := res.Config.Agents["coder"]
	if !ok || cfg.DeferTools == nil || *cfg.DeferTools {
		t.Fatalf("defer_tools = %v", cfg.DeferTools)
	}

	res, err = loadAgentKeys(t, "[agents.coder]\ndefer_tools = true\n")
	if err != nil {
		t.Fatal(err)
	}
	cfg = res.Config.Agents["coder"]
	if cfg.DeferTools == nil || !*cfg.DeferTools {
		t.Fatalf("defer_tools = %v", cfg.DeferTools)
	}

	res, err = loadAgentKeys(t, "[agents.coder]\ndescription = \"x\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Config.Agents["coder"].DeferTools; got != nil {
		t.Fatalf("defer_tools absent = %v, want nil", got)
	}
}

// API-DTL-002: [mcp.<name>] defer is an optional boolean.
func TestMCPDeferValidation(t *testing.T) {
	for _, table := range []string{
		"[mcp.docs]\ncommand = \"cmd\"\ndefer = true\n",
		"[mcp.docs]\ncommand = \"cmd\"\ndefer = false\n",
	} {
		if _, err := loadAgentKeys(t, table); err != nil {
			t.Fatalf("unexpected error for %q: %v", table, err)
		}
	}
	errs := agentKeyErrors(t, "[mcp.docs]\ncommand = \"cmd\"\ndefer = \"yes\"\n")
	found := findError(errs, "mcp.docs.defer")
	if found == nil || !strings.Contains(found.Msg, "boolean") {
		t.Fatalf("defer error = %+v", found)
	}
}

// API-DTL-002: defer loads into MCPConfig.Defer, nil when absent.
func TestMCPDeferLoaded(t *testing.T) {
	res, err := loadAgentKeys(t, "[mcp.docs]\ncommand = \"cmd\"\ndefer = false\n")
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := res.Config.MCP["docs"]
	if !ok || cfg.Defer == nil || *cfg.Defer {
		t.Fatalf("defer = %v", cfg.Defer)
	}

	res, err = loadAgentKeys(t, "[mcp.docs]\ncommand = \"cmd\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Config.MCP["docs"].Defer; got != nil {
		t.Fatalf("defer absent = %v, want nil", got)
	}
}
