package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// setupSavedConfig is the part of config.toml the integration test asserts.
type setupSavedConfig struct {
	Version   int                           `toml:"version"`
	Web       setupSavedWeb                 `toml:"web"`
	Providers map[string]setupSavedProvider `toml:"providers"`
	Agents    map[string]setupSavedAgent    `toml:"agents"`
}

type setupSavedWeb struct {
	Enabled bool   `toml:"enabled"`
	Addr    string `toml:"addr"`
}

type setupSavedProvider struct {
	Kind      string `toml:"kind"`
	BaseURL   string `toml:"base_url"`
	APIKeyEnv string `toml:"api_key_env"`
}

type setupSavedAgent struct {
	Provider   string `toml:"provider"`
	Model      string `toml:"model"`
	Workspace  string `toml:"workspace"`
	Permission string `toml:"permission"`
}

const setupTestProviderKey = "sk-setup-test-key"

// setupFakeProvider is a minimal OpenAI-compatible server for the setup save's
// connection test.
type setupFakeProvider struct {
	mu       sync.Mutex
	requests int
	auth     string
}

func newSetupFakeProvider(t *testing.T) (*httptest.Server, *setupFakeProvider) {
	t.Helper()
	fake := &setupFakeProvider{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.requests++
		fake.auth = r.Header.Get("Authorization")
		fake.mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, fake
}

func (f *setupFakeProvider) count() (int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests, f.auth
}

type setupStatusResponse struct {
	Needed        bool     `json:"needed"`
	Reasons       []string `json:"reasons"`
	ProviderCount int      `json:"provider_count"`
	AgentCount    int      `json:"agent_count"`
}

type setupSavedResponse struct {
	Restart string `json:"restart"`
	PID     int    `json:"pid"`
}

// TestDaemonSetupSaveRestartsInPlace covers AC-WEB-037: a fresh LANA_HOME with
// no config.toml is set up from the browser, the daemon restarts in place with
// the same PID, and the saved files match the contract.
func TestDaemonSetupSaveRestartsInPlace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("restart in place is not supported on Windows")
	}
	home := t.TempDir()
	addr := freeLoopbackAddr(t)
	writeRestartConfig(t, home, addr)
	provider, fake := newSetupFakeProvider(t)

	child := startRestartChild(t, home)
	tokenLine := child.waitLine(t, "web: owner token created at ", 10*time.Second)
	i := strings.Index(tokenLine, restartTokenMark)
	if i < 0 {
		t.Fatalf("token line has no marker: %q", tokenLine)
	}
	token := tokenLine[i+len(restartTokenMark):]
	if listening := child.waitLine(t, "web: listening on http://", 10*time.Second); listening != addr {
		t.Fatalf("listening address = %q, want %q", listening, addr)
	}
	pid := child.cmd.Process.Pid
	cookie, csrf := restartLogin(t, addr, token)
	before, err := tryRestartDaemonInfo(addr, cookie)
	if err != nil {
		t.Fatalf("daemon info before setup: %v", err)
	}
	if before.PID != pid {
		t.Fatalf("pid before setup = %d, want %d", before.PID, pid)
	}

	// A new LANA_HOME has no config.toml. Removing it after the daemon starts
	// keeps the test on its own port while still exercising the missing-file
	// path of API-WEB-018 and API-WEB-021.
	if err := os.Remove(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}

	resp, body, err := restartDo(t, http.MethodGet, addr, "/v1/setup", "", cookie, "")
	if err != nil {
		t.Fatalf("GET /v1/setup: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/setup status = %d, body = %q", resp.StatusCode, body)
	}
	var status setupStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("status body %q: %v", body, err)
	}
	if !status.Needed || !containsValue(status.Reasons, "config_missing") {
		t.Fatalf("setup status = %+v, want needed with config_missing", status)
	}

	payload, err := json.Marshal(map[string]any{
		"provider": map[string]any{
			"name":     "openai",
			"api_key":  setupTestProviderKey,
			"base_url": provider.URL + "/v1",
			"model":    "gpt-5-mini",
		},
		"agent": map[string]any{
			"name":       "coder",
			"template":   "coder",
			"workspace":  "~/lana/workspace",
			"permission": "ask",
		},
		"persona": map[string]any{
			"agent_md": "# coder\n",
			"user_md":  "- pref",
		},
		"expected_version": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, body, err = restartDo(t, http.MethodPost, addr, "/v1/setup", string(payload), cookie, csrf)
	if err != nil {
		t.Fatalf("POST /v1/setup: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /v1/setup status = %d, body = %q, want 202", resp.StatusCode, body)
	}
	var saved setupSavedResponse
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("setup body %q: %v", body, err)
	}
	if saved.Restart != "scheduled" || saved.PID != pid {
		t.Fatalf("setup response = %+v, want pid %d", saved, pid)
	}

	// The daemon replaces its process image; the PID stays and started_at
	// changes, which is what the page polls (REQ-WEB-034).
	var after restartDaemonInfo
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		info, err := tryRestartDaemonInfo(addr, cookie)
		if err == nil && info.StartedAt != before.StartedAt {
			after = info
			break
		}
		if err != nil && strings.Contains(err.Error(), "status 401") {
			cookie, _ = restartLogin(t, addr, token)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if after.StartedAt == before.StartedAt {
		t.Fatalf("started_at did not change: before=%d after=%d stderr=%s", before.StartedAt, after.StartedAt, child.stderr())
	}
	if after.PID != pid {
		t.Fatalf("pid after setup = %d, want %d", after.PID, pid)
	}

	// The daemon answers again; setup is no longer needed, which is the
	// server side of wizard step 5.
	resp, body, err = restartDo(t, http.MethodGet, addr, "/v1/setup", "", cookie, "")
	if err != nil {
		t.Fatalf("GET /v1/setup after restart: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup status after restart = %d, body = %q", resp.StatusCode, body)
	}
	var afterStatus setupStatusResponse
	if err := json.Unmarshal(body, &afterStatus); err != nil {
		t.Fatalf("setup status body %q: %v", body, err)
	}
	if afterStatus.Needed || afterStatus.ProviderCount != 1 || afterStatus.AgentCount != 1 {
		t.Fatalf("setup status after restart = %+v", afterStatus)
	}

	configData, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var savedConfig setupSavedConfig
	if err := toml.Unmarshal(configData, &savedConfig); err != nil {
		t.Fatalf("config %q: %v", configData, err)
	}
	if savedConfig.Version != 1 {
		t.Fatalf("version = %d, want 1", savedConfig.Version)
	}
	if !savedConfig.Web.Enabled || savedConfig.Web.Addr != addr {
		t.Fatalf("web = %+v, want enabled at %s", savedConfig.Web, addr)
	}
	providerConfig, ok := savedConfig.Providers["openai"]
	if !ok {
		t.Fatalf("providers = %+v, want openai", savedConfig.Providers)
	}
	if providerConfig.Kind != "openai_compat" || providerConfig.BaseURL != provider.URL+"/v1" || providerConfig.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("provider = %+v", providerConfig)
	}
	agentConfig, ok := savedConfig.Agents["coder"]
	if !ok {
		t.Fatalf("agents = %+v, want coder", savedConfig.Agents)
	}
	if agentConfig.Provider != "openai" || agentConfig.Model != "gpt-5-mini" ||
		agentConfig.Workspace != "~/lana/workspace" || agentConfig.Permission != "ask" {
		t.Fatalf("agent = %+v", agentConfig)
	}

	secrets := filepath.Join(home, "secrets.env")
	data, err := os.ReadFile(secrets)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "OPENAI_API_KEY="+setupTestProviderKey) {
		t.Fatalf("secrets = %q", data)
	}
	if info, err := os.Stat(secrets); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets mode = %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "coder", "AGENT.md")); err != nil {
		t.Fatalf("AGENT.md: %v", err)
	}
	user, err := os.ReadFile(filepath.Join(home, "USER.md"))
	if err != nil {
		t.Fatalf("USER.md: %v", err)
	}
	if string(user) != "- pref\n" {
		t.Fatalf("USER.md = %q", user)
	}

	requests, auth := fake.count()
	if requests != 1 {
		t.Fatalf("provider requests = %d, want exactly 1", requests)
	}
	if auth != "Bearer "+setupTestProviderKey {
		t.Fatalf("Authorization = %q, want the setup key", auth)
	}
	if strings.Contains(string(body), setupTestProviderKey) {
		t.Fatalf("setup status response leaked the key: %q", body)
	}
}

func containsValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
