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
	// TopLevelOnly excludes child sessions.
	TopLevelOnly bool
	Limit        int
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
	SubscribeLive(sessionID string) (*Subscription[LiveFrame], error)
}
