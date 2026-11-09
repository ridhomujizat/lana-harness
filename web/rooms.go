package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/toolfmt"
)

const (
	// defaultRoomTickInterval is how often the room hub recomputes the rows
	// (API-ROOM-005).
	defaultRoomTickInterval = 1500 * time.Millisecond
	// roomPreviewLimit is the maximum length of a room's last-message preview
	// (API-ROOM-001).
	roomPreviewLimit = 140
	// roomLabelLimit is the maximum length of a presence label (REQ-ROOM-006).
	roomLabelLimit = 80
	// defaultSegmentLimit and maxSegmentLimit follow API-ROOM-002.
	defaultSegmentLimit = 5
	maxSegmentLimit     = 20
	// roomOnlineWindowMS is the activity window of the online state (REQ-ROOM-006,
	// Q-002).
	roomOnlineWindowMS = int64(6 * 60 * 60 * 1000)
	// roomWorkingScan is how many recent sessions the presence scan looks at.
	roomWorkingScan = 20
	// roomSubscriberBuffer is the per-stream queue of room events; a subscriber
	// whose buffer is full is dropped and reconnects (API-ROOM-005).
	roomSubscriberBuffer = 16
	roomSendBody         = 1 << 20
	roomWeekMS           = int64(7 * 24 * 60 * 60 * 1000)
)

// presenceJSON is the shared presence object of API-ROOM-001 (REQ-ROOM-006).
type presenceJSON struct {
	State     string `json:"state"`
	Label     string `json:"label"`
	Since     int64  `json:"since"`
	SessionID string `json:"session_id"`
}

// roomMessageJSON is the last_message object of API-ROOM-001.
type roomMessageJSON struct {
	Role      string `json:"role"`
	Agent     string `json:"agent"`
	Preview   string `json:"preview"`
	At        int64  `json:"at"`
	SessionID string `json:"session_id"`
	Channel   string `json:"channel"`
}

// roomJSON is one row of API-ROOM-001.
type roomJSON struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// DisplayName is the human name shown in the UI, "" when unset.
	DisplayName string   `json:"display_name"`
	Color       string   `json:"color"`
	Members     []string `json:"members"`
	Lead        string   `json:"lead"`
	// Presence is derived, never stored (REQ-ROOM-006).
	Presence     presenceJSON     `json:"presence"`
	LastMessage  *roomMessageJSON `json:"last_message"`
	Unread       int64            `json:"unread"`
	Turns7d      int64            `json:"turns_7d"`
	LastActiveAt int64            `json:"last_active_at"`
}

type roomsResponse struct {
	Rooms []roomJSON `json:"rooms"`
}

