package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
)

// The revision 3 setup body of the Router onboarding: several providers, a
// model address, an optional fallback (the default combo), and an optional
// background model. Every save is in router form and passes config.Check.

func newRouterSetupBody(providers []map[string]any, model string) map[string]any {
	list := make([]any, 0, len(providers))
	for _, provider := range providers {
		list = append(list, provider)
	}
	return map[string]any{
		"providers": list,
		"model":     model,
		"agent": map[string]any{
			"name":       "default",
			"template":   "assistant",
			"workspace":  "~/lana/workspace",
			"permission": "ask",
		},
		"persona": map[string]any{
			"agent_md": "# default",
			"user_md":  "",
		},
		"expected_version": "",
	}
}

// saveRouterSetup posts body to a fresh home and returns the store, the
// provider tests the save ran, and the response.
func saveRouterSetup(t *testing.T, body map[string]any) (*testConfigStore, []ProviderTest, *httptestResponse) {
	t.Helper()
	store := setupMissingConfigStore(t)
	var seen []ProviderTest
	s, cookie, csrf, logs := newSetupTestServer(t, store, func(_ context.Context, test ProviderTest) (TestResult, error) {
		seen = append(seen, test)
		return TestResult{OK: true, LatencyMS: 2}, nil
	}, nil)
	response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
	for _, provider := range body["providers"].([]any) {
		if key, _ := provider.(map[string]any)["api_key"].(string); key != "" {
			if strings.Contains(string(response.body), key) || strings.Contains(logs.String(), key) {
				t.Fatalf("response or log leaked a key: %q", response.body)
			}
		}
	}
	return store, seen, response
}

// checkSavedConfig runs config.Check on the saved file, as `lana config check`
// would.
func checkSavedConfig(t *testing.T, store *testConfigStore) string {
	t.Helper()
	raw := readSetupFile(t, store.path)
	if _, err := config.Check(context.Background(), store.home, []byte(raw)); err != nil {
		t.Fatalf("saved config fails config.Check: %v\n%s", err, raw)
	}
	return raw
}

func TestSetupRouterMultiProvider(t *testing.T) {
	body := newRouterSetupBody([]map[string]any{
		{"name": "openai", "kind": "openai", "api_key": "sk-openai-key"},
		{"name": "deepseek", "kind": "deepseek", "api_key": "sk-deepseek-key"},
		{"name": "gemini", "kind": "openai_compat", "api_key": "sk-gemini-key", "base_url": "https://generativelanguage.googleapis.com/v1beta/openai/"},
	}, "openai/gpt-5.1")
	store, seen, response := saveRouterSetup(t, body)
	if response.code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	if len(seen) != 3 {
		t.Fatalf("provider tests = %+v, want 3", seen)
	}
	// The main model's provider is tested with that model; the others with
	// their kind's or host's test model.
	if seen[0].Kind != "openai" || seen[0].Model != "gpt-5.1" || seen[0].APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("openai test = %+v", seen[0])
	}
	if seen[1].Kind != "deepseek" || seen[1].Model != "deepseek-flash" || seen[1].APIKeyEnv != "DEEPSEEK_API_KEY" {
		t.Fatalf("deepseek test = %+v", seen[1])
	}
	if seen[2].Kind != "openai_compat" || seen[2].Model != "gemini-2.5-flash" ||
		seen[2].BaseURL != "https://generativelanguage.googleapis.com/v1beta/openai" || seen[2].APIKeyEnv != "GEMINI_API_KEY" {
		t.Fatalf("gemini test = %+v", seen[2])
	}

	raw := checkSavedConfig(t, store)
	saved := parseSetupConfig(t, raw)
	if p := saved.Providers["openai"]; p.Kind != "openai" || p.BaseURL != "" || p.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("openai = %+v", p)
	}
	if p := saved.Providers["deepseek"]; p.Kind != "deepseek" || p.BaseURL != "" || p.APIKeyEnv != "DEEPSEEK_API_KEY" {
		t.Fatalf("deepseek = %+v", p)
	}
	if p := saved.Providers["gemini"]; p.Kind != "openai_compat" || p.BaseURL != "https://generativelanguage.googleapis.com/v1beta/openai" || p.APIKeyEnv != "GEMINI_API_KEY" {
		t.Fatalf("gemini = %+v", p)
	}
	agent := saved.Agents["default"]
	if agent.Provider != "" || agent.Model != "openai/gpt-5.1" {
		t.Fatalf("agent = %+v, want router form", agent)
	}
	if strings.Contains(raw, "provider = ") {
		t.Fatalf("router form has no agent provider key:\n%s", raw)
	}
	if len(saved.Combos) != 0 || len(saved.Router) != 0 {
		t.Fatalf("no fallback or background: combos %v router %v", saved.Combos, saved.Router)
	}
	secrets := readSetupFile(t, filepath.Join(store.home, "secrets.env"))
	for _, want := range []string{"OPENAI_API_KEY=sk-openai-key", "DEEPSEEK_API_KEY=sk-deepseek-key", "GEMINI_API_KEY=sk-gemini-key"} {
		if !strings.Contains(secrets, want) {
			t.Fatalf("secrets = %q, want %s", secrets, want)
		}
	}
}

