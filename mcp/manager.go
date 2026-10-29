// Package mcp is lana's MCP client and module.
package mcp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ridhomujizat/lana-harness/core"
)

const (
	stateStopped     = "stopped"
	stateRunning     = "running"
	stateUnavailable = "unavailable"
)

const (
	defaultIdleTimeout    = 10 * time.Minute
	defaultConnectTimeout = 30 * time.Second
	defaultCallTimeout    = 120 * time.Second
	defaultReaperInterval = 30 * time.Second
)

// server is the runtime state of one configured MCP server.
type server struct {
	name string
	cfg  core.MCPConfig

	mu       sync.Mutex
	state    string
	reason   string
	session  *sdk.ClientSession
	tools    []*sdk.Tool
	lastUsed time.Time
	inflight int
}

// Manager connects the configured MCP servers and registers their tools.
type Manager struct {
	cfg      map[string]core.MCPConfig
	home     string
	registry core.ToolRegistry
	servers  map[string]*server

	// IdleTimeout is how long a server may stay unused before it is closed.
	IdleTimeout time.Duration
	// ConnectTimeout bounds initialize and tools/list for one server.
	ConnectTimeout time.Duration
	// CallTimeout bounds one tools/call.
	CallTimeout time.Duration

	now            func() time.Time
	connect        func(ctx context.Context, name string, cfg core.MCPConfig) (sdk.Transport, error)
	reaperInterval time.Duration

	cancelMu sync.Mutex
	cancel   context.CancelFunc
}

// NewManager creates a manager for the given [mcp.*] config.
func NewManager(cfg map[string]core.MCPConfig, home string, registry core.ToolRegistry) *Manager {
	m := &Manager{
		cfg:            cfg,
		home:           home,
		registry:       registry,
		servers:        make(map[string]*server, len(cfg)),
		IdleTimeout:    defaultIdleTimeout,
		ConnectTimeout: defaultConnectTimeout,
		CallTimeout:    defaultCallTimeout,
		now:            time.Now,
		reaperInterval: defaultReaperInterval,
	}
	for name, serverCfg := range cfg {
		m.servers[name] = &server{
			name:  name,
			cfg:   serverCfg,
			state: stateStopped,
		}
	}
	return m
}

// Ensure connects the servers that are not running and returns their tool names
// and the unavailable servers with a short reason.
func (m *Manager) Ensure(ctx context.Context, servers []string) ([]string, map[string]string) {
	unavailable := make(map[string]string)
	names := make(map[string]struct{})
	for _, name := range servers {
		s, ok := m.servers[name]
		if !ok {
			unavailable[name] = "not configured"
			continue
		}

		s.mu.Lock()
		if s.state != stateRunning {
			if err := m.connectLocked(ctx, s); err != nil {
				reason := shortReason(err)
				s.state = stateUnavailable
				s.reason = reason
				s.session = nil
				s.tools = nil
				s.mu.Unlock()
				unavailable[name] = reason
				slog.Info("mcp: server unavailable", "server", name, "reason", reason)
				continue
			}
			slog.Info("mcp: server connected", "server", name, "tools", len(s.tools))
		}
		for _, tool := range s.tools {
			names[toolName(name, tool.Name)] = struct{}{}
		}
		s.mu.Unlock()
	}

	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, unavailable
}

// connectLocked connects one server. The caller holds s.mu.
func (m *Manager) connectLocked(ctx context.Context, s *server) error {
	ctx, cancel := context.WithTimeout(ctx, m.ConnectTimeout)
	defer cancel()

	transport, err := m.transport(ctx, s.name, s.cfg)
	if err != nil {
		return err
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "lana", Version: "dev"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return err
	}
	tools, err := listTools(ctx, session)
	if err != nil {
		_ = session.Close()
		return err
	}
	if m.registry != nil {
		for _, tool := range tools {
			wrapper := newToolWrapper(m, s.name, tool)
			if _, ok := m.registry.Get(wrapper.spec.Name); ok {
				continue
			}
			if err := m.registry.Register(wrapper); err != nil {
				_ = session.Close()
				return err
			}
		}
	}

	s.session = session
	s.state = stateRunning
	s.reason = ""
	s.tools = tools
	s.lastUsed = m.now()
	return nil
}

