package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/config"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// The sizes a new session's pty starts with, until its pane measures it.
const (
	defaultCols = 80
	defaultRows = 24
	// actionTimeout bounds a call to the daemon the app makes for the user.
	actionTimeout = 15 * time.Second
)

// Options are what the app needs to start.
type Options struct {
	// Home is the Lana home to talk to (REQ-DW-012).
	Home     string
	Settings config.Settings
	// DataDir is the app's data directory, where settings.json is kept
	// (REQ-DW-060).
	DataDir string
	// UserHome is the user's home directory, shown as "~" in a pane's header.
	// An empty one is the system's.
	UserHome string
	// Dev marks a development build, so the default Lana home is ~/.lana-dev
	// and the settings screen says so (REQ-DW-012, REQ-DW-060).
	Dev bool
	// SystemDark reports whether the system is in its dark appearance, for a
	// window that follows it. A nil one reads MyGo's own appearance; a test
	// fixes it (REQ-DW-035).
	SystemDark func() bool
	// Dial opens a connection. A nil one is DialLocal.
	Dial Dialer
	// Host reads the machine for the host chip. A nil one is the system's.
	Host HostInfo
	// NewWebView makes the window's web view for the preview panel, given
	// the URL it should show (REQ-DW-083). A nil one is left by a test, and
	// the panel draws a placeholder instead.
	NewWebView func(url string) *mygo.WebView
	// Now is the clock, for tests.
	Now func() time.Time
}

// App is the state of the window: the daemon connection, the sessions and
// the shared layout document (REQ-DW-013, REQ-DW-020).
type App struct {
	home     string
	userHome string
	settings config.Settings
	dataDir  string
	dev      bool
	dial     Dialer
	host     HostInfo
	now      func() time.Time
	// systemDark tells whether the system is in its dark appearance, and
	// themeOff stops the listener on MyGo's own appearance (REQ-DW-035).
	systemDark func() bool
	themeOff   func()

	// The command palette (REQ-DW-052): whether it shows, what is typed in
	// it, and the row it has selected.
	paletteOpen  bool
	paletteQuery string
	paletteSel   int

	// The settings sheet (REQ-DW-060): whether it shows, and the Lana home
	// being typed into its field.
	settingsOpen bool
	settingsHome string

	win *mygo.Window
	// post hands a function to the UI thread and draws a frame
	// (mygo.Window.Update). Tests run it at once, on their own thread.
	post func(func())

	// The seams the terminal code is driven through (see term.go).
	attach  attachFunc
	termFor termFactory
	after   timerFunc
	async   func(func())

	// terms are the terminals of the sessions the window shows, made as the
	// view draws them (REQ-DW-036, REQ-DW-041). focusReq asks the view to
	// give a pane's terminal the keyboard focus.
	terms    map[string]*paneTerm
	focusReq string
	// focusedWin tells whether the window has the keyboard focus, for size
	// ownership (REQ-DW-040).
	focusedWin bool

	// conn is the live connection, nil while not connected.
	conn    Conn
	online  bool
	connErr string

	sessions map[string]lana.Session
	layout   layout.Layout

	// done is the client-only "done" mark: the ids of agents that finished
	// their work while the user was not looking at their pane, cleared when
	// the pane gets the focus (spec 58, done status). It is kept here, not
	// in the shared layout document.
	done map[string]bool

	// recent lists, per tab, the sessions by when they last had the focus,
	// the latest last: closing a pane gives the focus back to the one the
	// user used before it (spec 58, window design). It is kept here, not in
	// the shared layout document.
	recent map[string][]string

	// entering and closing are the pane cards that fade in as their pane is
	// made and out as it is closed (spec 58, window design; REQ-DW-044). A
	// card in entering is posed faded out for one frame and cleared there; a
	// card in closing is posed faded out until its session is killed. A pane
	// that was never marked — one at app start, on a tab switch, or from
	// another client's layout event — is not in either, and never fades.
	entering map[string]bool
	closing  map[string]bool

	// save is the layout write of REQ-DW-023: one write at a time, the
	// latest wins.
	save     debounce
	inflight bool
	pending  *json.RawMessage

	// renamingTab is the tab whose title is being edited in place.
	renamingTab string
	renameText  string
	renameFocus bool

	// dialog is the rename dialog a space row or a tab opens from its menu
	// (spec 58, spaces).
	dialog renameDialog

	// spaceMenus is the ⋯ menu-open state of each space row, so that a row's
	// button keeps being built, and so its menu survives the pointer leaving
	// the row for the menu (spec 58, spaces; PopoverMenuButtonOptions.Open).
	spaceMenus map[string]*bool

	// badge is the last Dock badge the app showed (REQ-DW-037).
	badge int
	// notice is a transient error, shown as a banner over the window.
	notice string

	// The preview panel (REQ-DW-080 … REQ-DW-084, REQ-WP-040): the file it
	// shows, its width as a share of the workspace, whether it is the focused
	// region (for Esc), and the web view it uses, made lazily.
	preview        *previewState
	previewShare   float32
	previewFocused bool
	previewView    *mygo.WebView
	newWebView     func(url string) *mygo.WebView

	ctx    context.Context
	cancel context.CancelFunc
	// connCancel ends the connection loop alone, so a new Lana home can start
	// it over without ending the app's own context (REQ-DW-060).
	connCancel context.CancelFunc
	wake       chan struct{}
}

