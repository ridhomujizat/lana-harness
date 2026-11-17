package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/notify"
	"github.com/ridhomujizat/lana-harness/internal/tailscale"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// The remote access settings API of REQ-RA-060..064 and the sign-in
// notification and audit of REQ-RA-070..072.
const (
	// remoteStatusTimeout bounds the Tailscale status and serve calls of one
	// settings request (REQ-RA-060).
	remoteStatusTimeout = 6 * time.Second
	// remoteNotifyTimeout bounds the best-effort Telegram message of a remote
	// sign-in (REQ-RA-070).
	remoteNotifyTimeout = 10 * time.Second
	// remoteSignInThread groups the remote sign-in banners in the menu bar
	// app.
	remoteSignInThread = "remote-sign-in"
	// remoteSignInURL is the dashboard page a remote sign-in banner opens
	// (REQ-RA-071).
	remoteSignInURL = "#/settings/remote"
	// remoteConfigTableHeader is the table the remote settings live in
	// (REQ-RA-010).
	remoteConfigTableHeader = "[web.remote]"
)

// openTailscaleApp runs `open -a Tailscale` for the setup checklist's Open
// Tailscale button (REQ-RA-064). Tests replace it.
var openTailscaleApp = func(ctx context.Context) error {
	return exec.CommandContext(ctx, "open", "-a", "Tailscale").Run()
}

// announceRemoteSignIn publishes the notification and sends the Telegram
// message of a successful remote sign-in (REQ-RA-070). It always writes the
// audit line of REQ-RA-072 first, then honours notify_sign_in.
func (s *Server) announceRemoteSignIn(r *http.Request, sess session) {
	ip := ""
	if remote, ok := remoteFromContext(r.Context()); ok {
		ip = remote.IP
	}
	// The audit target is "login@device"; the device falls back to the client
	// IP when whois did not report a machine name (REQ-RA-072).
	device := sess.device
	if device == "" {
		device = ip
	}
	s.auditSave("remote.sign_in", "", sess.login+"@"+device, nil)

	if !s.remoteConfig().NotifySignIn {
		return
	}
	const title = "New remote sign-in"
	body := fmt.Sprintf("%s on %s (%s) signed in over Tailscale.", browserLabel(r.UserAgent()), device, sess.login)
	if bus := s.notificationBus(); bus != nil {
		bus.Publish(notify.Event{
			Kind:          notify.KindRemoteSignIn,
			Title:         title,
			Body:          body,
			URL:           remoteSignInURL,
			TimeSensitive: true,
			Thread:        remoteSignInThread,
		})
	}
	if messenger := s.telegramOwnerMessenger(); messenger != nil {
		// Best effort: a slow or failing channel never delays the sign-in.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), remoteNotifyTimeout)
			defer cancel()
			if _, err := messenger.SendOwnerMessage(ctx, title+"\n\n"+body); err != nil {
				s.logger.Warn("web: remote sign-in telegram", "error", err)
			}
		}()
	}
}

// auditRemoteSignInFailed records the audit entry of a wrong remote password
// (REQ-RA-072). The target is the Tailscale login, or the client IP when the
// request carried no identity.
func (s *Server) auditRemoteSignInFailed(r *http.Request) {
	remote, _ := remoteFromContext(r.Context())
	target := remote.Login
	if target == "" {
		target = remote.IP
	}
	s.auditSave("remote.sign_in_failed", "", target, nil)
}

// registerRemoteAPIRoutes installs the remote access settings routes of
// REQ-RA-060..064 behind the owner cookie and CSRF middleware. GET is allowed
// from a remote browser; the four writers are refused there (REQ-RA-040).
func (s *Server) registerRemoteAPIRoutes() {
	s.Handle("GET /v1/web/remote", http.HandlerFunc(s.handleGetRemote))
	s.Handle("PUT /v1/web/remote", s.refuseRemote(http.HandlerFunc(s.handlePutRemote)))
	s.Handle("POST /v1/web/remote/enable", s.refuseRemote(http.HandlerFunc(s.handleEnableRemote)))
	s.Handle("POST /v1/web/remote/disable", s.refuseRemote(http.HandlerFunc(s.handleDisableRemote)))
	s.Handle("POST /v1/web/remote/open-app", s.refuseRemote(http.HandlerFunc(s.handleOpenRemoteApp)))
}

