package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/calendar"
)

// The calendar agent tool (spec #35, API-CAL-016, REQ-CAL-009). Dates and
// times in its input are local to the calendar time zone, so the model never
// computes Unix time.

// CalendarService is the part of *calendar.Service the calendar tool calls.
type CalendarService interface {
	Location(ctx context.Context) *time.Location
	Entries(ctx context.Context, from, to time.Time, sources []string) (calendar.EntriesResult, error)
	GetItem(ctx context.Context, id string) (calendar.ItemDetail, error)
	CreateItem(ctx context.Context, it calendar.Item, by string) (calendar.Item, error)
	UpdateItem(ctx context.Context, id string, patch json.RawMessage, by string) (calendar.Item, error)
	DeleteItem(ctx context.Context, id, by string) error
	SaveOverride(ctx context.Context, o calendar.Override, by string) (calendar.Override, error)
	Complete(ctx context.Context, id string, occurrence int64, done bool, by string) (calendar.Item, *calendar.EntryHabit, error)
	RunFiring(ctx context.Context, itemID string, occurrence int64, by string) (calendar.Firing, error)
}

// calendarListDays is the default list range: today and the next 6 days.
const calendarListDays = 7

// Local date and time layouts of the tool input.
const (
	calendarDateLayout     = "2006-01-02"
	calendarDateTimeLayout = "2006-01-02T15:04"
)

const calendarDescription = "Read and change the owner's calendar: events, tasks, reminders, notes and habits, plus workflow slots. " +
	"Dates are local YYYY-MM-DD and times local YYYY-MM-DDTHH:MM in the calendar time zone. " +
	"list shows entries with their key (default today and the next 6 days). " +
	"create needs item.kind and item.title; event: start+end (or all_day+date), task: date, reminder: start, note: date, habit: date + repeat.freq daily|weekly. " +
	"update takes the changed item fields only. move/skip change one occurrence of a repeating item (source item) or one workflow slot (source workflow, ref <workflow id>/<node id>). " +
	"complete marks a task done or a habit day ticked; run_missed runs a missed or failed action again."

var calendarSpec = json.RawMessage(`{"type":"object","properties":{` +
	`"op":{"type":"string","enum":["list","get","create","update","delete","move","skip","complete","run_missed"],"description":"Operation."},` +
	`"from":{"type":"string","description":"list: first local date YYYY-MM-DD (or YYYY-MM-DDTHH:MM). Defaults to today."},` +
	`"to":{"type":"string","description":"list: end local date, exclusive (the same date as from means that one day). Defaults to from + 7 days. move: the new local time YYYY-MM-DDTHH:MM."},` +
	`"id":{"type":"string","description":"Item id (ci_...) for get, update, delete, complete, run_missed, and move/skip of an item."},` +
	`"item":{"type":"object","description":"create: the item; update: only the fields to change. Fields: kind (event|task|reminder|note|habit), title, notes, start and end (local YYYY-MM-DDTHH:MM), all_day, date and end_date (YYYY-MM-DD), time (HH:MM), repeat {freq: none|daily|weekdays|weekly|monthly|yearly, days: [mon..sun], until: YYYY-MM-DD}, assignee (owner or an agent name), color, state (open|done), action {type: agent_prompt|run_workflow|send_message, agent, prompt, workflow_id, input, target: telegram|web, text}."},` +
	`"source":{"type":"string","enum":["item","workflow"],"description":"move/skip: what to change. Defaults to item."},` +
	`"ref":{"type":"string","description":"move/skip: the item id, or <workflow id>/<node id> for a workflow slot. Defaults to id."},` +
	`"occurrence":{"type":"string","description":"The original local time of the occurrence, YYYY-MM-DDTHH:MM, or a date YYYY-MM-DD for an item that has one occurrence that day."},` +
	`"done":{"type":"boolean","description":"complete: true to mark done (default), false to undo."}` +
	`},"required":["op"],"additionalProperties":false}`)

type calendarTool struct{ svc CalendarService }

// NewCalendar creates the calendar tool over svc (API-CAL-016).
func NewCalendar(svc CalendarService) core.Tool { return &calendarTool{svc: svc} }

func (t *calendarTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "calendar", Description: calendarDescription, InputSchema: calendarSpec}
}