// New makes the app. Call Start once there is a window.
func New(o Options) *App {
	if o.Dial == nil {
		o.Dial = DialLocal
	}
	if o.Host == nil {
		o.Host = &systemHost{}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.UserHome == "" {
		o.UserHome = homeDir()
	}
	a := &App{
		home:       o.Home,
		userHome:   o.UserHome,
		settings:   o.Settings,
		dataDir:    o.DataDir,
		dev:        o.Dev,
		dial:       o.Dial,
		host:       o.Host,
		now:        o.Now,
		systemDark: o.SystemDark,
		post:       func(fn func()) { fn() },
		terms:      map[string]*paneTerm{},
		sessions:   map[string]lana.Session{},
		layout:     layout.Empty(),
		done:       map[string]bool{},
		entering:   map[string]bool{},
		closing:    map[string]bool{},
		spaceMenus: map[string]*bool{},
		wake:       make(chan struct{}, 1),

		previewShare: defaultPreviewShare,
		newWebView:   o.NewWebView,
	}
	// The seams: the real dial, terminal and clock, and a goroutine for the
	// calls that must not hold the UI thread.
	a.attach = func(ctx context.Context, id string) (paneAttachment, error) {
		if a.conn == nil {
			return nil, errors.New("app: not connected to the daemon")
		}
		return a.conn.Attach(ctx, id)
	}
	a.termFor = pluginTerm
	a.after = func(d time.Duration, fn func()) paneTimer { return time.AfterFunc(d, fn) }
	a.async = func(fn func()) { go fn() }
	return a
}

// View is the window's content: the view, the app's shortcuts, and the
// command palette (REQ-DW-050, REQ-DW-052).
func (a *App) View() func(c *ui.Context) {
	return func(c *ui.Context) {
		a.view(c)
		a.shortcuts(c)
		a.commandPalette(c)
		a.dialogs(c)
	}
}

// Start binds the app to a window and connects to the daemon, retrying
// with backoff (REQ-DW-013).
func (a *App) Start(win *mygo.Window) {
	a.win = win
	a.post = win.Update
	a.focusedWin = win.IsFocused()
	// The window's own parts and MyGo's widgets follow the appearance the
	// settings chose, and a change to the system's when it follows it
	// redraws the palette (REQ-DW-035).
	a.applyAppearance()
	a.themeOff = mygo.Theme.OnUpdated(a.invalidate)
	// A window that gains the focus takes the pty size for every pane it
	// shows (REQ-DW-040).
	win.OnFocus(func() {
		a.post(func() {
			a.focusedWin = true
			a.claimVisible()
			// A done pane the window comes back to stops being done: the
			// user is looking at it again (spec 58, done status).
			a.clearFocusedDone()
		})
	})
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.startConn()
}

// startConn runs the connection loop under a context of its own, so that a
// new Lana home can end it and start over without touching the app's context
// (REQ-DW-060).
func (a *App) startConn() {
	if a.ctx == nil {
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.connCancel = cancel
	go a.connectLoop(ctx)
}

// Stop ends the connection loop and the event stream, and lets every pane's
// terminal go. It never ends a session (REQ-DW-044).
func (a *App) Stop() {
	if a.connCancel != nil {
		a.connCancel()
		a.connCancel = nil
	}
	if a.cancel != nil {
		a.cancel()
	}
	if a.themeOff != nil {
		a.themeOff()
		a.themeOff = nil
	}
	// The preview panel's web view goes with the window (REQ-DW-083).
	a.previewView = nil
	a.releaseTerms()
	a.win = nil
}

// invalidate asks the window for a frame. It does nothing before there is
// a window: tests drive the view with a Tester of their own.
func (a *App) invalidate() {
	if a.win != nil {
		a.win.Invalidate()
	}
}

// connectLoop keeps a connection to the daemon: it connects, loads the
// sessions and the layout, then reads the event stream, reconnecting with
// backoff whenever any of that fails (REQ-DW-013).
func (a *App) connectLoop(ctx context.Context) {
	var backoff lana.Backoff
	for ctx.Err() == nil {
		conn, sessions, raw, err := a.dialAndLoad(ctx)
		if err != nil {
			reason := connectReason(err, a.home, a.addressOf(conn))
			a.post(func() { a.setOffline(reason) })
			if a.wait(ctx, backoff.Next()) {
				return
			}
			continue
		}
		backoff.Reset()
		a.post(func() { a.setOnline(conn, sessions, raw) })
		err = conn.Subscribe(ctx, func(ev lana.Event) {
			a.post(func() { a.applyEvent(ev) })
		})
		if ctx.Err() != nil {
			return
		}
		a.post(func() { a.setOffline("the event stream stopped") })
		if a.wait(ctx, backoff.Next()) {
			return
		}
	}
}

// dialAndLoad opens a connection and reads what the window needs to draw.
// It returns the connection, when there is one, even on failure, so the
// reason can name its address.
func (a *App) dialAndLoad(ctx context.Context) (Conn, []lana.Session, json.RawMessage, error) {
	conn, err := a.dial(ctx, a.home)
	if err != nil {
		return nil, nil, nil, err
	}
	sessions, err := conn.ListSessions(ctx)
	if err != nil {
		return conn, nil, nil, err
	}
	raw, err := conn.GetLayout(ctx)
	if err != nil {
		return conn, nil, nil, err
	}
	return conn, sessions, raw, nil
}

// wait sleeps for d, returning early when ctx ends or the user asks for a
// retry. It reports whether the loop should stop.
func (a *App) wait(ctx context.Context, d time.Duration) (done bool) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-a.wake:
		return false
	case <-timer.C:
		return false
	}
}

