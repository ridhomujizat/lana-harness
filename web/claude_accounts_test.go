package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// API-CCT-010: PUT /v1/providers/{name} with kind claude-code writes the
// account table, creates its folder (0700), and answers the login command;
// GET lists it and the built-in account; DELETE removes only the table.
func TestClaudeAccountProviderFlow(t *testing.T) {
	s, store, fake, cookie, csrf := newClaudeRouterServer(t, true)
	version := func() string {
		data, err := os.ReadFile(store.path)
		if err != nil {
			t.Fatal(err)
		}
		return shaHex(data)
	}

	saved := serveMCP(t, s, http.MethodPut, "/v1/providers/claude-work",
		`{"kind":"claude-code","allow_scheduled":true,"expected_version":"`+version()+`"}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save = %d %s", saved.code, saved.body)
	}
	var response providerSaveResponse
	saved.json(t, &response)
	dir := filepath.Join(store.home, "claude", "claude-work")
	if response.ConfigDir != dir || response.LoginCommand != "CLAUDE_CONFIG_DIR='"+dir+"' CLAUDE_SECURESTORAGE_CONFIG_DIR='"+dir+"' claude auth login" {
		t.Fatalf("response = %+v", response)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("folder = %v %v", info, err)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[providers.claude-work]\nkind = 'claude-code'\nallow_scheduled = true\n") {
		t.Fatalf("config:\n%s", data)
	}

	// A custom folder; an absent key keeps its stored value.
	custom := filepath.Join(t.TempDir(), "nested", "work")
	saved = serveMCP(t, s, http.MethodPut, "/v1/providers/claude-work",
		`{"kind":"claude-code","config_dir":"`+custom+`","expected_version":"`+version()+`"}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save custom = %d %s", saved.code, saved.body)
	}
	saved.json(t, &response)
	if info, err := os.Stat(custom); err != nil || info.Mode().Perm() != 0o700 || response.ConfigDir != custom {
		t.Fatalf("custom folder = %v %v, response %+v", info, err, response)
	}

	list := serveMCP(t, s, http.MethodGet, "/v1/providers", "", cookie, csrf)
	var listed listProvidersResponse
	list.json(t, &listed)
	byName := map[string]providerJSON{}
	for _, row := range listed.Providers {
		byName[row.Name] = row
	}
	work, builtin := byName["claude-work"], byName["claude-code"]
	if work.Kind != "claude-code" || work.ConfigDir != custom || !work.AllowScheduled || work.Builtin || work.Disabled {
		t.Fatalf("claude-work row = %+v", work)
	}
	if !builtin.Builtin || builtin.Kind != "claude-code" || builtin.ConfigDir != "" || builtin.LoginCommand != "claude auth login" ||
		strings.Join(builtin.UsedBy, ",") != "agents.bima,combos.claude" {
		t.Fatalf("built-in row = %+v", builtin)
	}

	// The provider test of an account runs the Router test of <name>/<model>.
	var tested []string
	fake.test = func(address string) (RouterTestResult, error) {
		tested = append(tested, address)
		return RouterTestResult{OK: true, LatencyMS: 9}, nil
	}
	test := serveMCP(t, s, http.MethodPost, "/v1/providers/test", `{"kind":"claude-code","name":"claude-work"}`, cookie, csrf)
	if test.code != http.StatusOK || !strings.Contains(string(test.body), `"latency_ms":9`) || len(tested) != 1 || tested[0] != "claude-work/haiku" {
		t.Fatalf("test = %d %s, tested %v", test.code, test.body, tested)
	}

	// In use by an agent: the delete is refused.
	agent := "\n[agents.worker]\nmodel = \"claude-work/opus\"\n"
	if err := os.WriteFile(store.path, append(mustRead(t, store.path), agent...), 0o600); err != nil {
		t.Fatal(err)
	}
	refused := serveMCP(t, s, http.MethodDelete, "/v1/providers/claude-work", `{"expected_version":"`+version()+`"}`, cookie, csrf)
	if refused.code != http.StatusConflict || !strings.Contains(string(refused.body), "agents: worker") {
		t.Fatalf("delete in use = %d %s", refused.code, refused.body)
	}
	raw := strings.Replace(string(mustRead(t, store.path)), agent, "", 1)
	if err := os.WriteFile(store.path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	deleted := serveMCP(t, s, http.MethodDelete, "/v1/providers/claude-work", `{"expected_version":"`+version()+`"}`, cookie, csrf)
	if deleted.code != http.StatusOK {
		t.Fatalf("delete = %d %s", deleted.code, deleted.body)
	}
	deleted.json(t, &response)
	if response.ConfigDir != custom || !strings.Contains(response.Message, custom) {
		t.Fatalf("delete response = %+v", response)
	}
	if _, err := os.Stat(custom); err != nil {
		t.Fatalf("the folder was removed: %v", err)
	}
	if strings.Contains(string(mustRead(t, store.path)), "claude-work") {
		t.Fatal("the table is still there")
	}
}

func TestClaudeAccountProviderRules(t *testing.T) {
	s, store, _, cookie, csrf := newClaudeRouterServer(t, true)
	version := shaHex(mustRead(t, store.path))
	cases := []struct{ name, body, key string }{
		{"claude-code", `{"kind":"deepseek"}`, "providers.claude-code"},
		{"claude-code", `{"kind":"claude-code","config_dir":"/x"}`, "providers.claude-code.config_dir"},
		{"cw", `{"kind":"claude-code","base_url":"https://x"}`, "providers.cw.base_url"},
		{"cw", `{"kind":"claude-code","api_key":"sk-x"}`, "providers.cw.api_key"},
		{"cw", `{"kind":"claude-code","auth":"oauth"}`, "providers.cw.auth"},
		{"cw", `{"kind":"claude-code","config_dir":"relative/dir"}`, "providers.cw.config_dir"},
		{"cw", `{"kind":"claude-code","allow_scheduled":"yes"}`, "providers.cw.allow_scheduled"},
		{"ds", `{"kind":"deepseek","config_dir":"/x"}`, "providers.ds.config_dir"},
	}
	for _, tc := range cases {
		body := strings.TrimSuffix(tc.body, "}") + `,"expected_version":"` + version + `"}`
		response := serveMCP(t, s, http.MethodPut, "/v1/providers/"+tc.name, body, cookie, csrf)
		if response.code != http.StatusUnprocessableEntity || !strings.Contains(string(response.body), `"key":"`+tc.key+`"`) {
			t.Fatalf("%s %s = %d %s", tc.name, tc.body, response.code, response.body)
		}
	}
	builtin := serveMCP(t, s, http.MethodDelete, "/v1/providers/claude-code", `{"expected_version":"`+version+`"}`, cookie, csrf)
	if builtin.code != http.StatusConflict {
		t.Fatalf("delete built-in = %d %s", builtin.code, builtin.body)
	}
	if _, err := os.Stat(filepath.Join(store.home, "claude", "cw")); err == nil {
		t.Fatal("a refused save created the folder")
	}
}

// API-CCT-004 with accounts: each enabled account's models are in the
// "Claude Code" group, labelled with the account; an agent on an account is
// a Claude Code agent (API-CCT-006).
func TestRouterClaudeAccountChoices(t *testing.T) {
	s, store, fake, cookie, csrf := newClaudeRouterServer(t, true)
	raw := string(mustRead(t, store.path)) + `
[providers.claude-work]
kind = "claude-code"

[providers.claude-off]
kind = "claude-code"
disabled = true

[agents.worker]
model = "claude-work/opus"
reasoning_effort = "xhigh"
`
	if err := os.WriteFile(store.path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	fake.lists["claude-work"] = RouterModelList{Provider: "claude-work", Models: []RouterModel{
		{ID: "opus", Address: "claude-work/opus", Name: "Claude Code · Opus (latest)", Source: "alias"},
	}}
	response := serveMCP(t, s, http.MethodGet, "/v1/router", "", cookie, csrf)
	var body routerOverviewResponse
	response.json(t, &body)
	var claude []routerChoiceJSON
	for _, choice := range body.Choices {
		if strings.HasPrefix(choice.Value, "claude-") {
			claude = append(claude, choice)
		}
	}
	if len(claude) != 4 || claude[3] != (routerChoiceJSON{Value: "claude-work/opus", Label: "Claude Code (claude-work) · Opus (latest)", Group: "Claude Code"}) {
		t.Fatalf("claude choices = %+v", claude)
	}

	agents := serveMCP(t, s, http.MethodGet, "/v1/agents", "", cookie, csrf)
	if agents.code != http.StatusOK || !strings.Contains(string(agents.body), `"name":"worker"`) {
		t.Fatalf("agents = %d %s", agents.code, agents.body)
	}
	var list struct {
		Agents []struct {
			Name       string `json:"name"`
			ClaudeCode bool   `json:"claude_code"`
		} `json:"agents"`
	}
	agents.json(t, &list)
	found := false
	for _, agent := range list.Agents {
		if agent.Name == "worker" {
			found = agent.ClaudeCode
		}
	}
	if !found {
		t.Fatalf("worker is not a Claude Code agent: %s", agents.body)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// API-CCT-022 (spec 39 revision 5): PUT /v1/providers/claude-code with kind
// claude-code stores the built-in account's settings in
// [providers.claude-code] without a folder or a new login command; GET
// reflects them; DELETE stays 409 builtin.
func TestBuiltinClaudeAccountSettingsSave(t *testing.T) {
	s, store, _, cookie, csrf := newClaudeRouterServer(t, true)
	version := func() string { return shaHex(mustRead(t, store.path)) }
	builtinRow := func() providerJSON {
		t.Helper()
		list := serveMCP(t, s, http.MethodGet, "/v1/providers", "", cookie, csrf)
		var listed listProvidersResponse
		list.json(t, &listed)
		var found []providerJSON
		for _, row := range listed.Providers {
			if row.Name == "claude-code" {
				found = append(found, row)
			}
		}
		if len(found) != 1 || !found[0].Builtin {
			t.Fatalf("built-in rows = %+v", found)
		}
		return found[0]
	}
	if row := builtinRow(); row.AllowScheduled || row.Disabled {
		t.Fatalf("default built-in row = %+v", row)
	}

	// The UI's toggle body: kind, expected_version, allow_scheduled, disabled.
	saved := serveMCP(t, s, http.MethodPut, "/v1/providers/claude-code",
		`{"kind":"claude-code","allow_scheduled":true,"disabled":false,"expected_version":"`+version()+`"}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save = %d %s", saved.code, saved.body)
	}
	var response providerSaveResponse
	saved.json(t, &response)
	if response.ConfigDir != "" || response.LoginCommand != "claude auth login" {
		t.Fatalf("response = %+v", response)
	}
	raw := string(mustRead(t, store.path))
	if !strings.Contains(raw, "[providers.claude-code]\nkind = 'claude-code'\nallow_scheduled = true\n") || strings.Contains(raw, "config_dir") {
		t.Fatalf("config:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(store.home, "claude", "claude-code")); err == nil {
		t.Fatal("a folder was created for the built-in account")
	}
	if row := builtinRow(); !row.AllowScheduled || row.Disabled || row.ConfigDir != "" || row.LoginCommand != "claude auth login" {
		t.Fatalf("built-in row = %+v", row)
	}

	// Off: an absent key keeps its stored value.
	saved = serveMCP(t, s, http.MethodPut, "/v1/providers/claude-code",
		`{"kind":"claude-code","disabled":true,"expected_version":"`+version()+`"}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save off = %d %s", saved.code, saved.body)
	}
	if row := builtinRow(); !row.AllowScheduled || !row.Disabled {
		t.Fatalf("built-in row after off = %+v", row)
	}
	saved = serveMCP(t, s, http.MethodPut, "/v1/providers/claude-code",
		`{"kind":"claude-code","allow_scheduled":false,"disabled":false,"expected_version":"`+version()+`"}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save back = %d %s", saved.code, saved.body)
	}
	if row := builtinRow(); row.AllowScheduled || row.Disabled {
		t.Fatalf("built-in row after reset = %+v", row)
	}

	deleted := serveMCP(t, s, http.MethodDelete, "/v1/providers/claude-code", `{"expected_version":"`+version()+`"}`, cookie, csrf)
	if deleted.code != http.StatusConflict || !strings.Contains(string(deleted.body), `"builtin"`) {
		t.Fatalf("delete built-in = %d %s", deleted.code, deleted.body)
	}
}
