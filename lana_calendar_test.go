package lana

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workflow"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// newCalendarWorkflows builds the calendar's workflow adapter over a workflow
// MemStore and a service on the runtime's workflow deps.
func newCalendarWorkflows(t *testing.T, rt *Runtime) (calendarWorkflows, *workflow.MemStore) {
	t.Helper()
	st := workflow.NewMemStore()
	svc := workflow.NewService(st, rt.WorkflowDeps(),
		workflow.WithTickInterval(time.Hour),
		workflow.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(svc.Close)
	return calendarWorkflows{svc: svc, store: st}, st
}

// seedCalendarWorkflow stores an active workflow with one daily 07:00 schedule
// trigger.
func seedCalendarWorkflow(t *testing.T, st *workflow.MemStore, id, name string) workflow.Workflow {
	t.Helper()
	g := workflow.Graph{Nodes: []workflow.Node{{
		ID: "every-day", Type: "trigger.schedule", Name: "Every day",
		Params: map[string]any{"cron": "0 7 * * *", "timezone": "UTC", "enabled": true},
	}}}
	wf, err := st.CreateWorkflow(context.Background(), workflow.Workflow{
		ID: id, Name: name, Guardian: "assistant", Status: workflow.StatusActive, CreatedAt: 1,
	}, g, "owner", "initial")
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	return wf
}

// The runtime builds the calendar, registers the calendar tool and offers it
// in the tool catalog (design §Runtime glue, REQ-CAL-009).
func TestRuntimeCalendarWired(t *testing.T) {
	rt := newWorkflowRuntime(t, toolCallScript(), nil)
	if rt.Calendar() == nil {
		t.Fatal("Calendar() is nil after New")
	}
	if rt.CalendarGoogle() == nil {
		t.Fatal("CalendarGoogle() is nil after New")
	}
	if _, ok := rt.coreCtx.Tools.Get("calendar"); !ok {
		t.Fatal("calendar tool is not registered")
	}
	groups, err := rt.ToolCatalog("assistant")
	if err != nil {
		t.Fatalf("ToolCatalog: %v", err)
	}
	found := false
	for _, g := range groups {
		for _, e := range g.Tools {
			if g.ID == "calendar" && e.Name == "calendar" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("calendar tool not in the catalog: %v", catalogGroupIDs(groups))
	}
	rt.StartCalendar(context.Background())
	rt.StartCalendar(context.Background()) // a second start is a no-op
}

// Slots and Runs map the workflow side with the workflow's name and guardian
// (REQ-CAL-008).
func TestCalendarWorkflowsSlotsAndRuns(t *testing.T) {
	rt := newWorkflowRuntime(t, toolCallScript(), nil)
	ws, st := newCalendarWorkflows(t, rt)
	ctx := context.Background()
	wf := seedCalendarWorkflow(t, st, "wf_daily", "Daily research")

	from := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	slots, err := ws.Slots(ctx, from, from.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if len(slots) != 2 {
		t.Fatalf("slots=%+v want 2 daily slots", slots)
	}
	sl := slots[0]
	if sl.WorkflowID != "wf_daily" || sl.NodeID != "every-day" || sl.Name != "Daily research" || sl.Guardian != "assistant" ||
		sl.Trigger != "schedule" || !sl.At.Equal(from.Add(7*time.Hour)) || sl.Ref() != "wf_daily/every-day" {
		t.Fatalf("slot=%+v", sl)
	}

	base := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{base.Add(-time.Hour), base.Add(7 * time.Hour), base.Add(9 * time.Hour), base.Add(25 * time.Hour)} {
		if _, err := st.CreateRun(ctx, workflow.Run{
			WorkflowID: wf.ID, Version: wf.Version, Trigger: "schedule", Status: workflow.RunSucceeded,
			StartedAt: at.UnixMilli(), EndedAt: at.Add(time.Minute).UnixMilli(),
		}); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}
	runs, err := ws.Runs(ctx, base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs=%+v want the 2 runs inside the day", runs)
	}
	if runs[0].StartedAt != base.Add(7*time.Hour).UnixMilli() || runs[1].StartedAt != base.Add(9*time.Hour).UnixMilli() {
		t.Fatalf("runs not oldest first: %+v", runs)
	}
	r := runs[0]
	if r.WorkflowID != wf.ID || r.Name != "Daily research" || r.Guardian != "assistant" || r.Trigger != "schedule" ||
		r.Status != string(workflow.RunSucceeded) || r.Number != 2 || r.ID == "" || r.EndedAt == 0 {
		t.Fatalf("run=%+v", r)
	}

	name, status, ok := ws.Workflow(ctx, wf.ID)
	if !ok || name != "Daily research" || status != "active" {
		t.Fatalf("Workflow=%q %q %v", name, status, ok)
	}
	if _, _, ok := ws.Workflow(ctx, "missing"); ok {
		t.Fatal("Workflow(missing) ok")
	}
}

// StartRun starts a run with trigger "calendar" (API-CAL-018).
func TestCalendarWorkflowsStartRun(t *testing.T) {
	rt := newWorkflowRuntime(t, toolCallScript(), nil)
	ws, st := newCalendarWorkflows(t, rt)
	ctx := context.Background()
	wf := seedCalendarWorkflow(t, st, "wf_daily", "Daily research")

	id, number, err := ws.StartRun(ctx, wf.ID, []byte(`{"from":"calendar"}`))
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if id == "" || number != 1 {
		t.Fatalf("StartRun=%q %d", id, number)
	}
	run, err := st.GetRun(ctx, id)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Trigger != "calendar" || string(run.Input) != `{"from":"calendar"}` {
		t.Fatalf("run=%+v want trigger calendar", run)
	}
}

// AgentExists checks the configured agents (REQ-CAL-006).
func TestCalendarActionsAgentExists(t *testing.T) {
	rt := newWorkflowRuntime(t, toolCallScript(), nil)
	acts := calendarActions{rt: rt}
	if !acts.AgentExists("assistant") {
		t.Fatal("assistant does not exist")
	}
	if acts.AgentExists("nobody") {
		t.Fatal("nobody exists")
	}
}

// AgentPrompt runs one turn in the agent's room, the owner web session, with
// a "Calendar · <title>" header, and reuses that session (REQ-CAL-006).
func TestCalendarActionsAgentPrompt(t *testing.T) {
	text, stop := "ok", "end_turn"
	reply := mock.Response{Events: []mock.Event{{Text: &text}, {Stop: &stop}}}
	rt := newWorkflowRuntime(t, mock.Script{Responses: []mock.Response{reply, reply}}, nil)
	acts := calendarActions{rt: rt}
	ctx := context.Background()

	sid, err := acts.AgentPrompt(ctx, "assistant", "Check the news", "Morning brief")
	if err != nil {
		t.Fatalf("AgentPrompt: %v", err)
	}
	info, err := rt.SessionInfo(ctx, sid)
	if err != nil {
		t.Fatalf("SessionInfo: %v", err)
	}
	if info.Channel != "web" || info.Address != "owner" || info.Agent != "assistant" || info.TurnCount != 1 {
		t.Fatalf("session=%+v want the assistant's web owner room with 1 turn", info)
	}
	events, err := rt.History(ctx, sid)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	found := false
	for _, e := range events {
		if strings.Contains(string(e.Payload), "Calendar · Morning brief\\n\\nCheck the news") {
			found = true
		}
	}
	if !found {
		t.Fatal("room has no message with the calendar header")
	}

	again, err := acts.AgentPrompt(ctx, "assistant", "Again", "Evening brief")
	if err != nil {
		t.Fatalf("second AgentPrompt: %v", err)
	}
	if again != sid {
		t.Fatalf("second prompt session=%s want the room %s", again, sid)
	}
}

// SendMessage goes to the Telegram sender for both targets and fails without
// one; SetCalendarTelegram also installs the owner notifier (REQ-CAL-006,
// REQ-CAL-007).
func TestCalendarActionsSendMessage(t *testing.T) {
	rt := newWorkflowRuntime(t, toolCallScript(), nil)
	acts := calendarActions{rt: rt}
	ctx := context.Background()

	if err := acts.SendMessage(ctx, "web", "hi"); !errors.Is(err, errNoWebChannel) {
		t.Fatalf("web without telegram err=%v", err)
	}
	if err := acts.SendMessage(ctx, "telegram", "hi"); err == nil {
		t.Fatal("telegram without a sender succeeded")
	}

	var mu sync.Mutex
	var sent []string
	rt.SetCalendarTelegram(func(_ context.Context, text string) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, text)
		return nil
	})
	if err := acts.SendMessage(ctx, "telegram", "one"); err != nil {
		t.Fatalf("telegram: %v", err)
	}
	if err := acts.SendMessage(ctx, "web", "two"); err != nil {
		t.Fatalf("web: %v", err)
	}
	if err := acts.SendMessage(ctx, "email", "x"); err == nil {
		t.Fatal("unknown target succeeded")
	}
	mu.Lock()
	got := strings.Join(sent, ",")
	mu.Unlock()
	if got != "one,two" {
		t.Fatalf("sent=%q", got)
	}

	rt.SetCalendarTelegram(nil)
	if err := acts.SendMessage(ctx, "telegram", "three"); err == nil {
		t.Fatal("telegram after removing the sender succeeded")
	}
}
