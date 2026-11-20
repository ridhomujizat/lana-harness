package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/config"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// The timings of a pane's terminal, as the web Workbench's
// workbench-term.tsx has them (REQ-DW-040, REQ-DW-041).
const (
	// resizeDebounce is how long a fit waits before it is told to the core,
	// so a divider drag is one resize.
	resizeDebounce = 60 * time.Millisecond
	// claimGrace is how long a pane that took the pty size keeps it: a size
	// the session reports later than this means another client was used
	// last.
	claimGrace = 1500 * time.Millisecond
	// hiddenDetach is how long a hidden pane keeps its attachment before it
	// lets it go; showing it again attaches and replays the ring.
	hiddenDetach = 30 * time.Second
)

// The padding around a pane's terminal, in DIPs (spec 58, window design).
const (
	termPadTop    = 0
	termPadBottom = 6
	termPadSide   = 5
)

// errNoAttachment reports a terminal read or write while the pane is between
// attaches.
var errNoAttachment = errors.New("app: the pane has no attachment")

// paneAttachment is one attached session (REQ-DW-040): the daemon sends
// terminal output through Read and takes key input through Write, and a size
// change goes through Resize. *lana.Attachment is the real one; tests use a
// fake.
type paneAttachment interface {
	Read() ([]byte, error)
	Write(data []byte) error
	Resize(cols, rows int) error
	Close() error
}

var _ paneAttachment = (*lana.Attachment)(nil)

// paneEmu is a pane's terminal emulator. The terminal plugin's terminal is
// the real one; a fake stands in for it in tests, so the ownership and
// lifecycle rules run without the native library.
type paneEmu interface {
	// Resize sets the screen's size, in cells: the pane's fit when it owns
	// the size, the session's own size otherwise (REQ-DW-040).
	Resize(cols, rows int)
	// Send sends bytes to the program as if typed (REQ-DW-042).
	Send(data []byte)
	// Paste pastes text into the program, bracketed when it asked for
	// bracketed pastes (REQ-DW-043).
	Paste(text string)
	// Clear clears the screen and scrollback of this terminal alone
	// (REQ-DW-050).
	Clear()
	// SetFontSize sets the text size of a running terminal (REQ-DW-036).
	SetFontSize(size float32)
	// Close hangs up the program and frees the emulator.
	Close() error
	// View draws the terminal in the window (REQ-DW-036).
	View(c *ui.Context) ui.Element
}

// termOptions are what a pane's terminal needs from its pane.
type termOptions struct {
	// FontSize is the terminals' text size, in DIPs (REQ-DW-036).
	FontSize float32
	// OnKey sees a key before the terminal encodes it (REQ-DW-042).
	OnKey func(mods ui.Modifiers, key ui.Key) bool
	// OnDrop sees the paths of files dropped on the pane's terminal before
	// the terminal escapes and pastes them, and reports whether it took the
	// drop (REQ-DW-043).
	OnDrop func(paths []string) bool
	// OpenLink sees a link the user ⌘+clicks in a terminal before the system
	// opens it, and reports whether it opened it (REQ-DW-080).
	OpenLink func(link string) bool
}

// termFactory makes a pane's emulator over conn. It is a seam for tests.
type termFactory func(conn *paneConn, o termOptions) (paneEmu, error)

// attachFunc opens the control-mode attachment of a session (REQ-DW-040).
type attachFunc func(ctx context.Context, id string) (paneAttachment, error)

// timerFunc schedules fn to run once after d and returns the timer. It is a
// seam for tests, which drive the clock themselves (REQ-DW-040, REQ-DW-041).
type timerFunc func(d time.Duration, fn func()) paneTimer

// paneTimer is a scheduled callback a pane can stop.
type paneTimer interface{ Stop() bool }

