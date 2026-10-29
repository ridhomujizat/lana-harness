package permission

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/pattern"
	"github.com/ridhomujizat/lana-harness/store/memstore"
	"github.com/ridhomujizat/lana-harness/tools"
)

type testSetup struct {
	store   *memstore.Store
	session core.SessionInfo
	hooks   *core.Hooks
	config  core.Config
	ws      string
}

func setup(t *testing.T, agent core.AgentConfig, withPipeline bool) *testSetup {
	t.Helper()
	ws := t.TempDir()
	store := memstore.New()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "coder", Channel: "library", Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	hooks := core.NewHooks()
	hooks.ApprovalRequest.SetDefault(func(context.Context, core.ApprovalRequest) (core.ApprovalOutcome, error) {
		return core.OutcomeUnavailable, nil
	})
	if withPipeline {
		tools.NewPipeline(hooks)
	}
	return &testSetup{store: store, session: info, hooks: hooks, ws: ws, config: core.Config{Agents: map[string]core.AgentConfig{"coder": agent}}}
}

func callInput(sessionID, toolName, input string) core.ToolsPreExecuteInput {
	risk := core.RiskWrite
	if toolName == "read_file" || toolName == "glob" || toolName == "grep" {
		risk = core.RiskRead
	}
	return core.ToolsPreExecuteInput{AgentName: "coder", Call: core.ToolCall{
		SessionID: sessionID, TurnID: "t", CallID: "c", Name: toolName, Input: json.RawMessage(input), Risk: risk,
	}}
}

type fakeApproval struct {
	outcome core.ApprovalOutcome
	calls   atomic.Int32
}

func (f *fakeApproval) Request(context.Context, core.ApprovalRequest) (core.ApprovalOutcome, error) {
	f.calls.Add(1)
	return f.outcome, nil
}
func (f *fakeApproval) SetPolicy(context.Context, string, core.Policy) error { return nil }
func (f *fakeApproval) EffectivePolicy(context.Context, string) (core.Policy, error) {
	return core.PolicyAsk, nil
}

func TestWorkspaceDotDot(t *testing.T) {
	ws := t.TempDir()
	arg, err := ResolvePath(ws, "../x")
	if err != nil {
		t.Fatal(err)
	}
	if WithinWorkspace(ws, arg.Value) {
		t.Fatalf("path lolos: %+v", arg)
	}
}

func TestWorkspaceSymlinkEscape(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}
	arg, err := ResolvePath(ws, "link")
	if err != nil {
		t.Fatal(err)
	}
	if WithinWorkspace(ws, arg.Alt) {
		t.Fatalf("symlink tidak terdeteksi: %+v", arg)
	}
}

func TestWorkspaceNewFile(t *testing.T) {
	ws := t.TempDir()
	arg, err := ResolvePath(ws, "sub/new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !WithinWorkspace(ws, arg.Value) || !WithinWorkspace(ws, arg.Alt) {
		t.Fatalf("path baru dianggap luar: %+v", arg)
	}
}

func TestEvaluateOrder(t *testing.T) {
	s := setup(t, core.AgentConfig{
		Permission: "ask",
		Rules:      core.AgentRules{Deny: []string{"bash(rm*)"}, Allow: []string{"bash(git*)"}},
	}, false)
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}

	if got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"rm -rf /"}`), nil); got.Allowed {
		t.Fatal("deny tidak menolak")
	}
	if got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"git status"}`), nil); !got.Allowed {
		t.Fatalf("allow tidak mengizinkan: %+v", got)
	}
	write := callInput(s.session.ID, "write_file", `{"path":"a.txt","content":"x"}`)
	if got, _ := engine.Handle(context.Background(), write, nil); got.Allowed {
		t.Fatal("write tanpa izin diizinkan")
	}
	read := callInput(s.session.ID, "read_file", `{"path":"a.txt"}`)
	if got, _ := engine.Handle(context.Background(), read, nil); !got.Allowed {
		t.Fatalf("read harus diizinkan: %+v", got)
	}
	outside := callInput(s.session.ID, "read_file", `{"path":"../../etc/passwd"}`)
	if got, _ := engine.Handle(context.Background(), outside, nil); got.Allowed {
		t.Fatal("path luar diizinkan")
	}

	s2 := setup(t, core.AgentConfig{Permission: "trust"}, false)
	engine2 := &Engine{Store: s2.store, Config: s2.config}
	w := callInput(s2.session.ID, "write_file", `{"path":"a.txt","content":"x"}`)
	if got, _ := engine2.Handle(context.Background(), w, nil); !got.Allowed {
		t.Fatalf("trust menolak: %+v", got)
	}
	s3 := setup(t, core.AgentConfig{Permission: "never"}, false)
	engine3 := &Engine{Store: s3.store, Config: s3.config}
	w3 := callInput(s3.session.ID, "write_file", `{"path":"a.txt","content":"x"}`)
	if got, _ := engine3.Handle(context.Background(), w3, nil); got.Allowed {
		t.Fatal("never mengizinkan")
	}
}

