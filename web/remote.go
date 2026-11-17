package web

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/tailscale"
)

const (
	// The Tailscale Serve headers of REQ-RA-001 and REQ-RA-002. They are read
	// only from a loopback peer.
	remoteForwardedForHeader  = "X-Forwarded-For"
	remoteForwardedHostHeader = "X-Forwarded-Host"
	remoteUserLoginHeader     = "Tailscale-User-Login"
	remoteUserNameHeader      = "Tailscale-User-Name"

	// remoteHTTPSPort is the Serve port suffix dropped from a remote host
	// (REQ-RA-002).
	remoteHTTPSPort = ":443"

	// remoteWhoisTimeout bounds the best-effort whois lookup of a request
	// (REQ-RA-020, REQ-RA-027).
	remoteWhoisTimeout = 2 * time.Second

	// remoteDisabledMessage is the plain-text body of a refused remote request
	// (REQ-RA-003).
	remoteDisabledMessage = "Remote access is off or this account is not allowed."

	// remoteForbiddenMessage is the JSON message of REQ-RA-040.
	remoteForbiddenMessage = "remote requests cannot change this setting"

	// localAPIPrefix is the daemon-only control API of spec 18. Those routes
	// already require a loopback peer and a shared secret, and they are not
	// behind the Host check, so the remote gate leaves them alone.
	localAPIPrefix = "/v1/local/"
)

// remoteContextKey carries the remoteRequest of an allowed remote request. It
// is distinct from sessionContextKey.
const remoteContextKey contextKey = 1

// remoteRequest is the Tailscale identity of an allowed remote request
// (REQ-RA-001, REQ-RA-002). Login and Name are empty when require_identity is
// off.
type remoteRequest struct {
	Host  string
	Login string
	Name  string
	IP    string
}

// remoteFromContext returns the remote identity the gate stored on an allowed
// remote request.
func remoteFromContext(ctx context.Context) (remoteRequest, bool) {
	remote, ok := ctx.Value(remoteContextKey).(remoteRequest)
	return remote, ok
}

// classify reports whether r is a remote request and, if so, its identity
// (REQ-RA-001, REQ-RA-002). A request is remote only when its TCP peer is a
// loopback address and at least one of these holds: it carries X-Forwarded-For,
// it carries Tailscale-User-Login, or its host differs from the bind address.
// The forwarded and Tailscale headers are read only from a loopback peer; from
// any other peer they are ignored and the request stays local.
func (s *Server) classify(r *http.Request) (remoteRequest, bool) {
	if !isLoopbackPeer(r.RemoteAddr) {
		return remoteRequest{}, false
	}
	host := remoteHost(r)
	xff := strings.TrimSpace(r.Header.Get(remoteForwardedForHeader))
	if xff == "" && r.Header.Get(remoteUserLoginHeader) == "" && sameBindHost(host, s.Addr()) {
		return remoteRequest{}, false
	}
	remote := remoteRequest{
		Host:  host,
		Login: strings.TrimSpace(r.Header.Get(remoteUserLoginHeader)),
		Name:  strings.TrimSpace(r.Header.Get(remoteUserNameHeader)),
	}
	if xff != "" {
		remote.IP = strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	}
	return remote, true
}

// sameBindHost reports whether a request host names the bind address. The
// comparison ignores case and the port, so a request to the bind host on any
// port stays local.
func sameBindHost(host, addr string) bool {
	return strings.EqualFold(hostOnly(host), hostOnly(addr))
}

// hostOnly drops the port of a host or host:port value. A value without a port
// is returned unchanged.
func hostOnly(value string) string {
	if host, _, err := net.SplitHostPort(value); err == nil {
		return host
	}
	return value
}

// remoteHost is the host of a remote request: X-Forwarded-Host when present,
// else Host, lower-cased, without a trailing ":443" (REQ-RA-002).
func remoteHost(r *http.Request) string {
	host := r.Host
	if forwarded := r.Header.Get(remoteForwardedHostHeader); forwarded != "" {
		host = forwarded
	}
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.TrimSuffix(host, remoteHTTPSPort)
}

// isLoopbackPeer reports whether a RemoteAddr such as "127.0.0.1:5555" comes
// from a loopback address.
func isLoopbackPeer(remoteAddr string) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// SetRemoteConfig installs the remote access settings (REQ-RA-011). The daemon
// calls it at start and after a save, so a change takes effect at once,
// without a restart. It also installs the remote session TTLs (REQ-RA-021).
func (s *Server) SetRemoteConfig(cfg core.WebRemoteConfig) {
	s.mu.Lock()
	s.remote = cfg
	s.mu.Unlock()
	s.sessions.setRemoteLimits(time.Duration(cfg.IdleMinutes)*time.Minute, time.Duration(cfg.MaxAgeHours)*time.Hour)
}

// remoteConfig returns the current remote access settings.
func (s *Server) remoteConfig() core.WebRemoteConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remote
}

// tailscaleClient is the part of *tailscale.Client the server uses
// (REQ-RA-051, REQ-RA-052, REQ-RA-053). Tests pass a fake.
type tailscaleClient interface {
	Status(ctx context.Context, daemonAddr string) (tailscale.Status, error)
	Serve(ctx context.Context, daemonAddr string) error
	Unserve(ctx context.Context) error
	Whois(ctx context.Context, ip string) (tailscale.Peer, error)
}

