// Package notify is the in-process notification event bus of the daemon
// (spec #42). The runtime and the web brokers publish owner-facing events
// (an agent reply, an approval, a question, a workflow run); the macOS menu
// bar app reads them from GET /v1/local/events and posts the notifications.
// The daemon never posts a notification itself, so the Go build stays
// CGO-free (ADR 0013).
package notify

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind names one notification event.
type Kind string

const (
	// KindReply is an agent reply at the end of a turn.
	KindReply Kind = "reply"
	// KindApprovalAsked is a tool approval answerable through the web broker.
	KindApprovalAsked Kind = "approval_asked"
	// KindApprovalDecided closes an approval, so the app removes its banner.
	KindApprovalDecided Kind = "approval_decided"
	// KindQuestionAsked is an ask_user question answerable on the web.
	KindQuestionAsked Kind = "question_asked"
	// KindQuestionAnswered closes a question, so the app removes its banner.
	KindQuestionAnswered Kind = "question_answered"
	// KindWorkflowStarted is a workflow run that started.
	KindWorkflowStarted Kind = "workflow_started"
	// KindWorkflowFinished is a workflow run that succeeded.
	KindWorkflowFinished Kind = "workflow_finished"
	// KindWorkflowFailed is a workflow run that failed.
	KindWorkflowFailed Kind = "workflow_failed"
	// KindWorkflowWaiting is a workflow run that waits for the owner.
	KindWorkflowWaiting Kind = "workflow_waiting"
)

// Control reports whether k only updates earlier banners (it removes them)
// and is never shown itself.
func (k Kind) Control() bool {
	return k == KindApprovalDecided || k == KindQuestionAnswered
}