// addressOf is where the app tried to reach the daemon: the connection's
// address, else the one daemon.secret names, so the not-connected state can
// say where (REQ-DW-013).
func (a *App) addressOf(conn Conn) string {
	if conn != nil {
		return conn.BaseURL()
	}
	if secret, err := lana.ReadSecret(a.home); err == nil {
		return secret.Addr
	}
	return ""
}

// setOnline takes a fresh connection and the state it loaded. It runs on
// the UI thread.
func (a *App) setOnline(conn Conn, sessions []lana.Session, raw json.RawMessage) {
	a.conn = conn
	a.online = true
	a.connErr = ""
	loaded := make(map[string]lana.Session, len(sessions))
	for _, s := range sessions {
		loaded[s.ID] = s
	}
	a.sessions = loaded
	a.layout = layout.Parse(raw)
	a.noteFocus()
	a.inflight, a.pending = false, nil
	a.save.reset()
	// The keyboard focus goes to the pane the layout has focused
	// (REQ-DW-040).
	a.focusReq = a.focusedSession()
	a.updateBadge()
	a.invalidate()
}

// setOffline reports why the window is not connected (REQ-DW-013).
func (a *App) setOffline(reason string) {
	if !a.online && a.connErr == reason {
		return
	}
	a.online, a.conn = false, nil
	a.connErr = reason
	a.invalidate()
}

// connectReason is the reason a connection failed, as the not-connected
// state shows it (REQ-DW-013).
func connectReason(err error, home, addr string) string {
	switch {
	case errors.Is(err, lana.ErrNoSecret):
		return "no daemon.secret in " + home
	case errors.Is(err, lana.ErrUnreachable):
		if addr != "" {
			return "daemon not running at " + addr
		}
		return "daemon not running"
	case errors.Is(err, lana.ErrUnavailable):
		return "Workbench core unavailable: " + err.Error()
	}
	return err.Error()
}

