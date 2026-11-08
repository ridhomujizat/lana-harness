package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

// accountStatusRuntime is a statusRuntime that reports each account.
type accountStatusRuntime struct {
	*statusRuntime
	seen []core.RuntimeAccount
}

func (a *accountStatusRuntime) AccountStatus(_ context.Context, account core.RuntimeAccount) core.RuntimeAccountStatus {
	a.seen = append(a.seen, account)
	return core.RuntimeAccountStatus{LoggedIn: true, Email: account.Name + "@example.com", Plan: "max", Running: len(a.seen)}
}

// API-CCT-011: GET /v1/router/claude-agent gains accounts: the built-in one
// first, then each claude-code provider, with its folder, login command,
// status, own plan windows, and users. The top-level fields still describe
// the built-in account.
func TestClaudeAgentStatusAccounts(t *testing.T) {
	home := t.TempDir()
	writeLiveConfig(t, home, routerWebConfig+`
[providers.claude-work]
kind = "claude-code"
allow_scheduled = true

[agents.worker]
model = "claude-work/opus"

[combos.cw]
models = ["claude-work/sonnet", "mock/m"]
`)
	rt, _ := liveWebRuntime(t, home, routerMockScript(1))
	fake := &accountStatusRuntime{statusRuntime: &statusRuntime{status: core.RuntimeStatus{Binary: "/bin/claude", LoggedIn: true}}}
	rt.SetAgentRuntime(lana.ClaudeCodeRuntime, fake)
	util := 0.4
	rt.Router().UpdateLimits(router.LimitUpdate{Provider: "claude-work", Kind: "claude-code", Windows: []router.Window{{Type: "five_hour", Status: "allowed", Utilization: &util}}})
	_, ts, cookie, _ := webServer(t, rt, home)

	resp, data := webDo(t, http.MethodGet, ts.URL+"/v1/router/claude-agent", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET claude-agent = %d %s", resp.StatusCode, data)
	}
	var got struct {
		Binary   string `json:"binary"`
		Accounts []struct {
			Name           string `json:"name"`
			Builtin        bool   `json:"builtin"`
			ConfigDir      string `json:"config_dir"`
			AllowScheduled bool   `json:"allow_scheduled"`
			LoggedIn       bool   `json:"logged_in"`
			Email          string `json:"email"`
			Plan           string `json:"plan"`
			LoginCommand   string `json:"login_command"`
			Windows        []struct {
				Type        string   `json:"type"`
				Utilization *float64 `json:"utilization"`
			} `json:"windows"`
			Agents []string `json:"agents"`
			Combos []string `json:"combos"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Binary != "/bin/claude" || len(got.Accounts) != 2 {
		t.Fatalf("status = %s", data)
	}
	builtin, work := got.Accounts[0], got.Accounts[1]
	if builtin.Name != "claude-code" || !builtin.Builtin || builtin.ConfigDir != "" || builtin.LoginCommand != "claude" ||
		builtin.Email != "claude-code@example.com" || builtin.Windows == nil || len(builtin.Agents) != 0 {
		t.Fatalf("built-in = %+v (%s)", builtin, data)
	}
	dir := filepath.Join(home, "claude", "claude-work")
	if work.Name != "claude-work" || work.Builtin || work.ConfigDir != dir || !work.AllowScheduled || work.Email != "claude-work@example.com" ||
		work.Plan != "max" || work.LoginCommand != "CLAUDE_CONFIG_DIR='"+dir+"' claude" ||
		len(work.Windows) != 1 || work.Windows[0].Utilization == nil || *work.Windows[0].Utilization != 0.4 ||
		strings.Join(work.Agents, ",") != "worker" || strings.Join(work.Combos, ",") != "cw" {
		t.Fatalf("claude-work = %+v (%s)", work, data)
	}
	if len(fake.seen) != 2 || fake.seen[1].ConfigDir != dir || fake.seen[0].ConfigDir != "" {
		t.Fatalf("probed = %+v", fake.seen)
	}
}
