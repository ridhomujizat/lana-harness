package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/toolbox"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// fakeToolboxBackend is an in-memory ToolboxBackend for the handler tests.
// Every method returns the configured value or error.
type fakeToolboxBackend struct {
	mu sync.Mutex

	packages []toolbox.Package

	getPackage toolbox.Package
	getErr     error

	addPackage toolbox.Package
	addIssues  []toolbox.Issue
	addErr     error
	addText    string

	updatePackage  toolbox.Package
	updateIssues   []toolbox.Issue
	updateErr      error
	updateManifest string

	deleteErr       error
	deleteID        string
	deleteUninstall bool

	installJob    toolbox.Job
	installErr    error
	installMethod int

	uninstallJob toolbox.Job
	uninstallErr error

	checkPackage toolbox.Package
	checkErr     error

	jobs    []toolbox.Job
	jobsFn  func(id string) []toolbox.Job
	logText string
	logDone bool
	logErr  error

	runResult toolbox.RunResult
	runErr    error

	subs       []chan string
	unsubCount int

	mcpServers []toolbox.MCPServer
}

func newFakeToolboxBackend() *fakeToolboxBackend {
	return &fakeToolboxBackend{}
}

func (f *fakeToolboxBackend) Subscribe() (<-chan string, func()) {
	f.mu.Lock()
	ch := make(chan string, 8)
	f.subs = append(f.subs, ch)
	f.mu.Unlock()
	return ch, func() {
		f.mu.Lock()
		f.unsubCount++
		f.mu.Unlock()
	}
}

// broadcast delivers a changed package id to every subscriber, like the real
// toolbox service does (REQ-TB-002).
func (f *fakeToolboxBackend) broadcast(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.subs {
		select {
		case ch <- id:
		default:
		}
	}
}

func (f *fakeToolboxBackend) MCPServers() []toolbox.MCPServer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mcpServers
}

func (f *fakeToolboxBackend) List(context.Context) []toolbox.Package {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.packages
}

func (f *fakeToolboxBackend) Get(_ context.Context, _ string) (toolbox.Package, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getPackage, f.getErr
}

func (f *fakeToolboxBackend) Add(_ context.Context, manifestText string) (toolbox.Package, []toolbox.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addText = manifestText
	return f.addPackage, f.addIssues, f.addErr
}

func (f *fakeToolboxBackend) Update(_ context.Context, _, manifestText string) (toolbox.Package, []toolbox.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateManifest = manifestText
	return f.updatePackage, f.updateIssues, f.updateErr
}

func (f *fakeToolboxBackend) Delete(_ context.Context, id string, uninstall bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteID = id
	f.deleteUninstall = uninstall
	return f.deleteErr
}

func (f *fakeToolboxBackend) Install(_ context.Context, _ string, method int) (toolbox.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installMethod = method
	return f.installJob, f.installErr
}

func (f *fakeToolboxBackend) Uninstall(_ context.Context, _ string) (toolbox.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.uninstallJob, f.uninstallErr
}

func (f *fakeToolboxBackend) Check(_ context.Context, _ string) (toolbox.Package, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkPackage, f.checkErr
}

func (f *fakeToolboxBackend) Jobs(id string) []toolbox.Job {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.jobsFn != nil {
		return f.jobsFn(id)
	}
	return f.jobs
}

func (f *fakeToolboxBackend) Log(_, _ string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logText, f.logDone, f.logErr
}

func (f *fakeToolboxBackend) Run(_ context.Context, _, _ string, _ map[string]any, _ string) (toolbox.RunResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runResult, f.runErr
}

// toolboxTestPackage builds a valid obscura-like package for the tests.
func toolboxTestPackage(id, name string) toolbox.Package {
	return toolbox.Package{
		Manifest: toolbox.Manifest{
			ID: id, Name: name, Description: "desc", Homepage: "https://example.test",
			Detect:   toolbox.Detect{Binary: id},
			Install:  []toolbox.InstallMethod{{Label: "Homebrew", Command: "brew install " + id}},
			Commands: []toolbox.Command{{Name: "fetch", Description: "Fetch", Risk: "read"}},
		},
		Status:    toolbox.Status{State: toolbox.StateInstalled, Version: "1.2.3", BinaryPath: "/opt/" + id},
		UpdatedAt: 1700000000000,
	}
}