// Event is one notification event, the data of the GET /v1/local/events SSE
// "notify" event (API-NTF-001).
type Event struct {
	// ID is "<boot>-<seq>": boot names the daemon process, seq counts up.
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	// At is the publish time in Unix milliseconds.
	At        int64  `json:"at"`
	SessionID string `json:"session_id,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	// URL is the dashboard hash the click opens, such as "#/sessions/<id>".
	URL string `json:"url"`
	// ApprovalID is the id POST /v1/local/approvals/{id} accepts. It is set
	// only when the approval can be answered there.
	ApprovalID    string `json:"approval_id,omitempty"`
	QuestionID    string `json:"question_id,omitempty"`
	WorkflowID    string `json:"workflow_id,omitempty"`
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
	// Outcome is the decision of an approval_decided event.
	Outcome       string `json:"outcome,omitempty"`
	Silent        bool   `json:"silent,omitempty"`
	TimeSensitive bool   `json:"time_sensitive,omitempty"`
	// Thread groups banners: the session id or "workflow:<id>".
	Thread string `json:"thread"`
	// Key dedupes publishes of the same occurrence, such as
	// "reply:<session>:<turn>". It is not sent.
	Key string `json:"-"`
}

const (
	// DefaultMaxEvents is the size of the resume buffer.
	DefaultMaxEvents = 200
	// DefaultMaxAge is how long a buffered event can be resumed.
	DefaultMaxAge = time.Hour
	// PresenceTTL is how long one visible report keeps a session visible.
	PresenceTTL = 45 * time.Second
	// subscriberBuffer is the per-subscriber queue; a full queue drops the
	// event for that subscriber, which resumes from the buffer on reconnect.
	subscriberBuffer = 64
	// dedupeRetention is how long a dedupe key is remembered.
	dedupeRetention = 2 * time.Hour
)

// Option configures a Bus.
type Option func(*Bus)

// WithClock sets the clock (tests).
func WithClock(now func() time.Time) Option {
	return func(b *Bus) {
		if now != nil {
			b.now = now
		}
	}
}

// WithLimits sets the resume buffer size and age.
func WithLimits(maxEvents int, maxAge time.Duration) Option {
	return func(b *Bus) {
		if maxEvents > 0 {
			b.maxEvents = maxEvents
		}
		if maxAge > 0 {
			b.maxAge = maxAge
		}
	}
}

// Bus fans notification events out to subscribers and keeps a small ring
// buffer so a reconnecting subscriber resumes after its last event id. It also
// holds the dashboard presence: a reply of a session a dashboard tab shows is
// not published.
type Bus struct {
	mu        sync.Mutex
	now       func() time.Time
	boot      string
	seq       uint64
	maxEvents int
	maxAge    time.Duration
	ring      []Event
	subs      map[chan Event]struct{}
	keys      map[string]time.Time
	visible   map[string]time.Time
}

// NewBus returns an empty bus.
func NewBus(opts ...Option) *Bus {
	b := &Bus{
		now:       time.Now,
		maxEvents: DefaultMaxEvents,
		maxAge:    DefaultMaxAge,
		subs:      map[chan Event]struct{}{},
		keys:      map[string]time.Time{},
		visible:   map[string]time.Time{},
	}
	for _, opt := range opts {
		opt(b)
	}
	b.boot = strconv.FormatInt(b.now().UnixMilli(), 36)
	return b
}

// Publish assigns the id and time of e, buffers it, and sends it to every
// subscriber. It returns false, and drops e, when e repeats a key published
// before or is a reply of a session a dashboard tab shows.
func (b *Bus) Publish(e Event) (Event, bool) {
	if b == nil {
		return e, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.pruneLocked(now)
	if e.Key != "" {
		if _, seen := b.keys[e.Key]; seen {
			return e, false
		}
	}
	if e.Kind == KindReply && e.SessionID != "" && b.visibleLocked(e.SessionID, now) {
		return e, false
	}
	if e.Key != "" {
		b.keys[e.Key] = now
	}
	b.seq++
	e.ID = b.boot + "-" + strconv.FormatUint(b.seq, 10)
	e.At = now.UnixMilli()
	if e.Thread == "" {
		e.Thread = e.SessionID
	}
	b.ring = append(b.ring, e)
	if len(b.ring) > b.maxEvents {
		b.ring = append([]Event(nil), b.ring[len(b.ring)-b.maxEvents:]...)
	}
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
	return e, true
}

// Subscribe registers a subscriber. after is the last event id it saw: the
// buffered events after it are returned for replay. An empty after, or an id
// of another daemon process, replays nothing and everything buffered
// respectively: a new subscriber starts live, and a subscriber that saw an
// older process gets what this process published. cancel unregisters.
func (b *Bus) Subscribe(after string) (replay []Event, events <-chan Event, cancel func()) {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	b.pruneLocked(b.now())
	replay = b.replayLocked(after)
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	cancel = func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
		})
	}
	return replay, ch, cancel
}

func (b *Bus) replayLocked(after string) []Event {
	after = strings.TrimSpace(after)
	if after == "" {
		return nil
	}
	boot, seqText, ok := strings.Cut(after, "-")
	if !ok || boot != b.boot {
		return append([]Event(nil), b.ring...)
	}
	seq, err := strconv.ParseUint(seqText, 10, 64)
	if err != nil {
		return append([]Event(nil), b.ring...)
	}
	out := make([]Event, 0)
	for _, e := range b.ring {
		_, n, _ := strings.Cut(e.ID, "-")
		if v, err := strconv.ParseUint(n, 10, 64); err == nil && v > seq {
			out = append(out, e)
		}
	}
	return out
}

// LastID returns the id of the newest buffered event, or "".
func (b *Bus) LastID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.ring) == 0 {
		return ""
	}
	return b.ring[len(b.ring)-1].ID
}

// Subscribers returns the number of open subscriptions.
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// SetVisible records that a dashboard tab shows (or stopped showing) a
// session. A visible report lasts PresenceTTL; the dashboard repeats it while
// the tab stays visible.
func (b *Bus) SetVisible(sessionID string, visible bool) {
	if b == nil || sessionID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if visible {
		b.visible[sessionID] = b.now().Add(PresenceTTL)
		return
	}
	delete(b.visible, sessionID)
}

// Visible reports whether a dashboard tab shows the session.
func (b *Bus) Visible(sessionID string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.visibleLocked(sessionID, b.now())
}

func (b *Bus) visibleLocked(sessionID string, now time.Time) bool {
	until, ok := b.visible[sessionID]
	return ok && now.Before(until)
}

func (b *Bus) pruneLocked(now time.Time) {
	cut := 0
	for cut < len(b.ring) && now.Sub(time.UnixMilli(b.ring[cut].At)) > b.maxAge {
		cut++
	}
	if cut > 0 {
		b.ring = append([]Event(nil), b.ring[cut:]...)
	}
	for key, at := range b.keys {
		if now.Sub(at) > dedupeRetention {
			delete(b.keys, key)
		}
	}
	for id, until := range b.visible {
		if !now.Before(until) {
			delete(b.visible, id)
		}
	}
}
