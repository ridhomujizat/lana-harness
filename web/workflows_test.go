package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// fakeWorkflowBackend is an in-memory WorkflowBackend for the handler tests.
// Every method returns the configured value or error.
type fakeWorkflowBackend struct {
	mu sync.Mutex

	overview    workflow.Overview
	overviewErr error

	createDetail workflow.Detail
	createErr    error
	createInput  workflow.CreateInput

	getDetail workflow.Detail
	getErr    error

	updateDetail workflow.Detail
	updateErr    error

	deleteErr error

	versions    []workflow.Version
	versionsErr error

	restoreDetail workflow.Detail
	restoreErr    error

	lockTools []workflow.LockTool
	lockErr   error

	activateDetail workflow.Detail
	activateErr    error

	pauseDetail workflow.Detail
	pauseErr    error

	resumeDetail workflow.Detail
	resumeErr    error

	run           workflow.Run
	runErr        error
	startRunInput json.RawMessage

	runs    []workflow.Run
	runsErr error

	runDetail    workflow.RunDetail
	runDetailErr error

	cancelRun workflow.Run
	cancelErr error

	answerRun workflow.Run
	answerErr error

	state    []workflow.StateEntry
	stateErr error

	setStateEntry workflow.StateEntry
	setStateErr   error

	deleteStateErr error

	chat    []workflow.ChatMessage
	chatErr error

	assistSession string
	assistNote    string
	noteCommits   int
	assistErr     error

	rotateToken string
	rotateErr   error

	webhookRun   workflow.Run
	webhookErr   error
	webhookID    string
	webhookToken string
	webhookBody  []byte

	command   workflow.CommandResult
	commandOK bool
	commandCh string
	commands  []string

	channels []workflow.MessageChannel

	workflowSessions map[string]bool

	changes    chan workflow.Change
	unsubCount int

	// Script Parse routes (spec #44).
	testScript         workflow.TestResult
	testScriptErr      error
	testScriptNode     workflow.Node
	testScriptInput    any
	testScriptHasInput bool
	testScriptRun      int

	generateScript         workflow.GenerateResult
	generateScriptErr      error
	generateScriptExpected map[string]any

	convertResult workflow.ConvertResult
	convertErr    error
	convertNodeID string

	suggestions    []workflow.Suggestion
	suggestionsErr error
}

func newFakeWorkflowBackend() *fakeWorkflowBackend {
	return &fakeWorkflowBackend{
		workflowSessions: map[string]bool{},
		changes:          make(chan workflow.Change, 8),
	}
}

func (f *fakeWorkflowBackend) Subscribe() (<-chan workflow.Change, func()) {
	return f.changes, func() {
		f.mu.Lock()
		f.unsubCount++
		f.mu.Unlock()
	}
}

func (f *fakeWorkflowBackend) Overview(_ context.Context) (workflow.Overview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.overview, f.overviewErr
}

func (f *fakeWorkflowBackend) Get(_ context.Context, _ string) (workflow.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getDetail, f.getErr
}

func (f *fakeWorkflowBackend) Create(_ context.Context, in workflow.CreateInput) (workflow.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createInput = in
	return f.createDetail, f.createErr
}

func (f *fakeWorkflowBackend) Update(_ context.Context, _ string, _ workflow.UpdateInput) (workflow.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.updateDetail, f.updateErr
}

func (f *fakeWorkflowBackend) Delete(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deleteErr
}

func (f *fakeWorkflowBackend) Versions(_ context.Context, _ string) ([]workflow.Version, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.versions, f.versionsErr
}

func (f *fakeWorkflowBackend) Restore(_ context.Context, _ string, _ int) (workflow.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restoreDetail, f.restoreErr
}

func (f *fakeWorkflowBackend) LockPreview(_ context.Context, _ string) ([]workflow.LockTool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lockTools, f.lockErr
}

