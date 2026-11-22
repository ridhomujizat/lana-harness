//go:build darwin || linux

package workbench

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestSessionTTYMarker checks that the program of a session sees
// LANA_WORKBENCH_TTY as the pty it runs on: the variable and `tty` print the
// same path (REQ-WA-001).
func TestSessionTTYMarker(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	script := `printf '%s\n' "$LANA_WORKBENCH_TTY"; tty`
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	// Wait for the program to end, so its whole output is in the ring and the
	// replay below holds all of it.
	waitFor(t, "exit", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && g.State == "exited"
	})
	stream, err := c.Attach(ctx, s.ID, "view")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()

	lines := markerLines(readReplay(t, stream))
	if len(lines) != 2 {
		t.Fatalf("output = %v, want two lines", lines)
	}
	if lines[0] == "" || lines[0] != lines[1] {
		t.Fatalf("LANA_WORKBENCH_TTY = %q, tty = %q, want the same path", lines[0], lines[1])
	}
}

// markerLines returns the non-empty lines of an attach replay, with the framing
// (the OSC 7701 size markers and the OSC 7702 end marker) dropped, so the
// program's own output is left.
func markerLines(replay []byte) []string {
	var clean []byte
	for i := 0; i < len(replay); {
		if bytes.HasPrefix(replay[i:], []byte("\x1b]7701")) || bytes.HasPrefix(replay[i:], []byte(replayEndMarker)) {
			end := bytes.IndexByte(replay[i:], '\a')
			if end < 0 {
				break
			}
			i += end + 1
			continue
		}
		clean = append(clean, replay[i])
		i++
	}
	var lines []string
	for _, line := range strings.Split(string(clean), "\n") {
		if line = strings.TrimRight(line, "\r"); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
