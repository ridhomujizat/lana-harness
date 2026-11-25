package config

import (
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// API-AGY-001: antigravity/<model> is a valid address, and <model> must not
// look like a flag.
func TestAntigravityAddressConfig(t *testing.T) {
	if _, err := loadAgentKeys(t, "[agents.a]\nmodel = \"antigravity/gemini-3.8-flash-low\"\n"); err != nil {
		t.Fatalf("valid antigravity model rejected: %v", err)
	}
	if _, err := loadAgentKeys(t, "[agents.a]\nmodel = \"antigravity/default\"\n"); err != nil {
		t.Fatalf("antigravity/default rejected: %v", err)
	}
	errs := agentKeyErrors(t, "[agents.a]\nmodel = \"antigravity/-x\"\n")
	e := findError(errs, "agents.a.model")
	if e == nil || !strings.Contains(e.Msg, "must not start with '-'") {
		t.Fatalf("antigravity/-x: %v", errs)
	}
	errs = agentKeyErrors(t, "[agents.a]\nmodel = \"antigravity/a b\"\n")
	e = findError(errs, "agents.a.model")
	if e == nil || !strings.Contains(e.Msg, "invalid antigravity model") {
		t.Fatalf("antigravity/a b: %v", errs)
	}
}

// API-AGY-002: a [providers.<name>] kind = "antigravity" account loads its
// home, binary, label, and allow_scheduled, and its addresses are valid.
func TestAntigravityAccountConfig(t *testing.T) {
	res, err := loadAgentKeys(t, `[providers.agy-work]
kind = "antigravity"
home = "$LANA_HOME/antigravity/agy-work"
binary = "/bin/agy"
label = "Work"
allow_scheduled = true

[agents.a]
model = "agy-work/gemini-4-neon-high"
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	work := res.Config.Providers["agy-work"]
	if work.Kind != core.AntigravityKind || work.Home != "$LANA_HOME/antigravity/agy-work" ||
		work.Binary != "/bin/agy" || work.Label != "Work" || !work.AllowScheduled {
		t.Fatalf("agy-work = %+v", work)
	}
}

// API-AGY-002, API-AGY-003: the key gating of an Antigravity account and its
// built-in table.
func TestAntigravityAccountRules(t *testing.T) {
	account := "[providers.agy]\nkind = \"antigravity\"\n"
	cases := []struct {
		name, table, key, msg string
	}{
		{"base_url", account + "base_url = \"https://x\"\n", "providers.agy.base_url", "not allowed for kind antigravity"},
		{"config_dir", account + "config_dir = \"/x\"\n", "providers.agy.config_dir", "not allowed for kind antigravity"},
		{"auth", account + "auth = \"subscription\"\n", "providers.agy.auth", "not allowed for kind antigravity"},
		{"api_key_env", account + "api_key_env = \"K\"\n", "providers.agy.api_key_env", "not allowed for kind antigravity"},
		{"label length", account + "label = \"" + strings.Repeat("x", 61) + "\"\n", "providers.agy.label", "at most 60 characters"},
		{"label type", account + "label = 3\n", "providers.agy.label", "string"},
		{"binary flag", account + "binary = \"-x\"\n", "providers.agy.binary", "must not start with '-'"},
		{"home relative", account + "home = \"agy/x\"\n", "providers.agy.home", "absolute path"},
		{"home type", account + "home = 3\n", "providers.agy.home", "string"},
		{"home on other kind", "[providers.ds]\nkind = \"deepseek\"\nhome = \"/x\"\n", "providers.ds.home", "allowed only for kind antigravity"},
		{"built-in kind", "[providers.antigravity]\nkind = \"deepseek\"\n", "providers.antigravity", "antigravity is built in"},
		{"built-in label", "[providers.antigravity]\nkind = \"antigravity\"\nlabel = \"Mine\"\n", "providers.antigravity.label", "antigravity is built in"},
		{"built-in home", "[providers.antigravity]\nkind = \"antigravity\"\nhome = \"/x\"\n", "providers.antigravity.home", "antigravity is built in"},
		{"model flag", account + "\n[agents.a]\nmodel = \"agy/-x\"\n", "agents.a.model", "must not start with '-'"},
		{"background", account + "\n[router]\nbackground_model = \"agy/haiku\"\n", "router.background_model", "cannot use antigravity"},
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

// AC-AGY-001: an Antigravity address, a Claude Code address, and an API
// provider mix in one combo.
func TestAntigravityComboAddresses(t *testing.T) {
	_, err := loadRouterConfig(t, `[providers.ds]
kind = "deepseek"

[combos.c]
models = ["antigravity/x", "claude-code/sonnet", "ds/y"]
`)
	if err != nil {
		t.Fatalf("mixed combo rejected: %v", err)
	}
}
