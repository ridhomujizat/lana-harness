package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// recordedUsage is the /usage answer of Claude Code 2.1.319 on a Max plan
// (2026-11-08), with the contribution list shortened.
const recordedUsage = `You are currently using your subscription to power your Claude Code usage

Current session: 43% used · resets Nov 8 at 3:39pm (Asia/Jakarta)
Current week (all models): 42% used · resets Nov 12 at 11:59am (Asia/Jakarta)
Current week (Fable): 0% used · resets Nov 12 at 12pm (Asia/Jakarta)

What's contributing to your usage?
Approximate · sessions on this machine only · shares overlap
Last 24h · 2962 requests · 15 sessions
  99%  Give "general-purpose" subagents a cheaper model
`

func jakarta(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	return loc
}

func utilOf(w core.RuntimeLimitWindow) float64 {
	if w.Utilization == nil {
		return -1
	}
	return *w.Utilization
}

// Spec 39 Revision 4: the recorded answer gives the session, weekly, and
// per-model weekly windows with their reset times in the named zone.
func TestParseUsageRecorded(t *testing.T) {
	loc := jakarta(t)
	now := time.Date(2026, 11, 8, 13, 0, 0, 0, loc)
	report, err := parseUsageText(recordedUsage, now)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Subscription || len(report.Windows) != 3 {
		t.Fatalf("report = %+v", report)
	}
	want := []struct {
		typ, label string
		util       float64
		resets     time.Time
	}{
		{"five_hour", "", 0.43, time.Date(2026, 11, 8, 15, 39, 0, 0, loc)},
		{"seven_day", "", 0.42, time.Date(2026, 11, 12, 11, 59, 0, 0, loc)},
		{"seven_day_fable", "Weekly · Fable", 0, time.Date(2026, 11, 12, 12, 0, 0, 0, loc)},
	}
	for i, w := range want {
		got := report.Windows[i]
		if got.Type != w.typ || got.Label != w.label || utilOf(got) != w.util || !got.ResetsAt.Equal(w.resets) {
			t.Fatalf("window %d = %+v (util %v), want %+v", i, got, utilOf(got), w)
		}
		if got.Status != "allowed" || got.ResetsAt.Location() != time.UTC {
			t.Fatalf("window %d status/zone = %q %v", i, got.Status, got.ResetsAt.Location())
		}
	}
}

func TestParseUsageVariants(t *testing.T) {
	loc := jakarta(t)
	now := time.Date(2026, 12, 30, 10, 0, 0, 0, loc)
	cases := []struct {
		name   string
		text   string
		typ    string
		label  string
		util   float64
		status string
		resets time.Time // zero: none
	}{
		{"relative", "Current session: 7% used · resets in 2h 5m", "five_hour", "", 0.07, "allowed",
			now.Add(2*time.Hour + 5*time.Minute)},
		{"relative days", "Current week (all models): 10% used · resets in 3d 4h", "seven_day", "", 0.10, "allowed",
			now.Add(76 * time.Hour)},
		{"no reset", "Current session: 12% used", "five_hour", "", 0.12, "allowed", time.Time{}},
		{"year rollover", "Current week (all models): 50% used · resets Jan 2 at 9am (Asia/Jakarta)", "seven_day", "", 0.5, "allowed",
			time.Date(2027, 1, 2, 9, 0, 0, 0, loc)},
		{"yesterday stays", "Current session: 5% used · resets Dec 29 at 11pm (Asia/Jakarta)", "five_hour", "", 0.05, "allowed",
			time.Date(2026, 12, 29, 23, 0, 0, 0, loc)},
		{"full limit", "Current session: 100% used · resets Dec 30 at 12am (Asia/Jakarta)", "five_hour", "", 1, "rejected",
			time.Date(2026, 12, 30, 0, 0, 0, 0, loc)},
		{"model with space", "  Current week (Sonnet only): 3.5% used • resets Jan 1, 2027 at 1:30pm (UTC)", "seven_day_sonnet_only", "Weekly · Sonnet only", 0.035, "allowed",
			time.Date(2027, 1, 1, 13, 30, 0, 0, time.UTC)},
		{"time only", "Current session: 20% used · resets 3pm (Asia/Jakarta)", "five_hour", "", 0.2, "allowed",
			time.Date(2026, 12, 30, 15, 0, 0, 0, loc)},
		{"unknown zone", "Current session: 20% used · resets Dec 30 at 3pm (Mars/Base)", "five_hour", "", 0.2, "allowed",
			time.Date(2026, 12, 30, 15, 0, 0, 0, loc)},
		{"unreadable reset", "Current session: 20% used · resets soon", "five_hour", "", 0.2, "allowed", time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			report, err := parseUsageText("intro\n"+c.text+"\n", now)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Windows) != 1 {
				t.Fatalf("windows = %+v", report.Windows)
			}
			got := report.Windows[0]
			if got.Type != c.typ || got.Label != c.label || got.Status != c.status {
				t.Fatalf("window = %+v", got)
			}
			if d := utilOf(got) - c.util; d > 1e-9 || d < -1e-9 {
				t.Fatalf("utilization = %v, want %v", utilOf(got), c.util)
			}
			if !got.ResetsAt.Equal(c.resets) {
				t.Fatalf("resets = %v, want %v", got.ResetsAt, c.resets)
			}
		})
	}
}