// newToolboxTestServer logs in an owner and installs the toolbox port.
func newToolboxTestServer(t *testing.T, backend ToolboxBackend) (*Server, *http.Cookie, string) {
	t.Helper()
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	if backend != nil {
		s.SetToolbox(backend)
	}
	return s, cookie, csrf
}

// API-TB-001, REQ-TB-001: the list answers 200 with the package views.
func TestToolboxList(t *testing.T) {
	backend := newFakeToolboxBackend()
	backend.packages = []toolbox.Package{toolboxTestPackage("obscura", "Obscura")}
	s, cookie, _ := newToolboxTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/toolbox", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got struct {
		Packages []toolbox.PackageView `json:"packages"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(got.Packages) != 1 || got.Packages[0].ID != "obscura" || got.Packages[0].Status != toolbox.StateInstalled {
		t.Fatalf("packages = %+v", got.Packages)
	}
	if len(got.Packages[0].Commands) != 1 || got.Packages[0].Commands[0].Tool != "obscura_fetch" {
		t.Fatalf("commands = %+v", got.Packages[0].Commands)
	}
}

// API-TB-002, REQ-TB-001: a valid manifest answers 201 with the package view.
func TestToolboxAdd(t *testing.T) {
	backend := newFakeToolboxBackend()
	backend.addPackage = toolboxTestPackage("obscura", "Obscura")
	s, cookie, csrf := newToolboxTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox", `{"manifest":"id = \"obscura\"\n"}`, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var view toolbox.PackageView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if view.ID != "obscura" {
		t.Fatalf("view = %+v", view)
	}
	backend.mu.Lock()
	text := backend.addText
	backend.mu.Unlock()
	if text == "" {
		t.Fatal("the manifest was not passed to Add")
	}
}

// API-TB-002: an invalid manifest is 400 invalid_manifest with the issues.
func TestToolboxAddInvalidManifest(t *testing.T) {
	backend := newFakeToolboxBackend()
	backend.addIssues = []toolbox.Issue{{Field: "id", Message: "id is required"}}
	s, cookie, csrf := newToolboxTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox", `{"manifest":"x"}`, cookie, csrf))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if env.Error.Code != "invalid_manifest" {
		t.Fatalf("code = %q", env.Error.Code)
	}
	if len(env.Error.Details) != 1 {
		t.Fatalf("details = %+v", env.Error.Details)
	}
	issues, ok := env.Error.Details[0]["issues"].([]any)
	if !ok || len(issues) != 1 {
		t.Fatalf("issues = %+v", env.Error.Details[0]["issues"])
	}
	first, _ := issues[0].(map[string]any)
	if first["field"] != "id" || first["message"] != "id is required" {
		t.Fatalf("issue = %+v", issues[0])
	}
}

// API-TB-002: a taken id is 409 package_exists.
func TestToolboxAddExists(t *testing.T) {
	backend := newFakeToolboxBackend()
	backend.addErr = toolbox.ErrExists
	s, cookie, csrf := newToolboxTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox", `{"manifest":"id = \"obscura\"\n"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "package_exists")
}

