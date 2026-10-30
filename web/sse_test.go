package web

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

type sseBlock struct {
	Name    string
	ID      string
	Data    string
	Comment string
}

func newStreamTestServer(t *testing.T, backend Backend) (*Server, *httptest.Server, *http.Cookie, string) {
	t.Helper()
	s, token := newSessionTestServer(t, backend)
	cookie, csrf := loginAndSession(t, s, token)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts, cookie, csrf
}

func openStream(t *testing.T, ts *httptest.Server, target string, cookie *http.Cookie, lastEventID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = authTestAddr
	req.AddCookie(cookie)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("stream status = %d, body = %q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		_ = resp.Body.Close()
		t.Fatalf("Content-Type = %q", got)
	}
	return resp
}

func readSSEBlock(r *bufio.Reader) (sseBlock, error) {
	var block sseBlock
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return block, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if block != (sseBlock{}) {
				return block, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			block.Comment = strings.TrimSpace(strings.TrimPrefix(line, ":"))
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			block.Name = value
		case "id":
			block.ID = value
		case "data":
			block.Data = value
		}
	}
}

func nextSSEBlock(t *testing.T, r *bufio.Reader) sseBlock {
	t.Helper()
	type result struct {
		block sseBlock
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		block, err := readSSEBlock(r)
		ch <- result{block: block, err: err}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("read SSE block: %v", got.err)
		}
		return got.block
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an SSE block")
		return sseBlock{}
	}
}

func waitSSE(t *testing.T, r *bufio.Reader, name string) sseBlock {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		block := nextSSEBlock(t, r)
		if block.Name == name || (name == "ping" && block.Comment == "ping") {
			return block
		}
	}
	t.Fatalf("no %q SSE block", name)
	return sseBlock{}
}

// ChildSessions is the optional childSessionLister extension of the stream
// fan-in (API-TEAM-011).
func (f *fakeWebBackend) ChildSessions(_ context.Context, parentID string) ([]core.SessionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	children := make([]core.SessionInfo, 0)
	for _, info := range f.sessions {
		if info.ParentSessionID == parentID {
			children = append(children, info)
		}
	}
	return children, nil
}

// taggedEvent is an event item with the child fields of API-TEAM-011.
type taggedEvent struct {
	Seq            int64           `json:"seq"`
	Kind           string          `json:"kind"`
	TurnID         string          `json:"turn_id"`
	CreatedAt      int64           `json:"created_at"`
	Payload        json.RawMessage `json:"payload"`
	ChildSessionID string          `json:"child_session_id"`
	Agent          string          `json:"agent"`
}

