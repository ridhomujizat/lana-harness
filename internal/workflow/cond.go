package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Compare evaluates "left op right" for the if and switch nodes (REQ-WF-004).
// The allowed ops are ==, !=, >, >=, <, <=, contains, not_contains, exists,
// not_exists, truthy and falsy. Numbers compare numerically when both sides are
// numbers or numeric strings; otherwise == and != compare JSON-normalized
// values and ordering compares strings. An unknown op returns an error.
func Compare(left any, op string, right any) (bool, error) {
	switch op {
	case "==":
		if l, lok := asNumber(left); lok {
			if r, rok := asNumber(right); rok {
				return l == r, nil
			}
		}
		return jsonEqual(left, right), nil
	case "!=":
		eq, err := Compare(left, "==", right)
		return !eq, err
	case ">", ">=", "<", "<=":
		lf, lok := asNumber(left)
		rf, rok := asNumber(right)
		if lok && rok {
			return compareFloats(lf, rf, op), nil
		}
		return compareStrings(stringOf(left), stringOf(right), op), nil
	case "contains":
		return containsValue(left, right), nil
	case "not_contains":
		return !containsValue(left, right), nil
	case "exists":
		return left != nil, nil
	case "not_exists":
		return left == nil, nil
	case "truthy":
		return truthy(left), nil
	case "falsy":
		return !truthy(left), nil
	default:
		return false, fmt.Errorf("workflow: unknown comparison op %q", op)
	}
}

// compareFloats applies an ordering op to two numbers.
func compareFloats(l, r float64, op string) bool {
	switch op {
	case ">":
		return l > r
	case ">=":
		return l >= r
	case "<":
		return l < r
	default:
		return l <= r
	}
}

// compareStrings applies an ordering op to two strings.
func compareStrings(l, r, op string) bool {
	switch op {
	case ">":
		return l > r
	case ">=":
		return l >= r
	case "<":
		return l < r
	default:
		return l <= r
	}
}

// stringOf renders a value as a plain string for string comparison and lookups.
func stringOf(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

// asNumber returns v as a float64 when it is a number or a numeric string.
func asNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int8:
		return float64(t), true
	case int16:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint8:
		return float64(t), true
	case uint16:
		return float64(t), true
	case uint32:
		return float64(t), true
	case uint64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// asFloat returns v as a float64 when it is a JSON number type; strings do not
// count, so it is used for schema checks where the JSON type matters.
func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int8:
		return float64(t), true
	case int16:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint8:
		return float64(t), true
	case uint16:
		return float64(t), true
	case uint32:
		return float64(t), true
	case uint64:
		return float64(t), true
	default:
		return 0, false
	}
}

// containsValue implements the contains op: substring for strings, element
// membership for slices and key membership for maps.
func containsValue(left, right any) bool {
	switch l := normalize(left).(type) {
	case string:
		return strings.Contains(l, stringOf(right))
	case []any:
		for _, e := range l {
			if jsonEqual(e, right) {
				return true
			}
		}
		return false
	case map[string]any:
		_, ok := l[stringOf(right)]
		return ok
	default:
		return false
	}
}

// truthy reports whether v counts as true: nil, false, 0, "", "false" and empty
// slices or maps are false.
func truthy(v any) bool {
	switch t := normalize(v).(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != "" && t != "false"
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	case json.Number:
		f, err := t.Float64()
		return err != nil || f != 0
	default:
		if f, ok := asNumber(t); ok {
			return f != 0
		}
		return true
	}
}

// jsonValue normalizes v through a JSON round trip so two values compare by
// their JSON form.
func jsonValue(v any) any {
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

// jsonEqual reports whether two values are equal after a JSON round trip.
func jsonEqual(a, b any) bool {
	return reflect.DeepEqual(jsonValue(a), jsonValue(b))
}

// ExtractJSON returns the first JSON object or array found in text: the whole
// trimmed text, then a ```json / ``` fence, then the span from the first { to
// its matching }. It is used to parse guardian replies. Numbers decode as
// float64.
func ExtractJSON(text string) (any, error) {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil, errors.New("workflow: no JSON found in text")
	}
	if v, ok := decodeJSONObject(t); ok {
		return v, nil
	}
	if content, ok := firstFence(t); ok {
		if v, ok := decodeJSONObject(strings.TrimSpace(content)); ok {
			return v, nil
		}
	}
	if span, ok := firstBraceSpan(t); ok {
		if v, ok := decodeJSONObject(span); ok {
			return v, nil
		}
	}
	return nil, errors.New("workflow: no JSON object or array found in text")
}

// decodeJSONObject decodes s and reports whether it is an object or an array.
func decodeJSONObject(s string) (any, bool) {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return nil, false
	}
	switch v.(type) {
	case map[string]any, []any:
		return v, true
	default:
		return nil, false
	}
}

// firstFence returns the content of the first ``` fence in s.
func firstFence(s string) (string, bool) {
	i := strings.Index(s, "```")
	if i < 0 {
		return "", false
	}
	rest := s[i+3:]
	nl := strings.IndexByte(rest, '\n')
	body := rest
	if nl >= 0 {
		body = rest[nl+1:]
	}
	end := strings.Index(body, "```")
	if end < 0 {
		return "", false
	}
	return body[:end], true
}

