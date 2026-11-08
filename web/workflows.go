package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

const (
	// workflowBodyLimit is the largest accepted /v1/workflows body and the
	// largest accepted webhook body (API-WF-020).
	workflowBodyLimit = 1 << 20
	// workflowDefaultRunLimit and workflowMaxRunLimit bound the runs page
	// (API-WF-012).
	workflowDefaultRunLimit = 50
	workflowMaxRunLimit     = 200
)

// WorkflowBackend is the subset of *workflow.Service the web handlers use
// (spec #33, design §"Web (web/workflows.go)"). It is defined in web so the
// package never imports the runtime; the service satisfies it. The daemon
// installs it with SetWorkflows.
type WorkflowBackend interface {
	// Subscribe returns the change stream and its unsubscribe function
	// (API-WF-019).
	Subscribe() (<-chan workflow.Change, func())
	Overview(ctx context.Context) (workflow.Overview, error)
	Get(ctx context.Context, id string) (workflow.Detail, error)
	Create(ctx context.Context, in workflow.CreateInput) (workflow.Detail, error)
	Update(ctx context.Context, id string, in workflow.UpdateInput) (workflow.Detail, error)
	Delete(ctx context.Context, id string) error
	Versions(ctx context.Context, id string) ([]workflow.Version, error)
	Restore(ctx context.Context, id string, version int) (workflow.Detail, error)
	LockPreview(ctx context.Context, id string) ([]workflow.LockTool, error)
	Activate(ctx context.Context, id string, version int) (workflow.Detail, error)
	Pause(ctx context.Context, id string) (workflow.Detail, error)
	Resume(ctx context.Context, id string) (workflow.Detail, error)
	StartRun(ctx context.Context, id, trigger string, input json.RawMessage) (workflow.Run, error)
	Runs(ctx context.Context, id string, limit, before int) ([]workflow.Run, error)
	// RunDetail accepts a run id or a run number (API-WF-013).
	RunDetail(ctx context.Context, id, run string) (workflow.RunDetail, error)
	Cancel(ctx context.Context, id, run string) (workflow.Run, error)
	Answer(ctx context.Context, id, decisionID, choice string) (workflow.Run, error)
	State(ctx context.Context, id string) ([]workflow.StateEntry, error)
	SetState(ctx context.Context, id, key string, value json.RawMessage) (workflow.StateEntry, error)
	// DeleteState clears all entries when key is empty (API-WF-016).
	DeleteState(ctx context.Context, id, key string) error
	Chat(ctx context.Context, id string) ([]workflow.ChatMessage, error)
	Assist(ctx context.Context, id string, baseVersion int, message string) (workflow.AssistResult, error)
	RotateWebhook(ctx context.Context, id string) (string, error)
	// Webhook authenticates an inbound hook and starts a run (API-WF-020).
	Webhook(ctx context.Context, id, token string, body []byte) (workflow.Run, error)
	// Command matches a chat command and starts a run (API-WF-021).
	Command(ctx context.Context, channel, text string) (workflow.CommandResult, bool)
	// IsWorkflowSession reports whether a session runs on channel "workflow".
	IsWorkflowSession(sessionID string) bool
}

// SetWorkflows installs the workflow port. Call it before Start; a nil port
// makes the /v1/workflows routes answer 503 workflows_unavailable.
func (s *Server) SetWorkflows(b WorkflowBackend) {
	s.workflowsMu.Lock()
	defer s.workflowsMu.Unlock()
	s.workflows = b
}

func (s *Server) workflowsBackend() WorkflowBackend {
	s.workflowsMu.RLock()
	defer s.workflowsMu.RUnlock()
	return s.workflows
}

func (s *Server) workflowsOrError(w http.ResponseWriter) WorkflowBackend {
	backend := s.workflowsBackend()
	if backend == nil {
		s.logger.Error("web: workflow backend is not configured")
		writeAPIError(w, http.StatusServiceUnavailable, "workflows_unavailable", "workflows are not available")
		return nil
	}
	return backend
}

