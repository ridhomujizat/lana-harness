package calendar

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

type weekFixture struct {
	loc      *time.Location
	from, to time.Time
	now      time.Time
}

// newWeek is the week Mon 2026-11-02 .. Mon 2026-11-09 in Asia/Jakarta, read
// on Wednesday 2026-11-04 at 12:00.
func newWeek(t *testing.T) weekFixture {
	loc := mustLoc(t, "Asia/Jakarta")
	from := time.Date(2026, 11, 2, 0, 0, 0, 0, loc)
	return weekFixture{loc: loc, from: from, to: from.AddDate(0, 0, 7), now: time.Date(2026, 11, 4, 12, 0, 0, 0, loc)}
}

func (w weekFixture) at(day, hour, minute int) time.Time {
	return time.Date(2026, 11, day, hour, minute, 0, 0, w.loc)
}

// dailySlots is a cron "0 7 * * *" slot per day of the week, past included.
func (w weekFixture) dailySlots() []WorkflowSlot {
	var out []WorkflowSlot
	for d := 2; d <= 8; d++ {
		out = append(out, WorkflowSlot{WorkflowID: "wf_r", Name: "Daily research", Guardian: "Dana", NodeID: "every-day",
			Trigger: "schedule", At: w.at(d, 7, 0)})
	}
	return out
}

func (w weekFixture) input() BuildInput {
	return BuildInput{From: w.from, To: w.to, Now: w.now, Loc: w.loc}
}

func bySource(entries []Entry, source string) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.Source == source {
			out = append(out, e)
		}
	}
	return out
}

func findEntry(t *testing.T, entries []Entry, key string) Entry {
	t.Helper()
	for _, e := range entries {
		if e.Key == key {
			return e
		}
	}
	t.Fatalf("no entry %s in %v", key, keys(entries))
	return Entry{}
}

func keys(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Key
	}
	return out
}

func itemKey(id string, at time.Time) string { return fmt.Sprintf("item:%s:%d", id, at.UnixMilli()) }

// AC-CAL-003: a daily habit, an active daily cron workflow and one finished run.
func TestEntriesWeekACCAL003(t *testing.T) {
	w := newWeek(t)
	in := w.input()
	in.Items = []Item{{ID: "ci_h", Kind: KindHabit, Title: "Read", Date: "2026-10-01", Repeat: Repeat{Freq: FreqDaily}}}
	in.Slots = w.dailySlots()
	runAt := w.at(3, 7, 0)
	in.Runs = []WorkflowRun{
		{ID: "run_1", WorkflowID: "wf_r", Name: "Daily research", Trigger: "schedule", Status: "succeeded", Number: 4,
			StartedAt: runAt.UnixMilli(), EndedAt: runAt.Add(3 * time.Minute).UnixMilli()},
		{ID: "run_0", WorkflowID: "wf_r", Name: "Daily research", Status: "succeeded", Number: 3,
			StartedAt: w.from.Add(-time.Hour).UnixMilli()},
	}
	got := Build(in)
	habits := bySource(got, SourceItem)
	if len(habits) != 7 {
		t.Fatalf("habit entries %d, want 7: %v", len(habits), keys(habits))
	}
	slots := bySource(got, SourceWorkflowSlot)
	if len(slots) != 4 {
		t.Fatalf("slots %d, want 4 (Nov 5–8): %v", len(slots), keys(slots))
	}
	for _, s := range slots {
		if s.Start <= w.now.UnixMilli() || s.Status != StatusScheduled || s.Workflow.NodeID != "every-day" {
			t.Errorf("slot %+v", s)
		}
	}
	runs := bySource(got, SourceWorkflowRun)
	if len(runs) != 1 {
		t.Fatalf("runs %d, want 1", len(runs))
	}
	r := runs[0]
	if r.Key != "run:run_1" || r.Start != runAt.UnixMilli() || !r.Readonly || r.Status != "succeeded" ||
		r.Workflow.RunNumber != 4 || r.Workflow.RunStatus != "succeeded" || r.End == 0 {
		t.Errorf("run entry %+v %+v", r, r.Workflow)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Start < got[i-1].Start {
			t.Fatalf("not sorted at %d: %v", i, keys(got))
		}
	}

	// Read before the week starts: all 7 slots are in the future part.
	in.Now = w.from.Add(-time.Hour)
	if n := len(bySource(Build(in), SourceWorkflowSlot)); n != 7 {
		t.Fatalf("slots before the week %d, want 7", n)
	}
}

