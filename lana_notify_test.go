package lana

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/notify"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

func newNotifyTestRuntime(t *testing.T, replies ...string) *Runtime {
	t.Helper()
	end := "end_turn"
	responses := make([]mock.Response, 0, len(replies))
	for i := range replies {
		text := replies[i]
		responses = append(responses, mock.Response{Events: []mock.Event{{Text: &text}, {Stop: &end}}})
	}
	cfg := "[providers.mock]\nkind = \"openai_compat\"\nbase_url = \"mock://local\"\n\n[agents.coder]\nprovider = \"mock\"\nmodel = \"m\"\n\n[[routes]]\nchannel = \"library\"\nagent = \"coder\"\n"
	rt, err := New(context.Background(), Options{
		Home:      t.TempDir(),
		Layers:    []config.Layer{{Name: "test", TOML: []byte(cfg)}},
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{Responses: responses})},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// titledSession creates a channel session with an owner title, so the spec 40
// title pass does not take a scripted response.
func titledSession(t *testing.T, rt *Runtime, channel, title string) core.SessionInfo {
	t.Helper()
	ctx := context.Background()
	info, err := rt.CreateChannelSession(ctx, channel, "owner", "coder")
	if err != nil {
		t.Fatal(err)
	}
	if info, err = rt.SetSessionTitle(ctx, info.ID, title); err != nil {
		t.Fatal(err)
	}
	return info
}

func waitNotify(t *testing.T, ch <-chan notify.Event, kind notify.Kind, within time.Duration) (notify.Event, bool) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case e := <-ch:
			if e.Kind == kind {
				return e, true
			}
		case <-deadline:
			return notify.Event{}, false
		}
	}
}

