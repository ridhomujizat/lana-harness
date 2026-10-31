package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

var bashSpec = json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","minLength":1,"description":"Shell command to run with sh -c."},"timeout_secs":{"type":"integer","minimum":1,"maximum":600,"description":"Timeout in seconds (1-600). Defaults to 120."},"run_in_background":{"type":"boolean","description":"Start the command as a background job and return its id without waiting for exit. Use for servers and watchers."}},"required":["command"],"additionalProperties":false}`)

// bashDescription tells the model to use run_in_background for long-lived
// commands instead of shell backgrounding (REQ-JOB-016) and to use the
// dedicated file tools instead of shell commands (REQ-TRL-002).
const bashDescription = "Runs a shell command (`sh -c`) in the workspace and returns stdout and stderr. " +
	"Each call starts in the workspace directory. Use it for builds, tests, git, package managers, and running programs. " +
	"Do not use it to read, search, create, or edit files: use read_file, grep, glob, write_file, and edit_file. " +
	"Shell commands such as `cat`, `head`, `tail`, `sed`, `awk`, `echo >`, `printf >`, heredocs, `grep`, `find`, and `ls -R` are not substitutes for those tools. " +
	"For servers, watchers, and anything that does not exit, set run_in_background: true; never use &, nohup, setsid, or disown for that. " +
	"The default timeout is 120 seconds; set timeout_secs (max 600) for longer commands."

// bashWaitDelay bounds how long a foreground call waits for the output pipe
// after the shell exits (REQ-JOB-001).
const bashWaitDelay = 2 * time.Second

// backgroundWait is how long a background start waits for output or exit
// (REQ-JOB-010).
const backgroundWait = 3 * time.Second

// backgroundTailMax is the largest tail a background start returns.
const backgroundTailMax = 4000

type bashTool struct{ guard *Guard }

// NewBash creates the bash tool.
func NewBash(guard *Guard) core.Tool { return &bashTool{guard: guard} }

func (t *bashTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "bash", Description: bashDescription, InputSchema: bashSpec}
}
func (t *bashTool) Risk(json.RawMessage) core.Risk { return core.RiskExec }

func (t *bashTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Command         string `json:"command"`
		TimeoutSecs     int    `json:"timeout_secs"`
		RunInBackground bool   `json:"run_in_background"`
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
	if in.RunInBackground {
		return t.runBackground(ctx, tc, ws, in.Command)
	}
	cmd := exec.Command("sh", "-c", in.Command)
	cmd.Dir = ws
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A command that leaves a process holding the output pipe must not block
	// the foreground call: after WaitDelay the pipe is closed and Wait returns
	// (REQ-JOB-001).
	cmd.WaitDelay = bashWaitDelay
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
		// ErrWaitDelay means the shell exited and only a leftover process kept
		// the pipe open; the call succeeded (REQ-JOB-001).
		if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
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

// runBackground starts the command as a background job and returns its id, pid,
// log path, status, and the output so far (REQ-JOB-010).
func (t *bashTool) runBackground(ctx context.Context, tc core.ToolCtx, ws, command string) (core.ToolOutput, error) {
	if tc.Jobs == nil {
		return errorOutput("run_in_background is not available in this runtime")
	}
	job, err := tc.Jobs.Start(tc.SessionID, ws, command)
	if err != nil {
		return errorOutput(err.Error())
	}
	// Wait briefly so a fast command reports its exit status right away.
	waited := tc.Jobs.Wait(ctx, tc.SessionID, job.ID, backgroundWait)
	status := jobStatusLabel(waited)
	tail, err := tc.Jobs.Tail(tc.SessionID, job.ID, backgroundTailMax)
	if err != nil {
		return errorOutput(err.Error())
	}
	output := fmt.Sprintf("started job %s (pid %d), log %s\n%s", job.ID, job.PID, job.LogPath, status)
	if tail != "" {
		output += "\n" + tail
	}
	return successOutput(output)
}

// jobStatusLabel renders the "[status: ...]" line of a job snapshot.
func jobStatusLabel(job core.JobView) string {
	switch job.Status {
	case core.JobStatusExited:
		return fmt.Sprintf("[status: exited %d]", job.ExitCode)
	case core.JobStatusKilled:
		return "[status: killed]"
	default:
		return "[status: running]"
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