// AC-CAL-008: a daily habit done on the last 4 days, not yet today.
func TestEntriesDailyStreakACCAL008(t *testing.T) {
	w := newWeek(t)
	in := w.input()
	in.Items = []Item{{ID: "ci_h", Kind: KindHabit, Title: "Read", Date: "2026-10-01", Repeat: Repeat{Freq: FreqDaily}}}
	for _, d := range []string{"2026-10-30", "2026-10-31", "2026-11-01", "2026-11-02", "2026-11-03"} {
		in.Checks = append(in.Checks, HabitCheck{ItemID: "ci_h", Date: d, By: Owner})
	}
	// Oct 30 is done but Oct 29 is not; the 4 days before today are Oct 31..Nov 3.
	in.Checks = in.Checks[1:]
	todayKey := itemKey("ci_h", w.at(4, 0, 0))
	today := findEntry(t, Build(in), todayKey)
	if today.Habit == nil || today.Habit.Done || today.Habit.Streak != 4 {
		t.Fatalf("today before tick: %+v", today.Habit)
	}
	in.Checks = append(in.Checks, HabitCheck{ItemID: "ci_h", Date: "2026-11-04", By: Owner})
	entries := Build(in)
	today = findEntry(t, entries, todayKey)
	if !today.Habit.Done || today.Habit.Streak != 5 {
		t.Fatalf("today after tick: %+v", today.Habit)
	}
	// A past day counts back from that day; a future day uses today.
	if h := findEntry(t, entries, itemKey("ci_h", w.at(2, 0, 0))).Habit; !h.Done || h.Streak != 3 {
		t.Fatalf("Nov 2: %+v", h)
	}
	if h := findEntry(t, entries, itemKey("ci_h", w.at(6, 0, 0))).Habit; h.Done || h.Streak != 5 {
		t.Fatalf("Nov 6: %+v", h)
	}
}

// AC-CAL-009: a Mon/Wed/Fri habit done on the last 3 scheduled days, read on Tuesday.
func TestEntriesWeeklyStreakACCAL009(t *testing.T) {
	w := newWeek(t)
	in := w.input()
	in.Now = w.at(3, 10, 0) // Tuesday
	in.Items = []Item{{ID: "ci_w", Kind: KindHabit, Title: "Gym", Date: "2026-10-01",
		Repeat: Repeat{Freq: FreqWeekly, Days: []string{"mon", "wed", "fri"}}}}
	for _, d := range []string{"2026-10-28", "2026-10-30", "2026-11-02"} {
		in.Checks = append(in.Checks, HabitCheck{ItemID: "ci_w", Date: d})
	}
	entries := Build(in)
	if n := len(entries); n != 3 {
		t.Fatalf("entries %d, want Mon/Wed/Fri: %v", n, keys(entries))
	}
	for _, e := range entries {
		if e.Habit.Streak != 3 {
			t.Errorf("%s streak %d, want 3", e.Date, e.Habit.Streak)
		}
	}
	// A skipped occurrence neither counts nor breaks the streak.
	in.Checks = in.Checks[1:]
	in.Overrides = []Override{{Source: OverrideItem, Ref: "ci_w", Occurrence: time.Date(2026, 10, 28, 0, 0, 0, 0, w.loc).UnixMilli(), Op: OpSkip}}
	in.Checks = append(in.Checks, HabitCheck{ItemID: "ci_w", Date: "2026-10-26"})
	if h := Build(in)[0].Habit; h.Streak != 3 {
		t.Fatalf("streak across a skip %d, want 3", h.Streak)
	}
}

