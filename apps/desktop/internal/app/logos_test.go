package app

import (
	"io/fs"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/assets"
)

// TestProgramTileLogos is the tile table's logos (REQ-WP-032): every agent
// whose tile draws a logo maps to one, a monochrome file being flagged so it
// is drawn in the tile's mark colour, and an agent the table does not know
// shows a monogram instead.
func TestProgramTileLogos(t *testing.T) {
	cases := []struct {
		agent string
		mono  bool
	}{
		{"claude", true},
		{"codex", true},
		{"gemini", false},
		{"devin", false},
		{"agy", false},
		{"kimi", false},
		{"kiro", false},
		{"amp", false},
		{"qodercli", false},
		{"qwen", false},
		{"muse", false},
		{"cursor", true},
		{"cline", true},
		{"opencode", true},
		{"copilot", true},
		{"grok", true},
		{"hermes", true},
		{"kilo", true},
		{"mastracode", true},
	}
	for _, tc := range cases {
		got := tileFor(tc.agent)
		if got.Logo == nil {
			t.Errorf("tileFor(%q) has no logo", tc.agent)
			continue
		}
		if got.Mono != tc.mono {
			t.Errorf("tileFor(%q) mono = %v, want %v", tc.agent, got.Mono, tc.mono)
		}
	}
	// An agent the table does not know draws a monogram, not a logo.
	for _, unknown := range []string{"nope", "droid", "letta", "maki", "", "  "} {
		if got := tileFor(unknown); got.Logo != nil {
			t.Errorf("tileFor(%q) found a logo", unknown)
		}
	}
	// The program's own spelling and case do not matter.
	if tileFor("Claude").Logo == nil {
		t.Error("tileFor(Claude) found no logo")
	}
}

// TestEmbeddedLogosParse is REQ-WP-032: every embedded agent logo parses, and
// every logo the table names has an SVG the app can draw.
func TestEmbeddedLogosParse(t *testing.T) {
	files, err := fs.Glob(assets.Agents, "agents/*.svg")
	if err != nil {
		t.Fatalf("listing the embedded logos: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no embedded agent logos")
	}
	for _, name := range files {
		data, err := assets.Agents.ReadFile(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if _, err := ui.ParseSVG(data); err != nil {
			t.Errorf("%s does not parse: %v", name, err)
		}
	}
	seen := map[string]bool{}
	for _, spec := range programTiles {
		if spec.Logo == "" || seen[spec.Logo] {
			continue
		}
		seen[spec.Logo] = true
		if agentLogos()[spec.Logo] == nil {
			t.Errorf("logo %q has no parsed SVG", spec.Logo)
		}
	}
}
