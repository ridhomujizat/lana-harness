package calendar

// This file holds the scheduler loop (design section "Scheduler", "Firing
// states"): due item actions, moved workflow slots, the daily agenda and the
// missed scan at start. Errors are logged and never stop the loop.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Start recovers firings interrupted by a restart, records the actions
// missed while the daemon was off and tells the owner once, then starts the
// scheduler goroutine; it returns at once. A second Start is a no-op.
func (s *Service) Start(ctx context.Context) error {
	s.ctxMu.Lock()
	if s.started || s.closed {
		s.ctxMu.Unlock()
		return nil
	}
	s.started = true
	s.rootCancel()
	s.rootCtx, s.rootCancel = context.WithCancel(ctx)
	root := s.rootCtx
	s.ctxMu.Unlock()

	s.boot(root)

	s.wg.Add(1)
	go s.loop(root)
	return nil
}

// Close stops the scheduler, cancels and joins the running actions, then
// closes the subscriber channels.
func (s *Service) Close() {
	s.ctxMu.Lock()
	s.closed = true
	cancel := s.rootCancel
	s.ctxMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	s.runWG.Wait()

	s.subMu.Lock()
	for id, ch := range s.subs {
		delete(s.subs, id)
		close(ch)
	}
	s.subMu.Unlock()
}

