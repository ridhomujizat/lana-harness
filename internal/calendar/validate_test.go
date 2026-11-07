package calendar

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

func validItems() map[Kind]Item {
	start := time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC).UnixMilli()
	return map[Kind]Item{
		KindEvent:    {Kind: KindEvent, Title: "Dentist", Start: start, End: start + 3600000, Color: "rose"},
		KindTask:     {Kind: KindTask, Title: "Pay electricity", Date: "2026-11-05", Time: "10:30", State: StateOpen},
		KindReminder: {Kind: KindReminder, Title: "Laundry", Start: start, Action: &Action{Type: ActionSendMessage, Target: "telegram", Text: "Take the laundry out"}},
		KindNote:     {Kind: KindNote, Title: "Idea", Date: "2026-11-05"},
		KindHabit:    {Kind: KindHabit, Title: "Walk", Date: "2026-11-01", Repeat: Repeat{Freq: FreqWeekly, Days: []string{"mon", "thu"}}},
	}
}

// AC-CAL-001: a valid item of each kind passes validation.
func TestValidateValidKindsACCAL001(t *testing.T) {
	agents := func(name string) bool { return name == "Dana" }
	workflows := func(id string) bool { return id == "wf_x" }
	for kind, it := range validItems() {
		if errs := Validate(it, agents, workflows); errs != nil {
			t.Errorf("%s: unexpected errors %v", kind, errs)
		}
	}
	allDay := Item{Kind: KindEvent, Title: "Holiday", AllDay: true, Date: "2026-11-08", EndDate: "2026-11-10"}
	if errs := Validate(allDay, nil, nil); errs != nil {
		t.Errorf("all-day event: %v", errs)
	}
	withAction := Item{Kind: KindHabit, Title: "Recap", Date: "2026-11-01", Repeat: Repeat{Freq: FreqDaily},
		Action: &Action{Type: ActionAgentPrompt, Agent: "Dana", Prompt: "Recap"}}
	if errs := Validate(withAction, agents, workflows); errs != nil {
		t.Errorf("habit with action: %v", errs)
	}
	runWF := Item{Kind: KindTask, Title: "Research", Date: "2026-11-05",
		Action: &Action{Type: ActionRunWorkflow, WorkflowID: "wf_x", Input: json.RawMessage(`{"topic":"AI"}`)}}
	if errs := Validate(runWF, agents, workflows); errs != nil {
		t.Errorf("run_workflow: %v", errs)
	}
}

// AC-CAL-002: an event ending before its start and a habit without a repeat fail.
func TestValidateACCAL002(t *testing.T) {
	ev := validItems()[KindEvent]
	ev.End = ev.Start - 1
	if !hasField(Validate(ev, nil, nil), "end") {
		t.Errorf("event end before start: want end error, got %v", Validate(ev, nil, nil))
	}
	habit := Item{Kind: KindHabit, Title: "Read", Date: "2026-11-01"}
	if !hasField(Validate(habit, nil, nil), "repeat.freq") {
		t.Errorf("habit without repeat: want repeat.freq error, got %v", Validate(habit, nil, nil))
	}
	err := &InvalidItemError{Fields: Validate(habit, nil, nil)}
	if ErrorCode(err) != "invalid_item" || !strings.Contains(err.Error(), "repeat.freq") {
		t.Errorf("error %q code %q", err.Error(), ErrorCode(err))
	}
}

