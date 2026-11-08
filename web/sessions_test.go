package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/attach"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

// fakeWebBackend is an in-memory web.Backend for handler tests. Its Sessions
// method follows the same filter, sort, and cursor rules as the daemon adapter.
type fakeWebBackend struct {
	mu           sync.Mutex
	sessions     []core.SessionInfo
	events       map[string][]core.SessionEvent
	models       map[string]string
	efforts      map[string]string
	policies     map[string]string
	agents       []Agent
	agentNames   map[string]bool
	createdAgent string
	createdModel string

	turnActive  map[string]bool
	leaseHeld   map[string]int
	inbox       map[string][]string
	continueErr map[string]error
	cancelled   []string
	compacted   []string
	forked      []string

	// sentAttachments records the paths of the last Send per session.
	sentAttachments map[string][]string
	// sentText records every text accepted by Send per session (REQ-ROOM-003).
	sentText map[string][]string
	// sentNotes records the last runtime note per session (REQ-ROOM-008).
	sentNotes map[string]string
	// roomReads holds the room read marks (REQ-ROOM-005).
	roomReads map[string]int64
	// savedAttachments records every file accepted by SaveAttachment.
	savedAttachments []attach.Info
	// attachmentPaths maps a workspace-relative attachments/ path to a file on
	// disk, so tests can serve it (API-ATT-003).
	attachmentPaths map[string]string
	// attachmentErr returns a forced SaveAttachment error for a file name.
	attachmentErr map[string]error

	frames     *core.Observe[core.LiveFrame]
	subscribed chan struct{}
}

func newFakeWebBackend() *fakeWebBackend {
	return &fakeWebBackend{
		events:          map[string][]core.SessionEvent{},
		models:          map[string]string{},
		efforts:         map[string]string{},
		policies:        map[string]string{},
		agentNames:      map[string]bool{},
		turnActive:      map[string]bool{},
		leaseHeld:       map[string]int{},
		inbox:           map[string][]string{},
		continueErr:     map[string]error{},
		sentAttachments: map[string][]string{},
		sentText:        map[string][]string{},
		sentNotes:       map[string]string{},
		roomReads:       map[string]int64{},
		attachmentPaths: map[string]string{},
		attachmentErr:   map[string]error{},
		frames:          core.NewObserve[core.LiveFrame](),
		subscribed:      make(chan struct{}, 8),
	}
}

func (f *fakeWebBackend) addSession(info core.SessionInfo) core.SessionInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addSessionLocked(info)
}

func (f *fakeWebBackend) addSessionLocked(info core.SessionInfo) core.SessionInfo {
	if info.ID == "" {
		info.ID = id.New()
	}
	if info.Status == "" {
		info.Status = "idle"
	}
	f.sessions = append(f.sessions, info)
	return info
}

func (f *fakeWebBackend) find(sessionID string) (core.SessionInfo, bool) {
	for _, info := range f.sessions {
		if info.ID == sessionID {
			return info, true
		}
	}
	return core.SessionInfo{}, false
}

func (f *fakeWebBackend) update(sessionID string, fn func(*core.SessionInfo)) bool {
	for i := range f.sessions {
		if f.sessions[i].ID == sessionID {
			fn(&f.sessions[i])
			return true
		}
	}
	return false
}

func (f *fakeWebBackend) appendEvent(sessionID string, event core.SessionEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if event.Seq == 0 {
		event.Seq = int64(len(f.events[sessionID]) + 1)
	}
	event.SessionID = sessionID
	f.events[sessionID] = append(f.events[sessionID], event)
	f.update(sessionID, func(info *core.SessionInfo) {
		if event.Seq > info.LastSeq {
			info.LastSeq = event.Seq
		}
	})
}

func (f *fakeWebBackend) setTurnActive(sessionID string, active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turnActive[sessionID] = active
}

func (f *fakeWebBackend) publishFrame(frame core.LiveFrame) {
	f.frames.Publish(frame)
}

func (f *fakeWebBackend) Sessions(_ context.Context, filter SessionFilter) (SessionPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	statuses := make(map[string]bool, len(filter.Statuses))
	for _, status := range filter.Statuses {
		statuses[status] = true
	}
	query := strings.ToLower(filter.Q)
	list := make([]core.SessionInfo, 0, len(f.sessions))
	for _, info := range f.sessions {
		if filter.TopLevelOnly && info.ParentSessionID != "" {
			continue
		}
		if filter.Channel != "" && info.Channel != filter.Channel {
			continue
		}
		if filter.Agent != "" && info.Agent != filter.Agent {
			continue
		}
		if filter.Before > 0 && info.UpdatedAt >= filter.Before {
			continue
		}
		if query != "" || len(statuses) > 0 {
			status, preview := deriveSessionStatusPreview(info, f.events[info.ID], f.turnActive[info.ID] || info.Status == "running", false)
			if len(statuses) > 0 && !statuses[status] {
				continue
			}
			if query != "" &&
				!strings.Contains(strings.ToLower(info.Title), query) &&
				!strings.Contains(strings.ToLower(preview), query) {
				continue
			}
		}
		list = append(list, info)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].UpdatedAt != list[j].UpdatedAt {
			return list[i].UpdatedAt > list[j].UpdatedAt
		}
		return list[i].ID > list[j].ID
	})
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	var next *int64
	if len(list) > limit {
		at := list[limit-1].UpdatedAt
		next = &at
		list = list[:limit]
	}
	sessions := make([]Session, 0, len(list))
	for _, info := range list {
		sessions = append(sessions, Session{SessionInfo: info, Team: f.teamLocked(info.Agent)})
	}
	return SessionPage{Sessions: sessions, NextBefore: next}, nil
}

func (f *fakeWebBackend) Session(_ context.Context, sessionID string) (Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, info := range f.sessions {
		if info.ID == sessionID {
			return f.detail(info), nil
		}
	}
	return Session{}, ErrSessionNotFound
}

func (f *fakeWebBackend) Events(_ context.Context, sessionID string, fromSeq int64) (EventPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var info core.SessionInfo
	found := false
	for _, candidate := range f.sessions {
		if candidate.ID == sessionID {
			info, found = candidate, true
			break
		}
	}
	if !found {
		return EventPage{}, ErrSessionNotFound
	}
	page := EventPage{Events: make([]core.SessionEvent, 0), LastSeq: info.LastSeq}
	for _, event := range f.events[sessionID] {
		if event.Seq > page.LastSeq {
			page.LastSeq = event.Seq
		}
		if event.Seq > fromSeq {
			page.Events = append(page.Events, event)
		}
	}
	return page, nil
}

func (f *fakeWebBackend) CreateSession(_ context.Context, agent, model string) (Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.agentNames[agent] {
		return Session{}, ErrUnknownAgent
	}
	now := time.Now().UnixMilli()
	info := f.addSessionLocked(core.SessionInfo{
		Agent:     agent,
		Channel:   "web",
		Address:   "owner",
		Workspace: "/tmp/workspaces/" + agent,
		CreatedAt: now,
		UpdatedAt: now,
	})
	f.createdAgent = agent
	if model != "" {
		f.createdModel = model
		f.models[info.ID] = model
	}
	return f.detail(info), nil
}

func (f *fakeWebBackend) Agents() []Agent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Agent(nil), f.agents...)
}

// teamLocked returns the team row of an agent name, or nil. The caller holds
// the mutex.
func (f *fakeWebBackend) teamLocked(name string) *Team {
	for _, agent := range f.agents {
		if agent.Name != name || agent.Kind != "team" {
			continue
		}
		return &Team{Name: name, Lead: agent.Lead, Members: append([]string{}, agent.Members...)}
	}
	return nil
}