// pluginTerm makes the real emulator, by the terminal plugin (REQ-DW-002).
func pluginTerm(conn *paneConn, o termOptions) (paneEmu, error) {
	t, err := terminal.New(terminal.Options{
		// The pane reads and writes the session itself, and the view only
		// asks for the size it fits: the screen keeps the size the pane
		// gives it, so a follower shows the session's own size
		// (REQ-DW-040).
		Conn:         conn,
		ResizeByConn: true,
		// The pane says its own end, so the plugin leaves out its
		// "[Disconnected]" line (REQ-DW-041).
		NoExitMessage: true,
		// The pane card is dark, so the terminal is too (REQ-DW-035). The
		// terminal draws no background of its own: the card's colour shows
		// through, so no second rectangle shows and the card's brightness
		// is the pane's focus (REQ-DW-040).
		Theme:       workbenchTerm,
		Transparent: true,
		// Option sends Alt (Meta) to programs (REQ-DW-042).
		OptionAsAlt: true,
		// The body draws the padding, so the grid has none of its own.
		Padding: terminal.Padding{X: -1, Y: -1},
		OnKey:   o.OnKey,
		OnDrop:  o.OnDrop,
		// A ⌘+click on a link comes to the app, which previews a file and
		// leaves a URL to the system browser (REQ-DW-080).
		OpenLink: o.OpenLink,
		// The terminal draws its text in Geist Mono, embedded with the app
		// (spec 58, REQ-DW-036).
		Font: terminal.Font{Family: monoFontFamily, Size: o.FontSize},
	})
	if err != nil {
		return nil, err
	}
	return pluginEmu{t}, nil
}

// pluginEmu is the terminal plugin's terminal as a paneEmu.
type pluginEmu struct{ *terminal.Terminal }

// View draws the terminal (REQ-DW-036).
func (e pluginEmu) View(c *ui.Context) ui.Element { return terminal.View(c, e.Terminal) }

// Paste pastes text into the program, bracketed when it asked for bracketed
// pastes, by the terminal plugin's own paste path (REQ-DW-043).
func (e pluginEmu) Paste(text string) { e.Terminal.Paste(text) }

// Clear clears the screen and scrollback of the terminal alone, leaving
// the session's own screen as it is (REQ-DW-050). A full-screen program's
// screen is left alone, as ClearScreen does.
func (e pluginEmu) Clear() { e.ClearScreen() }

// SetFontSize sets the text size of a running terminal (REQ-DW-036).
func (e pluginEmu) SetFontSize(size float32) { e.SetFont(terminal.Font{Size: size}) }

// workbenchTerm is the Workbench's dark terminal theme: the web Workbench's
// xterm colours. Its background is the focused pane card's colour: the
// terminal draws none of its own (Transparent), so only programs that ask
// for it, or the contrast of text, see it (REQ-DW-035, REQ-DW-036).
var workbenchTerm = &terminal.Theme{
	Background: ui.Hex("#2A2826"),
	Foreground: ui.Hex("#E6E4DF"),
	Cursor:     ui.Hex("#E6E4DF"),
	CursorText: ui.Hex("#191817"),
	Selection:  ui.Hex("#3A3835"),
	Palette: [16]ui.Color{
		ui.Hex("#2A2927"), ui.Hex("#F08A80"), ui.Hex("#7FD39A"), ui.Hex("#E8C96A"),
		ui.Hex("#8AA8F0"), ui.Hex("#C99AF0"), ui.Hex("#7CC9CC"), ui.Hex("#E8E6E1"),
		ui.Hex("#8A867E"), ui.Hex("#F5A79F"), ui.Hex("#9EE3B4"), ui.Hex("#F0D98E"),
		ui.Hex("#A9C0F5"), ui.Hex("#D9B6F5"), ui.Hex("#A0DCDE"), ui.Hex("#FFFFFF"),
	},
}

// terminalKey returns the bytes a macOS terminal sends for a key the plugin
// does not, as the web Workbench's terminalKey does (REQ-DW-042): Shift+Enter
// is the newline agents read, and Cmd and Option move and delete by line and
// by word. It reports false for a key the plugin encodes itself, so its own
// copy, paste and other shortcuts are left alone.
func terminalKey(mods ui.Modifiers, key ui.Key) (string, bool) {
	shift := mods&ui.Shift != 0
	alt := mods&ui.Alt != 0
	cmd := mods&ui.Super != 0
	if mods&ui.Ctrl != 0 {
		return "", false
	}
	switch {
	case key == ui.KeyEnter && shift && !alt && !cmd:
		return "\x1b\r", true
	case cmd && !alt:
		switch key {
		case ui.KeyLeft:
			return "\x01", true
		case ui.KeyRight:
			return "\x05", true
		case ui.KeyBackspace:
			return "\x15", true
		}
	case alt && !cmd && !shift:
		switch key {
		case ui.KeyLeft:
			return "\x1bb", true
		case ui.KeyRight:
			return "\x1bf", true
		case ui.KeyBackspace:
			return "\x1b\x7f", true
		}
	}
	return "", false
}

