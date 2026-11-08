// Package avatar makes and checks the generated agent avatars of spec 43: a
// solid body shape drawn in currentColor with exactly two white eye holes, in
// a 100×100 SVG. Sanitize turns model output (or a stored file) into a safe,
// canonical SVG; Generate draws one locally from a seed; Create asks a model
// through the router and falls back to Generate.
package avatar

import (
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
)

// Size limits of an avatar (REQ-AVT-004).
const (
	// MaxSize is the largest accepted SVG, before and after canonical
	// re-serialization.
	MaxSize = 8 << 10
	// MaxPathData is the largest accepted d attribute.
	MaxPathData = 4 << 10
)

const (
	// maxShapes caps the drawn elements of one avatar.
	maxShapes = 24
	// maxDepth caps the nesting of g elements.
	maxDepth = 4
	// boxMin and boxMax bound every coordinate: a shape may overhang the
	// 100×100 box a little, never far.
	boxMin = -20.0
	boxMax = 120.0
	// maxEyeShare is the largest eye bounding box as a share of the body's.
	maxEyeShare = 0.25
)

// ErrInvalid is wrapped by every Sanitize error.
var ErrInvalid = errors.New("invalid avatar")

// InvalidError says why an SVG was refused.
type InvalidError struct {
	Reason string
}

func (e *InvalidError) Error() string { return "invalid avatar: " + e.Reason }

// Unwrap makes errors.Is(err, ErrInvalid) true.
func (e *InvalidError) Unwrap() error { return ErrInvalid }

func invalid(reason string) error { return &InvalidError{Reason: reason} }

// SVG namespace and the canonical root element.
const (
	svgNS   = "http://www.w3.org/2000/svg"
	svgOpen = `<svg xmlns="` + svgNS + `" viewBox="0 0 100 100">`
)

// Fill values of a canonical avatar.
const (
	fillBody = "currentColor"
	fillEye  = "#ffffff"
)

// forbidden elements make the whole SVG invalid: they run code, load
// resources, animate, or style.
var forbidden = map[string]bool{
	"script": true, "style": true, "foreignobject": true, "iframe": true, "image": true,
	"use": true, "a": true, "animate": true, "animatemotion": true, "animatetransform": true,
	"animatecolor": true, "set": true, "feimage": true, "object": true, "embed": true,
	"link": true, "meta": true, "handler": true, "listener": true, "audio": true,
	"video": true, "canvas": true, "base": true, "svg:script": true,
}

// shapes are the drawn elements an avatar may hold.
var shapes = map[string]bool{"path": true, "ellipse": true, "circle": true, "rect": true}

// element is a parsed XML element.
type element struct {
	name     string
	attrs    map[string]string
	children []*element
}

// Sanitize checks an avatar SVG and returns its canonical form. raw may be a
// model reply: text around the first <svg …</svg> is dropped. Unknown
// elements and attributes are stripped; active content (scripts, event
// attributes, references, styles, DOCTYPE, entities) is refused, as are an
// avatar without a currentColor body, without exactly two white eyes, with
// coordinates far outside the box, or over the size limits.
func Sanitize(raw string) (string, error) {
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "<!doctype") || strings.Contains(lower, "<!entity") {
		return "", invalid("DOCTYPE or entity declarations are not allowed")
	}
	start := strings.Index(lower, "<svg")
	end := strings.LastIndex(lower, "</svg>")
	if start < 0 || end < start {
		return "", invalid("no <svg> element")
	}
	src := raw[start : end+len("</svg>")]
	if len(src) > MaxSize {
		return "", invalid("the SVG is over 8 KB")
	}
	root, err := parseXML(src)
	if err != nil {
		return "", err
	}
	if root.name != "svg" {
		return "", invalid("the root element is not <svg>")
	}
	if !unitViewBox(root.attrs["viewbox"]) {
		return "", invalid(`viewBox must be "0 0 100 100"`)
	}
	w := &walker{}
	inherited := fillClass(root.attrs["fill"])
	out := w.children(root.children, identity, inherited, 0)
	if w.err != nil {
		return "", w.err
	}
	if w.shapes > maxShapes {
		return "", invalid("too many shapes")
	}
	if len(w.bodies) == 0 {
		return "", invalid("no body shape filled with currentColor")
	}
	if len(w.eyes) != 2 {
		return "", invalid("an avatar has exactly two white eyes, found " + strconv.Itoa(len(w.eyes)))
	}
	body := union(w.bodies)
	for _, eye := range w.eyes {
		cx, cy := (eye[0]+eye[2])/2, (eye[1]+eye[3])/2
		if cx < body[0] || cx > body[2] || cy < body[1] || cy > body[3] {
			return "", invalid("an eye is outside the body")
		}
		if area(eye) > maxEyeShare*area(body) {
			return "", invalid("an eye is too large for the body")
		}
	}
	svg := svgOpen + out + "</svg>"
	if len(svg) > MaxSize {
		return "", invalid("the SVG is over 8 KB")
	}
	return svg, nil
}

