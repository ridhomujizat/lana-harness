package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
)

// Spec 83 REQ-WAG-003, REQ-WAG-004: the switch endpoint takes agy_account,
// hands the core the account's HOME ("" for the built-in account) with agent
// "agy", relinks a named account's mcp_config.json before the core switches,
// and refuses a body with both or neither account. A create with agy_account
// relinks too.
func TestWorkbenchSwitchAccountAgy(t *testing.T) {
	owner := t.TempDir()
	home := filepath.Join(t.TempDir(), "antigravity", "agy-work")
	ownerMCP := filepath.Join(owner, ".gemini", "config", "mcp_config.json")
	accountMCP := filepath.Join(home, ".gemini", "config", "mcp_config.json")
	for _, dir := range []string{filepath.Dir(ownerMCP), filepath.Dir(accountMCP)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(ownerMCP, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	replaced := func() {
		t.Helper()
		_ = os.Remove(accountMCP)
		if err := os.WriteFile(accountMCP, []byte(`{"mcpServers":{"own":{}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	replaced()
	config := routerTestConfig + "\n[providers.agy-work]\nkind = \"antigravity\"\nhome = \"" + home + "\"\n"
	s, _, _, cookie, csrf := newRouterTestServer(t, config)
	s.SetAntigravityShareHome(owner)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "p1", Command: []string{"/bin/zsh"}}}
	linkedAtSwitch := false
	fake.onSwitch = func(string, string) {
		target, err := os.Readlink(accountMCP)
		linkedAtSwitch = err == nil && target == ownerMCP
	}
	s.SetWorkbench(fake)
	post := func(body string) (int, string) {
		rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/p1/switch-account", body, cookie, csrf)
		return rr.Code, rr.Body.String()
	}

	if code, body := post(`{"agy_account":"agy-work"}`); code != http.StatusOK {
		t.Fatalf("switch = %d %s", code, body)
	}
	if last := fake.switches[len(fake.switches)-1]; last != (workbenchSwitchCall{id: "p1", agent: "agy", configDir: home}) {
		t.Fatalf("switch call = %+v", last)
	}
	if !linkedAtSwitch {
		t.Fatal("mcp_config.json was not relinked before the switch")
	}
	if code, body := post(`{"agy_account":"antigravity"}`); code != http.StatusOK {
		t.Fatalf("built-in switch = %d %s", code, body)
	}
	if last := fake.switches[len(fake.switches)-1]; last != (workbenchSwitchCall{id: "p1", agent: "agy"}) {
		t.Fatalf("built-in switch call = %+v", last)
	}

	calls := len(fake.switches)
	for _, bad := range []string{`{"agy_account":"nope"}`, `{"agy_account":"claude-code"}`, `{"agy_account":"agy-work","claude_account":"claude-code"}`, `{}`} {
		if code, body := post(bad); code != http.StatusBadRequest || !strings.Contains(body, "invalid_request") {
			t.Fatalf("switch %s = %d %s", bad, code, body)
		}
	}
	if len(fake.switches) != calls {
		t.Fatal("a refused body reached the core")
	}

	// The core's agy refusal message passes through as it is.
	fake.mu.Lock()
	fake.switchErr = workbenchAgyRefusal{}
	fake.mu.Unlock()
	code, body := post(`{"agy_account":"agy-work"}`)
	var apiErr struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &apiErr)
	if code != http.StatusConflict || apiErr.Error.Code != "still_running" || apiErr.Error.Message != (workbenchAgyRefusal{}).Error() {
		t.Fatalf("refusal = %d %s", code, body)
	}

	// A create for the account relinks as well.
	replaced()
	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions", `{"input":"agy","agy_account":"agy-work"}`, cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("create = %d %s", rr.Code, rr.Body.String())
	}
	if target, err := os.Readlink(accountMCP); err != nil || target != ownerMCP {
		t.Fatalf("create did not relink: %q, %v", target, err)
	}
}

// workbenchAgyRefusal stands in for the core's agy still_running refusal.
type workbenchAgyRefusal struct{}

func (workbenchAgyRefusal) Error() string {
	return "Antigravity did not exit. Exit it in the pane and try again."
}
func (workbenchAgyRefusal) Unwrap() error { return workbench.ErrStillRunning }
