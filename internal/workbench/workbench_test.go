package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortHome returns a temp home short enough for a Unix socket path.
func shortHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// serve runs the core in a goroutine and waits for its socket.
func serve(t *testing.T, home string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, home) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not stop")
		}
	})
	waitFor(t, "core socket", func() bool {
		c, err := Dial(context.Background(), home)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func mustClient(t *testing.T, home string) *Client {
	t.Helper()
	c, err := Dial(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// readUntil reads until want appears, or fails at the deadline.
func readUntil(t *testing.T, r io.Reader, want string, timeout time.Duration) string {
	t.Helper()
	conn, ok := r.(net.Conn)
	if !ok {
		t.Fatal("stream is not a net.Conn")
	}
	deadline := time.Now().Add(timeout)
	var got strings.Builder
	buf := make([]byte, 4096)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		n, err := conn.Read(buf)
		if n > 0 {
			got.Write(buf[:n])
			if strings.Contains(got.String(), want) {
				return got.String()
			}
		}
		if err != nil {
			t.Fatalf("reading %q: %v (got %q)", want, err, got.String())
		}
	}
}

// readReplay reads an attach stream through its final replay-end marker and
// returns the replay.
func readReplay(t *testing.T, r io.Reader) []byte {
	t.Helper()
	conn := r.(net.Conn)
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var got []byte
	buf := make([]byte, 4096)
	for !bytes.Contains(got, []byte(replayEndMarker)) {
		n, err := conn.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			t.Fatalf("reading replay: %v (got %q)", err, got)
		}
	}
	return got
}

func hasSession(list []Session, id string) bool {
	for _, s := range list {
		if s.ID == id {
			return true
		}
	}
	return false
}

func TestHelloVersion(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	h, err := c.Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != ProtocolVersion {
		t.Fatalf("version = %d, want %d", h.Version, ProtocolVersion)
	}
	if h.PID <= 0 {
		t.Fatalf("pid = %d", h.PID)
	}
	if h.Started.IsZero() {
		t.Fatal("started is zero")
	}
}

func TestEnsureDialPath(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c, err := Ensure(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if h, err := c.Hello(context.Background()); err != nil || h.Version != ProtocolVersion {
		t.Fatalf("hello = %+v, %v", h, err)
	}
}

func TestEnsureRemovesStaleSocket(t *testing.T) {
	home := shortHome(t)
	if err := os.MkdirAll(Dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", sockPath(home))
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = l.Close()
	if _, err := os.Stat(sockPath(home)); err != nil {
		t.Fatalf("stale socket missing: %v", err)
	}

	old := startCore
	startCore = func(string) error { return errors.New("no spawn in tests") }
	t.Cleanup(func() { startCore = old })

	if _, err := Ensure(context.Background(), home); err == nil {
		t.Fatal("Ensure succeeded with no core")
	}
	if _, err := os.Stat(sockPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket not removed: %v", err)
	}
}

func TestCreateAttachReplay(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", "printf hello; sleep 0.2"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID == "" || s.PID <= 0 {
		t.Fatalf("bad session: %+v", s)
	}
	stream, err := c.Attach(ctx, s.ID, "view")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if got := readUntil(t, stream, "hello", 5*time.Second); !strings.Contains(got, "hello") {
		t.Fatalf("replay = %q", got)
	}
	// The stream ends once the session exits, after its last output.
	conn := stream.(net.Conn)
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	for {
		if _, err := conn.Read(buf); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("stream did not close on exit: %v", err)
			}
			break
		}
	}
}

// TestAttachReplayFraming checks that an attach replay is framed: it starts
// with the size marker of the session's pty size and ends with the replay-end
// marker, so the client can replay each byte at the size it was produced at.
func TestAttachReplayFraming(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", "printf hello; sleep 30"}, Cols: 100, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()

	// Wait for the program's output to reach the ring, so the replay carries it
	// as well as the framing.
	waitFor(t, "output", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && !g.LastOutput.IsZero()
	})
	stream, err := c.Attach(ctx, s.ID, "view")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()

	got := readReplay(t, stream)
	if !bytes.HasPrefix(got, sizeMarker(100, 40)) {
		t.Fatalf("replay does not start with the size marker: %q", got)
	}
	if !bytes.HasSuffix(got, []byte(replayEndMarker)) {
		t.Fatalf("replay does not end with the replay-end marker: %q", got)
	}
	if !bytes.Contains(got, []byte("hello")) {
		t.Fatalf("replay is missing the output: %q", got)
	}
}

