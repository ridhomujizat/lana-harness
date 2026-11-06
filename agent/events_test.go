package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

func TestEventsPayloadVersion(t *testing.T) {
	store := memstore.New()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	events := &Events{Store: store, SessionID: info.ID}
	written, err := events.Append(context.Background(), core.EventDraft{Kind: core.KindTurnStart, TurnID: "t1", Payload: core.TurnStart{TurnID: "t1"}})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(written[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["v"].(float64) != 1 {
		t.Fatalf("payload=%s", written[0].Payload)
	}
}
