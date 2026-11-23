package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
	"github.com/ridhomujizat/lana-harness/internal/workbench"
)

// Attach modes of REQ-WB-041.
const (
	workbenchModeView    = "view"
	workbenchModeControl = "control"
)

const (
	// workbenchBodyLimit bounds a create or resize body.
	workbenchBodyLimit = 1 << 16
	// workbenchLayoutLimit bounds the opaque layout document (REQ-WB-031).
	workbenchLayoutLimit = 256 << 10
	// workbenchMessageLimit bounds one attach WebSocket message (REQ-WB-041).
	workbenchMessageLimit int64 = 1 << 20
	// workbenchCloseNotFound is the attach close code for a session that is
	// gone: the WebSocket private range 4000-4999.
	workbenchCloseNotFound = websocket.StatusCode(4404)
)

// WorkbenchBackend is the subset of *workbench.Client the workbench handlers
// use (REQ-WB-040, REQ-WB-041). It is defined in web so the package does not
// depend on the client type; the daemon installs it with SetWorkbench. Without
// it every /v1/workbench route answers 503 workbench_unavailable.
type WorkbenchBackend interface {
	List(ctx context.Context) ([]workbench.Session, error)
	Get(ctx context.Context, id string) (workbench.Session, error)
	Create(ctx context.Context, opts workbench.CreateOptions) (workbench.Session, error)
	Kill(ctx context.Context, id string) error
	Restart(ctx context.Context, id string) (workbench.Session, error)
	Resize(ctx context.Context, id string, cols, rows int) error
	Layout(ctx context.Context) (json.RawMessage, error)
	SetLayout(ctx context.Context, layout json.RawMessage) error
	Attach(ctx context.Context, id, mode string) (io.ReadWriteCloser, error)
	Subscribe(ctx context.Context) (<-chan workbench.Event, error)
}

// SetWorkbench installs the Workbench core port. Call it before Start; without
// it every /v1/workbench route answers 503 workbench_unavailable.
func (s *Server) SetWorkbench(b WorkbenchBackend) {
	s.workbenchMu.Lock()
	defer s.workbenchMu.Unlock()
	s.workbench = b
}

func (s *Server) workbenchBackend() WorkbenchBackend {
	s.workbenchMu.RLock()
	defer s.workbenchMu.RUnlock()
	return s.workbench
}

// workbenchOrError returns the Workbench port, or writes 503
// workbench_unavailable when none is installed.
func (s *Server) workbenchOrError(w http.ResponseWriter) WorkbenchBackend {
	backend := s.workbenchBackend()
	if backend == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "workbench_unavailable", "the workbench core is not available")
		return nil
	}
	return backend
}

// writeWorkbenchError maps a core error to its API answer (REQ-WB-042): an
// unknown session is 404 not_found, anything else is 503 workbench_unavailable
// with the reason, so a core that cannot be reached or started is visible.
func (s *Server) writeWorkbenchError(w http.ResponseWriter, operation string, err error) {
	if errors.Is(err, workbench.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "not_found", "the workbench session does not exist")
		return
	}
	s.logger.Warn("web: "+operation, "error", err)
	writeAPIError(w, http.StatusServiceUnavailable, "workbench_unavailable", err.Error())
}

// auditWorkbench appends the REQ-WB-043 line for a workbench change: the kind
// and the session id in the audit file, and the browser session in the log.
func (s *Server) auditWorkbench(kind, sessionID string, sess session) {
	attrs := []any{"kind", kind, "session", sessionID}
	if sess.csrf != "" {
		attrs = append(attrs, "browser", fmt.Sprintf("%x", sess.key[:4]))
	}
	s.logger.Info("web: workbench", attrs...)
	entry := webfiles.AuditEntry{
		Time:   s.nowTime().UnixMilli(),
		Kind:   "workbench." + kind,
		Target: sessionID,
	}
	if err := webfiles.AppendAudit(webfiles.AuditPath(s.auditHome()), entry); err != nil {
		s.logger.Error("web: append audit line", "error", err)
	}
}

