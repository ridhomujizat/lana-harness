package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

const (
	// lastUsedWriteInterval throttles writes of last_used_at to the session
	// file. Every request refreshes the in-memory value; only a move of more
	// than a minute is persisted (design.md, "Browser session records").
	lastUsedWriteInterval = time.Minute
	// sessionIDDisplayHex is the prefix length of the session id hash that
	// API-WEB-024 returns.
	sessionIDDisplayHex = 12
	// remoteIdleDefault is the idle limit of a remote session (REQ-RA-021,
	// the idle_minutes default).
	remoteIdleDefault = 60 * time.Minute
	// remoteMaxAgeDefault is the absolute lifetime of a remote session
	// (REQ-RA-021, the max_age_hours default).
	remoteMaxAgeDefault = 168 * time.Hour
)

// remoteLimits holds the remote session TTLs (REQ-RA-021): idle is the quiet
// time that ends a remote session and maxAge is the time from its start. Both
// are read under the store mutex.
type remoteLimits struct {
	idle   time.Duration
	maxAge time.Duration
}

// remoteIdentity is the Tailscale identity recorded for a remote session
// (REQ-RA-020).
type remoteIdentity struct {
	Login  string
	Device string
	IP     string
}

// browserSessionRecord is one logged-in browser. Cookie and CSRF values are
// never stored: only their SHA-256 hashes and times.
type browserSessionRecord struct {
	csrfHash          [sha256.Size]byte
	createdAt         time.Time
	lastUsedAt        time.Time
	persistedLastUsed time.Time
	userAgent         string
	// The remote-access fields (REQ-RA-020). remote is false and the rest are
	// empty on a local session. controlUntil is the zero time when control is
	// locked.
	remote       bool
	login        string
	device       string
	ip           string
	controlUntil time.Time
}

// browserSessionInfo is one row of GET /v1/auth/sessions (API-WEB-024).
type browserSessionInfo struct {
	ID         string `json:"id"`
	UserAgent  string `json:"user_agent"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"`
	Current    bool   `json:"current"`
	// The remote-access fields of REQ-RA-024. control_until is a Unix
	// millisecond time, 0 when control is locked.
	Remote       bool   `json:"remote"`
	Login        string `json:"login,omitempty"`
	Device       string `json:"device,omitempty"`
	IP           string `json:"ip,omitempty"`
	ControlUntil int64  `json:"control_until,omitempty"`
}

// browserSessions keeps the in-memory copy of web-sessions.json. A path of ""
// means no persistence (the daemon has no LANA_HOME).
type browserSessions struct {
	path    string
	mu      sync.Mutex
	records map[[sha256.Size]byte]*browserSessionRecord
	limits  remoteLimits
}

func newBrowserSessions(path string) *browserSessions {
	return &browserSessions{
		path:    path,
		records: make(map[[sha256.Size]byte]*browserSessionRecord),
		limits:  remoteLimits{idle: remoteIdleDefault, maxAge: remoteMaxAgeDefault},
	}
}

