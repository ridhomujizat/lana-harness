package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

// titleWebBackend is the fake backend with the SessionTitleBackend extension
// (spec 40). An empty title queues an automatic one, which the test finishes
// with finishAuto.
type titleWebBackend struct {
	*fakeWebBackend
	tmu         sync.Mutex
	pending     map[string]bool
	regenerated []string
	nextAuto    string
	regenErr    error
}

func newTitleWebBackend() *titleWebBackend {
	return &titleWebBackend{fakeWebBackend: newFakeWebBackend(), pending: map[string]bool{}, nextAuto: "Generated title"}
}

func (b *titleWebBackend) SetSessionTitle(_ context.Context, sessionID, title string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	source := core.TitleSourceOwner
	if title == "" {
		source = ""
	}
	if !b.update(sessionID, func(info *core.SessionInfo) { info.Title, info.TitleSource = title, source }) {
		return ErrSessionNotFound
	}
	if title == "" {
		b.tmu.Lock()
		b.pending[sessionID] = true
		b.tmu.Unlock()
	}
	return nil
}

func (b *titleWebBackend) RegenerateSessionTitle(_ context.Context, sessionID string) error {
	b.tmu.Lock()
	if b.pending[sessionID] {
		b.tmu.Unlock()
		return ErrTitleBusy
	}
	if b.regenErr != nil {
		err := b.regenErr
		b.tmu.Unlock()
		return err
	}
	b.regenerated = append(b.regenerated, sessionID)
	title := b.nextAuto
	b.tmu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.update(sessionID, func(info *core.SessionInfo) { info.Title, info.TitleSource = title, core.TitleSourceAuto })
	return nil
}

func (b *titleWebBackend) TitlePending(sessionID string) bool {
	b.tmu.Lock()
	defer b.tmu.Unlock()
	return b.pending[sessionID]
}

func (b *titleWebBackend) SessionTitle(_ context.Context, sessionID string) (string, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	info, ok := b.find(sessionID)
	if !ok {
		return "", "", ErrSessionNotFound
	}
	return info.Title, info.TitleSource, nil
}

// finishAuto stores an automatic title and clears the pending flag, as the
// title pass does.
func (b *titleWebBackend) finishAuto(sessionID, title string) {
	b.mu.Lock()
	b.update(sessionID, func(info *core.SessionInfo) { info.Title, info.TitleSource = title, core.TitleSourceAuto })
	b.mu.Unlock()
	b.tmu.Lock()
	delete(b.pending, sessionID)
	b.tmu.Unlock()
}

func decodeDetail(t *testing.T, body []byte) sessionDetailJSON {
	t.Helper()
	var detail sessionDetailJSON
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return detail
}

// REQ-TTL-008, REQ-TTL-009: PATCH title renames (trimmed, at most 80
// characters), and "" goes back to an automatic title.
func TestPatchSessionTitle(t *testing.T) {
	backend := newTitleWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "coder", Title: "Auto", TitleSource: core.TitleSourceAuto})
	s, cookie, csrf := newLoggedInSessionServer(t, backend)
	path := "/v1/sessions/" + info.ID

	rr := serveAuth(s, sessionTestRequest(http.MethodPatch, path, `{"title":"  Trip \n plan  "}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %q", rr.Code, rr.Body.String())
	}
	detail := decodeDetail(t, rr.Body.Bytes())
	if detail.Title != "Trip plan" || detail.TitleSource != core.TitleSourceOwner || detail.TitlePending {
		t.Fatalf("rename detail = %+v", detail.sessionJSON)
	}

	// 80 characters is the limit.
	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, path, `{"title":"`+strings.Repeat("é", 80)+`"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("80 characters status = %d", rr.Code)
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, path, `{"title":"`+strings.Repeat("x", 81)+`"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, path, `{"title":42}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")

	// "" clears the owner title and queues an automatic one.
	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, path, `{"title":"  "}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %q", rr.Code, rr.Body.String())
	}
	detail = decodeDetail(t, rr.Body.Bytes())
	if detail.Title != "" || detail.TitleSource != "" || !detail.TitlePending {
		t.Fatalf("clear detail = %+v", detail.sessionJSON)
	}

	// The list shows the source and the pending flag.
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions", "", cookie, csrf))
	var list listSessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 || !list.Sessions[0].TitlePending {
		t.Fatalf("list = %+v", list.Sessions)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, "/v1/sessions/"+id.New(), `{"title":"x"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

// A backend without title support refuses a title PATCH.
func TestPatchSessionTitleUnsupported(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "coder"})
	s, cookie, csrf := newLoggedInSessionServer(t, backend)
	rr := serveAuth(s, sessionTestRequest(http.MethodPatch, "/v1/sessions/"+info.ID, `{"title":"x"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusInternalServerError, "internal_error")
}

// REQ-TTL-010: regenerate answers the new title, 409 title_busy while a pass
// runs, and 409 nothing_to_title without an owner message.
func TestRegenerateSessionTitle(t *testing.T) {
	backend := newTitleWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "coder", Title: "Mine", TitleSource: core.TitleSourceOwner})
	s, cookie, csrf := newLoggedInSessionServer(t, backend)
	path := "/v1/sessions/" + info.ID + "/title/regenerate"

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, path, "", cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("regenerate status = %d, body = %q", rr.Code, rr.Body.String())
	}
	detail := decodeDetail(t, rr.Body.Bytes())
	if detail.Title != "Generated title" || detail.TitleSource != core.TitleSourceAuto {
		t.Fatalf("regenerate detail = %+v", detail.sessionJSON)
	}

	backend.tmu.Lock()
	backend.pending[info.ID] = true
	backend.tmu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, path, "", cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "title_busy")

	backend.tmu.Lock()
	delete(backend.pending, info.ID)
	backend.regenErr = ErrNothingToTitle
	backend.tmu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, path, "", cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "nothing_to_title")

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+id.New()+"/title/regenerate", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

// REQ-TTL-011: the session stream sends a "title" event when the automatic
// title arrives.
func TestStreamTitleEvent(t *testing.T) {
	backend := newTitleWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	s, ts, cookie, _ := newStreamTestServer(t, backend)
	s.SetHeartbeatInterval(20 * time.Millisecond)
	s.SetStreamPollInterval(10 * time.Millisecond)

	resp := openStream(t, ts, "/v1/sessions/"+info.ID+"/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	waitSSE(t, reader, "status")

	backend.tmu.Lock()
	backend.pending[info.ID] = true
	backend.tmu.Unlock()
	block := waitSSE(t, reader, "title")
	var got titleJSON
	if err := json.Unmarshal([]byte(block.Data), &got); err != nil {
		t.Fatal(err)
	}
	if !got.TitlePending || got.Title != "" {
		t.Fatalf("pending title event = %+v", got)
	}

	backend.finishAuto(info.ID, "Fix the build")
	block = waitSSE(t, reader, "title")
	if err := json.Unmarshal([]byte(block.Data), &got); err != nil {
		t.Fatal(err)
	}
	if got != (titleJSON{Title: "Fix the build", TitleSource: core.TitleSourceAuto}) {
		t.Fatalf("title event = %+v", got)
	}
}
