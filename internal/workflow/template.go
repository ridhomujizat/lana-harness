package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Scope is the data a template expression can read while a node runs
// (REQ-WF-004). Nodes maps a node id to its latest output; inside a loop body
// that is the output for the current item.
type Scope struct {
	Input     any            // the run input
	Nodes     map[string]any // node id -> output
	Item      any            // current loop item
	ItemIndex int            // current loop item index
	HasItem   bool           // true while rendering inside a loop body
	State     map[string]any // workflow state
	Run       map[string]any // run metadata
	Workflow  map[string]any // workflow metadata
}

// templateRoots are the allowed template path roots.
var templateRoots = map[string]bool{
	"input": true, "nodes": true, "item": true, "item_index": true,
	"state": true, "run": true, "workflow": true,
}

// Render walks v and replaces every {{ ... }} expression with the value it
// resolves to (REQ-WF-004). Strings, map[string]any and []any are walked
// recursively; any other value is returned unchanged. A string that is exactly
// one expression (after trimming) keeps the resolved JSON type. In mixed text a
// nil becomes "", a string is inserted raw, other scalars use fmt and maps or
// slices render as compact JSON.
func Render(v any, sc Scope) (any, error) {
	switch t := v.(type) {
	case string:
		return renderString(t, sc)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			r, err := Render(val, sc)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			r, err := Render(val, sc)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

// renderString renders one string, returning the resolved value with its JSON
// type when the trimmed string is a single expression.
func renderString(s string, sc Scope) (any, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	trimmed := strings.TrimSpace(s)
	if toks, err := parseTemplate(trimmed); err == nil && len(toks) == 1 && toks[0].isExpr {
		return resolveExpr(toks[0].expr, sc)
	}
	toks, err := parseTemplate(s)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, tok := range toks {
		if !tok.isExpr {
			b.WriteString(tok.lit)
			continue
		}
		val, err := resolveExpr(tok.expr, sc)
		if err != nil {
			return nil, err
		}
		b.WriteString(stringify(val))
	}
	return b.String(), nil
}

// stringify renders a resolved value for use inside mixed text. A node output
// map prefers its primary text; a list or a map without one stays compact JSON.
func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case map[string]any:
		if text, ok := primaryText(t); ok {
			return text
		}
		return jsonText(v)
	case []any:
		return jsonText(v)
	default:
		return fmt.Sprint(t)
	}
}

// jsonText encodes v as compact JSON for use inside mixed text, falling back to
// fmt when the value cannot be marshalled.
func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// primaryText returns the first non-empty string among the well-known text keys
// of a node output, so {{ nodes.<id>.output }} inside mixed text yields the
// node's main text (text, markdown, content, ...) instead of the JSON of its
// whole output object.
func primaryText(m map[string]any) (string, bool) {
	for _, key := range []string{"text", "markdown", "content", "output", "result"} {
		if s, ok := m[key].(string); ok && s != "" {
			return s, true
		}
	}
	return "", false
}

// tmplToken is one literal or expression part of a template string.
type tmplToken struct {
	lit    string
	expr   string
	isExpr bool
}

// parseTemplate splits s into literal and {{ }} parts. It reports an unclosed
// template or an empty expression as a syntax error.
func parseTemplate(s string) ([]tmplToken, error) {
	var toks []tmplToken
	for {
		open := strings.Index(s, "{{")
		if open < 0 {
			if s != "" {
				toks = append(toks, tmplToken{lit: s})
			}
			return toks, nil
		}
		if open > 0 {
			toks = append(toks, tmplToken{lit: s[:open]})
		}
		rest := s[open+2:]
		close := strings.Index(rest, "}}")
		if close < 0 {
			return nil, fmt.Errorf("workflow: unclosed template in %q", s)
		}
		expr := strings.TrimSpace(rest[:close])
		if expr == "" {
			return nil, fmt.Errorf("workflow: empty template in %q", s)
		}
		toks = append(toks, tmplToken{expr: expr, isExpr: true})
		s = rest[close+2:]
	}
}

// pathSeg is one step of a template path: a map key or a slice index.
type pathSeg struct {
	isIndex bool
	key     string
	idx     int
}

// parsePath parses root(.segment | [index])* into a root name and its segments.
func parsePath(s string) (string, []pathSeg, error) {
	i := 0
	start := i
	for i < len(s) && isPathIdent(s[i]) {
		i++
	}
	if i == start {
		return "", nil, fmt.Errorf("workflow: bad template path %q", s)
	}
	root := s[start:i]
	var segs []pathSeg
	for i < len(s) {
		switch s[i] {
		case '.':
			i++
			start = i
			for i < len(s) && isPathIdent(s[i]) {
				i++
			}
			if i == start {
				return "", nil, fmt.Errorf("workflow: bad template path %q", s)
			}
			segs = append(segs, pathSeg{key: s[start:i]})
		case '[':
			i++
			start = i
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
			if i == start || i >= len(s) || s[i] != ']' {
				return "", nil, fmt.Errorf("workflow: bad template path %q", s)
			}
			n, err := strconv.Atoi(s[start:i])
			if err != nil {
				return "", nil, fmt.Errorf("workflow: bad template path %q", s)
			}
			segs = append(segs, pathSeg{isIndex: true, idx: n})
			i++
		default:
			return "", nil, fmt.Errorf("workflow: bad template path %q", s)
		}
	}
	return root, segs, nil
}

