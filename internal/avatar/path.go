package avatar

import (
	"errors"
	"math"
	"strings"
)

// maxPathCommands caps the commands of one path so a hostile path cannot make
// the bounds walk expensive.
const maxPathCommands = 400

// pathCmd is one path command with its arguments as written: the letter keeps
// its case (lower case is relative).
type pathCmd struct {
	op   byte
	args []float64
}

// pathArgs is the argument count of each path command.
var pathArgs = map[byte]int{
	'M': 2, 'L': 2, 'T': 2, 'H': 1, 'V': 1, 'C': 6, 'S': 4, 'Q': 4, 'A': 7, 'Z': 0,
}

var errBadPath = errors.New("path data is not valid")

// parsePath parses SVG path data. Numbers follow the SVG grammar; the arc
// flags may be written without a separator ("a5 5 0 015 5").
func parsePath(d string) ([]pathCmd, error) {
	var cmds []pathCmd
	i := 0
	skip := func() {
		for i < len(d) && (d[i] == ' ' || d[i] == ',' || d[i] == '\t' || d[i] == '\n' || d[i] == '\r') {
			i++
		}
	}
	var op byte
	for {
		skip()
		if i >= len(d) {
			break
		}
		c := d[i]
		upper := c &^ 0x20
		if _, ok := pathArgs[upper]; ok && (c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
			op = c
			i++
		} else if op == 0 {
			return nil, errBadPath
		} else if op&^0x20 == 'Z' {
			return nil, errBadPath
		}
		n := pathArgs[op&^0x20]
		args := make([]float64, 0, n)
		for k := 0; k < n; k++ {
			skip()
			if op&^0x20 == 'A' && (k == 3 || k == 4) {
				if i >= len(d) || (d[i] != '0' && d[i] != '1') {
					return nil, errBadPath
				}
				args = append(args, float64(d[i]-'0'))
				i++
				continue
			}
			v, next, ok := readNumber(d, i)
			if !ok {
				return nil, errBadPath
			}
			args = append(args, v)
			i = next
		}
		cmds = append(cmds, pathCmd{op: op, args: args})
		if len(cmds) > maxPathCommands {
			return nil, errBadPath
		}
		// An implicit repeat of moveto is a lineto (SVG 1.1 §8.3.2).
		switch op {
		case 'M':
			op = 'L'
		case 'm':
			op = 'l'
		}
	}
	if len(cmds) == 0 || cmds[0].op&^0x20 != 'M' {
		return nil, errBadPath
	}
	return cmds, nil
}

// readNumber reads one SVG number at i.
func readNumber(s string, i int) (float64, int, bool) {
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return 0, start, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	v, ok := parseFloat(s[start:i])
	return v, i, ok
}

// formatPath writes commands canonically: one letter per command, numbers
// rounded to two decimals, separated by spaces.
func formatPath(cmds []pathCmd) string {
	var b strings.Builder
	for n, cmd := range cmds {
		if n > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte(cmd.op)
		for k, v := range cmd.args {
			b.WriteByte(' ')
			if cmd.op&^0x20 == 'A' && (k == 3 || k == 4) {
				if v != 0 {
					b.WriteByte('1')
				} else {
					b.WriteByte('0')
				}
				continue
			}
			b.WriteString(formatNumber(v))
		}
	}
	return b.String()
}

