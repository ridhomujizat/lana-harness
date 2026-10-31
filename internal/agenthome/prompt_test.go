package agenthome

import (
	"strings"
	"testing"
	"time"
)

func TestBuildRosterAfterPersona(t *testing.T) {
	got := Build(PromptInput{
		Persona: "You lead the team.",
		Roster: []RosterEntry{
			{Name: "researcher", Description: "Finds sources"},
			{Name: "writer"},
			{Name: "explorer", Description: "Looks around"},
		},
		Agent: "lead",
	})
	want := "# Your team\n- researcher: Finds sources\n- writer\n- explorer: Looks around\n" +
		"When a task fits a teammate's role, use the task tool to delegate it, and task_continue to follow up on it; this keeps each session focused on its own work."
	if !strings.Contains(got, want) {
		t.Fatalf("prompt lacks the roster section:\n%s", got)
	}
	persona := strings.Index(got, "You lead the team.")
	team := strings.Index(got, "# Your team")
	runtime := strings.Index(got, "# Runtime context")
	if persona < 0 || team < persona || runtime < team {
		t.Fatalf("roster section is not after the persona:\n%s", got)
	}
}

func TestBuildWithoutRoster(t *testing.T) {
	got := Build(PromptInput{Persona: "You lead the team.", Agent: "lead"})
	if strings.Contains(got, "# Your team") || strings.Contains(got, "task_continue") {
		t.Fatalf("empty roster must add no section:\n%s", got)
	}
}

// REQ-ECX-012: the general blocker rules are always present, and the MCP rules
// appear only when the agent has an MCP server.
const (
	blockersHeading     = "# When something blocks you"
	blockGeneralRuleOne = "- When you cannot finish the request, say so plainly and say what is still needed, so the owner can decide the next step."
	blockGeneralRuleTwo = "- Talk about tools, MCP servers, or their status only when the current request needs them or the owner asks; unrelated status reports are noise."
	blockMCPRuleOne     = "- When an MCP server you need is unavailable or a call to it fails, say so in your answer: name it, quote the error, say what you could not do, and say what the owner can do (for example: check the server in Settings → MCP, add a secret, or retry)."
	blockMCPRuleTwo     = "- Do not work around a missing or failing MCP server by calling it or its API by hand (for example with bash), because that bypasses the server's own setup and status."
)

func TestBuildReportsBlockers(t *testing.T) {
	cases := []struct {
		name    string
		persona string
		hasMCP  bool
	}{
		{name: "no persona"},
		{name: "persona", persona: "You lead the team."},
		{name: "mcp", hasMCP: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Build(PromptInput{Agent: "lead", Persona: tc.persona, HasMCP: tc.hasMCP})
			for _, want := range []string{blockersHeading, blockGeneralRuleOne, blockGeneralRuleTwo} {
				if !strings.Contains(got, want) {
					t.Fatalf("prompt lacks %q:\n%s", want, got)
				}
			}
			for _, mcpRule := range []string{blockMCPRuleOne, blockMCPRuleTwo} {
				if found := strings.Contains(got, mcpRule); found != tc.hasMCP {
					t.Fatalf("MCP rule present=%v want %v for %q:\n%s", found, tc.hasMCP, mcpRule, got)
				}
			}
			blockers := strings.Index(got, blockersHeading)
			runtime := strings.Index(got, "# Runtime context")
			if blockers < 0 || runtime < 0 || blockers > runtime {
				t.Fatalf("blocker section is not before the runtime context:\n%s", got)
			}
			if tc.persona != "" {
				if persona := strings.Index(got, tc.persona); persona < 0 || persona > blockers {
					t.Fatalf("blocker section is not after the persona:\n%s", got)
				}
			}
		})
	}
}

// The head prompt must not carry the unavailable MCP list; it moves to the
// <context> block (REQ-LTC-001).
func TestBuildOmitsUnavailableMCPServer(t *testing.T) {
	got := Build(PromptInput{
		Agent:          "lead",
		UnavailableMCP: map[string]string{"dbx": "spawn ENOENT"},
	})
	if strings.Contains(got, "unavailable MCP servers") {
		t.Fatalf("head prompt must not carry the unavailable MCP list:\n%s", got)
	}
}

func TestContextBlock(t *testing.T) {
	got := ContextBlock(PromptInput{
		Agent: "lead",
		Now:   time.Date(2026, 10, 31, 14, 5, 0, 0, time.FixedZone("WIB", 7*3600)),
		UnavailableMCP: map[string]string{
			"b": "down", "a": "spawn ENOENT",
		},
	})
	want := "<context>\n" +
		"date: 2026-10-31 Saturday 14:05 WIB (UTC+07:00)\n" +
		"unavailable MCP servers: a (spawn ENOENT), b (down)\n" +
		"</context>"
	if got != want {
		t.Fatalf("context:\n%s\n--- want:\n%s", got, want)
	}
}

