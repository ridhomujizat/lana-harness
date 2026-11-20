package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// tile is a program's mark everywhere the Workbench shows it (REQ-WP-030,
// REQ-WP-033): the landscape tile of a tab, a pane header, an Agents row, the
// space and tab switcher and the palette. Every tile has a background; its
// mark is a logo, the shell prompt icon, a text glyph (pi, a monogram) or
// nothing.
type tile struct {
	// BG is the tile's background colour.
	BG ui.Color
	// FG is the mark's colour.
	FG ui.Color
	// Border is a 1 px outline, the zero colour for none.
	Border ui.Color
	// Logo is a parsed logo, nil for a prompt, a glyph or a plain tile.
	Logo *ui.SVG
	// Mono is a monochrome logo, drawn in FG rather than its own colours.
	Mono bool
	// Prompt is the shell ">_" prompt icon, drawn in FG.
	Prompt bool
	// Glyph is a text glyph or a monogram, "" for a logo or a prompt.
	Glyph string
	// Accent is a small dot at the tile's bottom right, the zero colour for
	// none. It tells two otherwise identical tiles apart (pi and omp).
	Accent ui.Color
}

// lanaAgentColors are the avatar colours of the Lana agents, hashed from the
// agent's name as the web Workbench hashes them.
var lanaAgentColors = []string{"#B21FB8", "#3B6FD8", "#7652C2", "#2F9E5B", "#D9824B", "#3E9C8F"}

// commandBase is a program or command as it shows on a tile or a header: the
// base name, without a leading dash.
func commandBase(program string) string {
	trimmed := strings.TrimSpace(program)
	if trimmed == "" {
		return ""
	}
	return strings.TrimPrefix(filepath.Base(trimmed), "-")
}

// programAgent is a program's agent label when it is one of the programs the
// Workbench treats as a coding agent (REQ-DW-030).
func programAgent(program string) string {
	name := strings.ToLower(commandBase(program))
	if name == "" {
		return ""
	}
	for _, p := range layout.AgentPrograms {
		if name == p {
			return name
		}
	}
	return ""
}

// agentKey is the coding agent a session runs: the agent the core detected,
// else its program when that is an agent program.
func agentKey(s lana.Session) string {
	if a := strings.ToLower(strings.TrimSpace(s.Agent)); a != "" {
		return a
	}
	return programAgent(s.Program)
}

// agentNames are the coding agents' product names, as the web Workbench's
// AGENT_NAMES has them.
var agentNames = map[string]string{
	"claude":     "Claude Code",
	"codex":      "Codex",
	"gemini":     "Gemini CLI",
	"cursor":     "Cursor",
	"devin":      "Devin",
	"agy":        "Antigravity",
	"cline":      "Cline",
	"mastracode": "Mastra Code",
	"opencode":   "OpenCode",
	"copilot":    "Copilot",
	"kimi":       "Kimi",
	"kiro":       "Kiro",
	"amp":        "Amp",
	"grok":       "Grok",
	"hermes":     "Hermes",
	"kilo":       "Kilo Code",
	"qodercli":   "Qoder",
	"qwen":       "Qwen Code",
	"pi":         "Pi",
	"omp":        "Oh My Pi",
	"droid":      "Droid",
	"letta":      "Letta",
	"maki":       "Maki",
	"muse":       "Muse",
}

// agentName is a coding agent's product name ("Claude Code" for claude),
// else the label itself.
func agentName(label string) string {
	if name, ok := agentNames[strings.ToLower(strings.TrimSpace(label))]; ok {
		return name
	}
	return label
}

// tileOf is the tile of a session: the agent the core detected, else its
// program (REQ-WP-032).
func tileOf(s lana.Session) tile {
	if a := strings.ToLower(strings.TrimSpace(s.Agent)); a != "" {
		return tileFor(a)
	}
	return tileFor(s.Program)
}

