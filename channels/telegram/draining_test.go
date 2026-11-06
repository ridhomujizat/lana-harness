package telegram

import (
	"testing"
	"time"
)

// TestDrainingReply covers AC-TFB-004: while the backend drains, a text message
// gets the draining reply and starts no turn (REQ-TFB-002, API-TFB-008).
func TestDrainingReply(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.draining = true
	startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "hello")
	api.waitCall("sendMessage", textIs(drainingReply))
	time.Sleep(50 * time.Millisecond)

	if got := backend.promptList(); len(got) != 0 {
		t.Fatalf("prompts = %v, want none", got)
	}
	backend.mu.Lock()
	sent := append([]string(nil), backend.sent...)
	backend.mu.Unlock()
	if len(sent) != 0 {
		t.Fatalf("queued messages = %v, want none", sent)
	}
	for _, call := range api.snapshot() {
		if call.Method == "sendMessage" && call.Params["text"] == "…" {
			t.Fatalf("a turn started while draining: %v", call)
		}
	}
}
