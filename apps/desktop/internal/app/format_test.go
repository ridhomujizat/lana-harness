package app

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// TestTileTable is the program tile table (REQ-WP-032): the shell tile, a
// few agents' tiles, pi and omp, and an unknown program's monogram.
func TestTileTable(t *testing.T) {
	type want struct {
		bg, fg ui.Color
		border bool
		logo   bool
		mono   bool
		prompt bool
		glyph  string
		accent bool
	}
	cases := []struct {
		name string
		in   string
		want want
	}{
		{"shell", "zsh", want{bg: ui.Hex("#2E2D2B"), fg: ui.Hex("#5CBA86"), border: true, prompt: true}},
		{"claude", "claude", want{bg: ui.Hex("#D97757"), fg: ui.Hex("#FFFFFF"), logo: true, mono: true}},
		{"codex", "codex", want{bg: ui.Hex("#F7F7F7"), fg: ui.Hex("#1A1A1A"), border: true, logo: true, mono: true}},
		{"cursor", "cursor", want{bg: ui.Hex("#1A1A1A"), fg: ui.Hex("#FFFFFF"), logo: true, mono: true}},
		{"gemini", "gemini", want{bg: ui.Hex("#2E2D2B"), fg: ui.Hex("#FFFFFF"), logo: true}},
		{"pi", "pi", want{bg: ui.Hex("#1A1A1A"), fg: ui.Hex("#FFFFFF"), glyph: "π"}},
		{"omp", "omp", want{bg: ui.Hex("#1A1A1A"), fg: ui.Hex("#FFFFFF"), glyph: "π", accent: true}},
		{"unknown program", "my-tool", want{bg: hashColor("my-tool"), fg: ui.Hex("#FFFFFF"), glyph: "M"}},
	}
	for _, tc := range cases {
		got := tileFor(tc.in)
		switch {
		case got.BG != tc.want.bg:
			t.Errorf("%s: BG = %v, want %v", tc.name, got.BG, tc.want.bg)
		case got.FG != tc.want.fg:
			t.Errorf("%s: FG = %v, want %v", tc.name, got.FG, tc.want.fg)
		case (got.Border.A != 0) != tc.want.border:
			t.Errorf("%s: Border = %v, want a border: %v", tc.name, got.Border, tc.want.border)
		case (got.Logo != nil) != tc.want.logo:
			t.Errorf("%s: Logo == nil is %v, want %v", tc.name, got.Logo == nil, !tc.want.logo)
		case got.Mono != tc.want.mono:
			t.Errorf("%s: Mono = %v, want %v", tc.name, got.Mono, tc.want.mono)
		case got.Prompt != tc.want.prompt:
			t.Errorf("%s: Prompt = %v, want %v", tc.name, got.Prompt, tc.want.prompt)
		case got.Glyph != tc.want.glyph:
			t.Errorf("%s: Glyph = %q, want %q", tc.name, got.Glyph, tc.want.glyph)
		case (got.Accent.A != 0) != tc.want.accent:
			t.Errorf("%s: Accent = %v, want a dot: %v", tc.name, got.Accent, tc.want.accent)
		}
	}
}

// TestTileOfSession is the agent label over the program, pi and omp reached by
// their program name, and omp's accent that tells it from pi (REQ-WP-032).
func TestTileOfSession(t *testing.T) {
	if got := tileOf(lana.Session{Agent: "claude", Program: "zsh"}); got.BG != ui.Hex("#D97757") {
		t.Errorf("an agent label wins over the program: BG = %v, want #D97757", got.BG)
	}
	if got := tileOf(lana.Session{Program: "codex"}); got.Logo == nil || !got.Mono {
		t.Errorf("codex by its program = %+v, want its mono logo", got)
	}
	pi := tileOf(lana.Session{Program: "pi"})
	omp := tileOf(lana.Session{Program: "omp"})
	if pi.Glyph != "π" || omp.Glyph != "π" {
		t.Errorf("pi/omp glyph = %q / %q, want π", pi.Glyph, omp.Glyph)
	}
	if pi.Accent.A != 0 || omp.Accent.A == 0 || omp.Accent == pi.Accent {
		t.Errorf("pi/omp accents = %v / %v, want no dot then a different one", pi.Accent, omp.Accent)
	}
}

