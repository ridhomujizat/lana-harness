package calendar

import (
	"strings"
	"testing"
	"time"
)

// API-CAL-017: the daily agenda message of the contract.
func TestAgendaTextContractExample(t *testing.T) {
	w := newWeek(t)
	in := w.input()
	in.From, in.To = w.at(5, 0, 0), w.at(6, 0, 0)
	in.Now = w.at(5, 6, 0)
	dentist := w.at(5, 14, 0)
	in.Items = []Item{
		{ID: "ci_d", Kind: KindEvent, Title: "Dentist", Start: dentist.UnixMilli(), End: dentist.Add(time.Hour).UnixMilli(), CreatedBy: Owner},
		{ID: "ci_l", Kind: KindReminder, Title: "Take the laundry out", Start: w.at(5, 10, 0).UnixMilli(), CreatedBy: Owner},
		{ID: "ci_p", Kind: KindTask, Title: "Pay electricity", Date: "2026-11-05", CreatedBy: "agent:Dana"},
		{ID: "ci_r", Kind: KindHabit, Title: "Read 20 min", Date: "2026-10-01", Repeat: Repeat{Freq: FreqDaily}},
		{ID: "ci_w", Kind: KindHabit, Title: "Walk", Date: "2026-10-01", Repeat: Repeat{Freq: FreqDaily}},
	}
	for _, d := range []string{"2026-11-02", "2026-11-03", "2026-11-04", "2026-11-05"} {
		in.Checks = append(in.Checks, HabitCheck{ItemID: "ci_r", Date: d})
	}
	in.Slots = []WorkflowSlot{{WorkflowID: "wf_r", Name: "Daily research", NodeID: "n", Trigger: "schedule", At: w.at(5, 7, 0)}}
	today := Build(in)
	yesterday := []Entry{
		{Kind: KindTask, Title: "Weekly recap", Firing: &Firing{Status: FiringMissed},
			Action: &Action{Type: ActionAgentPrompt, Agent: "Dana", Prompt: "recap"}},
		{Kind: KindReminder, Title: "Fine", Firing: &Firing{Status: FiringDone}},
	}
	got := AgendaText(w.at(5, 7, 0), today, yesterday, w.loc)
	want := strings.Join([]string{
		"📅 Thu 5 Nov",
		"• 07:00 ⚙ Daily research (workflow)",
		"• 10:00 ⏰ Take the laundry out",
		"• 14:00–15:00 Dentist",
		"☐ Task: Pay electricity (added by Dana)",
		"✓ Habits: Read 20 min (🔥 4) · ☐ Walk",
		"⚠ Yesterday: 1 missed — Weekly recap (Dana)",
	}, "\n")
	if got != want {
		t.Fatalf("agenda\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAgendaTextOtherLines(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 11, 5, 7, 0, 0, 0, loc)
	mid := time.Date(2026, 11, 5, 0, 0, 0, 0, loc).UnixMilli()
	entries := []Entry{
		{Source: SourceItem, Kind: KindEvent, Title: "Holiday", AllDay: true, Start: mid, Date: "2026-11-05", EndDate: "2026-11-06"},
		{Source: SourceWorkflowSlot, Kind: KindWorkflow, Title: "Ping", AllDay: true, Start: mid, Date: "2026-11-05",
			Workflow: &EntryWorkflow{EveryMinutes: 5}},
		{Source: SourceItem, Kind: KindTask, Title: "Ship", Start: mid + 3600000*16, Date: "2026-11-05", Status: StateDone},
		{Source: SourceItem, Kind: KindNote, Title: "Idea", Start: mid, Date: "2026-11-05"},
		{Source: SourceItem, Kind: KindReminder, Title: "Pills", Start: mid + 3600000*9, Moved: true,
			Firing: &Firing{Status: FiringFailed}},
		{Source: SourceWorkflowRun, Kind: KindWorkflow, Title: "Sync", Start: mid + 3600000*2, Status: "succeeded",
			Workflow: &EntryWorkflow{RunNumber: 7}},
	}
	problems := []Entry{
		{Title: "A", Firing: &Firing{Status: FiringFailed}, Assignee: "Ira"},
		{Title: "B", Status: FiringMissed},
		{Title: "C", Status: FiringMissed}, {Title: "D", Status: FiringMissed},
		{Title: "E", Status: FiringMissed}, {Title: "F", Status: FiringMissed},
	}
	got := AgendaText(day, entries, problems, loc)
	want := strings.Join([]string{
		"📅 Thu 5 Nov",
		"• All day: Holiday",
		"• ⚙ Ping (workflow every 5 min)",
		"• 02:00 ⚙ Sync (run #7, succeeded)",
		"• 09:00 ⏰ Pills (moved) ⚠ failed",
		"☑ Task: Ship · 16:00",
		"📝 Note: Idea",
		"⚠ Yesterday: 5 missed, 1 failed — A (Ira), B, C, D, E, and 1 more",
	}, "\n")
	if got != want {
		t.Fatalf("agenda\n got:\n%s\nwant:\n%s", got, want)
	}
	if got := AgendaText(day, nil, nil, loc); got != "📅 Thu 5 Nov\nNothing planned today." {
		t.Fatalf("empty agenda %q", got)
	}
}

func TestListText(t *testing.T) {
	w := newWeek(t)
	in := w.input()
	in.From, in.To = w.at(5, 0, 0), w.at(7, 0, 0)
	in.Items = []Item{
		{ID: "ci_p", Kind: KindTask, Title: "Pay", Date: "2026-11-05", CreatedBy: "agent:Dana", Assignee: "Dana"},
		{ID: "ci_d", Kind: KindEvent, Title: "Dentist", Start: w.at(5, 14, 0).UnixMilli(), End: w.at(5, 15, 0).UnixMilli()},
		{ID: "ci_t", Kind: KindEvent, Title: "Trip", AllDay: true, Date: "2026-11-06", EndDate: "2026-11-08"},
	}
	got := ListText(Build(in), w.loc)
	want := strings.Join([]string{
		"2026-11-05 all-day  task  Pay  [" + itemKey("ci_p", w.at(5, 0, 0)) + "]  status=open  assignee Dana  added by Dana",
		"2026-11-05 14:00  event  Dentist  [" + itemKey("ci_d", w.at(5, 14, 0)) + "]",
		"2026-11-06..2026-11-08 all-day  event  Trip  [" + itemKey("ci_t", w.at(6, 0, 0)) + "]",
	}, "\n")
	if got != want {
		t.Fatalf("list\n got:\n%s\nwant:\n%s", got, want)
	}
	if ListText(nil, w.loc) != "No entries in this range." {
		t.Fatal("empty list text")
	}
}