// setRemoteLimits installs the remote session TTLs (REQ-RA-021). A value of
// zero or less keeps the default, so a missing setting never disables expiry.
func (st *browserSessions) setRemoteLimits(idle, maxAge time.Duration) {
	if idle <= 0 {
		idle = remoteIdleDefault
	}
	if maxAge <= 0 {
		maxAge = remoteMaxAgeDefault
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.limits = remoteLimits{idle: idle, maxAge: maxAge}
}

// loadBrowserSessions reads $LANA_HOME/web-sessions.json. A missing file has
// no records; a file that cannot be parsed is an error that names the file so
// the daemon exits with code 2 (Q-020).
func loadBrowserSessions(home string, now time.Time) (*browserSessions, error) {
	store := newBrowserSessions("")
	if home == "" {
		return store, nil
	}
	store.path = filepath.Join(home, webfiles.SessionsFileName)
	if err := store.load(now); err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	return store, nil
}

func (st *browserSessions) load(now time.Time) error {
	file, err := webfiles.ReadSessionsFile(st.path)
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, stored := range file.Sessions {
		key, err := parseSessionHash(stored.IDHash)
		if err != nil {
			return fmt.Errorf("web: %w", err)
		}
		csrfHash, err := parseSessionHash(stored.CSRFHash)
		if err != nil {
			return fmt.Errorf("web: %w", err)
		}
		record := &browserSessionRecord{
			csrfHash:          csrfHash,
			createdAt:         time.UnixMilli(stored.CreatedAt),
			lastUsedAt:        time.UnixMilli(stored.LastUsedAt),
			persistedLastUsed: time.UnixMilli(stored.LastUsedAt),
			userAgent:         truncateSessionUserAgent(stored.UserAgent),
			remote:            stored.Remote,
			login:             truncateRemoteLabel(stored.Login),
			device:            truncateRemoteLabel(stored.Device),
			ip:                truncateRemoteLabel(stored.IP),
			controlUntil:      unixMilliOrZero(stored.ControlUntil),
		}
		if st.expiredLocked(record, now) {
			continue
		}
		st.records[key] = record
	}
	return nil
}

// expiredLocked reports whether a record has reached its expiry. A local
// record expires after sessionIdleTTL idle; a remote record expires after its
// remote idle limit or maxAge after it started, whichever comes first
// (REQ-RA-021). The caller holds the lock.
func (st *browserSessions) expiredLocked(record *browserSessionRecord, now time.Time) bool {
	if !record.remote {
		return !now.Before(record.lastUsedAt.Add(sessionIdleTTL))
	}
	if !now.Before(record.lastUsedAt.Add(st.limits.idle)) {
		return true
	}
	return !now.Before(record.createdAt.Add(st.limits.maxAge))
}

// expiresAtLocked returns the moment a record lapses, for the session data of
// an authenticated request. A remote record reports the earlier of its idle
// deadline and its max-age deadline (REQ-RA-021). The caller holds the lock.
func (st *browserSessions) expiresAtLocked(record *browserSessionRecord, now time.Time) time.Time {
	if !record.remote {
		return now.Add(sessionIdleTTL)
	}
	idle := now.Add(st.limits.idle)
	maxAge := record.createdAt.Add(st.limits.maxAge)
	if maxAge.Before(idle) {
		return maxAge
	}
	return idle
}

// sessionLocked builds the session data of a record for an authenticated
// request. The caller holds the lock.
func (st *browserSessions) sessionLocked(key [sha256.Size]byte, record *browserSessionRecord, cookie string, now time.Time) session {
	return session{
		key:          key,
		csrf:         csrfValue(cookie),
		csrfHash:     record.csrfHash,
		expiresAt:    st.expiresAtLocked(record, now),
		remote:       record.remote,
		login:        record.login,
		device:       record.device,
		controlUntil: record.controlUntil,
	}
}

// add records a new local browser session and writes the file.
func (st *browserSessions) add(cookie, userAgent string, now time.Time) (session, error) {
	return st.addRecord(cookie, userAgent, remoteIdentity{}, false, now)
}

// addRemote records a new remote browser session and writes the file
// (REQ-RA-020). id holds the Tailscale login, machine name, and client IP.
func (st *browserSessions) addRemote(cookie, userAgent string, id remoteIdentity, now time.Time) (session, error) {
	return st.addRecord(cookie, userAgent, id, true, now)
}

func (st *browserSessions) addRecord(cookie, userAgent string, id remoteIdentity, remote bool, now time.Time) (session, error) {
	key := sessionKey(cookie)
	csrf := csrfValue(cookie)
	record := &browserSessionRecord{
		csrfHash:          sha256.Sum256([]byte(csrf)),
		createdAt:         now,
		lastUsedAt:        now,
		persistedLastUsed: now,
		userAgent:         truncateSessionUserAgent(userAgent),
		remote:            remote,
		login:             truncateRemoteLabel(id.Login),
		device:            truncateRemoteLabel(id.Device),
		ip:                truncateRemoteLabel(id.IP),
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.pruneLocked(now)
	st.records[key] = record
	if err := st.writeLocked(); err != nil {
		delete(st.records, key)
		return session{}, err
	}
	return st.sessionLocked(key, record, cookie, now), nil
}

// touch validates the cookie, refreshes its idle expiry, and persists
// last_used_at at most once a minute. A write error does not fail the request;
// the caller logs it and the next write retries.
func (st *browserSessions) touch(cookie string, now time.Time) (session, bool, error) {
	key := sessionKey(cookie)
	st.mu.Lock()
	defer st.mu.Unlock()
	record, ok := st.records[key]
	if !ok {
		return session{}, false, nil
	}
	if st.expiredLocked(record, now) {
		delete(st.records, key)
		return session{}, false, st.writeLocked()
	}
	record.lastUsedAt = now
	var err error
	if now.Sub(record.persistedLastUsed) >= lastUsedWriteInterval {
		record.persistedLastUsed = now
		err = st.writeLocked()
	}
	return st.sessionLocked(key, record, cookie, now), true, err
}

// delete removes the session and writes the file.
func (st *browserSessions) delete(key [sha256.Size]byte) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, ok := st.records[key]; !ok {
		return nil
	}
	delete(st.records, key)
	return st.writeLocked()
}

// list returns the unexpired records, oldest first.
func (st *browserSessions) list(current [sha256.Size]byte, now time.Time) []browserSessionInfo {
	st.mu.Lock()
	defer st.mu.Unlock()
	infos := make([]browserSessionInfo, 0, len(st.records))
	for key, record := range st.records {
		if st.expiredLocked(record, now) {
			continue
		}
		info := browserSessionInfo{
			ID:         hex.EncodeToString(key[:])[:sessionIDDisplayHex],
			UserAgent:  record.userAgent,
			CreatedAt:  record.createdAt.UnixMilli(),
			LastUsedAt: record.lastUsedAt.UnixMilli(),
			Current:    key == current,
			Remote:     record.remote,
			Login:      record.login,
			Device:     record.device,
			IP:         record.ip,
		}
		if !record.controlUntil.IsZero() {
			info.ControlUntil = record.controlUntil.UnixMilli()
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].CreatedAt != infos[j].CreatedAt {
			return infos[i].CreatedAt < infos[j].CreatedAt
		}
		return infos[i].ID < infos[j].ID
	})
	return infos
}