// tileFor is the tile of a program's name: its row in the table, else a
// shell's, else a monogram on a colour hashed from the name, the same every
// time (REQ-WP-032).
func tileFor(program string) tile {
	name := strings.ToLower(commandBase(program))
	if spec, ok := programTiles[name]; ok {
		return spec.tile(name)
	}
	if name == "" || shells[name] {
		return shellTile.tile(name)
	}
	return monogramTile(name)
}

// monogramTile is the tile of a program without a logo: the first letter of
// its name, white, on a colour hashed from the name.
func monogramTile(name string) tile {
	return tile{BG: hashColor(name), FG: ui.Hex("#FFFFFF"), Glyph: monogram(name)}
}

// monogram is the first letter a program without a logo shows.
func monogram(name string) string {
	r := []rune(strings.TrimSpace(name))
	if len(r) == 0 {
		return "?"
	}
	return strings.ToUpper(string(r[0]))
}

// hashColor picks a Lana agent's avatar colour, the same every time, as the
// web Workbench does.
func hashColor(s string) ui.Color {
	h := uint32(0)
	for i := 0; i < len(s); i++ {
		h = h*31 + uint32(s[i])
	}
	return ui.Hex(lanaAgentColors[h%uint32(len(lanaAgentColors))])
}

// programName is what runs in a session, as a header and the palette show it:
// the agent's product name for a coding agent ("Claude Code"), else the
// program ("zsh"), else the command's base name (the web Workbench's
// programName).
func programName(s lana.Session) string {
	if key := agentKey(s); key != "" {
		return agentName(key)
	}
	if name := commandBase(s.Program); name != "" {
		return name
	}
	if len(s.Command) > 0 {
		if name := commandBase(s.Command[0]); name != "" {
			return name
		}
	}
	return "Terminal"
}

// sessionTitle is a session's title as a tab, the Agents panel and the
// palette show it: the terminal's own title with its leading glyph and any
// space around it dropped (Claude Code sets titles like "✳ Fix the login
// test"), else the agent's name, the program, or the command's base name. A
// title that is not valid UTF-8, or carries U+FFFD, names nothing and the
// fallbacks stand in (the web Workbench's sessionTitle).
func sessionTitle(s lana.Session) string {
	if usableTitle(s.Title) {
		if title := leadingGlyphStripped(s.Title); title != "" {
			return title
		}
	}
	if key := agentKey(s); key != "" {
		return agentName(key)
	}
	if name := commandBase(s.Program); name != "" {
		return name
	}
	if len(s.Command) > 0 {
		if name := commandBase(s.Command[0]); name != "" {
			return name
		}
	}
	return "Terminal"
}

