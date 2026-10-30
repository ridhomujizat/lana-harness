package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// AC-WS-001: a turn creates the session workspace with mode 0700 before the
// agent's bash call runs.
func TestTurnCreatesMissingWorkspace(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "lana", "workspace")
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "bash", `{"command":"pwd"}`),
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"bash"}}})
	// The harness installs the fail-closed default gate; allow the bash call so
	// the test exercises the workspace, not the approval flow.
	h.hooks.ToolsPreExecute.Use(func(ctx context.Context, in core.ToolsPreExecuteInput, next core.Next[core.ToolsPreExecuteInput, core.ToolDecision]) (core.ToolDecision, error) {
		return core.ToolDecision{Allowed: true}, nil
	})

	ctx := context.Background()
	info, err := h.store.Create(ctx, core.NewSession{Agent: "assistant", Channel: "library", Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	turnID, err := h.reg.Start(ctx, info.ID, "run pwd")
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.reg.Wait(ctx, turnID)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}

	stat, err := os.Stat(ws)
	if err != nil {
		t.Fatalf("workspace not created: %v", err)
	}
	if !stat.IsDir() {
		t.Fatalf("workspace is not a directory: %s", ws)
	}
	if got := stat.Mode().Perm(); got != 0o700 {
		t.Fatalf("workspace mode=%#o want %#o", got, 0o700)
	}

	events, err := h.store.ChainEvents(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Kind != core.KindToolResult {
			continue
		}
		var payload core.ToolResultPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.CallID != "c1" {
			continue
		}
		found = true
		if payload.Status != core.ToolStatusSuccess {
			t.Fatalf("bash result=%+v", payload)
		}
		got, _ := filepath.EvalSymlinks(strings.TrimSpace(payload.Output))
		want, _ := filepath.EvalSymlinks(ws)
		if got != want {
			t.Fatalf("pwd=%q want %q", got, want)
		}
	}
	if !found {
		t.Fatal("bash tool result not found")
	}
}
