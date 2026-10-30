package web

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

const (
	// maxMessageChars is CQ-002: the maximum length of a chat message.
	maxMessageChars = 32000
	maxMessageBody  = 1 << 20
)

// registerTurnRoutes registers API-WEB-007, API-WEB-009, and API-TFB-003.
func (s *Server) registerTurnRoutes() {
	s.Handle("POST /v1/sessions/{id}/messages", http.HandlerFunc(s.handleSendMessage))
	s.Handle("POST /v1/sessions/{id}/cancel", http.HandlerFunc(s.handleCancelTurn))
	s.Handle("POST /v1/sessions/{id}/continue", http.HandlerFunc(s.handleContinueTurn))
}

type sendMessageResponse struct {
	Turn   string `json:"turn"`
	TurnID string `json:"turn_id,omitempty"`
}

type cancelTurnResponse struct {
	Turn string `json:"turn"`
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Text *string `json:"text"`
	}
	if !decodeJSONObject(w, r, maxMessageBody, &body) {
		return
	}
	if body.Text == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "text is required")
		return
	}
	text := *body.Text
	if strings.TrimSpace(text) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "text must not be empty")
		return
	}
	if utf8.RuneCountInString(text) > maxMessageChars {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "text must be at most 32000 characters")
		return
	}
	result, err := backend.Send(r.Context(), r.PathValue("id"), text)
	if err != nil {
		s.writeBackendError(w, "send message", err)
		return
	}
	writeJSON(w, http.StatusAccepted, sendMessageResponse(result))
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
