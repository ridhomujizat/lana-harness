package app

import (
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/assets"
)

// glyphKind is how a tile draws its mark (REQ-WP-032).
type glyphKind int

const (
	glyphMono   glyphKind = iota // a monochrome logo file, drawn in the tile's FG
	glyphColor                   // a multi-colour logo file, drawn as it is
	glyphPrompt                  // the shell ">_" prompt icon (icons.go), drawn in FG
	glyphText                    // a text glyph such as "π"
)

// tileSpec is one row of the program tile table (REQ-WP-032): the tile's
// background and mark colour, an optional 1 px border, and the mark itself.
// The table is keyed by the agent label the core reports, or by a program's
// base name, and drives every tile in the app: the tab strip, the pane
// headers, the Agents panel, the space and tab switcher and the palette all
// draw from it (REQ-WP-033).
type tileSpec struct {
	BG     ui.Color  // the tile's background
	FG     ui.Color  // the mark's colour
	Border ui.Color  // a 1 px border, the zero colour for none
	Kind   glyphKind // how the mark is drawn
	Logo   string    // the logo file in assets/agents, without folder and extension
	Text   string    // the text of a glyphText mark
	Accent ui.Color  // a small dot at the tile's bottom right, the zero colour for none
}

// shellTile is the tile of a plain shell at its prompt (REQ-WP-032): a dark
// tile with a faint light border and a green ">_" prompt glyph.
var shellTile = tileSpec{
	BG:     ui.Hex("#2E2D2B"),
	FG:     ui.Hex("#5CBA86"),
	Border: ui.RGBA(255, 255, 255, 0.08),
	Kind:   glyphPrompt,
}

// programTiles is the program tile table the two clients share (REQ-WP-032).
// A name that is not here is a plain shell or, failing that, a monogram.
var programTiles = map[string]tileSpec{
	// Claude Code: its white sparkle on a coral tile.
	"claude": {BG: ui.Hex("#D97757"), FG: ui.Hex("#FFFFFF"), Kind: glyphMono, Logo: "claude"},
	// Codex: a dark mark on a near-white tile with a thin dark border.
	"codex": {BG: ui.Hex("#F7F7F7"), FG: ui.Hex("#1A1A1A"), Border: ui.RGBA(26, 26, 26, 0.16), Kind: glyphMono, Logo: "codex"},
	// Agents with a white mono mark on the near-black tile.
	"cursor":     {BG: ui.Hex("#1A1A1A"), FG: ui.Hex("#FFFFFF"), Kind: glyphMono, Logo: "cursor"},
	"grok":       {BG: ui.Hex("#1A1A1A"), FG: ui.Hex("#FFFFFF"), Kind: glyphMono, Logo: "grok"},
	"opencode":   {BG: ui.Hex("#1A1A1A"), FG: ui.Hex("#FFFFFF"), Kind: glyphMono, Logo: "opencode"},
	"cline":      {BG: ui.Hex("#1A1A1A"), FG: ui.Hex("#FFFFFF"), Kind: glyphMono, Logo: "cline"},
	"mastracode": {BG: ui.Hex("#1A1A1A"), FG: ui.Hex("#FFFFFF"), Kind: glyphMono, Logo: "mastra"},
	"hermes":     {BG: ui.Hex("#1A1A1A"), FG: ui.Hex("#FFFFFF"), Kind: glyphMono, Logo: "hermesagent"},
	// Copilot: a white mark on GitHub's dark grey.
	"copilot": {BG: ui.Hex("#24292F"), FG: ui.Hex("#FFFFFF"), Kind: glyphMono, Logo: "githubcopilot"},
	// Multi-colour logos on the neutral dark tile. kilo's logo is a mono one
	// in the LobeHub set, so it draws in FG on the same neutral tile.
	"gemini":   {BG: ui.Hex("#2E2D2B"), FG: ui.Hex("#FFFFFF"), Kind: glyphColor, Logo: "geminicli-color"},
	"agy":      {BG: ui.Hex("#2E2D2B"), FG: ui.Hex("#FFFFFF"), Kind: glyphColor, Logo: "antigravity-color"},
	"devin":    {BG: ui.Hex("#2E2D2B"), FG: ui.Hex("#FFFFFF"), Kind: glyphColor, Logo: "devin-color"},
	"kimi":     {BG: ui.Hex("#2E2D2B"), FG: ui.Hex("#FFFFFF"), Kind: glyphColor, Logo: "kimi-color"},
	"kiro":     {BG: ui.Hex("#2E2D2B"), FG: ui.Hex("#FFFFFF"), Kind: glyphColor, Logo: "kiro-color"},
	"amp":      {BG: ui.Hex("#2E2D2B"), FG: ui.Hex("#FFFFFF"), Kind: glyphColor, Logo: "amp-color"},
	"qwen":     {BG: ui.Hex("#2E2D2B"), FG: ui.Hex("#FFFFFF"), Kind: glyphColor, Logo: "qwen-color"},
	"qodercli": {BG: ui.Hex("#2E2D2B"), FG: ui.Hex("#FFFFFF"), Kind: glyphColor, Logo: "qoder-color"},
	"muse":     {BG: ui.Hex("#2E2D2B"), FG: ui.Hex("#FFFFFF"), Kind: glyphColor, Logo: "muse-color"},
	"kilo":     {BG: ui.Hex("#2E2D2B"), FG: ui.Hex("#FFFFFF"), Kind: glyphMono, Logo: "kilocode"},
	// pi and omp share a near-black tile with a white "π"; omp carries a
	// small purple accent so the two tell apart.
	"pi":  {BG: ui.Hex("#1A1A1A"), FG: ui.Hex("#FFFFFF"), Kind: glyphText, Text: "π"},
	"omp": {BG: ui.Hex("#1A1A1A"), FG: ui.Hex("#FFFFFF"), Kind: glyphText, Text: "π", Accent: ui.Hex("#7652C2")},
}

