package lana

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// claudeAccountsConfig is a config with two Claude Code accounts on the fake
// binary: claude-work in the default folder, claude-home in its own.
func claudeAccountsConfig(bin, homeDir, extra string) string {
	return `
[providers.claude-work]
kind = "claude-code"
binary = "` + bin + `"

[providers.claude-home]
kind = "claude-code"
binary = "` + bin + `"
config_dir = "` + homeDir + `"

[agents.work]
model = "claude-work/haiku"
permission = "trust"

[agents.home]
model = "claude-home/sonnet"
permission = "trust"

[combos.both]
models = ["claude-home/opus", "claude-work/opus"]
` + extra
}

// Spec 39 r2 with the fake `claude` (T-CCT-04):
//   - AC-CCT-012: an agent on claude-work/haiku runs claude with
//     CLAUDE_CONFIG_DIR = $LANA_HOME/claude/claude-work, and the call record
//     and the limit snapshot name provider claude-work;
//   - AC-CCT-013 (backend): each account row has its own e-mail and plan from
//     its folder's `claude auth status`, its own plan windows from its own
//     rate_limit_event, and the agents and combos that use it;
//   - the built-in account is unchanged: no CLAUDE_CONFIG_DIR is set by lana.
func TestClaudeCodeAccounts(t *testing.T) {
	bin := buildFakeClaude(t)
	fixtures, err := filepath.Abs(filepath.Join("runtime", "claudecode", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "fake.jsonl")
	t.Setenv("FAKECLAUDE_LOG", logPath)
	t.Setenv("FAKECLAUDE_FIXTURE", filepath.Join(fixtures, "hi.jsonl"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	workDir := filepath.Join(home, "claude", "claude-work")
	homeDir := filepath.Join(t.TempDir(), "claude-home")
	for _, dir := range []string{workDir, homeDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(workDir, "fakeclaude_auth.json"),
		`{"loggedIn":true,"authMethod":"claude.ai","email":"work@example.com","subscriptionType":"max"}`)
	writeTestFile(t, filepath.Join(homeDir, "fakeclaude_auth.json"),
		`{"loggedIn":true,"authMethod":"claude.ai","email":"home@example.com","planTier":"pro"}`)
	hi, err := os.ReadFile(filepath.Join(fixtures, "hi.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	homeFixture := strings.Replace(string(hi), `"five_hour":{"utilization":0.08`, `"five_hour":{"utilization":0.55`, 1)
	if homeFixture == string(hi) {
		t.Fatal("hi.jsonl has no five_hour window to change")
	}
	writeTestFile(t, filepath.Join(homeDir, "fakeclaude_fixture.jsonl"), homeFixture)

	cfg := claudeAccountsConfig(bin, homeDir, `
[agents.builtin]
model = "claude-code/haiku"
binary = "`+bin+`"
permission = "trust"
`)
	st := openRouterTestStore(t)
	rt, err := New(context.Background(), Options{Home: home, Store: st, Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	ctx := context.Background()
	for _, agent := range []string{"work", "home", "builtin"} {
		result, err := rt.Run(ctx, Request{Agent: agent, Prompt: "hi"})
		if err != nil || result.StopReason != StopEndTurn {
			t.Fatalf("run %s: %+v %v", agent, result, err)
		}
	}

	starts := fakeClaudeLog(t, logPath, "start")
	if len(starts) != 3 {
		t.Fatalf("starts = %d", len(starts))
	}
	configDir := func(i int) (string, bool) {
		for _, kv := range starts[i]["env"].([]any) {
			if value, ok := strings.CutPrefix(kv.(string), "CLAUDE_CONFIG_DIR="); ok {
				return value, true
			}
		}
		return "", false
	}
	if dir, _ := configDir(0); dir != workDir {
		t.Fatalf("claude-work CLAUDE_CONFIG_DIR = %q, want %q", dir, workDir)
	}
	if dir, _ := configDir(1); dir != homeDir {
		t.Fatalf("claude-home CLAUDE_CONFIG_DIR = %q, want %q", dir, homeDir)
	}
	if dir, ok := configDir(2); ok && dir != "" {
		t.Fatalf("built-in CLAUDE_CONFIG_DIR = %q", dir)
	}
	if argAfter(jsonStrings(starts[1]["argv"]), "--model") != "sonnet" {
		t.Fatalf("claude-home argv = %v", starts[1]["argv"])
	}

	accounts, err := rt.ClaudeCodeAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	calls := routerCalls(t, st, "turn")
	if len(calls) != 3 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].Provider != "claude-work" || calls[0].Requested != "claude-work/haiku" || calls[0].Outcome != "ok" ||
		calls[1].Provider != "claude-home" || calls[2].Provider != "claude-code" {
		t.Fatalf("calls = %+v", calls)
	}

	byName := map[string]ClaudeCodeAccount{}
	var order []string
	for _, account := range accounts {
		byName[account.Name] = account
		order = append(order, account.Name)
	}
	if strings.Join(order, ",") != "claude-code,claude-home,claude-work" {
		t.Fatalf("accounts = %v", order)
	}
	work, homeAcct, builtin := byName["claude-work"], byName["claude-home"], byName["claude-code"]
	if work.Status.Email != "work@example.com" || work.Status.Plan != "max" || !work.Status.LoggedIn || work.ConfigDir != workDir ||
		work.LoginCommand != "CLAUDE_CONFIG_DIR='"+workDir+"' CLAUDE_SECURESTORAGE_CONFIG_DIR='"+workDir+"' claude auth login" || work.Builtin {
		t.Fatalf("claude-work = %+v", work)
	}
	if homeAcct.Status.Email != "home@example.com" || homeAcct.Status.Plan != "pro" || homeAcct.ConfigDir != homeDir {
		t.Fatalf("claude-home = %+v", homeAcct)
	}
	if !builtin.Builtin || builtin.ConfigDir != "" || builtin.LoginCommand != "claude auth login" || builtin.Status.Email != "user@example.com" {
		t.Fatalf("claude-code = %+v", builtin)
	}
	five := func(account ClaudeCodeAccount) float64 {
		for _, w := range account.Windows {
			if w.Type == "five_hour" && w.Utilization != nil {
				return *w.Utilization
			}
		}
		return -1
	}
	if five(work) != 0.08 || five(homeAcct) != 0.55 || five(builtin) != 0.08 {
		t.Fatalf("five_hour: work %v home %v built-in %v", five(work), five(homeAcct), five(builtin))
	}
	if strings.Join(work.Agents, ",") != "work" || strings.Join(work.Combos, ",") != "both" ||
		strings.Join(homeAcct.Agents, ",") != "home" || strings.Join(homeAcct.Combos, ",") != "both" ||
		strings.Join(builtin.Agents, ",") != "builtin" || len(builtin.Combos) != 0 {
		t.Fatalf("users: work %v %v, home %v %v, built-in %v %v", work.Agents, work.Combos, homeAcct.Agents, homeAcct.Combos, builtin.Agents, builtin.Combos)
	}
}

// REQ-CCT-008, REQ-CCT-010: a scheduled run may use an account whose
// allow_scheduled is set, without the agent key; an account without it falls
// back like the built-in one.
func TestClaudeCodeAccountScheduled(t *testing.T) {
	bin := buildFakeClaude(t)
	cfg := claudeAccountsConfig(bin, t.TempDir(), "")
	cfg = strings.Replace(cfg, "[providers.claude-home]\n", "[providers.claude-home]\nallow_scheduled = true\n", 1)
	rt, _ := routedRuntime(t, cfg)
	if err := rt.scheduledRunAllowed("home", ""); err != nil {
		t.Fatalf("account with allow_scheduled refused: %v", err)
	}
	const want = "claude-code is not allowed for scheduled runs of agent 'work'"
	if _, err := (workflowDeps{rt: rt}).RunAgent(context.Background(), workflow.AgentRequest{WorkflowID: "wf", Agent: "work", Prompt: "x"}); err == nil || err.Error() != want {
		t.Fatalf("workflow err = %v", err)
	}
	// The router knows the accounts; it never streams to them.
	r := rt.Router()
	if kind, ok := r.Kind("claude-work"); !ok || kind != core.ClaudeCodeKind {
		t.Fatalf("kind = %q %v", kind, ok)
	}
	if !llmcall.IsClaudeCodeAddress(r, "claude-work/haiku") || llmcall.IsClaudeCodeAddress(r, "claude-nope/haiku") {
		t.Fatal("IsClaudeCodeAddress over the router")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func jsonStrings(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}
