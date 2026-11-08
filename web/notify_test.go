package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
	"github.com/ridhomujizat/lana-harness/internal/notify"
)

// nextNotify waits for the next bus event of one of kinds.
func nextNotify(t *testing.T, ch <-chan notify.Event, kinds ...notify.Kind) notify.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-ch:
			for _, k := range kinds {
				if e.Kind == k {
					return e
				}
			}
		case <-deadline:
			t.Fatalf("no %v notification", kinds)
			return notify.Event{}
		}
	}
}

func localEventsRequest(target, secret, remoteAddr string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = remoteAddr
	if secret != "" {
		req.Header.Set(LocalSecretHeader, secret)
	}
	return req
}

func TestLocalEventsAuth(t *testing.T) {
	s, _, _ := newLoggedInSessionServer(t, newFakeWebBackend())
	s.SetLocalSecret(localTestSecret)
	s.SetNotifications(notify.NewBus())

	assertAuthError(t, serveAuth(s, localEventsRequest(LocalEventsPath, "", localTestAddr)), http.StatusUnauthorized, "unauthorized")
	assertAuthError(t, serveAuth(s, localEventsRequest(LocalEventsPath, "wrong", localTestAddr)), http.StatusUnauthorized, "unauthorized")
	assertAuthError(t, serveAuth(s, localEventsRequest(LocalEventsPath, localTestSecret, localTestOutside)), http.StatusForbidden, "forbidden_remote")

	post := localEventsRequest(LocalEventsPath, localTestSecret, localTestAddr)
	post.Method = http.MethodPost
	if rr := serveAuth(s, post); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", rr.Code)
	}

	answer := httptest.NewRequest(http.MethodPost, LocalApprovalsPath+"abc", strings.NewReader(`{"outcome":"allowed_once"}`))
	answer.RemoteAddr = localTestAddr
	assertAuthError(t, serveAuth(s, answer), http.StatusUnauthorized, "unauthorized")
	answer = httptest.NewRequest(http.MethodPost, LocalApprovalsPath+"abc", strings.NewReader(`{"outcome":"allowed_once"}`))
	answer.RemoteAddr = localTestOutside
	answer.Header.Set(LocalSecretHeader, localTestSecret)
	assertAuthError(t, serveAuth(s, answer), http.StatusForbidden, "forbidden_remote")

	// Without a bus the stream is unavailable.
	s.SetNotifications(nil)
	if rr := serveAuth(s, localEventsRequest(LocalEventsPath, localTestSecret, localTestAddr)); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("no bus status = %d", rr.Code)
	}
}

