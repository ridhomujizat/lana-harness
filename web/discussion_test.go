package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/roommemory"
)

// fakeDiscussionBackend adds the spec 47 extension to the in-memory backend.
type fakeDiscussionBackend struct {
	*fakeWebBackend
	memory  *roommemory.Store
	stopped map[string]string
}

func (b *fakeDiscussionBackend) StopDiscussion(_ context.Context, id, mode string) (bool, error) {
	if id != "s1" {
		return false, nil
	}
	b.stopped[id] = mode
	return true, nil
}

func (b *fakeDiscussionBackend) RoomMemory() *roommemory.Store { return b.memory }

func newDiscussionTestServer(t *testing.T) (*Server, *fakeDiscussionBackend, *http.Cookie, string) {
	t.Helper()
	backend := &fakeDiscussionBackend{fakeWebBackend: newFakeWebBackend(), memory: roommemory.New(t.TempDir()), stopped: map[string]string{}}
	backend.agents = []Agent{{Name: "lana"}, {Name: "career", Kind: "team", Lead: "lana", Members: []string{"karin"}}}
	s, cookie, csrf := newLoggedInSessionServer(t, backend)
	return s, backend, cookie, csrf
}

// TestDiscussionStopRoute covers API-RDS-011.
func TestDiscussionStopRoute(t *testing.T) {
	s, backend, cookie, csrf := newDiscussionTestServer(t)
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/s1/discussion/stop", `{"mode":"direct"}`, cookie, csrf))
	if rr.Code != http.StatusOK || backend.stopped["s1"] != "direct" {
		t.Fatalf("status = %d body = %q stopped = %v", rr.Code, rr.Body.String(), backend.stopped)
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/s1/discussion/stop", `{"mode":"later"}`, cookie, csrf))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad mode status = %d", rr.Code)
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/s2/discussion/stop", `{}`, cookie, csrf))
	if rr.Code != http.StatusOK || rr.Body.String() == "" {
		t.Fatalf("no discussion status = %d", rr.Code)
	}
	var out map[string]bool
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["stopped"] {
		t.Fatal("stopped = true for a session without a discussion")
	}
}

// TestRoomMemoryRoutes covers API-RDS-012.
func TestRoomMemoryRoutes(t *testing.T) {
	s, _, cookie, csrf := newDiscussionTestServer(t)
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/career/memory", `{"kind":"fact","text":"Target 31 jt"}`, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("add status = %d body = %q", rr.Code, rr.Body.String())
	}
	var note roommemory.Note
	_ = json.Unmarshal(rr.Body.Bytes(), &note)
	if note.ID == "" || note.Source != "owner" || note.Agent != "" {
		t.Fatalf("note = %+v", note)
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodPut, "/v1/rooms/career/memory/"+note.ID, `{"text":"Target 32 jt"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("update status = %d body = %q", rr.Code, rr.Body.String())
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/rooms/career/memory", "", cookie, ""))
	var list struct {
		Notes []roommemory.Note `json:"notes"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &list)
	if rr.Code != http.StatusOK || len(list.Notes) != 1 || list.Notes[0].Text != "Target 32 jt" {
		t.Fatalf("list = %d %+v", rr.Code, list)
	}
	if rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/rooms/career/memory", `{"kind":"wish","text":"x"}`, cookie, csrf)); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad kind status = %d", rr.Code)
	}
	if rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/rooms/lana/memory", "", cookie, "")); rr.Code != http.StatusNotFound {
		t.Fatalf("agent room status = %d", rr.Code)
	}
	if rr = serveAuth(s, sessionTestRequest(http.MethodDelete, "/v1/rooms/career/memory/"+note.ID, "", cookie, csrf)); rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rr.Code)
	}
	if rr = serveAuth(s, sessionTestRequest(http.MethodDelete, "/v1/rooms/career/memory/"+note.ID, "", cookie, csrf)); rr.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d", rr.Code)
	}
}
