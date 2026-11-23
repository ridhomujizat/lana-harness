package claudechat

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// rawPane reads a stored raw pane replay.
func rawPane(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "raw", name))
	if err != nil {
		t.Fatalf("read raw fixture %s: %v", name, err)
	}
	return b
}

// hasTrimmedPrefix reports whether any line starts with prefix once it is
// trimmed.
func hasTrimmedPrefix(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			return true
		}
	}
	return false
}

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

// TestMirrorOSC7701ResizeBeforeText checks that a size marker at the head of a
// Write resizes the screen before the bytes that follow it render, so they are
// not wrapped at the old size.
func TestMirrorOSC7701ResizeBeforeText(t *testing.T) {
	m := NewMirror(10, 3)
	defer m.Close()

	line := "abcdefghijklmnopqrstuvwxy" // 25 characters
	if _, err := m.Write([]byte("\x1b]7701;30;5\x07" + line)); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines := m.Lines()
	if len(lines) != 5 {
		t.Fatalf("Lines() len = %d, want 5 after resize to 30x5", len(lines))
	}
	if got := strings.TrimRight(lines[0], " "); got != line {
		t.Errorf("lines[0] = %q, want %q", got, line)
	}
}

// TestMirrorOSC7701MarkerSplitAcrossWrites checks that a marker split between
// two Writes is reassembled and applied before the following text.
func TestMirrorOSC7701MarkerSplitAcrossWrites(t *testing.T) {
	m := NewMirror(10, 3)
	defer m.Close()

	line := "abcdefghijklmnopqrstuvwxy" // 25 characters
	if _, err := m.Write([]byte("\x1b]77")); err != nil {
		t.Fatalf("write prefix: %v", err)
	}
	if _, err := m.Write([]byte("01;30;5\x07" + line)); err != nil {
		t.Fatalf("write rest: %v", err)
	}
	lines := m.Lines()
	if len(lines) != 5 {
		t.Fatalf("Lines() len = %d, want 5 after resize to 30x5", len(lines))
	}
	if got := strings.TrimRight(lines[0], " "); got != line {
		t.Errorf("lines[0] = %q, want %q", got, line)
	}
}

// TestMirrorOSC7701MultipleMarkersInOneWrite checks that several markers in one
// Write each take effect in order, so text between them renders at the size in
// effect then and the screen ends at the last size.
func TestMirrorOSC7701MultipleMarkersInOneWrite(t *testing.T) {
	m := NewMirror(30, 5)
	defer m.Close()

	line := "abcdefghijklmnopqrstuvwxy" // 25 characters
	// "0123456789AB" is 12 characters: it wraps at 10 columns but not at 30.
	in := "\x1b]7701;10;3\x07" + "0123456789AB" + "\x1b]7701;30;5\x07" + line
	if _, err := m.Write([]byte(in)); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines := m.Lines()
	if len(lines) != 5 {
		t.Fatalf("Lines() len = %d, want 5 after resize to 30x5", len(lines))
	}
	if got := strings.TrimRight(lines[0], " "); got != "0123456789" {
		t.Errorf("lines[0] = %q, want %q (text before the second marker rendered at 10 columns)", got, "0123456789")
	}
	if got := strings.TrimRight(lines[1], " "); got != "AB"+line {
		t.Errorf("lines[1] = %q, want %q (text after the second marker rendered at 30 columns)", got, "AB"+line)
	}
}

// TestMirrorStripsWindowTitle checks that a Claude Code window-title OSC does
// not overwrite the first option of a question dialog, whether the replay
// arrives in one Write or in small chunks.
func TestMirrorStripsWindowTitle(t *testing.T) {
	raw := rawPane(t, "question.raw")
	cases := []struct {
		name  string
		chunk int
	}{
		{"whole", 0},
		{"chunked", 37},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMirror(100, 40)
			defer m.Close()
			if tc.chunk == 0 {
				if _, err := m.Write(raw); err != nil {
					t.Fatalf("write: %v", err)
				}
			} else {
				for i := 0; i < len(raw); i += tc.chunk {
					end := i + tc.chunk
					if end > len(raw) {
						end = len(raw)
					}
					if _, err := m.Write(raw[i:end]); err != nil {
						t.Fatalf("write chunk: %v", err)
					}
				}
			}
			lines := m.Lines()
			if !hasTrimmedPrefix(lines, "❯ 1. Red (Recommended)") {
				t.Errorf("no dialog option line; lines = %q", lines)
			}
			for _, l := range lines {
				if strings.Contains(l, "AskUserQuestion tool test") {
					t.Errorf("title text leaked onto the screen: %q", l)
				}
			}
		})
	}
}

// TestMirrorTitleSplitAcrossWrites checks that a window-title OSC split between
// two Writes is still removed, leaving the text around it untouched.
func TestMirrorTitleSplitAcrossWrites(t *testing.T) {
	m := NewMirror(40, 3)
	defer m.Close()

	if _, err := m.Write([]byte("A\x1b]0;the tit")); err != nil {
		t.Fatalf("write prefix: %v", err)
	}
	if _, err := m.Write([]byte("le\x07B")); err != nil {
		t.Fatalf("write rest: %v", err)
	}
	lines := m.Lines()
	for _, l := range lines {
		if strings.Contains(l, "the title") {
			t.Errorf("title leaked onto the screen: %q", l)
		}
	}
	if !hasTrimmedPrefix(lines, "AB") {
		t.Errorf("lines = %q, want a line starting with %q", lines, "AB")
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
