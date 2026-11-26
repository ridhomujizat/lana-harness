package workbench

import (
	"encoding/json"
	"net"
	"sync"
	"time"
)

// hub fans session events out to the subscribers of one core and detects the
// changes it reports by polling the sessions (REQ-WB-030).
type hub struct {
	mu     sync.Mutex
	subs   map[*subscriber]struct{}
	tracks map[string]*track
}

// track is what the hub last sent about one session.
type track struct {
	ss    *session
	last  Session
	dirty bool
	sent  time.Time
}

func newHub() *hub {
	return &hub{subs: map[*subscriber]struct{}{}, tracks: map[string]*track{}}
}

// subscriber is a connection streaming events, whose output a goroutine
// writes so a slow client never holds up the hub.
type subscriber struct {
	conn    net.Conn
	out     chan Event
	outOnce sync.Once
}

// send queues an event, or reports that the client is too far behind.
func (sub *subscriber) send(ev Event) bool {
	select {
	case sub.out <- ev:
		return true
	default:
		return false
	}
}

// finish ends the stream after the queued events are written.
func (sub *subscriber) finish() { sub.outOnce.Do(func() { close(sub.out) }) }

// drop ends the stream now, closing the connection to unblock a stuck writer.
func (sub *subscriber) drop() {
	sub.finish()
	_ = sub.conn.Close()
}

// writer sends the queued events as JSON lines and closes the connection when
// done.
func (sub *subscriber) writer() {
	for ev := range sub.out {
		b, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		if _, err := sub.conn.Write(append(b, '\n')); err != nil {
			break
		}
	}
	_ = sub.conn.Close()
	for range sub.out {
	}
}

// add starts streaming to a subscriber.
func (h *hub) add(sub *subscriber) {
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
}

// remove stops streaming to a subscriber, closing its connection.
func (h *hub) remove(sub *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(sub)
}

// removeLocked detaches a subscriber; the hub lock must be held.
func (h *hub) removeLocked(sub *subscriber) {
	if _, ok := h.subs[sub]; !ok {
		return
	}
	delete(h.subs, sub)
	sub.drop()
}

// run polls the sessions and flushes their events until quit closes. Every
// other tick it also refreshes the foreground program (REQ-WB-013).
func (h *hub) run(quit <-chan struct{}) {
	t := time.NewTicker(updateInterval)
	defer t.Stop()
	for i := 0; ; i++ {
		select {
		case <-quit:
			return
		case <-t.C:
		}
		if i%2 == 1 {
			h.refreshPrograms()
		}
		h.poll()
	}
}

// poll recomputes every tracked session and sends the ones that changed.
func (h *hub) poll() {
	for _, ss := range h.sessions() {
		h.update(ss.id, ss.info())
	}
}

// refreshPrograms reads the foreground program of every tracked session.
func (h *hub) refreshPrograms() {
	for _, ss := range h.sessions() {
		ss.refreshProgram()
	}
}

// sessions returns the tracked sessions. The hub lock is not held when the
// caller uses them, so it may take a session lock.
func (h *hub) sessions() []*session {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := make([]*session, 0, len(h.tracks))
	for _, t := range h.tracks {
		list = append(list, t.ss)
	}
	return list
}

// update records what changed for one session and sends session.updated when
// it is due: a dirty session is sent at most once every updateInterval, and
// the flag stays set until it is actually sent, so the last state is never
// dropped.
func (h *hub) update(id string, info Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.tracks[id]
	if t == nil {
		return
	}
	if !sameTracked(t.last, info) {
		t.dirty = true
	}
	now := time.Now()
	if !shouldSend(t.dirty, t.sent, now) {
		return
	}
	t.last, t.dirty, t.sent = info, false, now
	h.emitLocked(Event{Type: evUpdated, Session: &info})
}

// markDirty queues a session for the next poll, so a change the session
// itself notices is reported without waiting for the comparison.
func (h *hub) markDirty(id string) {
	h.mu.Lock()
	if t := h.tracks[id]; t != nil {
		t.dirty = true
	}
	h.mu.Unlock()
}

// created tracks a new session and emits session.created.
func (h *hub) created(ss *session, info Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tracks[ss.id] = &track{ss: ss, last: info, sent: time.Now()}
	h.emitLocked(Event{Type: evCreated, Session: &info})
}

// removed stops tracking a session and emits session.removed. It is done
// under the hub lock so no session.updated can follow it.
func (h *hub) removed(id string, info Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.tracks, id)
	h.emitLocked(Event{Type: evRemoved, Session: &info})
}

// layout emits layout.changed with the stored document.
func (h *hub) layout(doc json.RawMessage) {
	h.emit(Event{Type: evLayout, Layout: doc})
}

// emit sends ev to every subscriber.
func (h *hub) emit(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.emitLocked(ev)
}

// emitLocked sends ev to every subscriber, dropping one that fell behind. The
// hub lock must be held.
func (h *hub) emitLocked(ev Event) {
	for sub := range h.subs {
		if !sub.send(ev) {
			h.removeLocked(sub)
		}
	}
}

// sameTracked reports whether two session snapshots agree on every field a
// session.updated event reports (REQ-WB-030).
func sameTracked(a, b Session) bool {
	return a.State == b.State &&
		a.Program == b.Program &&
		a.Title == b.Title &&
		a.Cwd == b.Cwd &&
		a.NeedsYou == b.NeedsYou &&
		a.ExitCode == b.ExitCode &&
		a.Cols == b.Cols &&
		a.Rows == b.Rows &&
		a.Attached == b.Attached &&
		a.Agent == b.Agent &&
		a.AgentState == b.AgentState &&
		a.AccountSwitch == b.AccountSwitch &&
		a.ConversationAgent == b.ConversationAgent &&
		a.ConversationID == b.ConversationID &&
		a.AgentConfigDir == b.AgentConfigDir
}

// shouldSend reports whether a dirty session may be sent now, so one session
// gets at most two session.updated events a second.
func shouldSend(dirty bool, sent, now time.Time) bool {
	return dirty && now.Sub(sent) >= updateInterval
}
