// Package mcphost serves lana's tools to Claude Code turns over a loopback
// MCP server (ADR 0020, REQ-CCT-006, API-CCT-007).
//
// One Host runs one listener on 127.0.0.1 with a random port, started on
// first use. Each lana session gets its own bearer token (32 random bytes)
// while its Claude process lives, and its own MCP server whose tools are the
// session's lana tools. A call runs through the handler the agent bound for
// the session's running turn, so lana's tool runner, permission engine, and
// approvals apply. Requests without a valid token get 401.
package mcphost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ridhomujizat/lana-harness/core"
)

// ServerName is the MCP server name Claude Code sees; its tools appear as
// mcp__lana__<tool> (API-CCT-007).
const ServerName = "lana"

// Path is the MCP endpoint path.
const Path = "/mcp"

// ToolPrefix is the prefix Claude Code gives the host's tools.
const ToolPrefix = "mcp__" + ServerName + "__"

// tokenBytes is the size of a session token (REQ-CCT-006).
const tokenBytes = 32

// ErrClosed is returned after Close.
var ErrClosed = errors.New("mcphost: closed")

// Options tune a Host.
type Options struct {
	// Logger receives warnings; nil discards them.
	Logger *slog.Logger
	// Listen opens the listener; nil listens on 127.0.0.1:0. Tests use it.
	Listen func() (net.Listener, error)
	// Version is the server version reported to clients.
	Version string
}

// Host is the loopback MCP server. It implements core.RuntimeToolHost and is
// safe for concurrent use.
type Host struct {
	opts Options
	log  *slog.Logger

	mu       sync.Mutex
	started  bool
	startErr error
	closed   bool
	listener net.Listener
	server   *http.Server
	url      string
	sessions map[string]*session
	tokens   map[string]string // token -> lana session ID
}

var _ core.RuntimeToolHost = (*Host)(nil)

// session is the MCP state of one lana session.
type session struct {
	id     string
	server *mcp.Server
	tools  map[string]string // registered tool name -> spec fingerprint
	call   core.RuntimeToolFunc
	tokens int
}

// New returns a Host. It does not listen until the first Bind or Open.
func New(opts Options) *Host {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	return &Host{opts: opts, log: log, sessions: map[string]*session{}, tokens: map[string]string{}}
}

// start opens the listener once. h.mu is held.
func (h *Host) start() error {
	if h.closed {
		return ErrClosed
	}
	if h.started {
		return h.startErr
	}
	h.started = true
	listen := h.opts.Listen
	if listen == nil {
		listen = func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	}
	ln, err := listen()
	if err != nil {
		h.startErr = fmt.Errorf("lana MCP host cannot listen on 127.0.0.1: %w", err)
		return h.startErr
	}
	mcpHandler := mcp.NewStreamableHTTPHandler(h.serverFor, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux := http.NewServeMux()
	mux.Handle(Path, h.authorize(mcpHandler))
	h.listener = ln
	h.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	h.url = "http://" + ln.Addr().String() + Path
	go func() {
		if err := h.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			h.log.Warn("mcphost: server stopped", "error", err)
		}
	}()
	return nil
}

// URL returns the MCP endpoint URL, or "" before the host started.
func (h *Host) URL() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.url
}

type sessionKey struct{}

// authorize admits requests that carry a live session token and puts the
// session ID in the request context (AC-CCT-008).
func (h *Host) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearer(r.Header.Get("Authorization"))
		sessionID := ""
		if ok {
			h.mu.Lock()
			sessionID = h.tokens[token]
			h.mu.Unlock()
		}
		if sessionID == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="lana"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sessionID)))
	})
}

func bearer(header string) (string, bool) {
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}

// serverFor returns the MCP server of the request's session. authorize ran
// first, so the session exists unless it was released in between.
func (h *Host) serverFor(r *http.Request) *mcp.Server {
	sessionID, _ := r.Context().Value(sessionKey{}).(string)
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.sessions[sessionID]; s != nil {
		return s.server
	}
	return nil
}

// sessionLocked returns the session state, creating it. h.mu is held.
func (h *Host) sessionLocked(sessionID string) *session {
	s := h.sessions[sessionID]
	if s == nil {
		s = &session{
			id:     sessionID,
			server: mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: h.opts.Version}, &mcp.ServerOptions{Logger: h.log}),
			tools:  map[string]string{},
		}
		h.sessions[sessionID] = s
	}
	return s
}

