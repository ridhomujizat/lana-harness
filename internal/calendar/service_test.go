package calendar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- fakes ------------------------------------------------------------------------

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

type startCall struct {
	workflowID string
	input      json.RawMessage
}

// fakeWorkflows lists its slots like the workflow side: future ones only.
type fakeWorkflows struct {
	mu      sync.Mutex
	clock   *fakeClock
	slots   []WorkflowSlot
	runs    []WorkflowRun
	status  map[string]string
	names   map[string]string
	started []startCall
}

func (f *fakeWorkflows) Slots(_ context.Context, from, to time.Time) ([]WorkflowSlot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	start := from
	if now := f.clock.Now(); start.Before(now) {
		start = now
	}
	var out []WorkflowSlot
	for _, s := range f.slots {
		if f.status[s.WorkflowID] == "active" && !s.At.Before(start) && s.At.Before(to) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeWorkflows) Runs(_ context.Context, from, to time.Time) ([]WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []WorkflowRun
	for _, r := range f.runs {
		if r.StartedAt >= from.UnixMilli() && r.StartedAt < to.UnixMilli() {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeWorkflows) StartRun(_ context.Context, id string, input json.RawMessage) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, startCall{id, input})
	return fmt.Sprintf("run_%d", len(f.started)), len(f.started), nil
}

func (f *fakeWorkflows) Workflow(_ context.Context, id string) (string, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.status[id]
	return f.names[id], st, ok
}

func (f *fakeWorkflows) startedRuns() []startCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]startCall(nil), f.started...)
}

type fakeActions struct {
	mu       sync.Mutex
	agents   map[string]bool
	prompts  []string
	messages []string
	// block, when set, holds SendMessage until it is closed.
	block chan struct{}
}

func (a *fakeActions) AgentPrompt(_ context.Context, agent, prompt, title string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prompts = append(a.prompts, agent+": "+prompt+" ["+title+"]")
	return fmt.Sprintf("s_%d", len(a.prompts)), nil
}

func (a *fakeActions) SendMessage(_ context.Context, target, text string) error {
	a.mu.Lock()
	block := a.block
	a.mu.Unlock()
	if block != nil {
		<-block
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append(a.messages, target+": "+text)
	return nil
}

func (a *fakeActions) AgentExists(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.agents[name]
}

func (a *fakeActions) sent() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.messages...)
}

type fakeNotifier struct {
	mu   sync.Mutex
	msgs []string
}

func (n *fakeNotifier) NotifyOwner(_ context.Context, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.msgs = append(n.msgs, text)
	return nil
}

func (n *fakeNotifier) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.msgs...)
}

// ---- fixture ------------------------------------------------------------------------

type svcFixture struct {
	t      *testing.T
	loc    *time.Location
	store  *MemStore
	clock  *fakeClock
	wf     *fakeWorkflows
	acts   *fakeActions
	notes  *fakeNotifier
	svc    *Service
	ctx    context.Context
	events <-chan Change
}

// newSvc builds a Service over a MemStore in Asia/Jakarta at Thu 2026-11-05
// 06:00, with one active workflow "wf_daily" (cron 07:00 every day of
// November), one paused workflow "wf_paused" and agent Dana.
func newSvc(t *testing.T) *svcFixture {
	t.Helper()
	loc := mustLoc(t, "Asia/Jakarta")
	f := &svcFixture{t: t, loc: loc, store: NewMemStore(), ctx: context.Background()}
	f.clock = &fakeClock{t: f.at(5, 6, 0)}
	f.wf = &fakeWorkflows{
		clock:  f.clock,
		status: map[string]string{"wf_daily": "active", "wf_paused": "paused"},
		names:  map[string]string{"wf_daily": "Daily research", "wf_paused": "Paused flow"},
	}
	for d := 1; d <= 30; d++ {
		f.wf.slots = append(f.wf.slots, WorkflowSlot{WorkflowID: "wf_daily", Name: "Daily research", Guardian: "Dana",
			NodeID: "n1", Trigger: "schedule", At: f.at(d, 7, 0)})
	}
	f.acts = &fakeActions{agents: map[string]bool{"Dana": true}}
	f.notes = &fakeNotifier{}
	f.svc = f.newService()
	ch, unsub := f.svc.Subscribe()
	f.events = ch
	t.Cleanup(func() {
		unsub()
		f.svc.Close()
	})
	return f
}

