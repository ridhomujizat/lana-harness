package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
)

func decodeWorkbenchClaudeAccount(t *testing.T, code int, body []byte) workbenchClaudeAccountResponse {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, body)
	}
	var out workbenchClaudeAccountResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	return out
}

// REQ-WRA-005: GET answers the built-in account first; PUT stores a Router
// account and new terminals get its CLAUDE_CONFIG_DIR and
// CLAUDE_SECURESTORAGE_CONFIG_DIR; an unknown name is 400; a removed account
// reads as the built-in one; the built-in account sets no env.
func TestWorkbenchClaudeAccount(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "work")
	config := routerTestConfig + "\n[providers.claude-work]\nkind = \"claude-code\"\nconfig_dir = \"" + dir + "\"\n"
	s, store, _, cookie, csrf := newRouterTestServer(t, config)
	fake := newFakeWorkbench()
	s.SetWorkbench(fake)

	get := func() workbenchClaudeAccountResponse {
		rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/claude-account", "", cookie, csrf)
		return decodeWorkbenchClaudeAccount(t, rr.Code, rr.Body.Bytes())
	}
	put := func(body string) (int, []byte) {
		rr := workbenchRequest(s, http.MethodPut, "/v1/workbench/claude-account", body, cookie, csrf)
		return rr.Code, rr.Body.Bytes()
	}
	create := func() []string {
		rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions", `{}`, cookie, csrf)
		if rr.Code != http.StatusOK {
			t.Fatalf("create status = %d, body = %s", rr.Code, rr.Body.String())
		}
		return fake.created[len(fake.created)-1].Env
	}

	if got := get(); got != (workbenchClaudeAccountResponse{Account: "claude-code"}) {
		t.Fatalf("initial GET = %+v", got)
	}
	if env := create(); len(env) != 0 {
		t.Fatalf("built-in create env = %v, want none", env)
	}

	code, body := put(`{"account":"claude-work"}`)
	if got := decodeWorkbenchClaudeAccount(t, code, body); got != (workbenchClaudeAccountResponse{Account: "claude-work", ConfigDir: dir}) {
		t.Fatalf("PUT = %+v", got)
	}
	if got := get(); got.Account != "claude-work" || got.ConfigDir != dir {
		t.Fatalf("GET after PUT = %+v", got)
	}
	want := []string{"CLAUDE_CONFIG_DIR=" + dir, "CLAUDE_SECURESTORAGE_CONFIG_DIR=" + dir}
	if env := create(); !reflect.DeepEqual(env, want) {
		t.Fatalf("create env = %v, want %v", env, want)
	}
	stored, err := os.ReadFile(filepath.Join(store.home, "workbench", "claude-account.json"))
	if err != nil || strings.TrimSpace(string(stored)) != `{"account":"claude-work"}` {
		t.Fatalf("stored = %q, %v", stored, err)
	}

	// Unknown names and bad bodies.
	for _, bad := range []string{`{"account":"deepseek"}`, `{"account":"nope"}`, `{"account":""}`, `{"other":1}`} {
		code, body := put(bad)
		if code != http.StatusBadRequest || !strings.Contains(string(body), "invalid_request") {
			t.Fatalf("PUT %s = %d %s, want 400 invalid_request", bad, code, body)
		}
	}
	if got := get(); got.Account != "claude-work" {
		t.Fatalf("a refused PUT changed the setting: %+v", got)
	}

	// The account is removed from the config: it reads as built-in.
	if err := os.WriteFile(store.path, []byte(routerTestConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := get(); got != (workbenchClaudeAccountResponse{Account: "claude-code"}) {
		t.Fatalf("GET after removal = %+v", got)
	}
	if env := create(); len(env) != 0 {
		t.Fatalf("create env after removal = %v", env)
	}

	// Back to built-in.
	code, body = put(`{"account":"claude-code"}`)
	if got := decodeWorkbenchClaudeAccount(t, code, body); got != (workbenchClaudeAccountResponse{Account: "claude-code"}) {
		t.Fatalf("PUT built-in = %+v", got)
	}
}

// The PUT is a state-changing workbench route: it needs the CSRF header.
func TestWorkbenchClaudeAccountCSRF(t *testing.T) {
	s, cookie, _ := newWorkbenchServer(t)
	rr := workbenchRequest(s, http.MethodPut, "/v1/workbench/claude-account", `{"account":"claude-code"}`, cookie, "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("PUT without CSRF = %d %s", rr.Code, rr.Body.String())
	}
}

// REQ-WRA-001: saving a Claude Code account links its folder to the default
// Claude Code folder; without a default folder set, nothing is linked.
func TestClaudeAccountSaveSharesHistory(t *testing.T) {
	s, store, _, cookie, csrf := newClaudeRouterServer(t, true)
	defaultDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(defaultDir, "settings.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.SetClaudeDefaultDir(defaultDir)
	// Spec 77 REQ-WRA-012: the default account's .claude.json, copied without
	// its login keys into an account that never signed in.
	defaultJSON := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(defaultJSON, []byte(`{"theme":"dark","oauthAccount":{"emailAddress":"o@example.com"},"primaryApiKey":"k"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.SetClaudeDefaultJSON(defaultJSON)
	version := func() string {
		data, err := os.ReadFile(store.path)
		if err != nil {
			t.Fatal(err)
		}
		return shaHex(data)
	}
	saved := serveMCP(t, s, http.MethodPut, "/v1/providers/claude-work",
		`{"kind":"claude-code","expected_version":"`+version()+`"}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save = %d %s", saved.code, saved.body)
	}
	dir := filepath.Join(store.home, "claude", "claude-work")
	for _, name := range []string{"projects", "history.jsonl", "settings.json"} {
		target, err := os.Readlink(filepath.Join(dir, name))
		if err != nil || target != filepath.Join(defaultDir, name) {
			t.Fatalf("%s -> %q (%v), want link to %s", name, target, err, filepath.Join(defaultDir, name))
		}
	}
	seeded, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil || !strings.Contains(string(seeded), `"theme"`) || strings.Contains(string(seeded), "oauthAccount") || strings.Contains(string(seeded), "primaryApiKey") {
		t.Fatalf("account .claude.json = %s (%v)", seeded, err)
	}

	// The built-in account links nothing.
	saved = serveMCP(t, s, http.MethodPut, "/v1/providers/claude-code",
		`{"kind":"claude-code","expected_version":"`+version()+`"}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save built-in = %d %s", saved.code, saved.body)
	}
	entries, _ := os.ReadDir(defaultDir)
	if len(entries) != 3 {
		t.Fatalf("default folder = %v, want settings.json, projects, history.jsonl", entries)
	}
}

// AC-SMC-008: a create naming a Claude Code account mirrors the built-in
// account's MCP servers into it before the pane starts; the built-in account,
// another kind, or an unknown name mirror nothing and still create the pane.
func TestWorkbenchCreateMirrorsClaudeMCP(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(t.TempDir(), "claude", "claude-3")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	accountFile := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(accountFile, []byte(`{"numStartups": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers": {"hub": {"type": "http", "url": "https://hub.example.invalid/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config := routerTestConfig + "\n[providers.claude-3]\nkind = \"claude-code\"\nconfig_dir = \"" + dir + "\"\n"
	s, _, _, cookie, csrf := newRouterTestServer(t, config)
	fake := newFakeWorkbench()
	var atCreate string
	fake.onCreate = func(workbench.CreateOptions) {
		data, _ := os.ReadFile(accountFile)
		atCreate = string(data)
	}
	s.SetWorkbench(fake)

	create := func(body string) {
		t.Helper()
		rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions", body, cookie, csrf)
		if rr.Code != http.StatusOK {
			t.Fatalf("create %s status = %d, body = %s", body, rr.Code, rr.Body.String())
		}
	}

	for _, body := range []string{`{"input":"claude"}`, `{"input":"claude","claude_account":"claude-code"}`, `{"claude_account":"nope"}`, `{"claude_account":"deepseek"}`} {
		create(body)
		if strings.Contains(atCreate, "hub") {
			t.Fatalf("%s mirrored: %s", body, atCreate)
		}
	}

	create(`{"input":"env CLAUDE_CONFIG_DIR=x claude","claude_account":"claude-3"}`)
	if !strings.Contains(atCreate, `"hub"`) || !strings.Contains(atCreate, `"numStartups": 1`) {
		t.Fatalf("account file at create = %s", atCreate)
	}
	if last := fake.created[len(fake.created)-1]; last.Input != "env CLAUDE_CONFIG_DIR=x claude" {
		t.Fatalf("created = %+v", last)
	}

	// A broken built-in config leaves the account as it was and still creates.
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(accountFile, []byte(`{"mcpServers": {"own": {}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	create(`{"claude_account":"claude-3"}`)
	if atCreate != `{"mcpServers": {"own": {}}}` {
		t.Fatalf("account changed with a broken source: %s", atCreate)
	}
}

// Spec 82 REQ-WAS-002: the switch endpoint checks the account, mirrors a
// named account's MCP servers before the core switches, hands the core the
// account's config dir ("" for the built-in account), and answers the core's
// refusals 409 with their code and message.
func TestWorkbenchSwitchAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(t.TempDir(), "claude", "claude-3")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	accountFile := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(accountFile, []byte(`{"numStartups": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers": {"hub": {"type": "http", "url": "https://hub.example.invalid/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "claude", "never-signed-in")
	config := routerTestConfig +
		"\n[providers.claude-3]\nkind = \"claude-code\"\nenabled = false\nconfig_dir = \"" + dir + "\"\n" +
		"\n[providers.claude-new]\nkind = \"claude-code\"\nconfig_dir = \"" + missing + "\"\n"
	s, _, _, cookie, csrf := newRouterTestServer(t, config)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "p1", Command: []string{"/bin/zsh"}, AccountSwitch: workbench.SwitchReady}}
	var atSwitch string
	fake.onSwitch = func(string, string) {
		data, _ := os.ReadFile(accountFile)
		atSwitch = string(data)
	}
	s.SetWorkbench(fake)
	post := func(id, body string) (int, string) {
		rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/"+id+"/switch-account", body, cookie, csrf)
		return rr.Code, rr.Body.String()
	}

	// A disabled named account still switches; its MCP servers are mirrored
	// before the core is asked.
	code, body := post("p1", `{"claude_account":"claude-3"}`)
	if code != http.StatusOK {
		t.Fatalf("switch status = %d, body = %s", code, body)
	}
	var got workbench.Session
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.ID != "p1" || got.AgentConfigDir != dir {
		t.Fatalf("switch body = %s (%v)", body, err)
	}
	if !strings.Contains(atSwitch, `"hub"`) {
		t.Fatalf("account file at switch = %s", atSwitch)
	}
	if last := fake.switches[len(fake.switches)-1]; last != (workbenchSwitchCall{id: "p1", agent: "claude", configDir: dir}) {
		t.Fatalf("switch call = %+v", last)
	}

	// The built-in account: no config dir.
	if code, body := post("p1", `{"claude_account":"claude-code"}`); code != http.StatusOK {
		t.Fatalf("built-in switch = %d %s", code, body)
	}
	if last := fake.switches[len(fake.switches)-1]; last != (workbenchSwitchCall{id: "p1", agent: "claude"}) {
		t.Fatalf("built-in switch call = %+v", last)
	}

	// Bad accounts never reach the core.
	calls := len(fake.switches)
	for _, bad := range []string{`{"claude_account":"deepseek"}`, `{"claude_account":"nope"}`, `{"claude_account":""}`, `{}`, `{"claude_account":"claude-new"}`} {
		if code, body := post("p1", bad); code != http.StatusBadRequest || !strings.Contains(body, "invalid_request") {
			t.Fatalf("switch %s = %d %s, want 400 invalid_request", bad, code, body)
		}
	}
	if len(fake.switches) != calls {
		t.Fatalf("a refused account reached the core: %+v", fake.switches[calls:])
	}

	// An unknown pane is 404.
	if code, body := post("nope", `{"claude_account":"claude-code"}`); code != http.StatusNotFound {
		t.Fatalf("unknown pane = %d %s", code, body)
	}

	// The core's refusals are 409 with their code and message.
	for _, tc := range []struct {
		err  error
		code string
		msg  string
	}{
		{workbench.ErrNoConversation, "no_conversation", "No Claude conversation in this pane"},
		{workbench.ErrNotSwitchable, "not_switchable", "This pane was opened before account switching; use New tab"},
		{workbench.ErrStillRunning, "still_running", "Claude did not exit. Exit it in the pane and try again."},
		{workbench.ErrCoreOutdated, "core_outdated", "Restart the Workbench to switch accounts in place"},
	} {
		fake.mu.Lock()
		fake.switchErr = tc.err
		fake.mu.Unlock()
		code, body := post("p1", `{"claude_account":"claude-code"}`)
		var apiErr struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal([]byte(body), &apiErr)
		if code != http.StatusConflict || apiErr.Error.Code != tc.code || apiErr.Error.Message != tc.msg {
			t.Fatalf("%s: switch = %d %s", tc.code, code, body)
		}
	}
}

// The switch is a state-changing workbench route: it needs the CSRF header.
func TestWorkbenchSwitchAccountCSRF(t *testing.T) {
	s, _, _, cookie, _ := newRouterTestServer(t, routerTestConfig)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "p1"}}
	s.SetWorkbench(fake)
	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/p1/switch-account", `{"claude_account":"claude-code"}`, cookie, "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("switch without CSRF = %d %s", rr.Code, rr.Body.String())
	}
	if len(fake.switches) != 0 {
		t.Fatalf("switch without CSRF reached the core: %+v", fake.switches)
	}
}