// parseXML parses src into an element tree. Processing instructions,
// directives, and active content are refused while parsing.
func parseXML(src string) (*element, error) {
	dec := xml.NewDecoder(strings.NewReader(src))
	dec.Strict = true
	var stack []*element
	var root *element
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, invalid("the SVG is not well-formed XML")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if forbidden[name] {
				return nil, invalid("<" + name + "> is not allowed")
			}
			el := &element{name: name, attrs: map[string]string{}}
			for _, attr := range t.Attr {
				key := strings.ToLower(attr.Name.Local)
				value := attr.Value
				lowerValue := strings.ToLower(value)
				switch {
				case strings.HasPrefix(key, "on"):
					return nil, invalid("event attributes are not allowed")
				case key == "href" || key == "src" || key == "style" || key == "class":
					return nil, invalid(key + " attributes are not allowed")
				case strings.Contains(lowerValue, "url(") || strings.Contains(lowerValue, "javascript:") || strings.Contains(lowerValue, "data:"):
					return nil, invalid("external references are not allowed")
				}
				if attr.Name.Space != "" {
					// Namespaced attributes (xmlns:xlink, sodipodi:…) are stripped.
					continue
				}
				el.attrs[key] = value
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, el)
			} else if root == nil {
				root = el
			} else {
				return nil, invalid("more than one root element")
			}
			stack = append(stack, el)
			if len(stack) > maxDepth+2 {
				return nil, invalid("elements are nested too deeply")
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, invalid("the SVG is not well-formed XML")
			}
			stack = stack[:len(stack)-1]
		case xml.ProcInst:
			return nil, invalid("processing instructions are not allowed")
		case xml.Directive:
			return nil, invalid("DOCTYPE or entity declarations are not allowed")
		case xml.CharData, xml.Comment:
			// Text and comments are dropped.
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, invalid("the SVG is not well-formed XML")
	}
	return root, nil
}

// unitViewBox reports whether value is the viewBox 0 0 100 100.
func unitViewBox(value string) bool {
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' })
	if len(fields) != 4 {
		return false
	}
	want := []float64{0, 0, 100, 100}
	for i, field := range fields {
		v, ok := parseFloat(field)
		if !ok || v != want[i] {
			return false
		}
	}
	return true
}

// Fill classes of a shape.
const (
	fillNone = iota
	fillIsBody
	fillIsEye
	fillOther
)

// fillClass classifies a fill value: "" is none (inherit), currentColor is the
// body, white is an eye, anything else is a shape the avatar drops.
func fillClass(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return fillNone
	case "currentcolor":
		return fillIsBody
	case "#fff", "#ffffff", "white":
		return fillIsEye
	default:
		return fillOther
	}
}

// walker collects the canonical markup and the bounds of the bodies and eyes.
type walker struct {
	err    error
	shapes int
	bodies [][4]float64
	eyes   [][4]float64
}

func (w *walker) fail(err error) string {
	if w.err == nil {
		w.err = err
	}
	return ""
}

// children renders the allowed children of an element under the transform m
// and the inherited fill class.
func (w *walker) children(list []*element, m matrix, inherited, depth int) string {
	var b strings.Builder
	for _, el := range list {
		if w.err != nil {
			return ""
		}
		switch {
		case el.name == "g":
			b.WriteString(w.group(el, m, inherited, depth))
		case shapes[el.name]:
			b.WriteString(w.shape(el, m, inherited))
		default:
			// Unknown elements (title, desc, defs, metadata, …) are stripped
			// with their content. Forbidden ones were refused while parsing.
		}
	}
	return b.String()
}

func (w *walker) group(el *element, m matrix, inherited, depth int) string {
	if depth >= maxDepth {
		return w.fail(invalid("groups are nested too deeply"))
	}
	fill := inherited
	if c := fillClass(el.attrs["fill"]); c != fillNone {
		fill = c
	}
	transform, local, err := parseTransform(el.attrs["transform"])
	if err != nil {
		return w.fail(err)
	}
	inner := w.children(el.children, m.mul(local), fill, depth+1)
	if inner == "" {
		return ""
	}
	if transform == "" {
		return inner
	}
	return `<g transform="` + transform + `">` + inner + "</g>"
}

