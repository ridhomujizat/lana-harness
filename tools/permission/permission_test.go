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
	return callInputFor(sessionID, "coder", toolName, input)
}

func callInputFor(sessionID, agentName, toolName, input string) core.ToolsPreExecuteInput {
	risk := core.RiskWrite
	if toolName == "read_file" || toolName == "glob" || toolName == "grep" {
		risk = core.RiskRead
	}
	return core.ToolsPreExecuteInput{AgentName: agentName, Call: core.ToolCall{
		SessionID: sessionID, TurnID: "t", CallID: "c", Name: toolName, Input: json.RawMessage(input), Risk: risk,
	}}
}

func newChildSession(t *testing.T, s *testSetup, agent string) core.SessionInfo {
	t.Helper()
	info, err := s.store.Create(context.Background(), core.NewSession{
		Agent: agent, Channel: s.session.Channel, Address: s.session.Address,
		Workspace: s.session.Workspace, ParentSessionID: s.session.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return info
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
		t.Fatalf("path not rejected: %+v", arg)
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
		t.Fatalf("symlink not detected: %+v", arg)
	}
}

func TestWorkspaceNewFile(t *testing.T) {
	ws := t.TempDir()
	arg, err := ResolvePath(ws, "sub/new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !WithinWorkspace(ws, arg.Value) || !WithinWorkspace(ws, arg.Alt) {
		t.Fatalf("new path treated as outside: %+v", arg)
	}
}

func TestEvaluateOrder(t *testing.T) {
	s := setup(t, core.AgentConfig{
		Permission:    "ask",
		WorkspaceOnly: true,
		Rules:         core.AgentRules{Deny: []string{"bash(rm*)"}, Allow: []string{"bash(git*)"}},
	}, false)
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}

	if got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"rm -rf /"}`), nil); got.Allowed {
		t.Fatal("deny did not reject")
	}
	if got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"git status"}`), nil); !got.Allowed {
		t.Fatalf("allow did not permit: %+v", got)
	}
	write := callInput(s.session.ID, "write_file", `{"path":"a.txt","content":"x"}`)
	if got, _ := engine.Handle(context.Background(), write, nil); got.Allowed {
		t.Fatal("write without approval was allowed")
	}
	read := callInput(s.session.ID, "read_file", `{"path":"a.txt"}`)
	if got, _ := engine.Handle(context.Background(), read, nil); !got.Allowed {
		t.Fatalf("read must be allowed: %+v", got)
	}
	outside := callInput(s.session.ID, "read_file", `{"path":"../../etc/passwd"}`)
	if got, _ := engine.Handle(context.Background(), outside, nil); got.Allowed {
		t.Fatal("outside path allowed")
	}

	s2 := setup(t, core.AgentConfig{Permission: "trust"}, false)
	engine2 := &Engine{Store: s2.store, Config: s2.config}
	w := callInput(s2.session.ID, "write_file", `{"path":"a.txt","content":"x"}`)
	if got, _ := engine2.Handle(context.Background(), w, nil); !got.Allowed {
		t.Fatalf("trust rejected: %+v", got)
	}
	s3 := setup(t, core.AgentConfig{Permission: "never"}, false)
	engine3 := &Engine{Store: s3.store, Config: s3.config}
	w3 := callInput(s3.session.ID, "write_file", `{"path":"a.txt","content":"x"}`)
	if got, _ := engine3.Handle(context.Background(), w3, nil); got.Allowed {
		t.Fatal("never allowed")
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
		t.Fatalf("grant not used: %+v", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatal("approval still called")
	}
}

