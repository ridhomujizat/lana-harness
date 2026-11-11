package web

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"time"
)

// The desktop app sign-in of spec #52. The Lana.app window is a WKWebView with
// its own cookie jar, so the browser's session cookie does not reach it. The
// app asks the daemon for a short-lived app link with the local secret
// (API-DSK-001) and loads it in the window; the link sets the session cookie
// in that jar and redirects to the dashboard (API-DSK-002). No password is
// set or changed, so other browsers stay signed in.
const (
	// LocalAppLinkPath is the local control path that returns an app link.
	LocalAppLinkPath = "/v1/local/app-link"
	// AppSignInPath is the dashboard path an app link opens.
	AppSignInPath = "/v1/auth/app"
	// appCodeTTL is how long an app link code stays valid. The app loads the
	// link right away, so the window is short.
	appCodeTTL = 2 * time.Minute
	// defaultAppNext is the page an app link opens without a safe next.
	defaultAppNext = "#/chats"
)

// newAppCodeStore keeps the one-time codes of app links.
func newAppCodeStore(now func() time.Time) *signInCodeStore {
	store := newSignInCodeStore(now)
	store.ttl = appCodeTTL
	return store
}

// AppLinkTo creates a one-time app link that signs the caller in and opens
// next (a dashboard hash that SafeNextHash accepts, else #/chats).
func (s *Server) AppLinkTo(next string) (string, error) {
	code, err := s.appCodes.create()
	if err != nil {
		return "", err
	}
	query := url.Values{"code": {code}}
	if safe, ok := SafeNextHash(next); ok {
		query.Set("next", safe)
	}
	return "http://" + s.Addr() + AppSignInPath + "?" + query.Encode(), nil
}

// localControlAllowed applies the API-WLG-008 checks of a local control
// request: the daemon secret header and a loopback caller. It writes the error
// answer and returns false when a check fails.
func (s *Server) localControlAllowed(w http.ResponseWriter, r *http.Request) bool {
	secret := s.localSecretValue()
	got := r.Header.Get(LocalSecretHeader)
	if secret == "" || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "local control secret is missing or invalid")
		return false
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeAPIError(w, http.StatusForbidden, "forbidden_remote", "local control is loopback only")
		return false
	}
	return true
}

// handleLocalAppLink answers POST /v1/local/app-link (API-DSK-001) with
// {"link": "http://<addr>/v1/auth/app?code=…&next=…"}.
func (s *Server) handleLocalAppLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}
	if !s.localControlAllowed(w, r) {
		return
	}
	link, err := s.AppLinkTo(r.URL.Query().Get("next"))
	if err != nil {
		s.logger.Error("web: local app link", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Link string `json:"link"`
	}{Link: link})
}

// handleAppSignIn answers GET /v1/auth/app (API-DSK-002). The code works once.
// A caller that already has a valid session keeps it; otherwise a valid code
// starts a new session. Either way the answer is a 303 to next. An unknown or
// expired code without a session goes to the dashboard, which shows the
// password sign-in.
func (s *Server) handleAppSignIn(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	query := r.URL.Query()
	valid := s.appCodes.consume(query.Get("code"))
	next, ok := SafeNextHash(query.Get("next"))
	if !ok {
		next = defaultAppNext
	}
	if s.hasValidSession(r) {
		http.Redirect(w, r, "/"+next, http.StatusSeeOther)
		return
	}
	if !valid {
		s.logger.Info("web: app sign-in with an unknown or expired code")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if _, err := s.startSession(w, r); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.logger.Info("web: app sign-in succeeded")
	http.Redirect(w, r, "/"+next, http.StatusSeeOther)
}

// hasValidSession reports whether the request carries a live session cookie.
// It refreshes the session's idle expiry like any authenticated request.
func (s *Server) hasValidSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	_, ok, err := s.sessions.touch(cookie.Value, s.nowTime())
	if err != nil {
		s.logger.Warn("web: save browser session", "error", err)
	}
	return ok
}
