package workflow

// Tests for the workflow Service and scheduler (REQ-WF-001, REQ-WF-003,
// REQ-WF-004, REQ-WF-005, REQ-WF-008, REQ-WF-009, REQ-WF-010, REQ-WF-011). They
// use MemStore, the fake Deps of the engine tests and a controllable clock; the
// scheduler tick is called directly with a large tick interval. Every test
// joins the run goroutines with Service.Close.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

// agentDeps is a fake Deps with a configurable agent registry, so a test can
// exercise the unknown_agent rule.
type agentDeps struct {
	*fakeDeps
	known map[string]bool
}

func (d *agentDeps) AgentExists(name string) bool { return d.known[name] }

// setClock moves a testClock to t.
func setClock(c *testClock, t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// newTestService builds a Service with a controllable clock, an instant retry
// sleep, a large tick interval and a discarded logger, and closes it at the end
// of the test.
func newTestService(t *testing.T, st *MemStore, deps Deps, clk *testClock) *Service {
	t.Helper()
	svc := NewService(st, deps,
		WithClock(clk.Now),
		WithTickInterval(time.Hour),
		WithSleep(func(context.Context, time.Duration) error { return nil }),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	t.Cleanup(svc.Close)
	return svc
}

// seedRunAt stores a run with an explicit start time and status.
func seedRunAt(t *testing.T, st *MemStore, wf Workflow, trigger string, status RunStatus, startedAt int64, input any) Run {
	t.Helper()
	run, err := st.CreateRun(context.Background(), Run{
		WorkflowID: wf.ID, Version: wf.Version, Trigger: trigger, Status: status,
		Input: nodeJSON(input), StartedAt: startedAt,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return run
}

// ---- AC-WF-001 ------------------------------------------------------------

func TestServiceCreateUpdateVersions(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateInput{Name: "Riset harian", Guardian: "g"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID != "riset-harian" || created.Status != StatusDraft || created.Version != 1 {
		t.Fatalf("detail=%+v", created)
	}
	if len(created.Graph.Nodes) != 1 || created.Graph.Nodes[0].ID != "run-button" {
		t.Fatalf("default graph=%+v", created.Graph)
	}

	g2 := Graph{Nodes: []Node{wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true})}}
	updated, err := svc.Update(ctx, "riset-harian", UpdateInput{BaseVersion: 1, Graph: &g2})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("version=%d want 2", updated.Version)
	}
	v1, err := st.GetVersion(ctx, "riset-harian", 1)
	if err != nil {
		t.Fatalf("version 1 not readable: %v", err)
	}
	if len(v1.Graph.Nodes) != 1 || v1.Graph.Nodes[0].ID != "run-button" {
		t.Fatalf("version 1 graph=%+v", v1.Graph)
	}

	if _, err := svc.Update(ctx, "riset-harian", UpdateInput{BaseVersion: 1, Graph: &g2}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale base err=%v want ErrVersionConflict", err)
	}
}

// ---- create with an unknown guardian --------------------------------------

func TestServiceCreateUnknownGuardian(t *testing.T) {
	st := NewMemStore()
	deps := &agentDeps{fakeDeps: newFakeDeps(), known: map[string]bool{"g": true}}
	svc := newTestService(t, st, deps, &testClock{})

	_, err := svc.Create(context.Background(), CreateInput{Name: "X", Guardian: "ghost"})
	var invalid *InvalidGraphError
	if !errors.As(err, &invalid) {
		t.Fatalf("err=%v want *InvalidGraphError", err)
	}
	if len(invalid.Issues) != 1 || invalid.Issues[0].Code != "unknown_agent" {
		t.Fatalf("issues=%+v", invalid.Issues)
	}
}

// ---- AC-WF-003 ------------------------------------------------------------

func TestSchedulerScheduleJakarta(t *testing.T) {
	st := NewMemStore()
	clk := &testClock{}
	svc := newTestService(t, st, newFakeDeps(), clk)
	ctx := context.Background()

	g := Graph{Nodes: []Node{wfNode("sched", "trigger.schedule", "Every day",
		map[string]any{"cron": "0 7 * * *", "timezone": "Asia/Jakarta", "enabled": true})}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	jakarta, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatalf("load Asia/Jakarta: %v", err)
	}
	setClock(clk, time.Date(2026, 11, 3, 7, 0, 0, 0, jakarta))
	svc.tick(clk.Now())
	svc.engine.Wait()

	runs, _ := st.ListRuns(ctx, RunFilter{WorkflowID: "w"})
	if len(runs) != 1 || runs[0].Trigger != "schedule" {
		t.Fatalf("runs=%+v want one schedule run", runs)
	}

	svc.tick(clk.Now())
	svc.engine.Wait()
	runs, _ = st.ListRuns(ctx, RunFilter{WorkflowID: "w"})
	if len(runs) != 1 {
		t.Fatalf("a second tick in the same minute started %d runs", len(runs))
	}

	setClock(clk, time.Date(2026, 11, 3, 7, 1, 0, 0, jakarta))
	svc.tick(clk.Now())
	svc.engine.Wait()
	runs, _ = st.ListRuns(ctx, RunFilter{WorkflowID: "w"})
	if len(runs) != 1 {
		t.Fatalf("a tick at 07:01 started %d runs", len(runs))
	}
}

// ---- interval trigger -----------------------------------------------------

func TestSchedulerInterval(t *testing.T) {
	st := NewMemStore()
	clk := &testClock{}
	svc := newTestService(t, st, newFakeDeps(), clk)
	ctx := context.Background()

	g := Graph{Nodes: []Node{wfNode("iv", "trigger.interval", "Every 30",
		map[string]any{"minutes": 30, "enabled": true})}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	setClock(clk, time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC))
	svc.tick(clk.Now())
	svc.engine.Wait()
	runs, _ := st.ListRuns(ctx, RunFilter{WorkflowID: "w"})
	if len(runs) != 1 || runs[0].Trigger != "interval" {
		t.Fatalf("runs=%+v want one interval run", runs)
	}

	svc.tick(clk.Now())
	svc.engine.Wait()
	runs, _ = st.ListRuns(ctx, RunFilter{WorkflowID: "w"})
	if len(runs) != 1 {
		t.Fatalf("interval fired again too soon: %d runs", len(runs))
	}

	setClock(clk, time.Date(2026, 11, 3, 10, 31, 0, 0, time.UTC))
	svc.tick(clk.Now())
	svc.engine.Wait()
	runs, _ = st.ListRuns(ctx, RunFilter{WorkflowID: "w"})
	if len(runs) != 2 {
		t.Fatalf("interval did not fire after 31 min: %d runs", len(runs))
	}
}

// ---- paused workflow never fires ------------------------------------------

func TestSchedulerPausedNeverFires(t *testing.T) {
	st := NewMemStore()
	clk := &testClock{}
	svc := newTestService(t, st, newFakeDeps(), clk)
	ctx := context.Background()

	g := Graph{Nodes: []Node{wfNode("sched", "trigger.schedule", "Every minute",
		map[string]any{"cron": "* * * * *", "timezone": "UTC", "enabled": true})}}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	if _, err := svc.Pause(ctx, wf.ID); err != nil {
		t.Fatalf("pause: %v", err)
	}

	setClock(clk, time.Date(2026, 11, 3, 10, 30, 0, 0, time.UTC))
	svc.tick(clk.Now())
	svc.engine.Wait()
	runs, _ := st.ListRuns(ctx, RunFilter{WorkflowID: "w"})
	if len(runs) != 0 {
		t.Fatalf("a paused workflow fired %d runs", len(runs))
	}
}

// ---- AC-WF-004 ------------------------------------------------------------

func TestServiceWebhook(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	ctx := context.Background()

	g := Graph{Nodes: []Node{wfNode("hook", "trigger.webhook", "Hook", map[string]any{"enabled": true})}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g", WebhookToken: "secret"}, g)

	if _, err := svc.Webhook(ctx, "w", "wrong", []byte(`{"a":1}`)); !errors.Is(err, ErrBadToken) {
		t.Fatalf("wrong token err=%v want ErrBadToken", err)
	}
	run, err := svc.Webhook(ctx, "w", "secret", []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if run.Trigger != "webhook" || string(run.Input) != `{"a":1}` {
		t.Fatalf("run=%+v", run)
	}
}

// ---- AC-WF-005 ------------------------------------------------------------

func TestServiceCommand(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	ctx := context.Background()

	g := Graph{Nodes: []Node{wfNode("cmd", "trigger.chat", "Riset",
		map[string]any{"command": "riset", "enabled": true})}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "Riset", Guardian: "g"}, g)

	res, handled := svc.Command(ctx, "web", "/riset ai news")
	if !handled {
		t.Fatal("command was not handled")
	}
	if res.WorkflowID != "w" || res.RunID == "" {
		t.Fatalf("result=%+v", res)
	}
	run := mustRun(t, st, res.RunID)
	if run.Trigger != "chat" {
		t.Fatalf("trigger=%q want chat", run.Trigger)
	}
	var in map[string]string
	if err := json.Unmarshal(run.Input, &in); err != nil {
		t.Fatalf("input %s: %v", run.Input, err)
	}
	if in["text"] != "ai news" || in["channel"] != "web" {
		t.Fatalf("input=%v", in)
	}
	if _, handled := svc.Command(ctx, "web", "/nope"); handled {
		t.Fatal("unknown command was handled")
	}
}

// ---- AC-WF-017 / AC-WF-018 ------------------------------------------------

// ---- activate / lock / draft ----------------------------------------------

func TestServiceActivateLockAndDraft(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	ctx := context.Background()

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true}),
			wfNode("run", "bash", "Run", map[string]any{"command": "echo hi"}),
		},
		Edges: []Edge{wfEdge("start", "out", "run")},
	}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g", Status: StatusDraft}, g)

	dl, err := svc.Activate(ctx, "w", 1)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if dl.Status != StatusActive || dl.Lock == nil || !dl.LockCurrent {
		t.Fatalf("detail=%+v", dl)
	}
	locked := false
	for _, tool := range dl.Lock.Tools {
		if tool.Tool == "bash" {
			locked = true
		}
	}
	if !locked {
		t.Fatalf("lock=%+v want bash", dl.Lock.Tools)
	}

	g2 := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true}),
			wfNode("run", "bash", "Run", map[string]any{"command": "echo hi"}),
			wfNode("page", "browser", "Page", map[string]any{"url": "https://example.com"}),
		},
		Edges: []Edge{wfEdge("start", "out", "run"), wfEdge("run", "out", "page")},
	}
	updated, err := svc.Update(ctx, "w", UpdateInput{BaseVersion: dl.Version, Graph: &g2})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Status != StatusDraft {
		t.Fatalf("status=%s want draft after a graph needing a new tool", updated.Status)
	}
}