func (f *fakeWorkflowBackend) Activate(_ context.Context, _ string, _ int) (workflow.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.activateDetail, f.activateErr
}

func (f *fakeWorkflowBackend) Pause(_ context.Context, _ string) (workflow.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pauseDetail, f.pauseErr
}

func (f *fakeWorkflowBackend) Resume(_ context.Context, _ string) (workflow.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resumeDetail, f.resumeErr
}

func (f *fakeWorkflowBackend) StartRun(_ context.Context, _, _ string, input json.RawMessage) (workflow.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startRunInput = append(json.RawMessage(nil), input...)
	return f.run, f.runErr
}

func (f *fakeWorkflowBackend) Runs(_ context.Context, _ string, _, _ int) ([]workflow.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs, f.runsErr
}

func (f *fakeWorkflowBackend) RunDetail(_ context.Context, _, _ string) (workflow.RunDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runDetail, f.runDetailErr
}

func (f *fakeWorkflowBackend) Cancel(_ context.Context, _, _ string) (workflow.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelRun, f.cancelErr
}

func (f *fakeWorkflowBackend) Answer(_ context.Context, _, _, _ string) (workflow.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answerRun, f.answerErr
}

func (f *fakeWorkflowBackend) State(_ context.Context, _ string) ([]workflow.StateEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, f.stateErr
}

func (f *fakeWorkflowBackend) SetState(_ context.Context, _, _ string, _ json.RawMessage) (workflow.StateEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setStateEntry, f.setStateErr
}

func (f *fakeWorkflowBackend) DeleteState(_ context.Context, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deleteStateErr
}

func (f *fakeWorkflowBackend) AssistantChat(_ context.Context, _ string) (string, []workflow.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.assistSession, f.chat, f.chatErr
}

func (f *fakeWorkflowBackend) EnsureAssistantSession(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.assistErr != nil {
		return "", f.assistErr
	}
	if f.assistSession == "" {
		f.assistSession = "wf-chat-1"
	}
	return f.assistSession, nil
}

func (f *fakeWorkflowBackend) NewAssistantSession(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.assistErr != nil {
		return "", f.assistErr
	}
	f.assistSession = "wf-chat-2"
	return f.assistSession, nil
}

func (f *fakeWorkflowBackend) AssistantNote(_ context.Context, _, _ string) (string, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.assistNote, func() {
		f.mu.Lock()
		f.noteCommits++
		f.mu.Unlock()
	}, nil
}

func (f *fakeWorkflowBackend) RotateWebhook(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rotateToken, f.rotateErr
}

func (f *fakeWorkflowBackend) Webhook(_ context.Context, id, token string, body []byte) (workflow.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.webhookID = id
	f.webhookToken = token
	f.webhookBody = append([]byte(nil), body...)
	return f.webhookRun, f.webhookErr
}

func (f *fakeWorkflowBackend) Command(_ context.Context, channel, text string) (workflow.CommandResult, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commandCh = channel
	f.commands = append(f.commands, text)
	return f.command, f.commandOK
}

func (f *fakeWorkflowBackend) IsWorkflowSession(sessionID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.workflowSessions[sessionID]
}

func (f *fakeWorkflowBackend) TestScript(_ context.Context, _ string, node workflow.Node, input any, hasInput bool, run int) (workflow.TestResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.testScriptNode = node
	f.testScriptInput = input
	f.testScriptHasInput = hasInput
	f.testScriptRun = run
	return f.testScript, f.testScriptErr
}

func (f *fakeWorkflowBackend) GenerateScript(_ context.Context, _ string, _ workflow.Node, _ any, _ bool, _ int, expected map[string]any) (workflow.GenerateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generateScriptExpected = expected
	return f.generateScript, f.generateScriptErr
}

func (f *fakeWorkflowBackend) ConvertNode(_ context.Context, _, nodeID string) (workflow.ConvertResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.convertNodeID = nodeID
	return f.convertResult, f.convertErr
}

