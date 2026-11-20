package avatar

import (
	"fmt"
	"math"
	"strconv"
)

// out is the outline and dark-detail colour of a sprite (REQ-PXA-001).
const out = "#1b1820"

// Fixed palette colours of the sprite keys (REQ-PXA-001).
const (
	colWhite  = "#f3f1ec" // w, jacket and tie highlights
	colWhite2 = "#dcd8cf" // W, jacket shade
	colTie    = "#c43d4b" // r
	colShoe   = "#2a2630" // o
	colBand   = "#9aa0ad" // g, headphone band
	colFrame  = "#2b2d3a" // f, glasses frame
	colShine  = "#6d7385" // n, sunglasses shine
	colBolt   = "#8b93a3" // B, robot bolts and antenna
	colBlush  = "#f2919a" // blush blend target
	colMouth  = "#9b4f48" // m
	colGlow   = "#73f0ff" // robot eyes
)

// skins are the skin tones, base then shade (REQ-PXA-001).
var skins = map[string][2]string{
	"porcelain": {"#f8d9c4", "#e2b096"},
	"light":     {"#f1c3a2", "#d69a78"},
	"warm":      {"#dba279", "#bd8059"},
	"tan":       {"#b97b52", "#99613e"},
	"brown":     {"#8a5636", "#6c4027"},
	"deep":      {"#5e3a26", "#47291a"},
}

// hairColors are the hair colours, base then shade (REQ-PXA-001).
var hairColors = map[string][2]string{
	"black":  {"#2e2a33", "#1f1c24"},
	"dark":   {"#4d3a33", "#3a2b26"},
	"brown":  {"#8a5a3a", "#6b4429"},
	"blonde": {"#ecc66e", "#c9a24c"},
	"ginger": {"#e3683f", "#b94d2d"},
	"silver": {"#e4e1d8", "#bdb8aa"},
	"pink":   {"#f38fbd", "#d06c9a"},
	"blue":   {"#5f80e6", "#4864c0"},
	"lilac":  {"#ab90e8", "#8a6cc8"},
	"mint":   {"#6fd6b4", "#4fb393"},
}

// pantsColors are the pant colours (REQ-PXA-001).
var pantsColors = map[string]string{
	"denim":    "#3f5d8f",
	"charcoal": "#34363f",
	"khaki":    "#b39a6a",
	"black":    "#27242c",
	"olive":    "#5d6b45",
}

// robotMetal is the metal base and shade of a robot sprite.
var robotMetal = [2]string{"#c7ceda", "#9aa3b4"}

// colorPresets are the seed colour picks of RandomParts (prototype COLORS).
var colorPresets = []string{
	"#7c5cff", "#f45bd0", "#ff6b2c", "#3d7bff", "#1fb37a",
	"#ff3d5e", "#13b5c8", "#f5a524", "#8a867e",
}

// hairMasks are the hair masks: a 14×16 window whose column c is pixel
// x = c + 1 and whose row r is y = r. '#' is the hair colour, 'd' its shade,
// and the left three columns are shaded too (REQ-PXA-001).
var hairMasks = map[string][]string{
	"short": {
		"..............",
		".....#...#....",
		"...#########..",
		"..###########.",
		".############.",
		".############.",
		".##d#....####.",
		".##........##.",
		".#............",
	},
	"bob": {
		"..............",
		"..............",
		"...########...",
		"..##########..",
		".############.",
		".############.",
		".#####...####.",
		".##........##.",
		".##........##.",
		".##........##.",
		".##.........#.",
		".#............",
	},
	"spiky": {
		"..#...#...#...",
		"..##.###.###..",
		".###########..",
		".############.",
		"#############.",
		".############.",
		".##.##..#.###.",
		".#.........##.",
		".#............",
	},
	"long": {
		"..............",
		"....######....",
		"...########...",
		"..##########..",
		".############.",
		".############.",
		".####....####.",
		".###.......##.",
		".##........##.",
		".##........##.",
		".##........##.",
		".##........##.",
		"##..........##",
		"##..........##",
		"##..........##",
		".#..........#.",
	},
	"ponytail": {
		"..............",
		"....######....",
		"...#########..",
		".############.",
		"#############.",
		"#############.",
		"##.d#....####.",
		"##.........##.",
		"##............",
		"#.............",
		"#.............",
	},
	"bun": {
		".....####.....",
		"....######....",
		".....####.....",
		"..##########..",
		".############.",
		".############.",
		".##d#....####.",
		".##........#..",
		".#............",
	},
	"curly": {
		"...########...",
		"..##########..",
		".############.",
		"##############",
		"##############",
		"##############",
		"####......####",
		"###........###",
		"###........###",
		"##..........##",
		".#..........#.",
	},
	"buzz": {
		"..............",
		"..............",
		"..............",
		"...########...",
		"..##########..",
		"..#d......##..",
	},
	"mohawk": {
		".....###......",
		".....####.....",
		"....#####.....",
		"...d#####d....",
		"..dd######dd..",
		"..d........d..",
	},
	"swoop": {
		"..............",
		"......#####...",
		"...##########.",
		"..############",
		".############.",
		".############.",
		".##d########..",
		".##.......###.",
		".#.........#..",
	},
}

