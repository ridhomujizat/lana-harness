package claudechat

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/x/vt"
)

// oscMirrorSizePrefix is the fixed head of an OSC 7701 size marker, which the
// Workbench core emits with a pane replay wherever the pty size changed; its
// payload is ";<cols>;<rows>" terminated by BEL or ST (spec 71 REQ-WC-020).
const oscMirrorSizePrefix = "\x1b]7701;"

// oscMirrorNoopCommand is registered so the emulator does not treat OSC 7702 as
// an unknown sequence; the chat does not need it.
const oscMirrorNoopCommand = 7702

// mirrorMarkerCarryMax is the longest unterminated marker Write holds back for
// the next call; a longer one is written as ordinary bytes so a lost
// terminator cannot stall the stream.
const mirrorMarkerCarryMax = 32

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

	// carry holds a trailing piece of a Write that may be the head of an OSC
	// 7701 marker split across two chunks, so it is prepended to the next
	// Write instead of being fed to the emulator as ordinary bytes.
	carry []byte
}

// NewMirror creates a mirror of cols×rows.
func NewMirror(cols, rows int) *Mirror {
	m := &Mirror{}
	m.emu = vt.NewSafeEmulator(cols, rows)
	m.emu.SetScrollbackSize(mirrorScrollback)
	m.emu.RegisterOscHandler(oscMirrorNoopCommand, func([]byte) bool { return true })
	// The emulator answers terminal queries (DA1, DSR, …) through an unbuffered
	// pipe; nothing needs those answers here and an unread pipe would block
	// Write forever while it holds the lock. Read does not take the lock.
	go func() { _, _ = io.Copy(io.Discard, m.emu) }()
	return m
}

// Write feeds terminal output to the emulator. Each OSC 7701 size marker in the
// input is applied with Resize at the point it appears, so the bytes before it
// render at the old size and the bytes after it at the new one. The marker
// bytes themselves are dropped. After Close it is a no-op so a late write never
// panics.
func (m *Mirror) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, nil
	}
	buf := p
	if len(m.carry) > 0 {
		buf = append(append([]byte(nil), m.carry...), p...)
		m.carry = nil
	}
	// written counts the bytes the emulator has accepted; it is only reported
	// when a Write is cut short by an error, otherwise Write returns len(p).
	written := 0
	write := func(b []byte) error {
		if len(b) == 0 {
			return nil
		}
		n, err := m.emu.Write(b)
		written += n
		return err
	}
	rest := buf
	for {
		i := bytes.Index(rest, []byte(oscMirrorSizePrefix))
		if i < 0 {
			keep := mirrorCarryLen(rest)
			if err := write(rest[:len(rest)-keep]); err != nil {
				return written, err
			}
			m.carry = append([]byte(nil), rest[len(rest)-keep:]...)
			return len(p), nil
		}
		if err := write(rest[:i]); err != nil {
			return written, err
		}
		payloadStart := i + len(oscMirrorSizePrefix)
		payloadEnd, next, ok := mirrorMarkerEnd(rest, payloadStart)
		if !ok {
			// No terminator yet; hold a short marker back for the next Write
			// and flush a longer one as ordinary bytes.
			rem := rest[i:]
			if len(rem) < mirrorMarkerCarryMax {
				m.carry = append([]byte(nil), rem...)
				return len(p), nil
			}
			if err := write(rem); err != nil {
				return written, err
			}
			return len(p), nil
		}
		if c, r, ok := parseMirrorSize(rest[payloadStart:payloadEnd]); ok {
			m.emu.Resize(c, r)
		}
		rest = rest[next:]
	}
}

// mirrorMarkerEnd finds the end of the size marker whose payload starts at
// from. It returns the index one past the payload and one past the terminator,
// which is BEL or ST. ok is false when no terminator is present.
func mirrorMarkerEnd(b []byte, from int) (payloadEnd, next int, ok bool) {
	for i := from; i < len(b); i++ {
		switch b[i] {
		case 0x07:
			return i, i + 1, true
		case 0x1b:
			if i+1 < len(b) && b[i+1] == '\\' {
				return i, i + 2, true
			}
		}
	}
	return 0, 0, false
}

// mirrorCarryLen is the length of rest's longest suffix that could be the head
// of an OSC 7701 marker, so Write can hold it back for the next call.
func mirrorCarryLen(rest []byte) int {
	for k := len(oscMirrorSizePrefix) - 1; k > 0; k-- {
		if len(rest) >= k && string(rest[len(rest)-k:]) == oscMirrorSizePrefix[:k] {
			return k
		}
	}
	return 0
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
	m.carry = nil
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
