package web

import (
	"context"
	"errors"
	"net/http"
)

// updateBodyLimit is the maximum size of an update request body.
const updateBodyLimit = 4 << 10

// Update channels (API-UPD-001).
const (
	UpdateChannelStable = "stable"
	UpdateChannelBeta   = "beta"
)

// Update job states (API-UPD-002 job.state).
const (
	UpdateStateDownloading = "downloading"
	UpdateStateVerifying   = "verifying"
	UpdateStateProbing     = "probing"
	UpdateStateWaiting     = "waiting"
	UpdateStateInstalling  = "installing"
	UpdateStateRestarting  = "restarting"
	UpdateStateFailed      = "failed"
	UpdateStateCancelled   = "cancelled"
)

// ErrUpdateInvalidState is returned by UpdateStore when a job is active or the
// version is not the available one; the handlers answer 409 invalid_state
// (API-UPD-004, API-UPD-005).
var ErrUpdateInvalidState = errors.New("an update job is already running")

// ErrUpdateUnavailable is returned by UpdateStore.ApplyUpdate when updates are
// unsupported or the binary cannot be replaced; the handler answers 409
// update_unavailable (API-UPD-005).
var ErrUpdateUnavailable = errors.New("this lana cannot update itself")

// UpdateStatus is the read model of API-UPD-002. Times are unix milliseconds.
type UpdateStatus struct {
	Current       string         `json:"current"`
	Channel       string         `json:"channel"`
	Check         bool           `json:"check"`
	Supported     bool           `json:"supported"`
	CanApply      bool           `json:"can_apply"`
	ManualReason  string         `json:"manual_reason"`
	ManualCommand string         `json:"manual_command"`
	CheckedAt     *int64         `json:"checked_at"`
	CheckError    string         `json:"check_error"`
	Available     *UpdateRelease `json:"available"`
	Skipped       string         `json:"skipped"`
	Job           *UpdateJob     `json:"job"`
	LastResult    *UpdateResult  `json:"last_result"`
}

// UpdateRelease is the available field of API-UPD-002.
type UpdateRelease struct {
	Version      string `json:"version"`
	Tag          string `json:"tag"`
	Name         string `json:"name"`
	PublishedAt  int64  `json:"published_at"`
	Notes        string `json:"notes"`
	URL          string `json:"url"`
	Size         int64  `json:"size"`
	Prerelease   bool   `json:"prerelease"`
	UpgradeNotes bool   `json:"upgrade_notes"`
}

// UpdateJob is the job field of API-UPD-002. Progress is set only while
// downloading; RunningTurns and Deadline only while waiting; Error only when
// failed.
type UpdateJob struct {
	State        string          `json:"state"`
	Version      string          `json:"version"`
	Progress     *UpdateProgress `json:"progress"`
	RunningTurns int             `json:"running_turns"`
	Deadline     *int64          `json:"deadline"`
	Error        string          `json:"error"`
}

// UpdateProgress is the download progress; Total is 0 when unknown.
type UpdateProgress struct {
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
}

// UpdateResult is the last_result field of API-UPD-002.
type UpdateResult struct {
	Outcome string `json:"outcome"`
	From    string `json:"from"`
	To      string `json:"to"`
	Reason  string `json:"reason"`
	At      int64  `json:"at"`
}

// UpdateStore is the backend port of API-UPD-003..009. The daemon implements it
// in cmd/lana; a server without it answers the routes with 500.
type UpdateStore interface {
	// UpdateStatus reads the state at call time and never calls GitHub.
	UpdateStatus(ctx context.Context) UpdateStatus
	// CheckUpdate checks GitHub now. A GitHub failure is reported in
	// check_error, not as an error. ErrUpdateInvalidState while a job runs.
	CheckUpdate(ctx context.Context) (UpdateStatus, error)
	// ApplyUpdate starts the update job for version. force interrupts running
	// turns instead of waiting. ErrUpdateInvalidState, ErrUpdateUnavailable.
	ApplyUpdate(ctx context.Context, version string, force bool) (UpdateStatus, error)
	// CancelUpdate cancels a job before installing and reports whether one
	// was cancelled.
	CancelUpdate(ctx context.Context) (bool, error)
	// SkipUpdate saves the skipped version; "" clears it.
	SkipUpdate(ctx context.Context, version string) (UpdateStatus, error)
	// UpdateSettings writes [update] in config.toml and applies it live. A nil
	// field stays unchanged.
	UpdateSettings(ctx context.Context, channel *string, check *bool) (UpdateStatus, error)
	// ClearUpdateResult clears last_result.
	ClearUpdateResult(ctx context.Context) error
}

// SetUpdateStore installs the self-update port (spec 36).
func (s *Server) SetUpdateStore(store UpdateStore) {
	s.update = store
}

