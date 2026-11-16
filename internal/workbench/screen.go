package workbench

import (
	"io"
	"net/url"
	"strings"

	"github.com/charmbracelet/x/vt"
)

// screenScrollback is how many scrollback lines the detection screen keeps.
// Agent state reads the visible screen plus this tail, which keeps the
// emulator's memory bounded.
const screenScrollback = 200

// oscProgressCommand is the OSC command that carries a progress report; its
// payload is "4;<state>[;<value>]".
const oscProgressCommand = 9

// oscWorkingDirectoryCommand is the OSC command that carries the shell's
// working directory; its payload is "7;file://<host>/<url-escaped path>", the
// form OSC 7 shells and tools emit (spec 55 A4).
const oscWorkingDirectoryCommand = 7

// screen is a session's headless terminal emulator. It processes the pty
// output as it arrives, so the detector can read the screen text on its tick.
// Every method is called with the session lock held, which is what serializes
// the emulator, the title callback and the render.
type screen struct {
	emu *vt.SafeEmulator
}

// newScreen creates a screen of cols×rows. onProgress and onWorkingDir are
// called from write, under the session lock. The window title is not read from
// the emulator: the session parses the OSC 0/2 payload itself, so a multi-byte
// title split across reads arrives whole (spec 55 A5).
func newScreen(cols, rows int, onProgress, onWorkingDir func(string)) *screen {
	emu := vt.NewSafeEmulator(cols, rows)
	emu.SetScrollbackSize(screenScrollback)
	emu.RegisterOscHandler(oscProgressCommand, func(data []byte) bool {
		onProgress(oscProgressPayload(data))
		return true
	})
	emu.RegisterOscHandler(oscWorkingDirectoryCommand, func(data []byte) bool {
		if dir := parseOSC7(data); dir != "" {
			onWorkingDir(dir)
		}
		return true
	})
	// The emulator answers terminal queries (DA1, DSR, …) through an unbuffered
	// pipe; nobody needs those answers here (the attached xterm.js clients
	// answer the program), and an unread pipe would block Write forever while
	// it holds the session lock. Read does not take the emulator lock.
	go func() { _, _ = io.Copy(io.Discard, emu) }()
	return &screen{emu: emu}
}

// close ends the emulator's answer pipe, which stops the drain goroutine.
// Rendering still works afterwards.
func (s *screen) close() { _ = s.emu.Close() }

// write feeds terminal output to the emulator.
func (s *screen) write(data []byte) { _, _ = s.emu.Write(data) }

// resize sizes the emulated screen to match the pty.
func (s *screen) resize(cols, rows int) { s.emu.Resize(cols, rows) }

// text renders the visible screen preceded by up to screenScrollback scrollback
// lines, as plain text the detector can match against.
func (s *screen) text() string {
	var b strings.Builder
	if sb := s.emu.Scrollback(); sb != nil {
		lines := sb.Lines()
		start := len(lines) - screenScrollback
		if start < 0 {
			start = 0
		}
		for _, line := range lines[start:] {
			b.WriteString(line.String())
			b.WriteByte('\n')
		}
	}
	b.WriteString(s.emu.String())
	return b.String()
}

// oscProgressPayload strips the command prefix the parser leaves in the OSC
// payload, turning "9;4;1;50" into "4;1;50".
func oscProgressPayload(data []byte) string {
	if rest, ok := strings.CutPrefix(string(data), "9;"); ok {
		return rest
	}
	return string(data)
}

// parseOSC7 returns the working directory an OSC 7 payload reports, with
// percent escapes decoded. The payload is "7;file://<host>/<escaped path>"; a
// bare path with no scheme is accepted too. It returns "" when the payload
// carries no usable directory.
func parseOSC7(data []byte) string {
	payload, ok := strings.CutPrefix(string(data), "7;")
	if !ok || payload == "" {
		return ""
	}
	// url.Parse decodes the path's percent escapes into Path; a payload that is
	// only a path parses the same way, with an empty scheme and host.
	u, err := url.Parse(payload)
	if err != nil {
		return ""
	}
	return u.Path
}
