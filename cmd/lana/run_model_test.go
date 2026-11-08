package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func chatServer(t *testing.T, text string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRunOutputModel covers the model of `lana run --output json`: a legacy
// agent shows "provider:model", a router agent the "provider/model" target
// that answered, and a Claude Code turn "claude-code/<model>" (API-CCT-008;
// spec 38 showed "claude-agent/<model>").
func TestRunOutputModel(t *testing.T) {
	up := chatServer(t, "ok")
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	t.Cleanup(down.Close)
	providers := fmt.Sprintf(`[providers.up]
kind = "openai_compat"
base_url = %q

[providers.down]
kind = "openai_compat"
base_url = %q

[combos.duo]
models = ["down/broken-model", "up/good-model"]
`, up.URL, down.URL)

	tests := []struct {
		name  string
		agent string
		want  string
	}{
		{name: "legacy", agent: "provider = \"up\"\nmodel = \"m\"", want: "up:m"},
		{name: "combo fallback", agent: `model = "duo"`, want: "up/good-model"},
		{name: "address", agent: `model = "up/good-model"`, want: "up/good-model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("LANA_HOME", home)
			cfg := providers + "\n[agents.helper]\n" + tt.agent + "\n\n[[routes]]\nchannel = \"library\"\nagent = \"helper\"\n"
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := runOutputModel(t); got != tt.want {
				t.Fatalf("model = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("claude agent", func(t *testing.T) {
		bin := buildCmdFakeClaude(t)
		t.Setenv("FAKECLAUDE_FIXTURE", claudeFixture(t, "hi.jsonl"))
		t.Setenv("FAKECLAUDE_LOG", filepath.Join(t.TempDir(), "fake.jsonl"))
		home := t.TempDir()
		t.Setenv("LANA_HOME", home)
		cfg := fmt.Sprintf("[agents.helper]\nruntime = \"claude-code\"\nbinary = %q\nworkspace = %q\npermission = \"trust\"\nmodel = \"haiku\"\n\n[[routes]]\nchannel = \"library\"\nagent = \"helper\"\n", bin, t.TempDir())
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		// The model Claude Code reported in its session meta, not the alias.
		if got := runOutputModel(t); got != "claude-code/claude-haiku-5-5" {
			t.Fatalf("model = %q", got)
		}
	})
}

func runOutputModel(t *testing.T) string {
	t.Helper()
	out, code := captureStdout(t, func() int { return cmdRun([]string{"--agent", "helper", "--output", "json", "hi"}) })
	if code != 0 {
		t.Fatalf("exit = %d, output = %q", code, out)
	}
	var result runOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	return result.Model
}
