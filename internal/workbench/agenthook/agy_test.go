package agenthook

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agyDoc decodes a hooks.json for the tests.
func agyDoc(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v\n%s", err, data)
	}
	return doc
}

// agyLana decodes Lana's entry of a hooks.json.
type agyLana struct {
	SessionStart []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	} `json:"SessionStart"`
	PostToolUse []struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"PostToolUse"`
}

func TestAgyCommands(t *testing.T) {
	got := AgySessionStartCommand("/opt/lana/bin/lana")
	want := `if [ -n "$LANA_WORKBENCH_SESSION" ] && [ -x '/opt/lana/bin/lana' ]; then exec '/opt/lana/bin/lana' workbench hook session-start --agent agy; fi; echo '{}'`
	if got != want {
		t.Fatalf("session-start command =\n%s\nwant\n%s", got, want)
	}
	if got := AgyPostEditCommand("/x/it's/lana"); !strings.Contains(got, `'/x/it'\''s/lana' workbench hook post-edit --agent agy`) {
		t.Fatalf("post-edit command = %s", got)
	}
}

func TestInstallAgyMissingFileCreatesFolder(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(AgyConfigDir(home), AgyHooksFile)
	changed, err := InstallAgy(path, "/bin/lana")
	if err != nil || !changed {
		t.Fatalf("InstallAgy = %v, %v", changed, err)
	}
	info, err := os.Stat(AgyConfigDir(home))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("config folder: %v %v", info, err)
	}
	if _, err := os.Stat(path + backupSuffix); !os.IsNotExist(err) {
		t.Fatalf("a new file must not get a backup: %v", err)
	}
	doc := agyDoc(t, path)
	if _, ok := doc["hooks"]; ok {
		t.Fatal("events must not be wrapped in a top-level hooks key")
	}
	var lana agyLana
	if err := json.Unmarshal(doc[AgyHookName], &lana); err != nil {
		t.Fatal(err)
	}
	if len(lana.SessionStart) != 1 || lana.SessionStart[0].Command != AgySessionStartCommand("/bin/lana") ||
		lana.SessionStart[0].Type != "command" || lana.SessionStart[0].Timeout != 10 {
		t.Fatalf("SessionStart = %+v", lana.SessionStart)
	}
	if len(lana.PostToolUse) != 1 || lana.PostToolUse[0].Matcher != agyEditMatcher ||
		len(lana.PostToolUse[0].Hooks) != 1 || lana.PostToolUse[0].Hooks[0].Command != AgyPostEditCommand("/bin/lana") ||
		lana.PostToolUse[0].Hooks[0].Timeout != 10 {
		t.Fatalf("PostToolUse = %+v", lana.PostToolUse)
	}
	if ok, err := InstalledAgy(path, "/bin/lana"); err != nil || !ok {
		t.Fatalf("InstalledAgy = %v, %v", ok, err)
	}
	if ok, _ := InstalledAgy(path, "/other/lana"); ok {
		t.Fatal("InstalledAgy must be false for another binary")
	}
}

func TestInstallAgyPreservesOthersIdempotentAndBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, AgyHooksFile)
	orig := `{"rtk":{"PreToolUse":[{"matcher":"run_command","hooks":[{"type":"command","command":"rtk hook"}]}]},"x-unknown":42}` + "\n"
	writeFile(t, path, orig, 0o644)

	if changed, err := InstallAgy(path, "/bin/lana"); err != nil || !changed {
		t.Fatalf("InstallAgy = %v, %v", changed, err)
	}
	if backup, err := os.ReadFile(path + backupSuffix); err != nil || string(backup) != orig {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	doc := agyDoc(t, path)
	if string(doc["x-unknown"]) != "42" || !strings.Contains(string(doc["rtk"]), "rtk hook") {
		t.Fatalf("other keys lost: %v", doc)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want kept 0644", info.Mode().Perm())
	}
	before, _ := os.ReadFile(path)
	if changed, err := InstallAgy(path, "/bin/lana"); err != nil || changed {
		t.Fatalf("second InstallAgy = %v, %v, want no change", changed, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("second install rewrote the file")
	}
	// A moved binary updates the entry; the backup is not overwritten.
	if changed, err := InstallAgy(path, "/new/lana"); err != nil || !changed {
		t.Fatalf("InstallAgy moved = %v, %v", changed, err)
	}
	if backup, _ := os.ReadFile(path + backupSuffix); string(backup) != orig {
		t.Fatal("backup overwritten")
	}

	if changed, err := RemoveAgy(path); err != nil || !changed {
		t.Fatalf("RemoveAgy = %v, %v", changed, err)
	}
	doc = agyDoc(t, path)
	if _, ok := doc[AgyHookName]; ok || len(doc) != 2 {
		t.Fatalf("after remove = %v", doc)
	}
	if changed, err := RemoveAgy(path); err != nil || changed {
		t.Fatalf("second RemoveAgy = %v, %v, want no change", changed, err)
	}
	if changed, err := RemoveAgy(filepath.Join(dir, "none.json")); err != nil || changed {
		t.Fatalf("RemoveAgy missing = %v, %v", changed, err)
	}
}

func TestInstallAgyInvalidLeftAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), AgyHooksFile)
	writeFile(t, path, "[1,2", 0o600)
	if _, err := InstallAgy(path, "/bin/lana"); !errors.Is(err, ErrInvalidHooks) {
		t.Fatalf("InstallAgy err = %v", err)
	}
	if _, err := InstalledAgy(path, "/bin/lana"); !errors.Is(err, ErrInvalidHooks) {
		t.Fatalf("InstalledAgy err = %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "[1,2" {
		t.Fatalf("file changed: %q", data)
	}
}

func TestInstallAgyWritesThroughSymlink(t *testing.T) {
	shared := filepath.Join(t.TempDir(), AgyHooksFile)
	writeFile(t, shared, `{"other":{}}`, 0o600)
	link := filepath.Join(AgyConfigDir(t.TempDir()), AgyHooksFile)
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, link); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallAgy(link, "/bin/lana"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v %v", info, err)
	}
	if _, ok := agyDoc(t, shared)[AgyHookName]; !ok {
		t.Fatal("target lacks the entry")
	}
	if _, err := os.Stat(shared + backupSuffix); err != nil {
		t.Fatalf("backup next to the target: %v", err)
	}
	if _, err := RemoveAgy(link); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced on remove: %v %v", info, err)
	}
}
