package web

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

// fakeClaudeRouterBackend adds the Claude Code part to the fake backend.
type fakeClaudeRouterBackend struct {
	*fakeRouterBackend
	installed bool
}

func (f fakeClaudeRouterBackend) ClaudeCodeInstalled(context.Context) bool { return f.installed }

const claudeRouterConfig = routerTestConfig + `
[combos.claude]
models = ["claude-code/sonnet", "deepseek/deepseek-flash"]

[agents.bima]
model = "claude-code/opus"

[agents.legacy]
runtime = "claude-code"
model = "opus"
`

func newClaudeRouterServer(t *testing.T, installed bool) (*Server, *testLiveConfigStore, *fakeRouterBackend, *http.Cookie, string) {
	t.Helper()
	s, store, fake, cookie, csrf := newRouterTestServer(t, claudeRouterConfig)
	fake.lists[ClaudeCodeProvider] = RouterModelList{Provider: ClaudeCodeProvider, Models: []RouterModel{
		{ID: "opus", Address: "claude-code/opus", Name: "Claude Code · Opus (latest)", Source: "alias"},
		{ID: "claude-opus-5-5", Address: "claude-code/claude-opus-5-5", Name: "Claude Code · Opus 5.5", Source: "claude_code"},
		{ID: "my-model", Address: "claude-code/my-model", Name: "Claude Code · my-model", Source: "custom"},
	}}
	s.SetRouterBackend(fakeClaudeRouterBackend{fakeRouterBackend: fake, installed: installed})
	return s, store, fake, cookie, csrf
}

// API-CCT-004: GET /v1/router lists the Claude Code models in their own group
// when the binary is found, and not otherwise.
func TestRouterClaudeCodeChoices(t *testing.T) {
	for _, installed := range []bool{true, false} {
		s, _, _, cookie, csrf := newClaudeRouterServer(t, installed)
		response := serveMCP(t, s, http.MethodGet, "/v1/router", "", cookie, csrf)
		if response.code != http.StatusOK {
			t.Fatalf("status = %d %s", response.code, response.body)
		}
		var body routerOverviewResponse
		response.json(t, &body)
		var claude []routerChoiceJSON
		for _, choice := range body.Choices {
			if choice.Group == "Claude Code" {
				claude = append(claude, choice)
			}
		}
		if !installed {
			if len(claude) != 0 {
				t.Fatalf("choices without the binary = %+v", claude)
			}
			continue
		}
		if len(claude) != 3 || claude[0] != (routerChoiceJSON{Value: "claude-code/opus", Label: "Claude Code · Opus (latest)", Group: "Claude Code"}) ||
			claude[1].Label != "Claude Code · Opus 5.5" || claude[2].Label != "Claude Code · my-model" {
			t.Fatalf("claude choices = %+v", claude)
		}
		for _, provider := range body.Providers {
			if provider.Name == ClaudeCodeProvider {
				t.Fatal("claude-code listed as a configured provider")
			}
		}
	}
}

// GET /v1/router/models?provider=claude-code returns the rows with their
// source and users; a legacy runtime agent uses its claude-code address.
func TestRouterClaudeCodeModels(t *testing.T) {
	s, _, _, cookie, csrf := newClaudeRouterServer(t, true)
	response := serveMCP(t, s, http.MethodGet, "/v1/router/models?provider=claude-code", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d %s", response.code, response.body)
	}
	var list RouterModelList
	response.json(t, &list)
	if list.Provider != ClaudeCodeProvider || len(list.Models) != 3 {
		t.Fatalf("list = %+v", list)
	}
	opus := list.Models[0]
	if opus.Source != "alias" || strings.Join(opus.UsedBy.Agents, ",") != "bima,legacy" || len(opus.UsedBy.Combos) != 0 {
		t.Fatalf("opus = %+v", opus)
	}
	if list.Models[2].Source != "custom" {
		t.Fatalf("custom = %+v", list.Models[2])
	}
}

// PUT /v1/router/claude-code/models writes [router] claude_code_models live,
// dedupes, and answers 422 for an invalid ID or 409 for a stale version.
func TestRouterPutClaudeCodeModels(t *testing.T) {
	s, store, _, cookie, csrf := newClaudeRouterServer(t, true)
	version := shaHex([]byte(claudeRouterConfig))
	saved := serveMCP(t, s, http.MethodPut, "/v1/router/claude-code/models",
		`{"models":["claude-opus-4-1"," my-model ","claude-opus-4-1"],"expected_version":"`+version+`"}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save = %d %s", saved.code, saved.body)
	}
	var response claudeCodeModelsResponse
	saved.json(t, &response)
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(response.Models, ",") != "claude-opus-4-1,my-model" || response.Version != shaHex(data) || response.RestartRequired {
		t.Fatalf("response = %+v", response)
	}
	if !strings.Contains(string(data), "[router]\nclaude_code_models = ['claude-opus-4-1', 'my-model']\n") {
		t.Fatalf("config:\n%s", data)
	}
	if len(store.applied) != 1 {
		t.Fatalf("ApplyLive calls = %d", len(store.applied))
	}
	// The second write replaces the key in the existing [router] table.
	saved = serveMCP(t, s, http.MethodPut, "/v1/router/claude-code/models", `{"models":[],"expected_version":"`+response.Version+`"}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("clear = %d %s", saved.code, saved.body)
	}
	saved.json(t, &response)
	if data, _ = os.ReadFile(store.path); !strings.Contains(string(data), "claude_code_models = []") || strings.Count(string(data), "[router]") != 1 {
		t.Fatalf("config after clear:\n%s", data)
	}

	bad := serveMCP(t, s, http.MethodPut, "/v1/router/claude-code/models", `{"models":["-x"],"expected_version":"`+response.Version+`"}`, cookie, csrf)
	if bad.code != http.StatusUnprocessableEntity || decodeError(t, bad).Error.Code != "invalid" {
		t.Fatalf("bad = %d %s", bad.code, bad.body)
	}
	stale := serveMCP(t, s, http.MethodPut, "/v1/router/claude-code/models", `{"models":["a"],"expected_version":"`+version+`"}`, cookie, csrf)
	if stale.code != http.StatusConflict {
		t.Fatalf("stale = %d %s", stale.code, stale.body)
	}
	missing := serveMCP(t, s, http.MethodPut, "/v1/router/claude-code/models", `{"expected_version":"`+response.Version+`"}`, cookie, csrf)
	if missing.code != http.StatusUnprocessableEntity {
		t.Fatalf("missing models = %d %s", missing.code, missing.body)
	}
}

// POST /v1/router/test passes a claude-code address to the backend, which
// runs one Claude Code turn (cmd/lana covers the turn).
func TestRouterTestClaudeCodeAddress(t *testing.T) {
	s, _, fake, cookie, csrf := newClaudeRouterServer(t, true)
	var got string
	fake.test = func(address string) (RouterTestResult, error) {
		got = address
		return RouterTestResult{OK: true, LatencyMS: 12}, nil
	}
	response := serveMCP(t, s, http.MethodPost, "/v1/router/test", `{"address":"claude-code/opus"}`, cookie, csrf)
	if response.code != http.StatusOK || got != "claude-code/opus" || !strings.Contains(string(response.body), `"ok":true`) {
		t.Fatalf("test = %d %s (address %q)", response.code, response.body, got)
	}
}
