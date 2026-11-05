package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/calendar"
)

// newCalendarTool builds the calendar tool over a MemStore service in
// Asia/Jakarta with the clock at Thu 5 Nov 2026 08:00.
func newCalendarTool(t *testing.T) (core.Tool, *calendar.Service, *time.Location) {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	now := time.Date(2026, 11, 5, 8, 0, 0, 0, loc)
	svc := calendar.NewService(calendar.NewMemStore(),
		calendar.WithLocation(loc),
		calendar.WithClock(func() time.Time { return now }),
		calendar.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(svc.Close)
	return NewCalendar(svc), svc, loc
}

// callCalendar runs the tool as agent Dana.
func callCalendar(t *testing.T, tool core.Tool, input string) core.ToolOutput {
	t.Helper()
	out, err := tool.Call(context.Background(), core.ToolCtx{AgentName: "Dana"}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("Call(%s): %v", input, err)
	}
	return out
}

// createdID returns the item id of a create or update output.
func createdID(t *testing.T, out core.ToolOutput) string {
	t.Helper()
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("output=%+v want success", out)
	}
	_, body, ok := strings.Cut(out.Output, "\n")
	if !ok {
		t.Fatalf("output has no JSON: %q", out.Output)
	}
	var it struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &it); err != nil || it.ID == "" {
		t.Fatalf("output JSON %q: %v", body, err)
	}
	return it.ID
}

// AC-CAL-016: a task Dana creates through the tool is created by
// "agent:Dana" and the list shows "added by Dana".
func TestCalendarToolCreateByAgent(t *testing.T) {
	tool, svc, _ := newCalendarTool(t)
	out := callCalendar(t, tool, `{"op":"create","item":{"kind":"task","title":"Pay electricity","date":"2026-11-05"}}`)
	id := createdID(t, out)
	detail, err := svc.GetItem(context.Background(), id)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if detail.Item.CreatedBy != "agent:Dana" || detail.Item.UpdatedBy != "agent:Dana" {
		t.Fatalf("item=%+v want created_by agent:Dana", detail.Item)
	}

	list := callCalendar(t, tool, `{"op":"list","from":"2026-11-05","to":"2026-11-05"}`)
	if list.Status != core.ToolStatusSuccess || !strings.Contains(list.Output, "Pay electricity") || !strings.Contains(list.Output, "added by Dana") {
		t.Fatalf("list=%q", list.Output)
	}
	agenda, err := svc.Agenda(context.Background(), time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Agenda: %v", err)
	}
	if !strings.Contains(agenda, "Pay electricity (added by Dana)") {
		t.Fatalf("agenda=%q", agenda)
	}
}

// Local times in the tool are read in the calendar time zone, and the list
// prints them back as local times (API-CAL-016).
func TestCalendarToolLocalTimes(t *testing.T) {
	tool, svc, loc := newCalendarTool(t)
	out := callCalendar(t, tool, `{"op":"create","item":{"kind":"event","title":"Dentist","start":"2026-11-06T14:00","end":"2026-11-06T15:00"}}`)
	id := createdID(t, out)
	if !strings.Contains(out.Output, `"start_local":"2026-11-06T14:00"`) {
		t.Fatalf("create output has no local start: %q", out.Output)
	}
	detail, err := svc.GetItem(context.Background(), id)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if want := time.Date(2026, 11, 6, 14, 0, 0, 0, loc).UnixMilli(); detail.Item.Start != want {
		t.Fatalf("start=%d want %d", detail.Item.Start, want)
	}

	list := callCalendar(t, tool, `{"op":"list","from":"2026-11-06","to":"2026-11-07"}`)
	if !strings.Contains(list.Output, "2026-11-06 14:00  event  Dentist  [item:"+id+":") {
		t.Fatalf("list=%q", list.Output)
	}
	empty := callCalendar(t, tool, `{"op":"list","from":"2026-11-07","to":"2026-11-08"}`)
	if !strings.Contains(empty.Output, "No entries in this range.") {
		t.Fatalf("empty list=%q", empty.Output)
	}

	// update moves the event with a local time.
	upd := callCalendar(t, tool, `{"op":"update","id":"`+id+`","item":{"start":"2026-11-06T16:00","end":"2026-11-06T17:00"}}`)
	createdID(t, upd)
	detail, _ = svc.GetItem(context.Background(), id)
	if want := time.Date(2026, 11, 6, 16, 0, 0, 0, loc).UnixMilli(); detail.Item.Start != want || detail.Item.UpdatedBy != "agent:Dana" {
		t.Fatalf("updated item=%+v", detail.Item)
	}
}

