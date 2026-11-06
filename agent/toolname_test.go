package agent

import (
	"strings"
	"testing"
)

// AC-LPG-020: ReadFile, read-file, and read_file_tool all run read_file.
func TestRepairToolNameNormalizes(t *testing.T) {
	offered := []string{"read_file"}
	for _, name := range []string{"ReadFile", "read-file", "read_file_tool"} {
		if got := repairToolName(name, offered); got != "read_file" {
			t.Fatalf("repairToolName(%q)=%q want read_file", name, got)
		}
	}
}

// AC-LPG-021: an unprefixed name matches the <tool> part of the only offered
// MCP tool, and is unknown when two servers offer it.
func TestRepairToolNameMCP(t *testing.T) {
	if got := repairToolName("create_issue", []string{"mcp__github__create_issue"}); got != "mcp__github__create_issue" {
		t.Fatalf("repairToolName=%q want mcp__github__create_issue", got)
	}
	two := []string{"mcp__github__create_issue", "mcp__gitlab__create_issue"}
	if got := repairToolName("create_issue", two); got != "" {
		t.Fatalf("repairToolName with two servers=%q want empty", got)
	}
}

func TestRepairToolNameNoMatch(t *testing.T) {
	cases := []struct {
		name    string
		call    string
		offered []string
	}{
		{"unknown", "bash", []string{"read_file"}},
		{"empty name", "", []string{"read_file"}},
		{"whitespace name", "   ", []string{"read_file"}},
		{"no fuzzy", "read", []string{"read_file"}},
		{"several normalized matches", "read_file", []string{"read_file", "ReadFile"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := repairToolName(tc.call, tc.offered); got != "" {
				t.Fatalf("repairToolName(%q)=%q want empty", tc.call, got)
			}
		})
	}
}

// AC-LPG-022: read with read_file offered suggests read_file.
func TestUnknownToolTextSuggestion(t *testing.T) {
	got := unknownToolText("read", []string{"read_file"})
	want := "Tool 'read' does not exist. Did you mean 'read_file'? Available tools: read_file."
	if got != want {
		t.Fatalf("unknownToolText=%q want %q", got, want)
	}
}

func TestUnknownToolTextNoSuggestion(t *testing.T) {
	got := unknownToolText("bash", []string{"read_file"})
	want := "Tool 'bash' does not exist. Available tools: read_file."
	if got != want {
		t.Fatalf("unknownToolText=%q want %q", got, want)
	}
}

func TestUnknownToolTextEmptyName(t *testing.T) {
	want := "Tool call rejected: the tool name was empty. To call a tool, use a valid name from your tool list; otherwise reply in plain text."
	for _, name := range []string{"", "   "} {
		if got := unknownToolText(name, []string{"read_file"}); got != want {
			t.Fatalf("unknownToolText(%q)=%q want %q", name, got, want)
		}
	}
}

// AC-LPG-023: 20 offered tools list the first 12 names and the total.
func TestUnknownToolTextManyTools(t *testing.T) {
	offered := make([]string, 0, 20)
	for _, c := range "abcdefghijklmnopqrst" {
		offered = append(offered, string(c))
	}
	got := unknownToolText("zzz", offered)
	if !strings.Contains(got, "(20 total)") {
		t.Fatalf("text=%q missing total", got)
	}
	list := got[strings.Index(got, "Available tools: ")+len("Available tools: "):]
	head := strings.TrimSuffix(list, ", ... (20 total).")
	names := strings.Split(head, ", ")
	if len(names) != 12 {
		t.Fatalf("listed %d names want 12: %q", len(names), head)
	}
	if names[0] != "a" || names[11] != "l" {
		t.Fatalf("names=%v want first 12 sorted", names)
	}
}

func TestSimilarity(t *testing.T) {
	if got := similarity("read_file", "read_file"); got != 1 {
		t.Fatalf("similarity=%v want 1", got)
	}
	if got := similarity("read", "read_file"); got >= unknownToolSimilarity {
		t.Fatalf("plain normalized similarity=%v should stay below the cutoff", got)
	}
}