// segmentJSON is one segment of API-ROOM-002.
type segmentJSON struct {
	SessionID string `json:"session_id"`
	Channel   string `json:"channel"`
	Address   string `json:"address"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	TurnCount int64  `json:"turn_count"`
	Status    string `json:"status"`
}

type segmentsResponse struct {
	Segments []segmentJSON `json:"segments"`
	HasMore  bool          `json:"has_more"`
}

type roomSendResponse struct {
	SessionID string `json:"session_id"`
	Created   bool   `json:"created"`
	Turn      string `json:"turn"`
	TurnID    string `json:"turn_id,omitempty"`
}

type roomReadResponse struct {
	ReadAt int64 `json:"read_at"`
}

// openRequest is one open approval or question as the room presence needs it
// (REQ-ROOM-006). SessionID is the session that opened the request; Agent is
// the child-session tag when the request belongs to a descendant (empty
// otherwise).
type openRequest struct {
	Kind      string
	SessionID string
	Agent     string
	Tool      string
	Since     int64
}

// registerRoomRoutes registers API-ROOM-001 through API-ROOM-005 through Handle
// so the auth, Host, Origin, and CSRF checks apply.
func (s *Server) registerRoomRoutes() {
	s.Handle("GET /v1/rooms", http.HandlerFunc(s.handleListRooms))
	s.Handle("GET /v1/rooms/stream", http.HandlerFunc(s.handleRoomsStream))
	s.Handle("GET /v1/rooms/{name}/segments", http.HandlerFunc(s.handleRoomSegments))
	s.Handle("POST /v1/rooms/{name}/messages", http.HandlerFunc(s.handleRoomSend))
	s.Handle("POST /v1/rooms/{name}/read", http.HandlerFunc(s.handleRoomRead))
}

// handleListRooms serves API-ROOM-001 (REQ-ROOM-001, REQ-ROOM-005,
// REQ-ROOM-006).
func (s *Server) handleListRooms(w http.ResponseWriter, r *http.Request) {
	if s.backend == nil {
		s.backendOrError(w)
		return
	}
	rows, err := s.computeRooms(r.Context())
	if err != nil {
		s.logger.Error("web: rooms list", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, roomsResponse{Rooms: rows})
}

// handleRoomSegments serves API-ROOM-002 (REQ-ROOM-002).
func (s *Server) handleRoomSegments(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	name := r.PathValue("name")
	if !roomExists(backend, name) {
		writeAPIError(w, http.StatusNotFound, "room_not_found", "room not found")
		return
	}
	limit, before, err := parseSegmentQuery(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	sessions, err := backend.ListAgentSessions(r.Context(), core.SessionFilter{
		Agent:        name,
		TopLevelOnly: true,
		Before:       before,
		Limit:        limit + 1,
	})
	if err != nil {
		s.writeBackendError(w, "room segments", err)
		return
	}
	// The page is the limit most recently active segments; the store already
	// returns them newest first (API-ROOM-002).
	hasMore := len(sessions) > limit
	if hasMore {
		sessions = sessions[:limit]
	}
	// The page is returned in ascending updated_at order, ties by created_at
	// then id, so the newest-active segment is last (API-ROOM-002).
	sort.SliceStable(sessions, func(i, j int) bool {
		a, b := sessions[i], sessions[j]
		if a.UpdatedAt != b.UpdatedAt {
			return a.UpdatedAt < b.UpdatedAt
		}
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt
		}
		return a.ID < b.ID
	})
	segments := make([]segmentJSON, 0, len(sessions))
	for _, info := range sessions {
		status := info.Status
		if status == "" {
			status = SessionStatusIdle
		}
		segments = append(segments, segmentJSON{
			SessionID: info.ID,
			Channel:   info.Channel,
			Address:   info.Address,
			CreatedAt: info.CreatedAt,
			UpdatedAt: info.UpdatedAt,
			TurnCount: info.TurnCount,
			Status:    status,
		})
	}
	writeJSON(w, http.StatusOK, segmentsResponse{Segments: segments, HasMore: hasMore})
}

// handleRoomSend serves API-ROOM-003 (REQ-ROOM-003, REQ-ROOM-008).
func (s *Server) handleRoomSend(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	name := r.PathValue("name")
	room, ok := findRoom(backend, name)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "room_not_found", "room not found")
		return
	}
	var body struct {
		Text        *string  `json:"text"`
		Attachments []string `json:"attachments"`
		NewTopic    bool     `json:"new_topic"`
	}
	if !decodeJSONObject(w, r, roomSendBody, &body) {
		return
	}
	text := ""
	if body.Text != nil {
		text = *body.Text
	}
	if len(body.Attachments) == 0 && strings.TrimSpace(text) == "" {
		writeAPIError(w, http.StatusBadRequest, "empty_message", "message text must not be blank")
		return
	}
	note := roomMentionNote(room, text)

	// One create per room: the mutex guards "find or create the current web
	// session" (design §Concurrency).
	unlock := s.lockRoom(name)
	defer unlock()

	sessionID, created, err := s.roomWebSession(r.Context(), backend, room, body.NewTopic)
	if err != nil {
		s.writeBackendError(w, "room send", err)
		return
	}
	result, ok := s.sendSessionMessage(w, r, backend, sessionID, messageBody{Text: body.Text, Attachments: body.Attachments}, note)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, roomSendResponse{
		SessionID: sessionID,
		Created:   created,
		Turn:      result.Turn,
		TurnID:    result.TurnID,
	})
}

// handleRoomRead serves API-ROOM-004 (REQ-ROOM-005).
func (s *Server) handleRoomRead(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	name := r.PathValue("name")
	if !roomExists(backend, name) {
		writeAPIError(w, http.StatusNotFound, "room_not_found", "room not found")
		return
	}
	readAt, err := backend.SetRoomRead(r.Context(), name, time.Now().UnixMilli())
	if err != nil {
		s.writeBackendError(w, "room read", err)
		return
	}
	writeJSON(w, http.StatusOK, roomReadResponse{ReadAt: readAt})
}

// computeRooms builds one row per configured agent and team. The hub and the
// list handler share it so the two cannot drift (API-ROOM-001).
func (s *Server) computeRooms(ctx context.Context) ([]roomJSON, error) {
	if s.backend == nil {
		return nil, errors.New("backend is not configured")
	}
	now := time.Now().UnixMilli()
	agents := s.backend.Agents()
	meta := s.roomAgentMeta(ctx)
	waiting := s.roomWaiting(ctx)
	rows := make([]roomJSON, 0, len(agents))
	for _, agent := range agents {
		row, err := s.roomRow(ctx, agent, meta, waiting, now)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	sortRoomRows(rows)
	return rows, nil
}

// agentMeta is the configured title, display name, and color of an agent
// (API-ROOM-006).
type agentMeta struct {
	title       string
	displayName string
	color       string
}

// roomAgentMeta reads the [agents.<name>] tables for the title and color, the
// same way handleAgents does. A missing store leaves the derived color.
func (s *Server) roomAgentMeta(ctx context.Context) map[string]agentMeta {
	meta := map[string]agentMeta{}
	if s.configStore == nil {
		return meta
	}
	file, err := s.configStore.ConfigFile(ctx)
	if err != nil || !file.Exists {
		return meta
	}
	tables, err := parseAgentTables(file.Raw)
	if err != nil {
		return meta
	}
	for name, table := range tables {
		record := agentRecordFromMap(table)
		meta[name] = agentMeta{title: record.Title, displayName: record.DisplayName, color: record.Color}
	}
	return meta
}

// roomRow builds one API-ROOM-001 row.
func (s *Server) roomRow(ctx context.Context, agent Agent, meta map[string]agentMeta, waiting map[string]presenceJSON, now int64) (roomJSON, error) {
	row := roomJSON{
		Name:    agent.Name,
		Kind:    "agent",
		Members: []string{},
		Color:   core.ResolveAgentColor(agent.Name, ""),
	}
	if agent.Kind == "team" {
		// A team row shows the description as its title and the brand color
		// (API-ROOM-001, API-ROOM-007).
		row.Kind = "team"
		row.Title = agent.Description
		row.Color = "brand"
		row.Lead = agent.Lead
		row.Members = nonNilStrings(agent.Members)
	} else if m, ok := meta[agent.Name]; ok {
		row.Title = m.title
		row.DisplayName = m.displayName
		row.Color = core.ResolveAgentColor(agent.Name, m.color)
	}

	newest, events, err := s.roomNewestSession(ctx, s.backend, agent.Name)
	if err != nil {
		return roomJSON{}, err
	}
	if newest != nil {
		row.LastActiveAt = newest.UpdatedAt
		row.LastMessage = s.roomLastMessage(*newest, events)
	}

	readAt, err := s.backend.RoomReadAt(ctx, agent.Name)
	if err != nil {
		return roomJSON{}, err
	}
	if readAt == 0 {
		// A room the owner never opened counts only the last day as unread,
		// so old history does not show as hundreds of new messages.
		readAt = now - (24 * time.Hour).Milliseconds()
	}
	unread, err := s.backend.CountAgentEvents(ctx, agent.Name, []string{core.KindAssistantMessage, core.KindQuestionAsked}, readAt, true)
	if err != nil {
		return roomJSON{}, err
	}
	row.Unread = unread
	turns, err := s.backend.CountAgentEvents(ctx, agent.Name, []string{core.KindTurnEnd}, now-roomWeekMS, false)
	if err != nil {
		return roomJSON{}, err
	}
	row.Turns7d = turns
	row.Presence = s.roomPresence(ctx, agent.Name, newest, events, waiting, now)
	return row, nil
}

// roomNewestSession returns the newest top-level session of an agent and its
// events (API-ROOM-001). The second result is nil when the room has no session.
func (s *Server) roomNewestSession(ctx context.Context, backend Backend, name string) (*core.SessionInfo, []core.SessionEvent, error) {
	sessions, err := backend.ListAgentSessions(ctx, core.SessionFilter{Agent: name, TopLevelOnly: true, Limit: 1})
	if err != nil {
		return nil, nil, err
	}
	if len(sessions) == 0 {
		return nil, nil, nil
	}
	newest := sessions[0]
	page, err := backend.Events(ctx, newest.ID, 0)
	if err != nil {
		return nil, nil, err
	}
	return &newest, page.Events, nil
}

// roomLastMessage returns the newest user or assistant message of a session,
// with runtime notes hidden (API-ROOM-001).
func (s *Server) roomLastMessage(info core.SessionInfo, events []core.SessionEvent) *roomMessageJSON {
	var last *roomMessageJSON
	for _, event := range events {
		switch event.Kind {
		case core.KindUserMessage:
			var payload core.UserMessage
			if json.Unmarshal(event.Payload, &payload) != nil || payload.Runtime {
				continue
			}
			text := userMessageText(payload)
			if text == "" {
				continue
			}
			last = &roomMessageJSON{Role: "user", Agent: info.Agent, Preview: roomPreview(text), At: event.CreatedAt, SessionID: info.ID, Channel: info.Channel}
		case core.KindAssistantMessage:
			var payload core.AssistantMessage
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			text := assistantText(payload.Content)
			if text == "" {
				continue
			}
			last = &roomMessageJSON{Role: "assistant", Agent: info.Agent, Preview: roomPreview(text), At: event.CreatedAt, SessionID: info.ID, Channel: info.Channel}
		}
	}
	return last
}

// userMessageText returns the owner text of a user message, skipping the
// leading context blocks lana injected (REQ-LTC-001).
func userMessageText(payload core.UserMessage) string {
	blocks := payload.Content
	if payload.ContextBlocks > 0 {
		if payload.ContextBlocks >= len(blocks) {
			blocks = nil
		} else {
			blocks = blocks[payload.ContextBlocks:]
		}
	}
	return strings.TrimSpace(core.TextContent(blocks))
}

// roomPreview normalizes whitespace and cuts a preview to the room limit.
func roomPreview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	return toolfmt.Cut(text, roomPreviewLimit)
}

// roomWaiting maps every open approval or question to the rooms it marks
// waiting. A request belongs to the root session's agent, or to the child tag
// agent (REQ-ROOM-006).
func (s *Server) roomWaiting(ctx context.Context) map[string]presenceJSON {
	out := map[string]presenceJSON{}
	if s.approvals == nil && s.questions == nil {
		return out
	}
	requests := make([]openRequest, 0)
	if s.approvals != nil {
		requests = append(requests, s.approvals.openRequests()...)
	}
	if s.questions != nil {
		requests = append(requests, s.questions.openRequests()...)
	}
	for _, request := range requests {
		label := "Waiting for your answer"
		if request.Kind == "approval" {
			label = "Waiting for your approval: " + request.Tool
		}
		presence := presenceJSON{
			State:     "waiting",
			Label:     cutLabel(label),
			Since:     request.Since,
			SessionID: request.SessionID,
		}
		for _, room := range s.openRequestRooms(ctx, request) {
			if room == "" {
				continue
			}
			if existing, ok := out[room]; !ok || presence.Since > existing.Since {
				out[room] = presence
			}
		}
	}
	return out
}

// openRequestRooms returns the room names an open request marks waiting.
func (s *Server) openRequestRooms(ctx context.Context, request openRequest) []string {
	rooms := make([]string, 0, 2)
	if request.Agent != "" {
		rooms = append(rooms, request.Agent)
	}
	root := sessionRoot(ctx, s.backend, request.SessionID)
	if root == "" {
		root = request.SessionID
	}
	if info, err := lookupSessionInfo(ctx, s.backend, root); err == nil && info.Agent != "" {
		rooms = append(rooms, info.Agent)
	}
	return rooms
}

// presenceFacts are the derived facts for one room. resolvePresence applies the
// precedence of REQ-ROOM-006.
type presenceFacts struct {
	waiting        bool
	waitingLabel   string
	waitingSince   int64
	waitingSession string

	working        bool
	workingLabel   string
	workingSince   int64
	workingSession string

	problem        bool
	problemLabel   string
	problemSince   int64
	problemSession string

	lastActiveAt int64
	now          int64
}

// resolvePresence is the pure presence function (REQ-ROOM-006, AC-ROOM-008,
// AC-ROOM-009, AC-ROOM-010).
func resolvePresence(f presenceFacts) presenceJSON {
	switch {
	case f.waiting:
		return presenceJSON{State: "waiting", Label: cutLabel(f.waitingLabel), Since: f.waitingSince, SessionID: f.waitingSession}
	case f.working:
		return presenceJSON{State: "working", Label: cutLabel(f.workingLabel), Since: f.workingSince, SessionID: f.workingSession}
	case f.problem:
		return presenceJSON{State: "problem", Label: cutLabel(f.problemLabel), Since: f.problemSince, SessionID: f.problemSession}
	}
	label := "No activity yet"
	if f.lastActiveAt > 0 {
		label = "Active " + relativeTime(f.now-f.lastActiveAt)
	}
	if f.lastActiveAt > 0 && f.now-f.lastActiveAt <= roomOnlineWindowMS {
		return presenceJSON{State: "online", Label: label, Since: f.lastActiveAt}
	}
	return presenceJSON{State: "resting", Label: label, Since: f.lastActiveAt}
}

// roomPresence gathers the facts of one room and resolves its presence.
func (s *Server) roomPresence(ctx context.Context, name string, newest *core.SessionInfo, events []core.SessionEvent, waiting map[string]presenceJSON, now int64) presenceJSON {
	facts := presenceFacts{now: now}
	if p, ok := waiting[name]; ok {
		facts.waiting, facts.waitingLabel, facts.waitingSince, facts.waitingSession = true, p.Label, p.Since, p.SessionID
	}
	if label, since, sessionID, ok := s.roomWorking(ctx, name); ok {
		facts.working, facts.workingLabel, facts.workingSince, facts.workingSession = true, label, since, sessionID
	}
	if newest != nil {
		facts.lastActiveAt = newest.UpdatedAt
		status, preview := deriveSessionStatusPreview(*newest, events, s.backend.TurnActive(newest.ID), s.questions != nil && s.questions.hasOpenQuestion(newest.ID))
		if status == SessionStatusFailed {
			facts.problem, facts.problemLabel, facts.problemSince, facts.problemSession = true, preview, newest.UpdatedAt, newest.ID
		}
	}
	return resolvePresence(facts)
}

// roomWorking reports the newest running session of an agent, including child
// sessions, with its label (REQ-ROOM-006).
func (s *Server) roomWorking(ctx context.Context, name string) (label string, since int64, sessionID string, ok bool) {
	sessions, err := s.backend.ListAgentSessions(ctx, core.SessionFilter{Agent: name, Limit: roomWorkingScan})
	if err != nil {
		s.logger.Debug("web: rooms working scan", "room", name, "error", err)
		return "", 0, "", false
	}
	for _, info := range sessions {
		if info.Status != SessionStatusRunning || !s.backend.TurnActive(info.ID) {
			continue
		}
		label, since = s.roomRunningLabel(ctx, info)
		return label, since, info.ID, true
	}
	return "", 0, "", false
}

// roomRunningLabel returns "Running <tool>" from the newest tool call of the
// session, else "Thinking" (REQ-ROOM-006).
func (s *Server) roomRunningLabel(ctx context.Context, info core.SessionInfo) (string, int64) {
	label := "Thinking"
	since := info.UpdatedAt
	page, err := s.backend.Events(ctx, info.ID, 0)
	if err != nil {
		return cutLabel(label), since
	}
	for _, event := range page.Events {
		if event.Kind != core.KindToolCall {
			continue
		}
		var payload core.ToolCallPayload
		if json.Unmarshal(event.Payload, &payload) != nil || payload.Name == "" {
			continue
		}
		label = "Running " + payload.Name
		if event.CreatedAt > 0 {
			since = event.CreatedAt
		}
	}
	return cutLabel(label), since
}

// roomWebSession finds the current web session of a room or creates one. The
// second result is true when this call created the session (API-ROOM-003).
func (s *Server) roomWebSession(ctx context.Context, backend Backend, room Agent, newTopic bool) (string, bool, error) {
	if !newTopic {
		sessions, err := backend.ListAgentSessions(ctx, core.SessionFilter{
			Agent:        room.Name,
			Channel:      "web",
			Address:      "owner",
			TopLevelOnly: true,
			Limit:        1,
		})
		if err != nil {
			return "", false, err
		}
		if len(sessions) > 0 {
			return sessions[0].ID, false, nil
		}
	}
	session, err := backend.CreateSession(ctx, room.Name, "")
	if err != nil {
		return "", false, err
	}
	return session.ID, true, nil
}

// roomMentionNote returns the runtime note for a team room whose text starts
// with a member mention, or "" (API-ROOM-003, AC-ROOM-013).
func roomMentionNote(room Agent, text string) string {
	if room.Kind != "team" {
		return ""
	}
	mention := mentionTarget(text)
	if mention == "" {
		return ""
	}
	for _, member := range room.Members {
		if strings.EqualFold(member, mention) {
			return "The owner addressed @" + member + ". Hand this request to " + member + " with the task tool and report back."
		}
	}
	return ""
}

// mentionTarget returns the token after a leading "@", or "".
func mentionTarget(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "@") {
		return ""
	}
	rest := text[1:]
	if end := strings.IndexAny(rest, " \t\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// lockRoom returns the release of the per-room create lock (design
// §Concurrency).
func (s *Server) lockRoom(name string) func() {
	value, _ := s.roomLocks.LoadOrStore(name, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// sortRoomRows orders by last_active_at descending; rooms with no session last,
// by name (API-ROOM-001).
func sortRoomRows(rows []roomJSON) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch {
		case a.LastActiveAt > 0 && b.LastActiveAt > 0:
			if a.LastActiveAt != b.LastActiveAt {
				return a.LastActiveAt > b.LastActiveAt
			}
			return a.Name < b.Name
		case a.LastActiveAt > 0:
			return true
		case b.LastActiveAt > 0:
			return false
		default:
			return a.Name < b.Name
		}
	})
}

// parseSegmentQuery reads before and limit of API-ROOM-002.
func parseSegmentQuery(r *http.Request) (int, int64, error) {
	limit := defaultSegmentLimit
	query := r.URL.Query()
	if values, ok := query["limit"]; ok {
		parsed, err := strconv.Atoi(values[0])
		if err != nil || parsed < 1 || parsed > maxSegmentLimit {
			return 0, 0, errors.New("limit must be an integer between 1 and 20")
		}
		limit = parsed
	}
	var before int64
	if values, ok := query["before"]; ok {
		parsed, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("before must be a non-negative integer in milliseconds")
		}
		before = parsed
	}
	return limit, before, nil
}

// roomExists reports whether name is a configured agent or team.
func roomExists(backend Backend, name string) bool {
	if name == "" {
		return false
	}
	_, ok := findRoom(backend, name)
	return ok
}

// findRoom returns the agent or team row of a room name.
func findRoom(backend Backend, name string) (Agent, bool) {
	for _, agent := range backend.Agents() {
		if agent.Name == name {
			return agent, true
		}
	}
	return Agent{}, false
}

// cutLabel normalizes and cuts a presence label to the contract limit.
func cutLabel(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	return toolfmt.Cut(text, roomLabelLimit)
}

// relativeTime renders a coarse "12 min ago" style string.
func relativeTime(deltaMS int64) string {
	if deltaMS < 0 {
		deltaMS = 0
	}
	minutes := deltaMS / 60000
	switch {
	case minutes < 1:
		return "just now"
	case minutes < 60:
		return fmt.Sprintf("%d min ago", minutes)
	case minutes < 60*24:
		return fmt.Sprintf("%d h ago", minutes/60)
	default:
		return fmt.Sprintf("%d d ago", minutes/(60*24))
	}
}

// roomEvent is one SSE event the hub pushes to subscribers (API-ROOM-005).
type roomEvent struct {
	name string
	data []byte
}

// roomHub pushes changed room rows to every open stream. One goroutine runs
// while at least one subscriber is connected (REQ-ROOM-007).
type roomHub struct {
	s *Server

	mu       sync.Mutex
	subs     map[chan roomEvent]struct{}
	last     map[string]string
	interval time.Duration
	running  bool
	stop     chan struct{}
}

func newRoomHub(s *Server) *roomHub {
	return &roomHub{
		s:        s,
		subs:     map[chan roomEvent]struct{}{},
		last:     map[string]string{},
		interval: defaultRoomTickInterval,
	}
}

// setInterval overrides the tick interval; it exists for tests.
func (h *roomHub) setInterval(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if d <= 0 {
		d = defaultRoomTickInterval
	}
	h.interval = d
}

// subscribe registers a stream and seeds the last-broadcast rows so the hub
// only pushes later changes. The first subscriber starts the goroutine
// (API-ROOM-005).
func (h *roomHub) subscribe(rows []roomJSON) (<-chan roomEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, row := range rows {
		if data, err := json.Marshal(row); err == nil {
			h.last[row.Name] = string(data)
		}
	}
	ch := make(chan roomEvent, roomSubscriberBuffer)
	h.subs[ch] = struct{}{}
	if !h.running {
		h.running = true
		h.stop = make(chan struct{})
		go h.loop(h.stop)
	}
	return ch, func() { h.unsubscribe(ch) }
}

// unsubscribe removes a stream and stops the goroutine after the last one
// leaves.
func (h *roomHub) unsubscribe(ch chan roomEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; !ok {
		return
	}
	delete(h.subs, ch)
	if len(h.subs) == 0 && h.running {
		h.running = false
		close(h.stop)
	}
}

// loop recomputes the rows every interval and pushes the changed ones.
func (h *roomHub) loop(stop chan struct{}) {
	h.mu.Lock()
	interval := h.interval
	h.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-h.s.stopping():
			return
		case <-ticker.C:
			h.tick()
		}
	}
}

// tick computes all rows once, compares each row with the last broadcast, and
// sends the changed ones (API-ROOM-005).
func (h *roomHub) tick() {
	rows, err := h.s.computeRooms(context.Background())
	if err != nil {
		h.s.logger.Warn("web: rooms stream", "error", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		data, err := json.Marshal(row)
		if err != nil {
			continue
		}
		seen[row.Name] = true
		if string(data) == h.last[row.Name] {
			continue
		}
		h.last[row.Name] = string(data)
		h.sendLocked(roomEvent{name: "room", data: data})
	}
	for name := range h.last {
		if !seen[name] {
			delete(h.last, name)
		}
	}
}

// sendLocked enqueues an event to every subscriber. A subscriber whose buffer
// is full is dropped and reconnects with a fresh rooms event (API-ROOM-005).
func (h *roomHub) sendLocked(event roomEvent) {
	for ch := range h.subs {
		select {
		case ch <- event:
		default:
			delete(h.subs, ch)
		}
	}
}

// handleRoomsStream serves API-ROOM-005 (REQ-ROOM-007).
func (s *Server) handleRoomsStream(w http.ResponseWriter, r *http.Request) {
	if s.backend == nil {
		s.backendOrError(w)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.logger.Error("web: rooms stream: response writer cannot flush")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	rows, err := s.computeRooms(r.Context())
	if err != nil {
		s.logger.Error("web: rooms stream", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, unsubscribe := s.roomHub.subscribe(rows)
	defer unsubscribe()
	// A new subscriber first gets the full list (API-ROOM-005).
	if err := writeSSE(w, flusher, "rooms", "", roomsResponse{Rooms: rows}); err != nil {
		return
	}

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
		case event := <-events:
			if err := writeSSE(w, flusher, event.name, "", json.RawMessage(event.data)); err != nil {
				return
			}
		}
	}
}

// SetRoomTickInterval overrides the room stream tick interval (tests).
func (s *Server) SetRoomTickInterval(d time.Duration) {
	if s.roomHub != nil {
		s.roomHub.setInterval(d)
	}
}
