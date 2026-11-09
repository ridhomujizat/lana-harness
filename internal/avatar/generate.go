package avatar

import (
	"math"
	"math/rand/v2"
	"strings"
)

// Families are the body shapes of an avatar. The prompt names one at random
// so model results vary; the local generator draws each of them.
var Families = []string{
	"circle",
	"ellipse",
	"squircle",
	"tilted rounded square",
	"rounded triangle",
	"cloud",
	"teardrop",
	"blob",
}

// FamilyFor returns the body family a seed asks for.
func FamilyFor(seed int64) string {
	return Families[newRand(seed).IntN(len(Families))]
}

func newRand(seed int64) *rand.Rand {
	return rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15))
}

// Generate draws an avatar locally from seed (REQ-AVT-005): a body of
// FamilyFor(seed) with seeded jitter and two seeded pill or oval eyes. The
// result is canonical: Sanitize returns it unchanged. Equal seeds give equal
// avatars.
func Generate(seed int64) string {
	return GenerateFamily(FamilyFor(seed), seed)
}

// GenerateFamily is Generate with the body family fixed. An unknown family
// draws a blob.
func GenerateFamily(family string, seed int64) string {
	rng := newRand(seed ^ 0x5bd1e995)
	b := drawBody(family, rng)
	eyes := placeEyes(b, rng)
	var out strings.Builder
	out.WriteString(svgOpen)
	out.WriteString(`<path d="` + formatPath(b.cmds) + `" fill="` + fillBody + `"/>`)
	for _, e := range eyes {
		out.WriteString(e.markup())
	}
	out.WriteString("</svg>")
	return out.String()
}

// body is a drawn body: its absolute path and the circle the eyes go in.
type body struct {
	cmds   []pathCmd
	fx, fy float64
	fr     float64
	tilt   float64
	// up is the share of fr the eyes may sit above the face center.
	up float64
	// size is the larger side of the fitted body, for the eye size.
	size float64
}

func uniform(rng *rand.Rand, lo, hi float64) float64 { return lo + rng.Float64()*(hi-lo) }

func signed(rng *rand.Rand, lo, hi float64) float64 {
	v := uniform(rng, lo, hi)
	if rng.IntN(2) == 0 {
		return -v
	}
	return v
}

// drawBody builds the body in a local frame, then scales it so it fills
// 87–95% of the box and centers it, keeping the fitted box inside [1, 99].
func drawBody(family string, rng *rand.Rand) body {
	var b body
	sx, sy, tilt := 1.0, 1.0, 0.0
	switch family {
	case "circle":
		b = roundedPolygon(4, 40, 40, 1e4, 45)
		sx = uniform(rng, 0.97, 1.03)
		b.up = 0.45
	case "ellipse":
		b = roundedPolygon(4, 40, 40, 1e4, 45)
		sy = uniform(rng, 0.74, 0.86)
		tilt = signed(rng, 0, 18)
		b.up = 0.4
	case "squircle":
		h := 40.0
		b = roundedPolygon(4, h, uniform(rng, 13, 19), uniform(rng, 90, 220), 45)
		sx = uniform(rng, 0.94, 1.06)
		tilt = signed(rng, 0, 12)
		b.up = 0.45
	case "tilted rounded square":
		h := 40.0
		b = roundedPolygon(4, h, uniform(rng, 12, 17), uniform(rng, 110, 260), 45)
		sy = uniform(rng, 0.88, 1.0)
		tilt = signed(rng, 24, 38)
		b.up = 0.45
	case "rounded triangle":
		h := 26.0
		b = roundedPolygon(3, h, uniform(rng, 11, 15), uniform(rng, 70, 140), -90)
		sx = uniform(rng, 1.0, 1.14)
		tilt = signed(rng, 0, 9)
		// The eyes sit in the wide lower part, below the incenter.
		b.fy += h * 0.12
		b.fr *= 0.9
		b.up = 0.2
	case "cloud":
		b = cloud(rng)
		b.up = 0.35
	case "teardrop":
		b = teardrop(rng)
		b.up = 0.35
	default:
		b = blob(rng)
		b.up = 0.45
	}
	b.tilt = tilt
	// Scale and rotate the local frame, then fit the box.
	b = transformBody(b, sx, sy, tilt, 1, 0, 0)
	box := pathBox(b.cmds)
	size := math.Max(box[2]-box[0], box[3]-box[1])
	target := uniform(rng, 87, 95)
	s := target / size
	cx := 50 + uniform(rng, -1.5, 1.5)
	cy := 50 + uniform(rng, -1, 2)
	// Clamp the translation so the fitted box stays inside [1, 99].
	tx := clamp(cx-s*(box[0]+box[2])/2, 1-s*box[0], 99-s*box[2])
	ty := clamp(cy-s*(box[1]+box[3])/2, 1-s*box[1], 99-s*box[3])
	b = transformBody(b, 1, 1, 0, s, tx, ty)
	b.size = target
	return b
}

