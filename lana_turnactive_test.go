package lana

import (
	"context"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// TestTurnActive covers G-7: the facade reports a turn running in this process
// and clears once the turn ends.
func TestTurnActive(t *testing.T) {
	text, stop := "hello", "end_turn"
	delay := 250
	rt, err := New(context.Background(), Options{
		Home: t.TempDir(),
		Layers: []config.Layer{{Name: "test", TOML: []byte(`
[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.assistant]
provider = "mock"
model = "m"
`)}},
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{Responses: []mock.Response{
			{Events: []mock.Event{{DelayMS: &delay}, {Text: &text}, {Stop: &stop}}},
		}})},
	})
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	ctx := context.Background()
	info, err := rt.CreateChannelSession(ctx, "web", "owner", "assistant")
	if err != nil {
		t.Fatalf("CreateChannelSession: %v", err)
	}
	if rt.TurnActive(info.ID) {
		t.Fatal("TurnActive is true before any turn")
	}
	if rt.TurnActive("unknown-session") {
		t.Fatal("TurnActive is true for an unknown session")
	}

	done := make(chan error, 1)
	go func() {
		_, err := rt.Run(ctx, Request{Prompt: "hello", SessionID: info.ID})
		done <- err
	}()

	deadline := time.Now().Add(3 * time.Second)
	for !rt.TurnActive(info.ID) {
		if !time.Now().Before(deadline) {
			t.Fatal("TurnActive stayed false while the turn was running")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	deadline = time.Now().Add(3 * time.Second)
	for rt.TurnActive(info.ID) {
		if !time.Now().Before(deadline) {
			t.Fatal("TurnActive stayed true after the turn ended")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
