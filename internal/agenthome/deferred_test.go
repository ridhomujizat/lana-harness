package agenthome

import (
	"strings"
	"testing"
)

func sectionNames(in PromptInput) []string {
	var names []string
	for _, section := range BuildSections(in) {
		names = append(names, section.Name)
	}
	return names
}

// REQ-DTL-003: an empty DeferredTools list adds no section.
func TestDeferredToolsEmptyAddsNoSection(t *testing.T) {
	if got := Build(PromptInput{Agent: "lead"}); strings.Contains(got, "# Deferred tools") {
		t.Fatalf("empty deferred tools must add no section:\n%s", got)
	}
	for _, name := range sectionNames(PromptInput{Agent: "lead"}) {
		if name == SectionDeferredTools {
			t.Fatal("empty deferred tools must add no section")
		}
	}
}

// REQ-DTL-003: names are grouped by server, servers and names sorted, and no
// schema is part of the section.
func TestDeferredToolsGroupingAndSorting(t *testing.T) {
	in := PromptInput{
		Agent: "lead",
		DeferredTools: []string{
			"mcp__kerjan__b",
			"mcp__dbx__z",
			"mcp__kerjan__a",
			"mcp__dbx__a",
		},
	}
	got := Build(in)
	want := "# Deferred tools\n" +
		"These MCP tools are deferred: the prompt lists only their names, not their schemas.\n" +
		"Call tool_search with keywords, or select:<name>, to see a tool's description and parameters and load it.\n" +
		"You can also call a deferred tool directly by its name.\n" +
		"dbx: mcp__dbx__a, mcp__dbx__z\n" +
		"kerjan: mcp__kerjan__a, mcp__kerjan__b"
	if !strings.Contains(got, want) {
		t.Fatalf("deferred tools section:\n%s\n--- want:\n%s", got, want)
	}
	if strings.Contains(got, "input_schema") || strings.Contains(got, "Input schema") {
		t.Fatalf("deferred tools section must carry no schema:\n%s", got)
	}
}

// REQ-DTL-003: a name that is not mcp__<server>__<tool> is skipped, not
// rendered, and does not panic.
func TestDeferredToolsSkipsNonMCPNames(t *testing.T) {
	got := Build(PromptInput{Agent: "lead", DeferredTools: []string{"not_an_mcp_tool", "mcp__kerjan__a"}})
	if !strings.Contains(got, "kerjan: mcp__kerjan__a") {
		t.Fatalf("valid name is missing:\n%s", got)
	}
	if strings.Contains(got, "not_an_mcp_tool") {
		t.Fatalf("non-MCP name must be skipped:\n%s", got)
	}
	if strings.Contains(Build(PromptInput{Agent: "lead", DeferredTools: []string{"nope"}}), "# Deferred tools") {
		t.Fatal("a section with only non-MCP names must not render")
	}
}

// REQ-DTL-003: the section sits after the tool notes and before the blockers.
func TestDeferredToolsSectionOrder(t *testing.T) {
	in := PromptInput{
		Agent:         "lead",
		ToolNotes:     []ToolNote{{Name: "bash", Text: "note"}},
		DeferredTools: []string{"mcp__kerjan__a"},
	}
	names := strings.Join(sectionNames(in), ",")
	if !strings.Contains(names, SectionToolNotes+","+SectionDeferredTools+","+SectionBlockers) {
		t.Fatalf("section order = %s, want tool_notes,deferred_tools,blockers", names)
	}
}
