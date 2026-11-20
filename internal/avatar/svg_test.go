package avatar

import (
	"strings"
	"testing"
)

// REQ-PXA-013: the bust and full views carry their viewBox, crisp edges, the
// outline colour, and no script.
func TestSVGViews(t *testing.T) {
	sprite := baseSprite(t)
	bust := SVG(sprite, "bust")
	if !strings.HasPrefix(bust, `<svg xmlns="`+svgNS+`"`) {
		t.Fatalf("bust has no xmlns: %s", bust[:40])
	}
	if !strings.Contains(bust, `viewBox="0 -1 16 16"`) {
		t.Fatalf("bust viewBox: %s", bust)
	}
	if !strings.Contains(bust, `shape-rendering="crispEdges"`) {
		t.Fatal("bust has no crispEdges")
	}
	if !strings.Contains(bust, `fill="`+out+`"`) {
		t.Fatal("bust has no outline colour")
	}
	if strings.Contains(bust, "<script") {
		t.Fatal("bust contains a script")
	}
	if strings.Contains(bust, "fill-opacity") {
		t.Fatal("bust must not draw the ground bar")
	}
	// Any other view is the bust.
	if SVG(sprite, "") != bust || SVG(sprite, "tiny") != bust {
		t.Fatal("an unknown view is not the bust")
	}

	full := SVG(sprite, "full")
	if !strings.Contains(full, `viewBox="-0.5 -1.5 17 21"`) {
		t.Fatalf("full viewBox: %s", full)
	}
	if !strings.Contains(full, `<rect x="2" y="19" width="12" height="1" fill="`+out+`" fill-opacity="0.14"/>`) {
		t.Fatal("full has no ground bar")
	}
	if !strings.Contains(full, `shape-rendering="crispEdges"`) {
		t.Fatal("full has no crispEdges")
	}
	if strings.Contains(full, "<script") {
		t.Fatal("full contains a script")
	}
	// The body and the head are both drawn: the head skin key and the shirt
	// key appear.
	if !strings.Contains(full, `fill="`+sprite.Palette["c"]+`"`) {
		t.Fatal("full does not draw the shirt")
	}
	if !strings.Contains(full, `fill="`+sprite.Palette["s"]+`"`) {
		t.Fatal("full does not draw the skin")
	}
}
