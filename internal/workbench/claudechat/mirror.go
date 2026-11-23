package claudechat

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charmbracelet/x/vt"
)

// oscMirrorSizePrefix is the fixed head of an OSC 7701 size marker, which the
// Workbench core emits with a pane replay wherever the pty size changed; its
// payload is ";<cols>;<rows>" terminated by BEL or ST (spec 71 REQ-WC-020).
const oscMirrorSizePrefix = "\x1b]7701;"

// oscMirrorReplayDonePrefix is the head of the OSC 7702 replay-end marker the
// core appends to a pane replay (spec 71 REQ-WC-020). It carries no payload and
// no ";"; Mirror drops it and closes ReplayDone.
const oscMirrorReplayDonePrefix = "\x1b]7702"

// mirrorMarkerCarryMax is the longest unterminated marker Write holds back for
// the next call; a longer one is written as ordinary bytes so a lost
// terminator cannot stall the stream.
const mirrorMarkerCarryMax = 32

// mirrorTitleCarryMax caps a title OSC Write holds back while waiting for its
// terminator. A longer one is dropped so a lost terminator cannot grow the
// carry unbounded.
const mirrorTitleCarryMax = 4 * 1024

// oscMirrorSequence is an OSC introducer and command Mirror consumes itself
// instead of letting the emulator draw it. Title commands (0, 1, 2) are
// dropped because the emulator would render their text onto the screen, over a
// dialog's first option; the 7701 size marker is applied with Resize and the
// 7702 replay-end marker closes ReplayDone. Every other OSC (7, 8, 9, …) keeps
// its current handling.
type oscMirrorSequence struct {
	head []byte // introducer and command, e.g. "\x1b]0;" or "\x1b]7701;"
	size bool   // true for the 7701 size marker, applied with Resize
	done bool   // true for the 7702 replay-end marker, closing ReplayDone
}

// oscMirrorSequences lists the OSC sequences Mirror consumes itself.
var oscMirrorSequences = []oscMirrorSequence{
	{head: []byte("\x1b]0;")},
	{head: []byte("\x1b]1;")},
	{head: []byte("\x1b]2;")},
	{head: []byte(oscMirrorSizePrefix), size: true},
	{head: []byte(oscMirrorReplayDonePrefix), done: true},
}

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
	// 7701 or 7702 marker split across two chunks, so it is prepended to the
	// next Write instead of being fed to the emulator as ordinary bytes.
	carry []byte

	// done is closed the first time a Write carries the core's replay-end
	// marker (OSC 7702), or the mirror is closed, so a caller waiting on a
	// fresh mirror's replay can learn when it has caught up.
	done     chan struct{}
	doneOnce sync.Once
}

// NewMirror creates a mirror of cols×rows.
func NewMirror(cols, rows int) *Mirror {
	m := &Mirror{done: make(chan struct{})}
	m.emu = vt.NewSafeEmulator(cols, rows)
	m.emu.SetScrollbackSize(mirrorScrollback)
	// The emulator answers terminal queries (DA1, DSR, …) through an unbuffered
	// pipe; nothing needs those answers here and an unread pipe would block
	// Write forever while it holds the lock. Read does not take the lock.
	go func() { _, _ = io.Copy(io.Discard, m.emu) }()
	return m
}

// Write feeds terminal output to the emulator. Each OSC 7701 size marker in the
// input is applied with Resize at the point it appears, so the bytes before it
// render at the old size and the bytes after it at the new one. The OSC 7702
// replay-end marker closes ReplayDone. Title OSC sequences (0, 1, 2) are removed
// so the emulator does not draw their text onto the screen. The consumed
// sequences themselves are dropped. After Close it is a no-op so a late write
// never panics.
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
		i, seq := indexMirrorOSC(rest)
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
		payloadStart := i + len(seq.head)
		payloadEnd, next, ok := mirrorMarkerEnd(rest, payloadStart)
		if !ok {
			rem := rest[i:]
			if seq.size {
				// No terminator yet; hold a short marker back for the next
				// Write and flush a longer one as ordinary bytes.
				if len(rem) < mirrorMarkerCarryMax {
					m.carry = append([]byte(nil), rem...)
					return len(p), nil
				}
				if err := write(rem); err != nil {
					return written, err
				}
				return len(p), nil
			}
			// Title with no terminator yet: hold it back for the next Write,
			// unless it grew past the cap, in which case drop it.
			if len(rem) <= mirrorTitleCarryMax {
				m.carry = append([]byte(nil), rem...)
			}
			return len(p), nil
		}
		if seq.size {
			if c, r, ok := parseMirrorSize(rest[payloadStart:payloadEnd]); ok {
				m.emu.Resize(c, r)
			}
		}
		if seq.done {
			m.markReplayDone()
		}
		rest = rest[next:]
	}
}