// paneConn adapts a pane's attachment to the terminal plugin's Conn
// (REQ-DW-040): the plugin reads the session's output through Read, writes
// keys through Write, and is told the size the view fits through Resize.
type paneConn struct {
	pt  *paneTerm
	gen int
	mu  sync.Mutex
	// rest is the tail of an output chunk that did not fit the plugin's
	// read buffer, handed over on the next Read.
	rest []byte
}

// Read hands the plugin the session's output, a chunk at a time.
func (c *paneConn) Read(p []byte) (int, error) {
	for {
		c.mu.Lock()
		if len(c.rest) > 0 {
			n := copy(p, c.rest)
			c.rest = c.rest[n:]
			c.mu.Unlock()
			return n, nil
		}
		c.mu.Unlock()
		data, err := c.pt.read(c.gen)
		if err != nil {
			return 0, err
		}
		if len(data) == 0 {
			continue
		}
		n := copy(p, data)
		if n < len(data) {
			c.mu.Lock()
			c.rest = append(c.rest[:0], data[n:]...)
			c.mu.Unlock()
		}
		return n, nil
	}
}

// Write sends key input to the session.
func (c *paneConn) Write(p []byte) (int, error) {
	if err := c.pt.write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close does nothing: the pane closes the attachment itself.
func (c *paneConn) Close() error { return nil }

// Resize is the size the view fits.
func (c *paneConn) Resize(cols, rows int) error { return c.pt.fit(c.gen, cols, rows) }

// paneTerm is the live terminal of one pane the window shows (REQ-DW-036,
// REQ-DW-040, REQ-DW-041): the attachment to the session, the emulator over
// it, and the rules of size ownership and of hiding.
type paneTerm struct {
	app       *App
	sessionID string

	// mu guards what the terminal's goroutines read: the attachment, the
	// emulator, the ownership state and the resize debounce.
	mu   sync.Mutex
	flow paneAttachment
	term paneEmu
	// owner tells that this pane sets the pty size; a follower shows the
	// session's own size (REQ-DW-040). claimedAt is when it last took it.
	owner     bool
	claimedAt time.Time
	// fitCols/fitRows is the size the view last fitted; sessCols/sessRows the
	// session's own size, from the events; cols/rows the size the emulator
	// shows.
	fitCols, fitRows   int
	sessCols, sessRows int
	cols, rows         int
	// gen numbers the pane's attachments, so a straggling read of the one
	// before is ignored.
	gen         int
	resizeTimer paneTimer

	// The rest runs on the UI thread, in the view and in the posted
	// callbacks: whether the pane is shown, and its lifecycle timers.
	visible      bool
	starting     bool
	reconnecting bool
	gone         bool
	hideTimer    paneTimer
	retryTimer   paneTimer
	backoff      lana.Backoff
}

// read reads the next chunk of the attachment's output for generation gen,
// reporting the stream's end to the UI thread (REQ-DW-041).
func (pt *paneTerm) read(gen int) ([]byte, error) {
	pt.mu.Lock()
	flow := pt.flow
	pt.mu.Unlock()
	if flow == nil {
		return nil, errNoAttachment
	}
	data, err := flow.Read()
	if err != nil {
		pt.app.post(func() { pt.ended(gen, err) })
	}
	return data, err
}

// write sends key input to the attachment (REQ-DW-040).
func (pt *paneTerm) write(data []byte) error {
	pt.mu.Lock()
	flow := pt.flow
	pt.mu.Unlock()
	if flow == nil {
		return errNoAttachment
	}
	return flow.Write(data)
}

// fit is the size the view fits, from the plugin. The pane takes it when it
// owns the pty size, and keeps the session's own size otherwise (REQ-DW-040).
func (pt *paneTerm) fit(gen, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	pt.mu.Lock()
	if gen != pt.gen {
		pt.mu.Unlock()
		return nil
	}
	pt.fitCols, pt.fitRows = cols, rows
	take := pt.owner
	pt.mu.Unlock()
	if !take {
		return nil
	}
	pt.resizeEmulator(cols, rows)
	pt.scheduleResize()
	return nil
}

// resizeEmulator sets the emulator's screen to cols×rows and remembers it.
func (pt *paneTerm) resizeEmulator(cols, rows int) {
	pt.mu.Lock()
	term := pt.term
	changed := pt.cols != cols || pt.rows != rows
	pt.cols, pt.rows = cols, rows
	pt.mu.Unlock()
	if term != nil && changed {
		term.Resize(cols, rows)
	}
}

// sessionSize applies the size the session reports (a session.updated event,
// REQ-DW-040): a follower shows it, and an owner lets it take the size when
// it comes later than the grace, since another client was used last.
func (pt *paneTerm) sessionSize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	pt.mu.Lock()
	pt.sessCols, pt.sessRows = cols, rows
	if pt.owner {
		same := pt.cols == cols && pt.rows == rows
		if same || pt.app.now().Sub(pt.claimedAt) < claimGrace {
			pt.mu.Unlock()
			return
		}
		pt.owner = false
	}
	pt.mu.Unlock()
	pt.resizeEmulator(cols, rows)
}

