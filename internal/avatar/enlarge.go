package avatar

import (
	"math"
	"strings"
)

// Bounds of the enlargement of a small model drawing (REQ-AVT-002).
const (
	// minBodySide is the body box side a drawing is enlarged up to.
	minBodySide = 88.0
	// maxEnlargeScale caps the enlargement, so a tiny drawing is not
	// blown up out of recognition.
	maxEnlargeScale = 1.25
)

// Enlarge grows the body of a canonical avatar whose larger body box side is
// below minBodySide, so a model drawing fills its frame like a generated one:
// all children of the root <svg> are wrapped in one <g> that scales the body
// about its center up to minBodySide (at most maxEnlargeScale). The result is
// canonical (Sanitize returns it unchanged); when the side is already large
// enough the input is returned as it is.
func Enlarge(svg string) (string, error) {
	if !strings.HasPrefix(svg, svgOpen) || !strings.HasSuffix(svg, "</svg>") {
		return "", invalid("not a canonical avatar")
	}
	box, err := bodyBounds(svg)
	if err != nil {
		return "", err
	}
	side := math.Max(box[2]-box[0], box[3]-box[1])
	if side >= minBodySide {
		return svg, nil
	}
	scale := math.Round(math.Min(minBodySide/side, maxEnlargeScale)*100) / 100
	cx, cy := (box[0]+box[2])/2, (box[1]+box[3])/2
	// Scale about the origin, then move the body center back to its place.
	tx := fitInside(cx*(1-scale), scale*box[0], scale*box[2])
	ty := fitInside(cy*(1-scale), scale*box[1], scale*box[3])
	inner := strings.TrimSuffix(strings.TrimPrefix(svg, svgOpen), "</svg>")
	transform := "translate(" + formatNumber(tx) + " " + formatNumber(ty) +
		") scale(" + formatNumber(scale) + " " + formatNumber(scale) + ")"
	out := svgOpen + `<g transform="` + transform + `">` + inner + "</g></svg>"
	// The extra <g> may break a Sanitize rule, such as the nesting cap; the
	// caller then keeps the original.
	if clean, err := Sanitize(out); err != nil || clean != out {
		return "", invalid("enlarged avatar is not canonical")
	}
	return out, nil
}

// fitInside shifts a translation t so the range [lo+t, hi+t] stays inside
// [1, 99].
func fitInside(t, lo, hi float64) float64 {
	if lo+t < 1 {
		t = 1 - lo
	}
	if hi+t > 99 {
		t = 99 - hi
	}
	return t
}