// API-TB-003, REQ-TB-005, REQ-TB-006: the detail carries the manifest text, the
// last jobs and the agents and workflows that use the package.
func TestToolboxGetDetail(t *testing.T) {
	backend := newFakeToolboxBackend()
	backend.getPackage = toolboxTestPackage("obscura", "Obscura")
	backend.jobs = make([]toolbox.Job, 12)
	for i := range backend.jobs {
		backend.jobs[i] = toolbox.Job{
			ID: fmt.Sprintf("tool-%02d", i), Package: "obscura",
			Kind: toolbox.JobKindInstall, Status: toolbox.JobSucceeded,
		}
	}
	s, cookie, _ := newToolboxTestServer(t, backend)
	s.SetConfigStore(newTestConfigStore(t, "[agents.helper]\ntools = [\"obscura_fetch\"]\n", 0o600, ConfigSettings{}))

	wf := newFakeWorkflowBackend()
	wf.overview = workflow.Overview{Workflows: []workflow.Summary{{ID: "wf1", Name: "Riset"}}}
	wf.getDetail = workflow.Detail{Graph: workflow.Graph{Nodes: []workflow.Node{
		{ID: "n1", Type: "tool", Params: map[string]any{"name": "obscura_fetch"}},
	}}}
	s.SetWorkflows(wf)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/toolbox/obscura", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got struct {
		ID       string        `json:"id"`
		Manifest string        `json:"manifest"`
		Jobs     []toolbox.Job `json:"jobs"`
		UsedBy   struct {
			Agents    []string `json:"agents"`
			Workflows []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"workflows"`
		} `json:"used_by"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if got.ID != "obscura" {
		t.Fatalf("id = %q", got.ID)
	}
	if !strings.Contains(got.Manifest, "obscura") {
		t.Fatalf("manifest = %q", got.Manifest)
	}
	if len(got.Jobs) != 10 || got.Jobs[0].ID != "tool-00" {
		t.Fatalf("jobs = %+v", got.Jobs)
	}
	if len(got.UsedBy.Agents) != 1 || got.UsedBy.Agents[0] != "helper" {
		t.Fatalf("used_by agents = %v", got.UsedBy.Agents)
	}
	if len(got.UsedBy.Workflows) != 1 || got.UsedBy.Workflows[0].ID != "wf1" || got.UsedBy.Workflows[0].Name != "Riset" {
		t.Fatalf("used_by workflows = %+v", got.UsedBy.Workflows)
	}
}

// REQ-TB-006: a browser node uses a package whose detect binary is obscura.
func TestWorkflowGraphUsesTool(t *testing.T) {
	browser := workflow.Graph{Nodes: []workflow.Node{{ID: "b", Type: "browser"}}}
	if !workflowGraphUsesTool(browser, map[string]bool{}, "obscura") {
		t.Fatal("a browser node did not match the obscura binary")
	}
	if workflowGraphUsesTool(browser, map[string]bool{}, "chromium") {
		t.Fatal("a browser node matched a different binary")
	}
	tool := workflow.Graph{Nodes: []workflow.Node{{ID: "t", Type: "tool", Params: map[string]any{"name": "obscura_fetch"}}}}
	if !workflowGraphUsesTool(tool, map[string]bool{"obscura_fetch": true}, "") {
		t.Fatal("a tool node did not match its tool name")
	}
	if workflowGraphUsesTool(tool, map[string]bool{"other": true}, "") {
		t.Fatal("a tool node matched a different tool name")
	}
}

// API-TB-005, REQ-TB-008: an installed package is 409 unless ?uninstall=1.
func TestToolboxDelete(t *testing.T) {
	backend := newFakeToolboxBackend()
	backend.deleteErr = toolbox.ErrInstalled
	s, cookie, csrf := newToolboxTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodDelete, "/v1/toolbox/obscura", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "package_installed")

	backend.deleteErr = nil
	rr = serveAuth(s, sessionTestRequest(http.MethodDelete, "/v1/toolbox/obscura?uninstall=1", "", cookie, csrf))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	backend.mu.Lock()
	uninstall := backend.deleteUninstall
	backend.mu.Unlock()
	if !uninstall {
		t.Fatal("uninstall=1 was not passed to Delete")
	}
}

// API-TB-006, REQ-TB-003: install answers 202 with the running job; a busy
// package is 409 package_busy.
func TestToolboxInstall(t *testing.T) {
	backend := newFakeToolboxBackend()
	backend.installJob = toolbox.Job{ID: "tool-1", Package: "obscura", Kind: toolbox.JobKindInstall, Method: 1, Status: toolbox.JobRunning}
	s, cookie, csrf := newToolboxTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/install", `{"method":1}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var job toolbox.Job
	if err := json.Unmarshal(rr.Body.Bytes(), &job); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if job.ID != "tool-1" || job.Status != toolbox.JobRunning {
		t.Fatalf("job = %+v", job)
	}
	backend.mu.Lock()
	method := backend.installMethod
	backend.mu.Unlock()
	if method != 1 {
		t.Fatalf("method = %d", method)
	}

	backend.installErr = toolbox.ErrBusy
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/install", `{"method":1}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "package_busy")
}

