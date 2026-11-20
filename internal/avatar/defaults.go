package avatar

import (
	"hash/fnv"
	"strings"
)

// Avatar sources of an agent row (API-PXA-005).
const (
	// Saved is an avatar the owner saved to <agent home>/avatar.json.
	Saved = "saved"
	// Default is the local avatar seeded from the agent name, shown until
	// the owner saves one.
	Default = "default"
)

// maxSeed keeps seeds in a range every JSON client reads exactly.
const maxSeed = 1<<31 - 1

// SeedForName is the stable seed of a name's default avatar: the same name
// gives the same avatar on every machine (REQ-PXA-002).
func SeedForName(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64()%maxSeed) + 1
}

// DefaultFor is the default avatar of a name: RandomParts(SeedForName(name))
// rendered, with Color set to color when color != "" (REQ-PXA-002).
func DefaultFor(name, color string) Document {
	p := RandomParts(SeedForName(name))
	if color != "" {
		p.Color = color
	}
	sprite, _ := Render(p)
	return Document{Kind: KindParts, Parts: &p, Sprite: sprite}
}

// themeColors are the hex values of the palette tokens in the dashboard
// themes (web/ui gr8r tokens.css), for an avatar with its colour baked in.
var themeColors = map[string]map[string]string{
	"light": {"brand": "#7652c2", "success": "#25754b", "warning": "#8f5e0e", "error": "#b23c30", "blue": "#3570c4", "gray": "#8a867e"},
	"dark":  {"brand": "#a98deb", "success": "#5cba86", "warning": "#d8a443", "error": "#e57d70", "blue": "#72a5ea", "gray": "#8f8b84"},
}

// ColorHex resolves an agent colour to a hex value for theme ("dark" or
// anything else for light): a "#RRGGBB" colour as it is (lower case), a
// palette token by the theme, anything else as the brand colour.
func ColorHex(color, theme string) string {
	if len(color) == 7 && color[0] == '#' {
		return strings.ToLower(color)
	}
	palette := themeColors["light"]
	if theme == "dark" {
		palette = themeColors["dark"]
	}
	if hex, ok := palette[color]; ok {
		return hex
	}
	return palette["brand"]
}
