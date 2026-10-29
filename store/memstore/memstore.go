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
	var out []core.SessionEvent
	now := nowMS()
	for _, draft := range drafts {
		payload, err := json.Marshal(draft.Payload)
		if err != nil {
			return nil, err
		}
		sess.seq++
		event := core.SessionEvent{
			SessionID: sess.info.ID,
			Seq:       sess.seq,
			Kind:      draft.Kind,
			Payload:   payload,
			TurnID:    draft.TurnID,
			CreatedAt: now,
		}
		sess.events = append(sess.events, event)
		out = append(out, event)
		switch draft.Kind {
		case core.KindTurnStart:
			sess.info.Status = "running"
			sess.info.TurnCount++
		case core.KindTurnEnd:
			sess.info.Status = "idle"
		}
	}
	sess.info.LastSeq = sess.seq
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
		out = append(out, info)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
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
