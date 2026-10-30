package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	tokenFileName     = "web.json"
	tokenVersion      = 1
	sessionCookieName = "lana_session"
	csrfHeaderName    = "X-Lana-CSRF"
	sessionIdleTTL    = 12 * time.Hour
	loginMaxBody      = 4 << 10
	randomBytesLen    = 32
)

// tokenFile is the on-disk form of $LANA_HOME/web.json.
type tokenFile struct {
	Version     int    `json:"version"`
	TokenSHA256 string `json:"token_sha256"`
}

// tokenStore holds the SHA-256 of the owner token.
type tokenStore struct {
	hash [sha256.Size]byte
}

// loadOrCreateToken reads $LANA_HOME/web.json, creating it when missing.
// An empty home means the token store is absent; login then always fails.
func loadOrCreateToken(home string, stderr io.Writer) (*tokenStore, error) {
	if home == "" {
		return nil, nil
	}
	path, err := filepath.Abs(filepath.Join(home, tokenFileName))
	if err != nil {
		return nil, tokenFileError(filepath.Join(home, tokenFileName), err.Error())
	}
	plain, err := createTokenFile(path)
	if err == nil {
		if stderr == nil {
			stderr = io.Discard
		}
		_, _ = fmt.Fprintf(stderr, "web: owner token created at %s; copy it now, it is shown only once: %s\n", path, plain)
		return &tokenStore{hash: sha256.Sum256([]byte(plain))}, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, tokenFileError(path, err.Error())
	}
	return readTokenFile(path)
}

// createTokenFile creates path with mode 0600 and returns the plain token.
// It returns os.ErrExist when the file already exists.
func createTokenFile(path string) (string, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}

	plain, err := randomToken()
	if err != nil {
		return fail(err)
	}
	sum := sha256.Sum256([]byte(plain))
	data, err := json.Marshal(tokenFile{Version: tokenVersion, TokenSHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		return fail(err)
	}
	if err := file.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := file.Write(data); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return plain, nil
}

// readTokenFile parses an existing token file and validates its contents.
func readTokenFile(path string) (*tokenStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, tokenFileError(path, "cannot read the file")
	}
	var stored tokenFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, tokenFileError(path, "not valid JSON")
	}
	if stored.Version != tokenVersion {
		return nil, tokenFileError(path, fmt.Sprintf("unsupported version %d", stored.Version))
	}
	if !isLowerHexSHA256(stored.TokenSHA256) {
		return nil, tokenFileError(path, "token_sha256 must be 64 lowercase hex characters")
	}
	hash, err := hex.DecodeString(stored.TokenSHA256)
	if err != nil {
		return nil, tokenFileError(path, "token_sha256 must be 64 lowercase hex characters")
	}
	var store tokenStore
	copy(store.hash[:], hash)
	return &store, nil
}

func tokenFileError(path, reason string) error {
	return fmt.Errorf("web: token file %s cannot be used: %s; delete it and restart the daemon to create a new token", path, reason)
}

func isLowerHexSHA256(value string) bool {
	if len(value) != 2*sha256.Size {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func randomToken() (string, error) {
	buf := make([]byte, randomBytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// valid reports whether token matches the stored hash in constant time.
func (t *tokenStore) valid(token string) bool {
	if t == nil || token == "" {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(sum[:], t.hash[:]) == 1
}

// session is the session data put in an authenticated request context.
type session struct {
	key       [sha256.Size]byte
	csrf      string
	csrfHash  [sha256.Size]byte
	expiresAt time.Time
}

type contextKey int

const sessionContextKey contextKey = iota

// sessionFromContext returns the browser session of an authenticated request.
func sessionFromContext(ctx context.Context) (session, bool) {
	sess, ok := ctx.Value(sessionContextKey).(session)
	return sess, ok
}

func (s *Server) nowTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// authenticate applies the Host, Origin, cookie, and CSRF checks in order.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return s.checkHost(s.checkOrigin(s.requireSession(s.checkCSRF(next))))
}

func (s *Server) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Host, s.Addr()) {
			writeAPIError(w, http.StatusForbidden, "forbidden_host", "host is not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStateChanging(r.Method) {
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+s.Addr() {
				writeAPIError(w, http.StatusForbidden, "forbidden_origin", "origin is not allowed")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		sess, ok, err := s.sessions.touch(cookie.Value, s.nowTime())
		if err != nil {
			s.logger.Warn("web: save browser session", "error", err)
		}
		if !ok {
			writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionContextKey, sess)))
	})
}

func (s *Server) checkCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStateChanging(r.Method) {
			sess, ok := sessionFromContext(r.Context())
			got := r.Header.Get(csrfHeaderName)
			gotHash := sha256.Sum256([]byte(got))
			if !ok || got == "" || subtle.ConstantTimeCompare(gotHash[:], sess.csrfHash[:]) != 1 {
				writeAPIError(w, http.StatusForbidden, "csrf_invalid", "CSRF token is missing or invalid")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token *string `json:"token"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, loginMaxBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Token == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object with a token string")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return
	}

	now := s.nowTime()
	if !s.token.valid(*body.Token) {
		s.logger.Info("web: login failed")
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "invalid owner token")
		return
	}
	cookie, err := randomToken()
	if err != nil {
		s.logger.Error("web: login: generate session cookie", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if _, err := s.sessions.add(cookie, r.UserAgent(), now); err != nil {
		s.logger.Error("web: login: save browser session", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    cookie,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	s.logger.Info("web: login succeeded")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sess, ok := sessionFromContext(r.Context()); ok {
		if err := s.sessions.delete(sess.key); err != nil {
			s.logger.Warn("web: logout: save browser sessions", "error", err)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		CSRF      string `json:"csrf"`
		ExpiresAt int64  `json:"expires_at"`
	}{
		CSRF:      sess.csrf,
		ExpiresAt: sess.expiresAt.UnixMilli(),
	})
}

type apiError struct {
	Code    string           `json:"code"`
	Message string           `json:"message"`
	Details []map[string]any `json:"details,omitempty"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeAPIErrorDetails(w, status, code, message, nil)
}

func writeAPIErrorDetails(w http.ResponseWriter, status int, code, message string, details []map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: apiError{Code: code, Message: message, Details: details}})
}