// Retry asks the connection loop to try again at once, for the Retry
// button of the not-connected state (REQ-DW-013).
func (a *App) Retry() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// updateBadge shows the number of sessions that need the user on the Dock
// (REQ-DW-037).
func (a *App) updateBadge() {
	n := 0
	for _, s := range a.sessions {
		if s.NeedsYou {
			n++
		}
	}
	if n == a.badge {
		return
	}
	a.badge = n
	if a.win != nil {
		mygo.App.SetBadgeCount(n)
	}
}

// sessionCtx is a context for one action, bounded by the action timeout.
func (a *App) sessionCtx() (context.Context, context.CancelFunc) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, actionTimeout)
}

// setNotice shows a transient error over the window.
func (a *App) setNotice(message string) {
	a.notice = message
	a.invalidate()
}

// DismissNotice clears the banner.
func (a *App) DismissNotice() {
	a.notice = ""
	a.invalidate()
}

// markLayout sets a local layout change, records the focused pane of every
// tab, draws a frame, and schedules the write the daemon gets (REQ-DW-023;
// spec 58, window design).
func (a *App) markLayout(l layout.Layout) {
	a.layout = l
	a.noteFocus()
	a.save.touch(a.now())
	a.invalidate()
}

// Ticks schedules the debounced layout write from the view, which knows
// the current time (REQ-DW-023).
func (a *App) tick(c *ui.Context) {
	if !a.save.dirty || !a.online {
		return
	}
	if left := a.save.remaining(c.Now()); left > 0 {
		c.After(left)
		return
	}
	a.flushLayout()
}

// flushLayout sends the layout to the daemon, one write at a time: a
// change made while a write is in flight waits for it and goes after, so
// the last change always wins (REQ-DW-023).
func (a *App) flushLayout() {
	conn := a.conn
	if conn == nil {
		return
	}
	raw, err := layout.Marshal(a.layout)
	if err != nil {
		return
	}
	a.save.consume()
	if a.inflight {
		document := json.RawMessage(raw)
		a.pending = &document
		return
	}
	a.inflight = true
	a.putLayout(conn, json.RawMessage(raw))
}

// putLayout writes the layout in a goroutine, then, on the UI thread,
// takes the newest change that waited and writes it too.
func (a *App) putLayout(conn Conn, raw json.RawMessage) {
	go func() {
		ctx, cancel := a.sessionCtx()
		defer cancel()
		err := conn.PutLayout(ctx, raw)
		a.post(func() {
			if err != nil {
				a.setNotice("saving the layout: " + err.Error())
			}
			a.inflight = false
			if a.pending != nil {
				next := *a.pending
				a.pending = nil
				// Without a connection the change is dropped: a reconnect
				// reloads the daemon's own layout.
				if a.conn != nil {
					a.inflight = true
					a.putLayout(a.conn, next)
				}
			}
		})
	}()
}

// focusedSession is the session of the pane with the keyboard focus here,
// "" when there is none.
func (a *App) focusedSession() string {
	space := layout.ActiveSpace(a.layout)
	tab := layout.ActiveTab(space)
	if tab == nil {
		return ""
	}
	if tab.Focus != "" {
		return tab.Focus
	}
	if ids := layout.TabSessions(*tab); len(ids) > 0 {
		return ids[0]
	}
	return ""
}

// focusedCwd is the folder new work starts in: the focused pane's folder,
// else the active space's, else the home folder.
func (a *App) focusedCwd() string {
	if id := a.focusedSession(); id != "" {
		if cwd := sessionCwd(a.sessions[id]); cwd != "" {
			return cwd
		}
	}
	if space := layout.ActiveSpace(a.layout); space != nil {
		if cwd := layout.SpaceCwd(space, layoutSessions(a.sessions)); cwd != "" {
			return cwd
		}
	}
	return homeDir()
}

// createSession starts a terminal and hands the new session to then, on
// the UI thread.
func (a *App) createSession(dir string, then func(lana.Session)) {
	conn := a.conn
	if conn == nil {
		return
	}
	go func() {
		ctx, cancel := a.sessionCtx()
		defer cancel()
		s, err := conn.CreateSession(ctx, lana.CreateOptions{Dir: dir, Cols: defaultCols, Rows: defaultRows})
		a.post(func() {
			if err != nil {
				a.setNotice("opening a terminal: " + err.Error())
				return
			}
			a.sessions[s.ID] = s
			then(s)
		})
	}()
}