// move, skip, complete and delete work on local occurrences.
func TestCalendarToolOccurrences(t *testing.T) {
	tool, svc, loc := newCalendarTool(t)
	ctx := context.Background()
	rem := createdID(t, callCalendar(t, tool, `{"op":"create","item":{"kind":"reminder","title":"Stretch","start":"2026-11-06T07:00","repeat":{"freq":"daily"}}}`))

	moved := callCalendar(t, tool, `{"op":"move","id":"`+rem+`","occurrence":"2026-11-07T07:00","to":"2026-11-07T09:30"}`)
	if moved.Status != core.ToolStatusSuccess || !strings.Contains(moved.Output, "to 2026-11-07T09:30") {
		t.Fatalf("move=%+v", moved)
	}
	// A date finds the one occurrence of that day.
	skipped := callCalendar(t, tool, `{"op":"skip","source":"item","ref":"`+rem+`","occurrence":"2026-11-08"}`)
	if skipped.Status != core.ToolStatusSuccess {
		t.Fatalf("skip=%+v", skipped)
	}
	res, err := svc.Entries(ctx, time.Date(2026, 11, 7, 0, 0, 0, 0, loc), time.Date(2026, 11, 9, 0, 0, 0, 0, loc), nil)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	var sawMove, sawSkip bool
	for _, e := range res.Entries {
		if e.Moved && e.Start == time.Date(2026, 11, 7, 9, 30, 0, 0, loc).UnixMilli() {
			sawMove = true
		}
		if e.Status == calendar.StatusSkipped {
			sawSkip = true
		}
	}
	if !sawMove || !sawSkip {
		t.Fatalf("entries=%+v want a moved and a skipped occurrence", res.Entries)
	}

	habit := createdID(t, callCalendar(t, tool, `{"op":"create","item":{"kind":"habit","title":"Walk","date":"2026-11-01","repeat":{"freq":"daily"}}}`))
	done := callCalendar(t, tool, `{"op":"complete","id":"`+habit+`","occurrence":"2026-11-05"}`)
	if done.Status != core.ToolStatusSuccess || !strings.Contains(done.Output, "Streak 1") {
		t.Fatalf("complete=%+v", done)
	}

	del := callCalendar(t, tool, `{"op":"delete","id":"`+habit+`"}`)
	if del.Status != core.ToolStatusSuccess {
		t.Fatalf("delete=%+v", del)
	}
	if _, err := svc.GetItem(ctx, habit); calendar.ErrorCode(err) != "not_found" {
		t.Fatalf("GetItem after delete err=%v", err)
	}
}

// Errors come back as tool error text with the contract code.
func TestCalendarToolErrors(t *testing.T) {
	tool, _, _ := newCalendarTool(t)
	cases := []struct {
		input string
		want  string
	}{
		{`{"op":"create","item":{"kind":"task","title":""}}`, "calendar error invalid_item:"},
		{`{"op":"get","id":"ci_missing"}`, "calendar error not_found:"},
		{`{"op":"list","from":"2026-11-01","to":"2027-03-01"}`, "calendar error range_too_large:"},
		{`{"op":"list","from":"tomorrow"}`, "invalid input: from:"},
		{`{"op":"fly"}`, "invalid input: unknown op"},
		{`{"op":"skip","source":"workflow","ref":"wf/x","occurrence":"2026-11-06"}`, "invalid input: occurrence needs a time"},
		{`{"op":"list","bogus":1}`, "invalid input:"},
	}
	for _, tc := range cases {
		out := callCalendar(t, tool, tc.input)
		if out.Status != core.ToolStatusError || !strings.Contains(out.Output, tc.want) {
			t.Errorf("%s: output=%+v want error containing %q", tc.input, out, tc.want)
		}
	}
}

// Reads are risk read; every change is low risk (memory), so it needs no
// approval (REQ-CAL-009).
func TestCalendarToolRisk(t *testing.T) {
	tool, _, _ := newCalendarTool(t)
	for input, want := range map[string]core.Risk{
		`{"op":"list"}`:                core.RiskRead,
		`{"op":"get","id":"x"}`:        core.RiskRead,
		`{"op":"create","item":{}}`:    core.RiskMemory,
		`{"op":"delete","id":"x"}`:     core.RiskMemory,
		`{"op":"run_missed","id":"x"}`: core.RiskMemory,
		`not json`:                     core.RiskMemory,
	} {
		if got := tool.Risk(json.RawMessage(input)); got != want {
			t.Errorf("Risk(%s)=%s want %s", input, got, want)
		}
	}
	if tool.Spec().Name != "calendar" || !json.Valid(tool.Spec().InputSchema) {
		t.Fatalf("spec=%+v", tool.Spec())
	}
}
