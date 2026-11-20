package app

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
)

// fakeAttachment is a pane's attachment without a daemon: tests queue the
// output it hands over and read what went the other way.
type fakeAttachment struct {
	mu      sync.Mutex
	reads   [][]byte
	readErr error
	writes  [][]byte
	resizes [][2]int
	closed  bool
}

func (a *fakeAttachment) Read() ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.reads) > 0 {
		data := a.reads[0]
		a.reads = a.reads[1:]
		return data, nil
	}
	if a.readErr != nil {
		return nil, a.readErr
	}
	return nil, io.EOF
}

func (a *fakeAttachment) Write(data []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.writes = append(a.writes, append([]byte(nil), data...))
	return nil
}

func (a *fakeAttachment) Resize(cols, rows int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resizes = append(a.resizes, [2]int{cols, rows})
	return nil
}

func (a *fakeAttachment) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	return nil
}

func (a *fakeAttachment) wrote() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []byte
	for _, w := range a.writes {
		out = append(out, w...)
	}
	return string(out)
}

func (a *fakeAttachment) resizeCalls() [][2]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([][2]int{}, a.resizes...)
}

func (a *fakeAttachment) isClosed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

// fakeEmu is a pane's terminal emulator without the native library: it
// records the sizes, input and closes it was given.
type fakeEmu struct {
	mu      sync.Mutex
	sizes   [][2]int
	sent    [][]byte
	pasted  []string
	fonts   []float32
	cleared bool
	closed  bool
}

func (e *fakeEmu) Resize(cols, rows int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sizes = append(e.sizes, [2]int{cols, rows})
}

func (e *fakeEmu) Send(data []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sent = append(e.sent, append([]byte(nil), data...))
}

func (e *fakeEmu) Paste(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pasted = append(e.pasted, text)
}

func (e *fakeEmu) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cleared = true
}

func (e *fakeEmu) SetFontSize(size float32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fonts = append(e.fonts, size)
}

func (e *fakeEmu) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	return nil
}

func (e *fakeEmu) View(*ui.Context) ui.Element { return ui.Element{} }

func (e *fakeEmu) lastSize() ([2]int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.sizes) == 0 {
		return [2]int{}, false
	}
	return e.sizes[len(e.sizes)-1], true
}

func (e *fakeEmu) lastSent() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.sent) == 0 {
		return ""
	}
	return string(e.sent[len(e.sent)-1])
}

func (e *fakeEmu) lastPaste() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pasted) == 0 {
		return ""
	}
	return e.pasted[len(e.pasted)-1]
}

func (e *fakeEmu) isClosed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closed
}

// fakeClock is the clock tests drive: a settable now and timers that fire
// when the test fires them (REQ-DW-040, REQ-DW-041).
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    int
	timers map[int]*fakeTimer
}

func newFakeClock() *fakeClock {
	return &fakeClock{
		now:    time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC),
		timers: map[int]*fakeTimer{},
	}
}

func (c *fakeClock) nowTime() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) after(d time.Duration, fn func()) paneTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	t := &fakeTimer{clock: c, id: c.seq, due: c.now.Add(d), fn: fn}
	c.timers[t.id] = t
	return t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fireDue runs the timers that are due, oldest first.
func (c *fakeClock) fireDue() {
	c.mu.Lock()
	now := c.now
	due := []*fakeTimer{}
	for id, t := range c.timers {
		if t.stopped || t.due.After(now) {
			continue
		}
		due = append(due, t)
		delete(c.timers, id)
	}
	c.mu.Unlock()
	for i := 0; i < len(due); i++ {
		for j := i + 1; j < len(due); j++ {
			if due[j].id < due[i].id {
				due[i], due[j] = due[j], due[i]
			}
		}
	}
	for _, t := range due {
		t.fn()
	}
}

type fakeTimer struct {
	clock   *fakeClock
	id      int
	due     time.Time
	fn      func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped {
		return false
	}
	t.stopped = true
	delete(t.clock.timers, t.id)
	return true
}

// noopTimer never fires: it keeps a view test from scheduling real timers.
type noopTimer struct{}

func (noopTimer) Stop() bool { return true }

// termHarness is an app whose clock, attachments, terminals and posting are
// run by the test, so the terminal rules are deterministic.
type termHarness struct {
	t         *testing.T
	app       *App
	clock     *fakeClock
	attaches  []*fakeAttachment
	emus      []*fakeEmu
	attachErr error
	readErr   error
	onKey     func(ui.Modifiers, ui.Key) bool
}

