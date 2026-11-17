package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/ridhomujizat/lana-harness/core"
)

const (
	// controlLockedMessage is the 403 message of REQ-RA-031.
	controlLockedMessage = "Unlock control to type in Workbench from this browser."
	// controlNotNeededMessage is the 409 message of REQ-RA-032 and REQ-RA-033
	// for a session that does not need to unlock control.
	controlNotNeededMessage = "control is not locked for this session"
	// remoteControlUnlockKind is the audit kind of REQ-RA-072.
	remoteControlUnlockKind = "remote.control_unlock"
	// controlCodeTTL is how long a Telegram control code stays valid
	// (REQ-RA-032).
	controlCodeTTL = 5 * time.Minute
	// controlCodeMaxTries is how many wrong codes void one (REQ-RA-032).
	controlCodeMaxTries = 5
	// controlSendInterval is the shortest gap between two control-code sends
	// of one session (REQ-RA-033).
	controlSendInterval = time.Minute
	// controlLockedReason is the WebSocket close reason of REQ-RA-035.
	controlLockedReason = "control locked"
	// workbenchCloseControlLocked is the attach close code when control ends
	// (REQ-RA-035): the WebSocket private range 4000-4999.
	workbenchCloseControlLocked = websocket.StatusCode(4403)
	// controlWatchDefault is how often a live control attachment rechecks that
	// control still holds (REQ-RA-035).
	controlWatchDefault = 2 * time.Second
)

// controlWatchNanos overrides the poll interval of the control watcher of
// REQ-RA-035 for tests, in nanoseconds; zero uses controlWatchDefault. It is
// atomic so a running watcher never races a test that shortens it.
var controlWatchNanos atomic.Int64

// controlWatchPoll returns the poll interval of the control watcher
// (REQ-RA-035): two seconds by default, or the value a test installed.
func controlWatchPoll() time.Duration {
	if nanos := controlWatchNanos.Load(); nanos > 0 {
		return time.Duration(nanos)
	}
	return controlWatchDefault
}

// controlAttachment identifies one live control attachment in the registry of
// REQ-RA-035.
type controlAttachment struct{}

// controlRegistry maps each session key to the close functions of its live
// control attachments. Locking control or ending a session signals them at
// once; the poll in watchControl covers expiry and a session that went away
// without a signal.
type controlRegistry struct {
	mu          sync.Mutex
	attachments map[[sha256.Size]byte]map[*controlAttachment]func()
}

func newControlRegistry() *controlRegistry {
	return &controlRegistry{attachments: map[[sha256.Size]byte]map[*controlAttachment]func(){}}
}

// add registers the close function of one control attachment.
func (r *controlRegistry) add(key [sha256.Size]byte, att *controlAttachment, closeFn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	set := r.attachments[key]
	if set == nil {
		set = map[*controlAttachment]func(){}
		r.attachments[key] = set
	}
	set[att] = closeFn
}

// remove forgets one control attachment.
func (r *controlRegistry) remove(key [sha256.Size]byte, att *controlAttachment) {
	r.mu.Lock()
	defer r.mu.Unlock()
	set := r.attachments[key]
	if set == nil {
		return
	}
	delete(set, att)
	if len(set) == 0 {
		delete(r.attachments, key)
	}
}

// signal closes every control attachment of the session key (REQ-RA-034,
// REQ-RA-035). The close functions run outside the lock so a close never holds
// it.
func (r *controlRegistry) signal(key [sha256.Size]byte) {
	r.mu.Lock()
	set := r.attachments[key]
	delete(r.attachments, key)
	fns := make([]func(), 0, len(set))
	for _, closeFn := range set {
		fns = append(fns, closeFn)
	}
	r.mu.Unlock()
	for _, closeFn := range fns {
		closeFn()
	}
}

// controlAttachments is the process-wide registry of live control attachments.
// Session keys are per-session secrets, so entries of one server never collide
// with another's.
var controlAttachments = newControlRegistry()

// controlCodeEntry is one Telegram control code: its hash, expiry, wrong-try
// count, and the time it was sent (REQ-RA-032, REQ-RA-033).
type controlCodeEntry struct {
	codeHash  [sha256.Size]byte
	expiresAt time.Time
	tries     int
	sentAt    time.Time
}

// controlCodeStore keeps the Telegram control codes of remote sessions, keyed
// by session key. Only the SHA-256 of a code is kept; a new code replaces the
// old one.
type controlCodeStore struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]*controlCodeEntry
}

func newControlCodeStore() *controlCodeStore {
	return &controlCodeStore{entries: map[[sha256.Size]byte]*controlCodeEntry{}}
}

// start stores a fresh code for key and replaces any earlier one. It reports
// false when the one-per-minute send limit of REQ-RA-033 rejects the send.
func (st *controlCodeStore) start(key [sha256.Size]byte, code string, now time.Time) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if entry, ok := st.entries[key]; ok && now.Before(entry.sentAt.Add(controlSendInterval)) {
		return false
	}
	st.entries[key] = &controlCodeEntry{
		codeHash:  sha256.Sum256([]byte(code)),
		expiresAt: now.Add(controlCodeTTL),
		sentAt:    now,
	}
	return true
}

