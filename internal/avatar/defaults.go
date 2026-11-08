package avatar

import (
	"hash/fnv"
	"strings"
)

// Avatar sources of an agent row (spec 43, API-AVT-005).
const (
	// Saved is an avatar the owner saved to <agent home>/avatar.svg.
	Saved = "saved"
	// Default is the local avatar seeded from the agent name, shown until
	// the owner saves one.
	Default = "default"
)

// maxSeed keeps seeds in a range every JSON client reads exactly.
const maxSeed = 1<<31 - 1

// SeedForName is the stable seed of a name's default avatar: the same name
// draws the same avatar on every machine (REQ-AVT-008).
func SeedForName(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64()%maxSeed) + 1
}

// DefaultFor is the default avatar of a name.
func DefaultFor(name string) string {
	return Generate(SeedForName(name))
}

// themeColors are the hex values of the palette tokens in the dashboard
// themes (web/ui gr8r tokens.css), for an avatar with its color baked in.
var themeColors = map[string]map[string]string{
	"light": {"brand": "#7652c2", "success": "#25754b", "warning": "#8f5e0e", "error": "#b23c30", "blue": "#3570c4", "gray": "#8a867e"},
	"dark":  {"brand": "#a98deb", "success": "#5cba86", "warning": "#d8a443", "error": "#e57d70", "blue": "#72a5ea", "gray": "#8f8b84"},
}

// ColorHex resolves an agent color to a hex value for theme ("dark" or
// anything else for light): a "#RRGGBB" color as it is (lower case), a
// palette token by the theme, anything else as the brand color.
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

// Colorize bakes a body color into a canonical avatar: every
// fill="currentColor" becomes fill="<hex>". hex must be "#RRGGBB".
func Colorize(svg, hex string) string {
	return strings.ReplaceAll(svg, `fill="`+fillBody+`"`, `fill="`+hex+`"`)
}