// NewTab opens a terminal in a space, in dir (REQ-DW-050).
func (a *App) NewTab(spaceID, dir string) {
	if dir == "" {
		dir = homeDir()
	}
	a.createSession(dir, func(s lana.Session) {
		a.focusReq = s.ID
		a.entering[s.ID] = true
		a.markLayout(layout.AddTab(a.layout, spaceID, s.ID, ""))
	})
}

// NewTerminal opens a terminal in a space, in the folder of its active
// pane (REQ-DW-030).
func (a *App) NewTerminal(spaceID string) {
	dir := ""
	for i := range a.layout.Spaces {
		if a.layout.Spaces[i].ID == spaceID {
			dir = layout.SpaceCwd(&a.layout.Spaces[i], layoutSessions(a.sessions))
			if dir == "" {
				dir = a.layout.Spaces[i].Dir
			}
			break
		}
	}
	if dir == "" {
		dir = homeDir()
	}
	a.NewTab(spaceID, dir)
}

// NewSpace makes a space in the focused pane's folder and opens a
// terminal in it (REQ-DW-030, REQ-DW-050), the empty state's button too.
func (a *App) NewSpace() {
	dir := a.focusedCwd()
	if dir == "" {
		dir = homeDir()
	}
	l := layout.AddSpace(a.layout, "", dir)
	space := layout.ActiveSpace(l)
	a.markLayout(l)
	a.NewTab(space.ID, dir)
}

// SplitPane splits a pane, right of it ("row") or below it ("col")
// (REQ-DW-050).
func (a *App) SplitPane(sessionID, dir string) {
	space, tab, ok := layout.Locate(a.layout, sessionID)
	if !ok {
		return
	}
	spaceID, tabID := space.ID, tab.ID
	cwd := sessionCwd(a.sessions[sessionID])
	if cwd == "" {
		cwd = a.focusedCwd()
	}
	a.createSession(cwd, func(s lana.Session) {
		a.focusReq = s.ID
		a.entering[s.ID] = true
		a.markLayout(layout.SplitPane(a.layout, spaceID, tabID, sessionID, s.ID, dir))
	})
}

// ToggleZoom zooms a pane, or unzooms it when it was (REQ-DW-050).
func (a *App) ToggleZoom(sessionID string) {
	space, tab, ok := layout.Locate(a.layout, sessionID)
	if !ok {
		return
	}
	a.focusReq = sessionID
	a.markLayout(layout.ToggleZoom(a.layout, space.ID, tab.ID, sessionID))
}

// FocusPane gives a pane the focus, and its terminal the keyboard focus, so
// that typing goes to it (REQ-DW-050).
func (a *App) FocusPane(sessionID string) {
	space, tab, ok := layout.Locate(a.layout, sessionID)
	if !ok {
		return
	}
	if tab.Focus == sessionID {
		return
	}
	a.focusReq = sessionID
	// Focusing a pane takes the focus off the preview panel, so Esc goes to
	// the terminal again (REQ-DW-081).
	a.previewFocused = false
	a.markLayout(layout.FocusPane(a.layout, space.ID, tab.ID, sessionID))
	// The pane the user just focused stops being done (spec 58, done status).
	a.clearFocusedDone()
}

// SelectSpace makes a space active (REQ-DW-030).
func (a *App) SelectSpace(spaceID string) {
	if a.layout.ActiveSpace == spaceID {
		return
	}
	l := layout.SelectSpace(a.layout, spaceID)
	if tab := layout.ActiveTab(layout.ActiveSpace(l)); tab != nil {
		a.focusReq = tab.Focus
	}
	a.markLayout(l)
	a.clearFocusedDone()
}

// SelectTab makes a tab active and shows its space (REQ-DW-030).
func (a *App) SelectTab(spaceID, tabID string) {
	l := layout.SelectTab(layout.SelectSpace(a.layout, spaceID), spaceID, tabID)
	if tab := findTab(l, spaceID, tabID); tab != nil {
		a.focusReq = tab.Focus
	}
	a.markLayout(l)
	a.clearFocusedDone()
}

// SetGrouping is how the Agents panel groups sessions, "space" or "status"
// (REQ-DW-030).
func (a *App) SetGrouping(grouping string) {
	if a.layout.Grouping == grouping {
		return
	}
	a.markLayout(layout.SetGrouping(a.layout, grouping))
}

// MoveTab moves a tab to another place among the tabs of its space
// (REQ-DW-034).
func (a *App) MoveTab(spaceID, tabID string, to int) {
	a.markLayout(layout.MoveTab(a.layout, spaceID, tabID, to))
}