// remoteConfigJSON is the config part of the settings response: the [web.remote]
// values plus version, the config file version other saves use as
// expected_version (REQ-RA-060).
type remoteConfigJSON struct {
	Version         string   `json:"version"`
	Enabled         bool     `json:"enabled"`
	Host            string   `json:"host"`
	RequireIdentity bool     `json:"require_identity"`
	AllowedLogins   []string `json:"allowed_logins"`
	Terminal        string   `json:"terminal"`
	ConfirmWith     string   `json:"confirm_with"`
	ControlMinutes  int      `json:"control_minutes"`
	IdleMinutes     int      `json:"idle_minutes"`
	MaxAgeHours     int      `json:"max_age_hours"`
	NotifySignIn    bool     `json:"notify_sign_in"`
}

// remoteSettingsResponse is the body of GET and of the enable, disable, and
// PUT answers (REQ-RA-060..063).
type remoteSettingsResponse struct {
	Config    remoteConfigJSON `json:"config"`
	Status    tailscale.Status `json:"status"`
	URL       string           `json:"url"`
	CheckedAt int64            `json:"checked_at"`
	Warning   string           `json:"warning,omitempty"`
}

// handleGetRemote answers GET /v1/web/remote (REQ-RA-060).
func (s *Server) handleGetRemote(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config for remote settings", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.writeRemoteSettings(w, r, file.Version, "")
}

// putRemoteRequest is the body of PUT /v1/web/remote (REQ-RA-061). enabled and
// host are kept from the current settings; the rest is the form.
type putRemoteRequest struct {
	ExpectedVersion string   `json:"expected_version"`
	RequireIdentity bool     `json:"require_identity"`
	AllowedLogins   []string `json:"allowed_logins"`
	Terminal        string   `json:"terminal"`
	ConfirmWith     string   `json:"confirm_with"`
	ControlMinutes  int      `json:"control_minutes"`
	IdleMinutes     int      `json:"idle_minutes"`
	MaxAgeHours     int      `json:"max_age_hours"`
	NotifySignIn    bool     `json:"notify_sign_in"`
}

// handlePutRemote answers PUT /v1/web/remote (REQ-RA-061): it validates the
// form values and saves them, so the change applies without a restart
// (REQ-RA-011).
func (s *Server) handlePutRemote(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	var body putRemoteRequest
	if !decodeJSONObject(w, r, configBodyLimit, &body) {
		return
	}
	if strings.TrimSpace(body.ExpectedVersion) == "" {
		writeWebIssue(w, "expected_version", "expected_version is required")
		return
	}
	terminal := strings.TrimSpace(body.Terminal)
	if msg := remoteTerminalIssue(terminal); msg != "" {
		writeWebIssue(w, "web.remote.terminal", msg)
		return
	}
	confirmWith := strings.TrimSpace(body.ConfirmWith)
	if msg := remoteConfirmIssue(confirmWith); msg != "" {
		writeWebIssue(w, "web.remote.confirm_with", msg)
		return
	}
	for _, field := range []struct {
		key   string
		value int
	}{
		{"control_minutes", body.ControlMinutes},
		{"idle_minutes", body.IdleMinutes},
		{"max_age_hours", body.MaxAgeHours},
	} {
		if msg := remoteRangeIssue(field.key, field.value); msg != "" {
			writeWebIssue(w, "web.remote."+field.key, msg)
			return
		}
	}

	cfg := s.remoteConfig()
	cfg.RequireIdentity = body.RequireIdentity
	cfg.AllowedLogins = cleanRemoteLogins(body.AllowedLogins)
	cfg.Terminal = terminal
	cfg.ConfirmWith = confirmWith
	cfg.ControlMinutes = body.ControlMinutes
	cfg.IdleMinutes = body.IdleMinutes
	cfg.MaxAgeHours = body.MaxAgeHours
	cfg.NotifySignIn = body.NotifySignIn

	version, ok := s.saveRemoteConfig(w, r, store, body.ExpectedVersion, cfg)
	if !ok {
		return
	}
	s.SetRemoteConfig(cfg)
	s.writeRemoteSettings(w, r, version, "")
}

// enableRemoteRequest is the body of POST /v1/web/remote/enable (REQ-RA-062).
type enableRemoteRequest struct {
	ExpectedVersion string `json:"expected_version"`
}

