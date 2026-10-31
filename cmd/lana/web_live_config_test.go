package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// lastModel returns the model of the last chat request the provider saw.
func (p *promptCaptureProvider) lastModel() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return ""
	}
	return p.requests[len(p.requests)-1].Model
}

// lastReasoningEffort returns the effort of the last chat request.
func (p *promptCaptureProvider) lastReasoningEffort() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return ""
	}
	return p.requests[len(p.requests)-1].ReasoningEffort
}

func writeLiveConfig(t *testing.T, home, raw string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func liveWebRuntime(t *testing.T, home string, script mock.Script) (*lana.Runtime, *promptCaptureProvider) {
	t.Helper()
	capture := &promptCaptureProvider{llm: mock.New(script)}
	rt, err := lana.New(context.Background(), lana.Options{
		Home:      home,
		Providers: map[string]core.Provider{"mock": capture},
	})
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt, capture
}

type liveConfigState struct {
	Version         string `json:"version"`
	LoadedVersion   string `json:"loaded_version"`
	RestartRequired bool   `json:"restart_required"`
}

func liveStrPtr(s string) *string { return &s }

func getLiveConfigState(t *testing.T, url string, cookie *http.Cookie) liveConfigState {
	t.Helper()
	resp, body := webDo(t, http.MethodGet, url+"/v1/config", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/config status = %d, body = %q", resp.StatusCode, body)
	}
	var state liveConfigState
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

// TestLiveMCPAttachNextTurn covers AC-TFB-013: attaching dbx to lana through the
// web applies to the running registry, so the next turn offers its tools.
func TestLiveMCPAttachNextTurn(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[mcp.dbx]
command = %q
args = ["-test.run=^$"]
env = { LANA_MCP_TEST_SERVER = "1" }

[agents.lana]
provider = "mock"
model = "m"

[[routes]]
channel = "web"
agent = "lana"
`, exe)
	home := t.TempDir()
	writeLiveConfig(t, home, raw)
	text := "ok"
	rt, capture := liveWebRuntime(t, home, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: liveStrPtr("end_turn")}}},
	}})
	_, ts, cookie, csrf := webServer(t, rt, home)

	state := getLiveConfigState(t, ts.URL, cookie)
	body := `{"mcp":["dbx"],"expected_version":"` + state.Version + `"}`
	resp, data := webDo(t, http.MethodPut, ts.URL+"/v1/agents/lana", body, cookie, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT agent status = %d, body = %q", resp.StatusCode, data)
	}
	var saved putAgentRestart
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.RestartRequired {
		t.Fatalf("attach answered restart_required true: %q", data)
	}

	info, err := rt.CreateChannelSession(context.Background(), "web", "owner", "lana")
	if err != nil {
		t.Fatalf("CreateChannelSession: %v", err)
	}
	if _, err := rt.Run(context.Background(), lana.Request{Prompt: "hi", SessionID: info.ID}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	offered := toolSpecNames(capture.lastTools())
	if !containsToolName(offered, "mcp__dbx__echo") {
		t.Fatalf("offered tools = %v, mcp__dbx__echo missing", offered)
	}
}

// putAgentRestart is the restart flag of the agent save responses.
type putAgentRestart struct {
	RestartRequired bool `json:"restart_required"`
}

// TestLiveAgentModelNextTurn covers AC-TFB-014: a web model change reaches the
// next turn of a new session, of an existing session, and stays overridden by
// an explicit owner model choice.
func TestLiveAgentModelNextTurn(t *testing.T) {
	raw := `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.lana]
provider = "mock"
model = "old-model"

[[routes]]
channel = "web"
agent = "lana"
`
	home := t.TempDir()
	writeLiveConfig(t, home, raw)
	text := "ok"
	rt, capture := liveWebRuntime(t, home, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: liveStrPtr("end_turn")}}},
		{Events: []mock.Event{{Text: &text}, {Stop: liveStrPtr("end_turn")}}},
		{Events: []mock.Event{{Text: &text}, {Stop: liveStrPtr("end_turn")}}},
		{Events: []mock.Event{{Text: &text}, {Stop: liveStrPtr("end_turn")}}},
	}})
	_, ts, cookie, csrf := webServer(t, rt, home)

	info, err := rt.CreateChannelSession(context.Background(), "web", "owner", "lana")
	if err != nil {
		t.Fatalf("CreateChannelSession: %v", err)
	}
	runTurn := func() {
		t.Helper()
		if _, err := rt.Run(context.Background(), lana.Request{Prompt: "hi", SessionID: info.ID}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	saveModel := func(model string) {
		t.Helper()
		state := getLiveConfigState(t, ts.URL, cookie)
		body := `{"model":"` + model + `","expected_version":"` + state.Version + `"}`
		resp, data := webDo(t, http.MethodPut, ts.URL+"/v1/agents/lana", body, cookie, csrf)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT agent status = %d, body = %q", resp.StatusCode, data)
		}
		var saved putAgentRestart
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.RestartRequired {
			t.Fatalf("model save answered restart_required true: %q", data)
		}
	}

	runTurn()
	if got := capture.lastModel(); got != "old-model" {
		t.Fatalf("first turn model = %q", got)
	}

	saveModel("new-model")
	runTurn()
	if got := capture.lastModel(); got != "new-model" {
		t.Fatalf("next turn model = %q, want new-model", got)
	}

	// A SetAgent event records no model, so the config model still applies.
	if err := rt.SetAgent(context.Background(), info.ID, "lana"); err != nil {
		t.Fatalf("SetAgent: %v", err)
	}
	runTurn()
	if got := capture.lastModel(); got != "new-model" {
		t.Fatalf("turn after SetAgent model = %q, want new-model", got)
	}

	// An explicit owner choice keeps its model over a later web change.
	if err := rt.SetModel(context.Background(), info.ID, "mock:owner-model"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	saveModel("newer-model")
	runTurn()
	if got := capture.lastModel(); got != "owner-model" {
		t.Fatalf("turn after SetModel model = %q, want owner-model", got)
	}
}

// TestLiveApplyBookkeeping covers the loaded-version rule: a live agent save
// keeps the generic restart banner off, a pending provider change keeps it on,
// and a later live save does not hide that pending change.
func TestLiveApplyBookkeeping(t *testing.T) {
	raw := `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.lana]
provider = "mock"
model = "old-model"

[[routes]]
channel = "web"
agent = "lana"
`
	home := t.TempDir()
	writeLiveConfig(t, home, raw)
	rt, _ := liveWebRuntime(t, home, mock.Script{})
	_, ts, cookie, csrf := webServer(t, rt, home)

	start := getLiveConfigState(t, ts.URL, cookie)
	if start.RestartRequired || start.LoadedVersion != start.Version {
		t.Fatalf("startup state = %+v", start)
	}

	saveAgent := func(model string) liveConfigState {
		t.Helper()
		state := getLiveConfigState(t, ts.URL, cookie)
		body := `{"model":"` + model + `","expected_version":"` + state.Version + `"}`
		resp, data := webDo(t, http.MethodPut, ts.URL+"/v1/agents/lana", body, cookie, csrf)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT agent status = %d, body = %q", resp.StatusCode, data)
		}
		var saved putAgentRestart
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.RestartRequired {
			t.Fatalf("agent save answered restart_required true: %q", data)
		}
		return getLiveConfigState(t, ts.URL, cookie)
	}

	state := saveAgent("new-model")
	if state.RestartRequired {
		t.Fatalf("banner is on after a live agent save: %+v", state)
	}
	if state.LoadedVersion != state.Version {
		t.Fatalf("loaded version = %q, file version = %q", state.LoadedVersion, state.Version)
	}

	// A provider save still needs a restart (AC-TFB-015).
	state = getLiveConfigState(t, ts.URL, cookie)
	body := `{"kind":"openai_compat","base_url":"https://example.com/v2","expected_version":"` + state.Version + `"}`
	resp, data := webDo(t, http.MethodPut, ts.URL+"/v1/providers/mock", body, cookie, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT provider status = %d, body = %q", resp.StatusCode, data)
	}
	var providerSaved struct {
		RestartRequired bool `json:"restart_required"`
	}
	if err := json.Unmarshal(data, &providerSaved); err != nil {
		t.Fatal(err)
	}
	if !providerSaved.RestartRequired {
		t.Fatalf("provider save answered restart_required false: %q", data)
	}
	pending := getLiveConfigState(t, ts.URL, cookie)
	if !pending.RestartRequired || pending.LoadedVersion == pending.Version {
		t.Fatalf("provider restart banner = %+v", pending)
	}

	// A live agent save keeps the pending provider change pending.
	state = saveAgent("newer-model")
	if !state.RestartRequired {
		t.Fatalf("live save hid the pending provider change: %+v", state)
	}
	if state.LoadedVersion == state.Version {
		t.Fatalf("loaded version moved while a provider change is pending: %+v", state)
	}
	if strings.TrimSpace(state.Version) == "" {
		t.Fatal("empty config version")
	}
}
