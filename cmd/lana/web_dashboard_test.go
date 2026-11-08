package main

import (
	"context"
	"log/slog"
	"reflect"
	"testing"
	"time"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/logring"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// TestWebBackendDashboard covers the Dashboard adapter of API-DSH-001: the
// range fields, the daemon start time, the live counts, the event path, and
// the daemon log rows from the buffer. It also covers the agent filter of
// REQ-DSH-011 / API-DSH-001 r2.
func TestWebBackendDashboard(t *testing.T) {
	text, end := "hello", "end_turn"
	rt, home := webRuntime(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
	}})
	ctx := context.Background()
	info, err := rt.CreateChannelSession(ctx, "web", "owner", "assistant")
	if err != nil {
		t.Fatalf("CreateChannelSession: %v", err)
	}
	if _, err := rt.Run(ctx, lana.Request{Prompt: "hello", SessionID: info.ID}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	leases := newLeaseSet(rt)
	t.Cleanup(leases.releaseAll)
	backend := newWebBackend(rt, home, leases, ctx)
	buffer := logring.New(10)
	backend.SetLogBuffer(buffer)
	buffer.Add(logring.Record{At: time.Now().UnixMilli(), Level: slog.LevelWarn, Message: "mcp server exited", Attrs: "name=github"})

	before := time.Now()
	dashboard, err := backend.Dashboard(ctx, "24h", "")
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if dashboard.Range != "24h" || dashboard.BucketMS != 3_600_000 {
		t.Fatalf("range = %q, bucket = %d", dashboard.Range, dashboard.BucketMS)
	}
	if dashboard.Agent != "" {
		t.Fatalf("agent = %q, want empty", dashboard.Agent)
	}
	wantAgents := []string{"assistant"}
	if !reflect.DeepEqual(dashboard.Agents, wantAgents) {
		t.Fatalf("agents = %+v, want %+v", dashboard.Agents, wantAgents)
	}
	if dashboard.To < before.UnixMilli() || dashboard.From != dashboard.To-24*60*60*1000 {
		t.Fatalf("window = from %d to %d", dashboard.From, dashboard.To)
	}
	if dashboard.DaemonStartedAt == 0 {
		t.Fatal("daemon_started_at is zero")
	}
	if dashboard.Now.SessionsRunning != 0 || dashboard.Now.SessionsWaitingInput != 0 {
		t.Fatalf("now = %+v, want idle counts", dashboard.Now)
	}
	if dashboard.Summary.Turns != 1 {
		t.Fatalf("turns = %d, want 1", dashboard.Summary.Turns)
	}
	found := false
	for _, row := range dashboard.Errors {
		if row.Source == "daemon" && row.Message == "mcp server exited name=github" {
			found = true
		}
	}
	if !found {
		t.Fatalf("daemon log row missing: %+v", dashboard.Errors)
	}

	filtered, err := backend.Dashboard(ctx, "24h", "assistant")
	if err != nil {
		t.Fatalf("Dashboard assistant: %v", err)
	}
	if filtered.Agent != "assistant" {
		t.Fatalf("filtered agent = %q, want assistant", filtered.Agent)
	}
	if !reflect.DeepEqual(filtered.Agents, wantAgents) {
		t.Fatalf("filtered agents = %+v, want %+v", filtered.Agents, wantAgents)
	}
	if filtered.Summary.Turns != 1 {
		t.Fatalf("filtered turns = %d, want 1", filtered.Summary.Turns)
	}
	for _, row := range filtered.Errors {
		if row.Source == "daemon" {
			t.Fatalf("filtered dashboard has a daemon row: %+v", row)
		}
	}

	missing, err := backend.Dashboard(ctx, "24h", "ghost")
	if err != nil {
		t.Fatalf("Dashboard ghost: %v", err)
	}
	if missing.Agent != "ghost" || missing.Summary.Turns != 0 || len(missing.Errors) != 0 {
		t.Fatalf("missing-agent dashboard = %+v", missing)
	}
	if !reflect.DeepEqual(missing.Agents, wantAgents) {
		t.Fatalf("missing-agent agents = %+v, want %+v", missing.Agents, wantAgents)
	}

	if _, err := backend.Dashboard(ctx, "1h", ""); err == nil {
		t.Fatal("Dashboard accepted the bad range 1h")
	}
}

// TestWebBackendDashboardAgentsIncludeConfigured covers API-DSH-001 r2: the
// agents list holds every configured agent even when it has no events in the
// range, plus every other agent that does.
func TestWebBackendDashboardAgentsIncludeConfigured(t *testing.T) {
	text, end := "hello", "end_turn"
	rt, home := webRuntime(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
	}})
	ctx := context.Background()

	// Run one turn as transient while it is configured, so the range holds its
	// events. It becomes the event-only agent after the next config apply.
	agentCfg := core.AgentConfig{Provider: "mock", Model: "m", Permission: "trust"}
	cfg := core.Config{Agents: map[string]core.AgentConfig{"assistant": agentCfg, "transient": agentCfg}}
	if err := rt.ApplyConfig(cfg); err != nil {
		t.Fatal(err)
	}

	info, err := rt.CreateChannelSession(ctx, "web", "owner", "transient")
	if err != nil {
		t.Fatalf("CreateChannelSession: %v", err)
	}
	if _, err := rt.Run(ctx, lana.Request{Prompt: "hello", SessionID: info.ID}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// default is configured but has no events; transient is no longer
	// configured but has events in the range.
	cfg.Agents = map[string]core.AgentConfig{"assistant": agentCfg, "default": agentCfg}
	if err := rt.ApplyConfig(cfg); err != nil {
		t.Fatal(err)
	}

	leases := newLeaseSet(rt)
	t.Cleanup(leases.releaseAll)
	backend := newWebBackend(rt, home, leases, ctx)

	want := []string{"assistant", "default", "transient"}
	dashboard, err := backend.Dashboard(ctx, "24h", "")
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if !reflect.DeepEqual(dashboard.Agents, want) {
		t.Fatalf("agents = %+v, want %+v", dashboard.Agents, want)
	}

	// The agents list ignores the filter, so a no-data configured filter still
	// reports every configured and seen agent.
	filtered, err := backend.Dashboard(ctx, "24h", "default")
	if err != nil {
		t.Fatalf("Dashboard default: %v", err)
	}
	if !reflect.DeepEqual(filtered.Agents, want) {
		t.Fatalf("filtered agents = %+v, want %+v", filtered.Agents, want)
	}
}
