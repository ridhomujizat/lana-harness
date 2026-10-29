package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func callTool(t *testing.T, tool core.Tool, workspace, input string) core.ToolOutput {
	t.Helper()
	out, err := tool.Call(context.Background(), core.ToolCtx{Workspace: workspace}, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadFileLines(t *testing.T) {
	ws := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&b, "line-%d\n", i)
	}
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFile(&Guard{Workspace: ws})
	out := callTool(t, tool, ws, `{"path":"a.txt"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	lines := strings.Split(out.Output, "\n")
	if len(lines) != 2000 || lines[0] != "line-1" || lines[1999] != "line-2000" {
		t.Fatalf("lines=%d first=%q last=%q", len(lines), lines[0], lines[len(lines)-1])
	}
	out = callTool(t, tool, ws, `{"path":"a.txt","offset":10,"limit":5}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	want := "line-10\nline-11\nline-12\nline-13\nline-14"
	if out.Output != want {
		t.Fatalf("output=%q want %q", out.Output, want)
	}
}

func TestReadFileBinary(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "bin")
	if err := os.WriteFile(path, []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewReadFile(&Guard{Workspace: ws}), ws, `{"path":"bin"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "file biner") {
		t.Fatalf("out=%+v", out)
	}
}

func TestWriteFileCreate(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewWriteFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","content":"hello"}`)
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "5") {
		t.Fatalf("out=%+v", out)
	}
	data, err := os.ReadFile(filepath.Join(ws, "a.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestWriteFileOverwrite(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("lama"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewWriteFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","content":"baru"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "baru" {
		t.Fatalf("data=%q", data)
	}
}

func TestWriteFileNoParent(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewWriteFile(&Guard{Workspace: ws}), ws, `{"path":"nope/a.txt","content":"x"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "direktori induk") {
		t.Fatalf("out=%+v", out)
	}
	if _, err := os.Stat(filepath.Join(ws, "nope", "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("file dibuat: %v", err)
	}
}

func TestEditFileOnce(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("satu dua tiga"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","old_string":"dua","new_string":"D"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "satu D tiga" {
		t.Fatalf("data=%q", data)
	}
}

func TestEditFileCount(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("x x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewEditFile(&Guard{Workspace: ws})
	out := callTool(t, tool, ws, `{"path":"a.txt","old_string":"x","new_string":"y"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "2 kali") {
		t.Fatalf("out=%+v", out)
	}
	out = callTool(t, tool, ws, `{"path":"a.txt","old_string":"z","new_string":"y"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "tidak ditemukan") {
		t.Fatalf("out=%+v", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "x x" {
		t.Fatalf("file berubah: %q", data)
	}
}

func TestEditFileEmpty(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","old_string":"","new_string":"x"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "input tidak valid") {
		t.Fatalf("out=%+v", out)
	}
}

func TestGuardOutside(t *testing.T) {
	ws := t.TempDir()
	if _, err := ResolveInWorkspace(ws, "/etc/passwd"); !errors.Is(err, ErrOutsideWorkspace) {
		t.Fatalf("err=%v", err)
	}
	out := callTool(t, NewReadFile(&Guard{Workspace: ws}), ws, `{"path":"/etc/passwd"}`)
	if out.Status != core.ToolStatusRejected || out.Output != "di luar workspace" {
		t.Fatalf("out=%+v", out)
	}
}

func TestGuardSymlinkOut(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewReadFile(&Guard{Workspace: ws}), ws, `{"path":"link"}`)
	if out.Status != core.ToolStatusRejected {
		t.Fatalf("out=%+v", out)
	}
}