// accessoryMasks are the accessory masks, in the same 14×16 window. 'a' is
// the accessory colour, 'A' its shade, 'g' the headphone band, 'w' a light
// highlight, and 'k' the outline colour (REQ-PXA-001).
var accessoryMasks = map[string][]string{
	"beanie": {
		"......aa......",
		"....aaaaaa....",
		"...aaaaaaaa...",
		"..aaaaaaaaaa..",
		".aaaaaaaaaaaa.",
		".AAAAAAAAAAAA.",
	},
	"cap": {
		"..............",
		"..............",
		"....aaaaaa....",
		"...aaaaaaaa...",
		"..aaaaaaaaaa..",
		"..aaaaaaaaaAAA",
	},
	"headphones": {
		"..............",
		"....gggggg....",
		"..gg......gg..",
		".g..........g.",
		".g..........g.",
		".g..........g.",
		"aa..........aa",
		"aa..........aa",
		"aa..........aa",
	},
	"hood": {
		"..A........A..",
		"..aA......Aa..",
		"..aaaaaaaaaa..",
		".aaaaaaaaaaaa.",
		".aaaaaaaaaaaa.",
		".aaaaaaaaaaaa.",
		".aA........aa.",
		".aA.........a.",
		".aA.........a.",
		".aA.........a.",
		".aaA.......aa.",
		"..aa......aa..",
	},
}

// mix blends a toward b by t (0..1), rounding each channel (REQ-PXA-001).
func mix(a, b string, t float64) string {
	ar, ag, ab := hexRGB(a)
	br, bg, bb := hexRGB(b)
	return rgbHex(
		float64(ar)+(float64(br)-float64(ar))*t,
		float64(ag)+(float64(bg)-float64(ag))*t,
		float64(ab)+(float64(bb)-float64(ab))*t,
	)
}

// shade mixes h a third of the way toward the outline colour (REQ-PXA-001).
func shade(h string) string { return mix(h, out, 0.3) }

// shadeT is mix(h, out, t).
func shadeT(h string, t float64) string { return mix(h, out, t) }

// hexRGB parses "#rrggbb" into its channels. A malformed value reads as black.
func hexRGB(h string) (int, int, int) {
	if len(h) != 7 || h[0] != '#' {
		return 0, 0, 0
	}
	r, _ := strconv.ParseInt(h[1:3], 16, 32)
	g, _ := strconv.ParseInt(h[3:5], 16, 32)
	b, _ := strconv.ParseInt(h[5:7], 16, 32)
	return int(r), int(g), int(b)
}

// rgbHex formats channels as "#rrggbb" (lower case), rounded and clamped.
func rgbHex(r, g, b float64) string {
	return fmt.Sprintf("#%02x%02x%02x", channel(r), channel(g), channel(b))
}

func channel(v float64) int {
	i := int(math.Round(v))
	if i < 0 {
		return 0
	}
	if i > 255 {
		return 255
	}
	return i
}

// validHex reports whether s is exactly "#rrggbb" in any letter case.
func validHex(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