// revokeOthers removes every record except current and returns the count.
func (st *browserSessions) revokeOthers(current [sha256.Size]byte) (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	revoked := 0
	for key := range st.records {
		if key == current {
			continue
		}
		delete(st.records, key)
		revoked++
	}
	if revoked == 0 {
		return 0, nil
	}
	return revoked, st.writeLocked()
}

// setControlUntil stores the control deadline of one session and writes the
// file (REQ-RA-032, REQ-RA-034). A zero time locks control again. A key with
// no record is a no-op.
func (st *browserSessions) setControlUntil(key [sha256.Size]byte, until time.Time) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	record, ok := st.records[key]
	if !ok {
		return nil
	}
	record.controlUntil = until
	return st.writeLocked()
}

// revokeByID removes the session whose 12-hex display id matches displayID and
// returns its key (REQ-RA-025). ok is false when no record matches, so the
// caller answers 404.
func (st *browserSessions) revokeByID(displayID string) (key [sha256.Size]byte, ok bool, err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(displayID) != sessionIDDisplayHex {
		return key, false, nil
	}
	for candidate := range st.records {
		if hex.EncodeToString(candidate[:])[:sessionIDDisplayHex] != displayID {
			continue
		}
		delete(st.records, candidate)
		return candidate, true, st.writeLocked()
	}
	return key, false, nil
}

// revokeRemote removes every remote record and returns the count (REQ-RA-025).
func (st *browserSessions) revokeRemote() (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	revoked := 0
	for key, record := range st.records {
		if !record.remote {
			continue
		}
		delete(st.records, key)
		revoked++
	}
	if revoked == 0 {
		return 0, nil
	}
	return revoked, st.writeLocked()
}

// remoteKeys returns the keys of every remote record. The daemon uses them to
// close the control attachments when remote access goes off (REQ-RA-063).
func (st *browserSessions) remoteKeys() [][sha256.Size]byte {
	st.mu.Lock()
	defer st.mu.Unlock()
	keys := make([][sha256.Size]byte, 0)
	for key, record := range st.records {
		if record.remote {
			keys = append(keys, key)
		}
	}
	return keys
}