func TestValidateFieldRules(t *testing.T) {
	agents := func(name string) bool { return name == "Dana" }
	workflows := func(id string) bool { return id == "wf_x" }
	base := validItems()
	mod := func(k Kind, f func(*Item)) Item {
		it := base[k]
		f(&it)
		return it
	}
	cases := []struct {
		name  string
		it    Item
		field string
	}{
		{"unknown kind", Item{Kind: "meeting", Title: "x"}, "kind"},
		{"blank title", mod(KindNote, func(it *Item) { it.Title = "   " }), "title"},
		{"long title", mod(KindNote, func(it *Item) { it.Title = strings.Repeat("a", 201) }), "title"},
		{"long notes", mod(KindNote, func(it *Item) { it.Notes = strings.Repeat("a", 10001) }), "notes"},
		{"event no start", mod(KindEvent, func(it *Item) { it.Start = 0 }), "start"},
		{"all-day no date", Item{Kind: KindEvent, Title: "x", AllDay: true}, "date"},
		{"all-day end before", Item{Kind: KindEvent, Title: "x", AllDay: true, Date: "2026-11-05", EndDate: "2026-11-04"}, "end_date"},
		{"bad date", mod(KindTask, func(it *Item) { it.Date = "2026-2-30" }), "date"},
		{"feb 30", mod(KindTask, func(it *Item) { it.Date = "2026-02-30" }), "date"},
		{"bad time", mod(KindTask, func(it *Item) { it.Time = "25:00" }), "time"},
		{"short time", mod(KindTask, func(it *Item) { it.Time = "9:00" }), "time"},
		{"bad state", mod(KindTask, func(it *Item) { it.State = "doing" }), "state"},
		{"reminder no start", Item{Kind: KindReminder, Title: "x"}, "start"},
		{"note repeat", mod(KindNote, func(it *Item) { it.Repeat = Repeat{Freq: FreqDaily} }), "repeat.freq"},
		{"note action", mod(KindNote, func(it *Item) { it.Action = &Action{Type: ActionSendMessage, Target: "web", Text: "x"} }), "action"},
		{"habit monthly", mod(KindHabit, func(it *Item) { it.Repeat = Repeat{Freq: FreqMonthly} }), "repeat.freq"},
		{"bad freq", mod(KindTask, func(it *Item) { it.Repeat = Repeat{Freq: "hourly"} }), "repeat.freq"},
		{"weekly no days", mod(KindHabit, func(it *Item) { it.Repeat.Days = nil }), "repeat.days"},
		{"weekly bad day", mod(KindHabit, func(it *Item) { it.Repeat.Days = []string{"mon", "funday"} }), "repeat.days"},
		{"weekly dup day", mod(KindHabit, func(it *Item) { it.Repeat.Days = []string{"mon", "mon"} }), "repeat.days"},
		{"bad until", mod(KindHabit, func(it *Item) { it.Repeat.Until = "soon" }), "repeat.until"},
		{"until before date", mod(KindHabit, func(it *Item) { it.Repeat.Until = "2026-10-01" }), "repeat.until"},
		{"unknown assignee", mod(KindTask, func(it *Item) { it.Assignee = "Zed" }), "assignee"},
		{"bad color", mod(KindTask, func(it *Item) { it.Color = "red" }), "color"},
		{"bad action type", mod(KindTask, func(it *Item) { it.Action = &Action{Type: "email"} }), "action.type"},
		{"prompt agent missing", mod(KindTask, func(it *Item) { it.Action = &Action{Type: ActionAgentPrompt, Prompt: "x"} }), "action.agent"},
		{"prompt agent unknown", mod(KindTask, func(it *Item) { it.Action = &Action{Type: ActionAgentPrompt, Agent: "Zed", Prompt: "x"} }), "action.agent"},
		{"prompt empty", mod(KindTask, func(it *Item) { it.Action = &Action{Type: ActionAgentPrompt, Agent: "Dana"} }), "action.prompt"},
		{"prompt long", mod(KindTask, func(it *Item) {
			it.Action = &Action{Type: ActionAgentPrompt, Agent: "Dana", Prompt: strings.Repeat("p", 4001)}
		}), "action.prompt"},
		{"workflow unknown", mod(KindTask, func(it *Item) { it.Action = &Action{Type: ActionRunWorkflow, WorkflowID: "wf_y"} }), "action.workflow_id"},
		{"workflow bad input", mod(KindTask, func(it *Item) {
			it.Action = &Action{Type: ActionRunWorkflow, WorkflowID: "wf_x", Input: json.RawMessage(`{bad`)}
		}), "action.input"},
		{"message target", mod(KindTask, func(it *Item) { it.Action = &Action{Type: ActionSendMessage, Target: "email", Text: "x"} }), "action.target"},
		{"message long", mod(KindTask, func(it *Item) {
			it.Action = &Action{Type: ActionSendMessage, Target: "web", Text: strings.Repeat("t", 2001)}
		}), "action.text"},
	}
	for _, tc := range cases {
		errs := Validate(tc.it, agents, workflows)
		if !hasField(errs, tc.field) {
			t.Errorf("%s: want error on %s, got %v", tc.name, tc.field, errs)
		}
	}
}