// transformBody maps a body by p' = s·R(tilt)·(sx·x, sy·y) + t. Arcs in the
// local frame must be circular (rx == ry, no rotation) when sx != sy.
func transformBody(b body, sx, sy, tiltDeg, s, tx, ty float64) body {
	r := tiltDeg * math.Pi / 180
	cos, sin := math.Cos(r), math.Sin(r)
	mapPt := func(x, y float64) (float64, float64) {
		x, y = x*sx, y*sy
		return s*(cos*x-sin*y) + tx, s*(sin*x+cos*y) + ty
	}
	out := body{fr: b.fr * s * math.Min(sx, sy), tilt: b.tilt, up: b.up}
	out.fx, out.fy = mapPt(b.fx, b.fy)
	out.cmds = make([]pathCmd, len(b.cmds))
	for i, cmd := range b.cmds {
		args := append([]float64(nil), cmd.args...)
		switch cmd.op {
		case 'A':
			args[0], args[1] = args[0]*s*sx, args[1]*s*sy
			args[2] += tiltDeg
			args[5], args[6] = mapPt(args[5], args[6])
		default:
			for k := 0; k+1 < len(args); k += 2 {
				args[k], args[k+1] = mapPt(args[k], args[k+1])
			}
		}
		out.cmds[i] = pathCmd{op: cmd.op, args: args}
	}
	return out
}

// pathBox is the bounding box of a path.
func pathBox(cmds []pathCmd) [4]float64 {
	box := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, p := range pathPoints(cmds) {
		box[0], box[1] = math.Min(box[0], p[0]), math.Min(box[1], p[1])
		box[2], box[3] = math.Max(box[2], p[0]), math.Max(box[3], p[1])
	}
	return box
}

// arcTo returns the arc from p to q on the circle (c, r) that passes the
// direction of m (a point on the circle).
func arcTo(c [2]float64, r float64, p, q, m [2]float64) pathCmd {
	ang := func(v [2]float64) float64 { return math.Atan2(v[1]-c[1], v[0]-c[0]) }
	norm := func(a float64) float64 {
		a = math.Mod(a, 2*math.Pi)
		if a < 0 {
			a += 2 * math.Pi
		}
		return a
	}
	ap, aq, am := ang(p), ang(q), ang(m)
	delta := norm(aq - ap)
	sweep, large := 1.0, 0.0
	if norm(am-ap) < delta {
		if delta > math.Pi {
			large = 1
		}
	} else {
		sweep = 0
		if 2*math.Pi-delta > math.Pi {
			large = 1
		}
	}
	return pathCmd{op: 'A', args: []float64{r, r, 0, large, sweep, q[0], q[1]}}
}

func polar(r, deg float64) [2]float64 {
	a := deg * math.Pi / 180
	return [2]float64{r * math.Cos(a), r * math.Sin(a)}
}

func add(p, q [2]float64) [2]float64           { return [2]float64{p[0] + q[0], p[1] + q[1]} }
func sub(p, q [2]float64) [2]float64           { return [2]float64{p[0] - q[0], p[1] - q[1]} }
func scale(p [2]float64, s float64) [2]float64 { return [2]float64{p[0] * s, p[1] * s} }
func length(p [2]float64) float64              { return math.Hypot(p[0], p[1]) }