// TestTileForIsStable is the same tile every time for the same unknown name
// (REQ-WP-032).
func TestTileForIsStable(t *testing.T) {
	a, b := tileFor("my-tool"), tileFor("my-tool")
	if a.BG != b.BG || a.Glyph != b.Glyph {
		t.Errorf("my-tool tiles differ: %+v and %+v", a, b)
	}
}

// TestProgramLabel is the program name a header shows.
func TestProgramLabel(t *testing.T) {
	cases := map[string]string{
		"":             "Shell",
		"zsh":          "Zsh",
		"/usr/bin/vim": "Vim",
		"claude":       "Claude",
	}
	for program, want := range cases {
		if got := programLabel(lana.Session{Program: program}); got != want {
			t.Errorf("programLabel(%q) = %q, want %q", program, got, want)
		}
	}
}

// TestPaneTitlePrefersTheSessionTitle is what a pane's header shows.
func TestPaneTitlePrefersTheSessionTitle(t *testing.T) {
	if got := paneTitle(lana.Session{Title: "api", Program: "claude"}); got != "api" {
		t.Errorf("paneTitle = %q, want api", got)
	}
	if got := paneTitle(lana.Session{Program: "claude"}); got != "Claude Code" {
		t.Errorf("paneTitle = %q, want Claude Code", got)
	}
}