func TestStreamChildFramesTagged(t *testing.T) {
	backend := newFakeWebBackend()
	root := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	child := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "researcher", ParentSessionID: root.ID})
	backend.setTurnActive(root.ID, true)
	s, ts, cookie, _ := newStreamTestServer(t, backend)
	s.SetHeartbeatInterval(20 * time.Millisecond)
	s.SetStreamPollInterval(10 * time.Millisecond)

	resp := openStream(t, ts, "/v1/sessions/"+root.ID+"/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	waitSSE(t, reader, "status")
	select {
	case <-backend.subscribed:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not subscribe")
	}

	backend.publishFrame(core.LiveFrame{SessionID: root.ID, TurnID: "t1", Kind: "text_delta", Text: "root"})
	// The child's tool_call has no Agent on the frame; the handler fills it from
	// the child session listing. The tool_result carries the Agent itself.
	backend.publishFrame(core.LiveFrame{SessionID: child.ID, TurnID: "c1", Kind: "tool_call", CallID: "call-1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)})
	backend.publishFrame(core.LiveFrame{SessionID: child.ID, Agent: child.Agent, TurnID: "c1", Kind: "tool_result", CallID: "call-1", Name: "bash", Status: "success", Output: "ok"})

	var rootFrame, childCall, childResult frameJSON
	var rootData string
	haveRoot, haveCall, haveResult := false, false, false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (!haveRoot || !haveCall || !haveResult) {
		block := nextSSEBlock(t, reader)
		if block.Name != "frame" {
			continue
		}
		var frame frameJSON
		if err := json.Unmarshal([]byte(block.Data), &frame); err != nil {
			t.Fatalf("frame data %q: %v", block.Data, err)
		}
		switch {
		case frame.ChildSessionID == child.ID && frame.Kind == "tool_call":
			childCall, haveCall = frame, true
		case frame.ChildSessionID == child.ID && frame.Kind == "tool_result":
			childResult, haveResult = frame, true
		case frame.Kind == "text_delta":
			rootFrame, rootData, haveRoot = frame, block.Data, true
		}
	}
	if !haveRoot || !haveCall || !haveResult {
		t.Fatalf("frames: root=%v call=%v result=%v", haveRoot, haveCall, haveResult)
	}
	if childCall.ChildSessionID != child.ID || childCall.Agent != "researcher" {
		t.Fatalf("child tool_call tags = %q, %q", childCall.ChildSessionID, childCall.Agent)
	}
	if childCall.Name != "bash" || childCall.Status != "running" {
		t.Fatalf("child tool_call = %+v", childCall)
	}
	if childResult.ChildSessionID != child.ID || childResult.Agent != "researcher" || childResult.Status != "ok" {
		t.Fatalf("child tool_result = %+v", childResult)
	}
	if rootFrame.ChildSessionID != "" || rootFrame.Agent != "" {
		t.Fatalf("root frame tags = %q, %q", rootFrame.ChildSessionID, rootFrame.Agent)
	}
	if strings.Contains(rootData, "child_session_id") || strings.Contains(rootData, `"agent"`) {
		t.Fatalf("root frame data = %s, want no child fields", rootData)
	}
}

func TestStreamChildTurnEndIsEventNotTurn(t *testing.T) {
	backend := newFakeWebBackend()
	root := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	child := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "researcher", ParentSessionID: root.ID})
	backend.setTurnActive(root.ID, true)
	backend.appendEvent(child.ID, core.SessionEvent{Kind: core.KindUserMessage, TurnID: "child-turn", Payload: json.RawMessage(`{"v":1}`)})
	backend.appendEvent(child.ID, core.SessionEvent{Kind: core.KindTurnEnd, TurnID: "child-turn", Payload: json.RawMessage(`{"v":1,"turn_id":"child-turn","reason":"end_turn"}`)})

	s, ts, cookie, _ := newStreamTestServer(t, backend)
	s.SetHeartbeatInterval(20 * time.Millisecond)
	s.SetStreamPollInterval(10 * time.Millisecond)

	resp := openStream(t, ts, "/v1/sessions/"+root.ID+"/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	status := waitSSE(t, reader, "status")
	var state statusJSON
	if err := json.Unmarshal([]byte(status.Data), &state); err != nil {
		t.Fatal(err)
	}
	if !state.TurnRunning {
		t.Fatalf("initial status = %+v, want running", state)
	}

	childEnded := false
	sawChildTurn := false
	sawStopped := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !childEnded {
		block := nextSSEBlock(t, reader)
		switch block.Name {
		case "turn":
			sawChildTurn = true
		case "status":
			var got statusJSON
			if err := json.Unmarshal([]byte(block.Data), &got); err != nil {
				t.Fatal(err)
			}
			if !got.TurnRunning {
				sawStopped = true
			}
		case "event":
			var event taggedEvent
			if err := json.Unmarshal([]byte(block.Data), &event); err != nil {
				t.Fatalf("event data %q: %v", block.Data, err)
			}
			if event.ChildSessionID != child.ID || event.Agent != "researcher" {
				continue
			}
			if block.ID != "" {
				t.Fatalf("child event has SSE id %q", block.ID)
			}
			if event.Kind == core.KindTurnEnd {
				if event.Seq != 2 || event.TurnID != "child-turn" {
					t.Fatalf("child turn/end = %+v", event)
				}
				childEnded = true
			}
		}
	}
	if !childEnded {
		t.Fatal("child turn/end was not replayed as an event")
	}
	if sawChildTurn {
		t.Fatal("child turn/end was sent as a turn item")
	}
	if sawStopped {
		t.Fatal("root running state stopped before the root turn/end")
	}

	backend.appendEvent(root.ID, core.SessionEvent{Kind: core.KindTurnEnd, TurnID: "root-turn", Payload: json.RawMessage(`{"v":1,"turn_id":"root-turn","reason":"end_turn"}`)})
	backend.setTurnActive(root.ID, false)

	var turn turnJSON
	sawRootEnd := false
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && turn.TurnID == "" {
		block := nextSSEBlock(t, reader)
		switch block.Name {
		case "event":
			var event taggedEvent
			if err := json.Unmarshal([]byte(block.Data), &event); err != nil {
				t.Fatal(err)
			}
			if event.ChildSessionID == "" && event.Kind == core.KindTurnEnd {
				sawRootEnd = true
			}
		case "turn":
			if err := json.Unmarshal([]byte(block.Data), &turn); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !sawRootEnd || turn.TurnID != "root-turn" || turn.StopReason != "end_turn" {
		t.Fatalf("root turn/end = %+v (event seen %v)", turn, sawRootEnd)
	}
}

func TestStreamReplaysChildrenFromStart(t *testing.T) {
	backend := newFakeWebBackend()
	root := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	child := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "researcher", ParentSessionID: root.ID})
	backend.setTurnActive(root.ID, true)
	backend.appendEvent(root.ID, core.SessionEvent{Kind: core.KindUserMessage, Payload: json.RawMessage(`{"v":1}`)})
	backend.appendEvent(root.ID, core.SessionEvent{Kind: core.KindAssistantMessage, Payload: json.RawMessage(`{"v":1}`)})
	backend.appendEvent(root.ID, core.SessionEvent{Kind: core.KindTurnEnd, TurnID: "root-turn", Payload: json.RawMessage(`{"v":1,"turn_id":"root-turn","reason":"end_turn"}`)})
	backend.appendEvent(child.ID, core.SessionEvent{Kind: core.KindUserMessage, TurnID: "child-turn", Payload: json.RawMessage(`{"v":1}`)})
	backend.appendEvent(child.ID, core.SessionEvent{Kind: core.KindToolResult, TurnID: "child-turn", Payload: json.RawMessage(`{"v":1}`)})

	s, ts, cookie, _ := newStreamTestServer(t, backend)
	s.SetHeartbeatInterval(20 * time.Millisecond)
	s.SetStreamPollInterval(10 * time.Millisecond)

	// Last-Event-ID = 1 applies to the root only. The child still replays from
	// its seq 1 (API-TEAM-011, AC-TEAM-035).
	resp := openStream(t, ts, "/v1/sessions/"+root.ID+"/stream", cookie, "1")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	waitSSE(t, reader, "status")

	var rootIDs []string
	var childSeqs []int64
	rootTurn := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (len(rootIDs) < 2 || len(childSeqs) < 2 || !rootTurn) {
		block := nextSSEBlock(t, reader)
		switch block.Name {
		case "event":
			var event taggedEvent
			if err := json.Unmarshal([]byte(block.Data), &event); err != nil {
				t.Fatalf("event data %q: %v", block.Data, err)
			}
			if event.ChildSessionID == "" {
				rootIDs = append(rootIDs, block.ID)
				continue
			}
			if event.ChildSessionID != child.ID || event.Agent != "researcher" {
				t.Fatalf("child event tags = %q, %q", event.ChildSessionID, event.Agent)
			}
			if block.ID != "" {
				t.Fatalf("child replay event has SSE id %q", block.ID)
			}
			childSeqs = append(childSeqs, event.Seq)
		case "turn":
			rootTurn = true
		}
	}
	if !rootTurn {
		t.Fatal("root turn/end was not replayed")
	}
	if len(rootIDs) != 2 || rootIDs[0] != "2" || rootIDs[1] != "3" {
		t.Fatalf("root replay ids = %v, want [2 3]", rootIDs)
	}
	if len(childSeqs) != 2 || childSeqs[0] != 1 || childSeqs[1] != 2 {
		t.Fatalf("child replay seqs = %v, want [1 2]", childSeqs)
	}
}

func TestStreamFramesEventsAndTurn(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	backend.setTurnActive(info.ID, true)
	s, ts, cookie, _ := newStreamTestServer(t, backend)
	s.SetHeartbeatInterval(20 * time.Millisecond)
	s.SetStreamPollInterval(10 * time.Millisecond)

	resp := openStream(t, ts, "/v1/sessions/"+info.ID+"/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)

	status := waitSSE(t, reader, "status")
	var state statusJSON
	if err := json.Unmarshal([]byte(status.Data), &state); err != nil {
		t.Fatalf("status data %q: %v", status.Data, err)
	}
	if state.Holder != "self" || !state.TurnRunning || state.PID != nil {
		t.Fatalf("status = %+v", state)
	}

	select {
	case <-backend.subscribed:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not subscribe")
	}

	backend.publishFrame(core.LiveFrame{SessionID: info.ID, TurnID: "t1", Kind: "text_delta", Text: "hel"})
	backend.publishFrame(core.LiveFrame{SessionID: info.ID, TurnID: "t1", Kind: "text_delta", Text: "lo"})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindUserMessage, TurnID: "t1", Payload: json.RawMessage(`{"v":1,"content":[{"type":"text","text":"hello"}]}`)})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindAssistantMessage, TurnID: "t1", Payload: json.RawMessage(`{"v":1,"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn"}`)})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindTurnEnd, TurnID: "t1", Payload: json.RawMessage(`{"v":1,"turn_id":"t1","reason":"end_turn"}`)})
	backend.setTurnActive(info.ID, false)

	var text strings.Builder
	var events []eventJSON
	var turn turnJSON
	statusChanged := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (len(events) < 3 || turn.TurnID == "" || !statusChanged) {
		block := nextSSEBlock(t, reader)
		switch block.Name {
		case "frame":
			var frame frameJSON
			if err := json.Unmarshal([]byte(block.Data), &frame); err != nil {
				t.Fatalf("frame data %q: %v", block.Data, err)
			}
			if frame.Kind == "text_delta" {
				text.WriteString(frame.Text)
			}
		case "event":
			var event eventJSON
			if err := json.Unmarshal([]byte(block.Data), &event); err != nil {
				t.Fatalf("event data %q: %v", block.Data, err)
			}
			events = append(events, event)
		case "turn":
			if err := json.Unmarshal([]byte(block.Data), &turn); err != nil {
				t.Fatalf("turn data %q: %v", block.Data, err)
			}
		case "status":
			var state statusJSON
			if err := json.Unmarshal([]byte(block.Data), &state); err != nil {
				t.Fatalf("status data %q: %v", block.Data, err)
			}
			if state.Holder == "none" && !state.TurnRunning {
				statusChanged = true
			}
		}
	}
	if !statusChanged {
		t.Fatal("no status change after the turn ended")
	}
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	for i, event := range events {
		if event.Seq != int64(i+1) {
			t.Fatalf("event %d seq = %d", i, event.Seq)
		}
	}
	if events[0].Kind != core.KindUserMessage || events[2].Kind != core.KindTurnEnd {
		t.Fatalf("event kinds = %s, %s, %s", events[0].Kind, events[1].Kind, events[2].Kind)
	}
	if turn.TurnID != "t1" || turn.StopReason != "end_turn" {
		t.Fatalf("turn = %+v", turn)
	}
	var assistant core.AssistantMessage
	if err := json.Unmarshal(events[1].Payload, &assistant); err != nil {
		t.Fatal(err)
	}
	var final string
	for _, block := range assistant.Content {
		if block.Kind == core.BlockText {
			final += block.Text
		}
	}
	if text.String() != final || final != "hello" {
		t.Fatalf("frames = %q, last assistant message = %q", text.String(), final)
	}
}

