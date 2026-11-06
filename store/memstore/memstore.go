// Package memstore provides an in-memory core.SessionStore.
package memstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

// ErrNotSupported is returned for operations memstore does not support.
var ErrNotSupported = errors.New("not supported by memstore")

// ErrTurnActive is returned when a turn/start arrives while a turn is open.
var ErrTurnActive = errors.New("a turn is still running")

type session struct {
	info   core.SessionInfo
	events []core.SessionEvent
	seq    int64
}

// Store is an in-memory SessionStore.
type Store struct {
	mu       sync.Mutex
	sessions map[string]*session
}

// New creates an empty Store.
func New() *Store { return &Store{sessions: map[string]*session{}} }

func nowMS() int64 { return time.Now().UnixMilli() }

func resolveWorkspace(workspace string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("workspace is required")
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// Create creates a new session and writes session/created.
func (s *Store) Create(ctx context.Context, in core.NewSession) (core.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return core.SessionInfo{}, err
	}
	workspace, err := resolveWorkspace(in.Workspace)
	if err != nil {
		return core.SessionInfo{}, err
	}
	now := nowMS()
	info := core.SessionInfo{
		ID:              id.New(),
		Agent:           in.Agent,
		Channel:         in.Channel,
		Address:         in.Address,
		Workspace:       workspace,
		Status:          "idle",
		ParentSessionID: in.ParentSessionID,
		InheritedSeq:    in.InheritedSeq,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	sess := &session{info: info}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.ParentSessionID != "" {
		if _, ok := s.sessions[in.ParentSessionID]; !ok {
			return core.SessionInfo{}, fmt.Errorf("%w: parent session %s", core.ErrNotFound, in.ParentSessionID)
		}
	}
	sess.seq = in.InheritedSeq
	s.sessions[info.ID] = sess
	payload := core.SessionCreated{
		V:               core.PayloadVersion,
		Agent:           in.Agent,
		Channel:         in.Channel,
		Address:         in.Address,
		Workspace:       workspace,
		ParentSessionID: in.ParentSessionID,
		ParentCallID:    in.ParentCallID,
		InheritedSeq:    in.InheritedSeq,
	}
	if _, err := s.appendLocked(sess, []core.EventDraft{{Kind: core.KindSessionCreated, Payload: payload}}); err != nil {
		delete(s.sessions, info.ID)
		return core.SessionInfo{}, err
	}
	return s.sessions[info.ID].info, nil
}

// Open opens a session (without repair it is the same as Info).
func (s *Store) Open(ctx context.Context, sessionID string) (core.SessionInfo, error) {
	return s.Info(ctx, sessionID)
}

// Info reads the session projection without repair.
func (s *Store) Info(ctx context.Context, sessionID string) (core.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return core.SessionInfo{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return core.SessionInfo{}, fmt.Errorf("%w: session %s", core.ErrNotFound, sessionID)
	}
	return sess.info, nil
}

// Append writes drafts in one transaction.
func (s *Store) Append(ctx context.Context, sessionID string, drafts []core.EventDraft) ([]core.SessionEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("%w: session %s", core.ErrNotFound, sessionID)
	}
	events, err := s.appendLocked(sess, drafts)
	if err != nil {
		return nil, err
	}
	return append([]core.SessionEvent(nil), events...), nil
}

func (s *Store) appendLocked(sess *session, drafts []core.EventDraft) ([]core.SessionEvent, error) {
	now := nowMS()
	type staged struct {
		kind    string
		payload json.RawMessage
		turnID  string
		seq     int64
	}
	status := sess.info.Status
	seq := sess.seq
	stagedEvents := make([]staged, 0, len(drafts))
	turnCount := int64(0)
	for _, draft := range drafts {
		if draft.Kind == core.KindTurnStart && (status == "running" || status == "waiting_input") {
			return nil, ErrTurnActive
		}
		payload, err := json.Marshal(draft.Payload)
		if err != nil {
			return nil, err
		}
		seq++
		stagedEvents = append(stagedEvents, staged{kind: draft.Kind, payload: payload, turnID: draft.TurnID, seq: seq})
		switch draft.Kind {
		case core.KindTurnStart:
			status = "running"
			turnCount++
		case core.KindTurnEnd:
			status = "idle"
		case core.KindQuestionAsked:
			status = "waiting_input"
		case core.KindQuestionAnswered:
			status = "running"
		}
	}
	out := make([]core.SessionEvent, 0, len(stagedEvents))
	for _, e := range stagedEvents {
		event := core.SessionEvent{
			SessionID: sess.info.ID,
			Seq:       e.seq,
			Kind:      e.kind,
			Payload:   e.payload,
			TurnID:    e.turnID,
			CreatedAt: now,
		}
		sess.events = append(sess.events, event)
		out = append(out, event)
	}
	sess.seq = seq
	sess.info.Status = status
	sess.info.TurnCount += turnCount
	sess.info.LastSeq = seq
	sess.info.UpdatedAt = now
	return out, nil
}

