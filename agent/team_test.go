package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// stubTeamTool registers the task spec without the T-004 implementation.
type stubTeamTool struct{ name string }

func (s stubTeamTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: s.name, InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (s stubTeamTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }
func (s stubTeamTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "ok"}, nil
}

func newTeamHarness(t *testing.T, script mock.Script) *harness {
	t.Helper()
	h := newHarness(t, script, map[string]core.AgentConfig{
		"assistant":  {Provider: "mock", Model: "m"},
		"lead":       {Provider: "mock", Model: "lead-model", Subagents: []string{"explorer"}},
		"researcher": {Provider: "member", Model: "member-model", Description: "Finds sources"},
		"writer":     {Provider: "member", Model: "member-model", Description: "Writes clearly"},
		"explorer":   {Provider: "member", Model: "member-model"},
	})
	h.reg.config.Teams = map[string]core.TeamConfig{
		"research-squad": {Description: "Research team", Lead: "lead", Members: []string{"researcher", "writer"}},
	}
	for _, name := range []string{"task", "task_continue"} {
		if err := h.tools.Register(stubTeamTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	info, err := h.store.Create(context.Background(), core.NewSession{Agent: "research-squad", Channel: "library", Workspace: h.ws})
	if err != nil {
		t.Fatal(err)
	}
	h.info = info
	return h
}

func TestTeamSessionUsesLeadProviderAndRoster(t *testing.T) {
	h := newTeamHarness(t, mock.Script{Responses: []mock.Response{textResponse("team answer")}})
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Err != nil || result.Text != "team answer" {
		t.Fatalf("result=%+v", result)
	}
	var step core.StepStart
	var system core.SystemMessage
	var haveStep, haveSystem bool
	for _, event := range h.events(t) {
		switch event.Kind {
		case core.KindStepStart:
			if err := json.Unmarshal(event.Payload, &step); err != nil {
				t.Fatal(err)
			}
			haveStep = true
		case core.KindSystemMessage:
			if err := json.Unmarshal(event.Payload, &system); err != nil {
				t.Fatal(err)
			}
			haveSystem = true
		}
	}
	if !haveStep || step.Provider != "mock" || step.Model != "lead-model" {
		t.Fatalf("step=%+v, want the lead's provider and model", step)
	}
	if !haveSystem {
		t.Fatal("system/message missing")
	}
	for _, want := range []string{"# Your team", "- researcher: Finds sources", "- writer: Writes clearly", "\n- explorer\n", "task_continue"} {
		if !strings.Contains(system.Text, want) {
			t.Fatalf("system prompt lacks %q:\n%s", want, system.Text)
		}
	}
}

func TestTeamResolveDelegationList(t *testing.T) {
	h := newTeamHarness(t, mock.Script{})
	rn := &Runner{reg: h.reg, sessionID: h.info.ID}
	resolved, err := rn.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.team != "research-squad" || resolved.name != "lead" {
		t.Fatalf("team=%q name=%q", resolved.team, resolved.name)
	}
	if got, want := strings.Join(resolved.delegates, ","), "researcher,writer,explorer"; got != want {
		t.Fatalf("delegates=%q want %q", got, want)
	}
	if len(resolved.roster) != 3 || resolved.roster[0].Description != "Finds sources" || resolved.roster[2].Description != "" {
		t.Fatalf("roster=%+v", resolved.roster)
	}
}

func TestTeamMergeDelegatesNoDuplicates(t *testing.T) {
	got := mergeDelegates([]string{"researcher", "writer"}, []string{"writer", "explorer"})
	if strings.Join(got, ",") != "researcher,writer,explorer" {
		t.Fatalf("delegates=%v", got)
	}
}

func TestTeamTurnTools(t *testing.T) {
	h := newTeamHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}})
	var names []string
	h.hooks.LLMStream.Use(func(ctx context.Context, in core.LLMStreamInput, next core.Next[core.LLMStreamInput, core.ChatStream]) (core.ChatStream, error) {
		for _, spec := range in.Request.Tools {
			names = append(names, spec.Name)
		}
		return next(ctx, in)
	})
	h.run(t, "hi")
	for _, want := range []string{"task", "task_continue"} {
		var count int
		for _, name := range names {
			if name == want {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("tool %q appears %d times in %v", want, count, names)
		}
	}
}

func TestTeamSetAgentEvent(t *testing.T) {
	h := newTeamHarness(t, mock.Script{})
	info, err := h.store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "library", Workspace: h.ws})
	if err != nil {
		t.Fatal(err)
	}
	h.info = info
	if err := h.reg.SetAgent(context.Background(), info.ID, "research-squad"); err != nil {
		t.Fatal(err)
	}
	var payload core.ModelSelection
	found := false
	for _, event := range h.events(t) {
		if event.Kind != core.KindModelSelection {
			continue
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found {
		t.Fatal("model/selection event missing")
	}
	if payload.Agent != "research-squad" || payload.Provider != "" || payload.Model != "" {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestTeamSetModelRefused(t *testing.T) {
	h := newTeamHarness(t, mock.Script{})
	before := len(h.events(t))
	err := h.reg.SetModel(context.Background(), h.info.ID, "mock:other")
	if !errors.Is(err, ErrTeamModel) || !errors.Is(err, ErrInvalidModel) {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(err.Error(), "teams use each agent's model") {
		t.Fatalf("err=%v", err)
	}
	if after := len(h.events(t)); after != before {
		t.Fatalf("event written: %d -> %d", before, after)
	}
}
