package web

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// The secret request broker of spec 53. The secret_request tool opens a
// request through the secret request service; the daemon routes web and
// workflow sessions here. The browser shows a secure input card and posts the
// value once to POST /v1/secret-requests/{request_id}; the broker writes it to
// secrets.env with the Settings > Secrets code and resolves the request with
// the outcome only. The value is never logged, published, or returned.

const (
	// secretRequestTimeout is the time an open request waits, as a question.
	secretRequestTimeout = 30 * time.Minute
	// secretRequestClosedRetention is how long a closed request ID is kept so
	// a late answer gets 409 secret_request_closed instead of 404.
	secretRequestClosedRetention = time.Hour
	// secretRequestSubscriberBuffer is the per-stream queue of request events.
	secretRequestSubscriberBuffer = 16
)

var (
	errSecretRequestNotFound = errors.New("secret request not found")
	errSecretRequestClosed   = errors.New("secret request closed")
	// errSecretRequestInvalid marks a body that is neither one value nor a
	// cancel.
	errSecretRequestInvalid = errors.New("invalid secret request answer")
)

// secretRequestEvent is the data of the secret_request SSE event
// (spec 53 API-SEC-003). It never carries a value.
type secretRequestEvent struct {
	RequestID   string `json:"request_id"`
	CallID      string `json:"call_id"`
	TurnID      string `json:"turn_id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Link        string `json:"link,omitempty"`
	Replaces    bool   `json:"replaces"`
	ExpiresAt   int64  `json:"expires_at"`
}

type pendingSecretRequest struct {
	id        string
	sessionID string
	event     secretRequestEvent
	reply     chan core.SecretOutcome
	timer     approvalTimer
	since     int64
}

// secretSaver writes one secret value to the owner's secret store.
type secretSaver func(name, value string) error

// SecretRequests is the browser secret request broker.
type SecretRequests struct {
	mu     sync.Mutex
	clock  approvalClock
	open   map[string]*pendingSecretRequest
	closed map[string]time.Time
	subs   map[string]map[chan secretRequestEvent]struct{}
	rootOf sessionRootResolver
	save   secretSaver
}

func newSecretRequests() *SecretRequests {
	return &SecretRequests{
		clock:  systemClock{},
		open:   map[string]*pendingSecretRequest{},
		closed: map[string]time.Time{},
		subs:   map[string]map[chan secretRequestEvent]struct{}{},
	}
}

// setClock replaces the clock used for expiry; it exists for tests.
func (q *SecretRequests) setClock(clock approvalClock) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if clock != nil {
		q.clock = clock
	}
}

// setSaver installs the secret store writer.
func (q *SecretRequests) setSaver(save secretSaver) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.save = save
}

// Ask registers one request and waits for the first browser answer, the
// timeout, or ctx cancellation. The service sets RequestID and ExpiresAt. A
// request of a descendant session is published to the root stream.
func (q *SecretRequests) Ask(ctx context.Context, req core.SecretRequest) (core.SecretOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return secretContextOutcome(err), nil
	}
	id := req.RequestID
	if id == "" {
		generated, err := newQuestionRequestID()
		if err != nil {
			return core.SecretOutcome{Status: core.SecretStatusUnavailable, Reason: "no_responder"}, nil
		}
		id = generated
	}
	now := q.clock.Now()
	expiresAt := req.ExpiresAt
	if expiresAt <= now.UnixMilli() {
		expiresAt = now.Add(secretRequestTimeout).UnixMilli()
	}
	wait := time.Duration(expiresAt-now.UnixMilli()) * time.Millisecond

	q.mu.Lock()
	resolve := q.rootOf
	q.mu.Unlock()
	rootID := req.SessionID
	if resolve != nil {
		if root := resolve(req.SessionID); root != "" {
			rootID = root
		}
	}

	pending := &pendingSecretRequest{
		id:        id,
		sessionID: req.SessionID,
		event: secretRequestEvent{
			RequestID:   id,
			CallID:      req.CallID,
			TurnID:      req.TurnID,
			Name:        req.Name,
			Description: req.Description,
			Link:        req.Link,
			Replaces:    req.Replaces,
			ExpiresAt:   expiresAt,
		},
		reply: make(chan core.SecretOutcome, 1),
		timer: q.clock.NewTimer(wait),
		since: now.UnixMilli(),
	}

	q.mu.Lock()
	q.pruneClosedLocked(now)
	q.open[id] = pending
	for ch := range q.subs[rootID] {
		select {
		case ch <- pending.event:
		default:
			// A slow stream gets the event again from the replay.
		}
	}
	q.mu.Unlock()

	select {
	case outcome := <-pending.reply:
		return outcome, nil
	case <-pending.timer.C():
		return q.resolve(pending, core.SecretOutcome{Status: core.SecretStatusExpired, Reason: "timeout"}), nil
	case <-ctx.Done():
		return q.resolve(pending, secretContextOutcome(ctx.Err())), nil
	}
}

// resolve closes a pending request unless an answer won the race; then it
// returns that answer.
func (q *SecretRequests) resolve(pending *pendingSecretRequest, outcome core.SecretOutcome) core.SecretOutcome {
	q.mu.Lock()
	if current, ok := q.open[pending.id]; ok && current == pending {
		delete(q.open, pending.id)
		q.closed[pending.id] = q.clock.Now()
		pending.timer.Stop()
		pending.reply <- outcome
	}
	q.mu.Unlock()
	return <-pending.reply
}

// secretRequestAnswer is the body of POST /v1/secret-requests/{request_id}:
// {"value":"..."} or {"cancel":true}.
type secretRequestAnswer struct {
	Value  *string `json:"value"`
	Cancel bool    `json:"cancel"`
}

// answer accepts the first valid submission. A value is validated and written
// to the secret store under the broker lock, so the timeout cannot close the
// request between the write and the reply. An invalid value or a failed
// write leaves the request open.
func (q *SecretRequests) answer(id string, body secretRequestAnswer) (core.SecretOutcome, string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clock.Now()
	q.pruneClosedLocked(now)
	pending, ok := q.open[id]
	if !ok {
		if _, seen := q.closed[id]; seen {
			return core.SecretOutcome{}, "", errSecretRequestClosed
		}
		return core.SecretOutcome{}, "", errSecretRequestNotFound
	}
	name := pending.event.Name
	var outcome core.SecretOutcome
	switch {
	case body.Cancel && body.Value != nil:
		return core.SecretOutcome{}, name, errSecretRequestInvalid
	case body.Cancel:
		outcome = core.SecretOutcome{Status: core.SecretStatusCancelled, Reason: "owner"}
	case body.Value == nil:
		return core.SecretOutcome{}, name, errSecretRequestInvalid
	default:
		if err := webfiles.ValidateSecretValue(*body.Value); err != nil {
			return core.SecretOutcome{}, name, err
		}
		if q.save == nil {
			return core.SecretOutcome{}, name, errors.New("secret store is not configured")
		}
		if err := q.save(name, *body.Value); err != nil {
			return core.SecretOutcome{}, name, err
		}
		outcome = core.SecretOutcome{Status: core.SecretStatusSaved}
	}
	delete(q.open, id)
	q.closed[id] = now
	pending.timer.Stop()
	pending.reply <- outcome
	return outcome, name, nil
}

func (q *SecretRequests) pruneClosedLocked(now time.Time) {
	for id, closedAt := range q.closed {
		if now.Sub(closedAt) >= secretRequestClosedRetention {
			delete(q.closed, id)
		}
	}
}

// openRequests returns the open requests for the room presence.
func (q *SecretRequests) openRequests() []openRequest {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]openRequest, 0, len(q.open))
	for _, pending := range q.open {
		out = append(out, openRequest{
			Kind:      "secret",
			SessionID: pending.sessionID,
			Tool:      pending.event.Name,
			Since:     pending.since,
		})
	}
	return out
}

// subscribe atomically returns the open requests of a session (and of its
// descendants) and registers a stream for new ones.
func (q *SecretRequests) subscribe(sessionID string, resolve sessionRootResolver) ([]secretRequestEvent, <-chan secretRequestEvent, func()) {
	q.mu.Lock()
	if resolve != nil {
		q.rootOf = resolve
	}
	now := q.clock.Now().UnixMilli()
	replay := make([]secretRequestEvent, 0)
	for _, request := range q.open {
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
	ch := make(chan secretRequestEvent, secretRequestSubscriberBuffer)
	if q.subs[sessionID] == nil {
		q.subs[sessionID] = map[chan secretRequestEvent]struct{}{}
	}
	q.subs[sessionID][ch] = struct{}{}
	q.mu.Unlock()

	unsubscribe := func() {
		q.mu.Lock()
		delete(q.subs[sessionID], ch)
		if len(q.subs[sessionID]) == 0 {
			delete(q.subs, sessionID)
		}
		q.mu.Unlock()
	}
	return replay, ch, unsubscribe
}

func secretContextOutcome(err error) core.SecretOutcome {
	if errors.Is(err, context.DeadlineExceeded) {
		return core.SecretOutcome{Status: core.SecretStatusExpired, Reason: "timeout"}
	}
	return core.SecretOutcome{Status: core.SecretStatusCancelled, Reason: "turn_cancelled"}
}

// SecretRequests returns the broker that opens browser secret requests.
func (s *Server) SecretRequests() *SecretRequests {
	return s.secretRequests
}

// registerSecretRequestRoutes registers API-SEC-004 through Handle so the
// auth, Host, Origin, and CSRF checks apply as for the other endpoints.
func (s *Server) registerSecretRequestRoutes() {
	s.secretRequests.setSaver(s.saveRequestedSecret)
	s.Handle("POST /v1/secret-requests/{request_id}", http.HandlerFunc(s.handleAnswerSecretRequest))
}

// saveRequestedSecret writes one value with the Settings > Secrets code
// (secrets.env, secrets.meta.json, the restart flag, and the audit line with
// the name only).
func (s *Server) saveRequestedSecret(name, value string) error {
	store := s.secretStore
	if store == nil {
		return errors.New("secret store is not configured")
	}
	if err := webfiles.ValidateSecretName(name); err != nil {
		return err
	}
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	return s.writeSecretValue(store, name, value)
}

type secretRequestAnswerResponse struct {
	Outcome string `json:"outcome"`
	Name    string `json:"name"`
}

// handleAnswerSecretRequest answers one request. The body is never logged:
// errors are logged without it and the response names the outcome only.
func (s *Server) handleAnswerSecretRequest(w http.ResponseWriter, r *http.Request) {
	var body secretRequestAnswer
	if !decodeJSONObject(w, r, secretBodyLimit, &body) {
		return
	}
	outcome, name, err := s.secretRequests.answer(r.PathValue("request_id"), body)
	var validation *webfiles.SecretValidationError
	switch {
	case errors.Is(err, errSecretRequestClosed):
		writeAPIError(w, http.StatusConflict, "secret_request_closed", "secret request closed")
	case errors.Is(err, errSecretRequestNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "secret request not found")
	case errors.Is(err, errSecretRequestInvalid):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", `send {"value":"..."} or {"cancel":true}`)
	case errors.As(err, &validation):
		writeSecretValidation(w, err)
	case err != nil:
		s.logger.Error("web: answer secret request", "name", name, "error", err.Error())
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	default:
		writeJSON(w, http.StatusOK, secretRequestAnswerResponse{Outcome: outcome.Status, Name: name})
	}
}