func TestContextBlockWithoutMCP(t *testing.T) {
	got := ContextBlock(PromptInput{Agent: "lead"})
	if strings.Contains(got, "unavailable MCP servers") || !strings.HasPrefix(got, "<context>\ndate: ") || !strings.HasSuffix(got, "\n</context>") {
		t.Fatalf("context without MCP:\n%s", got)
	}
}

// REQ-JOB-017: the background-jobs line is appended to <context> when present
// and never leaks into the stable head prompt.
func TestContextBlockJobs(t *testing.T) {
	line := "background jobs: bash-ab12 running (npm run dev, 4m)"
	got := ContextBlock(PromptInput{Agent: "lead", Jobs: line})
	if !strings.Contains(got, "\n"+line+"\n</context>") {
		t.Fatalf("context lacks the jobs line:\n%s", got)
	}
	if strings.Contains(ContextBlock(PromptInput{Agent: "lead"}), "background jobs") {
		t.Fatal("empty Jobs must add no line")
	}
	if strings.Contains(Build(PromptInput{Agent: "lead", Jobs: line}), "background jobs") {
		t.Fatal("head prompt carries the volatile jobs line")
	}
}

// REQ-ECX-010: # Tool notes holds one block per offered tool's guidance, in
// tool-name order, after # Skills and before the blocker section.
func TestBuildToolNotes(t *testing.T) {
	in := PromptInput{
		Agent:  "lead",
		Skills: []Skill{{Name: "deploy", Description: "Ship it"}},
		ToolNotes: []ToolNote{
			{Name: "edit_file", Text: "Read before editing."},
			{Name: "bash", Text: "Use run_in_background for long commands."},
		},
		HasMCP: true,
	}
	got := Build(in)
	skills := strings.Index(got, "# Skills")
	notes := strings.Index(got, "# Tool notes")
	blockers := strings.Index(got, blockersHeading)
	if skills < 0 || notes < 0 || blockers < 0 || notes < skills || notes > blockers {
		t.Fatalf("tool notes position wrong (skills=%d notes=%d blockers=%d):\n%s", skills, notes, blockers, got)
	}
	want := "# Tool notes\n## bash\nUse run_in_background for long commands.\n\n## edit_file\nRead before editing."
	if !strings.Contains(got, want) {
		t.Fatalf("prompt lacks the ordered tool notes block:\n%s", got)
	}

	var names []string
	for _, section := range BuildSections(in) {
		names = append(names, section.Name)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, SectionSkills+","+SectionToolNotes+","+SectionBlockers) {
		t.Fatalf("section order=%s want skills,tool_notes,blockers", joined)
	}

	if strings.Contains(Build(PromptInput{Agent: "lead"}), "# Tool notes") {
		t.Fatal("no guidance must add no Tool notes section")
	}
	if strings.Contains(Build(PromptInput{Agent: "lead", ToolNotes: []ToolNote{{Name: "bash", Text: "  "}}}), "# Tool notes") {
		t.Fatal("empty guidance must add no Tool notes section")
	}
	// Without # Skills the notes still sit before the blockers.
	only := Build(PromptInput{Agent: "lead", ToolNotes: []ToolNote{{Name: "bash", Text: "Short note."}}})
	if n, b := strings.Index(only, "# Tool notes"), strings.Index(only, blockersHeading); n < 0 || b < 0 || n > b {
		t.Fatalf("tool notes without skills are misplaced:\n%s", only)
	}
}

// REQ-ECX-001: every fixed section text states its scope calmly, without
// emphatic capitals or absolute triggers. Persona templates are excluded.
func TestFixedSectionWording(t *testing.T) {
	inputs := []PromptInput{
		{
			Persona:   "You are helper.",
			User:      []string{"Name: Ridho"},
			Memory:    []string{"Server kenari"},
			ToolNotes: []ToolNote{{Name: "bash", Text: "When a command runs long, start it with run_in_background."}},
			Skills:    []Skill{{Name: "deploy", Description: "Ship it"}},
			Roster:    []RosterEntry{{Name: "researcher", Description: "Finds sources"}},
			Agent:     "lead",
			Channel:   "cli",
			Workspace: "/tmp/x",
			HasMCP:    true,
		},
		{Agent: "lead", Memory: []string{strings.Repeat("m", MemoryLimit+10)}},
	}
	banned := []string{"MUST", "ALWAYS", "NEVER", "CRITICAL", "IMPORTANT", "after every message"}
	for _, in := range inputs {
		for _, section := range BuildSections(in) {
			if section.Name == SectionPersona {
				continue
			}
			for _, word := range banned {
				if strings.Contains(section.Text, word) {
					t.Errorf("section %s uses %q:\n%s", section.Name, word, section.Text)
				}
			}
		}
	}
}
