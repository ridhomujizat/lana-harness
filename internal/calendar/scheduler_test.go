package calendar

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func (f *svcFixture) firing(itemID string, occ time.Time) Firing {
	f.t.Helper()
	fr, err := f.store.GetFiring(f.ctx, itemID, occ.UnixMilli())
	if err != nil {
		f.t.Fatalf("GetFiring %s %v: %v", itemID, occ, err)
	}
	return fr
}

// tickAt moves the clock, runs one scheduler pass and waits for the actions
// it started.
func (f *svcFixture) tickAt(t time.Time) {
	f.clock.Set(t)
	f.svc.tick()
	f.svc.runWG.Wait()
}

func (f *svcFixture) setLastTick(t time.Time) {
	_ = f.store.SetKV(f.ctx, "last_tick", strconv.FormatInt(t.UnixMilli(), 10))
}

// AC-CAL-010: a send_message action due at 10:00 sends one message and the
// firing is done; an agent_prompt action records its session.
func TestSchedulerSendMessageACCAL010(t *testing.T) {
	f := newSvc(t)
	due := f.at(5, 10, 0)
	msg := f.create(Item{Kind: KindReminder, Title: "Laundry", Start: due.UnixMilli(),
		Action: &Action{Type: ActionSendMessage, Target: "telegram", Text: "Take the laundry out"}}, "owner")
	ask := f.create(Item{Kind: KindEvent, Title: "Recap", Start: due.UnixMilli(), End: due.Add(time.Hour).UnixMilli(),
		Action: &Action{Type: ActionAgentPrompt, Agent: "Dana", Prompt: "Recap the week"}}, "owner")
	f.clock.Set(f.at(5, 9, 59))
	f.svc.boot(f.ctx)
	f.tickAt(f.at(5, 9, 59))
	if n := len(f.acts.sent()); n != 0 {
		t.Fatalf("sent before due = %d", n)
	}
	f.tickAt(due.Add(20 * time.Second))
	f.tickAt(due.Add(50 * time.Second))
	f.tickAt(due.Add(10 * time.Minute))
	if got := f.acts.sent(); len(got) != 1 || got[0] != "telegram: Take the laundry out" {
		t.Fatalf("sent = %v", got)
	}
	fr := f.firing(msg.ID, due)
	if fr.Status != FiringDone || fr.StartedAt != due.Add(20*time.Second).UnixMilli() || fr.EndedAt == 0 || fr.Error != "" {
		t.Fatalf("firing = %+v", fr)
	}
	fa := f.firing(ask.ID, due)
	if fa.Status != FiringDone || fa.SessionID != "s_1" {
		t.Fatalf("agent firing = %+v", fa)
	}
	if len(f.acts.prompts) != 1 || f.acts.prompts[0] != "Dana: Recap the week [Recap]" {
		t.Fatalf("prompts = %v", f.acts.prompts)
	}
	if n := len(f.notes.all()); n != 0 {
		t.Fatalf("notices = %v", f.notes.all())
	}
	raw, _, _ := f.store.GetKV(f.ctx, "last_tick")
	if raw != strconv.FormatInt(due.Add(10*time.Minute).UnixMilli(), 10) {
		t.Fatalf("last_tick = %s", raw)
	}
	res := f.entries(f.at(5, 0, 0), f.at(6, 0, 0))
	if e := findEntry(t, res.Entries, itemKey(msg.ID, due)); e.Firing == nil || e.Status != FiringDone {
		t.Fatalf("entry = %+v", e)
	}
}

