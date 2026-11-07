package workflow

// Tests for the calendar hooks of the scheduler (spec #35, REQ-CAL-004,
// REQ-CAL-008): the slot filter suppresses one schedule or interval fire, and
// Slots lists upcoming fire times. They reuse the Service test helpers.

import (
	"context"
	"sync"
	"testing"
	"time"
)

// slotCall records one slot filter call.
type slotCall struct {
	workflowID, nodeID string
	at                 time.Time
}

// recordingFilter is a SlotFilter that records calls and refuses the listed
// times.
type recordingFilter struct {
	mu     sync.Mutex
	calls  []slotCall
	refuse []time.Time
}

func (f *recordingFilter) filter(_ context.Context, workflowID, nodeID string, at time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, slotCall{workflowID, nodeID, at})
	for _, r := range f.refuse {
		if r.Equal(at) {
			return false
		}
	}
	return true
}

func (f *recordingFilter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *recordingFilter) lastCall() slotCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func mustJakarta(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatalf("load Asia/Jakarta: %v", err)
	}
	return loc
}

// tickAt moves the clock, runs one tick and joins the started runs.
func tickAt(svc *Service, clk *testClock, at time.Time) {
	setClock(clk, at)
	svc.tick(clk.Now())
	svc.engine.Wait()
}

func runCount(t *testing.T, st *MemStore, id string) int {
	t.Helper()
	runs, err := st.ListRuns(context.Background(), RunFilter{WorkflowID: id})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	return len(runs)
}

// ---- AC-CAL-006 / AC-CAL-007: suppressed schedule slot ---------------------