func TestSetupRouterFallbackCombo(t *testing.T) {
	body := newRouterSetupBody([]map[string]any{
		{"name": "anthropic", "kind": "anthropic", "api_key": "sk-ant-key"},
		{"name": "deepseek", "kind": "deepseek", "api_key": "sk-ds-key"},
	}, "anthropic/claude-sonnet-5-5")
	body["fallback"] = "deepseek/deepseek-v4-pro"
	store, seen, response := saveRouterSetup(t, body)
	if response.code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	// Each provider is tested with the model the combo uses on it.
	if len(seen) != 2 || seen[0].Model != "claude-sonnet-5-5" || seen[1].Model != "deepseek-v4-pro" {
		t.Fatalf("provider tests = %+v", seen)
	}
	saved := parseSetupConfig(t, checkSavedConfig(t, store))
	combo, ok := saved.Combos["default"]
	if !ok || len(combo.Models) != 2 || combo.Models[0] != "anthropic/claude-sonnet-5-5" || combo.Models[1] != "deepseek/deepseek-v4-pro" {
		t.Fatalf("combos = %+v", saved.Combos)
	}
	if agent := saved.Agents["default"]; agent.Model != "default" || agent.Provider != "" {
		t.Fatalf("agent = %+v, want the default combo", agent)
	}
}

func TestSetupRouterClaudeCodeMain(t *testing.T) {
	t.Run("with background model", func(t *testing.T) {
		body := newRouterSetupBody([]map[string]any{
			{"name": "openai", "kind": "openai", "api_key": "sk-openai-key"},
		}, "claude-code/sonnet")
		body["fallback"] = "openai/gpt-5-mini"
		body["background_model"] = "openai/gpt-5-mini"
		store, seen, response := saveRouterSetup(t, body)
		if response.code != http.StatusAccepted {
			t.Fatalf("status = %d, body = %q", response.code, response.body)
		}
		if len(seen) != 1 || seen[0].Model != "gpt-5-mini" {
			t.Fatalf("provider tests = %+v, want only openai", seen)
		}
		saved := parseSetupConfig(t, checkSavedConfig(t, store))
		if combo := saved.Combos["default"]; len(combo.Models) != 2 || combo.Models[0] != "claude-code/sonnet" {
			t.Fatalf("combo = %+v", combo)
		}
		if saved.Router["background_model"] != "openai/gpt-5-mini" {
			t.Fatalf("router = %+v", saved.Router)
		}
		if _, ok := saved.Providers["claude-code"]; ok {
			t.Fatalf("claude-code is built in; no table: %+v", saved.Providers)
		}
	})

	t.Run("claude code only", func(t *testing.T) {
		body := newRouterSetupBody(nil, "claude-code/opus")
		store := setupMissingConfigStore(t)
		s, cookie, csrf, _ := newSetupTestServer(t, store, func(context.Context, ProviderTest) (TestResult, error) {
			t.Fatal("no provider to test")
			return TestResult{}, nil
		}, nil)
		response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
		if response.code != http.StatusAccepted {
			t.Fatalf("status = %d, body = %q", response.code, response.body)
		}
		saved := parseSetupConfig(t, checkSavedConfig(t, store))
		if len(saved.Providers) != 0 || saved.Agents["default"].Model != "claude-code/opus" {
			t.Fatalf("config = %+v", saved)
		}
		if _, err := os.Stat(filepath.Join(store.home, "secrets.env")); !os.IsNotExist(err) {
			t.Fatalf("no key, no secrets.env: %v", err)
		}
		// A Claude Code agent is a model source: setup is done.
		var state SetupStatus
		serveSetup(t, s, http.MethodGet, "", cookie, "").json(t, &state)
		if state.Needed || state.ProviderCount != 0 || state.AgentCount != 1 {
			t.Fatalf("status after save = %+v", state)
		}
	})
}