func (f *fakeWorkflowBackend) ScriptSuggestions(_ context.Context, _ string) ([]workflow.Suggestion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.suggestions, f.suggestionsErr
}

func (f *fakeWorkflowBackend) MessageChannels() []workflow.MessageChannel {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.channels
}

// newWorkflowTestServer logs in an owner and installs the workflow port.
func newWorkflowTestServer(t *testing.T, wf WorkflowBackend) (*Server, *http.Cookie, string) {
	t.Helper()
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	if wf != nil {
		s.SetWorkflows(wf)
	}
	return s, cookie, csrf
}

// API-WF-001, REQ-WF-011: the attention list merges the open tool approvals of
// workflow sessions, newest first, and counts them in the stats.
func TestWorkflowListMergesToolApprovals(t *testing.T) {
	overview := workflow.Overview{
		Workflows: []workflow.Summary{{ID: "riset-harian", Name: "Riset harian"}},
		Stats:     workflow.Stats{Decisions: 1, WaitingOnYou: 1},
		Attention: []workflow.Attention{
			{Decision: workflow.Decision{ID: "d_1", Kind: "guardian", CreatedAt: 300}},
		},
	}
	approvals := []workflowApproval{
		{ID: "ap_1", SessionID: "wf-session", Tool: "bash", Argument: "/bin/rm -rf /tmp/x", Agent: "researcher", CreatedAt: 900},
		{ID: "ap_2", SessionID: "other-session", Tool: "webfetch", CreatedAt: 800},
	}
	mergeWorkflowApprovals(&overview, approvals,
		func(sessionID string) bool { return sessionID == "wf-session" },
		func(sessionID string) string {
			if sessionID == "wf-session" {
				return "riset-harian"
			}
			return ""
		})

	if len(overview.Attention) != 2 {
		t.Fatalf("attention = %+v", overview.Attention)
	}
	// Newest first: ap_1 (900) before the pending decision (300).
	if overview.Attention[0].ID != "ap_1" || overview.Attention[1].ID != "d_1" {
		t.Fatalf("attention order = %+v", overview.Attention)
	}
	row := overview.Attention[0]
	if row.Kind != "tool_approval" || row.Status != "pending" {
		t.Fatalf("row = %+v", row)
	}
	if row.Tool != "bash" || row.Argument != "/bin/rm -rf /tmp/x" {
		t.Fatalf("tool/argument = %q/%q", row.Tool, row.Argument)
	}
	if row.Reason != "researcher wants bash: /bin/rm -rf /tmp/x" {
		t.Fatalf("reason = %q", row.Reason)
	}
	if row.WorkflowID != "riset-harian" || row.WorkflowName != "Riset harian" {
		t.Fatalf("workflow = %q/%q", row.WorkflowID, row.WorkflowName)
	}
	if overview.Stats.WaitingOnYou != 2 || overview.Stats.Approvals != 1 || overview.Stats.Decisions != 1 {
		t.Fatalf("stats = %+v", overview.Stats)
	}
}

// API-WF-001, REQ-WF-011: the list endpoint merges the broker's open workflow
// approvals (AC-WF-019 server side).
func TestWorkflowListIncludesBrokerApproval(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.overview = workflow.Overview{Workflows: []workflow.Summary{{ID: "riset-harian", Name: "Riset harian"}}}
	backend.workflowSessions["wf-session"] = true

	sessions := newFakeWebBackend()
	sessions.addSession(core.SessionInfo{ID: "wf-session", Channel: "workflow", Address: "riset-harian", Agent: "researcher"})
	s, cookie, _ := newLoggedInSessionServer(t, sessions)
	s.SetWorkflows(backend)

	expires := int64(5_000_000) + approvalTimeout.Milliseconds()
	s.approvals.mu.Lock()
	s.approvals.open["ap_1"] = &pendingApproval{
		id:        "ap_1",
		sessionID: "wf-session",
		event:     approvalEvent{RequestID: "ap_1", Tool: "bash", Argument: "rm -rf /tmp/x", Agent: "researcher", ExpiresAt: expires},
	}
	s.approvals.mu.Unlock()

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/workflows", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got workflow.Overview
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(got.Attention) != 1 || got.Attention[0].Kind != "tool_approval" || got.Attention[0].ID != "ap_1" {
		t.Fatalf("attention = %+v", got.Attention)
	}
	if got.Attention[0].WorkflowID != "riset-harian" || got.Attention[0].WorkflowName != "Riset harian" {
		t.Fatalf("workflow = %q/%q", got.Attention[0].WorkflowID, got.Attention[0].WorkflowName)
	}
	if got.Stats.WaitingOnYou != 1 || got.Stats.Approvals != 1 {
		t.Fatalf("stats = %+v", got.Stats)
	}
}

