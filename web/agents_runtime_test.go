package web

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// putAgentJSON sends PUT /v1/agents/{name} with fields and the file's current
// version, and returns the status and body.
func putAgentJSON(t *testing.T, s *Server, store *testConfigStore, cookie *http.Cookie, csrf, name string, fields map[string]any) (int, string) {
	t.Helper()
	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"expected_version": shaHex(raw)}
	for key, value := range fields {
		body[key] = value
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/"+name, string(payload), cookie, csrf))
	return rr.Code, rr.Body.String()
}

// AC-CCR-010 (backend side), API-CCR-004: creating a Claude Agent with model
// opus writes runtime = "claude-code" and model = "opus" with no provider,
// template tools, or persona, and its row shows the engine label.
func TestAgentsCreateClaudeAgentCoversACCCR010(t *testing.T) {
	s, store, cookie, csrf := newT016AgentsServer(t, t016AgentsConfig)

	rr := serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents",
		`{"name":"claude","runtime":"claude-code","model":"opus","auth":"subscription","permission":"ask","allow_scheduled":false,"reasoning_effort":"xhigh","title":"Pair","rules":{"deny":["bash(rm *)"]}}`, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %q", rr.Code, rr.Body.String())
	}
	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	tables := t016ReadAgentTables(t, store.path)
	table := tables["claude"]
	if table["runtime"] != "claude-code" || table["model"] != "opus" || table["reasoning_effort"] != "xhigh" {
		t.Fatalf("claude table = %+v\n%s", table, raw)
	}
	for _, key := range []string{"provider", "tools", "fallback", "allow_scheduled"} {
		if _, ok := table[key]; ok {
			t.Fatalf("claude table has %s: %+v", key, table)
		}
	}
	if table["auth"] != "subscription" || table["permission"] != "ask" || table["title"] != "Pair" {
		t.Fatalf("claude table = %+v", table)
	}
	if _, err := os.Stat(agenthome.Paths(store.home, "claude").Agent); !os.IsNotExist(err) {
		t.Fatalf("a Claude Agent got a persona file: %v", err)
	}

	row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "claude")
	if row.Runtime != "claude-code" || row.EngineLabel != "Claude Agent" || row.Auth != "subscription" ||
		row.AllowScheduled == nil || *row.AllowScheduled || row.Provider != "" || row.Model == nil || *row.Model != "opus" {
		t.Fatalf("claude row = %+v", row)
	}
	coder := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder")
	if coder.Runtime != "" || coder.EngineLabel != "lana" || coder.Auth != "" || coder.AllowScheduled != nil {
		t.Fatalf("coder row = %+v", coder)
	}
	list := serveAuth(s, settingsRequest(http.MethodGet, "/v1/agents", "", cookie, ""))
	if !strings.Contains(list.Body.String(), `"engine_label":"Claude Agent"`) || !strings.Contains(list.Body.String(), `"runtime":"claude-code"`) {
		t.Fatalf("list body = %s", list.Body.String())
	}

	// A full model ID is free-form; no provider or address check applies.
	rr = serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents",
		`{"name":"claude-2","runtime":"claude-code","model":"claude-opus-4-1","auth":"api_key","api_key_env":"ANTHROPIC_API_KEY","binary":"/opt/claude","allow_scheduled":true}`, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create claude-2 status = %d, body = %q", rr.Code, rr.Body.String())
	}
	table = t016ReadAgentTables(t, store.path)["claude-2"]
	if table["model"] != "claude-opus-4-1" || table["auth"] != "api_key" || table["api_key_env"] != "ANTHROPIC_API_KEY" ||
		table["binary"] != "/opt/claude" || table["allow_scheduled"] != true {
		t.Fatalf("claude-2 table = %+v", table)
	}
}

// API-CCR-004: a Claude Agent create rejects a template and a lana agent
// create rejects the runtime-only keys; a lana effort is rejected for a
// Claude Agent.
func TestAgentsCreateClaudeAgentRejects(t *testing.T) {
	s, _, cookie, csrf := newT016AgentsServer(t, t016AgentsConfig)
	for _, tc := range []struct {
		body string
		key  string
	}{
		{`{"name":"a","runtime":"claude-code","template":"coder"}`, "template"},
		{`{"name":"b","template":"coder","auth":"api_key"}`, "auth"},
		{`{"name":"c","runtime":"claude-code","reasoning_effort":"off"}`, "agents.c.reasoning_effort"},
		{`{"name":"d","runtime":"claude-code","auth":"api_key"}`, "agents.d.api_key_env"},
		{`{"name":"e","runtime":"nope"}`, "agents.e.runtime"},
	} {
		rr := serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents", tc.body, cookie, csrf))
		if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), `"key":"`+tc.key+`"`) {
			t.Fatalf("%s = %d %s, want 422 at %s", tc.body, rr.Code, rr.Body.String(), tc.key)
		}
	}
}

