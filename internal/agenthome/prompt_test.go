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
		"Use the task tool to delegate work to a teammate, and task_continue to follow up."
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

// The three blocker rules must appear verbatim in every prompt (AC-TFB-018).
const (
	blockersHeading = "# When something blocks you"
	blockRuleOne    = "- If a tool or MCP server you need is unavailable, or a call to it fails, say so in your answer: name it, quote the error, say what you could not do, and say what the owner can do (for example: check the server in Settings → MCP, add a secret, or retry)."
	blockRuleTwo    = "- Do not work around a missing or failing MCP server by calling it or its API by hand (for example with bash)."
	blockRuleThree  = "- If you cannot finish the request, say so plainly and say what is still needed. Never end a turn without telling the owner the result."
)

func TestBuildReportsBlockers(t *testing.T) {
	cases := []struct {
		name    string
		persona string
	}{
		{name: "no persona"},
		{name: "persona", persona: "You lead the team."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Build(PromptInput{Agent: "lead", Persona: tc.persona})
			for _, want := range []string{blockersHeading, blockRuleOne, blockRuleTwo, blockRuleThree} {
				if !strings.Contains(got, want) {
					t.Fatalf("prompt lacks %q:\n%s", want, got)
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

// REQ-MEM-001: the memory capture section is present only when MemoryCapture is
// set, and it sits between the memory section and skills.
func TestBuildMemoryCaptureSection(t *testing.T) {
	on := BuildSections(PromptInput{
		Agent: "lead", Memory: []string{"Server kenari"},
		Skills:        []Skill{{Name: "deploy", Description: "Ship it"}},
		MemoryCapture: true,
	})
	var names []string
	for _, section := range on {
		names = append(names, section.Name)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, SectionMemory+","+SectionMemoryCapture+","+SectionSkills) {
		t.Fatalf("section order=%s want memory,memory_capture,skills", joined)
	}
	var capture Section
	for _, section := range on {
		if section.Name == SectionMemoryCapture {
			capture = section
		}
	}
	if capture.Text != memoryCaptureRules || !strings.HasPrefix(capture.Text, "# Memory capture\n") {
		t.Fatalf("memory capture text:\n%s", capture.Text)
	}

	off := BuildSections(PromptInput{Agent: "lead"})
	for _, section := range off {
		if section.Name == SectionMemoryCapture {
			t.Fatalf("section present without MemoryCapture: %+v", off)
		}
	}

	// The section does not depend on the agent having memory entries yet.
	fresh := BuildSections(PromptInput{Agent: "lead", MemoryCapture: true})
	found := false
	for _, section := range fresh {
		if section.Name == SectionMemoryCapture {
			found = true
		}
	}
	if !found {
		t.Fatalf("section missing without memory entries: %+v", fresh)
	}
}

// The fixed section keeps the prompt byte-identical between two turns.
func TestBuildMemoryCaptureStable(t *testing.T) {
	in := PromptInput{Agent: "lead", Memory: []string{"Server kenari"}, MemoryCapture: true}
	if first, second := Build(in), Build(in); first != second {
		t.Fatalf("prompt changed between builds:\n%s\n---\n%s", first, second)
	}
}
