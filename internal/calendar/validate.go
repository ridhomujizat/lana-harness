package calendar

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Field limits of the contract's item and action objects.
const (
	maxTitle  = 200
	maxNotes  = 10000
	maxPrompt = 4000
	maxText   = 2000
)

var validColors = map[string]bool{
	"gray": true, "indigo": true, "blue": true, "teal": true,
	"green": true, "amber": true, "rose": true, "violet": true,
}

// Validate checks it against the contract's field table (REQ-CAL-001,
// REQ-CAL-003, REQ-CAL-006) and returns every problem, or nil. agentExists
// and workflowExists check assignees and action targets; a nil func accepts
// every name. Validate checks only the fields its kind uses; Normalize clears
// the others.
func Validate(it Item, agentExists func(string) bool, workflowExists func(string) bool) []FieldError {
	v := &validator{}
	if !it.Kind.Valid() {
		v.add("kind", "must be one of event, task, reminder, note, habit")
		return v.out
	}
	title := strings.TrimSpace(it.Title)
	switch n := utf8.RuneCountInString(title); {
	case n == 0:
		v.add("title", "is required")
	case n > maxTitle:
		v.add("title", "must be at most 200 characters")
	}
	if utf8.RuneCountInString(it.Notes) > maxNotes {
		v.add("notes", "must be at most 10000 characters")
	}

	switch it.Kind {
	case KindEvent:
		if it.AllDay {
			v.date("date", it.Date, true)
			if it.EndDate != "" && v.date("end_date", it.EndDate, false) {
				if start, ok := parseDay(it.Date); ok {
					if end, _ := parseDay(it.EndDate); end.before(start) {
						v.add("end_date", "must not be before date")
					}
				}
			}
		} else {
			if it.Start <= 0 {
				v.add("start", "is required")
			}
			if it.End <= it.Start {
				v.add("end", "must be after start")
			}
		}
	case KindTask:
		v.date("date", it.Date, true)
		v.clock("time", it.Time)
		if it.State != "" && it.State != StateOpen && it.State != StateDone {
			v.add("state", "must be open or done")
		}
	case KindReminder:
		if it.Start <= 0 {
			v.add("start", "is required")
		}
	case KindNote:
		v.date("date", it.Date, true)
		if it.Repeat.Repeats() {
			v.add("repeat.freq", "a note cannot repeat")
		}
	case KindHabit:
		v.date("date", it.Date, true)
		v.clock("time", it.Time)
		if it.Repeat.Freq != FreqDaily && it.Repeat.Freq != FreqWeekly {
			v.add("repeat.freq", "a habit repeats daily or weekly")
		}
	}
	v.repeat(it)

	switch {
	case it.Assignee == "" || it.Assignee == Owner:
	case agentExists != nil && !agentExists(it.Assignee):
		v.add("assignee", "unknown agent")
	}
	if it.Color != "" && !validColors[it.Color] {
		v.add("color", "must be one of gray, indigo, blue, teal, green, amber, rose, violet")
	}
	if it.Action != nil {
		if it.Kind == KindNote {
			v.add("action", "a note cannot have an action")
		} else {
			v.action(*it.Action, agentExists, workflowExists)
		}
	}
	return v.out
}

type validator struct{ out []FieldError }

func (v *validator) add(field, msg string) {
	v.out = append(v.out, FieldError{Field: field, Message: msg})
}

// date checks a "YYYY-MM-DD" value and reports whether it is valid.
func (v *validator) date(field, s string, required bool) bool {
	if s == "" {
		if required {
			v.add(field, "is required")
		}
		return false
	}
	if _, ok := parseDay(s); !ok {
		v.add(field, "must be a date YYYY-MM-DD")
		return false
	}
	return true
}

func (v *validator) clock(field, s string) {
	if s == "" {
		return
	}
	if _, _, ok := parseClock(s); !ok {
		v.add(field, "must be a time HH:MM")
	}
}