// registerWorkflowRoutes registers API-WF-001 through API-WF-021 (REQ-WF-001
// through REQ-WF-011). The /v1 routes go through Handle so the auth, Host,
// Origin, and CSRF checks apply. API-WF-020 is registered on the raw mux: a
// webhook carries no session cookie, CSRF token, Origin, or matching Host.
func (s *Server) registerWorkflowRoutes() {
	s.mux.Handle("POST /hooks/{id}", http.HandlerFunc(s.handleWorkflowWebhook))

	s.Handle("GET /v1/workflows", http.HandlerFunc(s.handleWorkflowList))
	s.Handle("GET /v1/workflows/stream", http.HandlerFunc(s.handleWorkflowStream))
	s.Handle("POST /v1/workflows", http.HandlerFunc(s.handleWorkflowCreate))
	s.Handle("GET /v1/workflows/{id}", http.HandlerFunc(s.handleWorkflowGet))
	s.Handle("PUT /v1/workflows/{id}", http.HandlerFunc(s.handleWorkflowUpdate))
	s.Handle("DELETE /v1/workflows/{id}", http.HandlerFunc(s.handleWorkflowDelete))
	s.Handle("GET /v1/workflows/{id}/versions", http.HandlerFunc(s.handleWorkflowVersions))
	s.Handle("POST /v1/workflows/{id}/restore", http.HandlerFunc(s.handleWorkflowRestore))
	s.Handle("GET /v1/workflows/{id}/lock", http.HandlerFunc(s.handleWorkflowLock))
	s.Handle("POST /v1/workflows/{id}/activate", http.HandlerFunc(s.handleWorkflowActivate))
	s.Handle("POST /v1/workflows/{id}/pause", http.HandlerFunc(s.handleWorkflowPause))
	s.Handle("POST /v1/workflows/{id}/resume", http.HandlerFunc(s.handleWorkflowResume))
	s.Handle("POST /v1/workflows/{id}/runs", http.HandlerFunc(s.handleWorkflowStartRun))
	s.Handle("GET /v1/workflows/{id}/runs", http.HandlerFunc(s.handleWorkflowRuns))
	s.Handle("GET /v1/workflows/{id}/runs/{run}", http.HandlerFunc(s.handleWorkflowRunDetail))
	s.Handle("POST /v1/workflows/{id}/runs/{run}/cancel", http.HandlerFunc(s.handleWorkflowCancel))
	s.Handle("POST /v1/workflows/{id}/decisions/{decision}", http.HandlerFunc(s.handleWorkflowAnswer))
	s.Handle("GET /v1/workflows/{id}/state", http.HandlerFunc(s.handleWorkflowState))
	s.Handle("PUT /v1/workflows/{id}/state/{key}", http.HandlerFunc(s.handleWorkflowSetState))
	s.Handle("DELETE /v1/workflows/{id}/state", http.HandlerFunc(s.handleWorkflowDeleteState))
	s.Handle("DELETE /v1/workflows/{id}/state/{key}", http.HandlerFunc(s.handleWorkflowDeleteState))
	s.Handle("GET /v1/workflows/{id}/assistant", http.HandlerFunc(s.handleWorkflowChat))
	s.Handle("POST /v1/workflows/{id}/assistant", http.HandlerFunc(s.handleWorkflowAssist))
	s.Handle("POST /v1/workflows/{id}/webhook/rotate", http.HandlerFunc(s.handleWorkflowRotateWebhook))
}

// handleWorkflowList serves API-WF-001 (REQ-WF-001, REQ-WF-011). It merges the
// open tool approvals of workflow sessions into the attention list.
func (s *Server) handleWorkflowList(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	overview, err := backend.Overview(r.Context())
	if err != nil {
		s.writeWorkflowError(w, "workflow list", err)
		return
	}
	if overview.Workflows == nil {
		overview.Workflows = []workflow.Summary{}
	}
	if overview.Attention == nil {
		overview.Attention = []workflow.Attention{}
	}
	mergeWorkflowApprovals(&overview, s.openWorkflowApprovals(), backend.IsWorkflowSession, s.workflowSessionAddress(r.Context()))
	writeJSON(w, http.StatusOK, overview)
}

