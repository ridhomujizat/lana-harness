package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/attach"
	"github.com/ridhomujizat/lana-harness/internal/toolfmt"
)

const (
	defaultSessionLimit = 50
	maxSessionLimit     = 50
	createSessionBody   = 64 << 10
	// previewLimit is the maximum length of the list preview (REQ-WEB-037).
	previewLimit = 120
)

// Derived session status values of REQ-WEB-037. The store keeps only idle and
// running (G-25); the other values are derived when the list is read.
const (
	SessionStatusRunning         = "running"
	SessionStatusWaitingApproval = "waiting_approval"
	SessionStatusWaitingInput    = "waiting_input"
	SessionStatusIdle            = "idle"
	SessionStatusFailed          = "failed"
)

// sessionStatuses are the valid status filter values. waiting_input is derived
// from an open question (REQ-ASK-011).
var sessionStatuses = map[string]bool{
	SessionStatusRunning:         true,
	SessionStatusWaitingApproval: true,
	SessionStatusWaitingInput:    true,
	SessionStatusIdle:            true,
	SessionStatusFailed:          true,
}

var (
	// ErrSessionNotFound is returned by a Backend when the id or prefix does
	// not identify exactly one session.
	ErrSessionNotFound = errors.New("session not found")
	// ErrUnknownAgent is returned by a Backend when the agent is not configured.
	ErrUnknownAgent = errors.New("unknown agent")
	// ErrInvalidModel is returned by a Backend when a model cannot be selected.
	ErrInvalidModel = errors.New("invalid model")
	// ErrTeamModel is returned by a Backend when a team session's model is
	// changed. A team uses each agent's model (API-TEAM-005, REQ-TEAM-022).
	ErrTeamModel = errors.New("teams use each agent's model")
	// ErrInvalidPolicy is returned by a Backend for an unknown policy value.
	ErrInvalidPolicy = errors.New("invalid policy")
	// ErrInvalidState is returned by a Backend when a write is refused because
	// a turn is running.
	ErrInvalidState = errors.New("session is not in a valid state for this operation")
	// ErrDraining is returned by a Backend while the daemon drains; the handler
	// answers 503 daemon_draining (REQ-TFB-002, API-TFB-008).
	ErrDraining = errors.New("daemon is draining")
	// ErrTurnRunning is returned by a Backend when Continue is requested while a
	// turn runs; the handler answers 409 turn_running (API-TFB-003).
	ErrTurnRunning = errors.New("turn is running")
	// ErrNothingToContinue is returned by a Backend when Continue is requested
	// after a normal end; the handler answers 409 nothing_to_continue
	// (API-TFB-003).
	ErrNothingToContinue = errors.New("nothing to continue")
)

// SendResult is the outcome of Backend.Send.
type SendResult struct {
	Turn   string
	TurnID string
}

// CompactResult is the outcome of Backend.Compact.
type CompactResult struct {
	TokensBefore int64
	TokensAfter  int64
}

// sessionChannels are the channel values accepted by the list filter.
var sessionChannels = map[string]bool{
	"cli":      true,
	"telegram": true,
	"library":  true,
	"web":      true,
}

// SessionFilter limits Backend.Sessions. Before is the updated_at cursor and
// zero means the first page. Q, Statuses, and Agent follow API-WEB-003 rev 2;
// status is derived from the log when the list is read (G-25). TopLevelOnly
// excludes child sessions (API-TEAM-005).
type SessionFilter struct {
	Channel      string
	Q            string
	Statuses     []string
	Agent        string
	Limit        int
	Before       int64
	TopLevelOnly bool
}

// Team is the team information of a session (API-TEAM-005). The lead and
// members are agent names; the API exposes member names only.
type Team struct {
	Name    string   `json:"name"`
	Lead    string   `json:"lead"`
	Members []string `json:"members"`
}

// SessionPage is one page of sessions. NextBefore is nil when no further page
// exists.
type SessionPage struct {
	Sessions   []Session
	NextBefore *int64
}

// Session is a session plus the fields of the detail view. Team is set when
// the session's agent or selected agent is a team (API-TEAM-005).
type Session struct {
	core.SessionInfo
	Model           string
	Policy          string
	ReasoningEffort string
	Team            *Team
	TurnRunning     bool
	InheritedSeq    int64
}