func TestGrantCoversWholeTool(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask"}, false)
	if _, err := s.store.Append(context.Background(), s.session.ID, []core.EventDraft{
		{Kind: core.KindToolCall, Payload: core.ToolCallPayload{V: 1, CallID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"cargo test"}`)}},
		{Kind: core.KindApprovalDecided, Payload: core.ApprovalDecided{V: 1, RequestID: "r1", CallID: "c1", Outcome: core.OutcomeAllowedSession}},
	}); err != nil {
		t.Fatal(err)
	}
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
	got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"cargo build"}`), nil)
	if !got.Allowed || got.Outcome != core.OutcomeAllowedSession {
		t.Fatalf("grant does not cover whole tool: %+v", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatal("approval still called")
	}
}

func TestGrantDoesNotBypassWorkspace(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask", WorkspaceOnly: true}, false)
	if _, err := s.store.Append(context.Background(), s.session.ID, []core.EventDraft{
		{Kind: core.KindToolCall, Payload: core.ToolCallPayload{V: 1, CallID: "c1", Name: "write_file", Input: json.RawMessage(`{"path":"a.txt","content":"x"}`)}},
		{Kind: core.KindApprovalDecided, Payload: core.ApprovalDecided{V: 1, RequestID: "r1", CallID: "c1", Outcome: core.OutcomeAllowedSession}},
	}); err != nil {
		t.Fatal(err)
	}
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
	got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "write_file", `{"path":"../outside.txt","content":"x"}`), nil)
	if got.Allowed {
		t.Fatalf("grant bypassed workspace: %+v", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatal("approval still called")
	}
}

func TestGrantDoesNotBypassDeny(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask", Rules: core.AgentRules{Deny: []string{"bash(rm*)"}}}, false)
	if _, err := s.store.Append(context.Background(), s.session.ID, []core.EventDraft{
		{Kind: core.KindToolCall, Payload: core.ToolCallPayload{V: 1, CallID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"cargo test"}`)}},
		{Kind: core.KindApprovalDecided, Payload: core.ApprovalDecided{V: 1, RequestID: "r1", CallID: "c1", Outcome: core.OutcomeAllowedSession}},
	}); err != nil {
		t.Fatal(err)
	}
	approval := &fakeApproval{outcome: core.OutcomeAllowedSession}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
	got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"rm -rf /"}`), nil)
	if got.Allowed {
		t.Fatalf("grant bypassed deny: %+v", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatal("approval still called")
	}
}

func TestGrantsInheritedFromFork(t *testing.T) {
	events := []core.SessionEvent{
		{Kind: core.KindToolCall, Payload: json.RawMessage(`{"v":1,"call_id":"c1","name":"write_file","input":{"path":"a"}}`)},
		{Kind: core.KindApprovalDecided, Payload: json.RawMessage(`{"v":1,"request_id":"r","call_id":"c1","outcome":"allowed_session"}`)},
	}
	grants := Grants(events)
	if len(grants) != 1 || grants[0].Tool != "write_file" {
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
		t.Fatal("initial ask allowed")
	}
	if _, err := s.store.Append(context.Background(), s.session.ID, []core.EventDraft{{Kind: core.KindApprovalPolicy, Payload: core.ApprovalPolicy{V: 1, Policy: core.PolicyTrust}}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "write_file", input), nil); !got.Allowed {
		t.Fatalf("new policy not applied: %+v", got)
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
		t.Fatalf("audit incomplete: %s %s", askedID, decidedID)
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
		t.Fatal("child ask must be stricter")
	}
	if ChildPolicy(core.PolicyNever, core.PolicyTrust) != core.PolicyNever {
		t.Fatal("never must win")
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

func TestMemoryRiskPolicies(t *testing.T) {
	memoryCall := func(sessionID string) core.ToolsPreExecuteInput {
		in := callInput(sessionID, "memory_write", `{"target":"memory","action":"add","content":"x"}`)
		in.Call.Risk = core.RiskMemory
		return in
	}
	for _, policy := range []string{"ask", "trust"} {
		s := setup(t, core.AgentConfig{Permission: policy}, false)
		approval := &fakeApproval{outcome: core.OutcomeRejected}
		engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
		if got, _ := engine.Handle(context.Background(), memoryCall(s.session.ID), nil); !got.Allowed {
			t.Fatalf("%s: memory_write rejected: %+v", policy, got)
		}
		if approval.calls.Load() != 0 {
			t.Fatalf("%s: approval was requested", policy)
		}
	}
	s := setup(t, core.AgentConfig{Permission: "never"}, false)
	engine := &Engine{Store: s.store, Config: s.config}
	if got, _ := engine.Handle(context.Background(), memoryCall(s.session.ID), nil); got.Allowed || got.Status != core.ToolStatusRejected {
		t.Fatalf("never: memory_write allowed: %+v", got)
	}
	s = setup(t, core.AgentConfig{Permission: "trust", Rules: core.AgentRules{Deny: []string{"memory_write"}}}, false)
	engine = &Engine{Store: s.store, Config: s.config}
	if got, _ := engine.Handle(context.Background(), memoryCall(s.session.ID), nil); got.Allowed {
		t.Fatal("deny rule did not reject memory_write")
	}
}

func TestSkillWriteNeverPolicy(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "never"}, false)
	engine := &Engine{Store: s.store, Config: s.config}
	in := callInput(s.session.ID, "skill_write", `{"name":"x","description":"Use x.","body":"Step 1."}`)
	in.Call.Risk = core.RiskWrite
	if got, _ := engine.Handle(context.Background(), in, nil); got.Allowed || got.Status != core.ToolStatusRejected {
		t.Fatalf("never: skill_write allowed: %+v", got)
	}
}

func TestMCPAllowRuleUnderAsk(t *testing.T) {
	mcpCall := func(sessionID string) core.ToolsPreExecuteInput {
		in := callInput(sessionID, "mcp__github__create_issue", `{"title":"x"}`)
		in.Call.Risk = core.RiskExec
		return in
	}

	s := setup(t, core.AgentConfig{Permission: "ask", Rules: core.AgentRules{Allow: []string{"mcp__github__*"}}}, false)
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
	if got, _ := engine.Handle(context.Background(), mcpCall(s.session.ID), nil); !got.Allowed {
		t.Fatalf("allow rule did not permit MCP call: %+v", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatal("approval was requested despite allow rule")
	}

	s = setup(t, core.AgentConfig{Permission: "ask"}, false)
	approval = &fakeApproval{outcome: core.OutcomeRejected}
	engine = &Engine{Store: s.store, Config: s.config, Approval: approval}
	if got, _ := engine.Handle(context.Background(), mcpCall(s.session.ID), nil); got.Allowed {
		t.Fatalf("MCP call allowed without rule: %+v", got)
	}
	if got := approval.calls.Load(); got != 1 {
		t.Fatalf("approval calls = %d, want 1", got)
	}
}

func TestChildPermissionIntersection(t *testing.T) {
	for _, tc := range []struct {
		name          string
		parent, child string
	}{
		{"parent trust child ask", "trust", "ask"},
		{"parent ask child trust", "ask", "trust"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := setup(t, core.AgentConfig{Permission: tc.parent}, false)
			s.config.Agents["researcher"] = core.AgentConfig{Permission: tc.child}
			child := newChildSession(t, s, "researcher")
			approval := &fakeApproval{outcome: core.OutcomeRejected}
			engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
			got, _ := engine.Handle(context.Background(), callInputFor(child.ID, "researcher", "bash", `{"command":"ls"}`), nil)
			if got.Allowed || approval.calls.Load() != 1 {
				t.Fatalf("got=%+v approval calls=%d, want an approval request", got, approval.calls.Load())
			}
		})
	}
}

func TestChildDenyUnionAndAllowNotInherited(t *testing.T) {
	t.Run("parent deny applies", func(t *testing.T) {
		s := setup(t, core.AgentConfig{Permission: "trust", Rules: core.AgentRules{Deny: []string{"bash(rm -rf*)"}}}, false)
		s.config.Agents["researcher"] = core.AgentConfig{Permission: "trust"}
		child := newChildSession(t, s, "researcher")
		engine := &Engine{Store: s.store, Config: s.config}
		got, _ := engine.Handle(context.Background(), callInputFor(child.ID, "researcher", "bash", `{"command":"rm -rf build"}`), nil)
		if got.Allowed || !strings.Contains(got.Reason, "deny rule") {
			t.Fatalf("got=%+v, want a deny rule rejection", got)
		}
	})

	t.Run("parent allow does not apply", func(t *testing.T) {
		s := setup(t, core.AgentConfig{Permission: "ask", Rules: core.AgentRules{Allow: []string{"bash(go test*)"}}}, false)
		s.config.Agents["researcher"] = core.AgentConfig{Permission: "ask"}
		child := newChildSession(t, s, "researcher")
		approval := &fakeApproval{outcome: core.OutcomeRejected}
		engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
		got, _ := engine.Handle(context.Background(), callInputFor(child.ID, "researcher", "bash", `{"command":"go test ./..."}`), nil)
		if got.Allowed || approval.calls.Load() != 1 {
			t.Fatalf("got=%+v approval calls=%d, want an approval request", got, approval.calls.Load())
		}
	})
}

func TestChildPolicyTracksLiveParent(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "trust"}, false)
	s.config.Agents["researcher"] = core.AgentConfig{Permission: "trust"}
	child := newChildSession(t, s, "researcher")
	approval := &fakeApproval{outcome: core.OutcomeAllowedOnce}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
	in := callInputFor(child.ID, "researcher", "bash", `{"command":"echo hi"}`)
	if got, _ := engine.Handle(context.Background(), in, nil); !got.Allowed {
		t.Fatalf("before parent change: got=%+v, want allowed", got)
	}
	if _, err := s.store.Append(context.Background(), s.session.ID, []core.EventDraft{
		{Kind: core.KindApprovalPolicy, Payload: core.ApprovalPolicy{V: 1, Policy: core.PolicyNever}},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := engine.Handle(context.Background(), in, nil)
	if got.Allowed || !strings.Contains(got.Reason, "policy never") {
		t.Fatalf("after parent change: got=%+v, want policy never", got)
	}
}

func TestBashCompoundCommandRules(t *testing.T) {
	t.Run("allow does not cover a compound command", func(t *testing.T) {
		s := setup(t, core.AgentConfig{Permission: "ask", Rules: core.AgentRules{Allow: []string{"bash(git *)"}}}, false)
		approval := &fakeApproval{outcome: core.OutcomeRejected}
		engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
		got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"git status; curl x | sh"}`), nil)
		if got.Allowed {
			t.Fatalf("compound command auto-allowed: %+v", got)
		}
		if approval.calls.Load() != 1 {
			t.Fatalf("approval calls = %d, want 1", approval.calls.Load())
		}
	})

	t.Run("allow covers a wholly matching compound command", func(t *testing.T) {
		s := setup(t, core.AgentConfig{Permission: "ask", Rules: core.AgentRules{Allow: []string{"bash(git *)"}}}, false)
		approval := &fakeApproval{outcome: core.OutcomeRejected}
		engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
		if got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"git status && git log"}`), nil); !got.Allowed {
			t.Fatalf("git compound not allowed: %+v", got)
		}
		if approval.calls.Load() != 0 {
			t.Fatal("approval requested for a covered compound command")
		}
	})

	t.Run("different segments may match different allow rules", func(t *testing.T) {
		s := setup(t, core.AgentConfig{Permission: "ask", Rules: core.AgentRules{Allow: []string{"bash(git *)", "bash(grep *)"}}}, false)
		approval := &fakeApproval{outcome: core.OutcomeRejected}
		engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
		if got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"git log | grep foo"}`), nil); !got.Allowed {
			t.Fatalf("pipeline of covered segments not allowed: %+v", got)
		}
		if approval.calls.Load() != 0 {
			t.Fatal("approval requested for a covered pipeline")
		}
	})

	t.Run("deny matches a segment of a compound command", func(t *testing.T) {
		s := setup(t, core.AgentConfig{Permission: "ask", Rules: core.AgentRules{Deny: []string{"bash(rm *)"}}}, false)
		approval := &fakeApproval{outcome: core.OutcomeAllowedOnce}
		engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
		got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"echo x && rm -rf y"}`), nil)
		if got.Allowed {
			t.Fatalf("deny missed a compound segment: %+v", got)
		}
		if approval.calls.Load() != 0 {
			t.Fatal("approval requested despite a deny rule")
		}
	})

	t.Run("unanalyzable commands are not auto-allowed", func(t *testing.T) {
		s := setup(t, core.AgentConfig{Permission: "ask", Rules: core.AgentRules{Allow: []string{"bash(git *)"}}}, false)
		approval := &fakeApproval{outcome: core.OutcomeRejected}
		engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
		if got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"git log $(whoami)"}`), nil); got.Allowed {
			t.Fatalf("substitution auto-allowed: %+v", got)
		}
		if got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"git log > out.txt"}`), nil); got.Allowed {
			t.Fatalf("redirection auto-allowed: %+v", got)
		}
		if approval.calls.Load() != 2 {
			t.Fatalf("approval calls = %d, want 2", approval.calls.Load())
		}
	})
}

