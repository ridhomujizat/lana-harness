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

func TestBuildListsUnavailableMCPServer(t *testing.T) {
	got := Build(PromptInput{
		Agent:          "lead",
		UnavailableMCP: map[string]string{"dbx": "spawn ENOENT"},
	})
	if !strings.Contains(got, "- unavailable MCP servers: dbx (spawn ENOENT)") {
		t.Fatalf("prompt lacks the unavailable MCP server line:\n%s", got)
	}
}
