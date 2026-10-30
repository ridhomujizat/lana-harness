package agent

import (
	"errors"
	"fmt"
)

var (
	ErrClosed          = errors.New("runtime is closed")
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionBusy     = errors.New("session is busy")
	ErrEmptyText       = errors.New("text is empty")
	ErrUnknownAgent    = errors.New("unknown agent")
	ErrUnknownProvider = errors.New("unknown provider")
	ErrEmptyModel      = errors.New("model is empty")
	ErrInvalidModel    = errors.New("invalid model")
	// ErrTeamModel refuses a model change on a team session: each agent of the
	// team keeps its own model.
	ErrTeamModel = fmt.Errorf("%w: teams use each agent's model", ErrInvalidModel)
)