// API-WF-002, REQ-WF-001: create answers 201 with the workflow.
func TestWorkflowCreate(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.createDetail = workflow.Detail{
		Workflow: workflow.Workflow{ID: "riset-harian", Name: "Riset harian", Guardian: "default", Status: workflow.StatusDraft, Version: 1},
	}
	s, cookie, csrf := newWorkflowTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows", `{"name":"Riset harian","guardian":"default"}`, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var detail workflow.Detail
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if detail.ID != "riset-harian" || detail.Status != workflow.StatusDraft {
		t.Fatalf("detail = %+v", detail)
	}
	if backend.createInput.Name != "Riset harian" || backend.createInput.Guardian != "default" {
		t.Fatalf("create input = %+v", backend.createInput)
	}
}

// API-WF-004, REQ-WF-009: a stale base_version is 409 version_conflict.
func TestWorkflowUpdateConflict(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.updateErr = workflow.ErrVersionConflict
	s, cookie, csrf := newWorkflowTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPut, "/v1/workflows/riset-harian", `{"base_version":1}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "version_conflict")
}

// API-WF-002, REQ-WF-002: an invalid graph is 400 with the issues, and a lone
// unknown_agent issue uses its own code.
func TestWorkflowInvalidGraph(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.createErr = &workflow.InvalidGraphError{Issues: []workflow.Issue{
		{NodeID: "a", Code: "cycle", Message: "cycle a -> a"},
	}}
	s, cookie, csrf := newWorkflowTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows", `{"name":"x","guardian":"default"}`, cookie, csrf))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if env.Error.Code != "invalid_graph" {
		t.Fatalf("code = %q", env.Error.Code)
	}
	if len(env.Error.Details) != 1 {
		t.Fatalf("details = %+v", env.Error.Details)
	}
	issues, ok := env.Error.Details[0]["issues"].([]any)
	if !ok || len(issues) != 1 {
		t.Fatalf("issues = %+v", env.Error.Details[0]["issues"])
	}
	if first, _ := issues[0].(map[string]any); first["code"] != "cycle" || first["node_id"] != "a" {
		t.Fatalf("issue = %+v", issues[0])
	}

	backend.createErr = &workflow.InvalidGraphError{Issues: []workflow.Issue{{Code: "unknown_agent", Message: "unknown agent"}}}
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows", `{"name":"x","guardian":"ghost"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "unknown_agent")
}

