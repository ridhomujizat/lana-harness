package lana

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

const teamConfig = `
[agents.lead]
provider = "local"
model = "lead"
permission = "trust"
subagents = ["explorer"]

[agents.researcher]
provider = "local"
model = "researcher"
description = "Finds sources"

[agents.writer]
provider = "local"
model = "writer"

[agents.explorer]
provider = "local"
model = "explorer"

[teams.research-squad]
description = "Research team"
lead = "lead"
members = ["researcher", "writer"]
`

func newTeamRuntime(t *testing.T, baseURL string) *Runtime {
	t.Helper()
	return newTestRuntime(t, baseURL, func(cfg string) string { return cfg + teamConfig })
}

func TestTeamNamesAndSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	rt := newTeamRuntime(t, srv.URL)
	ctx := context.Background()

	if got := rt.TeamNames(); len(got) != 1 || got[0] != "research-squad" {
		t.Fatalf("teams=%v", got)
	}
	if !rt.IsTeam("research-squad") || rt.IsTeam("lead") || rt.IsTeam("missing") {
		t.Fatal("IsTeam reported the wrong kind")
	}
	if got := rt.AgentPolicy("research-squad"); got != "trust" {
		t.Fatalf("policy=%q, want the lead's", got)
	}
	if got := rt.AgentModel("research-squad"); got != "local:lead" {
		t.Fatalf("model=%q, want the lead's", got)
	}
	if _, err := rt.routeAgent("research-squad"); err != nil {
		t.Fatalf("routeAgent(team): %v", err)
	}
	if _, err := rt.routeAgent("missing"); err == nil {
		t.Fatal("routeAgent accepted an unknown name")
	}
	info, err := rt.CreateSession(ctx, "research-squad")
	if err != nil {
		t.Fatalf("CreateSession(team): %v", err)
	}
	if info.Agent != "research-squad" {
		t.Fatalf("session agent=%q", info.Agent)
	}
}

func TestTeamSetAgentAndModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	rt := newTeamRuntime(t, srv.URL)
	ctx := context.Background()

	info, err := rt.CreateSession(ctx, "assistant")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.SetAgent(ctx, info.ID, "research-squad"); err != nil {
		t.Fatalf("SetAgent(team): %v", err)
	}
	var selection core.ModelSelection
	found := false
	events, err := rt.History(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind != core.KindModelSelection {
			continue
		}
		if err := json.Unmarshal(event.Payload, &selection); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found || selection.Agent != "research-squad" || selection.Provider != "local" || selection.Model != "lead" {
		t.Fatalf("selection=%+v", selection)
	}
	err = rt.SetModel(ctx, info.ID, "local:other")
	if !errors.Is(err, ErrTeamModel) || !errors.Is(err, ErrInvalidModel) {
		t.Fatalf("SetModel(team) err=%v", err)
	}
}