// roundedPolygon draws a regular k-gon around the origin whose edges are arcs
// of radius edgeR (slightly convex sides) and whose corners are arcs of radius
// cornerR, tangent to each other. apothem is the distance from the center to
// the middle of a side; vertexDeg is the direction of the first corner. A
// corner radius equal to the apothem with flat sides draws a circle.
func roundedPolygon(k int, apothem, cornerR, edgeR, vertexDeg float64) body {
	step := 360 / float64(k)
	half := math.Pi / float64(k)
	if cornerR >= apothem {
		// A circle of radius apothem, drawn as four quarter arcs so the
		// rounded path stays well-conditioned (a two-arc circle loses
		// up to a unit of its box when the arcs are rounded).
		r := apothem
		origin := [2]float64{0, 0}
		pts := make([][2]float64, 4)
		for i := range pts {
			pts[i] = polar(r, float64(90*i))
		}
		cmds := []pathCmd{{op: 'M', args: []float64{pts[0][0], pts[0][1]}}}
		for i := 0; i < 4; i++ {
			cmds = append(cmds, arcTo(origin, r, pts[i], pts[(i+1)%4], polar(r, float64(90*i)+45)))
		}
		cmds = append(cmds, pathCmd{op: 'Z'})
		return body{cmds: cmds, fr: r * 0.86}
	}
	// The corner circle center sits on the vertex ray at distance d with
	// |d·u_v - (apothem-edgeR)·u_e| = edgeR - cornerR, the angle between
	// u_v and u_e being π/k.
	a := edgeR - apothem
	// d² + 2·d·a·cos(half) + a² - (edgeR-cornerR)² = 0
	bq := 2 * a * math.Cos(half)
	cq := a*a - (edgeR-cornerR)*(edgeR-cornerR)
	d := (-bq + math.Sqrt(bq*bq-4*cq)) / 2
	type corner struct {
		c        [2]float64
		in, out  [2]float64
		mid      [2]float64
		edgeNext [2]float64 // edge circle center between this corner and the next
	}
	corners := make([]corner, k)
	for i := 0; i < k; i++ {
		vDeg := vertexDeg + step*float64(i)
		corners[i].c = polar(d, vDeg)
		corners[i].mid = add(corners[i].c, polar(cornerR, vDeg))
		corners[i].edgeNext = polar(-a, vDeg+step/2)
	}
	// Tangent points: on the line from the edge circle center through the
	// corner center, at distance edgeR from the edge center.
	for i := 0; i < k; i++ {
		prevEdge := corners[(i+k-1)%k].edgeNext
		nextEdge := corners[i].edgeNext
		dirIn := sub(corners[i].c, prevEdge)
		corners[i].in = add(prevEdge, scale(dirIn, edgeR/length(dirIn)))
		dirOut := sub(corners[i].c, nextEdge)
		corners[i].out = add(nextEdge, scale(dirOut, edgeR/length(dirOut)))
	}
	cmds := []pathCmd{{op: 'M', args: []float64{corners[0].in[0], corners[0].in[1]}}}
	for i := 0; i < k; i++ {
		c := corners[i]
		cmds = append(cmds, arcTo(c.c, cornerR, c.in, c.out, c.mid))
		next := corners[(i+1)%k]
		edgeMid := add(c.edgeNext, polar(edgeR, vertexDeg+step*float64(i)+step/2))
		cmds = append(cmds, arcTo(c.edgeNext, edgeR, c.out, next.in, edgeMid))
	}
	cmds = append(cmds, pathCmd{op: 'Z'})
	return body{cmds: cmds, fr: apothem * 0.86}
}

// cloud draws the outline of 4–5 overlapping round puffs set on a ring:
// each puff contributes the arc between its outer crossings with its
// neighbors.
func cloud(rng *rand.Rand) body {
	n := 4 + rng.IntN(2)
	rx, ry := uniform(rng, 13, 17), uniform(rng, 8, 11)
	start := uniform(rng, 0, 360)
	centers := make([][2]float64, n)
	radii := make([]float64, n)
	for i := range centers {
		deg := start + 360*float64(i)/float64(n) + uniform(rng, -10, 10)
		p := polar(1, deg)
		centers[i] = [2]float64{p[0] * rx, p[1] * ry}
		radii[i] = uniform(rng, 15, 20)
	}
	// cross is the crossing of puffs i and j farther from the origin.
	cross := func(i, j int) [2]float64 {
		c0, c1 := centers[i], centers[j]
		r0, r1 := radii[i], radii[j]
		d := length(sub(c1, c0))
		a := (r0*r0 - r1*r1 + d*d) / (2 * d)
		h := math.Sqrt(math.Max(r0*r0-a*a, 0))
		base := add(c0, scale(sub(c1, c0), a/d))
		off := [2]float64{-(c1[1] - c0[1]) * h / d, (c1[0] - c0[0]) * h / d}
		p, q := add(base, off), sub(base, off)
		if length(p) >= length(q) {
			return p
		}
		return q
	}
	points := make([][2]float64, n) // points[i] joins puff i and puff i+1
	for i := range points {
		points[i] = cross(i, (i+1)%n)
	}
	cmds := []pathCmd{{op: 'M', args: []float64{points[n-1][0], points[n-1][1]}}}
	for i := 0; i < n; i++ {
		from, to := points[(i+n-1)%n], points[i]
		out := add(centers[i], scale(centers[i], radii[i]/length(centers[i])))
		cmds = append(cmds, arcTo(centers[i], radii[i], from, to, out))
	}
	cmds = append(cmds, pathCmd{op: 'Z'})
	return body{cmds: cmds, fr: ry + 12}
}

