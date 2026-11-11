package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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

// TestRoomRowDisplayName covers the display_name field of a room row: an agent
// with a configured display_name carries it, a team row leaves it empty.
func TestRoomRowDisplayName(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{
		{Name: "coder", Kind: "agent"},
		{Name: "squad", Kind: "team", Lead: "coder", Members: []string{"coder"}},
	}
	s, cookie, _ := newLoggedInSessionServer(t, backend)
	s.SetConfigStore(newTestConfigStore(t, "[agents.coder]\ndisplay_name = \"Omnix Lead\"\n", 0o600, ConfigSettings{}))

	if row := roomRowForTest(t, s, cookie, "coder"); row.DisplayName != "Omnix Lead" {
		t.Fatalf("agent display_name = %q, want Omnix Lead", row.DisplayName)
	}
	if row := roomRowForTest(t, s, cookie, "squad"); row.DisplayName != "" {
		t.Fatalf("team display_name = %q, want empty", row.DisplayName)
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

// API-ROOM-003: the room send threads reply_to to the backend.
func TestRoomSendReplyTo(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agentNames["dana"] = true
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/dana/messages", `{"text":"halo","reply_to":9}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body roomSendResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	backend.mu.Lock()
	replyTo := backend.sentReplyTo[body.SessionID]
	backend.mu.Unlock()
	if replyTo != 9 {
		t.Fatalf("reply_to = %d, want 9", replyTo)
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

// REQ-ROOM-002, AC-ROOM-002: the page is the most recently active segments, and
// the conversation the owner continues (created long ago but active today) is
// last and is not dropped.
func TestRoomSegmentsOrderByLastActivity(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	byUpdated := map[int64]string{}
	for _, s := range []struct{ created, updated int64 }{
		{100, 900}, // created first, active last
		{200, 800},
		{300, 700},
		{400, 600},
	} {
		info := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "web", Address: "owner", CreatedAt: s.created, UpdatedAt: s.updated})
		byUpdated[s.updated] = info.ID
	}
	srv, cookie, _ := newLoggedInSessionServer(t, backend)

	page := segmentsForTest(t, srv, cookie, "/v1/rooms/dana/segments?limit=2")
	if !page.HasMore || len(page.Segments) != 2 {
		t.Fatalf("page = %+v, want 2 segments and has_more", page)
	}
	if page.Segments[0].SessionID != byUpdated[800] || page.Segments[1].SessionID != byUpdated[900] {
		t.Fatalf("page = %+v, want the newest-active segment last", page.Segments)
	}
}

// Bug: a workflow session of an agent belongs to the Workflows page, so it is
// neither a chat segment nor the room's last message, while its web session is.
func TestRoomSegmentsExcludeWorkflow(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	wf := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "workflow", Address: "wf1", CreatedAt: 1000, UpdatedAt: 3000})
	addSessionEvent(t, backend, wf.ID, core.KindUserMessage, core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "Here is the raw content of a scraped page"}}})
	web := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "web", Address: "owner", CreatedAt: 500, UpdatedAt: 2000})
	addSessionEvent(t, backend, web.ID, core.KindAssistantMessage, core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hello from web"}}})

	s, cookie, _ := newLoggedInSessionServer(t, backend)

	page := segmentsForTest(t, s, cookie, "/v1/rooms/dana/segments")
	if len(page.Segments) != 1 || page.Segments[0].SessionID != web.ID {
		t.Fatalf("segments = %+v, want only the web session %s", page.Segments, web.ID)
	}

	row := roomRowForTest(t, s, cookie, "dana")
	if row.LastActiveAt != 2000 {
		t.Fatalf("last_active_at = %d, want the web session 2000", row.LastActiveAt)
	}
	if row.LastMessage == nil || row.LastMessage.Preview != "hello from web" || row.LastMessage.Channel != "web" {
		t.Fatalf("last_message = %+v, want the web message", row.LastMessage)
	}
}