// isTeamLocked reports whether the session's agent is a team. The caller holds
// the mutex.
func (f *fakeWebBackend) isTeamLocked(sessionID string) bool {
	info, ok := f.find(sessionID)
	if !ok {
		return false
	}
	for _, agent := range f.agents {
		if agent.Name == info.Agent {
			return agent.Kind == "team"
		}
	}
	return false
}

func (f *fakeWebBackend) leaseError(sessionID string) error {
	if pid, ok := f.leaseHeld[sessionID]; ok {
		return &core.LeaseHeldError{SessionID: sessionID, PID: pid}
	}
	return nil
}

func (f *fakeWebBackend) Send(_ context.Context, sessionID, text string, attachments []string) (SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(sessionID); !ok {
		return SendResult{}, ErrSessionNotFound
	}
	if err := f.leaseError(sessionID); err != nil {
		return SendResult{}, err
	}
	f.sentAttachments[sessionID] = append([]string(nil), attachments...)
	f.sentText[sessionID] = append(f.sentText[sessionID], text)
	if f.turnActive[sessionID] {
		f.inbox[sessionID] = append(f.inbox[sessionID], text)
		return SendResult{Turn: "queued"}, nil
	}
	f.turnActive[sessionID] = true
	return SendResult{Turn: "started", TurnID: "turn-fake"}, nil
}

// SendNote implements the optional noteSender extension (API-ROOM-003,
// REQ-ROOM-008): it records the runtime note and then sends the message.
func (f *fakeWebBackend) SendNote(ctx context.Context, sessionID, text string, attachments []string, note string) (SendResult, error) {
	f.mu.Lock()
	f.sentNotes[sessionID] = note
	f.mu.Unlock()
	return f.Send(ctx, sessionID, text, attachments)
}

// SaveAttachment records the uploaded bytes and returns an Info with a fixed
// date, so the tests can assert the response and the paths.
func (f *fakeWebBackend) SaveAttachment(_ context.Context, sessionID, name string, r io.Reader) (attach.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(sessionID); !ok {
		return attach.Info{}, ErrSessionNotFound
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return attach.Info{}, err
	}
	if err := f.attachmentErr[name]; err != nil {
		return attach.Info{}, err
	}
	if int64(len(data)) > attach.MaxFileBytes {
		return attach.Info{}, fmt.Errorf("%s is over the limit: %w", name, attach.ErrTooLarge)
	}
	info := attach.Info{
		Path:      "attachments/2026-11-01/" + name,
		Name:      name,
		MediaType: fakeAttachmentMediaType(name),
		Size:      int64(len(data)),
	}
	f.savedAttachments = append(f.savedAttachments, info)
	return info, nil
}

// AttachmentPath resolves a path the test registered, and rejects anything
// outside attachments/ so the handler's 404 path is exercised.
func (f *fakeWebBackend) AttachmentPath(_ context.Context, sessionID, rel string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(sessionID); !ok {
		return "", ErrSessionNotFound
	}
	if !strings.HasPrefix(rel, attach.Dir+"/") || strings.Contains(rel, "..") {
		return "", attach.ErrOutside
	}
	abs, ok := f.attachmentPaths[rel]
	if !ok {
		return "", os.ErrNotExist
	}
	return abs, nil
}