func TestStreamReplayFromSeqAndLastEventID(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindUserMessage, Payload: json.RawMessage(`{"v":1}`)})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindAssistantMessage, Payload: json.RawMessage(`{"v":1}`)})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindTurnEnd, Payload: json.RawMessage(`{"v":1,"turn_id":"t1","reason":"end_turn"}`)})
	s, ts, cookie, _ := newStreamTestServer(t, backend)
	s.SetHeartbeatInterval(20 * time.Millisecond)
	s.SetStreamPollInterval(20 * time.Millisecond)

	resp := openStream(t, ts, "/v1/sessions/"+info.ID+"/stream?from_seq=1", cookie, "")
	reader := bufio.NewReader(resp.Body)
	if status := waitSSE(t, reader, "status"); status.Data == "" {
		t.Fatal("status data is empty")
	}
	first := waitSSE(t, reader, "event")
	if first.ID != "2" {
		t.Fatalf("first replay id = %q, want 2", first.ID)
	}
	second := waitSSE(t, reader, "event")
	if second.ID != "3" {
		t.Fatalf("second replay id = %q, want 3", second.ID)
	}
	turn := waitSSE(t, reader, "turn")
	if turn.Data == "" {
		t.Fatal("turn data is empty")
	}
	_ = resp.Body.Close()

	resp = openStream(t, ts, "/v1/sessions/"+info.ID+"/stream?from_seq=0&ignored=1", cookie, "2")
	reader = bufio.NewReader(resp.Body)
	waitSSE(t, reader, "status")
	last := waitSSE(t, reader, "event")
	if last.ID != "3" {
		t.Fatalf("Last-Event-ID replay id = %q, want 3", last.ID)
	}
	_ = resp.Body.Close()

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/"+info.ID+"/stream?from_seq=nope", "", cookie, ""))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/missing/stream", "", cookie, ""))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

