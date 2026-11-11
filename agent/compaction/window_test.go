package compaction

import (
	"context"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// The context meter's window is the policy a compaction would use.
func TestContextWindowMatchesPolicy(t *testing.T) {
	_, info, svc := newService(t, &recordProvider{}, core.Config{Agents: map[string]core.AgentConfig{"coder": {Provider: "mock", Model: "m", ContextWindow: 100000}}})
	got, err := svc.ContextWindow(context.Background(), info.ID)
	if err != nil {
		t.Fatalf("context window: %v", err)
	}
	if got.Window != 100000 || got.CompactAt <= 0 || got.CompactAt >= got.Window {
		t.Fatalf("window = %+v, want 0 < compact_at < window", got)
	}
}