func TestRejectionDecisionShape(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "never"}, false)
	engine := &Engine{Store: s.store, Config: s.config}
	got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "write_file", `{"path":"a.txt","content":"x"}`), nil)
	if got.Allowed || got.Status != core.ToolStatusRejected || got.Reason == "" {
		t.Fatalf("got=%+v", got)
	}
}

func TestGrantReused(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask"}, false)
	input := `{"path":"a.txt","content":"x"}`
	if _, err := s.store.Append(context.Background(), s.session.ID, []core.EventDraft{
		{Kind: core.KindToolCall, Payload: core.ToolCallPayload{V: 1, CallID: "c1", Name: "write_file", Input: json.RawMessage(input)}},
		{Kind: core.KindApprovalDecided, Payload: core.ApprovalDecided{V: 1, RequestID: "r1", CallID: "c1", Outcome: core.OutcomeAllowedSession}},
	}); err != nil {
		t.Fatal(err)
	}
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
	got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "write_file", input), nil)
	if !got.Allowed || got.Outcome != core.OutcomeAllowedSession {
		t.Fatalf("grant tidak dipakai: %+v", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatal("approval tetap dipanggil")
	}
}

func TestGrantArgsMismatch(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask"}, false)
	if _, err := s.store.Append(context.Background(), s.session.ID, []core.EventDraft{
		{Kind: core.KindToolCall, Payload: core.ToolCallPayload{V: 1, CallID: "c1", Name: "write_file", Input: json.RawMessage(`{"path":"a.txt","content":"x"}`)}},
		{Kind: core.KindApprovalDecided, Payload: core.ApprovalDecided{V: 1, RequestID: "r1", CallID: "c1", Outcome: core.OutcomeAllowedSession}},
	}); err != nil {
		t.Fatal(err)
	}
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
	got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "write_file", `{"path":"b.txt","content":"x"}`), nil)
	if got.Allowed || approval.calls.Load() != 1 {
		t.Fatalf("got=%+v calls=%d", got, approval.calls.Load())
	}
}

func TestGrantsInheritedFromFork(t *testing.T) {
	events := []core.SessionEvent{
		{Kind: core.KindToolCall, Payload: json.RawMessage(`{"v":1,"call_id":"c1","name":"write_file","input":{"path":"a"}}`)},
		{Kind: core.KindApprovalDecided, Payload: json.RawMessage(`{"v":1,"request_id":"r","call_id":"c1","outcome":"allowed_session"}`)},
	}
	grants := Grants(events)
	if len(grants) != 1 || grants[0].Tool != "write_file" || grants[0].Arg != "a" {
		t.Fatalf("grants=%+v", grants)
	}
}

func TestEffectivePolicyDefaults(t *testing.T) {
	if got := EffectivePolicy(nil, core.PolicyTrust); got != core.PolicyTrust {
		t.Fatalf("got=%q", got)
	}
}

