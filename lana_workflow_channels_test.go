package lana

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// The runtime registers the "web" Message channel and lists it as available.
func TestWorkflowMessageChannelsIncludeWeb(t *testing.T) {
	rt := newWorkflowRuntime(t, mock.Script{}, nil)

	var web *workflow.MessageChannel
	for _, c := range rt.Workflows().MessageChannels() {
		if c.ID == "web" {
			channel := c
			web = &channel
		}
	}
	if web == nil {
		t.Fatalf("channels = %+v", rt.Workflows().MessageChannels())
	}
	if web.Label != "Web chat" || !web.NeedsAgent || !web.Available {
		t.Fatalf("web channel = %+v", web)
	}
}

// A workflow run with a message node on ["web"] posts into the agent's web chat
// as an assistant message with Source "workflow" and SourceName the workflow
// name.
func TestWorkflowMessageWebPostsToAgentChat(t *testing.T) {
	rt := newWorkflowRuntime(t, mock.Script{}, nil)
	ctx := context.Background()

	graph := workflow.Graph{
		Nodes: []workflow.Node{
			{ID: "start", Type: "trigger.manual", Params: map[string]any{"enabled": true}},
			{ID: "say", Type: "message", Params: map[string]any{
				"text": "Daily report", "channels": []any{"web"}, "agent": "assistant",
			}},
		},
		Edges: []workflow.Edge{{ID: "e", From: "start", FromPort: "out", To: "say"}},
	}
	detail, err := rt.Workflows().Create(ctx, workflow.CreateInput{Name: "Digest", Guardian: "assistant", Graph: &graph})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	run, err := rt.Workflows().StartRun(ctx, detail.ID, "manual", nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	waitFor(t, "the run to settle", func() bool {
		r, err := rt.Workflows().RunDetail(ctx, detail.ID, run.ID)
		if err != nil {
			return false
		}
		return r.Run.Status == workflow.RunSucceeded || r.Run.Status == workflow.RunFailed
	})
	settled, err := rt.Workflows().RunDetail(ctx, detail.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Run.Status != workflow.RunSucceeded {
		t.Fatalf("run = %+v", settled.Run)
	}

	sessions, err := rt.store.List(ctx, core.SessionFilter{Agent: "assistant", Channel: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("web sessions = %+v", sessions)
	}
	events, err := rt.History(ctx, sessions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var found *core.AssistantMessage
	for _, event := range events {
		if event.Kind != core.KindAssistantMessage {
			continue
		}
		var msg core.AssistantMessage
		if json.Unmarshal(event.Payload, &msg) != nil {
			continue
		}
		if msg.Source == "workflow" {
			message := msg
			found = &message
		}
	}
	if found == nil {
		t.Fatal("no workflow assistant message in the agent's web chat")
	}
	if found.SourceName != "Digest" {
		t.Fatalf("source name = %q", found.SourceName)
	}
	if len(found.Content) != 1 || found.Content[0].Text != "Daily report" {
		t.Fatalf("content = %+v", found.Content)
	}
}
