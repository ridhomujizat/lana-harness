package workflow

// This file holds the calendar hooks of the workflow scheduler (spec #35,
// design section "Workflow hook", REQ-CAL-004, REQ-CAL-008): a slot filter that
// can suppress one scheduled or interval fire without changing the trigger,
// and the list of upcoming fire times ("slots") for a time range.

import (
	"context"
	"sort"
	"time"
)

// Slot limits.
const (
	// maxSlotsPerNode caps the slots one trigger node contributes to a range.
	maxSlotsPerNode = 1000
	// collapseIntervalMinutes is the interval below which a trigger shows one
	// all-day slot per day instead of one slot per fire (REQ-CAL-008).
	collapseIntervalMinutes = 60
)

// SlotFilter decides whether the scheduler may fire a trigger slot. at is the
// planned fire minute: the matched cron minute for a schedule trigger, or the
// planned interval time. Returning false suppresses that one fire; the
// trigger itself does not change (REQ-CAL-004).
type SlotFilter func(ctx context.Context, workflowID, nodeID string, at time.Time) bool

// Slot is one upcoming fire time of a schedule or interval trigger
// (REQ-CAL-008). A Collapsed slot stands for every fire of a short interval
// trigger on one local day; its At is that day's local midnight.
type Slot struct {
	WorkflowID   string
	Name         string
	Guardian     string
	NodeID       string
	Trigger      string // schedule|interval
	At           time.Time
	EveryMinutes int
	Collapsed    bool
}

// SetSlotFilter installs the slot filter; nil allows every fire. It may be
// called after Start.
func (s *Service) SetSlotFilter(f SlotFilter) {
	s.slotMu.Lock()
	s.slotFilter = f
	s.slotMu.Unlock()
}

// allowSlot asks the slot filter whether a fire may start.
func (s *Service) allowSlot(ctx context.Context, workflowID, nodeID string, at time.Time) bool {
	s.slotMu.RLock()
	f := s.slotFilter
	s.slotMu.RUnlock()
	if f == nil {
		return true
	}
	return f(ctx, workflowID, nodeID, at)
}

// consumeIntervalSlot records a suppressed interval fire, so the trigger's
// cadence continues from that slot instead of retrying it every tick.
func (s *Service) consumeIntervalSlot(workflowID, nodeID string, at time.Time) {
	s.lastFiredMu.Lock()
	s.intervalConsumed[scheduleKey(workflowID, nodeID)] = at
	s.lastFiredMu.Unlock()
}

// intervalLast returns the time an interval trigger last fired: the newer of
// its newest interval run start and its last suppressed slot. ok is false when
// it has neither. runs must be newest first.
func (s *Service) intervalLast(workflowID, nodeID string, runs []Run) (time.Time, bool) {
	var last time.Time
	ok := false
	for i := range runs {
		if runs[i].Trigger == "interval" {
			last = time.UnixMilli(runs[i].StartedAt)
			ok = true
			break
		}
	}
	s.lastFiredMu.Lock()
	consumed, has := s.intervalConsumed[scheduleKey(workflowID, nodeID)]
	s.lastFiredMu.Unlock()
	if has && (!ok || consumed.After(last)) {
		return consumed, true
	}
	return last, ok
}

// Slots lists the upcoming fire times of every active workflow's enabled
// schedule and interval triggers in [max(from, now), to), sorted by time,
// workflow id and node id (REQ-CAL-008). Past fires are not slots: the
// calendar shows their runs instead.
func (s *Service) Slots(ctx context.Context, from, to time.Time) ([]Slot, error) {
	now := s.now()
	start := from
	if start.Before(now) {
		start = now
	}
	if !start.Before(to) {
		return []Slot{}, nil
	}
	wfs, err := s.store.ListWorkflows(ctx)
	if err != nil {
		return nil, err
	}
	out := []Slot{}
	for _, wf := range wfs {
		if wf.Status != StatusActive {
			continue
		}
		ver, err := s.store.GetVersion(ctx, wf.ID, wf.Version)
		if err != nil {
			return nil, err
		}
		var runs []Run
		loaded := false
		for _, n := range ver.Graph.Nodes {
			if !triggerEnabled(n) {
				continue
			}
			base := Slot{WorkflowID: wf.ID, Name: wf.Name, Guardian: wf.Guardian, NodeID: n.ID}
			switch n.Type {
			case "trigger.schedule":
				expr, _ := n.Params["cron"].(string)
				cron, err := ParseCron(expr)
				if err != nil {
					continue
				}
				base.Trigger = "schedule"
				for _, at := range scheduleTimes(cron, scheduleLocation(n), start, to) {
					slot := base
					slot.At = at
					out = append(out, slot)
				}
			case "trigger.interval":
				minutes := int(paramFloat(n.Params, "minutes", 0))
				if minutes < 1 {
					continue
				}
				base.Trigger = "interval"
				base.EveryMinutes = minutes
				if minutes < collapseIntervalMinutes {
					for _, day := range localDays(start, to) {
						slot := base
						slot.At = day
						slot.Collapsed = true
						out = append(out, slot)
					}
					continue
				}
				if !loaded {
					runs, err = s.store.ListRuns(ctx, RunFilter{WorkflowID: wf.ID, Limit: 20})
					if err != nil {
						return nil, err
					}
					loaded = true
				}
				first := now
				if last, ok := s.intervalLast(wf.ID, n.ID, runs); ok {
					first = last.Add(time.Duration(minutes) * time.Minute)
				}
				for _, at := range intervalTimes(first, time.Duration(minutes)*time.Minute, start, to) {
					slot := base
					slot.At = at
					out = append(out, slot)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		if out[i].WorkflowID != out[j].WorkflowID {
			return out[i].WorkflowID < out[j].WorkflowID
		}
		return out[i].NodeID < out[j].NodeID
	})
	return out, nil
}

// scheduleTimes returns the minutes in [start, to) whose time in loc matches
// cron, the same test tickSchedule applies, capped at maxSlotsPerNode.
func scheduleTimes(cron Cron, loc *time.Location, start, to time.Time) []time.Time {
	var out []time.Time
	t := start.Truncate(time.Minute)
	if t.Before(start) {
		t = t.Add(time.Minute)
	}
	for ; t.Before(to) && len(out) < maxSlotsPerNode; t = t.Add(time.Minute) {
		local := t.In(loc)
		if cron.Matches(local) {
			out = append(out, local)
		}
	}
	return out
}

// intervalTimes returns first + k*every for k >= 0 inside [start, to), each
// truncated to the minute the scheduler passes to the slot filter, capped at
// maxSlotsPerNode.
func intervalTimes(first time.Time, every time.Duration, start, to time.Time) []time.Time {
	t := first
	if t.Before(start) {
		steps := (start.Sub(t) + every - 1) / every
		t = t.Add(steps * every)
	}
	var out []time.Time
	for ; t.Before(to) && len(out) < maxSlotsPerNode; t = t.Add(every) {
		out = append(out, t.Truncate(time.Minute))
	}
	return out
}

// localDays returns the local midnight of every day from start's day up to
// to, capped at maxSlotsPerNode.
func localDays(start, to time.Time) []time.Time {
	s := start.In(time.Local)
	day := time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, time.Local)
	var out []time.Time
	for day.Before(to) && len(out) < maxSlotsPerNode {
		out = append(out, day)
		day = time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, time.Local)
	}
	return out
}
