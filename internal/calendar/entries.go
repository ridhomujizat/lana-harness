package calendar

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// maxStreakWalk caps how many days a streak count walks back.
const maxStreakWalk = 3700

// WorkflowSlot is one computed fire time of an active workflow trigger, as
// handed over by the workflow side (REQ-CAL-008). Slots cover the whole
// entries range, past included: Build hides past ones. A Collapsed slot
// stands for one local day (the day of At) of an interval shorter than 60
// minutes; the workflow side emits one per day, duplicates merge.
type WorkflowSlot struct {
	WorkflowID   string
	Name         string
	Guardian     string
	NodeID       string
	Trigger      string // schedule|interval
	At           time.Time
	EveryMinutes int
	Collapsed    bool
}

// Ref is the override ref of the slot: "<workflow id>/<node id>".
func (s WorkflowSlot) Ref() string { return s.WorkflowID + "/" + s.NodeID }

// WorkflowRun is one workflow run shown on the calendar (REQ-CAL-008).
type WorkflowRun struct {
	ID         string
	WorkflowID string
	Name       string
	Guardian   string
	Trigger    string
	Status     string
	Number     int
	StartedAt  int64
	EndedAt    int64
}

// GoogleCalendarInfo is the display data of one linked Google calendar.
type GoogleCalendarInfo struct {
	Name  string
	Color string
}

// BuildInput is everything Build merges into entries.
type BuildInput struct {
	Items     []Item
	Overrides []Override
	Firings   []Firing
	Checks    []HabitCheck
	Slots     []WorkflowSlot
	Runs      []WorkflowRun
	// Google holds cached Google events; linked ones (ItemID set) are skipped.
	Google []GoogleEvent
	// GoogleCalendars gives Google entries their calendar name and color.
	GoogleCalendars map[string]GoogleCalendarInfo
	From, To, Now   time.Time
	Loc             *time.Location
}

type overrideKey struct {
	source, ref string
	occurrence  int64
}

type firingKey struct {
	itemID     string
	occurrence int64
}

// builder holds the indexes of one Build call.
type builder struct {
	in        BuildInput
	loc       *time.Location
	today     civilDay
	overrides map[overrideKey]Override
	firings   map[firingKey]Firing
	checks    map[string]map[string]bool
	out       []Entry
}

// Build merges items, overrides, firings, habit checks, workflow slots, runs
// and Google events into the entries that overlap [From, To), sorted by
// start, then title (REQ-CAL-002, REQ-CAL-005, REQ-CAL-008). It is pure.
func Build(in BuildInput) []Entry {
	b := &builder{
		in:        in,
		loc:       in.Loc,
		overrides: map[overrideKey]Override{},
		firings:   map[firingKey]Firing{},
		checks:    map[string]map[string]bool{},
		out:       []Entry{},
	}
	if b.loc == nil {
		b.loc = time.Local
	}
	b.today = dayOf(in.Now, b.loc)
	for _, o := range in.Overrides {
		b.overrides[overrideKey{o.Source, o.Ref, o.Occurrence}] = o
	}
	for _, f := range in.Firings {
		b.firings[firingKey{f.ItemID, f.Occurrence}] = f
	}
	for _, c := range in.Checks {
		if b.checks[c.ItemID] == nil {
			b.checks[c.ItemID] = map[string]bool{}
		}
		b.checks[c.ItemID][c.Date] = true
	}
	if !in.From.Before(in.To) {
		return b.out
	}
	for _, it := range in.Items {
		if it.DeletedAt == 0 {
			b.item(it)
		}
	}
	b.slots()
	b.runs()
	b.google()
	sort.SliceStable(b.out, func(i, j int) bool {
		x, y := b.out[i], b.out[j]
		if x.Start != y.Start {
			return x.Start < y.Start
		}
		if x.Title != y.Title {
			return x.Title < y.Title
		}
		return x.Key < y.Key
	})
	return b.out
}