// REQ-ROOM-002, AC-ROOM-003: paging by last activity returns the next older
// page with no overlap and no missing segment.
func TestRoomSegmentsPageByLastActivity(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	byUpdated := map[int64]string{}
	for _, s := range []struct{ created, updated int64 }{
		{100, 900},
		{200, 800},
		{300, 700},
		{400, 600},
		{500, 500},
	} {
		info := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "web", Address: "owner", CreatedAt: s.created, UpdatedAt: s.updated})
		byUpdated[s.updated] = info.ID
	}
	srv, cookie, _ := newLoggedInSessionServer(t, backend)

	first := segmentsForTest(t, srv, cookie, "/v1/rooms/dana/segments?limit=2")
	if !first.HasMore || len(first.Segments) != 2 {
		t.Fatalf("first page = %+v", first)
	}
	if first.Segments[0].SessionID != byUpdated[800] || first.Segments[1].SessionID != byUpdated[900] {
		t.Fatalf("first page = %+v", first.Segments)
	}
	// The oldest segment in the page is the cursor for the next page.
	before := first.Segments[0].UpdatedAt

	second := segmentsForTest(t, srv, cookie, "/v1/rooms/dana/segments?limit=2&before="+strconv.FormatInt(before, 10))
	if !second.HasMore || len(second.Segments) != 2 {
		t.Fatalf("second page = %+v", second)
	}
	if second.Segments[0].SessionID != byUpdated[600] || second.Segments[1].SessionID != byUpdated[700] {
		t.Fatalf("second page = %+v", second.Segments)
	}
	seen := map[string]bool{}
	for _, seg := range first.Segments {
		seen[seg.SessionID] = true
	}
	for _, seg := range second.Segments {
		if seen[seg.SessionID] {
			t.Fatalf("segment %s appears on both pages", seg.SessionID)
		}
		seen[seg.SessionID] = true
	}
	for _, id := range []string{byUpdated[600], byUpdated[700], byUpdated[800], byUpdated[900]} {
		if !seen[id] {
			t.Fatalf("segment %s is missing from both pages", id)
		}
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

// userMessageText (the room list preview) hides the per-attachment text blocks
// attach writes and summarizes them as a trailing "· N files" marker.
func TestRoomPreviewAttachmentCount(t *testing.T) {
	notes := "Attachment attachments/2026-11-09/notes.md (text/markdown):\n# Notes"
	scan := "Attachment attachments/2026-11-09/scan.pdf (PDF, 2 pages) has no text layer. It is probably a scan: ask the owner to send photos or screenshots of the pages instead."
	cases := []struct {
		name string
		msg  core.UserMessage
		want string
	}{
		{
			name: "context and text with two attachments",
			msg: core.UserMessage{V: 1, ContextBlocks: 1, Content: []core.ContentBlock{
				{Kind: core.BlockText, Text: "<context>injected</context>"},
				{Kind: core.BlockText, Text: "pelajari materi ini"},
				{Kind: core.BlockText, Text: notes},
				{Kind: core.BlockText, Text: scan},
			}},
			want: "pelajari materi ini · 2 files",
		},
		{
			name: "attachments only",
			msg: core.UserMessage{V: 1, Content: []core.ContentBlock{
				{Kind: core.BlockText, Text: notes},
				{Kind: core.BlockText, Text: scan},
			}},
			want: "2 files",
		},
		{
			name: "files-only placeholder",
			msg: core.UserMessage{V: 1, Content: []core.ContentBlock{
				{Kind: core.BlockText, Text: "(attachments)"},
				{Kind: core.BlockText, Text: notes},
			}},
			want: "1 file",
		},
		{
			name: "one image",
			msg: core.UserMessage{V: 1, Content: []core.ContentBlock{
				{Kind: core.BlockImage, MediaType: "image/png", Path: "attachments/x.png"},
			}},
			want: "1 file",
		},
		{
			name: "plain text",
			msg:  core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "halo"}}},
			want: "halo",
		},
		{
			name: "empty",
			msg:  core.UserMessage{V: 1},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			backend := newFakeWebBackend()
			backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
			info := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "web", Address: "owner", CreatedAt: 1000, UpdatedAt: 1000})
			addSessionEvent(t, backend, info.ID, core.KindUserMessage, c.msg)
			s, cookie, _ := newLoggedInSessionServer(t, backend)
			row := roomRowForTest(t, s, cookie, "dana")
			got := ""
			if row.LastMessage != nil {
				got = row.LastMessage.Preview
			}
			if got != c.want {
				t.Fatalf("preview = %q, want %q", got, c.want)
			}
		})
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

// TestRoomSendDiscussNote covers API-RDS-013: Discuss on adds the runtime note.
func TestRoomSendDiscussNote(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agentNames["squad"] = true
	backend.agents = []Agent{{Name: "squad", Kind: "team", Lead: "lana", Members: []string{"k", "d"}}}
	s, cookie, csrf := newLoggedInSessionServer(t, backend)
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/squad/messages", `{"text":"ambil ga?","discuss":true}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body roomSendResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	note := backend.sentNotes[body.SessionID]
	backend.mu.Unlock()
	if note != core.DiscussRequestNote {
		t.Fatalf("note = %q", note)
	}
}

// An agent at work in a team room shows where in its own chat row, not a
// bare "Thinking", so the owner's chat with it does not look busy.
func TestPresenceWorkingInTeamRoom(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "karin", Kind: "agent"}, {Name: "template-email", Kind: "team"}}
	room := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "template-email", Channel: "web", Address: "owner", Status: "running", CreatedAt: 1000, UpdatedAt: 2000})
	backend.addSession(core.SessionInfo{ID: id.New(), Agent: "karin", Channel: "web", Address: "owner", Status: "running", ParentSessionID: room.ID, CreatedAt: 1000, UpdatedAt: 2000})
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	row := roomRowForTest(t, s, cookie, "karin")
	if row.Presence.State != "working" || row.Presence.Label != "Working in template-email" {
		t.Fatalf("presence = %+v, want working in template-email", row.Presence)
	}
}

// The agent's own chat running beside its team room work shows its own
// status: the two run in parallel.
func TestPresenceOwnChatBeatsTeamRoom(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "karin", Kind: "agent"}, {Name: "template-email", Kind: "team"}}
	room := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "template-email", Channel: "web", Address: "owner", Status: "running", CreatedAt: 1000, UpdatedAt: 2000})
	backend.addSession(core.SessionInfo{ID: id.New(), Agent: "karin", Channel: "web", Address: "owner", Status: "running", CreatedAt: 1000, UpdatedAt: 1500})
	backend.addSession(core.SessionInfo{ID: id.New(), Agent: "karin", Channel: "web", Address: "owner", Status: "running", ParentSessionID: room.ID, CreatedAt: 1000, UpdatedAt: 3000})
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	row := roomRowForTest(t, s, cookie, "karin")
	if row.Presence.State != "working" || strings.HasPrefix(row.Presence.Label, "Working in") {
		t.Fatalf("presence = %+v, want the own chat's status", row.Presence)
	}
}