func fakeAttachmentMediaType(name string) string {
	switch {
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(name, ".pdf"):
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}

func (f *fakeWebBackend) Cancel(_ context.Context, sessionID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(sessionID); !ok {
		return false, ErrSessionNotFound
	}
	if err := f.leaseError(sessionID); err != nil {
		return false, err
	}
	running := f.turnActive[sessionID]
	f.turnActive[sessionID] = false
	if running {
		f.cancelled = append(f.cancelled, sessionID)
	}
	return running, nil
}

func (f *fakeWebBackend) Continue(_ context.Context, sessionID string) (SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(sessionID); !ok {
		return SendResult{}, ErrSessionNotFound
	}
	if err := f.leaseError(sessionID); err != nil {
		return SendResult{}, err
	}
	if f.turnActive[sessionID] {
		return SendResult{}, ErrTurnRunning
	}
	if err := f.continueErr[sessionID]; err != nil {
		return SendResult{}, err
	}
	f.turnActive[sessionID] = true
	return SendResult{Turn: "started", TurnID: "turn-fake"}, nil
}

func (f *fakeWebBackend) Compact(_ context.Context, sessionID string) (CompactResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(sessionID); !ok {
		return CompactResult{}, ErrSessionNotFound
	}
	if err := f.leaseError(sessionID); err != nil {
		return CompactResult{}, err
	}
	if f.turnActive[sessionID] {
		return CompactResult{}, ErrInvalidState
	}
	f.compacted = append(f.compacted, sessionID)
	f.appendEventLocked(sessionID, core.KindCompactionSummary, json.RawMessage(`{"v":1,"summary":"fake summary","upto_seq":1}`))
	return CompactResult{TokensBefore: 100, TokensAfter: 20}, nil
}

func (f *fakeWebBackend) Fork(_ context.Context, sessionID string) (Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.find(sessionID)
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	if err := f.leaseError(sessionID); err != nil {
		return Session{}, err
	}
	if f.turnActive[sessionID] {
		return Session{}, ErrInvalidState
	}
	completed := false
	for _, event := range f.events[sessionID] {
		if event.Kind == core.KindTurnEnd {
			completed = true
		}
	}
	if !completed {
		return Session{}, ErrInvalidState
	}
	now := time.Now().UnixMilli()
	child := f.addSessionLocked(core.SessionInfo{
		Agent:           info.Agent,
		Channel:         info.Channel,
		Address:         info.Address,
		Workspace:       info.Workspace,
		ParentSessionID: sessionID,
		InheritedSeq:    info.LastSeq,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	f.forked = append(f.forked, child.ID)
	return f.detail(child), nil
}

func (f *fakeWebBackend) SetAgent(_ context.Context, sessionID, agent string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(sessionID); !ok {
		return ErrSessionNotFound
	}
	if err := f.leaseError(sessionID); err != nil {
		return err
	}
	if f.turnActive[sessionID] {
		return ErrInvalidState
	}
	if !f.agentNames[agent] {
		return ErrUnknownAgent
	}
	f.update(sessionID, func(info *core.SessionInfo) { info.Agent = agent })
	f.appendEventLocked(sessionID, core.KindModelSelection, json.RawMessage(`{"v":1,"agent":"`+agent+`"}`))
	return nil
}

func (f *fakeWebBackend) SetModel(_ context.Context, sessionID, model string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(sessionID); !ok {
		return ErrSessionNotFound
	}
	if err := f.leaseError(sessionID); err != nil {
		return err
	}
	if f.turnActive[sessionID] {
		return ErrInvalidState
	}
	if f.isTeamLocked(sessionID) {
		return ErrTeamModel
	}
	if !validModel(model) {
		return ErrInvalidModel
	}
	f.models[sessionID] = model
	f.appendEventLocked(sessionID, core.KindModelSelection, json.RawMessage(`{"v":1}`))
	return nil
}

func (f *fakeWebBackend) SetReasoningEffort(_ context.Context, sessionID, effort string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(sessionID); !ok {
		return ErrSessionNotFound
	}
	if err := f.leaseError(sessionID); err != nil {
		return err
	}
	if f.turnActive[sessionID] {
		return ErrInvalidState
	}
	f.efforts[sessionID] = effort
	return nil
}

func (f *fakeWebBackend) SetPolicy(_ context.Context, sessionID, policy string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(sessionID); !ok {
		return ErrSessionNotFound
	}
	if err := f.leaseError(sessionID); err != nil {
		return err
	}
	if f.turnActive[sessionID] {
		return ErrInvalidState
	}
	switch policy {
	case "ask", "never", "trust":
	default:
		return ErrInvalidPolicy
	}
	f.policies[sessionID] = policy
	f.appendEventLocked(sessionID, core.KindApprovalPolicy, json.RawMessage(`{"v":1,"policy":"`+policy+`"}`))
	return nil
}

func (f *fakeWebBackend) appendEventLocked(sessionID, kind string, payload json.RawMessage) {
	seq := int64(len(f.events[sessionID]) + 1)
	f.events[sessionID] = append(f.events[sessionID], core.SessionEvent{
		SessionID: sessionID, Seq: seq, Kind: kind, Payload: payload,
	})
	f.update(sessionID, func(info *core.SessionInfo) { info.LastSeq = seq })
}

func (f *fakeWebBackend) Subscribe(_ context.Context, _ string) (*core.Subscription[core.LiveFrame], error) {
	sub := f.frames.Subscribe()
	select {
	case f.subscribed <- struct{}{}:
	default:
	}
	return sub, nil
}

func (f *fakeWebBackend) TurnActive(sessionID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.turnActive[sessionID] {
		return true
	}
	info, ok := f.find(sessionID)
	return ok && info.Status == "running"
}

func (f *fakeWebBackend) Holder(_ context.Context, sessionID string) (Holder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pid, ok := f.leaseHeld[sessionID]; ok {
		return Holder{State: HolderOther, PID: pid}, nil
	}
	if f.turnActive[sessionID] {
		return Holder{State: HolderSelf}, nil
	}
	if info, ok := f.find(sessionID); ok && info.Status == "running" {
		return Holder{State: HolderSelf}, nil
	}
	return Holder{State: HolderNone}, nil
}

// ListAgentSessions follows the same filter, sort, and limit rules as the store
// adapter, backing a room's segments and its newest session (REQ-ROOM-002).
func (f *fakeWebBackend) ListAgentSessions(_ context.Context, filter core.SessionFilter) ([]core.SessionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]core.SessionInfo, 0, len(f.sessions))
	for _, info := range f.sessions {
		if filter.Agent != "" && info.Agent != filter.Agent {
			continue
		}
		if filter.Channel != "" && info.Channel != filter.Channel {
			continue
		}
		if len(filter.ExcludeChannels) > 0 && slices.Contains(filter.ExcludeChannels, info.Channel) {
			continue
		}
		if filter.Address != "" && info.Address != filter.Address {
			continue
		}
		if filter.TopLevelOnly && info.ParentSessionID != "" {
			continue
		}
		if filter.Before > 0 && info.UpdatedAt >= filter.Before {
			continue
		}
		out = append(out, info)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return out[i].ID > out[j].ID
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// CountAgentEvents mirrors the store query: events of the kinds in the agent's
// sessions created after sinceMS (REQ-ROOM-002, REQ-ROOM-005).
func (f *fakeWebBackend) CountAgentEvents(_ context.Context, agent string, kinds []string, sinceMS int64, topLevelOnly bool) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wanted := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		wanted[kind] = true
	}
	var count int64
	for _, info := range f.sessions {
		if info.Agent != agent {
			continue
		}
		if topLevelOnly && info.ParentSessionID != "" {
			continue
		}
		for _, event := range f.events[info.ID] {
			if wanted[event.Kind] && event.CreatedAt > sinceMS {
				count++
			}
		}
	}
	return count, nil
}

// RoomReadAt implements web.Backend (REQ-ROOM-005).
func (f *fakeWebBackend) RoomReadAt(_ context.Context, room string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.roomReads[room], nil
}

// SetRoomRead implements web.Backend; the read time only moves forward
// (REQ-ROOM-005).
func (f *fakeWebBackend) SetRoomRead(_ context.Context, room string, at int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if current, ok := f.roomReads[room]; !ok || at > current {
		f.roomReads[room] = at
	}
	return f.roomReads[room], nil
}

// ScanEvents walks every session's events of the wanted kinds, in created_at
// order, and tags each with its owning session's agent, like the store query
// the agent overview reads (API-ROOM-008).
func (f *fakeWebBackend) ScanEvents(_ context.Context, kinds []string, sinceMS int64) iter.Seq2[core.ScannedEvent, error] {
	return func(yield func(core.ScannedEvent, error) bool) {
		f.mu.Lock()
		wanted := make(map[string]bool, len(kinds))
		for _, kind := range kinds {
			wanted[kind] = true
		}
		scanned := make([]core.ScannedEvent, 0)
		for _, info := range f.sessions {
			for _, event := range f.events[info.ID] {
				if !wanted[event.Kind] || event.CreatedAt < sinceMS {
					continue
				}
				scanned = append(scanned, core.ScannedEvent{SessionEvent: event, Agent: info.Agent})
			}
		}
		f.mu.Unlock()
		sort.SliceStable(scanned, func(i, j int) bool {
			if scanned[i].CreatedAt != scanned[j].CreatedAt {
				return scanned[i].CreatedAt < scanned[j].CreatedAt
			}
			return scanned[i].Seq < scanned[j].Seq
		})
		for _, event := range scanned {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func (f *fakeWebBackend) detail(info core.SessionInfo) Session {
	model := f.models[info.ID]
	if model == "" {
		model = "openai:gpt-5-mini"
	}
	policy := f.policies[info.ID]
	if policy == "" {
		policy = "ask"
	}
	return Session{
		SessionInfo:     info,
		Model:           model,
		Policy:          policy,
		ReasoningEffort: f.efforts[info.ID],
		Team:            f.teamLocked(info.Agent),
		TurnRunning:     info.Status == "running" || f.turnActive[info.ID],
		InheritedSeq:    info.InheritedSeq,
	}
}

func newSessionTestServer(t *testing.T, backend Backend) (*Server, string) {
	t.Helper()
	home := t.TempDir()
	token := authTokenFor(t, home)
	s, err := New(Config{Addr: authTestAddr, Enabled: true, Home: home})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.SetBackend(backend)
	return s, token
}

func newLoggedInSessionServer(t *testing.T, backend Backend) (*Server, *http.Cookie, string) {
	t.Helper()
	s, token := newSessionTestServer(t, backend)
	cookie, csrf := loginAndSession(t, s, token)
	return s, cookie, csrf
}

func sessionTestRequest(method, target, body string, cookie *http.Cookie, csrf string) *http.Request {
	req := authRequest(method, target, body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	return req
}

func TestSessionListAllChannelsAndPaging(t *testing.T) {
	backend := newFakeWebBackend()
	backend.addSession(core.SessionInfo{ID: id.New(), Channel: "cli", Address: "/work/helper", Agent: "helper", Title: "cli chat", TurnCount: 2, CreatedAt: 1000, UpdatedAt: 1000})
	backend.addSession(core.SessionInfo{ID: id.New(), Channel: "telegram", Address: "111", Agent: "helper", Title: "telegram chat", TurnCount: 3, CreatedAt: 2000, UpdatedAt: 2000})
	backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "coder", Title: "web chat", TurnCount: 1, CreatedAt: 3000, UpdatedAt: 3000})
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var all listSessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &all); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(all.Sessions) != 3 || all.NextBefore != nil {
		t.Fatalf("all sessions = %+v, next = %v", all.Sessions, all.NextBefore)
	}
	seen := map[string]bool{}
	for _, session := range all.Sessions {
		seen[session.Channel] = true
		if session.InUse != "none" {
			t.Fatalf("session %s in_use = %q", session.ID, session.InUse)
		}
	}
	for _, channel := range []string{"cli", "telegram", "web"} {
		if !seen[channel] {
			t.Fatalf("list has no %s session: %+v", channel, all.Sessions)
		}
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions?limit=2", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("limit status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var first listSessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Sessions) != 2 || first.Sessions[0].Channel != "web" || first.Sessions[1].Channel != "telegram" {
		t.Fatalf("first page = %+v", first.Sessions)
	}
	if first.NextBefore == nil || *first.NextBefore != 2000 {
		t.Fatalf("next_before = %v, want 2000", first.NextBefore)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions?limit=2&before=2000", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("page two status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var second listSessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Sessions) != 1 || second.Sessions[0].Channel != "cli" || second.NextBefore != nil {
		t.Fatalf("second page = %+v, next = %v", second.Sessions, second.NextBefore)
	}

	for _, target := range []string{
		"/v1/sessions?channel=unknown",
		"/v1/sessions?limit=0",
		"/v1/sessions?limit=51",
		"/v1/sessions?limit=one",
		"/v1/sessions?before=-1",
	} {
		rr := serveAuth(s, sessionTestRequest(http.MethodGet, target, "", cookie, ""))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	}
}

func TestSessionEventsEqualHistory(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "telegram", Address: "111", Agent: "helper", LastSeq: 5, CreatedAt: 1000, UpdatedAt: 5000})
	backend.events[info.ID] = []core.SessionEvent{
		{SessionID: info.ID, Seq: 1, Kind: core.KindSessionCreated, CreatedAt: 1000, Payload: json.RawMessage(`{"v":1}`)},
		{SessionID: info.ID, Seq: 2, Kind: core.KindUserMessage, TurnID: "turn-1", CreatedAt: 2000, Payload: json.RawMessage(`{"v":1,"content":[{"kind":"text","text":"hi"}]}`)},
		{SessionID: info.ID, Seq: 3, Kind: core.KindToolCall, TurnID: "turn-1", CreatedAt: 3000, Payload: json.RawMessage(`{"v":1,"call_id":"c1","name":"read_file","input":{}}`)},
		{SessionID: info.ID, Seq: 4, Kind: core.KindToolResult, TurnID: "turn-1", CreatedAt: 4000, Payload: json.RawMessage(`{"v":1,"call_id":"c1","status":"success","output":"ok"}`)},
		{SessionID: info.ID, Seq: 5, Kind: core.KindAssistantMessage, TurnID: "turn-1", CreatedAt: 5000, Payload: json.RawMessage(`{"v":1,"content":[{"kind":"text","text":"done"}],"stop_reason":"end_turn"}`)},
	}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/"+info.ID+"/events", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var page eventsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if page.LastSeq != 5 {
		t.Fatalf("last_seq = %d, want 5", page.LastSeq)
	}
	history := backend.events[info.ID]
	if len(page.Events) != len(history) {
		t.Fatalf("events = %d, history = %d", len(page.Events), len(history))
	}
	for i, event := range page.Events {
		want := history[i]
		if event.Seq != want.Seq || event.Kind != want.Kind || event.TurnID != want.TurnID || event.CreatedAt != want.CreatedAt || string(event.Payload) != string(want.Payload) {
			t.Fatalf("event %d = %+v, want %+v", i, event, want)
		}
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/"+info.ID+"/events?from_seq=2", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("from_seq status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var replay eventsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if len(replay.Events) != 3 || replay.Events[0].Seq != 3 || replay.Events[2].Seq != 5 {
		t.Fatalf("replay events = %+v", replay.Events)
	}
	if replay.LastSeq != 5 {
		t.Fatalf("replay last_seq = %d, want 5", replay.LastSeq)
	}

	for _, target := range []string{
		"/v1/sessions/" + info.ID + "/events?from_seq=-1",
		"/v1/sessions/" + info.ID + "/events?from_seq=no",
	} {
		rr := serveAuth(s, sessionTestRequest(http.MethodGet, target, "", cookie, ""))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/missing/events", "", cookie, ""))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

// REQ-LTC-005: the session usage endpoint sums step/end usage and reports the
// cached input tokens.
func TestSessionUsageIncludesCachedTokens(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Agent: "coder", LastSeq: 2, CreatedAt: 1000, UpdatedAt: 2000})
	backend.events[info.ID] = []core.SessionEvent{
		{SessionID: info.ID, Seq: 1, Kind: core.KindStepEnd, TurnID: "t1", CreatedAt: 1500, Payload: json.RawMessage(`{"v":1,"step":1,"usage":{"input_tokens":100,"output_tokens":5,"cached_input_tokens":80}}`)},
		{SessionID: info.ID, Seq: 2, Kind: core.KindStepEnd, TurnID: "t1", CreatedAt: 1600, Payload: json.RawMessage(`{"v":1,"step":2,"usage":{"input_tokens":50,"output_tokens":3,"cached_input_tokens":10}}`)},
	}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/"+info.ID+"/usage", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var usage sessionUsageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &usage); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if usage.InputTokens != 150 || usage.OutputTokens != 8 || usage.CachedInputTokens != 90 {
		t.Fatalf("usage = %+v", usage)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/missing/usage", "", cookie, ""))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

func TestCreateWebSessionWithModel(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agentNames["coder"] = true
	backend.agentNames["helper"] = true
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions", `{"agent":"coder","model":"openai:gpt-5-mini"}`, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var created sessionDetailJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if created.Channel != "web" || created.Address != "owner" || created.Agent != "coder" {
		t.Fatalf("created session = %+v", created)
	}
	if created.Model != "openai:gpt-5-mini" {
		t.Fatalf("created model = %q", created.Model)
	}
	if created.Policy != "ask" || created.TurnRunning {
		t.Fatalf("created policy = %q, turn_running = %v", created.Policy, created.TurnRunning)
	}
	if backend.createdAgent != "coder" || backend.createdModel != "openai:gpt-5-mini" {
		t.Fatalf("backend got agent=%q model=%q", backend.createdAgent, backend.createdModel)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/"+created.ID, "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var detail sessionDetailJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != created.ID || detail.Model != "openai:gpt-5-mini" || detail.Channel != "web" {
		t.Fatalf("detail = %+v", detail)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions", `{"agent":"missing"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "unknown_agent")
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions", `{"agent":"coder","model":"no-colon"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions", `{"model":"openai:gpt-5-mini"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/missing", "", cookie, ""))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

func TestAgentsList(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{
		{Name: "helper", Model: "openai:gpt-5-mini", Policy: "ask", Skills: []string{"go"}, MCP: []string{"github"}, MCPStatus: []MCPStatus{{Name: "github", State: "running"}}},
		{Name: "coder", Model: "openai:gpt-5", Policy: "never"},
	}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/agents", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body listAgentsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(body.Agents) != 2 || body.Agents[0].Name != "coder" || body.Agents[1].Name != "helper" {
		t.Fatalf("agents = %+v", body.Agents)
	}
	if body.Agents[0].Skills == nil || body.Agents[0].MCP == nil || body.Agents[0].MCPStatus == nil {
		t.Fatalf("coder lists must be empty arrays, not null: %+v", body.Agents[0])
	}
	if body.Agents[1].Model == nil || *body.Agents[1].Model != "openai:gpt-5-mini" || body.Agents[1].MCPStatus[0].Name != "github" || body.Agents[1].MCPStatus[0].State != "running" {
		t.Fatalf("helper = %+v", body.Agents[1])
	}
}

func TestPatchSessionSettings(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agentNames["coder"] = true
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	s, cookie, csrf := newLoggedInSessionServer(t, backend)
	target := "/v1/sessions/" + info.ID

	rr := serveAuth(s, sessionTestRequest(http.MethodPatch, target, `{"policy":"never"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("policy status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var detail sessionDetailJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Policy != "never" {
		t.Fatalf("policy = %q", detail.Policy)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, target, `{"policy":"trust"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, target, `{"policy":"trust","confirm_trust":true}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("trust status = %d, body = %q", rr.Code, rr.Body.String())
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, target, `{"agent":"coder","model":"openai:gpt-5"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("agent/model status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Agent != "coder" || detail.Model != "openai:gpt-5" {
		t.Fatalf("detail = %+v", detail)
	}

	for _, body := range []string{`{}`, `{"policy":"sometimes"}`, `{"agent":""}`, `{"model":"no-colon"}`, `{"policy":"never","extra":true}`} {
		rr := serveAuth(s, sessionTestRequest(http.MethodPatch, target, body, cookie, csrf))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d, body = %q", body, rr.Code, rr.Body.String())
		}
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, target, `{"agent":"missing"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "unknown_agent")
	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, "/v1/sessions/missing", `{"policy":"never"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")

	events, err := backend.Events(context.Background(), info.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var policyEvents int
	for _, event := range events.Events {
		if event.Kind == core.KindApprovalPolicy {
			policyEvents++
		}
	}
	if policyEvents != 2 {
		t.Fatalf("approval/policy events = %d, want 2", policyEvents)
	}

	backend.setTurnActive(info.ID, true)
	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, target, `{"policy":"never"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "invalid_state")
}

func TestCompactAndForkSession(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindTurnEnd, Payload: json.RawMessage(`{"v":1,"turn_id":"t1","reason":"end_turn"}`)})
	empty := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/compact", "", cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("compact status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var compact struct {
		TokensBefore int64 `json:"tokens_before"`
		TokensAfter  int64 `json:"tokens_after"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &compact); err != nil {
		t.Fatal(err)
	}
	if compact.TokensBefore != 100 || compact.TokensAfter != 20 {
		t.Fatalf("compact = %+v", compact)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/fork", "", cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("fork status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var forked sessionDetailJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &forked); err != nil {
		t.Fatal(err)
	}
	if forked.ParentSessionID != info.ID || forked.InheritedSeq == 0 || forked.ID == info.ID {
		t.Fatalf("forked = %+v", forked)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+empty.ID+"/fork", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "invalid_state")

	events, err := backend.Events(context.Background(), info.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events.Events {
		if event.Kind == core.KindCompactionSummary {
			found = true
		}
	}
	if !found {
		t.Fatal("compaction/summary event is missing")
	}

	backend.setTurnActive(info.ID, true)
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/compact", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "invalid_state")
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/fork", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "invalid_state")

	backend.mu.Lock()
	backend.leaseHeld[info.ID] = 99
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/compact", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "session_in_use")
}

func TestSessionRoutesRequireAuth(t *testing.T) {
	backend := newFakeWebBackend()
	backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	s, token := newSessionTestServer(t, backend)

	targets := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/v1/sessions", ""},
		{http.MethodPost, "/v1/sessions", `{"agent":"helper"}`},
		{http.MethodGet, "/v1/sessions/" + backend.sessions[0].ID, ""},
		{http.MethodGet, "/v1/sessions/" + backend.sessions[0].ID + "/events", ""},
		{http.MethodGet, "/v1/sessions/" + backend.sessions[0].ID + "/stream", ""},
		{http.MethodPatch, "/v1/sessions/" + backend.sessions[0].ID, `{"policy":"never"}`},
		{http.MethodPost, "/v1/sessions/" + backend.sessions[0].ID + "/compact", ""},
		{http.MethodPost, "/v1/sessions/" + backend.sessions[0].ID + "/fork", ""},
		{http.MethodGet, "/v1/agents", ""},
	}
	for _, target := range targets {
		req := authRequest(target.method, target.path, target.body)
		rr := serveAuth(s, req)
		assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
	}

	cookie, _ := loginAndSession(t, s, token)
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions", `{"agent":"helper"}`, cookie, ""))
	assertAuthError(t, rr, http.StatusForbidden, "csrf_invalid")

	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("authenticated list status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestCreateSessionRejectsBadJSONBody(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agentNames["helper"] = true
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	for _, body := range []string{``, `{`, `[]`, `{"agent":"helper","extra":true}`, `{"agent":"helper"}{"agent":"coder"}`} {
		rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions", body, cookie, csrf))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d, body = %q", body, rr.Code, rr.Body.String())
		}
		var envelope errorEnvelope
		if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("body %q response %q: %v", body, rr.Body.String(), err)
		}
		if envelope.Error.Code != "invalid_request" {
			t.Fatalf("body %q error code = %q", body, envelope.Error.Code)
		}
	}
}

func TestSessionDetailModelFallsBackToAgent(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "cli", Address: "/work", Agent: "helper", Workspace: "/work", ParentSessionID: id.New(), Status: "running", CreatedAt: 1000, UpdatedAt: 2000})
	backend.models[info.ID] = ""
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/"+info.ID, "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var detail sessionDetailJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Model != "openai:gpt-5-mini" || detail.Policy != "ask" {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.Workspace != "/work" || detail.ParentSessionID != info.ParentSessionID {
		t.Fatalf("detail workspace/parent = %+v", detail)
	}
	if !detail.TurnRunning {
		t.Fatal("turn_running = false for a running session")
	}
}

// Ensure the fake's cursor rule and the JSON contract agree on the null cursor.
func TestNextBeforeIsNullOnLastPage(t *testing.T) {
	backend := newFakeWebBackend()
	backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper", UpdatedAt: 1000})
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions", "", cookie, ""))
	if !strings.Contains(rr.Body.String(), `"next_before":null`) {
		t.Fatalf("body = %q, want a null next_before", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), fmt.Sprintf(`"id":%q`, backend.sessions[0].ID)) {
		t.Fatalf("body = %q, want the session id", rr.Body.String())
	}
}

// sessionListFixture builds one session for each status plus a library session
// that must stay out of the channel facet (Q-023).
func sessionListFixture(t *testing.T) (*fakeWebBackend, map[string]string) {
	t.Helper()
	backend := newFakeWebBackend()
	ids := map[string]string{}

	running := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "coder", Title: "disk cleanup", Status: "running", TurnCount: 1, CreatedAt: 1000, UpdatedAt: 5000})
	ids["running"] = running.ID
	addSessionEvent(t, backend, running.ID, core.KindToolCall, core.ToolCallPayload{V: 1, CallID: "c-running", Name: "read_file", Input: json.RawMessage(`{"path":"disk.go"}`)})

	waiting := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "cli", Address: "/work", Agent: "helper", Title: "approval needed", Status: "idle", CreatedAt: 2000, UpdatedAt: 4000})
	ids["waiting"] = waiting.ID
	addSessionEvent(t, backend, waiting.ID, core.KindToolCall, core.ToolCallPayload{V: 1, CallID: "c-waiting", Name: "bash", Input: json.RawMessage(`{"command":"go test ./..."}`)})
	addSessionEvent(t, backend, waiting.ID, core.KindApprovalAsked, core.ApprovalAsked{V: 1, RequestID: "req-1", CallID: "c-waiting", Tool: "bash", Risk: core.RiskExec})

	failed := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "telegram", Address: "111", Agent: "helper", Title: "failed chat", Status: "idle", CreatedAt: 3000, UpdatedAt: 3000})
	ids["failed"] = failed.ID
	addSessionEvent(t, backend, failed.ID, core.KindTurnEnd, core.TurnEnd{V: 1, TurnID: "turn-failed", Reason: "error", Error: &core.PayloadError{Class: "provider", Message: "disk full"}})

	idle := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "coder", Title: "idle chat", Status: "idle", CreatedAt: 4000, UpdatedAt: 2000})
	ids["idle"] = idle.ID
	addSessionEvent(t, backend, idle.ID, core.KindAssistantMessage, core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "all done"}}})

	library := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "library", Address: "embed", Agent: "coder", Title: "library chat", Status: "idle", CreatedAt: 5000, UpdatedAt: 1000})
	ids["library"] = library.ID

	return backend, ids
}

func addSessionEvent(t *testing.T, backend *fakeWebBackend, sessionID, kind string, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", kind, err)
	}
	backend.appendEvent(sessionID, core.SessionEvent{Kind: kind, Payload: data})
}

func listSessionsForTest(t *testing.T, s *Server, cookie *http.Cookie, target string) listSessionsResponse {
	t.Helper()
	rr := serveAuth(s, sessionTestRequest(http.MethodGet, target, "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, body = %q", target, rr.Code, rr.Body.String())
	}
	var body listSessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET %s body %q: %v", target, rr.Body.String(), err)
	}
	return body
}

func sessionStatusesByID(body listSessionsResponse) map[string]string {
	out := map[string]string{}
	for _, session := range body.Sessions {
		out[session.ID] = session.Status
	}
	return out
}

// facetAgent returns the agent facet entry of a name.
func facetAgent(facets sessionFacets, name string) (agentFacet, bool) {
	for _, entry := range facets.Agent {
		if entry.Name == name {
			return entry, true
		}
	}
	return agentFacet{}, false
}

func facetAgentCount(facets sessionFacets, name string) int {
	entry, _ := facetAgent(facets, name)
	return entry.Count
}

// AC-WEB-040: the list derives all status values from the log.
func TestSessionListAllDerivedStatuses(t *testing.T) {
	backend, ids := sessionListFixture(t)
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	body := listSessionsForTest(t, s, cookie, "/v1/sessions")
	if len(body.Sessions) != 5 {
		t.Fatalf("sessions = %d, want 5: %+v", len(body.Sessions), body.Sessions)
	}
	want := map[string]string{
		ids["running"]: SessionStatusRunning,
		ids["waiting"]: SessionStatusWaitingApproval,
		ids["failed"]:  SessionStatusFailed,
		ids["idle"]:    SessionStatusIdle,
		ids["library"]: SessionStatusIdle,
	}
	got := sessionStatusesByID(body)
	for sessionID, status := range want {
		if got[sessionID] != status {
			t.Fatalf("session %s status = %q, want %q; rows = %+v", sessionID, got[sessionID], status, body.Sessions)
		}
	}
	for _, session := range body.Sessions {
		if session.Status == SessionStatusWaitingInput {
			t.Fatalf("session %s produced waiting_input without an open question", session.ID)
		}
	}
	previews := map[string]string{}
	for _, session := range body.Sessions {
		previews[session.ID] = session.Preview
	}
	if previews[ids["running"]] != "Running: read_file disk.go" {
		t.Fatalf("running preview = %q", previews[ids["running"]])
	}
	if previews[ids["waiting"]] != "Waiting for your approval: bash go test ./..." {
		t.Fatalf("waiting preview = %q", previews[ids["waiting"]])
	}
	if previews[ids["failed"]] != "disk full" {
		t.Fatalf("failed preview = %q", previews[ids["failed"]])
	}
	if previews[ids["idle"]] != "all done" {
		t.Fatalf("idle preview = %q", previews[ids["idle"]])
	}
}

// AC-ASK-019 (list part): waiting_input is derived from an open question in
// the log and clears on question/answered.
func TestSessionListWaitingInputFromLog(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper", Title: "question", Status: "idle", CreatedAt: 1000, UpdatedAt: 1000})
	addSessionEvent(t, backend, info.ID, core.KindQuestionAsked, core.QuestionAsked{V: 1, RequestID: "q-1", CallID: "c-1", ExpiresAt: time.Now().Add(time.Minute).UnixMilli()})

	s, cookie, _ := newLoggedInSessionServer(t, backend)
	all := listSessionsForTest(t, s, cookie, "/v1/sessions")
	if got := sessionStatusesByID(all); got[info.ID] != SessionStatusWaitingInput {
		t.Fatalf("status = %q, want %q", got[info.ID], SessionStatusWaitingInput)
	}
	if all.Facets.Status[SessionStatusWaitingInput] != 1 {
		t.Fatalf("waiting_input facet = %d, want 1", all.Facets.Status[SessionStatusWaitingInput])
	}
	waiting := listSessionsForTest(t, s, cookie, "/v1/sessions?status=waiting_input")
	if len(waiting.Sessions) != 1 || waiting.Sessions[0].ID != info.ID {
		t.Fatalf("waiting_input rows = %+v", waiting.Sessions)
	}

	addSessionEvent(t, backend, info.ID, core.KindQuestionAnswered, core.QuestionAnswered{V: 1, RequestID: "q-1", CallID: "c-1", Outcome: core.QuestionStatusAnswered, Responder: "web"})
	all = listSessionsForTest(t, s, cookie, "/v1/sessions")
	if got := sessionStatusesByID(all); got[info.ID] != SessionStatusIdle {
		t.Fatalf("status after answer = %q, want %q", got[info.ID], SessionStatusIdle)
	}
}

// waiting_input also covers an open request the broker still holds, even
// before its question/asked write is read back.
func TestSessionListWaitingInputFromBroker(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper", Title: "question", Status: "idle", CreatedAt: 1000, UpdatedAt: 1000})
	s, cookie, csrf := newLoggedInSessionServer(t, backend)
	event, outcomes := askQuestionForTest(t, s.Questions(), info.ID)

	all := listSessionsForTest(t, s, cookie, "/v1/sessions")
	if got := sessionStatusesByID(all); got[info.ID] != SessionStatusWaitingInput {
		t.Fatalf("status = %q, want %q", got[info.ID], SessionStatusWaitingInput)
	}

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/questions/"+event.RequestID, `{"answers":[{"question":0,"selected":[0]}]}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("answer status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if outcome := waitQuestionOutcome(t, outcomes); outcome.Status != core.QuestionStatusAnswered {
		t.Fatalf("outcome = %+v", outcome)
	}
	all = listSessionsForTest(t, s, cookie, "/v1/sessions")
	if got := sessionStatusesByID(all); got[info.ID] != SessionStatusIdle {
		t.Fatalf("status after answer = %q, want %q", got[info.ID], SessionStatusIdle)
	}
}

// AC-WEB-040: Status "Waiting approval" returns only the waiting session.
func TestSessionListAllStatusFilterWaitingApproval(t *testing.T) {
	backend, ids := sessionListFixture(t)
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	body := listSessionsForTest(t, s, cookie, "/v1/sessions?status=waiting_approval")
	if len(body.Sessions) != 1 || body.Sessions[0].ID != ids["waiting"] {
		t.Fatalf("waiting_approval rows = %+v", body.Sessions)
	}
	body = listSessionsForTest(t, s, cookie, "/v1/sessions?status=running,idle")
	if len(body.Sessions) != 3 {
		t.Fatalf("running,idle rows = %d, want 3: %+v", len(body.Sessions), body.Sessions)
	}
	for _, session := range body.Sessions {
		if session.Status != SessionStatusRunning && session.Status != SessionStatusIdle {
			t.Fatalf("row %s status = %q", session.ID, session.Status)
		}
	}
}

// AC-WEB-040: q matches the title or the preview.
func TestSessionListAllSearchTitleOrPreview(t *testing.T) {
	backend, ids := sessionListFixture(t)
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	body := listSessionsForTest(t, s, cookie, "/v1/sessions?q=disk")
	if len(body.Sessions) != 2 {
		t.Fatalf("q=disk rows = %+v", body.Sessions)
	}
	got := sessionStatusesByID(body)
	if got[ids["running"]] != SessionStatusRunning || got[ids["failed"]] != SessionStatusFailed {
		t.Fatalf("q=disk statuses = %+v", got)
	}
	body = listSessionsForTest(t, s, cookie, "/v1/sessions?q=all%20done")
	if len(body.Sessions) != 1 || body.Sessions[0].ID != ids["idle"] {
		t.Fatalf("q=all done rows = %+v", body.Sessions)
	}
}

// AC-WEB-040 and Q-023: facets count the q matches only and keep three
// channel entries even when a library session is in the list.
func TestSessionListFacetCountsUseSearchOnly(t *testing.T) {
	backend, _ := sessionListFixture(t)
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	filtered := listSessionsForTest(t, s, cookie, "/v1/sessions?q=disk&status=waiting_approval&agent=nobody&channel=web")
	if len(filtered.Sessions) != 0 {
		t.Fatalf("filtered rows = %+v, want none", filtered.Sessions)
	}
	unfiltered := listSessionsForTest(t, s, cookie, "/v1/sessions?q=disk")
	if len(unfiltered.Sessions) != 2 {
		t.Fatalf("q=disk rows = %+v", unfiltered.Sessions)
	}
	if filtered.Facets.Channel["web"] != unfiltered.Facets.Channel["web"] ||
		filtered.Facets.Channel["telegram"] != unfiltered.Facets.Channel["telegram"] ||
		filtered.Facets.Status[SessionStatusRunning] != unfiltered.Facets.Status[SessionStatusRunning] ||
		filtered.Facets.Status[SessionStatusFailed] != unfiltered.Facets.Status[SessionStatusFailed] ||
		facetAgentCount(filtered.Facets, "coder") != facetAgentCount(unfiltered.Facets, "coder") ||
		facetAgentCount(filtered.Facets, "helper") != facetAgentCount(unfiltered.Facets, "helper") {
		t.Fatalf("filtered facets = %+v, unfiltered facets = %+v", filtered.Facets, unfiltered.Facets)
	}
	if unfiltered.Facets.Channel["web"] != 1 || unfiltered.Facets.Channel["telegram"] != 1 || unfiltered.Facets.Channel["cli"] != 0 {
		t.Fatalf("q=disk channel facets = %+v", unfiltered.Facets.Channel)
	}
	if unfiltered.Facets.Status[SessionStatusRunning] != 1 || unfiltered.Facets.Status[SessionStatusFailed] != 1 {
		t.Fatalf("q=disk status facets = %+v", unfiltered.Facets.Status)
	}

	all := listSessionsForTest(t, s, cookie, "/v1/sessions")
	if all.Facets.Channel["web"] != 2 || all.Facets.Channel["cli"] != 1 || all.Facets.Channel["telegram"] != 1 {
		t.Fatalf("channel facets = %+v", all.Facets.Channel)
	}
	if _, ok := all.Facets.Channel["library"]; ok {
		t.Fatalf("channel facets include library: %+v", all.Facets.Channel)
	}
	if all.Facets.Status[SessionStatusIdle] != 2 {
		t.Fatalf("idle facet = %d, want 2", all.Facets.Status[SessionStatusIdle])
	}
	if facetAgentCount(all.Facets, "coder") != 3 || facetAgentCount(all.Facets, "helper") != 2 {
		t.Fatalf("agent facets = %+v", all.Facets.Agent)
	}
	if all.Facets.Status[SessionStatusWaitingInput] != 0 {
		t.Fatalf("waiting_input facet = %d, want 0", all.Facets.Status[SessionStatusWaitingInput])
	}
}

// AC-WEB-040: "Clear all" returns every row again.
func TestSessionListAllClearFiltersReturnsAll(t *testing.T) {
	backend, _ := sessionListFixture(t)
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	filtered := listSessionsForTest(t, s, cookie, "/v1/sessions?q=disk&status=failed&agent=helper&channel=telegram")
	if len(filtered.Sessions) != 1 || filtered.Sessions[0].Status != SessionStatusFailed {
		t.Fatalf("filtered rows = %+v", filtered.Sessions)
	}
	cleared := listSessionsForTest(t, s, cookie, "/v1/sessions")
	if len(cleared.Sessions) != 5 {
		t.Fatalf("cleared rows = %d, want 5: %+v", len(cleared.Sessions), cleared.Sessions)
	}
	if cleared.Facets.Channel["web"] != 2 || cleared.Facets.Status[SessionStatusWaitingApproval] != 1 {
		t.Fatalf("cleared facets = %+v", cleared.Facets)
	}
}

// REQ-WEB-037: the preview is whitespace-normalized and cut at 120 characters.
func TestSessionListPreviewTruncated(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper", Title: "long answer", Status: "idle", UpdatedAt: 1000})
	addSessionEvent(t, backend, info.ID, core.KindAssistantMessage, core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "  long\n\tanswer\t" + strings.Repeat("disk ", 100)}}})
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	body := listSessionsForTest(t, s, cookie, "/v1/sessions")
	if len(body.Sessions) != 1 {
		t.Fatalf("rows = %+v", body.Sessions)
	}
	preview := body.Sessions[0].Preview
	if len([]rune(preview)) != previewLimit {
		t.Fatalf("preview length = %d, want %d: %q", len([]rune(preview)), previewLimit, preview)
	}
	if !strings.HasSuffix(preview, "…") {
		t.Fatalf("preview = %q, want an ellipsis", preview)
	}
	if strings.Contains(preview, "  ") || strings.Contains(preview, "\n") || strings.Contains(preview, "\t") {
		t.Fatalf("preview is not whitespace-normalized: %q", preview)
	}
}

// REQ-MDL-006: the preview shows the answer text and ignores the thinking
// block of the same assistant message.
func TestSessionListPreviewSkipsThinking(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper", Title: "thinking", Status: "idle", UpdatedAt: 1000})
	addSessionEvent(t, backend, info.ID, core.KindAssistantMessage, core.AssistantMessage{V: 1, Content: []core.ContentBlock{
		{Kind: core.BlockThinking, Text: "private reasoning"},
		{Kind: core.BlockText, Text: "the answer"},
	}})
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	body := listSessionsForTest(t, s, cookie, "/v1/sessions")
	if len(body.Sessions) != 1 {
		t.Fatalf("rows = %+v", body.Sessions)
	}
	if preview := body.Sessions[0].Preview; preview != "the answer" {
		t.Fatalf("preview = %q, want %q", preview, "the answer")
	}
}

func TestSessionListAllInvalidStatusIsRejected(t *testing.T) {
	backend, _ := sessionListFixture(t)
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	for _, target := range []string{
		"/v1/sessions?status=waiting",
		"/v1/sessions?status=bogus",
		"/v1/sessions?status=running,bogus",
	} {
		rr := serveAuth(s, sessionTestRequest(http.MethodGet, target, "", cookie, ""))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	}
	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions?status=waiting_input", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("waiting_input status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// AC-TEAM-032: the default list returns only root sessions, while the detail
// of a child stays readable and carries parent_session_id.
func TestSessionListChildrenCoversACTEAM032(t *testing.T) {
	backend := newFakeWebBackend()
	root := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper", Title: "root", UpdatedAt: 3000})
	childOne := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "worker", ParentSessionID: root.ID, Title: "child one", UpdatedAt: 2000})
	backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "worker", ParentSessionID: root.ID, Title: "child two", UpdatedAt: 1000})
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	body := listSessionsForTest(t, s, cookie, "/v1/sessions")
	if len(body.Sessions) != 1 || body.Sessions[0].ID != root.ID {
		t.Fatalf("list rows = %+v, want only root %s", body.Sessions, root.ID)
	}

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/"+childOne.ID, "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("child detail status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var detail sessionDetailJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatalf("child detail body %q: %v", rr.Body.String(), err)
	}
	if detail.ID != childOne.ID || detail.ParentSessionID != root.ID {
		t.Fatalf("child detail = %+v, want parent_session_id %s", detail, root.ID)
	}
}

