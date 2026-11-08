package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/notify"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// The notification side of the web server (spec #42): the brokers publish
// their approvals and questions to the notification bus, the menu bar app
// reads the bus from GET /v1/local/events and answers approvals through POST
// /v1/local/approvals/{id}, and the dashboard reports which session a tab
// shows so the reply of that session does not notify.
const (
	// LocalEventsPath is the SSE stream of notification events (API-NTF-001).
	LocalEventsPath = "/v1/local/events"
	// LocalApprovalsPath prefixes the local approval answer (API-NTF-002).
	LocalApprovalsPath = "/v1/local/approvals/"
	// localEventsHeartbeat is the comment interval of the local event stream.
	localEventsHeartbeat = 20 * time.Second
	// notifyLookupTimeout bounds the session lookups of one broker event.
	notifyLookupTimeout = 2 * time.Second
	maxPresenceBody     = 1 << 10
	maxLocalAnswerBody  = 4 << 10
)

// SetNotifications installs the notification bus. The approval and question
// brokers then publish every request they open and close to it, the presence
// route records visible sessions on it, and GET /v1/local/events streams it.
// A nil bus turns the stream off (503).
func (s *Server) SetNotifications(bus *notify.Bus) {
	s.mu.Lock()
	s.notifications = bus
	s.mu.Unlock()
	if bus == nil {
		s.approvals.setObserver(nil)
		s.questions.setObserver(nil)
		return
	}
	observer := brokerNotifier{s: s}
	s.approvals.setObserver(observer)
	s.questions.setObserver(observer)
}

func (s *Server) notificationBus() *notify.Bus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notifications
}

func (s *Server) localHeartbeatInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.localHeartbeat > 0 {
		return s.localHeartbeat
	}
	return localEventsHeartbeat
}

// brokerNotifier turns broker requests into notification events.
type brokerNotifier struct{ s *Server }

func (n brokerNotifier) approvalOpened(id string, req core.ApprovalRequest, argument string) {
	bus := n.s.notificationBus()
	if bus == nil {
		return
	}
	target, info := n.s.notifyTarget(req.SessionID)
	agent := req.AgentName
	if agent == "" {
		agent = info.Agent
	}
	if agent == "" {
		agent = req.Label
	}
	bus.Publish(notify.ApprovalAsked(id, agent, req.ToolName, argument, target))
}

func (n brokerNotifier) approvalClosed(id, sessionID string, outcome core.ApprovalOutcome) {
	bus := n.s.notificationBus()
	if bus == nil {
		return
	}
	target, _ := n.s.notifyTarget(sessionID)
	bus.Publish(notify.ApprovalDecided(id, string(outcome), target))
}

func (n brokerNotifier) questionOpened(id string, req core.QuestionRequest) {
	bus := n.s.notificationBus()
	if bus == nil || len(req.Questions) == 0 {
		return
	}
	target, info := n.s.notifyTarget(req.SessionID)
	agent := req.AgentName
	if agent == "" {
		agent = info.Agent
	}
	first := req.Questions[0]
	text := first.Text
	if strings.TrimSpace(text) == "" {
		text = first.Header
	}
	bus.Publish(notify.QuestionAsked(id, agent, text, len(req.Questions)-1, target))
}

func (n brokerNotifier) questionClosed(id, sessionID string) {
	bus := n.s.notificationBus()
	if bus == nil {
		return
	}
	target, _ := n.s.notifyTarget(sessionID)
	bus.Publish(notify.QuestionAnswered(id, target))
}

// notifyTarget resolves where a banner of sessionID leads: its root session,
// or the workflow when the root runs on channel "workflow" (its address is the
// workflow id). It also returns the session's own info. A lookup failure
// leaves the session as its own root.
func (s *Server) notifyTarget(sessionID string) (notify.ApprovalTarget, core.SessionInfo) {
	target := notify.ApprovalTarget{SessionID: sessionID, RootID: sessionID}
	backend := s.backend
	if backend == nil || sessionID == "" {
		return target, core.SessionInfo{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), notifyLookupTimeout)
	defer cancel()
	info, err := lookupSessionInfo(ctx, backend, sessionID)
	if err != nil {
		return target, core.SessionInfo{}
	}
	root := info
	if info.ParentSessionID != "" {
		target.RootID = sessionRoot(ctx, backend, sessionID)
		if rootInfo, err := lookupSessionInfo(ctx, backend, target.RootID); err == nil {
			root = rootInfo
		}
	}
	if root.Channel == "workflow" && root.Address != "" {
		target.WorkflowID = root.Address
	}
	return target, info
}

// registerNotifyRoutes installs the presence route behind the browser session
// and CSRF checks, and the two daemon-only local routes behind the local
// secret and loopback checks (API-NTF-001 … API-NTF-003).
func (s *Server) registerNotifyRoutes() {
	s.Handle("POST /v1/sessions/{id}/presence", http.HandlerFunc(s.handleSessionPresence))
	// Registered without a method like the sign-in link; the handlers reject
	// a wrong method themselves.
	s.mux.HandleFunc(LocalEventsPath, s.handleLocalEvents)
	s.mux.HandleFunc(LocalApprovalsPath+"{id}", s.handleLocalApproval)
}

