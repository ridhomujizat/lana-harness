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
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "timeout setelah 1 detik") {
		t.Fatalf("out=%+v", out)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout terlalu lama: %v", time.Since(start))
	}
}

func TestBashTimeoutMax(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"touch harusnya-tidak-ada","timeout_secs":601}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "1 sampai 600") {
		t.Fatalf("out=%+v", out)
	}
	if _, err := os.Stat(filepath.Join(ws, "harusnya-tidak-ada")); !os.IsNotExist(err) {
		t.Fatalf("perintah dijalankan: %v", err)
	}
}

func TestBashExitCode(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"echo out; echo err >&2; exit 7"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "exit code 7") {
		t.Fatalf("out=%+v", out)
	}
	if !strings.Contains(out.Output, "out") || !strings.Contains(out.Output, "err") {
		t.Fatalf("output tidak lengkap: %q", out.Output)
	}
}

func TestBashOutputTruncated(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewBash(&Guard{Workspace: ws}), ws, `{"command":"yes x | head -c 40000"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if !strings.Contains(out.Output, "dipotong dari") {
		t.Fatalf("catatan potong tidak ada")
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
		if out.Status != core.ToolStatusError || out.Output != "dibatalkan" {
			t.Fatalf("out=%+v", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("bash tidak berhenti saat dibatalkan")
	}
}