func TestSlotFilterSchedule(t *testing.T) {
	st := NewMemStore()
	clk := &testClock{}
	svc := newTestService(t, st, newFakeDeps(), clk)
	jakarta := mustJakarta(t)

	g := Graph{Nodes: []Node{wfNode("sched", "trigger.schedule", "Every day",
		map[string]any{"cron": "0 7 * * *", "timezone": "Asia/Jakarta", "enabled": true})}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	slot := time.Date(2026, 11, 3, 7, 0, 0, 0, jakarta)
	f := &recordingFilter{refuse: []time.Time{slot}}
	svc.SetSlotFilter(f.filter)

	tickAt(svc, clk, slot.Add(10*time.Second))
	if n := runCount(t, st, "w"); n != 0 {
		t.Fatalf("suppressed 07:00 started %d runs", n)
	}
	if f.callCount() != 1 {
		t.Fatalf("filter calls=%d want 1", f.callCount())
	}
	call := f.lastCall()
	if call.workflowID != "w" || call.nodeID != "sched" || !call.at.Equal(slot) || call.at.Location().String() != "Asia/Jakarta" {
		t.Fatalf("filter call=%+v want w/sched at %v in Asia/Jakarta", call, slot)
	}

	// A second tick in the same minute does not ask again or start a run.
	tickAt(svc, clk, slot.Add(40*time.Second))
	if n := runCount(t, st, "w"); n != 0 || f.callCount() != 1 {
		t.Fatalf("retry in the same minute: runs=%d calls=%d", n, f.callCount())
	}

	tickAt(svc, clk, slot.Add(time.Minute))
	if n := runCount(t, st, "w"); n != 0 {
		t.Fatalf("a tick at 07:01 started %d runs", n)
	}

	// The next day the slot is back at 07:00.
	tickAt(svc, clk, slot.AddDate(0, 0, 1))
	runs, _ := st.ListRuns(context.Background(), RunFilter{WorkflowID: "w"})
	if len(runs) != 1 || runs[0].Trigger != "schedule" {
		t.Fatalf("next day runs=%+v want one schedule run", runs)
	}
}

// ---- suppressed interval slot ---------------------------------------------

func TestSlotFilterInterval(t *testing.T) {
	st := NewMemStore()
	clk := &testClock{}
	svc := newTestService(t, st, newFakeDeps(), clk)

	g := Graph{Nodes: []Node{wfNode("iv", "trigger.interval", "Every 30",
		map[string]any{"minutes": 30, "enabled": true})}}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	base := time.Date(2026, 11, 3, 10, 0, 15, 0, time.UTC)
	seedRunAt(t, st, wf, "interval", RunSucceeded, base.UnixMilli(), map[string]any{})

	planned := time.Date(2026, 11, 3, 10, 30, 0, 0, time.UTC)
	f := &recordingFilter{refuse: []time.Time{planned}}
	svc.SetSlotFilter(f.filter)

	tickAt(svc, clk, base.Add(30*time.Minute))
	if n := runCount(t, st, "w"); n != 1 {
		t.Fatalf("suppressed 10:30 slot: runs=%d want 1", n)
	}
	if f.callCount() != 1 || !f.lastCall().at.Equal(planned) || f.lastCall().nodeID != "iv" {
		t.Fatalf("filter calls=%d last=%+v want one call at %v", f.callCount(), f.lastCall(), planned)
	}

	// Later ticks inside the same planned slot neither fire nor ask again.
	for _, at := range []time.Time{
		base.Add(30*time.Minute + 30*time.Second),
		base.Add(45 * time.Minute),
		planned.Add(29 * time.Minute),
	} {
		tickAt(svc, clk, at)
		if n := runCount(t, st, "w"); n != 1 || f.callCount() != 1 {
			t.Fatalf("tick at %v: runs=%d calls=%d want 1 and 1", at, n, f.callCount())
		}
	}

	// The cadence continues from the suppressed slot: 11:00 fires.
	tickAt(svc, clk, planned.Add(30*time.Minute))
	if n := runCount(t, st, "w"); n != 2 {
		t.Fatalf("11:00 slot: runs=%d want 2", n)
	}
	if !f.lastCall().at.Equal(planned.Add(30 * time.Minute)) {
		t.Fatalf("filter at=%v want 11:00", f.lastCall().at)
	}
}

// ---- REQ-CAL-008: Slots ---------------------------------------------------

func TestSlots(t *testing.T) {
	st := NewMemStore()
	clk := &testClock{}
	svc := newTestService(t, st, newFakeDeps(), clk)
	ctx := context.Background()
	jakarta := mustJakarta(t)

	now := time.Date(2026, 11, 3, 6, 17, 0, 0, jakarta)
	setClock(clk, now)

	seedWorkflow(t, st, Workflow{ID: "a-sched", Name: "Sched", Guardian: "g"},
		Graph{Nodes: []Node{wfNode("sched", "trigger.schedule", "Daily",
			map[string]any{"cron": "0 7 * * *", "timezone": "Asia/Jakarta", "enabled": true})}})
	ivWF := seedWorkflow(t, st, Workflow{ID: "b-interval", Name: "Interval", Guardian: "g"},
		Graph{Nodes: []Node{wfNode("iv", "trigger.interval", "Every 2h",
			map[string]any{"minutes": 120, "enabled": true})}})
	seedRunAt(t, st, ivWF, "interval", RunSucceeded, now.Add(-30*time.Minute).UnixMilli(), map[string]any{})
	seedWorkflow(t, st, Workflow{ID: "c-short", Name: "Short", Guardian: "g"},
		Graph{Nodes: []Node{wfNode("iv5", "trigger.interval", "Every 5",
			map[string]any{"minutes": 5, "enabled": true})}})
	seedWorkflow(t, st, Workflow{ID: "d-paused", Name: "Paused", Guardian: "g", Status: StatusPaused},
		Graph{Nodes: []Node{wfNode("sched", "trigger.schedule", "Daily",
			map[string]any{"cron": "0 7 * * *", "timezone": "Asia/Jakarta", "enabled": true})}})
	disabled := wfNode("sched2", "trigger.schedule", "Off",
		map[string]any{"cron": "0 8 * * *", "timezone": "Asia/Jakarta", "enabled": true})
	disabled.Disabled = true
	seedWorkflow(t, st, Workflow{ID: "e-disabled", Name: "Disabled", Guardian: "g"},
		Graph{Nodes: []Node{
			wfNode("sched", "trigger.schedule", "Off", map[string]any{"cron": "0 7 * * *", "enabled": false}),
			disabled,
		}})

	t.Run("schedule over three future days", func(t *testing.T) {
		from := time.Date(2026, 11, 2, 0, 0, 0, 0, jakarta)
		to := time.Date(2026, 11, 6, 0, 0, 0, 0, jakarta)
		slots, err := svc.Slots(ctx, from, to)
		if err != nil {
			t.Fatalf("slots: %v", err)
		}
		got := slotsOf(slots, "a-sched")
		want := []time.Time{
			time.Date(2026, 11, 3, 7, 0, 0, 0, jakarta),
			time.Date(2026, 11, 4, 7, 0, 0, 0, jakarta),
			time.Date(2026, 11, 5, 7, 0, 0, 0, jakarta),
		}
		if len(got) != len(want) {
			t.Fatalf("schedule slots=%+v want %v", got, want)
		}
		for i, s := range got {
			if !s.At.Equal(want[i]) || s.Trigger != "schedule" || s.NodeID != "sched" ||
				s.Name != "Sched" || s.Guardian != "g" || s.Collapsed {
				t.Fatalf("slot %d=%+v want schedule at %v", i, s, want[i])
			}
		}
		if n := len(slotsOf(slots, "d-paused")); n != 0 {
			t.Fatalf("paused workflow has %d slots", n)
		}
		if n := len(slotsOf(slots, "e-disabled")); n != 0 {
			t.Fatalf("disabled triggers have %d slots", n)
		}
		for i := 1; i < len(slots); i++ {
			a, b := slots[i-1], slots[i]
			if b.At.Before(a.At) || (b.At.Equal(a.At) && b.WorkflowID < a.WorkflowID) {
				t.Fatalf("slots not sorted at %d: %+v then %+v", i, a, b)
			}
		}
	})

	t.Run("interval from the last run", func(t *testing.T) {
		slots, err := svc.Slots(ctx, now, now.Add(7*time.Hour))
		if err != nil {
			t.Fatalf("slots: %v", err)
		}
		got := slotsOf(slots, "b-interval")
		first := now.Add(90 * time.Minute) // last run 05:47 + 2h = 07:47
		want := []time.Time{first, first.Add(2 * time.Hour), first.Add(4 * time.Hour)}
		if len(got) != len(want) {
			t.Fatalf("interval slots=%+v want %v", got, want)
		}
		for i, s := range got {
			if !s.At.Equal(want[i]) || s.Trigger != "interval" || s.EveryMinutes != 120 || s.Collapsed {
				t.Fatalf("slot %d=%+v want interval at %v", i, s, want[i])
			}
		}
	})

	t.Run("short interval collapses per day", func(t *testing.T) {
		slots, err := svc.Slots(ctx, now.Add(-24*time.Hour), now.Add(48*time.Hour))
		if err != nil {
			t.Fatalf("slots: %v", err)
		}
		got := slotsOf(slots, "c-short")
		// now is never a local midnight (minute 17), so the range covers
		// today, tomorrow and part of the day after.
		if len(got) != 3 {
			t.Fatalf("collapsed slots=%+v want 3", got)
		}
		local := now.In(time.Local)
		for i, s := range got {
			want := time.Date(local.Year(), local.Month(), local.Day()+i, 0, 0, 0, 0, time.Local)
			if !s.At.Equal(want) || !s.Collapsed || s.EveryMinutes != 5 || s.Trigger != "interval" {
				t.Fatalf("slot %d=%+v want collapsed at %v", i, s, want)
			}
		}
	})

	t.Run("past range is empty", func(t *testing.T) {
		slots, err := svc.Slots(ctx, now.Add(-48*time.Hour), now.Add(-time.Hour))
		if err != nil || len(slots) != 0 {
			t.Fatalf("slots=%+v err=%v want none", slots, err)
		}
	})
}

// slotsOf filters slots by workflow id.
func slotsOf(slots []Slot, workflowID string) []Slot {
	var out []Slot
	for _, s := range slots {
		if s.WorkflowID == workflowID {
			out = append(out, s)
		}
	}
	return out
}

// ---- API-CAL-018: calendar-started runs -----------------------------------

func TestStartRunCalendarTrigger(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	ctx := context.Background()

	g := Graph{Nodes: []Node{wfNode("sched", "trigger.schedule", "Daily",
		map[string]any{"cron": "0 7 * * *", "timezone": "Asia/Jakarta", "enabled": true})}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	run, err := svc.StartRun(ctx, "w", "calendar", nodeJSON(map[string]any{"fired_at": 1, "occurrence": 2}))
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if run.Trigger != "calendar" {
		t.Fatalf("run trigger=%q want calendar", run.Trigger)
	}
	svc.engine.Wait()
	runs, _ := st.ListRuns(ctx, RunFilter{WorkflowID: "w"})
	if len(runs) != 1 || runs[0].Trigger != "calendar" {
		t.Fatalf("runs=%+v want one calendar run", runs)
	}
}