func TestServiceResumeRequiresLock(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})

	g := Graph{Nodes: []Node{wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true})}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	if _, err := svc.Resume(context.Background(), "w"); !errors.Is(err, ErrLockRequired) {
		t.Fatalf("resume err=%v want ErrLockRequired", err)
	}
}

// ---- overview -------------------------------------------------------------

func TestServiceOverview(t *testing.T) {
	st := NewMemStore()
	clk := &testClock{t: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	svc := newTestService(t, st, newFakeDeps(), clk)
	ctx := context.Background()
	nowMs := clk.Now().UnixMilli()

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true}),
			wfNode("kirim", "message", "Kirim", map[string]any{"text": "hai"}),
		},
		Edges: []Edge{wfEdge("start", "out", "kirim")},
	}
	seedWorkflow(t, st, Workflow{ID: "w1", Name: "Alpha", Guardian: "g"}, g)
	seedWorkflow(t, st, Workflow{ID: "w2", Name: "Beta", Guardian: "g", Status: StatusPaused}, g)

	ok1 := seedRunAt(t, st, Workflow{ID: "w1", Version: 1}, "manual", RunSucceeded, nowMs-1000, map[string]any{})
	seedRunAt(t, st, Workflow{ID: "w1", Version: 1}, "manual", RunFailed, nowMs-2000, map[string]any{})
	seedRunAt(t, st, Workflow{ID: "w2", Version: 1}, "manual", RunSucceeded, nowMs-3000, map[string]any{})

	if err := st.PutDecision(ctx, Decision{
		ID: "d_1", WorkflowID: "w1", RunID: ok1.ID, RunNumber: ok1.Number, NodeID: "kirim",
		Kind: "guardian", Action: "ask", Status: "pending", CreatedAt: nowMs,
	}); err != nil {
		t.Fatalf("put decision: %v", err)
	}

	overview, err := svc.Overview(ctx)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.Stats.Total != 2 || overview.Stats.Active != 1 || overview.Stats.Paused != 1 || overview.Stats.Error != 0 {
		t.Fatalf("stats=%+v", overview.Stats)
	}
	if overview.Stats.Decisions != 1 || overview.Stats.Approvals != 0 || overview.Stats.WaitingOnYou != 1 {
		t.Fatalf("attention stats=%+v", overview.Stats)
	}
	if overview.Stats.RunsToday != 3 || overview.Stats.RunsTodayOK != 2 || overview.Stats.RunsTodayFailed != 1 {
		t.Fatalf("runs today=%+v", overview.Stats)
	}
	if overview.Stats.Success7d == nil || *overview.Stats.Success7d < 0.66 || *overview.Stats.Success7d > 0.67 {
		t.Fatalf("success7d=%v want ~0.667", overview.Stats.Success7d)
	}
	if len(overview.Attention) != 1 {
		t.Fatalf("attention=%+v", overview.Attention)
	}
	if overview.Attention[0].WorkflowName != "Alpha" || overview.Attention[0].NodeName != "Kirim" {
		t.Fatalf("attention row=%+v", overview.Attention[0])
	}

	var alpha Summary
	for _, s := range overview.Workflows {
		if s.ID == "w1" {
			alpha = s
		}
	}
	if alpha.Runs7d != 2 || alpha.Success7d == nil || *alpha.Success7d != 0.5 {
		t.Fatalf("summary=%+v", alpha)
	}
	if len(alpha.Bots) != 1 || alpha.Bots[0] != "g" {
		t.Fatalf("bots=%v", alpha.Bots)
	}
}