func TestSetPolicyLastWins(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask"}, false)
	service := NewApprovalService(s.store, s.hooks, s.config)
	if err := service.SetPolicy(context.Background(), s.session.ID, core.PolicyTrust); err != nil {
		t.Fatal(err)
	}
	if err := service.SetPolicy(context.Background(), s.session.ID, core.PolicyNever); err != nil {
		t.Fatal(err)
	}
	got, err := service.EffectivePolicy(context.Background(), s.session.ID)
	if err != nil || got != core.PolicyNever {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestSetPolicyInvalid(t *testing.T) {
	s := setup(t, core.AgentConfig{}, false)
	service := NewApprovalService(s.store, s.hooks, s.config)
	if err := service.SetPolicy(context.Background(), s.session.ID, core.Policy("x")); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("err=%v", err)
	}
}

func TestPolicyChangeMidTurn(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask"}, false)
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
	input := `{"path":"a.txt","content":"x"}`
	if got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "write_file", input), nil); got.Allowed {
		t.Fatal("ask awal diizinkan")
	}
	if _, err := s.store.Append(context.Background(), s.session.ID, []core.EventDraft{{Kind: core.KindApprovalPolicy, Payload: core.ApprovalPolicy{V: 1, Policy: core.PolicyTrust}}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "write_file", input), nil); !got.Allowed {
		t.Fatalf("policy baru tidak dipakai: %+v", got)
	}
}

func TestApprovalAuditPair(t *testing.T) {
	s := setup(t, core.AgentConfig{}, false)
	s.hooks.ApprovalRequest.SetDefault(func(context.Context, core.ApprovalRequest) (core.ApprovalOutcome, error) {
		return core.OutcomeAllowedOnce, nil
	})
	service := NewApprovalService(s.store, s.hooks, s.config)
	outcome, err := service.Request(context.Background(), core.ApprovalRequest{SessionID: s.session.ID, TurnID: "t", CallID: "c1", ToolName: "write_file", Risk: core.RiskWrite})
	if err != nil || outcome != core.OutcomeAllowedOnce {
		t.Fatalf("outcome=%q err=%v", outcome, err)
	}
	events, _ := s.store.ChainEvents(context.Background(), s.session.ID)
	var asked, decided bool
	var askedID, decidedID string
	for _, event := range events {
		if event.Kind == core.KindApprovalAsked {
			asked = true
			var p core.ApprovalAsked
			_ = json.Unmarshal(event.Payload, &p)
			askedID = p.RequestID
		}
		if event.Kind == core.KindApprovalDecided {
			decided = true
			var p core.ApprovalDecided
			_ = json.Unmarshal(event.Payload, &p)
			decidedID = p.RequestID
		}
	}
	if !asked || !decided || askedID == "" || askedID != decidedID {
		t.Fatalf("audit tidak lengkap: %s %s", askedID, decidedID)
	}
}

func TestNoAnswererUnavailable(t *testing.T) {
	s := setup(t, core.AgentConfig{}, false)
	s.hooks.ApprovalRequest.SetDefault(nil)
	service := NewApprovalService(s.store, s.hooks, s.config)
	outcome, _ := service.Request(context.Background(), core.ApprovalRequest{SessionID: s.session.ID, CallID: "c"})
	if outcome != core.OutcomeUnavailable {
		t.Fatalf("outcome=%q", outcome)
	}
}

func TestAnswererErrorUnavailable(t *testing.T) {
	s := setup(t, core.AgentConfig{}, false)
	s.hooks.ApprovalRequest.Use(func(context.Context, core.ApprovalRequest, core.Next[core.ApprovalRequest, core.ApprovalOutcome]) (core.ApprovalOutcome, error) {
		return "", errors.New("boom")
	})
	service := NewApprovalService(s.store, s.hooks, s.config)
	outcome, _ := service.Request(context.Background(), core.ApprovalRequest{SessionID: s.session.ID, CallID: "c"})
	if outcome != core.OutcomeUnavailable {
		t.Fatalf("outcome=%q", outcome)
	}
}

func TestInvalidOutcomeUnavailable(t *testing.T) {
	s := setup(t, core.AgentConfig{}, false)
	s.hooks.ApprovalRequest.SetDefault(func(context.Context, core.ApprovalRequest) (core.ApprovalOutcome, error) {
		return core.ApprovalOutcome("bogus"), nil
	})
	service := NewApprovalService(s.store, s.hooks, s.config)
	outcome, _ := service.Request(context.Background(), core.ApprovalRequest{SessionID: s.session.ID, CallID: "c"})
	if outcome != core.OutcomeUnavailable {
		t.Fatalf("outcome=%q", outcome)
	}
}

