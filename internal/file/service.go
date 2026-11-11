// Package file implements the service behind the send_file tool: it records
// the file/sent event of one sent file and delivers it to the session's
// channel (REQ-AGF-003, REQ-AGF-004).
package file

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ridhomujizat/lana-harness/core"
)

// Service implements core.FileSender. It writes the file/sent event through
// the session store and asks the channel hook to deliver the file.
type Service struct {
	Store core.SessionStore
	Hooks *core.Hooks
}

// New creates the file service.
func New(store core.SessionStore, hooks *core.Hooks) *Service {
	return &Service{Store: store, Hooks: hooks}
}

// SendFile writes one file/sent event in the turn of tc and delivers the file
// to the session's channel. A delivery failure is reported as a note, not an
// error, so the event stays (REQ-AGF-004).
func (s *Service) SendFile(ctx context.Context, tc core.ToolCtx, f core.SentFile) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	payload := core.FileSentPayload{
		V:         core.PayloadVersion,
		CallID:    tc.CallID,
		Path:      f.Path,
		Name:      f.Name,
		MediaType: f.MediaType,
		Size:      f.Size,
		Caption:   f.Caption,
	}
	if _, err := s.Store.Append(ctx, tc.SessionID, []core.EventDraft{{Kind: core.KindFileSent, TurnID: tc.TurnID, Payload: payload}}); err != nil {
		return "", err
	}
	slog.Info("file sent", "session", tc.SessionID, "name", f.Name, "size", f.Size, "media_type", f.MediaType)
	return s.deliver(ctx, tc, f), nil
}

// deliver calls the channel hook. A missing hook or listener means there is
// nothing to deliver, so the note is empty; a hook error is logged and left
// unreported because the event is already written.
func (s *Service) deliver(ctx context.Context, tc core.ToolCtx, f core.SentFile) string {
	if s.Hooks == nil {
		return ""
	}
	outcome, err := s.Hooks.FileSend.Call(ctx, core.FileSendRequest{SessionID: tc.SessionID, AgentName: tc.AgentName, File: f})
	if err != nil {
		if !errors.Is(err, core.ErrNoDefault) {
			slog.Warn("file delivery failed", "session", tc.SessionID, "name", f.Name, "error", err)
		}
		return ""
	}
	return outcome.Note
}

var _ core.FileSender = (*Service)(nil)
