// Package pattern parses tool permission patterns.
package pattern

import (
	"fmt"
	"strings"
)

// ArgKind is the kind of a pattern argument.
type ArgKind int

const (
	ArgCommand ArgKind = iota
	ArgPath
	ArgURL
	ArgAgent
	ArgRaw
)

// Argument is the value to match against.
type Argument struct {
	Kind  ArgKind
	Value string
	Alt   string
}

// Pattern is a tool pattern: tool or tool(arg).
type Pattern struct {
	Raw  string
	Tool string
	Arg  *string
}

func (p Pattern) String() string { return p.Raw }

// ParsePattern parses a pattern.
func ParsePattern(s string) (Pattern, error) {
	if s == "" {
		return Pattern{}, fmt.Errorf("pattern is empty")
	}
	open := strings.IndexByte(s, '(')
	if open < 0 {
		if !validTool(s) {
			return Pattern{}, fmt.Errorf("invalid tool name: %s", s)
		}
		return Pattern{Raw: s, Tool: s}, nil
	}
	tool := s[:open]
	if !validTool(tool) {
		return Pattern{}, fmt.Errorf("invalid tool name: %s", tool)
	}
	if !strings.HasSuffix(s, ")") {
		return Pattern{}, fmt.Errorf("unclosed parenthesis at position %d", open)
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
				return fmt.Errorf("escape at end of pattern")
			}
			i++
		}
	}
	return nil
}

// matchTool reports whether the pattern's tool matches name. MCP tool patterns
// may use globs, for example mcp__github__*.
func (p Pattern) matchTool(name string) bool {
	if p.Tool == name {
		return true
	}
	return globMatch(p.Tool, name, false)
}

// Match reports whether the tool and argument value match.
func (p Pattern) Match(tool string, arg Argument) bool {
	if !p.matchTool(tool) {
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

// MatchBoth matches both the path value and the symlink target at once.
func (p Pattern) MatchBoth(tool string, arg Argument) bool {
	if !p.matchTool(tool) {
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