func (w *walker) shape(el *element, m matrix, inherited int) string {
	w.shapes++
	fill := inherited
	if c := fillClass(el.attrs["fill"]); c != fillNone {
		fill = c
	}
	transform, local, err := parseTransform(el.attrs["transform"])
	if err != nil {
		return w.fail(err)
	}
	m = m.mul(local)

	var markup strings.Builder
	var pts [][2]float64
	num := func(key string, required bool, positive bool) (float64, bool) {
		raw, ok := el.attrs[key]
		if !ok {
			if required {
				w.fail(invalid("<" + el.name + "> needs " + key))
			}
			return 0, false
		}
		v, ok := parseFloat(strings.TrimSpace(raw))
		if !ok || (positive && v < 0) {
			w.fail(invalid("<" + el.name + "> has a bad " + key))
			return 0, false
		}
		return v, true
	}
	write := func(key string, v float64) {
		markup.WriteString(" " + key + `="` + formatNumber(v) + `"`)
	}
	switch el.name {
	case "path":
		d := el.attrs["d"]
		if len(d) > MaxPathData {
			return w.fail(invalid("path data is over 4 KB"))
		}
		cmds, err := parsePath(d)
		if err != nil {
			return w.fail(invalid("path data is not valid"))
		}
		markup.WriteString(`<path d="` + formatPath(cmds) + `"`)
		pts = pathPoints(cmds)
	case "ellipse", "circle":
		cx, _ := num("cx", false, false)
		cy, _ := num("cy", false, false)
		var rx, ry float64
		if el.name == "circle" {
			r, _ := num("r", true, true)
			rx, ry = r, r
		} else {
			rx, _ = num("rx", true, true)
			ry, _ = num("ry", true, true)
		}
		if w.err != nil {
			return ""
		}
		markup.WriteString("<" + el.name)
		write("cx", cx)
		write("cy", cy)
		if el.name == "circle" {
			write("r", rx)
		} else {
			write("rx", rx)
			write("ry", ry)
		}
		pts = [][2]float64{{cx - rx, cy - ry}, {cx + rx, cy - ry}, {cx - rx, cy + ry}, {cx + rx, cy + ry}}
	case "rect":
		x, _ := num("x", false, false)
		y, _ := num("y", false, false)
		width, _ := num("width", true, true)
		height, _ := num("height", true, true)
		rx, hasRX := num("rx", false, true)
		ry, hasRY := num("ry", false, true)
		if w.err != nil {
			return ""
		}
		markup.WriteString("<rect")
		write("x", x)
		write("y", y)
		write("width", width)
		write("height", height)
		if hasRX {
			write("rx", rx)
		}
		if hasRY {
			write("ry", ry)
		}
		pts = [][2]float64{{x, y}, {x + width, y}, {x, y + height}, {x + width, y + height}}
	}
	if w.err != nil {
		return ""
	}
	box, ok := bounds(m, pts)
	if !ok {
		return w.fail(invalid("a coordinate is far outside the 100×100 box"))
	}
	switch fill {
	case fillIsBody:
		w.bodies = append(w.bodies, box)
		markup.WriteString(` fill="` + fillBody + `"`)
	case fillIsEye:
		w.eyes = append(w.eyes, box)
		markup.WriteString(` fill="` + fillEye + `"`)
	default:
		// A shape in another color (a mouth, a pupil, an outline) or with
		// no fill at all is dropped.
		return ""
	}
	if transform != "" {
		markup.WriteString(` transform="` + transform + `"`)
	}
	markup.WriteString("/>")
	return markup.String()
}

// bounds transforms pts by m and returns their bounding box. ok is false when
// a point is not finite or lies far outside the box.
func bounds(m matrix, pts [][2]float64) ([4]float64, bool) {
	box := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	if len(pts) == 0 {
		return box, false
	}
	for _, p := range pts {
		x, y := m.apply(p[0], p[1])
		if math.IsNaN(x) || math.IsNaN(y) || x < boxMin || x > boxMax || y < boxMin || y > boxMax {
			return box, false
		}
		box[0], box[1] = math.Min(box[0], x), math.Min(box[1], y)
		box[2], box[3] = math.Max(box[2], x), math.Max(box[3], y)
	}
	return box, true
}

func union(boxes [][4]float64) [4]float64 {
	out := boxes[0]
	for _, b := range boxes[1:] {
		out[0], out[1] = math.Min(out[0], b[0]), math.Min(out[1], b[1])
		out[2], out[3] = math.Max(out[2], b[2]), math.Max(out[3], b[3])
	}
	return out
}

func area(b [4]float64) float64 { return (b[2] - b[0]) * (b[3] - b[1]) }

// parseFloat parses a finite number without units.
func parseFloat(s string) (float64, bool) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) || math.Abs(v) > 1e6 {
		return 0, false
	}
	return v, true
}

// formatNumber rounds to two decimals and drops trailing zeros.
func formatNumber(v float64) string {
	v = math.Round(v*100) / 100
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