// AC-CAL-015: a 5-minute interval shows one all-day entry per day.
func TestEntriesCollapsedIntervalACCAL015(t *testing.T) {
	w := newWeek(t)
	in := w.input()
	in.From, in.To = w.at(5, 0, 0), w.at(6, 0, 0)
	for i := range 288 {
		in.Slots = append(in.Slots, WorkflowSlot{WorkflowID: "wf_p", Name: "Ping", NodeID: "tick", Trigger: "interval",
			At: w.at(5, 0, 0).Add(time.Duration(i) * 5 * time.Minute), EveryMinutes: 5, Collapsed: true})
	}
	got := Build(in)
	if len(got) != 1 {
		t.Fatalf("entries %d, want 1", len(got))
	}
	e := got[0]
	if !e.AllDay || e.Workflow.EveryMinutes != 5 || !e.Readonly || e.Date != "2026-11-05" || e.Start != w.at(5, 0, 0).UnixMilli() ||
		e.Key != fmt.Sprintf("wf:wf_p/tick:%d", w.at(5, 0, 0).UnixMilli()) || e.Workflow.Trigger != "interval" {
		t.Fatalf("collapsed entry %+v %+v", e, e.Workflow)
	}
	// A whole past day shows no collapsed row.
	in.From, in.To = w.at(3, 0, 0), w.at(4, 0, 0)
	in.Slots = []WorkflowSlot{{WorkflowID: "wf_p", NodeID: "tick", At: w.at(3, 0, 0), EveryMinutes: 5, Collapsed: true}}
	if got := Build(in); len(got) != 0 {
		t.Fatalf("past collapsed day: %v", keys(got))
	}
}

func TestEntriesItemOverrides(t *testing.T) {
	w := newWeek(t)
	in := w.input()
	base := w.at(1, 10, 0)
	in.Items = []Item{{ID: "ci_r", Kind: KindReminder, Title: "Water", Start: base.UnixMilli(), Repeat: Repeat{Freq: FreqDaily},
		CreatedBy: "agent:Dana", Assignee: Owner}}
	in.Overrides = []Override{
		{Source: OverrideItem, Ref: "ci_r", Occurrence: w.at(5, 10, 0).UnixMilli(), Op: OpSkip},
		{Source: OverrideItem, Ref: "ci_r", Occurrence: w.at(6, 10, 0).UnixMilli(), Op: OpMove, To: w.at(6, 15, 0).UnixMilli()},
		{Source: OverrideItem, Ref: "ci_r", Occurrence: w.at(7, 10, 0).UnixMilli(), Op: OpMove, To: w.at(12, 10, 0).UnixMilli()},
		{Source: OverrideItem, Ref: "ci_r", Occurrence: w.at(10, 10, 0).UnixMilli(), Op: OpMove, To: w.at(8, 18, 0).UnixMilli()},
		// Not a computed occurrence: ignored.
		{Source: OverrideItem, Ref: "ci_r", Occurrence: w.at(11, 11, 0).UnixMilli(), Op: OpMove, To: w.at(8, 19, 0).UnixMilli()},
	}
	got := Build(in)
	if len(got) != 7 {
		t.Fatalf("entries %d, want 7 (Nov 7 moved out, Nov 10 moved in): %v", len(got), keys(got))
	}
	skipped := findEntry(t, got, itemKey("ci_r", w.at(5, 10, 0)))
	if skipped.Status != StatusSkipped || skipped.Moved {
		t.Errorf("skipped %+v", skipped)
	}
	moved := findEntry(t, got, itemKey("ci_r", w.at(6, 10, 0)))
	if !moved.Moved || moved.Start != w.at(6, 15, 0).UnixMilli() || moved.Occurrence != w.at(6, 10, 0).UnixMilli() {
		t.Errorf("moved %+v", moved)
	}
	in2 := findEntry(t, got, itemKey("ci_r", w.at(10, 10, 0)))
	if !in2.Moved || in2.Date != "2026-11-08" || !in2.Repeating || in2.CreatedBy != "agent:Dana" || in2.Color != "amber" {
		t.Errorf("moved in %+v", in2)
	}
}

