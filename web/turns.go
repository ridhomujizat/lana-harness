package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/internal/attach"
)

const (
	// maxMessageChars is CQ-002: the maximum length of a chat message.
	maxMessageChars = 32000
	maxMessageBody  = 1 << 20
)

// registerTurnRoutes registers API-WEB-007, API-WEB-009, API-TFB-003, and the
// attachment routes of API-ATT-001 and API-ATT-003.
func (s *Server) registerTurnRoutes() {
	s.Handle("POST /v1/sessions/{id}/messages", http.HandlerFunc(s.handleSendMessage))
	s.Handle("POST /v1/sessions/{id}/cancel", http.HandlerFunc(s.handleCancelTurn))
	s.Handle("POST /v1/sessions/{id}/continue", http.HandlerFunc(s.handleContinueTurn))
	s.registerAttachmentRoutes()
}

type sendMessageResponse struct {
	Turn   string `json:"turn"`
	TurnID string `json:"turn_id,omitempty"`
}

type cancelTurnResponse struct {
	Turn string `json:"turn"`
}

// messageBody is the JSON body of a chat message (API-WEB-007, API-ROOM-003).
type messageBody struct {
	Text        *string  `json:"text"`
	Attachments []string `json:"attachments"`
}

// noteSender is an optional Backend extension. A room send uses it to start a
// turn that carries a hidden runtime note the agent reads before the owner
// message (API-ROOM-003, REQ-ROOM-008).
type noteSender interface {
	SendNote(ctx context.Context, id, text string, attachments []string, note string) (SendResult, error)
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	var body messageBody
	if !decodeJSONObject(w, r, maxMessageBody, &body) {
		return
	}
	result, ok := s.sendSessionMessage(w, r, backend, r.PathValue("id"), body, "")
	if !ok {
		return
	}
	writeJSON(w, http.StatusAccepted, sendMessageResponse(result))
}

// sendSessionMessage validates a message body and starts a turn on the session.
// A non-empty note is delivered as a hidden runtime note before the owner
// message (API-ROOM-003, REQ-ROOM-008). It writes the failure response and
// returns ok=false on error.
func (s *Server) sendSessionMessage(w http.ResponseWriter, r *http.Request, backend Backend, id string, body messageBody, note string) (SendResult, bool) {
	text := ""
	if body.Text != nil {
		text = *body.Text
	}
	attachments := body.Attachments
	if len(attachments) > attach.MaxFiles {
		writeAPIError(w, http.StatusBadRequest, "too_many_files", fmt.Sprintf("a message may carry at most %d files", attach.MaxFiles))
		return SendResult{}, false
	}
	// API-ATT-002: the text may be empty or absent when attachments are present.
	if len(attachments) == 0 {
		if body.Text == nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "text is required")
			return SendResult{}, false
		}
		if strings.TrimSpace(text) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "text must not be empty")
			return SendResult{}, false
		}
	}
	if utf8.RuneCountInString(text) > maxMessageChars {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "text must be at most 32000 characters")
		return SendResult{}, false
	}
	for _, rel := range attachments {
		// API-ATT-002: every path must be a file under the session workspace
		// attachments/ directory; the error names the path.
		if rel == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_attachment", "attachment path is empty")
			return SendResult{}, false
		}
		if _, err := backend.AttachmentPath(r.Context(), id, rel); err != nil {
			if errors.Is(err, ErrSessionNotFound) {
				s.writeBackendError(w, "send message", err)
				return SendResult{}, false
			}
			writeAPIError(w, http.StatusBadRequest, "invalid_attachment", fmt.Sprintf("attachment path %q is not valid", rel))
			return SendResult{}, false
		}
	}
	// API-WF-021: a leading "/" may be a workflow chat command. A handled
	// command starts no turn and has already written its 200 response, so the
	// caller must not write again.
	if s.runWorkflowCommand(w, r, text) {
		return SendResult{}, false
	}
	result, err := startMessageTurn(r.Context(), backend, id, text, attachments, note)
	if err != nil {
		s.writeBackendError(w, "send message", err)
		return SendResult{}, false
	}
	return result, true
}

// startMessageTurn starts a turn on the session. A non-empty note is delivered
// as a hidden runtime note when the backend supports notes; otherwise the note
// is dropped and the plain owner message starts (API-ROOM-003, REQ-ROOM-008).
func startMessageTurn(ctx context.Context, backend Backend, id, text string, attachments []string, note string) (SendResult, error) {
	if note != "" {
		if sender, ok := backend.(noteSender); ok {
			return sender.SendNote(ctx, id, text, attachments, note)
		}
	}
	return backend.Send(ctx, id, text, attachments)
}

func (s *Server) handleCancelTurn(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	running, err := backend.Cancel(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeBackendError(w, "cancel turn", err)
		return
	}
	if !running {
		writeJSON(w, http.StatusOK, cancelTurnResponse{Turn: "idle"})
		return
	}
	writeJSON(w, http.StatusAccepted, cancelTurnResponse{Turn: "cancelling"})
}

// handleContinueTurn resumes the session's last turn (API-TFB-003).
func (s *Server) handleContinueTurn(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	result, err := backend.Continue(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeBackendError(w, "continue turn", err)
		return
	}
	writeJSON(w, http.StatusAccepted, sendMessageResponse(result))
}
