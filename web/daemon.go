package web

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"time"
)

// daemonRestartBodyLimit is the maximum size of a restart request body.
const daemonRestartBodyLimit = 4 << 10

// Daemon restart states and outcomes (API-TFB-002).
const (
	DaemonRestartIdle      = "idle"
	DaemonRestartDraining  = "draining"
	DaemonRestartScheduled = "scheduled"
	DaemonRestartCancelled = "cancelled"
)

// ErrRestartInvalidState is returned by DaemonStore.Restart while a stop drain
// runs or after the restart has started; the handler answers 409 invalid_state.
var ErrRestartInvalidState = errors.New("restart is not available while the daemon is stopping")

// DaemonTurn is one running turn listed by the restart confirmation.
type DaemonTurn struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Agent     string `json:"agent"`
}

// DaemonRestartInfo is the restart field of GET /v1/daemon (API-TFB-002).
type DaemonRestartInfo struct {
	State    string `json:"state"`
	Deadline *int64 `json:"deadline"`
}

// DaemonRestartOutcome is what DaemonStore.Restart reports. State is either
// DaemonRestartScheduled or DaemonRestartDraining; Deadline is unix
// milliseconds and only set while draining.
type DaemonRestartOutcome struct {
	State        string
	RunningTurns int
	Deadline     int64
}

// DaemonInfo is the read model of API-WEB-022, extended by API-TFB-002.
type DaemonInfo struct {
	PID              int               `json:"pid"`
	StartedAt        int64             `json:"started_at"`
	UptimeMS         int64             `json:"uptime_ms"`
	Addr             string            `json:"addr"`
	Home             string            `json:"home"`
	Version          string            `json:"version"`
	GoVersion        string            `json:"go_version"`
	OS               string            `json:"os"`
	Arch             string            `json:"arch"`
	RestartSupported bool              `json:"restart_supported"`
	Restart          DaemonRestartInfo `json:"restart"`
	RunningTurns     []DaemonTurn      `json:"running_turns"`
	BrowsersSignedIn int               `json:"browsers_signed_in"`
}

// DaemonStore is the optional backend port for API-WEB-022 and API-WEB-023.
// The daemon adapter implements it in cmd/lana; a backend that does not leaves
// the endpoints unavailable.
type DaemonStore interface {
	// DaemonInfo reads the runtime state at call time.
	DaemonInfo() DaemonInfo
	// Restart preflights and starts a restart. force interrupts running turns
	// now instead of draining. It returns before the daemon stops.
	Restart(ctx context.Context, force bool) (DaemonRestartOutcome, error)
	// CancelRestart cancels a pending restart drain and reports whether one
	// was pending.
	CancelRestart(ctx context.Context) (bool, error)
}

// SetDaemonStore installs the daemon status and restart port.
func (s *Server) SetDaemonStore(store DaemonStore) {
	s.daemon = store
}

// registerDaemonRoutes registers API-WEB-022, API-WEB-023, and API-TFB-002
// through Handle so the host, origin, cookie, and CSRF checks apply.
func (s *Server) registerDaemonRoutes() {
	s.Handle("GET /v1/daemon", http.HandlerFunc(s.handleDaemonInfo))
	s.Handle("POST /v1/daemon/restart", http.HandlerFunc(s.handleDaemonRestart))
	s.Handle("DELETE /v1/daemon/restart", http.HandlerFunc(s.handleDaemonRestartCancel))
}

func (s *Server) handleDaemonInfo(w http.ResponseWriter, r *http.Request) {
	store := s.daemonStoreOrError(w)
	if store == nil {
		return
	}
	info := store.DaemonInfo()
	s.fillDaemonInfo(r, &info)
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleDaemonRestart(w http.ResponseWriter, r *http.Request) {
	store := s.daemonStoreOrError(w)
	if store == nil {
		return
	}
	var body struct {
		Confirm *bool `json:"confirm"`
		Force   *bool `json:"force"`
	}
	if !decodeJSONObject(w, r, daemonRestartBodyLimit, &body) {
		return
	}
	if body.Confirm == nil || !*body.Confirm {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "confirm must be true")
		return
	}
	force := body.Force != nil && *body.Force
	info := store.DaemonInfo()
	s.fillDaemonInfo(r, &info)
	if !info.RestartSupported {
		writeAPIError(w, http.StatusConflict, "invalid_state", "restart in place is not supported on this platform")
		return
	}
	outcome, err := store.Restart(r.Context(), force)
	if err != nil {
		// A stop drain from SIGTERM/SIGINT refuses the web restart with 409:
		// the process is already shutting down and will not restart.
		if errors.Is(err, ErrRestartInvalidState) {
			writeAPIError(w, http.StatusConflict, "invalid_state", err.Error())
			return
		}
		s.logger.Error("web: restart", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "restart_failed", "restart could not be prepared")
		return
	}
	// API-TFB-002: a draining restart answers at once with its deadline; the
	// daemon restarts when the count reaches 0 or at the deadline.
	if outcome.State == DaemonRestartDraining {
		writeJSON(w, http.StatusAccepted, struct {
			Restart      string `json:"restart"`
			RunningTurns int    `json:"running_turns"`
			Deadline     int64  `json:"deadline"`
		}{Restart: DaemonRestartDraining, RunningTurns: outcome.RunningTurns, Deadline: outcome.Deadline})
		return
	}
	writeJSON(w, http.StatusAccepted, struct {
		Restart      string `json:"restart"`
		RunningTurns int    `json:"running_turns"`
	}{Restart: DaemonRestartScheduled, RunningTurns: outcome.RunningTurns})
}

// handleDaemonRestartCancel implements DELETE /v1/daemon/restart
// (API-TFB-002): it aborts a pending restart drain so turns are accepted again.
func (s *Server) handleDaemonRestartCancel(w http.ResponseWriter, r *http.Request) {
	store := s.daemonStoreOrError(w)
	if store == nil {
		return
	}
	cancelled, err := store.CancelRestart(r.Context())
	if err != nil {
		s.logger.Error("web: cancel restart", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !cancelled {
		writeAPIError(w, http.StatusConflict, "invalid_state", "no restart is pending")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Restart string `json:"restart"`
	}{Restart: DaemonRestartCancelled})
}

// fillDaemonInfo adds the fields the server owns: the bound address, the
// uptime, the browser count, and platform defaults.
func (s *Server) fillDaemonInfo(r *http.Request, info *DaemonInfo) {
	info.Addr = s.Addr()
	if info.StartedAt > 0 {
		info.UptimeMS = time.Since(time.UnixMilli(info.StartedAt)).Milliseconds()
	}
	if info.Restart.State == "" {
		info.Restart.State = DaemonRestartIdle
	}
	if info.RunningTurns == nil {
		info.RunningTurns = []DaemonTurn{}
	}
	if info.GoVersion == "" {
		info.GoVersion = runtime.Version()
	}
	if info.OS == "" {
		info.OS = runtime.GOOS
	}
	if info.Arch == "" {
		info.Arch = runtime.GOARCH
	}
	if sess, ok := sessionFromContext(r.Context()); ok {
		info.BrowsersSignedIn = len(s.sessions.list(sess.key, s.nowTime()))
	}
}

func (s *Server) daemonStoreOrError(w http.ResponseWriter) DaemonStore {
	if s.daemon != nil {
		return s.daemon
	}
	// A backend that implements the port is used without an extra wiring call.
	if store, ok := s.backend.(DaemonStore); ok {
		return store
	}
	s.logger.Error("web: daemon port is not configured")
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	return nil
}
