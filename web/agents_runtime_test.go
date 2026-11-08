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

// AC-CCT-003 (backend side, amends AC-CCR-010): a create with the legacy
// Claude Agent body writes model = "claude-code/opus" and no runtime, with no
// provider, template tools, or persona; its row has claude_code: true.
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
	if table["model"] != "claude-code/opus" || table["reasoning_effort"] != "xhigh" {
		t.Fatalf("claude table = %+v\n%s", table, raw)
	}
	for _, key := range []string{"runtime", "provider", "tools", "fallback", "allow_scheduled"} {
		if _, ok := table[key]; ok {
			t.Fatalf("claude table has %s: %+v", key, table)
		}
	}
	if table["auth"] != "subscription" || table["permission"] != "ask" || table["title"] != "Pair" {
		t.Fatalf("claude table = %+v", table)
	}
	if _, err := os.Stat(agenthome.Paths(store.home, "claude").Agent); !os.IsNotExist(err) {
		t.Fatalf("a legacy body create got a persona file: %v", err)
	}

	row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "claude")
	if row.Runtime != "" || !row.ClaudeCode || row.Auth != "subscription" ||
		row.AllowScheduled == nil || *row.AllowScheduled || row.Model == nil || *row.Model != "claude-code/opus" {
		t.Fatalf("claude row = %+v", row)
	}
	coder := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder")
	if coder.Runtime != "" || coder.ClaudeCode || coder.Auth != "" || coder.AllowScheduled != nil {
		t.Fatalf("coder row = %+v", coder)
	}
	list := serveAuth(s, settingsRequest(http.MethodGet, "/v1/agents", "", cookie, ""))
	if !strings.Contains(list.Body.String(), `"claude_code":true`) {
		t.Fatalf("list body = %s", list.Body.String())
	}

	// A full model ID is free-form; the Claude-only keys are kept.
	rr = serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents",
		`{"name":"claude-2","runtime":"claude-code","model":"claude-opus-4-1","auth":"api_key","api_key_env":"ANTHROPIC_API_KEY","binary":"/opt/claude","allow_scheduled":true}`, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create claude-2 status = %d, body = %q", rr.Code, rr.Body.String())
	}
	table = t016ReadAgentTables(t, store.path)["claude-2"]
	if table["model"] != "claude-code/claude-opus-4-1" || table["auth"] != "api_key" || table["api_key_env"] != "ANTHROPIC_API_KEY" ||
		table["binary"] != "/opt/claude" || table["allow_scheduled"] != true {
		t.Fatalf("claude-2 table = %+v", table)
	}

	// The new form: a template agent on a claude-code address with a Claude
	// Code effort and the Claude-only keys.
	rr = serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents",
		`{"name":"bima","template":"assistant","model":"claude-code/sonnet","reasoning_effort":"medium","allow_scheduled":true}`, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create bima = %d %s", rr.Code, rr.Body.String())
	}
	table = t016ReadAgentTables(t, store.path)["bima"]
	if table["model"] != "claude-code/sonnet" || table["allow_scheduled"] != true || table["tools"] == nil {
		t.Fatalf("bima table = %+v", table)
	}
	if row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "bima"); !row.ClaudeCode {
		t.Fatalf("bima row = %+v", row)
	}
}

// The legacy body still gets its rules checked: a template with the legacy
// body, a lana-only effort on it, a missing api_key_env, and an unknown
// runtime are rejected.
func TestAgentsCreateClaudeAgentRejects(t *testing.T) {
	s, _, cookie, csrf := newT016AgentsServer(t, t016AgentsConfig)
	for _, tc := range []struct {
		body string
		key  string
	}{
		{`{"name":"a","runtime":"claude-code","template":"coder"}`, "template"},
		{`{"name":"c","runtime":"claude-code","reasoning_effort":"off"}`, "agents.c.reasoning_effort"},
		{`{"name":"d","runtime":"claude-code","auth":"api_key"}`, "agents.d.api_key_env"},
		{`{"name":"e","runtime":"nope"}`, "agents.e.runtime"},
		{`{"name":"f","template":"coder","model":"claude-code/opus","reasoning_effort":"huge"}`, "reasoning_effort"},
	} {
		rr := serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents", tc.body, cookie, csrf))
		if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), tc.key) {
			t.Fatalf("%s = %d %s, want 422 at %s", tc.body, rr.Code, rr.Body.String(), tc.key)
		}
	}
	// ADR 0020: the Claude-only keys are accepted on any agent.
	rr := serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents", `{"name":"b","template":"coder","auth":"subscription","allow_scheduled":true}`, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("claude-only keys on a lana agent = %d %s", rr.Code, rr.Body.String())
	}
}

