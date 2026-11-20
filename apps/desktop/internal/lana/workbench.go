package lana

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// Workbench route paths of REQ-DW-014.
const (
	SessionsPath = "/v1/workbench/sessions"
	LayoutPath   = "/v1/workbench/layout"
	EventsPath   = "/v1/workbench/events"

	// AttachModeControl is the attach mode the app uses: it sends keys and
	// sizes (REQ-DW-040).
	AttachModeControl = "control"
)

// Event types of the SSE stream (REQ-WB-030).
const (
	EventSessionCreated = "session.created"
	EventSessionUpdated = "session.updated"
	EventSessionRemoved = "session.removed"
	EventLayoutChanged  = "layout.changed"
)

// Session describes a session and what runs in it now. The JSON shape is
// copied from internal/workbench proto.go, so this module does not import the
// daemon.
type Session struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Command    []string          `json:"command"`
	Dir        string            `json:"dir"`
	Labels     map[string]string `json:"labels,omitempty"`
	PID        int               `json:"pid"`
	Created    time.Time         `json:"created"`
	Cols       int               `json:"cols"`
	Rows       int               `json:"rows"`
	Program    string            `json:"program"`
	Cwd        string            `json:"cwd,omitempty"`
	State      string            `json:"state"`
	ExitCode   int               `json:"exit_code"`
	NeedsYou   bool              `json:"needs_you"`
	LastOutput time.Time         `json:"last_output"`
	Attached   int               `json:"attached"`
	// Agent is the coding agent running in the foreground job, "" when none,
	// and AgentState is its screen-derived state.
	Agent      string `json:"agent"`
	AgentState string `json:"agent_state"`
}

// CreateOptions starts a session.
type CreateOptions struct {
	Command []string          `json:"command,omitempty"`
	Dir     string            `json:"dir,omitempty"`
	Env     []string          `json:"env,omitempty"`
	Cols    int               `json:"cols,omitempty"`
	Rows    int               `json:"rows,omitempty"`
	Title   string            `json:"title,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
}

// Event is one line of the event stream: what happened, and the session or
// layout it happened to.
type Event struct {
	Type    string          `json:"type"`
	Session *Session        `json:"session,omitempty"`
	Layout  json.RawMessage `json:"layout,omitempty"`
}

// sessionPath is the path of one session.
func sessionPath(id string) string { return SessionsPath + "/" + url.PathEscape(id) }

// ListSessions answers GET /v1/workbench/sessions. The answer is never nil.
func (c *Local) ListSessions(ctx context.Context) ([]Session, error) {
	var answer struct {
		Sessions []Session `json:"sessions"`
	}
	if err := c.Do(ctx, http.MethodGet, SessionsPath, nil, &answer); err != nil {
		return nil, err
	}
	if answer.Sessions == nil {
		answer.Sessions = []Session{}
	}
	return answer.Sessions, nil
}

// CreateSession answers POST /v1/workbench/sessions.
func (c *Local) CreateSession(ctx context.Context, opts CreateOptions) (Session, error) {
	var session Session
	if err := c.Do(ctx, http.MethodPost, SessionsPath, opts, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

// KillSession answers DELETE /v1/workbench/sessions/{id}.
func (c *Local) KillSession(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodDelete, sessionPath(id), nil, nil)
}

// RestartSession answers POST /v1/workbench/sessions/{id}/restart.
func (c *Local) RestartSession(ctx context.Context, id string) (Session, error) {
	var session Session
	if err := c.Do(ctx, http.MethodPost, sessionPath(id)+"/restart", nil, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

// Resize answers POST /v1/workbench/sessions/{id}/resize.
func (c *Local) Resize(ctx context.Context, id string, cols, rows int) error {
	body := struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}{Cols: cols, Rows: rows}
	return c.Do(ctx, http.MethodPost, sessionPath(id)+"/resize", body, nil)
}

// GetLayout answers GET /v1/workbench/layout: the shared layout document, or
// "{}" when the daemon has none.
func (c *Local) GetLayout(ctx context.Context) (json.RawMessage, error) {
	var layout json.RawMessage
	if err := c.Do(ctx, http.MethodGet, LayoutPath, nil, &layout); err != nil {
		return nil, err
	}
	if len(layout) == 0 {
		layout = json.RawMessage(`{}`)
	}
	return layout, nil
}

// PutLayout answers PUT /v1/workbench/layout (REQ-WB-031). The document is
// sent unchanged, so fields this app does not know survive.
func (c *Local) PutLayout(ctx context.Context, layout json.RawMessage) error {
	return c.Do(ctx, http.MethodPut, LayoutPath, layout, nil)
}
