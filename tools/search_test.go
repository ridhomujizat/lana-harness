package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestGlobDoublestar(t *testing.T) {
	ws := t.TempDir()
	for _, name := range []string{"a.go", "b.txt", "ignored.go", "sub/c.go"} {
		path := filepath.Join(ws, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte("ignored.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewGlob(&Guard{Workspace: ws}), ws, `{"pattern":"**/*.go"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	want := "a.go\nsub/c.go"
	if out.Output != want {
		t.Fatalf("output=%q want %q", out.Output, want)
	}
}

func TestGlobLimit(t *testing.T) {
	ws := t.TempDir()
	for i := 0; i < 1001; i++ {
		if err := os.WriteFile(filepath.Join(ws, fmt.Sprintf("f%04d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := callTool(t, NewGlob(&Guard{Workspace: ws}), ws, `{"pattern":"*.txt"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if !strings.Contains(out.Output, "of 1001 results") {
		t.Fatalf("limit note missing: %s", out.Output)
	}
	lines := strings.Split(out.Output, "\n")
	if len(lines) != 1001 {
		t.Fatalf("lines=%d", len(lines))
	}
}

func TestGrepMatches(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("one\ntwo three\nfour"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewGrep(&Guard{Workspace: ws}), ws, `{"pattern":"two"}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "a.txt:2: two three" {
		t.Fatalf("out=%+v", out)
	}
}

func TestGrepInvalidRegex(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewGrep(&Guard{Workspace: ws}), ws, `{"pattern":"["}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "invalid pattern") {
		t.Fatalf("out=%+v", out)
	}
}

func TestGrepSkipBinaryAndLarge(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "bin"), []byte("needle\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("a", 1<<20+1) + "\nneedle\n"
	if err := os.WriteFile(filepath.Join(ws, "large.txt"), []byte(large), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "small.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewGrep(&Guard{Workspace: ws}), ws, `{"pattern":"needle"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if !strings.Contains(out.Output, "small.txt:1") || strings.Contains(out.Output, "bin:") || strings.Contains(out.Output, "large") {
		t.Fatalf("output=%q", out.Output)
	}
}

func TestGrepGlobFilter(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.go"), []byte("match\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("match\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewGrep(&Guard{Workspace: ws}), ws, `{"pattern":"match","glob":"*.go"}`)
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "a.go") || strings.Contains(out.Output, "a.txt") {
		t.Fatalf("out=%+v", out)
	}
	_ = json.RawMessage(nil)
}

func TestGlobMissingWorkspace(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "missing", "workspace")
	out := callTool(t, NewGlob(&Guard{Workspace: ws}), ws, `{"pattern":"**/*.go"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "workspace does not exist:") {
		t.Fatalf("out=%+v", out)
	}
}

func TestGrepMissingWorkspace(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "missing", "workspace")
	out := callTool(t, NewGrep(&Guard{Workspace: ws}), ws, `{"pattern":"needle"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "workspace does not exist:") {
		t.Fatalf("out=%+v", out)
	}
}