func TestParseUsageNoWindows(t *testing.T) {
	now := time.Now()
	report, err := parseUsageText("You are currently using API usage billing for Claude Code.\n", now)
	if err != nil || report.Subscription || len(report.Windows) != 0 {
		t.Fatalf("api key answer = %+v, %v", report, err)
	}
	report, err = parseUsageText("/usage is only available for subscription plans.", now)
	if err != nil || report.Subscription {
		t.Fatalf("subscription-only answer = %+v, %v", report, err)
	}
	for text, want := range map[string]string{
		"Not logged in · Please run /login":                                                     "not logged in",
		"You are currently using your subscription to power your Claude Code usage\n\nLoading…": "no plan windows",
		"Something new entirely":                                                                "unknown form: Something new entirely",
		"":                                                                                      "no text",
	} {
		if _, err := parseUsageText(text, now); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: error = %v, want %q", text, err, want)
		}
	}
}

func TestUsageResult(t *testing.T) {
	ok := `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"from assistant"}]}}
{"type":"result","is_error":false,"result":"from result","usage":{"input_tokens":0,"output_tokens":0}}`
	if text, err := usageResult([]byte(ok)); err != nil || text != "from result" {
		t.Fatalf("text = %q, %v", text, err)
	}
	empty := strings.Replace(ok, `"result":"from result"`, `"result":""`, 1)
	if text, err := usageResult([]byte(empty)); err != nil || text != "from assistant" {
		t.Fatalf("fallback = %q, %v", text, err)
	}
	tokens := strings.Replace(ok, `"output_tokens":0`, `"output_tokens":5`, 1)
	if _, err := usageResult([]byte(tokens)); err == nil || !strings.Contains(err.Error(), "model call") {
		t.Fatalf("tokens = %v", err)
	}
	failed := strings.Replace(ok, `"is_error":false`, `"is_error":true`, 1)
	if _, err := usageResult([]byte(failed)); err == nil || !strings.Contains(err.Error(), "failed: from result") {
		t.Fatalf("is_error = %v", err)
	}
	if _, err := usageResult([]byte(`{"type":"system"}`)); err != errNoUsageResult {
		t.Fatalf("no result = %v", err)
	}
}

func (h *harness) usageLogs() []logEntry {
	var out []logEntry
	for _, e := range h.log() {
		if e.Kind == "usage" {
			out = append(out, e)
		}
	}
	return out
}

// Spec 39 Revision 4: the probe runs `claude -p /usage` with the account's
// folder, no token variable, stdin closed, and a new empty folder that is
// removed afterwards; no session is saved and user settings are skipped.
func TestUsageProbeProcess(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{TempDir: t.TempDir()}, tokenEnv...)
	dir := t.TempDir()
	report, err := h.rt.UsageProbe(context.Background(), core.RuntimeAccount{Name: "claude-2", ConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Subscription || len(report.Windows) != 3 || report.Windows[2].Label != "Weekly · Fable" {
		t.Fatalf("report = %+v", report)
	}
	logs := h.usageLogs()
	if len(logs) != 1 {
		t.Fatalf("usage runs = %d", len(logs))
	}
	e := logs[0]
	if got := strings.Join(e.Argv, " "); got != strings.Join(usageArgs, " ") {
		t.Fatalf("argv = %q", got)
	}
	if !strings.Contains(strings.Join(e.Argv, " "), "--no-session-persistence") {
		t.Fatal("no --no-session-persistence")
	}
	if !e.StdinEOF {
		t.Fatal("stdin was not closed")
	}
	checkAccountEnv(t, "usage", e.Env, dir)
	if !strings.HasPrefix(filepath.Base(e.Cwd), "lana-usage-") {
		t.Fatalf("cwd = %q", e.Cwd)
	}
	if _, err := os.Stat(e.Cwd); !os.IsNotExist(err) {
		t.Fatalf("probe folder left behind: %v", err)
	}

	// An account file overrides the answer: no subscription.
	writeFile(t, filepath.Join(dir, "fakeclaude_usage.txt"), "You are currently using API usage billing.")
	report, err = h.rt.UsageProbe(context.Background(), core.RuntimeAccount{Name: "claude-2", ConfigDir: dir})
	if err != nil || report.Subscription || len(report.Windows) != 0 {
		t.Fatalf("api answer = %+v, %v", report, err)
	}
}

func TestUsageProbeFailures(t *testing.T) {
	account := core.RuntimeAccount{Name: core.ClaudeCodeProvider}
	for mode, want := range map[string]string{
		"tokens":   "model call (12 input, 3 output tokens)",
		"error":    "Claude Code /usage failed",
		"noresult": "gave no answer: Error: something broke, see link",
	} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, "hi.jsonl", Options{}, "FAKECLAUDE_USAGE_MODE="+mode)
			_, err := h.rt.UsageProbe(context.Background(), account)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}

// A hanging probe is killed at the timeout; an api_key account runs nothing.
func TestUsageProbeTimeoutAndAPIKey(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{UsageProbeTimeout: 300 * time.Millisecond, KillGrace: 100 * time.Millisecond},
		"FAKECLAUDE_USAGE_MODE=hang")
	start := time.Now()
	_, err := h.rt.UsageProbe(context.Background(), core.RuntimeAccount{Name: core.ClaudeCodeProvider})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	before := len(h.usageLogs())
	report, err := h.rt.UsageProbe(context.Background(), core.RuntimeAccount{Name: "api", Auth: core.RuntimeAuthAPIKey, APIKeyEnv: "MY_CLAUDE_KEY"})
	if err != nil || report.Subscription || len(h.usageLogs()) != before {
		t.Fatalf("api_key = %+v, %v", report, err)
	}
}