// listTools collects all pages of the server's tool list.
func listTools(ctx context.Context, session *sdk.ClientSession) ([]*sdk.Tool, error) {
	var tools []*sdk.Tool
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// transport builds the transport for one server.
func (m *Manager) transport(ctx context.Context, name string, cfg core.MCPConfig) (sdk.Transport, error) {
	if m.connect != nil {
		return m.connect(ctx, name, cfg)
	}
	if cfg.Command != "" {
		cmd := exec.Command(cfg.Command, cfg.Args...)
		cmd.Env = commandEnv(cfg.Env)
		cmd.Dir = m.home
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, err
		}
		go logStderr(name, stderr)
		return &sdk.CommandTransport{Command: cmd}, nil
	}
	if cfg.URL != "" {
		return &sdk.StreamableClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: &http.Client{Transport: headerTransport{headers: cfg.Headers, base: http.DefaultTransport}},
		}, nil
	}
	return nil, errors.New("no command or url configured")
}

// commandEnv returns the process environment overridden by the configured one.
func commandEnv(configured map[string]core.Secret) []string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	for key, value := range configured {
		env[key] = value.Reveal()
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

// logStderr forwards a server's stderr to the debug log.
func logStderr(name string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		slog.Debug("mcp: server stderr", "server", name, "line", scanner.Text())
	}
}

// headerTransport adds configured headers to every HTTP request.
type headerTransport struct {
	headers map[string]core.Secret
	base    http.RoundTripper
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for key, value := range t.headers {
		clone.Header.Set(key, value.Reveal())
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// Status returns the state of each listed server in the given order.
func (m *Manager) Status(servers []string) []core.MCPServerStatus {
	out := make([]core.MCPServerStatus, 0, len(servers))
	for _, name := range servers {
		s, ok := m.servers[name]
		if !ok {
			out = append(out, core.MCPServerStatus{Name: name, State: stateUnavailable, Reason: "not configured"})
			continue
		}
		s.mu.Lock()
		out = append(out, core.MCPServerStatus{
			Name:   name,
			State:  s.state,
			Reason: s.reason,
			Tools:  len(s.tools),
		})
		s.mu.Unlock()
	}
	return out
}

// Start launches the idle reaper until ctx is cancelled or Close is called.
func (m *Manager) Start(ctx context.Context) {
	interval := m.reaperInterval
	if interval <= 0 {
		interval = defaultReaperInterval
	}
	ctx, cancel := context.WithCancel(ctx)
	m.cancelMu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = cancel
	m.cancelMu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.ReapIdle()
			}
		}
	}()
}

// ReapIdle closes running servers that have been idle for IdleTimeout.
func (m *Manager) ReapIdle() {
	now := m.now()
	for _, s := range m.servers {
		s.mu.Lock()
		if s.state == stateRunning && s.inflight == 0 && now.Sub(s.lastUsed) >= m.IdleTimeout {
			_ = closeSession(s)
			s.state = stateStopped
			s.reason = ""
			s.tools = nil
			s.mu.Unlock()
			slog.Info("mcp: server stopped (idle)", "server", s.name)
			continue
		}
		s.mu.Unlock()
	}
}

// Close closes all sessions.
func (m *Manager) Close() error {
	m.cancelMu.Lock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.cancelMu.Unlock()

	var errs []error
	for _, s := range m.servers {
		s.mu.Lock()
		if err := closeSession(s); err != nil {
			errs = append(errs, err)
		}
		if s.state == stateRunning {
			s.state = stateStopped
			s.reason = ""
		}
		s.tools = nil
		s.mu.Unlock()
	}
	return errors.Join(errs...)
}

// closeSession closes and clears a server session. The caller holds s.mu.
func closeSession(s *server) error {
	if s.session == nil {
		return nil
	}
	err := s.session.Close()
	s.session = nil
	return err
}

// markStopped marks a failed session as stopped so the next Ensure reconnects.
func (m *Manager) markStopped(s *server, session *sdk.ClientSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == session {
		_ = closeSession(s)
	}
	s.state = stateStopped
	s.reason = ""
	s.tools = nil
}

// shortReason turns an error into the reason shown by the runtime context.
func shortReason(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case errors.Is(err, exec.ErrNotFound), strings.Contains(lower, "executable file not found"):
		return "command not found"
	case strings.Contains(lower, "401"), strings.Contains(lower, "403"),
		strings.Contains(lower, "unauthorized"), strings.Contains(lower, "forbidden"):
		return "unauthorized"
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	runes := []rune(msg)
	if len(runes) > 80 {
		msg = string(runes[:80])
	}
	return msg
}

var _ core.MCPManager = (*Manager)(nil)
