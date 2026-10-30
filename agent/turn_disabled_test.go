package agent

import (
	"slices"
	"testing"
)

// TestFilterDisabledTools covers the tool-list half of AC-WEB-049: built-in and
// MCP names are removed, unknown names are ignored, and the order is kept.
func TestFilterDisabledTools(t *testing.T) {
	tools := []string{"read_file", "bash", "mcp__github__create_issue", "task", "task_continue", "web_fetch"}
	cases := []struct {
		name     string
		disabled []string
		want     []string
	}{
		{
			name: "no disabled_tools key",
			want: tools,
		},
		{
			name:     "built-in",
			disabled: []string{"bash"},
			want:     []string{"read_file", "mcp__github__create_issue", "task", "task_continue", "web_fetch"},
		},
		{
			name:     "MCP reference",
			disabled: []string{"github/create_issue"},
			want:     []string{"read_file", "bash", "task", "task_continue", "web_fetch"},
		},
		{
			name:     "team tools",
			disabled: []string{"task", "task_continue"},
			want:     []string{"read_file", "bash", "mcp__github__create_issue", "web_fetch"},
		},
		{
			name:     "unknown names are ignored",
			disabled: []string{"not_a_tool", "other/create_issue"},
			want:     tools,
		},
		{
			name:     "exact model-facing MCP name",
			disabled: []string{"mcp__github__create_issue"},
			want:     []string{"read_file", "bash", "task", "task_continue", "web_fetch"},
		},
		{
			name:     "empty entry is ignored",
			disabled: []string{""},
			want:     tools,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := slices.Clone(tools)
			got := filterDisabledTools(input, tc.disabled)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("filterDisabledTools = %v, want %v", got, tc.want)
			}
			if !slices.Equal(input, tools) {
				t.Fatalf("filterDisabledTools mutated the input: %v", input)
			}
		})
	}
}

// TestMCPToolRef checks the parsing of the model-facing MCP name.
func TestMCPToolRef(t *testing.T) {
	cases := []struct {
		name string
		ref  string
		ok   bool
	}{
		{name: "mcp__github__create_issue", ref: "github/create_issue", ok: true},
		{name: "mcp__srv__tool__with__underscores", ref: "srv/tool__with__underscores", ok: true},
		{name: "bash", ok: false},
		{name: "mcp__github", ok: false},
		{name: "mcp__github__", ok: false},
	}
	for _, tc := range cases {
		ref, ok := mcpToolRef(tc.name)
		if ref != tc.ref || ok != tc.ok {
			t.Fatalf("mcpToolRef(%q) = %q, %v; want %q, %v", tc.name, ref, ok, tc.ref, tc.ok)
		}
	}
}