// EventPage is the result of the events endpoint.
type EventPage struct {
	Events  []core.SessionEvent
	LastSeq int64
}

// MCPStatus is the state of one MCP server.
type MCPStatus struct {
	Name  string
	State string
	Error string
}

// Agent is one row of the agent picker. Kind is "agent" or "team"; a team row
// carries its lead and members, and its policy is the lead's (API-TEAM-006).
type Agent struct {
	Name        string
	Kind        string
	Model       string
	Policy      string
	Lead        string
	Members     []string
	Description string
	Skills      []string
	MCP         []string
	MCPStatus   []MCPStatus
}

// Holder values for the session in_use field and the status event.
const (
	HolderNone  = "none"
	HolderSelf  = "self"
	HolderOther = "other"
)

// Holder describes which process holds the session lease.
type Holder struct {
	State string
	PID   int
}

// Backend is the runtime port used by the session handlers. It follows the
// channels/telegram port pattern: the web package never imports lana.
type Backend interface {
	Sessions(ctx context.Context, filter SessionFilter) (SessionPage, error)
	Session(ctx context.Context, id string) (Session, error)
	Events(ctx context.Context, id string, fromSeq int64) (EventPage, error)
	CreateSession(ctx context.Context, agent, model string) (Session, error)
	Agents() []Agent

	Send(ctx context.Context, id, text string, attachments []string) (SendResult, error)
	// SaveAttachment stores one uploaded file in the session workspace
	// (API-ATT-001). r is streamed, never buffered whole.
	SaveAttachment(ctx context.Context, id, name string, r io.Reader) (attach.Info, error)
	// AttachmentPath resolves a workspace-relative attachments/ path to an
	// absolute file path (API-ATT-002, API-ATT-003).
	AttachmentPath(ctx context.Context, id, rel string) (string, error)
	// Continue starts a continue turn for the session (API-TFB-003).
	Continue(ctx context.Context, id string) (SendResult, error)
	Cancel(ctx context.Context, id string) (bool, error)
	Compact(ctx context.Context, id string) (CompactResult, error)
	Fork(ctx context.Context, id string) (Session, error)
	SetAgent(ctx context.Context, id, agent string) error
	SetModel(ctx context.Context, id, model string) error
	SetReasoningEffort(ctx context.Context, id, effort string) error
	SetPolicy(ctx context.Context, id, policy string) error

	// Subscribe returns the live frames of a turn running in this process.
	Subscribe(ctx context.Context, id string) (*core.Subscription[core.LiveFrame], error)
	// TurnActive reports whether a turn is running for the session.
	TurnActive(id string) bool
	// Holder reports the lease holder without taking the lease.
	Holder(ctx context.Context, id string) (Holder, error)
}

// SetBackend installs the runtime port used by the session handlers. Call it
// before Start; a nil backend makes the endpoints return 500. A backend that
// also implements a settings port installs that port too.
func (s *Server) SetBackend(backend Backend) {
	s.backend = backend
	if store, ok := backend.(ConfigStore); ok {
		s.configStore = store
	}
	if store, ok := backend.(SecretStore); ok {
		s.secretStore = store
	}
	if store, ok := backend.(AgentFileStore); ok {
		s.agentFileStore = store
	}
	if store, ok := backend.(SkillStore); ok {
		s.skillStore = store
	}
	if port, ok := backend.(JobBackend); ok {
		s.jobBackend = port
	}
	if port, ok := backend.(DashboardBackend); ok {
		s.dashboardBackend = port
	}
	if port, ok := backend.(KnowledgeBackend); ok {
		s.knowledgeBackend = port
	}
}

// registerSessionRoutes registers the T-003 routes through Handle so the
// host, origin, cookie, and CSRF checks apply.
func (s *Server) registerSessionRoutes() {
	s.Handle("GET /v1/sessions", http.HandlerFunc(s.handleListSessions))
	s.Handle("POST /v1/sessions", http.HandlerFunc(s.handleCreateSession))
	s.Handle("GET /v1/sessions/{id}", http.HandlerFunc(s.handleGetSession))
	s.Handle("PATCH /v1/sessions/{id}", http.HandlerFunc(s.handlePatchSession))
	s.Handle("GET /v1/sessions/{id}/events", http.HandlerFunc(s.handleSessionEvents))
	s.Handle("GET /v1/sessions/{id}/usage", http.HandlerFunc(s.handleSessionUsage))
	s.Handle("POST /v1/sessions/{id}/compact", http.HandlerFunc(s.handleCompactSession))
	s.Handle("POST /v1/sessions/{id}/fork", http.HandlerFunc(s.handleForkSession))
	s.registerJobRoutes()
}

