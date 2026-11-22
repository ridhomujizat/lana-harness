// Package agenthook installs the Claude Code SessionStart hook that tells a
// session it runs in a Lana Workbench pane (spec 69, REQ-WA-012 to REQ-WA-014).
// The hook entry lives in the settings.json of a Claude Code config folder;
// Install and Remove merge into it and keep every other key, hook and value.
package agenthook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrInvalidSettings reports a settings.json that is not a JSON object Lana can
// merge into. The file is left untouched.
var ErrInvalidSettings = errors.New("settings.json is not valid JSON")

// hookNeedle identifies Lana's SessionStart hook in a settings.json: its
// command contains this fragment (REQ-WA-013).
const hookNeedle = "workbench hook session-start"

// backupSuffix is the copy written next to settings.json before Lana's first
// change to it (REQ-WA-013), e.g. settings.json.lana-bak.
const backupSuffix = ".lana-bak"

// newFileMode is the mode of a settings.json Lana creates; an existing file
// keeps its mode.
const newFileMode os.FileMode = 0o600

// dirMode is the mode of a config folder Lana creates.
const dirMode os.FileMode = 0o700

// Command returns the SessionStart hook command of REQ-WA-012. It checks the
// Workbench variable in the shell first, so outside a pane no lana process
// runs, and it runs the installed binary by its absolute path. A single quote
// in the path is escaped for the shell.
func Command(lanaBinary string) string {
	quoted := "'" + strings.ReplaceAll(lanaBinary, "'", `'\''`) + "'"
	return `[ -n "$LANA_WORKBENCH_SESSION" ] && exec ` + quoted + " workbench hook session-start; exit 0"
}

// Install adds or updates Lana's SessionStart hook in the settings.json at
// settingsPath (REQ-WA-013). It reports whether it changed the file; a second
// call with the same command leaves the file untouched. A missing file starts
// from an empty object and its folder is created. A file that is not a JSON
// object is left alone with ErrInvalidSettings. Before the first change it
// writes a copy next to the file as settings.json.lana-bak, once.
func Install(settingsPath, lanaBinary string) (bool, error) {
	top, existed, err := load(settingsPath)
	if err != nil {
		return false, err
	}
	want := Command(lanaBinary)

	hooks, err := subObject(top, "hooks")
	if err != nil {
		return false, err
	}
	entries, err := subArray(hooks, "SessionStart")
	if err != nil {
		return false, err
	}

	found, changed := false, false
	for i, entryRaw := range entries {
		entry, ok := decodeObject(entryRaw)
		if !ok {
			continue
		}
		hookItems, ok := entryHooks(entry)
		if !ok {
			continue
		}
		entryChanged := false
		for j, hookRaw := range hookItems {
			hook, ok := decodeObject(hookRaw)
			if !ok {
				continue
			}
			command := objectString(hook, "command")
			if !strings.Contains(command, hookNeedle) {
				continue
			}
			found = true
			if command == want {
				continue
			}
			// The binary moved: fix the command in place.
			hook.set("command", marshalValue(want))
			hookItems[j] = marshalValue(hook)
			entryChanged = true
		}
		if entryChanged {
			entry.set("hooks", marshalRawArray(hookItems))
			entries[i] = marshalValue(entry)
			changed = true
		}
	}
	if !found {
		// No matcher, so the hook runs for every SessionStart source.
		entries = append(entries, newHookEntry(want))
		changed = true
	}
	if !changed {
		return false, nil
	}
	hooks.set("SessionStart", marshalRawArray(entries))
	top.set("hooks", marshalValue(hooks))
	if err := writeSettings(settingsPath, existed, top); err != nil {
		return false, err
	}
	return true, nil
}

