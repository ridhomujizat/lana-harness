package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/mcpconfig"
	"github.com/ridhomujizat/lana-harness/internal/toolbox"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

const (
	// toolboxBodyLimit is the largest accepted /v1/toolbox body (API-TB-002).
	toolboxBodyLimit = 1 << 20
	// toolboxJobHistory is how many jobs the detail view keeps (API-TB-003).
	toolboxJobHistory = 10
	// toolboxJobWatchTimeout bounds how long the install watcher follows a job
	// before giving up (REQ-TB-011).
	toolboxJobWatchTimeout = 20 * time.Minute
	// toolboxJobPollDefault is the install-watcher poll interval.
	toolboxJobPollDefault = 2 * time.Second
)

// ToolboxBackend is the subset of *toolbox.Service the web handlers use
// (spec #34, design section "Web API"). It is defined in web so the package
// never imports the runtime; the service satisfies it. The daemon installs it
// with SetToolbox.
type ToolboxBackend interface {
	// Subscribe returns the changed-package stream and its unsubscribe
	// function (REQ-TB-002).
	Subscribe() (<-chan string, func())
	List(ctx context.Context) []toolbox.Package
	Get(ctx context.Context, id string) (toolbox.Package, error)
	Add(ctx context.Context, manifestText string) (toolbox.Package, []toolbox.Issue, error)
	Update(ctx context.Context, id, manifestText string) (toolbox.Package, []toolbox.Issue, error)
	Delete(ctx context.Context, id string, uninstall bool) error
	Install(ctx context.Context, id string, method int) (toolbox.Job, error)
	Uninstall(ctx context.Context, id string) (toolbox.Job, error)
	Check(ctx context.Context, id string) (toolbox.Package, error)
	Jobs(id string) []toolbox.Job
	Log(id, jobID string) (string, bool, error)
	Run(ctx context.Context, id, cmd string, input map[string]any, workspace string) (toolbox.RunResult, error)
	// MCPServers returns one MCP server per installed package that declares an
	// [mcp] spec (REQ-TB-011).
	MCPServers() []toolbox.MCPServer
}

// toolboxMCPSyncMu serializes the managed-MCP applies so a toolbox event and a
// direct sync never interleave. The config write itself is also under
// configWriteMu (REQ-TB-011).
var toolboxMCPSyncMu sync.Mutex

// toolboxMCPLoops tracks the one background MCP sync loop each server owns.
var toolboxMCPLoops sync.Map

// SetToolbox installs the Tools port. Call it before Start; a nil port makes
// the /v1/toolbox routes answer 503 toolbox_unavailable.
//
// Installing the port also starts the managed-MCP loop: the loop keeps the
// [mcp.<package id>] servers lana recorded in step with the installed packages
// (it updates or removes a recorded server, never adds one) and follows the
// toolbox change stream, and it stops with the server (REQ-TB-011).
func (s *Server) SetToolbox(b ToolboxBackend) {
	s.toolboxMu.Lock()
	s.toolbox = b
	store := s.configStore
	s.toolboxMu.Unlock()
	if b == nil {
		return
	}
	// Capture the config store here so the loop never reads a field another
	// goroutine may write. A server without a config store has nothing to sync.
	if _, loaded := toolboxMCPLoops.LoadOrStore(s, struct{}{}); loaded {
		return
	}
	go s.runToolboxMCP(b, store)
}

// runToolboxMCP keeps the managed MCP servers in step with the installed
// packages on every toolbox change until the server stops (REQ-TB-011).
func (s *Server) runToolboxMCP(backend ToolboxBackend, store ConfigStore) {
	changes, unsubscribe := backend.Subscribe()
	defer unsubscribe()
	s.runToolboxMCPSync(context.Background(), backend, store)
	for {
		select {
		case <-s.stopping():
			return
		case _, ok := <-changes:
			if !ok {
				return
			}
			s.runToolboxMCPSync(context.Background(), backend, store)
		}
	}
}

// syncToolboxMCP applies the managed MCP servers once with the installed config
// store (REQ-TB-011).
func (s *Server) syncToolboxMCP(ctx context.Context) {
	s.runToolboxMCPSync(ctx, s.toolboxBackend(), s.configStore)
}

// runToolboxMCPSync serializes one apply of the managed MCP servers.
func (s *Server) runToolboxMCPSync(ctx context.Context, backend ToolboxBackend, store ConfigStore) {
	toolboxMCPSyncMu.Lock()
	defer toolboxMCPSyncMu.Unlock()
	s.applyToolboxMCP(ctx, backend, store)
}

func (s *Server) toolboxBackend() ToolboxBackend {
	s.toolboxMu.RLock()
	defer s.toolboxMu.RUnlock()
	return s.toolbox
}