// AC-TEAM-036: a team session's detail has team = {name, lead, members}; a
// plain agent session has team = null.
func TestSessionDetailTeamCoversACTEAM036(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{
		Name: "research-squad", Kind: "team", Lead: "lead",
		Members: []string{"researcher", "writer"}, Policy: "ask",
	}}
	teamSession := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "research-squad", UpdatedAt: 2000})
	plainSession := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "coder", UpdatedAt: 1000})
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/"+teamSession.ID, "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("team detail status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var teamDetail sessionDetailJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &teamDetail); err != nil {
		t.Fatalf("team detail body %q: %v", rr.Body.String(), err)
	}
	if teamDetail.Team == nil {
		t.Fatalf("team detail has no team: %s", rr.Body.String())
	}
	if teamDetail.Team.Name != "research-squad" || teamDetail.Team.Lead != "lead" ||
		!reflect.DeepEqual(teamDetail.Team.Members, []string{"researcher", "writer"}) {
		t.Fatalf("team detail = %+v", teamDetail.Team)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/"+plainSession.ID, "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("plain detail status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var plainDetail sessionDetailJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &plainDetail); err != nil {
		t.Fatalf("plain detail body %q: %v", rr.Body.String(), err)
	}
	if plainDetail.Team != nil {
		t.Fatalf("plain detail team = %+v, want null", plainDetail.Team)
	}
	if !strings.Contains(rr.Body.String(), `"team":null`) {
		t.Fatalf("plain detail body has no team:null: %s", rr.Body.String())
	}
}

