package agenthook

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readCommands returns every SessionStart hook command in settings.json.
func readCommands(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks struct {
			SessionStart []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"SessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v\n%s", err, data)
	}
	var commands []string
	for _, entry := range doc.Hooks.SessionStart {
		for _, hook := range entry.Hooks {
			commands = append(commands, hook.Command)
		}
	}
	return commands
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func TestCommandQuotesTheBinary(t *testing.T) {
	got := Command("/Users/me/.local/bin/lana")
	want := `[ -n "$LANA_WORKBENCH_SESSION" ] && exec '/Users/me/.local/bin/lana' workbench hook session-start; exit 0`
	if got != want {
		t.Fatalf("Command = %q, want %q", got, want)
	}
}

// readHookCommands returns every hook command of one event in settings.json.
func readHookCommands(t *testing.T, path, event string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v\n%s", err, data)
	}
	var commands []string
	for _, entry := range doc.Hooks[event] {
		for _, hook := range entry.Hooks {
			commands = append(commands, hook.Command)
		}
	}
	return commands
}

func TestPreEditCommandQuotesTheBinary(t *testing.T) {
	got := PreEditCommand("/Users/me/.local/bin/lana")
	want := `[ -n "$LANA_WORKBENCH_SESSION" ] && exec '/Users/me/.local/bin/lana' workbench hook pre-edit; exit 0`
	if got != want {
		t.Fatalf("PreEditCommand = %q, want %q", got, want)
	}
	const quoted = "/Users/o'brien/bin/lana"
	if got := PreEditCommand(quoted); !strings.Contains(got, `'\''`) {
		t.Fatalf("PreEditCommand %q does not escape the single quote", got)
	}
}

func TestInstallAddsBothHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	const bin = "/opt/lana/lana"

	changed, err := Install(path, bin)
	if err != nil || !changed {
		t.Fatalf("Install = %v, %v", changed, err)
	}
	if got := readHookCommands(t, path, "SessionStart"); len(got) != 1 || got[0] != Command(bin) {
		t.Fatalf("SessionStart commands = %q", got)
	}
	if got := readHookCommands(t, path, "PreToolUse"); len(got) != 1 || got[0] != PreEditCommand(bin) {
		t.Fatalf("PreToolUse commands = %q", got)
	}
	if installed, err := Installed(path, bin); err != nil || !installed {
		t.Fatalf("Installed = %v, %v", installed, err)
	}
	if changed, err := Install(path, bin); err != nil || changed {
		t.Fatalf("second Install = %v, %v, want no change", changed, err)
	}
}

func TestInstallUpgradesSessionStartOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	const bin = "/opt/lana/lana"
	writeFile(t, path, `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":`+jsonStringForTest(Command(bin))+`}]}]}}`, 0o600)

	if installed, err := Installed(path, bin); err != nil || installed {
		t.Fatalf("Installed with only SessionStart = %v, %v, want false", installed, err)
	}
	changed, err := Install(path, bin)
	if err != nil || !changed {
		t.Fatalf("Install = %v, %v", changed, err)
	}
	if got := readHookCommands(t, path, "PreToolUse"); len(got) != 1 || got[0] != PreEditCommand(bin) {
		t.Fatalf("PreToolUse commands = %q", got)
	}
	if installed, err := Installed(path, bin); err != nil || !installed {
		t.Fatalf("Installed after upgrade = %v, %v", installed, err)
	}
	if changed, err := Install(path, bin); err != nil || changed {
		t.Fatalf("second Install = %v, %v, want no change", changed, err)
	}
}

func TestInstallKeepsRtkPreToolUseEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	const bin = "/opt/lana/lana"
	const original = `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk hook bash"}]}
    ]
  }
}
`
	writeFile(t, path, original, 0o600)

	if changed, err := Install(path, bin); err != nil || !changed {
		t.Fatalf("Install = %v, %v", changed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, `"matcher": "Bash"`) || !strings.Contains(out, "rtk hook bash") {
		t.Fatalf("the rtk entry changed:\n%s", out)
	}
	if !strings.Contains(out, `"matcher": "Edit|Write|MultiEdit|NotebookEdit"`) {
		t.Fatalf("the Lana matcher is missing:\n%s", out)
	}
	if strings.Index(out, "rtk hook bash") >= strings.Index(out, "workbench hook pre-edit") {
		t.Fatalf("the rtk entry moved after Lana's:\n%s", out)
	}
	if got := readHookCommands(t, path, "PreToolUse"); len(got) != 2 || got[0] != "rtk hook bash" || got[1] != PreEditCommand(bin) {
		t.Fatalf("PreToolUse commands = %q", got)
	}
}