// AC-CCT-003: saving a legacy agent writes model = "claude-code/<M>" and no
// runtime; lana's keys stay. A legacy "runtime" body is still accepted.
func TestAgentsSwitchRuntimeRoundTrip(t *testing.T) {
	const config = `version = 1

[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "coder"
tools = ["read_file", "bash"]
skills = ["review"]
max_steps = 9
reasoning_effort = "off"
permission = "ask"
workspace = "/tmp/coder"
description = "Builds things"
title = "Builder"
rules = { deny = ["bash(rm *)"] }

[agents.legacy]
runtime = "claude-code"
model = "haiku"
binary = "/opt/claude"

[[routes]]
channel = "cli"
agent = "coder"
`
	s, store, cookie, csrf := newT016AgentsServer(t, config)

	// AC-CCT-003: a legacy file lists with runtime, then a save migrates it.
	row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "legacy")
	if row.Runtime != "claude-code" || !row.ClaudeCode || row.Model == nil || *row.Model != "claude-code/haiku" || row.Binary != "/opt/claude" {
		t.Fatalf("legacy row = %+v", row)
	}
	code, body := putAgentJSON(t, s, store, cookie, csrf, "legacy", map[string]any{"title": "Pair"})
	if code != http.StatusOK {
		t.Fatalf("save legacy = %d %s", code, body)
	}
	table := t016ReadAgentTables(t, store.path)["legacy"]
	if _, ok := table["runtime"]; ok || table["model"] != "claude-code/haiku" || table["binary"] != "/opt/claude" || table["title"] != "Pair" {
		t.Fatalf("legacy table = %+v", table)
	}
	if row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "legacy"); row.Runtime != "" || !row.ClaudeCode {
		t.Fatalf("saved legacy row = %+v", row)
	}

	// The old dashboard body for a switch to Claude: runtime and model.
	code, body = putAgentJSON(t, s, store, cookie, csrf, "coder", map[string]any{
		"runtime": "claude-code", "provider": "", "model": "sonnet", "reasoning_effort": "medium",
	})
	if code != http.StatusOK {
		t.Fatalf("switch to claude = %d %s", code, body)
	}
	table = t016ReadAgentTables(t, store.path)["coder"]
	if _, ok := table["runtime"]; ok || table["model"] != "claude-code/sonnet" || table["reasoning_effort"] != "medium" {
		t.Fatalf("claude table = %+v", table)
	}
	if table["tools"] == nil || table["max_steps"] == nil || table["permission"] != "ask" || table["title"] != "Builder" {
		t.Fatalf("lana keys lost: %+v", table)
	}

	// Claude-only keys on the claude-code agent; then back to a lana model.
	code, body = putAgentJSON(t, s, store, cookie, csrf, "coder", map[string]any{
		"auth": "api_key", "api_key_env": "ANTHROPIC_API_KEY", "binary": "/opt/claude", "allow_scheduled": true,
	})
	if code != http.StatusOK {
		t.Fatalf("edit claude = %d %s", code, body)
	}
	code, body = putAgentJSON(t, s, store, cookie, csrf, "coder", map[string]any{"model": "mock/coder", "reasoning_effort": "off"})
	if code != http.StatusOK {
		t.Fatalf("switch to lana = %d %s", code, body)
	}
	table = t016ReadAgentTables(t, store.path)["coder"]
	if table["model"] != "mock/coder" || table["auth"] != "api_key" {
		t.Fatalf("lana table = %+v", table)
	}
	row = t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder")
	if row.Runtime != "" || row.ClaudeCode || row.Provider != "mock" {
		t.Fatalf("row = %+v", row)
	}
}

// Duplicating a legacy Claude Agent writes the claude-code address and keeps
// the Claude-only keys.
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
	if _, ok := table["runtime"]; ok || table["model"] != "claude-code/opus" || table["auth"] != "api_key" || table["api_key_env"] != "ANTHROPIC_API_KEY" || table["allow_scheduled"] != true {
		t.Fatalf("duplicate table = %+v", table)
	}
}
