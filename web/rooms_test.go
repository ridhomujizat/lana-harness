package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

func roomsForTest(t *testing.T, s *Server, cookie *http.Cookie) roomsResponse {
	t.Helper()
	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/rooms", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/rooms status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body roomsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /v1/rooms body %q: %v", rr.Body.String(), err)
	}
	return body
}

func roomRowForTest(t *testing.T, s *Server, cookie *http.Cookie, name string) roomJSON {
	t.Helper()
	for _, row := range roomsForTest(t, s, cookie).Rooms {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("room %q is missing", name)
	return roomJSON{}
}

func segmentsForTest(t *testing.T, s *Server, cookie *http.Cookie, target string) segmentsResponse {
	t.Helper()
	rr := serveAuth(s, sessionTestRequest(http.MethodGet, target, "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, body = %q", target, rr.Code, rr.Body.String())
	}
	var body segmentsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET %s body %q: %v", target, rr.Body.String(), err)
	}
	return body
}

// AC-ROOM-001: the list is one row per agent and team, sorted by last activity,
// with the newest segment preview.
func TestRoomsSortAndPreview(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{
		{Name: "a", Kind: "agent"},
		{Name: "b", Kind: "agent"},
		{Name: "t", Kind: "team", Lead: "a", Members: []string{"a"}},
	}
	aSession := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "a", Channel: "telegram", Address: "1", CreatedAt: 1000, UpdatedAt: 1000})
	addSessionEvent(t, backend, aSession.ID, core.KindAssistantMessage, core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "older a"}}})
	bSession := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "b", Channel: "web", Address: "owner", CreatedAt: 3000, UpdatedAt: 3000})
	addSessionEvent(t, backend, bSession.ID, core.KindAssistantMessage, core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "  hello   b "}}})

	s, cookie, _ := newLoggedInSessionServer(t, backend)
	body := roomsForTest(t, s, cookie)
	if len(body.Rooms) != 3 {
		t.Fatalf("rooms = %d, want 3: %+v", len(body.Rooms), body.Rooms)
	}
	if body.Rooms[0].Name != "b" || body.Rooms[1].Name != "a" || body.Rooms[2].Name != "t" {
		t.Fatalf("room order = %q, %q, %q", body.Rooms[0].Name, body.Rooms[1].Name, body.Rooms[2].Name)
	}
	b := body.Rooms[0]
	if b.LastActiveAt != 3000 || b.LastMessage == nil || b.LastMessage.Preview != "hello b" ||
		b.LastMessage.Role != "assistant" || b.LastMessage.Channel != "web" {
		t.Fatalf("b row = %+v, last_message = %+v", b, b.LastMessage)
	}
	tm := body.Rooms[2]
	if tm.Kind != "team" || tm.Color != "brand" || tm.Lead != "a" || tm.LastActiveAt != 0 || tm.LastMessage != nil {
		t.Fatalf("team row = %+v", tm)
	}
	if len(tm.Members) != 1 || tm.Members[0] != "a" {
		t.Fatalf("team members = %+v", tm.Members)
	}
	if body.Rooms[1].Color == "" {
		t.Fatalf("agent color is empty: %+v", body.Rooms[1])
	}
}

// AC-ROOM-004: sending to a room with no web session creates one and starts the
// turn there.
func TestRoomSendCreatesWebSession(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agentNames["dana"] = true
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/dana/messages", `{"text":"halo"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body roomSendResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !body.Created || body.SessionID == "" || body.Turn != "started" {
		t.Fatalf("response = %+v", body)
	}
	backend.mu.Lock()
	info, ok := backend.find(body.SessionID)
	texts := append([]string(nil), backend.sentText[body.SessionID]...)
	backend.mu.Unlock()
	if !ok || info.Channel != "web" || info.Address != "owner" || info.Agent != "dana" {
		t.Fatalf("created session = %+v", info)
	}
	if len(texts) != 1 || texts[0] != "halo" {
		t.Fatalf("sent texts = %+v", texts)
	}
}

// AC-ROOM-005: new_topic creates a web session before sending.
func TestRoomSendNewTopic(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agentNames["dana"] = true
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	existing := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "web", Address: "owner", CreatedAt: 1000, UpdatedAt: 1000})
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/dana/messages", `{"text":"hi"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var reused roomSendResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &reused); err != nil {
		t.Fatal(err)
	}
	if reused.Created || reused.SessionID != existing.ID {
		t.Fatalf("reused = %+v, want session %s", reused, existing.ID)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/dana/messages", `{"text":"new","new_topic":true}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("new topic status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var created roomSendResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Created || created.SessionID == existing.ID || created.SessionID == "" {
		t.Fatalf("new topic = %+v", created)
	}
}