// SetTailscaleClient replaces the Tailscale client (tests).
func (s *Server) SetTailscaleClient(c tailscaleClient) {
	s.mu.Lock()
	s.tailscale = c
	s.mu.Unlock()
}

func (s *Server) tailscaleClient() tailscaleClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tailscale
}

// whoisDevice maps a remote client IP to its Tailscale machine name, best
// effort (REQ-RA-020): an empty IP, a missing client, or a whois failure
// yields "".
func (s *Server) whoisDevice(ctx context.Context, ip string) string {
	client := s.tailscaleClient()
	if client == nil || ip == "" {
		return ""
	}
	whoCtx, cancel := context.WithTimeout(ctx, remoteWhoisTimeout)
	defer cancel()
	peer, err := client.Whois(whoCtx, ip)
	if err != nil {
		return ""
	}
	return peer.Device
}

// remoteGate is the outermost middleware. It classifies every request and, for
// a remote one, refuses it with 403 remote_disabled when remote access is off,
// the host differs from the configured host, or the identity is required and
// missing or not allowed (REQ-RA-003). An allowed remote request carries its
// identity in the request context. Local requests pass through unchanged.
func (s *Server) remoteGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, localAPIPrefix) {
			// The routes under /v1/local/ authenticate with the daemon secret
			// and a loopback peer, so they are normally left alone. Through
			// Tailscale Serve, however, the peer is loopback too; a request
			// that carries a forwarded or Tailscale identity header came from
			// Serve, not the daemon's own CLI, so it is refused like any other
			// remote request (REQ-RA-003). A plain local request, even with an
			// odd Host, still passes.
			if isLoopbackPeer(r.RemoteAddr) &&
				(r.Header.Get(remoteForwardedForHeader) != "" || r.Header.Get(remoteUserLoginHeader) != "") {
				refuseRemoteDisabled(w, r)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		remote, ok := s.classify(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if !s.remoteAllowed(remote) {
			refuseRemoteDisabled(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), remoteContextKey, remote)))
	})
}

// remoteAllowed applies the REQ-RA-003 gate to a classified remote request.
func (s *Server) remoteAllowed(remote remoteRequest) bool {
	cfg := s.remoteConfig()
	if !cfg.Enabled || !strings.EqualFold(remote.Host, cfg.Host) {
		return false
	}
	if !cfg.RequireIdentity {
		return true
	}
	if remote.Login == "" {
		return false
	}
	for _, allowed := range cfg.AllowedLogins {
		if strings.EqualFold(allowed, remote.Login) {
			return true
		}
	}
	return false
}

// refuseRemoteDisabled answers a refused remote request: the API error shape
// for /v1/*, a short plain-text page otherwise (REQ-RA-003).
func refuseRemoteDisabled(w http.ResponseWriter, r *http.Request) {
	if isAPIPath(r.URL.Path) {
		writeAPIError(w, http.StatusForbidden, "remote_disabled", remoteDisabledMessage)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = io.WriteString(w, remoteDisabledMessage+"\n")
}

// refuseRemote answers 403 remote_forbidden for a remote request and passes a
// local request through. It wraps the routes of REQ-RA-040, so a stolen phone
// cannot loosen remote access or lock the owner out.
func (s *Server) refuseRemote(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := remoteFromContext(r.Context()); ok {
			writeAPIError(w, http.StatusForbidden, "remote_forbidden", remoteForbiddenMessage)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// TelegramOwnerMessenger delivers a plain-text message to the owner's Telegram
// private chats (REQ-RA-070). cmd/lana implements it with the running channel.
type TelegramOwnerMessenger interface {
	// SendOwnerMessage sends text to every owner private chat and returns the
	// number of messages sent.
	SendOwnerMessage(ctx context.Context, text string) (int, error)
}

// SetTelegramOwnerMessenger installs the Telegram owner messenger. The daemon
// calls it before the server starts serving.
func (s *Server) SetTelegramOwnerMessenger(m TelegramOwnerMessenger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.telegramOwner = m
}

func (s *Server) telegramOwnerMessenger() TelegramOwnerMessenger {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.telegramOwner
}

// handleRevokeSession answers POST /v1/auth/sessions/{id}/revoke (REQ-RA-025).
// An unknown display id is 404 not_found; revoking the caller's own session
// signs it out and clears its cookie.
func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	id := r.PathValue("id")
	key, found, err := s.sessions.revokeByID(id)
	if err != nil {
		s.logger.Error("web: revoke session", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	s.auditSave(remoteSessionRevokeKind, "", id, nil)
	if key == sess.key {
		clearSessionCookie(w)
	}
	writeJSON(w, http.StatusOK, struct {
		Revoked int `json:"revoked"`
	}{Revoked: 1})
}

// handleRevokeRemoteSessions answers POST /v1/auth/sessions/revoke-remote
// (REQ-RA-025): it ends every remote session and returns the count.
func (s *Server) handleRevokeRemoteSessions(w http.ResponseWriter, r *http.Request) {
	if _, ok := sessionFromContext(r.Context()); !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	revoked, err := s.sessions.revokeRemote()
	if err != nil {
		s.logger.Error("web: revoke remote sessions", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Revoked int `json:"revoked"`
	}{Revoked: revoked})
}

// remoteSessionRevokeKind is the audit kind of REQ-RA-072.
const remoteSessionRevokeKind = "remote.session_revoke"

// clearSessionCookie ends the browser session of w by expiring its cookie.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}
