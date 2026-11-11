package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/roommemory"
)

// Spec 47 room discussion routes: Stop (API-RDS-011) and room memory
// (API-RDS-012).

const roomMemoryBodyLimit = 16 << 10

// discussionBackend is an optional Backend extension the runtime implements.
type discussionBackend interface {
	StopDiscussion(ctx context.Context, sessionID, mode string) (bool, error)
	RoomMemory() *roommemory.Store
}

func (s *Server) registerDiscussionRoutes() {
	s.Handle("POST /v1/sessions/{id}/discussion/stop", http.HandlerFunc(s.handleStopDiscussion))
	s.Handle("GET /v1/rooms/{name}/memory", http.HandlerFunc(s.handleListRoomMemory))
	s.Handle("POST /v1/rooms/{name}/memory", http.HandlerFunc(s.handleAddRoomMemory))
	s.Handle("PUT /v1/rooms/{name}/memory/{id}", http.HandlerFunc(s.handleUpdateRoomMemory))
	s.Handle("DELETE /v1/rooms/{name}/memory/{id}", http.HandlerFunc(s.handleDeleteRoomMemory))
}

func (s *Server) discussionBackendOrError(w http.ResponseWriter) discussionBackend {
	backend := s.backendOrError(w)
	if backend == nil {
		return nil
	}
	d, ok := backend.(discussionBackend)
	if !ok {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "discussions are not available")
		return nil
	}
	return d
}

func (s *Server) handleStopDiscussion(w http.ResponseWriter, r *http.Request) {
	d := s.discussionBackendOrError(w)
	if d == nil {
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if !decodeJSONObject(w, r, 1<<10, &body) {
		return
	}
	if body.Mode == "" {
		body.Mode = core.DiscussionStop
	}
	if body.Mode != core.DiscussionStop && body.Mode != core.DiscussionDirect {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "mode must be stop or direct")
		return
	}
	stopped, err := d.StopDiscussion(r.Context(), r.PathValue("id"), body.Mode)
	if err != nil {
		s.logger.Error("web: stop discussion", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": stopped})
}

// teamRoom returns the store and the team name for a memory route, writing
// the error response when the room is not a team.
func (s *Server) teamRoom(w http.ResponseWriter, r *http.Request) (*roommemory.Store, string) {
	d := s.discussionBackendOrError(w)
	if d == nil {
		return nil, ""
	}
	backend := s.backendOrError(w)
	if backend == nil {
		return nil, ""
	}
	name := r.PathValue("name")
	room, ok := findRoom(backend, name)
	if !ok || room.Kind != "team" {
		writeAPIError(w, http.StatusNotFound, "not_found", "team room not found")
		return nil, ""
	}
	store := d.RoomMemory()
	if store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "room memory is not available")
		return nil, ""
	}
	return store, name
}

func (s *Server) writeRoomMemoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, roommemory.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "note not found")
	case errors.Is(err, roommemory.ErrInvalid):
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
	default:
		s.logger.Error("web: room memory", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func (s *Server) handleListRoomMemory(w http.ResponseWriter, r *http.Request) {
	store, team := s.teamRoom(w, r)
	if store == nil {
		return
	}
	notes, err := store.List(team)
	if err != nil {
		s.writeRoomMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
}

func (s *Server) handleAddRoomMemory(w http.ResponseWriter, r *http.Request) {
	store, team := s.teamRoom(w, r)
	if store == nil {
		return
	}
	var body struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	if !decodeJSONObject(w, r, roomMemoryBodyLimit, &body) {
		return
	}
	note, err := store.Add(team, body.Kind, body.Text, "", "owner", "")
	if err != nil {
		s.writeRoomMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, note)
}

func (s *Server) handleUpdateRoomMemory(w http.ResponseWriter, r *http.Request) {
	store, team := s.teamRoom(w, r)
	if store == nil {
		return
	}
	var body struct {
		Kind *string `json:"kind"`
		Text *string `json:"text"`
	}
	if !decodeJSONObject(w, r, roomMemoryBodyLimit, &body) {
		return
	}
	note, err := store.Update(team, r.PathValue("id"), body.Kind, body.Text)
	if err != nil {
		s.writeRoomMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, note)
}

func (s *Server) handleDeleteRoomMemory(w http.ResponseWriter, r *http.Request) {
	store, team := s.teamRoom(w, r)
	if store == nil {
		return
	}
	if err := store.Delete(team, r.PathValue("id")); err != nil {
		s.writeRoomMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