// verify consumes code for key (REQ-RA-032): a match clears the entry, so a
// code is single use. A wrong code counts a try and the fifth wrong try clears
// the entry; an expired or missing entry does not match.
func (st *controlCodeStore) verify(key [sha256.Size]byte, code string, now time.Time) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	entry, ok := st.entries[key]
	if !ok {
		return false
	}
	if !now.Before(entry.expiresAt) {
		delete(st.entries, key)
		return false
	}
	sum := sha256.Sum256([]byte(code))
	if subtle.ConstantTimeCompare(sum[:], entry.codeHash[:]) != 1 {
		entry.tries++
		if entry.tries >= controlCodeMaxTries {
			delete(st.entries, key)
		}
		return false
	}
	delete(st.entries, key)
	return true
}

// controlCodes is the process-wide Telegram control-code store.
var controlCodes = newControlCodeStore()

// registerRemoteControlRoutes installs the remote terminal control routes of
// REQ-RA-032..035 behind the auth middleware.
func (s *Server) registerRemoteControlRoutes() {
	s.Handle("POST /v1/auth/control/unlock", http.HandlerFunc(s.handleControlUnlock))
	s.Handle("POST /v1/auth/control/telegram", http.HandlerFunc(s.handleControlTelegram))
	s.Handle("POST /v1/auth/control/lock", http.HandlerFunc(s.handleControlLock))
}

// controlAllowed reports whether r may control Workbench terminals
// (REQ-RA-030): always for a local request, and for a remote one when the
// terminal policy is "full" or, with "confirm", the request session's control
// has not expired. The "view" policy never allows control.
func (s *Server) controlAllowed(r *http.Request) bool {
	if _, ok := remoteFromContext(r.Context()); !ok {
		return true
	}
	switch s.remoteConfig().Terminal {
	case core.WebRemoteTerminalFull:
		return true
	case core.WebRemoteTerminalConfirm:
		sess, ok := sessionFromContext(r.Context())
		return ok && !sess.controlUntil.IsZero() && s.nowTime().Before(sess.controlUntil)
	default:
		return false
	}
}

// controlWatchable reports whether a control attachment of r needs the watcher
// of REQ-RA-035: it is remote and the terminal policy is "confirm", the only
// policy where control can lapse while the session lives.
func (s *Server) controlWatchable(r *http.Request) bool {
	if _, ok := remoteFromContext(r.Context()); !ok {
		return false
	}
	return s.remoteConfig().Terminal == core.WebRemoteTerminalConfirm
}

// refuseControlLocked answers 403 control_locked (REQ-RA-031).
func refuseControlLocked(w http.ResponseWriter) {
	writeAPIError(w, http.StatusForbidden, "control_locked", controlLockedMessage)
}

// controlUntil returns a session's control deadline and whether the session
// still exists, read under the store mutex (REQ-RA-035). It lives here rather
// than in browsersessions.go so this task changes no other file.
func (st *browserSessions) controlUntil(key [sha256.Size]byte) (time.Time, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	record, ok := st.records[key]
	if !ok {
		return time.Time{}, false
	}
	return record.controlUntil, true
}

// controlHolds reports whether key still names a session whose control has not
// expired (REQ-RA-035).
func (s *Server) controlHolds(key [sha256.Size]byte) bool {
	until, ok := s.sessions.controlUntil(key)
	return ok && s.nowTime().Before(until)
}

// watchControl closes a live control attachment when its session's control
// lapses or the session ends (REQ-RA-035). closeFn is called at most once by
// its caller: the registry signals it at once on a lock, and the poll covers
// expiry and a session that went away without a signal.
func (s *Server) watchControl(ctx context.Context, key [sha256.Size]byte, att *controlAttachment, closeFn func()) {
	defer controlAttachments.remove(key, att)
	ticker := time.NewTicker(controlWatchPoll())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.controlHolds(key) {
				closeFn()
				return
			}
		}
	}
}