// teardrop draws a round body with one rounded point, the hull of a large and
// a small circle.
func teardrop(rng *rand.Rand) body {
	r1 := 32.0
	dist := r1 * uniform(rng, 1.45, 1.75)
	r2 := uniform(rng, 4, 6.5)
	dir := uniform(rng, -150, -30) // the point goes up, left or right
	beta := math.Acos((r1 - r2) / dist)
	betaDeg := beta * 180 / math.Pi
	c1 := [2]float64{0, 0}
	c2 := polar(dist, dir)
	p1 := polar(r1, dir-betaDeg)
	p2 := add(c2, polar(r2, dir-betaDeg))
	q2 := add(c2, polar(r2, dir+betaDeg))
	q1 := polar(r1, dir+betaDeg)
	cmds := []pathCmd{
		{op: 'M', args: []float64{p1[0], p1[1]}},
		{op: 'L', args: []float64{p2[0], p2[1]}},
		arcTo(c2, r2, p2, q2, add(c2, polar(r2, dir))),
		{op: 'L', args: []float64{q1[0], q1[1]}},
		arcTo(c1, r1, q1, p1, polar(r1, dir+180)),
		{op: 'Z'},
	}
	return body{cmds: cmds, fr: r1 * 0.86}
}

// blob draws a soft organic outline: a circle with low harmonics, smoothed
// through a closed Catmull-Rom spline.
func blob(rng *rand.Rand) body {
	n := 10
	a2, p2 := uniform(rng, 0.03, 0.08), uniform(rng, 0, 2*math.Pi)
	a3, p3 := uniform(rng, 0.02, 0.06), uniform(rng, 0, 2*math.Pi)
	pts := make([][2]float64, n)
	minR := math.Inf(1)
	for i := range pts {
		t := 2 * math.Pi * float64(i) / float64(n)
		r := 38 * (1 + a2*math.Cos(2*t+p2) + a3*math.Cos(3*t+p3))
		minR = math.Min(minR, r)
		pts[i] = [2]float64{r * math.Cos(t), r * math.Sin(t)}
	}
	cmds := []pathCmd{{op: 'M', args: []float64{pts[0][0], pts[0][1]}}}
	for i := 0; i < n; i++ {
		p0, p1 := pts[(i+n-1)%n], pts[i]
		p2, p3 := pts[(i+1)%n], pts[(i+2)%n]
		c1 := add(p1, scale(sub(p2, p0), 1.0/6))
		c2 := sub(p2, scale(sub(p3, p1), 1.0/6))
		cmds = append(cmds, pathCmd{op: 'C', args: []float64{c1[0], c1[1], c2[0], c2[1], p2[0], p2[1]}})
	}
	cmds = append(cmds, pathCmd{op: 'Z'})
	return body{cmds: cmds, fr: minR * 0.86}
}

// eye is one white eye: a pill (rect with full rx) or an oval.
type eye struct {
	pill   bool
	cx, cy float64
	w, h   float64
	rotate float64
}

func (e eye) markup() string {
	rot := ` transform="rotate(` + formatNumber(e.rotate) + " " + formatNumber(e.cx) + " " + formatNumber(e.cy) + `)"`
	if e.pill {
		return `<rect x="` + formatNumber(e.cx-e.w/2) + `" y="` + formatNumber(e.cy-e.h/2) +
			`" width="` + formatNumber(e.w) + `" height="` + formatNumber(e.h) +
			`" rx="` + formatNumber(e.w/2) + `" fill="` + fillEye + `"` + rot + "/>"
	}
	return `<ellipse cx="` + formatNumber(e.cx) + `" cy="` + formatNumber(e.cy) +
		`" rx="` + formatNumber(e.w/2) + `" ry="` + formatNumber(e.h/2) + `" fill="` + fillEye + `"` + rot + "/>"
}