// pathPoints returns absolute points that bound the path: end points, curve
// control points (a curve stays in their hull), and samples along arcs.
func pathPoints(cmds []pathCmd) [][2]float64 {
	var pts [][2]float64
	var x, y, sx, sy float64
	// lastCtrl is the last cubic or quadratic control point, for S and T.
	var lcx, lcy float64
	var lastOp byte
	for _, cmd := range cmds {
		rel := cmd.op >= 'a'
		op := cmd.op &^ 0x20
		a := cmd.args
		ox, oy := 0.0, 0.0
		if rel {
			ox, oy = x, y
		}
		switch op {
		case 'M':
			x, y = ox+a[0], oy+a[1]
			sx, sy = x, y
			pts = append(pts, [2]float64{x, y})
		case 'L', 'T':
			nx, ny := ox+a[0], oy+a[1]
			if op == 'T' {
				cx, cy := x, y
				if lastOp == 'Q' || lastOp == 'T' {
					cx, cy = 2*x-lcx, 2*y-lcy
				}
				pts = append(pts, [2]float64{cx, cy})
				lcx, lcy = cx, cy
			}
			x, y = nx, ny
			pts = append(pts, [2]float64{x, y})
		case 'H':
			x = ox + a[0]
			pts = append(pts, [2]float64{x, y})
		case 'V':
			y = oy + a[0]
			pts = append(pts, [2]float64{x, y})
		case 'C':
			pts = append(pts, [2]float64{ox + a[0], oy + a[1]}, [2]float64{ox + a[2], oy + a[3]})
			lcx, lcy = ox+a[2], oy+a[3]
			x, y = ox+a[4], oy+a[5]
			pts = append(pts, [2]float64{x, y})
		case 'S':
			c1x, c1y := x, y
			if lastOp == 'C' || lastOp == 'S' {
				c1x, c1y = 2*x-lcx, 2*y-lcy
			}
			pts = append(pts, [2]float64{c1x, c1y}, [2]float64{ox + a[0], oy + a[1]})
			lcx, lcy = ox+a[0], oy+a[1]
			x, y = ox+a[2], oy+a[3]
			pts = append(pts, [2]float64{x, y})
		case 'Q':
			pts = append(pts, [2]float64{ox + a[0], oy + a[1]})
			lcx, lcy = ox+a[0], oy+a[1]
			x, y = ox+a[2], oy+a[3]
			pts = append(pts, [2]float64{x, y})
		case 'A':
			nx, ny := ox+a[5], oy+a[6]
			pts = append(pts, arcSamples(x, y, a[0], a[1], a[2], a[3] != 0, a[4] != 0, nx, ny)...)
			x, y = nx, ny
		case 'Z':
			x, y = sx, sy
		}
		lastOp = op
	}
	return pts
}

// arcSamples returns points along an elliptical arc, using the end point to
// center conversion of SVG 1.1 appendix F.6.5.
func arcSamples(x1, y1, rx, ry, phiDeg float64, large, sweep bool, x2, y2 float64) [][2]float64 {
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 || (x1 == x2 && y1 == y2) {
		return [][2]float64{{x2, y2}}
	}
	phi := phiDeg * math.Pi / 180
	cosP, sinP := math.Cos(phi), math.Sin(phi)
	dx, dy := (x1-x2)/2, (y1-y2)/2
	x1p := cosP*dx + sinP*dy
	y1p := -sinP*dx + cosP*dy
	if l := x1p*x1p/(rx*rx) + y1p*y1p/(ry*ry); l > 1 {
		s := math.Sqrt(l)
		rx, ry = rx*s, ry*s
	}
	num := rx*rx*ry*ry - rx*rx*y1p*y1p - ry*ry*x1p*x1p
	den := rx*rx*y1p*y1p + ry*ry*x1p*x1p
	coef := 0.0
	if den > 0 && num > 0 {
		coef = math.Sqrt(num / den)
	}
	if large == sweep {
		coef = -coef
	}
	cxp := coef * rx * y1p / ry
	cyp := -coef * ry * x1p / rx
	cx := cosP*cxp - sinP*cyp + (x1+x2)/2
	cy := sinP*cxp + cosP*cyp + (y1+y2)/2
	angle := func(ux, uy, vx, vy float64) float64 {
		return math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
	}
	t1 := angle(1, 0, (x1p-cxp)/rx, (y1p-cyp)/ry)
	dt := angle((x1p-cxp)/rx, (y1p-cyp)/ry, (-x1p-cxp)/rx, (-y1p-cyp)/ry)
	if !sweep && dt > 0 {
		dt -= 2 * math.Pi
	} else if sweep && dt < 0 {
		dt += 2 * math.Pi
	}
	const steps = 16
	pts := make([][2]float64, 0, steps+1)
	for k := 1; k <= steps; k++ {
		t := t1 + dt*float64(k)/steps
		ex, ey := rx*math.Cos(t), ry*math.Sin(t)
		pts = append(pts, [2]float64{cosP*ex - sinP*ey + cx, sinP*ex + cosP*ey + cy})
	}
	return pts
}
