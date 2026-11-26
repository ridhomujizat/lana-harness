//go:build darwin || linux

package workbench

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	creackpty "github.com/creack/pty"
)

// sessionPtySize reads the window size the kernel holds for the session's pty.
func sessionPtySize(t *testing.T, s *session) (rows, cols int) {
	t.Helper()
	s.mu.Lock()
	p := s.p
	s.mu.Unlock()
	rows, cols, err := creackpty.Getsize(p.file)
	if err != nil {
		t.Fatalf("Getsize: %v", err)
	}
	return rows, cols
}

// waitPtySize polls the session's pty until it is wantRows×wantCols, or fails
// at the deadline.
func waitPtySize(t *testing.T, s *session, wantRows, wantCols int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		rows, cols := sessionPtySize(t, s)
		if rows == wantRows && cols == wantCols {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pty size = %d rows × %d cols, want %d × %d", rows, cols, wantRows, wantCols)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// startNudgedSession starts a session at cols×rows and attaches a control
// client to it, which triggers the REQ-WB-022 redraw nudge. The caller gets the
// session and must kill it.
func startNudgedSession(t *testing.T, cols, rows int) *session {
	t.Helper()
	s, err := newSession("nudge", CreateOptions{Command: []string{"sleep", "30"}, Cols: cols, Rows: rows}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.kill() })
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	go s.attach(server, modeControl, cols, rows, nil)
	return s
}

// TestAttachRedrawNudgeRestoresPtySize checks the nudge on the pty itself: a
// control attach leaves the pty one row short, then restores it to the session
// size after the delay, so a full-screen program gets a second size change to
// redraw from.
func TestAttachRedrawNudgeRestoresPtySize(t *testing.T) {
	old := redrawNudgeDelay
	redrawNudgeDelay = 200 * time.Millisecond
	t.Cleanup(func() { redrawNudgeDelay = old })

	s := startNudgedSession(t, 100, 40)

	waitPtySize(t, s, 39, 100, 2*time.Second)
	waitPtySize(t, s, 40, 100, 2*time.Second)
}

// TestAttachRedrawNudgeKeepsLaterResize checks that a resize another client
// makes while the pty is one row short wins: the delayed restore reads the
// current session size, so it does not revert to the attach size.
func TestAttachRedrawNudgeKeepsLaterResize(t *testing.T) {
	old := redrawNudgeDelay
	redrawNudgeDelay = 300 * time.Millisecond
	t.Cleanup(func() { redrawNudgeDelay = old })

	s := startNudgedSession(t, 100, 40)

	// Observe the nudge, then resize from another client inside the window.
	waitPtySize(t, s, 39, 100, 2*time.Second)
	start := time.Now()
	s.resize(120, 50)
	waitPtySize(t, s, 50, 120, time.Second)

	// Keep watching past the moment the delayed restore fires; a restore that
	// used the attach size would show up here as 40 rows × 100 cols.
	deadline := start.Add(redrawNudgeDelay + 300*time.Millisecond)
	for time.Now().Before(deadline) {
		if rows, cols := sessionPtySize(t, s); rows != 50 || cols != 120 {
			t.Fatalf("pty size = %d rows × %d cols after the nudge, want 50 × 120", rows, cols)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestAttachRedrawNudgeRedrawsOnWinch runs the nudge end to end against a
// program that reports its pty size on SIGWINCH, the way a full-screen program
// notices it must redraw: it sees the one-row-short size and then the restored
// session size, not two back-to-back resizes of the same size.
//
// The handler reads the size with a forked stty, after the loop's sleep ends,
// so on a loaded machine it can run only after the default 150ms restore and
// print "40 100" for both signals. The nudge is lengthened so the first read
// lands well inside the one-row-short window; what is checked is unchanged.
func TestAttachRedrawNudgeRedrawsOnWinch(t *testing.T) {
	old := redrawNudgeDelay
	redrawNudgeDelay = 2 * time.Second
	t.Cleanup(func() { redrawNudgeDelay = old })

	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	// The program installs its SIGWINCH handler, announces that it is ready,
	// then reports the pty size on every window change.
	script := `trap "stty size" WINCH; printf ready; while :; do sleep 0.05; done`
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", script}, Cols: 100, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()

	// A view attach does not nudge, so it only waits for the handler to be
	// installed before the control attach below fires the nudge: attaching
	// this soon after Create could otherwise race the handler's install.
	view, err := c.Attach(ctx, s.ID, "view")
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, view, "ready", 10*time.Second)
	_ = view.Close()

	stream, err := c.Attach(ctx, s.ID, "control")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()

	// The handler prints "rows cols": 39 while the pty is one row short, then
	// 40 once it is restored. The 40 must come after the 39, so only output
	// past the 39 counts; anything before it (the replay) is skipped.
	got := readUntil(t, stream, "39 100", 10*time.Second)
	after := got[strings.Index(got, "39 100")+len("39 100"):]
	if !strings.Contains(after, "40 100") {
		readUntil(t, stream, "40 100", 10*time.Second)
	}
}
