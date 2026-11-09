package web

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
)

// The daemon-only local control endpoint of API-WLG-008. `lana web reset-login`
// (REQ-WLG-006) uses it to ask a running daemon for a sign-in link without a
// browser session.
const (
	// LocalSecretHeader carries the daemon local secret. The daemon writes the
	// same value to $LANA_HOME/daemon.secret.
	LocalSecretHeader = "X-Lana-Local-Secret"
	// LocalSignInPath is the local control path that returns a sign-in link.
	LocalSignInPath = "/v1/local/sign-in-link"
)

// SetLocalSecret installs the secret the local control endpoint requires. The
// daemon generates it on start and passes it here. An empty secret makes the
// endpoint reject every request, so an unset server never accepts a caller.
func (s *Server) SetLocalSecret(secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localSecret = secret
}

func (s *Server) localSecretValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.localSecret
}

// registerLocalRoutes installs the daemon-only control endpoint. It is not
// behind the browser session, Host, or Origin checks; it authenticates with the
// shared local secret and a loopback check instead (API-WLG-008).
func (s *Server) registerLocalRoutes() {
	// The path is registered without a method because the catch-all "/" route
	// would otherwise answer a wrong method with 404 before the mux could
	// return 405. handleLocalSignInLink rejects non-POST itself.
	s.mux.HandleFunc(LocalSignInPath, s.handleLocalSignInLink)
}

// handleLocalSignInLink answers POST /v1/local/sign-in-link (API-WLG-008). A
// request must carry the daemon secret in X-Lana-Local-Secret and come from a
// loopback address; the answer is the one-time sign-in link. The optional
// query parameter next (spec #41) names the dashboard hash, such as
// "#/calendar", the link opens after the claim; an unsafe next is ignored.
func (s *Server) handleLocalSignInLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}
	secret := s.localSecretValue()
	got := r.Header.Get(LocalSecretHeader)
	if secret == "" || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "local control secret is missing or invalid")
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeAPIError(w, http.StatusForbidden, "forbidden_remote", "local control is loopback only")
		return
	}
	link, err := s.SignInLinkTo(r.URL.Query().Get("next"))
	if err != nil {
		s.logger.Error("web: local sign-in link", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Link string `json:"link"`
	}{Link: link})
}

// isLoopbackRemote reports whether a request RemoteAddr is a loopback IP.
func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// RequestReset marks reset_requested in $LANA_HOME/web.json so the next daemon
// start opens a sign-in link (REQ-WLG-006). `lana web reset-login` calls it when
// no daemon is running; the CLI lives in another package, so the credential
// store is reached through this entry point.
func RequestReset(home string) error {
	store, err := loadCredentials(home)
	if err != nil {
		return err
	}
	if store == nil {
		return errors.New("web: no credential store")
	}
	return store.SetResetRequested(true)
}

// ResetRequested reports whether $LANA_HOME/web.json asks for a reset. It reads
// the file through loadCredentials, the same store the daemon uses.
func ResetRequested(home string) (bool, error) {
	store, err := loadCredentials(home)
	if err != nil {
		return false, err
	}
	if store == nil {
		return false, nil
	}
	return store.ResetRequested(), nil
}
