package web

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// setupTestConfig is the part of a saved config.toml the setup tests assert.
type setupTestConfig struct {
	Version   int                            `toml:"version"`
	Web       setupTestWeb                   `toml:"web"`
	Providers map[string]setupTestProvider   `toml:"providers"`
	Agents    map[string]setupTestAgentSetup `toml:"agents"`
}

type setupTestWeb struct {
	Enabled bool   `toml:"enabled"`
	Addr    string `toml:"addr"`
}

type setupTestProvider struct {
	Kind      string `toml:"kind"`
	BaseURL   string `toml:"base_url"`
	APIKeyEnv string `toml:"api_key_env"`
}

type setupTestAgentSetup struct {
	Provider   string   `toml:"provider"`
	Model      string   `toml:"model"`
	Workspace  string   `toml:"workspace"`
	Permission string   `toml:"permission"`
	Tools      []string `toml:"tools"`
}

func parseSetupConfig(t *testing.T, raw string) setupTestConfig {
	t.Helper()
	var config setupTestConfig
	if err := toml.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("config %q: %v", raw, err)
	}
	return config
}

func newSetupTestServer(t *testing.T, store *testConfigStore, test func(context.Context, ProviderTest) (TestResult, error), daemon *fakeDaemonStore) (*Server, *http.Cookie, string, *bytes.Buffer) {
	t.Helper()
	s, cookie, csrf := newConfigTestServer(t, store)
	if test == nil {
		test = func(context.Context, ProviderTest) (TestResult, error) {
			return TestResult{OK: true, LatencyMS: 1}, nil
		}
	}
	s.SetBackend(&testProviderTesterBackend{fakeWebBackend: newFakeWebBackend(), test: test})
	if daemon == nil {
		daemon = &fakeDaemonStore{info: DaemonInfo{PID: 4242, RestartSupported: true}}
	}
	s.SetDaemonStore(daemon)
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return s, cookie, csrf, &logs
}

func serveSetup(t *testing.T, s *Server, method, body string, cookie *http.Cookie, csrf string) *httptestResponse {
	t.Helper()
	req := authRequest(method, "/v1/setup", body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	rr := serveAuth(s, req)
	return &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}
}

func newSetupBody() map[string]any {
	return map[string]any{
		"provider": map[string]any{
			"name":     "openai",
			"api_key":  "sk-test",
			"base_url": "https://api.openai.com/v1",
			"model":    "gpt-5-mini",
		},
		"agent": map[string]any{
			"name":       "coder",
			"template":   "coder",
			"workspace":  "~/lana/workspace",
			"permission": "ask",
		},
		"persona": map[string]any{
			"agent_md": "# coder",
			"user_md":  "- pref",
		},
		"expected_version": "",
	}
}

func setupPart(body map[string]any, key string) map[string]any {
	part, _ := body[key].(map[string]any)
	return part
}