type sessionJSON struct {
	ID        string `json:"id"`
	Channel   string `json:"channel"`
	Address   string `json:"address"`
	Agent     string `json:"agent"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Preview   string `json:"preview"`
	TurnCount int64  `json:"turn_count"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	InUse     string `json:"in_use"`
	HolderPID *int   `json:"holder_pid,omitempty"`
	// Team is null unless the session's agent is a team (API-TEAM-005).
	Team *Team `json:"team"`
}

type sessionDetailJSON struct {
	sessionJSON
	Model           string `json:"model"`
	Policy          string `json:"policy"`
	ReasoningEffort string `json:"reasoning_effort"`
	Workspace       string `json:"workspace"`
	ParentSessionID string `json:"parent_session_id"`
	InheritedSeq    int64  `json:"inherited_seq,omitempty"`
	TurnRunning     bool   `json:"turn_running"`
}

type eventJSON struct {
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	TurnID    string          `json:"turn_id"`
	CreatedAt int64           `json:"created_at"`
	Payload   json.RawMessage `json:"payload"`
}

// agentFacet is one entry of facets.agent (API-TEAM-005). Kind is "agent" for
// an agent and "team" for a team.
type agentFacet struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// sessionFacets are the filter counts of API-WEB-003 rev 2. They count the
// sessions that match q and no other filter (Q-023). Agent mixes agents and
// teams, sorted by name (API-TEAM-005).
type sessionFacets struct {
	Channel map[string]int `json:"channel"`
	Status  map[string]int `json:"status"`
	Agent   []agentFacet   `json:"agent"`
}

type listSessionsResponse struct {
	Sessions   []sessionJSON `json:"sessions"`
	NextBefore *int64        `json:"next_before"`
	Facets     sessionFacets `json:"facets"`
}

// sessionView is one session with the status and preview derived from its log.
type sessionView struct {
	info    core.SessionInfo
	status  string
	preview string
	team    *Team
}

