package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/toolfmt"
)

const (
	// approvalTimeout is the time an open approval waits for an answer
	// (API-WEB-011, Q-014).
	approvalTimeout = 10 * time.Minute
	// approvalClosedRetention is how long a closed request ID is remembered so
	// a late answer gets 409 approval_closed instead of 404 not_found.
	approvalClosedRetention = time.Hour
	// maxApprovalReasonChars is CQ-002: the maximum length of a rejection
	// reason.
	maxApprovalReasonChars = 500
	maxApprovalBody        = 64 << 10
	// approvalSubscriberBuffer is the per-stream queue of approval events.
	approvalSubscriberBuffer = 32
)

var (
	errApprovalNotFound = errors.New("approval request not found")
	errApprovalClosed   = errors.New("approval request closed")
)

// approvalEvent is the data of the approval SSE event (API-WEB-008, API-TEAM-011).
type approvalEvent struct {
	RequestID string    `json:"request_id"`
	Tool      string    `json:"tool"`
	Argument  string    `json:"argument"`
	Risk      core.Risk `json:"risk"`
	ExpiresAt int64     `json:"expires_at"`
	// ChildSessionID and Agent tag an approval of a descendant session. Root
	// approvals leave them empty (API-TEAM-011).
	ChildSessionID string `json:"child_session_id,omitempty"`
	Agent          string `json:"agent,omitempty"`
	Label          string `json:"label,omitempty"`
}

// sessionRootResolver reports the root session id of a session. The stream
// installs it so a child approval is stored under and published to the root
// (REQ-TEAM-010, API-TEAM-011).
type sessionRootResolver func(sessionID string) string

// approvalClock supplies the current time and the approval timer. It is an
// interface so tests can drive expiry with a fake clock.
type approvalClock interface {
	Now() time.Time
	NewTimer(time.Duration) approvalTimer
}

// approvalTimer is the subset of *time.Timer the broker needs.
type approvalTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) NewTimer(d time.Duration) approvalTimer {
	return &systemTimer{timer: time.NewTimer(d)}
}

type systemTimer struct {
	timer *time.Timer
}

func (t *systemTimer) C() <-chan time.Time { return t.timer.C }
func (t *systemTimer) Stop() bool          { return t.timer.Stop() }

type pendingApproval struct {
	id        string
	sessionID string
	event     approvalEvent
	reply     chan core.ApprovalOutcome
	timer     approvalTimer
}

// Approvals is the browser approval broker. It opens requests for the web
// channel, publishes them to the session's open streams, and resolves them on
// the first answer, the timeout, or cancellation.
type Approvals struct {
	mu     sync.Mutex
	clock  approvalClock
	open   map[string]*pendingApproval
	closed map[string]time.Time
	subs   map[string]map[chan approvalEvent]struct{}
	// rootOf resolves the root session of an approval request. It is nil until
	// a stream installs it, so a broker used alone treats every session as its
	// own root.
	rootOf sessionRootResolver
}

func newApprovals() *Approvals {
	return &Approvals{
		clock:  systemClock{},
		open:   map[string]*pendingApproval{},
		closed: map[string]time.Time{},
		subs:   map[string]map[chan approvalEvent]struct{}{},
	}
}

// setClock replaces the clock used for expiry. Call it before the first Ask;
// it exists for tests.
func (a *Approvals) setClock(clock approvalClock) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if clock != nil {
		a.clock = clock
	}
}

