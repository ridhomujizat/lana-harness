package avatar

import (
	"math"
	"testing"
)

// mustSanitize canonicalizes in or fails the test.
func mustSanitize(t *testing.T, in string) string {
	t.Helper()
	out, err := Sanitize(in)
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	return out
}

// circleAvatar builds a canonical avatar: a body circle of radius r at
// (cx, cy) and two proportional eyes on it.
func circleAvatar(t *testing.T, cx, cy, r float64) string {
	t.Helper()
	rx, ry := r*0.12, r*0.24
	eye := func(ex float64) string {
		return `<ellipse cx="` + formatNumber(ex) + `" cy="` + formatNumber(cy) +
			`" rx="` + formatNumber(rx) + `" ry="` + formatNumber(ry) + `" fill="#fff"/>`
	}
	return mustSanitize(t, svgOpen+
		`<circle cx="`+formatNumber(cx)+`" cy="`+formatNumber(cy)+`" r="`+formatNumber(r)+`" fill="currentColor"/>`+
		eye(cx-r/4)+eye(cx+r/4)+`</svg>`)
}

// bodySide returns the larger side of a canonical avatar's body box.
func bodySide(t *testing.T, svg string) [4]float64 {
	t.Helper()
	box, err := bodyBounds(svg)
	if err != nil {
		t.Fatalf("bodyBounds: %v", err)
	}
	return box
}

func TestEnlargeSmallBody(t *testing.T) {
	in := circleAvatar(t, 50, 50, 35) // body box side 70
	box0 := bodySide(t, in)
	if side := box0[2] - box0[0]; math.Abs(side-70) > 1e-6 {
		t.Fatalf("input body side = %g", side)
	}
	out, err := Enlarge(in)
	if err != nil {
		t.Fatal(err)
	}
	box := bodySide(t, out)
	side := math.Max(box[2]-box[0], box[3]-box[1])
	if math.Abs(side-88) > 0.5 {
		t.Fatalf("enlarged body side = %g", side)
	}
	if box[0] < 1 || box[1] < 1 || box[2] > 99 || box[3] > 99 {
		t.Fatalf("enlarged body box = %v", box)
	}
	cx0, cy0 := (box0[0]+box0[2])/2, (box0[1]+box0[3])/2
	cx, cy := (box[0]+box[2])/2, (box[1]+box[3])/2
	if math.Abs(cx-cx0) > 0.5 || math.Abs(cy-cy0) > 0.5 {
		t.Fatalf("center moved from (%g,%g) to (%g,%g)", cx0, cy0, cx, cy)
	}
	if again, err := Sanitize(out); err != nil || again != out {
		t.Fatalf("Enlarge is not canonical: %v\n%s\n%s", err, out, again)
	}
	for _, eye := range eyesOf(t, out) {
		ex, ey := (eye[0]+eye[2])/2, (eye[1]+eye[3])/2
		if ex < box[0] || ex > box[2] || ey < box[1] || ey > box[3] {
			t.Fatalf("an eye left the body: %v vs %v", eye, box)
		}
	}
}

func TestEnlargeBigBodyUnchanged(t *testing.T) {
	in := circleAvatar(t, 50, 50, 45) // body box side 90
	out, err := Enlarge(in)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("a large body was changed:\n%s\n%s", in, out)
	}
}

func TestEnlargeOffCenterStaysInBox(t *testing.T) {
	in := circleAvatar(t, 80, 80, 20) // body box [60, 100]²
	out, err := Enlarge(in)
	if err != nil {
		t.Fatal(err)
	}
	box := bodySide(t, out)
	if box[0] < 1 || box[1] < 1 || box[2] > 99 || box[3] > 99 {
		t.Fatalf("clamped body box = %v", box)
	}
	if again, err := Sanitize(out); err != nil || again != out {
		t.Fatalf("Enlarge is not canonical: %v", err)
	}
}

func TestEnlargeScaleCap(t *testing.T) {
	for _, r := range []float64{10, 20, 30, 35, 40, 43} {
		in := circleAvatar(t, 50, 50, r)
		out, err := Enlarge(in)
		if err != nil {
			t.Fatalf("r=%g: %v", r, err)
		}
		box := bodySide(t, out)
		side := math.Max(box[2]-box[0], box[3]-box[1])
		if side > 2*r*maxEnlargeScale+1e-6 {
			t.Fatalf("r=%g: side %g is over the %g cap", r, side, maxEnlargeScale)
		}
		if again, err := Sanitize(out); err != nil || again != out {
			t.Fatalf("r=%g: not canonical: %v", r, err)
		}
	}
}

// eyesOf returns the bounding boxes of the eyes of a canonical avatar.
func eyesOf(t *testing.T, svg string) [][4]float64 {
	t.Helper()
	root, err := parseXML(svg)
	if err != nil {
		t.Fatalf("parseXML: %v", err)
	}
	w := &walker{}
	w.children(root.children, identity, fillClass(root.attrs["fill"]), 0)
	if w.err != nil {
		t.Fatalf("walk: %v", w.err)
	}
	return w.eyes
}
