package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

// maxSessionTitle is the longest title the owner may give a session
// (spec 40, REQ-TTL-008).
const maxSessionTitle = 80

var (
	// ErrTitleBusy is returned by a SessionTitleBackend while a title pass
	// runs for the session; the handler answers 409 title_busy (REQ-TTL-010).
	ErrTitleBusy = errors.New("a title is already being generated")
	// ErrNothingToTitle is returned by a SessionTitleBackend when the session
	// has no owner message to name it from; the handler answers 409
	// nothing_to_title.
	ErrNothingToTitle = errors.New("the session has no message to name it from yet")
	// ErrInvalidTitle is returned by a SessionTitleBackend for a title it
	// refuses; the handler answers 400 invalid_request.
	ErrInvalidTitle = errors.New("title must be at most 80 characters")
)

// SessionTitleBackend is an optional Backend extension for session titles
// (spec 40). Without it, a title PATCH and the regenerate endpoint answer 500.
type SessionTitleBackend interface {
	// SetSessionTitle stores an owner title; "" clears it and queues an
	// automatic one (REQ-TTL-008, REQ-TTL-009).
	SetSessionTitle(ctx context.Context, id, title string) error
	// RegenerateSessionTitle names the session again now and blocks until
	// the new title is stored (REQ-TTL-010).
	RegenerateSessionTitle(ctx context.Context, id string) error
	// TitlePending reports whether a title pass runs for the session
	// (REQ-TTL-012).
	TitlePending(id string) bool
	// SessionTitle reads the stored title and its source by full session id.
	// The session stream polls it for live updates (REQ-TTL-011).
	SessionTitle(ctx context.Context, id string) (title, source string, err error)
}

// titleJSON is the data of the session stream's "title" event (REQ-TTL-011).
type titleJSON struct {
	Title        string `json:"title"`
	TitleSource  string `json:"title_source"`
	TitlePending bool   `json:"title_pending"`
}

func (s *Server) titleBackend() SessionTitleBackend {
	port, _ := s.backend.(SessionTitleBackend)
	return port
}

// titlePending reports whether a title pass runs for the session.
func (s *Server) titlePending(id string) bool {
	port := s.titleBackend()
	return port != nil && port.TitlePending(id)
}

// normalizeSessionTitle collapses whitespace runs, newlines included, into one
// space and trims the ends. ok is false for a title over 80 characters
// (REQ-TTL-008).
func normalizeSessionTitle(raw string) (string, bool) {
	title := strings.Join(strings.Fields(raw), " ")
	return title, utf8.RuneCountInString(title) <= maxSessionTitle
}

// handleRegenerateTitle implements POST /v1/sessions/{id}/title/regenerate
// (REQ-TTL-010). It answers the session detail with the new title.
func (s *Server) handleRegenerateTitle(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	port := s.titleBackend()
	if port == nil {
		s.writeBackendError(w, "regenerate session title", errors.New("session titles are not available"))
		return
	}
	session, err := backend.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeBackendError(w, "get session", err)
		return
	}
	if err := port.RegenerateSessionTitle(r.Context(), session.ID); err != nil {
		s.writeBackendError(w, "regenerate session title", err)
		return
	}
	s.writeSessionDetail(w, r, backend, session.ID, http.StatusOK)
}

// writeSessionDetail reads the session again and writes its detail view.
func (s *Server) writeSessionDetail(w http.ResponseWriter, r *http.Request, backend Backend, id string, status int) {
	session, err := backend.Session(r.Context(), id)
	if err != nil {
		s.writeBackendError(w, "get session", err)
		return
	}
	holder, ok := s.sessionHolder(w, r, backend, session.ID)
	if !ok {
		return
	}
	detail := sessionDetailFromSession(session, holder)
	detail.TitlePending = s.titlePending(session.ID)
	writeJSON(w, status, detail)
}
