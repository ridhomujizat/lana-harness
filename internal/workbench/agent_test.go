package workbench

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// shellWithExecA returns a shell that supports `exec -a name program`, which is
// how the test gives a process an agent-shaped argv[0]. The probe execs the
// shell itself rather than a coreutils program, since a multi-call coreutils
// dispatches on argv[0] and would fail for a name it does not know.
func shellWithExecA(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"/bin/sh", "sh", "bash", "/bin/bash"} {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		probe := "exec -a claude " + shellQuote(path) + " -c 'exit 0'"
		if err := exec.Command(path, "-c", probe).Run(); err == nil {
			return path
		}
	}
	t.Skip("no shell supports exec -a")
	return ""
}

// shellQuote wraps a value in single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestSessionDetectsAgentFromForegroundJob(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	shell := shellWithExecA(t)
	// `exec -a claude <shell> -c 'sleep 5; :'` replaces the session's process
	// with a shell whose argv[0] is "claude", the way a coding agent names its
	// own process. The trailing `:` keeps the shell from exec'ing sleep, so the
	// process that carries the name stays alive long enough to be detected.
	script := "exec -a claude " + shellQuote(shell) + " -c 'sleep 5; :'"
	s, err := c.Create(ctx, CreateOptions{Command: []string{shell, "-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	waitFor(t, "agent claude", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && g.Agent == "claude"
	})
	g, err := c.Get(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.Program != "claude" {
		t.Fatalf("program = %q, want claude", g.Program)
	}
	if g.State == "exited" {
		t.Fatalf("state = %q, want a running state", g.State)
	}
}

func TestScreenCapturesOSCProgress(t *testing.T) {
	var progress string
	sc := newScreen(80, 24, func(p string) { progress = p }, func(string) {})
	sc.write([]byte("\x1b]0;first title\x07"))
	sc.write([]byte("\x1b]2;second title\x07"))
	sc.write([]byte("\x1b]9;4;1;50\x07"))
	sc.write([]byte("hello world\r\nsecond line"))
	if progress != "4;1;50" {
		t.Fatalf("progress = %q, want %q", progress, "4;1;50")
	}
	text := sc.text()
	if !strings.Contains(text, "hello world") || !strings.Contains(text, "second line") {
		t.Fatalf("screen text = %q, want both lines", text)
	}
}