// item adds the occurrences of one item, overrides applied. An occurrence
// moved into the range from outside it is included; one moved out is not.
func (b *builder) item(it Item) {
	p := patternOf(it, b.loc)
	if !p.ok {
		return
	}
	occs := Occurrences(it, b.loc, b.in.From, b.in.To)
	seen := make(map[int64]bool, len(occs))
	for _, occ := range occs {
		seen[occ.UnixMilli()] = true
	}
	if it.Repeat.Repeats() {
		for _, o := range b.in.Overrides {
			if o.Source != OverrideItem || o.Ref != it.ID || o.Op != OpMove || seen[o.Occurrence] {
				continue
			}
			to := time.UnixMilli(o.To)
			s, e := p.spanAt(to, b.loc)
			if !overlaps(s, e, b.in.From, b.in.To) {
				continue
			}
			occ := time.UnixMilli(o.Occurrence)
			if IsOccurrence(it, b.loc, occ) {
				occs = append(occs, occ)
				seen[o.Occurrence] = true
			}
		}
	}
	for _, occ := range occs {
		b.occurrence(it, p, occ)
	}
}

func (b *builder) occurrence(it Item, p pattern, occ time.Time) {
	occMs := occ.UnixMilli()
	start := occ
	ov, hasOv := b.overrides[overrideKey{OverrideItem, it.ID, occMs}]
	moved := hasOv && ov.Op == OpMove
	if moved {
		start = time.UnixMilli(ov.To)
	}
	s, e := p.spanAt(start, b.loc)
	if !overlaps(s, e, b.in.From, b.in.To) {
		return
	}
	occDay := dayOf(occ, b.loc)
	e2 := Entry{
		Key:        fmt.Sprintf("item:%s:%d", it.ID, occMs),
		Source:     SourceItem,
		Kind:       it.Kind,
		Title:      it.Title,
		AllDay:     it.AllDay,
		Color:      it.Color,
		Moved:      moved,
		Occurrence: occMs,
		ItemID:     it.ID,
		Repeating:  it.Repeat.Repeats(),
		Assignee:   it.Assignee,
		CreatedBy:  it.CreatedBy,
		Action:     cloneAction(it.Action),
	}
	if e2.Color == "" {
		e2.Color = DefaultColor(it.Kind)
	}
	if p.timed {
		e2.Start = start.UnixMilli()
		e2.Date = dayOf(start, b.loc).String()
		e2.EndDate = e2.Date
		if p.span > 0 {
			e2.End = e.UnixMilli()
			e2.EndDate = dayOf(e.Add(-time.Nanosecond), b.loc).String()
		}
	} else {
		d := dayOf(start, b.loc)
		e2.Start = start.UnixMilli()
		e2.Date = d.String()
		e2.EndDate = d.add(p.days - 1).String()
	}
	if f, ok := b.firings[firingKey{it.ID, occMs}]; ok {
		e2.Firing = &f
	}
	switch {
	case hasOv && ov.Op == OpSkip:
		e2.Status = StatusSkipped
	case it.Kind == KindTask:
		e2.Status = b.taskState(it, occDay)
	case e2.Firing != nil:
		e2.Status = e2.Firing.Status
	}
	if it.Kind == KindHabit {
		done := b.checks[it.ID][occDay.String()]
		e2.Habit = &EntryHabit{Done: done, Streak: b.streak(it, p, occDay)}
	}
	if it.Google != nil {
		e2.Google = &EntryGoogle{CalendarID: it.Google.CalendarID, EventID: it.Google.EventID, Sync: it.Google.Sync}
	}
	b.out = append(b.out, e2)
}

// taskState is "open" or "done". A repeating task is done per occurrence
// through a HabitCheck of that date; a single task uses its State.
func (b *builder) taskState(it Item, d civilDay) string {
	if it.Repeat.Repeats() {
		if b.checks[it.ID][d.String()] {
			return StateDone
		}
		return StateOpen
	}
	if it.State == "" {
		return StateOpen
	}
	return it.State
}

// streak counts the consecutive scheduled occurrences marked done, back from
// ref (REQ-CAL-005). A future ref counts from today; when the reference day
// is today and not yet done, counting starts at the previous occurrence.
// Skipped occurrences are not scheduled and neither count nor break it.
func (b *builder) streak(it Item, p pattern, ref civilDay) int {
	done := b.checks[it.ID]
	if b.today.before(ref) {
		ref = b.today
	}
	d := ref
	if d == b.today && !done[d.String()] {
		d = d.add(-1)
	}
	count := 0
	for i := 0; i < maxStreakWalk && !d.before(p.base); i, d = i+1, d.add(-1) {
		if !p.matches(it.Repeat, d) {
			continue
		}
		ov, ok := b.overrides[overrideKey{OverrideItem, it.ID, p.instant(d, b.loc).UnixMilli()}]
		if ok && ov.Op == OpSkip {
			continue
		}
		if !done[d.String()] {
			break
		}
		count++
	}
	return count
}

