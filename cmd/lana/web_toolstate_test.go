package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/web"
)

const toolStateConfig = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "m"
permission = "ask"
tools = ["read_file", "bash"]

[[routes]]
channel = "web"
agent = "coder"

[mcp.github]
command = "github-mcp"
`

// newToolStateServer starts a real runtime and web server from a config file so
// the agent save API can rewrite and live-apply it. Every test gets its own
// home and prompt capture.
func newToolStateServer(t *testing.T) (*lana.Runtime, *promptCaptureProvider, string, *http.Cookie, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(toolStateConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	text, end := "ok", "end_turn"
	capture := &promptCaptureProvider{llm: mock.New(mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
	}})}
	rt, err := lana.New(context.Background(), lana.Options{
		Home:      home,
		Providers: map[string]core.Provider{"mock": capture},
	})
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	_, ts, cookie, csrf := webServer(t, rt, home)
	return rt, capture, ts.URL, cookie, csrf
}

func toolStateGet(t *testing.T, base, target string, cookie *http.Cookie, out any) {
	t.Helper()
	resp, data := webDo(t, http.MethodGet, base+target, "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, body = %q", target, resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("GET %s body %q: %v", target, data, err)
	}
}

func toolStateConfigVersion(t *testing.T, base string, cookie *http.Cookie) string {
	t.Helper()
	var body struct {
		Version string `json:"version"`
	}
	toolStateGet(t, base, "/v1/config", cookie, &body)
	if body.Version == "" {
		t.Fatal("GET /v1/config version is empty")
	}
	return body.Version
}

func putToolState(t *testing.T, base string, cookie *http.Cookie, csrf, version string, fields map[string]any) {
	t.Helper()
	fields["expected_version"] = version
	payload, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	resp, data := webDo(t, http.MethodPut, base+"/v1/agents/coder", string(payload), cookie, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /v1/agents/coder status = %d, body = %q", resp.StatusCode, data)
	}
}

func toolStateCatalogEntry(t *testing.T, base string, cookie *http.Cookie, groupID, name string) web.ToolEntry {
	t.Helper()
	var body struct {
		Groups []web.ToolGroup `json:"groups"`
	}
	toolStateGet(t, base, "/v1/tools?agent=coder", cookie, &body)
	entry, ok := findToolEntry(body.Groups, groupID, name)
	if !ok {
		t.Fatalf("%s/%s is not in the catalog: %+v", groupID, name, body.Groups)
	}
	return entry
}

func toolStateAgentConfig(t *testing.T, base string, cookie *http.Cookie) ([]string, []string) {
	t.Helper()
	var body struct {
		Settings struct {
			Agents map[string]struct {
				Tools []string `json:"tools"`
				MCP   []string `json:"mcp"`
			} `json:"agents"`
		} `json:"settings"`
	}
	toolStateGet(t, base, "/v1/config", cookie, &body)
	coder := body.Settings.Agents["coder"]
	return coder.Tools, coder.MCP
}

func toolStateDisabled(t *testing.T, base string, cookie *http.Cookie) []string {
	t.Helper()
	var body struct {
		Agents []struct {
			Name          string   `json:"name"`
			DisabledTools []string `json:"disabled_tools"`
		} `json:"agents"`
	}
	toolStateGet(t, base, "/v1/agents", cookie, &body)
	for _, agent := range body.Agents {
		if agent.Name == "coder" {
			return agent.DisabledTools
		}
	}
	t.Fatal("coder is not in GET /v1/agents")
	return nil
}

// TestToolStateSaveAskUserCoversACTTS004 covers AC-TTS-004: turning ask_user On
// adds it to tools, the next turn offers it, and the catalog reloads it as
// listed and enabled.
func TestToolStateSaveAskUserCoversACTTS004(t *testing.T) {
	rt, capture, base, cookie, csrf := newToolStateServer(t)
	version := toolStateConfigVersion(t, base, cookie)

	before := toolStateCatalogEntry(t, base, cookie, "chat", "ask_user")
	if before.Enabled || before.Source != "not_listed" {
		t.Fatalf("ask_user before save = %+v", before)
	}

	putToolState(t, base, cookie, csrf, version, map[string]any{
		"tools":          []string{"read_file", "bash", "ask_user"},
		"disabled_tools": []string{},
		"mcp":            []string{},
	})

	tools, _ := toolStateAgentConfig(t, base, cookie)
	if !containsToolName(tools, "ask_user") {
		t.Fatalf("agents.coder.tools = %v, want ask_user", tools)
	}

	ctx := context.Background()
	info, err := rt.CreateChannelSession(ctx, "web", "owner", "coder")
	if err != nil {
		t.Fatalf("CreateChannelSession: %v", err)
	}
	if _, err := rt.Run(ctx, lana.Request{Prompt: "hello", SessionID: info.ID}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if offered := toolSpecNames(capture.lastTools()); !containsToolName(offered, "ask_user") {
		t.Fatalf("ask_user is not offered at the next turn: %v", offered)
	}

	after := toolStateCatalogEntry(t, base, cookie, "chat", "ask_user")
	if !after.Enabled || after.Source != "listed" {
		t.Fatalf("ask_user after save = %+v, want enabled true source listed", after)
	}
}

// TestToolStateSaveDisablesBashCoversACTTS005 covers AC-TTS-005: turning a
// listed tool Off keeps it in tools and adds it to disabled_tools.
func TestToolStateSaveDisablesBashCoversACTTS005(t *testing.T) {
	_, _, base, cookie, csrf := newToolStateServer(t)
	version := toolStateConfigVersion(t, base, cookie)

	putToolState(t, base, cookie, csrf, version, map[string]any{
		"tools":          []string{"read_file", "bash"},
		"disabled_tools": []string{"bash"},
		"mcp":            []string{},
	})

	tools, _ := toolStateAgentConfig(t, base, cookie)
	if !containsToolName(tools, "bash") {
		t.Fatalf("agents.coder.tools = %v, want bash kept", tools)
	}
	if disabled := toolStateDisabled(t, base, cookie); !containsToolName(disabled, "bash") {
		t.Fatalf("agents.coder.disabled_tools = %v, want bash", disabled)
	}

	bash := toolStateCatalogEntry(t, base, cookie, "shell", "bash")
	if bash.Enabled || bash.Source != "listed" {
		t.Fatalf("bash after save = %+v, want enabled false source listed", bash)
	}
}

// TestToolStateSaveAttachesMCPCoversACTTS006 covers AC-TTS-006: turning an MCP
// tool On for a not-attached server adds the server to mcp.
func TestToolStateSaveAttachesMCPCoversACTTS006(t *testing.T) {
	_, _, base, cookie, csrf := newToolStateServer(t)
	version := toolStateConfigVersion(t, base, cookie)

	if _, mcp := toolStateAgentConfig(t, base, cookie); containsToolName(mcp, "github") {
		t.Fatalf("agents.coder.mcp = %v, want github not attached yet", mcp)
	}

	putToolState(t, base, cookie, csrf, version, map[string]any{
		"tools":          []string{"read_file", "bash"},
		"disabled_tools": []string{},
		"mcp":            []string{"github"},
	})

	_, mcp := toolStateAgentConfig(t, base, cookie)
	if !containsToolName(mcp, "github") {
		t.Fatalf("agents.coder.mcp = %v, want github", mcp)
	}
}
