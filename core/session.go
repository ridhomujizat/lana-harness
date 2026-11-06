package core

import (
	"context"
	"encoding/json"
	"iter"
)

// SessionInfo is a projection of a session.
type SessionInfo struct {
	ID              string
	Agent           string
	Channel         string
	Address         string
	Workspace       string
	Title           string
	Status          string
	ParentSessionID string
	InheritedSeq    int64
	LastSeq         int64
	TurnCount       int64
	CreatedAt       int64
	UpdatedAt       int64
}

// NewSession is the input to Create.
type NewSession struct {
	Agent           string
	Channel         string
	Address         string
	Workspace       string
	ParentSessionID string
	ParentCallID    string
	InheritedSeq    int64
}

// SessionFilter limits List.
type SessionFilter struct {
	Channel         string
	Address         string
	ParentSessionID string
	// Agent matches sessions.agent exactly; empty means no filter (REQ-ROOM-002).
	Agent string
	// TopLevelOnly excludes child sessions.
	TopLevelOnly bool
	// Before, when > 0, keeps only sessions last updated before this Unix ms
	// time (REQ-ROOM-002).
	Before int64
	Limit  int
}

// SessionStore is the append-only event log.
type SessionStore interface {
	Create(ctx context.Context, s NewSession) (SessionInfo, error)
	Open(ctx context.Context, id string) (SessionInfo, error)
	Info(ctx context.Context, id string) (SessionInfo, error)
	Append(ctx context.Context, id string, drafts []EventDraft) ([]SessionEvent, error)
	Events(ctx context.Context, id string, fromSeq int64) iter.Seq2[SessionEvent, error]
	ChainEvents(ctx context.Context, id string) ([]SessionEvent, error)
	DeriveMessages(ctx context.Context, id string) ([]Message, error)
	List(ctx context.Context, f SessionFilter) ([]SessionInfo, error)
	FindPrefix(ctx context.Context, prefix string) ([]SessionInfo, error)
	Fork(ctx context.Context, parentID string, atSeq int64) (SessionInfo, error)
	TryLease(ctx context.Context, id string) (release func() error, err error)
}

// LeaseProbe is an optional SessionStore extension. LeaseHolder reports the
// process holding the session lease without taking the lease.
type LeaseProbe interface {
	LeaseHolder(ctx context.Context, id string) (pid int, held bool, err error)
}

// EventScanner is an optional SessionStore extension that reads events of the
// given kinds, across all sessions, created at or after sinceMS, in created_at
// order. It backs the monitoring dashboard scan (design.md §2, REQ-DSH-001).
type EventScanner interface {
	ScanEvents(ctx context.Context, kinds []string, sinceMS int64) iter.Seq2[ScannedEvent, error]
}

// AgentEventCounter is an optional SessionStore extension that counts the
// events of one agent's sessions (REQ-ROOM-002, REQ-ROOM-005). It backs the
// unread count and the finished-turn count of a room. The SQLite store
// implements it; the in-memory store does not.
type AgentEventCounter interface {
	// CountAgentEvents counts the events of the given kinds in the sessions of
	// agent created after sinceMS. When topLevelOnly, only the agent's
	// top-level sessions are counted. An empty kinds slice returns 0 without
	// querying.
	CountAgentEvents(ctx context.Context, agent string, kinds []string, sinceMS int64, topLevelOnly bool) (int64, error)
}

// RoomReadStore is an optional SessionStore extension that stores the last read
// time of every room (REQ-ROOM-005). The SQLite store implements it; the
// in-memory store does not.
type RoomReadStore interface {
	// RoomReadAt returns the last read time of room, or 0 when it was never
	// read.
	RoomReadAt(ctx context.Context, room string) (int64, error)
	// SetRoomRead stores at as the read time of room and returns the stored
	// value. The read time only moves forward.
	SetRoomRead(ctx context.Context, room string, at int64) (int64, error)
}

// ScannedEvent is one session_events row with the owning session's agent.
type ScannedEvent struct {
	SessionEvent
	Agent string // sessions.agent
}

// LiveFrame is a transient (streamed) frame that is not written to the log.
type LiveFrame struct {
	SessionID string
	Agent     string
	TurnID    string
	Kind      string
	Text      string
	CallID    string
	Name      string
	Input     json.RawMessage
	Status    string
	Output    string
}

// AgentRegistry manages live agents and their inboxes.
type AgentRegistry interface {
	Send(ctx context.Context, sessionID string, text string) error
	Cancel(ctx context.Context, sessionID string) error
	SetAgent(ctx context.Context, sessionID string, agent string) error
	SetModel(ctx context.Context, sessionID string, model string) error
	SetReasoningEffort(ctx context.Context, sessionID string, effort string) error
	SubscribeLive(sessionID string) (*Subscription[LiveFrame], error)
}