// firstBraceSpan returns the substring from the first { to its matching },
// ignoring braces inside JSON strings.
func firstBraceSpan(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// schemaFieldType returns the type name of one schema field spec: a type name
// optionally followed by a free note (REQ-WF-005).
func schemaFieldType(spec string) string {
	want := spec
	if i := strings.IndexByte(want, ' '); i >= 0 {
		want = want[:i]
	}
	return strings.TrimSpace(want)
}

// CheckSchema validates v against an output_schema: every schema field is a
// type string (optionally followed by a free note) and every field is required
// (REQ-WF-005). It joins every problem into one error.
func CheckSchema(schema map[string]any, v any) error {
	m, ok := normalize(v).(map[string]any)
	if !ok {
		return fmt.Errorf("workflow: output_schema wants an object, got %s", typeName(normalize(v)))
	}
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var problems []string
	for _, k := range keys {
		spec, ok := schema[k].(string)
		if !ok {
			continue
		}
		want := schemaFieldType(spec)
		val, present := m[k]
		if !present {
			problems = append(problems, fmt.Sprintf("field %q: missing", k))
			continue
		}
		if !schemaTypeOK(want, val) {
			problems = append(problems, fmt.Sprintf("field %q: want %s, got %s", k, want, typeName(val)))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// schemaTypeOK checks one value against one schema type name.
func schemaTypeOK(want string, v any) bool {
	switch want {
	case "string":
		_, ok := v.(string)
		return ok
	case "integer":
		f, ok := asFloat(v)
		return ok && f == math.Trunc(f)
	case "number":
		_, ok := asFloat(v)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		_, ok := normalize(v).(map[string]any)
		return ok
	case "array":
		_, ok := normalize(v).([]any)
		return ok
	case "string[]":
		return schemaArrayOK(v, "string")
	case "integer[]":
		return schemaArrayOK(v, "integer")
	case "number[]":
		return schemaArrayOK(v, "number")
	case "object[]":
		return schemaArrayOK(v, "object")
	default:
		return false
	}
}

// schemaArrayOK checks that v is an array whose elements all match elem.
func schemaArrayOK(v any, elem string) bool {
	arr, ok := normalize(v).([]any)
	if !ok {
		return false
	}
	for _, e := range arr {
		if !schemaTypeOK(elem, e) {
			return false
		}
	}
	return true
}

// CoerceToSchema returns a copy of v (which must be a map) with its fields
// converted to the JSON types named by an output_schema, so a near-miss answer
// still validates (REQ-WF-005, REQ-WF-008): a list of lines where a string is
// wanted, a numeric string where a number is wanted, and so on. Fields the
// schema does not name are kept untouched. It then runs CheckSchema on the
// result and returns its error when the answer is still invalid.
func CoerceToSchema(schema map[string]any, v any) (any, error) {
	m, ok := normalize(v).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("workflow: output_schema wants an object, got %s", typeName(normalize(v)))
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		out[k] = val
	}
	for _, k := range sortedKeys(schema) {
		spec, ok := schema[k].(string)
		if !ok {
			continue
		}
		val, present := m[k]
		if !present {
			continue
		}
		if c, ok := coerceToType(schemaFieldType(spec), val); ok {
			out[k] = c
		}
	}
	if err := CheckSchema(schema, out); err != nil {
		return nil, err
	}
	return out, nil
}

// coerceToType converts one value to the JSON type named want, reporting whether
// it applies (REQ-WF-005).
func coerceToType(want string, v any) (any, bool) {
	switch want {
	case "string":
		return coerceString(v)
	case "integer":
		return coerceNumber(v, true)
	case "number":
		return coerceNumber(v, false)
	case "boolean":
		return coerceBool(v)
	case "string[]":
		if s, ok := v.(string); ok {
			return []any{s}, true
		}
	case "integer[]":
		return coerceNumberArray(v, true)
	case "number[]":
		return coerceNumberArray(v, false)
	}
	return v, false
}

// coerceString joins an array of strings into one newline-separated string with
// each element trimmed, and formats a number or bool with fmt (REQ-WF-005).
func coerceString(v any) (any, bool) {
	switch t := v.(type) {
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return v, false
			}
			parts = append(parts, strings.TrimSpace(s))
		}
		return strings.Join(parts, "\n"), true
	case float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, json.Number, bool:
		return fmt.Sprint(t), true
	}
	return v, false
}

// coerceNumber converts a numeric string to a number; with wantInt it also
// requires an integral value, so "8" and 8.0 coerce while 7.5 stays a number
// (REQ-WF-005).
func coerceNumber(v any, wantInt bool) (any, bool) {
	if s, ok := v.(string); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return v, false
		}
		if wantInt && f != math.Trunc(f) {
			return v, false
		}
		return f, true
	}
	if f, ok := asFloat(v); ok {
		if wantInt && f != math.Trunc(f) {
			return v, false
		}
		return f, true
	}
	return v, false
}

// coerceNumberArray converts the numeric strings of an array per element
// (REQ-WF-005).
func coerceNumberArray(v any, wantInt bool) (any, bool) {
	arr, ok := normalize(v).([]any)
	if !ok {
		return v, false
	}
	out := make([]any, len(arr))
	for i, e := range arr {
		if c, ok := coerceNumber(e, wantInt); ok {
			out[i] = c
		} else {
			out[i] = e
		}
	}
	return out, true
}

// coerceBool reads the strings "true" and "false" (REQ-WF-005).
func coerceBool(v any) (any, bool) {
	if s, ok := v.(string); ok {
		switch strings.TrimSpace(s) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return v, false
}

// typeName names the JSON type of v for schema error messages.
func typeName(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		return "number"
	case float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return "number"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	default:
		switch normalize(t).(type) {
		case map[string]any:
			return "object"
		case []any:
			return "array"
		default:
			return "unknown"
		}
	}
}
