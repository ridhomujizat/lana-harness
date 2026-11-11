package file

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

// newFileTestSession creates an in-memory session for a file/sent test.
func newFileTestSession(t *testing.T) (*memstore.Store, core.SessionInfo) {
	t.Helper()
	st := memstore.New()
	info, err := st.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "web", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return st, info
}

// fileSentEvents returns the file/sent events of a session.
func fileSentEvents(t *testing.T, st *memstore.Store, sessionID string) []core.SessionEvent {
	t.Helper()
	var out []core.SessionEvent
	for event, err := range st.Events(context.Background(), sessionID, 0) {
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == core.KindFileSent {
			out = append(out, event)
		}
	}
	return out
}

// REQ-AGF-003: SendFile appends exactly one file/sent event in the turn and its
// payload carries the API-AGF-001 keys. The caption key is omitted when empty.
func TestSendFileAppendsEvent(t *testing.T) {
	st, info := newFileTestSession(t)
	svc := New(st, nil)
	tc := core.ToolCtx{SessionID: info.ID, TurnID: "turn-1", CallID: "toolu_1", AgentName: "assistant"}
	f := core.SentFile{Path: "attachments/2026-11-11/x.png", Name: "x.png", MediaType: "image/png", Size: 319488}

	note, err := svc.SendFile(context.Background(), tc, f)
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Fatalf("note=%q, want empty (no hook)", note)
	}

	events := fileSentEvents(t, st, info.ID)
	if len(events) != 1 {
		t.Fatalf("file/sent events=%d, want exactly one", len(events))
	}
	event := events[0]
	if event.TurnID != "turn-1" {
		t.Fatalf("turn_id=%q, want turn-1", event.TurnID)
	}
	var raw map[string]any
	if err := json.Unmarshal(event.Payload, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 6 {
		t.Fatalf("payload=%v, want no caption key", raw)
	}
	for _, key := range []string{"v", "call_id", "path", "name", "media_type", "size"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("payload=%v lacks key %q", raw, key)
		}
	}
	if raw["v"] != float64(core.PayloadVersion) || raw["call_id"] != "toolu_1" ||
		raw["path"] != f.Path || raw["name"] != f.Name || raw["media_type"] != f.MediaType {
		t.Fatalf("payload=%v", raw)
	}
	if raw["size"] != float64(f.Size) {
		t.Fatalf("size=%v, want %d", raw["size"], f.Size)
	}
}

// API-AGF-001: a non-empty caption is kept in the payload.
func TestSendFilePayloadCaption(t *testing.T) {
	st, info := newFileTestSession(t)
	svc := New(st, nil)
	tc := core.ToolCtx{SessionID: info.ID, TurnID: "turn-1", CallID: "toolu_1"}
	f := core.SentFile{Path: "attachments/2026-11-11/x.png", Name: "x.png", MediaType: "image/png", Size: 3, Caption: "Dashboard staging"}

	if _, err := svc.SendFile(context.Background(), tc, f); err != nil {
		t.Fatal(err)
	}
	events := fileSentEvents(t, st, info.ID)
	if len(events) != 1 {
		t.Fatalf("file/sent events=%d", len(events))
	}
	var payload core.FileSentPayload
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Caption != "Dashboard staging" {
		t.Fatalf("caption=%q", payload.Caption)
	}
}

// REQ-AGF-004: the channel hook's note is returned as the delivery note. With
// no hook the note is empty.
func TestSendFileHookNote(t *testing.T) {
	st, info := newFileTestSession(t)
	hooks := core.NewHooks()
	hooks.FileSend.Use(func(_ context.Context, _ core.FileSendRequest, _ core.Next[core.FileSendRequest, core.FileSendOutcome]) (core.FileSendOutcome, error) {
		return core.FileSendOutcome{Note: "Telegram delivery failed: chat is blocked"}, nil
	})
	svc := New(st, hooks)
	tc := core.ToolCtx{SessionID: info.ID, TurnID: "turn-1"}
	f := core.SentFile{Path: "attachments/2026-11-11/x.png", Name: "x.png", MediaType: "image/png", Size: 3}

	note, err := svc.SendFile(context.Background(), tc, f)
	if err != nil {
		t.Fatal(err)
	}
	if note != "Telegram delivery failed: chat is blocked" {
		t.Fatalf("note=%q", note)
	}

	noHook := New(st, nil)
	note, err = noHook.SendFile(context.Background(), core.ToolCtx{SessionID: info.ID, TurnID: "turn-2"}, f)
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Fatalf("note=%q, want empty", note)
	}
}
