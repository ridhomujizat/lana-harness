package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
)

func TestRenderConfig(t *testing.T) {
	const (
		providerURL = "https://example.com/v1"
		keyEnv      = "EVAL_TEST_KEY"
		model       = "eval-model"
		workspace   = "/tmp/eval workspace"
	)
	text, err := renderConfig(providerURL, keyEnv, model, workspace, map[string]any{
		"context_window": 24000,
	})
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	for _, want := range []string{
		`[providers.eval]`,
		`kind = "openai_compat"`,
		`base_url = "https://example.com/v1"`,
		`api_key_env = "EVAL_TEST_KEY"`,
		`timeout = "300s"`,
		`[agents.eval]`,
		`provider = "eval"`,
		`model = "eval-model"`,
		`workspace = "/tmp/eval workspace"`,
		`tools = ["read_file","write_file","edit_file","glob","grep","bash","web_fetch"]`,
		`permission = "trust"`,
		`context_window = 24000`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("config does not contain %q:\n%s", want, text)
		}
	}

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := config.Load(context.Background(), config.Options{
		Home: home,
		Env:  func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	provider, ok := result.Config.Providers["eval"]
	if !ok {
		t.Fatal("provider eval is not in the loaded config")
	}
	if provider.Kind != "openai_compat" || provider.BaseURL != providerURL ||
		provider.APIKeyEnv != keyEnv || provider.Timeout != "300s" {
		t.Fatalf("provider = %+v", provider)
	}
	agent, ok := result.Config.Agents["eval"]
	if !ok {
		t.Fatal("agent eval is not in the loaded config")
	}
	if agent.Provider != "eval" || agent.Model != model || agent.Workspace != workspace ||
		agent.Permission != "trust" || agent.ContextWindow != 24000 {
		t.Fatalf("agent = %+v", agent)
	}
	wantTools := []string{"read_file", "write_file", "edit_file", "glob", "grep", "bash", "web_fetch"}
	if len(agent.Tools) != len(wantTools) {
		t.Fatalf("tools = %v, want %v", agent.Tools, wantTools)
	}
	for i := range wantTools {
		if agent.Tools[i] != wantTools[i] {
			t.Fatalf("tools = %v, want %v", agent.Tools, wantTools)
		}
	}
}

func TestRenderConfigRejectsBadOverride(t *testing.T) {
	if _, err := renderConfig("https://example.com/v1", "KEY", "m", "/tmp/ws", map[string]any{
		"not a key": 1,
	}); err == nil {
		t.Fatal("renderConfig accepted an invalid key")
	}
	if _, err := renderConfig("https://example.com/v1", "KEY", "m", "/tmp/ws", map[string]any{
		"context_window": map[string]string{},
	}); err == nil {
		t.Fatal("renderConfig accepted an unsupported value")
	}
}