// claim takes the pty size for the pane: it fits its box and tells the core
// (REQ-DW-040). It runs on the UI thread, as the view or a key does.
func (pt *paneTerm) claim() {
	pt.mu.Lock()
	if pt.gone {
		pt.mu.Unlock()
		return
	}
	pt.owner = true
	pt.claimedAt = pt.app.now()
	cols, rows := pt.fitCols, pt.fitRows
	pt.mu.Unlock()
	if cols > 0 && rows > 0 {
		pt.resizeEmulator(cols, rows)
		pt.scheduleResize()
	}
}

// onKey sees a key before the terminal encodes it (REQ-DW-042): it takes the
// pty size when the user types, and sends the macOS editing keys the plugin
// does not. It reports whether it took the key.
func (pt *paneTerm) onKey(mods ui.Modifiers, key ui.Key) bool {
	pt.claim()
	seq, ok := terminalKey(mods, key)
	if !ok {
		return false
	}
	pt.send([]byte(seq))
	return true
}

// send sends bytes to the program as if typed.
func (pt *paneTerm) send(data []byte) {
	pt.mu.Lock()
	term := pt.term
	pt.mu.Unlock()
	if term != nil {
		term.Send(data)
	}
}

// paste sends text to the program as a paste, bracketed when the program
// asked for bracketed pastes (REQ-DW-043).
func (pt *paneTerm) paste(text string) {
	if text == "" {
		return
	}
	pt.mu.Lock()
	term := pt.term
	pt.mu.Unlock()
	if term != nil {
		term.Paste(text)
	}
}

// dropFiles takes the files dropped on the pane's terminal (REQ-DW-043): the
// app types their quoted paths into the session and focuses the pane. It
// reports that it took the drop, so the terminal plugin pastes nothing of its
// own.
func (pt *paneTerm) dropFiles(paths []string) bool {
	pt.app.dropFilesOnPane(pt.sessionID, paths)
	return true
}

// scheduleResize tells the core the pane's fit once a quiet moment passed.
func (pt *paneTerm) scheduleResize() {
	pt.mu.Lock()
	if pt.resizeTimer != nil {
		pt.resizeTimer.Stop()
	}
	pt.resizeTimer = pt.app.after(resizeDebounce, pt.flushResize)
	pt.mu.Unlock()
}

