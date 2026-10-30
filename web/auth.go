package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	sessionCookieName = "lana_session"
	csrfHeaderName    = "X-Lana-CSRF"
	sessionIdleTTL    = 12 * time.Hour
	authMaxBody       = 4 << 10
	randomBytesLen    = 32

	// The UI messages of the contract (api-contract.md, "Error messages for
	// the UI").
	invalidPasswordMessage     = "That password is not right."
	tooManyAttemptsMessage     = "Too many tries. Wait 5 minutes or reset your password."
	linkExpiredMessage         = "This link has expired. Restart lana to get a new one."
	codeExpiredMessage         = "Ask for a new code."
	invalidCodeMessage         = "That code is not right."
	telegramUnavailableMessage = "Telegram reset is not available."
)

// TelegramResetSender is the daemon port that delivers a reset code to the
// owners' Telegram chats (REQ-WLG-004). cmd/lana implements it with the running
// Telegram channel.
type TelegramResetSender interface {
	// ResetAvailable reports that a Telegram reset code can be sent: the
	// channel is polling and at least one owner has a private-chat route.
	ResetAvailable() bool
	// SendOwnerCode sends code to every owner private chat and returns the
	// number of messages sent.
	SendOwnerCode(ctx context.Context, code string) (int, error)
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

// SetTelegramResetSender installs the Telegram reset port. The daemon calls it
// before the server starts serving.
func (s *Server) SetTelegramResetSender(sender TelegramResetSender) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.telegramReset = sender
}

func (s *Server) telegramResetSender() TelegramResetSender {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.telegramReset
}

func (s *Server) telegramResetAvailable() bool {
	if sender := s.telegramResetSender(); sender != nil {
		return sender.ResetAvailable()
	}
	return false
}

// SignInLink creates a one-time sign-in link for the server address. The code
// lives in memory only and is valid for 15 minutes (REQ-WLG-002).
func (s *Server) SignInLink() (string, error) {
	code, err := s.signInCodes.create()
	if err != nil {
		return "", err
	}
	return "http://" + s.Addr() + "/#/welcome?code=" + code, nil
}

// OpenOnStart runs REQ-WLG-001: when no password is set or a reset was
// requested, it creates a sign-in link, writes it once to stderr, and opens it
// with open when [web] open_browser is on. The daemon calls it after the
// listener is up. reset_requested is cleared by SetPassword, so the flag
// survives until the owner sets a password through the link.
func (s *Server) OpenOnStart(open func(string) error) {
	if s.credentials == nil {
		return
	}
	if s.credentials.PasswordSet() && !s.credentials.ResetRequested() {
		return
	}
	link, err := s.SignInLink()
	if err != nil {
		s.logger.Error("web: create sign-in link", "error", err)
		return
	}
	_, _ = fmt.Fprintf(s.cfg.Stderr, "web: open this link to set your password: %s\n", link)
	if !s.cfg.OpenBrowser || open == nil {
		return
	}
	if err := open(link); err != nil {
		s.logger.Warn("web: open browser", "error", err)
	}
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

// handleAuthState answers GET /v1/auth/state (API-WLG-001).
func (s *Server) handleAuthState(w http.ResponseWriter, _ *http.Request) {
	response := struct {
		PasswordSet   bool   `json:"password_set"`
		TelegramReset bool   `json:"telegram_reset"`
		LockedUntil   *int64 `json:"locked_until"`
	}{
		PasswordSet:   s.credentials.PasswordSet(),
		TelegramReset: s.telegramResetAvailable(),
	}
	if until := s.loginLock.lockedUntilTime(); !until.IsZero() {
		ms := until.UnixMilli()
		response.LockedUntil = &ms
	}
	writeJSON(w, http.StatusOK, response)
}

// handleClaim answers POST /v1/auth/claim (API-WLG-002): a valid sign-in code
// plus a new password sets the password, consumes the code, and signs in.
func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if !decodeJSONObject(w, r, authMaxBody, &body) {
		return
	}
	if len([]rune(body.Password)) < minPasswordChars {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "password must be at least 8 characters")
		return
	}
	if s.credentials == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !s.signInCodes.consume(body.Code) {
		writeAPIError(w, http.StatusGone, "link_expired", linkExpiredMessage)
		return
	}
	if err := s.applyPassword(body.Password, nil); err != nil {
		s.logger.Error("web: claim: set password", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.issueSession(w, r)
}

// handleLogin answers POST /v1/auth/login (API-WLG-003). A password works once
// one is set; the legacy owner token works only while no password is set.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password *string `json:"password"`
		Token    *string `json:"token"`
	}
	if !decodeJSONObject(w, r, authMaxBody, &body) {
		return
	}
	if (body.Password == nil) == (body.Token == nil) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must name exactly one of password or token")
		return
	}
	if body.Password != nil {
		if s.credentials == nil || !s.credentials.PasswordSet() {
			writeAPIError(w, http.StatusUnauthorized, "invalid_password", invalidPasswordMessage)
			return
		}
		if s.loginLock.lockedUntilTime() != (time.Time{}) {
			writeAPIError(w, http.StatusTooManyRequests, "too_many_attempts", tooManyAttemptsMessage)
			return
		}
		if !s.credentials.VerifyPassword(*body.Password) {
			s.loginLock.fail()
			s.logger.Info("web: login failed")
			writeAPIError(w, http.StatusUnauthorized, "invalid_password", invalidPasswordMessage)
			return
		}
		s.loginLock.success()
	} else if s.credentials == nil || !s.credentials.VerifyToken(*body.Token) {
		s.logger.Info("web: login failed")
		writeAPIError(w, http.StatusUnauthorized, "invalid_password", invalidPasswordMessage)
		return
	}
	s.logger.Info("web: login succeeded")
	s.issueSession(w, r)
}