func TestControlAttachEcho(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/cat"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	stream, err := c.Attach(ctx, s.ID, "control")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Write([]byte("ping\r")); err != nil {
		t.Fatal(err)
	}
	if got := readUntil(t, stream, "ping", 5*time.Second); !strings.Contains(got, "ping") {
		t.Fatalf("echo = %q", got)
	}
}

func TestViewInputDropped(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/cat"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	stream, err := c.Attach(ctx, s.ID, "view")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	// Consume the replay framing so the read below sees only what input would
	// have produced.
	readReplay(t, stream)
	if _, err := stream.Write([]byte("nope\r")); err != nil {
		t.Fatal(err)
	}
	conn := stream.(net.Conn)
	if err := conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if n > 0 {
		t.Fatalf("view mode sent input through: %q", buf[:n])
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("read = %v, want a timeout", err)
	}
}

func TestResizeReflected(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/cat"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	if err := c.Resize(ctx, s.ID, 100, 40); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "resize", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && g.Cols == 100 && g.Rows == 40
	})
}

func TestExitStateAndKill(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", "exit 3"}})
	if err != nil {
		t.Fatal(err)
	}
	var got Session
	waitFor(t, "exit", func() bool {
		g, err := c.Get(ctx, s.ID)
		if err != nil {
			return false
		}
		got = g
		return g.State == "exited"
	})
	if got.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", got.ExitCode)
	}
	list, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSession(list, s.ID) {
		t.Fatal("exited session is not listed")
	}
	if err := c.Kill(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "removal", func() bool {
		l, err := c.List(ctx)
		return err == nil && !hasSession(l, s.ID)
	})
	if _, err := c.Get(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after kill = %v, want ErrNotFound", err)
	}
}

func TestKillRunningSession(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"sleep", "30"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Kill(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	list, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hasSession(list, s.ID) {
		t.Fatal("killed session is still listed")
	}
}

func TestRestart(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", "printf one; sleep 0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "exit", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && g.State == "exited"
	})
	r, err := c.Restart(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	if r.ID != s.ID {
		t.Fatalf("id changed: %s -> %s", s.ID, r.ID)
	}
	if r.PID == s.PID {
		t.Fatalf("pid did not change: %d", r.PID)
	}
}

func TestLayout(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	l, err := c.Layout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(l) != "{}" {
		t.Fatalf("empty layout = %s, want {}", l)
	}
	doc := json.RawMessage(`{"spaces":[{"id":"a"}]}`)
	if err := c.SetLayout(ctx, doc); err != nil {
		t.Fatal(err)
	}
	l, err = c.Layout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(l) != string(doc) {
		t.Fatalf("layout = %s, want %s", l, doc)
	}
	if _, err := os.Stat(filepath.Join(Dir(home), "layout.json")); err != nil {
		t.Fatalf("layout file: %v", err)
	}
	big := json.RawMessage(`"` + strings.Repeat("x", layoutMax) + `"`)
	if err := c.SetLayout(ctx, big); err == nil {
		t.Fatal("oversized layout was accepted")
	}
}

func TestKillUnknownSession(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	if err := c.Kill(context.Background(), "deadbeef"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Kill unknown = %v, want ErrNotFound", err)
	}
}

func TestShutdownStopsServe(t *testing.T) {
	home := shortHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, home) }()
	waitFor(t, "core socket", func() bool {
		c, err := Dial(context.Background(), home)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	})
	c := mustClient(t, home)
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop after shutdown")
	}
	if _, err := os.Stat(sockPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind: %v", err)
	}
}