// ---- StartRun concurrency -------------------------------------------------

func TestServiceStartRunActive(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.blockStarted = make(chan struct{}, 1)
	svc := newTestService(t, st, deps, &testClock{})
	ctx := context.Background()

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true}),
			wfNode("b", "bash", "Block", map[string]any{"command": "block"}),
		},
		Edges: []Edge{wfEdge("start", "out", "b")},
	}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	if _, err := svc.StartRun(ctx, "w", "manual", nil); err != nil {
		t.Fatalf("start run: %v", err)
	}
	<-deps.blockStarted

	if _, err := svc.StartRun(ctx, "w", "manual", nil); !errors.Is(err, ErrRunActive) {
		t.Fatalf("second start err=%v want ErrRunActive", err)
	}
}

// ---- durable wait resume --------------------------------------------------

func TestServiceWaitResumeOnTick(t *testing.T) {
	st := NewMemStore()
	clk := &testClock{t: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)}
	svc := newTestService(t, st, newFakeDeps(), clk)
	ctx := context.Background()

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true}),
			wfNode("w", "wait", "Wait", map[string]any{"seconds": 60}),
		},
		Edges: []Edge{wfEdge("start", "out", "w")},
	}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	run, err := svc.StartRun(ctx, "w", "manual", nil)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	svc.engine.Wait()
	if got := mustRun(t, st, run.ID); got.Status != RunWaiting || got.WaitingReason != "wait" {
		t.Fatalf("run=%+v want waiting/wait", got)
	}

	clk.advance(61 * time.Second)
	svc.tick(clk.Now())
	svc.engine.Wait()
	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v want succeeded after the wait expired", got)
	}
}

