package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// mustEval resolves symlinks so a comparison matches the guard's output on a
// platform whose temp directory sits behind a symlink.
func mustEval(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

// mustJSON marshals a tool input, failing the test on error.
func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCheckWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := checkWorkspace(dir); err != nil {
		t.Fatalf("existing directory: %v", err)
	}

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkWorkspace(file); err == nil || !strings.Contains(err.Error(), "workspace is not a directory:") {
		t.Fatalf("regular file error=%v", err)
	}

	missing := filepath.Join(dir, "missing")
	err := checkWorkspace(missing)
	if err == nil || !strings.Contains(err.Error(), "workspace does not exist:") || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing path error=%v", err)
	}
}

func TestResolveInWorkspaceSymlinks(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()

	// A dangling symlink to outside is rejected even though its target does not
	// exist yet.
	if err := os.Symlink(filepath.Join(outside, "missing"), filepath.Join(ws, "dangling")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveInWorkspace(ws, "dangling"); !errors.Is(err, ErrOutsideWorkspace) {
		t.Fatalf("dangling err=%v, want outside workspace", err)
	}

	// A new file below a symlinked directory that points outside is rejected.
	if err := os.Symlink(outside, filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveInWorkspace(ws, "link/newdir/f.txt"); !errors.Is(err, ErrOutsideWorkspace) {
		t.Fatalf("symlinked dir err=%v, want outside workspace", err)
	}

	// A normal new file inside the workspace is accepted.
	got, err := ResolveInWorkspace(ws, "sub/new.txt")
	if err != nil {
		t.Fatalf("inside new file: %v", err)
	}
	if want := filepath.Join(mustEval(t, ws), "sub", "new.txt"); got != want {
		t.Fatalf("inside new file got=%q want=%q", got, want)
	}

	// A symlink that stays inside the workspace is accepted and resolved.
	if err := os.MkdirAll(filepath.Join(ws, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ws, "real"), filepath.Join(ws, "inside")); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveInWorkspace(ws, "inside/f.txt")
	if err != nil {
		t.Fatalf("inside symlink: %v", err)
	}
	if want := filepath.Join(mustEval(t, ws), "real", "f.txt"); got != want {
		t.Fatalf("inside symlink got=%q want=%q", got, want)
	}
}

func TestResolvePathAnywhere(t *testing.T) {
	ws := t.TempDir()

	// A relative path joins the workspace.
	got, err := ResolvePathAnywhere(ws, "sub/new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(mustEval(t, ws), "sub", "new.txt"); got != want {
		t.Fatalf("relative got=%q want=%q", got, want)
	}

	// A leading ~ expands to the user home.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err = ResolvePathAnywhere(ws, "~/lana-tilde.txt")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(mustEval(t, home), "lana-tilde.txt"); got != want {
		t.Fatalf("tilde got=%q want=%q", got, want)
	}
}

func TestFileToolsWorkspaceOnly(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The default (workspace_only off) reaches an absolute path outside the
	// workspace for both reading and writing.
	out := callToolCtx(t, NewReadFile(&Guard{Workspace: ws}), core.ToolCtx{Workspace: ws}, mustJSON(t, map[string]string{"path": outside}))
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "outside content") {
		t.Fatalf("read outside (open) out=%+v", out)
	}
	target := filepath.Join(t.TempDir(), "new", "file.txt")
	out = callToolCtx(t, NewWriteFile(&Guard{Workspace: ws}), core.ToolCtx{Workspace: ws}, mustJSON(t, map[string]string{"path": target, "content": "hi"}))
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("write outside (open) out=%+v", out)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("file not written: %v", err)
	}

	// workspace_only on rejects the same calls.
	rejected := callToolCtx(t, NewReadFile(&Guard{Workspace: ws}), core.ToolCtx{Workspace: ws, WorkspaceOnly: true}, mustJSON(t, map[string]string{"path": outside}))
	if rejected.Status != core.ToolStatusRejected || rejected.Output != "outside workspace" {
		t.Fatalf("read outside (workspace_only) out=%+v", rejected)
	}
	rejected = callToolCtx(t, NewWriteFile(&Guard{Workspace: ws}), core.ToolCtx{Workspace: ws, WorkspaceOnly: true}, mustJSON(t, map[string]string{"path": filepath.Join(t.TempDir(), "x.txt"), "content": "hi"}))
	if rejected.Status != core.ToolStatusRejected || rejected.Output != "outside workspace" {
		t.Fatalf("write outside (workspace_only) out=%+v", rejected)
	}
}
