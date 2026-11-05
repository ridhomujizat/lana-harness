package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
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
	listErr  error

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
	logText string
	logDone bool
	logErr  error

	runResult toolbox.RunResult
	runErr    error

	changes    chan string
	unsubCount int
}

func newFakeToolboxBackend() *fakeToolboxBackend {
	return &fakeToolboxBackend{changes: make(chan string, 8)}
}

func (f *fakeToolboxBackend) Subscribe() (<-chan string, func()) {
	return f.changes, func() {
		f.mu.Lock()
		f.unsubCount++
		f.mu.Unlock()
	}
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

func (f *fakeToolboxBackend) Jobs(_ string) []toolbox.Job {
	f.mu.Lock()
	defer f.mu.Unlock()
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

	backend.changes <- "obscura"
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
