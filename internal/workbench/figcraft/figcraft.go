// Package figcraft reads .figcraft design files for the Workbench FigCraft
// viewer (spec 67, REQ-FV-001..008). It runs the external figcraft-cli binary
// without a shell, with a private temp folder per run, fixed automation roots
// and a 30 s timeout (REQ-FV-003), and it answers page lists, layer children
// and PNG renders from the file on disk (REQ-FV-004..006). Results are cached
// in memory keyed by the file fingerprint (REQ-FV-008), and a failed run on a
// changing file falls back to the last good result (REQ-FV-007). Live mode over
// the FigCraft control port (REQ-FV-010..013) reads an open document instead: a
// Service only ever calls its source, and a chooser wires the live app and the
// file source. It knows nothing of the web server or the Workbench core, so its
// rules are tested on their own against a fake CLI and a fake control server.
package figcraft

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// maxConcurrent is how many figcraft-cli processes may run at once
// (REQ-FV-003).
const maxConcurrent = 2

// cliTTL is how long a found CLI path, its version and the app version are
// reused (REQ-FV-001, REQ-FV-002).
const cliTTL = 60 * time.Second

// Rect is a rectangle in page units (REQ-FV-004).
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Point is a position in page units.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Size is a width and height in page units.
type Size struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Layer is one layer of a page (REQ-FV-004, REQ-FV-005).
type Layer struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Bounds     Rect   `json:"bounds"`
	ChildCount int    `json:"child_count"`
	Visible    bool   `json:"visible"`
}

// Page is one page of a design with its top-level layers (REQ-FV-004).
type Page struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Divider    bool    `json:"divider"`
	Background string  `json:"background,omitempty"`
	Origin     Point   `json:"origin"`
	Size       Size    `json:"size"`
	Layers     []Layer `json:"layers"`
}

// CLIInfo is the path and version of the figcraft-cli a Service found
// (REQ-FV-001).
type CLIInfo struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

// AppInfo is the version of the FigCraft app (REQ-FV-002).
type AppInfo struct {
	Version string `json:"version"`
}

// Info is the answer to one Info request (REQ-FV-021).
type Info struct {
	Path        string   `json:"path"`
	Source      string   `json:"source"`
	LivePort    int      `json:"live_port"`
	LiveError   string   `json:"live_error"`
	Fingerprint string   `json:"fingerprint"`
	Unsaved     bool     `json:"unsaved"`
	Stale       bool     `json:"stale"`
	CLI         *CLIInfo `json:"cli"`
	App         *AppInfo `json:"app"`
	Warnings    []string `json:"warnings"`
	Pages       []Page   `json:"pages"`
}

// Children is the list of a node's children (REQ-FV-005).
type Children struct {
	Node        int64   `json:"node"`
	Fingerprint string  `json:"fingerprint"`
	Children    []Layer `json:"children"`
}

// Render is one PNG render with the page units of its top-left pixel
// (REQ-FV-006).
type Render struct {
	PNG         []byte
	OriginX     float64
	OriginY     float64
	Scale       float64
	Fingerprint string
	Stale       bool
	Took        time.Duration
}

// Settings is the FigCraft viewer settings (REQ-FV-050).
type Settings struct {
	CLIPath   string `json:"cli_path"`
	Live      bool   `json:"live"`
	LivePorts []int  `json:"live_ports"`
}

// DefaultSettings is the default FigCraft viewer settings (REQ-FV-050): live on,
// no custom CLI path and no live ports.
func DefaultSettings() Settings {
	return Settings{Live: true, LivePorts: []int{}}
}

// Options configures a Service. Every field may be nil or empty for the
// production default, so the zero Options is usable.
type Options struct {
	// Settings returns the current viewer settings; nil means DefaultSettings.
	Settings func() Settings
	// HomeDir holds ~/.local/share/figcraft-cli/figcraft-cli; empty means the
	// user's home folder.
	HomeDir string
	// TempDir is the private temp root of every run; empty means os.TempDir.
	TempDir string
	// LookPath finds figcraft-cli on PATH; nil means exec.LookPath.
	LookPath func(string) (string, error)
	// AppVersion reads the FigCraft app version; nil means the macOS
	// Info.plist.
	AppVersion func() string
	// DiscoverPorts finds the control ports of running FigCraft apps (REQ-FV-010);
	// nil means the macOS lsof detector, and none elsewhere.
	DiscoverPorts func(ctx context.Context) ([]int, error)
	// Logger takes the debug and warn lines; nil means slog.Default.
	Logger *slog.Logger
	// Now is the time source; nil means time.Now.
	Now func() time.Time
}

// Service answers FigCraft viewer requests (spec 67). It holds the resolved
// CLI, the settings, the caches and the source every request goes through.
type Service struct {
	opts     Options
	log      *slog.Logger
	settings func() Settings
	now      func() time.Time
	appVerFn func() string

	sem  chan struct{}
	runs singleflight.Group

	cache *cache
	stale *staleStore

	mu         sync.Mutex
	cliKey     string
	cliAt      time.Time
	cliPath    string
	cliVer     string
	cliMissing *CLIMissingError
	appAt      time.Time
	appVer     string

	// src is the chooser between the live app and the file on disk
	// (REQ-FV-013); the file source is the fallback.
	src  source
	file *fileSource
}

