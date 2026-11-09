package config

import (
	"strings"
	"testing"
)

// AC-CCR-001 (kept by API-CCT-002): a legacy runtime = "claude-code" agent
// cannot set provider; its model is the Claude Code address.
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

// API-CCT-002: the keys spec 38 rejected on a runtime agent are accepted;
// lana-only keys simply do not apply to its Claude Code turns.
func TestRuntimeAgentLanaKeysAccepted(t *testing.T) {
	values := map[string]string{
		"tools":                "[\"bash\"]",
		"disabled_tools":       "[\"bash\"]",
		"skills":               "[\"x\"]",
		"max_steps":            "3",
		"compaction_headroom":  "1000",
		"compaction_threshold": "0.5",
		"compaction_retain":    "0.5",
		"memory_capture":       "false",
		"context_window":       "1000",
		"vision":               "true",
		"fallback":             "[\"claude-code/haiku\"]",
	}
	for key, value := range values {
		t.Run(key, func(t *testing.T) {
			if _, err := loadAgentKeys(t, "[agents.claude]\nruntime = \"claude-code\"\n"+key+" = "+value+"\n"); err != nil {
				t.Fatalf("key %s: %v", key, err)
			}
		})
	}
}

// AC-CCT-003 (config side): a legacy runtime agent loads as its claude-code
// address with the Claude-only keys kept.
func TestRuntimeAgentLegacyMapping(t *testing.T) {
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

[agents.haiku]
runtime = "claude-code"
model = "haiku"

[agents.plain]
runtime = "claude-code"
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	agent := res.Config.Agents["claude"]
	if agent.Runtime != "" || agent.Model != "claude-code/claude-sonnet-4-5[1m]" || agent.Auth != "api_key" || agent.APIKeyEnv != "MY_KEY" ||
		agent.Binary != "~/bin/claude" || !agent.AllowScheduled || agent.ReasoningEffort != "xhigh" {
		t.Fatalf("agent = %+v", agent)
	}
	if got := res.Config.Agents["haiku"].Model; got != "claude-code/haiku" {
		t.Fatalf("haiku model = %q", got)
	}
	if got := res.Config.Agents["plain"].Model; got != "claude-code/default" {
		t.Fatalf("plain model = %q", got)
	}
}

// AC-CCT-001, AC-CCT-002: claude-code addresses are valid in model,
// fallback, and combos without a [providers] entry; the Claude-only keys are
// accepted on any agent.
func TestClaudeCodeAddresses(t *testing.T) {
	res, err := loadAgentKeys(t, `[providers.ds]
kind = "deepseek"

[combos.c]
models = ["claude-code/sonnet", "ds/deepseek-flash"]

[agents.a]
model = "claude-code/opus"
fallback = ["ds/deepseek-flash"]
reasoning_effort = "medium"
auth = "subscription"
binary = "/bin/claude"
allow_scheduled = true

[agents.b]
model = "c"

[agents.d]
model = "ds/deepseek-flash"
fallback = ["claude-code/haiku"]
allow_scheduled = false

[router]
background_model = "ds/deepseek-flash"
claude_code_models = ["claude-opus-4-1", "my-model[1m]"]
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if res.Config.Agents["a"].Model != "claude-code/opus" || res.Config.Router.BackgroundModel != "ds/deepseek-flash" || len(res.Config.Router.ClaudeCodeModels) != 2 {
		t.Fatalf("config = %+v", res.Config)
	}
}

func TestClaudeCodeKeyRules(t *testing.T) {
	cases := []struct {
		name, table, key, msg string
	}{
		{"unknown runtime", "[agents.a]\nruntime = \"acp\"\n", "agents.a.runtime", "unknown runtime \"acp\""},
		{"bad auth", "[agents.a]\nmodel = \"claude-code/opus\"\nauth = \"oauth\"\n", "agents.a.auth", "invalid value 'oauth'"},
		{"api_key needs env", "[agents.a]\nruntime = \"claude-code\"\nauth = \"api_key\"\n", "agents.a.api_key_env", "required when auth is api_key"},
		{"legacy model flag", "[agents.a]\nruntime = \"claude-code\"\nmodel = \"--dangerously-skip-permissions\"\n", "agents.a.model", "must not start with '-'"},
		{"address model flag", "[agents.a]\nmodel = \"claude-code/--x\"\n", "agents.a.model", "must not start with '-'"},
		{"combo model flag", "[combos.c]\nmodels = [\"claude-code/-x\"]\n", "combos.c.models[0]", "must not start with '-'"},
		{"legacy effort", "[agents.a]\nruntime = \"claude-code\"\nreasoning_effort = \"off\"\n", "agents.a.reasoning_effort", "invalid value 'off'"},
		{"claude effort", "[agents.a]\nmodel = \"claude-code/opus\"\nreasoning_effort = \"huge\"\n", "agents.a.reasoning_effort", "invalid value 'huge'"},
		{"allow_scheduled type", "[agents.a]\nmodel = \"claude-code/opus\"\nallow_scheduled = \"yes\"\n", "agents.a.allow_scheduled", "boolean"},
		{"provider reserved", "[providers.claude-code]\nkind = \"anthropic\"\n", "providers.claude-code", "claude-code is built in"},
		{"combo name reserved", "[combos.claude-code]\nmodels = [\"claude-code/opus\"]\n", "combos.claude-code", "already used by a provider"},
		{"background claude", "[router]\nbackground_model = \"claude-code/haiku\"\n", "router.background_model", "cannot use claude-code"},
		{"background combo claude", "[combos.c]\nmodels = [\"claude-code/haiku\"]\n\n[router]\nbackground_model = \"c\"\n", "router.background_model", "cannot use claude-code"},
		{"background unknown", "[router]\nbackground_model = \"nope/x\"\n", "router.background_model", "unknown provider"},
		{"background type", "[router]\nbackground_model = 3\n", "router.background_model", "string"},
		{"custom models type", "[router]\nclaude_code_models = \"x\"\n", "router.claude_code_models", "array"},
		{"custom model flag", "[router]\nclaude_code_models = [\"-x\"]\n", "router.claude_code_models[0]", "must not start with '-'"},
		{"custom model twice", "[router]\nclaude_code_models = [\"a\", \"a\"]\n", "router.claude_code_models[1]", "listed twice"},
		{"custom model space", "[router]\nclaude_code_models = [\"a b\"]\n", "router.claude_code_models[0]", "invalid model ID"},
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

// ADR 0020: a Claude Code agent may lead a team and run the knowledge passes;
// both reach lana's tools and background chain.
func TestRuntimeAgentTeamAndKnowledge(t *testing.T) {
	_, err := loadAgentKeys(t, "[agents.a]\nruntime = \"claude-code\"\n\n[agents.b]\nmodel = \"x\"\nsubagents = [\"a\"]\n\n[teams.t]\nlead = \"a\"\nmembers = [\"b\"]\n\n[knowledge]\nagent = \"a\"\n")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
}