// slots adds workflow slot entries (REQ-CAL-004, REQ-CAL-008).
func (b *builder) slots() {
	collapsed := map[string]bool{}
	for _, s := range b.in.Slots {
		ref := s.Ref()
		info := &EntryWorkflow{
			ID:           s.WorkflowID,
			Name:         s.Name,
			Guardian:     s.Guardian,
			NodeID:       s.NodeID,
			Trigger:      s.Trigger,
			EveryMinutes: s.EveryMinutes,
		}
		e := Entry{
			Source:    SourceWorkflowSlot,
			Kind:      KindWorkflow,
			Title:     s.Name,
			Color:     DefaultColor(KindWorkflow),
			Status:    StatusScheduled,
			Repeating: true,
			Workflow:  info,
		}
		if s.Collapsed {
			d := dayOf(s.At, b.loc)
			mid, next := d.midnight(b.loc), d.add(1).midnight(b.loc)
			id := ref + "@" + d.String()
			if collapsed[id] || !next.After(b.in.Now) || !overlaps(mid, next, b.in.From, b.in.To) {
				continue
			}
			collapsed[id] = true
			e.Key = fmt.Sprintf("wf:%s:%d", ref, mid.UnixMilli())
			e.Start = mid.UnixMilli()
			e.AllDay = true
			e.Date, e.EndDate = d.String(), d.String()
			e.Occurrence = mid.UnixMilli()
			e.Readonly = true
			b.out = append(b.out, e)
			continue
		}
		at := s.At
		atMs := at.UnixMilli()
		start := at
		ov, hasOv := b.overrides[overrideKey{OverrideWorkflow, ref, atMs}]
		switch {
		case hasOv && ov.Op == OpSkip:
			e.Status = StatusSkipped
		case hasOv && ov.Op == OpMove:
			start = time.UnixMilli(ov.To)
			e.Moved = true
			if !start.After(b.in.Now) {
				continue
			}
		case !at.After(b.in.Now):
			continue
		}
		if !overlaps(start, start, b.in.From, b.in.To) {
			continue
		}
		e.Key = fmt.Sprintf("wf:%s:%d", ref, atMs)
		e.Start = start.UnixMilli()
		e.Date = dayOf(start, b.loc).String()
		e.EndDate = e.Date
		e.Occurrence = atMs
		b.out = append(b.out, e)
	}
}

// runs adds one entry per workflow run started in the range.
func (b *builder) runs() {
	from, to := b.in.From.UnixMilli(), b.in.To.UnixMilli()
	for _, r := range b.in.Runs {
		if r.StartedAt < from || r.StartedAt >= to {
			continue
		}
		name := r.Name
		if name == "" {
			name = r.WorkflowID
		}
		e := Entry{
			Key:        "run:" + r.ID,
			Source:     SourceWorkflowRun,
			Kind:       KindWorkflow,
			Title:      name,
			Start:      r.StartedAt,
			Color:      runColor(r.Status),
			Status:     r.Status,
			Occurrence: r.StartedAt,
			Readonly:   true,
			Workflow: &EntryWorkflow{
				ID:        r.WorkflowID,
				Name:      name,
				Guardian:  r.Guardian,
				Trigger:   r.Trigger,
				RunNumber: r.Number,
				RunStatus: r.Status,
			},
		}
		start := time.UnixMilli(r.StartedAt)
		e.Date = dayOf(start, b.loc).String()
		e.EndDate = e.Date
		if r.EndedAt > r.StartedAt {
			e.End = r.EndedAt
			e.EndDate = dayOf(time.UnixMilli(r.EndedAt).Add(-time.Nanosecond), b.loc).String()
		}
		b.out = append(b.out, e)
	}
}