// newService builds another Service over the same store, clock and fakes,
// as a daemon restart would.
func (f *svcFixture) newService() *Service {
	s := NewService(f.store, WithClock(f.clock.Now), WithLocation(f.loc),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	s.SetWorkflows(f.wf)
	s.SetActions(f.acts)
	s.SetNotifier(f.notes)
	return s
}

func (f *svcFixture) at(day, hour, minute int) time.Time {
	return time.Date(2026, 11, day, hour, minute, 0, 0, f.loc)
}

func (f *svcFixture) create(it Item, by string) Item {
	f.t.Helper()
	out, err := f.svc.CreateItem(f.ctx, it, by)
	if err != nil {
		f.t.Fatalf("CreateItem: %v", err)
	}
	return out
}

func (f *svcFixture) entries(from, to time.Time) EntriesResult {
	f.t.Helper()
	res, err := f.svc.Entries(f.ctx, from, to, nil)
	if err != nil {
		f.t.Fatalf("Entries: %v", err)
	}
	return res
}

// drain returns the change events received so far.
func (f *svcFixture) drain() []Change {
	var out []Change
	for {
		select {
		case c := <-f.events:
			out = append(out, c)
		default:
			return out
		}
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if got := ErrorCode(err); got != code {
		t.Fatalf("error = %v (code %q), want code %q", err, got, code)
	}
}

// ---- tests ------------------------------------------------------------------------------

// AC-CAL-004 at service level.
func TestServiceEntriesRangeErrors(t *testing.T) {
	f := newSvc(t)
	from := f.at(1, 0, 0)
	_, err := f.svc.Entries(f.ctx, from, from, nil)
	wantCode(t, err, "invalid_range")
	_, err = f.svc.Entries(f.ctx, from, from.Add(-time.Hour), nil)
	wantCode(t, err, "invalid_range")
	_, err = f.svc.Entries(f.ctx, from, from.Add(MaxRange+time.Millisecond), nil)
	wantCode(t, err, "range_too_large")
	res, err := f.svc.Entries(f.ctx, from, from.Add(MaxRange), nil)
	if err != nil {
		t.Fatalf("92 days: %v", err)
	}
	if res.Timezone != "Asia/Jakarta" || res.WeekStart != 1 || res.GoogleState != "off" || res.Now != f.clock.Now().UnixMilli() {
		t.Fatalf("result header = %+v", res)
	}
	raw, _ := json.Marshal(res)
	for _, want := range []string{`"google":{"state":"off"}`, `"missed":0`, `"timezone":"Asia/Jakarta"`, `"week_start":1`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("json %s lacks %s", raw, want)
		}
	}
}

func TestServiceEntriesSources(t *testing.T) {
	f := newSvc(t)
	f.create(Item{Kind: KindReminder, Title: "Laundry", Start: f.at(5, 10, 0).UnixMilli()}, "owner")
	f.wf.runs = []WorkflowRun{{ID: "r1", WorkflowID: "wf_daily", Name: "Daily research", Status: "succeeded", Number: 3,
		StartedAt: f.at(5, 7, 0).UnixMilli() - 86400000}}
	from, to := f.at(4, 0, 0), f.at(6, 0, 0)
	all := f.entries(from, to).Entries
	if n := len(bySource(all, SourceItem)); n != 1 {
		t.Fatalf("items = %d", n)
	}
	if n := len(bySource(all, SourceWorkflowSlot)); n != 1 { // 5 Nov 07:00; 4 Nov is past
		t.Fatalf("slots = %d: %v", n, keys(all))
	}
	if n := len(bySource(all, SourceWorkflowRun)); n != 1 {
		t.Fatalf("runs = %d", n)
	}
	res, err := f.svc.Entries(f.ctx, from, to, []string{"item"})
	if err != nil || len(res.Entries) != 1 || res.Entries[0].Source != SourceItem {
		t.Fatalf("item only = %v, %v", keys(res.Entries), err)
	}
	res, err = f.svc.Entries(f.ctx, from, to, []string{"workflow"})
	if err != nil || len(res.Entries) != 2 {
		t.Fatalf("workflow only = %v, %v", keys(res.Entries), err)
	}
}

func TestServiceCreateUpdateDeleteHistory(t *testing.T) {
	f := newSvc(t)
	task := f.create(Item{Kind: KindTask, Title: "  Pay electricity ", Date: "2026-11-06"}, "agent:Dana")
	if !strings.HasPrefix(task.ID, "ci_") || task.CreatedBy != "agent:Dana" || task.UpdatedBy != "agent:Dana" ||
		task.Title != "Pay electricity" || task.State != StateOpen || task.CreatedAt != f.clock.Now().UnixMilli() {
		t.Fatalf("created = %+v", task)
	}
	if got := f.drain(); len(got) != 1 || got[0] != (Change{What: "item", ItemID: task.ID}) {
		t.Fatalf("events = %v", got)
	}

	// AC-CAL-002: invalid items are not stored.
	_, err := f.svc.CreateItem(f.ctx, Item{Kind: KindEvent, Title: "Bad", Start: 2000, End: 1000}, "owner")
	var inv *InvalidItemError
	if !errors.As(err, &inv) || ErrorCode(err) != "invalid_item" {
		t.Fatalf("invalid event err = %v", err)
	}
	_, err = f.svc.CreateItem(f.ctx, Item{Kind: KindHabit, Title: "Walk", Date: "2026-11-05"}, "owner")
	if !errors.As(err, &inv) || !hasField(inv.Fields, "repeat.freq") {
		t.Fatalf("habit without repeat err = %v", err)
	}
	_, err = f.svc.CreateItem(f.ctx, Item{Kind: KindTask, Title: "X", Date: "2026-11-05", Assignee: "Nobody"}, "owner")
	if !errors.As(err, &inv) || !hasField(inv.Fields, "assignee") {
		t.Fatalf("unknown assignee err = %v", err)
	}
	_, err = f.svc.CreateItem(f.ctx, Item{Kind: KindReminder, Title: "X", Start: 1000,
		Action: &Action{Type: ActionRunWorkflow, WorkflowID: "wf_missing"}}, "owner")
	if !errors.As(err, &inv) || !hasField(inv.Fields, "action.workflow_id") {
		t.Fatalf("unknown workflow err = %v", err)
	}
	_, err = f.svc.CreateItem(f.ctx, Item{Kind: "bogus", Title: "X"}, "owner")
	wantCode(t, err, "invalid_item")
	if items, _ := f.store.ListItems(f.ctx); len(items) != 1 {
		t.Fatalf("stored items = %d", len(items))
	}

	f.clock.Set(f.clock.Now().Add(time.Minute))
	up, err := f.svc.UpdateItem(f.ctx, task.ID, json.RawMessage(`{"title":"Pay bills","kind":"note","id":"x","created_by":"y","bogus":1,"time":"18:00"}`), "owner")
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	if up.Title != "Pay bills" || up.Kind != KindTask || up.ID != task.ID || up.CreatedBy != "agent:Dana" ||
		up.UpdatedBy != "owner" || up.Time != "18:00" || up.UpdatedAt != f.clock.Now().UnixMilli() {
		t.Fatalf("updated = %+v", up)
	}
	_, err = f.svc.UpdateItem(f.ctx, task.ID, json.RawMessage(`{"time":"25:00"}`), "owner")
	if !errors.As(err, &inv) || !hasField(inv.Fields, "time") {
		t.Fatalf("bad patch err = %v", err)
	}
	_, err = f.svc.UpdateItem(f.ctx, task.ID, json.RawMessage(`[1]`), "owner")
	wantCode(t, err, "invalid_item")
	same, err := f.svc.UpdateItem(f.ctx, task.ID, json.RawMessage(`{"title":"Pay bills"}`), "owner")
	if err != nil || same.UpdatedAt != up.UpdatedAt {
		t.Fatalf("no-op update = %+v, %v", same, err)
	}
	_, err = f.svc.UpdateItem(f.ctx, "ci_missing", json.RawMessage(`{}`), "owner")
	wantCode(t, err, "not_found")

	detail, err := f.svc.GetItem(f.ctx, task.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if len(detail.History) != 2 || detail.History[0].Summary != "Updated title, time" || detail.History[1].Summary != "Created" ||
		detail.History[1].By != "agent:Dana" || detail.Firings == nil {
		t.Fatalf("detail = %+v", detail)
	}
	raw, _ := json.Marshal(detail)
	if !bytes.Contains(raw, []byte(`"firings":[]`)) || !bytes.Contains(raw, []byte(`"summary":"Created"`)) {
		t.Fatalf("detail json = %s", raw)
	}

	f.drain()
	if err := f.svc.DeleteItem(f.ctx, task.ID, "agent:Dana"); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}
	if _, err := f.svc.GetItem(f.ctx, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted = %v", err)
	}
	wantCode(t, f.svc.DeleteItem(f.ctx, task.ID, "owner"), "not_found")
	hist, _ := f.store.ListHistory(f.ctx, task.ID, 1)
	if len(hist) != 1 || hist[0].Summary != "Deleted" || hist[0].By != "agent:Dana" {
		t.Fatalf("delete history = %+v", hist)
	}
	if got := f.drain(); len(got) != 1 || got[0].What != "item" {
		t.Fatalf("delete events = %v", got)
	}
}

func TestServiceGooglePending(t *testing.T) {
	f := newSvc(t)
	ev := f.create(Item{Kind: KindEvent, Title: "Lunch", Start: f.at(6, 12, 0).UnixMilli(), End: f.at(6, 13, 0).UnixMilli()}, "owner")
	if ev.Google != nil {
		t.Fatalf("unlinked google = %+v", ev.Google)
	}
	_ = f.store.SetKV(f.ctx, "google.state", "linked")
	_ = f.store.SetKV(f.ctx, "google.target", "primary")
	ev2 := f.create(Item{Kind: KindEvent, Title: "Dinner", Start: f.at(6, 19, 0).UnixMilli(), End: f.at(6, 20, 0).UnixMilli()}, "owner")
	if ev2.Google == nil || ev2.Google.Sync != SyncPending || ev2.Google.CalendarID != "primary" {
		t.Fatalf("linked google = %+v", ev2.Google)
	}
	task := f.create(Item{Kind: KindTask, Title: "T", Date: "2026-11-06"}, "owner")
	if task.Google != nil {
		t.Fatal("a task never syncs")
	}
	cur, _ := f.store.GetItem(f.ctx, ev2.ID)
	cur.Google.Sync, cur.Google.EventID = SyncSynced, "g1"
	_ = f.store.UpdateItem(f.ctx, cur)
	up, err := f.svc.UpdateItem(f.ctx, ev2.ID, json.RawMessage(`{"title":"Late dinner","google":null}`), "owner")
	if err != nil || up.Google == nil || up.Google.Sync != SyncPending || up.Google.EventID != "g1" {
		t.Fatalf("update google = %+v, %v", up.Google, err)
	}
	res := f.entries(f.at(6, 0, 0), f.at(7, 0, 0))
	if res.GoogleState != "linked" {
		t.Fatalf("google state = %q", res.GoogleState)
	}
}

func TestServiceUpdateDropsStaleOverrides(t *testing.T) {
	f := newSvc(t)
	// Daily 08:00 reminder from Mon 2 Nov.
	r := f.create(Item{Kind: KindReminder, Title: "Stretch", Start: f.at(2, 8, 0).UnixMilli(), Repeat: Repeat{Freq: FreqDaily}}, "owner")
	mon, tue := f.at(9, 8, 0), f.at(10, 8, 0)
	for _, occ := range []time.Time{mon, tue} {
		if _, err := f.svc.SaveOverride(f.ctx, Override{Source: OverrideItem, Ref: r.ID, Occurrence: occ.UnixMilli(), Op: OpSkip}, "owner"); err != nil {
			t.Fatalf("SaveOverride: %v", err)
		}
	}
	if _, err := f.svc.UpdateItem(f.ctx, r.ID, json.RawMessage(`{"repeat":{"freq":"weekly","days":["mon"]}}`), "owner"); err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	ovs, _ := f.store.ListOverrides(f.ctx)
	if len(ovs) != 1 || ovs[0].Occurrence != mon.UnixMilli() {
		t.Fatalf("overrides = %+v", ovs)
	}
	// A title change keeps them.
	if _, err := f.svc.UpdateItem(f.ctx, r.ID, json.RawMessage(`{"title":"Stretch!"}`), "owner"); err != nil {
		t.Fatal(err)
	}
	if ovs, _ := f.store.ListOverrides(f.ctx); len(ovs) != 1 {
		t.Fatalf("overrides after title = %+v", ovs)
	}
	// Stopping the repeat drops every override.
	if _, err := f.svc.UpdateItem(f.ctx, r.ID, json.RawMessage(`{"repeat":{"freq":"none"}}`), "owner"); err != nil {
		t.Fatal(err)
	}
	if ovs, _ := f.store.ListOverrides(f.ctx); len(ovs) != 0 {
		t.Fatalf("overrides after none = %+v", ovs)
	}
}

func TestServiceOverrideValidation(t *testing.T) {
	f := newSvc(t)
	single := f.create(Item{Kind: KindReminder, Title: "Once", Start: f.at(6, 9, 0).UnixMilli()}, "owner")
	daily := f.create(Item{Kind: KindReminder, Title: "Daily", Start: f.at(2, 9, 0).UnixMilli(), Repeat: Repeat{Freq: FreqDaily}}, "owner")
	occ := f.at(6, 9, 0).UnixMilli()
	cases := []struct {
		name string
		o    Override
		code string
	}{
		{"not repeating", Override{Source: OverrideItem, Ref: single.ID, Occurrence: occ, Op: OpSkip}, "not_repeating"},
		{"not an occurrence", Override{Source: OverrideItem, Ref: daily.ID, Occurrence: occ + 60000, Op: OpSkip}, "no_such_occurrence"},
		{"missing item", Override{Source: OverrideItem, Ref: "ci_missing", Occurrence: occ, Op: OpSkip}, "not_found"},
		{"bad op", Override{Source: OverrideItem, Ref: daily.ID, Occurrence: occ, Op: "drop"}, "invalid_override"},
		{"move without to", Override{Source: OverrideItem, Ref: daily.ID, Occurrence: occ, Op: OpMove}, "invalid_override"},
		{"bad source", Override{Source: "google", Ref: daily.ID, Occurrence: occ, Op: OpSkip}, "invalid_override"},
		{"bad ref", Override{Source: OverrideWorkflow, Ref: "wf_daily", Occurrence: f.at(6, 7, 0).UnixMilli(), Op: OpSkip}, "invalid_override"},
		{"unknown workflow", Override{Source: OverrideWorkflow, Ref: "wf_x/n1", Occurrence: f.at(6, 7, 0).UnixMilli(), Op: OpSkip}, "not_found"},
		{"not minute aligned", Override{Source: OverrideWorkflow, Ref: "wf_daily/n1", Occurrence: f.at(6, 7, 0).UnixMilli() + 1500, Op: OpSkip}, "no_such_occurrence"},
		{"not a slot", Override{Source: OverrideWorkflow, Ref: "wf_daily/n1", Occurrence: f.at(6, 8, 0).UnixMilli(), Op: OpSkip}, "no_such_occurrence"},
		{"wrong node", Override{Source: OverrideWorkflow, Ref: "wf_daily/n2", Occurrence: f.at(6, 7, 0).UnixMilli(), Op: OpSkip}, "no_such_occurrence"},
	}
	for _, c := range cases {
		_, err := f.svc.SaveOverride(f.ctx, c.o, "owner")
		if ErrorCode(err) != c.code {
			t.Errorf("%s: err = %v, want %s", c.name, err, c.code)
		}
	}
	if ovs, _ := f.store.ListOverrides(f.ctx); len(ovs) != 0 {
		t.Fatalf("stored = %+v", ovs)
	}

	o, err := f.svc.SaveOverride(f.ctx, Override{Source: OverrideItem, Ref: daily.ID, Occurrence: occ, Op: OpMove, To: occ + 3600000}, "agent:Dana")
	if err != nil || o.By != "agent:Dana" || o.At != f.clock.Now().UnixMilli() {
		t.Fatalf("item move = %+v, %v", o, err)
	}
	if _, err := f.svc.SaveOverride(f.ctx, Override{Source: OverrideItem, Ref: daily.ID, Occurrence: occ, Op: OpSkip, To: 99}, "owner"); err != nil {
		t.Fatal(err)
	}
	ovs, _ := f.store.ListOverrides(f.ctx)
	if len(ovs) != 1 || ovs[0].Op != OpSkip || ovs[0].To != 0 {
		t.Fatalf("second save replaces: %+v", ovs)
	}
	hist, _ := f.store.ListHistory(f.ctx, daily.ID, 0)
	if len(hist) != 3 || !strings.HasPrefix(hist[0].Summary, "Skipped the Fri 6 Nov 2026 09:00") ||
		!strings.Contains(hist[1].Summary, "to Fri 6 Nov 2026 10:00") {
		t.Fatalf("history = %+v", hist)
	}
	if _, err := f.svc.SaveOverride(f.ctx, Override{Source: OverrideWorkflow, Ref: "wf_daily/n1", Occurrence: f.at(6, 7, 0).UnixMilli(), Op: OpSkip}, "owner"); err != nil {
		t.Fatalf("workflow skip: %v", err)
	}
	// A past slot is accepted.
	if _, err := f.svc.SaveOverride(f.ctx, Override{Source: OverrideWorkflow, Ref: "wf_daily/n1", Occurrence: f.at(4, 7, 0).UnixMilli(), Op: OpSkip}, "owner"); err != nil {
		t.Fatalf("past workflow skip: %v", err)
	}
	f.drain()
	if err := f.svc.ClearOverride(f.ctx, OverrideItem, daily.ID, occ); err != nil {
		t.Fatal(err)
	}
	if got := f.drain(); len(got) != 1 || got[0] != (Change{What: "override", ItemID: daily.ID}) {
		t.Fatalf("clear events = %v", got)
	}
	if ovs, _ := f.store.ListOverrides(f.ctx); len(ovs) != 2 {
		t.Fatalf("after clear = %+v", ovs)
	}
}

func TestServiceComplete(t *testing.T) {
	f := newSvc(t)
	// Single task: state, occurrence ignored.
	task := f.create(Item{Kind: KindTask, Title: "Pay", Date: "2026-11-05"}, "owner")
	it, habit, err := f.svc.Complete(f.ctx, task.ID, 0, true, "agent:Dana")
	if err != nil || it.State != StateDone || habit != nil || it.UpdatedBy != "agent:Dana" {
		t.Fatalf("task done = %+v %v %v", it, habit, err)
	}
	it, _, _ = f.svc.Complete(f.ctx, task.ID, 0, false, "owner")
	if it.State != StateOpen {
		t.Fatalf("task reopened = %q", it.State)
	}

	// Repeating task: per-occurrence check.
	rt := f.create(Item{Kind: KindTask, Title: "Water plants", Date: "2026-11-02", Repeat: Repeat{Freq: FreqDaily}}, "owner")
	occ := f.at(5, 0, 0)
	if _, _, err := f.svc.Complete(f.ctx, rt.ID, occ.UnixMilli(), true, "owner"); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.svc.Complete(f.ctx, rt.ID, occ.UnixMilli()+1, true, "owner")
	wantCode(t, err, "no_such_occurrence")
	res := f.entries(f.at(4, 0, 0), f.at(6, 0, 0))
	if e := findEntry(t, res.Entries, itemKey(rt.ID, occ)); e.Status != StateDone {
		t.Fatalf("repeating task today = %q", e.Status)
	}
	if e := findEntry(t, res.Entries, itemKey(rt.ID, f.at(4, 0, 0))); e.Status != StateOpen {
		t.Fatalf("repeating task yesterday = %q", e.Status)
	}

	// Habit: 4 days done, today ticked → streak 5 (AC-CAL-008).
	h := f.create(Item{Kind: KindHabit, Title: "Read", Date: "2026-10-01", Repeat: Repeat{Freq: FreqDaily}}, "owner")
	for d := 1; d <= 4; d++ {
		if _, hs, err := f.svc.Complete(f.ctx, h.ID, f.at(d, 0, 0).UnixMilli(), true, "owner"); err != nil || hs == nil {
			t.Fatalf("tick %d: %v", d, err)
		}
	}
	_, hs, err := f.svc.Complete(f.ctx, h.ID, f.at(5, 0, 0).UnixMilli(), true, "owner")
	if err != nil || hs == nil || !hs.Done || hs.Streak != 5 {
		t.Fatalf("habit today = %+v, %v", hs, err)
	}
	_, hs, _ = f.svc.Complete(f.ctx, h.ID, f.at(5, 0, 0).UnixMilli(), false, "owner")
	if hs == nil || hs.Done || hs.Streak != 4 {
		t.Fatalf("habit unticked = %+v", hs)
	}
	_, _, err = f.svc.Complete(f.ctx, h.ID, 0, true, "owner")
	wantCode(t, err, "no_such_occurrence")

	ev := f.create(Item{Kind: KindReminder, Title: "R", Start: f.at(5, 9, 0).UnixMilli()}, "owner")
	_, _, err = f.svc.Complete(f.ctx, ev.ID, 0, true, "owner")
	wantCode(t, err, "not_completable")
	_, _, err = f.svc.Complete(f.ctx, "ci_missing", 0, true, "owner")
	wantCode(t, err, "not_found")
}

func TestServiceSettings(t *testing.T) {
	f := newSvc(t)
	st, err := f.svc.Settings(f.ctx)
	if err != nil || st.Timezone != "Asia/Jakarta" || st.WeekStart != 1 || st.Agenda.Enabled || st.Agenda.Time != "07:00" {
		t.Fatalf("defaults = %+v, %v", st, err)
	}
	for _, bad := range []Settings{
		{Timezone: "Mars/Base", WeekStart: 1},
		{Timezone: "", WeekStart: 1},
		{Timezone: "UTC", WeekStart: 2},
		{Timezone: "UTC", WeekStart: 1, Agenda: AgendaSettings{Time: "7am"}},
	} {
		_, err := f.svc.PutSettings(f.ctx, bad)
		wantCode(t, err, "invalid_settings")
		if !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("errors.Is: %v", err)
		}
	}
	f.drain()
	saved, err := f.svc.PutSettings(f.ctx, Settings{Timezone: "Europe/Berlin", WeekStart: 0, Agenda: AgendaSettings{Enabled: true}})
	if err != nil || saved.Agenda.Time != "07:00" {
		t.Fatalf("put = %+v, %v", saved, err)
	}
	if got := f.drain(); len(got) != 1 || got[0].What != "settings" {
		t.Fatalf("events = %v", got)
	}
	res := f.entries(f.at(5, 0, 0), f.at(6, 0, 0))
	if res.Timezone != "Europe/Berlin" || res.WeekStart != 0 {
		t.Fatalf("entries header = %+v", res)
	}
	if f.svc.location(f.ctx).String() != "Europe/Berlin" {
		t.Fatalf("location = %v", f.svc.location(f.ctx))
	}
}

