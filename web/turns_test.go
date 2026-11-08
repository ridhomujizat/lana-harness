package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

func newTurnTestBackend() (*fakeWebBackend, core.SessionInfo) {
	backend := newFakeWebBackend()
	backend.agentNames["helper"] = true
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	return backend, info
}

func TestSendMessageStartsTurn(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", `{"text":"hello"}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body sendMessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if body.Turn != "started" || body.TurnID != "turn-fake" {
		t.Fatalf("response = %+v", body)
	}
	if !backend.TurnActive(info.ID) {
		t.Fatal("turn is not active after a started response")
	}
}

func TestSendMessageValidation(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	for _, body := range []string{
		``,
		`{}`,
		`{"text":""}`,
		`{"text":"   "}`,
		`{"text":"hello","extra":true}`,
		`{"text":"` + strings.Repeat("x", maxMessageChars+1) + `"}`,
	} {
		rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", body, cookie, csrf))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %d chars status = %d, body = %q", len(body), rr.Code, rr.Body.String())
		}
		var envelope errorEnvelope
		if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("body %q: %v", rr.Body.String(), err)
		}
		if envelope.Error.Code != "invalid_request" {
			t.Fatalf("body %d chars code = %q", len(body), envelope.Error.Code)
		}
	}

	exact := strings.Repeat("x", maxMessageChars)
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", `{"text":"`+exact+`"}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("maximum length status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestSendMessageQueuedDuringTurn(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)
	backend.setTurnActive(info.ID, true)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", `{"text":"second"}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body sendMessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if body.Turn != "queued" {
		t.Fatalf("turn = %q, want queued", body.Turn)
	}
	backend.mu.Lock()
	inbox := append([]string(nil), backend.inbox[info.ID]...)
	backend.mu.Unlock()
	if len(inbox) != 1 || inbox[0] != "second" {
		t.Fatalf("inbox = %v", inbox)
	}
}

func TestCancelTurn(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/cancel", "", cookie, csrf))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"turn":"idle"}` {
		t.Fatalf("idle cancel status = %d, body = %q", rr.Code, rr.Body.String())
	}

	backend.setTurnActive(info.ID, true)
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/cancel", "", cookie, csrf))
	if rr.Code != http.StatusAccepted || strings.TrimSpace(rr.Body.String()) != `{"turn":"cancelling"}` {
		t.Fatalf("running cancel status = %d, body = %q", rr.Code, rr.Body.String())
	}
	backend.mu.Lock()
	cancelled := append([]string(nil), backend.cancelled...)
	backend.mu.Unlock()
	if len(cancelled) != 1 || cancelled[0] != info.ID {
		t.Fatalf("cancelled = %v", cancelled)
	}
}

// AC-TFB-012: POST /v1/sessions/{id}/continue maps the contract refusals
// (API-TFB-003).
func TestContinueTurn(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/continue", "", cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body sendMessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if body.Turn != "started" || body.TurnID != "turn-fake" {
		t.Fatalf("response = %+v", body)
	}

	backend.setTurnActive(info.ID, true)
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/continue", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "turn_running")

	backend.setTurnActive(info.ID, false)
	backend.mu.Lock()
	backend.continueErr[info.ID] = ErrNothingToContinue
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/continue", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "nothing_to_continue")

	backend.mu.Lock()
	backend.continueErr[info.ID] = ErrDraining
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/continue", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusServiceUnavailable, "daemon_draining")

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/missing/continue", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}
func TestTurnEndpointsMapBackendErrors(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/missing/messages", `{"text":"hi"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/missing/cancel", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")

	backend.mu.Lock()
	backend.leaseHeld[info.ID] = 4242
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", `{"text":"hi"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "session_in_use")
	var envelope struct {
		Error struct {
			Details []map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(envelope.Error.Details) != 1 || envelope.Error.Details[0]["pid"] != float64(4242) {
		t.Fatalf("details = %+v", envelope.Error.Details)
	}
}

func TestTurnRoutesRequireAuth(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, token := newSessionTestServer(t, backend)

	for _, target := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/v1/sessions/" + info.ID + "/messages", `{"text":"hi"}`},
		{http.MethodPost, "/v1/sessions/" + info.ID + "/cancel", ""},
	} {
		rr := serveAuth(s, authRequest(target.method, target.path, target.body))
		assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
	}

	cookie, _ := loginAndSession(t, s, token)
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", `{"text":"hi"}`, cookie, ""))
	assertAuthError(t, rr, http.StatusForbidden, "csrf_invalid")
}

// API-WEB-007: reply_to is threaded to the backend.
func TestSendMessageReplyTo(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", `{"text":"explain","reply_to":7}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	backend.mu.Lock()
	replyTo := backend.sentReplyTo[info.ID]
	backend.mu.Unlock()
	if replyTo != 7 {
		t.Fatalf("reply_to = %d, want 7", replyTo)
	}
}

// A backend that refuses the reply_to answers 400 with the contract message.
func TestSendMessageReplyToRejected(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)
	backend.mu.Lock()
	backend.replyErr[info.ID] = ErrInvalidReplyTo
	backend.mu.Unlock()

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", `{"text":"x","reply_to":4}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	if !strings.Contains(rr.Body.String(), "reply_to must point at an automation message in this chat") {
		t.Fatalf("body = %q", rr.Body.String())
	}
}

// A negative reply_to is refused before it reaches the backend.
func TestSendMessageReplyToNegative(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", `{"text":"x","reply_to":-1}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}