func openLocalEvents(t *testing.T, ts *httptest.Server, after string) (*bufio.Reader, func()) {
	t.Helper()
	target := ts.URL + LocalEventsPath
	if after != "" {
		target += "?after=" + after
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(LocalSecretHeader, localTestSecret)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		_ = resp.Body.Close()
		t.Fatalf("local events status = %d type = %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return bufio.NewReader(resp.Body), func() { _ = resp.Body.Close() }
}

func decodeNotify(t *testing.T, block sseBlock) notify.Event {
	t.Helper()
	var e notify.Event
	if err := json.Unmarshal([]byte(block.Data), &e); err != nil {
		t.Fatalf("notify data %q: %v", block.Data, err)
	}
	if block.ID != e.ID {
		t.Fatalf("SSE id %q != event id %q", block.ID, e.ID)
	}
	return e
}

func TestLocalEventsStreamAndResume(t *testing.T) {
	backend := newFakeWebBackend()
	s, ts, _, _ := newStreamTestServer(t, backend)
	s.SetLocalSecret(localTestSecret)
	bus := notify.NewBus()
	s.SetNotifications(bus)
	s.localHeartbeat = 50 * time.Millisecond

	reader, closeStream := openLocalEvents(t, ts, "")
	ready := waitSSE(t, reader, "ready")
	if !strings.Contains(ready.Data, `"last_id":""`) {
		t.Fatalf("ready = %q", ready.Data)
	}
	first, _ := bus.Publish(notify.Reply("coder", "Fix", "s1", "t1", "**done**"))
	got := decodeNotify(t, waitSSE(t, reader, "notify"))
	if got.ID != first.ID || got.Kind != notify.KindReply || got.Body != "done" || got.URL != "#/sessions/s1" || got.Title != "coder · Fix" {
		t.Fatalf("notify = %+v", got)
	}
	waitSSE(t, reader, "ping")
	closeStream()

	second, _ := bus.Publish(notify.Reply("coder", "Fix", "s1", "t2", "again"))
	third, _ := bus.Publish(notify.Reply("coder", "Fix", "s1", "t3", "and again"))
	reader, closeStream = openLocalEvents(t, ts, first.ID)
	defer closeStream()
	waitSSE(t, reader, "ready")
	if e := decodeNotify(t, waitSSE(t, reader, "notify")); e.ID != second.ID {
		t.Fatalf("resume got %s, want %s", e.ID, second.ID)
	}
	if e := decodeNotify(t, waitSSE(t, reader, "notify")); e.ID != third.ID {
		t.Fatalf("resume got %s, want %s", e.ID, third.ID)
	}
}

// TestLocalApprovalAnswerEndToEnd: an approval opened on the web broker
// reaches the bus with its broker id, the local endpoint answers it through
// the same broker, and the bus gets the decision so the app removes the
// banner.
func TestLocalApprovalAnswerEndToEnd(t *testing.T) {
	backend := newFakeWebBackend()
	root := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "lead"})
	child := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "worker", ParentSessionID: root.ID})
	s, ts, cookie, csrf := newStreamTestServer(t, backend)
	s.SetLocalSecret(localTestSecret)
	bus := notify.NewBus()
	s.SetNotifications(bus)
	_, events, cancel := bus.Subscribe("")
	defer cancel()

	outcomes := make(chan core.ApprovalOutcome, 1)
	go func() {
		outcomes <- s.Approvals().Ask(context.Background(), core.ApprovalRequest{
			SessionID: child.ID, AgentName: "worker", ToolName: "bash", Input: json.RawMessage(`{"command":"rm -rf build"}`), Risk: core.RiskExec,
		})
	}()
	asked := nextNotify(t, events, notify.KindApprovalAsked)
	if asked.ApprovalID == "" || asked.Title != "Approval needed · worker" || asked.Body != "bash: rm -rf build" || !asked.TimeSensitive {
		t.Fatalf("asked = %+v", asked)
	}
	if asked.URL != "#/sessions/"+root.ID || asked.Thread != root.ID || asked.SessionID != child.ID {
		t.Fatalf("asked leads to %q thread %q", asked.URL, asked.Thread)
	}

	post := func(id, body string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, ts.URL+LocalApprovalsPath+id, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(LocalSecretHeader, localTestSecret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}
	if resp := post(asked.ApprovalID, `{"outcome":"allowed_session"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("allowed_session status = %d", resp.StatusCode)
	}
	if resp := post(asked.ApprovalID, `{"outcome":"allowed_once"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("answer status = %d", resp.StatusCode)
	}
	if outcome := waitApprovalOutcome(t, outcomes); outcome != core.OutcomeAllowedOnce {
		t.Fatalf("outcome = %q", outcome)
	}
	decided := nextNotify(t, events, notify.KindApprovalDecided)
	if decided.ApprovalID != asked.ApprovalID || decided.Outcome != "allowed_once" {
		t.Fatalf("decided = %+v", decided)
	}
	if resp := post(asked.ApprovalID, `{"outcome":"rejected"}`); resp.StatusCode != http.StatusConflict {
		t.Fatalf("second answer status = %d", resp.StatusCode)
	}
	if resp := post("never-existed", `{"outcome":"rejected"}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown id status = %d", resp.StatusCode)
	}

	// An approval answered in the dashboard closes the banner too.
	go func() {
		outcomes <- s.Approvals().Ask(context.Background(), core.ApprovalRequest{SessionID: root.ID, ToolName: "bash", Input: json.RawMessage(`{"command":"ls"}`)})
	}()
	asked = nextNotify(t, events, notify.KindApprovalAsked)
	if asked.Title != "Approval needed · lead" {
		t.Fatalf("root approval title = %q", asked.Title)
	}
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/approvals/"+asked.ApprovalID, `{"outcome":"rejected"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("web answer status = %d", rr.Code)
	}
	waitApprovalOutcome(t, outcomes)
	if decided := nextNotify(t, events, notify.KindApprovalDecided); decided.ApprovalID != asked.ApprovalID || decided.Outcome != "rejected" {
		t.Fatalf("decided = %+v", decided)
	}
}

func TestWorkflowSessionApprovalLeadsToWorkflow(t *testing.T) {
	backend := newFakeWebBackend()
	ws := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "workflow", Address: "wf-1", Agent: "runner"})
	s, _, _ := newLoggedInSessionServer(t, backend)
	bus := notify.NewBus()
	s.SetNotifications(bus)
	_, events, cancel := bus.Subscribe("")
	defer cancel()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Approvals().Ask(ctx, core.ApprovalRequest{SessionID: ws.ID, ToolName: "write_file", Input: json.RawMessage(`{"path":"out.md"}`)})
		close(done)
	}()
	asked := nextNotify(t, events, notify.KindApprovalAsked)
	if asked.URL != "#/workflows/wf-1" || asked.WorkflowID != "wf-1" || asked.Thread != "workflow:wf-1" || asked.Body != "write_file: out.md" {
		t.Fatalf("workflow approval = %+v", asked)
	}
	stop()
	<-done
	if decided := nextNotify(t, events, notify.KindApprovalDecided); decided.Outcome != string(core.OutcomeCancelled) {
		t.Fatalf("cancelled approval = %+v", decided)
	}
}

