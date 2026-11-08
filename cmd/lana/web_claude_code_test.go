package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/web"
)

// Spec 39 scope additions over the real runtime and the fake `claude`:
// GET /v1/router/models?provider=claude-code lists the aliases, the models of
// the initialize response (after a Claude Code run), and the custom IDs;
// GET /v1/router lists them as choices; POST /v1/router/test with a
// claude-code address runs one Claude Code turn and records source "test".
func TestRouterClaudeCodeBackend(t *testing.T) {
	bin := buildCmdFakeClaude(t)
	t.Setenv("FAKECLAUDE_FIXTURE", claudeFixture(t, "hi.jsonl"))
	home := t.TempDir()
	writeLiveConfig(t, home, routerWebConfig+`
[router]
claude_code_models = ["my-model"]

[agents.claude]
model = "claude-code/opus"
binary = "`+bin+`"
`)
	rt, _ := liveWebRuntime(t, home, routerMockScript(1))
	_, ts, cookie, csrf := webServer(t, rt, home)

	models := func() web.RouterModelList {
		t.Helper()
		resp, data := webDo(t, http.MethodGet, ts.URL+"/v1/router/models?provider=claude-code", "", cookie, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("models = %d %s", resp.StatusCode, data)
		}
		var list web.RouterModelList
		if err := json.Unmarshal(data, &list); err != nil {
			t.Fatal(err)
		}
		return list
	}
	list := models()
	if len(list.Models) < 5 || list.Models[0].Address != "claude-code/opus" || list.Models[0].Source != "alias" ||
		strings.Join(list.Models[0].UsedBy.Agents, ",") != "claude" || list.Models[3].Source != "catalog" {
		t.Fatalf("models before a run = %+v", list.Models)
	}
	if last := list.Models[len(list.Models)-1]; last.Address != "claude-code/my-model" || last.Source != "custom" {
		t.Fatalf("custom = %+v", last)
	}

	resp, data := webDo(t, http.MethodPost, ts.URL+"/v1/router/test", `{"address":"claude-code/haiku"}`, cookie, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"ok":true`) {
		t.Fatalf("test = %d %s", resp.StatusCode, data)
	}
	resp, data = webDo(t, http.MethodPost, ts.URL+"/v1/router/test", `{"address":"claude-code/-x"}`, cookie, csrf)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("flag model test = %d %s", resp.StatusCode, data)
	}
	waitFor(t, "the claude-code test record", func() bool {
		_, data := webDo(t, http.MethodGet, ts.URL+"/v1/router/calls?source=test", "", cookie, "")
		return strings.Contains(string(data), `"provider":"claude-code"`) && strings.Contains(string(data), `"requested":"claude-code/haiku"`)
	})

	// The test run started Claude Code, whose initialize response listed
	// its models.
	list = models()
	found := false
	for _, m := range list.Models {
		found = found || (m.Address == "claude-code/claude-opus-5-5" && m.Name == "Claude Code · Opus 5.5" && m.Source == "claude_code")
	}
	if !found {
		t.Fatalf("models after a run = %+v", list.Models)
	}

	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/router", "", cookie, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `{"value":"claude-code/opus","label":"Claude Code · Opus (latest)","group":"Claude Code"}`) {
		t.Fatalf("overview = %d %s", resp.StatusCode, data)
	}
}
