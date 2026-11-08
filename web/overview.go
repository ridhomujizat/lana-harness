package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/toolfmt"
)

const (
	// overviewActivityLimit and overviewTextLimit are the API-ROOM-008 caps.
	overviewActivityLimit = 20
	overviewTextLimit     = 140
	// overviewMemoryLimit is how many MEMORY.md entries the profile shows.
	overviewMemoryLimit = 6
	// overviewWeekDays is the number of day buckets of the week block.
	overviewWeekDays = 7
)

// overviewStepJSON is one tool step of the running turn (API-ROOM-008).
type overviewStepJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	At     int64  `json:"at"`
}

// overviewNowJSON is the "Right now" block; it is null when no turn runs.
type overviewNowJSON struct {
	SessionID string             `json:"session_id"`
	TurnID    string             `json:"turn_id"`
	StartedAt int64              `json:"started_at"`
	Steps     []overviewStepJSON `json:"steps"`
}

// overviewActivityJSON is one Activity item of API-ROOM-008.
type overviewActivityJSON struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	At        int64  `json:"at"`
	SessionID string `json:"session_id"`
}

// overviewDayJSON is one day bucket of the week block.
type overviewDayJSON struct {
	Day   string `json:"day"`
	Turns int64  `json:"turns"`
}

// overviewWeekJSON is the "This week" block of API-ROOM-008.
type overviewWeekJSON struct {
	Days      []overviewDayJSON `json:"days"`
	Turns     int64             `json:"turns"`
	Questions int64             `json:"questions"`
	Notes     int64             `json:"notes"`
	Tokens    int64             `json:"tokens"`
}

// overviewMemoryJSON is the "What it remembers" block.
type overviewMemoryJSON struct {
	Entries []string `json:"entries"`
	Total   int      `json:"total"`
}

// overviewChannelJSON is one route the agent looks after.
type overviewChannelJSON struct {
	Channel string `json:"channel"`
	Match   string `json:"match"`
}

// overviewLooksAfterJSON is the "Looks after" block of API-ROOM-008.
type overviewLooksAfterJSON struct {
	Channels []overviewChannelJSON `json:"channels"`
	Teams    []string              `json:"teams"`
}

// overviewResponse is the API-ROOM-008 payload.
type overviewResponse struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Color string `json:"color"`
	// AvatarSVG is the avatar the room shows, never empty, and AvatarSource
	// "saved" or "default" (spec 43, API-AVT-005).
	AvatarSVG    string `json:"avatar_svg"`
	AvatarSource string `json:"avatar_source"`
	// Members and Lead are set for a team room only (API-ROOM-008).
	Members     []string               `json:"members,omitempty"`
	Lead        string                 `json:"lead,omitempty"`
	PersonaLine string                 `json:"persona_line"`
	CreatedAt   int64                  `json:"created_at"`
	Presence    presenceJSON           `json:"presence"`
	Now         *overviewNowJSON       `json:"now"`
	Activity    []overviewActivityJSON `json:"activity"`
	Week        overviewWeekJSON       `json:"week"`
	Memory      overviewMemoryJSON     `json:"memory"`
	Job         string                 `json:"job"`
	LooksAfter  overviewLooksAfterJSON `json:"looks_after"`
}

// registerOverviewRoutes registers API-ROOM-008 through Handle so the auth,
// Host, Origin, and CSRF checks apply.
func (s *Server) registerOverviewRoutes() {
	s.Handle("GET /v1/agents/{name}/overview", http.HandlerFunc(s.handleAgentOverview))
}

