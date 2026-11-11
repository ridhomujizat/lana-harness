// Package web serves the browser dashboard and its HTTP API.
package web

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/notify"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// ErrNotLoopback is returned when [web] addr is not a loopback address.
var ErrNotLoopback = errors.New("web: addr must be a loopback address")

const (
	shutdownTimeout          = 10 * time.Second
	defaultHeartbeatInterval = 15 * time.Second
	defaultStreamPoll        = time.Second
)

//go:embed dist
var distFS embed.FS

// Config is the [web] table of the effective config.
type Config struct {
	Addr    string
	Enabled bool
	// Home is the LANA_HOME directory. When it is empty the credential store
	// is absent and login always fails.
	Home string
	// Stderr receives the one-time sign-in link line. A nil writer discards
	// it.
	Stderr io.Writer
	// OpenBrowser opens the sign-in link on start when no password is set or
	// a reset was requested ([web] open_browser, default true).
	OpenBrowser bool
}

// Server serves the dashboard and the /v1 API from embedded assets.
type Server struct {
	cfg              Config
	logger           *slog.Logger
	handler          http.Handler
	mux              *http.ServeMux
	assets           fs.FS
	index            []byte
	credentials      *credentialStore
	signInCodes      *signInCodeStore
	resetCodes       *resetCodeStore
	loginLock        *loginLock
	sessions         *browserSessions
	backend          Backend
	configStore      ConfigStore
	secretStore      SecretStore
	agentFileStore   AgentFileStore
	skillStore       SkillStore
	jobBackend       JobBackend
	dashboardBackend DashboardBackend
	knowledgeBackend KnowledgeBackend
	// knowledgeDomainBackend serves the page list, page read, review queue,
	// and the agent overview knowledge block (API-DKN-004–008); nil when the
	// backend does not implement the port.
	knowledgeDomainBackend KnowledgeDomainBackend
	// workflows is the workflow port of the /v1/workflows routes (spec #33).
	// It is guarded by workflowsMu so SetWorkflows may run after Start.
	workflows   WorkflowBackend
	workflowsMu sync.RWMutex
	// toolbox is the Tools port of the /v1/toolbox routes (spec #34). It is
	// guarded by toolboxMu so SetToolbox may run after Start.
	toolbox   ToolboxBackend
	toolboxMu sync.RWMutex
	// calendar holds the calendar and Google ports of the /v1/calendar routes
	// (spec #35) and the pending Google OAuth states.
	calendar calendarPort
	// routerBackend is the Router page and proxy port (spec 37). It is
	// guarded by routerMu because the proxy reads it on every request.
	routerBackend  RouterBackend
	routerMu       sync.RWMutex
	daemon         DaemonStore
	update         UpdateStore
	telegramStatus func() TelegramStatus
	telegramReset  TelegramResetSender
	approvals      *Approvals
	questions      *Questions
	// secretRequests is the secure secret input broker (spec 53).
	secretRequests *SecretRequests
	// roomHub pushes changed room rows to the room streams (REQ-ROOM-007).
	roomHub *roomHub
	// roomLocks guards "find or create the current web session" per room
	// (API-ROOM-003).
	roomLocks sync.Map

	// localSecret authenticates the daemon-only local control endpoint of
	// API-WLG-008 (lana web reset-login, REQ-WLG-006). It is empty until the
	// daemon calls SetLocalSecret, and an empty secret rejects every request.
	localSecret string

	// notifications is the notification bus of spec #42; nil until the
	// daemon calls SetNotifications. localHeartbeat overrides the local event
	// stream's heartbeat (tests).
	notifications  *notify.Bus
	localHeartbeat time.Duration

	// secretsChanged is set after a successful write to secrets.env and stays
	// set until the daemon restarts.
	secretsChanged bool

	// now is the clock used for browser sessions; nil means time.Now.
	now func() time.Time

	// heartbeat is the SSE comment interval. poll is the interval at which
	// stored events are read for sessions without in-process live frames.
	heartbeat time.Duration
	poll      time.Duration
	// toolboxJobPoll is the poll interval of the install watcher that
	// registers a package's managed MCP server once its install succeeds
	// (REQ-TB-011).
	toolboxJobPoll time.Duration

	mu       sync.Mutex
	ln       net.Listener
	srv      *http.Server
	stop     chan struct{}
	stopOnce sync.Once
	once     sync.Once
	done     chan struct{}
}