// Ask registers one approval request and waits for the first browser answer,
// the timeout, or ctx cancellation. The request ID is random so it cannot be
// guessed from the session. A request of a descendant is stored under the root
// session so it reaches the root's stream (REQ-TEAM-010, API-TEAM-011).
func (a *Approvals) Ask(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return approvalContextOutcome(err)
	}
	id, err := newApprovalRequestID()
	if err != nil {
		return core.OutcomeUnavailable
	}
	rootID := req.SessionID
	if resolve := a.sessionRootResolver(); resolve != nil {
		if root := resolve(req.SessionID); root != "" {
			rootID = root
		}
	}
	now := a.clock.Now()
	event := approvalEvent{
		RequestID: id,
		Tool:      req.ToolName,
		Argument:  toolfmt.PrimaryArg(req.ToolName, req.Input),
		Risk:      req.Risk,
		ExpiresAt: now.Add(approvalTimeout).UnixMilli(),
	}
	if rootID != req.SessionID {
		agent := req.AgentName
		if agent == "" {
			agent = req.Label
		}
		label := req.Label
		if label == "" {
			label = agent
		}
		event.ChildSessionID = req.SessionID
		event.Agent = agent
		event.Label = label
	}
	pending := &pendingApproval{
		id:        id,
		sessionID: req.SessionID,
		event:     event,
		reply:     make(chan core.ApprovalOutcome, 1),
		timer:     a.clock.NewTimer(approvalTimeout),
	}

	a.mu.Lock()
	a.pruneClosedLocked(now)
	a.open[id] = pending
	for ch := range a.subs[rootID] {
		select {
		case ch <- pending.event:
		default:
			// A slow stream gets the event again from the replay of open
			// requests when it reconnects.
		}
	}
	a.mu.Unlock()

	select {
	case outcome := <-pending.reply:
		return outcome
	case <-pending.timer.C():
		return a.resolve(pending, core.OutcomeUnavailable)
	case <-ctx.Done():
		return a.resolve(pending, approvalContextOutcome(ctx.Err()))
	}
}

// openRequests returns a snapshot of the open approval requests for the room
// presence (REQ-ROOM-006). The caller maps each request to its room.
func (a *Approvals) openRequests() []openRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]openRequest, 0, len(a.open))
	for _, pending := range a.open {
		out = append(out, openRequest{
			Kind:      "approval",
			SessionID: pending.sessionID,
			Agent:     pending.event.Agent,
			Tool:      pending.event.Tool,
			Since:     pending.event.ExpiresAt - approvalTimeout.Milliseconds(),
		})
	}
	return out
}

// sessionRootResolver returns the current resolver under the broker lock.
func (a *Approvals) sessionRootResolver() sessionRootResolver {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rootOf
}

// resolve closes a pending request if no answer won the race yet. When another
// path already closed it, resolve returns that decision instead.
func (a *Approvals) resolve(pending *pendingApproval, outcome core.ApprovalOutcome) core.ApprovalOutcome {
	a.mu.Lock()
	if current, ok := a.open[pending.id]; ok && current == pending {
		delete(a.open, pending.id)
		a.markClosedLocked(pending.id)
		pending.timer.Stop()
		pending.reply <- outcome
	}
	a.mu.Unlock()
	return <-pending.reply
}

// answer accepts the first response for an open request. A known but closed ID
// returns errApprovalClosed; an ID that never existed returns
// errApprovalNotFound.
func (a *Approvals) answer(id string, outcome core.ApprovalOutcome) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	pending, ok := a.open[id]
	if !ok {
		if _, seen := a.closed[id]; seen {
			return errApprovalClosed
		}
		return errApprovalNotFound
	}
	delete(a.open, id)
	a.markClosedLocked(id)
	pending.timer.Stop()
	pending.reply <- outcome
	return nil
}

func (a *Approvals) markClosedLocked(id string) {
	a.closed[id] = a.clock.Now()
}

func (a *Approvals) pruneClosedLocked(now time.Time) {
	for id, closedAt := range a.closed {
		if now.Sub(closedAt) >= approvalClosedRetention {
			delete(a.closed, id)
		}
	}
}