// checkLocal applies the local control checks of API-WLG-008: the daemon
// secret in X-Lana-Local-Secret (401) and a loopback caller (403).
func (s *Server) checkLocal(w http.ResponseWriter, r *http.Request) bool {
	secret := s.localSecretValue()
	got := r.Header.Get(LocalSecretHeader)
	if secret == "" || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "local control secret is missing or invalid")
		return false
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeAPIError(w, http.StatusForbidden, "forbidden_remote", "local control is loopback only")
		return false
	}
	return true
}

type presenceRequest struct {
	Visible *bool `json:"visible"`
}

// handleSessionPresence answers POST /v1/sessions/{id}/presence (API-NTF-003):
// the dashboard reports that a tab shows, or stopped showing, a session.
func (s *Server) handleSessionPresence(w http.ResponseWriter, r *http.Request) {
	var body presenceRequest
	if !decodeJSONObject(w, r, maxPresenceBody, &body) {
		return
	}
	if body.Visible == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "visible is required")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || len(id) > 128 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid session id")
		return
	}
	if bus := s.notificationBus(); bus != nil {
		bus.SetVisible(id, *body.Visible)
	}
	w.WriteHeader(http.StatusNoContent)
}

// localReady is the first event of the local stream.
type localReady struct {
	LastID string `json:"last_id"`
}

// handleLocalEvents answers GET /v1/local/events (API-NTF-001): an SSE stream
// of notification events for the menu bar app. ?after=<id> (or the
// Last-Event-ID header) replays the buffered events after that id. The first
// event is "ready" with the newest id; each notification is a "notify" event
// with an id line; a comment line keeps the connection alive every 20 s.
func (s *Server) handleLocalEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	if !s.checkLocal(w, r) {
		return
	}
	bus := s.notificationBus()
	if bus == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "notifications are not available")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	after := r.URL.Query().Get("after")
	if after == "" {
		after = r.Header.Get("Last-Event-ID")
	}
	replay, events, cancel := bus.Subscribe(after)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	last := bus.LastID()
	if len(replay) > 0 {
		last = replay[len(replay)-1].ID
	}
	if err := writeSSE(w, flusher, "ready", "", localReady{LastID: last}); err != nil {
		return
	}
	for _, event := range replay {
		if err := writeSSE(w, flusher, "notify", event.ID, event); err != nil {
			return
		}
	}
	heartbeat := time.NewTicker(s.localHeartbeatInterval())
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stopping():
			_ = writeSSE(w, flusher, "shutdown", "", struct{}{})
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case event := <-events:
			if err := writeSSE(w, flusher, "notify", event.ID, event); err != nil {
				return
			}
		}
	}
}

type localAnswerRequest struct {
	Outcome string `json:"outcome"`
}

// handleLocalApproval answers POST /v1/local/approvals/{id} (API-NTF-002):
// the Allow once and Reject actions of a banner. A broker id goes through the
// same broker as POST /v1/approvals/{id}; a "wf:<workflow>:<decision>" id
// answers a workflow approval decision with approve or deny.
func (s *Server) handleLocalApproval(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}
	if !s.checkLocal(w, r) {
		return
	}
	var body localAnswerRequest
	if !decodeJSONObject(w, r, maxLocalAnswerBody, &body) {
		return
	}
	outcome := core.ApprovalOutcome(strings.TrimSpace(body.Outcome))
	if outcome != core.OutcomeAllowedOnce && outcome != core.OutcomeRejected {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "outcome must be allowed_once or rejected")
		return
	}
	id := r.PathValue("id")
	if workflowID, decisionID, ok := notify.ParseWorkflowApprovalID(id); ok {
		s.answerWorkflowDecision(w, r, workflowID, decisionID, outcome)
		return
	}
	err := s.approvals.answer(id, outcome)
	switch {
	case errors.Is(err, errApprovalClosed):
		writeAPIError(w, http.StatusConflict, "approval_closed", "approval closed")
	case errors.Is(err, errApprovalNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "approval request not found")
	case err != nil:
		s.logger.Error("web: local approval answer", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	default:
		// The approval log has no answer source; the daemon log keeps it.
		s.logger.Info("web: approval answered", "source", "macos", "outcome", string(outcome))
		writeJSON(w, http.StatusOK, approvalAnswerResponse{Outcome: string(outcome)})
	}
}

func (s *Server) answerWorkflowDecision(w http.ResponseWriter, r *http.Request, workflowID, decisionID string, outcome core.ApprovalOutcome) {
	s.workflowsMu.RLock()
	backend := s.workflows
	s.workflowsMu.RUnlock()
	if backend == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "approval request not found")
		return
	}
	choice := "approve"
	if outcome == core.OutcomeRejected {
		choice = "deny"
	}
	_, err := backend.Answer(r.Context(), workflowID, decisionID, choice)
	switch {
	case errors.Is(err, workflow.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "approval request not found")
	case errors.Is(err, workflow.ErrDecisionClosed):
		writeAPIError(w, http.StatusConflict, "approval_closed", "approval closed")
	case errors.Is(err, workflow.ErrInvalidChoice):
		writeAPIError(w, http.StatusConflict, "not_approvable", "this decision has no approve or deny choice")
	case err != nil:
		s.logger.Error("web: local workflow decision", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	default:
		s.logger.Info("web: workflow decision answered", "source", "macos", "choice", choice)
		writeJSON(w, http.StatusOK, approvalAnswerResponse{Outcome: string(outcome)})
	}
}