// New validates the configured address and prepares the server.
func New(cfg Config) (*Server, error) {
	if cfg.Addr == "" {
		cfg.Addr = core.DefaultWebAddr
	}
	addr, err := normalizeAddr(cfg.Addr)
	if err != nil {
		return nil, err
	}
	cfg.Addr = addr
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}

	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("web: open assets: %w", err)
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return nil, fmt.Errorf("web: read index.html: %w", err)
	}
	s := &Server{
		cfg:       cfg,
		logger:    slog.Default(),
		assets:    dist,
		index:     index,
		sessions:  newBrowserSessions(""),
		approvals: newApprovals(),
		questions: newQuestions(),
		// Spec 53: the secure secret input broker.
		secretRequests: newSecretRequests(),
		heartbeat:      defaultHeartbeatInterval,
		poll:           defaultStreamPoll,
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
	}
	s.signInCodes = newSignInCodeStore(s.nowTime)
	s.resetCodes = newResetCodeStore(s.nowTime)
	s.loginLock = newLoginLock(s.nowTime)
	s.roomHub = newRoomHub(s)
	if cfg.Enabled {
		sessions, err := loadBrowserSessions(cfg.Home, s.nowTime())
		if err != nil {
			return nil, err
		}
		s.sessions = sessions
		credentials, err := loadCredentials(cfg.Home)
		if err != nil {
			return nil, err
		}
		s.credentials = credentials
	}
	s.mux = http.NewServeMux()
	s.routes()
	s.registerSessionRoutes()
	s.registerRoomRoutes()
	s.registerDiscussionRoutes()
	s.registerTurnRoutes()
	s.registerStreamRoutes()
	s.registerApprovalRoutes()
	s.registerQuestionRoutes()
	s.registerSecretRequestRoutes()
	s.registerConfigRoutes()
	s.registerSecretRoutes()
	s.registerAgentRoutes()
	s.registerTeamRoutes()
	s.registerProviderRoutes()
	s.registerMCPRoutes()
	s.registerChannelRoutes()
	s.registerAgentFileRoutes()
	s.registerOverviewRoutes()
	s.registerAvatarRoutes()
	s.registerSkillRoutes()
	s.registerDaemonRoutes()
	s.registerUpdateRoutes()
	s.registerSetupRoutes()
	s.registerToolRoutes()
	s.registerWebRoutes()
	s.registerAuditRoutes()
	s.registerLocalRoutes()
	s.registerNotifyRoutes()
	s.registerWorkflowRoutes()
	s.registerToolboxRoutes()
	s.registerCalendarRoutes()
	s.registerRouterRoutes()
	s.handler = securityHeaders(s.mux)
	return s, nil
}

// Handle registers an HTTP handler under the /v1 auth middleware: Host,
// Origin, cookie, and CSRF checks. Future tasks use it for API routes.
func (s *Server) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, s.authenticate(handler))
}

// Handler returns the HTTP handler, security headers included.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Approvals returns the broker that opens browser approval requests.
func (s *Server) Approvals() *Approvals {
	return s.approvals
}

// Questions returns the broker that opens browser question requests.
func (s *Server) Questions() *Questions {
	return s.questions
}

// Start listens on the configured address and serves in the background.
// A listen error is returned before any request is served.
func (s *Server) Start(ctx context.Context) error {
	if !s.cfg.Enabled {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// The daemon root context keeps turns alive after the HTTP request ends.
	if setter, ok := s.backend.(RootContextSetter); ok {
		setter.SetRootContext(ctx)
	}
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.mu.Lock()
	if s.srv != nil {
		s.mu.Unlock()
		_ = ln.Close()
		return errors.New("web: server already started")
	}
	s.ln = ln
	s.srv = srv
	s.mu.Unlock()

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("web: serve", "error", err)
		}
		s.closeDone()
	}()
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			_ = s.Shutdown(shutdownCtx)
		case <-s.done:
		}
	}()
	return nil
}

