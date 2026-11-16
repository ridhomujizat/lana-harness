package main

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ridhomujizat/lana-harness/internal/notify"
	"github.com/ridhomujizat/lana-harness/internal/workbench"
)

// Tunables of the Workbench agent notifications (spec 55 A5).
const (
	// workbenchNotifyDebounce is the shortest gap between two agent
	// notifications of one session, over both kinds.
	workbenchNotifyDebounce = 10 * time.Second
	// workbenchNotifyMinBackoff and workbenchNotifyMaxBackoff bound the wait
	// before re-subscribing after the core went away.
	workbenchNotifyMinBackoff = 1 * time.Second
	workbenchNotifyMaxBackoff = 30 * time.Second
)

// workbenchWatcher watches the Workbench core and publishes agent_finished and
// agent_needs_you notifications to the daemon bus (spec 55 A5). It subscribes
// to a core that is already running; it never starts one.
type workbenchWatcher struct {
	bus *notify.Bus
	now func() time.Time

	mu       sync.Mutex
	client   *workbench.Client
	prev     map[string]workbench.Session
	lastSent map[string]time.Time
	nudge    chan struct{}
}

// newWorkbenchWatcher returns a watcher publishing to bus. It does nothing
// until attach gives it a live client.
func newWorkbenchWatcher(bus *notify.Bus) *workbenchWatcher {
	return &workbenchWatcher{
		bus:      bus,
		now:      time.Now,
		prev:     map[string]workbench.Session{},
		lastSent: map[string]time.Time{},
		nudge:    make(chan struct{}, 1),
	}
}

// attach records the live core client and wakes the watch loop, which
// subscribes on it. The daemon registers it as the backend's onLive hook and
// calls it at start when the core socket already exists.
func (w *workbenchWatcher) attach(c *workbench.Client) {
	if c == nil {
		return
	}
	w.mu.Lock()
	w.client = c
	w.mu.Unlock()
	select {
	case w.nudge <- struct{}{}:
	default:
	}
}

// current returns the live client, or nil when none is attached yet.
func (w *workbenchWatcher) current() *workbench.Client {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.client
}

// run subscribes to the core and, when the stream ends, re-subscribes on the
// current client with a backoff that doubles from 1 s to 30 s until ctx ends.
// It waits for a client when none is attached, so it never starts a core.
func (w *workbenchWatcher) run(ctx context.Context) {
	backoff := workbenchNotifyMinBackoff
	for ctx.Err() == nil {
		client := w.current()
		if client == nil {
			if !w.sleep(ctx, 0) {
				return
			}
			continue
		}
		events, err := client.Subscribe(ctx)
		if err != nil {
			if !w.sleep(ctx, backoff) {
				return
			}
			backoff = nextWorkbenchBackoff(backoff)
			continue
		}
		w.seed(ctx, client)
		if w.consume(ctx, events) {
			backoff = workbenchNotifyMinBackoff
		}
		if !w.sleep(ctx, backoff) {
			return
		}
		backoff = nextWorkbenchBackoff(backoff)
	}
}

// sleep waits for d, a new client, or the end of ctx; d <= 0 waits for the
// latter two only. It reports whether to keep watching.
func (w *workbenchWatcher) sleep(ctx context.Context, d time.Duration) bool {
	var tick <-chan time.Time
	if d > 0 {
		timer := time.NewTimer(d)
		defer timer.Stop()
		tick = timer.C
	}
	select {
	case <-ctx.Done():
		return false
	case <-w.nudge:
		return true
	case <-tick:
		return true
	}
}

// nextWorkbenchBackoff doubles d, capped at workbenchNotifyMaxBackoff.
func nextWorkbenchBackoff(d time.Duration) time.Duration {
	if d *= 2; d > workbenchNotifyMaxBackoff {
		return workbenchNotifyMaxBackoff
	}
	return d
}

