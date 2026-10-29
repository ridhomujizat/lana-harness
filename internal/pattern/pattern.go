// Package pattern mengurai pola izin tool.
package pattern

import (
	"fmt"
	"strings"
)

// ArgKind adalah jenis argumen pola.
type ArgKind int

const (
	ArgCommand ArgKind = iota
	ArgPath
	ArgURL
	ArgAgent
	ArgRaw
)

// Argument adalah nilai yang dicocokkan.
type Argument struct {
	Kind  ArgKind
	Value string
	Alt   string
}

// Pattern adalah pola tool atau tool(arg).
type Pattern struct {
	Raw  string
	Tool string
	Arg  *string
}

func (p Pattern) String() string { return p.Raw }

// ParsePattern mengurai pola.
func ParsePattern(s string) (Pattern, error) {
	if s == "" {
		return Pattern{}, fmt.Errorf("pola kosong")
	}
	open := strings.IndexByte(s, '(')
	if open < 0 {
		if !validTool(s) {
			return Pattern{}, fmt.Errorf("nama tool tidak valid: %s", s)
		}
		return Pattern{Raw: s, Tool: s}, nil
	}
	tool := s[:open]
	if !validTool(tool) {
		return Pattern{}, fmt.Errorf("nama tool tidak valid: %s", tool)
	}
	if !strings.HasSuffix(s, ")") {
		return Pattern{}, fmt.Errorf("tanda kurung tidak tertutup pada posisi %d", open)
	}
	arg := s[open+1 : len(s)-1]
	if err := validateGlob(arg); err != nil {
		return Pattern{}, err
	}
	return Pattern{Raw: s, Tool: tool, Arg: &arg}, nil
}

func validTool(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "mcp__") {
		parts := strings.Split(s, "__")
		return len(parts) == 3 && parts[1] != "" && parts[2] != ""
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && !isAlpha(r) {
				return false
			}
			continue
		}
		if r != '_' && !isAlpha(r) && !isDigit(r) {
			return false
		}
	}
	return true
}

func validateGlob(s string) error {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			if i+1 >= len(s) {
				return fmt.Errorf("escape di akhir pola")
			}
			i++
		}
	}
	return nil
}

// Match mencocokkan tool dan nilai argumen.
func (p Pattern) Match(tool string, arg Argument) bool {
	if p.Tool != tool {
		return false
	}
	if p.Arg == nil {
		return true
	}
	if arg.Kind != ArgPath {
		return globMatch(*p.Arg, arg.Value, false)
	}
	if globMatch(*p.Arg, arg.Value, true) {
		return true
	}
	return arg.Alt != "" && globMatch(*p.Arg, arg.Alt, true)
}

// MatchBoth mencocokkan nilai path dan hasil symlink sekaligus.
func (p Pattern) MatchBoth(tool string, arg Argument) bool {
	if p.Tool != tool {
		return false
	}
	if p.Arg == nil {
		return true
	}
	if !globMatch(*p.Arg, arg.Value, true) {
		return false
	}
	return arg.Alt == "" || globMatch(*p.Arg, arg.Alt, true)
}

func globMatch(pattern, value string, pathMode bool) bool {
	p := []rune(pattern)
	v := []rune(value)
	var match func(pi, vi int) bool
	memo := map[[2]int]bool{}
	seen := map[[2]int]bool{}
	match = func(pi, vi int) bool {
		key := [2]int{pi, vi}
		if seen[key] {
			return memo[key]
		}
		seen[key] = true
		ok := false
		switch {
		case pi == len(p):
			ok = vi == len(v)
		case p[pi] == '\\' && pi+1 < len(p):
			ok = vi < len(v) && v[vi] == p[pi+1] && match(pi+2, vi+1)
		case p[pi] == '*':
			if pi+1 < len(p) && p[pi+1] == '*' {
				ok = match(pi+2, vi) || (vi < len(v) && (pathMode || v[vi] != '/') && match(pi, vi+1))
			} else {
				ok = match(pi+1, vi) || (vi < len(v) && (!pathMode || v[vi] != '/') && match(pi, vi+1))
			}
		case p[pi] == '?':
			ok = vi < len(v) && (!pathMode || v[vi] != '/') && match(pi+1, vi+1)
		default:
			ok = vi < len(v) && p[pi] == v[vi] && match(pi+1, vi+1)
		}
		memo[key] = ok
		return ok
	}
	return match(0, 0)
}
func isAlpha(r rune) bool { return r >= 'a' && r <= 'z' }
func isDigit(r rune) bool { return r >= '0' && r <= '9' }
