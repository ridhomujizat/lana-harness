package antigravity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// testdataFile returns the absolute path of a fixture in testdata/.
func testdataFile(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// countStarts counts the recorded process starts whose argv begins with arg.
func (h *harness) countStarts(arg string) int {
	n := 0
	for _, e := range h.starts() {
		if len(e.Argv) > 0 && e.Argv[0] == arg {
			n++
		}
	}
	return n
}

// AC-AGY-012: `agy models` run with the account env reports the signed-in
// state and the model list.
func TestAccountStatusSignedIn(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "FAKEAGY_MODELS="+testdataFile(t, "models.txt"))
	st := h.rt.AccountStatus(context.Background(), core.RuntimeAccount{})
	if !st.LoggedIn || !st.VersionOK || st.Version != "1.5.17" || st.Error != "" {
		t.Fatalf("status = %+v", st)
	}
	if st.Binary != fakeBin {
		t.Fatalf("binary = %q", st.Binary)
	}
	models, ok := h.rt.Models(core.AntigravityProvider)
	if !ok || len(models) == 0 {
		t.Fatalf("models = %v, ok = %v", models, ok)
	}
	if models[0].Value != "gemini-4-neon-high" || models[0].DisplayName != "Gemini 4 Neon (High)" {
		t.Fatalf("first model = %+v", models[0])
	}
	if last := models[len(models)-1]; last.Value != "gpt-oss-120b-medium" {
		t.Fatalf("last model = %+v", last)
	}
	if n := h.countStarts("models"); n != 1 {
		t.Fatalf("models runs = %d", n)
	}
}

// A signed-out account is not an error.
func TestAccountStatusSignedOut(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "FAKEAGY_SIGNED_OUT=1")
	st := h.rt.AccountStatus(context.Background(), core.RuntimeAccount{})
	if st.LoggedIn || st.Error != "" {
		t.Fatalf("status = %+v", st)
	}
	if _, ok := h.rt.Models(core.AntigravityProvider); ok {
		t.Fatal("models reported for a signed-out account")
	}
}

// Any other answer becomes the error, with URLs replaced by "sign-in link".
func TestAccountStatusErrorRedactsURL(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "FAKEAGY_MODELS_ERROR=Sign in again at https://accounts.google.com/o/x now")
	st := h.rt.AccountStatus(context.Background(), core.RuntimeAccount{})
	if st.LoggedIn || st.Error == "" {
		t.Fatalf("status = %+v", st)
	}
	if !strings.Contains(st.Error, "sign-in link") || strings.Contains(st.Error, "accounts.google.com") {
		t.Fatalf("error = %q", st.Error)
	}
}

// The status probe is cached for StatusTTL: a second call runs no process.
func TestAccountStatusCache(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "FAKEAGY_MODELS="+testdataFile(t, "models.txt"))
	a := core.RuntimeAccount{}
	for i := 0; i < 3; i++ {
		if st := h.rt.AccountStatus(context.Background(), a); !st.LoggedIn {
			t.Fatalf("status = %+v", st)
		}
	}
	if n := h.countStarts("models"); n != 1 {
		t.Fatalf("models runs = %d, want 1", n)
	}
}

// An unknown account has no cached model list.
func TestModelsUnknownAccount(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{})
	if _, ok := h.rt.Models("agy-9"); ok {
		t.Fatal("unknown account reported models")
	}
}

// A named account probes `agy models` with its own HOME.
func TestAccountStatusNamedHome(t *testing.T) {
	home := t.TempDir()
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "FAKEAGY_MODELS="+testdataFile(t, "models.txt"))
	st := h.rt.AccountStatus(context.Background(), core.RuntimeAccount{Name: "agy-2", Home: home})
	if !st.LoggedIn {
		t.Fatalf("status = %+v", st)
	}
	found := false
	for _, e := range h.starts() {
		if len(e.Argv) > 0 && e.Argv[0] == "models" && hasEnv(e.Env, "HOME="+home) {
			found = true
		}
	}
	if !found {
		t.Fatal("the models probe did not use the account HOME")
	}
}
