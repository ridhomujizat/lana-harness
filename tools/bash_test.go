package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

// TestBashBackgroundedProcessDoesNotBlock covers REQ-JOB-001: a foreground
// command whose backgrounded child holds the output pipe returns when the shell
// exits, and the child is left running.
func TestBashBackgroundedProcessDoesNotBlock(t *testing.T) {
	ws := t.TempDir()
	start := time.Now()
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"sleep 30 & echo $! > pid; echo hi"}`)
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "hi") {
		t.Fatalf("out=%+v", out)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("foreground bash blocked for %v", elapsed)
	}
	pid := readPidFile(t, filepath.Join(ws, "pid"))
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("backgrounded sleep %d was killed: %v", pid, err)
	}
}

// TestBashTimeoutKillsProcessGroup covers REQ-JOB-001: a command that never
// exits still times out and the whole process group, including a backgrounded
// child, is killed.
func TestBashTimeoutKillsProcessGroup(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"sleep 30 & echo $! > pid; exec sleep 30","timeout_secs":1}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "timed out after 1 seconds") {
		t.Fatalf("out=%+v", out)
	}
	pid := readPidFile(t, filepath.Join(ws, "pid"))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil {
		time.Sleep(10 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("timed-out child %d survived the group kill", pid)
	}
}

func TestBashRunInBackgroundWithoutRegistry(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"echo hi","run_in_background":true}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "not available") {
		t.Fatalf("out=%+v", out)
	}
}

// TestBashRunInBackground returns the exact REQ-JOB-010 line from a fake
// registry.
func TestBashRunInBackground(t *testing.T) {
	fake := &fakeJobService{
		startJob: core.JobView{ID: "bash-ab12", PID: 42, LogPath: "/tmp/bash-ab12.log", Status: core.JobStatusRunning},
		waitJob:  core.JobView{ID: "bash-ab12", Status: core.JobStatusRunning},
		tail:     "ready\n",
	}
	ws := t.TempDir()
	out, err := NewBash(&Guard{Workspace: ws}).Call(context.Background(), core.ToolCtx{SessionID: "s1", Workspace: ws, Jobs: fake}, json.RawMessage(`{"command":"server","run_in_background":true}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "started job bash-ab12 (pid 42), log /tmp/bash-ab12.log\n[status: running]\nready\n"
	if out.Output != want {
		t.Fatalf("output=%q want %q", out.Output, want)
	}
}

func readPidFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr == nil && pid > 0 {
				return pid
			}
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("cannot read pid from %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
