package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// fakeApprovalClock drives approval expiry without waiting ten minutes.
type fakeApprovalClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeApprovalTimer
}

type fakeApprovalTimer struct {
	clock    *fakeApprovalClock
	ch       chan time.Time
	deadline time.Time
	fired    bool
	stopped  bool
}

func newFakeApprovalClock() *fakeApprovalClock {
	return &fakeApprovalClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeApprovalClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeApprovalClock) NewTimer(d time.Duration) approvalTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeApprovalTimer{clock: c, ch: make(chan time.Time, 1), deadline: c.now.Add(d)}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *fakeApprovalClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, timer := range c.timers {
		if timer.stopped || timer.fired || timer.deadline.After(c.now) {
			continue
		}
		timer.fired = true
		timer.ch <- c.now
	}
}

func (t *fakeApprovalTimer) C() <-chan time.Time { return t.ch }

func (t *fakeApprovalTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	active := !t.fired && !t.stopped
	t.stopped = true
	return active
}

func nextApprovalEvent(t *testing.T, ch <-chan approvalEvent) approvalEvent {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an approval event")
		return approvalEvent{}
	}
}

func waitApprovalOutcome(t *testing.T, ch <-chan core.ApprovalOutcome) core.ApprovalOutcome {
	t.Helper()
	select {
	case outcome := <-ch:
		return outcome
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an approval outcome")
		return ""
	}
}

func decodeApprovalEvent(t *testing.T, block sseBlock) approvalEvent {
	t.Helper()
	if block.Name != "approval" {
		t.Fatalf("SSE block = %q, want approval", block.Name)
	}
	var event approvalEvent
	if err := json.Unmarshal([]byte(block.Data), &event); err != nil {
		t.Fatalf("approval data %q: %v", block.Data, err)
	}
	return event
}

// AC-WEB-016: the first answer wins and a later answer is 409 approval_closed.
func TestApprovalFirstAnswerWins(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	_, events, unsubscribe := s.Approvals().subscribe("session-1")
	defer unsubscribe()

	outcomes := make(chan core.ApprovalOutcome, 1)
	go func() {
		outcomes <- s.Approvals().Ask(context.Background(), core.ApprovalRequest{
			SessionID: "session-1", ToolName: "bash", Input: json.RawMessage(`{"command":"echo hi"}`), Risk: core.RiskExec,
		})
	}()
	event := nextApprovalEvent(t, events)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+event.RequestID, `{"outcome":"allowed_once"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("first answer status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if outcome := waitApprovalOutcome(t, outcomes); outcome != core.OutcomeAllowedOnce {
		t.Fatalf("first outcome = %q", outcome)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+event.RequestID, `{"outcome":"rejected"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "approval_closed")
}