// API-CCR-004: switching a lana agent to Claude Agent removes the lana-only
// keys; switching back removes the runtime keys. Each write passes config
// validation.
func TestAgentsSwitchRuntimeRoundTrip(t *testing.T) {
	const config = `version = 1

[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "coder"
fallback = ["mock:other"]
tools = ["read_file", "bash"]
disabled_tools = ["bash"]
skills = ["review"]
max_steps = 9
context_window = 64000
compaction_threshold = 0.7
memory_capture = false
vision = true
reasoning_effort = "off"
permission = "ask"
workspace = "/tmp/coder"
description = "Builds things"
title = "Builder"
rules = { deny = ["bash(rm *)"] }

[[routes]]
channel = "cli"
agent = "coder"
`
	s, store, cookie, csrf := newT016AgentsServer(t, config)

	// The body the dashboard sends on the switch: runtime, the cleared
	// provider and fallback, and the new model.
	code, body := putAgentJSON(t, s, store, cookie, csrf, "coder", map[string]any{
		"runtime": "claude-code", "provider": "", "fallback": []string{}, "model": "sonnet",
	})
	if code != http.StatusOK {
		t.Fatalf("switch to claude = %d %s", code, body)
	}
	table := t016ReadAgentTables(t, store.path)["coder"]
	if table["runtime"] != "claude-code" || table["model"] != "sonnet" {
		t.Fatalf("claude table = %+v", table)
	}
	for _, key := range lanaOnlyAgentKeys {
		if _, ok := table[key]; ok {
			t.Fatalf("claude table keeps %s: %+v", key, table)
		}
	}
	if _, ok := table["reasoning_effort"]; ok {
		t.Fatalf("lana effort carried over: %+v", table)
	}
	if table["permission"] != "ask" || table["workspace"] != "/tmp/coder" || table["description"] != "Builds things" || table["title"] != "Builder" {
		t.Fatalf("shared keys lost: %+v", table)
	}
	if rules, _ := table["rules"].(map[string]any); rules == nil {
		t.Fatalf("rules lost: %+v", table)
	}
	row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder")
	if row.EngineLabel != "Claude Agent" || row.Provider != "" {
		t.Fatalf("row = %+v", row)
	}

	// Claude Agent edits keep the runtime keys.
	code, body = putAgentJSON(t, s, store, cookie, csrf, "coder", map[string]any{
		"auth": "api_key", "api_key_env": "ANTHROPIC_API_KEY", "binary": "/opt/claude", "allow_scheduled": true, "reasoning_effort": "medium",
	})
	if code != http.StatusOK {
		t.Fatalf("edit claude = %d %s", code, body)
	}
	table = t016ReadAgentTables(t, store.path)["coder"]
	if table["auth"] != "api_key" || table["binary"] != "/opt/claude" || table["allow_scheduled"] != true || table["reasoning_effort"] != "medium" || table["runtime"] != "claude-code" {
		t.Fatalf("claude edit table = %+v", table)
	}

	// A lana-only key on a Claude Agent is a validation error, not dropped.
	code, body = putAgentJSON(t, s, store, cookie, csrf, "coder", map[string]any{"tools": []string{"bash"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "agents.coder.tools") {
		t.Fatalf("tools on claude = %d %s", code, body)
	}

	// Back to lana: the runtime keys go; the new model is a router address.
	code, body = putAgentJSON(t, s, store, cookie, csrf, "coder", map[string]any{"runtime": "", "model": "mock/coder"})
	if code != http.StatusOK {
		t.Fatalf("switch to lana = %d %s", code, body)
	}
	table = t016ReadAgentTables(t, store.path)["coder"]
	for _, key := range append([]string{"runtime", "reasoning_effort"}, runtimeOnlyAgentKeys...) {
		if _, ok := table[key]; ok {
			t.Fatalf("lana table keeps %s: %+v", key, table)
		}
	}
	if table["model"] != "mock/coder" || table["permission"] != "ask" || table["title"] != "Builder" {
		t.Fatalf("lana table = %+v", table)
	}
	row = t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder")
	if row.Runtime != "" || row.EngineLabel != "lana" || row.Provider != "mock" {
		t.Fatalf("row = %+v", row)
	}
}

// Duplicating a Claude Agent keeps its runtime keys.
func TestAgentsDuplicateClaudeAgent(t *testing.T) {
	s, store, cookie, csrf := newT016AgentsServer(t, t016AgentsConfig+`
[agents.claude]
runtime = "claude-code"
model = "opus"
auth = "api_key"
api_key_env = "ANTHROPIC_API_KEY"
allow_scheduled = true
`)
	rr := serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents/claude/duplicate", `{"name":"claude-2"}`, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("duplicate = %d %s", rr.Code, rr.Body.String())
	}
	table := t016ReadAgentTables(t, store.path)["claude-2"]
	if table["runtime"] != "claude-code" || table["auth"] != "api_key" || table["api_key_env"] != "ANTHROPIC_API_KEY" || table["allow_scheduled"] != true {
		t.Fatalf("duplicate table = %+v", table)
	}
}
