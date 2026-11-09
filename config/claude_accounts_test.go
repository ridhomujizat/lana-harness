package config

import (
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// API-CCT-010: [providers.<name>] kind = "claude-code" is a Claude Code
// account; its addresses are valid wherever claude-code ones are.
func TestClaudeCodeAccountConfig(t *testing.T) {
	res, err := loadAgentKeys(t, `[providers.ds]
kind = "deepseek"

[providers.claude-work]
kind = "claude-code"
config_dir = "$LANA_HOME/claude/work"
binary = "/bin/claude"
auth = "api_key"
api_key_env = "WORK_KEY"
allow_scheduled = true
label = "Work"

[providers.claude-home]
kind = "claude-code"
config_dir = "~/claude-home"
disabled = true

[combos.c]
models = ["claude-work/opus", "claude-home/sonnet", "ds/deepseek-flash"]

[agents.a]
model = "claude-work/haiku"
reasoning_effort = "xhigh"
fallback = ["claude-home/sonnet", "ds/deepseek-flash"]

[agents.b]
model = "c"
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	work := res.Config.Providers["claude-work"]
	if work.Kind != core.ClaudeCodeKind || work.ConfigDir != "$LANA_HOME/claude/work" || work.Binary != "/bin/claude" ||
		work.Auth != "api_key" || work.APIKeyEnv != "WORK_KEY" || !work.AllowScheduled || work.Label != "Work" {
		t.Fatalf("claude-work = %+v", work)
	}
	if !res.Config.Providers["claude-home"].Disabled {
		t.Fatal("claude-home not disabled")
	}
}

// API-CCT-027 (spec 39 revision 5): an optional [providers.claude-code]
// table of kind "claude-code" holds the built-in account's settings; it is
// not a configured provider, so claude-code addresses stay Claude Code ones.
func TestBuiltinClaudeAccountTable(t *testing.T) {
	res, err := loadAgentKeys(t, `[providers.claude-code]
kind = "claude-code"
allow_scheduled = true
disabled = true
binary = "/opt/claude"
auth = "api_key"
api_key_env = "MY_CLAUDE_KEY"

[combos.c]
models = ["claude-code/opus"]

[agents.a]
model = "claude-code/haiku"
fallback = ["claude-code/sonnet"]
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	builtin := res.Config.Providers[core.ClaudeCodeProvider]
	if builtin.Kind != core.ClaudeCodeKind || !builtin.AllowScheduled || !builtin.Disabled || builtin.Binary != "/opt/claude" ||
		builtin.Auth != "api_key" || builtin.APIKeyEnv != "MY_CLAUDE_KEY" {
		t.Fatalf("built-in = %+v", builtin)
	}
	// A legacy provider key never names the built-in table.
	errs := agentKeyErrors(t, "[providers.claude-code]\nkind = \"claude-code\"\n\n[agents.a]\nprovider = \"claude-code\"\nmodel = \"haiku\"\n")
	if e := findError(errs, "agents.a.provider"); e == nil || !strings.Contains(e.Msg, "not registered") {
		t.Fatalf("provider key: %v", errs)
	}
}

func TestClaudeCodeAccountRules(t *testing.T) {
	account := "[providers.cw]\nkind = \"claude-code\"\n"
	cases := []struct {
		name, table, key, msg string
	}{
		{"base_url", account + "base_url = \"https://x\"\n", "providers.cw.base_url", "not allowed for kind claude-code"},
		{"headers", account + "headers = { a = \"b\" }\n", "providers.cw.headers", "not allowed for kind claude-code"},
		{"timeout", account + "timeout = \"5s\"\n", "providers.cw.timeout", "not allowed for kind claude-code"},
		{"bad auth", account + "auth = \"oauth\"\n", "providers.cw.auth", "invalid value 'oauth'"},
		{"api_key needs env", account + "auth = \"api_key\"\n", "providers.cw.api_key_env", "required when auth is api_key"},
		{"relative dir", account + "config_dir = \"claude/x\"\n", "providers.cw.config_dir", "absolute path"},
		{"dir type", account + "config_dir = 3\n", "providers.cw.config_dir", "string"},
		{"allow type", account + "allow_scheduled = \"yes\"\n", "providers.cw.allow_scheduled", "boolean"},
		{"binary flag", account + "binary = \"-x\"\n", "providers.cw.binary", "must not start with '-'"},
		{"label length", account + "label = \"" + strings.Repeat("x", 61) + "\"\n", "providers.cw.label", "at most 60 characters"},
		{"label type", account + "label = 3\n", "providers.cw.label", "string"},
		{"label on other kind", "[providers.ds]\nkind = \"deepseek\"\nlabel = \"x\"\n", "providers.ds.label", "only for kind claude-code"},
		{"account key on other kind", "[providers.ds]\nkind = \"deepseek\"\nconfig_dir = \"/x\"\n", "providers.ds.config_dir", "only for kind claude-code"},
		{"reserved name", "[providers.claude-code]\nkind = \"deepseek\"\n", "providers.claude-code", "claude-code is built in"},
		{"built-in config_dir", "[providers.claude-code]\nkind = \"claude-code\"\nconfig_dir = \"/x\"\n", "providers.claude-code.config_dir", "not allowed for the built-in account"},
		{"built-in label", "[providers.claude-code]\nkind = \"claude-code\"\nlabel = \"Mine\"\n", "providers.claude-code.label", "not allowed for the built-in account"},
		{"built-in base_url", "[providers.claude-code]\nkind = \"claude-code\"\nbase_url = \"https://x\"\n", "providers.claude-code.base_url", "not allowed for kind claude-code"},
		{"built-in auth", "[providers.claude-code]\nkind = \"claude-code\"\nauth = \"oauth\"\n", "providers.claude-code.auth", "invalid value 'oauth'"},
		{"model flag", account + "\n[agents.a]\nmodel = \"cw/-x\"\n", "agents.a.model", "must not start with '-'"},
		{"combo flag", account + "\n[combos.c]\nmodels = [\"cw/-x\"]\n", "combos.c.models[0]", "must not start with '-'"},
		{"combo name", account + "\n[combos.cw]\nmodels = [\"cw/opus\"]\n", "combos.cw", "already used by a provider"},
		{"background", account + "\n[router]\nbackground_model = \"cw/haiku\"\n", "router.background_model", "cannot use claude-code"},
		{"background combo", account + "\n[combos.c]\nmodels = [\"cw/haiku\"]\n\n[router]\nbackground_model = \"c\"\n", "router.background_model", "cannot use claude-code"},
		{"embedding", account + "\n[knowledge]\nembedding_provider = \"cw\"\n", "knowledge.embedding_provider", "must have kind"},
		{"native effort", "[providers.ds]\nkind = \"deepseek\"\n\n[agents.a]\nmodel = \"ds/deepseek-flash\"\nreasoning_effort = \"xhigh\"\n", "agents.a.reasoning_effort", "invalid value 'xhigh'"},
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