// API-WF-017, spec 51 REQ-WFC-002: an owner message goes to the guardian
// chat session as a chat turn with the workflow context note before it.
func TestWorkflowAssistSendsToSession(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.assistNote = "You are the guardian of workflow"
	web := newFakeWebBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, web)
	s.SetWorkflows(backend)
	web.mu.Lock()
	web.sessions = append(web.sessions, core.SessionInfo{ID: "wf-chat-1", Agent: "lana", Channel: "workflow"})
	web.mu.Unlock()

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/riset-harian/assistant", `{"message":"tambah kirim ke telegram"}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var out struct {
		SessionID string `json:"session_id"`
		Turn      string `json:"turn"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.SessionID != "wf-chat-1" || out.Turn != "started" {
		t.Fatalf("response = %+v", out)
	}
	web.mu.Lock()
	note, texts := web.sentNotes["wf-chat-1"], web.sentText["wf-chat-1"]
	web.mu.Unlock()
	if note != backend.assistNote || len(texts) != 1 || texts[0] != "tambah kirim ke telegram" {
		t.Fatalf("note = %q, texts = %v", note, texts)
	}
	if backend.noteCommits != 1 {
		t.Fatalf("note commits = %d, want 1", backend.noteCommits)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/riset-harian/assistant", `{"message":"  "}`, cookie, csrf))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty message status = %d", rr.Code)
	}
}

// Spec 51 REQ-WFC-001: the session routes ensure and replace the session; a
// guardian that cannot start answers guardian_unavailable.
func TestWorkflowAssistSessionRoutes(t *testing.T) {
	backend := newFakeWorkflowBackend()
	s, cookie, csrf := newWorkflowTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/workflows/riset-harian/assistant", "", cookie, csrf))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"session_id":""`) {
		t.Fatalf("chat before use = %d %q", rr.Code, rr.Body.String())
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/riset-harian/assistant/session", "", cookie, csrf))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"session_id":"wf-chat-1"`) {
		t.Fatalf("ensure = %d %q", rr.Code, rr.Body.String())
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/riset-harian/assistant/new", "", cookie, csrf))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"session_id":"wf-chat-2"`) {
		t.Fatalf("new = %d %q", rr.Code, rr.Body.String())
	}

	backend.assistErr = workflow.ErrGuardianUnavailable
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/riset-harian/assistant", `{"message":"hi"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusServiceUnavailable, "guardian_unavailable")
}

// API-WF-011, REQ-WF-003: a manual run answers 202 with the run.
func TestWorkflowStartRun(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.run = workflow.Run{ID: "r_1", WorkflowID: "riset-harian", Number: 1, Status: workflow.RunRunning}
	s, cookie, csrf := newWorkflowTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/riset-harian/runs", `{"input":{"x":1}}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var run workflow.Run
	if err := json.Unmarshal(rr.Body.Bytes(), &run); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if run.ID != "r_1" || run.Status != workflow.RunRunning {
		t.Fatalf("run = %+v", run)
	}
	if string(backend.startRunInput) != `{"x":1}` {
		t.Fatalf("input = %s", backend.startRunInput)
	}
}

