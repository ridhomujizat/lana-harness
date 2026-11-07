package calendar

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// maxYesterdayNames caps the titles listed on the "Yesterday" agenda line.
const maxYesterdayNames = 5

// AgendaText renders the daily agenda message of API-CAL-017 for day:
//
//	📅 Thu 5 Nov
//	• 07:00 ⚙ Daily research (workflow)
//	• 10:00 ⏰ Take the laundry out
//	• 14:00–15:00 Dentist
//	☐ Task: Pay electricity (added by Dana)
//	✓ Habits: Read 20 min (🔥 4) · ☐ Walk
//	⚠ Yesterday: 1 missed — Weekly recap (Dana)
//
// Dated lines come first, then timed lines by start, tasks, notes and one
// habits line (a habit not done today is marked "☐"; a streak shows as
// "(🔥 N)"). yesterdayProblems are entries whose firing was missed or failed.
// A day with no entries says "Nothing planned today."
func AgendaText(day time.Time, today []Entry, yesterdayProblems []Entry, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	sorted := slices.Clone(today)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].Title < sorted[j].Title
	})
	var dated, timed, tasks, notes, habits []string
	for _, e := range sorted {
		switch {
		case e.Kind == KindHabit:
			h := e.Title
			if e.Habit != nil && e.Habit.Streak > 0 {
				h += fmt.Sprintf(" (🔥 %d)", e.Habit.Streak)
			}
			if e.Habit == nil || !e.Habit.Done {
				h = "☐ " + h
			}
			habits = append(habits, h)
		case e.Kind == KindTask:
			box := "☐"
			if e.Status == StateDone {
				box = "☑"
			}
			line := box + " Task: " + e.Title
			if entryTimed(e, loc) {
				line += " · " + clock(e.Start, loc)
			}
			tasks = append(tasks, line+suffixes(e))
		case e.Kind == KindNote:
			notes = append(notes, "📝 Note: "+e.Title+suffixes(e))
		case e.Source == SourceWorkflowSlot && e.AllDay:
			every := ""
			if e.Workflow != nil && e.Workflow.EveryMinutes > 0 {
				every = fmt.Sprintf(" every %d min", e.Workflow.EveryMinutes)
			}
			dated = append(dated, "• ⚙ "+e.Title+" (workflow"+every+")"+suffixes(e))
		case !entryTimed(e, loc):
			dated = append(dated, "• All day: "+e.Title+suffixes(e))
		default:
			when := clock(e.Start, loc)
			if e.End > e.Start {
				when += "–" + clock(e.End, loc)
			}
			timed = append(timed, "• "+when+" "+entryLabel(e)+suffixes(e))
		}
	}
	lines := []string{"📅 " + day.In(loc).Format("Mon 2 Jan")}
	lines = append(lines, dated...)
	lines = append(lines, timed...)
	lines = append(lines, tasks...)
	lines = append(lines, notes...)
	if len(habits) > 0 {
		lines = append(lines, "✓ Habits: "+strings.Join(habits, " · "))
	}
	if len(lines) == 1 {
		lines = append(lines, "Nothing planned today.")
	}
	if y := yesterdayLine(yesterdayProblems); y != "" {
		lines = append(lines, y)
	}
	return strings.Join(lines, "\n")
}

// entryLabel is the icon and title of a timed agenda line.
func entryLabel(e Entry) string {
	switch {
	case e.Source == SourceWorkflowSlot:
		return "⚙ " + e.Title + " (workflow)"
	case e.Source == SourceWorkflowRun:
		n := 0
		if e.Workflow != nil {
			n = e.Workflow.RunNumber
		}
		return fmt.Sprintf("⚙ %s (run #%d, %s)", e.Title, n, e.Status)
	case e.Kind == KindReminder:
		return "⏰ " + e.Title
	}
	return e.Title
}

