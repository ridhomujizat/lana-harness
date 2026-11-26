package agenthook

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
)

// Antigravity (agy) hooks of spec 77 REQ-WRA-009. agy reads its global hooks
// from <HOME>/.gemini/config/hooks.json. The top-level keys are hook names; each
// names an object of events. SessionStart is a flat list of command handlers;
// PostToolUse is a list of groups with a "matcher" regex and "hooks". agy runs
// a command through `sh -c` with the hooks.json folder as cwd. Lana owns only
// the "lana-workbench" entry and keeps every other key as it is.

// ErrInvalidHooks reports a hooks.json that is not a JSON object Lana can merge
// into. The file is left untouched.
var ErrInvalidHooks = errors.New("hooks.json is not valid JSON")

// AgyHookName is the top-level key of Lana's entry in hooks.json.
const AgyHookName = "lana-workbench"

// AgyHooksFile is the name of the agy hooks file in its config folder.
const AgyHooksFile = "hooks.json"

// AgyConfigDir is the agy config folder of a HOME: <home>/.gemini/config.
func AgyConfigDir(home string) string {
	return filepath.Join(home, ".gemini", "config")
}

// agyEditMatcher is the PostToolUse matcher: the agy tools that write a file
// whose worktree the hook reports.
const agyEditMatcher = "write_file|edit_file|write_to_file|replace_file_content|multi_replace_file_content|create_file|delete_file"

// agyHookTimeout is the timeout, in seconds, of each Lana handler.
const agyHookTimeout = 10

// AgySessionStartCommand is the agy SessionStart command. Outside a pane, or
// when the lana binary is gone, it prints `{}` without running lana, so agy
// always gets an answer it accepts.
func AgySessionStartCommand(lanaBinary string) string {
	return agyCommand(lanaBinary, "session-start")
}

// AgyPostEditCommand is the agy PostToolUse command that reports the worktree
// an edit landed in. Like AgySessionStartCommand it prints `{}` itself
// outside a pane.
func AgyPostEditCommand(lanaBinary string) string {
	return agyCommand(lanaBinary, "post-edit")
}

func agyCommand(lanaBinary, event string) string {
	bin := quoteBinary(lanaBinary)
	return `if [ -n "$LANA_WORKBENCH_SESSION" ] && [ -x ` + bin + ` ]; then exec ` + bin +
		" workbench hook " + event + ` --agent agy; fi; echo '{}'`
}

// agyEntry builds Lana's "lana-workbench" entry.
func agyEntry(lanaBinary string) json.RawMessage {
	handler := func(command string) *orderedObject {
		h := newObject()
		h.set("type", marshalValue("command"))
		h.set("command", marshalValue(command))
		h.set("timeout", marshalValue(agyHookTimeout))
		return h
	}
	group := newObject()
	group.set("matcher", marshalValue(agyEditMatcher))
	group.set("hooks", marshalRawArray([]json.RawMessage{marshalValue(handler(AgyPostEditCommand(lanaBinary)))}))
	entry := newObject()
	entry.set("SessionStart", marshalRawArray([]json.RawMessage{marshalValue(handler(AgySessionStartCommand(lanaBinary)))}))
	entry.set("PostToolUse", marshalRawArray([]json.RawMessage{marshalValue(group)}))
	return marshalValue(entry)
}

// sameJSON reports whether two JSON values are equal, whatever their layout.
func sameJSON(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

// loadAgy reads hooks.json, mapping an unusable file to ErrInvalidHooks.
func loadAgy(hooksPath string) (*orderedObject, bool, error) {
	top, existed, err := load(hooksPath)
	if errors.Is(err, ErrInvalidSettings) {
		return nil, existed, ErrInvalidHooks
	}
	return top, existed, err
}

// InstallAgy adds or updates Lana's "lana-workbench" entry in the agy
// hooks.json at hooksPath (spec 77 REQ-WRA-009). It reports whether it changed
// the file; a second call with the same binary leaves it untouched. A missing
// file starts from an empty object and its folder is created (0700). A file
// that is not a JSON object is left alone with ErrInvalidHooks. A symlinked
// hooks.json is written through (the target is replaced atomically, the link
// stays). Before the first change to an existing file it writes
// hooks.json.lana-bak, once.
func InstallAgy(hooksPath, lanaBinary string) (bool, error) {
	hooksPath = resolveLink(hooksPath)
	top, existed, err := loadAgy(hooksPath)
	if err != nil {
		return false, err
	}
	want := agyEntry(lanaBinary)
	if have, ok := top.get(AgyHookName); ok && sameJSON(have, want) {
		return false, nil
	}
	top.set(AgyHookName, want)
	if err := writeSettings(hooksPath, existed, top); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveAgy takes Lana's "lana-workbench" entry out of the agy hooks.json. A
// missing file or entry is no change. Every other key is kept.
func RemoveAgy(hooksPath string) (bool, error) {
	hooksPath = resolveLink(hooksPath)
	top, existed, err := loadAgy(hooksPath)
	if err != nil {
		return false, err
	}
	if _, ok := top.get(AgyHookName); !ok {
		return false, nil
	}
	top.del(AgyHookName)
	if err := writeSettings(hooksPath, existed, top); err != nil {
		return false, err
	}
	return true, nil
}

// InstalledAgy reports whether the agy hooks.json holds Lana's entry for
// lanaBinary exactly. A missing file is not installed; a file that is not a
// JSON object is ErrInvalidHooks.
func InstalledAgy(hooksPath, lanaBinary string) (bool, error) {
	top, _, err := loadAgy(hooksPath)
	if err != nil {
		return false, err
	}
	have, ok := top.get(AgyHookName)
	return ok && sameJSON(have, agyEntry(lanaBinary)), nil
}
