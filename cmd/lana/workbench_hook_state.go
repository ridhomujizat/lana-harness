package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// hookPreEditStateDirMode is the mode of the hook state folder Lana creates.
const hookPreEditStateDirMode os.FileMode = 0o700

// hookPreEditStateFileMode keeps a pane's hook state readable only by the
// owner.
const hookPreEditStateFileMode os.FileMode = 0o600

// hookPreEditState is the state of one pane session, so most edits stay a local
// comparison that runs no git and calls no daemon (spec 69).
type hookPreEditState struct {
	// CommonDir is the git common dir of the pane's repo, absolute.
	CommonDir string `json:"common_dir"`
	// HomeRoot is the worktree root of the pane's own folder.
	HomeRoot string `json:"home_root"`
	// Worktrees are the worktree roots of the repo, absolute.
	Worktrees []string `json:"worktrees"`
	// Current is the root last reported; "" means following the pane's own
	// folder.
	Current string `json:"current"`
}

// hookPreEditStatePath is the per pane session state file:
// <lana home>/workbench/hook-state/<pane session id>.json.
func hookPreEditStatePath(home, id string) string {
	return filepath.Join(home, "workbench", "hook-state", id+".json")
}

// hookPreEditReadState reads a pane session's state. ok is false when the file
// is missing or unusable, so the caller rebuilds it.
func hookPreEditReadState(path string) (hookPreEditState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return hookPreEditState{}, false
	}
	var state hookPreEditState
	if err := json.Unmarshal(data, &state); err != nil {
		return hookPreEditState{}, false
	}
	if state.CommonDir == "" || state.HomeRoot == "" {
		return hookPreEditState{}, false
	}
	return state, true
}

// hookPreEditWriteState writes a pane session's state atomically (a temp file
// in the same folder, then a rename) with mode 0600. A failure is ignored: the
// hook never fails the tool call.
func hookPreEditWriteState(path string, state hookPreEditState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, hookPreEditStateDirMode); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".hook-state-*.json")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Chmod(hookPreEditStateFileMode); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(name, path)
}

// workbenchHookStateRemember points a pane's saved hook state at the worktree
// root a manual `lana workbench use` reported, or at the pane's own folder
// again for root "". It writes the state only when the file already exists and
// never reports a failure: the command that called it must keep its output and
// exit code. The root is symlink-resolved the same way the hook stores its
// roots, and a root equal to the state's home_root is stored as "" (spec 69).
func workbenchHookStateRemember(home, id, root string) {
	if home == "" || id == "" {
		return
	}
	path := hookPreEditStatePath(home, id)
	state, ok := hookPreEditReadState(path)
	if !ok {
		// No saved state to update: the hook builds a fresh one on its next
		// edit, and `use` must not create the file.
		return
	}
	if root != "" {
		root = hookResolvedPath(root)
	}
	if root == state.HomeRoot {
		root = ""
	}
	if state.Current == root {
		return
	}
	state.Current = root
	hookPreEditWriteState(path, state)
}
