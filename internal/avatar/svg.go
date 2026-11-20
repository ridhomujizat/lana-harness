package avatar

import (
	"fmt"
	"sort"
	"strings"
)

// svgNS is the SVG namespace of a rendered sprite.
const svgNS = "http://www.w3.org/2000/svg"

// SVG draws frame 0 of a sprite as SVG (REQ-PXA-013): the body layer, the head
// layer over it, then the computed 4-neighbour outline, as run-length rects
// per row and colour with crisp edges. view "full" adds the ground bar and the
// whole-sprite viewBox; any other view is the bust.
func SVG(s Sprite, view string) string {
	filled := make(map[[2]int]bool)
	cells := make(map[[2]int]string)
	add := func(rows []string) {
		for y, row := range rows {
			for x := 0; x < len(row); x++ {
				if c := row[x]; c != '.' {
					filled[[2]int{x, y}] = true
					cells[[2]int{x, y}] = s.Palette[string(c)]
				}
			}
		}
	}
	add(s.Body)
	add(s.Head)

	// Outline: an empty cell in -1..16 × -1..18 next to a filled one.
	for y := -1; y <= 18; y++ {
		for x := -1; x <= 16; x++ {
			if filled[[2]int{x, y}] {
				continue
			}
			if filled[[2]int{x + 1, y}] || filled[[2]int{x - 1, y}] ||
				filled[[2]int{x, y + 1}] || filled[[2]int{x, y - 1}] {
				cells[[2]int{x, y}] = out
			}
		}
	}

	viewBox, ground := "0 -1 16 16", ""
	if view == "full" {
		viewBox = "-0.5 -1.5 17 21"
		ground = `<rect x="2" y="19" width="12" height="1" fill="` + out + `" fill-opacity="0.14"/>`
	}
	return `<svg xmlns="` + svgNS + `" viewBox="` + viewBox + `" shape-rendering="crispEdges">` +
		ground + rects(cells) + `</svg>`
}

// rects writes the cells as run-length rects, one row at a time, top to
// bottom and left to right.
func rects(cells map[[2]int]string) string {
	keys := make([][2]int, 0, len(cells))
	for k := range cells {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][1] != keys[j][1] {
			return keys[i][1] < keys[j][1]
		}
		return keys[i][0] < keys[j][0]
	})
	var b strings.Builder
	for i := 0; i < len(keys); {
		x, y := keys[i][0], keys[i][1]
		color := cells[keys[i]]
		n := 1
		for i+n < len(keys) && keys[i+n][1] == y && keys[i+n][0] == x+n && cells[keys[i+n]] == color {
			n++
		}
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="1" fill="%s"/>`, x, y, n, color)
		i += n
	}
	return b.String()
}