// handleEnableRemote answers POST /v1/web/remote/enable (REQ-RA-062): it checks
// that Tailscale is ready, runs Serve, saves enabled and host, and answers like
// GET.
func (s *Server) handleEnableRemote(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	var body enableRemoteRequest
	if !decodeJSONObject(w, r, configBodyLimit, &body) {
		return
	}
	if strings.TrimSpace(body.ExpectedVersion) == "" {
		writeWebIssue(w, "expected_version", "expected_version is required")
		return
	}
	client := s.tailscaleClient()
	if client == nil {
		writeAPIError(w, http.StatusConflict, "tailscale_not_ready", remoteNotReadyMessages[0])
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), remoteStatusTimeout)
	defer cancel()
	status, err := client.Status(ctx, s.Addr())
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "tailscale_failed", err.Error())
		return
	}
	if msg := remoteNotReadyMessage(status); msg != "" {
		writeAPIError(w, http.StatusConflict, "tailscale_not_ready", msg)
		return
	}
	if err := client.Serve(ctx, s.Addr()); err != nil {
		writeAPIError(w, http.StatusBadGateway, "tailscale_failed", err.Error())
		return
	}

	cfg := s.remoteConfig()
	cfg.Enabled = true
	cfg.Host = status.DNSName
	if len(cfg.AllowedLogins) == 0 && strings.TrimSpace(status.Login) != "" {
		cfg.AllowedLogins = []string{strings.ToLower(strings.TrimSpace(status.Login))}
	}
	version, ok := s.saveRemoteConfig(w, r, store, body.ExpectedVersion, cfg)
	if !ok {
		return
	}
	s.SetRemoteConfig(cfg)
	s.auditSave("remote.enable", "", status.DNSName, nil)
	s.writeRemoteSettings(w, r, version, "")
}

// disableRemoteRequest is the body of POST /v1/web/remote/disable (REQ-RA-063).
type disableRemoteRequest struct {
	ExpectedVersion string `json:"expected_version"`
}

// handleDisableRemote answers POST /v1/web/remote/disable (REQ-RA-063): it runs
// Unserve (a failure is a warning, not fatal), saves enabled = false, ends every
// remote session, and answers like GET.
func (s *Server) handleDisableRemote(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	var body disableRemoteRequest
	if !decodeJSONObject(w, r, configBodyLimit, &body) {
		return
	}
	if strings.TrimSpace(body.ExpectedVersion) == "" {
		writeWebIssue(w, "expected_version", "expected_version is required")
		return
	}
	warning := ""
	if client := s.tailscaleClient(); client != nil {
		ctx, cancel := context.WithTimeout(r.Context(), remoteStatusTimeout)
		if err := client.Unserve(ctx); err != nil {
			s.logger.Warn("web: unserve tailscale", "error", err)
			warning = err.Error()
		}
		cancel()
	}

	cfg := s.remoteConfig()
	cfg.Enabled = false
	version, ok := s.saveRemoteConfig(w, r, store, body.ExpectedVersion, cfg)
	if !ok {
		return
	}
	s.SetRemoteConfig(cfg)
	if _, err := s.sessions.revokeRemote(); err != nil {
		s.logger.Warn("web: revoke remote sessions", "error", err)
	}
	s.auditSave("remote.disable", "", cfg.Host, nil)
	s.writeRemoteSettings(w, r, version, warning)
}

