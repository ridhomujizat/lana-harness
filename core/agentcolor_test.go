package core

import "testing"

// TestResolveAgentColor covers API-ROOM-006: an explicit color wins, otherwise
// the color is palette[fnv32a(name) % 6] and is stable for a name.
func TestResolveAgentColor(t *testing.T) {
	if got := ResolveAgentColor("dana", "success"); got != "success" {
		t.Fatalf("explicit color = %q, want success", got)
	}
	golden := map[string]string{
		"":         "success",
		"dana":     "gray",
		"coder":    "brand",
		"lana":     "success",
		"karin":    "brand",
		"default":  "brand",
		"reviewer": "blue",
	}
	palette := map[string]bool{}
	for _, color := range agentColorPalette {
		palette[color] = true
	}
	for name, want := range golden {
		got := ResolveAgentColor(name, "")
		if got != want {
			t.Fatalf("ResolveAgentColor(%q) = %q, want %q", name, got, want)
		}
		if !palette[got] {
			t.Fatalf("ResolveAgentColor(%q) = %q, not in the palette", name, got)
		}
		if again := ResolveAgentColor(name, ""); again != got {
			t.Fatalf("ResolveAgentColor(%q) is not stable: %q then %q", name, got, again)
		}
	}
}
