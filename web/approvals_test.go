package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