// Risk is read for list and get and memory (low, no approval) for every
// change (REQ-CAL-009).
func (t *calendarTool) Risk(raw json.RawMessage) core.Risk {
	var in struct {
		Op string `json:"op"`
	}
	if json.Unmarshal(raw, &in) == nil && (in.Op == "list" || in.Op == "get") {
		return core.RiskRead
	}
	return core.RiskMemory
}

// calendarInput is the tool input of API-CAL-016.
type calendarInput struct {
	Op         string          `json:"op"`
	From       string          `json:"from"`
	To         string          `json:"to"`
	ID         string          `json:"id"`
	Item       json.RawMessage `json:"item"`
	Source     string          `json:"source"`
	Ref        string          `json:"ref"`
	Occurrence json.RawMessage `json:"occurrence"`
	Done       *bool           `json:"done"`
}

func (t *calendarTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in calendarInput
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if t.svc == nil {
		return errorOutput("the calendar is not available")
	}
	by := calendarActor(tc)
	loc := t.svc.Location(ctx)
	if loc == nil {
		loc = time.Local
	}
	switch in.Op {
	case "list":
		return t.list(ctx, in, loc)
	case "get":
		if in.ID == "" {
			return inputErrorOutput(errors.New("id is required"))
		}
		detail, err := t.svc.GetItem(ctx, in.ID)
		if err != nil {
			return calendarError(err)
		}
		return calendarJSON("Item "+detail.Item.ID+":", struct {
			Item    json.RawMessage        `json:"item"`
			Firings []calendar.Firing      `json:"firings"`
			History []calendar.HistoryLine `json:"history"`
		}{localItem(detail.Item, loc), detail.Firings, detail.History})
	case "create":
		return t.create(ctx, in, loc, by)
	case "update":
		return t.update(ctx, in, loc, by)
	case "delete":
		if in.ID == "" {
			return inputErrorOutput(errors.New("id is required"))
		}
		if err := t.svc.DeleteItem(ctx, in.ID, by); err != nil {
			return calendarError(err)
		}
		return successOutput("Deleted " + in.ID + ".")
	case "move", "skip":
		return t.override(ctx, in, loc, by)
	case "complete":
		return t.complete(ctx, in, loc, by)
	case "run_missed":
		return t.runMissed(ctx, in, loc, by)
	case "":
		return inputErrorOutput(errors.New("op is required"))
	default:
		return inputErrorOutput(fmt.Errorf("unknown op %q", in.Op))
	}
}

// calendarActor is the author of a change: "agent:<name>", or "workflow" for
// a workflow tool node, which has no agent.
func calendarActor(tc core.ToolCtx) string {
	if name := strings.TrimSpace(tc.AgentName); name != "" {
		return "agent:" + name
	}
	return "workflow"
}

