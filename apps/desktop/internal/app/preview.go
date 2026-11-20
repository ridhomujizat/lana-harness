package app

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// The preview panel's width as a share of the workspace, and the limits a
// drag keeps it between (REQ-DW-081).
const (
	defaultPreviewShare float32 = 0.45
	minPreviewShare     float32 = 0.2
	maxPreviewShare     float32 = 0.8
)

// The kinds the daemon answers (REQ-WP-002); the panel draws each one
// differently (REQ-DW-083).
const (
	previewKindHTML     = "html"
	previewKindMarkdown = "markdown"
	previewKindImage    = "image"
	previewKindPDF      = "pdf"
	previewKindText     = "text"
	previewKindBinary   = "binary"
)

// previewState is the file the panel shows (REQ-DW-080 … REQ-DW-083): the
// file the daemon resolved, its name and folder, the line to scroll to, its
// kind, the absolute daemon URLs the web view loads, and its size.
type previewState struct {
	path   string
	name   string
	dir    string
	line   int
	col    int
	kind   string
	url    string
	rawURL string
	size   int64
}

// fileName is the name the panel's header shows, the daemon's own name when it
// gave one and the path's base otherwise.
func (p *previewState) fileName() string {
	if p.name != "" {
		return p.name
	}
	return filepath.Base(p.path)
}

// folder is the folder the panel's header shows, the daemon's own dir when it
// gave one and the path's dir otherwise.
func (p *previewState) folder() string {
	if p.dir != "" {
		return p.dir
	}
	return filepath.Dir(p.path)
}

// OpenPreview opens the file a ⌘+click on a terminal link names (REQ-DW-080,
// REQ-WP-040): the daemon resolves and classifies the link, and the panel
// shows the URL it answers. The daemon call runs off the UI thread, so nothing
// opens until the answer arrives; a failure only shows a notice. A link with
// a URL scheme other than file:// is left to the system browser, so it reports
// false. It reports whether it handled the link.
func (a *App) OpenPreview(session, link, cwd string) bool {
	if !isLocalPreviewLink(link) {
		return false
	}
	conn := a.conn
	if conn == nil {
		a.setNotice("preview: not connected to the daemon")
		return true
	}
	dir := a.previewDir(cwd)
	go func() {
		ctx, cancel := a.sessionCtx()
		defer cancel()
		file, err := conn.Preview(ctx, session, link, dir)
		a.post(func() {
			if err != nil {
				a.setNotice(err.Error())
				return
			}
			a.applyPreview(file)
		})
	}()
	return true
}

// isLocalPreviewLink reports whether a link may name a local file (REQ-DW-080):
// a file:// URL or a path. It is a cheap, local check only; any other URL
// scheme is left to the system browser, and the daemon has the last word on
// what is a file.
func isLocalPreviewLink(link string) bool {
	link = strings.TrimSpace(link)
	if link == "" {
		return false
	}
	if strings.HasPrefix(link, "file://") {
		return true
	}
	return !strings.Contains(link, "://")
}

// previewDir is the space folder the daemon tries a relative link against last
// (REQ-WP-001): the active space's folder, else the terminal's own folder.
func (a *App) previewDir(cwd string) string {
	if space := layout.ActiveSpace(a.layout); space != nil && space.Dir != "" {
		return space.Dir
	}
	return cwd
}

// applyPreview shows the file the daemon answered and points the web view at
// its URL (REQ-DW-083, REQ-WP-040). It runs on the UI thread.
func (a *App) applyPreview(file lana.PreviewFile) {
	state := &previewState{
		path:   file.Path,
		name:   file.Name,
		dir:    file.Dir,
		line:   file.Line,
		col:    file.Col,
		kind:   file.Kind,
		url:    a.daemonURL(file.URL),
		rawURL: a.daemonURL(file.RawURL),
		size:   file.Size,
	}
	a.preview = state
	// The panel the user just opened is where Esc closes it (REQ-DW-081).
	a.previewFocused = true
	a.navigatePreview(state.url)
	a.invalidate()
}

// daemonURL makes a daemon-relative path absolute against the connection's
// base URL, the same way attach builds the WebSocket address.
func (a *App) daemonURL(rel string) string {
	if rel == "" || a.conn == nil {
		return rel
	}
	return strings.TrimRight(a.conn.BaseURL(), "/") + rel
}

// daemonLocal reports whether the daemon the app talks to runs on this machine
// (REQ-WP-040): its address is a loopback one. Open in default app and Reveal
// in Finder act on a local path alone, so a remote daemon hides them.
func (a *App) daemonLocal() bool {
	if a.conn == nil {
		return false
	}
	u, err := url.Parse(a.conn.BaseURL())
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

// ClosePreview closes the preview panel (REQ-DW-081).
func (a *App) ClosePreview() {
	if a.preview == nil {
		return
	}
	a.preview = nil
	a.previewFocused = false
	a.invalidate()
}

// openPreviewInApp opens the previewed file in the default app (REQ-DW-040).
// It acts on a local daemon alone: a remote one's path is on another machine.
func (a *App) openPreviewInApp(path string) {
	if !a.daemonLocal() {
		a.setNotice("the preview is on a remote daemon")
		return
	}
	openInDefaultApp(path)
}

// revealPreview reveals the previewed file in the file manager (REQ-DW-040).
// It acts on a local daemon alone.
func (a *App) revealPreview(path string) {
	if !a.daemonLocal() {
		a.setNotice("the preview is on a remote daemon")
		return
	}
	revealInFinder(path)
}

// navigatePreview points the panel's web view at url (REQ-DW-083): the first
// preview makes the view, later ones navigate the one there. A view that was
// destroyed (its window closed) is made again. It does nothing before there
// is a web view factory, as in a headless test.
func (a *App) navigatePreview(url string) {
	if a.newWebView == nil {
		return
	}
	if a.previewView == nil {
		a.previewView = a.newWebView(url)
		return
	}
	if err := a.previewView.Page().LoadURL(url); err != nil || a.previewView.IsDestroyed() {
		a.previewView = a.newWebView(url)
	}
}

// humanSize is a file size as the panel's binary message shows it, "1.2 MB".
func humanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	f := float64(n) / 1024
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}