// loop ticks immediately, then every tickInterval until ctx is done.
func (s *Service) loop(ctx context.Context) {
	defer s.wg.Done()
	s.tick()
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

// boot runs the start pass: firings left running become failed and are
// never run again (AC-CAL-012); firings left scheduled (inserted but never
// started) become missed; actions due more than 5 minutes ago in
// (last_tick, now] with no firing become missed. Every missed firing found
// here is listed in one owner message (REQ-CAL-007, AC-CAL-013). Actions due
// within the grace are left to the first tick.
func (s *Service) boot(ctx context.Context) {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	now := s.now()
	missed := s.recoverInterrupted(ctx, now)
	last := s.loadLastTick(ctx, now)
	s.lastTick = last
	if now.After(last) {
		missed = append(missed, s.processDue(ctx, last, now, false)...)
	}
	s.notifyMissed(ctx, missed)
}

// recoverInterrupted fails running firings and marks scheduled ones missed.
// It returns the firings it marked missed.
func (s *Service) recoverInterrupted(ctx context.Context, now time.Time) []missedFiring {
	firings, err := s.store.ListFirings(ctx, FiringFilter{Statuses: []string{FiringRunning, FiringScheduled}})
	if err != nil {
		s.logger.Error("calendar: list interrupted firings", "error", err)
		return nil
	}
	var missed []missedFiring
	for _, f := range firings {
		from := f.Status
		if f.Status == FiringRunning {
			f.Status = FiringFailed
			f.Error = "interrupted by restart"
			f.EndedAt = now.UnixMilli()
		} else {
			f.Status = FiringMissed
		}
		ok, err := s.store.UpdateFiring(ctx, f, from)
		if err != nil {
			s.logger.Error("calendar: recover firing", "item", f.ItemID, "error", err)
			continue
		}
		if !ok {
			continue
		}
		s.broadcast(ChangeFiring, f.ItemID)
		if f.Status == FiringMissed {
			if it, err := s.store.GetItem(ctx, f.ItemID); err == nil {
				missed = append(missed, missedFiring{title: it.Title, due: time.UnixMilli(f.Occurrence)})
			}
		}
	}
	return missed
}

// loadLastTick reads KV "last_tick"; absent or unreadable means now, so a
// first start never reports missed actions.
func (s *Service) loadLastTick(ctx context.Context, now time.Time) time.Time {
	raw, ok, err := s.store.GetKV(ctx, kvLastTick)
	if err != nil {
		s.logger.Error("calendar: read last tick", "error", err)
		return now
	}
	if !ok {
		return now
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms <= 0 {
		return now
	}
	return time.UnixMilli(ms)
}

// tick runs one scheduler pass over (lastTick, now].
func (s *Service) tick() {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	ctx := s.rootContext()
	if ctx.Err() != nil {
		return
	}
	now := s.now()
	last := s.lastTick
	if last.IsZero() {
		last = s.loadLastTick(ctx, now)
	}
	if now.After(last) {
		s.notifyMissed(ctx, s.processDue(ctx, last, now, true))
		s.fireMovedSlots(ctx, last, now)
	}
	s.sendAgenda(ctx, now)
	s.lastTick = now
	if err := s.store.SetKV(ctx, kvLastTick, strconv.FormatInt(now.UnixMilli(), 10)); err != nil {
		s.logger.Error("calendar: save last tick", "error", err)
	}
}

// dueOcc is one item occurrence whose action is due.
type dueOcc struct {
	item Item
	occ  time.Time // original occurrence, the firing key
	due  time.Time
}

// missedFiring is one line of the missed notice.
type missedFiring struct {
	title string
	due   time.Time
}

// dueOccurrences returns the action occurrences due in (from, to], by due
// time. An overridden occurrence is not due at its computed time: a skipped
// one never, a moved one at DueAt of its new time.
func dueOccurrences(items []Item, overrides []Override, loc *time.Location, from, to time.Time) []dueOcc {
	byItem := map[string][]Override{}
	overridden := map[overrideKey]bool{}
	for _, o := range overrides {
		if o.Source != OverrideItem {
			continue
		}
		overridden[overrideKey{o.Source, o.Ref, o.Occurrence}] = true
		if o.Op == OpMove {
			byItem[o.Ref] = append(byItem[o.Ref], o)
		}
	}
	inWindow := func(t time.Time) bool { return t.After(from) && !t.After(to) }
	var out []dueOcc
	for _, it := range items {
		if it.Action == nil || it.DeletedAt != 0 {
			continue
		}
		// Dated occurrences are due at 09:00 of their day: look back one day.
		for _, occ := range Occurrences(it, loc, from.Add(-24*time.Hour), to.Add(time.Millisecond)) {
			if overridden[overrideKey{OverrideItem, it.ID, occ.UnixMilli()}] {
				continue
			}
			if due := DueAt(it, occ, loc); inWindow(due) {
				out = append(out, dueOcc{item: it, occ: occ, due: due})
			}
		}
		if !it.Repeat.Repeats() {
			continue
		}
		for _, o := range byItem[it.ID] {
			occ := time.UnixMilli(o.Occurrence)
			if !IsOccurrence(it, loc, occ) {
				continue
			}
			if due := DueAt(it, time.UnixMilli(o.To), loc); inWindow(due) {
				out = append(out, dueOcc{item: it, occ: occ, due: due})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].due.Before(out[j].due) })
	return out
}

// processDue handles the actions due in (from, now]. One first seen more
// than 5 minutes late becomes missed (REQ-CAL-007); with run set, one seen
// in time is inserted scheduled, moved to running by compare-and-set and
// started (REQ-CAL-006). The unique firing key makes every occurrence fire at
// most once. It returns the firings it marked missed.
func (s *Service) processDue(ctx context.Context, from, now time.Time, run bool) []missedFiring {
	if now.Sub(from) > maxScanBack {
		from = now.Add(-maxScanBack)
	}
	items, err := s.store.ListItems(ctx)
	if err != nil {
		s.logger.Error("calendar: scheduler list items", "error", err)
		return nil
	}
	overrides, err := s.store.ListOverrides(ctx)
	if err != nil {
		s.logger.Error("calendar: scheduler list overrides", "error", err)
		return nil
	}
	var missed []missedFiring
	for _, d := range dueOccurrences(items, overrides, s.location(ctx), from, now) {
		f := Firing{ItemID: d.item.ID, Occurrence: d.occ.UnixMilli()}
		if now.Sub(d.due) > missedGrace {
			f.Status = FiringMissed
			ok, err := s.store.InsertFiring(ctx, f)
			if err != nil {
				s.logger.Error("calendar: insert missed firing", "item", f.ItemID, "error", err)
				continue
			}
			if ok {
				missed = append(missed, missedFiring{title: d.item.Title, due: d.due})
				s.broadcast(ChangeFiring, f.ItemID)
			}
			continue
		}
		if !run {
			continue
		}
		f.Status = FiringScheduled
		ok, err := s.store.InsertFiring(ctx, f)
		if err != nil {
			s.logger.Error("calendar: insert firing", "item", f.ItemID, "error", err)
			continue
		}
		if !ok {
			continue
		}
		f.Status = FiringRunning
		f.StartedAt = now.UnixMilli()
		ok, err = s.store.UpdateFiring(ctx, f, FiringScheduled)
		if err != nil {
			s.logger.Error("calendar: start firing", "item", f.ItemID, "error", err)
			continue
		}
		if !ok {
			continue
		}
		s.broadcast(ChangeFiring, f.ItemID)
		s.launch(d.item, f)
	}
	return missed
}

// notifyMissed sends one owner message listing up to 10 missed firings
// (REQ-CAL-007, API-CAL-017).
func (s *Service) notifyMissed(ctx context.Context, missed []missedFiring) {
	if len(missed) == 0 {
		return
	}
	loc := s.location(ctx)
	noun := "actions were"
	if len(missed) == 1 {
		noun = "action was"
	}
	lines := []string{fmt.Sprintf("⚠ %d calendar %s missed while lana could not run them:", len(missed), noun)}
	for i, m := range missed {
		if i == maxMissedNames {
			lines = append(lines, fmt.Sprintf("and %d more", len(missed)-maxMissedNames))
			break
		}
		lines = append(lines, "• "+m.title+" ("+fmtWhen(m.due, loc)+")")
	}
	lines = append(lines, "Open the calendar to run or dismiss them.")
	s.notifyOwner(ctx, strings.Join(lines, "\n"))
}

// launch runs the action of a running firing in a goroutine bounded by the
// root context. After Close the firing fails at once.
func (s *Service) launch(it Item, f Firing) {
	s.ctxMu.Lock()
	if s.closed {
		s.ctxMu.Unlock()
		s.finish(context.Background(), it, f, errors.New("calendar service stopped"))
		return
	}
	ctx := s.rootCtx
	s.runWG.Add(1)
	s.ctxMu.Unlock()
	go func() {
		defer s.runWG.Done()
		err := s.runAction(ctx, it, &f)
		s.finish(ctx, it, f, err)
	}()
}

// runAction runs one item action (REQ-CAL-006) and records the session or
// run it started on f.
func (s *Service) runAction(ctx context.Context, it Item, f *Firing) error {
	a := it.Action
	if a == nil {
		return errors.New("item has no action")
	}
	switch a.Type {
	case ActionAgentPrompt:
		acts := s.actionRunner()
		if acts == nil {
			return errors.New("agent actions are not available")
		}
		if !acts.AgentExists(a.Agent) {
			return fmt.Errorf("agent %q does not exist", a.Agent)
		}
		sid, err := acts.AgentPrompt(ctx, a.Agent, a.Prompt, it.Title)
		f.SessionID = sid
		return err
	case ActionRunWorkflow:
		ws := s.workflowSource()
		if ws == nil {
			return errors.New("workflows are not available")
		}
		_, status, ok := ws.Workflow(ctx, a.WorkflowID)
		if !ok {
			return fmt.Errorf("workflow %q does not exist", a.WorkflowID)
		}
		if status != workflowActive {
			return fmt.Errorf("workflow is %s", status)
		}
		_, number, err := ws.StartRun(ctx, a.WorkflowID, a.Input)
		if err != nil {
			return err
		}
		f.RunWorkflow, f.RunNumber = a.WorkflowID, number
		return nil
	case ActionSendMessage:
		acts := s.actionRunner()
		if acts == nil {
			return errors.New("messages are not available")
		}
		return acts.SendMessage(ctx, a.Target, a.Text)
	}
	return fmt.Errorf("unknown action type %q", a.Type)
}

// finish stores the outcome of a running firing (running → done | failed)
// and tells the owner about a failure (AC-CAL-011). The writes outlive a
// cancelled root context.
func (s *Service) finish(ctx context.Context, it Item, f Firing, runErr error) {
	ctx = context.WithoutCancel(ctx)
	f.EndedAt = s.now().UnixMilli()
	if runErr != nil {
		f.Status = FiringFailed
		f.Error = runErr.Error()
	} else {
		f.Status = FiringDone
		f.Error = ""
	}
	ok, err := s.store.UpdateFiring(ctx, f, FiringRunning)
	switch {
	case err != nil:
		s.logger.Error("calendar: finish firing", "item", f.ItemID, "error", err)
	case !ok:
		s.logger.Warn("calendar: firing changed while running", "item", f.ItemID, "occurrence", f.Occurrence)
	}
	s.broadcast(ChangeFiring, f.ItemID)
	if runErr != nil {
		s.logger.Warn("calendar: action failed", "item", f.ItemID, "error", runErr)
		when := fmtWhen(time.UnixMilli(f.Occurrence), s.location(ctx))
		s.notifyOwner(ctx, fmt.Sprintf("⚠ Calendar action failed: %s (%s): %s", it.Title, when, runErr))
	}
}

// fireMovedSlots starts each moved workflow slot whose new time is in
// (from, now], once (AC-CAL-006): the override gets fired_at before the run
// starts. A slot first seen more than 5 minutes late, or of a workflow that
// is not active, does not run.
func (s *Service) fireMovedSlots(ctx context.Context, from, now time.Time) {
	ws := s.workflowSource()
	if ws == nil {
		return
	}
	overrides, err := s.store.ListOverrides(ctx)
	if err != nil {
		s.logger.Error("calendar: scheduler list overrides", "error", err)
		return
	}
	for _, o := range overrides {
		if o.Source != OverrideWorkflow || o.Op != OpMove || o.FiredAt != 0 {
			continue
		}
		at := time.UnixMilli(o.To)
		if !at.After(from) || at.After(now) {
			continue
		}
		if now.Sub(at) > missedGrace {
			s.logger.Warn("calendar: moved workflow slot missed", "ref", o.Ref, "to", o.To)
			continue
		}
		wfID, _, ok := splitRef(o.Ref)
		if !ok {
			continue
		}
		if _, status, ok := ws.Workflow(ctx, wfID); !ok || status != workflowActive {
			continue
		}
		o.FiredAt = now.UnixMilli()
		if err := s.store.PutOverride(ctx, o); err != nil {
			s.logger.Error("calendar: mark moved slot fired", "ref", o.Ref, "error", err)
			continue
		}
		input, _ := json.Marshal(map[string]int64{"fired_at": now.UnixMilli(), "occurrence": o.Occurrence})
		if _, _, err := ws.StartRun(ctx, wfID, input); err != nil {
			s.logger.Error("calendar: start moved slot", "ref", o.Ref, "error", err)
			continue
		}
		s.broadcast(ChangeRun, "")
	}
}

// sendAgenda sends the daily agenda once per local day, when enabled and
// now is within 5 minutes after agenda.time (AC-CAL-019). KV
// "agenda.sent_on" holds the last day sent, so a restart in the window does
// not send it twice.
func (s *Service) sendAgenda(ctx context.Context, now time.Time) {
	st, loc := s.settingsLoc(ctx)
	notifier := s.ownerNotifier()
	if !st.Agenda.Enabled || notifier == nil {
		return
	}
	h, m, ok := parseClock(st.Agenda.Time)
	if !ok {
		return
	}
	today := dayOf(now, loc)
	at := today.at(loc, h, m, 0)
	if now.Before(at) || now.Sub(at) > agendaWindow {
		return
	}
	sent, _, err := s.store.GetKV(ctx, kvAgendaSentOn)
	if err != nil {
		s.logger.Error("calendar: read agenda day", "error", err)
		return
	}
	if sent == today.String() {
		return
	}
	text, err := s.Agenda(ctx, now)
	if err != nil {
		s.logger.Error("calendar: build agenda", "error", err)
		return
	}
	if err := notifier.NotifyOwner(ctx, text); err != nil {
		s.logger.Error("calendar: send agenda", "error", err)
		return
	}
	if err := s.store.SetKV(ctx, kvAgendaSentOn, today.String()); err != nil {
		s.logger.Error("calendar: save agenda day", "error", err)
	}
}