func TestValidateNilFuncsAcceptNames(t *testing.T) {
	it := Item{Kind: KindTask, Title: "x", Date: "2026-11-05", Assignee: "Anyone",
		Action: &Action{Type: ActionAgentPrompt, Agent: "Anyone", Prompt: "hi"}}
	if errs := Validate(it, nil, nil); errs != nil {
		t.Fatalf("nil funcs: %v", errs)
	}
}

func TestNormalize(t *testing.T) {
	it := Normalize(Item{Kind: KindNote, Title: "  Idea  ", Date: "2026-11-05", Start: 5, Time: "10:00",
		Repeat: Repeat{Freq: FreqDaily, Days: []string{"mon"}}})
	if it.Title != "Idea" || it.Start != 0 || it.Time != "" || it.Repeat.Freq != FreqNone || len(it.Repeat.Days) != 0 || it.Assignee != Owner {
		t.Fatalf("note: %+v", it)
	}
	task := Normalize(Item{Kind: KindTask, Title: "t", Date: "2026-11-05",
		Action: &Action{Type: ActionSendMessage, Target: "web", Text: "x", Agent: "Dana"}})
	if task.State != StateOpen || task.Action.Agent != "" {
		t.Fatalf("task: %+v %+v", task, task.Action)
	}
	allDay := Normalize(Item{Kind: KindEvent, Title: "e", AllDay: true, Date: "2026-11-05", Start: 9, End: 10})
	if allDay.EndDate != "2026-11-05" || allDay.Start != 0 || allDay.End != 0 {
		t.Fatalf("all-day: %+v", allDay)
	}
	ev := Normalize(Item{Kind: KindEvent, Title: "e", Start: 9, End: 10, Date: "2026-11-05", State: "done"})
	if ev.Date != "" || ev.State != "" {
		t.Fatalf("event: %+v", ev)
	}
}

func TestItemJSONShape(t *testing.T) {
	it := Item{ID: "ci_1", Kind: KindEvent, Title: "Dentist", Assignee: Owner}
	raw, err := json.Marshal(it)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{`"repeat":{"freq":"none","days":[],"until":""}`, `"action":null`, `"google":null`,
		`"all_day":false`, `"end_date":""`, `"created_by":""`, `"updated_at":0`} {
		if !strings.Contains(s, want) {
			t.Errorf("item JSON %s lacks %s", s, want)
		}
	}
	if strings.Contains(s, "deleted") {
		t.Errorf("item JSON leaks deleted_at: %s", s)
	}
	it.Google = &GoogleRef{CalendarID: "primary", EventID: "abc", ETag: "secret", Sync: SyncSynced}
	it.Action = &Action{Type: ActionRunWorkflow, WorkflowID: "wf_x", Input: json.RawMessage(`{"topic":"AI"}`)}
	raw, _ = json.Marshal(it)
	s = string(raw)
	if !strings.Contains(s, `"google":{"calendar_id":"primary","event_id":"abc","sync":"synced"}`) || strings.Contains(s, "secret") {
		t.Errorf("google ref JSON: %s", s)
	}
	if !strings.Contains(s, `"action":{"type":"run_workflow","workflow_id":"wf_x","input":{"topic":"AI"}}`) {
		t.Errorf("action JSON: %s", s)
	}
	var back Item
	if err := json.Unmarshal(raw, &back); err != nil || back.Action.WorkflowID != "wf_x" || back.Google.EventID != "abc" {
		t.Errorf("round trip: %v %+v", err, back)
	}
}