func (t *calendarTool) list(ctx context.Context, in calendarInput, loc *time.Location) (core.ToolOutput, error) {
	now := time.Now().In(loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if in.From != "" {
		parsed, _, err := parseCalendarLocal(in.From, loc)
		if err != nil {
			return inputErrorOutput(fmt.Errorf("from: %w", err))
		}
		from = parsed
	}
	to := from.AddDate(0, 0, calendarListDays)
	if in.To != "" {
		parsed, dateOnly, err := parseCalendarLocal(in.To, loc)
		if err != nil {
			return inputErrorOutput(fmt.Errorf("to: %w", err))
		}
		to = parsed
		if dateOnly && !to.After(from) && to.Format(calendarDateLayout) == from.Format(calendarDateLayout) {
			to = to.AddDate(0, 0, 1)
		}
	}
	res, err := t.svc.Entries(ctx, from, to, nil)
	if err != nil {
		return calendarError(err)
	}
	header := fmt.Sprintf("Calendar %s to %s (%s, end exclusive):", from.Format(calendarDateTimeLayout), to.Format(calendarDateTimeLayout), calendar.ZoneName(loc))
	return successOutput(header + "\n" + calendar.ListText(res.Entries, loc))
}

func (t *calendarTool) create(ctx context.Context, in calendarInput, loc *time.Location, by string) (core.ToolOutput, error) {
	if len(in.Item) == 0 {
		return inputErrorOutput(errors.New("item is required"))
	}
	body, err := localizeItem(in.Item, loc)
	if err != nil {
		return inputErrorOutput(err)
	}
	var it calendar.Item
	if err := json.Unmarshal(body, &it); err != nil {
		return inputErrorOutput(fmt.Errorf("item: %w", err))
	}
	created, err := t.svc.CreateItem(ctx, it, by)
	if err != nil {
		return calendarError(err)
	}
	return calendarJSON(fmt.Sprintf("Created %s %s %q.", created.Kind, created.ID, created.Title), localItem(created, loc))
}

func (t *calendarTool) update(ctx context.Context, in calendarInput, loc *time.Location, by string) (core.ToolOutput, error) {
	if in.ID == "" {
		return inputErrorOutput(errors.New("id is required"))
	}
	if len(in.Item) == 0 {
		return inputErrorOutput(errors.New("item is required"))
	}
	body, err := localizeItem(in.Item, loc)
	if err != nil {
		return inputErrorOutput(err)
	}
	updated, err := t.svc.UpdateItem(ctx, in.ID, body, by)
	if err != nil {
		return calendarError(err)
	}
	return calendarJSON(fmt.Sprintf("Updated %s %q.", updated.ID, updated.Title), localItem(updated, loc))
}

func (t *calendarTool) override(ctx context.Context, in calendarInput, loc *time.Location, by string) (core.ToolOutput, error) {
	source := in.Source
	if source == "" {
		source = calendar.OverrideItem
	}
	ref := in.Ref
	if ref == "" {
		ref = in.ID
	}
	if ref == "" {
		return inputErrorOutput(errors.New("ref is required"))
	}
	itemID := ""
	if source == calendar.OverrideItem {
		itemID = ref
	}
	occ, err := t.occurrence(ctx, in.Occurrence, itemID, loc)
	if err != nil {
		return occurrenceError(err)
	}
	o := calendar.Override{Source: source, Ref: ref, Occurrence: occ, Op: in.Op}
	if in.Op == calendar.OpMove {
		if in.To == "" {
			return inputErrorOutput(errors.New("to is required for move"))
		}
		at, dateOnly, err := parseCalendarLocal(in.To, loc)
		if err != nil {
			return inputErrorOutput(fmt.Errorf("to: %w", err))
		}
		if dateOnly {
			return inputErrorOutput(errors.New("to must be a local time YYYY-MM-DDTHH:MM"))
		}
		o.To = at.UnixMilli()
	}
	saved, err := t.svc.SaveOverride(ctx, o, by)
	if err != nil {
		return calendarError(err)
	}
	when := time.UnixMilli(saved.Occurrence).In(loc).Format(calendarDateTimeLayout)
	if saved.Op == calendar.OpMove {
		return successOutput(fmt.Sprintf("Moved %s %s to %s.", saved.Ref, when, time.UnixMilli(saved.To).In(loc).Format(calendarDateTimeLayout)))
	}
	return successOutput(fmt.Sprintf("Skipped %s %s.", saved.Ref, when))
}

func (t *calendarTool) complete(ctx context.Context, in calendarInput, loc *time.Location, by string) (core.ToolOutput, error) {
	if in.ID == "" {
		return inputErrorOutput(errors.New("id is required"))
	}
	done := true
	if in.Done != nil {
		done = *in.Done
	}
	var occ int64
	if len(in.Occurrence) > 0 {
		var err error
		if occ, err = t.occurrence(ctx, in.Occurrence, in.ID, loc); err != nil {
			return occurrenceError(err)
		}
	}
	it, habit, err := t.svc.Complete(ctx, in.ID, occ, done, by)
	if err != nil {
		return calendarError(err)
	}
	word := "done"
	if !done {
		word = "not done"
	}
	msg := fmt.Sprintf("Marked %s %q %s.", it.ID, it.Title, word)
	if habit != nil {
		msg += fmt.Sprintf(" Streak %d.", habit.Streak)
	}
	return successOutput(msg)
}

func (t *calendarTool) runMissed(ctx context.Context, in calendarInput, loc *time.Location, by string) (core.ToolOutput, error) {
	if in.ID == "" {
		return inputErrorOutput(errors.New("id is required"))
	}
	occ, err := t.occurrence(ctx, in.Occurrence, in.ID, loc)
	if err != nil {
		return occurrenceError(err)
	}
	f, err := t.svc.RunFiring(ctx, in.ID, occ, by)
	if err != nil {
		return calendarError(err)
	}
	return successOutput(fmt.Sprintf("Running the %s action of %s (status %s).", time.UnixMilli(f.Occurrence).In(loc).Format(calendarDateTimeLayout), in.ID, f.Status))
}

// errOccurrenceRequired is returned when an operation needs an occurrence.
var errOccurrenceRequired = errors.New("occurrence is required")

// occurrence resolves the occurrence input to Unix ms: a local time, a
// number of Unix ms, or a local date when itemID has exactly one occurrence
// that day.
func (t *calendarTool) occurrence(ctx context.Context, raw json.RawMessage, itemID string, loc *time.Location) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, errOccurrenceRequired
	}
	var ms int64
	if err := json.Unmarshal(raw, &ms); err == nil {
		return ms, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, errors.New("occurrence must be a local time YYYY-MM-DDTHH:MM")
	}
	at, dateOnly, err := parseCalendarLocal(text, loc)
	if err != nil {
		return 0, err
	}
	if !dateOnly {
		return at.UnixMilli(), nil
	}
	if itemID == "" {
		return 0, errors.New("occurrence needs a time YYYY-MM-DDTHH:MM for a workflow slot")
	}
	detail, err := t.svc.GetItem(ctx, itemID)
	if err != nil {
		return 0, err
	}
	occs := calendar.Occurrences(detail.Item, loc, at, at.AddDate(0, 0, 1))
	switch len(occs) {
	case 0:
		return 0, calendar.ErrNoSuchOccurrence
	case 1:
		return occs[0].UnixMilli(), nil
	default:
		return 0, fmt.Errorf("%s has %d occurrences on %s; give the time YYYY-MM-DDTHH:MM", itemID, len(occs), text)
	}
}

