package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

var bashSpec = json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","minLength":1},"timeout_secs":{"type":"integer","minimum":1,"maximum":600}},"required":["command"],"additionalProperties":false}`)

type bashTool struct{ guard *Guard }

// NewBash creates the bash tool.
func NewBash(guard *Guard) core.Tool { return &bashTool{guard: guard} }

func (t *bashTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "bash", Description: "Runs a shell command in the workspace.", InputSchema: bashSpec}
}
func (t *bashTool) Risk(json.RawMessage) core.Risk { return core.RiskExec }

func (t *bashTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Command     string `json:"command"`
		TimeoutSecs int    `json:"timeout_secs"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if in.Command == "" {
		return inputErrorOutput(fmt.Errorf("command is required"))
	}
	if in.TimeoutSecs == 0 {
		in.TimeoutSecs = 120
	}
	if in.TimeoutSecs < 1 || in.TimeoutSecs > 600 {
		return errorOutput("timeout_secs must be between 1 and 600")
	}
	ws := effectiveWorkspace(t.guard, tc.Workspace)
	if err := checkWorkspace(ws); err != nil {
		return errorOutput(err.Error())
	}
	cmd := exec.Command("sh", "-c", in.Command)
	cmd.Dir = ws
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out := &limitedWriter{limit: maxToolOutput}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		return errorOutput(err.Error())
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(time.Duration(in.TimeoutSecs) * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		text := out.String()
		if err != nil {
			code := -1
			if exitErr, ok := err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
			}
			return errorOutput(fmt.Sprintf("exit code %d\n%s", code, text))
		}
		return successOutput(text)
	case <-ctx.Done():
		killGroup(cmd)
		err := <-done
		_ = err
		return errorOutput("cancelled")
	case <-timer.C:
		killGroup(cmd)
		err := <-done
		_ = err
		return errorOutput(fmt.Sprintf("timed out after %d seconds", in.TimeoutSecs))
	}
}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

type limitedWriter struct {
	mu    sync.Mutex
	buf   []byte
	limit int
	total int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.total += len(p)
	if len(w.buf) < w.limit {
		room := w.limit - len(w.buf)
		if room > len(p) {
			room = len(p)
		}
		w.buf = append(w.buf, p[:room]...)
	}
	return len(p), nil
}

func (w *limitedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := string(w.buf)
	if w.total > w.limit {
		s += fmt.Sprintf("\n...[truncated from %d characters]", w.total)
	}
	return s
}