func TestEntriesSlotOverrides(t *testing.T) {
	w := newWeek(t)
	in := w.input()
	in.Slots = w.dailySlots()
	ref := "wf_r/every-day"
	in.Overrides = []Override{
		{Source: OverrideWorkflow, Ref: ref, Occurrence: w.at(3, 7, 0).UnixMilli(), Op: OpSkip},
		{Source: OverrideWorkflow, Ref: ref, Occurrence: w.at(5, 7, 0).UnixMilli(), Op: OpMove, To: w.at(5, 9, 30).UnixMilli()},
		{Source: OverrideWorkflow, Ref: ref, Occurrence: w.at(6, 7, 0).UnixMilli(), Op: OpMove, To: w.at(4, 11, 0).UnixMilli()},
	}
	got := bySource(Build(in), SourceWorkflowSlot)
	if len(got) != 4 {
		t.Fatalf("slots %d, want skipped Nov 3 + Nov 5 moved + Nov 7, 8: %v", len(got), keys(got))
	}
	sk := findEntry(t, got, fmt.Sprintf("wf:%s:%d", ref, w.at(3, 7, 0).UnixMilli()))
	if sk.Status != StatusSkipped {
		t.Errorf("past skipped slot %+v", sk)
	}
	mv := findEntry(t, got, fmt.Sprintf("wf:%s:%d", ref, w.at(5, 7, 0).UnixMilli()))
	if !mv.Moved || mv.Start != w.at(5, 9, 30).UnixMilli() || mv.Occurrence != w.at(5, 7, 0).UnixMilli() || mv.Readonly {
		t.Errorf("moved slot %+v", mv)
	}
}