func newTermHarness(t *testing.T) *termHarness {
	t.Helper()
	h := &termHarness{t: t, clock: newFakeClock()}
	h.app = New(Options{Home: "/Users/ada/.lana", UserHome: "/Users/ada"})
	h.app.now = h.clock.nowTime
	h.app.after = h.clock.after
	h.app.async = func(fn func()) { fn() }
	h.app.attach = func(context.Context, string) (paneAttachment, error) {
		if h.attachErr != nil {
			return nil, h.attachErr
		}
		a := &fakeAttachment{readErr: h.readErr}
		if a.readErr == nil {
			a.readErr = io.EOF
		}
		h.attaches = append(h.attaches, a)
		return a, nil
	}
	h.app.termFor = func(_ *paneConn, o termOptions) (paneEmu, error) {
		h.onKey = o.OnKey
		e := &fakeEmu{}
		h.emus = append(h.emus, e)
		return e, nil
	}
	return h
}

func (h *termHarness) session(id string, cols, rows int) {
	h.app.sessions[id] = lana.Session{ID: id, Cols: cols, Rows: rows, Program: "zsh"}
}

func (h *termHarness) pane(id string) *paneTerm { return h.app.termOf(id) }

func (h *termHarness) lastEmu() *fakeEmu {
	h.t.Helper()
	if len(h.emus) == 0 {
		h.t.Fatal("no terminal was made")
	}
	return h.emus[len(h.emus)-1]
}

func (h *termHarness) fireResize() {
	h.clock.advance(resizeDebounce)
	h.clock.fireDue()
}