// handleAgentOverview serves API-ROOM-008 (REQ-ROOM-010, REQ-ROOM-011).
func (s *Server) handleAgentOverview(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	name := r.PathValue("name")
	room, ok := findRoom(backend, name)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "agent is not configured")
		return
	}
	overview, err := s.buildOverview(r.Context(), backend, room, time.Now())
	if err != nil {
		s.logger.Error("web: overview "+name, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

// buildOverview assembles the API-ROOM-008 payload for one room. The presence,
// title, and color reuse the room row so the two surfaces cannot drift
// (REQ-ROOM-010, REQ-ROOM-011).
func (s *Server) buildOverview(ctx context.Context, backend Backend, room Agent, now time.Time) (overviewResponse, error) {
	nowMS := now.UnixMilli()
	row, err := s.roomRow(ctx, room, s.roomAgentMeta(ctx), s.roomWaiting(ctx), nowMS)
	if err != nil {
		return overviewResponse{}, err
	}
	out := overviewResponse{
		Name:         row.Name,
		Kind:         row.Kind,
		Title:        row.Title,
		Color:        row.Color,
		AvatarSVG:    row.AvatarSVG,
		AvatarSource: row.AvatarSource,
		Presence:     row.Presence,
		Activity:     []overviewActivityJSON{},
		Memory:       overviewMemoryJSON{Entries: []string{}},
		LooksAfter:   overviewLooksAfterJSON{Channels: []overviewChannelJSON{}, Teams: []string{}},
	}
	if row.Kind == "team" {
		out.Members = row.Members
		out.Lead = row.Lead
	}

	// The agent's sessions, child sessions included, give the room age, the
	// channel of every event, and the running session (API-ROOM-008).
	sessions, err := backend.ListAgentSessions(ctx, core.SessionFilter{Agent: room.Name})
	if err != nil {
		return overviewResponse{}, err
	}
	channels := make(map[string]string, len(sessions))
	var oldest int64
	for _, info := range sessions {
		channels[info.ID] = info.Channel
		if info.CreatedAt > 0 && (oldest == 0 || info.CreatedAt < oldest) {
			oldest = info.CreatedAt
		}
	}
	out.CreatedAt = oldest

	out.Now, err = s.overviewNow(ctx, backend, sessions)
	if err != nil {
		return overviewResponse{}, err
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	since := today.AddDate(0, 0, -(overviewWeekDays - 1)).UnixMilli()
	scanned, err := s.overviewScan(ctx, backend, room.Name, since)
	if err != nil {
		return overviewResponse{}, err
	}
	out.Week = buildOverviewWeek(scanned, today)
	out.Activity = buildOverviewActivity(scanned, channels)

	home := s.overviewHome()
	if home != "" {
		paths := agenthome.Paths(home, room.Name)
		persona, err := readOverviewFile(paths.Agent)
		if err != nil {
			return overviewResponse{}, err
		}
		out.PersonaLine = personaLine([]byte(persona))
		memory, err := overviewMemory(paths.Memory)
		if err != nil {
			return overviewResponse{}, err
		}
		out.Memory = memory
		job, err := readOverviewFile(paths.Job)
		if err != nil {
			return overviewResponse{}, err
		}
		out.Job = job
	}
	out.LooksAfter = overviewLooksAfter(s.overviewConfig(ctx), room.Name)
	return out, nil
}

// overviewNow returns the running turn of the agent, or nil when no turn runs
// (API-ROOM-008).
func (s *Server) overviewNow(ctx context.Context, backend Backend, sessions []core.SessionInfo) (*overviewNowJSON, error) {
	for _, info := range sessions {
		if info.Status != SessionStatusRunning || !backend.TurnActive(info.ID) {
			continue
		}
		page, err := backend.Events(ctx, info.ID, 0)
		if err != nil {
			return nil, err
		}
		return buildOverviewNow(info.ID, page.Events), nil
	}
	return nil, nil
}

// buildOverviewNow builds the steps of the running turn from its tool/call and
// tool/result events in order (AC-ROOM-017).
func buildOverviewNow(sessionID string, events []core.SessionEvent) *overviewNowJSON {
	turnID := ""
	startedAt := int64(0)
	for _, event := range events {
		if event.Kind != core.KindTurnStart {
			continue
		}
		var payload core.TurnStart
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		turnID = overviewTurnID(event, payload.TurnID)
		startedAt = event.CreatedAt
	}
	if turnID == "" {
		// No turn/start in the log: fall back to the newest tool call's turn.
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Kind == core.KindToolCall && events[i].TurnID != "" {
				turnID = events[i].TurnID
				break
			}
		}
	}

	results := make(map[string]core.ToolResultPayload)
	for _, event := range events {
		if event.Kind != core.KindToolResult {
			continue
		}
		var payload core.ToolResultPayload
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		results[payload.CallID] = payload
	}

	steps := make([]overviewStepJSON, 0)
	for _, event := range events {
		if event.Kind != core.KindToolCall {
			continue
		}
		if turnID != "" && event.TurnID != "" && event.TurnID != turnID {
			continue
		}
		var payload core.ToolCallPayload
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		status := "running"
		if result, ok := results[payload.CallID]; ok {
			status = "ok"
			if result.Status == core.ToolStatusError || result.Status == core.ToolStatusRejected {
				status = "error"
			}
		}
		steps = append(steps, overviewStepJSON{Name: payload.Name, Status: status, At: event.CreatedAt})
	}
	return &overviewNowJSON{SessionID: sessionID, TurnID: turnID, StartedAt: startedAt, Steps: steps}
}

// overviewScan reads the agent's events of interest since sinceMS, in order
// (API-ROOM-008).
func (s *Server) overviewScan(ctx context.Context, backend Backend, name string, since int64) ([]core.ScannedEvent, error) {
	kinds := []string{
		core.KindTurnEnd, core.KindAssistantMessage, core.KindQuestionAsked,
		core.KindApprovalAsked, core.KindMemoryCaptured, core.KindKnowledgeCompiled,
		core.KindCompactionSummary, core.KindStepEnd,
	}
	scanned := make([]core.ScannedEvent, 0)
	for event, err := range backend.ScanEvents(ctx, kinds, since) {
		if err != nil {
			return nil, err
		}
		if event.Agent != name {
			continue
		}
		scanned = append(scanned, event)
	}
	return scanned, nil
}

// buildOverviewWeek buckets the scanned events into the seven day cells of the
// week block, oldest first (AC-ROOM-018).
func buildOverviewWeek(scanned []core.ScannedEvent, today time.Time) overviewWeekJSON {
	days := make([]overviewDayJSON, overviewWeekDays)
	index := make(map[string]int, overviewWeekDays)
	for i := 0; i < overviewWeekDays; i++ {
		key := today.AddDate(0, 0, i-(overviewWeekDays-1)).Format("2006-01-02")
		days[i] = overviewDayJSON{Day: key}
		index[key] = i
	}
	week := overviewWeekJSON{Days: days}
	for _, event := range scanned {
		switch event.Kind {
		case core.KindTurnEnd:
			week.Turns++
			if i, ok := index[time.UnixMilli(event.CreatedAt).In(time.Local).Format("2006-01-02")]; ok {
				week.Days[i].Turns++
			}
		case core.KindQuestionAsked:
			week.Questions++
		case core.KindMemoryCaptured, core.KindKnowledgeCompiled:
			week.Notes++
		case core.KindStepEnd:
			var payload core.StepEnd
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			// InputTokens already counts cache hits, so the total is input
			// plus output (core.Usage).
			week.Tokens += payload.Usage.InputTokens + payload.Usage.OutputTokens
		}
	}
	return week
}

// buildOverviewActivity turns the scanned events into at most 20 items, newest
// first (AC-ROOM-018).
func buildOverviewActivity(scanned []core.ScannedEvent, channels map[string]string) []overviewActivityJSON {
	answers := make(map[string]string)
	items := make([]overviewActivityJSON, 0)
	for _, event := range scanned {
		switch event.Kind {
		case core.KindAssistantMessage:
			var payload core.AssistantMessage
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			if text := assistantText(payload.Content); text != "" {
				answers[activityTurnKey(event, "")] = text
			}
		case core.KindTurnEnd:
			var payload core.TurnEnd
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			item := overviewActivityJSON{At: event.CreatedAt, SessionID: event.SessionID}
			if overviewTurnFailed(payload) {
				item.Kind = "failed"
				item.Title = "Turn failed"
				item.Detail = cutOverviewText(turnErrorSummary(payload))
			} else {
				item.Kind = "answered"
				item.Title = "Answered in " + overviewChannelLabel(channels[event.SessionID])
				item.Detail = cutOverviewText(answers[activityTurnKey(event, payload.TurnID)])
			}
			items = append(items, item)
		case core.KindQuestionAsked:
			items = append(items, overviewActivityJSON{Kind: "asked", Title: "Asked a question", At: event.CreatedAt, SessionID: event.SessionID})
		case core.KindApprovalAsked:
			var payload core.ApprovalAsked
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			items = append(items, overviewActivityJSON{Kind: "approval", Title: "Requested approval", Detail: cutOverviewText(payload.Tool), At: event.CreatedAt, SessionID: event.SessionID})
		case core.KindMemoryCaptured:
			items = append(items, overviewActivityJSON{Kind: "remembered", Title: "Captured a memory", At: event.CreatedAt, SessionID: event.SessionID})
		case core.KindKnowledgeCompiled:
			items = append(items, overviewActivityJSON{Kind: "learned", Title: "Compiled knowledge", At: event.CreatedAt, SessionID: event.SessionID})
		case core.KindCompactionSummary:
			items = append(items, overviewActivityJSON{Kind: "summarized", Title: "Summarized the conversation", At: event.CreatedAt, SessionID: event.SessionID})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].At > items[j].At })
	if len(items) > overviewActivityLimit {
		items = items[:overviewActivityLimit]
	}
	return items
}

// activityTurnKey identifies the turn an event belongs to.
func activityTurnKey(event core.ScannedEvent, payloadTurnID string) string {
	return event.SessionID + "\x00" + overviewTurnID(event.SessionEvent, payloadTurnID)
}

// overviewTurnID prefers the event's turn id and falls back to the payload's.
func overviewTurnID(event core.SessionEvent, payloadTurnID string) string {
	if event.TurnID != "" {
		return event.TurnID
	}
	return payloadTurnID
}

// overviewTurnFailed reports whether a finished turn ended in an error.
func overviewTurnFailed(end core.TurnEnd) bool {
	if end.Error != nil {
		return true
	}
	switch end.Reason {
	case "error", "max_steps", "loop", "interrupted":
		return true
	}
	return false
}

// turnErrorSummary renders the error of a failed turn, or its reason.
func turnErrorSummary(end core.TurnEnd) string {
	if end.Error != nil {
		return joinOverviewParts(end.Error.Class, end.Error.Message)
	}
	if end.Reason == "" {
		return "unknown error"
	}
	return end.Reason
}

func joinOverviewParts(parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, ": ")
}