// isPathIdent reports whether c may appear in a root or dot segment.
func isPathIdent(c byte) bool {
	return c == '_' || c == '-' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// resolveExpr resolves one parsed expression against the scope.
func resolveExpr(expr string, sc Scope) (any, error) {
	root, segs, err := parsePath(expr)
	if err != nil {
		return nil, err
	}
	switch root {
	case "input":
		return walkPath(sc.Input, segs)
	case "nodes":
		return walkNodes(sc.Nodes, segs)
	case "item":
		return walkPath(sc.Item, segs)
	case "item_index":
		if !sc.HasItem {
			return nil, nil
		}
		return walkPath(sc.ItemIndex, segs)
	case "state":
		return walkPath(sc.State, segs)
	case "run":
		return walkPath(sc.Run, segs)
	case "workflow":
		return walkPath(sc.Workflow, segs)
	default:
		return nil, fmt.Errorf("workflow: unknown template root %q", root)
	}
}

// walkNodes resolves a path under the nodes root. The literal "output" segment
// right after the node id is skipped, so both nodes.fetch.status and
// nodes.fetch.output.status read Nodes["fetch"]["status"].
func walkNodes(nodes map[string]any, segs []pathSeg) (any, error) {
	if len(segs) == 0 {
		return normalize(nodes), nil
	}
	if segs[0].isIndex {
		return nil, nil
	}
	out, ok := nodes[segs[0].key]
	if !ok {
		return nil, nil
	}
	rest := segs[1:]
	if len(rest) > 0 && !rest[0].isIndex && rest[0].key == "output" {
		rest = rest[1:]
	}
	return walkPath(out, rest)
}

// walkPath applies segs to cur. A missing key, wrong type or out-of-range index
// yields nil without an error.
func walkPath(cur any, segs []pathSeg) (any, error) {
	v := normalize(cur)
	for _, seg := range segs {
		if seg.isIndex {
			arr, ok := v.([]any)
			if !ok || seg.idx < 0 || seg.idx >= len(arr) {
				return nil, nil
			}
			v = normalize(arr[seg.idx])
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, nil
		}
		nv, ok := m[seg.key]
		if !ok {
			return nil, nil
		}
		v = normalize(nv)
	}
	return v, nil
}

// normalize decodes json.RawMessage and []byte and JSON round-trips typed
// values, so traversal can index them. Scalars pass through unchanged.
func normalize(v any) any {
	switch t := v.(type) {
	case nil, map[string]any, []any:
		return v
	case json.RawMessage:
		return decodeRaw(t)
	case []byte:
		return decodeRaw(t)
	case string, bool, float64, float32,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		json.Number:
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return v
		}
		var out any
		if json.Unmarshal(b, &out) != nil {
			return v
		}
		return out
	}
}

// decodeRaw decodes a JSON blob value; other values pass through.
func decodeRaw(v any) any {
	var b []byte
	switch t := v.(type) {
	case json.RawMessage:
		b = t
	case []byte:
		b = t
	default:
		return v
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return v
	}
	return out
}

// CheckTemplates parses every template in v, using the same walk as Render, and
// returns the first syntax error with the offending text.
func CheckTemplates(v any) error {
	switch t := v.(type) {
	case string:
		if !strings.Contains(t, "{{") {
			return nil
		}
		toks, err := parseTemplate(t)
		if err != nil {
			return err
		}
		for _, tok := range toks {
			if !tok.isExpr {
				continue
			}
			root, _, err := parsePath(tok.expr)
			if err != nil {
				return fmt.Errorf("%w in %q", err, t)
			}
			if !templateRoots[root] {
				return fmt.Errorf("workflow: unknown template root %q in %q", root, t)
			}
		}
	case map[string]any:
		for _, k := range sortedMapKeys(t) {
			if err := CheckTemplates(t[k]); err != nil {
				return err
			}
		}
	case []any:
		for _, val := range t {
			if err := CheckTemplates(val); err != nil {
				return err
			}
		}
	}
	return nil
}

// TemplateRefs returns the node ids referenced as nodes.<id> in v, deduplicated
// and in first-seen order. It is used later to validate graph edges.
func TemplateRefs(v any) []string {
	var refs []string
	collectRefs(v, &refs, map[string]bool{})
	return refs
}

// collectRefs walks v the same way as Render and appends node references.
func collectRefs(v any, refs *[]string, seen map[string]bool) {
	switch t := v.(type) {
	case string:
		if !strings.Contains(t, "{{") {
			return
		}
		toks, err := parseTemplate(t)
		if err != nil {
			return
		}
		for _, tok := range toks {
			if !tok.isExpr {
				continue
			}
			root, segs, err := parsePath(tok.expr)
			if err != nil || root != "nodes" || len(segs) == 0 || segs[0].isIndex {
				continue
			}
			if id := segs[0].key; !seen[id] {
				seen[id] = true
				*refs = append(*refs, id)
			}
		}
	case map[string]any:
		for _, k := range sortedMapKeys(t) {
			collectRefs(t[k], refs, seen)
		}
	case []any:
		for _, val := range t {
			collectRefs(val, refs, seen)
		}
	}
}

// sortedMapKeys returns the keys of m in sorted order so walks over maps are
// deterministic.
func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