func (s *Server) toolboxOrError(w http.ResponseWriter) ToolboxBackend {
	backend := s.toolboxBackend()
	if backend == nil {
		s.logger.Error("web: toolbox backend is not configured")
		writeAPIError(w, http.StatusServiceUnavailable, "toolbox_unavailable", "tools are not available")
		return nil
	}
	return backend
}

// registerToolboxRoutes registers API-TB-001 through API-TB-010 (spec #34).
// The /v1 routes go through Handle so the auth, Host, Origin, and CSRF checks
// apply.
func (s *Server) registerToolboxRoutes() {
	s.Handle("GET /v1/toolbox", http.HandlerFunc(s.handleToolboxList))
	s.Handle("POST /v1/toolbox", http.HandlerFunc(s.handleToolboxAdd))
	s.Handle("GET /v1/toolbox/stream", http.HandlerFunc(s.handleToolboxStream))
	s.Handle("GET /v1/toolbox/{id}", http.HandlerFunc(s.handleToolboxGet))
	s.Handle("PUT /v1/toolbox/{id}", http.HandlerFunc(s.handleToolboxUpdate))
	s.Handle("DELETE /v1/toolbox/{id}", http.HandlerFunc(s.handleToolboxDelete))
	s.Handle("POST /v1/toolbox/{id}/install", http.HandlerFunc(s.handleToolboxInstall))
	s.Handle("POST /v1/toolbox/{id}/uninstall", http.HandlerFunc(s.handleToolboxUninstall))
	s.Handle("POST /v1/toolbox/{id}/check", http.HandlerFunc(s.handleToolboxCheck))
	s.Handle("POST /v1/toolbox/{id}/agents", http.HandlerFunc(s.handleToolboxSetAgent))
	s.Handle("GET /v1/toolbox/{id}/jobs/{job}/log", http.HandlerFunc(s.handleToolboxJobLog))
	s.Handle("POST /v1/toolbox/{id}/run", http.HandlerFunc(s.handleToolboxRun))
}

type toolboxListView struct {
	Packages []toolbox.PackageView `json:"packages"`
}

// handleToolboxList serves API-TB-001 (REQ-TB-001).
func (s *Server) handleToolboxList(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	packages := backend.List(r.Context())
	views := make([]toolbox.PackageView, 0, len(packages))
	for _, pkg := range packages {
		views = append(views, pkg.View())
	}
	writeJSON(w, http.StatusOK, toolboxListView{Packages: views})
}

// handleToolboxAdd serves API-TB-002 (REQ-TB-001). An invalid manifest is 400
// invalid_manifest; a taken id is 409 package_exists.
func (s *Server) handleToolboxAdd(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Manifest string `json:"manifest"`
	}
	if !decodeJSONObject(w, r, toolboxBodyLimit, &body) {
		return
	}
	pkg, issues, err := backend.Add(r.Context(), body.Manifest)
	switch {
	case err != nil:
		s.writeToolboxError(w, "add package", err)
	case len(issues) > 0:
		writeToolboxIssues(w, issues)
	default:
		writeJSON(w, http.StatusCreated, pkg.View())
	}
}

// handleToolboxGet serves API-TB-003 (REQ-TB-001): the PackageView plus the
// manifest text, the last jobs and the agents and workflows that use it.
func (s *Server) handleToolboxGet(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	pkg, err := backend.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeToolboxError(w, "get package", err)
		return
	}
	writeJSON(w, http.StatusOK, s.toolboxDetail(r.Context(), backend, r.PathValue("id"), pkg))
}

// handleToolboxUpdate serves API-TB-004 (REQ-TB-001). The manifest may not
// change the package id; that is 400 id_mismatch.
func (s *Server) handleToolboxUpdate(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	id := r.PathValue("id")
	var body struct {
		Manifest string `json:"manifest"`
	}
	if !decodeJSONObject(w, r, toolboxBodyLimit, &body) {
		return
	}
	if manifest, issues := toolbox.ParseManifest(body.Manifest); len(issues) == 0 && manifest.ID != id {
		writeAPIError(w, http.StatusBadRequest, "id_mismatch", "the manifest id must match the package id")
		return
	}
	pkg, issues, err := backend.Update(r.Context(), id, body.Manifest)
	switch {
	case err != nil:
		s.writeToolboxError(w, "update package", err)
	case len(issues) > 0:
		writeToolboxIssues(w, issues)
	default:
		writeJSON(w, http.StatusOK, pkg.View())
	}
}