// overviewChannelLabel capitalises a channel name the way the views do.
func overviewChannelLabel(channel string) string {
	switch channel {
	case "web":
		return "Web"
	case "cli":
		return "CLI"
	case "telegram":
		return "Telegram"
	case "library":
		return "Library"
	case "":
		return "Unknown"
	default:
		return channel
	}
}

// cutOverviewText normalizes whitespace and cuts a line to the profile limit.
func cutOverviewText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	return toolfmt.Cut(text, overviewTextLimit)
}

// personaLine is the first non-empty, non-heading line of AGENT.md, cut to the
// profile limit (API-ROOM-008).
func personaLine(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return cutOverviewText(line)
	}
	return ""
}

// overviewMemory reads the first entries of MEMORY.md and the total count.
func overviewMemory(path string) (overviewMemoryJSON, error) {
	entries, err := agenthome.ReadEntries(path)
	if err != nil {
		return overviewMemoryJSON{}, err
	}
	out := overviewMemoryJSON{Entries: []string{}, Total: len(entries)}
	for i, entry := range entries {
		if i >= overviewMemoryLimit {
			break
		}
		out.Entries = append(out.Entries, cutOverviewText(entry))
	}
	return out, nil
}

// readOverviewFile reads a small profile file; a missing file reads as empty.
func readOverviewFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// overviewHome returns the LANA_HOME used for the agent folder, the same home
// web/agentfiles.go reads.
func (s *Server) overviewHome() string {
	if s.agentFileStore != nil {
		if home := s.agentFileStore.SettingsHome(); home != "" {
			return home
		}
	}
	return s.agentHome()
}

