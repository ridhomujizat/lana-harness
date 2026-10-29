package core

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound              = errors.New("not found")
	ErrClosed                = errors.New("subscription is closed")
	ErrNoDefault             = errors.New("default handler is not set")
	ErrContextTooLong        = errors.New("context is too long")
	ErrPrefixTooShort        = errors.New("session prefix must be at least 6 characters")
	ErrAmbiguousPrefix       = errors.New("session prefix matches more than one session")
	ErrUnknownPayloadVersion = errors.New("unknown payload version")
)

// LeaseHeldError is returned by TryLease when the session is held by another process.
type LeaseHeldError struct {
	SessionID string
	PID       int
	Host      string
}

func (e *LeaseHeldError) Error() string {
	return fmt.Sprintf("session %s is in use by process %d on %s", e.SessionID, e.PID, e.Host)
}

// AmbiguousPrefixError is returned by ResolvePrefix when the prefix matches more than one session.
type AmbiguousPrefixError struct {
	Candidates []string
}

func (e *AmbiguousPrefixError) Error() string {
	return fmt.Sprintf("session prefix matches %d sessions: %v", len(e.Candidates), e.Candidates)
}

// Is makes errors.Is(err, ErrAmbiguousPrefix) return true.
func (e *AmbiguousPrefixError) Is(target error) bool { return target == ErrAmbiguousPrefix }
