package config

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// loadRouterConfig loads text as the home config.toml.
func loadRouterConfig(t *testing.T, text string) (*Result, error) {
	t.Helper()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), text)
	return Load(context.Background(), Options{Home: home, Env: envMap(nil)})
}

// routerError returns the message of the error at key, or "" when there is
// none.
func routerError(t *testing.T, err error, key string) string {
	t.Helper()
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	for _, e := range errs {
		if e.Key == key {
			return e.Msg
		}
	}
	t.Fatalf("no error at %s: %v", key, errs)
	return ""
}

const routerProviders = `
[providers.openrouter]
kind = "openrouter"

[providers.ds]
kind = "deepseek"

[providers.an]
kind = "anthropic"

[providers.oa]
kind = "openai"

[providers.local]
kind = "openai_compat"
base_url = "http://localhost:8080/v1"
`

// AC-RTR-004 (config side): combo, address, and legacy agents are all valid;
// preset kinds need no base_url.
func TestRouterAgentFormsValid(t *testing.T) {
	res, err := loadRouterConfig(t, routerProviders+`
[combos.coding]
description = "Claude first"
models = ["an/claude-sonnet-5-5", "openrouter/openai/gpt-5-mini", "ds/deepseek-flash"]

[router]
proxy = true
cooldown = "0s"

[agents.combo]
model = "coding"

[agents.address]
model = "openrouter/openai/gpt-5-mini"
fallback = ["ds/deepseek-flash", "local:llama"]

[agents.legacy]
provider = "openrouter"
model = "openai/gpt-5-mini"
`)
	if err != nil {
		t.Fatalf("valid router config rejected: %v", err)
	}
	if got := res.Config.Combos["coding"].Models; len(got) != 3 || got[1] != "openrouter/openai/gpt-5-mini" {
		t.Fatalf("combo models = %v", got)
	}
	if !res.Config.Router.Proxy || res.Config.Router.Cooldown != "0s" {
		t.Fatalf("router = %+v", res.Config.Router)
	}
}

// AC-RTR-005: an address with an unknown provider fails at agents.<name>.model.
func TestRouterAgentUnknownProvider(t *testing.T) {
	_, err := loadRouterConfig(t, routerProviders+`
[agents.bad]
model = "nope/x"
`)
	if msg := routerError(t, err, "agents.bad.model"); !strings.Contains(msg, "unknown provider or combo") {
		t.Fatalf("message = %q", msg)
	}
}

// AC-RTR-006: a combo must not list another combo.
func TestRouterComboListsCombo(t *testing.T) {
	_, err := loadRouterConfig(t, routerProviders+`
[combos.coding]
models = ["ds/x", "coding2"]

[combos.coding2]
models = ["ds/y"]
`)
	if msg := routerError(t, err, "combos.coding.models[1]"); !strings.Contains(msg, "combo") {
		t.Fatalf("message = %q", msg)
	}
}

// API-RTR-002: combo name, list size, address form, known provider, and
// duplicate rules.
func TestRouterComboRules(t *testing.T) {
	cases := []struct {
		name, toml, key, want string
	}{
		{"name equals a provider", "[combos.ds]\nmodels = [\"ds/x\"]\n", "combos.ds", "provider"},
		{"bad name", "[combos.Coding]\nmodels = [\"ds/x\"]\n", "combos.Coding", "name must match"},
		{"empty models", "[combos.c]\nmodels = []\n", "combos.c.models", "at least one"},
		{"missing models", "[combos.c]\ndescription = \"x\"\n", "combos.c.models", "is required"},
		{"too many models", "[combos.c]\nmodels = [\"ds/1\",\"ds/2\",\"ds/3\",\"ds/4\",\"ds/5\",\"ds/6\",\"ds/7\",\"ds/8\",\"ds/9\",\"ds/10\",\"ds/11\"]\n", "combos.c.models", "at most 10"},
		{"legacy form", "[combos.c]\nmodels = [\"ds:x\"]\n", "combos.c.models[0]", "provider/model"},
		{"unknown provider", "[combos.c]\nmodels = [\"nope/x\"]\n", "combos.c.models[0]", "unknown provider"},
		{"duplicate", "[combos.c]\nmodels = [\"ds/x\", \"ds/x\"]\n", "combos.c.models[1]", "twice"},
		{"unknown key", "[combos.c]\nmodels = [\"ds/x\"]\nweight = 1\n", "combos.c.weight", "unknown key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadRouterConfig(t, routerProviders+tc.toml)
			if msg := routerError(t, err, tc.key); !strings.Contains(msg, tc.want) {
				t.Fatalf("message = %q, want %q", msg, tc.want)
			}
		})
	}
}