// A [router] table that exists keeps its other keys.
func TestSetupRouterKeepsRouterKeys(t *testing.T) {
	const raw = "version = 1\n\n[web]\nenabled = true\n\n[router]\ncooldown = \"30s\"\n"
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	s, cookie, csrf, _ := newSetupTestServer(t, store, nil, nil)
	body := newRouterSetupBody([]map[string]any{
		{"name": "deepseek", "kind": "deepseek", "api_key": "sk-ds-key"},
	}, "claude-code/sonnet")
	body["background_model"] = "deepseek/deepseek-flash"
	body["expected_version"] = shaHex([]byte(raw))
	response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
	if response.code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	saved := parseSetupConfig(t, checkSavedConfig(t, store))
	if saved.Router["cooldown"] != "30s" || saved.Router["background_model"] != "deepseek/deepseek-flash" {
		t.Fatalf("router = %+v", saved.Router)
	}
}

func TestSetupRouterRejectsBadInput(t *testing.T) {
	openai := map[string]any{"name": "openai", "kind": "openai", "api_key": "sk-openai-key"}
	cases := []struct {
		name   string
		field  string
		mutate func(map[string]any)
	}{
		{name: "no model", field: "model", mutate: func(b map[string]any) { b["model"] = "" }},
		{name: "bare model", field: "model", mutate: func(b map[string]any) { b["model"] = "gpt-5-mini" }},
		{name: "default as model", field: "model", mutate: func(b map[string]any) { b["model"] = "default" }},
		{name: "fallback equals model", field: "fallback", mutate: func(b map[string]any) { b["fallback"] = "openai/gpt-5-mini" }},
		{name: "claude background", field: "background_model", mutate: func(b map[string]any) { b["background_model"] = "claude-code/haiku" }},
		{name: "bad kind", field: "providers.0.kind", mutate: func(b map[string]any) {
			b["providers"] = []any{map[string]any{"name": "openai", "kind": "claude-code", "api_key": "k"}}
		}},
		{name: "claude-code name", field: "providers.0.name", mutate: func(b map[string]any) {
			b["providers"] = []any{map[string]any{"name": "claude-code", "kind": "openai", "api_key": "k"}}
		}},
		{name: "missing key", field: "providers.0.api_key", mutate: func(b map[string]any) {
			b["providers"] = []any{map[string]any{"name": "openai", "kind": "openai"}}
		}},
		{name: "compat without url", field: "providers.0.base_url", mutate: func(b map[string]any) {
			b["providers"] = []any{map[string]any{"name": "groq", "kind": "openai_compat", "api_key": "k"}}
		}},
		{name: "duplicate provider", field: "providers.1.name", mutate: func(b map[string]any) {
			b["providers"] = []any{openai, openai}
		}},
		{name: "nothing connected", field: "providers", mutate: func(b map[string]any) { b["providers"] = []any{} }},
		{name: "both shapes", field: "provider", mutate: func(b map[string]any) {
			b["provider"] = map[string]any{"name": "openai", "api_key": "k", "model": "gpt-5-mini"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := newRouterSetupBody([]map[string]any{openai}, "openai/gpt-5-mini")
			tc.mutate(body)
			store := setupMissingConfigStore(t)
			s, cookie, csrf, _ := newSetupTestServer(t, store, func(context.Context, ProviderTest) (TestResult, error) {
				t.Fatal("invalid input must not reach the provider")
				return TestResult{}, nil
			}, nil)
			response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
			if response.code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, body = %q, want 422", response.code, response.body)
			}
			envelope := decodeError(t, response)
			if envelope.Error.Code != "validation_failed" || len(envelope.Error.Details) == 0 || envelope.Error.Details[0]["field"] != tc.field {
				t.Fatalf("error = %+v, want field %q", envelope.Error, tc.field)
			}
			if _, err := os.Stat(store.path); !os.IsNotExist(err) {
				t.Fatalf("config was written after invalid input: %v", err)
			}
		})
	}
}