// outline returns points around the eye grown by margin, for the inside test.
func (e eye) outline(margin float64) [][2]float64 {
	m := rotate(e.rotate, e.cx, e.cy)
	rx, ry := e.w/2+margin, e.h/2+margin
	pts := make([][2]float64, 0, 12)
	for i := 0; i < 12; i++ {
		t := 2 * math.Pi * float64(i) / 12
		x, y := m.apply(e.cx+rx*math.Cos(t), e.cy+ry*math.Sin(t))
		pts = append(pts, [2]float64{x, y})
	}
	return pts
}

// placeEyes picks two eyes in the face circle with seeded size, spacing,
// tilt, and gaze, then pulls them toward the center until they sit fully
// inside the body.
func placeEyes(b body, rng *rand.Rand) []eye {
	poly := flatten(b.cmds)
	fr := b.fr
	pill := rng.IntN(3) != 0
	w := clamp(b.size*uniform(rng, 0.095, 0.125), 6.5, 11)
	h := w * uniform(rng, 1.6, 2.5)
	if rng.IntN(5) == 0 {
		w *= 1.15
		h = w * uniform(rng, 1.05, 1.3) // round eyes
	}
	h = math.Min(h, 23)
	gap := math.Max(fr*uniform(rng, 0.4, 0.62), w+3.5)
	axis := b.tilt*0.6 + uniform(rng, -10, 10)
	lean := axis + signed(rng, 4, 30)
	gx := fr * uniform(rng, -0.42, 0.42)
	gy := fr * uniform(rng, -b.up, 0.08)
	small, dy := 1.0, 0.0
	if rng.IntN(10) < 3 {
		small = uniform(rng, 0.72, 0.9)
		dy = fr * uniform(rng, -0.08, 0.08)
	}
	smallFirst := rng.IntN(2) == 0
	jitter := [2]float64{uniform(rng, -4, 4), uniform(rng, -4, 4)}
	for try := 0; ; try++ {
		cx, cy := b.fx+gx, b.fy+gy
		ux, uy := math.Cos(axis*math.Pi/180), math.Sin(axis*math.Pi/180)
		eyes := make([]eye, 2)
		for i, side := range []float64{-1, 1} {
			sz := 1.0
			if (i == 0) == smallFirst {
				sz = small
			}
			eyes[i] = eye{
				pill:   pill,
				cx:     round2(cx + side*ux*gap/2),
				cy:     round2(cy + side*uy*gap/2 + float64(i)*dy),
				w:      round2(w * sz),
				h:      round2(h * sz),
				rotate: round2(lean + jitter[i]),
			}
		}
		inside := true
		for _, e := range eyes {
			for _, p := range e.outline(2.5) {
				if !pointInPolygon(p, poly) {
					inside = false
				}
			}
		}
		if inside || try >= 20 {
			return eyes
		}
		gx, gy, dy = gx*0.7, gy*0.7, dy*0.7
		switch {
		case try > 10:
			w, h, gap = w*0.9, h*0.9, math.Max(gap*0.92, w+2)
		case try > 5 && h > w*1.15:
			// Shorter eyes fit a flat body better than smaller ones.
			h *= 0.88
		}
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// flatten turns an absolute path of M, L, A, C, Z into a polygon.
func flatten(cmds []pathCmd) [][2]float64 {
	var poly [][2]float64
	var x, y float64
	for _, cmd := range cmds {
		a := cmd.args
		switch cmd.op {
		case 'M', 'L':
			x, y = a[0], a[1]
			poly = append(poly, [2]float64{x, y})
		case 'A':
			poly = append(poly, arcSamples(x, y, a[0], a[1], a[2], a[3] != 0, a[4] != 0, a[5], a[6])...)
			x, y = a[5], a[6]
		case 'C':
			for k := 1; k <= 12; k++ {
				t := float64(k) / 12
				mt := 1 - t
				px := mt*mt*mt*x + 3*mt*mt*t*a[0] + 3*mt*t*t*a[2] + t*t*t*a[4]
				py := mt*mt*mt*y + 3*mt*mt*t*a[1] + 3*mt*t*t*a[3] + t*t*t*a[5]
				poly = append(poly, [2]float64{px, py})
			}
			x, y = a[4], a[5]
		}
	}
	return poly
}

// pointInPolygon is the even-odd ray test.
func pointInPolygon(p [2]float64, poly [][2]float64) bool {
	inside := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		a, b := poly[i], poly[j]
		if (a[1] > p[1]) != (b[1] > p[1]) && p[0] < (b[0]-a[0])*(p[1]-a[1])/(b[1]-a[1])+a[0] {
			inside = !inside
		}
	}
	return inside
}
