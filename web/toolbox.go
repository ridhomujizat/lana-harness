package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/toolbox"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

const (
	// toolboxBodyLimit is the largest accepted /v1/toolbox body (API-TB-002).
	toolboxBodyLimit = 1 << 20
	// toolboxJobHistory is how many jobs the detail view keeps (API-TB-003).
	toolboxJobHistory = 10
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
}

// SetToolbox installs the Tools port. Call it before Start; a nil port makes
// the /v1/toolbox routes answer 503 toolbox_unavailable.
func (s *Server) SetToolbox(b ToolboxBackend) {
	s.toolboxMu.Lock()
	defer s.toolboxMu.Unlock()
	s.toolbox = b
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

// registerToolboxRoutes registers API-TB-001 through API-TB-009 (spec #34).
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

// handleToolboxInstall serves API-TB-006 (REQ-TB-003). The job runs in the
// background, so the answer is 202 with the running job.
func (s *Server) handleToolboxInstall(w http.ResponseWriter, r *http.Request) {
	backend := s.toolboxOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Method int `json:"method"`
	}
	if !decodeJSONObject(w, r, toolboxBodyLimit, &body) {
		return
	}
	job, err := backend.Install(r.Context(), r.PathValue("id"), body.Method)
	if err != nil {
		s.writeToolboxError(w, "install package", err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
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

// toolboxDetailView is the PackageView plus the manifest text, the last jobs
// and the used_by lists (API-TB-003).
type toolboxDetailView struct {
	toolbox.PackageView
	Manifest string        `json:"manifest"`
	Jobs     []toolbox.Job `json:"jobs"`
	UsedBy   toolboxUsedBy `json:"used_by"`
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
	}
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