func setupBodyJSON(t *testing.T, body map[string]any) string {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func setupMissingConfigStore(t *testing.T) *testConfigStore {
	t.Helper()
	store := newTestConfigStore(t, "", 0o600, ConfigSettings{})
	if err := os.Remove(store.path); err != nil {
		t.Fatal(err)
	}
	return store
}

func readSetupFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestSetupState covers AC-WEB-035: config.toml is read at call time and the
// reasons name what is missing.
func TestSetupState(t *testing.T) {
	const complete = `[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"

[agents.coder]
provider = "openai"
model = "gpt-5-mini"
`
	cases := []struct {
		name          string
		raw           string
		remove        bool
		needed        bool
		reasons       []string
		providerCount int
		agentCount    int
	}{
		{name: "complete", raw: complete, needed: false, reasons: nil, providerCount: 1, agentCount: 1},
		{name: "missing config", remove: true, needed: true, reasons: []string{"config_missing"}, providerCount: 0, agentCount: 0},
		{name: "no provider", raw: "[agents.coder]\nmodel = \"gpt-5-mini\"\n", needed: true, reasons: []string{"no_provider"}, providerCount: 0, agentCount: 1},
		{name: "no agent", raw: "[providers.openai]\nkind = \"openai_compat\"\nbase_url = \"https://api.openai.com/v1\"\n", needed: true, reasons: []string{"no_agent"}, providerCount: 1, agentCount: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestConfigStore(t, tc.raw, 0o600, ConfigSettings{})
			if tc.remove {
				if err := os.Remove(store.path); err != nil {
					t.Fatal(err)
				}
			}
			s, cookie, _, _ := newSetupTestServer(t, store, nil, nil)
			response := serveSetup(t, s, http.MethodGet, "", cookie, "")
			if response.code != http.StatusOK {
				t.Fatalf("status = %d, body = %q", response.code, response.body)
			}
			var status SetupStatus
			response.json(t, &status)
			if status.Needed != tc.needed {
				t.Fatalf("needed = %v, want %v", status.Needed, tc.needed)
			}
			if status.ProviderCount != tc.providerCount || status.AgentCount != tc.agentCount {
				t.Fatalf("counts = %d/%d, want %d/%d", status.ProviderCount, status.AgentCount, tc.providerCount, tc.agentCount)
			}
			if tc.reasons == nil {
				if len(status.Reasons) != 0 {
					t.Fatalf("reasons = %v, want []", status.Reasons)
				}
				return
			}
			for _, want := range tc.reasons {
				if !containsString(status.Reasons, want) {
					t.Fatalf("reasons = %v, want %q", status.Reasons, want)
				}
			}
		})
	}
}

// TestSetupSaveNotNeeded covers the setup_not_needed refusal of API-WEB-021.
func TestSetupSaveNotNeeded(t *testing.T) {
	const raw = `[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"

[agents.coder]
provider = "openai"
model = "gpt-5-mini"
`
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	var tests int
	daemon := &fakeDaemonStore{info: DaemonInfo{PID: 1, RestartSupported: true}}
	s, cookie, csrf, _ := newSetupTestServer(t, store, func(context.Context, ProviderTest) (TestResult, error) {
		tests++
		return TestResult{OK: true}, nil
	}, daemon)

	body := newSetupBody()
	body["expected_version"] = shaHex([]byte(raw))
	response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
	if response.code != http.StatusConflict {
		t.Fatalf("status = %d, body = %q, want 409", response.code, response.body)
	}
	if envelope := decodeError(t, response); envelope.Error.Code != "setup_not_needed" {
		t.Fatalf("code = %q, want setup_not_needed", envelope.Error.Code)
	}
	if tests != 0 || daemon.restarts != 0 {
		t.Fatalf("provider tests/restarts = %d/%d, want 0/0", tests, daemon.restarts)
	}
	if got := readSetupFile(t, store.path); got != raw {
		t.Fatalf("config changed:\n%s", got)
	}
}

// TestSetupSaveRequiresExpectedVersion covers the required expected_version
// field of API-WEB-021.
func TestSetupSaveRequiresExpectedVersion(t *testing.T) {
	store := newTestConfigStore(t, "[web]\naddr = \"127.0.0.1:18300\"\n", 0o600, ConfigSettings{})
	s, cookie, csrf, _ := newSetupTestServer(t, store, nil, nil)
	body := newSetupBody()
	delete(body, "expected_version")

	response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
	if response.code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %q, want 422", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "validation_failed" {
		t.Fatalf("code = %q, want validation_failed", envelope.Error.Code)
	}
	if field := envelope.Error.Details[0]["field"]; field != "expected_version" {
		t.Fatalf("field = %v, want expected_version", field)
	}
}

