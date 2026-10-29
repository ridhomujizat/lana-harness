package agent

import "errors"

var (
	ErrClosed          = errors.New("runtime is closed")
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionBusy     = errors.New("session is busy")
	ErrEmptyText       = errors.New("text is empty")
	ErrUnknownAgent    = errors.New("unknown agent")
	ErrUnknownProvider = errors.New("unknown provider")
	ErrEmptyModel      = errors.New("model is empty")
)