// AC-ROOM-007: unread counts assistant answers and questions after the last
// read, and the read mark resets it.
func TestRoomUnreadAndRead(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	info := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "telegram", Address: "1", CreatedAt: 500, UpdatedAt: 6000})
	backend.mu.Lock()
	backend.roomReads["dana"] = 1000
	backend.mu.Unlock()
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindAssistantMessage, CreatedAt: 5000, Payload: json.RawMessage(`{"v":1,"content":[{"kind":"text","text":"one"}]}`)})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindAssistantMessage, CreatedAt: 5000, Payload: json.RawMessage(`{"v":1,"content":[{"kind":"text","text":"two"}]}`)})

	s, cookie, csrf := newLoggedInSessionServer(t, backend)
	if row := roomRowForTest(t, s, cookie, "dana"); row.Unread != 2 {
		t.Fatalf("unread = %d, want 2", row.Unread)
	}
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/dana/read", "", cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("read status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var read roomReadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if read.ReadAt <= 5000 {
		t.Fatalf("read_at = %d, want past the events", read.ReadAt)
	}
	if row := roomRowForTest(t, s, cookie, "dana"); row.Unread != 0 {
		t.Fatalf("unread after read = %d, want 0", row.Unread)
	}
	// A question after the read counts too (REQ-ROOM-005).
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindQuestionAsked, CreatedAt: read.ReadAt + 1, Payload: json.RawMessage(`{"v":1,"request_id":"q1"}`)})
	if row := roomRowForTest(t, s, cookie, "dana"); row.Unread != 1 {
		t.Fatalf("unread after question = %d, want 1", row.Unread)
	}
}

// AC-ROOM-008: an open approval wins over a running turn.
func TestPresenceWaitingBeatsWorking(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	info := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "web", Address: "owner", Status: "running", CreatedAt: 1000, UpdatedAt: 2000})
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = s.Approvals().Ask(ctx, core.ApprovalRequest{SessionID: info.ID, ToolName: "bash", Risk: core.RiskExec})
	}()
	waitForOpenApproval(t, s)

	row := roomRowForTest(t, s, cookie, "dana")
	if row.Presence.State != "waiting" || !strings.Contains(row.Presence.Label, "bash") {
		t.Fatalf("presence = %+v, want waiting on bash", row.Presence)
	}

	cancel()
	waitForNoApproval(t, s)
	row = roomRowForTest(t, s, cookie, "dana")
	if row.Presence.State != "working" {
		t.Fatalf("presence = %+v, want working", row.Presence)
	}
}

// AC-ROOM-009: a failed newest turn is problem with the error summary.
func TestPresenceProblem(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	info := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "telegram", Address: "1", CreatedAt: 1000, UpdatedAt: 3000})
	addSessionEvent(t, backend, info.ID, core.KindTurnEnd, core.TurnEnd{V: 1, TurnID: "t1", Reason: "error", Error: &core.PayloadError{Class: "provider", Message: "provider returned 429"}})

	s, cookie, _ := newLoggedInSessionServer(t, backend)
	row := roomRowForTest(t, s, cookie, "dana")
	if row.Presence.State != "problem" || !strings.Contains(row.Presence.Label, "provider returned 429") {
		t.Fatalf("presence = %+v", row.Presence)
	}
}

// AC-ROOM-010: no activity for 7 hours and no open request is resting.
func TestPresenceResting(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "telegram", Address: "1", CreatedAt: 1000, UpdatedAt: time.Now().Add(-7 * time.Hour).UnixMilli()})

	s, cookie, _ := newLoggedInSessionServer(t, backend)
	row := roomRowForTest(t, s, cookie, "dana")
	if row.Presence.State != "resting" {
		t.Fatalf("presence = %+v, want resting", row.Presence)
	}
	if !strings.Contains(row.Presence.Label, "h ago") {
		t.Fatalf("label = %q, want a relative time", row.Presence.Label)
	}
}

