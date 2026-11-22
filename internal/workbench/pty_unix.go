//go:build darwin || linux

package workbench

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	creackpty "github.com/creack/pty"
)

// pty is a pseudo-terminal and the program it runs.
type pty struct {
	file *os.File
	cmd  *exec.Cmd
}

// startPTY runs argv in a new pseudo-terminal of cols×rows.
func startPTY(path string, argv []string, dir string, env []string, cols, rows int) (*pty, error) {
	p, tty, err := creackpty.Open()
	if err != nil {
		return nil, err
	}
	if err := creackpty.Setsize(p, &creackpty.Winsize{Cols: clampSize(cols), Rows: clampSize(rows)}); err != nil {
		_ = p.Close()
		_ = tty.Close()
		return nil, err
	}
	cmd := &exec.Cmd{
		Path: path, Args: argv, Dir: dir, Env: withWorkbenchTTY(env, tty.Name()),
		Stdin: tty, Stdout: tty, Stderr: tty,
		// The child leads a new session and gets the slave as its controlling
		// terminal, as creack/pty's own Start does (Ctty 0).
		SysProcAttr: &syscall.SysProcAttr{Setsid: true, Setctty: true},
	}
	if err := cmd.Start(); err != nil {
		_ = p.Close()
		_ = tty.Close()
		return nil, err
	}
	// The child holds the slave now; the parent keeps only the master, so the
	// master reads EOF when the program ends.
	_ = tty.Close()
	return &pty{file: p, cmd: cmd}, nil
}

// withWorkbenchTTY returns env with LANA_WORKBENCH_TTY set to tty, replacing
// any entry the caller passed in: the marker names the pty the program runs on
// (REQ-WA-001).
func withWorkbenchTTY(env []string, tty string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k == "LANA_WORKBENCH_TTY" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "LANA_WORKBENCH_TTY="+tty)
}

func (p *pty) read(b []byte) (int, error)  { return p.file.Read(b) }
func (p *pty) write(b []byte) (int, error) { return p.file.Write(b) }
func (p *pty) close() error                { return p.file.Close() }

// fd returns the master's file descriptor for ioctls, or 0 when the file is
// closed or otherwise unusable.
func (p *pty) fd() uintptr {
	sc, err := p.file.SyscallConn()
	if err != nil {
		return 0
	}
	var fd uintptr
	if err := sc.Control(func(f uintptr) { fd = f }); err != nil {
		return 0
	}
	return fd
}

func (p *pty) resize(cols, rows int) error {
	return creackpty.Setsize(p.file, &creackpty.Winsize{Cols: clampSize(cols), Rows: clampSize(rows)})
}

func (p *pty) pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// wait waits for the program and returns its exit code, 128 plus the signal
// number for a program a signal ended.
func (p *pty) wait() int {
	err := p.cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return exit.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// hangup ends the session: its process group gets SIGHUP, as when a terminal
// window closes.
func (p *pty) hangup() { signalGroup(p, syscall.SIGHUP) }

// kill forces the session's process group to end.
func (p *pty) kill() { signalGroup(p, syscall.SIGKILL) }

func signalGroup(p *pty, sig syscall.Signal) {
	if p.cmd.Process != nil {
		_ = syscall.Kill(-p.cmd.Process.Pid, sig)
	}
}

func clampSize(v int) uint16 {
	if v <= 0 {
		return 1
	}
	if v > 0xFFFF {
		return 0xFFFF
	}
	return uint16(v)
}

// shellCommand returns the program, the argv it sees and the program's name
// for a session; an empty cmd is the user's shell (REQ-WB-010).
func shellCommand(cmd []string) (path string, argv []string, name string, err error) {
	if len(cmd) > 0 {
		path, err = exec.LookPath(cmd[0])
		if err != nil {
			return "", nil, "", err
		}
		return path, cmd, filepath.Base(cmd[0]), nil
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	return sh, []string{sh}, filepath.Base(sh), nil
}

// detached starts a process in a session of its own, which outlives the
// caller.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