func TestServiceICSToken(t *testing.T) {
	f := newSvc(t)
	if f.svc.CheckICSToken(f.ctx, "") || f.svc.CheckICSToken(f.ctx, "x") {
		t.Fatal("no token yet")
	}
	tok, err := f.svc.ICSToken(f.ctx)
	if err != nil || len(tok) != 43 || strings.ContainsAny(tok, "+/=") {
		t.Fatalf("token = %q, %v", tok, err)
	}
	again, _ := f.svc.ICSToken(f.ctx)
	if again != tok || !f.svc.CheckICSToken(f.ctx, tok) || f.svc.CheckICSToken(f.ctx, tok+"x") {
		t.Fatal("token is not stable or check is wrong")
	}
	rot, err := f.svc.RotateICS(f.ctx)
	if err != nil || rot == tok || f.svc.CheckICSToken(f.ctx, tok) || !f.svc.CheckICSToken(f.ctx, rot) {
		t.Fatalf("rotate = %q, %v", rot, err)
	}

	// A daily dated task: the read is split at 6 Jan 06:00, inside the 6 Jan entry.
	r := f.create(Item{Kind: KindTask, Title: "Feed", Date: "2026-11-02", Repeat: Repeat{Freq: FreqDaily}}, "owner")
	var buf bytes.Buffer
	if err := f.svc.WriteFeed(f.ctx, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// 30 days back from 5 Nov 06:00 to 90 days ahead (3 Feb 06:00): 2 Nov .. 3 Feb = 94 days, each once.
	if n := strings.Count(out, "SUMMARY:Feed"); n != 94 {
		t.Fatalf("feed occurrences = %d", n)
	}
	if !strings.Contains(out, "UID:"+itemKey(r.ID, f.at(5, 0, 0))+"@lana") || !strings.Contains(out, "Daily research (workflow)") {
		t.Fatalf("feed lacks uid or slot")
	}
	if strings.Count(out, "UID:"+itemKey(r.ID, time.Date(2027, 1, 6, 0, 0, 0, 0, f.loc))+"@lana") != 1 {
		t.Fatal("entry at the split point is duplicated or missing")
	}
}

func TestServiceAgendaText(t *testing.T) {
	f := newSvc(t)
	f.create(Item{Kind: KindReminder, Title: "Laundry", Start: f.at(5, 10, 0).UnixMilli()}, "owner")
	f.create(Item{Kind: KindTask, Title: "Pay electricity", Date: "2026-11-05"}, "agent:Dana")
	y := f.create(Item{Kind: KindReminder, Title: "Weekly recap", Start: f.at(4, 18, 0).UnixMilli(),
		Action: &Action{Type: ActionAgentPrompt, Agent: "Dana", Prompt: "recap"}}, "owner")
	_, _ = f.store.InsertFiring(f.ctx, Firing{ItemID: y.ID, Occurrence: f.at(4, 18, 0).UnixMilli(), Status: FiringMissed})
	text, err := f.svc.Agenda(f.ctx, f.at(5, 6, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"📅 Thu 5 Nov", "• 07:00 ⚙ Daily research (workflow)", "• 10:00 ⏰ Laundry",
		"☐ Task: Pay electricity (added by Dana)", "⚠ Yesterday: 1 missed — Weekly recap (Dana)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("agenda lacks %q:\n%s", want, text)
		}
	}
}

// AC-CAL-006 (calendar side): the slot filter suppresses the original slot,
// the moved slot stays visible after its original time and starts once.
func TestServiceMovedWorkflowSlotACCAL006(t *testing.T) {
	f := newSvc(t)
	f.svc.boot(f.ctx)
	orig, moved := f.at(5, 7, 0), f.at(5, 9, 30)
	if _, err := f.svc.SaveOverride(f.ctx, Override{Source: OverrideWorkflow, Ref: "wf_daily/n1",
		Occurrence: orig.UnixMilli(), Op: OpMove, To: moved.UnixMilli()}, "owner"); err != nil {
		t.Fatal(err)
	}
	filter := f.svc.SlotFilter()
	if filter(f.ctx, "wf_daily", "n1", orig) {
		t.Fatal("original slot must be suppressed")
	}
	if !filter(f.ctx, "wf_daily", "n1", f.at(6, 7, 0)) || !filter(f.ctx, "wf_daily", "n2", orig) {
		t.Fatal("other slots must fire")
	}

	day := func() Entry {
		res := f.entries(f.at(5, 0, 0), f.at(6, 0, 0))
		return findEntry(t, res.Entries, fmt.Sprintf("wf:wf_daily/n1:%d", orig.UnixMilli()))
	}
	if e := day(); !e.Moved || e.Start != moved.UnixMilli() {
		t.Fatalf("before 07:00 = %+v", e)
	}
	f.clock.Set(f.at(5, 7, 0))
	f.svc.tick()
	f.clock.Set(f.at(5, 8, 0))
	f.svc.tick()
	if e := day(); !e.Moved || e.Start != moved.UnixMilli() || e.Title != "Daily research" {
		t.Fatalf("after original time = %+v", e)
	}
	if n := len(f.wf.startedRuns()); n != 0 {
		t.Fatalf("runs before 09:30 = %d", n)
	}
	f.clock.Set(f.at(5, 9, 30).Add(10 * time.Second))
	f.svc.tick()
	runs := f.wf.startedRuns()
	if len(runs) != 1 || runs[0].workflowID != "wf_daily" || !strings.Contains(string(runs[0].input), fmt.Sprintf(`"occurrence":%d`, orig.UnixMilli())) {
		t.Fatalf("runs at 09:30 = %+v", runs)
	}
	f.clock.Set(f.at(5, 9, 31))
	f.svc.tick()
	// A restart does not fire it again either.
	s2 := f.newService()
	defer s2.Close()
	s2.boot(f.ctx)
	s2.tick()
	if n := len(f.wf.startedRuns()); n != 1 {
		t.Fatalf("runs after 09:31 = %d", n)
	}
	ovs, _ := f.store.ListOverrides(f.ctx)
	if len(ovs) != 1 || ovs[0].FiredAt == 0 {
		t.Fatalf("override = %+v", ovs)
	}
	f.clock.Set(f.at(5, 10, 0))
	res := f.entries(f.at(5, 0, 0), f.at(6, 0, 0))
	if n := len(bySource(res.Entries, SourceWorkflowSlot)); n != 0 {
		t.Fatalf("moved slot after it ran = %v", keys(res.Entries))
	}
	// The next day the slot is at 07:00 again.
	res = f.entries(f.at(6, 0, 0), f.at(7, 0, 0))
	if e := findEntry(t, res.Entries, fmt.Sprintf("wf:wf_daily/n1:%d", f.at(6, 7, 0).UnixMilli())); e.Moved || e.Start != f.at(6, 7, 0).UnixMilli() {
		t.Fatalf("next day = %+v", e)
	}
}

// A slot moved from another day into the range shows up even when the
// original is outside the range.
func TestServiceMovedSlotFromOutsideRange(t *testing.T) {
	f := newSvc(t)
	orig := f.at(8, 7, 0)
	moved := f.at(6, 15, 0)
	if _, err := f.svc.SaveOverride(f.ctx, Override{Source: OverrideWorkflow, Ref: "wf_daily/n1",
		Occurrence: orig.UnixMilli(), Op: OpMove, To: moved.UnixMilli()}, "owner"); err != nil {
		t.Fatal(err)
	}
	res := f.entries(f.at(6, 0, 0), f.at(7, 0, 0))
	if e := findEntry(t, res.Entries, fmt.Sprintf("wf:wf_daily/n1:%d", orig.UnixMilli())); !e.Moved || e.Start != moved.UnixMilli() {
		t.Fatalf("moved in = %+v", e)
	}
	res = f.entries(f.at(8, 0, 0), f.at(9, 0, 0))
	for _, e := range res.Entries {
		if e.Occurrence == orig.UnixMilli() {
			t.Fatalf("moved out slot still shows: %+v", e)
		}
	}
}

// AC-CAL-007: a skipped slot does not run and shows skipped after its time.
func TestServiceSkippedWorkflowSlotACCAL007(t *testing.T) {
	f := newSvc(t)
	f.svc.boot(f.ctx)
	orig := f.at(5, 7, 0)
	if _, err := f.svc.SaveOverride(f.ctx, Override{Source: OverrideWorkflow, Ref: "wf_daily/n1",
		Occurrence: orig.UnixMilli(), Op: OpSkip}, "owner"); err != nil {
		t.Fatal(err)
	}
	if f.svc.SlotFilter()(f.ctx, "wf_daily", "n1", orig) {
		t.Fatal("skipped slot must be suppressed")
	}
	f.clock.Set(f.at(5, 8, 0))
	f.svc.tick()
	if n := len(f.wf.startedRuns()); n != 0 {
		t.Fatalf("runs = %d", n)
	}
	res := f.entries(f.at(5, 0, 0), f.at(6, 0, 0))
	if e := findEntry(t, res.Entries, fmt.Sprintf("wf:wf_daily/n1:%d", orig.UnixMilli())); e.Status != StatusSkipped {
		t.Fatalf("entry = %+v", e)
	}
	// A paused workflow's past skip is not shown.
	f.wf.mu.Lock()
	f.wf.status["wf_daily"] = "paused"
	f.wf.mu.Unlock()
	res = f.entries(f.at(5, 0, 0), f.at(6, 0, 0))
	if n := len(bySource(res.Entries, SourceWorkflowSlot)); n != 0 {
		t.Fatalf("paused slots = %v", keys(res.Entries))
	}
}

func TestServiceSubscribeAndClose(t *testing.T) {
	f := newSvc(t)
	ch, unsub := f.svc.Subscribe()
	it := f.create(Item{Kind: KindNote, Title: "Idea", Date: "2026-11-05"}, "owner")
	select {
	case c := <-ch:
		if c.What != ChangeItem || c.ItemID != it.ID {
			t.Fatalf("change = %+v", c)
		}
		raw, _ := json.Marshal(c)
		if string(raw) != fmt.Sprintf(`{"what":"item","item_id":%q}`, it.ID) {
			t.Fatalf("change json = %s", raw)
		}
	default:
		t.Fatal("no change event")
	}
	unsub()
	unsub()
	if _, ok := <-ch; ok {
		t.Fatal("channel open after unsubscribe")
	}
}
