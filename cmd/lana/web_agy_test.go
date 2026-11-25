package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	cmdFakeAgyOnce sync.Once
	cmdFakeAgyBin  string
	cmdFakeAgyErr  error
)

// buildCmdFakeAgy builds the runtime/antigravity fake `agy` once per test
// binary.
func buildCmdFakeAgy(t *testing.T) string {
	t.Helper()
	cmdFakeAgyOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lana-cmd-fakeagy")
		if err != nil {
			cmdFakeAgyErr = err
			return
		}
		bin := filepath.Join(dir, "agy")
		out, err := exec.Command("go", "build", "-o", bin, "../../runtime/antigravity/testdata/fakeagy").CombinedOutput()
		if err != nil {
			cmdFakeAgyErr = fmt.Errorf("build fakeagy: %v\n%s", err, out)
			return
		}
		cmdFakeAgyBin = bin
	})
	if cmdFakeAgyErr != nil {
		t.Fatal(cmdFakeAgyErr)
	}
	return cmdFakeAgyBin
}

// agyFixture returns the absolute path of a fake agy fixture.
func agyFixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "runtime", "antigravity", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// Spec 74 through the daemon's web backend with the fake `agy`:
//   - API-AGY-027: POST /v1/router/limits/refresh probes /usage for both
//     signed-in accounts (kind antigravity);
//   - API-AGY-020: GET /v1/router/antigravity lists both accounts in pool
//     order with their sign-in, label, windows per group, models, and users;
//   - API-AGY-026: GET /v1/router lists one picker group per account;
//   - the Router test of an Antigravity address runs one tiny turn;
//   - spec 75: GET /v1/router/claude-agent rows carry the pool fields.
func TestAntigravityThroughWebBackend(t *testing.T) {
	bin := buildCmdFakeAgy(t)
	t.Setenv("FAKEAGY_MODELS", agyFixture(t, "models.txt"))
	t.Setenv("FAKEAGY_USAGE", agyFixture(t, "usage.json"))
	t.Setenv("FAKEAGY_FIXTURE", agyFixture(t, "acp_twoturn.jsonl"))
	home := t.TempDir()
	writeLiveConfig(t, home, routerWebConfig+`
[providers.antigravity]
kind = "antigravity"
binary = "`+bin+`"

[providers.agy-2]
kind = "antigravity"
binary = "`+bin+`"
label = "Work"

[agents.worker]
model = "agy-2/gemini-4-neon-high"
`)
	rt, _ := liveWebRuntime(t, home, routerMockScript(1))
	_, ts, cookie, csrf := webServer(t, rt, home)

	resp, data := webDo(t, http.MethodPost, ts.URL+"/v1/router/limits/refresh", "", cookie, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh = %d %s", resp.StatusCode, data)
	}
	var limits struct {
		Limits []struct {
			Provider string `json:"provider"`
			Kind     string `json:"kind"`
			Windows  []struct {
				Type  string `json:"type"`
				Label string `json:"label"`
			} `json:"windows"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(data, &limits); err != nil {
		t.Fatal(err)
	}
	found := map[string]int{}
	for _, snap := range limits.Limits {
		if snap.Kind == "antigravity" {
			found[snap.Provider] = len(snap.Windows)
		}
	}
	if found["antigravity"] != 4 || found["agy-2"] != 4 {
		t.Fatalf("limits = %s", data)
	}

	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/router/antigravity", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET antigravity = %d %s", resp.StatusCode, data)
	}
	var status struct {
		Installed   bool   `json:"installed"`
		Version     string `json:"version"`
		VersionOK   bool   `json:"version_ok"`
		MinVersion  string `json:"min_version"`
		PoolWarning bool   `json:"pool_warning"`
		Accounts    []struct {
			Name        string `json:"name"`
			Builtin     bool   `json:"builtin"`
			Label       string `json:"label"`
			DisplayName string `json:"display_name"`
			Home        string `json:"home"`
			SignedIn    bool   `json:"signed_in"`
			ModelsCount int    `json:"models_count"`
			Priority    int    `json:"priority"`
			Pool        bool   `json:"pool"`
			Windows     []struct {
				Group     string   `json:"group"`
				Type      string   `json:"type"`
				Remaining *float64 `json:"remaining"`
				Source    string   `json:"source"`
			} `json:"windows"`
			Agents []string `json:"agents"`
			SignIn struct {
				State string `json:"state"`
			} `json:"sign_in"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if !status.Installed || status.Version != "1.5.17" || !status.VersionOK || status.MinVersion != "1.5.17" ||
		!status.PoolWarning || len(status.Accounts) != 2 {
		t.Fatalf("status = %s", data)
	}
	builtin, work := status.Accounts[0], status.Accounts[1]
	if builtin.Name != "antigravity" || !builtin.Builtin || builtin.Home != "" || !builtin.SignedIn || builtin.Priority != 0 {
		t.Fatalf("built-in = %+v", builtin)
	}
	if work.Name != "agy-2" || work.Label != "Work" || work.DisplayName != "Work" || !work.SignedIn ||
		work.Home != filepath.Join(home, "antigravity", "agy-2") || work.ModelsCount != 27 || work.Priority != 100 || !work.Pool ||
		strings.Join(work.Agents, ",") != "worker" || work.SignIn.State != "idle" || len(work.Windows) != 4 {
		t.Fatalf("agy-2 = %+v (%s)", work, data)
	}
	groups := map[string]bool{}
	for _, w := range work.Windows {
		groups[w.Group+"|"+w.Type] = w.Remaining != nil && w.Source == "usage_probe"
	}
	for _, key := range []string{"Gemini Models|5h", "Gemini Models|weekly", "Claude and GPT models|5h", "Claude and GPT models|weekly"} {
		if !groups[key] {
			t.Fatalf("windows = %+v", work.Windows)
		}
	}

	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/router", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET router = %d %s", resp.StatusCode, data)
	}
	for _, want := range []string{`"group":"Antigravity · antigravity"`, `"group":"Antigravity · Work"`, `"value":"agy-2/gemini-4-neon-high"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("router choices lack %s: %s", want, data)
		}
	}
	resp, data = webDo(t, http.MethodGet, ts.URL+"/router/v1/models", "", nil, "")
	if strings.Contains(string(data), "gemini-4-neon-high") {
		t.Fatalf("proxy models list Antigravity: %d %s", resp.StatusCode, data)
	}

	resp, data = webDo(t, http.MethodPost, ts.URL+"/v1/router/test", `{"address":"antigravity/default"}`, cookie, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"ok":true`) {
		t.Fatalf("router test = %d %s", resp.StatusCode, data)
	}

	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/router/claude-agent", "", cookie, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"pool_warning":false`) ||
		!strings.Contains(string(data), `"priority":0,"pool":true,"cooling_until":null`) {
		t.Fatalf("claude-agent = %d %s", resp.StatusCode, data)
	}
}
