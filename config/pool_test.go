package config

import (
	"reflect"
	"strings"
	"testing"
)

// REQ-PAF-001, REQ-PAF-005 (spec 75): priority and pool on subscription
// accounts and the built-in tables; api_key_envs on API kinds.
func TestPoolConfigLoads(t *testing.T) {
	res, err := loadAgentKeys(t, `[providers.claude-code]
kind = "claude-code"
priority = 20

[providers.antigravity]
kind = "antigravity"
pool = false

[providers.claude-work]
kind = "claude-code"
priority = 0
pool = true

[providers.agy-2]
kind = "antigravity"
priority = 5

[providers.ds]
kind = "deepseek"
api_key_env = "K1"
api_key_envs = ["K2", "K3"]
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	providers := res.Config.Providers
	if p := providers["claude-code"].Priority; p == nil || *p != 20 {
		t.Fatalf("claude-code priority = %v", p)
	}
	if p := providers["claude-work"].Priority; p == nil || *p != 0 {
		t.Fatalf("claude-work priority = %v (0 must differ from unset)", p)
	}
	if !providers["claude-work"].InPool() {
		t.Fatal("claude-work pool = true not in pool")
	}
	if providers["antigravity"].InPool() {
		t.Fatal("antigravity pool = false still in pool")
	}
	if providers["agy-2"].Priority == nil || providers["agy-2"].Pool != nil || !providers["agy-2"].InPool() {
		t.Fatalf("agy-2 = %+v", providers["agy-2"])
	}
	if providers["ds"].Priority != nil {
		t.Fatal("ds has a priority")
	}
	if got := providers["ds"].APIKeyEnvs; !reflect.DeepEqual(got, []string{"K2", "K3"}) {
		t.Fatalf("ds api_key_envs = %v", got)
	}
}

func TestPoolConfigRules(t *testing.T) {
	claude := "[providers.cw]\nkind = \"claude-code\"\n"
	agy := "[providers.agy]\nkind = \"antigravity\"\n"
	ds := "[providers.ds]\nkind = \"deepseek\"\n"
	cases := []struct {
		name, table, key, msg string
	}{
		{"priority negative", claude + "priority = -1\n", "providers.cw.priority", "0 or more"},
		{"priority type", agy + "priority = \"1\"\n", "providers.agy.priority", "integer"},
		{"priority float", agy + "priority = 1.5\n", "providers.agy.priority", "integer"},
		{"pool type", claude + "pool = \"no\"\n", "providers.cw.pool", "boolean"},
		{"builtin priority negative", "[providers.antigravity]\nkind = \"antigravity\"\npriority = -5\n", "providers.antigravity.priority", "0 or more"},
		{"builtin pool type", "[providers.claude-code]\nkind = \"claude-code\"\npool = 1\n", "providers.claude-code.pool", "boolean"},
		{"priority on api kind", ds + "priority = 1\n", "providers.ds.priority", "allowed only for kind claude-code or antigravity"},
		{"pool on api kind", ds + "pool = false\n", "providers.ds.pool", "allowed only for kind claude-code or antigravity"},
		{"api_key_envs on claude", claude + "api_key_envs = [\"K2\"]\n", "providers.cw.api_key_envs", "not allowed for kind claude-code"},
		{"api_key_envs on agy", agy + "api_key_envs = [\"K2\"]\n", "providers.agy.api_key_envs", "not allowed for kind antigravity"},
		{"api_key_envs type", ds + "api_key_envs = \"K2\"\n", "providers.ds.api_key_envs", "array"},
		{"api_key_envs item type", ds + "api_key_envs = [3]\n", "providers.ds.api_key_envs.0", "string"},
		{"api_key_envs bad name", ds + "api_key_envs = [\"K2\", \"2BAD\"]\n", "providers.ds.api_key_envs.1", "not a valid secret name"},
		{"api_key_envs empty name", ds + "api_key_envs = [\"\"]\n", "providers.ds.api_key_envs.0", "not a valid secret name"},
		{"api_key_envs duplicate", ds + "api_key_envs = [\"K2\", \"K2\"]\n", "providers.ds.api_key_envs.1", "duplicate"},
		{"api_key_envs equals api_key_env", ds + "api_key_env = \"K1\"\napi_key_envs = [\"K1\"]\n", "providers.ds.api_key_envs.0", "duplicate"},
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

// Every API kind accepts api_key_envs.
func TestAPIKeyEnvsAllowedKinds(t *testing.T) {
	for _, kind := range apiKeyPoolKinds {
		t.Run(kind, func(t *testing.T) {
			table := "[providers.p]\nkind = \"" + kind + "\"\nbase_url = \"https://example.test/v1\"\napi_key_env = \"K1\"\napi_key_envs = [\"K2\"]\n"
			if _, err := loadAgentKeys(t, table); err != nil {
				t.Fatalf("load: %v", err)
			}
		})
	}
}
