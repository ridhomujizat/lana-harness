package lana

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/router"
)

var (
	fakeAgyOnce sync.Once
	fakeAgyDir  string
	fakeAgyBin  string
	fakeAgyErr  error
)

// buildFakeAgy builds the runtime/antigravity fake `agy` once per test binary;
// TestMain removes its folder.
func buildFakeAgy(t *testing.T) string {
	t.Helper()
	fakeAgyOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lana-fakeagy")
		if err != nil {
			fakeAgyErr = err
			return
		}
		fakeAgyDir = dir
		bin := filepath.Join(dir, "agy")
		out, err := exec.Command("go", "build", "-o", bin, "./runtime/antigravity/testdata/fakeagy").CombinedOutput()
		if err != nil {
			fakeAgyErr = fmt.Errorf("build fakeagy: %v\n%s", err, out)
			return
		}
		fakeAgyBin = bin
	})
	if fakeAgyErr != nil {
		t.Fatal(fakeAgyErr)
	}
	return fakeAgyBin
}

// agyFixture is the absolute path of a runtime/antigravity test fixture.
func agyFixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("runtime", "antigravity", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeAgyRecords returns the fake's record lines of one kind.
func fakeAgyRecords(t *testing.T, path, kind string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		var line map[string]any
		if json.Unmarshal(sc.Bytes(), &line) == nil && line["kind"] == kind {
			out = append(out, line)
		}
	}
	return out
}

// recordEnv reports whether a fake start record has env kv.
func recordEnv(record map[string]any, kv string) bool {
	env, _ := record["env"].([]any)
	for _, v := range env {
		if v == kv {
			return true
		}
	}
	return false
}