// suffixes are the moved/skipped/firing/author marks of an item line.
func suffixes(e Entry) string {
	var b strings.Builder
	if e.Moved {
		b.WriteString(" (moved)")
	}
	if e.Status == StatusSkipped {
		b.WriteString(" (skipped)")
	}
	if e.Firing != nil && (e.Firing.Status == FiringMissed || e.Firing.Status == FiringFailed) {
		b.WriteString(" ⚠ " + e.Firing.Status)
	}
	if name := agentName(e.CreatedBy); name != "" {
		b.WriteString(" (added by " + name + ")")
	}
	return b.String()
}

// yesterdayLine summarizes missed and failed firings, or returns "".
func yesterdayLine(problems []Entry) string {
	missed, failed := 0, 0
	var names []string
	for _, e := range problems {
		status := e.Status
		if e.Firing != nil {
			status = e.Firing.Status
		}
		switch status {
		case FiringMissed:
			missed++
		case FiringFailed:
			failed++
		default:
			continue
		}
		name := e.Title
		if who := actor(e); who != "" {
			name += " (" + who + ")"
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	var counts []string
	if missed > 0 {
		counts = append(counts, fmt.Sprintf("%d missed", missed))
	}
	if failed > 0 {
		counts = append(counts, fmt.Sprintf("%d failed", failed))
	}
	list := names
	more := ""
	if len(list) > maxYesterdayNames {
		more = fmt.Sprintf(", and %d more", len(list)-maxYesterdayNames)
		list = list[:maxYesterdayNames]
	}
	return "⚠ Yesterday: " + strings.Join(counts, ", ") + " — " + strings.Join(list, ", ") + more
}

// actor is the agent behind an entry: the agent_prompt target, else an
// agent assignee.
func actor(e Entry) string {
	if e.Action != nil && e.Action.Type == ActionAgentPrompt && e.Action.Agent != "" {
		return e.Action.Agent
	}
	if e.Assignee != "" && e.Assignee != Owner {
		return e.Assignee
	}
	return ""
}

// agentName returns <name> of "agent:<name>", else "".
func agentName(by string) string {
	if name, ok := strings.CutPrefix(by, "agent:"); ok {
		return name
	}
	return ""
}

func clock(ms int64, loc *time.Location) string {
	return time.UnixMilli(ms).In(loc).Format("15:04")
}

// ListText renders entries for the agent tool's list operation (API-CAL-016),
// one line each:
//
//	2026-11-05 14:00  event  Dentist  [item:ci_x:1793084400000]  status=done  added by Dana
//
// Dated entries show "all-day" instead of a time.
func ListText(entries []Entry, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	if len(entries) == 0 {
		return "No entries in this range."
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		date := e.Date
		if date == "" {
			date = dayOf(time.UnixMilli(e.Start), loc).String()
		}
		when := "all-day"
		if entryTimed(e, loc) {
			date = dayOf(time.UnixMilli(e.Start), loc).String()
			when = clock(e.Start, loc)
		}
		kind := string(e.Kind)
		switch e.Source {
		case SourceWorkflowRun:
			kind = "workflow_run"
		case SourceGoogle:
			kind = "google"
		}
		parts := []string{date + " " + when, kind, e.Title, "[" + e.Key + "]"}
		if e.EndDate != "" && e.EndDate != e.Date && !entryTimed(e, loc) {
			parts[0] = date + ".." + e.EndDate + " " + when
		}
		if e.Status != "" {
			parts = append(parts, "status="+e.Status)
		}
		if e.Firing != nil && e.Firing.Status != e.Status {
			parts = append(parts, "firing="+e.Firing.Status)
		}
		if e.Moved {
			parts = append(parts, "moved")
		}
		if e.Habit != nil {
			parts = append(parts, fmt.Sprintf("done=%t streak=%d", e.Habit.Done, e.Habit.Streak))
		}
		if e.Workflow != nil && e.Workflow.EveryMinutes > 0 {
			parts = append(parts, fmt.Sprintf("every %d min", e.Workflow.EveryMinutes))
		}
		if e.Assignee != "" && e.Assignee != Owner {
			parts = append(parts, "assignee "+e.Assignee)
		}
		if name := agentName(e.CreatedBy); name != "" {
			parts = append(parts, "added by "+name)
		}
		lines = append(lines, strings.Join(parts, "  "))
	}
	return strings.Join(lines, "\n")
}