// overviewConfig is the two config sections the "Looks after" block needs.
type overviewConfig struct {
	Routes []core.RouteConfig
	Teams  map[string]core.TeamConfig
}

// overviewConfig reads the routes and teams from the config file, the same way
// handleAgents reads the [agents.<name>] tables.
func (s *Server) overviewConfig(ctx context.Context) overviewConfig {
	cfg := overviewConfig{Teams: map[string]core.TeamConfig{}}
	if s.configStore == nil {
		return cfg
	}
	file, err := s.configStore.ConfigFile(ctx)
	if err != nil || !file.Exists {
		return cfg
	}
	var document struct {
		Routes []core.RouteConfig         `toml:"routes"`
		Teams  map[string]core.TeamConfig `toml:"teams"`
	}
	if toml.Unmarshal(file.Raw, &document) != nil {
		return cfg
	}
	cfg.Routes = document.Routes
	if document.Teams != nil {
		cfg.Teams = document.Teams
	}
	return cfg
}

// overviewLooksAfter lists the routes an agent serves and the teams it belongs
// to (API-ROOM-008).
func overviewLooksAfter(cfg overviewConfig, name string) overviewLooksAfterJSON {
	out := overviewLooksAfterJSON{Channels: []overviewChannelJSON{}, Teams: []string{}}
	for _, route := range cfg.Routes {
		if route.Agent != name {
			continue
		}
		out.Channels = append(out.Channels, overviewChannelJSON{Channel: route.Channel, Match: overviewRouteMatch(route)})
	}
	sort.SliceStable(out.Channels, func(i, j int) bool {
		if out.Channels[i].Channel != out.Channels[j].Channel {
			return out.Channels[i].Channel < out.Channels[j].Channel
		}
		return out.Channels[i].Match < out.Channels[j].Match
	})
	for teamName, team := range cfg.Teams {
		if team.Lead == name || containsString(team.Members, name) {
			out.Teams = append(out.Teams, teamName)
		}
	}
	sort.Strings(out.Teams)
	return out
}

// overviewRouteMatch renders a short label for a route's match, e.g. the
// Telegram chat it reaches (API-ROOM-008).
func overviewRouteMatch(route core.RouteConfig) string {
	if id, ok := route.Match["chat_id"]; ok {
		return "chat " + fmt.Sprint(id)
	}
	if name, ok := route.Match["name"].(string); ok && name != "" {
		return "chat " + name
	}
	return ""
}
