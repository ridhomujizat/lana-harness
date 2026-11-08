package lana

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

// Spec 39 Revision 4 with the fake `claude`: a refresh probes `/usage` for
// the built-in account and every enabled subscription account in parallel,
// merges the windows into each account's snapshot with source usage_probe
// (stream windows of other types and their labels stay), records a failed
// probe as the snapshot error with the old values and time, skips disabled
// and api_key accounts, and probes an account at most once per 60 s.
func TestRefreshClaudeUsage(t *testing.T) {
	bin := buildFakeClaude(t)
	logPath := filepath.Join(t.TempDir(), "fake.jsonl")
	t.Setenv("FAKECLAUDE_LOG", logPath)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	homeDir := filepath.Join(t.TempDir(), "claude-home")
	brokenDir := filepath.Join(home, "claude", "claude-broken")
	for _, dir := range []string{homeDir, brokenDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(homeDir, "fakeclaude_usage.txt"),
		"Current session: 70% used · resets in 1h\nCurrent week (all models): 10% used\n")
	writeTestFile(t, filepath.Join(brokenDir, "fakeclaude_usage.txt"), "Not logged in · Please run /login")
	cfg := claudeAccountsConfig(bin, homeDir, `
[providers.claude-broken]
kind = "claude-code"
binary = "`+bin+`"

[providers.claude-off]
kind = "claude-code"
binary = "`+bin+`"
disabled = true

[providers.claude-key]
kind = "claude-code"
binary = "`+bin+`"
auth = "api_key"
api_key_env = "CLAUDE_KEY"

[agents.builtin]
model = "claude-code/haiku"
binary = "`+bin+`"
permission = "trust"
`)
	rt, err := New(context.Background(), Options{Home: home, Store: openRouterTestStore(t), Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	r := rt.Router()

	// Windows of earlier turns: claude-work has an Opus weekly window the
	// probe does not list; claude-broken has a five-hour window.
	util := 0.3
	r.UpdateLimits(router.LimitUpdate{Provider: "claude-work", Kind: "claude-code", MergeWindows: true, Windows: []router.Window{
		{Type: "five_hour", Status: "allowed", Utilization: &util, Source: router.WindowSourceStream},
		{Type: "seven_day_opus", Status: "allowed", Utilization: &util, Source: router.WindowSourceStream},
	}})
	brokenAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	r.UpdateLimits(router.LimitUpdate{Provider: "claude-broken", Kind: "claude-code", At: brokenAt, Windows: []router.Window{
		{Type: "five_hour", Status: "allowed", Utilization: &util, Source: router.WindowSourceStream},
	}})

	ctx := context.Background()
	rt.RefreshClaudeUsage(ctx)
	snaps := map[string]router.LimitSnapshot{}
	for _, snap := range r.Limits(ctx, false) {
		snaps[snap.Provider] = snap
	}
	windowOf := func(provider, typ string) router.Window {
		for _, w := range snaps[provider].Windows {
			if w.Type == typ {
				return w
			}
		}
		t.Fatalf("%s has no %s window: %+v", provider, typ, snaps[provider].Windows)
		return router.Window{}
	}
	used := func(w router.Window) float64 {
		if w.Utilization == nil {
			return -1
		}
		return *w.Utilization
	}

	work := snaps["claude-work"]
	if work.Error != "" || len(work.Windows) != 4 {
		t.Fatalf("claude-work = %+v", work)
	}
	if w := windowOf("claude-work", "five_hour"); used(w) != 0.43 || w.Source != router.WindowSourceUsageProbe || w.ResetsAt == nil {
		t.Fatalf("claude-work five_hour = %+v", w)
	}
	if w := windowOf("claude-work", "seven_day_opus"); w.Source != router.WindowSourceStream {
		t.Fatalf("claude-work seven_day_opus = %+v", w)
	}
	if w := windowOf("claude-work", "seven_day_fable"); w.Label != "Weekly · Fable" || w.Source != router.WindowSourceUsageProbe {
		t.Fatalf("claude-work seven_day_fable = %+v", w)
	}
	if w := windowOf("claude-home", "five_hour"); used(w) != 0.7 {
		t.Fatalf("claude-home five_hour = %+v", w)
	}
	if w := windowOf("claude-code", "seven_day"); used(w) != 0.42 {
		t.Fatalf("built-in seven_day = %+v", w)
	}
	broken := snaps["claude-broken"]
	if !strings.Contains(broken.Error, "not logged in") || len(broken.Windows) != 1 || used(broken.Windows[0]) != 0.3 ||
		!broken.UpdatedAt.Equal(brokenAt) {
		t.Fatalf("claude-broken = %+v", broken)
	}
	for _, name := range []string{"claude-off", "claude-key"} {
		if snap, ok := snaps[name]; ok && (len(snap.Windows) > 0 || snap.Error != "") {
			t.Fatalf("%s was probed: %+v", name, snap)
		}
	}

	// A later turn's windows keep the label and clear the probe error.
	rt.updateExternalLimits("claude-work", "claude-code", []core.RuntimeLimitWindow{{Type: "seven_day_fable", Status: "allowed"}})
	rt.updateExternalLimits("claude-broken", "claude-code", []core.RuntimeLimitWindow{{Type: "five_hour", Status: "allowed"}})
	for _, snap := range r.Limits(ctx, false) {
		snaps[snap.Provider] = snap
	}
	if w := windowOf("claude-work", "seven_day_fable"); w.Label != "Weekly · Fable" || w.Source != router.WindowSourceStream {
		t.Fatalf("claude-work seven_day_fable after a turn = %+v", w)
	}
	if snaps["claude-broken"].Error != "" {
		t.Fatalf("claude-broken error after a turn = %q", snaps["claude-broken"].Error)
	}
	usage := fakeClaudeLog(t, logPath, "usage")
	if len(usage) != 4 {
		t.Fatalf("usage probes = %d, want 4 (built-in, work, home, broken)", len(usage))
	}
	for _, line := range usage {
		if line["stdin_eof"] != true {
			t.Fatalf("stdin not closed: %v", line)
		}
	}

	// Throttled: a second refresh within 60 s runs nothing.
	rt.RefreshClaudeUsage(ctx)
	if n := len(fakeClaudeLog(t, logPath, "usage")); n != 4 {
		t.Fatalf("usage probes after the second refresh = %d", n)
	}
}
