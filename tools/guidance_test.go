package tools

import (
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// REQ-ECX-011: the initial guidance set, and REQ-ECX-001: calm wording without
// emphatic capitals or absolute triggers.
func TestPromptGuidance(t *testing.T) {
	cases := []struct {
		name string
		tool core.Tool
		want []string
	}{
		{
			name: "edit_file",
			tool: NewEditFile(nil),
			want: []string{"Read the file, or the relevant range, before editing it", "edit fails, read the file again", "one edit per logical change"},
		},
		{
			name: "bash",
			tool: NewBash(nil),
			want: []string{"run_in_background and read its output with job_output", "reports ready, check it with a request"},
		},
		{
			name: "ask_user",
			tool: NewAskUser(),
			want: []string{"decision belongs to them", "Do not ask for something the tools can find out"},
		},
		{
			name: "memory_write",
			tool: NewMemoryWrite(),
			want: []string{"replace or remove that entry instead of adding a second one", "workaround that a tool now covers", "passwords, API keys, and tokens"},
		},
		{
			name: "skill_load",
			tool: NewSkillLoad(),
			want: []string{"Call skill_load with a skill's name before following that skill"},
		},
	}
	banned := []string{"MUST", "ALWAYS", "NEVER", "CRITICAL", "IMPORTANT", "after every message"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			guidance, ok := tc.tool.(core.ToolGuidance)
			if !ok {
				t.Fatalf("%s does not implement core.ToolGuidance", tc.name)
			}
			text := guidance.PromptGuidance()
			if strings.TrimSpace(text) == "" {
				t.Fatalf("%s returns empty guidance", tc.name)
			}
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Fatalf("guidance lacks %q:\n%s", want, text)
				}
			}
			for _, word := range banned {
				if strings.Contains(text, word) {
					t.Fatalf("guidance uses %q:\n%s", word, text)
				}
			}
		})
	}

	// memory_read shares the memory rules rendered under memory_write; it
	// returns "" so the text is not duplicated in # Tool notes.
	read, ok := NewMemoryRead().(core.ToolGuidance)
	if !ok {
		t.Fatal("memory_read does not implement core.ToolGuidance")
	}
	if text := read.PromptGuidance(); text != "" {
		t.Fatalf("memory_read guidance = %q, want empty", text)
	}
}