// REQ-TB-011: a successful install job adds the managed MCP server of the
// package. The job is watched in the background; nothing is added while it runs.
func TestToolboxInstallRegistersMCP(t *testing.T) {
	store := newTestConfigStore(t, toolboxSyncMCPConfig, 0o600, ConfigSettings{})
	backend := newFakeToolboxBackend()
	backend.installJob = toolbox.Job{ID: "tool-1", Package: "obscura", Kind: toolbox.JobKindInstall, Status: toolbox.JobRunning}
	backend.mcpServers = []toolbox.MCPServer{{Package: "obscura", Command: "/opt/obscura", Args: []string{"mcp"}}}
	var status atomic.Value
	status.Store(toolbox.JobRunning)
	backend.jobsFn = func(string) []toolbox.Job {
		return []toolbox.Job{{
			ID: "tool-1", Package: "obscura", Kind: toolbox.JobKindInstall,
			Status: status.Load().(string),
		}}
	}
	s, cookie, csrf := newToolboxTestServer(t, backend)
	s.SetConfigStore(store)
	s.SetToolboxJobPollInterval(5 * time.Millisecond)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/install", `{"method":1}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if raw := readToolboxConfig(t, store); strings.Contains(raw, "[mcp.obscura]") {
		t.Fatalf("the server was added while the job still runs: %q", raw)
	}

	// The job succeeds: the watcher registers the server and records it.
	status.Store(toolbox.JobSucceeded)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		names := managedMCPNames(s, store)
		if len(names) == 1 && names[0] == "obscura" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	raw := readToolboxConfig(t, store)
	if !strings.Contains(raw, "[mcp.obscura]") || !strings.Contains(raw, "/opt/obscura") {
		t.Fatalf("the successful install did not register the server: %q", raw)
	}
	if managed := readManagedMCPNames(t, s, store); len(managed) != 1 || managed[0] != "obscura" {
		t.Fatalf("managed = %v", managed)
	}
}

// managedMCPNames reads the managed-MCP record, or nil when it is absent.
func managedMCPNames(s *Server, store *testConfigStore) []string {
	data, err := os.ReadFile(s.toolboxManagedMCPPath(store))
	if err != nil {
		return nil
	}
	var doc struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	return doc.Names
}

// API-TB-009: the job log answer carries the text and the done flag.
func TestToolboxJobLog(t *testing.T) {
	backend := newFakeToolboxBackend()
	backend.logText = "downloading\n"
	backend.logDone = true
	s, cookie, _ := newToolboxTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/toolbox/obscura/jobs/tool-1/log", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got struct {
		Text string `json:"text"`
		Done bool   `json:"done"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if got.Text != "downloading\n" || !got.Done {
		t.Fatalf("log = %+v", got)
	}
}

// API-TB-009, REQ-TB-004: run answers the output object; a missing package is
// 409 not_installed.
func TestToolboxRun(t *testing.T) {
	backend := newFakeToolboxBackend()
	backend.runResult = toolbox.RunResult{Output: "page text", ExitCode: 0, DurationMs: 42}
	s, cookie, csrf := newToolboxTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/run", `{"command":"fetch","input":{"url":"https://x"}}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got toolbox.RunResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if got.Output != "page text" || got.DurationMs != 42 {
		t.Fatalf("run = %+v", got)
	}

	backend.runErr = toolbox.ErrNotInstalled
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/run", `{"command":"fetch"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "not_installed")
}