// Bind sets the session's tools and the call handler of its running turn. The
// tool list replaces the previous one.
func (h *Host) Bind(sessionID string, tools []core.ToolSpec, call core.RuntimeToolFunc) error {
	if sessionID == "" {
		return errors.New("mcphost: no session ID")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.start(); err != nil {
		return err
	}
	s := h.sessionLocked(sessionID)
	s.call = call
	want := map[string]core.ToolSpec{}
	for _, spec := range tools {
		if spec.Name != "" {
			want[spec.Name] = spec
		}
	}
	var stale []string
	for name, fingerprint := range s.tools {
		spec, ok := want[name]
		if !ok || specFingerprint(spec) != fingerprint {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 {
		s.server.RemoveTools(stale...)
		for _, name := range stale {
			delete(s.tools, name)
		}
	}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := s.tools[name]; ok {
			continue
		}
		spec := want[name]
		s.server.AddTool(&mcp.Tool{Name: name, Description: spec.Description, InputSchema: objectSchema(spec.InputSchema)}, h.handler(sessionID, name))
		s.tools[name] = specFingerprint(spec)
	}
	return nil
}

// Unbind clears the session's call handler. Calls then fail until the next
// Bind.
func (h *Host) Unbind(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.sessions[sessionID]; s != nil {
		s.call = nil
		h.dropIdleLocked(s)
	}
}

// Open issues a token for the session and returns the --mcp-config JSON
// (API-CCT-007). release revokes the token.
func (h *Host) Open(sessionID string) (string, func(), error) {
	if sessionID == "" {
		return "", nil, errors.New("mcphost: no session ID")
	}
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	token := hex.EncodeToString(buf)
	h.mu.Lock()
	if err := h.start(); err != nil {
		h.mu.Unlock()
		return "", nil, err
	}
	s := h.sessionLocked(sessionID)
	s.tokens++
	h.tokens[token] = sessionID
	url := h.url
	h.mu.Unlock()
	config, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		ServerName: map[string]any{"type": "http", "url": url, "headers": map[string]string{"Authorization": "Bearer " + token}},
	}})
	if err != nil {
		h.revoke(token)
		return "", nil, err
	}
	var once sync.Once
	return string(config), func() { once.Do(func() { h.revoke(token) }) }, nil
}

func (h *Host) revoke(token string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sessionID, ok := h.tokens[token]
	if !ok {
		return
	}
	delete(h.tokens, token)
	if s := h.sessions[sessionID]; s != nil {
		s.tokens--
		h.dropIdleLocked(s)
	}
}

// dropIdleLocked forgets a session with no token and no bound turn. h.mu is
// held.
func (h *Host) dropIdleLocked(s *session) {
	if s.tokens <= 0 && s.call == nil {
		delete(h.sessions, s.id)
	}
}

// handler runs one tool call through the session's bound call handler.
func (h *Host) handler(sessionID, name string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.mu.Lock()
		var call core.RuntimeToolFunc
		if s := h.sessions[sessionID]; s != nil {
			call = s.call
		}
		h.mu.Unlock()
		if call == nil {
			return errorResult("no lana turn is running for this session"), nil
		}
		var input json.RawMessage
		callID := ""
		if req != nil && req.Params != nil {
			input = req.Params.Arguments
			if id, ok := req.Params.Meta["claudecode/toolUseId"].(string); ok {
				callID = id
			}
		}
		if len(input) == 0 || string(input) == "null" {
			input = json.RawMessage("{}")
		}
		if callID == "" {
			callID = "mcp_" + randomID()
		}
		out := call(ctx, callID, name, input)
		return toolResult(out), nil
	}
}

// toolResult converts a lana tool output to an MCP result: the text, then
// any images.
func toolResult(out core.ToolOutput) *mcp.CallToolResult {
	res := &mcp.CallToolResult{IsError: out.Status != core.ToolStatusSuccess}
	text := out.Output
	if text == "" && res.IsError {
		text = "the tool failed"
	}
	res.Content = append(res.Content, &mcp.TextContent{Text: text})
	for _, img := range out.Images {
		if img.Kind != core.BlockImage || len(img.Data) == 0 {
			continue
		}
		res.Content = append(res.Content, &mcp.ImageContent{Data: img.Data, MIMEType: img.MediaType})
	}
	return res
}

func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// objectSchema returns a tool's input schema as a JSON object with type
// "object", which MCP requires. An empty or unreadable schema accepts any
// object.
func objectSchema(raw json.RawMessage) map[string]any {
	schema := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &schema)
	}
	if schema == nil {
		schema = map[string]any{}
	}
	if schema["type"] != "object" {
		schema["type"] = "object"
	}
	return schema
}

func specFingerprint(spec core.ToolSpec) string {
	return spec.Description + "\x00" + string(spec.InputSchema)
}

func randomID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// Close stops the listener and revokes every token.
func (h *Host) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	srv := h.server
	h.tokens = map[string]string{}
	h.sessions = map[string]*session{}
	h.mu.Unlock()
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
