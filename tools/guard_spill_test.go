package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/spill"
)

// spillHome saves text for sessionID and returns the root and the saved path.
func spillHome(t *testing.T, sessionID, text string) (string, string) {
	t.Helper()
	home := t.TempDir()
	store := spill.Store{Root: filepath.Join(home, "spill")}
	path, err := store.Save(sessionID, "bash", "call-1", text)
	if err != nil {
		t.Fatal(err)
	}
	return home, path
}

func TestReadFileSpillPath(t *testing.T) {
	home, path := spillHome(t, "s1", "spilled content\n")
	ws := t.TempDir()
	input, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	out, err := NewReadFile(&Guard{}).Call(context.Background(), core.ToolCtx{Workspace: ws, Home: home, SessionID: "s1"}, input)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "spilled content") {
		t.Fatalf("out=%+v", out)
	}
}

func TestReadFileOtherSessionSpillRefused(t *testing.T) {
	home, path := spillHome(t, "s1", "secret\n")
	ws := t.TempDir()
	input, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	out, err := NewReadFile(&Guard{}).Call(context.Background(), core.ToolCtx{Workspace: ws, Home: home, SessionID: "s2", WorkspaceOnly: true}, input)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusRejected {
		t.Fatalf("out=%+v", out)
	}
}

func TestGrepSpillFile(t *testing.T) {
	home, path := spillHome(t, "s1", "alpha\nneedle here\nomega\n")
	ws := t.TempDir()
	input, err := json.Marshal(map[string]string{"pattern": "needle", "path": path})
	if err != nil {
		t.Fatal(err)
	}
	out, err := NewGrep(&Guard{}).Call(context.Background(), core.ToolCtx{Workspace: ws, Home: home, SessionID: "s1"}, input)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "needle here") {
		t.Fatalf("out=%+v", out)
	}
}

func TestGlobSpillDir(t *testing.T) {
	home, _ := spillHome(t, "s1", "x")
	ws := t.TempDir()
	dir := spill.SessionDir(filepath.Join(home, "spill"), "s1")
	input, err := json.Marshal(map[string]string{"pattern": "*.txt", "path": dir})
	if err != nil {
		t.Fatal(err)
	}
	out, err := NewGlob(&Guard{}).Call(context.Background(), core.ToolCtx{Workspace: ws, Home: home, SessionID: "s1"}, input)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "bash.txt") {
		t.Fatalf("out=%+v", out)
	}
}