// handleControlUnlock answers POST /v1/auth/control/unlock (REQ-RA-032): the
// Lana password or a Telegram code lifts the control lock of the caller's
// session for control_minutes. Only a remote session with terminal = confirm
// may call it.
func (s *Server) handleControlUnlock(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	cfg := s.remoteConfig()
	if _, isRemote := remoteFromContext(r.Context()); !isRemote || cfg.Terminal != core.WebRemoteTerminalConfirm {
		writeAPIError(w, http.StatusConflict, "not_needed", controlNotNeededMessage)
		return
	}
	var body struct {
		Password *string `json:"password"`
		Code     *string `json:"code"`
	}
	if !decodeJSONObject(w, r, authMaxBody, &body) {
		return
	}
	if (body.Password == nil) == (body.Code == nil) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must name exactly one of password or code")
		return
	}
	method := "password"
	if body.Password != nil {
		if !s.verifyControlPassword(w, *body.Password) {
			return
		}
	} else if cfg.ConfirmWith != core.WebRemoteConfirmPasswordOrTelegram {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "a code is not accepted with this configuration")
		return
	} else {
		method = "telegram"
		if !controlCodes.verify(sess.key, *body.Code, s.nowTime()) {
			writeAPIError(w, http.StatusUnauthorized, "invalid_code", invalidCodeMessage)
			return
		}
	}
	until := s.nowTime().Add(time.Duration(cfg.ControlMinutes) * time.Minute)
	if err := s.sessions.setControlUntil(sess.key, until); err != nil {
		s.logger.Warn("web: unlock control", "error", err)
	}
	s.auditControlUnlock(sess.key, method)
	writeJSON(w, http.StatusOK, struct {
		ControlUntil int64 `json:"control_until"`
	}{ControlUntil: until.UnixMilli()})
}

// verifyControlPassword checks the Lana password shared with login, including
// its lockout (REQ-RA-032), and writes the matching error. It reports whether
// the unlock may continue.
func (s *Server) verifyControlPassword(w http.ResponseWriter, password string) bool {
	if s.credentials == nil || !s.credentials.PasswordSet() {
		writeAPIError(w, http.StatusUnauthorized, "invalid_password", invalidPasswordMessage)
		return false
	}
	if s.loginLock.lockedUntilTime() != (time.Time{}) {
		writeAPIError(w, http.StatusTooManyRequests, "too_many_attempts", tooManyAttemptsMessage)
		return false
	}
	if !s.credentials.VerifyPassword(password) {
		s.loginLock.fail()
		s.logger.Info("web: control unlock failed")
		writeAPIError(w, http.StatusUnauthorized, "invalid_password", invalidPasswordMessage)
		return false
	}
	s.loginLock.success()
	return true
}

// handleControlTelegram answers POST /v1/auth/control/telegram (REQ-RA-033):
// it sends a fresh 6-digit code to the owner's Telegram chats. Only a remote
// session with terminal = confirm and confirm_with = password_or_telegram may
// call it, at most once a minute.
func (s *Server) handleControlTelegram(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	remote, isRemote := remoteFromContext(r.Context())
	cfg := s.remoteConfig()
	if !isRemote || cfg.Terminal != core.WebRemoteTerminalConfirm || cfg.ConfirmWith != core.WebRemoteConfirmPasswordOrTelegram {
		writeAPIError(w, http.StatusConflict, "not_needed", controlNotNeededMessage)
		return
	}
	messenger := s.telegramOwnerMessenger()
	if messenger == nil || !s.telegramResetAvailable() {
		writeAPIError(w, http.StatusConflict, "telegram_unavailable", "Telegram is not connected, so a control code cannot be sent.")
		return
	}
	code, err := newResetCode()
	if err != nil {
		s.logger.Error("web: control telegram: generate code", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !controlCodes.start(sess.key, code, s.nowTime()) {
		writeAPIError(w, http.StatusTooManyRequests, "too_many_attempts", tooManyAttemptsMessage)
		return
	}
	device := sess.device
	if device == "" {
		device = s.whoisDevice(r.Context(), remote.IP)
	}
	if device == "" {
		device = remote.IP
	}
	sent, err := messenger.SendOwnerMessage(r.Context(), controlCodeMessage(code, device, cfg.ControlMinutes))
	if err != nil {
		s.logger.Error("web: control telegram: send code", "error", err)
		writeAPIError(w, http.StatusConflict, "telegram_unavailable", "Telegram is not connected, so a control code cannot be sent.")
		return
	}
	writeJSON(w, http.StatusAccepted, struct {
		SentTo int `json:"sent_to"`
	}{SentTo: sent})
}

// controlCodeMessage is the plain-text owner message of REQ-RA-033.
func controlCodeMessage(code, device string, minutes int) string {
	return fmt.Sprintf("Lana control code: %s\nIt unlocks typing in Workbench on %s for %d minutes. It expires in %d minutes.",
		code, device, minutes, int(controlCodeTTL/time.Minute))
}

// handleControlLock answers POST /v1/auth/control/lock (REQ-RA-034): it locks
// the caller's control and closes its control attachments (REQ-RA-035).
func (s *Server) handleControlLock(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if err := s.sessions.setControlUntil(sess.key, time.Time{}); err != nil {
		s.logger.Warn("web: lock control", "error", err)
	}
	controlAttachments.signal(sess.key)
	writeJSON(w, http.StatusOK, struct{}{})
}

// auditControlUnlock appends the REQ-RA-072 line for a successful unlock: the
// session's 12-hex display id as the target, and the method (password or
// telegram) among the changed keys.
func (s *Server) auditControlUnlock(key [sha256.Size]byte, method string) {
	displayID := hex.EncodeToString(key[:])[:sessionIDDisplayHex]
	s.auditSave(remoteControlUnlockKind, "", displayID, []string{method})
}
