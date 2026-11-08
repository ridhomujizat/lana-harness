package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/core"
)

// authRuntime is an accountStatusRuntime that also signs accounts in and
// out (core.RuntimeAccountAuth).
type authRuntime struct {
	*accountStatusRuntime
	mu       sync.Mutex
	signIns  []core.RuntimeAccount
	signOuts []core.RuntimeAccount
}

func (a *authRuntime) SignIn(_ context.Context, account core.RuntimeAccount) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.signIns = append(a.signIns, account)
	return nil
}

func (a *authRuntime) SignOut(_ context.Context, account core.RuntimeAccount) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.signOuts = append(a.signOuts, account)
	return nil
}

// REQ-CCT-012 through the daemon's web backend: one POST adds the account
// and starts its sign-in with the new folder; the sign-in state reaches
// succeeded; sign-out runs for the account; removal with remove_folder
// signs out and deletes the folder.
func TestClaudeAccountSignInThroughDaemon(t *testing.T) {
	home := t.TempDir()
	writeLiveConfig(t, home, routerWebConfig)
	rt, _ := liveWebRuntime(t, home, routerMockScript(1))
	fake := &authRuntime{accountStatusRuntime: &accountStatusRuntime{statusRuntime: &statusRuntime{status: core.RuntimeStatus{Binary: "/bin/claude"}}}}
	rt.SetAgentRuntime(lana.ClaudeCodeRuntime, fake)
	_, ts, cookie, csrf := webServer(t, rt, home)

	resp, data := webDo(t, http.MethodPost, ts.URL+"/v1/router/claude-code/accounts", "", cookie, csrf)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("create = %d %s", resp.StatusCode, data)
	}
	var created struct {
		Name      string `json:"name"`
		ConfigDir string `json:"config_dir"`
		SignIn    struct {
			State string `json:"state"`
		} `json:"sign_in"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "claude", "claude-2")
	if created.Name != "claude-2" || created.ConfigDir != dir || created.SignIn.State != "running" {
		t.Fatalf("created = %s", data)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/router/claude-code/accounts/claude-2/sign-in", "", cookie, csrf)
		if strings.Contains(string(data), `"state":"succeeded"`) {
			break
		}
		if resp.StatusCode != http.StatusOK || time.Now().After(deadline) {
			t.Fatalf("sign-in = %d %s", resp.StatusCode, data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(data), `"finished_at":"`) {
		t.Fatalf("state = %s", data)
	}
	fake.mu.Lock()
	signIns := append([]core.RuntimeAccount(nil), fake.signIns...)
	fake.mu.Unlock()
	if len(signIns) != 1 || signIns[0].Name != "claude-2" || signIns[0].ConfigDir != dir {
		t.Fatalf("sign-ins = %+v", signIns)
	}

	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/router/claude-agent", "", cookie, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"display_name":"claude-2@example.com"`) ||
		!strings.Contains(string(data), `"sign_in":{"state":"succeeded"`) {
		t.Fatalf("claude-agent = %d %s", resp.StatusCode, data)
	}

	resp, data = webDo(t, http.MethodPost, ts.URL+"/v1/router/claude-code/accounts/claude-code/sign-out", `{"confirm":true}`, cookie, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"name":"claude-code"`) {
		t.Fatalf("built-in sign-out = %d %s", resp.StatusCode, data)
	}

	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/providers", "", cookie, csrf)
	var list struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &list); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("providers = %d %s", resp.StatusCode, data)
	}
	resp, data = webDo(t, http.MethodDelete, ts.URL+"/v1/providers/claude-2?remove_folder=1", `{"expected_version":"`+list.Version+`"}`, cookie, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"folder_removed":true`) {
		t.Fatalf("remove = %d %s", resp.StatusCode, data)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.signOuts) != 2 || fake.signOuts[0].Name != "claude-code" || fake.signOuts[0].ConfigDir != "" ||
		fake.signOuts[1].Name != "claude-2" || fake.signOuts[1].ConfigDir != dir {
		t.Fatalf("sign-outs = %+v", fake.signOuts)
	}
}
