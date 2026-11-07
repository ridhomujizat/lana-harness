package config

import (
	"strings"
	"testing"
)

// AC-CCR-001: a claude-code agent cannot set provider.
func TestRuntimeAgentRejectsProvider(t *testing.T) {
	errs := agentKeyErrors(t, "[providers.x]\nkind = \"openai_compat\"\nbase_url = \"http://x\"\n\n[agents.claude]\nruntime = \"claude-code\"\nprovider = \"x\"\n")
	e := findError(errs, "agents.claude.provider")
	if e == nil {
		t.Fatalf("no agents.claude.provider error: %v", errs)
	}
	if e.Msg != "a claude-code agent cannot set provider" {
		t.Fatalf("msg = %q", e.Msg)
	}
}

// REQ-CCR-001: every key that does not apply to a runtime agent is an error.
func TestRuntimeAgentForbiddenKeys(t *testing.T) {
	values := map[string]string{
		"tools":                "[\"bash\"]",
		"disabled_tools":       "[\"bash\"]",
		"mcp":                  "[\"x\"]",
		"skills":               "[\"x\"]",
		"subagents":            "[\"x\"]",
		"max_steps":            "3",
		"compaction_headroom":  "1000",
		"compaction_threshold": "0.5",
		"compaction_retain":    "0.5",
		"memory_capture":       "false",
		"context_window":       "1000",
		"vision":               "true",
		"fallback":             "[\"x/y\"]",
	}
	for key, value := range values {
		t.Run(key, func(t *testing.T) {
			errs := agentKeyErrors(t, "[agents.claude]\nruntime = \"claude-code\"\n"+key+" = "+value+"\n")
			e := findError(errs, "agents.claude."+key)
			if e == nil || e.Msg != "a claude-code agent cannot set "+key {
				t.Fatalf("key %s: %v", key, errs)
			}
		})
	}
}

// API-CCR-001: a valid Claude Agent loads, with a free-form model that is
// never checked as a router address.
func TestRuntimeAgentValid(t *testing.T) {
	res, err := loadAgentKeys(t, `[agents.claude]
runtime = "claude-code"
auth = "api_key"
api_key_env = "MY_KEY"
model = "claude-sonnet-4-5[1m]"
reasoning_effort = "xhigh"
binary = "~/bin/claude"
allow_scheduled = true
workspace = "~/coding"
permission = "ask"
rules = { allow = [], deny = ["bash(rm *)"] }
description = "Claude"

[agents.other]
runtime = "claude-code"
model = "opus/latest"
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	agent := res.Config.Agents["claude"]
	if agent.Runtime != "claude-code" || agent.Auth != "api_key" || agent.APIKeyEnv != "MY_KEY" ||
		agent.Binary != "~/bin/claude" || !agent.AllowScheduled || agent.ReasoningEffort != "xhigh" {
		t.Fatalf("agent = %+v", agent)
	}
}

func TestRuntimeAgentKeyRules(t *testing.T) {
	cases := []struct {
		name, table, key, msg string
	}{
		{"unknown runtime", "[agents.a]\nruntime = \"acp\"\n", "agents.a.runtime", "unknown runtime \"acp\""},
		{"bad auth", "[agents.a]\nruntime = \"claude-code\"\nauth = \"oauth\"\n", "agents.a.auth", "invalid value 'oauth'"},
		{"api_key needs env", "[agents.a]\nruntime = \"claude-code\"\nauth = \"api_key\"\n", "agents.a.api_key_env", "required when auth is api_key"},
		{"model flag", "[agents.a]\nruntime = \"claude-code\"\nmodel = \"--dangerously-skip-permissions\"\n", "agents.a.model", "must not start with '-'"},
		{"effort", "[agents.a]\nruntime = \"claude-code\"\nreasoning_effort = \"off\"\n", "agents.a.reasoning_effort", "invalid value 'off'"},
		{"binary on native", "[agents.a]\nmodel = \"x\"\nbinary = \"/bin/claude\"\n", "agents.a.binary", "only a claude-code agent can set binary"},
		{"allow_scheduled on native", "[agents.a]\nmodel = \"x\"\nallow_scheduled = true\n", "agents.a.allow_scheduled", "only a claude-code agent"},
		{"allow_scheduled type", "[agents.a]\nruntime = \"claude-code\"\nallow_scheduled = \"yes\"\n", "agents.a.allow_scheduled", "boolean"},
		{"team lead", "[agents.a]\nruntime = \"claude-code\"\n\n[agents.b]\nmodel = \"x\"\n\n[teams.t]\nlead = \"a\"\nmembers = [\"b\"]\n", "teams.t.lead", "cannot lead a team"},
		{"knowledge agent", "[agents.a]\nruntime = \"claude-code\"\n\n[knowledge]\nagent = \"a\"\n", "knowledge.agent", "background passes cannot use it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := agentKeyErrors(t, tc.table)
			e := findError(errs, tc.key)
			if e == nil || !strings.Contains(e.Msg, tc.msg) {
				t.Fatalf("want %s %q, got %v", tc.key, tc.msg, errs)
			}
		})
	}
}

// A claude-code agent may be a team member: lana agents delegate to it
// through task (ADR 0011, ADR 0018).
func TestRuntimeAgentTeamMember(t *testing.T) {
	_, err := loadAgentKeys(t, "[agents.a]\nruntime = \"claude-code\"\n\n[agents.b]\nmodel = \"x\"\nsubagents = [\"a\"]\n\n[teams.t]\nlead = \"b\"\nmembers = [\"a\"]\n")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
}