func TestNotificationsReplyOfWebTurn(t *testing.T) {
	rt := newNotifyTestRuntime(t, "Here is the **fix**:\n```go\nx := 1\n```\nSee [PR](http://x).", "second", "third")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.StartNotifications(ctx)
	rt.StartNotifications(ctx) // a second call is a no-op
	bus := rt.Notifications()
	_, events, unsubscribe := bus.Subscribe("")
	defer unsubscribe()

	web := titledSession(t, rt, "web", "Bug hunt")
	if _, err := rt.Run(ctx, Request{SessionID: web.ID, Prompt: "fix it"}); err != nil {
		t.Fatal(err)
	}
	e, ok := waitNotify(t, events, notify.KindReply, 5*time.Second)
	if !ok {
		t.Fatal("no reply notification for a web turn")
	}
	if e.Title != "coder · Bug hunt" || e.Body != "Here is the fix: [code] See PR." || e.SessionID != web.ID || e.URL != "#/sessions/"+web.ID || e.Thread != web.ID {
		t.Fatalf("reply = %+v", e)
	}

	// A session a dashboard tab shows does not notify.
	bus.SetVisible(web.ID, true)
	if _, err := rt.Run(ctx, Request{SessionID: web.ID, Prompt: "again"}); err != nil {
		t.Fatal(err)
	}
	if e, ok := waitNotify(t, events, notify.KindReply, 500*time.Millisecond); ok {
		t.Fatalf("reply of a visible session notified: %+v", e)
	}

	// A Telegram session's reply already reaches Telegram.
	tg := titledSession(t, rt, "telegram", "Chat")
	if _, err := rt.Run(ctx, Request{SessionID: tg.ID, Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	if e, ok := waitNotify(t, events, notify.KindReply, 500*time.Millisecond); ok {
		t.Fatalf("telegram reply notified: %+v", e)
	}
}

func TestNotificationsNotStartedPublishNothing(t *testing.T) {
	rt := newNotifyTestRuntime(t, "hello")
	_, events, unsubscribe := rt.Notifications().Subscribe("")
	defer unsubscribe()
	web := titledSession(t, rt, "web", "Quiet")
	if _, err := rt.Run(context.Background(), Request{SessionID: web.ID, Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	if e, ok := waitNotify(t, events, notify.KindReply, 300*time.Millisecond); ok {
		t.Fatalf("a library runtime published %+v", e)
	}
}

func sessionEvent(sessionID, kind string, payload any) core.SessionEvent {
	data, _ := json.Marshal(payload)
	return core.SessionEvent{SessionID: sessionID, Kind: kind, Payload: data}
}

func TestReplyFeederRules(t *testing.T) {
	sessions := map[string]core.SessionInfo{
		"web":   {ID: "web", Channel: "web", Agent: "coder"},
		"child": {ID: "child", Channel: "web", Agent: "worker", ParentSessionID: "web"},
		"wf":    {ID: "wf", Channel: "workflow", Agent: "runner"},
		"lib":   {ID: "lib", Channel: "library", Agent: "coder"},
	}
	lookup := func(_ context.Context, id string) (core.SessionInfo, error) {
		info, ok := sessions[id]
		if !ok {
			return core.SessionInfo{}, errors.New("not found")
		}
		return info, nil
	}
	bus := notify.NewBus()
	_, events, cancel := bus.Subscribe("")
	defer cancel()
	feeder := newReplyFeeder(bus, lookup)
	ctx := context.Background()
	turn := func(session, reason string, texts ...string) {
		feeder.handle(ctx, sessionEvent(session, core.KindTurnStart, core.TurnStart{TurnID: "t-" + session + reason}))
		for _, text := range texts {
			feeder.handle(ctx, sessionEvent(session, core.KindAssistantMessage, core.AssistantMessage{Content: []core.ContentBlock{
				{Kind: core.BlockThinking, Text: "hmm"}, {Kind: core.BlockText, Text: text},
			}}))
		}
		feeder.handle(ctx, sessionEvent(session, core.KindTurnEnd, core.TurnEnd{TurnID: "t-" + session + reason, Reason: reason}))
	}

	for _, session := range []string{"child", "wf", "lib", "missing"} {
		turn(session, "end_turn", "hello")
	}
	turn("web", "cancelled", "partial")
	turn("web", "end_turn") // no text: a tool-only turn
	select {
	case e := <-events:
		t.Fatalf("unexpected notification %+v", e)
	default:
	}

	turn("web", "max_steps", "first message", "final answer")
	e, ok := waitNotify(t, events, notify.KindReply, time.Second)
	if !ok || e.Body != "final answer" || e.Title != "coder · New chat" {
		t.Fatalf("reply = %+v ok=%v", e, ok)
	}
}

type fakeRunSource struct {
	runs      map[string]workflow.RunDetail
	workflows map[string]workflow.Detail
}

func (f *fakeRunSource) RunDetail(_ context.Context, id, ref string) (workflow.RunDetail, error) {
	d, ok := f.runs[ref]
	if !ok || d.Run.WorkflowID != id {
		return workflow.RunDetail{}, workflow.ErrNotFound
	}
	return d, nil
}

func (f *fakeRunSource) Get(_ context.Context, id string) (workflow.Detail, error) {
	d, ok := f.workflows[id]
	if !ok {
		return workflow.Detail{}, workflow.ErrNotFound
	}
	return d, nil
}

func TestWorkflowRunNotifier(t *testing.T) {
	start := time.UnixMilli(1_700_000_000_000)
	source := &fakeRunSource{
		runs:      map[string]workflow.RunDetail{},
		workflows: map[string]workflow.Detail{"wf1": {Workflow: workflow.Workflow{ID: "wf1", Name: "Daily digest"}}},
	}
	bus := notify.NewBus()
	_, events, cancel := bus.Subscribe("")
	defer cancel()
	n := newWorkflowRunNotifier(bus, source, start)
	ctx := context.Background()
	set := func(run workflow.Run, decisions ...workflow.Decision) {
		source.runs[run.ID] = workflow.RunDetail{Run: run, Decisions: decisions}
		n.handle(ctx, workflow.Change{WorkflowID: run.WorkflowID, RunID: run.ID})
	}
	next := func() notify.Event {
		select {
		case e := <-events:
			return e
		case <-time.After(time.Second):
			t.Fatal("no notification")
			return notify.Event{}
		}
	}
	none := func() {
		select {
		case e := <-events:
			t.Fatalf("unexpected notification %+v", e)
		default:
		}
	}

	run := workflow.Run{ID: "r1", WorkflowID: "wf1", Number: 4, Trigger: "manual", Status: workflow.RunQueued, StartedAt: start.UnixMilli() + 1000}
	set(run)
	if e := next(); e.Kind != notify.KindWorkflowStarted || e.Title != "Workflow started · Daily digest" || e.Body != "Trigger: manual" || !e.Silent || e.URL != "#/workflows/wf1/runs/4" {
		t.Fatalf("started = %+v", e)
	}
	run.Status = workflow.RunRunning
	set(run)
	none()

	run.Status = workflow.RunWaiting
	approval := workflow.Decision{ID: "d1", WorkflowID: "wf1", RunID: "r1", Kind: "approval", Status: "pending", Reason: "Send the report?"}
	set(run, approval)
	e := next()
	if e.Kind != notify.KindWorkflowWaiting || e.ApprovalID != "wf:wf1:d1" || e.Body != "Send the report?" || !e.TimeSensitive {
		t.Fatalf("waiting = %+v", e)
	}
	set(run, approval)
	none()

	approval.Status, approval.Choice = "resolved", "approve"
	run.Status = workflow.RunRunning
	set(run, approval)
	if e := next(); e.Kind != notify.KindApprovalDecided || e.ApprovalID != "wf:wf1:d1" || e.Outcome != "approve" {
		t.Fatalf("decided = %+v", e)
	}

	run.Status, run.EndedAt, run.Summary = workflow.RunSucceeded, run.StartedAt+65_000, "Sent 3 items"
	set(run, approval)
	if e := next(); e.Kind != notify.KindWorkflowFinished || e.Title != "Workflow done · Daily digest · 1m 05s" || e.Body != "Sent 3 items" || e.Silent {
		t.Fatalf("finished = %+v", e)
	}
	set(run, approval)
	none()

	failed := workflow.Run{ID: "r2", WorkflowID: "wf1", Number: 5, Trigger: "schedule", Status: workflow.RunFailed, StartedAt: start.UnixMilli() + 2000, EndedAt: start.UnixMilli() + 3000, Error: "node fetch: 500 Internal Server Error"}
	set(failed)
	if e := next(); e.Kind != notify.KindWorkflowStarted {
		t.Fatalf("fast run start = %+v", e)
	}
	if e := next(); e.Kind != notify.KindWorkflowFailed || e.Body != "node fetch: 500 Internal Server Error" || e.Title != "Workflow failed · Daily digest" {
		t.Fatalf("failed = %+v", e)
	}

	// A run resumed after a daemon start does not announce its start; one
	// that ended before the start announces nothing.
	resumed := workflow.Run{ID: "r3", WorkflowID: "wf1", Number: 2, Status: workflow.RunRunning, StartedAt: start.UnixMilli() - 60_000}
	set(resumed)
	none()
	old := workflow.Run{ID: "r4", WorkflowID: "wf1", Number: 1, Status: workflow.RunSucceeded, StartedAt: start.UnixMilli() - 90_000, EndedAt: start.UnixMilli() - 80_000}
	set(old)
	none()
	resumed.Status, resumed.EndedAt = workflow.RunSucceeded, start.UnixMilli()+5000
	set(resumed)
	if e := next(); e.Kind != notify.KindWorkflowFinished {
		t.Fatalf("resumed finish = %+v", e)
	}

	// A guardian decision waits without the Allow and Reject actions.
	guard := workflow.Run{ID: "r5", WorkflowID: "wf1", Number: 6, Status: workflow.RunWaiting, StartedAt: start.UnixMilli() - 1}
	set(guard, workflow.Decision{ID: "d2", Kind: "guardian", Status: "pending", Reason: "Retry with a smaller page?"})
	if e := next(); e.Kind != notify.KindWorkflowWaiting || e.ApprovalID != "" {
		t.Fatalf("guardian waiting = %+v", e)
	}
}