// leadingGlyphStripped drops the leading run of non-letter, non-digit glyphs
// a terminal title carries (a spinner or a mark) and the space around what is
// left.
func leadingGlyphStripped(title string) string {
	i := 0
	for i < len(title) {
		r, size := utf8.DecodeRuneInString(title[i:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			break
		}
		i += size
	}
	return strings.TrimSpace(title[i:])
}

// paneTitle is a session's title as a pane's header and the Agents panel show
// it (REQ-DW-031): its sessionTitle.
func paneTitle(s lana.Session) string { return sessionTitle(s) }

// usableTitle reports whether a title can be shown: it is valid UTF-8 and
// carries no replacement character.
func usableTitle(t string) bool {
	return utf8.ValidString(t) && !strings.ContainsRune(t, utf8.RuneError)
}

// programLabel is a program's name as a header shows it, "Shell" for a
// session that runs none the core knows.
func programLabel(s lana.Session) string {
	name := commandBase(s.Program)
	if name == "" {
		return "Shell"
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// tabLabel is a tab's name: its own title (REQ-DW-034), else the focused
// session's title, else its first pane's.
func tabLabel(t layout.Tab, sessions map[string]lana.Session) string {
	if title := strings.TrimSpace(t.Title); title != "" {
		return title
	}
	focus := t.Focus
	if focus == "" {
		if ids := layout.TabSessions(t); len(ids) > 0 {
			focus = ids[0]
		}
	}
	return sessionTitle(sessions[focus])
}

// sessionCwd is where a session runs: its live folder, else the one it
// started in.
func sessionCwd(s lana.Session) string {
	if s.Cwd != "" {
		return s.Cwd
	}
	return s.Dir
}

// displayCwd shortens a path for a header, showing the home folder as "~".
func displayCwd(path, home string) string {
	if path == "" {
		return ""
	}
	if home != "" {
		if path == home {
			return "~"
		}
		if strings.HasPrefix(path, home+"/") {
			return "~" + strings.TrimPrefix(path, home)
		}
	}
	return path
}

// ageFormat is how long ago t was, as the Agents panel shows it: "<1m",
// "4m", "2h", "3d".
func ageFormat(now, t time.Time) string {
	if t.IsZero() {
		return "<1m"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// hostDetail is the muted half of the host chip: the processor and the
// memory, either one alone when the system tells only one.
func hostDetail(chip string, memoryGB int) string {
	switch {
	case chip != "" && memoryGB > 0:
		return fmt.Sprintf("%s · %d GB", chip, memoryGB)
	case chip != "":
		return chip
	case memoryGB > 0:
		return fmt.Sprintf("%d GB", memoryGB)
	}
	return ""
}

// shortenChip is a processor name as the host chip shows it: "Apple M4
// Pro" becomes "M4 Pro".
func shortenChip(brand string) string {
	b := strings.TrimSpace(brand)
	for _, prefix := range []string{"Apple ", "Intel(R) ", "Intel ", "AMD "} {
		b = strings.TrimPrefix(b, prefix)
	}
	if i := strings.Index(b, " CPU @"); i >= 0 {
		b = b[:i]
	}
	b = strings.ReplaceAll(b, "(TM)", "")
	b = strings.ReplaceAll(b, "(R)", "")
	return strings.Join(strings.Fields(b), " ")
}

// homeDir is the user's home directory, "" when the system does not tell.
func homeDir() string {
	d, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return d
}

// layoutSessions is the little of each session the layout helpers need.
func layoutSessions(sessions map[string]lana.Session) map[string]layout.Session {
	out := make(map[string]layout.Session, len(sessions))
	for id, s := range sessions {
		out[id] = layout.Session{
			Program:  s.Program,
			Labels:   s.Labels,
			State:    s.State,
			NeedsYou: s.NeedsYou,
			ExitCode: s.ExitCode,
			Cwd:      s.Cwd,
			Dir:      s.Dir,
			Agent:    s.Agent,
		}
	}
	return out
}

// layoutSession is one session as the layout helpers see it.
func layoutSession(s lana.Session) layout.Session {
	return layout.Session{
		Program:  s.Program,
		Labels:   s.Labels,
		State:    s.State,
		NeedsYou: s.NeedsYou,
		ExitCode: s.ExitCode,
		Cwd:      s.Cwd,
		Dir:      s.Dir,
		Agent:    s.Agent,
	}
}

// isAgent reports whether a session is a coding agent (REQ-DW-030).
func isAgent(s lana.Session) bool { return layout.IsAgent(layoutSession(s)) }

// statusOf is what a session is doing, as its dot shows it.
func statusOf(s lana.Session) layout.Status {
	ls := layoutSession(s)
	return layout.StatusOf(&ls)
}

// statusText is a pane's status word (spec 58, window design). An agent
// that finished while the user was not looking shows "done" in place of
// "idle" (spec 58, done status).
func statusText(s lana.Session, done bool) string {
	switch displayStatus(statusOf(s), done) {
	case layout.StatusWorking:
		return "printing"
	case layout.StatusBlocked:
		return "needs you"
	case layout.StatusExited:
		return fmt.Sprintf("exited %d", s.ExitCode)
	case statusDone:
		return "done"
	}
	return "idle"
}

// agentLabel is the Lana agent name a session carries, "" when none.
func agentLabel(s lana.Session) string {
	if s.Labels == nil {
		return ""
	}
	return strings.TrimSpace(s.Labels["lana.agent"])
}
