package avatar

import (
	"math"
	"regexp"
	"strings"
)

// matrix is a 2D affine transform [a c e; b d f] as in SVG.
type matrix struct{ a, b, c, d, e, f float64 }

var identity = matrix{a: 1, d: 1}

// mul returns m·n: n applies first, then m.
func (m matrix) mul(n matrix) matrix {
	return matrix{
		a: m.a*n.a + m.c*n.b,
		b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d,
		d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e,
		f: m.b*n.e + m.d*n.f + m.f,
	}
}

func (m matrix) apply(x, y float64) (float64, float64) {
	return m.a*x + m.c*y + m.e, m.b*x + m.d*y + m.f
}

func translate(x, y float64) matrix { return matrix{a: 1, d: 1, e: x, f: y} }

func rotate(deg, cx, cy float64) matrix {
	r := deg * math.Pi / 180
	cos, sin := math.Cos(r), math.Sin(r)
	rot := matrix{a: cos, b: sin, c: -sin, d: cos}
	return translate(cx, cy).mul(rot).mul(translate(-cx, -cy))
}

var transformRE = regexp.MustCompile(`^\s*(rotate|translate|scale)\s*\(([^()]*)\)\s*,?`)

// parseTransform parses a transform list of rotate, translate, and scale with
// plain numbers. It returns the canonical text and the matrix; "" is the
// identity. Any other function (matrix, skew) is refused.
func parseTransform(value string) (string, matrix, error) {
	rest := strings.TrimSpace(value)
	m := identity
	var parts []string
	for rest != "" {
		match := transformRE.FindStringSubmatch(rest)
		if match == nil {
			return "", identity, invalid("transform may only use rotate, translate, and scale")
		}
		rest = strings.TrimSpace(rest[len(match[0]):])
		fields := strings.FieldsFunc(match[2], func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' })
		args := make([]float64, 0, len(fields))
		for _, field := range fields {
			v, ok := parseFloat(field)
			if !ok {
				return "", identity, invalid("transform has a bad number")
			}
			args = append(args, v)
		}
		var step matrix
		switch match[1] {
		case "rotate":
			switch len(args) {
			case 1:
				args = append(args, 0, 0)
			case 3:
			default:
				return "", identity, invalid("rotate takes 1 or 3 numbers")
			}
			step = rotate(args[0], args[1], args[2])
		case "translate":
			switch len(args) {
			case 1:
				args = append(args, 0)
			case 2:
			default:
				return "", identity, invalid("translate takes 1 or 2 numbers")
			}
			step = translate(args[0], args[1])
		case "scale":
			switch len(args) {
			case 1:
				args = append(args, args[0])
			case 2:
			default:
				return "", identity, invalid("scale takes 1 or 2 numbers")
			}
			if math.Abs(args[0]) > 10 || math.Abs(args[1]) > 10 {
				return "", identity, invalid("scale is too large")
			}
			step = matrix{a: args[0], d: args[1]}
		}
		m = m.mul(step)
		text := make([]string, len(args))
		for i, v := range args {
			text[i] = formatNumber(v)
		}
		parts = append(parts, match[1]+"("+strings.Join(text, " ")+")")
	}
	return strings.Join(parts, " "), m, nil
}
