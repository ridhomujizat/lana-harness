package claudecode

import (
	"encoding/json"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestToolRisk(t *testing.T) {
	cases := map[string]core.Risk{
		"Read": core.RiskRead, "Glob": core.RiskRead, "Grep": core.RiskRead, "LS": core.RiskRead, "NotebookRead": core.RiskRead,
		"Edit": core.RiskWrite, "Write": core.RiskWrite, "MultiEdit": core.RiskWrite, "NotebookEdit": core.RiskWrite,
		"Bash": core.RiskExec, "BashOutput": core.RiskExec, "KillShell": core.RiskExec,
		"WebFetch": core.RiskNetwork, "WebSearch": core.RiskNetwork,
		"mcp__github__create_issue": core.RiskExec, "Task": core.RiskExec, "SomethingNew": core.RiskExec,
	}
	for name, want := range cases {
		if got := ToolRisk(name); got != want {
			t.Errorf("ToolRisk(%s) = %s, want %s", name, got, want)
		}
	}
}

func TestRuleStringAndPermCall(t *testing.T) {
	cases := []struct {
		tool, input, rule, permTool, permInput string
	}{
		{"Bash", `{"command":"rm -rf x","description":"d"}`, "bash(rm -rf x)", "bash", `{"command":"rm -rf x"}`},
		{"Read", `{"file_path":"/etc/hosts"}`, "read(/etc/hosts)", "read_file", `{"path":"/etc/hosts"}`},
		{"Write", `{"file_path":"a.txt","content":"x"}`, "write(a.txt)", "write_file", `{"path":"a.txt"}`},
		{"Edit", `{"file_path":"a.go","old_string":"x","new_string":"y"}`, "edit(a.go)", "edit_file", `{"path":"a.go"}`},
		{"MultiEdit", `{"file_path":"a.go","edits":[]}`, "multiedit(a.go)", "edit_file", `{"path":"a.go"}`},
		{"Glob", `{"pattern":"*.go","path":"src"}`, "glob(src)", "glob", `{"path":"src"}`},
		{"Grep", `{"pattern":"x"}`, "grep", "grep", `{"path":""}`},
		{"WebFetch", `{"url":"https://example.com","prompt":"p"}`, "webfetch(https://example.com)", "web_fetch", `{"url":"https://example.com"}`},
		{"WebSearch", `{"query":"go"}`, "websearch(go)", "websearch", `{"query":"go"}`},
		{"mcp__demo__echo", `{"text":"x"}`, "mcp__demo__echo", "mcp__demo__echo", `{"text":"x"}`},
	}
	for _, c := range cases {
		if got := RuleString(c.tool, json.RawMessage(c.input)); got != c.rule {
			t.Errorf("RuleString(%s) = %q, want %q", c.tool, got, c.rule)
		}
		tool, input := PermCall(c.tool, json.RawMessage(c.input))
		if tool != c.permTool || string(input) != c.permInput {
			t.Errorf("PermCall(%s) = %s %s, want %s %s", c.tool, tool, input, c.permTool, c.permInput)
		}
	}
}