// TestPaneConnReadsWritesAndResizes is the adapter of REQ-DW-040: the plugin
// reads the session's output in the chunks its buffer takes, writes keys as
// input, and is told the size the pane fit.
func TestPaneConnReadsWritesAndResizes(t *testing.T) {
	h := newTermHarness(t)
	h.session("s1", 80, 24)
	pt := h.pane("s1")
	pt.show()
	at := h.attaches[0]
	emu := h.lastEmu()

	// A chunk larger than the plugin's buffer goes over in pieces.
	at.mu.Lock()
	at.reads = [][]byte{[]byte("hello")}
	at.mu.Unlock()
	conn := &paneConn{pt: pt, gen: pt.gen}
	buf := make([]byte, 3)
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "hel" {
		t.Fatalf("Read = %q, %v; want hel", buf[:n], err)
	}
	n, err = conn.Read(buf)
	if err != nil || string(buf[:n]) != "lo" {
		t.Fatalf("Read = %q, %v; want lo", buf[:n], err)
	}

	// Writing sends key input to the session.
	if _, err := conn.Write([]byte("ls\r")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := at.wrote(); got != "ls\r" {
		t.Fatalf("the session got %q, want ls\\r", got)
	}

	// A follower keeps the session's size, so the view's fit is ignored.
	if err := conn.Resize(120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	h.fireResize()
	if calls := at.resizeCalls(); len(calls) != 0 {
		t.Fatalf("a follower told the core %v", calls)
	}
	if size, _ := emu.lastSize(); size == [2]int{120, 40} {
		t.Fatal("a follower resized its emulator to its box")
	}

	// The owner fits and tells the core, once the debounce passed.
	pt.claim()
	if err := conn.Resize(120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	h.fireResize()
	if size, _ := emu.lastSize(); size != [2]int{120, 40} {
		t.Fatalf("owner emulator size = %v, want 120x40", size)
	}
	if calls := at.resizeCalls(); len(calls) != 1 || calls[0] != [2]int{120, 40} {
		t.Fatalf("the core was told %v, want one 120x40", calls)
	}

	// With nothing left, the attachment's end comes through, and the pane
	// lets its terminal go (REQ-DW-041).
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("Read did not report the stream's end")
	}
}

// TestPaneClaimsWhenShownInFocusedWindow is REQ-DW-040: a pane that opens in
// the focused window takes the pty size.
func TestPaneClaimsWhenShownInFocusedWindow(t *testing.T) {
	h := newTermHarness(t)
	h.app.focusedWin = true
	h.session("s1", 80, 24)
	pt := h.pane("s1")
	pt.show()
	if !pt.owner {
		t.Fatal("a pane shown in the focused window did not claim")
	}
	pt.fit(pt.gen, 100, 30)
	h.fireResize()
	if calls := h.attaches[0].resizeCalls(); len(calls) != 1 || calls[0] != [2]int{100, 30} {
		t.Fatalf("the core was told %v, want one 100x30", calls)
	}
}

// TestPaneClaimsWhenTyping is REQ-DW-040: the user typing in a pane takes the
// pty size for it.
func TestPaneClaimsWhenTyping(t *testing.T) {
	h := newTermHarness(t)
	h.session("s1", 80, 24)
	pt := h.pane("s1")
	pt.show()
	if pt.owner {
		t.Fatal("a pane in an unfocused window claimed on its own")
	}
	pt.fit(pt.gen, 100, 30)
	pt.onKey(0, ui.KeyA)
	if !pt.owner {
		t.Fatal("typing did not claim the size")
	}
	h.fireResize()
	if calls := h.attaches[0].resizeCalls(); len(calls) != 1 || calls[0] != [2]int{100, 30} {
		t.Fatalf("the core was told %v, want one 100x30", calls)
	}
}

// TestPaneHandsOffAfterTheGrace is REQ-DW-040: another client's resize within
// the grace is ignored, and one past it takes the size away.
func TestPaneHandsOffAfterTheGrace(t *testing.T) {
	h := newTermHarness(t)
	h.app.focusedWin = true
	h.session("s1", 80, 24)
	pt := h.pane("s1")
	pt.show()
	pt.fit(pt.gen, 100, 30)
	emu := h.lastEmu()

	// Within the grace, the pane keeps its size.
	pt.sessionSize(90, 25)
	if !pt.owner {
		t.Fatal("a resize within the grace took the size")
	}
	if size, _ := emu.lastSize(); size != [2]int{100, 30} {
		t.Fatalf("emulator size within the grace = %v, want 100x30", size)
	}

	// Past the grace, the other client was used last.
	h.clock.advance(claimGrace)
	pt.sessionSize(90, 25)
	if pt.owner {
		t.Fatal("the pane kept the size past the grace")
	}
	if size, _ := emu.lastSize(); size != [2]int{90, 25} {
		t.Fatalf("emulator size after the hand-off = %v, want 90x25", size)
	}
}

// TestFollowerShowsTheSessionSize is REQ-DW-040: a pane that does not own the
// size shows the session's own, from the events.
func TestFollowerShowsTheSessionSize(t *testing.T) {
	h := newTermHarness(t)
	h.session("s1", 80, 24)
	pt := h.pane("s1")
	pt.show()
	emu := h.lastEmu()
	if size, ok := emu.lastSize(); !ok || size != [2]int{80, 24} {
		t.Fatalf("the pane did not open at the session's size, it shows %v", size)
	}
	pt.sessionSize(90, 25)
	if size, _ := emu.lastSize(); size != [2]int{90, 25} {
		t.Fatalf("the follower shows %v, want 90x25", size)
	}
}

// TestPaneDetachesWhenHiddenAndAttachesAgain is REQ-DW-041: a hidden pane
// lets its attachment go after a while, and attaches again when shown.
func TestPaneDetachesWhenHiddenAndAttachesAgain(t *testing.T) {
	h := newTermHarness(t)
	h.session("s1", 80, 24)
	pt := h.pane("s1")
	pt.show()
	emu := h.lastEmu()
	first := h.attaches[0]

	pt.hide()
	if pt.emulator() == nil {
		t.Fatal("a pane hidden a moment ago dropped its terminal already")
	}
	h.clock.advance(hiddenDetach)
	h.clock.fireDue()
	if pt.emulator() != nil {
		t.Fatal("a pane hidden for 30 s kept its terminal")
	}
	if !emu.isClosed() {
		t.Fatal("the terminal was not freed")
	}
	if !first.isClosed() {
		t.Fatal("the attachment was not closed")
	}

	pt.show()
	if len(h.attaches) != 2 {
		t.Fatalf("showing the pane attached %d times, want 2", len(h.attaches))
	}
	if pt.emulator() == nil {
		t.Fatal("showing the pane did not make a terminal again")
	}
}

// TestPaneRetriesAndStopsOnSessionNotFound is REQ-DW-041: a stream that fails
// retries with the backoff, and a session that is gone stops for good.
func TestPaneRetriesAndStopsOnSessionNotFound(t *testing.T) {
	h := newTermHarness(t)
	h.session("s1", 80, 24)
	pt := h.pane("s1")
	pt.show()
	first := h.attaches[0]

	// The stream ends: the pane shows the reconnecting line and retries.
	pt.ended(pt.gen, io.ErrUnexpectedEOF)
	if !pt.isReconnecting() {
		t.Fatal("a stream that ended did not ask to reconnect")
	}
	h.clock.advance(time.Second)
	h.clock.fireDue()
	if len(h.attaches) != 2 {
		t.Fatalf("the retry attached %d times, want 2", len(h.attaches))
	}
	if !first.isClosed() {
		t.Fatal("the old attachment was not closed on the retry")
	}

	// A session that is gone stops retrying.
	pt.ended(pt.gen, lana.ErrSessionNotFound)
	if pt.isReconnecting() {
		t.Fatal("a session that is gone still asked to reconnect")
	}
	pt.show()
	if len(h.attaches) != 2 {
		t.Fatal("a pane whose session is gone attached again")
	}
}

// TestPaneKeepsTheLastScreenOfAnExitedSession is REQ-DW-033: when the program
// ends, the pane keeps its screen for the exited bar to sit over.
func TestPaneKeepsTheLastScreenOfAnExitedSession(t *testing.T) {
	h := newTermHarness(t)
	h.session("s1", 80, 24)
	pt := h.pane("s1")
	pt.show()
	emu := h.lastEmu()
	h.app.sessions["s1"] = lana.Session{ID: "s1", Cols: 80, Rows: 24, State: "exited"}

	pt.ended(pt.gen, io.EOF)
	if pt.emulator() == nil {
		t.Fatal("the pane dropped the exited session's last screen")
	}
	if emu.isClosed() {
		t.Fatal("the exited session's terminal was freed")
	}
	if pt.isReconnecting() {
		t.Fatal("the pane retried a session that had exited")
	}
}

// TestTerminalKeyIsTheMacOSEditingKeys is REQ-DW-042.
func TestTerminalKeyIsTheMacOSEditingKeys(t *testing.T) {
	cases := []struct {
		name string
		mods ui.Modifiers
		key  ui.Key
		want string
		ok   bool
	}{
		{"Shift+Enter", ui.Shift, ui.KeyEnter, "\x1b\r", true},
		{"Cmd+Left", ui.Super, ui.KeyLeft, "\x01", true},
		{"Cmd+Right", ui.Super, ui.KeyRight, "\x05", true},
		{"Cmd+Backspace", ui.Super, ui.KeyBackspace, "\x15", true},
		{"Option+Left", ui.Alt, ui.KeyLeft, "\x1bb", true},
		{"Option+Right", ui.Alt, ui.KeyRight, "\x1bf", true},
		{"Option+Backspace", ui.Alt, ui.KeyBackspace, "\x1b\x7f", true},
		// The app's own shortcuts are left to the plugin and the menus.
		{"Cmd+T", ui.Super, ui.KeyT, "", false},
		{"Cmd+C", ui.Super, ui.KeyC, "", false},
		{"Cmd+V", ui.Super, ui.KeyV, "", false},
		{"Cmd+A", ui.Super, ui.KeyA, "", false},
		{"Enter", 0, ui.KeyEnter, "", false},
		{"Ctrl+Left", ui.Ctrl, ui.KeyLeft, "", false},
		{"Option+Shift+Left", ui.Alt | ui.Shift, ui.KeyLeft, "", false},
	}
	for _, tc := range cases {
		got, ok := terminalKey(tc.mods, tc.key)
		if ok != tc.ok || got != tc.want {
			t.Errorf("terminalKey(%s) = %q, %v; want %q, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// TestPaneTitleDropsUnusableTitles is the first-run fix: a title that is not
// valid UTF-8, or carries U+FFFD, names nothing and the agent or the
// program stands in.
func TestPaneTitleDropsUnusableTitles(t *testing.T) {
	cases := []struct {
		name string
		s    lana.Session
		want string
	}{
		{"replacement character", lana.Session{Title: "cla\uFFFDde", Program: "claude"}, "Claude Code"},
		{"invalid utf-8", lana.Session{Title: string([]byte{0xff, 0xfe}), Program: "codex"}, "Codex"},
		{"blank", lana.Session{Title: "   ", Program: "claude"}, "Claude Code"},
		{"usable", lana.Session{Title: "api", Program: "claude"}, "api"},
	}
	for _, tc := range cases {
		if got := paneTitle(tc.s); got != tc.want {
			t.Errorf("%s: paneTitle = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestPaneHeaderShortensTheUserHome is the first-run fix: the header shows the
// working directory from the user's home, not the Lana home.
func TestPaneHeaderShortensTheUserHome(t *testing.T) {
	a := testApp(t)
	a.userHome = "/Users/ada"
	s := a.sessions["s1"]
	s.Cwd = "/Users/ada/work"
	a.sessions["s1"] = s
	tt := ui.NewTester(a.view, 1440, 900)
	if !tt.HasText("~/work") {
		t.Errorf("the pane header does not show ~/work; it shows %q", tt.Texts())
	}
}