// Remove takes Lana's SessionStart hook out of the settings.json at
// settingsPath. A SessionStart entry whose hooks list becomes empty is dropped,
// and so is the SessionStart key, then the hooks key, when their containers
// become empty. It reports whether it changed the file; a missing file or a
// file without the hook is no change. A file that is not a JSON object is left
// alone with ErrInvalidSettings.
func Remove(settingsPath string) (bool, error) {
	top, existed, err := load(settingsPath)
	if err != nil {
		return false, err
	}
	hooksRaw, ok := top.get("hooks")
	if !ok {
		return false, nil
	}
	hooks, ok := decodeObject(hooksRaw)
	if !ok {
		return false, fmt.Errorf("agenthook: %q is not a JSON object", "hooks")
	}
	entriesRaw, ok := hooks.get("SessionStart")
	if !ok {
		return false, nil
	}
	entries, ok := decodeArray(entriesRaw)
	if !ok {
		return false, fmt.Errorf("agenthook: %q is not a JSON array", "SessionStart")
	}

	changed := false
	kept := make([]json.RawMessage, 0, len(entries))
	for _, entryRaw := range entries {
		entry, ok := decodeObject(entryRaw)
		if !ok {
			kept = append(kept, entryRaw)
			continue
		}
		hookItems, ok := entryHooks(entry)
		if !ok {
			kept = append(kept, entryRaw)
			continue
		}
		remaining := make([]json.RawMessage, 0, len(hookItems))
		removed := false
		for _, hookRaw := range hookItems {
			hook, ok := decodeObject(hookRaw)
			if ok && strings.Contains(objectString(hook, "command"), hookNeedle) {
				removed = true
				continue
			}
			remaining = append(remaining, hookRaw)
		}
		if !removed {
			kept = append(kept, entryRaw)
			continue
		}
		changed = true
		if len(remaining) == 0 {
			continue // the entry's hooks list became empty: drop the entry
		}
		entry.set("hooks", marshalRawArray(remaining))
		kept = append(kept, marshalValue(entry))
	}
	if !changed {
		return false, nil
	}
	if len(kept) == 0 {
		hooks.del("SessionStart")
	} else {
		hooks.set("SessionStart", marshalRawArray(kept))
	}
	if hooks.empty() {
		top.del("hooks")
	} else {
		top.set("hooks", marshalValue(hooks))
	}
	if err := writeSettings(settingsPath, existed, top); err != nil {
		return false, err
	}
	return true, nil
}

// Installed reports whether Lana's SessionStart hook runs lanaBinary in the
// settings.json at settingsPath. A missing file, or a hook of another binary
// (one that moved before the daemon fixed it), is not installed; a file that is
// not a JSON object is ErrInvalidSettings.
func Installed(settingsPath, lanaBinary string) (bool, error) {
	top, _, err := load(settingsPath)
	if err != nil {
		return false, err
	}
	want := Command(lanaBinary)
	hooksRaw, ok := top.get("hooks")
	if !ok {
		return false, nil
	}
	hooks, ok := decodeObject(hooksRaw)
	if !ok {
		return false, fmt.Errorf("agenthook: %q is not a JSON object", "hooks")
	}
	entriesRaw, ok := hooks.get("SessionStart")
	if !ok {
		return false, nil
	}
	entries, ok := decodeArray(entriesRaw)
	if !ok {
		return false, fmt.Errorf("agenthook: %q is not a JSON array", "SessionStart")
	}
	for _, entryRaw := range entries {
		entry, ok := decodeObject(entryRaw)
		if !ok {
			continue
		}
		hookItems, ok := entryHooks(entry)
		if !ok {
			continue
		}
		for _, hookRaw := range hookItems {
			hook, ok := decodeObject(hookRaw)
			if !ok {
				continue
			}
			if objectString(hook, "command") == want {
				return true, nil
			}
		}
	}
	return false, nil
}

// load reads settingsPath into an ordered object. A missing or empty file is an
// empty object and existed is false for a missing one only; a file that is not
// a JSON object is ErrInvalidSettings.
func load(settingsPath string) (obj *orderedObject, existed bool, err error) {
	data, err := os.ReadFile(settingsPath)
	if errors.Is(err, os.ErrNotExist) {
		return newObject(), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return newObject(), true, nil
	}
	if !json.Valid(data) {
		return nil, true, ErrInvalidSettings
	}
	obj, ok := decodeObject(data)
	if !ok {
		return nil, true, ErrInvalidSettings
	}
	return obj, true, nil
}

// writeSettings writes obj to settingsPath with 2-space indentation and a
// trailing newline. Before the first change to an existing file it writes a
// copy as settings.json.lana-bak, once. The write is atomic (a temp file in the
// same folder, then a rename) and keeps the file's mode, 0600 for a new file.
func writeSettings(settingsPath string, existed bool, obj *orderedObject) error {
	if existed {
		if err := writeBackup(settingsPath); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), dirMode); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Keep the shell "&&" and any "$" in the command literal, as in the
	// api-contract example, instead of the encoder's \u escapes.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(obj); err != nil {
		return err
	}
	mode := newFileMode
	if info, err := os.Stat(settingsPath); err == nil {
		mode = info.Mode().Perm()
	}
	return atomicWrite(settingsPath, buf.Bytes(), mode)
}

