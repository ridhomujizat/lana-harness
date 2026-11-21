package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// TestToolsLoadedEventRoundTrip checks the real SQLite store accepts a
// tools/loaded event and returns its payload unchanged through the chain.
func TestToolsLoadedEventRoundTrip(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)

	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{{
		Kind:    core.KindToolsLoaded,
		Payload: core.ToolsLoaded{V: core.PayloadVersion, Names: []string{"mcp__srv__a"}},
	}}); err != nil {
		t.Fatalf("append tools/loaded: %v", err)
	}

	chain, err := s.ChainEvents(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found *core.ToolsLoaded
	for _, ev := range chain {
		if ev.Kind != core.KindToolsLoaded {
			continue
		}
		var payload core.ToolsLoaded
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatalf("decode tools/loaded payload: %v", err)
		}
		found = &payload
		break
	}
	if found == nil {
		t.Fatal("tools/loaded event not found in chain")
	}
	if want := []string{"mcp__srv__a"}; !reflect.DeepEqual(found.Names, want) {
		t.Fatalf("names=%v want %v", found.Names, want)
	}
}
