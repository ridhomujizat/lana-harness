package web

import (
	"context"
	"net/http"
	"runtime"
	"time"
)

// daemonRestartBodyLimit is the maximum size of a restart request body.
const daemonRestartBodyLimit = 4 << 10

// DaemonTurn is one running turn listed by the restart confirmation.
type DaemonTurn struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Agent     string `json:"agent"`
}

// DaemonInfo is the read model of API-WEB-022.
type DaemonInfo struct {
	PID              int          `json:"pid"`
	StartedAt        int64        `json:"started_at"`
	UptimeMS         int64        `json:"uptime_ms"`
	Addr             string       `json:"addr"`
	Home             string       `json:"home"`
	Version          string       `json:"version"`
	GoVersion        string       `json:"go_version"`
	OS               string       `json:"os"`
	Arch             string       `json:"arch"`
	RestartSupported bool         `json:"restart_supported"`
	RunningTurns     []DaemonTurn `json:"running_turns"`
	BrowsersSignedIn int          `json:"browsers_signed_in"`
}

// DaemonStore is the optional backend port for API-WEB-022 and API-WEB-023.
// The daemon adapter implements it in cmd/lana; a backend that does not leaves
// the endpoints unavailable.
type DaemonStore interface {
	// DaemonInfo reads the runtime state at call time.
	DaemonInfo() DaemonInfo
	// Restart preflights and schedules an in-place restart. It returns after
	// the preflight, before the daemon stops.
	Restart(ctx context.Context) error
}

// SetDaemonStore installs the daemon status and restart port.
func (s *Server) SetDaemonStore(store DaemonStore) {
	s.daemon = store
}

// registerDaemonRoutes registers API-WEB-022 and API-WEB-023 through Handle so
// the host, origin, cookie, and CSRF checks apply.
func (s *Server) registerDaemonRoutes() {
	s.Handle("GET /v1/daemon", http.HandlerFunc(s.handleDaemonInfo))
	s.Handle("POST /v1/daemon/restart", http.HandlerFunc(s.handleDaemonRestart))
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
	}
	if !decodeJSONObject(w, r, daemonRestartBodyLimit, &body) {
		return
	}
	if body.Confirm == nil || !*body.Confirm {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "confirm must be true")
		return
	}
	info := store.DaemonInfo()
	s.fillDaemonInfo(r, &info)
	if !info.RestartSupported {
		writeAPIError(w, http.StatusConflict, "invalid_state", "restart in place is not supported on this platform")
		return
	}
	if err := store.Restart(r.Context()); err != nil {
		s.logger.Error("web: restart", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "restart_failed", "restart could not be prepared")
		return
	}
	writeJSON(w, http.StatusAccepted, struct {
		Restart      string `json:"restart"`
		RunningTurns int    `json:"running_turns"`
	}{Restart: "scheduled", RunningTurns: len(info.RunningTurns)})
}

// fillDaemonInfo adds the fields the server owns: the bound address, the
// uptime, the browser count, and platform defaults.
func (s *Server) fillDaemonInfo(r *http.Request, info *DaemonInfo) {
	info.Addr = s.Addr()
	if info.StartedAt > 0 {
		info.UptimeMS = time.Since(time.UnixMilli(info.StartedAt)).Milliseconds()
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