func TestWaterfallFirstOwner(t *testing.T) {
	s := setup(t, core.AgentConfig{}, false)
	var second atomic.Int32
	s.hooks.ApprovalRequest.Use(func(context.Context, core.ApprovalRequest, core.Next[core.ApprovalRequest, core.ApprovalOutcome]) (core.ApprovalOutcome, error) {
		return core.OutcomeAllowedOnce, nil
	})
	s.hooks.ApprovalRequest.Use(func(context.Context, core.ApprovalRequest, core.Next[core.ApprovalRequest, core.ApprovalOutcome]) (core.ApprovalOutcome, error) {
		second.Add(1)
		return core.OutcomeRejected, nil
	})
	service := NewApprovalService(s.store, s.hooks, s.config)
	if outcome, _ := service.Request(context.Background(), core.ApprovalRequest{SessionID: s.session.ID, CallID: "c"}); outcome != core.OutcomeAllowedOnce || second.Load() != 0 {
		t.Fatalf("outcome=%q second=%d", outcome, second.Load())
	}
}

func TestApprovalTimeout10Min(t *testing.T) {
	s := setup(t, core.AgentConfig{}, false)
	s.hooks.ApprovalRequest.SetDefault(func(ctx context.Context, _ core.ApprovalRequest) (core.ApprovalOutcome, error) {
		<-ctx.Done()
		return core.OutcomeUnavailable, ctx.Err()
	})
	service := NewApprovalService(s.store, s.hooks, s.config)
	service.Timeout = 20 * time.Millisecond
	outcome, _ := service.Request(context.Background(), core.ApprovalRequest{SessionID: s.session.ID, CallID: "c"})
	if outcome != core.OutcomeUnavailable {
		t.Fatalf("outcome=%q", outcome)
	}
	var decided core.ApprovalDecided
	events, _ := s.store.ChainEvents(context.Background(), s.session.ID)
	for _, event := range events {
		if event.Kind == core.KindApprovalDecided {
			_ = json.Unmarshal(event.Payload, &decided)
		}
	}
	if decided.Reason != "timeout" {
		t.Fatalf("reason=%q", decided.Reason)
	}
}

func TestApprovalCancelled(t *testing.T) {
	s := setup(t, core.AgentConfig{}, false)
	s.hooks.ApprovalRequest.SetDefault(func(ctx context.Context, _ core.ApprovalRequest) (core.ApprovalOutcome, error) {
		<-ctx.Done()
		return core.OutcomeUnavailable, ctx.Err()
	})
	service := NewApprovalService(s.store, s.hooks, s.config)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	outcome, _ := service.Request(ctx, core.ApprovalRequest{SessionID: s.session.ID, CallID: "c"})
	if outcome != core.OutcomeCancelled {
		t.Fatalf("outcome=%q", outcome)
	}
}

func TestPermissionListenerReplacesDefault(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "trust"}, true)
	engine := &Engine{Store: s.store, Config: s.config}
	s.hooks.ToolsPreExecute.Use(engine.Handle)
	got, err := s.hooks.ToolsPreExecute.Call(context.Background(), callInput(s.session.ID, "write_file", `{"path":"a","content":"x"}`))
	if err != nil || !got.Allowed {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestNoPermissionModuleFailsClosed(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "trust"}, true)
	got, err := s.hooks.ToolsPreExecute.Call(context.Background(), callInput(s.session.ID, "write_file", `{"path":"a","content":"x"}`))
	if err != nil || got.Allowed || !strings.Contains(got.Reason, "default fail-closed") {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestChildPolicy(t *testing.T) {
	if ChildPolicy(core.PolicyTrust, core.PolicyAsk) != core.PolicyAsk {
		t.Fatal("child ask harus lebih ketat")
	}
	if ChildPolicy(core.PolicyNever, core.PolicyTrust) != core.PolicyNever {
		t.Fatal("never harus menang")
	}
}

func TestChildInheritsDeny(t *testing.T) {
	p1, _ := pattern.ParsePattern("bash(rm*)")
	p2, _ := pattern.ParsePattern("read_file(*)")
	got := MergeDeny([]pattern.Pattern{p1}, []pattern.Pattern{p2})
	if len(got) != 2 {
		t.Fatalf("got=%v", got)
	}
}
