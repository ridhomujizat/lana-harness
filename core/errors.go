package core

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound              = errors.New("tidak ditemukan")
	ErrClosed                = errors.New("subscription ditutup")
	ErrNoDefault             = errors.New("handler default belum diset")
	ErrContextTooLong        = errors.New("context terlalu panjang")
	ErrPrefixTooShort        = errors.New("prefix session minimal 6 karakter")
	ErrAmbiguousPrefix       = errors.New("prefix session cocok lebih dari satu")
	ErrUnknownPayloadVersion = errors.New("versi payload tidak dikenal")
)

// LeaseHeldError dikembalikan TryLease saat session dipakai proses lain.
type LeaseHeldError struct {
	SessionID string
	PID       int
	Host      string
}

func (e *LeaseHeldError) Error() string {
	return fmt.Sprintf("session %s sedang dipakai proses %d di %s", e.SessionID, e.PID, e.Host)
}

// AmbiguousPrefixError dikembalikan ResolvePrefix jika prefix cocok lebih dari satu session.
type AmbiguousPrefixError struct {
	Candidates []string
}

func (e *AmbiguousPrefixError) Error() string {
	return fmt.Sprintf("prefix session cocok dengan %d session: %v", len(e.Candidates), e.Candidates)
}

// Is membuat errors.Is(err, ErrAmbiguousPrefix) benar.
func (e *AmbiguousPrefixError) Is(target error) bool { return target == ErrAmbiguousPrefix }