// flushResize sends the pane's fit, when it still owns the size.
func (pt *paneTerm) flushResize() {
	pt.mu.Lock()
	pt.resizeTimer = nil
	flow, owner := pt.flow, pt.owner
	cols, rows := pt.fitCols, pt.fitRows
	pt.mu.Unlock()
	if !owner || flow == nil || cols <= 0 || rows <= 0 {
		return
	}
	_ = flow.Resize(cols, rows)
}

// show makes the pane visible and starts its terminal when it has none
// (REQ-DW-041).
func (pt *paneTerm) show() {
	if pt.visible {
		return
	}
	pt.visible = true
	if pt.hideTimer != nil {
		pt.hideTimer.Stop()
		pt.hideTimer = nil
	}
	if pt.term == nil && !pt.starting && !pt.gone {
		pt.start()
	}
}

// hide marks the pane hidden and lets its attachment and terminal go once it
// has been hidden for a while (REQ-DW-041).
func (pt *paneTerm) hide() {
	if !pt.visible {
		return
	}
	pt.visible = false
	if pt.hideTimer != nil {
		pt.hideTimer.Stop()
	}
	pt.hideTimer = pt.app.after(hiddenDetach, func() {
		pt.app.post(pt.detach)
	})
}

// detach lets the pane's attachment and terminal go, when it is still hidden
// (REQ-DW-041).
func (pt *paneTerm) detach() {
	pt.hideTimer = nil
	if pt.visible {
		return
	}
	pt.stopRetrying()
	pt.release()
	pt.starting = false
}

// start opens the pane's attachment and makes the terminal over it
// (REQ-DW-040). It runs the dial off the UI thread.
func (pt *paneTerm) start() {
	if pt.starting || pt.term != nil || pt.gone {
		return
	}
	pt.starting = true
	pt.mu.Lock()
	pt.gen++
	gen := pt.gen
	pt.mu.Unlock()
	app := pt.app
	app.async(func() {
		flow, err := app.attach(app.termCtx(), pt.sessionID)
		if err != nil {
			app.post(func() { pt.startFailed(gen, err) })
			return
		}
		pt.mu.Lock()
		if gen != pt.gen {
			pt.mu.Unlock()
			flow.Close()
			return
		}
		pt.flow = flow
		pt.mu.Unlock()
		term, err := app.termFor(&paneConn{pt: pt, gen: gen}, termOptions{
			FontSize: app.textSize(),
			OnKey:    pt.onKey,
			OnDrop:   pt.dropFiles,
			// A link the user ⌘+clicks goes to the daemon, which resolves it
			// and answers the file to preview; a non-file URL is left to the
			// system browser (REQ-DW-080, REQ-WP-040).
			OpenLink: func(link string) bool {
				return app.OpenPreview(pt.sessionID, link, sessionCwd(app.sessions[pt.sessionID]))
			},
		})
		if err != nil {
			flow.Close()
			pt.mu.Lock()
			pt.flow = nil
			pt.mu.Unlock()
			app.post(func() { pt.startFailed(gen, err) })
			return
		}
		app.post(func() { pt.started(gen, term) })
	})
}

// startFailed is an attach that did not open. ErrSessionNotFound stops for
// good; anything else retries while the pane is shown (REQ-DW-041).
func (pt *paneTerm) startFailed(gen int, err error) {
	pt.mu.Lock()
	current := gen == pt.gen
	pt.mu.Unlock()
	if !current {
		return
	}
	pt.starting = false
	if errors.Is(err, lana.ErrSessionNotFound) {
		// The session is gone: the SSE removal drops the pane.
		pt.gone = true
		pt.app.invalidate()
		return
	}
	if !pt.visible {
		return
	}
	pt.reconnecting = true
	pt.retryLater()
	pt.app.invalidate()
}

// started takes the pane's new terminal and shows the session's own size
// until a fit or a claim comes; a pane that opens in the focused window takes
// the size (REQ-DW-040).
func (pt *paneTerm) started(gen int, term paneEmu) {
	pt.mu.Lock()
	current := gen == pt.gen
	if current {
		pt.term = term
	}
	cols, rows := pt.sessCols, pt.sessRows
	pt.mu.Unlock()
	if !current {
		term.Close()
		return
	}
	pt.starting = false
	pt.reconnecting = false
	pt.backoff.Reset()
	if cols > 0 && rows > 0 {
		pt.resizeEmulator(cols, rows)
	}
	if pt.app.windowFocused() {
		pt.claim()
	}
	pt.app.invalidate()
}

