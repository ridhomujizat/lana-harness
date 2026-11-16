//go:build darwin || linux

package workbench

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// evalSymlinks resolves a path so a physical process path (/tmp is
// /private/tmp on macOS) can be compared with the logical one a test passed in.
func evalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}

// waitCwd polls a session until it reports want as its working directory.
func waitCwd(t *testing.T, c *Client, id, want string, timeout time.Duration) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	var got string
	for time.Now().Before(deadline) {
		g, err := c.Get(ctx, id)
		if err == nil {
			got = g.Cwd
			if got == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("cwd = %q, want %q", got, want)
}

func TestSessionCwdFollowsCd(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh"}, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	stream, err := c.Attach(ctx, s.ID, "control")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Write([]byte("cd /tmp\n")); err != nil {
		t.Fatal(err)
	}
	waitCwd(t, c, s.ID, evalSymlinks(t, "/tmp"), 3*time.Second)
}

func TestSessionCwdOfSleep(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	dir := t.TempDir()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"sleep", "5"}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	waitCwd(t, c, s.ID, evalSymlinks(t, dir), 3*time.Second)
}

func TestSessionCwdFromOSC7(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	// The shell reports a directory with OSC 7 while staying in the session's
	// own (different) directory; OSC 7 is authoritative, and its value is used
	// verbatim, without resolving symlinks.
	script := `printf '\033]7;file://host/tmp/a%%20b\a'; sleep 5`
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", script}, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	waitCwd(t, c, s.ID, "/tmp/a b", 3*time.Second)
}
