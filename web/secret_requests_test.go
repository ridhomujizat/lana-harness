package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

const secretRequestTestValue = "ghp_TopSecretValue_9f8e7d"

// syncBuffer is a log sink safe for the broker and handler goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func nextSecretRequestEvent(t *testing.T, ch <-chan secretRequestEvent) secretRequestEvent {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a secret request event")
		return secretRequestEvent{}
	}
}

func waitSecretOutcome(t *testing.T, ch <-chan core.SecretOutcome) core.SecretOutcome {
	t.Helper()
	select {
	case outcome := <-ch:
		return outcome
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a secret request outcome")
		return core.SecretOutcome{}
	}
}

// askSecretForTest opens a request on the broker in the background.
func askSecretForTest(t *testing.T, broker *SecretRequests, sessionID, name string, replaces bool) (secretRequestEvent, <-chan core.SecretOutcome) {
	t.Helper()
	_, events, unsubscribe := broker.subscribe(sessionID, nil)
	t.Cleanup(unsubscribe)
	outcomes := make(chan core.SecretOutcome, 1)
	go func() {
		outcome, _ := broker.Ask(context.Background(), core.SecretRequest{
			SessionID:   sessionID,
			CallID:      "call-1",
			Name:        name,
			Description: "Needed to read your repos.",
			Link:        "https://github.com/settings/tokens",
			Replaces:    replaces,
		})
		outcomes <- outcome
	}()
	return nextSecretRequestEvent(t, events), outcomes
}

func secretRequestPath(id string) string { return "/v1/secret-requests/" + id }

func (q *SecretRequests) isOpen(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.open[id]
	return ok
}