// handleWorkflowCreate serves API-WF-002 (REQ-WF-001, REQ-WF-002).
func (s *Server) handleWorkflowCreate(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Guardian    string          `json:"guardian"`
		Graph       *workflow.Graph `json:"graph"`
	}
	if !decodeJSONObject(w, r, workflowBodyLimit, &body) {
		return
	}
	detail, err := backend.Create(r.Context(), workflow.CreateInput{
		Name:        body.Name,
		Description: body.Description,
		Guardian:    body.Guardian,
		Graph:       body.Graph,
	})
	if err != nil {
		s.writeWorkflowError(w, "create workflow", err)
		return
	}
	writeJSON(w, http.StatusCreated, detail)
}

// handleWorkflowGet serves API-WF-003 (REQ-WF-001).
func (s *Server) handleWorkflowGet(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	detail, err := backend.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeWorkflowError(w, "get workflow", err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleWorkflowUpdate serves API-WF-004 (REQ-WF-001, REQ-WF-002, REQ-WF-009).
func (s *Server) handleWorkflowUpdate(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		BaseVersion    int                     `json:"base_version"`
		Name           *string                 `json:"name"`
		Description    *string                 `json:"description"`
		Guardian       *string                 `json:"guardian"`
		Graph          *workflow.Graph         `json:"graph"`
		FailureDefault *workflow.FailurePolicy `json:"failure_default"`
		Summary        string                  `json:"summary"`
	}
	if !decodeJSONObject(w, r, workflowBodyLimit, &body) {
		return
	}
	detail, err := backend.Update(r.Context(), r.PathValue("id"), workflow.UpdateInput{
		BaseVersion:    body.BaseVersion,
		Name:           body.Name,
		Description:    body.Description,
		Guardian:       body.Guardian,
		Graph:          body.Graph,
		FailureDefault: body.FailureDefault,
		Summary:        body.Summary,
	})
	if err != nil {
		s.writeWorkflowError(w, "update workflow", err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleWorkflowDelete serves API-WF-005 (REQ-WF-001).
func (s *Server) handleWorkflowDelete(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	if err := backend.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.writeWorkflowError(w, "delete workflow", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// versionJSON is one version row of API-WF-006.
type versionJSON struct {
	Version   int    `json:"version"`
	Author    string `json:"author"`
	Summary   string `json:"summary"`
	CreatedAt int64  `json:"created_at"`
	NodeCount int    `json:"node_count"`
}

// handleWorkflowVersions serves API-WF-006 (REQ-WF-001).
func (s *Server) handleWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	versions, err := backend.Versions(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeWorkflowError(w, "workflow versions", err)
		return
	}
	rows := make([]versionJSON, 0, len(versions))
	for _, version := range versions {
		rows = append(rows, versionJSON{
			Version:   version.Version,
			Author:    version.Author,
			Summary:   version.Summary,
			CreatedAt: version.CreatedAt,
			NodeCount: len(version.Graph.Nodes),
		})
	}
	writeJSON(w, http.StatusOK, struct {
		Versions []versionJSON `json:"versions"`
	}{Versions: rows})
}

// handleWorkflowRestore serves API-WF-007 (REQ-WF-001, REQ-WF-010).
func (s *Server) handleWorkflowRestore(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Version int `json:"version"`
	}
	if !decodeJSONObject(w, r, workflowBodyLimit, &body) {
		return
	}
	detail, err := backend.Restore(r.Context(), r.PathValue("id"), body.Version)
	if err != nil {
		s.writeWorkflowError(w, "restore workflow", err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleWorkflowLock serves API-WF-008 (REQ-WF-009).
func (s *Server) handleWorkflowLock(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	tools, err := backend.LockPreview(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeWorkflowError(w, "workflow lock preview", err)
		return
	}
	if tools == nil {
		tools = []workflow.LockTool{}
	}
	writeJSON(w, http.StatusOK, struct {
		Tools []workflow.LockTool `json:"tools"`
	}{Tools: tools})
}

// handleWorkflowActivate serves API-WF-009 (REQ-WF-009).
func (s *Server) handleWorkflowActivate(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Version int `json:"version"`
	}
	if !decodeJSONObject(w, r, workflowBodyLimit, &body) {
		return
	}
	detail, err := backend.Activate(r.Context(), r.PathValue("id"), body.Version)
	if err != nil {
		s.writeWorkflowError(w, "activate workflow", err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleWorkflowPause serves API-WF-010 (REQ-WF-003, REQ-WF-008).
func (s *Server) handleWorkflowPause(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	detail, err := backend.Pause(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeWorkflowError(w, "pause workflow", err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleWorkflowResume serves API-WF-010 (REQ-WF-003, REQ-WF-008).
func (s *Server) handleWorkflowResume(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	detail, err := backend.Resume(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeWorkflowError(w, "resume workflow", err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleWorkflowStartRun serves API-WF-011 (REQ-WF-003).
func (s *Server) handleWorkflowStartRun(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Input json.RawMessage `json:"input"`
	}
	if !decodeJSONObject(w, r, workflowBodyLimit, &body) {
		return
	}
	run, err := backend.StartRun(r.Context(), r.PathValue("id"), "manual", body.Input)
	if err != nil {
		s.writeWorkflowError(w, "start workflow run", err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

// handleWorkflowRuns serves API-WF-012 (REQ-WF-007).
func (s *Server) handleWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	limit, before, err := parseWorkflowRunQuery(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	runs, err := backend.Runs(r.Context(), r.PathValue("id"), limit, before)
	if err != nil {
		s.writeWorkflowError(w, "workflow runs", err)
		return
	}
	if runs == nil {
		runs = []workflow.Run{}
	}
	writeJSON(w, http.StatusOK, struct {
		Runs []workflow.Run `json:"runs"`
	}{Runs: runs})
}

// handleWorkflowRunDetail serves API-WF-013 (REQ-WF-004, REQ-WF-007).
func (s *Server) handleWorkflowRunDetail(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	detail, err := backend.RunDetail(r.Context(), r.PathValue("id"), r.PathValue("run"))
	if err != nil {
		s.writeWorkflowErrorWithNotFound(w, "workflow run detail", err, "run_not_found")
		return
	}
	if detail.Nodes == nil {
		detail.Nodes = []workflow.NodeRun{}
	}
	if detail.Decisions == nil {
		detail.Decisions = []workflow.Decision{}
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleWorkflowCancel serves API-WF-014 (REQ-WF-007).
func (s *Server) handleWorkflowCancel(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	run, err := backend.Cancel(r.Context(), r.PathValue("id"), r.PathValue("run"))
	if err != nil {
		s.writeWorkflowErrorWithNotFound(w, "cancel workflow run", err, "run_not_found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// handleWorkflowAnswer serves API-WF-015 (REQ-WF-005, REQ-WF-008).
func (s *Server) handleWorkflowAnswer(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Choice string `json:"choice"`
	}
	if !decodeJSONObject(w, r, workflowBodyLimit, &body) {
		return
	}
	run, err := backend.Answer(r.Context(), r.PathValue("id"), r.PathValue("decision"), body.Choice)
	if err != nil {
		s.writeWorkflowErrorWithNotFound(w, "answer workflow decision", err, "decision_not_found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// handleWorkflowState serves API-WF-016 (REQ-WF-006).
func (s *Server) handleWorkflowState(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	entries, err := backend.State(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeWorkflowError(w, "workflow state", err)
		return
	}
	if entries == nil {
		entries = []workflow.StateEntry{}
	}
	writeJSON(w, http.StatusOK, struct {
		Entries []workflow.StateEntry `json:"entries"`
	}{Entries: entries})
}

// handleWorkflowSetState serves API-WF-016 (REQ-WF-006).
func (s *Server) handleWorkflowSetState(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Value json.RawMessage `json:"value"`
	}
	if !decodeJSONObject(w, r, workflowBodyLimit, &body) {
		return
	}
	entry, err := backend.SetState(r.Context(), r.PathValue("id"), r.PathValue("key"), body.Value)
	if err != nil {
		s.writeWorkflowError(w, "set workflow state", err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// handleWorkflowDeleteState serves API-WF-016 (REQ-WF-006): the key path
// deletes one entry, the collection path clears every entry.
func (s *Server) handleWorkflowDeleteState(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	if err := backend.DeleteState(r.Context(), r.PathValue("id"), r.PathValue("key")); err != nil {
		s.writeWorkflowError(w, "delete workflow state", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleWorkflowChat serves API-WF-017 (REQ-WF-010).
func (s *Server) handleWorkflowChat(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	messages, err := backend.Chat(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeWorkflowError(w, "workflow assistant chat", err)
		return
	}
	if messages == nil {
		messages = []workflow.ChatMessage{}
	}
	writeJSON(w, http.StatusOK, struct {
		Messages []workflow.ChatMessage `json:"messages"`
	}{Messages: messages})
}

// handleWorkflowAssist serves API-WF-017 (REQ-WF-010).
func (s *Server) handleWorkflowAssist(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Message     string `json:"message"`
		BaseVersion int    `json:"base_version"`
	}
	if !decodeJSONObject(w, r, workflowBodyLimit, &body) {
		return
	}
	result, err := backend.Assist(r.Context(), r.PathValue("id"), body.BaseVersion, body.Message)
	if err != nil {
		s.writeWorkflowError(w, "workflow assistant", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleWorkflowRotateWebhook serves API-WF-018 (REQ-WF-003).
func (s *Server) handleWorkflowRotateWebhook(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	token, err := backend.RotateWebhook(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeWorkflowError(w, "rotate workflow webhook", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Token string `json:"token"`
	}{Token: token})
}

// handleWorkflowWebhook serves API-WF-020 (REQ-WF-003). It is registered
// without the auth wrapper, so a hook works without a session cookie, CSRF
// token, Origin, or matching Host. It authenticates with X-Lana-Token.
func (s *Server) handleWorkflowWebhook(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, workflowBodyLimit))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be at most 1 MiB")
		return
	}
	run, err := backend.Webhook(r.Context(), r.PathValue("id"), r.Header.Get("X-Lana-Token"), body)
	switch {
	case errors.Is(err, workflow.ErrBadToken):
		writeAPIError(w, http.StatusUnauthorized, "bad_token", "webhook token is not valid")
		return
	case errors.Is(err, workflow.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "workflow_not_found", "workflow not found")
		return
	case err != nil:
		s.writeWorkflowError(w, "workflow webhook", err)
		return
	}
	writeJSON(w, http.StatusAccepted, struct {
		RunID  string `json:"run_id"`
		Number int    `json:"number"`
	}{RunID: run.ID, Number: run.Number})
}

// handleWorkflowStream serves API-WF-019 (REQ-WF-011). It follows the rooms
// stream loop: text/event-stream, a heartbeat comment, a shutdown event, and a
// return on client disconnect.
func (s *Server) handleWorkflowStream(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.logger.Error("web: workflow stream: response writer cannot flush")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	changes, unsubscribe := backend.Subscribe()
	defer unsubscribe()

	heartbeat := time.NewTicker(s.heartbeatInterval())
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
		case change, ok := <-changes:
			if !ok {
				return
			}
			if err := writeSSE(w, flusher, "changed", "", change); err != nil {
				return
			}
		}
	}
}

// workflowApproval is one open web approval of a workflow session, as the
// Needs attention list needs it (API-WF-001, REQ-WF-011).
type workflowApproval struct {
	ID        string
	SessionID string
	// Address is the workflow id of the session (channel "workflow" carries the
	// workflow id). It is empty until the session is looked up.
	Address   string
	Tool      string
	Argument  string
	Agent     string
	CreatedAt int64
}

// openWorkflowApprovals snapshots the broker's open approval requests. The
// broker and the handlers share the web package, so the request id and the tool
// argument are read under the broker lock.
func (s *Server) openWorkflowApprovals() []workflowApproval {
	if s.approvals == nil {
		return nil
	}
	s.approvals.mu.Lock()
	defer s.approvals.mu.Unlock()
	out := make([]workflowApproval, 0, len(s.approvals.open))
	for id, pending := range s.approvals.open {
		event := pending.event
		out = append(out, workflowApproval{
			ID:        id,
			SessionID: pending.sessionID,
			Tool:      event.Tool,
			Argument:  event.Argument,
			Agent:     event.Agent,
			CreatedAt: event.ExpiresAt - approvalTimeout.Milliseconds(),
		})
	}
	return out
}

// workflowSessionAddress returns the address (the workflow id) of a session,
// read through the web backend's session lookup. It returns "" when the backend
// is absent or the session cannot be read.
func (s *Server) workflowSessionAddress(ctx context.Context) func(string) string {
	return func(sessionID string) string {
		if s.backend == nil {
			return ""
		}
		session, err := s.backend.Session(ctx, sessionID)
		if err != nil {
			return ""
		}
		return session.Address
	}
}

// mergeWorkflowApprovals appends the open tool approvals of workflow sessions
// to the attention list, newest first, and counts them in the stats
// (API-WF-001, REQ-WF-011). isWorkflow reports whether a session runs on
// channel "workflow". addressOf resolves a session's address (the workflow id)
// when the approval does not already carry it.
func mergeWorkflowApprovals(overview *workflow.Overview, approvals []workflowApproval, isWorkflow func(string) bool, addressOf func(string) string) {
	names := make(map[string]string, len(overview.Workflows))
	for _, summary := range overview.Workflows {
		names[summary.ID] = summary.Name
	}
	added := 0
	for _, approval := range approvals {
		if isWorkflow != nil && !isWorkflow(approval.SessionID) {
			continue
		}
		address := approval.Address
		if address == "" && addressOf != nil {
			address = addressOf(approval.SessionID)
		}
		overview.Attention = append(overview.Attention, workflowApprovalAttention(approval, address, names))
		added++
	}
	if added == 0 {
		return
	}
	overview.Stats.WaitingOnYou += added
	overview.Stats.Approvals += added
	sort.SliceStable(overview.Attention, func(i, j int) bool {
		return overview.Attention[i].CreatedAt > overview.Attention[j].CreatedAt
	})
}

// workflowApprovalAttention builds one tool_approval attention row. The id is
// the web approval request id, so the UI answers it through
// POST /v1/approvals/{id} (AC-WF-019). WorkflowID is the session address and
// WorkflowName is the workflow's name, falling back to its id.
func workflowApprovalAttention(approval workflowApproval, workflowID string, names map[string]string) workflow.Attention {
	agent := approval.Agent
	if agent == "" {
		agent = "The worker"
	}
	reason := agent + " wants " + approval.Tool
	if approval.Argument != "" {
		reason += ": " + approval.Argument
	}
	name := names[workflowID]
	if name == "" {
		name = workflowID
	}
	return workflow.Attention{
		Decision: workflow.Decision{
			ID:         approval.ID,
			WorkflowID: workflowID,
			Kind:       "tool_approval",
			Action:     "ask",
			Reason:     reason,
			Status:     "pending",
			CreatedAt:  approval.CreatedAt,
		},
		WorkflowName: name,
		Tool:         approval.Tool,
		Argument:     approval.Argument,
	}
}

// parseWorkflowRunQuery reads the limit and before query of API-WF-012.
func parseWorkflowRunQuery(r *http.Request) (int, int, error) {
	limit := workflowDefaultRunLimit
	if values, ok := r.URL.Query()["limit"]; ok {
		parsed, err := strconv.Atoi(values[0])
		if err != nil || parsed < 1 || parsed > workflowMaxRunLimit {
			return 0, 0, fmt.Errorf("limit must be an integer between 1 and %d", workflowMaxRunLimit)
		}
		limit = parsed
	}
	before := 0
	if values, ok := r.URL.Query()["before"]; ok {
		parsed, err := strconv.Atoi(values[0])
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("before must be a non-negative integer")
		}
		before = parsed
	}
	return limit, before, nil
}

// writeWorkflowError maps a workflow service error to the contract error
// (api-contract.md, "Errors"). An unknown error is a 500.
func (s *Server) writeWorkflowError(w http.ResponseWriter, operation string, err error) {
	s.writeWorkflowErrorWithNotFound(w, operation, err, "workflow_not_found")
}

// writeWorkflowErrorWithNotFound is writeWorkflowError with the 404 code of the
// route. The run and decision routes name the missing object.
func (s *Server) writeWorkflowErrorWithNotFound(w http.ResponseWriter, operation string, err error, notFound string) {
	var invalid *workflow.InvalidGraphError
	var needsBackground *workflow.GuardianBackgroundError
	switch {
	case errors.Is(err, workflow.ErrNotFound):
		code, message := "workflow_not_found", "workflow not found"
		switch notFound {
		case "run_not_found":
			code, message = "run_not_found", "run not found"
		case "decision_not_found":
			code, message = "decision_not_found", "decision not found"
		}
		writeAPIError(w, http.StatusNotFound, code, message)
	case errors.Is(err, workflow.ErrExists):
		writeAPIError(w, http.StatusConflict, "workflow_exists", "workflow already exists")
	case errors.Is(err, workflow.ErrVersionConflict):
		writeAPIError(w, http.StatusConflict, "version_conflict", "the workflow changed since it was loaded")
	case errors.Is(err, workflow.ErrRunActive):
		writeAPIError(w, http.StatusConflict, "run_active", "a run of this workflow is already running")
	case errors.Is(err, workflow.ErrRunFinished):
		writeAPIError(w, http.StatusConflict, "run_finished", "the run already finished")
	case errors.Is(err, workflow.ErrDecisionClosed):
		writeAPIError(w, http.StatusConflict, "decision_resolved", "the decision was already answered")
	case errors.Is(err, workflow.ErrInvalidChoice):
		writeAPIError(w, http.StatusBadRequest, "invalid_choice", "the choice is not valid for this decision")
	case errors.Is(err, workflow.ErrLockRequired):
		writeAPIError(w, http.StatusConflict, "lock_required", "the workflow permissions must be locked again")
	case errors.As(err, &needsBackground):
		writeAPIError(w, http.StatusServiceUnavailable, "guardian_needs_background_model", needsBackground.Error())
	case errors.Is(err, workflow.ErrGuardianUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "guardian_unavailable", "the guardian is not available")
	case errors.As(err, &invalid):
		code := "invalid_graph"
		if len(invalid.Issues) == 1 && invalid.Issues[0].Code == "unknown_agent" {
			code = "unknown_agent"
		}
		writeAPIErrorDetails(w, http.StatusBadRequest, code, "the graph is not valid", []map[string]any{{"issues": invalid.Issues}})
	default:
		s.logger.Error("web: "+operation, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// workflowCommandResponse is the response of a handled chat command
// (API-WF-021).
type workflowCommandResponse struct {
	Command workflow.CommandResult `json:"command"`
}

// runWorkflowCommand checks a chat command before a turn starts. It writes the
// 200 command response and returns true when an active workflow matched, so the
// caller starts no turn and writes nothing more (API-WF-021, AC-WF-005).
func (s *Server) runWorkflowCommand(w http.ResponseWriter, r *http.Request, text string) bool {
	backend := s.workflowsBackend()
	if backend == nil {
		return false
	}
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return false
	}
	result, handled := backend.Command(r.Context(), "web", trimmed)
	if !handled {
		return false
	}
	writeJSON(w, http.StatusOK, workflowCommandResponse{Command: result})
	return true
}