func (v *validator) repeat(it Item) {
	r := it.Repeat
	switch r.Freq {
	case "", FreqNone, FreqDaily, FreqWeekdays, FreqMonthly, FreqYearly:
	case FreqWeekly:
		if len(r.Days) == 0 || len(r.Days) > 7 {
			v.add("repeat.days", "a weekly repeat needs 1 to 7 weekdays")
			break
		}
		seen := map[string]bool{}
		for _, d := range r.Days {
			if _, ok := weekdayNames[d]; !ok || seen[d] {
				v.add("repeat.days", "must be distinct values of mon tue wed thu fri sat sun")
				break
			}
			seen[d] = true
		}
	default:
		v.add("repeat.freq", "must be one of none, daily, weekdays, weekly, monthly, yearly")
	}
	if r.Until != "" && v.date("repeat.until", r.Until, false) {
		until, _ := parseDay(r.Until)
		if base, ok := baseDay(it); ok && until.before(base) {
			v.add("repeat.until", "must not be before the first date")
		}
	}
}

// baseDay is the first day of a dated item. Timed kinds report false: their
// local day needs the calendar zone, which Validate does not know.
func baseDay(it Item) (civilDay, bool) {
	if it.Kind == KindReminder || (it.Kind == KindEvent && !it.AllDay) {
		return civilDay{}, false
	}
	return parseDay(it.Date)
}

func (v *validator) action(a Action, agentExists, workflowExists func(string) bool) {
	switch a.Type {
	case ActionAgentPrompt:
		switch {
		case strings.TrimSpace(a.Agent) == "":
			v.add("action.agent", "is required")
		case agentExists != nil && !agentExists(a.Agent):
			v.add("action.agent", "unknown agent")
		}
		v.text("action.prompt", a.Prompt, maxPrompt)
	case ActionRunWorkflow:
		switch {
		case strings.TrimSpace(a.WorkflowID) == "":
			v.add("action.workflow_id", "is required")
		case workflowExists != nil && !workflowExists(a.WorkflowID):
			v.add("action.workflow_id", "unknown workflow")
		}
		if len(a.Input) > 0 && !json.Valid(a.Input) {
			v.add("action.input", "must be JSON")
		}
	case ActionSendMessage:
		if a.Target != "telegram" && a.Target != "web" {
			v.add("action.target", "must be telegram or web")
		}
		v.text("action.text", a.Text, maxText)
	default:
		v.add("action.type", "must be agent_prompt, run_workflow or send_message")
	}
}

func (v *validator) text(field, s string, limit int) {
	switch n := utf8.RuneCountInString(s); {
	case strings.TrimSpace(s) == "":
		v.add(field, "is required")
	case n > limit:
		v.add(field, "is too long")
	}
}

// Normalize trims the title, applies defaults (assignee owner, task state
// open, repeat none) and clears the time fields and action fields that the
// item's kind and action type do not use, so a stored item carries only the
// contract's fields for its kind.
func Normalize(it Item) Item {
	it.Title = strings.TrimSpace(it.Title)
	if it.Assignee == "" {
		it.Assignee = Owner
	}
	if it.Repeat.Freq == "" {
		it.Repeat.Freq = FreqNone
	}
	days := make([]string, 0, len(it.Repeat.Days))
	if it.Repeat.Freq == FreqWeekly {
		for _, d := range it.Repeat.Days {
			days = append(days, strings.ToLower(strings.TrimSpace(d)))
		}
	}
	it.Repeat.Days = days
	if !it.Repeat.Repeats() {
		it.Repeat.Until = ""
	}
	switch it.Kind {
	case KindEvent:
		it.Time = ""
		if it.AllDay {
			it.Start, it.End = 0, 0
			if it.EndDate == "" {
				it.EndDate = it.Date
			}
		} else {
			it.Date, it.EndDate = "", ""
		}
	case KindReminder:
		it.End, it.AllDay, it.Date, it.EndDate, it.Time = 0, false, "", "", ""
	case KindNote:
		it.Start, it.End, it.AllDay, it.EndDate, it.Time = 0, 0, false, "", ""
		it.Repeat = Repeat{Freq: FreqNone, Days: []string{}}
	case KindTask, KindHabit:
		it.Start, it.End, it.AllDay, it.EndDate = 0, 0, false, ""
	}
	if it.Kind == KindTask {
		if it.State == "" {
			it.State = StateOpen
		}
	} else {
		it.State = ""
	}
	if it.Action != nil {
		a := *it.Action
		switch a.Type {
		case ActionAgentPrompt:
			a = Action{Type: a.Type, Agent: a.Agent, Prompt: a.Prompt}
		case ActionRunWorkflow:
			a = Action{Type: a.Type, WorkflowID: a.WorkflowID, Input: a.Input}
		case ActionSendMessage:
			a = Action{Type: a.Type, Target: a.Target, Text: a.Text}
		}
		it.Action = &a
	}
	return it
}
