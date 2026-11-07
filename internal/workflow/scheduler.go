package workflow

// This file holds the scheduler and durable-resume loop (design section
// "Service and scheduler", REQ-WF-003, REQ-WF-007, REQ-WF-008). The loop ticks
// every tickInterval; each tick fires schedule and interval triggers, resumes
// due wait nodes and expires old decisions. Errors are logged and never stop
// the loop.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	// Embed the IANA time zone database so schedule triggers work even when the
	// host has no zoneinfo files (REQ-WF-003).
	_ "time/tzdata"
)

// Start resumes durable runs and starts the scheduler goroutine; it returns at
// once. A second Start is a no-op (design section "Durable resume").
func (s *Service) Start(ctx context.Context) error {
	s.ctxMu.Lock()
	if s.started {
		s.ctxMu.Unlock()
		return nil
	}
	s.started = true
	s.rootCancel()
	s.rootCtx, s.rootCancel = context.WithCancel(ctx)
	root := s.rootCtx
	s.ctxMu.Unlock()

	// Runs launched from a request (Answer, StartRun) must outlive that
	// request: the engine derives each run's context from this root.
	s.engine.SetRoot(root)

	now := s.now()
	s.seedLastFired(root, now)

	resume, err := s.store.ListRuns(root, RunFilter{Statuses: []RunStatus{RunRunning, RunQueued}})
	if err != nil {
		s.logger.Error("workflow: resume runs", "error", err)
	} else {
		for _, r := range resume {
			s.engine.Launch(root, r.ID)
		}
	}

	s.wg.Add(1)
	go s.loop(root)
	return nil
}

// Close cancels the service context, joins the scheduler and every run
// goroutine, then closes the subscriber channels.
func (s *Service) Close() {
	s.ctxMu.Lock()
	cancel := s.rootCancel
	s.ctxMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	s.engine.CancelAll()
	s.engine.Wait()

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
	s.tick(s.now())
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(s.now())
		}
	}
}

// seedLastFired marks the current minute for every enabled schedule trigger, so
// a run whose cron matches the start minute does not fire on the first tick.
func (s *Service) seedLastFired(ctx context.Context, now time.Time) {
	wfs, err := s.store.ListWorkflows(ctx)
	if err != nil {
		return
	}
	for _, wf := range wfs {
		if wf.Status != StatusActive {
			continue
		}
		ver, err := s.store.GetVersion(ctx, wf.ID, wf.Version)
		if err != nil {
			continue
		}
		for _, n := range ver.Graph.Nodes {
			if n.Type != "trigger.schedule" || !triggerEnabled(n) {
				continue
			}
			loc := scheduleLocation(n)
			s.lastFiredMu.Lock()
			s.lastFired[scheduleKey(wf.ID, n.ID)] = minuteKey(now.In(loc))
			s.lastFiredMu.Unlock()
		}
	}
}

// tick runs one scheduler pass (REQ-WF-003, REQ-WF-007, REQ-WF-008).
func (s *Service) tick(now time.Time) {
	ctx := s.rootContext()
	wfs, err := s.store.ListWorkflows(ctx)
	if err != nil {
		s.logger.Error("workflow: scheduler list", "error", err)
		return
	}
	for _, wf := range wfs {
		if wf.Status != StatusActive {
			continue
		}
		ver, err := s.store.GetVersion(ctx, wf.ID, wf.Version)
		if err != nil {
			continue
		}
		s.tickSchedule(ctx, wf, ver.Graph, now)
		s.tickIntervals(ctx, wf, ver.Graph, now)
	}
	s.resumeWaits(ctx, now)
	s.expireDecisions(ctx, now)
}

// tickSchedule fires every enabled schedule trigger whose cron matches now in
// its zone, at most once per minute (AC-WF-003).
func (s *Service) tickSchedule(ctx context.Context, wf Workflow, g Graph, now time.Time) {
	for _, n := range g.Nodes {
		if n.Type != "trigger.schedule" || !triggerEnabled(n) {
			continue
		}
		expr, _ := n.Params["cron"].(string)
		cron, err := ParseCron(expr)
		if err != nil {
			s.logger.Error("workflow: bad cron", "workflow", wf.ID, "node", n.ID, "error", err)
			continue
		}
		loc := scheduleLocation(n)
		local := now.In(loc)
		if !cron.Matches(local) {
			continue
		}
		key := scheduleKey(wf.ID, n.ID)
		minute := minuteKey(local)

		s.lastFiredMu.Lock()
		if last, ok := s.lastFired[key]; ok && last == minute {
			s.lastFiredMu.Unlock()
			continue
		}
		s.lastFired[key] = minute
		s.lastFiredMu.Unlock()

		// A calendar override may suppress this one fire (REQ-CAL-004); the
		// minute stays recorded so it is not retried.
		if !s.allowSlot(ctx, wf.ID, n.ID, local.Truncate(time.Minute)) {
			continue
		}

		input, _ := json.Marshal(map[string]any{"fired_at": now.UnixMilli()})
		s.startIgnoringActive(ctx, wf.ID, "schedule", input)
	}
}

