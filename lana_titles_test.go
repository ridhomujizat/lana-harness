package lana

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// waitSessionTitle polls until the session has a title source and no title
// pass runs, and returns the title and source.
func waitSessionTitle(t *testing.T, rt *Runtime, id string) (string, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		title, source, err := rt.SessionTitle(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if source != "" && !rt.TitlePending(id) {
			return title, source
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("session got no title")
	return "", ""
}

// Spec 40: a web session is named after its first turn through the router
// with source "title"; an owner title is never overwritten by a later turn;
// an empty title queues a fresh automatic one; regenerate replaces any title.
func TestSessionTitleLifecycle(t *testing.T) {
	sse := newSSEServer(t,
		sseResponse{body: textSSE("Install it with the script, then enable the service.")},
		sseResponse{body: textSSE(`"Deploy lana on a VPS."`)},
		sseResponse{body: textSSE("Done.")},
		sseResponse{body: textSSE("Title: VPS deploy steps")},
		sseResponse{body: textSSE("Server setup guide")},
	)
	rt, st := routedRuntime(t, fmt.Sprintf(`
[providers.a]
kind = "openai_compat"
base_url = %q

[agents.assistant]
model = "a/x"

[[routes]]
channel = "library"
agent = "assistant"
`, sse.server.URL))
	ctx := context.Background()
	info, err := rt.CreateChannelSession(ctx, "web", "owner", "assistant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Run(ctx, Request{SessionID: info.ID, Prompt: "How do I deploy lana on my VPS?"}); err != nil {
		t.Fatal(err)
	}
	if title, source := waitSessionTitle(t, rt, info.ID); title != "Deploy lana on a VPS" || source != core.TitleSourceAuto {
		t.Fatalf("auto title = %q/%q", title, source)
	}

	// REQ-TTL-008: an owner title, whitespace collapsed; a later turn keeps it.
	renamed, err := rt.SetSessionTitle(ctx, info.ID, "  My   VPS\nnotes ")
	if err != nil || renamed.Title != "My VPS notes" || renamed.TitleSource != core.TitleSourceOwner {
		t.Fatalf("rename = %+v err=%v", renamed, err)
	}
	if _, err := rt.Run(ctx, Request{SessionID: info.ID, Prompt: "thanks"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if title, source := waitSessionTitle(t, rt, info.ID); title != "My VPS notes" || source != core.TitleSourceOwner {
		t.Fatalf("owner title overwritten: %q/%q", title, source)
	}
	if _, err := rt.SetSessionTitle(ctx, info.ID, strings.Repeat("x", 81)); !errors.Is(err, ErrInvalidTitle) {
		t.Fatalf("81 characters: %v", err)
	}

	// REQ-TTL-009: an empty title queues a fresh automatic one.
	cleared, err := rt.SetSessionTitle(ctx, info.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if cleared.TitleSource == core.TitleSourceOwner {
		t.Fatalf("cleared = %+v", cleared)
	}
	if title, source := waitSessionTitle(t, rt, info.ID); title != "VPS deploy steps" || source != core.TitleSourceAuto {
		t.Fatalf("after clear = %q/%q", title, source)
	}

	// REQ-TTL-010: regenerate replaces the title and answers the new one.
	if _, err := rt.SetSessionTitle(ctx, info.ID, "Mine"); err != nil {
		t.Fatal(err)
	}
	regenerated, err := rt.RegenerateSessionTitle(ctx, info.ID)
	if err != nil || regenerated.Title != "Server setup guide" || regenerated.TitleSource != core.TitleSourceAuto {
		t.Fatalf("regenerate = %+v err=%v", regenerated, err)
	}

	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	calls := routerCalls(t, st, "title")
	if len(calls) != 3 {
		t.Fatalf("title calls = %d, want 3", len(calls))
	}
	for _, call := range calls {
		if call.Agent != "assistant" || call.SessionID != info.ID || call.Provider != "a" {
			t.Fatalf("title call = %+v", call)
		}
	}
}

// REQ-TTL-001: library sessions belong to the embedding program and get no
// automatic title.
func TestLibrarySessionNotTitled(t *testing.T) {
	text, stop := "ok", "end_turn"
	reply := mock.Response{Events: []mock.Event{{Text: &text}, {Stop: &stop}}}
	rt := newWorkflowRuntime(t, mock.Script{Responses: []mock.Response{reply}}, nil)
	ctx := context.Background()
	info, err := rt.CreateChannelSession(ctx, "library", "", "assistant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Run(ctx, Request{SessionID: info.ID, Prompt: "hello"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if title, source, _ := rt.SessionTitle(ctx, info.ID); title != "" || source != "" || rt.TitlePending(info.ID) {
		t.Fatalf("library session titled %q/%q", title, source)
	}
}

// REQ-TTL-007: a workflow run session is named "Workflow · <name>" at
// creation, with source system, and the title pass leaves it alone.
func TestWorkflowSessionFixedTitle(t *testing.T) {
	text, stop := "done", "end_turn"
	reply := mock.Response{Events: []mock.Event{{Text: &text}, {Stop: &stop}}}
	rt := newWorkflowRuntime(t, mock.Script{Responses: []mock.Response{reply, reply}}, nil)
	ctx := context.Background()
	detail, err := rt.workflows.Create(ctx, workflow.CreateInput{Name: "Morning digest", Guardian: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := rt.WorkflowDeps().RunAgent(ctx, workflow.AgentRequest{WorkflowID: detail.ID, Agent: "assistant", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := rt.SessionInfo(ctx, result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Workflow · Morning digest" || info.TitleSource != core.TitleSourceSystem {
		t.Fatalf("workflow session title = %q/%q", info.Title, info.TitleSource)
	}
	// An unknown workflow id names the session after the id.
	if got := rt.workflowSessionTitle(ctx, "wf-missing"); got != "Workflow · wf-missing" {
		t.Fatalf("missing workflow title = %q", got)
	}
}

// REQ-TTL-007: a calendar item that creates the agent's room session names it
// "Calendar · <item title>"; a later item keeps the room's title.
func TestCalendarSessionFixedTitle(t *testing.T) {
	text, stop := "ok", "end_turn"
	reply := mock.Response{Events: []mock.Event{{Text: &text}, {Stop: &stop}}}
	rt := newWorkflowRuntime(t, mock.Script{Responses: []mock.Response{reply, reply}}, nil)
	acts := calendarActions{rt: rt}
	ctx := context.Background()
	sid, err := acts.AgentPrompt(ctx, "assistant", "Check the news", "Morning brief")
	if err != nil {
		t.Fatal(err)
	}
	if title, source, _ := rt.SessionTitle(ctx, sid); title != "Calendar · Morning brief" || source != core.TitleSourceSystem {
		t.Fatalf("calendar session title = %q/%q", title, source)
	}
	if _, err := acts.AgentPrompt(ctx, "assistant", "Again", "Evening brief"); err != nil {
		t.Fatal(err)
	}
	if title, _, _ := rt.SessionTitle(ctx, sid); title != "Calendar · Morning brief" {
		t.Fatalf("room title changed to %q", title)
	}
}
