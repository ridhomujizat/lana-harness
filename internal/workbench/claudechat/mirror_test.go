package claudechat

import (
	"strings"
	"sync"
	"testing"
)

// TestMirrorResizeViaOSC7701 checks that an OSC 7701 size marker in the pane
// output resizes the headless screen.
func TestMirrorResizeViaOSC7701(t *testing.T) {
	m := NewMirror(110, 40)
	defer m.Close()

	if _, err := m.Write([]byte("hello world\r\n\x1b]7701;20;5\x07")); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines := m.Lines()
	if len(lines) != 5 {
		t.Fatalf("Lines() len = %d, want 5 after resize to 20x5", len(lines))
	}
	if got := strings.TrimRight(lines[0], " "); got != "hello world" {
		t.Errorf("lines[0] = %q, want %q", got, "hello world")
	}
}

// TestMirrorNoopOSC7702 checks that the no-op OSC 7702 marker does not disturb
// the screen.
func TestMirrorNoopOSC7702(t *testing.T) {
	m := NewMirror(10, 3)
	defer m.Close()

	if _, err := m.Write([]byte("\x1b]7702;whatever\x07ok")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if lines := m.Lines(); len(lines) != 3 {
		t.Fatalf("Lines() len = %d, want 3", len(lines))
	}
}

// TestMirrorWriteAfterClose checks a late write (or resize) never panics and
// Close is idempotent.
func TestMirrorWriteAfterClose(t *testing.T) {
	m := NewMirror(10, 3)
	m.Close()
	m.Close()

	if _, err := m.Write([]byte("late")); err != nil {
		t.Fatalf("write after close: %v", err)
	}
	m.Resize(5, 2)
}

// TestMirrorConcurrentAccess runs Write, Lines and Resize together; it is meant
// to be run with -race.
func TestMirrorConcurrentAccess(t *testing.T) {
	m := NewMirror(40, 10)
	defer m.Close()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = m.Write([]byte("x\r\n"))
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = m.Lines()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			m.Resize(20+j%5, 5+j%5)
		}
	}()
	wg.Wait()
}