func TestQuestionNotifications(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Agent: "coder"})
	s, _, _ := newLoggedInSessionServer(t, backend)
	bus := notify.NewBus()
	s.SetNotifications(bus)
	_, events, cancel := bus.Subscribe("")
	defer cancel()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = s.Questions().Ask(ctx, core.QuestionRequest{SessionID: info.ID, AgentName: "coder", Questions: []core.Question{
			{Text: "Which branch should I use?", Options: []core.QuestionOption{{Label: "main"}}},
			{Text: "Squash?"},
		}})
		close(done)
	}()
	asked := nextNotify(t, events, notify.KindQuestionAsked)
	if asked.Title != "Question from coder" || asked.Body != "Which branch should I use? (+1 more)" || asked.QuestionID == "" || asked.URL != "#/sessions/"+info.ID {
		t.Fatalf("question = %+v", asked)
	}
	stop()
	<-done
	if closed := nextNotify(t, events, notify.KindQuestionAnswered); closed.QuestionID != asked.QuestionID {
		t.Fatalf("closed = %+v", closed)
	}
}

func TestSessionPresenceRoute(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	bus := notify.NewBus()
	s.SetNotifications(bus)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/s-1/presence", `{"visible":true}`, cookie, csrf))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("presence status = %d body = %q", rr.Code, rr.Body.String())
	}
	if !bus.Visible("s-1") {
		t.Fatal("session not visible after the report")
	}
	if _, ok := bus.Publish(notify.Reply("a", "", "s-1", "t", "hi")); ok {
		t.Fatal("reply of a visible session was published")
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/s-1/presence", `{"visible":false}`, cookie, csrf))
	if rr.Code != http.StatusNoContent || bus.Visible("s-1") {
		t.Fatalf("hidden report: status %d visible %v", rr.Code, bus.Visible("s-1"))
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/s-1/presence", `{}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	// The CSRF check applies.
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/s-1/presence", `{"visible":true}`, cookie, "wrong"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("no CSRF status = %d", rr.Code)
	}
}