func TestStreamHeartbeatAndShutdown(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	s, ts, cookie, _ := newStreamTestServer(t, backend)
	s.SetHeartbeatInterval(10 * time.Millisecond)

	resp := openStream(t, ts, "/v1/sessions/"+info.ID+"/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	waitSSE(t, reader, "status")
	ping := waitSSE(t, reader, "ping")
	if ping.Comment != "ping" {
		t.Fatalf("heartbeat = %+v", ping)
	}

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	shutdown := waitSSE(t, reader, "shutdown")
	if strings.TrimSpace(shutdown.Data) != "{}" {
		t.Fatalf("shutdown data = %q", shutdown.Data)
	}
}

// AC-ASK-021: an open question reaches the live stream and is replayed to a
// stream that connects while it is still open.
func TestQuestionSSEEventAndReplay(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	s, ts, cookie, csrf := newStreamTestServer(t, backend)

	resp := openStream(t, ts, "/v1/sessions/"+info.ID+"/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	waitSSE(t, reader, "status")

	outcomes := make(chan core.QuestionOutcome, 1)
	go func() {
		outcome, _ := s.Questions().Ask(context.Background(), core.QuestionRequest{
			SessionID: info.ID, Questions: questionFixture(),
		})
		outcomes <- outcome
	}()
	live := decodeQuestionEvent(t, waitSSE(t, reader, "question"))
	if live.RequestID == "" {
		t.Fatal("question event has no request_id")
	}
	if len(live.Questions) != 1 || live.Questions[0].Question != questionFixture()[0].Text {
		t.Fatalf("question event = %+v", live)
	}
	if live.Questions[0].Header != "" || live.Questions[0].MultiSelect {
		t.Fatalf("question payload = %+v", live.Questions[0])
	}
	if len(live.Questions[0].Options) != 3 || live.Questions[0].Options[2].Description != "" {
		t.Fatalf("options = %+v", live.Questions[0].Options)
	}
	if live.ExpiresAt < time.Now().Add(29*time.Minute).UnixMilli() {
		t.Fatalf("expires_at = %d, want about thirty minutes from now", live.ExpiresAt)
	}

	// A second stream connected while the request is open gets it replayed.
	second := openStream(t, ts, "/v1/sessions/"+info.ID+"/stream", cookie, "")
	defer func() { _ = second.Body.Close() }()
	secondReader := bufio.NewReader(second.Body)
	waitSSE(t, secondReader, "status")
	replayed := decodeQuestionEvent(t, waitSSE(t, secondReader, "question"))
	if !reflect.DeepEqual(replayed, live) {
		t.Fatalf("replayed event = %+v, want %+v", replayed, live)
	}

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/questions/"+live.RequestID, `{"answers":[{"question":0,"selected":[0]}]}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("answer status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if outcome := waitQuestionOutcome(t, outcomes); outcome.Status != core.QuestionStatusAnswered {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// runtimeStreamBackend adapts a real *lana.Runtime to the stream handlers so
// AC-WEB-012 runs against MockLlm, not a scripted handler fake.
type runtimeStreamBackend struct {
	*fakeWebBackend
	rt *lana.Runtime
}

func (b *runtimeStreamBackend) Session(ctx context.Context, sessionID string) (Session, error) {
	info, err := b.rt.SessionInfo(ctx, sessionID)
	if err != nil {
		return Session{}, ErrSessionNotFound
	}
	policy, err := b.rt.Policy(ctx, info.ID)
	if err != nil {
		return Session{}, err
	}
	return Session{SessionInfo: info, Model: b.rt.AgentModel(info.Agent), Policy: string(policy), TurnRunning: info.Status == "running"}, nil
}

func (b *runtimeStreamBackend) Events(ctx context.Context, sessionID string, fromSeq int64) (EventPage, error) {
	info, err := b.rt.SessionInfo(ctx, sessionID)
	if err != nil {
		return EventPage{}, ErrSessionNotFound
	}
	events, err := b.rt.History(ctx, info.ID)
	if err != nil {
		return EventPage{}, err
	}
	page := EventPage{Events: make([]core.SessionEvent, 0, len(events)), LastSeq: info.LastSeq}
	for _, event := range events {
		if event.Seq > page.LastSeq {
			page.LastSeq = event.Seq
		}
		if event.Seq > fromSeq {
			page.Events = append(page.Events, event)
		}
	}
	return page, nil
}

func (b *runtimeStreamBackend) Subscribe(ctx context.Context, sessionID string) (*core.Subscription[core.LiveFrame], error) {
	return b.rt.SubscribeLive(ctx, sessionID)
}

func (b *runtimeStreamBackend) TurnActive(sessionID string) bool {
	info, err := b.rt.SessionInfo(context.Background(), sessionID)
	return err == nil && info.Status == "running"
}

func (b *runtimeStreamBackend) Holder(ctx context.Context, sessionID string) (Holder, error) {
	pid, held, err := b.rt.LeaseHolder(ctx, sessionID)
	if err != nil {
		return Holder{}, err
	}
	if held {
		return Holder{State: HolderOther, PID: pid}, nil
	}
	return Holder{State: HolderNone}, nil
}

func TestStreamWithMockLlm(t *testing.T) {
	text, end := "hello", "end_turn"
	layer := `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.assistant]
provider = "mock"
model = "m"

[[routes]]
channel = "web"
agent = "assistant"
`
	rt, err := lana.New(context.Background(), lana.Options{
		Home:      t.TempDir(),
		Layers:    []config.Layer{{Name: "test", TOML: []byte(layer)}},
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{Responses: []mock.Response{{Events: []mock.Event{{Text: &text}, {Stop: &end}}}}})},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	info, err := rt.CreateChannelSession(context.Background(), "web", "owner", "assistant")
	if err != nil {
		t.Fatal(err)
	}

	backend := &runtimeStreamBackend{fakeWebBackend: newFakeWebBackend(), rt: rt}
	s, ts, cookie, _ := newStreamTestServer(t, backend)
	s.SetHeartbeatInterval(20 * time.Millisecond)
	s.SetStreamPollInterval(20 * time.Millisecond)

	resp := openStream(t, ts, "/v1/sessions/"+info.ID+"/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	waitSSE(t, reader, "status")

	go func() {
		_, _ = rt.Run(context.Background(), lana.Request{Prompt: "hello", SessionID: info.ID})
	}()

	var frames strings.Builder
	var lastAssistant string
	var stopReason string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && stopReason == "" {
		block := nextSSEBlock(t, reader)
		switch block.Name {
		case "frame":
			var frame frameJSON
			if err := json.Unmarshal([]byte(block.Data), &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Kind == "text_delta" {
				frames.WriteString(frame.Text)
			}
		case "event":
			var event eventJSON
			if err := json.Unmarshal([]byte(block.Data), &event); err != nil {
				t.Fatal(err)
			}
			if event.Kind == core.KindAssistantMessage {
				var message core.AssistantMessage
				if err := json.Unmarshal(event.Payload, &message); err != nil {
					t.Fatal(err)
				}
				for _, content := range message.Content {
					if content.Kind == core.BlockText {
						lastAssistant += content.Text
					}
				}
			}
		case "turn":
			var turn turnJSON
			if err := json.Unmarshal([]byte(block.Data), &turn); err != nil {
				t.Fatal(err)
			}
			stopReason = turn.StopReason
		}
	}
	if stopReason != "end_turn" {
		t.Fatalf("turn stop reason = %q", stopReason)
	}
	if frames.String() != "hello" || lastAssistant != frames.String() {
		t.Fatalf("frames = %q, last assistant message = %q", frames.String(), lastAssistant)
	}
}
