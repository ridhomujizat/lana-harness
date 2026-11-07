package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestOriginsTableAndLeaf(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	writeFile(t, configPath, "[providers.openai]\nkind = \"openai_compat\"\nbase_url = \"https://example.com/v1\"\n")
	patch := filepath.Join(t.TempDir(), "p.toml")
	writeFile(t, patch, "[agents.x]\nmodel = \"m\"\n")
	res, err := loadWith(t, home, patch)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := res.Origins.Of("providers.openai"); got != configPath {
		t.Fatalf("providers.openai=%q", got)
	}
	if got, _ := res.Origins.Of("agents.x.model"); got != patch {
		t.Fatalf("agents.x.model=%q", got)
	}
}

func TestSecretEnvWins(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "secrets.env"), "OPENAI_API_KEY=from-file\n")
	if err := os.Chmod(filepath.Join(home, "secrets.env"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(map[string]string{"OPENAI_API_KEY": "from-env"})})
	if err != nil {
		t.Fatal(err)
	}
	secret, ok := res.Secrets.Lookup("OPENAI_API_KEY")
	if !ok || secret.Reveal() != "from-env" {
		t.Fatalf("secret=%q ok=%v", secret.Reveal(), ok)
	}
	res2, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	secret, ok = res2.Secrets.Lookup("OPENAI_API_KEY")
	if !ok || secret.Reveal() != "from-file" {
		t.Fatalf("fallback secret=%q ok=%v", secret.Reveal(), ok)
	}
}

func TestEffectiveConfigRedacted(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[mcp.x]\ncommand = \"cmd\"\nenv = { TOKEN = \"literal-value\" }\n")
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Config.MCP["x"].Env["TOKEN"].Reveal(); got != "literal-value" {
		t.Fatalf("reveal=%q", got)
	}
	tomlOut, err := toml.Marshal(res.Config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(tomlOut), "literal-value") {
		t.Fatalf("TOML leaked secret: %s", tomlOut)
	}
	jsonOut, err := json.Marshal(res.Config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(jsonOut), "literal-value") {
		t.Fatalf("JSON leaked secret: %s", jsonOut)
	}
	if !strings.Contains(string(tomlOut), "***") || !strings.Contains(string(jsonOut), "***") {
		t.Fatalf("redaction missing: toml=%s json=%s", tomlOut, jsonOut)
	}
}

func TestSecretsPermWarning(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "secrets.env")
	writeFile(t, path, "TOKEN=abc\n")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w.Msg, "permission 0644") && strings.Contains(w.Msg, "chmod 600") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warning missing: %v", res.Warnings)
	}
	if !strings.Contains(buf.String(), "chmod 600") {
		t.Fatalf("warning not logged to slog: %s", buf.String())
	}
}

func TestMCPEnvInterpolationSecret(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[mcp.github]\ncommand = \"cmd\"\nenv = { GITHUB_TOKEN = \"${GITHUB_TOKEN}\" }\n")
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(map[string]string{"GITHUB_TOKEN": "abc"})})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Config.MCP["github"].Env["GITHUB_TOKEN"].Reveal(); got != "abc" {
		t.Fatalf("value=%q", got)
	}
	out, err := toml.Marshal(res.Config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "abc") {
		t.Fatalf("secret leaked: %s", out)
	}
	if !strings.Contains(string(out), "***") {
		t.Fatalf("redaction missing: %s", out)
	}
}

func TestNonMCPFieldNotInterpolated(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\nprompt_file = \"${HOME}/p.md\"\n")
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(map[string]string{"HOME": "/home/x"})})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Config.Agents["x"].PromptFile; got != "${HOME}/p.md" {
		t.Fatalf("prompt_file changed: %q", got)
	}
}

func TestMissingVarError(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[mcp.x]\ncommand = \"cmd\"\nenv = { TOKEN = \"${MISSING_VAR}\" }\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	if !strings.Contains(err.Error(), "MISSING_VAR") || !strings.Contains(err.Error(), "mcp.x.env.TOKEN") {
		t.Fatalf("wrong message: %v", err)
	}
}

// A key written to secrets.env after Load is visible to the loaded secret
// source without a restart; the environment still wins (REQ-RTR-008).
func TestSecretsFileReloadsOnChange(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "secrets.env")
	writeFile(t, path, "A_KEY=one\n")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(map[string]string{"ENV_KEY": "from-env"})})
	if err != nil {
		t.Fatal(err)
	}
	if secret, ok := res.Secrets.Lookup("A_KEY"); !ok || secret.Reveal() != "one" {
		t.Fatalf("A_KEY = %q, %v", secret.Reveal(), ok)
	}
	if _, ok := res.Secrets.Lookup("B_KEY"); ok {
		t.Fatal("B_KEY found before it was written")
	}
	writeFile(t, path, "A_KEY=two-longer\nB_KEY=new\nENV_KEY=from-file\n")
	if secret, ok := res.Secrets.Lookup("A_KEY"); !ok || secret.Reveal() != "two-longer" {
		t.Fatalf("A_KEY after write = %q, %v", secret.Reveal(), ok)
	}
	if secret, ok := res.Secrets.Lookup("B_KEY"); !ok || secret.Reveal() != "new" {
		t.Fatalf("B_KEY after write = %q, %v", secret.Reveal(), ok)
	}
	if secret, ok := res.Secrets.Lookup("ENV_KEY"); !ok || secret.Reveal() != "from-env" {
		t.Fatalf("ENV_KEY = %q, want the environment value", secret.Reveal())
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Secrets.Lookup("B_KEY"); ok {
		t.Fatal("B_KEY still found after secrets.env was removed")
	}
	// A secrets.env created after boot is read too.
	writeFile(t, path, "C_KEY=late\n")
	if secret, ok := res.Secrets.Lookup("C_KEY"); !ok || secret.Reveal() != "late" {
		t.Fatalf("C_KEY = %q, %v", secret.Reveal(), ok)
	}
}
