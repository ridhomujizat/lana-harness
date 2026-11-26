package web

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// Spec 77 REQ-WRA-010: creating an Antigravity account links its HOME's
// conversations to the owner HOME the daemon set; without one nothing is
// linked.
func TestAntigravityAccountCreateSharesConversations(t *testing.T) {
	s, store, _, cookie, csrf := newAgyServer(t, routerTestConfig)
	if got := serveMCP(t, s, http.MethodPost, "/v1/router/antigravity/accounts", "", cookie, csrf); got.code != http.StatusAccepted {
		t.Fatalf("create = %d %s", got.code, got.body)
	}
	if _, err := os.Lstat(filepath.Join(store.home, "antigravity", "agy-2", ".gemini")); !os.IsNotExist(err) {
		t.Fatalf("linked without an owner HOME: %v", err)
	}

	owner := t.TempDir()
	// Spec 77 REQ-WRA-011: the owner's agy settings, linked before the first
	// sign-in starts.
	if err := os.MkdirAll(filepath.Join(owner, ".gemini", "antigravity-cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owner, ".gemini", "antigravity-cli", "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.SetAntigravityShareHome(owner)
	if got := serveMCP(t, s, http.MethodPost, "/v1/router/antigravity/accounts", "", cookie, csrf); got.code != http.StatusAccepted {
		t.Fatalf("create = %d %s", got.code, got.body)
	}
	link := filepath.Join(store.home, "antigravity", "agy-3", ".gemini", "antigravity-cli", "conversations")
	if target, err := os.Readlink(link); err != nil || target != filepath.Join(owner, ".gemini", "antigravity-cli", "conversations") {
		t.Fatalf("conversations -> %q (%v)", target, err)
	}
	settings := filepath.Join(store.home, "antigravity", "agy-3", ".gemini", "antigravity-cli", "settings.json")
	if target, err := os.Readlink(settings); err != nil || target != filepath.Join(owner, ".gemini", "antigravity-cli", "settings.json") {
		t.Fatalf("settings.json -> %q (%v)", target, err)
	}
}
