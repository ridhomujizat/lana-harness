package agenthome

import (
	"strings"
	"testing"
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