// handleOpenRemoteApp answers POST /v1/web/remote/open-app (REQ-RA-064): it
// opens the Tailscale app on macOS and answers 501 elsewhere.
func (s *Server) handleOpenRemoteApp(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "darwin" {
		writeAPIError(w, http.StatusNotImplemented, "unsupported", "opening the Tailscale app is supported on macOS only")
		return
	}
	if err := openTailscaleApp(r.Context()); err != nil {
		s.logger.Error("web: open tailscale app", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// writeRemoteSettings answers with the settings shape of REQ-RA-060. extraWarning
// is a warning from the caller (a failed Unserve) and is merged with a status
// warning. A status error does not fail the request: it yields a status with
// installed false and the warning text.
func (s *Server) writeRemoteSettings(w http.ResponseWriter, r *http.Request, version, extraWarning string) {
	cfg := s.remoteConfig()
	status, warning := s.remoteStatus(r.Context())
	if extraWarning != "" {
		if warning == "" {
			warning = extraWarning
		} else {
			warning = extraWarning + "; " + warning
		}
	}
	writeJSON(w, http.StatusOK, remoteSettingsResponse{
		Config:    remoteSettingsConfig(cfg, version),
		Status:    status,
		URL:       remoteSettingsURL(cfg.Host),
		CheckedAt: s.nowTime().UnixMilli(),
		Warning:   warning,
	})
}

// remoteStatus reads the Tailscale status with the 6-second timeout of
// REQ-RA-060. A status error is returned as warning text, never as a 500.
func (s *Server) remoteStatus(ctx context.Context) (tailscale.Status, string) {
	client := s.tailscaleClient()
	if client == nil {
		return tailscale.Status{}, "Tailscale is not available."
	}
	ctx, cancel := context.WithTimeout(ctx, remoteStatusTimeout)
	defer cancel()
	status, err := client.Status(ctx, s.Addr())
	if err != nil {
		return tailscale.Status{Installed: false}, err.Error()
	}
	return status, ""
}

// remoteSettingsConfig is the config part of the settings response.
func remoteSettingsConfig(cfg core.WebRemoteConfig, version string) remoteConfigJSON {
	logins := cfg.AllowedLogins
	if logins == nil {
		logins = []string{}
	}
	return remoteConfigJSON{
		Version:         version,
		Enabled:         cfg.Enabled,
		Host:            cfg.Host,
		RequireIdentity: cfg.RequireIdentity,
		AllowedLogins:   logins,
		Terminal:        cfg.Terminal,
		ConfirmWith:     cfg.ConfirmWith,
		ControlMinutes:  cfg.ControlMinutes,
		IdleMinutes:     cfg.IdleMinutes,
		MaxAgeHours:     cfg.MaxAgeHours,
		NotifySignIn:    cfg.NotifySignIn,
	}
}

// remoteSettingsURL is the ts.net URL of the settings, or "" before a host is
// saved.
func remoteSettingsURL(host string) string {
	if host == "" {
		return ""
	}
	return "https://" + host
}

// saveRemoteConfig rewrites the whole [web.remote] table like handlePutWeb
// writes [web] (REQ-RA-011). It returns the new config version, or ok false
// after writing the error response.
func (s *Server) saveRemoteConfig(w http.ResponseWriter, r *http.Request, store ConfigStore, expected string, cfg core.WebRemoteConfig) (string, bool) {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before remote save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return "", false
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return "", false
	}
	if file.Version != expected {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return "", false
	}
	// RewriteTable replaces an existing [web.remote] table in place and appends
	// the table after [web] when it is missing, so both a saved table and a
	// config.toml that only has [web] work (REQ-RA-010, REQ-RA-011).
	candidate, changed, err := webfiles.RewriteTable(r.Context(), webChecker(store), file.Raw, remoteConfigTableHeader, remoteConfigTable(cfg))
	if err != nil {
		s.writeWebSaveError(w, "save remote settings", err)
		return "", false
	}
	if !bytes.Equal(candidate, file.Raw) {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for remote save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return "", false
		}
		s.auditSave("remote.settings", "config.toml", "", changed)
	}
	return webfiles.ConfigVersion(candidate), true
}

// remoteConfigTable renders the whole [web.remote] table in the key order of
// REQ-RA-010, with allowed_logins as a TOML array of quoted strings.
func remoteConfigTable(cfg core.WebRemoteConfig) string {
	var b strings.Builder
	b.WriteString(remoteConfigTableHeader)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "enabled = %t\n", cfg.Enabled)
	fmt.Fprintf(&b, "host = %q\n", cfg.Host)
	fmt.Fprintf(&b, "require_identity = %t\n", cfg.RequireIdentity)
	b.WriteString("allowed_logins = [")
	for i, login := range cfg.AllowedLogins {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", login)
	}
	b.WriteString("]\n")
	fmt.Fprintf(&b, "terminal = %q\n", cfg.Terminal)
	fmt.Fprintf(&b, "confirm_with = %q\n", cfg.ConfirmWith)
	fmt.Fprintf(&b, "control_minutes = %d\n", cfg.ControlMinutes)
	fmt.Fprintf(&b, "idle_minutes = %d\n", cfg.IdleMinutes)
	fmt.Fprintf(&b, "max_age_hours = %d\n", cfg.MaxAgeHours)
	fmt.Fprintf(&b, "notify_sign_in = %t\n", cfg.NotifySignIn)
	return b.String()
}

// remoteNotReadyMessages are the not-ready messages of REQ-RA-062, in the order
// the checks run.
var remoteNotReadyMessages = []string{
	"Tailscale is not installed.",
	"Tailscale is not running.",
	"HTTPS certificates are off.",
	"Port 443 is already serving something else.",
}