// samePathTest reports whether two paths name the same folder.
func samePathTest(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// waitAgySignIn waits until the account's sign-in reaches one of states.
func waitAgySignIn(t *testing.T, rt *Runtime, name string, states ...string) AntigravitySignIn {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		state := rt.AntigravitySignInState(name)
		for _, want := range states {
			if state.State == want {
				return state
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the sign-in of %s is %+v, want one of %v", name, rt.AntigravitySignInState(name), states)
	return AntigravitySignIn{}
}

// AC-AGY-013 at the Go API level with the fake `agy`: a sign-in of a named
// account runs `agy acp --login` with HOME = the account folder (created
// 0700), shows the Google link while it waits for the code, writes the code
// once on stdin, and ends succeeded; a wrong code fails with a URL-free
// line; a second start while one runs, a code outside waiting_code, a bad
// code, and the built-in account are refused; cancel and Close stop it.
func TestAntigravitySignInStateMachine(t *testing.T) {
	bin := buildFakeAgy(t)
	record := filepath.Join(t.TempDir(), "agy.jsonl")
	t.Setenv("FAKEAGY_RECORD", record)
	t.Setenv("FAKEAGY_LOGIN_CODE", "4/good-code")
	t.Setenv("FAKEAGY_MODELS", agyFixture(t, "models.txt"))
	home := t.TempDir()
	cfg := `
[providers.agy-2]
kind = "antigravity"
binary = "` + bin + `"
`
	rt, err := New(context.Background(), Options{Home: home, Store: openRouterTestStore(t), Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	accountHome := filepath.Join(home, "antigravity", "agy-2")
	if got := rt.AntigravityHome("agy-2"); got != accountHome {
		t.Fatalf("home = %q, want %q", got, accountHome)
	}

	if _, err := rt.StartAntigravitySignIn(core.RuntimeAccount{Name: AntigravityRuntime}); !errors.Is(err, ErrAntigravityBuiltin) {
		t.Fatalf("built-in start = %v", err)
	}
	if state := rt.AntigravitySignInState("agy-2"); state.State != AntigravitySignInIdle {
		t.Fatalf("idle = %+v", state)
	}
	if _, err := rt.SubmitAntigravityCode("agy-2", "4/good-code"); !errors.Is(err, ErrAntigravityNotWaiting) {
		t.Fatalf("code while idle = %v", err)
	}
	state, err := rt.StartAntigravitySignIn(core.RuntimeAccount{Name: "agy-2"})
	if err != nil || state.State != AntigravitySignInStarting || state.StartedAt.IsZero() {
		t.Fatalf("start = %+v %v", state, err)
	}
	state = waitAgySignIn(t, rt, "agy-2", AntigravitySignInWaitingCode)
	if !strings.HasPrefix(state.URL, "https://accounts.google.com/") {
		t.Fatalf("waiting = %+v", state)
	}
	info, err := os.Stat(accountHome)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("home = %v %v", info, err)
	}
	if _, err := rt.StartAntigravitySignIn(core.RuntimeAccount{Name: "agy-2"}); !errors.Is(err, ErrAntigravitySignInRunning) {
		t.Fatalf("second start = %v", err)
	}
	for _, bad := range []string{"", "  ", "a\nb", strings.Repeat("x", 2049)} {
		if _, err := rt.SubmitAntigravityCode("agy-2", bad); !errors.Is(err, ErrAntigravityBadCode) {
			t.Fatalf("bad code %q = %v", bad, err)
		}
	}
	state, err = rt.SubmitAntigravityCode("agy-2", "  4/good-code \n")
	if err != nil || state.State != AntigravitySignInRunning || state.URL != "" {
		t.Fatalf("submit = %+v %v", state, err)
	}
	state = waitAgySignIn(t, rt, "agy-2", AntigravitySignInSucceeded, AntigravitySignInFailed)
	if state.State != AntigravitySignInSucceeded || state.Error != "" || state.URL != "" || state.FinishedAt.IsZero() {
		t.Fatalf("finished = %+v", state)
	}
	starts := fakeAgyRecords(t, record, "start")
	if len(starts) != 1 || fmt.Sprint(starts[0]["argv"]) != "[acp --login]" || !recordEnv(starts[0], "HOME="+accountHome) || !samePathTest(t, fmt.Sprint(starts[0]["cwd"]), accountHome) {
		t.Fatalf("login start = %+v", starts)
	}
	logins := fakeAgyRecords(t, record, "login")
	if len(logins) != 1 || logins[0]["line"] != "4/good-code" {
		t.Fatalf("login lines = %+v", logins)
	}

	// A wrong code fails with agy's last line.
	if _, err := rt.StartAntigravitySignIn(core.RuntimeAccount{Name: "agy-2"}); err != nil {
		t.Fatal(err)
	}
	waitAgySignIn(t, rt, "agy-2", AntigravitySignInWaitingCode)
	if _, err := rt.SubmitAntigravityCode("agy-2", "wrong"); err != nil {
		t.Fatal(err)
	}
	state = waitAgySignIn(t, rt, "agy-2", AntigravitySignInFailed, AntigravitySignInSucceeded)
	if state.State != AntigravitySignInFailed || state.Error != "login failed: bad code" || state.URL != "" {
		t.Fatalf("failed = %+v", state)
	}

	// Cancel stops a sign-in that waits for its code.
	if _, err := rt.StartAntigravitySignIn(core.RuntimeAccount{Name: "agy-2"}); err != nil {
		t.Fatal(err)
	}
	waitAgySignIn(t, rt, "agy-2", AntigravitySignInWaitingCode)
	state = rt.CancelAntigravitySignIn("agy-2")
	if state.State != AntigravitySignInCancelled || state.Error != "" || state.URL != "" {
		t.Fatalf("cancelled = %+v", state)
	}

	// Close stops a running sign-in.
	if _, err := rt.StartAntigravitySignIn(core.RuntimeAccount{Name: "agy-2"}); err != nil {
		t.Fatal(err)
	}
	waitAgySignIn(t, rt, "agy-2", AntigravitySignInWaitingCode)
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if state := waitAgySignIn(t, rt, "agy-2", AntigravitySignInCancelled); state.State != AntigravitySignInCancelled {
		t.Fatalf("after close = %+v", state)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "accounts.google.com") {
		t.Fatal("the fake record has the sign-in link")
	}
}

// AC-AGY-012 with the fake `agy`: a refresh probes /usage for the signed-in
// accounts and stores one snapshot per account with kind "antigravity" and
// each group's 5-hour and weekly windows; a second refresh within 60 s runs
// no probe; a signed-out account is not probed. The account rows report the
// sign-in, the models (kept in router_kv), and the windows.
func TestAntigravityUsageSnapshot(t *testing.T) {
	bin := buildFakeAgy(t)
	record := filepath.Join(t.TempDir(), "agy.jsonl")
	t.Setenv("FAKEAGY_RECORD", record)
	t.Setenv("FAKEAGY_USAGE", agyFixture(t, "usage.json"))
	t.Setenv("FAKEAGY_MODELS", agyFixture(t, "models.txt"))
	home := t.TempDir()
	cfg := `
[providers.antigravity]
kind = "antigravity"
binary = "` + bin + `"

[providers.agy-2]
kind = "antigravity"
binary = "` + bin + `"
label = "Work"
priority = 5
`
	st := openRouterTestStore(t)
	rt, err := New(context.Background(), Options{Home: home, Store: st, Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	rt.RefreshAntigravityUsage(context.Background())
	snaps := map[string]router.LimitSnapshot{}
	for _, snap := range rt.Router().Limits(context.Background(), false) {
		snaps[snap.Provider] = snap
	}
	for _, name := range []string{"antigravity", "agy-2"} {
		snap, ok := snaps[name]
		if !ok || snap.Kind != llmcall.Antigravity || snap.Error != "" {
			t.Fatalf("snapshot %s = %+v", name, snap)
		}
		types := map[string]router.Window{}
		for _, w := range snap.Windows {
			types[w.Type] = w
		}
		for _, typ := range []string{"Gemini Models/5h", "Gemini Models/weekly", "Claude and GPT models/5h", "Claude and GPT models/weekly"} {
			w, ok := types[typ]
			if !ok || w.Source != router.WindowSourceUsageProbe || w.Utilization == nil {
				t.Fatalf("%s window %s = %+v (all %+v)", name, typ, w, snap.Windows)
			}
		}
		if types["Claude and GPT models/weekly"].ResetsAt != nil || types["Gemini Models/weekly"].ResetsAt == nil {
			t.Fatalf("%s reset times = %+v", name, snap.Windows)
		}
	}
	usageRuns := func() int {
		n := 0
		for _, start := range fakeAgyRecords(t, record, "start") {
			if strings.Contains(fmt.Sprint(start["argv"]), "/usage") {
				n++
			}
		}
		return n
	}
	if got := usageRuns(); got != 2 {
		t.Fatalf("usage probes = %d, want 2", got)
	}
	rt.RefreshAntigravityUsage(context.Background())
	if got := usageRuns(); got != 2 {
		t.Fatalf("usage probes after a second refresh = %d, want 2 (throttled)", got)
	}

	accounts, err := rt.AntigravityAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 || accounts[0].Name != "antigravity" || accounts[1].Name != "agy-2" {
		t.Fatalf("accounts = %+v", accounts)
	}
	work := accounts[1]
	if !work.Status.LoggedIn || work.Label != "Work" || work.Priority != 5 || !work.Pool || work.Builtin ||
		work.Home != filepath.Join(home, "antigravity", "agy-2") || len(work.Windows) != 4 || work.ModelsCount != 27 {
		t.Fatalf("agy-2 row = %+v", work)
	}
	if !accounts[0].Builtin || accounts[0].Home != "" || accounts[0].Priority != 0 {
		t.Fatalf("built-in row = %+v", accounts[0])
	}
	if !rt.PoolWarning(AntigravityRuntime) {
		t.Fatal("two pooled accounts give no pool warning")
	}
	value, ok, err := st.GetRouterKV(context.Background(), kvAntigravityModelsPrefix+"agy-2")
	if err != nil || !ok || !strings.Contains(value, "gemini-4-neon-high") {
		t.Fatalf("stored models = %q %v %v", value, ok, err)
	}

	// A signed-out account is not probed.
	t.Setenv("FAKEAGY_SIGNED_OUT", "1")
	rt2, err := New(context.Background(), Options{Home: t.TempDir(), Store: openRouterTestStore(t), Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt2.Close() })
	before := usageRuns()
	rt2.RefreshAntigravityUsage(context.Background())
	if got := usageRuns(); got != before {
		t.Fatalf("signed-out accounts were probed: %d -> %d", before, got)
	}
}

// AC-AGY-002 with the fake `agy`: an agent on a named account runs agy with
// HOME = the account folder, and the turn's call record names the account as
// provider; the session/new tier is kept for the account.
func TestAntigravityTurnRecordsAccount(t *testing.T) {
	bin := buildFakeAgy(t)
	record := filepath.Join(t.TempDir(), "agy.jsonl")
	t.Setenv("FAKEAGY_RECORD", record)
	t.Setenv("FAKEAGY_FIXTURE", agyFixture(t, "acp_turn1.jsonl"))
	t.Setenv("FAKEAGY_USAGE", agyFixture(t, "usage.json"))
	t.Setenv("FAKEAGY_MODELS", agyFixture(t, "models.txt"))
	t.Setenv("GEMINI_API_KEY", "should-not-pass")
	home := t.TempDir()
	ws := t.TempDir()
	cfg := `
[providers.agy-work]
kind = "antigravity"
binary = "` + bin + `"

[agents.gem]
toolset = "custom"
model = "agy-work/gemini-3.8-flash-low"
workspace = "` + ws + `"
permission = "trust"

[[routes]]
channel = "library"
agent = "gem"
`
	st := openRouterTestStore(t)
	rt, err := New(context.Background(), Options{Home: home, Store: st, Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if got := rt.RuntimeAgents(AntigravityRuntime); len(got) != 1 || got[0] != "gem" {
		t.Fatalf("users = %v", got)
	}
	result, err := rt.Run(context.Background(), Request{Agent: "gem", Prompt: "list the files"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.StopReason != StopEndTurn || result.Text == "" {
		t.Fatalf("result = %+v", result)
	}
	accountHome := filepath.Join(home, "antigravity", "agy-work")
	var acp map[string]any
	for _, start := range fakeAgyRecords(t, record, "start") {
		if fmt.Sprint(start["argv"]) == "[acp]" {
			acp = start
		}
	}
	if acp == nil || !recordEnv(acp, "HOME="+accountHome) || recordEnv(acp, "GEMINI_API_KEY=should-not-pass") {
		t.Fatalf("acp start = %+v", acp)
	}
	waitFor(t, "the call record", func() bool { return len(routerCalls(t, st, "turn")) >= 1 })
	calls := routerCalls(t, st, "turn")
	if calls[0].Provider != "agy-work" || calls[0].Model != "gemini-3.8-flash-low" || calls[0].Outcome != "ok" || calls[0].InputTokens != 15469 {
		t.Fatalf("call = %+v", calls[0])
	}
	if tier := rt.AntigravityTier("agy-work"); tier != "Google AI Pro" {
		t.Fatalf("tier = %q", tier)
	}
	value, ok, err := st.GetRouterKV(context.Background(), kvAntigravityTierPrefix+"agy-work")
	if err != nil || !ok || value != "Google AI Pro" {
		t.Fatalf("stored tier = %q %v %v", value, ok, err)
	}
	// The turn's end refreshes the account's windows in the background.
	waitFor(t, "the usage snapshot", func() bool {
		for _, snap := range rt.Router().Limits(context.Background(), false) {
			if snap.Provider == "agy-work" && snap.Kind == llmcall.Antigravity && len(snap.Windows) == 4 {
				return true
			}
		}
		return false
	})
}

// REQ-AGY-008: a scheduled run of an agent whose chain is only Antigravity
// needs allow_scheduled on the account.
func TestAntigravityScheduledRunAllowed(t *testing.T) {
	cfg := `
[providers.agy-2]
kind = "antigravity"
allow_scheduled = true

[agents.plain]
toolset = "custom"
model = "antigravity/gemini-4-neon-high"

[agents.pinned]
toolset = "custom"
model = "agy-2/gemini-4-neon-high"

[[routes]]
channel = "library"
agent = "plain"
`
	rt, err := New(context.Background(), Options{Home: t.TempDir(), Store: openRouterTestStore(t), Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	// The pooled address may run on agy-2, which allows scheduled runs.
	if err := rt.scheduledRunAllowed("plain", ""); err != nil {
		t.Fatalf("pooled: %v", err)
	}
	if err := rt.scheduledRunAllowed("pinned", ""); err != nil {
		t.Fatalf("pinned: %v", err)
	}
	const want = "antigravity is not allowed for scheduled runs of agent 'plain'"
	if err := rt.scheduledRunAllowed("plain", "antigravity/gemini-4-neon-high"); err != nil {
		t.Fatalf("model override on the pool: %v", err)
	}
	rt3, err := New(context.Background(), Options{Home: t.TempDir(), Store: openRouterTestStore(t), Layers: []config.Layer{{Name: "test", TOML: []byte(`
[agents.plain]
toolset = "custom"
model = "antigravity/gemini-4-neon-high"

[[routes]]
channel = "library"
agent = "plain"
`)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt3.Close() })
	if err := rt3.scheduledRunAllowed("plain", ""); err == nil || err.Error() != want {
		t.Fatalf("no allow_scheduled = %v", err)
	}
}