// Events returns events with seq >= fromSeq.
func (s *Store) Events(ctx context.Context, sessionID string, fromSeq int64) iter.Seq2[core.SessionEvent, error] {
	return func(yield func(core.SessionEvent, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(core.SessionEvent{}, err)
			return
		}
		s.mu.Lock()
		sess, ok := s.sessions[sessionID]
		if !ok {
			s.mu.Unlock()
			yield(core.SessionEvent{}, fmt.Errorf("%w: session %s", core.ErrNotFound, sessionID))
			return
		}
		events := append([]core.SessionEvent(nil), sess.events...)
		s.mu.Unlock()
		for _, event := range events {
			if event.Seq < fromSeq {
				continue
			}
			if err := ctx.Err(); err != nil {
				yield(core.SessionEvent{}, err)
				return
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}

// ScanEvents implements core.EventScanner by iterating every session's events.
// It mirrors the SQLite scan: the given kinds, created at or after sinceMS, in
// (created_at, session_id, seq) order, with a reduced tool/result payload so a
// large tool output is not carried (design.md §2, REQ-DSH-001).
func (s *Store) ScanEvents(ctx context.Context, kinds []string, sinceMS int64) iter.Seq2[core.ScannedEvent, error] {
	return func(yield func(core.ScannedEvent, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(core.ScannedEvent{}, err)
			return
		}
		if len(kinds) == 0 {
			return
		}
		want := make(map[string]bool, len(kinds))
		for _, kind := range kinds {
			want[kind] = true
		}
		s.mu.Lock()
		var out []core.ScannedEvent
		for _, sess := range s.sessions {
			for _, event := range sess.events {
				if !want[event.Kind] || event.CreatedAt < sinceMS {
					continue
				}
				scanned := core.ScannedEvent{SessionEvent: event, Agent: sess.info.Agent}
				if event.Kind == core.KindToolResult {
					scanned.Payload = reducedToolResult(event.Payload)
				}
				out = append(out, scanned)
			}
		}
		s.mu.Unlock()
		sort.Slice(out, func(i, j int) bool {
			if out[i].CreatedAt != out[j].CreatedAt {
				return out[i].CreatedAt < out[j].CreatedAt
			}
			if out[i].SessionID != out[j].SessionID {
				return out[i].SessionID < out[j].SessionID
			}
			return out[i].Seq < out[j].Seq
		})
		for _, event := range out {
			if err := ctx.Err(); err != nil {
				yield(core.ScannedEvent{}, err)
				return
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}

// reducedToolResult keeps call_id, status, and the first 300 characters of the
// output of a tool/result payload, matching the SQLite json_object reduction.
func reducedToolResult(raw json.RawMessage) json.RawMessage {
	var payload core.ToolResultPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return raw
	}
	reduced, err := json.Marshal(struct {
		CallID string          `json:"call_id"`
		Status core.ToolStatus `json:"status"`
		Output string          `json:"output"`
	}{
		CallID: payload.CallID,
		Status: payload.Status,
		Output: cutRunes(payload.Output, 300),
	})
	if err != nil {
		return raw
	}
	return reduced
}

// cutRunes keeps at most n leading characters of s.
func cutRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// ChainEvents returns the parent chain truncated at inherited_seq, followed by the session's events.
func (s *Store) ChainEvents(ctx context.Context, sessionID string) ([]core.SessionEvent, error) {
	info, err := s.Info(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var chain []core.SessionEvent
	if info.ParentSessionID != "" {
		parent, err := s.ChainEvents(ctx, info.ParentSessionID)
		if err != nil {
			return nil, err
		}
		for _, event := range parent {
			if event.Seq <= info.InheritedSeq {
				chain = append(chain, event)
			}
		}
	}
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: session %s", core.ErrNotFound, sessionID)
	}
	chain = append(chain, sess.events...)
	s.mu.Unlock()
	return chain, nil
}

// DeriveMessages applies core.DeriveMessages to ChainEvents.
func (s *Store) DeriveMessages(ctx context.Context, sessionID string) ([]core.Message, error) {
	events, err := s.ChainEvents(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return core.DeriveMessages(events)
}

// List returns the most recent sessions first.
func (s *Store) List(ctx context.Context, filter core.SessionFilter) ([]core.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	out := make([]core.SessionInfo, 0, len(s.sessions))
	for _, sess := range s.sessions {
		info := sess.info
		if filter.Channel != "" && info.Channel != filter.Channel {
			continue
		}
		if filter.Address != "" && info.Address != filter.Address {
			continue
		}
		if filter.ParentSessionID != "" && info.ParentSessionID != filter.ParentSessionID {
			continue
		}
		if filter.Agent != "" && info.Agent != filter.Agent {
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
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// FindPrefix finds sessions by case-insensitive prefix.
func (s *Store) FindPrefix(ctx context.Context, prefix string) ([]core.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	needle := strings.ToLower(prefix)
	s.mu.Lock()
	var out []core.SessionInfo
	for key, sess := range s.sessions {
		if strings.HasPrefix(strings.ToLower(key), needle) {
			out = append(out, sess.info)
		}
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Fork is not supported by memstore.
func (s *Store) Fork(ctx context.Context, parentID string, atSeq int64) (core.SessionInfo, error) {
	return core.SessionInfo{}, ErrNotSupported
}

// TryLease always succeeds within the same process.
func (s *Store) TryLease(ctx context.Context, sessionID string) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionID]; !ok {
		return nil, fmt.Errorf("%w: session %s", core.ErrNotFound, sessionID)
	}
	return func() error { return nil }, nil
}

// Close does nothing; it is provided for symmetry.
func (s *Store) Close() error { return nil }

var _ core.SessionStore = (*Store)(nil)
var _ core.EventScanner = (*Store)(nil)
