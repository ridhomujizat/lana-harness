package web

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// workbenchCoreRestartTimeout bounds a core restart: up to 10 s for the old
// core to stop, then the new core's start and Hello (spec 84 REQ-WBR-003).
const workbenchCoreRestartTimeout = 30 * time.Second

// WorkbenchCore is the body of GET /v1/workbench/core and of a restart's
// answer (spec 84 REQ-WBR-002).
type WorkbenchCore struct {
	Running       bool   `json:"running"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	DaemonVersion string `json:"daemon_version"`
	DaemonCommit  string `json:"daemon_commit"`
	Outdated      bool   `json:"outdated"`
	Sessions      int    `json:"sessions"`
}

// NewWorkbenchCore fills a WorkbenchCore and decides Outdated: a running core
// whose build differs from the daemon's, or that reports no version (a core
// started before spec 84), is outdated. A core that is not running is not: the
// next one starts from the daemon's binary.
func NewWorkbenchCore(running bool, version, commit, daemonVersion, daemonCommit string, sessions int) WorkbenchCore {
	return WorkbenchCore{
		Running:       running,
		Version:       version,
		Commit:        commit,
		DaemonVersion: daemonVersion,
		DaemonCommit:  daemonCommit,
		Outdated:      running && (version == "" || version != daemonVersion || commit != daemonCommit),
		Sessions:      sessions,
	}
}

// WorkbenchCoreControl is the part of a Workbench backend that reads and
// restarts the core process itself (spec 84). The daemon's backend implements
// it; a backend without it answers 503 on the core routes.
type WorkbenchCoreControl interface {
	// CoreInfo reports the running core; it never starts one.
	CoreInfo(ctx context.Context) (WorkbenchCore, error)
	// RestartCore stops the core, starts a new one, and reports it. A second
	// call while one runs fails with ErrWorkbenchRestartRunning.
	RestartCore(ctx context.Context) (WorkbenchCore, error)
}

// ErrWorkbenchRestartRunning is RestartCore's answer while another restart
// runs; the route answers it 409 restart_running.
var ErrWorkbenchRestartRunning = errors.New("a Workbench restart is already running")

// registerWorkbenchCoreRoutes registers the routes of spec 84 REQ-WBR-002 and
// REQ-WBR-003 behind the same Host, Origin, cookie and CSRF checks as the
// other Workbench routes.
func (s *Server) registerWorkbenchCoreRoutes() {
	s.Handle("GET /v1/workbench/core", http.HandlerFunc(s.handleWorkbenchCore))
	s.Handle("POST /v1/workbench/core/restart", http.HandlerFunc(s.handleWorkbenchCoreRestart))
}

// workbenchCoreOrError returns the backend's core control, or writes 503
// workbench_unavailable.
func (s *Server) workbenchCoreOrError(w http.ResponseWriter) WorkbenchCoreControl {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return nil
	}
	ctl, ok := backend.(WorkbenchCoreControl)
	if !ok {
		writeAPIError(w, http.StatusServiceUnavailable, "workbench_unavailable", "this daemon cannot restart the Workbench")
		return nil
	}
	return ctl
}

// handleWorkbenchCore serves GET /v1/workbench/core (spec 84 REQ-WBR-002).
func (s *Server) handleWorkbenchCore(w http.ResponseWriter, r *http.Request) {
	ctl := s.workbenchCoreOrError(w)
	if ctl == nil {
		return
	}
	info, err := ctl.CoreInfo(r.Context())
	if err != nil {
		s.writeWorkbenchError(w, "workbench core", err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleWorkbenchCoreRestart serves POST /v1/workbench/core/restart (spec 84
// REQ-WBR-003). The restart runs to its end even when the browser goes away,
// so the core is never left stopped half-way.
func (s *Server) handleWorkbenchCoreRestart(w http.ResponseWriter, r *http.Request) {
	if !s.controlAllowed(r) {
		refuseControlLocked(w)
		return
	}
	ctl := s.workbenchCoreOrError(w)
	if ctl == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), workbenchCoreRestartTimeout)
	defer cancel()
	info, err := ctl.RestartCore(ctx)
	if err != nil {
		if errors.Is(err, ErrWorkbenchRestartRunning) {
			writeAPIError(w, http.StatusConflict, "restart_running", err.Error())
			return
		}
		s.logger.Warn("web: workbench core restart", "error", err)
		writeAPIError(w, http.StatusServiceUnavailable, "workbench_unavailable", err.Error())
		return
	}
	browser, _ := sessionFromContext(r.Context())
	s.auditWorkbench("core-restart", "core", browser)
	writeJSON(w, http.StatusOK, info)
}