// Shutdown stops accepting requests and waits for active ones until ctx ends.
func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// Signal the streams first so they can emit shutdown and return before
	// http.Server.Shutdown waits for them.
	s.closeStop()
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	var err error
	if srv != nil {
		err = srv.Shutdown(ctx)
	}
	s.closeDone()
	return err
}

// Addr returns the bound address, or the configured address before Start.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.cfg.Addr
}

func (s *Server) closeDone() {
	s.once.Do(func() { close(s.done) })
}

func (s *Server) closeStop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// stopping is closed when the server begins shutting down.
func (s *Server) stopping() <-chan struct{} {
	return s.stop
}

// SetHeartbeatInterval overrides the SSE heartbeat interval (tests).
func (s *Server) SetHeartbeatInterval(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d <= 0 {
		d = defaultHeartbeatInterval
	}
	s.heartbeat = d
}

// SetStreamPollInterval overrides the stored-event poll interval (tests).
func (s *Server) SetStreamPollInterval(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d <= 0 {
		d = defaultStreamPoll
	}
	s.poll = d
}

func (s *Server) heartbeatInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.heartbeat <= 0 {
		return defaultHeartbeatInterval
	}
	return s.heartbeat
}

func (s *Server) streamPollInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.poll <= 0 {
		return defaultStreamPoll
	}
	return s.poll
}

// RootContextSetter is implemented by a Backend that runs turns on the daemon
// root context. Start calls it before serving.
type RootContextSetter interface {
	SetRootContext(context.Context)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.serveIndex)
	s.mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.HandlerFunc(s.serveAsset)))
	s.mux.HandleFunc("/", http.NotFound)
	// The auth endpoints of spec #18. state, claim, login, and the reset calls
	// are unauthenticated (an owner has no session yet); they keep the Host and
	// Origin checks.
	s.mux.Handle("GET /v1/auth/state", s.checkHost(http.HandlerFunc(s.handleAuthState)))
	s.mux.Handle("POST /v1/auth/claim", s.checkHost(s.checkOrigin(http.HandlerFunc(s.handleClaim))))
	s.mux.Handle("POST /v1/auth/login", s.checkHost(s.checkOrigin(http.HandlerFunc(s.handleLogin))))
	s.mux.Handle("POST /v1/auth/reset/telegram", s.checkHost(s.checkOrigin(http.HandlerFunc(s.handleResetTelegram))))
	s.mux.Handle("POST /v1/auth/reset/verify", s.checkHost(s.checkOrigin(http.HandlerFunc(s.handleResetVerify))))
	s.mux.Handle("POST /v1/auth/reset/restart", s.checkHost(s.checkOrigin(http.HandlerFunc(s.handleResetRestart))))
	s.mux.Handle("POST /v1/auth/password", s.authenticate(http.HandlerFunc(s.handleChangePassword)))
	s.mux.Handle("POST /v1/auth/logout", s.authenticate(http.HandlerFunc(s.handleLogout)))
	s.mux.Handle("GET /v1/auth/session", s.checkHost(s.requireSession(http.HandlerFunc(s.handleSession))))
	s.mux.Handle("GET /v1/auth/sessions", s.authenticate(http.HandlerFunc(s.handleBrowserSessions)))
	s.mux.Handle("POST /v1/auth/sessions/revoke-others", s.authenticate(http.HandlerFunc(s.handleRevokeOtherBrowsers)))
	s.registerDashboardRoutes()
	s.registerKnowledgeRoutes()
}

func (s *Server) serveIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.index)
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" || !fs.ValidPath(name) {
		http.NotFound(w, r)
		return
	}
	info, err := fs.Stat(s.assets, name)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(s.assets, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), bytes.NewReader(data))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if isAPIPath(r.URL.Path) {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func isAPIPath(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/")
}

// normalizeAddr validates the loopback host and returns a net.Listen address.
func normalizeAddr(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// Accept an unbracketed IPv6 literal such as "::1:18300".
		if i := strings.LastIndex(addr, ":"); i > 0 {
			host, port = addr[:i], addr[i+1:]
		} else {
			return "", fmt.Errorf("web: invalid addr %q: %v", addr, err)
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return "", fmt.Errorf("web: invalid addr %q: port must be between 0 and 65535", addr)
	}
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", ErrNotLoopback
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(n)), nil
}