// TestSetupSaveStaleVersion covers the stale_version refusal of API-WEB-021.
func TestSetupSaveStaleVersion(t *testing.T) {
	const raw = "[web]\naddr = \"127.0.0.1:18300\"\n"
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	var tests int
	s, cookie, csrf, _ := newSetupTestServer(t, store, func(context.Context, ProviderTest) (TestResult, error) {
		tests++
		return TestResult{OK: true}, nil
	}, nil)

	body := newSetupBody()
	body["expected_version"] = "not-the-version"
	response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
	if response.code != http.StatusConflict {
		t.Fatalf("status = %d, body = %q, want 409", response.code, response.body)
	}
	if envelope := decodeError(t, response); envelope.Error.Code != "stale_version" {
		t.Fatalf("code = %q, want stale_version", envelope.Error.Code)
	}
	if tests != 0 {
		t.Fatalf("provider tests = %d, want 0", tests)
	}
	if got := readSetupFile(t, store.path); got != raw {
		t.Fatalf("config changed:\n%s", got)
	}
}

// TestSetupSaveWritesFiles covers AC-WEB-037 at the handler level: config,
// secrets, agent home, and USER.md are written, and the 202 response is sent.
func TestSetupSaveWritesFiles(t *testing.T) {
	const key = "sk-test-secret-key"
	store := setupMissingConfigStore(t)
	daemon := &fakeDaemonStore{info: DaemonInfo{PID: 4242, RestartSupported: true}}
	var seen []ProviderTest
	s, cookie, csrf, logs := newSetupTestServer(t, store, func(_ context.Context, test ProviderTest) (TestResult, error) {
		seen = append(seen, test)
		return TestResult{OK: true, LatencyMS: 3}, nil
	}, daemon)

	body := newSetupBody()
	setupPart(body, "provider")["api_key"] = key
	response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
	if response.code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q, want 202", response.code, response.body)
	}
	var saved setupSaveResponse
	response.json(t, &saved)
	if saved.Restart != "scheduled" || saved.PID != 4242 {
		t.Fatalf("response = %+v", saved)
	}
	if len(seen) != 1 {
		t.Fatalf("provider tests = %d, want 1", len(seen))
	}
	if seen[0].Kind != "openai" || seen[0].BaseURL != "" ||
		seen[0].Model != "gpt-5-mini" || seen[0].APIKeyEnv != "OPENAI_API_KEY" ||
		seen[0].APIKey.Reveal() != key {
		t.Fatalf("provider test = %+v", seen[0])
	}
	if daemon.restarts != 1 {
		t.Fatalf("restarts = %d, want 1", daemon.restarts)
	}

	config := parseSetupConfig(t, readSetupFile(t, store.path))
	if config.Version != 1 {
		t.Fatalf("version = %d, want 1", config.Version)
	}
	if !config.Web.Enabled || config.Web.Addr != authTestAddr {
		t.Fatalf("web = %+v, want enabled at %s", config.Web, authTestAddr)
	}
	provider, ok := config.Providers["openai"]
	if !ok {
		t.Fatalf("providers = %+v, want openai", config.Providers)
	}
	if provider.Kind != "openai" || provider.BaseURL != "" || provider.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("provider = %+v", provider)
	}
	agent, ok := config.Agents["coder"]
	if !ok {
		t.Fatalf("agents = %+v, want coder", config.Agents)
	}
	if agent.Provider != "openai" || agent.Model != "gpt-5-mini" || agent.Workspace != "~/lana/workspace" || agent.Permission != "ask" {
		t.Fatalf("agent = %+v", agent)
	}
	if !containsString(agent.Tools, "bash") {
		t.Fatalf("agent tools = %v, want bash", agent.Tools)
	}
	if info, err := os.Stat(store.path); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("config mode = %v, %v", info, err)
	}

	secrets := readSetupFile(t, filepath.Join(store.home, "secrets.env"))
	if !strings.Contains(secrets, "OPENAI_API_KEY="+key) {
		t.Fatalf("secrets = %q", secrets)
	}
	if info, err := os.Stat(filepath.Join(store.home, "secrets.env")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets mode = %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(store.home, "secrets.meta.json")); err != nil {
		t.Fatalf("secrets meta: %v", err)
	}

	agentFile := filepath.Join(agenthome.Paths(store.home, "coder").Agent)
	if got := readSetupFile(t, agentFile); got != "# coder\n" {
		t.Fatalf("AGENT.md = %q", got)
	}
	userFile := agenthome.SharedUser(store.home)
	if got := readSetupFile(t, userFile); got != "- pref\n" {
		t.Fatalf("USER.md = %q", got)
	}

	// The setup is now complete; the status endpoint says so.
	status := serveSetup(t, s, http.MethodGet, "", cookie, "")
	var state SetupStatus
	status.json(t, &state)
	if state.Needed || state.ProviderCount != 1 || state.AgentCount != 1 {
		t.Fatalf("status after save = %+v", state)
	}

	if strings.Contains(string(response.body), key) || strings.Contains(logs.String(), key) {
		t.Fatalf("response or log leaked the key: %q %q", response.body, logs.String())
	}
}