// shells are the programs the app treats as a plain shell at its prompt, so
// they all share the shell tile (REQ-WP-032).
var shells = map[string]bool{
	"zsh": true, "bash": true, "fish": true, "sh": true, "dash": true, "nu": true,
	"pwsh": true, "elvish": true, "xonsh": true, "tcsh": true, "csh": true,
	"ksh": true, "cmd": true, "powershell": true,
}

// tile builds the tile of a row for a program of the given name. A logo file
// that is missing or does not parse falls back to the program's monogram, so
// its tile still shows something.
func (s tileSpec) tile(name string) tile {
	t := tile{BG: s.BG, FG: s.FG, Border: s.Border, Accent: s.Accent, Glyph: s.Text}
	switch s.Kind {
	case glyphMono, glyphColor:
		logo := agentLogos()[s.Logo]
		if logo == nil {
			return monogramTile(name)
		}
		t.Logo, t.Mono = logo, s.Kind == glyphMono
	case glyphPrompt:
		t.Prompt = true
	}
	return t
}

var (
	agentLogosOnce sync.Once
	agentLogoSet   map[string]*ui.SVG
)

// agentLogos parses the embedded logos once, by file name. The files are
// embedded from assets/agents (the LobeHub icon set, see its README). A file
// that does not parse is left out, so its tile shows a monogram rather than
// nothing.
func agentLogos() map[string]*ui.SVG {
	agentLogosOnce.Do(func() {
		agentLogoSet = make(map[string]*ui.SVG, len(programTiles))
		for _, spec := range programTiles {
			if spec.Logo == "" {
				continue
			}
			if _, ok := agentLogoSet[spec.Logo]; ok {
				continue
			}
			data, err := assets.Agents.ReadFile("agents/" + spec.Logo + ".svg")
			if err != nil {
				continue
			}
			if svg, err := ui.ParseSVG(data); err == nil {
				agentLogoSet[spec.Logo] = svg
			}
		}
	})
	return agentLogoSet
}