// registerWebRoutes installs the web address save of API-WEB-029 behind the
// owner cookie and CSRF middleware. The audit route is in web/audit.go.
func (s *Server) registerWebRoutes() {
	s.Handle("PUT /v1/web", http.HandlerFunc(s.handlePutWeb))
}

type putWebResponse struct {
	Addr            string `json:"addr"`
	URL             string `json:"url"`
	Version         string `json:"version"`
	RestartRequired bool   `json:"restart_required"`
}

// webValidationError carries config.Check issues through the section writer,
// the same way providerValidationError does.
type webValidationError struct {
	issues []ConfigIssue
}

func (e *webValidationError) Error() string { return "config.toml is not valid" }

// handlePutWeb saves [web] addr through the section writer. The loopback rule
// of REQ-WEB-003 is checked before anything is written, and the new address
// applies after a restart (Q-028).
func (s *Server) handlePutWeb(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	var body struct {
		Addr            string `json:"addr"`
		ExpectedVersion string `json:"expected_version"`
	}
	if !decodeJSONObject(w, r, configBodyLimit, &body) {
		return
	}
	addr, err := normalizeAddr(strings.TrimSpace(body.Addr))
	if err != nil {
		writeWebIssue(w, "web.addr", err.Error())
		return
	}
	if body.ExpectedVersion == "" {
		writeWebIssue(w, "expected_version", "expected_version is required")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before web address save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return
	}
	if file.Version != body.ExpectedVersion {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return
	}
	// Keep the other [web] key; only addr is part of the form.
	var document struct {
		Web struct {
			Enabled *bool `toml:"enabled"`
		} `toml:"web"`
	}
	if err := toml.Unmarshal(file.Raw, &document); err != nil {
		s.logger.Error("web: parse config before web address save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	var table strings.Builder
	table.WriteString("[web]\n")
	if document.Web.Enabled != nil {
		fmt.Fprintf(&table, "enabled = %t\n", *document.Web.Enabled)
	}
	fmt.Fprintf(&table, "addr = %q\n", addr)

	candidate, changed, err := webfiles.RewriteTable(r.Context(), webChecker(store), file.Raw, "[web]", table.String())
	if err != nil {
		s.writeWebSaveError(w, "save web address", err)
		return
	}
	if !bytes.Equal(candidate, file.Raw) {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for web address save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		s.auditSave("web.save", providerConfigPath(file, s.agentHome()), "web", changed)
	}
	writeJSON(w, http.StatusOK, putWebResponse{
		Addr:            addr,
		URL:             "http://" + addr,
		Version:         webfiles.ConfigVersion(candidate),
		RestartRequired: true,
	})
}

func webChecker(store ConfigStore) func(context.Context, []byte) error {
	return func(ctx context.Context, candidate []byte) error {
		issues, err := store.ConfigCheck(ctx, candidate)
		if err != nil {
			return err
		}
		if len(issues) > 0 {
			return &webValidationError{issues: issues}
		}
		return nil
	}
}

func (s *Server) writeWebSaveError(w http.ResponseWriter, operation string, err error) {
	var validation *webValidationError
	if errors.As(err, &validation) {
		writeWebIssues(w, validation.issues)
		return
	}
	s.logger.Error("web: "+operation, "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeWebIssue(w http.ResponseWriter, key, message string) {
	writeWebIssues(w, []ConfigIssue{{Key: key, Message: message}})
}

func writeWebIssues(w http.ResponseWriter, issues []ConfigIssue) {
	details := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		detail := map[string]any{"key": issue.Key, "field": issue.Key, "message": issue.Message}
		if issue.File != "" {
			detail["file"] = issue.File
		}
		details = append(details, detail)
	}
	writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "config.toml is not valid", details)
}