// handleResetTelegram answers POST /v1/auth/reset/telegram (API-WLG-004).
func (s *Server) handleResetTelegram(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !decodeJSONObject(w, r, authMaxBody, &body) {
		return
	}
	sender := s.telegramResetSender()
	if sender == nil || !sender.ResetAvailable() {
		writeAPIError(w, http.StatusConflict, "telegram_unavailable", telegramUnavailableMessage)
		return
	}
	code, err := newResetCode()
	if err != nil {
		s.logger.Error("web: reset: generate code", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !s.resetCodes.start(code) {
		writeAPIError(w, http.StatusTooManyRequests, "too_many_attempts", tooManyAttemptsMessage)
		return
	}
	sent, err := sender.SendOwnerCode(r.Context(), code)
	if err != nil {
		s.logger.Error("web: reset: send code", "error", err)
		writeAPIError(w, http.StatusConflict, "telegram_unavailable", telegramUnavailableMessage)
		return
	}
	writeJSON(w, http.StatusAccepted, struct {
		SentTo int `json:"sent_to"`
	}{SentTo: sent})
}

// handleResetVerify answers POST /v1/auth/reset/verify (API-WLG-005).
func (s *Server) handleResetVerify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if !decodeJSONObject(w, r, authMaxBody, &body) {
		return
	}
	if len([]rune(body.Password)) < minPasswordChars {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "password must be at least 8 characters")
		return
	}
	ok, expired := s.resetCodes.verify(body.Code)
	if expired {
		writeAPIError(w, http.StatusGone, "code_expired", codeExpiredMessage)
		return
	}
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "invalid_code", invalidCodeMessage)
		return
	}
	if err := s.applyPassword(body.Password, nil); err != nil {
		s.logger.Error("web: reset verify: set password", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.issueSession(w, r)
}

// handleResetRestart answers POST /v1/auth/reset/restart (API-WLG-006): it
// marks reset_requested so the next daemon start opens a sign-in link.
func (s *Server) handleResetRestart(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !decodeJSONObject(w, r, authMaxBody, &body) {
		return
	}
	if err := s.credentials.SetResetRequested(true); err != nil {
		s.logger.Error("web: reset restart: set flag", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusAccepted, struct{}{})
}

// handleChangePassword answers POST /v1/auth/password (API-WLG-007). It ends
// every other browser session.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var body struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if !decodeJSONObject(w, r, authMaxBody, &body) {
		return
	}
	if len([]rune(body.Password)) < minPasswordChars {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "password must be at least 8 characters")
		return
	}
	if s.credentials == nil || !s.credentials.VerifyPassword(body.Current) {
		writeAPIError(w, http.StatusUnauthorized, "invalid_password", invalidPasswordMessage)
		return
	}
	if err := s.applyPassword(body.Password, &sess.key); err != nil {
		s.logger.Error("web: change password", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// applyPassword stores the new password and ends the other browser sessions.
// keep names the caller's session, or nil to end all of them.
func (s *Server) applyPassword(password string, keep *[sha256.Size]byte) error {
	if err := s.credentials.SetPassword(password, s.nowTime()); err != nil {
		return err
	}
	var key [sha256.Size]byte
	if keep != nil {
		key = *keep
	}
	if _, err := s.sessions.revokeOthers(key); err != nil {
		s.logger.Warn("web: save browser sessions after password change", "error", err)
	}
	return nil
}

// issueSession creates a browser session and answers with its CSRF value.
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request) {
	cookie, err := randomToken()
	if err != nil {
		s.logger.Error("web: generate session cookie", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	sess, err := s.sessions.add(cookie, r.UserAgent(), s.nowTime())
	if err != nil {
		s.logger.Error("web: save browser session", "error", err)
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
	writeJSON(w, http.StatusOK, struct {
		CSRF string `json:"csrf"`
	}{CSRF: sess.csrf})
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

// randomToken returns 32 random bytes as base64url without padding.
func randomToken() (string, error) {
	buf := make([]byte, randomBytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// isLowerHexSHA256 reports whether value is 64 lowercase hex characters.
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
