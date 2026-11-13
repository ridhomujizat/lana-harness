package knowledge

import (
	"context"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Background-gate tuning (REQ-KBG-020–023, REQ-KBG-030, REQ-KBG-040). These are
// package variables, not constants, so a test can shorten them.
var (
	// quietWindow is how long the owner must be idle before a background pass
	// may run.
	quietWindow = 2 * time.Minute
	// busyStale is how long a busy mark with no event counts as a lost turn/end.
	busyStale = 30 * time.Minute
	// journalCooldown is how long a session waits after a background journal
	// pass before it is eligible again.
	journalCooldown = 30 * time.Minute
	// journalMaxFailures stops retrying a session after this many failures in a
	// row; it then waits for the nightly run (REQ-KBG-022).
	journalMaxFailures = 3
	// gatePollInterval is how often waitQuiet re-checks the gate.
	gatePollInterval = 5 * time.Second
)

// activity tracks owner activity from Hooks.SessionEvent and gates background
// knowledge passes on it (REQ-KBG-020). It also holds the set of running passes
// that the next turn/start preempts (REQ-KBG-030).
type activity struct {
	mu sync.Mutex
	// now reads the service clock.
	now func() time.Time
	// busy maps a session id to the time of its last event while a turn runs.
	busy map[string]time.Time
	// lastEnd is the time of the last turn/end.
	lastEnd time.Time
	// cancels is the set of registered pass contexts, keyed by id.
	cancels map[int]context.CancelFunc
	nextID  int
}

// newActivity creates the gate with the given clock.
func newActivity(now func() time.Time) *activity {
	if now == nil {
		now = time.Now
	}
	return &activity{now: now, busy: map[string]time.Time{}, cancels: map[int]context.CancelFunc{}}
}

// observe records one session event (REQ-KBG-020). A turn/start marks the
// session busy and cancels every registered pass; a turn/end marks it idle and
// records the time; any other event refreshes a busy mark, and is ignored for a
// session that is not busy.
func (a *activity) observe(ev core.SessionEvent) {
	if a == nil {
		return
	}
	now := a.now()
	a.mu.Lock()
	switch ev.Kind {
	case core.KindTurnStart:
		if a.busy == nil {
			a.busy = map[string]time.Time{}
		}
		a.busy[ev.SessionID] = now
		cancels := make([]context.CancelFunc, 0, len(a.cancels))
		for id, cancel := range a.cancels {
			cancels = append(cancels, cancel)
			delete(a.cancels, id)
		}
		a.mu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}
		return
	case core.KindTurnEnd:
		delete(a.busy, ev.SessionID)
		a.lastEnd = now
	default:
		if _, ok := a.busy[ev.SessionID]; ok {
			a.busy[ev.SessionID] = now
		}
	}
	a.mu.Unlock()
}

// quiet reports whether no session is busy and no turn ended in the last
// quietWindow (REQ-KBG-020). A busy mark older than busyStale is treated as a
// lost turn/end and dropped.
func (a *activity) quiet(now time.Time) bool {
	if a == nil {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, at := range a.busy {
		if now.Sub(at) >= busyStale {
			delete(a.busy, id)
		}
	}
	if len(a.busy) > 0 {
		return false
	}
	if a.lastEnd.IsZero() {
		return true
	}
	return now.Sub(a.lastEnd) >= quietWindow
}

// preemptible returns a child of parent registered for preemption: the next
// turn/start cancels it (REQ-KBG-030). The release function unregisters it and
// cancels it to free resources; it is safe to call more than once.
func (a *activity) preemptible(parent context.Context) (context.Context, func()) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	if a == nil {
		return ctx, cancel
	}
	a.mu.Lock()
	a.nextID++
	id := a.nextID
	if a.cancels == nil {
		a.cancels = map[int]context.CancelFunc{}
	}
	a.cancels[id] = cancel
	a.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			a.mu.Lock()
			delete(a.cancels, id)
			a.mu.Unlock()
			cancel()
		})
	}
	return ctx, release
}

// waitQuiet blocks until the gate is quiet or ctx is done (REQ-KBG-032). It
// returns ctx.Err() when ctx ends first.
func (a *activity) waitQuiet(ctx context.Context) error {
	if a == nil {
		return nil
	}
	for {
		if a.quiet(a.now()) {
			return nil
		}
		timer := time.NewTimer(gatePollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
