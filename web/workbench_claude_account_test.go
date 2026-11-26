package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
