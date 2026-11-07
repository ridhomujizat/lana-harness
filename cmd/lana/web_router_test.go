package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
	"github.com/ridhomujizat/lana-harness/providers/anthropic"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/web"
)

const routerWebConfig = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[combos.coding]
models = ["mock/m"]

[agents.lana]
model = "coding"

[[routes]]
channel = "web"
agent = "lana"
`

func routerMockScript(n int) mock.Script {
	text := "pong"
	responses := make([]mock.Response, 0, n)
	for range n {
		responses = append(responses, mock.Response{Events: []mock.Event{{Text: &text}, {Stop: liveStrPtr("end_turn")}}})
	}
	return mock.Script{Responses: responses}
}

// proxyDo calls the proxy without a session cookie, with an optional bearer
// key.
func proxyDo(t *testing.T, method, url, body, key string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = webTestAddr
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, data
}

// waitFor polls cond for up to 5 seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// AC-RTR-021, AC-RTR-022, REQ-RTR-015: the proxy is mounted and unmounted
// live by PUT /v1/router/proxy, needs a live proxy key, records its calls
// with source "proxy", and refuses a revoked key.
func TestRouterProxyLive(t *testing.T) {
	home := t.TempDir()
	writeLiveConfig(t, home, routerWebConfig)
	rt, _ := liveWebRuntime(t, home, routerMockScript(4))
	_, ts, cookie, csrf := webServer(t, rt, home)

	if resp, data := proxyDo(t, http.MethodGet, ts.URL+"/router/v1/models", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("proxy off = %d %s", resp.StatusCode, data)
	}
	setProxy := func(enabled bool) {
		t.Helper()
		state := getLiveConfigState(t, ts.URL, cookie)
		body, _ := json.Marshal(map[string]any{"enabled": enabled, "expected_version": state.Version})
		resp, data := webDo(t, http.MethodPut, ts.URL+"/v1/router/proxy", string(body), cookie, csrf)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"restart_required":false`) {
			t.Fatalf("PUT proxy %v = %d %s", enabled, resp.StatusCode, data)
		}
	}
	setProxy(true)

	// AC-RTR-021: no key or a wrong key is 401 with an OpenAI-style body.
	for _, key := range []string{"", "lrk_wrong"} {
		resp, data := proxyDo(t, http.MethodGet, ts.URL+"/router/v1/models", "", key)
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(data), `"code":"invalid_api_key"`) {
			t.Fatalf("key %q = %d %s", key, resp.StatusCode, data)
		}
	}

	resp, data := webDo(t, http.MethodPost, ts.URL+"/v1/router/keys", `{"name":"cli"}`, cookie, csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create key = %d %s", resp.StatusCode, data)
	}
	var created web.RouterKeyCreated
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Key, "lrk_") || len(created.Key) != 47 || created.Prefix != created.Key[:8] || !strings.HasPrefix(created.ID, "k_") {
		t.Fatalf("created = %+v", created)
	}

	resp, data = proxyDo(t, http.MethodGet, ts.URL+"/router/v1/models", "", created.Key)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"id":"coding"`) {
		t.Fatalf("models with key = %d %s", resp.StatusCode, data)
	}
	resp, data = proxyDo(t, http.MethodPost, ts.URL+"/router/v1/chat/completions",
		`{"model":"coding","messages":[{"role":"user","content":"ping"}]}`, created.Key)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), "pong") || resp.Header.Get("X-Lana-Served-By") != "mock/m" {
		t.Fatalf("chat = %d %s served-by %q", resp.StatusCode, data, resp.Header.Get("X-Lana-Served-By"))
	}

	// The call is recorded with source proxy, and the key's last use is set.
	waitFor(t, "a proxy call record", func() bool {
		resp, data := webDo(t, http.MethodGet, ts.URL+"/v1/router/calls?source=proxy", "", cookie, "")
		return resp.StatusCode == http.StatusOK && strings.Contains(string(data), `"requested":"coding"`)
	})
	waitFor(t, "the key's last use", func() bool {
		_, data := webDo(t, http.MethodGet, ts.URL+"/v1/router/keys", "", cookie, "")
		return strings.Contains(string(data), `"id":"`+created.ID+`"`) && !strings.Contains(string(data), `"last_used_at":null`)
	})

	// AC-RTR-022: a revoked key is refused.
	resp, data = webDo(t, http.MethodDelete, ts.URL+"/v1/router/keys/"+created.ID, "", cookie, csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", resp.StatusCode, data)
	}
	if resp, data := proxyDo(t, http.MethodGet, ts.URL+"/router/v1/models", "", created.Key); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked key = %d %s", resp.StatusCode, data)
	}

	// Switching the proxy off unmounts it without a restart.
	setProxy(false)
	if resp, data := proxyDo(t, http.MethodGet, ts.URL+"/router/v1/models", "", created.Key); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("proxy off again = %d %s", resp.StatusCode, data)
	}
}

// The Router page reads the runtime router: overview, test, usage, calls,
// limits, and models.
func TestRouterBackendEndpoints(t *testing.T) {
	home := t.TempDir()
	writeLiveConfig(t, home, routerWebConfig)
	rt, _ := liveWebRuntime(t, home, routerMockScript(2))
	_, ts, cookie, csrf := webServer(t, rt, home)

	resp, data := webDo(t, http.MethodPost, ts.URL+"/v1/router/test", `{"address":"mock/m"}`, cookie, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"ok":true`) {
		t.Fatalf("test = %d %s", resp.StatusCode, data)
	}
	resp, data = webDo(t, http.MethodPost, ts.URL+"/v1/router/test", `{"address":"nope/x"}`, cookie, csrf)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown test = %d %s", resp.StatusCode, data)
	}
	waitFor(t, "the test call record", func() bool {
		_, data := webDo(t, http.MethodGet, ts.URL+"/v1/router/usage?range=24h", "", cookie, "")
		return strings.Contains(string(data), `"source":"test"`)
	})
	_, data = webDo(t, http.MethodGet, ts.URL+"/v1/router/usage?range=24h", "", cookie, "")
	var usage struct {
		Range  string `json:"range"`
		Series []any  `json:"series"`
		Totals struct {
			Calls int64 `json:"calls"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(data, &usage); err != nil {
		t.Fatal(err)
	}
	if usage.Range != "24h" || usage.Totals.Calls != 1 || len(usage.Series) < 24 {
		t.Fatalf("usage = %+v", usage)
	}
	_, data = webDo(t, http.MethodGet, ts.URL+"/v1/router/usage?range=7d", "", cookie, "")
	if err := json.Unmarshal(data, &usage); err != nil {
		t.Fatal(err)
	}
	if len(usage.Series) < 7 || len(usage.Series) > 8 {
		t.Fatalf("7d series has %d buckets", len(usage.Series))
	}

	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/router/calls?limit=1", "", cookie, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"source":"test"`) || !strings.Contains(string(data), `"next_before":0`) {
		t.Fatalf("calls = %d %s", resp.StatusCode, data)
	}

	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/router/limits", "", cookie, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"provider":"mock"`) {
		t.Fatalf("limits = %d %s", resp.StatusCode, data)
	}

	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/router", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overview = %d %s", resp.StatusCode, data)
	}
	var overview struct {
		Proxy struct {
			Enabled bool   `json:"enabled"`
			BaseURL string `json:"base_url"`
		} `json:"proxy"`
		Choices []struct {
			Value string `json:"value"`
			Group string `json:"group"`
		} `json:"choices"`
		Combos []struct {
			Name   string   `json:"name"`
			UsedBy []string `json:"used_by"`
		} `json:"combos"`
	}
	if err := json.Unmarshal(data, &overview); err != nil {
		t.Fatal(err)
	}
	if overview.Proxy.Enabled || overview.Proxy.BaseURL != "http://"+webTestAddr+"/router/v1" {
		t.Fatalf("proxy = %+v", overview.Proxy)
	}
	if len(overview.Choices) == 0 || overview.Choices[0].Value != "coding" || overview.Choices[0].Group != "Combos" {
		t.Fatalf("choices = %+v", overview.Choices)
	}
	if len(overview.Combos) != 1 || strings.Join(overview.Combos[0].UsedBy, ",") != "lana" {
		t.Fatalf("combos = %+v", overview.Combos)
	}

	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/router/models?provider=mock", "", cookie, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"provider":"mock"`) {
		t.Fatalf("models = %d %s", resp.StatusCode, data)
	}
	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/router/claude-agent", "", cookie, "")
	// API-CCR-003: no Claude Agent is configured, so none is listed and the
	// runtime is not probed.
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"configured_agents":[]`) || !strings.Contains(string(data), `"error":""`) {
		t.Fatalf("claude agent = %d %s", resp.StatusCode, data)
	}
}

// fakeOpenAI is an OpenAI-compatible chat server that records the bearer of
// each call.
type fakeOpenAI struct {
	mu      sync.Mutex
	bearers []string
	models  []string
}

func (f *fakeOpenAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.bearers = append(f.bearers, r.Header.Get("Authorization"))
	f.models = append(f.models, body.Model)
	f.mu.Unlock()
	if r.URL.Path == "/v1/models" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"from openai"}}]}` + "\n\n" +
		`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"))
}

func (f *fakeOpenAI) last() (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bearers) == 0 {
		return "", ""
	}
	return f.bearers[len(f.bearers)-1], f.models[len(f.models)-1]
}

// AC-RTR-011: the owner adds [providers.oa] kind openai with a key and
// switches the agent to oa/gpt-5-mini from the dashboard; both saves answer
// restart_required false and the next turn uses the new provider with the
// key written to secrets.env.
func TestRouterProviderAddedLive(t *testing.T) {
	t.Setenv("OA_API_KEY", "")
	upstream := &fakeOpenAI{}
	server := httptest.NewServer(upstream)
	defer server.Close()

	home := t.TempDir()
	writeLiveConfig(t, home, routerWebConfig)
	rt, _ := liveWebRuntime(t, home, routerMockScript(2))
	_, ts, cookie, csrf := webServer(t, rt, home)

	const key = "sk-live-openai-key"
	state := getLiveConfigState(t, ts.URL, cookie)
	body := `{"kind":"openai","base_url":"` + server.URL + `/v1","api_key":"` + key + `","expected_version":"` + state.Version + `"}`
	resp, data := webDo(t, http.MethodPut, ts.URL+"/v1/providers/oa", body, cookie, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"restart_required":false`) {
		t.Fatalf("PUT provider = %d %s", resp.StatusCode, data)
	}
	// The save-time test used the openai kind's default test model.
	if bearer, model := upstream.last(); bearer != "Bearer "+key || model != models.TestModel("openai") {
		t.Fatalf("save test bearer = %q model = %q", bearer, model)
	}

	state = getLiveConfigState(t, ts.URL, cookie)
	if state.RestartRequired {
		t.Fatalf("restart banner after a provider save: %+v", state)
	}
	body = `{"provider":"","model":"oa/gpt-5-mini","expected_version":"` + state.Version + `"}`
	resp, data = webDo(t, http.MethodPut, ts.URL+"/v1/agents/lana", body, cookie, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"restart_required":false`) {
		t.Fatalf("PUT agent = %d %s", resp.StatusCode, data)
	}

	info, err := rt.CreateChannelSession(context.Background(), "web", "owner", "lana")
	if err != nil {
		t.Fatal(err)
	}
	result, err := rt.Run(context.Background(), lana.Request{Prompt: "hi", SessionID: info.ID})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result.Text, "from openai") {
		t.Fatalf("turn text = %q", result.Text)
	}
	if bearer, model := upstream.last(); bearer != "Bearer "+key || model != "gpt-5-mini" {
		t.Fatalf("turn bearer = %q model = %q", bearer, model)
	}
	secrets, err := os.ReadFile(filepath.Join(home, "secrets.env"))
	if err != nil || !strings.Contains(string(secrets), "OA_API_KEY="+key) {
		t.Fatalf("secrets.env = %q, %v", secrets, err)
	}
}

// Spec 37 failure table: a provider in use without a key logs a warning at
// boot and the runtime starts; its target then fails with missing_secret.
func TestRouterBootWithMissingKey(t *testing.T) {
	t.Setenv("DS_API_KEY", "")
	home := t.TempDir()
	writeLiveConfig(t, home, `[providers.ds]
kind = "deepseek"

[agents.lana]
model = "ds/deepseek-flash"
`)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	rt, err := lana.New(context.Background(), lana.Options{Home: home})
	if err != nil {
		t.Fatalf("boot with a missing key failed: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if text := logs.String(); !strings.Contains(text, "level=WARN") || !strings.Contains(text, "provider=ds") || !strings.Contains(text, "env=DS_API_KEY") {
		t.Fatalf("boot log = %q", text)
	}
	backend := newWebBackend(rt, home, nil, context.Background())
	result, err := backend.RouterTest(context.Background(), "ds/deepseek-flash")
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Class != "missing_secret" || !strings.Contains(result.Message, "DS_API_KEY") {
		t.Fatalf("test result = %+v", result)
	}
}

// The provider test builds its adapter through the router's kind registry:
// kind anthropic calls the Messages API with x-api-key.
func TestProviderTestUsesKindAdapter(t *testing.T) {
	if models.DefaultBaseURL("anthropic") != anthropic.DefaultBaseURL {
		t.Fatalf("anthropic default base URL differs: %q vs %q", models.DefaultBaseURL("anthropic"), anthropic.DefaultBaseURL)
	}
	const key = "sk-ant-inline"
	var path, apiKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, apiKey = r.URL.Path, r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	defer server.Close()
	backend := &webBackend{home: t.TempDir()}
	result, err := backend.TestProvider(context.Background(), web.ProviderTest{
		Kind:    "anthropic",
		BaseURL: server.URL,
		Model:   "claude-haiku-5-5",
		APIKey:  core.NewSecret(key),
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/messages" || apiKey != key {
		t.Fatalf("anthropic request path = %q x-api-key = %q", path, apiKey)
	}
	if result.OK || result.Status != http.StatusUnauthorized || strings.Contains(result.Message, key) {
		t.Fatalf("result = %+v", result)
	}
}

// statusRuntime is a core.AgentRuntime that only answers Status and counts
// the calls.
type statusRuntime struct {
	mu     sync.Mutex
	calls  int
	status core.RuntimeStatus
}

func (s *statusRuntime) RunTurn(context.Context, core.RuntimeTurn) error { return nil }
func (s *statusRuntime) Interrupt(string) error                          { return nil }
func (s *statusRuntime) Close(string)                                    {}
func (s *statusRuntime) Shutdown(context.Context) error                  { return nil }
func (s *statusRuntime) Status(context.Context) core.RuntimeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.status
}

func (s *statusRuntime) statusCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// API-CCR-003: without a Claude Agent the status lists none and never asks
// the runtime; with Claude Agents it lists them sorted and reports the
// runtime's status.
func TestClaudeAgentStatus(t *testing.T) {
	t.Run("no claude agents", func(t *testing.T) {
		home := t.TempDir()
		writeLiveConfig(t, home, routerWebConfig)
		rt, _ := liveWebRuntime(t, home, routerMockScript(1))
		fake := &statusRuntime{status: core.RuntimeStatus{Binary: "/bin/claude"}}
		rt.SetAgentRuntime(lana.ClaudeCodeRuntime, fake)
		_, ts, cookie, _ := webServer(t, rt, home)

		resp, data := webDo(t, http.MethodGet, ts.URL+"/v1/router/claude-agent", "", cookie, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET claude-agent = %d %s", resp.StatusCode, data)
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if agents, ok := got["configured_agents"].([]any); !ok || len(agents) != 0 {
			t.Fatalf("configured_agents = %s", data)
		}
		if got["binary"] != "" || got["error"] != "" {
			t.Fatalf("status = %s", data)
		}
		if n := fake.statusCalls(); n != 0 {
			t.Fatalf("runtime Status called %d times without a Claude Agent", n)
		}
	})

	t.Run("fake runtime", func(t *testing.T) {
		home := t.TempDir()
		workspace := t.TempDir()
		writeLiveConfig(t, home, routerWebConfig+`
[agents.zeta]
runtime = "claude-code"
model = "opus"
workspace = "`+workspace+`"

[agents.claude]
runtime = "claude-code"
binary = "/nonexistent/claude"
workspace = "`+workspace+`"
`)
		rt, _ := liveWebRuntime(t, home, routerMockScript(1))
		fake := &statusRuntime{status: core.RuntimeStatus{
			Binary: "/usr/local/bin/claude", Version: "2.1.319", VersionOK: true,
			LoggedIn: true, AuthMethod: "claude.ai", Running: 1, Queued: 2, MaxConcurrent: 2,
		}}
		rt.SetAgentRuntime(lana.ClaudeCodeRuntime, fake)
		_, ts, cookie, _ := webServer(t, rt, home)

		resp, data := webDo(t, http.MethodGet, ts.URL+"/v1/router/claude-agent", "", cookie, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET claude-agent = %d %s", resp.StatusCode, data)
		}
		var got web.ClaudeAgentStatus
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		want := web.ClaudeAgentStatus{
			ConfiguredAgents: []string{"claude", "zeta"},
			Binary:           "/usr/local/bin/claude", Version: "2.1.319", VersionOK: true,
			LoggedIn: true, AuthMethod: "claude.ai", Running: 1, Queued: 2, MaxConcurrent: 2,
		}
		if strings.Join(got.ConfiguredAgents, ",") != "claude,zeta" || got.Binary != want.Binary ||
			got.Version != want.Version || !got.VersionOK || !got.LoggedIn || got.AuthMethod != want.AuthMethod ||
			got.Running != 1 || got.Queued != 2 || got.MaxConcurrent != 2 || got.Error != "" {
			t.Fatalf("status = %+v", got)
		}
		if fake.statusCalls() != 1 {
			t.Fatalf("runtime Status calls = %d", fake.statusCalls())
		}
	})
}

// AC-CCR-010 (backend side): creating Claude Agent "claude" with model opus
// through the daemon writes runtime = "claude-code", model = "opus", and no
// provider; the agent list and the settings view show the runtime.
func TestCreateClaudeAgentThroughDaemon(t *testing.T) {
	home := t.TempDir()
	writeLiveConfig(t, home, routerWebConfig)
	rt, _ := liveWebRuntime(t, home, routerMockScript(1))
	_, ts, cookie, csrf := webServer(t, rt, home)

	resp, data := webDo(t, http.MethodPost, ts.URL+"/v1/agents",
		`{"name":"claude","runtime":"claude-code","model":"opus","auth":"subscription","permission":"ask","allow_scheduled":false}`, cookie, csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d %s", resp.StatusCode, data)
	}
	raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Agents map[string]map[string]any `toml:"agents"`
	}
	if err := toml.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	claude := file.Agents["claude"]
	if claude["runtime"] != "claude-code" || claude["model"] != "opus" {
		t.Fatalf("config.toml claude table = %+v\n%s", claude, raw)
	}
	if _, ok := claude["provider"]; ok {
		t.Fatalf("config.toml claude table has a provider: %+v", claude)
	}

	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/agents", "", cookie, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"engine_label":"Claude Agent"`) {
		t.Fatalf("agents = %d %s", resp.StatusCode, data)
	}
	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/config", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config = %d %s", resp.StatusCode, data)
	}
	var cfg struct {
		Settings struct {
			Agents map[string]web.ConfigAgent `json:"agents"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Settings.Agents["claude"]; got.Runtime != "claude-code" || got.Model != "opus" || got.Provider != "" {
		t.Fatalf("settings claude = %+v", got)
	}
}