// Spec 53 AC-SEC-001, AC-SEC-005: a saved value goes to secrets.env through the
// Settings code (meta time, audit line), the response and the outcome name the
// outcome only, and neither the logs nor the audit file contain the value.
func TestSecretRequestSaveWritesStore(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home}
	s, cookie, csrf := newSettingsTestServer(t, store)
	logs := &syncBuffer{}
	s.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	event, outcomes := askSecretForTest(t, s.SecretRequests(), "session-1", "GITHUB_TOKEN", false)
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"request_id":"` + event.RequestID + `","call_id":"call-1","name":"GITHUB_TOKEN","description":"Needed to read your repos.","link":"https://github.com/settings/tokens","replaces":false,"expires_at":` + jsonNumber(event.ExpiresAt) + `}`
	if string(raw) != want {
		t.Fatalf("event = %s, want %s", raw, want)
	}

	rr := serveAuth(s, settingsRequest(http.MethodPost, secretRequestPath(event.RequestID), `{"value":"`+secretRequestTestValue+`"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if got := strings.TrimSpace(rr.Body.String()); got != `{"outcome":"saved","name":"GITHUB_TOKEN"}` {
		t.Fatalf("save body = %s", got)
	}
	outcome := waitSecretOutcome(t, outcomes)
	if outcome != (core.SecretOutcome{Status: core.SecretStatusSaved}) {
		t.Fatalf("outcome = %+v", outcome)
	}

	values, err := webfiles.ReadSecrets(filepath.Join(home, "secrets.env"))
	if err != nil {
		t.Fatal(err)
	}
	if values["GITHUB_TOKEN"] != secretRequestTestValue {
		t.Fatalf("secrets.env GITHUB_TOKEN = %q", values["GITHUB_TOKEN"])
	}
	list := decodeSecretList(t, s, cookie)
	row := secretByName(list, "GITHUB_TOKEN")
	if row.State != "set" || row.UpdatedAt == nil || !list.RestartRequired {
		t.Fatalf("secret row = %+v, restart = %v", row, list.RestartRequired)
	}
	audit, err := os.ReadFile(webfiles.AuditPath(home))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !strings.Contains(string(audit), "GITHUB_TOKEN") {
		t.Fatalf("audit lacks the name: %s", audit)
	}
	for label, text := range map[string]string{"audit": string(audit), "logs": logs.String(), "event": string(raw), "response": rr.Body.String()} {
		if strings.Contains(text, secretRequestTestValue) {
			t.Fatalf("%s contains the secret value: %s", label, text)
		}
	}

	// The first answer wins; a late one is 409.
	rr = serveAuth(s, settingsRequest(http.MethodPost, secretRequestPath(event.RequestID), `{"value":"other"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "secret_request_closed")
}

// AC-SEC-002: saving over an existing name replaces the value; the replace
// flag reaches the event.
func TestSecretRequestReplaceFlagAndOverwrite(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "secrets.env"), []byte("GITHUB_TOKEN=old-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, cookie, csrf := newSettingsTestServer(t, &testSettingsStore{home: home})
	event, outcomes := askSecretForTest(t, s.SecretRequests(), "session-1", "GITHUB_TOKEN", true)
	if !event.Replaces {
		t.Fatal("event does not carry replaces")
	}
	rr := serveAuth(s, settingsRequest(http.MethodPost, secretRequestPath(event.RequestID), `{"value":"new-value"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %q", rr.Code, rr.Body.String())
	}
	waitSecretOutcome(t, outcomes)
	values, _ := webfiles.ReadSecrets(filepath.Join(home, "secrets.env"))
	if values["GITHUB_TOKEN"] != "new-value" {
		t.Fatalf("GITHUB_TOKEN = %q, want new-value", values["GITHUB_TOKEN"])
	}
}

// AC-SEC-003: the answer route needs the CSRF header; a refused request
// stays open, and cancel closes it without writing anything.
func TestSecretRequestCSRFAndCancel(t *testing.T) {
	home := t.TempDir()
	s, cookie, csrf := newSettingsTestServer(t, &testSettingsStore{home: home})
	event, outcomes := askSecretForTest(t, s.SecretRequests(), "session-1", "API_KEY", false)

	rr := serveAuth(s, settingsRequest(http.MethodPost, secretRequestPath(event.RequestID), `{"value":"x"}`, cookie, ""))
	assertAuthError(t, rr, http.StatusForbidden, "csrf_invalid")
	rr = serveAuth(s, settingsRequest(http.MethodPost, secretRequestPath(event.RequestID), `{"value":"x"}`, nil, ""))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie status = %d", rr.Code)
	}
	if !s.SecretRequests().isOpen(event.RequestID) {
		t.Fatal("request closed after a refused answer")
	}

	rr = serveAuth(s, settingsRequest(http.MethodPost, secretRequestPath(event.RequestID), `{"cancel":true}`, cookie, csrf))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"outcome":"cancelled","name":"API_KEY"}` {
		t.Fatalf("cancel = %d %s", rr.Code, rr.Body.String())
	}
	if outcome := waitSecretOutcome(t, outcomes); outcome.Status != core.SecretStatusCancelled {
		t.Fatalf("outcome = %+v", outcome)
	}
	if _, err := os.Stat(filepath.Join(home, "secrets.env")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secrets.env exists after a cancel: %v", err)
	}
}

// AC-SEC-004: a bad body or value is refused and the request stays open; an
// unknown id is 404.
func TestSecretRequestValidation(t *testing.T) {
	s, cookie, csrf := newSettingsTestServer(t, &testSettingsStore{home: t.TempDir()})
	event, outcomes := askSecretForTest(t, s.SecretRequests(), "session-1", "API_KEY", false)
	path := secretRequestPath(event.RequestID)

	for _, tc := range []struct {
		body   string
		status int
		code   string
	}{
		{`{}`, http.StatusBadRequest, "invalid_request"},
		{`{"value":"x","cancel":true}`, http.StatusBadRequest, "invalid_request"},
		{`{"value":"x","name":"OTHER"}`, http.StatusBadRequest, "invalid_request"},
		{`{"value":`, http.StatusBadRequest, "invalid_request"},
		{`{"value":""}`, http.StatusUnprocessableEntity, "validation_failed"},
		{`{"value":"line\nbreak"}`, http.StatusUnprocessableEntity, "validation_failed"},
		{`{"value":"has \"quote\""}`, http.StatusUnprocessableEntity, "validation_failed"},
	} {
		rr := serveAuth(s, settingsRequest(http.MethodPost, path, tc.body, cookie, csrf))
		assertAuthError(t, rr, tc.status, tc.code)
		if !s.SecretRequests().isOpen(event.RequestID) {
			t.Fatalf("%s closed the request", tc.body)
		}
	}
	rr := serveAuth(s, settingsRequest(http.MethodPost, secretRequestPath("never-issued"), `{"value":"x"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")

	rr = serveAuth(s, settingsRequest(http.MethodPost, path, `{"value":"good-value"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("valid save = %d %s", rr.Code, rr.Body.String())
	}
	waitSecretOutcome(t, outcomes)
}

// AC-SEC-006: the fake clock drives the 30-minute expiry; a late answer is 409.
func TestSecretRequestTimeoutExpired(t *testing.T) {
	broker := newSecretRequests()
	clock := newFakeApprovalClock()
	broker.setClock(clock)
	event, outcomes := askSecretForTest(t, broker, "session-1", "API_KEY", false)
	if want := clock.Now().Add(secretRequestTimeout).UnixMilli(); event.ExpiresAt != want {
		t.Fatalf("expires_at = %d, want %d", event.ExpiresAt, want)
	}
	clock.Advance(secretRequestTimeout)
	if outcome := waitSecretOutcome(t, outcomes); outcome.Status != core.SecretStatusExpired {
		t.Fatalf("outcome = %+v", outcome)
	}
	value := "x"
	if _, _, err := broker.answer(event.RequestID, secretRequestAnswer{Value: &value}); !errors.Is(err, errSecretRequestClosed) {
		t.Fatalf("late answer err = %v", err)
	}
}

// AC-SEC-007: an open request reaches the session stream as a secret_request
// event, is replayed to a stream that connects later, and the room shows the
// owner is awaited.
func TestSecretRequestSSEEventAndReplay(t *testing.T) {
	backend := newFakeWebBackend()
	info := backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "helper"})
	s, ts, cookie, _ := newStreamTestServer(t, backend)

	resp := openStream(t, ts, "/v1/sessions/"+info.ID+"/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	waitSSE(t, reader, "status")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outcomes := make(chan core.SecretOutcome, 1)
	go func() {
		outcome, _ := s.SecretRequests().Ask(ctx, core.SecretRequest{SessionID: info.ID, Name: "API_KEY", Description: "why"})
		outcomes <- outcome
	}()
	block := waitSSE(t, reader, "secret_request")
	var live secretRequestEvent
	if err := json.Unmarshal([]byte(block.Data), &live); err != nil {
		t.Fatal(err)
	}
	if live.RequestID == "" || live.Name != "API_KEY" || live.Description != "why" {
		t.Fatalf("event = %+v", live)
	}

	second := openStream(t, ts, "/v1/sessions/"+info.ID+"/stream", cookie, "")
	defer func() { _ = second.Body.Close() }()
	secondReader := bufio.NewReader(second.Body)
	waitSSE(t, secondReader, "status")
	replayed := waitSSE(t, secondReader, "secret_request")
	if replayed.Data != block.Data {
		t.Fatalf("replayed = %s, want %s", replayed.Data, block.Data)
	}

	requests := s.SecretRequests().openRequests()
	if len(requests) != 1 || requests[0].Kind != "secret" || requests[0].Tool != "API_KEY" || requests[0].SessionID != info.ID {
		t.Fatalf("open requests = %+v", requests)
	}

	cancel()
	if outcome := waitSecretOutcome(t, outcomes); outcome.Status != core.SecretStatusCancelled {
		t.Fatalf("outcome = %+v", outcome)
	}
}
