package lana

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// adminRiskTool is an admin-class tool for the catalog ask-first test.
type adminRiskTool struct{}

func (adminRiskTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "mcp_manage", InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (adminRiskTool) Risk(json.RawMessage) core.Risk { return core.RiskAdmin }
func (adminRiskTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusSuccess}, nil
}

// REQ-AMCP-006: an admin tool always asks first, whatever the policy or allow
// rules say, because the permission engine always asks for admin calls.
func TestToolAsksFirstAdminAlwaysAsks(t *testing.T) {
	cases := []struct {
		name string
		cfg  core.AgentConfig
	}{
		{name: "ask policy", cfg: core.AgentConfig{Permission: "ask"}},
		{name: "trust policy", cfg: core.AgentConfig{Permission: "trust"}},
		{name: "never policy", cfg: core.AgentConfig{Permission: "never"}},
		{name: "allow rule", cfg: core.AgentConfig{Permission: "ask", Rules: core.AgentRules{Allow: []string{"mcp_manage"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !toolAsksFirst(adminRiskTool{}, tc.cfg) {
				t.Fatal("admin tool reported as not asking first")
			}
		})
	}
}
