package lana

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

type probeTool struct{}

func (probeTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "write_probe", InputSchema: json.RawMessage("{\"type\":\"object\"}")}
}

func (probeTool) Risk(json.RawMessage) core.Risk { return core.RiskWrite }

func (probeTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "written"}, nil
}

func TestCliSessionLiveFramesAndApproval(t *testing.T) {
	use := "tool_use"
	end := "end_turn"
	text := "done"
	script := mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{ToolUse: &mock.ToolUse{ID: "c1", Name: "write_probe", Input: json.RawMessage("{}")}}, {Stop: &use}}},
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
	}}
	base := "[providers.mock]\nkind = \"openai_compat\"\nbase_url = \"mock://local\"\n\n[agents.assistant]\ntoolset = \"custom\"\nprovider = \"mock\"\nmodel = \"m\"\npermission = \"ask\"\ntools = [\"write_probe\"]\n\n[[routes]]\nchannel = \"cli\"\nagent = \"assistant\"\n"
	approved := 0
	rt, err := New(context.Background(), Options{
		Home:      t.TempDir(),
		Layers:    []config.Layer{{Name: "test", TOML: []byte(base)}},
		Tools:     []core.Tool{probeTool{}},
		Providers: map[string]core.Provider{"mock": mock.New(script)},
	}, ApproveWith(func(context.Context, core.ApprovalRequest) core.ApprovalOutcome {
		approved++
		return core.OutcomeAllowedOnce
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	ctx := context.Background()
	info, err := rt.CreateSession(ctx, "assistant")
	if err != nil {
		t.Fatal(err)
	}
	if info.Channel != "cli" {
		t.Fatalf("channel = %q", info.Channel)
	}
	policy, err := rt.Policy(ctx, info.ID)
	if err != nil || policy != core.PolicyAsk {
		t.Fatalf("policy = %q err=%v", policy, err)
	}
	if last, err := rt.LastSession(ctx); err != nil || last.ID != info.ID {
		t.Fatalf("last session = %+v err=%v", last, err)
	}
	sub, err := rt.SubscribeLive(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	result, err := rt.Run(ctx, Request{Agent: "assistant", Prompt: "write", SessionID: info.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "done" {
		t.Fatalf("text = %q", result.Text)
	}
	if approved != 1 {
		t.Fatalf("approval = %d", approved)
	}
	var kinds []string
	for sub.Len() > 0 {
		recvCtx, cancel := context.WithTimeout(ctx, time.Second)
		received, err := sub.Recv(recvCtx)
		cancel()
		if err != nil {
			break
		}
		if received.Value.Kind == "tool_result" && received.Value.Status != string(core.ToolStatusSuccess) {
			t.Fatalf("status tool = %q", received.Value.Status)
		}
		kinds = append(kinds, received.Value.Kind)
	}
	joined := strings.Join(kinds, ",")
	for _, want := range []string{"text_delta", "tool_call", "tool_result"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("frame %q missing: %s", want, joined)
		}
	}
	sessions, err := rt.ListSessions(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != info.ID {
		t.Fatalf("sessions = %+v", sessions)
	}
	forked, err := rt.ForkSession(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if forked.ParentSessionID != info.ID || forked.ID == info.ID {
		t.Fatalf("fork = %+v", forked)
	}
}