// AC-TEAM-037: the agent filter accepts a team name, and facets.agent lists
// the team and the agent with their kind.
func TestSessionListAgentFilterAndFacetsCoversACTEAM037(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{
		{Name: "research-squad", Kind: "team", Lead: "lead", Members: []string{"researcher"}, Policy: "ask"},
		{Name: "coder", Kind: "agent", Model: "openai:gpt-5", Policy: "ask"},
	}
	teamSession := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "research-squad", UpdatedAt: 2000})
	backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "coder", UpdatedAt: 1000})
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	body := listSessionsForTest(t, s, cookie, "/v1/sessions?agent=research-squad")
	if len(body.Sessions) != 1 || body.Sessions[0].ID != teamSession.ID {
		t.Fatalf("team rows = %+v, want only %s", body.Sessions, teamSession.ID)
	}
	teamFacet, ok := facetAgent(body.Facets, "research-squad")
	if !ok || teamFacet.Kind != "team" || teamFacet.Count != 1 {
		t.Fatalf("team facet = %+v", body.Facets.Agent)
	}
	coderFacet, ok := facetAgent(body.Facets, "coder")
	if !ok || coderFacet.Kind != "agent" || coderFacet.Count != 1 {
		t.Fatalf("coder facet = %+v", body.Facets.Agent)
	}

	unknown := listSessionsForTest(t, s, cookie, "/v1/sessions?agent=missing")
	if len(unknown.Sessions) != 0 {
		t.Fatalf("unknown agent rows = %+v, want none", unknown.Sessions)
	}
}