// ended is the end of the pane's stream, reported by its read (REQ-DW-041).
func (pt *paneTerm) ended(gen int, err error) {
	pt.mu.Lock()
	current := gen == pt.gen
	pt.mu.Unlock()
	if !current {
		return
	}
	if errors.Is(err, lana.ErrSessionNotFound) {
		pt.gone = true
		pt.release()
		pt.app.invalidate()
		return
	}
	if !pt.visible {
		// Hidden: let it go now; showing it attaches again.
		pt.release()
		return
	}
	if s, ok := pt.app.sessions[pt.sessionID]; ok && s.State == "exited" {
		// The program ended: its last screen stays, and the exited bar
		// shows over it (REQ-DW-033). A hide and show attaches again.
		return
	}
	pt.release()
	pt.reconnecting = true
	pt.retryLater()
	pt.app.invalidate()
}

// retryLater schedules the next attach with the backoff (REQ-DW-041).
func (pt *paneTerm) retryLater() {
	if pt.retryTimer != nil {
		pt.retryTimer.Stop()
	}
	d := pt.backoff.Next()
	pt.retryTimer = pt.app.after(d, func() {
		pt.app.post(pt.retry)
	})
}

// retry attaches again, while the pane is still shown.
func (pt *paneTerm) retry() {
	pt.retryTimer = nil
	if !pt.visible || pt.gone {
		return
	}
	pt.start()
}

// stopRetrying cancels a scheduled retry.
func (pt *paneTerm) stopRetrying() {
	if pt.retryTimer != nil {
		pt.retryTimer.Stop()
		pt.retryTimer = nil
	}
	pt.reconnecting = false
}

// release lets the pane's terminal and attachment go, freeing the terminal
// (REQ-DW-041). It does not end the session. It also numbers the pane anew,
// so an attach still in flight is dropped rather than kept for a pane that
// no longer shows it.
func (pt *paneTerm) release() {
	pt.mu.Lock()
	pt.gen++
	flow, term := pt.flow, pt.term
	pt.flow, pt.term = nil, nil
	pt.mu.Unlock()
	if flow != nil {
		flow.Close()
	}
	if term != nil {
		term.Close()
	}
}

// stop lets the pane go for good, as when its session is removed or the app
// quits.
func (pt *paneTerm) stop() {
	if pt.hideTimer != nil {
		pt.hideTimer.Stop()
		pt.hideTimer = nil
	}
	pt.stopRetrying()
	pt.mu.Lock()
	if pt.resizeTimer != nil {
		pt.resizeTimer.Stop()
		pt.resizeTimer = nil
	}
	pt.mu.Unlock()
	pt.gone = true
	pt.release()
}

// rebuild makes the pane's terminal anew, as when a session that had exited
// runs again.
func (pt *paneTerm) rebuild() {
	pt.release()
	pt.starting = false
	if pt.visible && !pt.gone {
		pt.start()
	}
}

// sessionUpdated folds a change the events reported of the pane's session
// (REQ-DW-040): a new size follows the ownership rules, and a session
// restarted from exited needs its terminal again.
func (pt *paneTerm) sessionUpdated(was lana.Session, now lana.Session) {
	if now.Cols > 0 && now.Rows > 0 {
		pt.sessionSize(now.Cols, now.Rows)
	}
	if was.State == "exited" && now.State != "exited" {
		pt.rebuild()
	}
}

// emulator is the pane's terminal, nil while it is between attaches.
func (pt *paneTerm) emulator() paneEmu {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.term
}

// isReconnecting tells whether the pane's stream is away and a retry is
// coming (REQ-DW-041).
func (pt *paneTerm) isReconnecting() bool { return pt.reconnecting }