// indexMirrorOSC returns the earliest index in b at which a consumed OSC
// sequence begins, together with its descriptor. The index is negative when b
// holds none.
func indexMirrorOSC(b []byte) (int, oscMirrorSequence) {
	best := -1
	var found oscMirrorSequence
	for _, seq := range oscMirrorSequences {
		if i := bytes.Index(b, seq.head); i >= 0 && (best < 0 || i < best) {
			best, found = i, seq
		}
	}
	return best, found
}

// mirrorMarkerEnd finds the end of a consumed OSC payload that starts at from.
// It returns the index one past the payload and one past the terminator, which
// is BEL or ST. ok is false when no terminator is present.
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
// of a consumed OSC sequence, so Write can hold it back for the next call.
func mirrorCarryLen(rest []byte) int {
	longest := 1
	for _, seq := range oscMirrorSequences {
		if n := len(seq.head); n-1 > longest {
			longest = n - 1
		}
	}
	for k := longest; k > 0; k-- {
		if len(rest) < k {
			continue
		}
		suf := rest[len(rest)-k:]
		for _, seq := range oscMirrorSequences {
			if k < len(seq.head) && bytes.Equal(suf, seq.head[:k]) {
				return k
			}
		}
	}
	return 0
}

// ReplayDone is closed once the core's replay-end marker (OSC 7702) has been
// written, or the mirror is closed, so a caller can wait for a fresh mirror to
// have caught up with the core's replay before using it.
func (m *Mirror) ReplayDone() <-chan struct{} { return m.done }

// markReplayDone closes the ReplayDone channel once. The caller holds m.mu.
func (m *Mirror) markReplayDone() { m.doneOnce.Do(func() { close(m.done) }) }

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
	lines, _ := m.Snapshot()
	return lines
}

// Snapshot returns the visible screen rows, right-trimmed of spaces, together
// with a mark for each of their runes: marked[row][i] is true when the i-th rune
// of lines[row] sits in a cell whose background colour is not the default. The
// parser uses the marks to tell which chip of a question dialog is active
// (spec 71 REQ-WC-021). Lines returns only the strings.
func (m *Mirror) Snapshot() (lines []string, marked [][]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, w := m.emu.Height(), m.emu.Width()
	lines = make([]string, 0, h)
	marked = make([][]bool, 0, h)
	for y := 0; y < h; y++ {
		line, marks := m.rowMarked(y, w)
		lines = append(lines, line)
		marked = append(marked, marks)
	}
	return lines, marked
}

// rowMarked rebuilds one screen row from the emulator's cells and reports the
// background mark of each of its runes. It reads cells rather than the row's
// text so every rune can be tied back to its cell: a wide character occupies
// one rune but two cells, and counting runes keeps the marks aligned with the
// returned string (spec 71 REQ-WC-021).
func (m *Mirror) rowMarked(y, w int) (string, []bool) {
	var buf strings.Builder
	var marks []bool
	for x := 0; x < w; x++ {
		cell := m.emu.CellAt(x, y)
		if cell == nil {
			break
		}
		// A zero cell is a wide character's continuation cell; it draws
		// nothing and must not shift the rune index.
		if cell.IsZero() || cell.Content == "" {
			continue
		}
		bg := cell.Style.Bg != nil
		buf.WriteString(cell.Content)
		for n := utf8.RuneCountInString(cell.Content); n > 0; n-- {
			marks = append(marks, bg)
		}
	}
	line := buf.String()
	if trimmed := strings.TrimRight(line, " "); len(trimmed) != len(line) {
		marks = marks[:len(marks)-(len(line)-len(trimmed))]
		line = trimmed
	}
	return line, marks
}

// Close ends the emulator's answer pipe, which stops the drain goroutine. The
// pipe is closed rather than the emulator because Emulator.Close writes a flag
// that races with the drain goroutine's Read. It is safe to call more than
// once, it closes ReplayDone so a waiter is never left hanging, and Write after
// Close is a no-op.
func (m *Mirror) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	m.carry = nil
	m.markReplayDone()
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