// seed records a baseline for every session not tracked yet, so the first state
// the watcher sees of a session is not treated as a change.
func (w *workbenchWatcher) seed(ctx context.Context, client *workbench.Client) {
	list, err := client.List(ctx)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, s := range list {
		if _, ok := w.prev[s.ID]; !ok {
			w.prev[s.ID] = s
		}
	}
}

// consume processes the event stream until it closes or ctx ends, reporting
// whether any event arrived.
func (w *workbenchWatcher) consume(ctx context.Context, events <-chan workbench.Event) bool {
	got := false
	for {
		select {
		case <-ctx.Done():
			return got
		case ev, ok := <-events:
			if !ok {
				return got
			}
			got = true
			w.handle(ev)
		}
	}
}

// handle applies one core event; a layout change is ignored.
func (w *workbenchWatcher) handle(ev workbench.Event) {
	switch ev.Type {
	case "session.created", "session.updated":
		if ev.Session != nil {
			w.note(*ev.Session)
		}
	case "session.removed":
		if ev.Session != nil {
			w.forget(ev.Session.ID)
		}
	}
}

// note publishes the notification of a session change, if any. The first
// snapshot of a session is a baseline and never notifies.
func (w *workbenchWatcher) note(next workbench.Session) {
	at := w.now()
	w.mu.Lock()
	prev, seen := w.prev[next.ID]
	w.prev[next.ID] = next
	w.mu.Unlock()
	if !seen {
		return
	}
	event, ok := workbenchTransition(prev, next, at)
	if !ok || !w.allowDebounce(next.ID, at) {
		return
	}
	w.bus.Publish(event)
}

// forget drops the state of a removed session.
func (w *workbenchWatcher) forget(id string) {
	w.mu.Lock()
	delete(w.prev, id)
	delete(w.lastSent, id)
	w.mu.Unlock()
}

// allowDebounce reports whether a notification of sessionID may be published
// now: at most one per session per workbenchNotifyDebounce, over both kinds.
func (w *workbenchWatcher) allowDebounce(sessionID string, at time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if last, ok := w.lastSent[sessionID]; ok && at.Sub(last) < workbenchNotifyDebounce {
		return false
	}
	w.lastSent[sessionID] = at
	return true
}

// workbenchTransition is the pure state machine of the watcher: it turns one
// session change into the notification to publish, or reports that the change
// is not owner-facing. A finished turn is a session that left "working" for
// "idle" (an exit is not), and needs-you is a false-to-true edge.
func workbenchTransition(prev, next workbench.Session, at time.Time) (notify.Event, bool) {
	if next.Agent == "" {
		return notify.Event{}, false
	}
	name := workbenchAgentName(next.Agent)
	occurrence := strconv.FormatInt(at.UnixNano(), 10)
	switch {
	case prev.State == "working" && next.State == "idle":
		return notify.AgentFinished(name, workbenchAgentBody(next), next.ID, occurrence), true
	case !prev.NeedsYou && next.NeedsYou:
		return notify.AgentNeedsYou(name, workbenchAgentBody(next), next.ID, occurrence), true
	}
	return notify.Event{}, false
}

// workbenchAgentName is the readable name of a coding agent: the two known
// agents get a display name, anything else keeps its label.
func workbenchAgentName(agent string) string {
	switch strings.TrimSpace(agent) {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	default:
		return strings.TrimSpace(agent)
	}
}

// workbenchAgentBody is the banner body of a session: its title with the
// leading status glyph and spaces dropped, cut to the banner limit. When that
// is empty, the folder name of the session's working directory (or start
// directory) stands in.
func workbenchAgentBody(s workbench.Session) string {
	title := strings.TrimLeftFunc(s.Title, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if body := notify.OneLine(title, notify.AgentMax); body != "" {
		return body
	}
	for _, dir := range []string{s.Cwd, s.Dir} {
		if dir = strings.TrimSpace(dir); dir == "" {
			continue
		}
		return filepath.Base(dir)
	}
	return ""
}