// TestSetupProviderMapping pins the provider cards to their preset kinds
// (API-RTR-001): each card writes its kind and key env and no base_url, since
// the router knows the URL of every preset kind. A typed preset URL (the old
// Anthropic …/v1 one included) is dropped; another URL is kept.
func TestSetupProviderMapping(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		typed   string
		baseURL string
		keyEnv  string
	}{
		{name: "openai", kind: "openai", keyEnv: "OPENAI_API_KEY"},
		{name: "anthropic", kind: "anthropic", typed: "https://api.anthropic.com/v1", keyEnv: "ANTHROPIC_API_KEY"},
		{name: "deepseek", kind: "deepseek", typed: "https://api.deepseek.com/v1/", keyEnv: "DEEPSEEK_API_KEY"},
		{name: "openrouter", kind: "openrouter", typed: "https://gateway.example.com/v1", baseURL: "https://gateway.example.com/v1", keyEnv: "OPENROUTER_API_KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := setupMissingConfigStore(t)
			var seen []ProviderTest
			s, cookie, csrf, _ := newSetupTestServer(t, store, func(_ context.Context, test ProviderTest) (TestResult, error) {
				seen = append(seen, test)
				return TestResult{OK: true}, nil
			}, nil)

			body := newSetupBody()
			provider := setupPart(body, "provider")
			provider["name"] = tc.name
			provider["api_key"] = "sk-mapping"
			if tc.typed == "" {
				delete(provider, "base_url")
			} else {
				provider["base_url"] = tc.typed
			}
			response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
			if response.code != http.StatusAccepted {
				t.Fatalf("status = %d, body = %q", response.code, response.body)
			}
			if len(seen) != 1 || seen[0].Kind != tc.kind || seen[0].BaseURL != tc.baseURL || seen[0].APIKeyEnv != tc.keyEnv {
				t.Fatalf("provider test = %+v", seen)
			}
			config := parseSetupConfig(t, readSetupFile(t, store.path))
			savedProvider, ok := config.Providers[tc.name]
			if !ok {
				t.Fatalf("providers = %+v, want %s", config.Providers, tc.name)
			}
			if savedProvider.Kind != tc.kind || savedProvider.BaseURL != tc.baseURL || savedProvider.APIKeyEnv != tc.keyEnv {
				t.Fatalf("provider = %+v, want %+v", savedProvider, tc)
			}
			secrets := readSetupFile(t, filepath.Join(store.home, "secrets.env"))
			if !strings.Contains(secrets, tc.keyEnv+"=sk-mapping") {
				t.Fatalf("secrets = %q", secrets)
			}
		})
	}
}

