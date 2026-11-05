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

// SplitCommand splits a shell command into its simple commands for permission
// matching. It splits on unquoted ;, &&, ||, |, & (background), and newline,
// respecting single quotes (no escapes inside), double quotes, and backslash
// escapes outside single quotes. Each segment is trimmed and empty segments are
// dropped.
//
// The second result is false when the command cannot be safely analyzed. That
// happens when the command uses a command substitution ($( or a backtick), a
// process substitution (<( or >(), an unbalanced quote, or an output or input
// redirection other than the safe /dev/null and file-descriptor forms. The
// segments are still returned when analyzable is false.
func SplitCommand(cmd string) (segments []string, analyzable bool) {
	analyzable = true
	var seg strings.Builder
	flush := func() {
		if s := strings.TrimSpace(seg.String()); s != "" {
			segments = append(segments, s)
		}
		seg.Reset()
	}
	var quote byte
	i := 0
	n := len(cmd)
	for i < n {
		c := cmd[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			seg.WriteByte(c)
			i++
			continue
		}
		if quote == '"' {
			if c == '\\' && i+1 < n {
				seg.WriteByte(c)
				seg.WriteByte(cmd[i+1])
				i += 2
				continue
			}
			if c == '`' || (c == '$' && i+1 < n && cmd[i+1] == '(') {
				analyzable = false
			}
			if c == '"' {
				quote = 0
			}
			seg.WriteByte(c)
			i++
			continue
		}
		switch {
		case c == '\\' && i+1 < n:
			seg.WriteByte(c)
			seg.WriteByte(cmd[i+1])
			i += 2
		case c == '\'':
			quote = '\''
			seg.WriteByte(c)
			i++
		case c == '"':
			quote = '"'
			seg.WriteByte(c)
			i++
		case c == '`':
			analyzable = false
			seg.WriteByte(c)
			i++
		case c == '$' && i+1 < n && cmd[i+1] == '(':
			analyzable = false
			seg.WriteString("$(")
			i += 2
		case (c == '<' || c == '>') && i+1 < n && cmd[i+1] == '(':
			analyzable = false
			seg.WriteByte(c)
			seg.WriteByte('(')
			i += 2
		case c == '<' || c == '>':
			if !safeRedirect(seg.String(), cmd[i:]) {
				analyzable = false
			}
			// Consume the operator so a following & in >& is not read as a
			// background separator.
			seg.WriteByte(c)
			if i+1 < n && (cmd[i+1] == '>' || cmd[i+1] == '&') {
				seg.WriteByte(cmd[i+1])
				i += 2
			} else {
				i++
			}
		case c == ';' || c == '\n':
			flush()
			i++
		case c == '&' && i+1 < n && cmd[i+1] == '&':
			flush()
			i += 2
		case c == '&' && i+1 < n && cmd[i+1] == '>':
			// &> is a redirection, not a background separator.
			seg.WriteByte(c)
			i++
		case c == '&':
			flush()
			i++
		case c == '|' && i+1 < n && cmd[i+1] == '|':
			flush()
			i += 2
		case c == '|':
			flush()
			i++
		default:
			seg.WriteByte(c)
			i++
		}
	}
	flush()
	if quote != 0 {
		analyzable = false
	}
	return segments, analyzable
}

// safeRedirect reports whether the redirection whose operator starts at rest
// (which begins with < or >) is one of the safe forms: 2>&1, 1>&2, >&2,
// >/dev/null, > /dev/null, 2>/dev/null, 2> /dev/null, and &>/dev/null. seg is
// the command text before the operator, used to read an optional descriptor
// prefix.
func safeRedirect(seg, rest string) bool {
	if rest[0] != '>' {
		return false
	}
	prefix := byte(0)
	if len(seg) > 0 {
		prefix = seg[len(seg)-1]
	}
	switch {
	case strings.HasPrefix(rest, ">&1") && prefix == '2':
		return redirectBoundary(rest, len(">&1"))
	case strings.HasPrefix(rest, ">&2") && (prefix == '1' || !isDigit(rune(prefix))):
		return redirectBoundary(rest, len(">&2"))
	}
	for _, form := range []string{">/dev/null", "> /dev/null"} {
		if strings.HasPrefix(rest, form) {
			return redirectBoundary(rest, len(form))
		}
	}
	return false
}

// redirectBoundary reports whether position i in s is the end or a separator.
func redirectBoundary(s string, i int) bool {
	if i >= len(s) {
		return true
	}
	switch s[i] {
	case ' ', '\t', '\n', ';', '|', '&', '>', '<':
		return true
	}
	return false
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
