package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestWorkbenchHookInsidePane(t *testing.T) {
	oldInPane, oldRoot := hookInPane, hookGitRoot
	t.Cleanup(func() { hookInPane, hookGitRoot = oldInPane, oldRoot })
	hookInPane = func() (string, bool) { return "ws-12", true }
	var gotDir string
	hookGitRoot = func(dir string) string { gotDir = dir; return "/repo" }

	var out bytes.Buffer
	in := strings.NewReader(`{"source":"startup","cwd":"/repo/sub"}`)
	if code := workbenchSessionStart(in, &out); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if gotDir != "/repo/sub" {
		t.Errorf("git lookup dir = %q, want the hook cwd /repo/sub", gotDir)
	}
	var got hookOutput
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("stdout is not one JSON object: %v (%q)", err, out.String())
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Errorf("stdout holds more than one JSON value: %q", out.String())
	}
	if got.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q, want SessionStart", got.HookSpecificOutput.HookEventName)
	}
	text := got.HookSpecificOutput.AdditionalContext
	if !strings.Contains(text, "session ws-12") {
		t.Errorf("context = %q, want the session id", text)
	}
	if !strings.Contains(text, "/repo") {
		t.Errorf("context = %q, want the worktree root", text)
	}
	if !strings.Contains(text, "reported automatically") {
		t.Errorf("context = %q, want the automatic-report sentence", text)
	}
	if !strings.Contains(text, "`lana workbench use --new <branch>`") {
		t.Errorf("context = %q, want the create-worktree line", text)
	}
	if !strings.Contains(text, "Subagents do not need to report.") {
		t.Errorf("context = %q, want the subagent line", text)
	}
}

func TestWorkbenchHookOutsidePane(t *testing.T) {
	oldInPane := hookInPane
	t.Cleanup(func() { hookInPane = oldInPane })
	hookInPane = func() (string, bool) { return "", false }

	var out bytes.Buffer
	if code := workbenchSessionStart(strings.NewReader(`{"source":"startup"}`), &out); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing", out.String())
	}
}

func TestWorkbenchHookBadInput(t *testing.T) {
	oldInPane, oldRoot := hookInPane, hookGitRoot
	t.Cleanup(func() { hookInPane, hookGitRoot = oldInPane, oldRoot })
	hookInPane = func() (string, bool) { return "ws-1", true }
	hookGitRoot = func(string) string { return "/repo" }

	var out bytes.Buffer
	if code := workbenchSessionStart(strings.NewReader("not json"), &out); code != 0 {
		t.Fatalf("exit = %d, want 0 for bad stdin", code)
	}
}

func TestWorkbenchHookUnknownEvent(t *testing.T) {
	if code := cmdWorkbenchHook(nil); code != 2 {
		t.Fatalf("missing hook exit = %d, want 2", code)
	}
	if code := cmdWorkbenchHook([]string{"nope"}); code != 2 {
		t.Fatalf("unknown hook exit = %d, want 2", code)
	}
}