// registerWorkbenchRoutes registers the routes of REQ-WB-040 (spec 55). They go
// through Handle, so the Host, Origin, cookie, and CSRF checks apply; the attach
// route checks Origin itself because its upgrade is a GET.
func (s *Server) registerWorkbenchRoutes() {
	s.Handle("GET /v1/workbench/sessions", http.HandlerFunc(s.handleWorkbenchList))
	s.Handle("POST /v1/workbench/sessions", http.HandlerFunc(s.handleWorkbenchCreate))
	s.Handle("DELETE /v1/workbench/sessions/{id}", http.HandlerFunc(s.handleWorkbenchKill))
	s.Handle("POST /v1/workbench/sessions/{id}/restart", http.HandlerFunc(s.handleWorkbenchRestart))
	s.Handle("POST /v1/workbench/sessions/{id}/resize", http.HandlerFunc(s.handleWorkbenchResize))
	// The worktree reports of spec 69 (REQ-WA-030).
	s.Handle("PUT /v1/workbench/sessions/{id}/worktree", http.HandlerFunc(s.handleWorkbenchSetWorktree))
	s.Handle("DELETE /v1/workbench/sessions/{id}/worktree", http.HandlerFunc(s.handleWorkbenchClearWorktree))
	s.Handle("GET /v1/workbench/layout", http.HandlerFunc(s.handleWorkbenchLayout))
	s.Handle("PUT /v1/workbench/layout", http.HandlerFunc(s.handleWorkbenchSetLayout))
	s.Handle("GET /v1/workbench/events", http.HandlerFunc(s.handleWorkbenchEvents))
	s.Handle("GET /v1/workbench/sessions/{id}/attach", http.HandlerFunc(s.handleWorkbenchAttach))
	s.Handle("POST /v1/workbench/drops", http.HandlerFunc(s.handleWorkbenchDrops))
	// The file preview of spec 61: the POST is authenticated like the routes
	// above; the GET carries its own token and needs no cookie, so it is
	// registered behind the host check alone (REQ-WP-004), like
	// GET /v1/auth/state.
	s.Handle("POST /v1/workbench/preview", http.HandlerFunc(s.handleWorkbenchPreview))
	s.mux.Handle("GET /v1/workbench/preview/{token}/{name...}", s.checkHost(http.HandlerFunc(s.handleWorkbenchPreviewFile)))
	// The git routes of spec 66 (REQ-WG-020).
	s.registerWorkbenchGitRoutes()
	// The FigCraft viewer routes of spec 67 (REQ-FV-020).
	s.registerWorkbenchFigcraftRoutes()
	// The Claude Code chat routes of spec 71 (REQ-WC-040).
	s.registerWorkbenchChatRoutes()
}

// handleWorkbenchList serves the GET of REQ-WB-040.
func (s *Server) handleWorkbenchList(w http.ResponseWriter, r *http.Request) {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	sessions, err := backend.List(r.Context())
	if err != nil {
		s.writeWorkbenchError(w, "workbench list", err)
		return
	}
	// A JSON array, never null: the page lists sessions directly.
	if sessions == nil {
		sessions = []workbench.Session{}
	}
	// REQ-WA-033: each session carries its reported worktree, and a report of a
	// session no longer in the list ends (REQ-WA-032).
	for i := range sessions {
		sessions[i] = s.decorateWorkbenchSession(sessions[i])
	}
	s.dropOrphanWorkbenchReports(sessions)
	writeJSON(w, http.StatusOK, struct {
		Sessions []workbench.Session `json:"sessions"`
	}{Sessions: sessions})
}

// workbenchCreateBody is the POST /v1/workbench/sessions body of REQ-WB-040.
type workbenchCreateBody struct {
	Command []string          `json:"command"`
	Dir     string            `json:"dir"`
	Cols    int               `json:"cols"`
	Rows    int               `json:"rows"`
	Title   string            `json:"title"`
	Labels  map[string]string `json:"labels"`
}

// handleWorkbenchCreate serves the POST of REQ-WB-040 (REQ-WB-043 audit).
func (s *Server) handleWorkbenchCreate(w http.ResponseWriter, r *http.Request) {
	if !s.controlAllowed(r) {
		refuseControlLocked(w)
		return
	}
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	var body workbenchCreateBody
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	sess, err := backend.Create(r.Context(), workbench.CreateOptions{
		Command: body.Command,
		Dir:     expandWorkbenchDir(body.Dir),
		Cols:    body.Cols,
		Rows:    body.Rows,
		Title:   body.Title,
		Labels:  body.Labels,
	})
	if err != nil {
		s.writeWorkbenchError(w, "workbench create", err)
		return
	}
	browser, _ := sessionFromContext(r.Context())
	s.auditWorkbench("create", sess.ID, browser)
	writeJSON(w, http.StatusOK, sess)
}