// API-TB-009, REQ-TB-002: the stream emits a changed event per package.
func TestToolboxStreamEmitsChanged(t *testing.T) {
	backend := newFakeToolboxBackend()
	s, ts, cookie, _ := newStreamTestServer(t, newFakeWebBackend())
	s.SetToolbox(backend)
	s.SetHeartbeatInterval(20 * time.Millisecond)

	resp := openStream(t, ts, "/v1/toolbox/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)

	backend.broadcast("obscura")
	block := waitSSE(t, reader, "changed")
	var change struct {
		Package string `json:"package"`
	}
	if err := json.Unmarshal([]byte(block.Data), &change); err != nil {
		t.Fatalf("data %q: %v", block.Data, err)
	}
	if change.Package != "obscura" {
		t.Fatalf("change = %+v", change)
	}
}

// The toolbox routes answer 503 when no port is installed.
func TestToolboxUnavailable(t *testing.T) {
	s, cookie, _ := newLoggedInSessionServer(t, newFakeWebBackend())
	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/toolbox", "", cookie, ""))
	assertAuthError(t, rr, http.StatusServiceUnavailable, "toolbox_unavailable")
}

// *toolbox.Service satisfies the ToolboxBackend port the web handlers use
// (spec #34, design section "Web API").
var _ ToolboxBackend = (*toolbox.Service)(nil)

// toolboxTestMCPPackage builds an obscura-like package that provides an MCP
// server (REQ-TB-011).
func toolboxTestMCPPackage(id, name string) toolbox.Package {
	pkg := toolboxTestPackage(id, name)
	pkg.Manifest.MCP = &toolbox.MCPSpec{Args: []string{"--stealth", "mcp"}}
	return pkg
}

func readToolboxConfig(t *testing.T, store *testConfigStore) string {
	t.Helper()
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readManagedMCPNames(t *testing.T, s *Server, store *testConfigStore) []string {
	t.Helper()
	data, err := os.ReadFile(s.toolboxManagedMCPPath(store))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("managed record %q: %v", data, err)
	}
	return doc.Names
}

func writeManagedMCPNames(t *testing.T, s *Server, store *testConfigStore, names []string) {
	t.Helper()
	if err := s.writeManagedMCP(s.toolboxManagedMCPPath(store), names); err != nil {
		t.Fatal(err)
	}
}

// The sync configs are small valid files. The removal one already holds the
// managed server and an agent that names it (REQ-TB-011).
const (
	toolboxSyncMCPConfig = `version = 1

[agents.helper]
model = "helper"
`

	toolboxSyncMCPRemovalConfig = `version = 1

[mcp.obscura]
command = "/old/obscura"
args = ["mcp"]

[agents.helper]
model = "helper"
mcp = ["obscura"]
`

	toolboxSyncMCPUnmanagedConfig = `version = 1

[mcp.other]
command = "/usr/bin/other"

[agents.helper]
model = "helper"
`
)

// REQ-TB-011: the background sync never adds a server on its own. An installed
// MCP package with an empty managed list leaves config.toml untouched.
func TestToolboxSyncMCPStartupAddsNothing(t *testing.T) {
	store := newTestConfigStore(t, toolboxSyncMCPConfig, 0o600, ConfigSettings{})
	backend := newFakeToolboxBackend()
	backend.mcpServers = []toolbox.MCPServer{{
		Package: "obscura",
		Command: "/opt/obscura/bin/obscura",
		Args:    []string{"--stealth", "mcp"},
	}}
	s, _, _ := newToolboxTestServer(t, backend)
	s.SetConfigStore(store)

	before := readToolboxConfig(t, store)
	s.syncToolboxMCP(context.Background())

	if after := readToolboxConfig(t, store); after != before {
		t.Fatalf("the sync added a server on its own:\n%s", after)
	}
	if _, err := os.Stat(s.toolboxManagedMCPPath(store)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the sync created a managed record: %v", err)
	}
}

// REQ-TB-011: the sync re-renders a managed server whose command and args
// changed, and removes it from config and from every agent's mcp list once the
// package stops providing it (uninstalled).
func TestToolboxSyncMCPUpdatesAndRemovesManaged(t *testing.T) {
	store := newTestConfigStore(t, toolboxSyncMCPRemovalConfig, 0o600, ConfigSettings{})
	backend := newFakeToolboxBackend()
	backend.mcpServers = []toolbox.MCPServer{{
		Package: "obscura",
		Command: "/new/obscura",
		Args:    []string{"--stealth", "mcp"},
	}}
	s, _, _ := newToolboxTestServer(t, backend)
	s.SetConfigStore(store)
	writeManagedMCPNames(t, s, store, []string{"obscura"})

	s.syncToolboxMCP(context.Background())

	raw := readToolboxConfig(t, store)
	if !strings.Contains(raw, "/new/obscura") || !strings.Contains(raw, "--stealth") {
		t.Fatalf("the managed server was not re-rendered: %q", raw)
	}
	if managed := readManagedMCPNames(t, s, store); len(managed) != 1 || managed[0] != "obscura" {
		t.Fatalf("managed = %v", managed)
	}

	// The package no longer provides an MCP server: it is dropped.
	backend.mu.Lock()
	backend.mcpServers = nil
	backend.mu.Unlock()
	s.syncToolboxMCP(context.Background())

	raw = readToolboxConfig(t, store)
	if strings.Contains(raw, "[mcp.obscura]") {
		t.Fatalf("config still has [mcp.obscura]: %q", raw)
	}
	tables, err := parseAgentTables([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if record := agentRecordFromMap(tables["helper"]); len(record.MCP) != 0 {
		t.Fatalf("agent mcp = %v", record.MCP)
	}
	if managed := readManagedMCPNames(t, s, store); len(managed) != 0 {
		t.Fatalf("managed = %v", managed)
	}
}

// REQ-TB-011: an MCP server lana did not create is never touched, and the sync
// does not add one of its own for an installed package.
func TestToolboxSyncMCPKeepsUnmanaged(t *testing.T) {
	store := newTestConfigStore(t, toolboxSyncMCPUnmanagedConfig, 0o600, ConfigSettings{})
	backend := newFakeToolboxBackend()
	backend.mcpServers = []toolbox.MCPServer{{Package: "obscura", Command: "/opt/obscura", Args: []string{"mcp"}}}
	s, _, _ := newToolboxTestServer(t, backend)
	s.SetConfigStore(store)

	s.syncToolboxMCP(context.Background())

	raw := readToolboxConfig(t, store)
	if !strings.Contains(raw, "[mcp.other]") || !strings.Contains(raw, `command = "/usr/bin/other"`) {
		t.Fatalf("the unmanaged server was touched: %q", raw)
	}
	if strings.Contains(raw, "[mcp.obscura]") {
		t.Fatalf("the sync added a server on its own: %q", raw)
	}
}

// REQ-TB-011: the detail lists every configured agent with its enabled flag,
// from the mcp list for an MCP package and from the tools list otherwise.
func TestToolboxDetailAgents(t *testing.T) {
	mcpConfig := `version = 1

[mcp.obscura]
command = "/opt/obscura"

[agents.a]
model = "m"
mcp = ["obscura"]

[agents.b]
model = "m"
`
	backend := newFakeToolboxBackend()
	backend.getPackage = toolboxTestMCPPackage("obscura", "Obscura")
	s, cookie, _ := newToolboxTestServer(t, backend)
	s.SetConfigStore(newTestConfigStore(t, mcpConfig, 0o600, ConfigSettings{}))

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/toolbox/obscura", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got struct {
		Agents []toolboxAgentUse `json:"agents"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(got.Agents) != 2 || got.Agents[0].Name != "a" || !got.Agents[0].Enabled || got.Agents[1].Name != "b" || got.Agents[1].Enabled {
		t.Fatalf("agents = %+v", got.Agents)
	}

	// A package without an MCP spec is enabled through the agent's tools list.
	commandConfig := `version = 1

[agents.a]
model = "m"
tools = ["obscura_fetch"]

[agents.b]
model = "m"
`
	backend.getPackage = toolboxTestPackage("obscura", "Obscura")
	s.SetConfigStore(newTestConfigStore(t, commandConfig, 0o600, ConfigSettings{}))
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/toolbox/obscura", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	got.Agents = nil
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(got.Agents) != 2 || !got.Agents[0].Enabled || got.Agents[1].Enabled {
		t.Fatalf("agents = %+v", got.Agents)
	}
}

// API-TB-010, REQ-TB-011: "Use with agents" toggles the package id in the
// agent's mcp list for an MCP package and answers the refreshed detail.
func TestToolboxSetAgentMCP(t *testing.T) {
	store := newTestConfigStore(t, toolboxSyncMCPConfig, 0o600, ConfigSettings{})
	backend := newFakeToolboxBackend()
	backend.getPackage = toolboxTestMCPPackage("obscura", "Obscura")
	backend.mcpServers = []toolbox.MCPServer{{Package: "obscura", Command: "/opt/obscura", Args: []string{"mcp"}}}
	s, cookie, csrf := newToolboxTestServer(t, backend)
	s.SetConfigStore(store)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/agents", `{"agent":"helper","enabled":true}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got struct {
		Agents []toolboxAgentUse `json:"agents"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(got.Agents) != 1 || !got.Agents[0].Enabled {
		t.Fatalf("agents = %+v", got.Agents)
	}
	raw := readToolboxConfig(t, store)
	if !strings.Contains(raw, "[mcp.obscura]") {
		t.Fatalf("config has no [mcp.obscura]: %q", raw)
	}
	tables, err := parseAgentTables([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if record := agentRecordFromMap(tables["helper"]); len(record.MCP) != 1 || record.MCP[0] != "obscura" {
		t.Fatalf("agent mcp = %v", record.MCP)
	}
	if managed := readManagedMCPNames(t, s, store); len(managed) != 1 || managed[0] != "obscura" {
		t.Fatalf("managed = %v", managed)
	}

	// Turning it off drops the id again, and leaves the server table alone.
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/agents", `{"agent":"helper","enabled":false}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(got.Agents) != 1 || got.Agents[0].Enabled {
		t.Fatalf("agents = %+v", got.Agents)
	}
	raw = readToolboxConfig(t, store)
	tables, err = parseAgentTables([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if record := agentRecordFromMap(tables["helper"]); len(record.MCP) != 0 {
		t.Fatalf("agent mcp = %v", record.MCP)
	}
}

// API-TB-010, REQ-TB-011: a package without an MCP spec toggles its command
// tools in the agent's tools list.
func TestToolboxSetAgentTools(t *testing.T) {
	store := newTestConfigStore(t, toolboxSyncMCPConfig, 0o600, ConfigSettings{})
	backend := newFakeToolboxBackend()
	backend.getPackage = toolboxTestPackage("obscura", "Obscura")
	s, cookie, csrf := newToolboxTestServer(t, backend)
	s.SetConfigStore(store)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/agents", `{"agent":"helper","enabled":true}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	tables, err := parseAgentTables([]byte(readToolboxConfig(t, store)))
	if err != nil {
		t.Fatal(err)
	}
	if record := agentRecordFromMap(tables["helper"]); len(record.Tools) != 1 || record.Tools[0] != "obscura_fetch" {
		t.Fatalf("agent tools = %v", record.Tools)
	}
	if record := agentRecordFromMap(tables["helper"]); len(record.MCP) != 0 {
		t.Fatalf("agent mcp = %v", record.MCP)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/agents", `{"agent":"helper","enabled":false}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	tables, err = parseAgentTables([]byte(readToolboxConfig(t, store)))
	if err != nil {
		t.Fatal(err)
	}
	if record := agentRecordFromMap(tables["helper"]); len(record.Tools) != 0 {
		t.Fatalf("agent tools = %v", record.Tools)
	}
}

// API-TB-010: an uninstalled package is 409 not_installed and an unknown agent
// is 404 agent_not_found.
func TestToolboxSetAgentErrors(t *testing.T) {
	store := newTestConfigStore(t, toolboxSyncMCPConfig, 0o600, ConfigSettings{})
	backend := newFakeToolboxBackend()
	pkg := toolboxTestMCPPackage("obscura", "Obscura")
	pkg.Status.State = toolbox.StateMissing
	backend.getPackage = pkg
	s, cookie, csrf := newToolboxTestServer(t, backend)
	s.SetConfigStore(store)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/agents", `{"agent":"helper","enabled":true}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "not_installed")

	backend.getPackage = toolboxTestMCPPackage("obscura", "Obscura")
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/agents", `{"agent":"nope","enabled":true}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "agent_not_found")
}

// API-TB-006, REQ-TB-009: a missing method and -1 both install with the method
// that fits this machine.
func TestToolboxInstallAutoMethod(t *testing.T) {
	backend := newFakeToolboxBackend()
	s, cookie, csrf := newToolboxTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/install", `{}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	backend.mu.Lock()
	method := backend.installMethod
	backend.mu.Unlock()
	if method != -1 {
		t.Fatalf("method = %d, want -1", method)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/toolbox/obscura/install", `{"method":0}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	backend.mu.Lock()
	method = backend.installMethod
	backend.mu.Unlock()
	if method != 0 {
		t.Fatalf("method = %d, want 0", method)
	}
}