// TestSetupSaveConnectionFailed covers the connection_failed refusal: nothing
// is written before the provider test passes.
func TestSetupSaveConnectionFailed(t *testing.T) {
	store := setupMissingConfigStore(t)
	daemon := &fakeDaemonStore{info: DaemonInfo{PID: 1, RestartSupported: true}}
	s, cookie, csrf, _ := newSetupTestServer(t, store, func(context.Context, ProviderTest) (TestResult, error) {
		return TestResult{OK: false, Status: http.StatusUnauthorized, Message: "401 Unauthorized from api.openai.com"}, nil
	}, daemon)

	response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, newSetupBody()), cookie, csrf)
	if response.code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %q, want 422", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "connection_failed" {
		t.Fatalf("code = %q, want connection_failed", envelope.Error.Code)
	}
	if envelope.Error.Message != "401 Unauthorized from api.openai.com" {
		t.Fatalf("message = %q", envelope.Error.Message)
	}
	if daemon.restarts != 0 {
		t.Fatalf("restarts = %d, want 0", daemon.restarts)
	}
	if _, err := os.Stat(store.path); !os.IsNotExist(err) {
		t.Fatalf("config was written after a failed test: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.home, "secrets.env")); !os.IsNotExist(err) {
		t.Fatalf("secrets.env was written after a failed test: %v", err)
	}
	if _, err := os.Stat(agenthome.Paths(store.home, "coder").Dir); !os.IsNotExist(err) {
		t.Fatalf("agent home was created after a failed test: %v", err)
	}
}

// TestSetupSaveRejectsBadInput covers validation_failed: nothing is written and
// the provider is not called.
func TestSetupSaveRejectsBadInput(t *testing.T) {
	cases := []struct {
		name   string
		field  string
		mutate func(map[string]any)
	}{
		{name: "provider name", field: "provider.name", mutate: func(b map[string]any) {
			setupPart(b, "provider")["name"] = "gemini"
		}},
		{name: "api key", field: "provider.api_key", mutate: func(b map[string]any) {
			setupPart(b, "provider")["api_key"] = ""
		}},
		{name: "base url", field: "provider.base_url", mutate: func(b map[string]any) {
			setupPart(b, "provider")["base_url"] = "ftp://api.openai.com"
		}},
		{name: "model", field: "provider.model", mutate: func(b map[string]any) {
			setupPart(b, "provider")["model"] = ""
		}},
		{name: "agent name", field: "agent.name", mutate: func(b map[string]any) {
			setupPart(b, "agent")["name"] = "Coder"
		}},
		{name: "template", field: "agent.template", mutate: func(b map[string]any) {
			setupPart(b, "agent")["template"] = "nope"
		}},
		{name: "permission", field: "agent.permission", mutate: func(b map[string]any) {
			setupPart(b, "agent")["permission"] = "always"
		}},
		{name: "agent md", field: "persona.agent_md", mutate: func(b map[string]any) {
			setupPart(b, "persona")["agent_md"] = "  "
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := setupMissingConfigStore(t)
			var tests int
			daemon := &fakeDaemonStore{info: DaemonInfo{PID: 1, RestartSupported: true}}
			s, cookie, csrf, _ := newSetupTestServer(t, store, func(context.Context, ProviderTest) (TestResult, error) {
				tests++
				return TestResult{OK: true}, nil
			}, daemon)
			body := newSetupBody()
			tc.mutate(body)

			response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
			if response.code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, body = %q, want 422", response.code, response.body)
			}
			envelope := decodeError(t, response)
			if envelope.Error.Code != "validation_failed" {
				t.Fatalf("code = %q, want validation_failed", envelope.Error.Code)
			}
			if len(envelope.Error.Details) == 0 || envelope.Error.Details[0]["field"] != tc.field {
				t.Fatalf("details = %v, want field %q", envelope.Error.Details, tc.field)
			}
			if tests != 0 || daemon.restarts != 0 {
				t.Fatalf("provider tests/restarts = %d/%d, want 0/0", tests, daemon.restarts)
			}
			if _, err := os.Stat(store.path); !os.IsNotExist(err) {
				t.Fatalf("config was written after invalid input: %v", err)
			}
		})
	}
}