// handleWorkbenchKill serves the DELETE of REQ-WB-040 (REQ-WB-043 audit).
func (s *Server) handleWorkbenchKill(w http.ResponseWriter, r *http.Request) {
	if !s.controlAllowed(r) {
		refuseControlLocked(w)
		return
	}
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	id := r.PathValue("id")
	if err := backend.Kill(r.Context(), id); err != nil {
		s.writeWorkbenchError(w, "workbench kill", err)
		return
	}
	browser, _ := sessionFromContext(r.Context())
	s.auditWorkbench("kill", id, browser)
	w.WriteHeader(http.StatusNoContent)
}

// handleWorkbenchRestart serves the restart of REQ-WB-040 (REQ-WB-043 audit).
func (s *Server) handleWorkbenchRestart(w http.ResponseWriter, r *http.Request) {
	if !s.controlAllowed(r) {
		refuseControlLocked(w)
		return
	}
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	id := r.PathValue("id")
	sess, err := backend.Restart(r.Context(), id)
	if err != nil {
		s.writeWorkbenchError(w, "workbench restart", err)
		return
	}
	browser, _ := sessionFromContext(r.Context())
	s.auditWorkbench("restart", id, browser)
	writeJSON(w, http.StatusOK, sess)
}

// handleWorkbenchResize serves the resize of REQ-WB-040.
func (s *Server) handleWorkbenchResize(w http.ResponseWriter, r *http.Request) {
	if !s.controlAllowed(r) {
		refuseControlLocked(w)
		return
	}
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	if body.Cols <= 0 || body.Rows <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "cols and rows must be positive")
		return
	}
	if err := backend.Resize(r.Context(), r.PathValue("id"), body.Cols, body.Rows); err != nil {
		s.writeWorkbenchError(w, "workbench resize", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleWorkbenchLayout serves the layout GET of REQ-WB-040: the opaque
// document, or "{}" when the core has none.
func (s *Server) handleWorkbenchLayout(w http.ResponseWriter, r *http.Request) {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	layout, err := backend.Layout(r.Context())
	if err != nil {
		s.writeWorkbenchError(w, "workbench layout", err)
		return
	}
	if len(layout) == 0 {
		layout = json.RawMessage(`{}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(layout)
}

// handleWorkbenchSetLayout serves the layout PUT of REQ-WB-040: at most 256 KiB
// of raw JSON, stored by the core (REQ-WB-031).
func (s *Server) handleWorkbenchSetLayout(w http.ResponseWriter, r *http.Request) {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, workbenchLayoutLimit))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "the layout must be at most 256 KiB of JSON")
		return
	}
	if !json.Valid(raw) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "the layout must be valid JSON")
		return
	}
	if err := backend.SetLayout(r.Context(), json.RawMessage(raw)); err != nil {
		s.writeWorkbenchError(w, "workbench set layout", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleWorkbenchEvents serves the SSE stream of REQ-WB-040 (REQ-WB-030): one
// `event: <type>` per core event, with `data` the event JSON, and the same
// heartbeat as the other streams.
func (s *Server) handleWorkbenchEvents(w http.ResponseWriter, r *http.Request) {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.logger.Error("web: workbench events: response writer cannot flush")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	events, err := backend.Subscribe(r.Context())
	if err != nil {
		s.writeWorkbenchError(w, "workbench subscribe", err)
		return
	}
	// REQ-WA-033: a report change publishes on the daemon bus; the subscription
	// lives with the request.
	bus, cancelBus := s.workbenchBus.subscribe()
	defer cancelBus()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

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
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := writeSSE(w, flusher, event.Type, "", s.decorateWorkbenchEvent(event)); err != nil {
				return
			}
		case busEvent, ok := <-bus:
			if !ok {
				return
			}
			if err := writeSSE(w, flusher, busEvent.Type, "", busEvent.Data); err != nil {
				return
			}
		}
	}
}

// handleWorkbenchAttach serves the WebSocket attach of REQ-WB-041. The upgrade
// is a GET, so the auth wrapper does not check Origin; it is checked here
// against the bound address. The library's own check is disabled because it
// would reject an origin whose host differs only in case. Server to client
// frames are terminal output; client binary frames are input (dropped in view
// mode) and client text frames are resize control messages (control only).
func (s *Server) handleWorkbenchAttach(w http.ResponseWriter, r *http.Request) {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	if origin := r.Header.Get("Origin"); origin != s.expectedOrigin(r) {
		writeAPIError(w, http.StatusForbidden, "forbidden_origin", "origin is not allowed")
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode != workbenchModeView && mode != workbenchModeControl {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "mode must be view or control")
		return
	}
	// REQ-RA-031: a remote browser with locked control attaches in view mode
	// only. The check runs before the upgrade so the client sees a plain 403.
	if mode == workbenchModeControl && !s.controlAllowed(r) {
		refuseControlLocked(w)
		return
	}
	id := r.PathValue("id")
	browser, _ := sessionFromContext(r.Context())
	// Check the session before the upgrade so an unknown id is a plain 404.
	if _, err := backend.Get(r.Context(), id); err != nil {
		s.writeWorkbenchError(w, "workbench attach session", err)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		// Accept has already written the response.
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(workbenchMessageLimit)

	// The request context ends with the handler, so the pumps run on their own
	// context (the library warns against using the request context after Accept).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := backend.Attach(ctx, id, mode)
	if err != nil {
		// The session passed the check above but the core reports it gone.
		if errors.Is(err, workbench.ErrNotFound) {
			_ = conn.Close(workbenchCloseNotFound, "session not found")
			return
		}
		_ = conn.Close(websocket.StatusInternalError, "attach failed")
		return
	}
	if mode == workbenchModeControl {
		s.auditWorkbench("attach", id, browser)
	}
	// REQ-RA-035: a control attachment of a remote confirm session ends when
	// its control lapses or is locked. The registry signals it at once on a
	// lock; the watcher polls for expiry and a session that went away.
	if mode == workbenchModeControl && s.controlWatchable(r) {
		att := &controlAttachment{}
		var onceLocked sync.Once
		locked := func() {
			onceLocked.Do(func() { _ = conn.Close(workbenchCloseControlLocked, controlLockedReason) })
		}
		controlAttachments.add(browser.key, att, locked)
		go s.watchControl(ctx, browser.key, att, locked)
	}

	// Either direction ending closes both: cancel stops the WebSocket read and
	// closing the stream stops the core read.
	var once sync.Once
	finish := func() {
		once.Do(func() {
			cancel()
			_ = stream.Close()
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		pumpWorkbenchOutput(ctx, conn, stream)
		finish()
	}()
	go func() {
		defer wg.Done()
		pumpWorkbenchInput(ctx, conn, backend, stream, id, mode)
		finish()
	}()
	wg.Wait()
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// pumpWorkbenchOutput forwards the core's output to the client as binary frames.
func pumpWorkbenchOutput(ctx context.Context, conn *websocket.Conn, stream io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// pumpWorkbenchInput forwards the client's input to the core. In view mode the
// input and the resize messages are dropped; the core would drop the input too.
func pumpWorkbenchInput(ctx context.Context, conn *websocket.Conn, backend WorkbenchBackend, stream io.Writer, id, mode string) {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if mode != workbenchModeControl {
			continue
		}
		switch typ {
		case websocket.MessageBinary:
			if _, err := stream.Write(data); err != nil {
				return
			}
		case websocket.MessageText:
			var message struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &message) != nil || message.Type != "resize" {
				continue
			}
			if message.Cols <= 0 || message.Rows <= 0 {
				continue
			}
			if err := backend.Resize(ctx, id, message.Cols, message.Rows); err != nil {
				if errors.Is(err, workbench.ErrNotFound) {
					return
				}
			}
		}
	}
}

// expandWorkbenchDir replaces a leading ~ or ~/ with the user's home
// directory, so a new session can start in the owner's home.
func expandWorkbenchDir(dir string) string {
	if dir != "~" && !strings.HasPrefix(dir, "~/") {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return dir
	}
	if dir == "~" {
		return home
	}
	return filepath.Join(home, dir[2:])
}