// termOf returns the terminal of a session's pane, making the record on the
// first frame that shows the session (REQ-DW-041).
func (a *App) termOf(sessionID string) *paneTerm {
	if pt := a.terms[sessionID]; pt != nil {
		return pt
	}
	pt := &paneTerm{app: a, sessionID: sessionID}
	if s, ok := a.sessions[sessionID]; ok {
		pt.sessCols, pt.sessRows = s.Cols, s.Rows
	}
	a.terms[sessionID] = pt
	return pt
}

// syncTerms shows the panes of the active tab and hides the rest
// (REQ-DW-041).
func (a *App) syncTerms() {
	visible := map[string]bool{}
	for _, id := range a.visibleSessions() {
		visible[id] = true
	}
	for id, pt := range a.terms {
		if visible[id] {
			pt.show()
		} else {
			pt.hide()
		}
	}
}

// visibleSessions are the sessions the window shows: the active tab's, or the
// one zoomed of it (REQ-DW-041).
func (a *App) visibleSessions() []string {
	space := layout.ActiveSpace(a.layout)
	tab := layout.ActiveTab(space)
	if tab == nil {
		return nil
	}
	if tab.Zoom != "" {
		return []string{tab.Zoom}
	}
	return layout.TabSessions(*tab)
}

// claimVisible takes the pty size for every pane the window shows, when the
// window gains the focus (REQ-DW-040).
func (a *App) claimVisible() {
	for _, id := range a.visibleSessions() {
		if pt := a.terms[id]; pt != nil {
			pt.claim()
		}
	}
}

// windowFocused tells whether the window has the keyboard focus, for size
// ownership (REQ-DW-040).
func (a *App) windowFocused() bool { return a.focusedWin }

// textSize is the terminals' text size, in DIPs (REQ-DW-036).
func (a *App) textSize() float32 {
	if a.settings.TextSize > 0 {
		return a.settings.TextSize
	}
	return config.DefaultTextSize
}

// termCtx is the context the panes' attachments live in: the app's own,
// ended when it stops.
func (a *App) termCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// dropTerm lets a session's pane go, as when the session is removed.
func (a *App) dropTerm(sessionID string) {
	if pt := a.terms[sessionID]; pt != nil {
		pt.stop()
		delete(a.terms, sessionID)
	}
}

// releaseTerms lets every pane's terminal go, as when the window closes. It
// never ends a session.
func (a *App) releaseTerms() {
	for _, pt := range a.terms {
		pt.stop()
	}
	a.terms = map[string]*paneTerm{}
}

// dropFilesOnPane is what a drop of files on a pane's card does (REQ-DW-043),
// as iTerm2, Ghostty and Terminal.app do it: the pane takes the focus, its
// terminal takes the keyboard focus, and the paths go into the session as a
// paste, quoted for a shell and separated by spaces, so the shell takes them
// as arguments and Return runs nothing on its own. A drop on a session that
// has exited does nothing.
func (a *App) dropFilesOnPane(sessionID string, paths []string) {
	if len(paths) == 0 {
		return
	}
	if s, ok := a.sessions[sessionID]; ok && s.State == "exited" {
		return
	}
	a.FocusPane(sessionID)
	a.focusReq = sessionID
	a.termOf(sessionID).paste(shellQuotePaths(paths))
}

// shellQuotePaths escapes the paths of files dropped on a pane for the shell
// the session runs, for typing them at its prompt: a path of plain characters
// stands as it is, and any other is single-quoted, its own single quotes
// escaped, so the shell reads it as one argument whatever it is named. The
// paths are separated by spaces and followed by one trailing space, so the
// shell takes the next thing typed as a new word, and with no newline the
// paths run nothing until Return. It returns "" for no paths.
func shellQuotePaths(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = shellQuotePath(p)
	}
	return strings.Join(quoted, " ") + " "
}

// shellQuotePath quotes one path for a shell when it holds a character the
// shell would read otherwise, as a space, a quote or a backslash.
func shellQuotePath(path string) string {
	if !strings.ContainsFunc(path, func(r rune) bool { return !shellPathPlain(r) }) {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// shellPathPlain reports whether a rune stands in a shell word without
// quoting: a letter, a digit, or one of the punctuation marks a file name
// commonly holds that a shell reads as text.
func shellPathPlain(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("_./:@%+=,-", r)
}