// TestSetupSaveAgentExists covers the invalid_state refusal when the agent name
// is already configured or its home already exists.
func TestSetupSaveAgentExists(t *testing.T) {
	t.Run("config table", func(t *testing.T) {
		const raw = "[agents.coder]\nmodel = \"m\"\n"
		store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
		var tests int
		s, cookie, csrf, _ := newSetupTestServer(t, store, func(context.Context, ProviderTest) (TestResult, error) {
			tests++
			return TestResult{OK: true}, nil
		}, nil)
		body := newSetupBody()
		body["expected_version"] = shaHex([]byte(raw))
		response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
		if response.code != http.StatusConflict {
			t.Fatalf("status = %d, body = %q", response.code, response.body)
		}
		if envelope := decodeError(t, response); envelope.Error.Code != "invalid_state" {
			t.Fatalf("code = %q, want invalid_state", envelope.Error.Code)
		}
		if tests != 0 {
			t.Fatalf("provider tests = %d, want 0", tests)
		}
		if got := readSetupFile(t, store.path); got != raw {
			t.Fatalf("config changed:\n%s", got)
		}
	})

	t.Run("agent home", func(t *testing.T) {
		store := setupMissingConfigStore(t)
		dir := agenthome.Paths(store.home, "coder").Dir
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		s, cookie, csrf, _ := newSetupTestServer(t, store, nil, nil)
		response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, newSetupBody()), cookie, csrf)
		if response.code != http.StatusConflict {
			t.Fatalf("status = %d, body = %q", response.code, response.body)
		}
		if envelope := decodeError(t, response); envelope.Error.Code != "invalid_state" {
			t.Fatalf("code = %q, want invalid_state", envelope.Error.Code)
		}
	})
}

// TestSetupSaveUserFile covers the USER.md rules: written only when missing and
// limited to 2,000 characters.
func TestSetupSaveUserFile(t *testing.T) {
	t.Run("written when missing", func(t *testing.T) {
		store := setupMissingConfigStore(t)
		s, cookie, csrf, _ := newSetupTestServer(t, store, nil, nil)
		response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, newSetupBody()), cookie, csrf)
		if response.code != http.StatusAccepted {
			t.Fatalf("status = %d, body = %q", response.code, response.body)
		}
		if got := readSetupFile(t, agenthome.SharedUser(store.home)); got != "- pref\n" {
			t.Fatalf("USER.md = %q", got)
		}
	})

	t.Run("never overwritten", func(t *testing.T) {
		store := setupMissingConfigStore(t)
		path := agenthome.SharedUser(store.home)
		if err := os.WriteFile(path, []byte("keep me\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		s, cookie, csrf, _ := newSetupTestServer(t, store, nil, nil)
		response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, newSetupBody()), cookie, csrf)
		if response.code != http.StatusAccepted {
			t.Fatalf("status = %d, body = %q", response.code, response.body)
		}
		if got := readSetupFile(t, path); got != "keep me\n" {
			t.Fatalf("USER.md was overwritten: %q", got)
		}
	})

	t.Run("over limit", func(t *testing.T) {
		store := setupMissingConfigStore(t)
		s, cookie, csrf, _ := newSetupTestServer(t, store, nil, nil)
		body := newSetupBody()
		setupPart(body, "persona")["user_md"] = strings.Repeat("x", agenthome.UserLimit+1)
		response := serveSetup(t, s, http.MethodPost, setupBodyJSON(t, body), cookie, csrf)
		if response.code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, body = %q, want 422", response.code, response.body)
		}
		envelope := decodeError(t, response)
		if envelope.Error.Code != "over_limit" {
			t.Fatalf("code = %q, want over_limit", envelope.Error.Code)
		}
		if field := envelope.Error.Details[0]["field"]; field != "persona.user_md" {
			t.Fatalf("field = %v, want persona.user_md", field)
		}
		if _, err := os.Stat(agenthome.SharedUser(store.home)); !os.IsNotExist(err) {
			t.Fatalf("USER.md was written despite over_limit: %v", err)
		}
	})
}

// TestSetupRoutesNeedLogin covers the owner-cookie authorization of both
// operations.
func TestSetupRoutesNeedLogin(t *testing.T) {
	store := setupMissingConfigStore(t)
	s, _, _, _ := newSetupTestServer(t, store, nil, nil)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := serveSetup(t, s, method, "", nil, "")
		if response.code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, body = %q", method, response.code, response.body)
		}
	}
}
