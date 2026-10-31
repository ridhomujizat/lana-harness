package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
)

func TestLookupProvider(t *testing.T) {
	preset, ok := lookupProvider("DeepSeek")
	if !ok {
		t.Fatal("deepseek not found")
	}
	if preset.baseURL != "https://api.deepseek.com/v1" || preset.keyEnv != "DEEPSEEK_API_KEY" || preset.model != "deepseek-flash" {
		t.Fatalf("preset = %+v", preset)
	}
	if _, ok := lookupProvider("anthropic"); ok {
		t.Fatal("anthropic not supported yet")
	}
}

func TestSecretsText(t *testing.T) {
	if got := secretsText("DEEPSEEK_API_KEY", "sk-abc"); got != "# Provider API key; do not commit.\nDEEPSEEK_API_KEY=sk-abc\n" {
		t.Fatalf("with key = %q", got)
	}
	if got := secretsText("DEEPSEEK_API_KEY", ""); !strings.Contains(got, "# DEEPSEEK_API_KEY=") {
		t.Fatalf("without key = %q", got)
	}
}

func TestInitDeepSeekYes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	if code := cmdInit([]string{"--yes", "--provider", "deepseek"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	cfg, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[providers.deepseek]", "https://api.deepseek.com/v1", "DEEPSEEK_API_KEY", "model = \"deepseek-flash\""} {
		if !strings.Contains(string(cfg), want) {
			t.Fatalf("config does not contain %q:\n%s", want, cfg)
		}
	}
	secrets, err := os.ReadFile(filepath.Join(home, "secrets.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secrets), "# DEEPSEEK_API_KEY=") {
		t.Fatalf("secrets = %q", secrets)
	}
	if info, err := os.Stat(filepath.Join(home, "secrets.env")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("permission secrets: %v %v", info, err)
	}
	if _, err := config.Load(context.Background(), config.Options{Home: home}); err != nil {
		t.Fatalf("config is invalid: %v", err)
	}
}

func TestInitUnknownProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	if code := cmdInit([]string{"--yes", "--provider", "anthropic"}); code != 2 {
		t.Fatalf("exit = %d", code)
	}
}
func withStdin(t *testing.T, input string) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = r.Close()
	})
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
}

func TestAskAndAskKey(t *testing.T) {
	withStdin(t, "deepseek\n\nsk-abc\n")
	if got := ask("Provider", "openai"); got != "deepseek" {
		t.Fatalf("provider = %q", got)
	}
	if got := ask("Env", "DEEPSEEK_API_KEY"); got != "DEEPSEEK_API_KEY" {
		t.Fatalf("default = %q", got)
	}
	if got := askKey("API key"); got != "sk-abc" {
		t.Fatalf("key = %q", got)
	}
}

func TestSecretInFileLastWins(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "secrets.env"), []byte("# comment\nDEEPSEEK_API_KEY=\nDEEPSEEK_API_KEY=sk-abc\nOPENAI_API_KEY=sk-x\nOPENAI_API_KEY=\"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !secretInFile(home, "DEEPSEEK_API_KEY") {
		t.Fatal("a later non-empty assignment must count")
	}
	if secretInFile(home, "OPENAI_API_KEY") {
		t.Fatal("a later empty assignment must win")
	}
}
