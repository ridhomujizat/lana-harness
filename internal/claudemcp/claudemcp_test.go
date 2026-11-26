package claudemcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// fixture is a temp built-in home and a named account dir; nothing touches
// the real ~/.claude.json.
type fixture struct {
	home, account string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{home: filepath.Join(root, "home"), account: filepath.Join(root, "lana", "claude", "claude-3")}
	for _, d := range []string{f.home, f.account} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f fixture) source(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.home, ".claude.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) accountFile(t *testing.T, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(f.account, ".claude.json")
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.account, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func rawMembers(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return out
}

func servers(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.MCPServers == nil {
		return map[string]any{}
	}
	return doc.MCPServers
}

const accountWithOthers = `{
  "numStartups": 12,
  "oauthAccount": {
    "emailAddress": "x@example.invalid",
    "nested":   [1, 2,  3]
  },
  "mcpServers": {
    "a": {"type": "http", "url": "https://old.example.invalid/mcp"},
    "c": {"type": "stdio", "command": "c-server"}
  },
  "projects": {
    "/x": {"mcpServers": {"p": {"type": "stdio", "command": "p"}}}
  },
  "weird": 1.50
}
`

// AC-SMC-004: source {a, b}, account {a (old url), c} without marker →
// account {a, b}, displaced holds old a and c, marker exists, every other key
// keeps its value byte for byte, the file mode is kept.
func TestMirrorFirstAdoption(t *testing.T) {
	f := newFixture(t)
	f.source(t, `{"mcpServers": {"a": {"type": "http", "url": "https://new.example.invalid/mcp", "headers": {"Authorization": "Bearer s3cret"}}, "b": {"type": "stdio", "command": "b"}}, "projects": {"/x": {"mcpServers": {"q": {}}}}}`)
	f.accountFile(t, accountWithOthers, 0o640)
	before := rawMembers(t, []byte(accountWithOthers))

	wrote, err := Mirror(f.home, f.account)
	if err != nil || !wrote {
		t.Fatalf("Mirror = %v, %v", wrote, err)
	}
	data := f.read(t, ".claude.json")
	after := rawMembers(t, data)
	for _, k := range []string{"numStartups", "oauthAccount", "projects", "weird"} {
		if !bytes.Equal(before[k], after[k]) {
			t.Fatalf("%s changed:\n%s\n%s", k, before[k], after[k])
		}
	}
	got := servers(t, data)
	if len(got) != 2 || got["a"].(map[string]any)["url"] != "https://new.example.invalid/mcp" || got["b"] == nil {
		t.Fatalf("mcpServers = %v", got)
	}
	st, err := os.Stat(filepath.Join(f.account, ".claude.json"))
	if err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, %v", st.Mode(), err)
	}

	stash := f.read(t, DisplacedName)
	disp := servers(t, stash)
	if len(disp) != 2 || disp["a"].(map[string]any)["url"] != "https://old.example.invalid/mcp" || disp["c"] == nil {
		t.Fatalf("displaced = %v", disp)
	}
	if st, err := os.Stat(filepath.Join(f.account, DisplacedName)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("displaced mode = %v, %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(f.account, MarkerName)); err != nil {
		t.Fatalf("marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.account, ".claude.json.lock")); !os.IsNotExist(err) {
		t.Fatalf("lock left behind: %v", err)
	}

	// Adopted: a later change in the account is overwritten, and the stash is
	// never rewritten.
	f.accountFile(t, `{"mcpServers": {"z": {}}, "numStartups": 13}`, 0o600)
	if wrote, err := Mirror(f.home, f.account); err != nil || !wrote {
		t.Fatalf("second Mirror = %v, %v", wrote, err)
	}
	if got := servers(t, f.read(t, ".claude.json")); len(got) != 2 || got["z"] != nil {
		t.Fatalf("second mcpServers = %v", got)
	}
	if !bytes.Equal(f.read(t, DisplacedName), stash) {
		t.Fatal("displaced file was rewritten")
	}
}

// AC-SMC-005: a source without mcpServers empties the account; a broken
// source, a non-object mcpServers, or a missing source leaves it unchanged.
func TestMirrorSourceShapes(t *testing.T) {
	f := newFixture(t)
	f.source(t, `{"numStartups": 1, "projects": {"/x": {"mcpServers": {"p": {}}}}}`)
	f.accountFile(t, `{"mcpServers": {"a": {}}, "projects": {"/y": {"mcpServers": {"keep": {}}}}}`, 0o600)
	if wrote, err := Mirror(f.home, f.account); err != nil || !wrote {
		t.Fatalf("Mirror = %v, %v", wrote, err)
	}
	data := f.read(t, ".claude.json")
	if got := servers(t, data); len(got) != 0 {
		t.Fatalf("mcpServers = %v, want {}", got)
	}
	if !bytes.Contains(data, []byte(`"keep"`)) {
		t.Fatalf("project servers lost: %s", data)
	}

	for _, broken := range []string{`{"mcpServers": `, `[1]`, `{"mcpServers": []}`, `{"mcpServers": null}`, `{} {}`} {
		f.source(t, broken)
		f.accountFile(t, `{"mcpServers": {"a": {}}}`, 0o600)
		if wrote, err := Mirror(f.home, f.account); err == nil || wrote {
			t.Fatalf("source %q: Mirror = %v, %v; want untouched", broken, wrote, err)
		}
		if got := string(f.read(t, ".claude.json")); got != `{"mcpServers": {"a": {}}}` {
			t.Fatalf("source %q: account = %s", broken, got)
		}
	}
	if err := os.Remove(filepath.Join(f.home, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if wrote, err := Mirror(f.home, f.account); err == nil || wrote {
		t.Fatalf("missing source: Mirror = %v, %v", wrote, err)
	}
}

// The account file must be a regular file holding an object whose
// mcpServers is an object; else it is left untouched.
func TestMirrorAccountShapes(t *testing.T) {
	f := newFixture(t)
	f.source(t, `{"mcpServers": {"a": {}}}`)
	if wrote, err := Mirror(f.home, f.account); err == nil || wrote {
		t.Fatalf("missing account file: Mirror = %v, %v", wrote, err)
	}
	for _, bad := range []string{`{"mcpServers": []}`, `not json`, `[]`} {
		f.accountFile(t, bad, 0o600)
		if wrote, err := Mirror(f.home, f.account); err == nil || wrote {
			t.Fatalf("account %q: Mirror = %v, %v", bad, wrote, err)
		}
		if got := string(f.read(t, ".claude.json")); got != bad {
			t.Fatalf("account %q changed to %q", bad, got)
		}
	}

	// A symlinked account file is never written through or replaced.
	target := filepath.Join(t.TempDir(), "real.json")
	if err := os.WriteFile(target, []byte(`{"mcpServers": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(f.account, ".claude.json")
	_ = os.Remove(p)
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	if wrote, err := Mirror(f.home, f.account); err == nil || wrote {
		t.Fatalf("symlink: Mirror = %v, %v", wrote, err)
	}
	if data, _ := os.ReadFile(target); string(data) != `{"mcpServers": {}}` {
		t.Fatalf("symlink target changed: %s", data)
	}

	// The built-in home itself is never a target.
	if wrote, err := Mirror(f.home, f.home); err == nil || wrote {
		t.Fatalf("built-in as account: Mirror = %v, %v", wrote, err)
	}
}

// AC-SMC-006: a held fresh lock leaves the account unchanged after at most
// the lock timeout; a lock older than the staleness is taken over.
func TestMirrorLock(t *testing.T) {
	oldTimeout, oldStale := LockTimeout, LockStaleness
	t.Cleanup(func() { LockTimeout, LockStaleness = oldTimeout, oldStale })
	LockTimeout = 300 * time.Millisecond

	f := newFixture(t)
	f.source(t, `{"mcpServers": {"a": {}}}`)
	f.accountFile(t, `{"mcpServers": {}}`, 0o600)
	lockDir := filepath.Join(f.account, ".claude.json.lock")
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	wrote, err := Mirror(f.home, f.account)
	if err != ErrLockTimeout || wrote {
		t.Fatalf("held lock: Mirror = %v, %v", wrote, err)
	}
	if d := time.Since(start); d > LockTimeout+time.Second {
		t.Fatalf("waited %v", d)
	}
	if got := string(f.read(t, ".claude.json")); got != `{"mcpServers": {}}` {
		t.Fatalf("account changed under a held lock: %s", got)
	}
	if _, err := os.Stat(lockDir); err != nil {
		t.Fatalf("held lock was removed: %v", err)
	}

	// The same lock, 11 s old, is stale and taken over.
	old := time.Now().Add(-11 * time.Second)
	if err := os.Chtimes(lockDir, old, old); err != nil {
		t.Fatal(err)
	}
	if wrote, err := Mirror(f.home, f.account); err != nil || !wrote {
		t.Fatalf("stale lock: Mirror = %v, %v", wrote, err)
	}
	if got := servers(t, f.read(t, ".claude.json")); !reflect.DeepEqual(got, map[string]any{"a": map[string]any{}}) {
		t.Fatalf("mcpServers = %v", got)
	}
	if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
		t.Fatalf("lock left behind: %v", err)
	}
}

// The default timings are the spec's.
func TestLockDefaults(t *testing.T) {
	if LockTimeout != 2*time.Second || LockStaleness != 10*time.Second {
		t.Fatalf("timings = %v, %v", LockTimeout, LockStaleness)
	}
}

// AC-SMC-007: in sync and adopted → nothing is written, no lock is taken.
func TestMirrorInSyncAdopted(t *testing.T) {
	f := newFixture(t)
	f.source(t, `{"mcpServers": {"a": {"url": "u"}}}`)
	f.accountFile(t, `{"mcpServers": {"a": {"url": "u"}}}`, 0o600)

	// Not adopted yet: in sync, so only the marker is created.
	if wrote, err := Mirror(f.home, f.account); err != nil || wrote {
		t.Fatalf("first Mirror = %v, %v", wrote, err)
	}
	if _, err := os.Stat(filepath.Join(f.account, MarkerName)); err != nil {
		t.Fatalf("marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.account, DisplacedName)); !os.IsNotExist(err) {
		t.Fatalf("displaced written when nothing was displaced: %v", err)
	}

	p := filepath.Join(f.account, ".claude.json")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	// A held lock would make a locked path fail; the steady state takes none.
	if err := os.Mkdir(p+".lock", 0o755); err != nil {
		t.Fatal(err)
	}
	if wrote, err := Mirror(f.home, f.account); err != nil || wrote {
		t.Fatalf("steady Mirror = %v, %v", wrote, err)
	}
	st, err := os.Stat(p)
	if err != nil || !st.ModTime().Equal(old) {
		t.Fatalf("mtime = %v, want %v (%v)", st.ModTime(), old, err)
	}
}

// The encoder keeps member order and raw values, and the trailing newline.
func TestObjectRoundTrip(t *testing.T) {
	in := "{\n  \"b\": {\n    \"x\":  1\n  },\n  \"a\": [1,2],\n  \"mcpServers\": {}\n}\n"
	o, err := decodeObject([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(o.encode()); got != in {
		t.Fatalf("round trip:\n%q\n%q", got, in)
	}
	o.remove("mcpServers")
	o.set("c", json.RawMessage(`true`))
	if got := string(o.encode()); got != "{\n  \"b\": {\n    \"x\":  1\n  },\n  \"a\": [1,2],\n  \"c\": true\n}\n" {
		t.Fatalf("edited: %q", got)
	}
}
