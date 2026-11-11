package secretreq

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

func newTestSession(t *testing.T, channel string) (*memstore.Store, core.SessionInfo) {
	t.Helper()
	st := memstore.New()
	info, err := st.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: channel, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return st, info
}

func secretEvents(t *testing.T, st *memstore.Store, sessionID string) []core.SessionEvent {
	t.Helper()
	var out []core.SessionEvent
	for event, err := range st.Events(context.Background(), sessionID, 0) {
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == core.KindSecretRequested || event.Kind == core.KindSecretResolved {
			out = append(out, event)
		}
	}
	return out
}

func request(info core.SessionInfo, name string) core.SecretRequest {
	return core.SecretRequest{SessionID: info.ID, TurnID: "turn-1", CallID: "call-1", AgentName: "assistant", Name: name, Description: "Needed to read your repos."}
}

// REQ-SEC-003, REQ-SEC-005: a web request writes secret/requested with the
// replace flag, waits for the responder, and writes secret/resolved with the
// outcome only.
func TestRequestSecretSavedWritesBothEvents(t *testing.T) {
	st, info := newTestSession(t, "web")
	svc := New(st, func(name string) bool { return name == "GITHUB_TOKEN" })
	var seen core.SecretRequest
	svc.SetResponder(func(_ context.Context, req core.SecretRequest) core.SecretOutcome {
		seen = req
		return core.SecretOutcome{Status: core.SecretStatusSaved}
	}, "web", "workflow")

	outcome, err := svc.RequestSecret(context.Background(), request(info, "GITHUB_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != core.SecretStatusSaved {
		t.Fatalf("outcome = %+v", outcome)
	}
	if !seen.Replaces || seen.RequestID == "" || seen.ExpiresAt <= time.Now().UnixMilli() {
		t.Fatalf("responder request = %+v", seen)
	}
	events := secretEvents(t, st, info.ID)
	if len(events) != 2 || events[0].Kind != core.KindSecretRequested || events[1].Kind != core.KindSecretResolved {
		t.Fatalf("events = %+v", events)
	}
	var requested core.SecretRequested
	if err := json.Unmarshal(events[0].Payload, &requested); err != nil {
		t.Fatal(err)
	}
	if requested.RequestID != seen.RequestID || requested.CallID != "call-1" || requested.Name != "GITHUB_TOKEN" || !requested.Replaces || requested.Description == "" {
		t.Fatalf("requested = %+v", requested)
	}
	var resolved core.SecretResolved
	if err := json.Unmarshal(events[1].Payload, &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Outcome != core.SecretStatusSaved || resolved.Name != "GITHUB_TOKEN" || resolved.RequestID != seen.RequestID {
		t.Fatalf("resolved = %+v", resolved)
	}
	if events[0].TurnID != "turn-1" || events[1].TurnID != "turn-1" {
		t.Fatalf("turn ids = %q %q", events[0].TurnID, events[1].TurnID)
	}

	// A new name is not a replace.
	if _, err := svc.RequestSecret(context.Background(), request(info, "NEW_KEY")); err != nil {
		t.Fatal(err)
	}
	if seen.Replaces {
		t.Fatal("a new name was marked as a replace")
	}
}

// REQ-SEC-009: Telegram and CLI sessions get an error and no event; a
// Telegram error sends the owner to the web.
func TestRequestSecretChannelRouting(t *testing.T) {
	for _, tc := range []struct {
		channel string
		want    error
	}{
		{"telegram", core.ErrSecretTelegram},
		{"cli", core.ErrSecretNoWeb},
		{"library", core.ErrSecretNoWeb},
	} {
		st, info := newTestSession(t, tc.channel)
		svc := New(st, nil)
		called := false
		svc.SetResponder(func(context.Context, core.SecretRequest) core.SecretOutcome {
			called = true
			return core.SecretOutcome{Status: core.SecretStatusSaved}
		}, "web", "workflow")
		_, err := svc.RequestSecret(context.Background(), request(info, "TOKEN"))
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.channel, err, tc.want)
		}
		if called || len(secretEvents(t, st, info.ID)) != 0 {
			t.Fatalf("%s: a card opened", tc.channel)
		}
	}
	if !strings.Contains(core.ErrSecretTelegram.Error(), "web") {
		t.Fatal("the Telegram error does not send the owner to the web")
	}

	// A workflow session uses the web responder; no responder at all is the
	// web-chat error.
	st, info := newTestSession(t, "workflow")
	svc := New(st, nil)
	if _, err := svc.RequestSecret(context.Background(), request(info, "TOKEN")); !errors.Is(err, core.ErrSecretNoWeb) {
		t.Fatalf("no responder err = %v", err)
	}
	svc.SetResponder(func(context.Context, core.SecretRequest) core.SecretOutcome {
		return core.SecretOutcome{Status: core.SecretStatusCancelled, Reason: "owner"}
	}, "web", "workflow")
	outcome, err := svc.RequestSecret(context.Background(), request(info, "TOKEN"))
	if err != nil || outcome.Status != core.SecretStatusCancelled {
		t.Fatalf("workflow outcome = %+v, %v", outcome, err)
	}
}