func TestChildNeverPolicyBindsAllow(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "never"}, false)
	s.config.Agents["researcher"] = core.AgentConfig{Permission: "trust", Rules: core.AgentRules{Allow: []string{"bash(*)"}}}
	child := newChildSession(t, s, "researcher")
	approval := &fakeApproval{outcome: core.OutcomeAllowedOnce}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
	got, _ := engine.Handle(context.Background(), callInputFor(child.ID, "researcher", "bash", `{"command":"echo hi"}`), nil)
	if got.Allowed || !strings.Contains(got.Reason, "policy never") {
		t.Fatalf("got=%+v, want a policy never rejection", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatal("approval was requested despite a never policy")
	}
}

func TestChildDoesNotInheritParentGrant(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask"}, false)
	s.config.Agents["researcher"] = core.AgentConfig{Permission: "ask"}
	child := newChildSession(t, s, "researcher")
	if _, err := s.store.Append(context.Background(), s.session.ID, []core.EventDraft{
		{Kind: core.KindToolCall, Payload: core.ToolCallPayload{V: 1, CallID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"git status"}`)}},
		{Kind: core.KindApprovalDecided, Payload: core.ApprovalDecided{V: 1, RequestID: "r1", CallID: "c1", Outcome: core.OutcomeAllowedSession}},
	}); err != nil {
		t.Fatal(err)
	}
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
	got, _ := engine.Handle(context.Background(), callInputFor(child.ID, "researcher", "bash", `{"command":"git status"}`), nil)
	if got.Allowed || approval.calls.Load() != 1 {
		t.Fatalf("got=%+v approval calls=%d, want an approval request", got, approval.calls.Load())
	}
}

// TestWorkspaceOnlyOffAllowsOutsidePaths: with workspace_only off (the
// default), the permission engine no longer rejects an outside path. A read is
// allowed and a write under ask goes to approval.
func TestWorkspaceOnlyOffAllowsOutsidePaths(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask"}, false)
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}

	read := callInput(s.session.ID, "read_file", `{"path":"../../etc/passwd"}`)
	if got, _ := engine.Handle(context.Background(), read, nil); !got.Allowed {
		t.Fatalf("read outside (workspace_only off) got=%+v, want allowed", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatal("read outside requested approval")
	}

	write := callInput(s.session.ID, "write_file", `{"path":"../outside.txt","content":"x"}`)
	got, _ := engine.Handle(context.Background(), write, nil)
	if got.Allowed || strings.Contains(got.Reason, "outside workspace") {
		t.Fatalf("write outside got=%+v, want an approval request", got)
	}
	if approval.calls.Load() != 1 {
		t.Fatalf("write outside approval calls = %d, want 1", approval.calls.Load())
	}
}

// TestWorkspaceOnlyRejectsOutsidePaths: workspace_only keeps the outside
// rejection for both reads and writes.
func TestWorkspaceOnlyRejectsOutsidePaths(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask", WorkspaceOnly: true}, false)
	approval := &fakeApproval{outcome: core.OutcomeAllowedOnce}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}

	read := callInput(s.session.ID, "read_file", `{"path":"../../etc/passwd"}`)
	if got, _ := engine.Handle(context.Background(), read, nil); got.Allowed || !strings.Contains(got.Reason, "outside workspace") {
		t.Fatalf("read outside got=%+v, want outside workspace", got)
	}
	write := callInput(s.session.ID, "write_file", `{"path":"../outside.txt","content":"x"}`)
	if got, _ := engine.Handle(context.Background(), write, nil); got.Allowed || !strings.Contains(got.Reason, "outside workspace") {
		t.Fatalf("write outside got=%+v, want outside workspace", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatal("approval was requested for an outside path")
	}
}

// TestChildInheritsParentWorkspaceOnly: a child whose parent agent sets
// workspace_only is restricted even when the child's own agent leaves it off.
func TestChildInheritsParentWorkspaceOnly(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask", WorkspaceOnly: true}, false)
	s.config.Agents["researcher"] = core.AgentConfig{Permission: "ask"}
	child := newChildSession(t, s, "researcher")
	approval := &fakeApproval{outcome: core.OutcomeAllowedOnce}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
	read := callInputFor(child.ID, "researcher", "read_file", `{"path":"../../etc/passwd"}`)
	if got, _ := engine.Handle(context.Background(), read, nil); got.Allowed || !strings.Contains(got.Reason, "outside workspace") {
		t.Fatalf("child read outside got=%+v, want outside workspace", got)
	}
}

// TestPolicyNeverAllowRule covers AC-PERM-007 and AC-PERM-008: a session's own
// never policy still lets its allow rules run, and never with no allow rule
// rejects a write without approval.
func TestPolicyNeverAllowRule(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "never", Rules: core.AgentRules{Allow: []string{"bash(git status*)"}}}, false)
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
	got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"git status"}`), nil)
	if !got.Allowed {
		t.Fatalf("AC-PERM-007: git status not allowed: %+v", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatal("AC-PERM-007: approval was requested")
	}

	s2 := setup(t, core.AgentConfig{Permission: "never"}, false)
	approval2 := &fakeApproval{outcome: core.OutcomeAllowedOnce}
	engine2 := &Engine{Store: s2.store, Config: s2.config, Approval: approval2}
	got2, _ := engine2.Handle(context.Background(), callInput(s2.session.ID, "write_file", `{"path":"a.txt","content":"x"}`), nil)
	if got2.Allowed || !strings.Contains(got2.Reason, "policy never") {
		t.Fatalf("AC-PERM-008: got=%+v, want policy never", got2)
	}
	if approval2.calls.Load() != 0 {
		t.Fatal("AC-PERM-008: approval was requested")
	}
}