// source is where a Service reads a design from (spec 67): the file source
// (REQ-FV-004..006) or the live source (REQ-FV-012), chosen by the chooser, so
// a Service always calls exactly one source.
type source interface {
	Info(ctx context.Context, abs string) (Info, error)
	Layers(ctx context.Context, abs string, node int64) (Children, error)
	RenderPage(ctx context.Context, abs string, page int64, scale float64) (Render, error)
	RenderNode(ctx context.Context, abs string, node int64, scale float64) (Render, error)
}

// New builds a Service. It fills the defaults of opts and wires the chooser
// between the live app and the file source (REQ-FV-013).
func New(opts Options) *Service {
	if opts.Settings == nil {
		opts.Settings = DefaultSettings
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.HomeDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			opts.HomeDir = home
		}
	}
	if opts.AppVersion == nil {
		opts.AppVersion = defaultAppVersion
	}
	if opts.DiscoverPorts == nil {
		opts.DiscoverPorts = defaultDiscoverPorts
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	s := &Service{
		opts:     opts,
		log:      opts.Logger,
		settings: opts.Settings,
		now:      opts.Now,
		appVerFn: opts.AppVersion,
		sem:      make(chan struct{}, maxConcurrent),
		cache:    newCache(),
		stale:    newStaleStore(),
	}
	s.file = &fileSource{svc: s}
	s.src = newChooser(s)
	return s
}

// Info answers a page list with each page's top-level layers (REQ-FV-004).
func (s *Service) Info(ctx context.Context, abs string) (Info, error) {
	return s.src.Info(ctx, abs)
}

// Layers lists the children of one node (REQ-FV-005).
func (s *Service) Layers(ctx context.Context, abs string, node int64) (Children, error) {
	return s.src.Layers(ctx, abs, node)
}

// RenderPage renders one page as PNG (REQ-FV-006).
func (s *Service) RenderPage(ctx context.Context, abs string, page int64, scale float64) (Render, error) {
	return s.src.RenderPage(ctx, abs, page, scale)
}

// RenderNode renders one node as PNG (REQ-FV-006).
func (s *Service) RenderNode(ctx context.Context, abs string, node int64, scale float64) (Render, error) {
	return s.src.RenderNode(ctx, abs, node, scale)
}

// clock is the time source: the injected test clock when set, else time.Now.
func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// buildInfo assembles an Info from cached pages data (REQ-FV-021): the file
// source, the CLI and app versions and the warnings.
func (s *Service) buildInfo(ctx context.Context, abs, fp string, data infoData, stale bool) Info {
	path, ver, _ := s.cli(ctx)
	info := Info{
		Path:        abs,
		Source:      "file",
		Fingerprint: fp,
		Stale:       stale,
		Pages:       data.Pages,
		Warnings:    []string{},
	}
	if info.Pages == nil {
		info.Pages = []Page{}
	}
	if path != "" {
		info.CLI = &CLIInfo{Path: path, Version: ver}
	}
	app := s.appVersion()
	if app != "" {
		info.App = &AppInfo{Version: app}
	}
	if cliOlder(ver, app) {
		info.Warnings = append(info.Warnings, "cli_older_than_app")
	}
	return info
}

// CLIMissingError means no figcraft-cli was found (REQ-FV-001). Tried lists the
// paths that were looked at, in order.
type CLIMissingError struct {
	Tried []string
}

// Error lists the paths that were tried.
func (e *CLIMissingError) Error() string {
	return "figcraft: no figcraft-cli found (tried " + strings.Join(e.Tried, ", ") + ")"
}

// ErrNotFound means the file is missing, or a page or node is not in the file.
var ErrNotFound = errors.New("figcraft: not found")

// FailedError is a CLI run that failed (REQ-FV-003). Code is "figcraft_failed"
// for a non-zero exit, "figcraft_timeout" for a run the timeout killed; Message
// is the first non-empty stderr line, so the caller can put it in the daemon
// envelope unchanged.
type FailedError struct {
	Code    string
	Message string
}

// Error renders the failure as code and message.
func (e *FailedError) Error() string {
	if e.Message == "" {
		return "figcraft: " + e.Code
	}
	return "figcraft: " + e.Code + ": " + e.Message
}

// semver is a simple x.y.z version (REQ-FV-002).
type semver [3]int

// parseSemver parses "x.y.z" (an optional leading v is dropped). It reports
// false for anything else, including pre-release suffixes.
func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		v[i] = n
	}
	return v, true
}

// less reports whether v sorts before o.
func (v semver) less(o semver) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] < o[i]
		}
	}
	return false
}

// cliOlder reports whether the CLI version is older than the app version, so
// Info can warn (REQ-FV-002). Two unparseable versions never compare.
func cliOlder(cli, app string) bool {
	cv, ok1 := parseSemver(cli)
	av, ok2 := parseSemver(app)
	if !ok1 || !ok2 {
		return false
	}
	return cv.less(av)
}