// handleToolboxDelete serves API-TB-005 (REQ-TB-008). An installed package is
// refused with 409 package_installed unless ?uninstall=1 asks for the removal.
func (s *Server) handleToolboxDelete(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	uninstall := r.URL.Query().Get("uninstall") == "1"
	if err := backend.Delete(r.Context(), r.PathValue("id"), uninstall); err != nil {
		s.writeToolboxError(w, "delete package", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleToolboxInstall serves API-TB-006 (REQ-TB-003, REQ-TB-009). The job runs
// in the background, so the answer is 202 with the running job. A missing
// method, or -1, installs with the first method that fits this machine. A
// successful install is followed by a watcher that registers the package's
// managed MCP server, when it provides one (REQ-TB-011).
func (s *Server) handleToolboxInstall(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Method *int `json:"method"`
	}
	if !decodeJSONObject(w, r, toolboxBodyLimit, &body) {
		return
	}
	method := -1
	if body.Method != nil {
		method = *body.Method
	}
	job, err := backend.Install(r.Context(), r.PathValue("id"), method)
	if err != nil {
		s.writeToolboxError(w, "install package", err)
		return
	}
	// The install is the owner's action that may add [mcp.<id>], but the job
	// runs in the background, so a server-owned watcher adds it on success
	// (REQ-TB-011).
	go s.watchToolboxInstallJob(backend, s.configStore, r.PathValue("id"), job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

// watchToolboxInstallJob follows one install job and, when it succeeds,
// registers the managed MCP server of the package (REQ-TB-011). It polls the
// job until it leaves "running", or until the deadline, and stops with the
// server.
func (s *Server) watchToolboxInstallJob(backend ToolboxBackend, store ConfigStore, id, jobID string) {
	if backend == nil || store == nil || id == "" || jobID == "" {
		return
	}
	ticker := time.NewTicker(s.toolboxJobPollInterval())
	defer ticker.Stop()
	deadline := time.NewTimer(toolboxJobWatchTimeout)
	defer deadline.Stop()
	for {
		select {
		case <-s.stopping():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
		status, ok := toolboxJobStatus(backend, id, jobID)
		if !ok || status == toolbox.JobRunning {
			continue
		}
		if status == toolbox.JobSucceeded {
			if err := s.addToolboxMCP(context.Background(), backend, store, id); err != nil {
				s.logger.Error("web: toolbox mcp: add after install", "package", id, "error", err)
			}
		}
		return
	}
}

// toolboxJobStatus returns the status of one job of a package, and whether it
// is known yet (REQ-TB-011).
func toolboxJobStatus(backend ToolboxBackend, id, jobID string) (string, bool) {
	for _, job := range backend.Jobs(id) {
		if job.ID == jobID {
			return job.Status, true
		}
	}
	return "", false
}

// toolboxJobPollInterval returns the install-watcher poll interval.
func (s *Server) toolboxJobPollInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.toolboxJobPoll <= 0 {
		return toolboxJobPollDefault
	}
	return s.toolboxJobPoll
}

// SetToolboxJobPollInterval overrides the install-watcher poll interval (tests).
func (s *Server) SetToolboxJobPollInterval(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d <= 0 {
		d = toolboxJobPollDefault
	}
	s.toolboxJobPoll = d
}

// handleToolboxUninstall serves API-TB-007 (REQ-TB-008).
func (s *Server) handleToolboxUninstall(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	job, err := backend.Uninstall(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeToolboxError(w, "uninstall package", err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// handleToolboxCheck serves API-TB-008 (REQ-TB-002).
func (s *Server) handleToolboxCheck(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	pkg, err := backend.Check(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeToolboxError(w, "check package", err)
		return
	}
	writeJSON(w, http.StatusOK, pkg.View())
}

// errToolboxAgentNotFound is the "Use with agents" error for an unknown agent.
var errToolboxAgentNotFound = errors.New("web: toolbox agent not found")

// errToolboxConfigMissing is the "Use with agents" error for a missing
// config.toml.
var errToolboxConfigMissing = errors.New("web: config.toml does not exist")

// handleToolboxSetAgent serves API-TB-010, the "Use with agents" switch of
// REQ-TB-011. It turns one package on or off for one agent and answers the
// refreshed detail. An MCP package toggles the agent's mcp list; a package
// without an MCP spec toggles its command tools in the agent's tools list. A
// package that is not installed is 409 not_installed (REQ-TB-011).
func (s *Server) handleToolboxSetAgent(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Agent   string `json:"agent"`
		Enabled bool   `json:"enabled"`
	}
	if !decodeJSONObject(w, r, toolboxBodyLimit, &body) {
		return
	}
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	id := r.PathValue("id")
	pkg, err := backend.Get(r.Context(), id)
	if err != nil {
		s.writeToolboxError(w, "get package", err)
		return
	}
	if pkg.Status.State != toolbox.StateInstalled {
		writeAPIError(w, http.StatusConflict, "not_installed", "package is not installed")
		return
	}
	if err := s.setToolboxAgentUse(r.Context(), backend, store, pkg, body.Agent, body.Enabled); err != nil {
		s.writeToolboxAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toolboxDetail(r.Context(), backend, id, pkg))
}

// setToolboxAgentUse edits one agent's mcp or tools list with the same config
// path PUT /v1/agents/{name} uses: read the current version internally, rewrite
// the one table, validate, write, and apply (REQ-TB-011).
func (s *Server) setToolboxAgentUse(ctx context.Context, backend ToolboxBackend, store ConfigStore, pkg toolbox.Package, agent string, enabled bool) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(ctx)
	if err != nil {
		return err
	}
	if !file.Exists {
		return errToolboxConfigMissing
	}
	tables, err := parseAgentTables(file.Raw)
	if err != nil {
		return err
	}
	source, exists := tables[agent]
	if !exists {
		return errToolboxAgentNotFound
	}
	record := agentRecordFromMap(source)
	candidate := file.Raw
	if pkg.Manifest.MCP != nil {
		if enabled {
			// The agent's mcp list must name a registered server, so write the
			// managed [mcp.<id>] table first when it is still missing.
			if srv, ok := toolboxMCPServer(backend, pkg.Manifest.ID); ok {
				next, _, err := toolboxEnsureMCPTable(ctx, store, candidate, srv)
				if err != nil {
					return err
				}
				candidate = next
			}
		}
		record.MCP = setStringList(record.MCP, pkg.Manifest.ID, enabled)
	} else {
		for _, command := range pkg.Manifest.Commands {
			record.Tools = setStringList(record.Tools, toolbox.ToolName(pkg.Manifest.ID, command.Name), enabled)
		}
	}
	table := renderAgentTable(agent, record)
	candidate, _, err = webfiles.RewriteTable(ctx, agentChecker(store), candidate, "[agents."+agent+"]", table)
	if err != nil {
		return err
	}
	if !bytes.Equal(candidate, file.Raw) {
		if err := store.ConfigWrite(ctx, candidate); err != nil {
			return err
		}
		s.applyLiveConfig(ctx, store, candidate)
	}
	// Turning an MCP package on for an agent is the owner's action that adds
	// the server, so record it as managed for the background sync (REQ-TB-011).
	if pkg.Manifest.MCP != nil && enabled {
		if err := s.recordManagedMCP(store, pkg.Manifest.ID); err != nil {
			return err
		}
	}
	return nil
}

// addToolboxMCP registers the managed MCP server of one installed package when
// it provides one: it writes [mcp.<id>] through the same rewrite path the MCP
// save handler uses and records the id as managed (REQ-TB-011). It never
// creates config.toml and never touches an unmanaged entry.
func (s *Server) addToolboxMCP(ctx context.Context, backend ToolboxBackend, store ConfigStore, id string) error {
	if backend == nil || store == nil {
		return nil
	}
	srv, ok := toolboxMCPServer(backend, id)
	if !ok {
		return nil
	}
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	file, err := store.ConfigFile(ctx)
	if err != nil {
		return err
	}
	if !file.Exists {
		return nil
	}
	candidate, wrote, err := toolboxEnsureMCPTable(ctx, store, file.Raw, srv)
	if err != nil {
		return err
	}
	if wrote {
		if err := store.ConfigWrite(ctx, candidate); err != nil {
			return err
		}
		s.applyLiveConfig(ctx, store, candidate)
	}
	return s.recordManagedMCP(store, id)
}

// recordManagedMCP adds a name to the managed-MCP record so the background sync
// keeps it in step and removes it on uninstall (REQ-TB-011).
func (s *Server) recordManagedMCP(store ConfigStore, name string) error {
	path := s.toolboxManagedMCPPath(store)
	if path == "" || name == "" {
		return nil
	}
	names := s.readManagedMCP(path)
	if slices.Contains(names, name) {
		return nil
	}
	names = append(names, name)
	sort.Strings(names)
	return s.writeManagedMCP(path, names)
}

// writeToolboxAgentError maps a "Use with agents" failure to the contract
// error. A validation problem keeps the config issue details.
func (s *Server) writeToolboxAgentError(w http.ResponseWriter, err error) {
	var mcpInvalid *mcpValidationError
	var agentInvalid *agentValidationError
	switch {
	case errors.Is(err, errToolboxAgentNotFound):
		writeAPIError(w, http.StatusNotFound, "agent_not_found", "agent is not configured")
	case errors.Is(err, errToolboxConfigMissing):
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
	case errors.As(err, &mcpInvalid):
		writeMCPIssues(w, mcpInvalid.issues)
	case errors.As(err, &agentInvalid):
		writeAgentIssues(w, agentInvalid.issues)
	default:
		s.logger.Error("web: set toolbox agent use", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// toolboxMCPServer returns the resolved MCP server of a package, when one is
// available (REQ-TB-011).
func toolboxMCPServer(backend ToolboxBackend, id string) (toolbox.MCPServer, bool) {
	for _, srv := range backend.MCPServers() {
		if srv.Package == id {
			return srv, true
		}
	}
	return toolbox.MCPServer{}, false
}

// setStringList adds value to list when on and removes it otherwise, keeping
// the order of the entries that stay.
func setStringList(list []string, value string, on bool) []string {
	out := make([]string, 0, len(list)+1)
	present := false
	for _, item := range list {
		if item == value {
			present = true
			if !on {
				continue
			}
		}
		out = append(out, item)
	}
	if on && !present {
		out = append(out, value)
	}
	return out
}

// handleToolboxJobLog serves API-TB-009: the log tail and whether the job has
// finished.
func (s *Server) handleToolboxJobLog(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	text, done, err := backend.Log(r.PathValue("id"), r.PathValue("job"))
	if err != nil {
		s.writeToolboxError(w, "job log", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Text string `json:"text"`
		Done bool   `json:"done"`
	}{Text: text, Done: done})
}

// exitCodeRE reads the exit code from a toolbox run error such as
// "exit code 3: ..." (API-TB-009).
var exitCodeRE = regexp.MustCompile(`^exit code (\d+):`)

// handleToolboxRun serves API-TB-009 (REQ-TB-004): the owner's "Try it". A
// missing package is 409 not_installed; any other command failure is 400
// command_failed, with the exit code when the message carries one.
func (s *Server) handleToolboxRun(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Command string         `json:"command"`
		Input   map[string]any `json:"input"`
	}
	if !decodeJSONObject(w, r, toolboxBodyLimit, &body) {
		return
	}
	result, err := backend.Run(r.Context(), r.PathValue("id"), body.Command, body.Input, "")
	switch {
	case errors.Is(err, toolbox.ErrNotInstalled):
		writeAPIError(w, http.StatusConflict, "not_installed", "package is not installed")
		return
	case errors.Is(err, toolbox.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "package_not_found", "package not found")
		return
	case err != nil:
		s.writeToolboxRunError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) writeToolboxRunError(w http.ResponseWriter, err error) {
	message := err.Error()
	var details []map[string]any
	if match := exitCodeRE.FindStringSubmatch(message); match != nil {
		if code, convErr := strconv.Atoi(match[1]); convErr == nil {
			details = []map[string]any{{"exit_code": code}}
		}
	}
	writeAPIErrorDetails(w, http.StatusBadRequest, "command_failed", message, details)
}

// toolboxChangeJSON is one changed event of the stream (API-TB-009).
type toolboxChangeJSON struct {
	Package string `json:"package"`
}

// handleToolboxStream serves the SSE stream of API-TB-009 (REQ-TB-002). It
// follows the workflow stream loop: text/event-stream, a heartbeat comment, a
// shutdown event, and a return on client disconnect.
func (s *Server) handleToolboxStream(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.logger.Error("web: toolbox stream: response writer cannot flush")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	changes, unsubscribe := backend.Subscribe()
	defer unsubscribe()

	heartbeat := time.NewTicker(s.heartbeatInterval())
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stopping():
			_ = writeSSE(w, flusher, "shutdown", "", struct{}{})
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case id, ok := <-changes:
			if !ok {
				return
			}
			if err := writeSSE(w, flusher, "changed", "", toolboxChangeJSON{Package: id}); err != nil {
				return
			}
		}
	}
}

// toolboxWorkflowRef is one used_by workflow row.
type toolboxWorkflowRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// toolboxUsedBy is the used_by object of API-TB-003 (REQ-TB-005, REQ-TB-006).
type toolboxUsedBy struct {
	Agents    []string             `json:"agents"`
	Workflows []toolboxWorkflowRef `json:"workflows"`
}

// toolboxAgentUse is one "Use with agents" row of API-TB-003 (REQ-TB-011).
type toolboxAgentUse struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// toolboxDetailView is the PackageView plus the manifest text, the last jobs,
// the used_by lists, and the "Use with agents" rows (API-TB-003).
type toolboxDetailView struct {
	toolbox.PackageView
	Manifest string            `json:"manifest"`
	Jobs     []toolbox.Job     `json:"jobs"`
	UsedBy   toolboxUsedBy     `json:"used_by"`
	Agents   []toolboxAgentUse `json:"agents"`
}

func (s *Server) toolboxDetail(ctx context.Context, backend ToolboxBackend, id string, pkg toolbox.Package) toolboxDetailView {
	toolNames := make(map[string]bool, len(pkg.Manifest.Commands))
	for _, command := range pkg.Manifest.Commands {
		toolNames[toolbox.ToolName(pkg.Manifest.ID, command.Name)] = true
	}
	jobs := backend.Jobs(id)
	if len(jobs) > toolboxJobHistory {
		jobs = jobs[:toolboxJobHistory]
	}
	if jobs == nil {
		jobs = []toolbox.Job{}
	}
	return toolboxDetailView{
		PackageView: pkg.View(),
		Manifest:    toolboxManifestText(backend, pkg),
		Jobs:        jobs,
		UsedBy: toolboxUsedBy{
			Agents:    s.toolboxUsedByAgents(ctx, toolNames),
			Workflows: s.toolboxUsedByWorkflows(ctx, toolNames, pkg.Manifest.Detect.Binary),
		},
		Agents: s.toolboxDetailAgents(ctx, pkg),
	}
}

// toolboxDetailAgents lists every configured agent with whether it may use the
// package: an MCP package is on when the agent's mcp list names the package id,
// a package without an MCP spec is on when the agent's tools list holds one of
// its command tools (REQ-TB-005, REQ-TB-011). Teams are not agents and are not
// listed.
func (s *Server) toolboxDetailAgents(ctx context.Context, pkg toolbox.Package) []toolboxAgentUse {
	rows := []toolboxAgentUse{}
	store := s.configStore
	if store == nil {
		return rows
	}
	file, err := store.ConfigFile(ctx)
	if err != nil || !file.Exists {
		return rows
	}
	tables, err := parseAgentTables(file.Raw)
	if err != nil {
		return rows
	}
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rows = append(rows, toolboxAgentUse{Name: name, Enabled: toolboxAgentUses(pkg, agentRecordFromMap(tables[name]))})
	}
	return rows
}

// toolboxAgentUses reports whether an agent record turns a package on.
func toolboxAgentUses(pkg toolbox.Package, record agentRecord) bool {
	if pkg.Manifest.MCP != nil {
		return slices.Contains(record.MCP, pkg.Manifest.ID)
	}
	for _, command := range pkg.Manifest.Commands {
		if slices.Contains(record.Tools, toolbox.ToolName(pkg.Manifest.ID, command.Name)) {
			return true
		}
	}
	return false
}

// toolboxManifestText returns the tool.toml text of a package: the raw file
// for a broken package when the backend exposes its store, else the encoded
// manifest (API-TB-003).
func toolboxManifestText(backend ToolboxBackend, pkg toolbox.Package) string {
	if pkg.Problem != "" {
		if provider, ok := backend.(interface{ Store() *toolbox.Store }); ok {
			if data, err := os.ReadFile(provider.Store().ManifestPath(pkg.Manifest.ID)); err == nil {
				return string(data)
			}
		}
	}
	text, err := toolbox.Encode(pkg.Manifest)
	if err != nil {
		return ""
	}
	return text
}

// toolboxUsedByAgents lists the configured agents whose tools include one of
// the package's commands (REQ-TB-005, API-TB-003). The agent tools live in
// config.toml, read the same way the agent list reads them.
func (s *Server) toolboxUsedByAgents(ctx context.Context, toolNames map[string]bool) []string {
	names := []string{}
	if s.configStore == nil || len(toolNames) == 0 {
		return names
	}
	file, err := s.configStore.ConfigFile(ctx)
	if err != nil || !file.Exists {
		return names
	}
	tables, err := parseAgentTables(file.Raw)
	if err != nil {
		return names
	}
	for name, table := range tables {
		record := agentRecordFromMap(table)
		for _, tool := range record.Tools {
			if toolNames[tool] {
				names = append(names, name)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}

// toolboxUsedByWorkflows lists the workflows that use one of the package's
// tools: a tool node whose params.name matches, or a browser node when the
// package detects the obscura binary (REQ-TB-006, API-TB-003).
func (s *Server) toolboxUsedByWorkflows(ctx context.Context, toolNames map[string]bool, detectBinary string) []toolboxWorkflowRef {
	refs := []toolboxWorkflowRef{}
	backend := s.workflowsBackend()
	if backend == nil {
		return refs
	}
	overview, err := backend.Overview(ctx)
	if err != nil {
		s.logger.Debug("web: toolbox used_by workflows", "error", err)
		return refs
	}
	for _, summary := range overview.Workflows {
		detail, err := backend.Get(ctx, summary.ID)
		if err != nil {
			s.logger.Debug("web: toolbox used_by workflow graph", "id", summary.ID, "error", err)
			continue
		}
		if workflowGraphUsesTool(detail.Graph, toolNames, detectBinary) {
			refs = append(refs, toolboxWorkflowRef{ID: summary.ID, Name: summary.Name})
		}
	}
	return refs
}

func workflowGraphUsesTool(graph workflow.Graph, toolNames map[string]bool, detectBinary string) bool {
	for _, node := range graph.Nodes {
		if node.Disabled {
			continue
		}
		switch node.Type {
		case "tool":
			if name, ok := node.Params["name"].(string); ok && toolNames[name] {
				return true
			}
		case "browser":
			if detectBinary == "obscura" {
				return true
			}
		}
	}
	return false
}

// writeToolboxIssues writes the 400 invalid_manifest of API-TB-002, with the
// issues under details[0].issues.
func writeToolboxIssues(w http.ResponseWriter, issues []toolbox.Issue) {
	rows := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		rows = append(rows, map[string]any{"field": issue.Field, "message": issue.Message})
	}
	writeAPIErrorDetails(w, http.StatusBadRequest, "invalid_manifest", "the manifest is not valid", []map[string]any{{"issues": rows}})
}

// writeToolboxError maps a toolbox service error to the contract error (design
// section "Web API"). An unknown error is a 500.
func (s *Server) writeToolboxError(w http.ResponseWriter, operation string, err error) {
	var invalid *toolbox.InvalidManifestError
	switch {
	case errors.Is(err, toolbox.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "package_not_found", "package not found")
	case errors.Is(err, toolbox.ErrExists):
		writeAPIError(w, http.StatusConflict, "package_exists", "package already exists")
	case errors.Is(err, toolbox.ErrBusy):
		writeAPIError(w, http.StatusConflict, "package_busy", "a job is already running for this package")
	case errors.Is(err, toolbox.ErrInstalled):
		writeAPIError(w, http.StatusConflict, "package_installed", "package is installed")
	case errors.Is(err, toolbox.ErrNotInstalled):
		writeAPIError(w, http.StatusConflict, "not_installed", "package is not installed")
	case errors.Is(err, toolbox.ErrMethodMismatch):
		writeAPIError(w, http.StatusBadRequest, "method_mismatch", "the install method does not match this system")
	case errors.As(err, &invalid):
		writeToolboxIssues(w, invalid.Issues)
	default:
		s.logger.Error("web: "+operation, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// toolboxManagedMCPFile names the file, under <LANA_HOME>/tools, that records
// the MCP servers lana created for installed packages (REQ-TB-011).
const toolboxManagedMCPFile = ".managed-mcp.json"

// applyToolboxMCP keeps the managed MCP servers in config.toml in step with the
// installed packages (REQ-TB-011). It never adds an entry: a server is added
// only by the owner's actions (the agent switch or a successful install). It
// re-renders a recorded [mcp.<id>] when the package's command or args changed,
// and a recorded name that is no longer provided (the package was uninstalled)
// is dropped from every agent's mcp list and then removed from config. A server
// lana did not create is never touched. On any failure the function logs and
// returns; the next toolbox event retries.
func (s *Server) applyToolboxMCP(ctx context.Context, backend ToolboxBackend, store ConfigStore) {
	if backend == nil || store == nil {
		return
	}
	servers := backend.MCPServers()
	desired := make(map[string]toolbox.MCPServer, len(servers))
	for _, srv := range servers {
		if srv.Package != "" {
			desired[srv.Package] = srv
		}
	}

	path := s.toolboxManagedMCPPath(store)

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	// The managed record is read and written under the same lock the add paths
	// take, so an in-flight add is never overwritten (REQ-TB-011).
	managed := s.readManagedMCP(path)
	sort.Strings(managed)

	file, err := store.ConfigFile(ctx)
	if err != nil {
		s.logger.Error("web: toolbox mcp: read config", "error", err)
		return
	}
	if !file.Exists {
		return
	}
	candidate := file.Raw
	changed := false
	kept := make([]string, 0, len(managed))

	for _, name := range managed {
		srv, ok := desired[name]
		if !ok {
			// The package that provided this server is gone: drop it from
			// every agent's mcp list first so each candidate stays valid, then
			// remove the server (REQ-TB-011).
			next, wrote, err := toolboxRemoveMCPFromAgents(ctx, store, candidate, name)
			if err != nil {
				s.logger.Error("web: toolbox mcp: drop server from agents", "package", name, "error", err)
				return
			}
			if wrote {
				candidate = next
				changed = true
			}
			next, wrote, err = toolboxRemoveMCPTable(ctx, store, candidate, name)
			if err != nil {
				s.logger.Error("web: toolbox mcp: remove server", "package", name, "error", err)
				return
			}
			if wrote {
				candidate = next
				changed = true
			}
			continue
		}
		// Re-render a recorded server whose command or args changed.
		next, wrote, err := toolboxEnsureMCPTable(ctx, store, candidate, srv)
		if err != nil {
			s.logger.Error("web: toolbox mcp: ensure server", "package", name, "error", err)
			return
		}
		if wrote {
			candidate = next
			changed = true
		}
		kept = append(kept, name)
	}

	if changed {
		if err := store.ConfigWrite(ctx, candidate); err != nil {
			s.logger.Error("web: toolbox mcp: write config", "error", err)
			return
		}
		s.applyLiveConfig(ctx, store, candidate)
	}
	if !slices.Equal(managed, kept) {
		if err := s.writeManagedMCP(path, kept); err != nil {
			s.logger.Error("web: toolbox mcp: write managed list", "error", err)
		}
	}
}

// toolboxManagedMCPPath is <LANA_HOME>/tools/.managed-mcp.json, the managed-MCP
// record of REQ-TB-011.
func (s *Server) toolboxManagedMCPPath(store ConfigStore) string {
	home := s.cfg.Home
	if settings, ok := store.(interface{ SettingsHome() string }); ok {
		if dir := settings.SettingsHome(); dir != "" {
			home = dir
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, "tools", toolboxManagedMCPFile)
}

// readManagedMCP returns the names recorded as managed. A missing or unreadable
// file reads as no names, so a first run never removes anything.
func (s *Server) readManagedMCP(path string) []string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		s.logger.Warn("web: toolbox mcp: managed record is not readable", "path", path, "error", err)
		return nil
	}
	return doc.Names
}

// writeManagedMCP records the managed names (REQ-TB-011).
func (s *Server) writeManagedMCP(path string, names []string) error {
	if path == "" {
		return nil
	}
	if names == nil {
		names = []string{}
	}
	data, err := json.MarshalIndent(struct {
		Names []string `json:"names"`
	}{Names: names}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return webfiles.AtomicWrite(path, data)
}

// toolboxEnsureMCPTable adds or updates [mcp.<package>] in data when it is
// missing or different from srv. It reuses the MCP rewrite helpers, so no TOML
// editing logic is duplicated (REQ-TB-011).
func toolboxEnsureMCPTable(ctx context.Context, store ConfigStore, data []byte, srv toolbox.MCPServer) ([]byte, bool, error) {
	def := mcpconfig.Definition{Type: "command", Command: srv.Command, Args: srv.Args, Env: srv.Env}
	mcpconfig.Normalize(&def)
	document, err := mcpconfig.Parse(data)
	if err != nil {
		return data, false, err
	}
	if toolboxMCPEqual(mcpconfig.FromMap(document.MCP[srv.Package]), def) {
		return data, false, nil
	}
	table := mcpconfig.Render(srv.Package, def)
	next, _, err := webfiles.RewriteTable(ctx, mcpChecker(store), data, "[mcp."+srv.Package+"]", table)
	if err != nil {
		return data, false, err
	}
	return next, true, nil
}

// toolboxRemoveMCPTable removes the [mcp.<name>] table when it is present.
func toolboxRemoveMCPTable(ctx context.Context, store ConfigStore, data []byte, name string) ([]byte, bool, error) {
	document, err := mcpconfig.Parse(data)
	if err != nil {
		return data, false, err
	}
	if _, ok := document.MCP[name]; !ok {
		return data, false, nil
	}
	next, _, err := webfiles.RewriteTable(ctx, mcpChecker(store), data, "[mcp."+name+"]", "")
	if err != nil {
		return data, false, err
	}
	return next, true, nil
}

// toolboxRemoveMCPFromAgents drops name from the mcp list of every agent that
// names it, through the same agent rewrite path PUT /v1/agents/{name} uses
// (REQ-TB-011).
func toolboxRemoveMCPFromAgents(ctx context.Context, store ConfigStore, data []byte, name string) ([]byte, bool, error) {
	tables, err := parseAgentTables(data)
	if err != nil {
		return data, false, err
	}
	agents := make([]string, 0, len(tables))
	for agent := range tables {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	changed := false
	for _, agent := range agents {
		record := agentRecordFromMap(tables[agent])
		if !slices.Contains(record.MCP, name) {
			continue
		}
		record.MCP = setStringList(record.MCP, name, false)
		next, _, err := webfiles.RewriteTable(ctx, agentChecker(store), data, "[agents."+agent+"]", renderAgentTable(agent, record))
		if err != nil {
			return data, false, err
		}
		data = next
		changed = true
	}
	return data, changed, nil
}

// toolboxMCPEqual reports whether a stored definition already matches a wanted
// one (REQ-TB-011).
func toolboxMCPEqual(stored, want mcpconfig.Definition) bool {
	return stored.Type == want.Type &&
		stored.Command == want.Command &&
		stored.URL == want.URL &&
		slices.Equal(stored.Args, want.Args) &&
		maps.Equal(stored.Env, want.Env) &&
		maps.Equal(stored.Headers, want.Headers)
}
