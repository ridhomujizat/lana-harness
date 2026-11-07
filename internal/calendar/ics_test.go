package calendar

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

// API-CAL-011 / AC-CAL-023: one VEVENT per item occurrence and workflow
// slot; runs and Google events are left out.
func TestWriteICSGolden(t *testing.T) {
	w := newWeek(t)
	in := w.input()
	in.From, in.To = w.at(5, 0, 0), w.at(8, 0, 0)
	in.Now = w.at(4, 12, 0)
	in.Items = []Item{
		{ID: "ci_d", Kind: KindEvent, Title: "Dentist; check-up, 2nd", Start: w.at(5, 14, 0).UnixMilli(), End: w.at(5, 15, 0).UnixMilli(), CreatedBy: Owner},
		{ID: "ci_l", Kind: KindReminder, Title: "Laundry", Start: w.at(5, 10, 0).UnixMilli(), Repeat: Repeat{Freq: FreqDaily},
			Action: &Action{Type: ActionSendMessage, Target: "telegram", Text: "Take the laundry out\nand fold it"}},
		{ID: "ci_p", Kind: KindTask, Title: "Pay electricity", Date: "2026-11-06", CreatedBy: "agent:Dana", State: StateOpen},
		{ID: "ci_t", Kind: KindEvent, Title: "Bali holiday", AllDay: true, Date: "2026-11-06", EndDate: "2026-11-07"},
	}
	in.Overrides = []Override{{Source: OverrideItem, Ref: "ci_l", Occurrence: w.at(6, 10, 0).UnixMilli(), Op: OpSkip}}
	in.Slots = []WorkflowSlot{
		{WorkflowID: "wf_r", Name: "Daily research", Guardian: "Dana", NodeID: "every-day", Trigger: "schedule", At: w.at(5, 7, 0)},
		{WorkflowID: "wf_p", Name: "Ping", NodeID: "tick", Trigger: "interval", At: w.at(6, 0, 0), EveryMinutes: 5, Collapsed: true},
	}
	in.Runs = []WorkflowRun{{ID: "run_9", WorkflowID: "wf_r", Name: "Daily research", Status: "succeeded", StartedAt: w.at(5, 7, 0).UnixMilli()}}
	in.Google = []GoogleEvent{{CalendarID: "p", EventID: "g", JSON: `{"summary":"G","start":{"date":"2026-11-05"},"end":{"date":"2026-11-06"}}`}}
	entries := Build(in)

	var buf bytes.Buffer
	if err := WriteICS(&buf, entries, w.loc, time.Date(2026, 11, 4, 5, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()
	path := filepath.Join("testdata", "feed.ics")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ICS differs from %s (run with -update after checking):\n%s", path, got)
	}
	s := string(got)
	if n := strings.Count(s, "BEGIN:VEVENT"); n != 8 {
		t.Fatalf("VEVENTs %d, want 8 (3 laundry, dentist, task, trip, 2 slots)", n)
	}
	if strings.Contains(s, "run:") || strings.Contains(s, "google:") {
		t.Fatal("runs or Google events leaked into the feed")
	}
	for _, l := range strings.Split(strings.TrimSuffix(s, "\r\n"), "\r\n") {
		if strings.Contains(l, "\n") || len(l) > 75 {
			t.Fatalf("bad line %q", l)
		}
	}
}

func TestICSFoldAndEscape(t *testing.T) {
	if got := icsEscape("a\\b;c,d\r\ne\nf"); got != `a\\b\;c\,d\ne\nf` {
		t.Fatalf("escape %q", got)
	}
	long := "SUMMARY:" + strings.Repeat("é", 60) + strings.Repeat("x", 50)
	var b strings.Builder
	writeFolded(&b, long)
	out := b.String()
	if !strings.HasSuffix(out, "\r\n") {
		t.Fatal("no CRLF")
	}
	lines := strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n")
	if len(lines) < 3 {
		t.Fatalf("not folded: %q", lines)
	}
	var joined strings.Builder
	for i, l := range lines {
		if len(l) > 75 {
			t.Fatalf("line %d has %d octets", i, len(l))
		}
		if i > 0 {
			if !strings.HasPrefix(l, " ") {
				t.Fatalf("continuation %d lacks a space", i)
			}
			l = l[1:]
		}
		if !utf8.ValidString(l) {
			t.Fatalf("line %d splits a UTF-8 sequence", i)
		}
		joined.WriteString(l)
	}
	if joined.String() != long {
		t.Fatal("unfolding does not restore the line")
	}
	b.Reset()
	writeFolded(&b, "SHORT:x")
	if b.String() != "SHORT:x\r\n" {
		t.Fatalf("short line %q", b.String())
	}
}