// AC-ROOM-013: a leading @member mention on a team room adds a runtime note.
func TestRoomSendMentionNote(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agentNames["squad"] = true
	backend.agents = []Agent{{Name: "squad", Kind: "team", Lead: "lana", Members: []string{"k", "d"}}}
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/squad/messages", `{"text":"@k cek gaji"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body roomSendResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	note := backend.sentNotes[body.SessionID]
	texts := append([]string(nil), backend.sentText[body.SessionID]...)
	backend.mu.Unlock()
	if !strings.Contains(note, "Hand this request to k") {
		t.Fatalf("note = %q", note)
	}
	if len(texts) != 1 || texts[0] != "@k cek gaji" {
		t.Fatalf("stored texts = %+v", texts)
	}

	// A mention that is not a member gets no note.
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/squad/messages", `{"text":"@zzz hi","new_topic":true}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("non-member status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var second roomSendResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	secondNote := backend.sentNotes[second.SessionID]
	backend.mu.Unlock()
	if secondNote != "" {
		t.Fatalf("non-member note = %q, want none", secondNote)
	}
}

// AC-ROOM-011: the room stream sends a changed row within a tick.
func TestRoomStreamSendsChangedRow(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	info := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "web", Address: "owner", CreatedAt: 1000, UpdatedAt: 1000})
	s, ts, cookie, _ := newStreamTestServer(t, backend)
	s.SetRoomTickInterval(20 * time.Millisecond)

	resp := openStream(t, ts, "/v1/rooms/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	first := nextSSEBlock(t, reader)
	if first.Name != "rooms" || !strings.Contains(first.Data, `"dana"`) {
		t.Fatalf("first event = %+v", first)
	}

	backend.setTurnActive(info.ID, true)
	backend.mu.Lock()
	backend.update(info.ID, func(info *core.SessionInfo) { info.Status = "running" })
	backend.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		block := nextSSEBlock(t, reader)
		if block.Name == "room" && strings.Contains(block.Data, `"state":"working"`) {
			return
		}
	}
	t.Fatal("no working room event")
}

func TestRoomSegmentsPaging(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	byCreated := map[int64]string{}
	for _, created := range []int64{100, 200, 300, 400, 500, 600} {
		info := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "web", Address: "owner", CreatedAt: created, UpdatedAt: created})
		byCreated[created] = info.ID
	}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	page := segmentsForTest(t, s, cookie, "/v1/rooms/dana/segments?limit=2")
	if !page.HasMore || len(page.Segments) != 2 || page.Segments[0].SessionID != byCreated[500] || page.Segments[1].SessionID != byCreated[600] {
		t.Fatalf("first page = %+v", page)
	}

	page = segmentsForTest(t, s, cookie, "/v1/rooms/dana/segments?limit=2&before=500")
	if !page.HasMore || len(page.Segments) != 2 || page.Segments[0].SessionID != byCreated[300] || page.Segments[1].SessionID != byCreated[400] {
		t.Fatalf("second page = %+v", page)
	}

	page = segmentsForTest(t, s, cookie, "/v1/rooms/dana/segments?limit=10&before=300")
	if page.HasMore || len(page.Segments) != 2 || page.Segments[0].SessionID != byCreated[100] || page.Segments[1].SessionID != byCreated[200] {
		t.Fatalf("third page = %+v", page)
	}
}

func TestRoomsNotFoundAndBadQuery(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agentNames["dana"] = true
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	for _, target := range []string{
		"/v1/rooms/missing/segments",
		"/v1/rooms/missing/read",
	} {
		method := http.MethodGet
		if strings.HasSuffix(target, "/read") {
			method = http.MethodPost
		}
		rr := serveAuth(s, sessionTestRequest(method, target, "", cookie, csrf))
		assertAuthError(t, rr, http.StatusNotFound, "room_not_found")
	}
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/missing/messages", `{"text":"hi"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "room_not_found")

	for _, target := range []string{
		"/v1/rooms/dana/segments?limit=0",
		"/v1/rooms/dana/segments?limit=21",
		"/v1/rooms/dana/segments?limit=x",
		"/v1/rooms/dana/segments?before=x",
		"/v1/rooms/dana/segments?before=-1",
	} {
		rr := serveAuth(s, sessionTestRequest(http.MethodGet, target, "", cookie, ""))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_query")
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/dana/messages", `{"text":"   "}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "empty_message")

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/dana/read", "", cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("read status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func waitForOpenApproval(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.approvals.openRequests()) > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("approval never opened")
}

func waitForNoApproval(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.approvals.openRequests()) == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("approval never closed")
}