// writeBackup copies settingsPath to settings.json.lana-bak unless the backup
// already exists.
func writeBackup(settingsPath string) error {
	backup := settingsPath + backupSuffix
	if _, err := os.Stat(backup); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return err
	}
	return os.WriteFile(backup, data, newFileMode)
}

// atomicWrite writes data to path through a temp file in the same folder.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// orderedObject is a JSON object that keeps its keys in the order read, so a
// round trip through the file changes only what Lana edits.
type orderedObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func newObject() *orderedObject {
	return &orderedObject{vals: map[string]json.RawMessage{}}
}

func (o *orderedObject) get(key string) (json.RawMessage, bool) {
	raw, ok := o.vals[key]
	return raw, ok
}

func (o *orderedObject) set(key string, raw json.RawMessage) {
	if o.vals == nil {
		o.vals = map[string]json.RawMessage{}
	}
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = raw
}

func (o *orderedObject) del(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *orderedObject) empty() bool { return len(o.keys) == 0 }

// MarshalJSON writes the object compactly with its keys in order; the encoder
// indents the result.
func (o *orderedObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		name, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		b.Write(name)
		b.WriteByte(':')
		raw, ok := o.vals[key]
		if !ok || len(raw) == 0 {
			b.WriteString("null")
			continue
		}
		b.Write(raw)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// decodeObject decodes one JSON object, keeping its key order. ok is false when
// raw is not a JSON object.
func decodeObject(raw []byte) (*orderedObject, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, false
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, false
	}
	obj := newObject()
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, false
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, false
		}
		obj.set(key, value)
	}
	return obj, true
}

// decodeArray decodes one JSON array into its raw elements. ok is false when
// raw is not a JSON array.
func decodeArray(raw []byte) ([]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, false
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '[' {
		return nil, false
	}
	var items []json.RawMessage
	for dec.More() {
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, false
		}
		items = append(items, value)
	}
	return items, true
}

// subObject returns the object at key, a fresh empty object when the key is
// absent. It is an error when the value is present but not an object.
func subObject(obj *orderedObject, key string) (*orderedObject, error) {
	raw, ok := obj.get(key)
	if !ok {
		return newObject(), nil
	}
	child, ok := decodeObject(raw)
	if !ok {
		return nil, fmt.Errorf("agenthook: %q is not a JSON object", key)
	}
	return child, nil
}

// subArray returns the array at key, or nil when the key is absent. It is an
// error when the value is present but not an array.
func subArray(obj *orderedObject, key string) ([]json.RawMessage, error) {
	raw, ok := obj.get(key)
	if !ok {
		return nil, nil
	}
	items, ok := decodeArray(raw)
	if !ok {
		return nil, fmt.Errorf("agenthook: %q is not a JSON array", key)
	}
	return items, nil
}

// entryHooks returns the hooks array of a SessionStart entry. ok is false when
// the entry has no readable hooks array.
func entryHooks(entry *orderedObject) ([]json.RawMessage, bool) {
	raw, ok := entry.get("hooks")
	if !ok {
		return nil, false
	}
	return decodeArray(raw)
}

// objectString returns the string value at key, or "" when it is absent or not
// a string.
func objectString(obj *orderedObject, key string) string {
	raw, ok := obj.get(key)
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

// newHookEntry builds the SessionStart entry Lana appends: one command hook
// with no matcher, so it runs for every source.
func newHookEntry(command string) json.RawMessage {
	hook := newObject()
	hook.set("type", marshalValue("command"))
	hook.set("command", marshalValue(command))
	entry := newObject()
	entry.set("hooks", marshalRawArray([]json.RawMessage{marshalValue(hook)}))
	return marshalValue(entry)
}

// marshalValue renders a value as JSON without HTML escaping and without the
// encoder's trailing newline.
func marshalValue(value any) json.RawMessage {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n"))
}

// marshalRawArray joins raw JSON elements into an array, byte for byte.
func marshalRawArray(items []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(item)
	}
	b.WriteByte(']')
	return b.Bytes()
}