// REQ-SEC-002: invalid input is refused before any event.
func TestRequestSecretValidation(t *testing.T) {
	st, info := newTestSession(t, "web")
	svc := New(st, nil)
	svc.SetResponder(func(context.Context, core.SecretRequest) core.SecretOutcome {
		return core.SecretOutcome{Status: core.SecretStatusSaved}
	}, "web")
	for _, name := range []string{"", "has-dash", "1LEADING", "SPACE NAME", strings.Repeat("A", 129)} {
		if _, err := svc.RequestSecret(context.Background(), request(info, name)); err == nil {
			t.Fatalf("name %q was accepted", name)
		}
	}
	if err := Validate("TOKEN", "x", "ftp://example.com"); err == nil {
		t.Fatal("ftp link accepted")
	}
	if err := Validate("TOKEN", strings.Repeat("x", MaxDescriptionRunes+1), ""); err == nil {
		t.Fatal("long description accepted")
	}
	if err := Validate("_OK_1", "x", "https://example.com/tokens"); err != nil {
		t.Fatalf("valid input refused: %v", err)
	}
	if len(secretEvents(t, st, info.ID)) != 0 {
		t.Fatal("an invalid request wrote an event")
	}
}

// REQ-SEC-004: a request times out as expired and a cancelled turn closes it
// as cancelled; both write secret/resolved.
func TestRequestSecretTimeoutAndCancel(t *testing.T) {
	st, info := newTestSession(t, "web")
	svc := New(st, nil)
	svc.Timeout = 50 * time.Millisecond
	svc.SetResponder(func(ctx context.Context, _ core.SecretRequest) core.SecretOutcome {
		<-ctx.Done()
		return core.SecretOutcome{Status: core.SecretStatusExpired, Reason: "timeout"}
	}, "web")
	outcome, err := svc.RequestSecret(context.Background(), request(info, "TOKEN"))
	if err != nil || outcome.Status != core.SecretStatusExpired {
		t.Fatalf("timeout outcome = %+v, %v", outcome, err)
	}

	svc.Timeout = time.Minute
	svc.SetResponder(func(ctx context.Context, _ core.SecretRequest) core.SecretOutcome {
		<-ctx.Done()
		return core.SecretOutcome{Status: core.SecretStatusCancelled, Reason: "turn_cancelled"}
	}, "web")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	outcome, err = svc.RequestSecret(ctx, request(info, "TOKEN"))
	if err != nil || outcome.Status != core.SecretStatusCancelled {
		t.Fatalf("cancel outcome = %+v, %v", outcome, err)
	}
	events := secretEvents(t, st, info.ID)
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
	var last core.SecretResolved
	_ = json.Unmarshal(events[3].Payload, &last)
	if last.Outcome != core.SecretStatusCancelled {
		t.Fatalf("last resolved = %+v", last)
	}
}

// One open request per session: a second one is busy.
func TestRequestSecretBusy(t *testing.T) {
	st, info := newTestSession(t, "web")
	svc := New(st, nil)
	opened := make(chan struct{})
	release := make(chan struct{})
	svc.SetResponder(func(context.Context, core.SecretRequest) core.SecretOutcome {
		close(opened)
		<-release
		return core.SecretOutcome{Status: core.SecretStatusSaved}
	}, "web")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = svc.RequestSecret(context.Background(), request(info, "TOKEN"))
	}()
	<-opened
	if _, err := svc.RequestSecret(context.Background(), request(info, "OTHER")); !errors.Is(err, core.ErrSecretBusy) {
		t.Fatalf("second request err = %v, want busy", err)
	}
	close(release)
	<-done
}
