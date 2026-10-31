package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestBashCwd(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"pwd"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	want, _ := filepath.EvalSymlinks(ws)
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(out.Output))
	if got != want {
		t.Fatalf("cwd=%q want %q", got, want)
	}
}

func TestBashTimeoutKillsGroup(t *testing.T) {
	ws := t.TempDir()
	start := time.Now()
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"sleep 5","timeout_secs":1}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "timed out after 1 seconds") {
		t.Fatalf("out=%+v", out)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout took too long: %v", time.Since(start))
	}
}

func TestBashTimeoutMax(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"touch should-not-exist","timeout_secs":601}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "between 1 and 600") {
		t.Fatalf("out=%+v", out)
	}
	if _, err := os.Stat(filepath.Join(ws, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatalf("command was executed: %v", err)
	}
}

func TestBashExitCode(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"echo out; echo err >&2; exit 7"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "exit code 7") {
		t.Fatalf("out=%+v", out)
	}
	if !strings.Contains(out.Output, "out") || !strings.Contains(out.Output, "err") {
		t.Fatalf("output incomplete: %q", out.Output)
	}
}

func TestBashOutputTruncated(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"yes x | head -c 5000000"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if !strings.Contains(out.Output, "truncated from") {
		t.Fatalf("truncation note missing")
	}
}

func TestBashCancel(t *testing.T) {
	ws := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan core.ToolOutput, 1)
	go func() {
		out, _ := NewBash(&Guard{Workspace: ws}).Call(ctx, core.ToolCtx{Workspace: ws}, json.RawMessage(`{"command":"sleep 5"}`))
		done <- out
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case out := <-done:
		if out.Status != core.ToolStatusError || out.Output != "cancelled" {
			t.Fatalf("out=%+v", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("bash did not stop when cancelled")
	}
}

func TestBashMissingWorkspace(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "missing", "workspace")
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"pwd"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "workspace does not exist:") {
		t.Fatalf("out=%+v", out)
	}
	if strings.Contains(out.Output, "fork/exec") {
		t.Fatalf("output contains the operating system exec error: %q", out.Output)
	}
}

func TestBashWorkspaceNotCreatable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(file, "workspace")
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"pwd"}`)
	if out.Status != core.ToolStatusError {
		t.Fatalf("out=%+v", out)
	}
	if !strings.Contains(out.Output, "workspace is not a directory:") && !strings.Contains(out.Output, "workspace does not exist:") {
		t.Fatalf("out=%+v", out)
	}
	if strings.Contains(out.Output, "fork/exec") {
		t.Fatalf("output contains the operating system exec error: %q", out.Output)
	}
}