func TestFiringAndOverrideJSON(t *testing.T) {
	f := Firing{ItemID: "ci_1", Occurrence: 100, Status: FiringDone, StartedAt: 101, EndedAt: 102, SessionID: "s_1",
		RunWorkflow: "wf_x", RunNumber: 12}
	raw, _ := json.Marshal(f)
	want := `{"status":"done","at":100,"started_at":101,"ended_at":102,"session_id":"s_1","run":{"workflow_id":"wf_x","number":12},"error":""}`
	if string(raw) != want {
		t.Errorf("firing JSON\n got %s\nwant %s", raw, want)
	}
	var back Firing
	if err := json.Unmarshal(raw, &back); err != nil || back.RunNumber != 12 || back.Occurrence != 100 {
		t.Errorf("firing round trip: %v %+v", err, back)
	}
	raw, _ = json.Marshal(Firing{Status: FiringMissed, Occurrence: 5})
	if !strings.Contains(string(raw), `"run":null`) {
		t.Errorf("firing without run: %s", raw)
	}
	raw, _ = json.Marshal(Override{Source: OverrideWorkflow, Ref: "wf/n", Occurrence: 1, Op: OpMove, To: 2, By: Owner, At: 3, FiredAt: 4})
	want = `{"source":"workflow","ref":"wf/n","occurrence":1,"op":"move","to":2,"by":"owner","at":3}`
	if string(raw) != want {
		t.Errorf("override JSON\n got %s\nwant %s", raw, want)
	}
	raw, _ = json.Marshal(HistoryLine{ItemID: "ci_1", At: 1, By: Owner, Summary: "created", Snapshot: "{}"})
	if string(raw) != `{"at":1,"by":"owner","summary":"created"}` {
		t.Errorf("history JSON: %s", raw)
	}
}

func TestErrorCodes(t *testing.T) {
	cases := map[error]string{
		ErrNotFound: "not_found", ErrRangeTooLarge: "range_too_large", ErrInvalidRange: "invalid_range",
		ErrNotRepeating: "not_repeating", ErrNoSuchOccurrence: "no_such_occurrence", ErrNotCompletable: "not_completable",
		ErrAlreadyRunning: "already_running", ErrAlreadyDone: "already_done",
	}
	for err, code := range cases {
		wrapped := errors.Join(errors.New("ctx"), err)
		if ErrorCode(wrapped) != code || !errors.Is(wrapped, err) {
			t.Errorf("%v: code %q, want %q", err, ErrorCode(wrapped), code)
		}
	}
	if ErrorCode(errors.New("plain")) != "" {
		t.Error("plain error has a code")
	}
}

func TestNewItemID(t *testing.T) {
	re := regexp.MustCompile(`^ci_[0-9a-z]{10}$`)
	seen := map[string]bool{}
	for range 200 {
		id := NewItemID()
		if !re.MatchString(id) || seen[id] {
			t.Fatalf("bad or duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestDefaultSettings(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	s := DefaultSettings(loc)
	if s.Timezone != "Asia/Jakarta" || s.WeekStart != 1 || s.Agenda.Enabled || s.Agenda.Time != "07:00" {
		t.Fatalf("defaults: %+v", s)
	}
	if s.Location().String() != "Asia/Jakarta" {
		t.Fatalf("location: %v", s.Location())
	}
	raw, _ := json.Marshal(s)
	if string(raw) != `{"timezone":"Asia/Jakarta","week_start":1,"agenda":{"enabled":false,"time":"07:00"}}` {
		t.Fatalf("settings JSON: %s", raw)
	}
}

func hasField(errs []FieldError, field string) bool {
	for _, e := range errs {
		if e.Field == field {
			return true
		}
	}
	return false
}
