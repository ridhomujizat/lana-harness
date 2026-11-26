package antigravity

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// AC-AGY-012: the recorded /usage fixture becomes one window per group and
// bucket, with the plan reset times.
func TestUsageProbeParse(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "FAKEAGY_USAGE="+testdataFile(t, "usage.json"))
	report, err := h.rt.UsageProbe(context.Background(), core.RuntimeAccount{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Subscription || len(report.Windows) != 4 {
		t.Fatalf("report = %+v", report)
	}
	byType := map[string]core.RuntimeLimitWindow{}
	for _, w := range report.Windows {
		byType[w.Type] = w
	}
	g5, ok := byType["Gemini Models/5h"]
	if !ok {
		t.Fatalf("missing Gemini 5h window: %+v", report.Windows)
	}
	if g5.Label != "Gemini Models · 5 hours" {
		t.Fatalf("5h label = %q", g5.Label)
	}
	if g5.Utilization == nil || math.Abs(*g5.Utilization-(1-0.9976739287376404)) > 1e-9 {
		t.Fatalf("5h utilization = %v", g5.Utilization)
	}
	if want := time.Date(2026, 11, 25, 20, 49, 23, 0, time.UTC); !g5.ResetsAt.UTC().Equal(want) {
		t.Fatalf("5h reset = %v", g5.ResetsAt)
	}
	gw, ok := byType["Gemini Models/weekly"]
	if !ok || gw.Label != "Gemini Models · weekly" {
		t.Fatalf("weekly window = %+v", gw)
	}
	if want := time.Date(2026, 12, 2, 9, 27, 16, 0, time.UTC); !gw.ResetsAt.UTC().Equal(want) {
		t.Fatalf("weekly reset = %v", gw.ResetsAt)
	}
	// A 1970 reset time means the window has not started: no reset time.
	c5 := byType["Claude and GPT models/5h"]
	if !c5.ResetsAt.IsZero() {
		t.Fatalf("1970 5h reset = %v", c5.ResetsAt)
	}
	if c5.Utilization == nil || *c5.Utilization != 0 {
		t.Fatalf("1970 5h utilization = %v", c5.Utilization)
	}
	if cw := byType["Claude and GPT models/weekly"]; !cw.ResetsAt.IsZero() {
		t.Fatalf("1970 weekly reset = %v", cw.ResetsAt)
	}
}

// The probe runs in a new empty folder that is removed afterwards.
func TestUsageProbeTempDir(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "FAKEAGY_USAGE="+testdataFile(t, "usage.json"))
	if _, err := h.rt.UsageProbe(context.Background(), core.RuntimeAccount{}); err != nil {
		t.Fatal(err)
	}
	cwd := ""
	for _, e := range h.starts() {
		if len(e.Argv) > 0 && e.Argv[0] == "-p" {
			cwd = e.Cwd
		}
	}
	if cwd == "" || !strings.HasPrefix(filepath.Base(cwd), "lana-usage-") {
		t.Fatalf("cwd = %q", cwd)
	}
	if _, err := os.Stat(cwd); !os.IsNotExist(err) {
		t.Fatalf("the temp folder was not removed: %v", err)
	}
}

// A bad status, a token count, or a missing answer all fail the probe.
func TestUsageProbeFailures(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct{ name, file string }{
		{"bad status", write("bad.json", `{"status":"FAILED","usage":{"input_tokens":0,"output_tokens":0},"command":{"data":{"groups":[]}}}`)},
		{"input tokens", write("in.json", `{"status":"SUCCESS","usage":{"input_tokens":3,"output_tokens":0},"command":{"data":{"groups":[]}}}`)},
		{"output tokens", write("out.json", `{"status":"SUCCESS","usage":{"input_tokens":0,"output_tokens":7},"command":{"data":{"groups":[]}}}`)},
		{"unparseable", write("junk.json", "not json at all")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "acp_turn1.jsonl", Options{}, "FAKEAGY_USAGE="+tc.file)
			if _, err := h.rt.UsageProbe(context.Background(), core.RuntimeAccount{}); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	// Missing JSON: no FAKEAGY_USAGE.
	h := newHarness(t, "acp_turn1.jsonl", Options{})
	if _, err := h.rt.UsageProbe(context.Background(), core.RuntimeAccount{}); err == nil {
		t.Fatal("expected an error for a missing answer")
	}
}