// pruneLocked removes expired records. The caller holds the lock.
func (st *browserSessions) pruneLocked(now time.Time) bool {
	removed := false
	for key, record := range st.records {
		if st.expiredLocked(record, now) {
			delete(st.records, key)
			removed = true
		}
	}
	return removed
}

// writeLocked writes the current records. The caller holds the lock.
func (st *browserSessions) writeLocked() error {
	if st.path == "" {
		return nil
	}
	file := webfiles.SessionsFile{
		Version:  webfiles.SessionsFileVersion,
		Sessions: make([]webfiles.SessionRecord, 0, len(st.records)),
	}
	for key, record := range st.records {
		controlUntil := int64(0)
		if !record.controlUntil.IsZero() {
			controlUntil = record.controlUntil.UnixMilli()
		}
		file.Sessions = append(file.Sessions, webfiles.SessionRecord{
			IDHash:       hex.EncodeToString(key[:]),
			CSRFHash:     hex.EncodeToString(record.csrfHash[:]),
			CreatedAt:    record.createdAt.UnixMilli(),
			LastUsedAt:   record.lastUsedAt.UnixMilli(),
			UserAgent:    record.userAgent,
			Remote:       record.remote,
			Login:        record.login,
			Device:       record.device,
			IP:           record.ip,
			ControlUntil: controlUntil,
		})
	}
	sort.Slice(file.Sessions, func(i, j int) bool {
		if file.Sessions[i].CreatedAt != file.Sessions[j].CreatedAt {
			return file.Sessions[i].CreatedAt < file.Sessions[j].CreatedAt
		}
		return file.Sessions[i].IDHash < file.Sessions[j].IDHash
	})
	return webfiles.WriteSessionsFile(st.path, file)
}

// sessionKey is the map key and the stored id_hash.
func sessionKey(cookie string) [sha256.Size]byte {
	return sha256.Sum256([]byte(cookie))
}

// csrfValue derives the CSRF value from the session cookie with a domain
// separator. It is 32 bytes, base64url, and it is never stored in plain text:
// the file keeps its SHA-256 only. Deriving it from the cookie lets
// GET /v1/auth/session answer with the same value after a restart, when only
// the hash is left on disk. Another origin cannot read the HttpOnly cookie, so
// the value stays unguessable to it.
func csrfValue(cookie string) string {
	sum := sha256.Sum256([]byte("lana-csrf\x00" + cookie))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func parseSessionHash(value string) ([sha256.Size]byte, error) {
	var out [sha256.Size]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return out, fmt.Errorf("session hash %q is not 64 lowercase hex characters", value)
	}
	copy(out[:], decoded)
	return out, nil
}

func truncateSessionUserAgent(value string) string {
	if utf8.RuneCountInString(value) <= webfiles.MaxUserAgentChars {
		return value
	}
	runes := []rune(value)
	return string(runes[:webfiles.MaxUserAgentChars])
}

// truncateRemoteLabel caps a login, device, or IP label like the user agent.
func truncateRemoteLabel(value string) string {
	if utf8.RuneCountInString(value) <= webfiles.MaxRemoteLabelChars {
		return value
	}
	runes := []rune(value)
	return string(runes[:webfiles.MaxRemoteLabelChars])
}

// unixMilliOrZero converts a stored Unix-millisecond time. Zero means "not
// set" and maps to the zero time, not 1970.
func unixMilliOrZero(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// handleBrowserSessions answers GET /v1/auth/sessions (API-WEB-024).
func (s *Server) handleBrowserSessions(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Sessions []browserSessionInfo `json:"sessions"`
	}{
		Sessions: s.sessions.list(sess.key, s.nowTime()),
	})
}

// handleRevokeOtherBrowsers answers POST /v1/auth/sessions/revoke-others
// (API-WEB-024). The calling browser stays signed in.
func (s *Server) handleRevokeOtherBrowsers(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	revoked, err := s.sessions.revokeOthers(sess.key)
	if err != nil {
		s.logger.Error("web: revoke other browsers", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Revoked int `json:"revoked"`
	}{Revoked: revoked})
}