// AC-TEAM-038: GET /v1/agents mixes teams with agents and marks each row with
// its kind; a team row has lead, members, model null, and the lead's policy.
func TestAgentsListTeamsCoversACTEAM038(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{
		{Name: "default", Kind: "agent", Model: "openai:gpt-5-mini", Policy: "ask"},
		{Name: "coder", Kind: "agent", Model: "openai:gpt-5", Policy: "never"},
		{Name: "research-squad", Kind: "team", Lead: "default", Members: []string{"researcher", "writer"}, Policy: "trust", Description: "Research a topic"},
	}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/agents", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body listAgentsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(body.Agents) != 3 {
		t.Fatalf("agents = %+v, want three rows", body.Agents)
	}
	if body.Agents[0].Name != "coder" || body.Agents[1].Name != "default" || body.Agents[2].Name != "research-squad" {
		t.Fatalf("agents are not sorted by name: %+v", body.Agents)
	}
	for _, row := range body.Agents[:2] {
		if row.Kind != "agent" {
			t.Fatalf("agent row %s kind = %q", row.Name, row.Kind)
		}
	}
	team := body.Agents[2]
	if team.Kind != "team" || team.Lead != "default" ||
		!reflect.DeepEqual(team.Members, []string{"researcher", "writer"}) ||
		team.Model != nil || team.Policy != "trust" || team.Description != "Research a topic" {
		t.Fatalf("team row = %+v", team)
	}
	if team.Skills == nil || len(team.Skills) != 0 || team.MCP == nil || len(team.MCP) != 0 || team.MCPStatus == nil || len(team.MCPStatus) != 0 {
		t.Fatalf("team skills/mcp/mcp_status = %+v", team)
	}
}

