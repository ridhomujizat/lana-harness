package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
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

func TestSeedHome(t *testing.T) {
	home := t.TempDir()
	task := evalTask{
		User:   []string{"I am the owner.", "Prefer short answers."},
		Memory: []string{"note one", "note two"},
	}
	if err := seedHome(home, task); err != nil {
		t.Fatalf("seedHome: %v", err)
	}
	tests := []struct {
		name    string
		path    string
		entries []string
	}{
		{name: "user", path: agenthome.SharedUser(home), entries: task.User},
		{name: "memory", path: agenthome.Paths(home, "eval").Memory, entries: task.Memory},
	}
	for _, tt := range tests {
		data, err := os.ReadFile(tt.path)
		if err != nil {
			t.Fatalf("read %s: %v", tt.name, err)
		}
		got := agenthome.Parse(string(data))
		if len(got) != len(tt.entries) {
			t.Fatalf("%s entries = %q, want %q", tt.name, got, tt.entries)
		}
		for i := range tt.entries {
			if got[i] != tt.entries[i] {
				t.Fatalf("%s entries = %q, want %q", tt.name, got, tt.entries)
			}
		}
		if string(data) != agenthome.Join(tt.entries)+"\n" {
			t.Fatalf("%s text = %q, want the agenthome entry format", tt.name, string(data))
		}
	}

	// A task without seeds leaves both files absent.
	empty := t.TempDir()
	if err := seedHome(empty, evalTask{}); err != nil {
		t.Fatalf("seedHome with no entries: %v", err)
	}
	for _, path := range []string{agenthome.SharedUser(empty), agenthome.Paths(empty, "eval").Memory} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s exists for an unseeded task", path)
		}
	}
}

func TestWriteAnswer(t *testing.T) {
	workspace := t.TempDir()
	if err := writeAnswer(workspace, "the final answer\n"); err != nil {
		t.Fatalf("writeAnswer: %v", err)
	}
	data, err := os.ReadFile(answerPath(workspace))
	if err != nil {
		t.Fatalf("read answer: %v", err)
	}
	if string(data) != "the final answer\n" {
		t.Fatalf("answer = %q", string(data))
	}
}

func TestCheckEnvPassing(t *testing.T) {
	workspace := t.TempDir()
	// An inherited value with the same name must be overridden.
	t.Setenv("LANA_EVAL_COMPACTIONS", "9")
	output, err := runShellEnv(context.Background(), workspace,
		`printf '%s/%s' "$LANA_EVAL_COMPACTIONS" "$LANA_EVAL_TOOL_CALLS"`,
		commandTimeout, checkEnv(2, 5))
	if err != nil {
		t.Fatalf("runShellEnv: %v", err)
	}
	if output != "2/5" {
		t.Fatalf("env output = %q, want 2/5", output)
	}
}

func TestTaskToolOverride(t *testing.T) {
	task := evalTask{
		Agent: map[string]any{"context_window": 24000},
		Tools: []string{"read_file", "bash"},
	}
	overrides := taskOverrides(task)
	if overrides["context_window"] != 24000 {
		t.Fatalf("context_window override = %v", overrides["context_window"])
	}
	tools, ok := overrides["tools"].([]string)
	if !ok || len(tools) != 2 || tools[0] != "read_file" || tools[1] != "bash" {
		t.Fatalf("tools override = %v", overrides["tools"])
	}
	text, err := renderConfig("https://example.com/v1", "KEY", "m", "/tmp/ws", overrides)
	if err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	if !strings.Contains(text, `tools = ["read_file","bash"]`) {
		t.Fatalf("config does not use the task tools:\n%s", text)
	}
	if count := strings.Count(text, "tools = "); count != 1 {
		t.Fatalf("config has %d tools keys, want 1:\n%s", count, text)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(context.Background(), config.Options{
		Home: home,
		Env:  func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatalf("config.Load with a tool override: %v", err)
	}
	wantTools := []string{"read_file", "bash"}
	agent := loaded.Config.Agents["eval"]
	if len(agent.Tools) != len(wantTools) || agent.Tools[0] != wantTools[0] || agent.Tools[1] != wantTools[1] {
		t.Fatalf("loaded tools = %v, want %v", agent.Tools, wantTools)
	}

	plain := taskOverrides(evalTask{Agent: map[string]any{"context_window": 1}})
	if _, ok := plain["tools"]; ok {
		t.Fatal("taskOverrides added tools without a task tool list")
	}
	empty := taskOverrides(evalTask{Tools: []string{}})
	if tools, ok := empty["tools"].([]string); !ok || len(tools) != 0 {
		t.Fatalf("explicit empty tools = %v, want an empty override", empty["tools"])
	}
}
