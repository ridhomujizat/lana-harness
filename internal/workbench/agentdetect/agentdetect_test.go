package agentdetect

import "testing"

func TestManifestsLoad(t *testing.T) {
	if manifestsErr != nil {
		t.Fatalf("loading bundled manifests failed: %v", manifestsErr)
	}
	if len(manifests) != len(bundledManifests) {
		t.Fatalf("loaded %d manifests, want %d", len(manifests), len(bundledManifests))
	}
	for _, bundled := range bundledManifests {
		rules, ok := manifests[bundled.label]
		if !ok {
			t.Errorf("manifest for %s is missing", bundled.label)
			continue
		}
		if len(rules) == 0 {
			t.Errorf("manifest for %s has no rules", bundled.label)
		}
	}
}

func TestIdentify(t *testing.T) {
	leaderJob := func(name string, argv ...string) Job {
		return Job{PGID: 100, Processes: []Process{{PID: 100, Name: name, Argv0: name, Argv: argv}}}
	}
	tests := []struct {
		label string
		job   Job
	}{
		{"claude", leaderJob("claude", "claude")},
		{"claude", leaderJob("claude-code", "claude-code")},
		{"codex", leaderJob("codex", "codex")},
		{"cursor", leaderJob("cursor-agent", "cursor-agent")},
		{"omp", leaderJob("omp", "omp")},
		{"opencode", leaderJob("opencode", "opencode")},
		{"gemini", leaderJob("gemini", "gemini")},
		{"muse", leaderJob("muse-bin-1.2.3", "muse-bin-1.2.3")},
		{"qwen", Job{PGID: 100, Processes: []Process{
			{PID: 100, Name: "node", Argv: []string{"node", "/x/bin/qwen"}},
		}}},
		{"cline", Job{PGID: 100, Processes: []Process{
			{PID: 100, Name: "bun", Argv: []string{"bun", "/opt/cline/bin/cline"}},
		}}},
		{"codex", Job{PGID: 42, Processes: []Process{
			{PID: 42, Name: "bash", Argv0: "bash", Argv: []string{"bash"}},
			{PID: 43, Name: "node", Argv: []string{"node", "/tmp/mcp/bin/codex"}},
		}}},
	}
	for _, tc := range tests {
		label, pid, ok := Identify(tc.job)
		if !ok {
			t.Errorf("Identify(%s job) = not found, want %q", tc.label, tc.label)
			continue
		}
		if label != tc.label {
			t.Errorf("Identify(%s job) = %q, want %q", tc.label, label, tc.label)
		}
		if pid <= 0 {
			t.Errorf("Identify(%s job) pid = %d", tc.label, pid)
		}
	}
}

func TestIdentifyUnknown(t *testing.T) {
	for _, name := range []string{"vim", "bash", "zsh", "node", "sleep", "muse-helper", "muse-bin"} {
		job := Job{PGID: 100, Processes: []Process{{PID: 100, Name: name, Argv0: name, Argv: []string{name}}}}
		if label, _, ok := Identify(job); ok {
			t.Errorf("Identify(%q) = %q, want not found", name, label)
		}
	}
}

func TestDetectClaudeBlocked(t *testing.T) {
	screen := "────────────────────────\n" +
		"Do you want to proceed?\n" +
		"❯ 1. Yes\n" +
		"  2. No\n" +
		"esc to cancel\n"
	got := Detect(agentClaude, Input{Screen: screen})
	if got.State != StateBlocked {
		t.Fatalf("Detect = %+v, want blocked", got)
	}
	if got.Skip {
		t.Fatal("Detect skip = true, want false")
	}
}

func TestDetectClaudeWorking(t *testing.T) {
	screen := "✻ Thinking… (3s · esc to interrupt)"
	got := Detect(agentClaude, Input{Screen: screen})
	if got.State != StateWorking {
		t.Fatalf("Detect = %+v, want working", got)
	}
}

func TestDetectClaudeIdlePromptBox(t *testing.T) {
	screen := "────────────────────────\n" +
		" ❯ \n" +
		"────────────────────────\n"
	got := Detect(agentClaude, Input{Screen: screen})
	if got.State != StateIdle {
		t.Fatalf("Detect = %+v, want idle", got)
	}
}

func TestDetectCodexOSCTitleBlocked(t *testing.T) {
	got := Detect(agentCodex, Input{OSCTitle: "Action Required"})
	if got.State != StateBlocked {
		t.Fatalf("Detect = %+v, want blocked", got)
	}
}

func TestDetectClaudeTranscriptViewerSkips(t *testing.T) {
	screen := "showing detailed transcript\nctrl+o to toggle\n"
	got := Detect(agentClaude, Input{Screen: screen})
	if got.State != StateUnknown {
		t.Fatalf("Detect state = %q, want unknown", got.State)
	}
	if !got.Skip {
		t.Fatal("Detect skip = false, want true")
	}
}

func TestDetectUnknownAgent(t *testing.T) {
	got := Detect("not-an-agent", Input{Screen: "whatever"})
	if got.State != StateUnknown {
		t.Fatalf("Detect = %+v, want unknown", got)
	}
}