// API-WF-020, AC-WF-004: a wrong token is 401, the right token is 202, and the
// hook needs no cookie, CSRF token, Origin, or matching Host.
func TestWorkflowWebhook(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.webhookErr = workflow.ErrBadToken
	s, _, _ := newWorkflowTestServer(t, backend)

	newHook := func(token, body string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/hooks/riset-harian", strings.NewReader(body))
		req.Host = "hooks.example.test"
		if token != "" {
			req.Header.Set("X-Lana-Token", token)
		}
		return req
	}

	rr := serveAuth(s, newHook("wrong", `{"a":1}`))
	assertAuthError(t, rr, http.StatusUnauthorized, "bad_token")

	backend.mu.Lock()
	backend.webhookErr = nil
	backend.webhookRun = workflow.Run{ID: "r_9", Number: 9}
	backend.mu.Unlock()

	rr = serveAuth(s, newHook("right", `{"a":1}`))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var resp struct {
		RunID  string `json:"run_id"`
		Number int    `json:"number"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if resp.RunID != "r_9" || resp.Number != 9 {
		t.Fatalf("resp = %+v", resp)
	}
	if backend.webhookToken != "right" || string(backend.webhookBody) != `{"a":1}` {
		t.Fatalf("webhook call = %q %s", backend.webhookToken, backend.webhookBody)
	}
}

// API-WF-021, AC-WF-005: a chat command returns the command reply and starts
// no turn.
func TestWorkflowChatCommandStartsNoTurn(t *testing.T) {
	sessions := newFakeWebBackend()
	info := sessions.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})

	backend := newFakeWorkflowBackend()
	backend.command = workflow.CommandResult{WorkflowID: "riset-harian", RunID: "r_8", Number: 8, Reply: "Run #8 of Riset harian started"}
	backend.commandOK = true

	s, cookie, csrf := newLoggedInSessionServer(t, sessions)
	s.SetWorkflows(backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", `{"text":"/riset ai news"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var resp struct {
		Command workflow.CommandResult `json:"command"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if resp.Command.RunID != "r_8" || resp.Command.Reply != "Run #8 of Riset harian started" {
		t.Fatalf("command = %+v", resp.Command)
	}

	backend.mu.Lock()
	channel, commands := backend.commandCh, append([]string(nil), backend.commands...)
	backend.mu.Unlock()
	if channel != "web" || len(commands) != 1 || commands[0] != "/riset ai news" {
		t.Fatalf("command call = %q %v", channel, commands)
	}

	sessions.mu.Lock()
	sent := append([]string(nil), sessions.sentText[info.ID]...)
	sessions.mu.Unlock()
	if len(sent) != 0 {
		t.Fatalf("a turn started: %v", sent)
	}
}

// API-WF-019, REQ-WF-011: the stream emits a changed event per change.
func TestWorkflowStreamEmitsChanged(t *testing.T) {
	backend := newFakeWorkflowBackend()
	s, ts, cookie, _ := newStreamTestServer(t, newFakeWebBackend())
	s.SetWorkflows(backend)
	s.SetHeartbeatInterval(20 * time.Millisecond)

	resp := openStream(t, ts, "/v1/workflows/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)

	backend.changes <- workflow.Change{WorkflowID: "riset-harian", RunID: "r_7"}

	block := waitSSE(t, reader, "changed")
	var change workflow.Change
	if err := json.Unmarshal([]byte(block.Data), &change); err != nil {
		t.Fatalf("data %q: %v", block.Data, err)
	}
	if change.WorkflowID != "riset-harian" || change.RunID != "r_7" {
		t.Fatalf("change = %+v", change)
	}
}

// The workflow routes answer 503 when no port is installed.
func TestWorkflowUnavailable(t *testing.T) {
	s, cookie, _ := newLoggedInSessionServer(t, newFakeWebBackend())
	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/workflows", "", cookie, ""))
	assertAuthError(t, rr, http.StatusServiceUnavailable, "workflows_unavailable")
}

// The channels endpoint lists the Message node channels. The get route returns
// 404, so a 200 proves the literal /v1/workflows/channels route wins over
// GET /v1/workflows/{id} with id "channels".
func TestWorkflowChannels(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.getErr = workflow.ErrNotFound
	backend.channels = []workflow.MessageChannel{
		{ID: "web", Label: "Web chat", NeedsAgent: true, Available: true},
		{ID: "telegram", Label: "Telegram", Available: false, Reason: "Telegram is not connected"},
	}
	s, cookie, _ := newWorkflowTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/workflows/channels", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got struct {
		Channels []workflow.MessageChannel `json:"channels"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(got.Channels) != 2 || got.Channels[0].ID != "web" || got.Channels[1].ID != "telegram" {
		t.Fatalf("channels = %+v", got.Channels)
	}
	if !got.Channels[0].NeedsAgent || got.Channels[0].Label != "Web chat" {
		t.Fatalf("web channel = %+v", got.Channels[0])
	}
	if got.Channels[1].Available || got.Channels[1].Reason != "Telegram is not connected" {
		t.Fatalf("telegram channel = %+v", got.Channels[1])
	}
}

// *workflow.Service satisfies the WorkflowBackend port the web handlers use
// (spec #33, design section "Web (web/workflows.go)").
var _ WorkflowBackend = (*workflow.Service)(nil)