// remoteNotReadyMessage returns the message of the first failing readiness
// check (REQ-RA-062), or "" when every check passes.
func remoteNotReadyMessage(status tailscale.Status) string {
	switch {
	case !status.Installed:
		return remoteNotReadyMessages[0]
	case !status.Running:
		return remoteNotReadyMessages[1]
	case !status.HTTPSCerts:
		return remoteNotReadyMessages[2]
	case status.ServeOther:
		return remoteNotReadyMessages[3]
	default:
		return ""
	}
}

// remoteTerminalIssue returns the validation message of a terminal policy, or
// "" when it is one of REQ-RA-030's values.
func remoteTerminalIssue(value string) string {
	switch value {
	case core.WebRemoteTerminalView, core.WebRemoteTerminalConfirm, core.WebRemoteTerminalFull:
		return ""
	default:
		return "must be one of view, confirm, full"
	}
}

// remoteConfirmIssue returns the validation message of a confirm_with value, or
// "" when it is one of REQ-RA-032's values.
func remoteConfirmIssue(value string) string {
	switch value {
	case core.WebRemoteConfirmPassword, core.WebRemoteConfirmPasswordOrTelegram:
		return ""
	default:
		return "must be one of password, password_or_telegram"
	}
}

// remoteRangeIssue returns the validation message of a minutes or hours value,
// or "" when it is inside the range of REQ-RA-010.
func remoteRangeIssue(key string, value int) string {
	lo, hi := remoteRange(key)
	if value < lo || value > hi {
		return fmt.Sprintf("must be between %d and %d", lo, hi)
	}
	return ""
}

// remoteRange returns the accepted range of a [web.remote] minutes or hours
// value (REQ-RA-010).
func remoteRange(key string) (int, int) {
	switch key {
	case "control_minutes":
		return core.WebRemoteControlMin, core.WebRemoteControlMax
	case "idle_minutes":
		return core.WebRemoteIdleMin, core.WebRemoteIdleMax
	default:
		return core.WebRemoteMaxAgeMin, core.WebRemoteMaxAgeMax
	}
}

// cleanRemoteLogins trims, lower-cases, de-duplicates, and drops the empty
// entries of an allowed_logins form value (REQ-RA-010, REQ-RA-061).
func cleanRemoteLogins(list []string) []string {
	out := make([]string, 0, len(list))
	seen := make(map[string]bool, len(list))
	for _, item := range list {
		login := strings.ToLower(strings.TrimSpace(item))
		if login == "" || seen[login] {
			continue
		}
		seen[login] = true
		out = append(out, login)
	}
	return out
}

// browserLabel renders a short browser and platform name from a User-Agent for
// the sign-in notification (REQ-RA-070), such as "Safari on iPhone". An
// unrecognized agent yields "A browser".
func browserLabel(userAgent string) string {
	browser := browserName(userAgent)
	platform := platformName(userAgent)
	switch {
	case browser == "" && platform == "":
		return "A browser"
	case browser == "":
		return "A browser on " + platform
	case platform == "":
		return browser
	default:
		return browser + " on " + platform
	}
}

// browserName returns the short browser name of a User-Agent, or "".
func browserName(userAgent string) string {
	switch {
	case strings.Contains(userAgent, "Edg/"), strings.Contains(userAgent, "EdgA/"), strings.Contains(userAgent, "EdgiOS/"):
		return "Edge"
	case strings.Contains(userAgent, "OPR/"), strings.Contains(userAgent, "Opera"):
		return "Opera"
	case strings.Contains(userAgent, "Firefox/"), strings.Contains(userAgent, "FxiOS/"):
		return "Firefox"
	case strings.Contains(userAgent, "Chrome/"), strings.Contains(userAgent, "CriOS/"):
		return "Chrome"
	case strings.Contains(userAgent, "Safari/"):
		return "Safari"
	default:
		return ""
	}
}

// platformName returns the short platform name of a User-Agent, or "".
func platformName(userAgent string) string {
	switch {
	case strings.Contains(userAgent, "iPad"):
		return "iPad"
	case strings.Contains(userAgent, "iPhone"):
		return "iPhone"
	case strings.Contains(userAgent, "Android"):
		return "Android"
	case strings.Contains(userAgent, "CrOS"):
		return "ChromeOS"
	case strings.Contains(userAgent, "Windows"):
		return "Windows"
	case strings.Contains(userAgent, "Macintosh"), strings.Contains(userAgent, "Mac OS X"):
		return "macOS"
	case strings.Contains(userAgent, "Linux"):
		return "Linux"
	default:
		return ""
	}
}