// REQ-TEAM-022: PATCH of a team session's model is invalid_request.
func TestPatchTeamSessionModelCoversTeamModelError(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "research-squad", Kind: "team", Lead: "lead", Members: []string{"researcher"}, Policy: "ask"}}
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "research-squad"})
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPatch, "/v1/sessions/"+info.ID, `{"model":"openai:gpt-5"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}

// REQ-MDL-001: PATCH accepts the four session efforts, rejects an unknown one,
// and an empty value ("Model default") clears it.
func TestPatchSessionReasoningEffort(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "coder"})
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	for _, effort := range []string{"off", "low", "high", "max"} {
		rr := serveAuth(s, sessionTestRequest(http.MethodPatch, "/v1/sessions/"+info.ID, `{"reasoning_effort":"`+effort+`"}`, cookie, csrf))
		if rr.Code != http.StatusOK {
			t.Fatalf("effort %q status = %d, body = %q", effort, rr.Code, rr.Body.String())
		}
		var detail sessionDetailJSON
		if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		if detail.ReasoningEffort != effort {
			t.Fatalf("effort %q read back = %q", effort, detail.ReasoningEffort)
		}
	}

	// "Model default" clears the session effort.
	rr := serveAuth(s, sessionTestRequest(http.MethodPatch, "/v1/sessions/"+info.ID, `{"reasoning_effort":""}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var cleared sessionDetailJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.ReasoningEffort != "" {
		t.Fatalf("cleared effort = %q, want empty", cleared.ReasoningEffort)
	}

	// An unknown value is rejected with the endpoint's error style.
	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, "/v1/sessions/"+info.ID, `{"reasoning_effort":"turbo"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}
