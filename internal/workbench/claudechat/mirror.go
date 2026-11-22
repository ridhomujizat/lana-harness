package claudechat

import (
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/x/vt"
)

// oscMirrorSizeCommand is the OSC command the Workbench core emits with a pane
// replay to report its pty size; its payload is ";<cols>;<rows>" (spec 71
// REQ-WC-020).
const oscMirrorSizeCommand = 7701

// oscMirrorNoopCommand is registered so the emulator does not treat OSC 7702 as
// an unknown sequence; the chat does not need it.
const oscMirrorNoopCommand = 7702

// mirrorScrollback is how many scrollback lines the mirror keeps. It is small
// because the screen parser only reads the visible rows.
const mirrorScrollback = 200

// Mirror is a headless terminal fed with a pane's output. The visible rows it
// renders are read by ParseScreen to get the live state the chat shows: the
// spinner line, the permission mode, the input line and any open dialog
// (spec 71 REQ-WC-020).
//
// A single mutex serializes Write, Resize and Lines so the daemon can feed one
// attachment and read the screen from another goroutine. The emulator's own
// write/read pipes are drained by a goroutine whose answers nobody needs.
type Mirror struct {
	mu     sync.Mutex
	emu    *vt.SafeEmulator
	closed bool

	// pendingCols/Rows hold a resize requested by an OSC 7701 marker inside the
	// current Write. The handler runs while the emulator holds its write lock,
	// so the resize is applied after Write returns instead of from the handler.
	pendingCols int
	pendingRows int
}

// NewMirror creates a mirror of cols×rows.
func NewMirror(cols, rows int) *Mirror {
	m := &Mirror{}
	m.emu = vt.NewSafeEmulator(cols, rows)
	m.emu.SetScrollbackSize(mirrorScrollback)
	m.emu.RegisterOscHandler(oscMirrorSizeCommand, func(data []byte) bool {
		if c, r, ok := parseMirrorSize(data); ok {
			m.pendingCols, m.pendingRows = c, r
		}
		return true
	})
	m.emu.RegisterOscHandler(oscMirrorNoopCommand, func([]byte) bool { return true })
	// The emulator answers terminal queries (DA1, DSR, …) through an unbuffered
	// pipe; nothing needs those answers here and an unread pipe would block
	// Write forever while it holds the lock. Read does not take the lock.
	go func() { _, _ = io.Copy(io.Discard, m.emu) }()
	return m
}

// Write feeds terminal output to the emulator. After Close it is a no-op so a
// late write never panics.
func (m *Mirror) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, nil
	}
	m.pendingCols, m.pendingRows = 0, 0
	n, err := m.emu.Write(p)
	if m.pendingCols > 0 && m.pendingRows > 0 {
		m.emu.Resize(m.pendingCols, m.pendingRows)
	}
	return n, err
}

// Resize sizes the emulated screen to match the pty.
func (m *Mirror) Resize(cols, rows int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.emu.Resize(cols, rows)
}

// Lines returns the visible screen rows, right-trimmed of spaces.
func (m *Mirror) Lines() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	parts := strings.Split(m.emu.String(), "\n")
	for i, l := range parts {
		parts[i] = strings.TrimRight(l, " ")
	}
	return parts
}

// Close ends the emulator's answer pipe, which stops the drain goroutine. The
// pipe is closed rather than the emulator because Emulator.Close writes a flag
// that races with the drain goroutine's Read. It is safe to call more than
// once, and Write after Close is a no-op.
func (m *Mirror) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	if c, ok := m.emu.InputPipe().(io.Closer); ok {
		_ = c.Close()
	}
}

// parseMirrorSize reads a "<cols>;<rows>" size from an OSC 7701 payload. The
// parser hands the payload back with its command prefix ("7701;20;5"); the raw
// form (";20;5") is accepted too.
func parseMirrorSize(data []byte) (cols, rows int, ok bool) {
	s := string(data)
	if rest, cut := strings.CutPrefix(s, "7701;"); cut {
		s = rest
	}
	s = strings.TrimPrefix(s, ";")
	parts := strings.Split(s, ";")
	if len(parts) < 2 {
		return 0, 0, false
	}
	c, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, false
	}
	r, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, false
	}
	if c <= 0 || r <= 0 {
		return 0, 0, false
	}
	return c, r, true
}