// API-RTR-004: [router] keys and cooldown bounds.
func TestRouterTableRules(t *testing.T) {
	cases := []struct {
		name, toml, key, want string
	}{
		{"cooldown over 1h", "[router]\ncooldown = \"2h\"\n", "router.cooldown", "at most 1h"},
		{"cooldown not a duration", "[router]\ncooldown = \"soon\"\n", "router.cooldown", "invalid duration"},
		{"negative cooldown", "[router]\ncooldown = \"-1m\"\n", "router.cooldown", "negative"},
		{"proxy not bool", "[router]\nproxy = \"yes\"\n", "router.proxy", "boolean"},
		{"unknown key", "[router]\nstrategy = \"rr\"\n", "router.strategy", "unknown key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadRouterConfig(t, tc.toml)
			if msg := routerError(t, err, tc.key); !strings.Contains(msg, tc.want) {
				t.Fatalf("message = %q, want %q", msg, tc.want)
			}
		})
	}
	if _, err := loadRouterConfig(t, "[router]\ncooldown = \"1h\"\n"); err != nil {
		t.Fatalf("cooldown 1h rejected: %v", err)
	}
}

// API-RTR-001 and API-RTR-003: kinds, base_url for openai_compat, fallback
// rules, and embedding kinds.
func TestRouterProviderAndFallbackRules(t *testing.T) {
	cases := []struct {
		name, toml, key, want string
	}{
		{"unknown kind", "[providers.g]\nkind = \"gemini\"\n", "providers.g.kind", "invalid value"},
		{"openai_compat needs base_url", "[providers.c]\nkind = \"openai_compat\"\n", "providers.c.base_url", "required"},
		{"combo in fallback", routerProviders + "[combos.coding]\nmodels = [\"ds/x\"]\n[agents.a]\nprovider = \"ds\"\nmodel = \"x\"\nfallback = [\"coding\"]\n", "agents.a.fallback.0", "combo"},
		{"fallback unknown provider", routerProviders + "[agents.a]\nprovider = \"ds\"\nmodel = \"x\"\nfallback = [\"nope/x\"]\n", "agents.a.fallback.0", "unknown provider"},
		{"fallback not a model ref", routerProviders + "[agents.a]\nprovider = \"ds\"\nmodel = \"x\"\nfallback = [\"ds\"]\n", "agents.a.fallback.0", "provider:model"},
		{"embedding on anthropic", routerProviders + "[knowledge]\nembedding_provider = \"an\"\nembedding_model = \"m\"\n", "knowledge.embedding_provider", "must have kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadRouterConfig(t, tc.toml)
			if msg := routerError(t, err, tc.key); !strings.Contains(msg, tc.want) {
				t.Fatalf("message = %q, want %q", msg, tc.want)
			}
		})
	}
	for _, kind := range []string{"openai", "deepseek", "openrouter"} {
		toml := "[providers.e]\nkind = \"" + kind + "\"\n[knowledge]\nembedding_provider = \"e\"\nembedding_model = \"m\"\n"
		if _, err := loadRouterConfig(t, toml); err != nil {
			t.Fatalf("embedding kind %s rejected: %v", kind, err)
		}
	}
}