// occurrenceError reports an occurrence problem: a calendar error with its
// code, or an input error.
func occurrenceError(err error) (core.ToolOutput, error) {
	if calendar.ErrorCode(err) != "" {
		return calendarError(err)
	}
	return inputErrorOutput(err)
}

// parseCalendarLocal parses a local YYYY-MM-DDTHH:MM (also with a space or
// seconds) or a YYYY-MM-DD date in loc. dateOnly is true for a date, which
// yields its local midnight.
func parseCalendarLocal(s string, loc *time.Location) (time.Time, bool, error) {
	s = strings.TrimSpace(s)
	if t, err := time.ParseInLocation(calendarDateLayout, s, loc); err == nil {
		return t, true, nil
	}
	for _, layout := range []string{calendarDateTimeLayout, "2006-01-02 15:04", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, false, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("%q is not a local date YYYY-MM-DD or time YYYY-MM-DDTHH:MM", s)
}

// localizeItem rewrites the start and end fields of an item object from local
// times to Unix ms. Numbers pass through; an empty string means 0.
func localizeItem(raw json.RawMessage, loc *time.Location) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, errors.New("item must be a JSON object")
	}
	for _, key := range []string{"start", "end"} {
		value, ok := fields[key]
		if !ok {
			continue
		}
		var text string
		if json.Unmarshal(value, &text) != nil {
			continue
		}
		ms := int64(0)
		if strings.TrimSpace(text) != "" {
			at, _, err := parseCalendarLocal(text, loc)
			if err != nil {
				return nil, fmt.Errorf("item.%s: %w", key, err)
			}
			ms = at.UnixMilli()
		}
		encoded, err := json.Marshal(ms)
		if err != nil {
			return nil, err
		}
		fields[key] = encoded
	}
	return json.Marshal(fields)
}

// localItem renders an item as JSON with its start and end also as local
// times ("start_local", "end_local"), so the model reads no Unix time.
func localItem(it calendar.Item, loc *time.Location) json.RawMessage {
	data, err := json.Marshal(it)
	if err != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return data
	}
	for key, ms := range map[string]int64{"start_local": it.Start, "end_local": it.End} {
		if ms <= 0 {
			continue
		}
		local, _ := json.Marshal(time.UnixMilli(ms).In(loc).Format(calendarDateTimeLayout))
		fields[key] = local
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return data
	}
	return out
}

// calendarError renders a service error with its contract code.
func calendarError(err error) (core.ToolOutput, error) {
	msg := strings.TrimPrefix(err.Error(), "calendar: ")
	if code := calendar.ErrorCode(err); code != "" {
		return errorOutput("calendar error " + code + ": " + msg)
	}
	return errorOutput("calendar error: " + msg)
}

// calendarJSON renders a success line followed by v as JSON.
func calendarJSON(line string, v any) (core.ToolOutput, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return errorOutput(err.Error())
	}
	return successOutput(line + "\n" + string(data))
}