// config.Check rejects a model on a provider that is not connected; nothing
// is written and no provider is called.
func TestSetupRouterUnknownProvider(t *testing.T) {
	body := newRouterSetupBody([]map[string]any{
		{"name": "openai", "kind": "openai", "api_key": "sk-openai-key"},
	}, "openai/gpt-5-mini")
	body["fallback"] = "mistral/mistral-small-latest"
	store := setupMissingConfigStore(t)
	s, cookie, csrf, _ := newSetupTestServer(t, store, func(context.Context, ProviderTest) (TestResult, error) {
		t.Fatal("a rejected config must not reach the provider")
		return TestResult{}, nil
	}, nil)
	response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
	if response.code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	if envelope := decodeError(t, response); envelope.Error.Code != "validation_failed" ||
		!strings.Contains(envelope.Error.Details[0]["field"].(string), "combos.default") {
		t.Fatalf("error = %+v", envelope.Error)
	}
	if _, err := os.Stat(store.path); !os.IsNotExist(err) {
		t.Fatalf("config was written: %v", err)
	}
}

// A failed test of the second provider names it and writes nothing.
func TestSetupRouterConnectionFailedNamesProvider(t *testing.T) {
	body := newRouterSetupBody([]map[string]any{
		{"name": "openai", "kind": "openai", "api_key": "sk-openai-key"},
		{"name": "deepseek", "kind": "deepseek", "api_key": "sk-ds-key"},
	}, "openai/gpt-5-mini")
	store := setupMissingConfigStore(t)
	s, cookie, csrf, _ := newSetupTestServer(t, store, func(_ context.Context, test ProviderTest) (TestResult, error) {
		if test.Kind == "deepseek" {
			return TestResult{OK: false, Status: 401, Message: "401 Unauthorized"}, nil
		}
		return TestResult{OK: true}, nil
	}, nil)
	response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
	if response.code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "connection_failed" || envelope.Error.Message != "deepseek: 401 Unauthorized" || envelope.Error.Details[0]["provider"] != "deepseek" {
		t.Fatalf("error = %+v", envelope.Error)
	}
	if _, err := os.Stat(store.path); !os.IsNotExist(err) {
		t.Fatalf("config was written: %v", err)
	}
}

// GET /v1/setup carries the picker catalog: built-in models per preset kind
// and the Claude Code aliases.
func TestSetupStatusCatalog(t *testing.T) {
	store := setupMissingConfigStore(t)
	s, cookie, _, _ := newSetupTestServer(t, store, nil, nil)
	var status SetupStatus
	serveSetup(t, s, http.MethodGet, "", cookie, "").json(t, &status)
	for _, kind := range []string{"openai", "anthropic", "deepseek", "openrouter"} {
		if len(status.Models[kind]) == 0 {
			t.Fatalf("models[%s] is empty: %+v", kind, status.Models)
		}
	}
	if status.Models["deepseek"][0].ID == "" || status.Models["openrouter"][0].ID != "openai/gpt-5-mini" {
		t.Fatalf("models = %+v", status.Models)
	}
	ids := make([]string, 0, len(status.ClaudeCodeModels))
	for _, model := range status.ClaudeCodeModels {
		ids = append(ids, model.ID)
	}
	if strings.Join(ids, ",") != "opus,sonnet,haiku" || status.ClaudeCodeModels[1].Name != "Sonnet" {
		t.Fatalf("claude code models = %+v", status.ClaudeCodeModels)
	}
}

// A combo on Claude Code counts as a model source too.
func TestSetupStateClaudeCodeCombo(t *testing.T) {
	raw := "[combos.main]\nmodels = [\"claude-code/sonnet\"]\n\n[agents.coder]\nmodel = \"main\"\n"
	status, err := setupStateFromFile(ConfigFile{Exists: true, Raw: []byte(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if status.Needed {
		t.Fatalf("status = %+v, want not needed", status)
	}
}
