package claudecode

import (
	"slices"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// REQ-RPR-002: the argv of a prompt run, with --system-prompt and --effort
// only when set.
func TestBuildPromptArgs(t *testing.T) {
	got, err := buildPromptArgs(core.RuntimeConfig{Model: "sonnet", Effort: "high"}, "Be brief.")
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--model", "sonnet",
		"--tools", "", "--strict-mcp-config", "--setting-sources", "project,local", "--no-session-persistence", "--system-prompt", "Be brief.", "--effort", "high"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("args = %q %v", got, err)
	}
	got, _ = buildPromptArgs(core.RuntimeConfig{Model: "haiku"}, "")
	if slices.Contains(got, "--system-prompt") || slices.Contains(got, "--effort") {
		t.Fatalf("plain args = %q", got)
	}
	if _, err := buildPromptArgs(core.RuntimeConfig{Model: "-x"}, ""); err == nil {
		t.Fatal("a flag-like model was accepted")
	}
}

// REQ-RPR-005: Claude Code's failures map to router classes.
func TestPromptClass(t *testing.T) {
	for msg, want := range map[string]string{
		notLoggedInMessage:                         classAuth,
		"Claude AI usage limit reached|1794088200": classRateLimit,
		"There's an issue with the selected model (claude-x). It may not exist or you may not have access to it.": classUnknownModel,
		"Claude Code exited: boom": classRuntime,
	} {
		if got := promptClass(&mapper{}, msg); got != want {
			t.Fatalf("%q = %s, want %s", msg, got, want)
		}
	}
	if got := promptClass(&mapper{rejected: "five_hour"}, "x"); got != classRateLimit {
		t.Fatalf("rejected = %s", got)
	}
}