// MoveTabToSpace moves a tab into another space (REQ-DW-034).
func (a *App) MoveTabToSpace(fromSpace, tabID, toSpace string) {
	a.markLayout(layout.MoveTabToSpace(a.layout, fromSpace, tabID, toSpace))
}

// StartRename begins editing a tab's title in place (REQ-DW-034).
func (a *App) StartRename(spaceID, tabID string) {
	a.renamingTab = tabID
	a.renameText, a.renameFocus = "", false
	if tab := findTab(a.layout, spaceID, tabID); tab != nil {
		a.renameText = tab.Title
	}
	a.invalidate()
}

// CommitRename keeps the edited title (REQ-DW-034).
func (a *App) CommitRename(spaceID, tabID string) {
	title := strings.TrimSpace(a.renameText)
	a.renamingTab, a.renameText, a.renameFocus = "", "", false
	a.markLayout(layout.RenameTab(a.layout, spaceID, tabID, title))
	a.focusReq = a.focusedSession()
}

// EndRename stops editing a tab's title, keeping it as it was.
func (a *App) EndRename() {
	if a.renamingTab == "" {
		return
	}
	a.renamingTab, a.renameText, a.renameFocus = "", "", false
	a.focusReq = a.focusedSession()
	a.invalidate()
}

// RenameText is the title being typed in a tab's rename field.
func (a *App) RenameText() *string { return &a.renameText }

// ClosePane closes a pane: its card fades out for closeFadeDelay, then its
// session is killed at once (REQ-DW-044). The focus moves off the pane at once
// when it held it, so it does not wait for the kill and jump far (spec 58,
// window design). A second close while it fades does nothing.
func (a *App) ClosePane(sessionID string) {
	if a.conn == nil || a.closing[sessionID] {
		return
	}
	a.focusOffClosing(sessionID)
	a.closing[sessionID] = true
	a.invalidate()
	a.after(closeFadeDelay, func() {
		a.post(func() {
			if a.conn == nil {
				delete(a.closing, sessionID)
				return
			}
			a.killSession(sessionID)
		})
	})
}

// CloseTab closes a tab and kills every session in it at once
// (REQ-DW-044).
func (a *App) CloseTab(spaceID, tabID string) {
	tab := findTab(a.layout, spaceID, tabID)
	if tab == nil {
		return
	}
	ids := layout.TabSessions(*tab)
	conn := a.conn
	for _, id := range ids {
		if conn != nil {
			go func(id string) {
				ctx, cancel := a.sessionCtx()
				defer cancel()
				_ = conn.KillSession(ctx, id)
			}(id)
		}
		delete(a.sessions, id)
		delete(a.done, id)
		delete(a.entering, id)
		delete(a.closing, id)
		a.forgetFocus(id)
	}
	a.markLayout(layout.CloseTab(a.layout, spaceID, tabID))
}

// killSession ends a session and drops it from the layout (REQ-DW-044).
func (a *App) killSession(sessionID string) {
	conn := a.conn
	if conn == nil {
		return
	}
	go func() {
		ctx, cancel := a.sessionCtx()
		defer cancel()
		err := conn.KillSession(ctx, sessionID)
		a.post(func() {
			if err != nil {
				a.setNotice("closing the session: " + err.Error())
			}
			delete(a.sessions, sessionID)
			delete(a.done, sessionID)
			delete(a.closing, sessionID)
			a.markLayout(a.dropSession(sessionID))
		})
	}()
}

// Restart restarts an exited session (REQ-DW-033).
func (a *App) Restart(sessionID string) {
	conn := a.conn
	if conn == nil {
		return
	}
	go func() {
		ctx, cancel := a.sessionCtx()
		defer cancel()
		s, err := conn.RestartSession(ctx, sessionID)
		a.post(func() {
			if err != nil {
				a.setNotice("restarting the session: " + err.Error())
				return
			}
			a.sessions[s.ID] = s
			a.invalidate()
		})
	}()
}

// findTab finds a tab of a space, nil when it is not there.
func findTab(l layout.Layout, spaceID, tabID string) *layout.Tab {
	for i := range l.Spaces {
		if l.Spaces[i].ID != spaceID {
			continue
		}
		for j := range l.Spaces[i].Tabs {
			if l.Spaces[i].Tabs[j].ID == tabID {
				return &l.Spaces[i].Tabs[j]
			}
		}
		return nil
	}
	return nil
}