func TestApprovalUnknownID(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/never-issued", `{"outcome":"allowed_once"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

func TestApprovalRejectsLongReason(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	_, events, unsubscribe := s.Approvals().subscribe("session-1")
	defer unsubscribe()

	outcomes := make(chan core.ApprovalOutcome, 1)
	go func() {
		outcomes <- s.Approvals().Ask(context.Background(), core.ApprovalRequest{SessionID: "session-1", ToolName: "bash"})
	}()
	event := nextApprovalEvent(t, events)

	long, err := json.Marshal(map[string]string{
		"outcome": "rejected",
		"reason":  strings.Repeat("x", maxApprovalReasonChars+1),
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+event.RequestID, string(long), cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")

	atLimit, err := json.Marshal(map[string]string{
		"outcome": "rejected",
		"reason":  strings.Repeat("x", maxApprovalReasonChars),
	})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+event.RequestID, string(atLimit), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("answer with max reason status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if outcome := waitApprovalOutcome(t, outcomes); outcome != core.OutcomeRejected {
		t.Fatalf("outcome = %q", outcome)
	}
}

// AC-WEB-017: a fake-clock timeout resolves the request as unavailable.
func TestApprovalTimeoutUnavailable(t *testing.T) {
	broker := newApprovals()
	clock := newFakeApprovalClock()
	broker.setClock(clock)
	_, events, unsubscribe := broker.subscribe("session-1")
	defer unsubscribe()

	outcomes := make(chan core.ApprovalOutcome, 1)
	go func() {
		outcomes <- broker.Ask(context.Background(), core.ApprovalRequest{
			SessionID: "session-1", ToolName: "bash", Input: json.RawMessage(`{"command":"echo hi"}`), Risk: core.RiskExec,
		})
	}()
	event := nextApprovalEvent(t, events)
	if want := clock.Now().Add(approvalTimeout).UnixMilli(); event.ExpiresAt != want {
		t.Fatalf("expires_at = %d, want %d", event.ExpiresAt, want)
	}

	clock.Advance(approvalTimeout)
	if outcome := waitApprovalOutcome(t, outcomes); outcome != core.OutcomeUnavailable {
		t.Fatalf("timeout outcome = %q", outcome)
	}
	if err := broker.answer(event.RequestID, core.OutcomeAllowedOnce); !errors.Is(err, errApprovalClosed) {
		t.Fatalf("late answer error = %v, want approval closed", err)
	}
}

func TestApprovalCancelOutcome(t *testing.T) {
	broker := newApprovals()
	_, events, unsubscribe := broker.subscribe("session-1")
	defer unsubscribe()

	ctx, cancel := context.WithCancel(context.Background())
	outcomes := make(chan core.ApprovalOutcome, 1)
	go func() {
		outcomes <- broker.Ask(ctx, core.ApprovalRequest{SessionID: "session-1", ToolName: "bash"})
	}()
	event := nextApprovalEvent(t, events)
	cancel()
	if outcome := waitApprovalOutcome(t, outcomes); outcome != core.OutcomeCancelled {
		t.Fatalf("cancel outcome = %q", outcome)
	}
	if err := broker.answer(event.RequestID, core.OutcomeAllowedOnce); !errors.Is(err, errApprovalClosed) {
		t.Fatalf("late answer error = %v, want approval closed", err)
	}
}

func TestApprovalSSEEventAndReplay(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	s, ts, cookie, csrf := newStreamTestServer(t, backend)

	resp := openStream(t, ts, "/v1/sessions/"+info.ID+"/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	waitSSE(t, reader, "status")

	outcomes := make(chan core.ApprovalOutcome, 1)
	go func() {
		outcomes <- s.Approvals().Ask(context.Background(), core.ApprovalRequest{
			SessionID: info.ID, ToolName: "bash", Input: json.RawMessage(`{"command":"echo hi"}`), Risk: core.RiskExec,
		})
	}()
	live := decodeApprovalEvent(t, waitSSE(t, reader, "approval"))
	if live.Tool != "bash" || live.Argument != "echo hi" || live.Risk != core.RiskExec {
		t.Fatalf("approval event = %+v", live)
	}
	if live.RequestID == "" {
		t.Fatal("approval event has no request_id")
	}
	if live.ExpiresAt < time.Now().Add(9*time.Minute).UnixMilli() {
		t.Fatalf("expires_at = %d, want about ten minutes from now", live.ExpiresAt)
	}

	// A second stream connected while the request is open gets it replayed.
	second := openStream(t, ts, "/v1/sessions/"+info.ID+"/stream", cookie, "")
	defer func() { _ = second.Body.Close() }()
	secondReader := bufio.NewReader(second.Body)
	waitSSE(t, secondReader, "status")
	replayed := decodeApprovalEvent(t, waitSSE(t, secondReader, "approval"))
	if replayed != live {
		t.Fatalf("replayed event = %+v, want %+v", replayed, live)
	}

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+live.RequestID, `{"outcome":"allowed_once"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("answer status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if outcome := waitApprovalOutcome(t, outcomes); outcome != core.OutcomeAllowedOnce {
		t.Fatalf("outcome = %q", outcome)
	}
}

// AC-TEAM-025 (web part): an approval of a child is listed on the root
// subscription with the child tags, and the label is the child agent.
func TestApprovalChildOnRootSubscription(t *testing.T) {
	backend := newFakeWebBackend()
	root := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	child := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "researcher", ParentSessionID: root.ID})
	s, ts, cookie, csrf := newStreamTestServer(t, backend)

	resp := openStream(t, ts, "/v1/sessions/"+root.ID+"/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	waitSSE(t, reader, "status")
	select {
	case <-backend.subscribed:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not subscribe")
	}

	outcomes := make(chan core.ApprovalOutcome, 1)
	go func() {
		outcomes <- s.Approvals().Ask(context.Background(), core.ApprovalRequest{
			SessionID: child.ID, AgentName: "researcher", Label: "researcher",
			ToolName: "bash", Input: json.RawMessage(`{"command":"echo hi"}`), Risk: core.RiskExec,
		})
	}()
	live := decodeApprovalEvent(t, waitSSE(t, reader, "approval"))
	if live.ChildSessionID != child.ID {
		t.Fatalf("child_session_id = %q, want %q", live.ChildSessionID, child.ID)
	}
	if live.Agent != "researcher" || live.Label != "researcher" {
		t.Fatalf("agent = %q, label = %q, want researcher", live.Agent, live.Label)
	}
	if live.Tool != "bash" || live.RequestID == "" {
		t.Fatalf("approval = %+v", live)
	}

	// A second root stream replays the open child request with the tags.
	second := openStream(t, ts, "/v1/sessions/"+root.ID+"/stream", cookie, "")
	defer func() { _ = second.Body.Close() }()
	secondReader := bufio.NewReader(second.Body)
	waitSSE(t, secondReader, "status")
	replayed := decodeApprovalEvent(t, waitSSE(t, secondReader, "approval"))
	if replayed != live {
		t.Fatalf("replayed event = %+v, want %+v", replayed, live)
	}

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+live.RequestID, `{"outcome":"allowed_once"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("answer status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if outcome := waitApprovalOutcome(t, outcomes); outcome != core.OutcomeAllowedOnce {
		t.Fatalf("outcome = %q", outcome)
	}
}

// A root approval keeps the spec #9 item shape: no child tags.
func TestApprovalRootHasNoChildTags(t *testing.T) {
	backend := newFakeWebBackend()
	root := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	s, ts, cookie, _ := newStreamTestServer(t, backend)

	resp := openStream(t, ts, "/v1/sessions/"+root.ID+"/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	waitSSE(t, reader, "status")
	select {
	case <-backend.subscribed:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not subscribe")
	}

	outcomes := make(chan core.ApprovalOutcome, 1)
	go func() {
		outcomes <- s.Approvals().Ask(context.Background(), core.ApprovalRequest{
			SessionID: root.ID, AgentName: "helper", ToolName: "bash", Risk: core.RiskExec,
		})
	}()
	event := decodeApprovalEvent(t, waitSSE(t, reader, "approval"))
	if event.ChildSessionID != "" || event.Agent != "" || event.Label != "" {
		t.Fatalf("root approval tags = %q, %q, %q", event.ChildSessionID, event.Agent, event.Label)
	}
	if err := s.Approvals().answer(event.RequestID, core.OutcomeAllowedOnce); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if outcome := waitApprovalOutcome(t, outcomes); outcome != core.OutcomeAllowedOnce {
		t.Fatalf("outcome = %q", outcome)
	}
}

// AC-WEB-017 at the tool-run level: a timeout rejects the tool, the file is not
// written, and the model gets the tool error back.
func TestApprovalTimeoutToolError(t *testing.T) {
	workspace := t.TempDir()
	layer := fmt.Sprintf(`[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.assistant]
provider = "mock"
model = "m"
workspace = %q
tools = ["write_file"]
permission = "ask"

[[routes]]
channel = "web"
agent = "assistant"
`, workspace)

	stopTool, stopEnd := "tool_use", "end_turn"
	text := "done"
	script := mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{ToolUse: &mock.ToolUse{ID: "c1", Name: "write_file", Input: json.RawMessage(`{"path":"blocked.txt","content":"x"}`)}},
			{Stop: &stopTool},
		}},
		{Events: []mock.Event{{Text: &text}, {Stop: &stopEnd}}},
	}}

	broker := newApprovals()
	clock := newFakeApprovalClock()
	broker.setClock(clock)
	rt, err := lana.New(context.Background(), lana.Options{
		Home:      t.TempDir(),
		Layers:    []config.Layer{{Name: "test", TOML: []byte(layer)}},
		Providers: map[string]core.Provider{"mock": mock.New(script)},
	}, lana.ApproveWith(broker.Ask), lana.ApproveChannels("web"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	info, err := rt.CreateChannelSession(context.Background(), "web", "owner", "assistant")
	if err != nil {
		t.Fatal(err)
	}
	_, events, unsubscribe := broker.subscribe(info.ID)
	defer unsubscribe()

	type runResult struct {
		result lana.Result
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, runErr := rt.Run(context.Background(), lana.Request{Prompt: "write it", SessionID: info.ID})
		done <- runResult{result: result, err: runErr}
	}()

	event := nextApprovalEvent(t, events)
	if event.Tool != "write_file" {
		t.Fatalf("approval tool = %q, want write_file", event.Tool)
	}
	clock.Advance(approvalTimeout)

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("run error: %v", result.err)
		}
		if result.result.StopReason != lana.StopEndTurn || result.result.Text != "done" {
			t.Fatalf("run result = %+v", result.result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not finish after the approval timeout")
	}

	history, err := rt.History(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	var toolResult core.ToolResultPayload
	var decided core.ApprovalDecided
	for _, stored := range history {
		switch stored.Kind {
		case core.KindToolResult:
			var payload core.ToolResultPayload
			if err := json.Unmarshal(stored.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.CallID == "c1" {
				toolResult = payload
			}
		case core.KindApprovalDecided:
			if err := json.Unmarshal(stored.Payload, &decided); err != nil {
				t.Fatal(err)
			}
		}
	}
	if toolResult.Status != core.ToolStatusRejected || !strings.Contains(toolResult.Output, "unavailable") {
		t.Fatalf("tool result = %+v, want a rejected unavailable tool error", toolResult)
	}
	if decided.Outcome != core.OutcomeUnavailable {
		t.Fatalf("decided outcome = %q, want unavailable", decided.Outcome)
	}
	if _, err := os.Stat(filepath.Join(workspace, "blocked.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the tool ran despite the timeout: %v", err)
	}
}

// askApproval subscribes the broker, opens a request, and returns its SSE event
// and outcome channel. A hardline request carries the given tier.
func askApproval(t *testing.T, broker *Approvals, sessionID string, hardline *core.HardlineMatch) (approvalEvent, <-chan core.ApprovalOutcome) {
	t.Helper()
	_, events, unsubscribe := broker.subscribe(sessionID)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); unsubscribe() })
	outcomes := make(chan core.ApprovalOutcome, 1)
	req := core.ApprovalRequest{
		SessionID: sessionID, ToolName: "bash",
		Input: json.RawMessage(`{"command":"echo hi"}`), Risk: core.RiskExec,
		Hardline: hardline,
	}
	go func() {
		outcomes <- broker.Ask(ctx, req)
	}()
	return nextApprovalEvent(t, events), outcomes
}

func askHardline(t *testing.T, broker *Approvals, sessionID, tier string) (approvalEvent, <-chan core.ApprovalOutcome) {
	t.Helper()
	return askApproval(t, broker, sessionID, &core.HardlineMatch{Rule: "HL-B-001", Tier: tier, Reason: "recursive delete of home"})
}

func askPlain(t *testing.T, broker *Approvals, sessionID string) (approvalEvent, <-chan core.ApprovalOutcome) {
	t.Helper()
	return askApproval(t, broker, sessionID, nil)
}

// approvalRequestStillOpen reports whether the broker still holds the request.
func approvalRequestStillOpen(t *testing.T, s *Server, id string) bool {
	t.Helper()
	_, err := s.Approvals().pending(id)
	return err == nil
}

// AC-HLF-013 / API-HLF-004: a block event carries hardline.step_up, "password"
// by default and "password_or_telegram" when confirm_with allows a code.
func TestApprovalHardlineStepUp(t *testing.T) {
	s, _, _ := newLoggedInSessionServer(t, newFakeWebBackend())

	block, _ := askHardline(t, s.Approvals(), "session-1", core.HardlineBlock)
	if block.Hardline == nil || block.Hardline.StepUp != core.WebRemoteConfirmPassword {
		t.Fatalf("default block hardline = %+v, want step_up password", block.Hardline)
	}
	if block.Hardline.Rule != "HL-B-001" || block.Hardline.Tier != core.HardlineBlock || block.Hardline.Reason == "" {
		t.Fatalf("block hardline = %+v", block.Hardline)
	}

	s.SetRemoteConfig(core.DefaultWebRemoteConfig())
	configured, _ := askHardline(t, s.Approvals(), "session-2", core.HardlineBlock)
	if configured.Hardline == nil || configured.Hardline.StepUp != core.WebRemoteConfirmPasswordOrTelegram {
		t.Fatalf("configured block hardline = %+v, want step_up password_or_telegram", configured.Hardline)
	}

	escalate, _ := askHardline(t, s.Approvals(), "session-3", core.HardlineEscalate)
	if escalate.Hardline == nil || escalate.Hardline.Tier != core.HardlineEscalate || escalate.Hardline.StepUp != "" {
		t.Fatalf("escalate hardline = %+v, want empty step_up", escalate.Hardline)
	}
}

// AC-HLF-013 / API-HLF-005: a block allow needs exactly one good step-up proof.
func TestApprovalHardlineBlockStepUp(t *testing.T) {
	s, _, _ := newLoggedInSessionServer(t, newFakeWebBackend())
	cookie, csrf := setPasswordViaLink(t, s, testPassword)
	event, outcomes := askHardline(t, s.Approvals(), "session-1", core.HardlineBlock)

	answer := func(body string) *httptest.ResponseRecorder {
		return serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+event.RequestID, body, cookie, csrf))
	}

	assertAuthError(t, answer(`{"outcome":"allowed_once"}`), http.StatusBadRequest, "step_up_required")
	if !approvalRequestStillOpen(t, s, event.RequestID) {
		t.Fatal("request closed without a step-up proof")
	}
	assertAuthError(t, answer(`{"outcome":"allowed_once","password":"x","code":"y"}`), http.StatusBadRequest, "step_up_required")
	assertAuthError(t, answer(`{"outcome":"allowed_session","password":"`+testPassword+`"}`), http.StatusBadRequest, "invalid_request")
	if !approvalRequestStillOpen(t, s, event.RequestID) {
		t.Fatal("request closed after allowed_session")
	}
	assertAuthError(t, answer(`{"outcome":"allowed_once","password":"wrong"}`), http.StatusUnauthorized, "invalid_password")
	if !approvalRequestStillOpen(t, s, event.RequestID) {
		t.Fatal("request closed after a wrong password")
	}
	assertAuthError(t, answer(`{"outcome":"allowed_once","code":"123456"}`), http.StatusBadRequest, "invalid_request")

	rr := answer(`{"outcome":"allowed_once","password":"` + testPassword + `"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("password answer status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var resp approvalAnswerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Outcome != string(core.OutcomeAllowedVerified) {
		t.Fatalf("body outcome = %q, want allowed_verified", resp.Outcome)
	}
	if outcome := waitApprovalOutcome(t, outcomes); outcome != core.OutcomeAllowedVerified {
		t.Fatalf("outcome = %q, want allowed_verified", outcome)
	}
}

// REQ-HLF-008: an escalate request keeps the ordinary flow, and no hardline
// request can be allowed for the session.
func TestApprovalHardlineEscalate(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	event, outcomes := askHardline(t, s.Approvals(), "session-1", core.HardlineEscalate)

	assertAuthError(t, serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+event.RequestID, `{"outcome":"allowed_session"}`, cookie, csrf)), http.StatusBadRequest, "invalid_request")
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+event.RequestID, `{"outcome":"allowed_once"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("escalate allow status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if outcome := waitApprovalOutcome(t, outcomes); outcome != core.OutcomeAllowedOnce {
		t.Fatalf("outcome = %q, want allowed_once", outcome)
	}
}

// approvalCodeFromMessage returns the 6-digit code of the API-HLF-006 owner
// message.
func approvalCodeFromMessage(t *testing.T, message string) string {
	t.Helper()
	const prefix = "Lana approval code: "
	i := strings.Index(message, prefix)
	if i < 0 {
		t.Fatalf("message %q has no approval code", message)
	}
	code := message[i+len(prefix):]
	if j := strings.IndexByte(code, '\n'); j >= 0 {
		code = code[:j]
	}
	if len(code) != 6 {
		t.Fatalf("code = %q, want 6 digits", code)
	}
	return code
}

// requestApprovalCode calls API-HLF-006 and returns the code of the owner
// message.
func requestApprovalCode(t *testing.T, s *Server, id string, cookie *http.Cookie, csrf string, messenger *fakeOwnerMessenger) string {
	t.Helper()
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+id+"/code", "", cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code request status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var sent struct {
		SentTo int `json:"sent_to"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.SentTo != 1 || len(messenger.sent) == 0 {
		t.Fatalf("sent_to = %d, messages = %d, want 1, 1", sent.SentTo, len(messenger.sent))
	}
	return approvalCodeFromMessage(t, messenger.sent[len(messenger.sent)-1])
}

// AC-HLF-013 / API-HLF-006: a Telegram code resolves a block request as
// allowed_verified and cannot be reused.
func TestApprovalHardlineCodeFlow(t *testing.T) {
	s, _, _ := newLoggedInSessionServer(t, newFakeWebBackend())
	s.SetRemoteConfig(core.DefaultWebRemoteConfig())
	cookie, csrf := setPasswordViaLink(t, s, testPassword)
	messenger := &fakeOwnerMessenger{}
	s.SetTelegramOwnerMessenger(messenger)
	s.SetTelegramResetSender(&fakeTelegramReset{available: true})

	eventA, outcomesA := askHardline(t, s.Approvals(), "session-a", core.HardlineBlock)
	eventB, _ := askHardline(t, s.Approvals(), "session-b", core.HardlineBlock)
	if eventA.Hardline.StepUp != core.WebRemoteConfirmPasswordOrTelegram {
		t.Fatalf("step_up = %q, want password_or_telegram", eventA.Hardline.StepUp)
	}

	code := requestApprovalCode(t, s, eventA.RequestID, cookie, csrf, messenger)

	assertAuthError(t, serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+eventA.RequestID,
		`{"outcome":"allowed_once","code":"000000"}`, cookie, csrf)), http.StatusUnauthorized, "invalid_code")
	if !approvalRequestStillOpen(t, s, eventA.RequestID) {
		t.Fatal("request closed after a wrong code")
	}

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+eventA.RequestID,
		`{"outcome":"allowed_once","code":"`+code+`"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("code answer status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var resp approvalAnswerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Outcome != string(core.OutcomeAllowedVerified) {
		t.Fatalf("body outcome = %q, want allowed_verified", resp.Outcome)
	}
	if outcome := waitApprovalOutcome(t, outcomesA); outcome != core.OutcomeAllowedVerified {
		t.Fatalf("outcome = %q, want allowed_verified", outcome)
	}

	// A code is single use: it cannot answer another request.
	assertAuthError(t, serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+eventB.RequestID,
		`{"outcome":"allowed_once","code":"`+code+`"}`, cookie, csrf)), http.StatusUnauthorized, "invalid_code")
}

// The approval code store follows REQ-RA-032/033: single use.
func TestApprovalCodeStoreSingleUse(t *testing.T) {
	store := newControlCodeStore()
	key := approvalCodeKey("req-1")
	now := time.Unix(1_700_000_000, 0)
	if !store.start(key, "123456", now) {
		t.Fatal("first start was rejected")
	}
	if !store.verify(key, "123456", now) {
		t.Fatal("first verify failed")
	}
	if store.verify(key, "123456", now) {
		t.Fatal("code was reused")
	}
}

// API-HLF-006: a code is only sent for an open block request whose step_up
// allows it, and only while Telegram is connected.
func TestApprovalCodeNotNeeded(t *testing.T) {
	s, _, _ := newLoggedInSessionServer(t, newFakeWebBackend())
	s.SetRemoteConfig(core.DefaultWebRemoteConfig())
	cookie, csrf := setPasswordViaLink(t, s, testPassword)
	s.SetTelegramOwnerMessenger(&fakeOwnerMessenger{})
	s.SetTelegramResetSender(&fakeTelegramReset{available: true})

	code := func(id string) *httptest.ResponseRecorder {
		return serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+id+"/code", "", cookie, csrf))
	}

	escalate, _ := askHardline(t, s.Approvals(), "session-1", core.HardlineEscalate)
	assertAuthError(t, code(escalate.RequestID), http.StatusConflict, "not_needed")

	plain, _ := askPlain(t, s.Approvals(), "session-1")
	assertAuthError(t, code(plain.RequestID), http.StatusConflict, "not_needed")

	// With confirm_with = password the block request needs no code.
	s.SetRemoteConfig(core.WebRemoteConfig{ConfirmWith: core.WebRemoteConfirmPassword})
	passwordOnly, _ := askHardline(t, s.Approvals(), "session-1", core.HardlineBlock)
	assertAuthError(t, code(passwordOnly.RequestID), http.StatusConflict, "not_needed")

	// Telegram not connected makes the send unavailable.
	s.SetRemoteConfig(core.DefaultWebRemoteConfig())
	s.SetTelegramOwnerMessenger(nil)
	block, _ := askHardline(t, s.Approvals(), "session-1", core.HardlineBlock)
	assertAuthError(t, code(block.RequestID), http.StatusConflict, "telegram_unavailable")
}

// API-HLF-005: a non-hardline request keeps the old behavior exactly.
func TestApprovalNonHardlineUnchanged(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	event, outcomes := askPlain(t, s.Approvals(), "session-1")
	if event.Hardline != nil {
		t.Fatalf("non-hardline event has hardline %+v", event.Hardline)
	}
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+event.RequestID, `{"outcome":"allowed_session"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("allowed_session status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if outcome := waitApprovalOutcome(t, outcomes); outcome != core.OutcomeAllowedSession {
		t.Fatalf("outcome = %q, want allowed_session", outcome)
	}
}
