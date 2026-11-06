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
)

// browserSessionRecord is one logged-in browser. Cookie and CSRF values are
// never stored: only their SHA-256 hashes and times.
type browserSessionRecord struct {
	csrfHash          [sha256.Size]byte
	createdAt         time.Time
	lastUsedAt        time.Time
	persistedLastUsed time.Time
	userAgent         string
}

// browserSessionInfo is one row of GET /v1/auth/sessions (API-WEB-024).
type browserSessionInfo struct {
	ID         string `json:"id"`
	UserAgent  string `json:"user_agent"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"`
	Current    bool   `json:"current"`
}

// browserSessions keeps the in-memory copy of web-sessions.json. A path of ""
// means no persistence (the daemon has no LANA_HOME).
type browserSessions struct {
	path    string
	mu      sync.Mutex
	records map[[sha256.Size]byte]*browserSessionRecord
}

func newBrowserSessions(path string) *browserSessions {
	return &browserSessions{
		path:    path,
		records: make(map[[sha256.Size]byte]*browserSessionRecord),
	}
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
	for _, record := range file.Sessions {
		key, err := parseSessionHash(record.IDHash)
		if err != nil {
			return fmt.Errorf("web: %w", err)
		}
		csrfHash, err := parseSessionHash(record.CSRFHash)
		if err != nil {
			return fmt.Errorf("web: %w", err)
		}
		createdAt := time.UnixMilli(record.CreatedAt)
		lastUsedAt := time.UnixMilli(record.LastUsedAt)
		if !now.Before(lastUsedAt.Add(sessionIdleTTL)) {
			continue
		}
		st.records[key] = &browserSessionRecord{
			csrfHash:          csrfHash,
			createdAt:         createdAt,
			lastUsedAt:        lastUsedAt,
			persistedLastUsed: lastUsedAt,
			userAgent:         truncateSessionUserAgent(record.UserAgent),
		}
	}
	return nil
}

// add records a new browser session and writes the file.
func (st *browserSessions) add(cookie, userAgent string, now time.Time) (session, error) {
	key := sessionKey(cookie)
	csrf := csrfValue(cookie)
	record := &browserSessionRecord{
		csrfHash:          sha256.Sum256([]byte(csrf)),
		createdAt:         now,
		lastUsedAt:        now,
		persistedLastUsed: now,
		userAgent:         truncateSessionUserAgent(userAgent),
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.pruneLocked(now)
	st.records[key] = record
	if err := st.writeLocked(); err != nil {
		delete(st.records, key)
		return session{}, err
	}
	return session{
		key:       key,
		csrf:      csrf,
		csrfHash:  record.csrfHash,
		expiresAt: now.Add(sessionIdleTTL),
	}, nil
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
	if !now.Before(record.lastUsedAt.Add(sessionIdleTTL)) {
		delete(st.records, key)
		return session{}, false, st.writeLocked()
	}
	record.lastUsedAt = now
	var err error
	if now.Sub(record.persistedLastUsed) >= lastUsedWriteInterval {
		record.persistedLastUsed = now
		err = st.writeLocked()
	}
	return session{
		key:       key,
		csrf:      csrfValue(cookie),
		csrfHash:  record.csrfHash,
		expiresAt: now.Add(sessionIdleTTL),
	}, true, err
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
		if !now.Before(record.lastUsedAt.Add(sessionIdleTTL)) {
			continue
		}
		infos = append(infos, browserSessionInfo{
			ID:         hex.EncodeToString(key[:])[:sessionIDDisplayHex],
			UserAgent:  record.userAgent,
			CreatedAt:  record.createdAt.UnixMilli(),
			LastUsedAt: record.lastUsedAt.UnixMilli(),
			Current:    key == current,
		})
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

// pruneLocked removes expired records. The caller holds the lock.
func (st *browserSessions) pruneLocked(now time.Time) bool {
	removed := false
	for key, record := range st.records {
		if !now.Before(record.lastUsedAt.Add(sessionIdleTTL)) {
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
		file.Sessions = append(file.Sessions, webfiles.SessionRecord{
			IDHash:     hex.EncodeToString(key[:]),
			CSRFHash:   hex.EncodeToString(record.csrfHash[:]),
			CreatedAt:  record.createdAt.UnixMilli(),
			LastUsedAt: record.lastUsedAt.UnixMilli(),
			UserAgent:  record.userAgent,
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