// registerUpdateRoutes registers API-UPD-003..009 through Handle so the host,
// origin, cookie, and CSRF checks apply.
func (s *Server) registerUpdateRoutes() {
	s.Handle("GET /v1/update", http.HandlerFunc(s.handleUpdateStatus))
	s.Handle("POST /v1/update/check", http.HandlerFunc(s.handleUpdateCheck))
	s.Handle("POST /v1/update/apply", http.HandlerFunc(s.handleUpdateApply))
	s.Handle("DELETE /v1/update/apply", http.HandlerFunc(s.handleUpdateCancel))
	s.Handle("POST /v1/update/skip", http.HandlerFunc(s.handleUpdateSkip))
	s.Handle("PATCH /v1/update/settings", http.HandlerFunc(s.handleUpdateSettings))
	s.Handle("DELETE /v1/update/result", http.HandlerFunc(s.handleUpdateResultClear))
}

// handleUpdateStatus implements API-UPD-003.
func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	store := s.updateStoreOrError(w)
	if store == nil {
		return
	}
	writeJSON(w, http.StatusOK, store.UpdateStatus(r.Context()))
}

// handleUpdateCheck implements API-UPD-004.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	store := s.updateStoreOrError(w)
	if store == nil {
		return
	}
	status, err := store.CheckUpdate(r.Context())
	if err != nil {
		s.writeUpdateError(w, "check", err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleUpdateApply implements API-UPD-005.
func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	store := s.updateStoreOrError(w)
	if store == nil {
		return
	}
	var body struct {
		Version *string `json:"version"`
		Confirm *bool   `json:"confirm"`
		Force   *bool   `json:"force"`
	}
	if !decodeJSONObject(w, r, updateBodyLimit, &body) {
		return
	}
	if body.Confirm == nil || !*body.Confirm {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "confirm must be true")
		return
	}
	if body.Version == nil || *body.Version == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "version is required")
		return
	}
	force := body.Force != nil && *body.Force
	status, err := store.ApplyUpdate(r.Context(), *body.Version, force)
	if err != nil {
		s.writeUpdateError(w, "apply", err)
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

// handleUpdateCancel implements API-UPD-006.
func (s *Server) handleUpdateCancel(w http.ResponseWriter, r *http.Request) {
	store := s.updateStoreOrError(w)
	if store == nil {
		return
	}
	cancelled, err := store.CancelUpdate(r.Context())
	if err != nil {
		s.writeUpdateError(w, "cancel", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Cancelled bool `json:"cancelled"`
	}{Cancelled: cancelled})
}

// handleUpdateSkip implements API-UPD-007.
func (s *Server) handleUpdateSkip(w http.ResponseWriter, r *http.Request) {
	store := s.updateStoreOrError(w)
	if store == nil {
		return
	}
	var body struct {
		Version *string `json:"version"`
	}
	if !decodeJSONObject(w, r, updateBodyLimit, &body) {
		return
	}
	if body.Version == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "version is required")
		return
	}
	status, err := store.SkipUpdate(r.Context(), *body.Version)
	if err != nil {
		s.writeUpdateError(w, "skip", err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleUpdateSettings implements API-UPD-008.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	store := s.updateStoreOrError(w)
	if store == nil {
		return
	}
	var body struct {
		Channel *string `json:"channel"`
		Check   *bool   `json:"check"`
	}
	if !decodeJSONObject(w, r, updateBodyLimit, &body) {
		return
	}
	if body.Channel != nil && *body.Channel != UpdateChannelStable && *body.Channel != UpdateChannelBeta {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", `channel must be "stable" or "beta"`)
		return
	}
	status, err := store.UpdateSettings(r.Context(), body.Channel, body.Check)
	if err != nil {
		s.writeUpdateError(w, "settings", err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleUpdateResultClear implements API-UPD-009.
func (s *Server) handleUpdateResultClear(w http.ResponseWriter, r *http.Request) {
	store := s.updateStoreOrError(w)
	if store == nil {
		return
	}
	if err := store.ClearUpdateResult(r.Context()); err != nil {
		s.writeUpdateError(w, "clear result", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeUpdateError maps the UpdateStore errors to the contract codes.
func (s *Server) writeUpdateError(w http.ResponseWriter, action string, err error) {
	switch {
	case errors.Is(err, ErrUpdateInvalidState):
		writeAPIError(w, http.StatusConflict, "invalid_state", err.Error())
	case errors.Is(err, ErrUpdateUnavailable):
		writeAPIError(w, http.StatusConflict, "update_unavailable", err.Error())
	default:
		s.logger.Error("web: update "+action, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func (s *Server) updateStoreOrError(w http.ResponseWriter) UpdateStore {
	if s.update != nil {
		return s.update
	}
	// A backend that implements the port is used without an extra wiring call.
	if store, ok := s.backend.(UpdateStore); ok {
		return store
	}
	s.logger.Error("web: update port is not configured")
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	return nil
}