// subscribe atomically returns the open requests of a session and registers a
// stream for new ones, so an answer racing a connection is not lost. When a
// resolver is given it also becomes the broker's resolver, and the replay
// includes the open requests of the session's descendants (API-TEAM-011).
func (a *Approvals) subscribe(sessionID string, resolvers ...sessionRootResolver) ([]approvalEvent, <-chan approvalEvent, func()) {
	var resolve sessionRootResolver
	if len(resolvers) > 0 {
		resolve = resolvers[0]
	}

	a.mu.Lock()
	// The resolver runs under the same lock as the replay so a request that
	// opens during the handshake is either in the replay or in the channel.
	if resolve != nil {
		a.rootOf = resolve
	}
	now := a.clock.Now().UnixMilli()
	replay := make([]approvalEvent, 0)
	for _, request := range a.open {
		rootID := request.sessionID
		if resolve != nil && request.sessionID != sessionID {
			if root := resolve(request.sessionID); root != "" {
				rootID = root
			}
		}
		if rootID == sessionID && request.event.ExpiresAt > now {
			replay = append(replay, request.event)
		}
	}
	sort.Slice(replay, func(i, j int) bool {
		if replay[i].ExpiresAt != replay[j].ExpiresAt {
			return replay[i].ExpiresAt < replay[j].ExpiresAt
		}
		return replay[i].RequestID < replay[j].RequestID
	})
	ch := make(chan approvalEvent, approvalSubscriberBuffer)
	if a.subs[sessionID] == nil {
		a.subs[sessionID] = map[chan approvalEvent]struct{}{}
	}
	a.subs[sessionID][ch] = struct{}{}
	a.mu.Unlock()

	unsubscribe := func() {
		a.mu.Lock()
		delete(a.subs[sessionID], ch)
		if len(a.subs[sessionID]) == 0 {
			delete(a.subs, sessionID)
		}
		a.mu.Unlock()
	}
	return replay, ch, unsubscribe
}

// sessionInfoLookup is an optional Backend extension that reports one session
// without the full detail read. The approval fan-in uses it to walk
// ParentSessionID cheaply.
type sessionInfoLookup interface {
	SessionInfo(ctx context.Context, sessionID string) (core.SessionInfo, error)
}

// sessionRoot walks ParentSessionID from a session up to its root. It stops on
// a lookup error or a cycle so a broken chain cannot stall a stream.
func sessionRoot(ctx context.Context, backend Backend, sessionID string) string {
	root := sessionID
	seen := map[string]bool{root: true}
	for {
		info, err := lookupSessionInfo(ctx, backend, root)
		if err != nil {
			return root
		}
		parent := info.ParentSessionID
		if parent == "" || seen[parent] {
			return root
		}
		seen[parent] = true
		root = parent
	}
}

func lookupSessionInfo(ctx context.Context, backend Backend, sessionID string) (core.SessionInfo, error) {
	if lookup, ok := backend.(sessionInfoLookup); ok {
		return lookup.SessionInfo(ctx, sessionID)
	}
	session, err := backend.Session(ctx, sessionID)
	if err != nil {
		return core.SessionInfo{}, err
	}
	return session.SessionInfo, nil
}

func approvalContextOutcome(err error) core.ApprovalOutcome {
	if errors.Is(err, context.DeadlineExceeded) {
		// The runtime wraps approvals in a deadline too. At the 10-minute mark
		// the deadline must read as a timeout, not as a user cancellation.
		return core.OutcomeUnavailable
	}
	return core.OutcomeCancelled
}

func newApprovalRequestID() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// registerApprovalRoutes registers API-WEB-011 through Handle so the auth and
// CSRF checks apply exactly as for the other endpoints.
func (s *Server) registerApprovalRoutes() {
	s.Handle("POST /v1/approvals/{id}", http.HandlerFunc(s.handleAnswerApproval))
}

type approvalAnswerRequest struct {
	Outcome string  `json:"outcome"`
	Reason  *string `json:"reason"`
}

type approvalAnswerResponse struct {
	Outcome string `json:"outcome"`
}

func (s *Server) handleAnswerApproval(w http.ResponseWriter, r *http.Request) {
	var body approvalAnswerRequest
	if !decodeJSONObject(w, r, maxApprovalBody, &body) {
		return
	}
	outcome := core.ApprovalOutcome(strings.TrimSpace(body.Outcome))
	switch outcome {
	case core.OutcomeAllowedOnce, core.OutcomeAllowedSession, core.OutcomeRejected:
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "outcome must be one of allowed_once, allowed_session, rejected")
		return
	}
	if body.Reason != nil && utf8.RuneCountInString(*body.Reason) > maxApprovalReasonChars {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "reason must be at most 500 characters")
		return
	}
	err := s.approvals.answer(r.PathValue("id"), outcome)
	switch {
	case errors.Is(err, errApprovalClosed):
		writeAPIError(w, http.StatusConflict, "approval_closed", "approval closed")
	case errors.Is(err, errApprovalNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "approval request not found")
	case err != nil:
		s.logger.Error("web: answer approval", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	default:
		writeJSON(w, http.StatusOK, approvalAnswerResponse{Outcome: string(outcome)})
	}
}