func TestEntriesTasksFiringsGoogle(t *testing.T) {
	w := newWeek(t)
	in := w.input()
	remAt := w.at(4, 10, 0)
	in.Items = []Item{
		{ID: "ci_t1", Kind: KindTask, Title: "Pay", Date: "2026-11-05", CreatedBy: "agent:Dana"},
		{ID: "ci_t2", Kind: KindTask, Title: "Done", Date: "2026-11-05", Time: "15:00", State: StateDone},
		{ID: "ci_t3", Kind: KindTask, Title: "Weekly", Date: "2026-11-02", Repeat: Repeat{Freq: FreqWeekly, Days: []string{"mon", "fri"}}},
		{ID: "ci_rem", Kind: KindReminder, Title: "Call", Start: remAt.UnixMilli(),
			Action: &Action{Type: ActionSendMessage, Target: "telegram", Text: "Call mom"}},
		{ID: "ci_del", Kind: KindNote, Title: "Gone", Date: "2026-11-04", DeletedAt: 5},
		{ID: "ci_ev", Kind: KindEvent, Title: "Trip", AllDay: true, Date: "2026-11-07", EndDate: "2026-11-10", Color: "teal",
			Google: &GoogleRef{CalendarID: "primary", EventID: "g1", Sync: SyncPending}},
	}
	in.Checks = []HabitCheck{{ItemID: "ci_t3", Date: "2026-11-02"}}
	in.Firings = []Firing{{ItemID: "ci_rem", Occurrence: remAt.UnixMilli(), Status: FiringMissed}}
	in.Google = []GoogleEvent{
		{CalendarID: "primary", EventID: "g1", ItemID: "ci_ev", JSON: `{"summary":"Trip","start":{"date":"2026-11-07"},"end":{"date":"2026-11-11"}}`},
		{CalendarID: "primary", EventID: "g2", JSON: `{"summary":"Flight","start":{"dateTime":"2026-11-06T06:40:00+07:00"},"end":{"dateTime":"2026-11-06T08:40:00+07:00"}}`},
		{CalendarID: "work", EventID: "g3", JSON: `{"summary":"Offsite","start":{"date":"2026-11-03"},"end":{"date":"2026-11-05"}}`},
		{CalendarID: "work", EventID: "g4", JSON: `{"status":"cancelled","summary":"x","start":{"date":"2026-11-03"},"end":{"date":"2026-11-04"}}`},
		{CalendarID: "work", EventID: "g5", JSON: `{"summary":"Later","start":{"date":"2026-12-03"},"end":{"date":"2026-12-04"}}`},
	}
	in.GoogleCalendars = map[string]GoogleCalendarInfo{"primary": {Name: "Owner", Color: "#4285f4"}}
	got := Build(in)

	if e := findEntry(t, got, itemKey("ci_t1", w.at(5, 0, 0))); e.Status != StateOpen || e.Start != w.at(5, 0, 0).UnixMilli() || e.End != 0 || e.Color != "violet" {
		t.Errorf("open task %+v", e)
	}
	if e := findEntry(t, got, itemKey("ci_t2", w.at(5, 15, 0))); e.Status != StateDone {
		t.Errorf("done task %+v", e)
	}
	if e := findEntry(t, got, itemKey("ci_t3", w.at(2, 0, 0))); e.Status != StateDone || !e.Repeating {
		t.Errorf("repeating task Mon %+v", e)
	}
	if e := findEntry(t, got, itemKey("ci_t3", w.at(6, 0, 0))); e.Status != StateOpen {
		t.Errorf("repeating task Fri %+v", e)
	}
	rem := findEntry(t, got, itemKey("ci_rem", remAt))
	if rem.Status != FiringMissed || rem.Firing == nil || rem.Firing.Status != FiringMissed || rem.Action == nil || rem.End != 0 {
		t.Errorf("reminder %+v", rem)
	}
	trip := findEntry(t, got, itemKey("ci_ev", w.at(7, 0, 0)))
	if !trip.AllDay || trip.Date != "2026-11-07" || trip.EndDate != "2026-11-10" || trip.Google == nil || trip.Google.Sync != SyncPending {
		t.Errorf("all-day linked event %+v", trip)
	}
	flight := findEntry(t, got, "google:primary:g2")
	if !flight.Readonly || flight.Kind != KindEvent || flight.Google.CalendarName != "Owner" || flight.Google.Color != "#4285f4" ||
		flight.Start != w.at(6, 6, 40).UnixMilli() || flight.End != w.at(6, 8, 40).UnixMilli() || flight.CreatedBy != "google" {
		t.Errorf("google timed %+v %+v", flight, flight.Google)
	}
	off := findEntry(t, got, "google:work:g3")
	if !off.AllDay || off.Date != "2026-11-03" || off.EndDate != "2026-11-04" {
		t.Errorf("google all-day %+v", off)
	}
	for _, e := range got {
		if e.Key == "google:primary:g1" || e.Key == "google:work:g4" || e.Key == "google:work:g5" || e.ItemID == "ci_del" {
			t.Errorf("unexpected entry %s", e.Key)
		}
	}

	raw, err := json.Marshal(rem)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"habit":null`, `"workflow":null`, `"google":null`, `"firing":{"status":"missed"`, `"readonly":false`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("entry JSON %s lacks %s", raw, want)
		}
	}
}

func TestEntriesSortAndEmptyRange(t *testing.T) {
	w := newWeek(t)
	in := w.input()
	at := w.at(5, 9, 0).UnixMilli()
	in.Items = []Item{
		{ID: "b", Kind: KindReminder, Title: "Bravo", Start: at},
		{ID: "a", Kind: KindReminder, Title: "Alpha", Start: at},
		{ID: "c", Kind: KindReminder, Title: "Early", Start: at - 1},
	}
	got := Build(in)
	if len(got) != 3 || got[0].Title != "Early" || got[1].Title != "Alpha" || got[2].Title != "Bravo" {
		t.Fatalf("order %v", keys(got))
	}
	in.To = in.From
	if got := Build(in); got == nil || len(got) != 0 {
		t.Fatalf("empty range: %v", got)
	}
}