// tickIntervals fires an interval trigger when it has no interval run or its
// newest one started at least N minutes ago (REQ-WF-003). A slot the filter
// suppressed counts as the newest fire.
func (s *Service) tickIntervals(ctx context.Context, wf Workflow, g Graph, now time.Time) {
	var runs []Run
	loaded := false
	for _, n := range g.Nodes {
		if n.Type != "trigger.interval" || !triggerEnabled(n) {
			continue
		}
		minutes := int(paramFloat(n.Params, "minutes", 0))
		if minutes < 1 {
			continue
		}
		if !loaded {
			runs, _ = s.store.ListRuns(ctx, RunFilter{WorkflowID: wf.ID, Limit: 20})
			loaded = true
		}
		every := time.Duration(minutes) * time.Minute
		last, ok := s.intervalLast(wf.ID, n.ID, runs)
		if ok && last.After(now.Add(-every)) {
			continue
		}
		// The planned fire time is the last fire plus N minutes, or now for a
		// first fire. A suppressed slot counts as a fire, so the cadence
		// continues from it instead of retrying every tick (REQ-CAL-004).
		at := now.Truncate(time.Minute)
		if ok {
			at = last.Add(every).Truncate(time.Minute)
		}
		if !s.allowSlot(ctx, wf.ID, n.ID, at) {
			s.consumeIntervalSlot(wf.ID, n.ID, at)
			continue
		}
		input, _ := json.Marshal(map[string]any{"fired_at": now.UnixMilli()})
		s.startIgnoringActive(ctx, wf.ID, "interval", input)
	}
}

// resumeWaits relaunches waiting runs whose timer is due (REQ-WF-007).
func (s *Service) resumeWaits(ctx context.Context, now time.Time) {
	runs, err := s.store.ListRuns(ctx, RunFilter{Statuses: []RunStatus{RunWaiting}})
	if err != nil {
		return
	}
	for _, r := range runs {
		if r.WaitingReason != "wait" || r.WakeAt > now.UnixMilli() {
			continue
		}
		r.Status = RunRunning
		r.WaitingReason = ""
		r.WakeAt = 0
		if err := s.store.UpdateRun(ctx, r); err != nil {
			continue
		}
		s.broadcast(r.WorkflowID, r.ID)
		s.engine.Launch(s.rootContext(), r.ID)
	}
}

// expireDecisions answers pending decisions past their expiry and marks them
// expired, keeping the choice Answer recorded (REQ-WF-008): an approval is
// denied and a guardian ask stops.
func (s *Service) expireDecisions(ctx context.Context, now time.Time) {
	decs, err := s.store.ListDecisions(ctx, DecisionFilter{Status: "pending"})
	if err != nil {
		return
	}
	for _, d := range decs {
		if d.ExpiresAt == 0 || d.ExpiresAt > now.UnixMilli() {
			continue
		}
		choice := "stop"
		if d.Kind == "approval" {
			choice = "deny"
		}
		_, _ = s.engine.Answer(ctx, d.ID, choice)

		current, err := s.store.GetDecision(ctx, d.ID)
		if err != nil || current.Status != "resolved" {
			continue
		}
		current.Status = "expired"
		if err := s.store.PutDecision(ctx, current); err != nil {
			continue
		}
		s.broadcast(current.WorkflowID, current.RunID)
	}
}

// startIgnoringActive starts a run, dropping the "already running" refusal
// (REQ-WF-003). Every other error is logged.
func (s *Service) startIgnoringActive(ctx context.Context, id, trigger string, input json.RawMessage) {
	if _, err := s.StartRun(ctx, id, trigger, input); err != nil && !errors.Is(err, ErrRunActive) {
		s.logger.Error("workflow: start run", "workflow", id, "trigger", trigger, "error", err)
	}
}

// scheduleKey identifies a schedule trigger's last-fired slot.
func scheduleKey(workflowID, nodeID string) string {
	return workflowID + "\x00" + nodeID
}

// minuteKey maps a time to its minute as a Unix timestamp.
func minuteKey(t time.Time) int64 {
	return t.Truncate(time.Minute).Unix()
}

// scheduleLocation resolves a schedule node's timezone, defaulting to the local
// zone for an empty or unknown name (REQ-WF-003).
func scheduleLocation(n Node) *time.Location {
	name, _ := n.Params["timezone"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return loc
}