func runColor(status string) string {
	switch status {
	case "succeeded":
		return "green"
	case "failed":
		return "rose"
	case "cancelled":
		return "gray"
	}
	return "amber"
}

type googleTimeJSON struct {
	Date     string `json:"date"`
	DateTime string `json:"dateTime"`
}

type googleEventJSON struct {
	Status  string         `json:"status"`
	Summary string         `json:"summary"`
	Start   googleTimeJSON `json:"start"`
	End     googleTimeJSON `json:"end"`
}

// googleSpan is a decoded Google event.
type googleSpan struct {
	title          string
	allDay         bool
	start, end     time.Time // timed: instants; all-day: local midnights, end exclusive
	firstDay, last civilDay
}

// decodeGoogle reads the parts of a Google event resource that the calendar
// shows. Cancelled and unreadable events report false.
func decodeGoogle(ev GoogleEvent, loc *time.Location) (googleSpan, bool) {
	var raw googleEventJSON
	if err := json.Unmarshal([]byte(ev.JSON), &raw); err != nil || raw.Status == "cancelled" {
		return googleSpan{}, false
	}
	g := googleSpan{title: strings.TrimSpace(raw.Summary)}
	if g.title == "" {
		g.title = "(no title)"
	}
	if raw.Start.Date != "" {
		first, ok := parseDay(raw.Start.Date)
		if !ok {
			return googleSpan{}, false
		}
		last := first
		if end, ok := parseDay(raw.End.Date); ok && first.before(end) {
			last = end.add(-1)
		}
		g.allDay, g.firstDay, g.last = true, first, last
		g.start, g.end = first.midnight(loc), last.add(1).midnight(loc)
		return g, true
	}
	start, err := time.Parse(time.RFC3339, raw.Start.DateTime)
	if err != nil {
		return googleSpan{}, false
	}
	end, err := time.Parse(time.RFC3339, raw.End.DateTime)
	if err != nil || end.Before(start) {
		end = start
	}
	g.start, g.end = start, end
	g.firstDay = dayOf(start, loc)
	g.last = g.firstDay
	if end.After(start) {
		g.last = dayOf(end.Add(-time.Nanosecond), loc)
	}
	return g, true
}

// google adds the cached Google events not linked to an item. They are read
// only: a change goes through a linked item.
func (b *builder) google() {
	for _, ev := range b.in.Google {
		if ev.ItemID != "" {
			continue
		}
		g, ok := decodeGoogle(ev, b.loc)
		if !ok || !overlaps(g.start, g.end, b.in.From, b.in.To) {
			continue
		}
		info := b.in.GoogleCalendars[ev.CalendarID]
		e := Entry{
			Key:        "google:" + ev.CalendarID + ":" + ev.EventID,
			Source:     SourceGoogle,
			Kind:       KindEvent,
			Title:      g.title,
			Start:      g.start.UnixMilli(),
			AllDay:     g.allDay,
			Date:       g.firstDay.String(),
			EndDate:    g.last.String(),
			Color:      DefaultColor(KindEvent),
			Occurrence: g.start.UnixMilli(),
			CreatedBy:  SourceGoogle,
			Readonly:   true,
			Google: &EntryGoogle{
				CalendarID:   ev.CalendarID,
				EventID:      ev.EventID,
				Sync:         SyncSynced,
				Color:        info.Color,
				CalendarName: info.Name,
			},
		}
		if !g.allDay && g.end.After(g.start) {
			e.End = g.end.UnixMilli()
		}
		b.out = append(b.out, e)
	}
}

// entryTimed reports whether e sits on the hour grid (mirrors the web UI's
// isTimed): all-day entries, notes and tasks or habits at local midnight of
// their date are dated.
func entryTimed(e Entry, loc *time.Location) bool {
	if e.AllDay || e.Kind == KindNote {
		return false
	}
	if e.Kind == KindTask || e.Kind == KindHabit {
		d, ok := parseDay(e.Date)
		return e.Start > 0 && (!ok || e.Start != d.midnight(loc).UnixMilli())
	}
	return e.Start > 0
}

func cloneAction(a *Action) *Action {
	if a == nil {
		return nil
	}
	c := *a
	if a.Input != nil {
		c.Input = append(json.RawMessage(nil), a.Input...)
	}
	return &c
}