// ---- decision expiry ------------------------------------------------------

func TestServiceApprovalExpiry(t *testing.T) {
	st := NewMemStore()
	clk := &testClock{t: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)}
	svc := newTestService(t, st, newFakeDeps(), clk)
	ctx := context.Background()

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true}),
			wfNode("appr", "approval", "Approve", map[string]any{"message": "go?", "timeout_hours": 1}),
		},
		Edges: []Edge{wfEdge("start", "out", "appr")},
	}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	run, err := svc.StartRun(ctx, "w", "manual", nil)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	svc.engine.Wait()
	if got := mustRun(t, st, run.ID); got.Status != RunWaiting || got.WaitingReason != "approval" {
		t.Fatalf("run=%+v want waiting/approval", got)
	}
	dec := pendingDecision(t, st, run.ID)

	clk.advance(61 * time.Minute)
	svc.tick(clk.Now())
	svc.engine.Wait()

	current, err := st.GetDecision(ctx, dec.ID)
	if err != nil {
		t.Fatalf("get decision: %v", err)
	}
	if current.Status != "expired" || current.Choice != "deny" {
		t.Fatalf("decision=%+v want expired/deny", current)
	}
	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v want succeeded after a denied expiry", got)
	}
}

// ---- change stream --------------------------------------------------------

func TestServiceSubscribeChange(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})

	changes, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	if _, err := svc.Create(context.Background(), CreateInput{Name: "X", Guardian: "g"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	select {
	case change := <-changes:
		if change.WorkflowID != "x" {
			t.Fatalf("change=%+v", change)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no change received after create")
	}
}