// Tasks and habits without a time fire at 09:00; skipped occurrences never
// fire and moved ones fire at their new time under the original key.
func TestSchedulerDueRulesAndItemOverrides(t *testing.T) {
	f := newSvc(t)
	task := f.create(Item{Kind: KindTask, Title: "Pay", Date: "2026-11-05",
		Action: &Action{Type: ActionSendMessage, Target: "web", Text: "pay"}}, "owner")
	daily := f.create(Item{Kind: KindReminder, Title: "Pill", Start: f.at(1, 8, 0).UnixMilli(), Repeat: Repeat{Freq: FreqDaily},
		Action: &Action{Type: ActionSendMessage, Target: "telegram", Text: "pill"}}, "owner")
	if _, err := f.svc.SaveOverride(f.ctx, Override{Source: OverrideItem, Ref: daily.ID, Occurrence: f.at(5, 8, 0).UnixMilli(),
		Op: OpMove, To: f.at(5, 9, 30).UnixMilli()}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SaveOverride(f.ctx, Override{Source: OverrideItem, Ref: daily.ID, Occurrence: f.at(6, 8, 0).UnixMilli(),
		Op: OpSkip}, "owner"); err != nil {
		t.Fatal(err)
	}
	f.svc.boot(f.ctx)
	f.tickAt(f.at(5, 8, 1))
	if n := len(f.acts.sent()); n != 0 {
		t.Fatalf("moved occurrence fired at its old time: %v", f.acts.sent())
	}
	f.tickAt(f.at(5, 9, 0))
	if got := f.acts.sent(); len(got) != 1 || got[0] != "web: pay" {
		t.Fatalf("09:00 = %v", got)
	}
	if f.firing(task.ID, f.at(5, 0, 0)).Status != FiringDone {
		t.Fatal("task firing is keyed by its dated occurrence")
	}
	f.tickAt(f.at(5, 9, 30))
	if got := f.acts.sent(); len(got) != 2 || got[1] != "telegram: pill" {
		t.Fatalf("09:30 = %v", got)
	}
	if f.firing(daily.ID, f.at(5, 8, 0)).Status != FiringDone {
		t.Fatal("moved firing keeps the original occurrence")
	}
	for h := 6; h <= 9; h++ {
		f.tickAt(f.at(6, h, 0))
	}
	if n := len(f.acts.sent()); n != 2 {
		t.Fatalf("skipped occurrence fired: %v", f.acts.sent())
	}
	if _, err := f.store.GetFiring(f.ctx, daily.ID, f.at(6, 8, 0).UnixMilli()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("skipped firing = %v", err)
	}
}

// AC-CAL-011: run_workflow for a paused workflow fails with the reason and
// the owner is told.
func TestSchedulerPausedWorkflowACCAL011(t *testing.T) {
	f := newSvc(t)
	due := f.at(5, 10, 0)
	it := f.create(Item{Kind: KindReminder, Title: "Run paused", Start: due.UnixMilli(),
		Action: &Action{Type: ActionRunWorkflow, WorkflowID: "wf_paused"}}, "owner")
	ok := f.create(Item{Kind: KindReminder, Title: "Run daily", Start: due.UnixMilli(),
		Action: &Action{Type: ActionRunWorkflow, WorkflowID: "wf_daily", Input: []byte(`{"topic":"AI"}`)}}, "owner")
	f.svc.boot(f.ctx)
	f.tickAt(due.Add(5 * time.Second))
	fr := f.firing(it.ID, due)
	if fr.Status != FiringFailed || fr.Error != "workflow is paused" {
		t.Fatalf("firing = %+v", fr)
	}
	runs := f.wf.startedRuns()
	if len(runs) != 1 || runs[0].workflowID != "wf_daily" || string(runs[0].input) != `{"topic":"AI"}` {
		t.Fatalf("runs = %+v", runs)
	}
	if fo := f.firing(ok.ID, due); fo.Status != FiringDone || fo.RunWorkflow != "wf_daily" || fo.RunNumber != 1 {
		t.Fatalf("ok firing = %+v", fo)
	}
	notes := f.notes.all()
	if len(notes) != 1 || !strings.Contains(notes[0], "Run paused") || !strings.Contains(notes[0], "workflow is paused") {
		t.Fatalf("notices = %v", notes)
	}
}

// Missing dependencies fail the firing with a clear error.
func TestSchedulerMissingDependencies(t *testing.T) {
	f := newSvc(t)
	f.svc.SetActions(nil)
	f.svc.SetWorkflows(nil)
	due := f.at(5, 10, 0)
	a := f.create(Item{Kind: KindReminder, Title: "A", Start: due.UnixMilli(),
		Action: &Action{Type: ActionSendMessage, Target: "web", Text: "x"}}, "owner")
	b := f.create(Item{Kind: KindReminder, Title: "B", Start: due.UnixMilli(),
		Action: &Action{Type: ActionRunWorkflow, WorkflowID: "wf_daily"}}, "owner")
	f.svc.boot(f.ctx)
	f.tickAt(due)
	if fr := f.firing(a.ID, due); fr.Status != FiringFailed || fr.Error != "messages are not available" {
		t.Fatalf("a = %+v", fr)
	}
	if fr := f.firing(b.ID, due); fr.Status != FiringFailed || fr.Error != "workflows are not available" {
		t.Fatalf("b = %+v", fr)
	}
}

// AC-CAL-012: a firing left running when the daemon stopped is failed at
// start and never started again.
func TestSchedulerRunningAtStartACCAL012(t *testing.T) {
	f := newSvc(t)
	due := f.at(5, 10, 0)
	it := f.create(Item{Kind: KindReminder, Title: "Laundry", Start: due.UnixMilli(),
		Action: &Action{Type: ActionSendMessage, Target: "telegram", Text: "go"}}, "owner")
	_, _ = f.store.InsertFiring(f.ctx, Firing{ItemID: it.ID, Occurrence: due.UnixMilli(), Status: FiringRunning, StartedAt: due.UnixMilli()})
	f.setLastTick(due.Add(-time.Minute))
	f.clock.Set(due.Add(time.Minute))
	f.svc.boot(f.ctx)
	fr := f.firing(it.ID, due)
	if fr.Status != FiringFailed || fr.Error != "interrupted by restart" || fr.EndedAt != due.Add(time.Minute).UnixMilli() {
		t.Fatalf("firing = %+v", fr)
	}
	f.tickAt(due.Add(time.Minute))
	f.tickAt(due.Add(2 * time.Minute))
	if n := len(f.acts.sent()); n != 0 {
		t.Fatalf("action rerun: %v", f.acts.sent())
	}
	if n := len(f.notes.all()); n != 0 {
		t.Fatalf("notices = %v", f.notes.all())
	}
}

// AC-CAL-013: daemon off 09:00–11:00, an action due at 10:00 is missed, no
// action runs, one notice lists it.
func TestSchedulerMissedACCAL013(t *testing.T) {
	f := newSvc(t)
	due := f.at(5, 10, 0)
	it := f.create(Item{Kind: KindReminder, Title: "Laundry", Start: due.UnixMilli(),
		Action: &Action{Type: ActionSendMessage, Target: "telegram", Text: "go"}}, "owner")
	// Due 10:58 at an 11:00 start: within the grace, so it runs.
	soon := f.create(Item{Kind: KindReminder, Title: "Soon", Start: f.at(5, 10, 58).UnixMilli(),
		Action: &Action{Type: ActionSendMessage, Target: "web", Text: "soon"}}, "owner")
	f.setLastTick(f.at(5, 9, 0))
	f.clock.Set(f.at(5, 11, 0))
	if err := f.svc.Start(f.ctx); err != nil {
		t.Fatal(err)
	}
	if fr := f.firing(it.ID, due); fr.Status != FiringMissed {
		t.Fatalf("firing = %+v", fr)
	}
	f.tickAt(f.at(5, 11, 0))
	f.tickAt(f.at(5, 11, 1))
	if got := f.acts.sent(); len(got) != 1 || got[0] != "web: soon" {
		t.Fatalf("sent = %v", got)
	}
	if f.firing(soon.ID, f.at(5, 10, 58)).Status != FiringDone {
		t.Fatal("in-grace action did not run")
	}
	notes := f.notes.all()
	if len(notes) != 1 || !strings.Contains(notes[0], "1 calendar action was missed") || !strings.Contains(notes[0], "• Laundry (Thu 5 Nov 2026 10:00)") {
		t.Fatalf("notices = %q", notes)
	}
	if res := f.entries(f.at(5, 0, 0), f.at(6, 0, 0)); res.Missed != 1 {
		t.Fatalf("missed = %d", res.Missed)
	}
}

func TestSchedulerMissedNoticeCap(t *testing.T) {
	f := newSvc(t)
	for i := 0; i < 12; i++ {
		f.create(Item{Kind: KindReminder, Title: "R" + strconv.Itoa(i), Start: f.at(5, 10, i).UnixMilli(),
			Action: &Action{Type: ActionSendMessage, Target: "web", Text: "x"}}, "owner")
	}
	f.setLastTick(f.at(5, 9, 0))
	f.clock.Set(f.at(5, 12, 0))
	f.svc.boot(f.ctx)
	f.tickAt(f.at(5, 12, 0))
	notes := f.notes.all()
	if len(notes) != 1 || !strings.Contains(notes[0], "12 calendar actions were missed") || !strings.Contains(notes[0], "and 2 more") ||
		!strings.Contains(notes[0], "• R9 ") || strings.Contains(notes[0], "• R10 ") {
		t.Fatalf("notice = %q", notes)
	}
	if n := len(f.acts.sent()); n != 0 {
		t.Fatalf("sent = %d", n)
	}
}

// AC-CAL-014: Run now on a missed firing runs the action once.
func TestSchedulerRunMissedACCAL014(t *testing.T) {
	f := newSvc(t)
	due := f.at(5, 10, 0)
	it := f.create(Item{Kind: KindReminder, Title: "Laundry", Start: due.UnixMilli(),
		Action: &Action{Type: ActionSendMessage, Target: "telegram", Text: "go"}}, "owner")
	f.setLastTick(f.at(5, 9, 0))
	f.clock.Set(f.at(5, 11, 0))
	f.svc.boot(f.ctx)

	_, err := f.svc.RunFiring(f.ctx, it.ID, due.UnixMilli()+1, "owner")
	wantCode(t, err, "not_found")

	block := make(chan struct{})
	f.acts.mu.Lock()
	f.acts.block = block
	f.acts.mu.Unlock()
	fr, err := f.svc.RunFiring(f.ctx, it.ID, due.UnixMilli(), "owner")
	if err != nil || fr.Status != FiringRunning || fr.StartedAt != f.at(5, 11, 0).UnixMilli() {
		t.Fatalf("run = %+v, %v", fr, err)
	}
	_, err = f.svc.RunFiring(f.ctx, it.ID, due.UnixMilli(), "owner")
	wantCode(t, err, "already_running")
	_, err = f.svc.DismissFiring(f.ctx, it.ID, due.UnixMilli(), "owner")
	wantCode(t, err, "already_running")
	close(block)
	f.svc.runWG.Wait()
	if got := f.firing(it.ID, due); got.Status != FiringDone {
		t.Fatalf("after run = %+v", got)
	}
	_, err = f.svc.RunFiring(f.ctx, it.ID, due.UnixMilli(), "owner")
	wantCode(t, err, "already_done")
	if got := f.acts.sent(); len(got) != 1 {
		t.Fatalf("sent = %v", got)
	}
	hist, _ := f.store.ListHistory(f.ctx, it.ID, 1)
	if len(hist) != 1 || !strings.HasPrefix(hist[0].Summary, "Ran the Thu 5 Nov 2026 10:00 action") {
		t.Fatalf("history = %+v", hist)
	}
}

func TestSchedulerDismissAndRerun(t *testing.T) {
	f := newSvc(t)
	due := f.at(5, 10, 0)
	it := f.create(Item{Kind: KindReminder, Title: "Laundry", Start: due.UnixMilli(),
		Action: &Action{Type: ActionSendMessage, Target: "telegram", Text: "go"}}, "owner")
	_, _ = f.store.InsertFiring(f.ctx, Firing{ItemID: it.ID, Occurrence: due.UnixMilli(), Status: FiringMissed})
	fr, err := f.svc.DismissFiring(f.ctx, it.ID, due.UnixMilli(), "owner")
	if err != nil || fr.Status != FiringDismissed {
		t.Fatalf("dismiss = %+v, %v", fr, err)
	}
	if fr, err := f.svc.DismissFiring(f.ctx, it.ID, due.UnixMilli(), "owner"); err != nil || fr.Status != FiringDismissed {
		t.Fatalf("dismiss again = %+v, %v", fr, err)
	}
	if _, err := f.svc.RunFiring(f.ctx, it.ID, due.UnixMilli(), "owner"); err != nil {
		t.Fatalf("run dismissed: %v", err)
	}
	f.svc.runWG.Wait()
	_, err = f.svc.DismissFiring(f.ctx, it.ID, due.UnixMilli(), "owner")
	wantCode(t, err, "already_done")
}

// AC-CAL-019: the daily agenda is sent once per day, also across a restart
// between 07:00 and 07:05.
func TestSchedulerAgendaACCAL019(t *testing.T) {
	f := newSvc(t)
	if _, err := f.svc.PutSettings(f.ctx, Settings{Timezone: "Asia/Jakarta", WeekStart: 1,
		Agenda: AgendaSettings{Enabled: true, Time: "07:00"}}); err != nil {
		t.Fatal(err)
	}
	f.create(Item{Kind: KindReminder, Title: "Laundry", Start: f.at(5, 10, 0).UnixMilli()}, "owner")
	f.clock.Set(f.at(5, 6, 59))
	f.svc.boot(f.ctx)
	f.tickAt(f.at(5, 6, 59))
	if n := len(f.notes.all()); n != 0 {
		t.Fatalf("agenda before time: %v", f.notes.all())
	}
	f.tickAt(f.at(5, 7, 0).Add(15 * time.Second))
	f.tickAt(f.at(5, 7, 1))
	notes := f.notes.all()
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "📅 Thu 5 Nov") || !strings.Contains(notes[0], "10:00 ⏰ Laundry") {
		t.Fatalf("agenda = %q", notes)
	}

	// Restart at 07:03 the same day: not sent again.
	f.svc.Close()
	f.svc = f.newService()
	f.clock.Set(f.at(5, 7, 3))
	f.svc.boot(f.ctx)
	f.tickAt(f.at(5, 7, 3))
	if n := len(f.notes.all()); n != 1 {
		t.Fatalf("agenda after restart = %d", n)
	}
	// The next day the daemon was down at 07:00 and starts at 07:04: sent.
	f.svc.Close()
	f.svc = f.newService()
	f.clock.Set(f.at(6, 7, 4))
	f.svc.boot(f.ctx)
	f.tickAt(f.at(6, 7, 4))
	if notes := f.notes.all(); len(notes) != 2 || !strings.HasPrefix(notes[1], "📅 Fri 6 Nov") {
		t.Fatalf("agenda next day = %q", notes)
	}
	// Starting after 07:05 skips the day.
	f.tickAt(f.at(7, 7, 6))
	if n := len(f.notes.all()); n != 2 {
		t.Fatalf("agenda after window = %d", n)
	}
	raw, _, _ := f.store.GetKV(f.ctx, "agenda.sent_on")
	if raw != "2026-11-06" {
		t.Fatalf("sent_on = %q", raw)
	}
}

func TestSchedulerFirstStartAndStartClose(t *testing.T) {
	f := newSvc(t)
	f.create(Item{Kind: KindReminder, Title: "Past", Start: f.at(5, 5, 0).UnixMilli(),
		Action: &Action{Type: ActionSendMessage, Target: "web", Text: "x"}}, "owner")
	f.clock.Set(f.at(5, 6, 0))
	s := f.newService()
	ch, _ := s.Subscribe()
	if err := s.Start(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(f.ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok, _ := f.store.GetKV(f.ctx, "last_tick"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("loop never ticked")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Close()
	for range ch {
	}
	// No last_tick before the first start: nothing is missed.
	fs, _ := f.store.ListFirings(f.ctx, FiringFilter{})
	if len(fs) != 0 || len(f.notes.all()) != 0 {
		t.Fatalf("first start firings = %+v notices = %v", fs, f.notes.all())
	}
}