type eventsResponse struct {
	Events  []eventJSON `json:"events"`
	LastSeq int64       `json:"last_seq"`
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	filter, err := parseSessionFilter(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	// Child sessions are never returned by the list in v1 (API-TEAM-005).
	filter.TopLevelOnly = true
	// Facets always count every session that matches q, so the handler reads
	// the whole list once and derives status and preview for each row (G-25).
	corpus, err := s.allSessionViews(r.Context(), backend)
	if err != nil {
		s.writeBackendError(w, "list sessions", err)
		return
	}
	response := listSessionsResponse{
		Sessions: make([]sessionJSON, 0, filter.Limit),
		Facets:   countSessionFacets(filterSessionViews(corpus, SessionFilter{Q: filter.Q})),
	}
	views, next, err := s.sessionListPage(r.Context(), backend, filter, corpus)
	if err != nil {
		s.writeBackendError(w, "list sessions", err)
		return
	}
	response.NextBefore = next
	for _, view := range views {
		holder, ok := s.sessionHolder(w, r, backend, view.info.ID)
		if !ok {
			return
		}
		row := sessionJSONFromInfo(view.info, holder, view.status)
		row.Preview = view.preview
		row.Team = view.team
		response.Sessions = append(response.Sessions, row)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	session, err := backend.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeBackendError(w, "get session", err)
		return
	}
	holder, ok := s.sessionHolder(w, r, backend, session.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sessionDetailFromSession(session, holder))
}

func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	fromSeq, err := parseFromSeq(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	page, err := backend.Events(r.Context(), r.PathValue("id"), fromSeq)
	if err != nil {
		s.writeBackendError(w, "session events", err)
		return
	}
	response := eventsResponse{
		Events:  make([]eventJSON, 0, len(page.Events)),
		LastSeq: page.LastSeq,
	}
	for _, event := range page.Events {
		response.Events = append(response.Events, eventJSON{
			Seq:       event.Seq,
			Kind:      event.Kind,
			TurnID:    event.TurnID,
			CreatedAt: event.CreatedAt,
			Payload:   publicEventPayload(event.Kind, event.Payload),
		})
	}
	writeJSON(w, http.StatusOK, response)
}

// sessionUsageResponse is the aggregated token usage of a session's log. The
// cached_input_tokens field is the part served from the provider prefix cache
// (REQ-LTC-005).
type sessionUsageResponse struct {
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
}

func (s *Server) handleSessionUsage(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	page, err := backend.Events(r.Context(), r.PathValue("id"), 0)
	if err != nil {
		s.writeBackendError(w, "session usage", err)
		return
	}
	var usage core.Usage
	for _, event := range page.Events {
		if event.Kind != core.KindStepEnd {
			continue
		}
		var payload core.StepEnd
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		usage.InputTokens += payload.Usage.InputTokens
		usage.OutputTokens += payload.Usage.OutputTokens
		usage.CachedInputTokens += payload.Usage.CachedInputTokens
	}
	writeJSON(w, http.StatusOK, sessionUsageResponse{
		InputTokens:       usage.InputTokens,
		OutputTokens:      usage.OutputTokens,
		CachedInputTokens: usage.CachedInputTokens,
	})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Agent string `json:"agent"`
		Model string `json:"model"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, createSessionBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object with agent and model")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return
	}
	agent := strings.TrimSpace(body.Agent)
	model := strings.TrimSpace(body.Model)
	if agent == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "agent is required")
		return
	}
	if model != "" && !validModel(model) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "model must be <provider>:<model>")
		return
	}
	session, err := backend.CreateSession(r.Context(), agent, model)
	if err != nil {
		s.writeBackendError(w, "create session", err)
		return
	}
	holder, ok := s.sessionHolder(w, r, backend, session.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, sessionDetailFromSession(session, holder))
}

func (s *Server) handlePatchSession(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Agent           *string `json:"agent"`
		Model           *string `json:"model"`
		Policy          *string `json:"policy"`
		ReasoningEffort *string `json:"reasoning_effort"`
		ConfirmTrust    bool    `json:"confirm_trust"`
	}
	if !decodeJSONObject(w, r, createSessionBody, &body) {
		return
	}
	if body.Agent == nil && body.Model == nil && body.Policy == nil && body.ReasoningEffort == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "at least one of agent, model, policy, reasoning_effort is required")
		return
	}
	if body.Agent != nil {
		agent := strings.TrimSpace(*body.Agent)
		if agent == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "agent must not be empty")
			return
		}
		body.Agent = &agent
	}
	if body.Model != nil {
		model := strings.TrimSpace(*body.Model)
		if !validModel(model) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "model must be <provider>:<model>")
			return
		}
		body.Model = &model
	}
	if body.Policy != nil {
		policy := strings.TrimSpace(*body.Policy)
		switch policy {
		case string(core.PolicyAsk), string(core.PolicyNever):
		case string(core.PolicyTrust):
			if !body.ConfirmTrust {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "confirm_trust must be true for policy trust")
				return
			}
		default:
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "policy must be one of ask, never, trust")
			return
		}
		body.Policy = &policy
	}
	if body.ReasoningEffort != nil {
		effort := strings.TrimSpace(*body.ReasoningEffort)
		// An empty value clears the session effort (REQ-MDL-001).
		if effort != "" && !validReasoningEffort(effort) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "reasoning_effort must be one of off, low, high, max")
			return
		}
		body.ReasoningEffort = &effort
	}

	id := r.PathValue("id")
	if body.Agent != nil {
		if err := backend.SetAgent(r.Context(), id, *body.Agent); err != nil {
			s.writeBackendError(w, "set session agent", err)
			return
		}
	}
	if body.Model != nil {
		if err := backend.SetModel(r.Context(), id, *body.Model); err != nil {
			s.writeBackendError(w, "set session model", err)
			return
		}
	}
	if body.ReasoningEffort != nil {
		if err := backend.SetReasoningEffort(r.Context(), id, *body.ReasoningEffort); err != nil {
			s.writeBackendError(w, "set session reasoning effort", err)
			return
		}
	}
	if body.Policy != nil {
		if err := backend.SetPolicy(r.Context(), id, *body.Policy); err != nil {
			s.writeBackendError(w, "set session policy", err)
			return
		}
	}
	session, err := backend.Session(r.Context(), id)
	if err != nil {
		s.writeBackendError(w, "get session", err)
		return
	}
	holder, ok := s.sessionHolder(w, r, backend, session.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sessionDetailFromSession(session, holder))
}

func (s *Server) handleCompactSession(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	result, err := backend.Compact(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeBackendError(w, "compact session", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		TokensBefore int64 `json:"tokens_before"`
		TokensAfter  int64 `json:"tokens_after"`
	}{TokensBefore: result.TokensBefore, TokensAfter: result.TokensAfter})
}

func (s *Server) handleForkSession(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	session, err := backend.Fork(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeBackendError(w, "fork session", err)
		return
	}
	holder, ok := s.sessionHolder(w, r, backend, session.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, sessionDetailFromSession(session, holder))
}

func (s *Server) backendOrError(w http.ResponseWriter) Backend {
	if s.backend == nil {
		s.logger.Error("web: backend is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return nil
	}
	return s.backend
}

func (s *Server) writeBackendError(w http.ResponseWriter, operation string, err error) {
	var held *core.LeaseHeldError
	switch {
	case errors.Is(err, ErrSessionNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "session not found")
	case errors.As(err, &held):
		writeAPIErrorDetails(w, http.StatusConflict, "session_in_use", "session is in use by another process", []map[string]any{{"pid": held.PID}})
	case errors.Is(err, ErrUnknownAgent):
		writeAPIError(w, http.StatusBadRequest, "unknown_agent", "unknown agent")
	case errors.Is(err, ErrTeamModel):
		// A team uses each agent's model (REQ-TEAM-022, API-TEAM-005).
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "teams use each agent's model")
	case errors.Is(err, ErrInvalidModel):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "model must be <provider>:<model>")
	case errors.Is(err, ErrInvalidPolicy):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "policy must be one of ask, never, trust")
	case errors.Is(err, ErrInvalidState):
		writeAPIError(w, http.StatusConflict, "invalid_state", "the operation is not allowed while a turn is running")
	case errors.Is(err, ErrDraining):
		// API-TFB-008: the daemon is shutting down and starts no turn.
		writeAPIError(w, http.StatusServiceUnavailable, "daemon_draining", "lana is restarting. Try again in a moment.")
	case errors.Is(err, ErrTurnRunning):
		// API-TFB-003: Continue is refused while a turn runs.
		writeAPIError(w, http.StatusConflict, "turn_running", "a turn is already running")
	case errors.Is(err, ErrNothingToContinue):
		// API-TFB-003: the last turn ended normally.
		writeAPIError(w, http.StatusConflict, "nothing_to_continue", "the last turn ended normally")
	default:
		s.logger.Error("web: "+operation, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func parseSessionFilter(r *http.Request) (SessionFilter, error) {
	filter := SessionFilter{Limit: defaultSessionLimit}
	query := r.URL.Query()
	if channel := query.Get("channel"); channel != "" {
		if !sessionChannels[channel] {
			return SessionFilter{}, errors.New("channel must be one of cli, telegram, library, web")
		}
		filter.Channel = channel
	}
	if values, ok := query["limit"]; ok {
		limit, err := strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > maxSessionLimit {
			return SessionFilter{}, errors.New("limit must be an integer between 1 and 50")
		}
		filter.Limit = limit
	}
	if values, ok := query["before"]; ok {
		before, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || before < 0 {
			return SessionFilter{}, errors.New("before must be a non-negative integer in milliseconds")
		}
		filter.Before = before
	}
	filter.Q = strings.TrimSpace(query.Get("q"))
	for _, raw := range query["status"] {
		for _, value := range strings.Split(raw, ",") {
			status := strings.TrimSpace(value)
			if status == "" {
				continue
			}
			if !sessionStatuses[status] {
				return SessionFilter{}, errors.New("status must be a comma list of running, waiting_approval, waiting_input, idle, failed")
			}
			if !containsString(filter.Statuses, status) {
				filter.Statuses = append(filter.Statuses, status)
			}
		}
	}
	filter.Agent = strings.TrimSpace(query.Get("agent"))
	return filter, nil
}

// allSessionViews reads every session page and derives status and preview for
// each row. The store has no before cursor, so the pages walk NextBefore.
func (s *Server) allSessionViews(ctx context.Context, backend Backend) ([]sessionView, error) {
	filter := SessionFilter{Limit: defaultSessionLimit, TopLevelOnly: true}
	seen := map[string]bool{}
	views := make([]sessionView, 0)
	for {
		page, err := backend.Sessions(ctx, filter)
		if err != nil {
			return nil, err
		}
		for _, session := range page.Sessions {
			info := session.SessionInfo
			if seen[info.ID] {
				continue
			}
			seen[info.ID] = true
			view, err := s.sessionView(ctx, backend, session)
			if err != nil {
				return nil, err
			}
			views = append(views, view)
		}
		if page.NextBefore == nil {
			break
		}
		next := *page.NextBefore
		// Stop on a cursor that cannot make progress so a buggy backend cannot
		// loop the handler forever.
		if next <= 0 || (filter.Before > 0 && next >= filter.Before) {
			break
		}
		filter.Before = next
	}
	return views, nil
}

// sessionView derives one row from the session log.
func (s *Server) sessionView(ctx context.Context, backend Backend, session Session) (sessionView, error) {
	info := session.SessionInfo
	page, err := backend.Events(ctx, info.ID, 0)
	if err != nil {
		return sessionView{}, err
	}
	status, preview := deriveSessionStatusPreview(info, page.Events, backend.TurnActive(info.ID), s.questions != nil && s.questions.hasOpenQuestion(info.ID))
	return sessionView{info: info, status: status, preview: preview, team: session.Team}, nil
}

// sessionListPage returns the page of rows and its next_before cursor. Without
// q, status, or agent it uses the backend paging path unchanged; otherwise it
// filters the full corpus in memory, because status is derived at read time.
func (s *Server) sessionListPage(ctx context.Context, backend Backend, filter SessionFilter, corpus []sessionView) ([]sessionView, *int64, error) {
	if filter.Q == "" && len(filter.Statuses) == 0 && filter.Agent == "" {
		page, err := backend.Sessions(ctx, SessionFilter{
			Channel:      filter.Channel,
			Limit:        filter.Limit,
			Before:       filter.Before,
			TopLevelOnly: filter.TopLevelOnly,
		})
		if err != nil {
			return nil, nil, err
		}
		byID := make(map[string]sessionView, len(corpus))
		for _, view := range corpus {
			byID[view.info.ID] = view
		}
		views := make([]sessionView, 0, len(page.Sessions))
		for _, session := range page.Sessions {
			view, ok := byID[session.ID]
			if !ok {
				view, err = s.sessionView(ctx, backend, session)
				if err != nil {
					return nil, nil, err
				}
			}
			view.info = session.SessionInfo
			view.team = session.Team
			views = append(views, view)
		}
		return views, page.NextBefore, nil
	}
	views := filterSessionViews(corpus, filter)
	list, next := pageSessionViews(views, filter)
	return list, next, nil
}

// filterSessionViews applies q, status, agent, and channel. An empty filter
// value means no filter for that field.
func filterSessionViews(views []sessionView, filter SessionFilter) []sessionView {
	query := strings.ToLower(filter.Q)
	statuses := make(map[string]bool, len(filter.Statuses))
	for _, status := range filter.Statuses {
		statuses[status] = true
	}
	out := make([]sessionView, 0, len(views))
	for _, view := range views {
		if filter.Channel != "" && view.info.Channel != filter.Channel {
			continue
		}
		if filter.Agent != "" && view.info.Agent != filter.Agent {
			continue
		}
		if len(statuses) > 0 && !statuses[view.status] {
			continue
		}
		if query != "" &&
			!strings.Contains(strings.ToLower(view.info.Title), query) &&
			!strings.Contains(strings.ToLower(view.preview), query) {
			continue
		}
		out = append(out, view)
	}
	return out
}

// pageSessionViews applies the existing updated_at cursor and 50-row page rule
// to the filtered rows.
func pageSessionViews(views []sessionView, filter SessionFilter) ([]sessionView, *int64) {
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].info.UpdatedAt != views[j].info.UpdatedAt {
			return views[i].info.UpdatedAt > views[j].info.UpdatedAt
		}
		return views[i].info.ID > views[j].info.ID
	})
	list := make([]sessionView, 0, len(views))
	for _, view := range views {
		if filter.Before > 0 && view.info.UpdatedAt >= filter.Before {
			continue
		}
		list = append(list, view)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultSessionLimit
	}
	var next *int64
	if len(list) > limit {
		at := list[limit-1].info.UpdatedAt
		next = &at
		list = list[:limit]
	}
	return list, next
}

func countSessionFacets(views []sessionView) sessionFacets {
	facets := sessionFacets{
		Channel: map[string]int{"web": 0, "cli": 0, "telegram": 0},
		Status: map[string]int{
			SessionStatusRunning:         0,
			SessionStatusWaitingApproval: 0,
			SessionStatusWaitingInput:    0,
			SessionStatusIdle:            0,
			SessionStatusFailed:          0,
		},
		Agent: []agentFacet{},
	}
	kinds := map[string]string{}
	counts := map[string]int{}
	for _, view := range views {
		if _, ok := facets.Channel[view.info.Channel]; ok {
			facets.Channel[view.info.Channel]++
		}
		facets.Status[view.status]++
		if view.info.Agent == "" {
			continue
		}
		kind := "agent"
		if view.team != nil {
			kind = "team"
		}
		kinds[view.info.Agent] = kind
		counts[view.info.Agent]++
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		facets.Agent = append(facets.Agent, agentFacet{Name: name, Kind: kinds[name], Count: counts[name]})
	}
	return facets
}

// deriveSessionStatusPreview derives the status and preview of one session
// from its event log (G-25). The store status only distinguishes idle and
// running. questionOpen reports an open request the broker still holds, which
// covers a request whose log write has not been polled yet.
func deriveSessionStatusPreview(info core.SessionInfo, events []core.SessionEvent, turnActive, questionOpen bool) (string, string) {
	calls := map[string]core.ToolCallPayload{}
	activeCalls := make([]string, 0)
	var openApproval core.ApprovalAsked
	approvalOpen := false
	openQuestionID := ""
	lastAnswer := ""
	var lastTurnEnd core.TurnEnd
	hasTurnEnd := false

	for _, event := range events {
		switch event.Kind {
		case core.KindToolCall:
			var payload core.ToolCallPayload
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			calls[payload.CallID] = payload
			activeCalls = append(activeCalls, payload.CallID)
		case core.KindToolResult:
			var payload core.ToolResultPayload
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			delete(calls, payload.CallID)
			activeCalls = removeString(activeCalls, payload.CallID)
		case core.KindApprovalAsked:
			var payload core.ApprovalAsked
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			openApproval = payload
			approvalOpen = true
		case core.KindApprovalDecided:
			var payload core.ApprovalDecided
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			if approvalOpen && payload.RequestID == openApproval.RequestID {
				approvalOpen = false
				openApproval = core.ApprovalAsked{}
			}
		case core.KindQuestionAsked:
			var payload core.QuestionAsked
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			openQuestionID = payload.RequestID
		case core.KindQuestionAnswered:
			var payload core.QuestionAnswered
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			if payload.RequestID == openQuestionID {
				openQuestionID = ""
			}
		case core.KindAssistantMessage:
			var payload core.AssistantMessage
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			if text := assistantText(payload.Content); text != "" {
				lastAnswer = text
			}
		case core.KindTurnEnd:
			var payload core.TurnEnd
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			lastTurnEnd = payload
			hasTurnEnd = true
		}
	}

	running := turnActive || info.Status == SessionStatusRunning
	failed := hasTurnEnd && (lastTurnEnd.Reason == "error" || lastTurnEnd.Error != nil)
	questionWaiting := questionOpen || openQuestionID != ""
	status := SessionStatusIdle
	switch {
	case approvalOpen || info.Status == SessionStatusWaitingApproval:
		status = SessionStatusWaitingApproval
	case questionWaiting || info.Status == SessionStatusWaitingInput:
		status = SessionStatusWaitingInput
	case running:
		status = SessionStatusRunning
	case failed || info.Status == SessionStatusFailed:
		status = SessionStatusFailed
	}

	var preview string
	switch {
	case approvalOpen:
		preview = approvalPreview(openApproval, calls)
	case running:
		preview = runningPreview(activeCalls, calls)
		if preview == "" {
			preview = lastAnswer
		}
	default:
		preview = lastAnswer
		if preview == "" && failed && lastTurnEnd.Error != nil {
			preview = lastTurnEnd.Error.Message
		}
	}
	return status, cutPreview(preview)
}

func approvalPreview(approval core.ApprovalAsked, calls map[string]core.ToolCallPayload) string {
	tool := strings.TrimSpace(approval.Tool)
	argument := ""
	if call, ok := calls[approval.CallID]; ok {
		if tool == "" {
			tool = call.Name
		}
		argument = toolfmt.PrimaryArg(call.Name, call.Input)
	}
	text := "Waiting for your approval: " + tool
	if argument != "" {
		text += " " + argument
	}
	return text
}

func runningPreview(activeCalls []string, calls map[string]core.ToolCallPayload) string {
	for i := len(activeCalls) - 1; i >= 0; i-- {
		call, ok := calls[activeCalls[i]]
		if !ok {
			continue
		}
		text := "Running: " + call.Name
		if argument := toolfmt.PrimaryArg(call.Name, call.Input); argument != "" {
			text += " " + argument
		}
		return text
	}
	return ""
}

// assistantText returns the text blocks of an assistant message as one string.
// Thinking blocks are not user-facing text, so they are skipped (REQ-MDL-006).
func assistantText(blocks []core.ContentBlock) string {
	return strings.TrimSpace(core.TextContent(blocks))
}

func cutPreview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	return toolfmt.Cut(text, previewLimit)
}

func removeString(values []string, remove string) []string {
	for i, value := range values {
		if value == remove {
			return append(values[:i], values[i+1:]...)
		}
	}
	return values
}

func parseFromSeq(r *http.Request) (int64, error) {
	values, ok := r.URL.Query()["from_seq"]
	if !ok {
		return 0, nil
	}
	fromSeq, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || fromSeq < 0 {
		return 0, errors.New("from_seq must be a non-negative integer")
	}
	return fromSeq, nil
}

func validModel(model string) bool {
	provider, name, ok := strings.Cut(model, ":")
	return ok && provider != "" && name != ""
}

// sessionHolder returns the lease holder for one session of a read view.
func (s *Server) sessionHolder(w http.ResponseWriter, r *http.Request, backend Backend, id string) (Holder, bool) {
	holder, err := backend.Holder(r.Context(), id)
	if err != nil {
		s.writeBackendError(w, "session holder", err)
		return Holder{}, false
	}
	return holder, true
}

func sessionJSONFromInfo(info core.SessionInfo, holder Holder, status string) sessionJSON {
	row := sessionJSON{
		ID:        info.ID,
		Channel:   info.Channel,
		Address:   info.Address,
		Agent:     info.Agent,
		Title:     info.Title,
		Status:    status,
		TurnCount: info.TurnCount,
		CreatedAt: info.CreatedAt,
		UpdatedAt: info.UpdatedAt,
		InUse:     holder.State,
	}
	if holder.State == HolderOther {
		pid := holder.PID
		row.HolderPID = &pid
	}
	return row
}

func sessionDetailFromSession(session Session, holder Holder) sessionDetailJSON {
	row := sessionJSONFromInfo(session.SessionInfo, holder, session.Status)
	row.Team = session.Team
	return sessionDetailJSON{
		sessionJSON:     row,
		Model:           session.Model,
		Policy:          session.Policy,
		ReasoningEffort: session.ReasoningEffort,
		Workspace:       session.Workspace,
		ParentSessionID: session.ParentSessionID,
		InheritedSeq:    session.InheritedSeq,
		TurnRunning:     session.TurnRunning,
	}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// decodeJSONObject decodes one JSON object into dst and rejects unknown fields
// and trailing data. It writes the error response and returns false on failure.
func decodeJSONObject(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