// TestSessionTitle is the title a tab, an Agents row and the palette show:
// the terminal's own title with its leading glyph dropped, else the agent,
// the program, or the command's base name (REQ-DW-034).
func TestSessionTitle(t *testing.T) {
	cases := []struct {
		name string
		s    lana.Session
		want string
	}{
		{"glyph prefix", lana.Session{Title: "✳ Fix the login test", Program: "claude"}, "Fix the login test"},
		{"spinner", lana.Session{Title: "⠋ Building", Program: "claude"}, "Building"},
		{"plain", lana.Session{Title: "api", Program: "claude"}, "api"},
		{"glyph only falls to the agent", lana.Session{Title: "✳", Agent: "codex"}, "Codex"},
		{"no title falls to the program", lana.Session{Program: "my-tool"}, "my-tool"},
		{"no title falls to the command", lana.Session{Command: []string{"/usr/bin/vim", "-u", "x"}}, "vim"},
		{"nothing at all", lana.Session{}, "Terminal"},
	}
	for _, tc := range cases {
		if got := sessionTitle(tc.s); got != tc.want {
			t.Errorf("%s: sessionTitle = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestAgeFormat is the "<1m", "4m", "2h", "3d" of the Agents panel.
func TestAgeFormat(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "<1m"},
		{4 * time.Minute, "4m"},
		{59 * time.Minute, "59m"},
		{2 * time.Hour, "2h"},
		{25 * time.Hour, "1d"},
		{72 * time.Hour, "3d"},
	}
	for _, tc := range cases {
		if got := ageFormat(now, now.Add(-tc.ago)); got != tc.want {
			t.Errorf("ageFormat(%v ago) = %q, want %q", tc.ago, got, tc.want)
		}
	}
	// A session the daemon gave no time keeps the shortest word.
	if got := ageFormat(now, time.Time{}); got != "<1m" {
		t.Errorf("ageFormat(zero) = %q, want <1m", got)
	}
}

// TestHostChipDetail is the muted half of the host chip.
func TestHostChipDetail(t *testing.T) {
	cases := []struct {
		chip   string
		memory int
		want   string
	}{
		{"M4 Pro", 48, "M4 Pro · 48 GB"},
		{"M4 Pro", 0, "M4 Pro"},
		{"", 16, "16 GB"},
		{"", 0, ""},
	}
	for _, tc := range cases {
		if got := hostDetail(tc.chip, tc.memory); got != tc.want {
			t.Errorf("hostDetail(%q, %d) = %q, want %q", tc.chip, tc.memory, got, tc.want)
		}
	}
}

// TestShortenChip is the processor as the chip shows it.
func TestShortenChip(t *testing.T) {
	cases := map[string]string{
		"Apple M4 Pro": "M4 Pro",
		"Apple M2":     "M2",
		"Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz": "Core i7-9750H",
		"": "",
	}
	for brand, want := range cases {
		if got := shortenChip(brand); got != want {
			t.Errorf("shortenChip(%q) = %q, want %q", brand, got, want)
		}
	}
}

// TestDisplayCwd shows the home folder as "~".
func TestDisplayCwd(t *testing.T) {
	home := "/Users/ada"
	cases := map[string]string{
		"/Users/ada":           "~",
		"/Users/ada/code/lana": "~/code/lana",
		"/Users/ada2/x":        "/Users/ada2/x",
		"/etc":                 "/etc",
		"":                     "",
	}
	for path, want := range cases {
		if got := displayCwd(path, home); got != want {
			t.Errorf("displayCwd(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestStatusTextAndDot is what a pane's status says.
func TestStatusTextAndDot(t *testing.T) {
	cases := []struct {
		session lana.Session
		done    bool
		want    string
	}{
		{lana.Session{State: "working"}, false, "printing"},
		{lana.Session{NeedsYou: true}, false, "needs you"},
		{lana.Session{State: "exited", ExitCode: 3}, false, "exited 3"},
		{lana.Session{State: "idle"}, false, "idle"},
		// A done agent shows "done" in place of "idle".
		{lana.Session{State: "idle"}, true, "done"},
		// A session that is not idle never shows as done.
		{lana.Session{State: "working"}, true, "printing"},
	}
	for _, tc := range cases {
		if got := statusText(tc.session, tc.done); got != tc.want {
			t.Errorf("statusText(%+v, %v) = %q, want %q", tc.session, tc.done, got, tc.want)
		}
	}
}

// TestTabLabel is a tab's name: its own title, else the focused session's
// title, with its leading glyph dropped (REQ-DW-034).
func TestTabLabel(t *testing.T) {
	sessions := map[string]lana.Session{
		"s1": {ID: "s1", Title: "✳ api", Program: "claude"},
	}
	if got := tabLabel(layout.Tab{ID: "t", Focus: "s1"}, sessions); got != "api" {
		t.Errorf("tabLabel = %q, want api", got)
	}
	if got := tabLabel(layout.Tab{ID: "t", Title: "web", Focus: "s1"}, sessions); got != "web" {
		t.Errorf("tabLabel with a title = %q, want web", got)
	}
	// A tab with no focus takes its first pane's session.
	tab := layout.Tab{ID: "t", Root: layout.Node{Type: "pane", Session: "s1"}}
	if got := tabLabel(tab, sessions); got != "api" {
		t.Errorf("tabLabel without a focus = %q, want api", got)
	}
	if got := tabLabel(layout.Tab{ID: "t", Focus: "missing"}, sessions); got != "Terminal" {
		t.Errorf("tabLabel without a session = %q, want Terminal", got)
	}
}

// TestConnectReason names why the window is not connected (REQ-DW-013).
func TestConnectReason(t *testing.T) {
	home := "/Users/ada/.lana"
	cases := []struct {
		err  error
		addr string
		want string
	}{
		{lana.ErrNoSecret, "", "no daemon.secret in /Users/ada/.lana"},
		{lana.ErrUnreachable, "127.0.0.1:18300", "daemon not running at 127.0.0.1:18300"},
		{lana.ErrUnreachable, "", "daemon not running"},
		{&lana.APIError{Status: 503, Code: "workbench_unavailable", Message: "no pty"}, "", "Workbench core unavailable: no pty (HTTP 503 workbench_unavailable)"},
	}
	for _, tc := range cases {
		if got := connectReason(tc.err, home, tc.addr); got != tc.want {
			t.Errorf("connectReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
	// A wrapped unreachable error still matches.
	wrapped := errors.Join(errors.New("dial"), lana.ErrUnreachable)
	if got := connectReason(wrapped, home, ""); got != "daemon not running" {
		t.Errorf("connectReason(wrapped) = %q", got)
	}
}