func TestInstallFixesBothCommandsWhenTheBinaryMoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Install(path, "/old/lana"); err != nil {
		t.Fatal(err)
	}
	changed, err := Install(path, "/new/lana")
	if err != nil || !changed {
		t.Fatalf("Install = %v, %v", changed, err)
	}
	if got := readHookCommands(t, path, "SessionStart"); len(got) != 1 || got[0] != Command("/new/lana") {
		t.Fatalf("SessionStart commands = %q", got)
	}
	if got := readHookCommands(t, path, "PreToolUse"); len(got) != 1 || got[0] != PreEditCommand("/new/lana") {
		t.Fatalf("PreToolUse commands = %q", got)
	}
	if installed, _ := Installed(path, "/new/lana"); !installed {
		t.Fatal("Installed is false for the new binary")
	}
	if installed, _ := Installed(path, "/old/lana"); installed {
		t.Fatal("Installed is true for the old binary")
	}
}

func TestRemoveRemovesBothKeepsRtk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	const bin = "/opt/lana/lana"
	const original = `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk hook bash"}]}
    ]
  }
}
`
	writeFile(t, path, original, 0o600)
	if _, err := Install(path, bin); err != nil {
		t.Fatal(err)
	}
	changed, err := Remove(path)
	if err != nil || !changed {
		t.Fatalf("Remove = %v, %v", changed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if strings.Contains(out, "workbench hook session-start") || strings.Contains(out, "workbench hook pre-edit") {
		t.Fatalf("Remove left a Lana hook:\n%s", out)
	}
	if got := readHookCommands(t, path, "PreToolUse"); len(got) != 1 || got[0] != "rtk hook bash" {
		t.Fatalf("PreToolUse commands = %q, want the rtk entry", got)
	}
	if installed, _ := Installed(path, bin); installed {
		t.Fatal("Installed is true after Remove")
	}
}

func TestInstalledFalseWithOnlyOneHook(t *testing.T) {
	const bin = "/opt/lana/lana"
	for name, body := range map[string]string{
		"session-start only": `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":` + jsonStringForTest(Command(bin)) + `}]}]}}`,
		"pre-edit only":      `{"hooks":{"PreToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":` + jsonStringForTest(PreEditCommand(bin)) + `}]}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			writeFile(t, path, body, 0o600)
			installed, err := Installed(path, bin)
			if err != nil || installed {
				t.Fatalf("Installed = %v, %v, want false", installed, err)
			}
		})
	}
}

func TestCommandQuotesASingleQuoteInThePath(t *testing.T) {
	const dir = "/Users/o'brien/bin/lana"
	got := Command(dir)
	want := `[ -n "$LANA_WORKBENCH_SESSION" ] && exec '/Users/o'\''brien/bin/lana' workbench hook session-start; exit 0`
	if got != want {
		t.Fatalf("Command = %q, want %q", got, want)
	}
	if !strings.Contains(got, `'\''`) {
		t.Fatalf("Command %q does not escape the single quote", got)
	}
}

func TestInstallMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	bin := "/opt/lana/bin/lana"

	changed, err := Install(path, bin)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !changed {
		t.Fatal("Install did not report a change")
	}
	if commands := readCommands(t, path); len(commands) != 1 || commands[0] != Command(bin) {
		t.Fatalf("commands = %q", commands)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != newFileMode {
		t.Fatalf("mode = %o, want %o", got, newFileMode)
	}

	installed, err := Installed(path, bin)
	if err != nil || !installed {
		t.Fatalf("Installed = %v, %v", installed, err)
	}
}

func TestInstallEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, "", 0o600)

	changed, err := Install(path, "/opt/lana")
	if err != nil || !changed {
		t.Fatalf("Install = %v, %v", changed, err)
	}
	if commands := readCommands(t, path); len(commands) != 1 {
		t.Fatalf("commands = %q", commands)
	}
}

func TestInstallKeepsOtherKeysAndHooksInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	const original = `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "echo hi"}]}
    ],
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "other-tool start"}]}
    ]
  },
  "statusLine": {"type": "command", "command": "x"}
}
`
	writeFile(t, path, original, 0o600)

	changed, err := Install(path, "/opt/lana")
	if err != nil || !changed {
		t.Fatalf("Install = %v, %v", changed, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	// Every other key and hook is kept, in its original order.
	for _, key := range []string{"model", `"statusLine"`, "echo hi", `"PreToolUse"`, "other-tool start"} {
		if !strings.Contains(out, key) {
			t.Fatalf("installed file lost %q:\n%s", key, out)
		}
	}
	if strings.Index(out, `"model"`) >= strings.Index(out, `"hooks"`) ||
		strings.Index(out, `"hooks"`) >= strings.Index(out, `"statusLine"`) {
		t.Fatalf("top-level key order changed:\n%s", out)
	}
	if strings.Index(out, `"PreToolUse"`) >= strings.Index(out, `"SessionStart"`) {
		t.Fatalf("hooks key order changed:\n%s", out)
	}
	commands := readCommands(t, path)
	if len(commands) != 2 || commands[0] != "other-tool start" || commands[1] != Command("/opt/lana") {
		t.Fatalf("commands = %q", commands)
	}
}

func TestInstallUpdatesCommandWhenTheBinaryMoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Install(path, "/old/lana"); err != nil {
		t.Fatal(err)
	}
	changed, err := Install(path, "/new/lana")
	if err != nil || !changed {
		t.Fatalf("Install = %v, %v", changed, err)
	}
	commands := readCommands(t, path)
	if len(commands) != 1 || commands[0] != Command("/new/lana") {
		t.Fatalf("commands = %q", commands)
	}
	if installed, _ := Installed(path, "/old/lana"); installed {
		t.Fatal("Installed is true for the old binary")
	}
	if installed, _ := Installed(path, "/new/lana"); !installed {
		t.Fatal("Installed is false for the new binary")
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Install(path, "/opt/lana"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	changed, err := Install(path, "/opt/lana")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second Install reported a change")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("second Install rewrote the file:\n%s\n%s", before, after)
	}
	if again, err := os.Stat(path); err != nil || !again.ModTime().Equal(info.ModTime()) {
		t.Fatalf("second Install rewrote the file (mtime changed): %v", err)
	}
}

func TestRemoveDropsEmptyContainersKeepsOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Install(path, "/opt/lana"); err != nil {
		t.Fatal(err)
	}
	// Add another hook and re-install so Lana sits between two entries.
	writeFile(t, path, `{
  "hooks": {
    "PreToolUse": [{"hooks": [{"type": "command", "command": "guard"}]}],
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "other"}]},
      {"hooks": [{"type": "command", "command": `+jsonStringForTest(Command("/opt/lana"))+`}]},
      {"hooks": [{"type": "command", "command": "extra"}]}
    ]
  }
}
`, 0o600)

	changed, err := Remove(path)
	if err != nil || !changed {
		t.Fatalf("Remove = %v, %v", changed, err)
	}
	commands := readCommands(t, path)
	if len(commands) != 2 || commands[0] != "other" || commands[1] != "extra" {
		t.Fatalf("commands after remove = %q", commands)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"PreToolUse"`) || !strings.Contains(string(data), "guard") {
		t.Fatalf("Remove dropped an unrelated hook:\n%s", data)
	}
}

func TestRemoveDropsEmptySessionStartAndHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Install(path, "/opt/lana"); err != nil {
		t.Fatal(err)
	}
	changed, err := Remove(path)
	if err != nil || !changed {
		t.Fatalf("Remove = %v, %v", changed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "{}" {
		t.Fatalf("file after remove = %q, want {}", got)
	}
	// A second Remove is a no-op.
	again, err := Remove(path)
	if err != nil || again {
		t.Fatalf("second Remove = %v, %v", again, err)
	}
}

func TestRemoveMissingFileIsNoChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	changed, err := Remove(path)
	if err != nil || changed {
		t.Fatalf("Remove = %v, %v", changed, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove created the file: %v", err)
	}
}

func TestRemoveKeepsUnreadableEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"hooks": {"SessionStart": ["scalar", {"nohooks": true}, {"hooks": [{"command": `+jsonStringForTest(Command("/opt/lana"))+`}]}]}}`, 0o600)

	changed, err := Remove(path)
	if err != nil || !changed {
		t.Fatalf("Remove = %v, %v", changed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "scalar") || !strings.Contains(out, "nohooks") {
		t.Fatalf("Remove dropped an entry it cannot read:\n%s", out)
	}
	if strings.Contains(out, "workbench hook session-start") {
		t.Fatalf("Remove left Lana's hook:\n%s", out)
	}
}

func TestInvalidSettingsLeftAlone(t *testing.T) {
	for _, body := range []string{"{ not json", "[1,2,3]", `"a string"`, "42"} {
		t.Run(body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			writeFile(t, path, body, 0o600)

			if _, err := Install(path, "/opt/lana"); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("Install error = %v, want ErrInvalidSettings", err)
			}
			if _, err := Remove(path); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("Remove error = %v, want ErrInvalidSettings", err)
			}
			if _, err := Installed(path, "/opt/lana"); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("Installed error = %v, want ErrInvalidSettings", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != body {
				t.Fatalf("file changed: %q, want %q", data, body)
			}
		})
	}
}

func TestBackupWrittenOnceNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	const original = `{"model":"opus"}` + "\n"
	writeFile(t, path, original, 0o600)

	if _, err := Install(path, "/opt/lana"); err != nil {
		t.Fatal(err)
	}
	backup := path + backupSuffix
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("backup was not written: %v", err)
	}
	if string(data) != original {
		t.Fatalf("backup = %q, want %q", data, original)
	}

	// A second change does not overwrite the first backup.
	if err := os.WriteFile(backup, []byte("SENTINEL"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := Install(path, "/moved/lana"); err != nil || !changed {
		t.Fatalf("Install = %v, %v", changed, err)
	}
	data, err = os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "SENTINEL" {
		t.Fatalf("backup was overwritten: %q", data)
	}
}

func TestFileModeKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"model":"opus"}`, 0o640)

	if _, err := Install(path, "/opt/lana"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %o, want %o", got, 0o640)
	}
}

// jsonStringForTest renders a string as a JSON string literal for test fixtures.
func jsonStringForTest(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}
