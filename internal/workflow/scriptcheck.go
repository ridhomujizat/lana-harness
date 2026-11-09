package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Drift codes name the first contract step that rejected a script_parse output
// (REQ-SP-003).
const (
	DriftScript = "script_error"
	DriftSchema = "schema_drift"
	DriftEmpty  = "empty_drift"
	DriftItem   = "item_drift"
)

// DriftError reports a contract failure of a script_parse output (REQ-SP-003):
// Code is one of the Drift* constants and Message describes the failure.
type DriftError struct {
	Code    string
	Message string
}

// Error renders a drift as "<code>: <message>".
func (e *DriftError) Error() string {
	return e.Code + ": " + e.Message
}

// CheckContract validates a script_parse output v against the node params
// (REQ-SP-003). The params are output_schema (a map of field to type string, as
// spec 33), item_schema (a map of "<object[] field>" to a per-element schema)
// and allow_empty (field names, or "<field>.<item field>" for item fields). It
// coerces v with CoerceToSchema, applies the non-empty rule to the top level and
// to each item element, and returns the coerced output. The first failing step
// wins and is reported as a *DriftError.
func CheckContract(params map[string]any, v any) (any, *DriftError) {
	if _, ok := normalize(v).(map[string]any); !ok {
		return nil, &DriftError{
			Code:    DriftScript,
			Message: fmt.Sprintf("output is %s, want an object", typeName(normalize(v))),
		}
	}
	schema := asMapParam(params["output_schema"])
	out, err := CoerceToSchema(schema, v)
	if err != nil {
		return nil, &DriftError{Code: DriftSchema, Message: err.Error()}
	}
	m, _ := out.(map[string]any)
	allow := allowEmptyNames(params["allow_empty"])
	if de := checkEmptyTop(schema, m, allow); de != nil {
		return nil, de
	}
	if de := checkItemSchema(params["item_schema"], m, allow); de != nil {
		return nil, de
	}
	return m, nil
}

// checkEmptyTop applies the non-empty rule to the top level of out (REQ-SP-003):
// a string that trims to "" and an array of length 0 are empty, unless the field
// is named in allow.
func checkEmptyTop(schema map[string]any, out map[string]any, allow map[string]bool) *DriftError {
	for _, k := range sortedKeys(schema) {
		spec, ok := schema[k].(string)
		if !ok {
			continue
		}
		if allow[k] {
			continue
		}
		val, present := out[k]
		if !present {
			continue
		}
		if fieldEmpty(schemaFieldType(spec), val) {
			return &DriftError{Code: DriftEmpty, Message: fmt.Sprintf("field %q: empty", k)}
		}
	}
	return nil
}

// checkItemSchema applies the item part of the contract (REQ-SP-003): for every
// item_schema entry the named array's elements are coerced to the entry schema,
// checked against it and held to the same non-empty rule. CoerceToSchema runs
// the CheckSchema pass per element; the coerced elements replace the originals
// in out so the returned output carries them.
func checkItemSchema(itemSchemaParam any, out map[string]any, allow map[string]bool) *DriftError {
	itemSchema := asMapParam(itemSchemaParam)
	for _, field := range sortedKeys(itemSchema) {
		entry := asMapParam(itemSchema[field])
		arr, ok := normalize(out[field]).([]any)
		if !ok {
			continue
		}
		arr = append([]any(nil), arr...) // never mutate the caller's input
		for i, elem := range arr {
			coerced, err := CoerceToSchema(entry, elem)
			if err != nil {
				return &DriftError{Code: DriftItem, Message: fmt.Sprintf("field %q item %d: %v", field, i, err)}
			}
			ce, ok := coerced.(map[string]any)
			if !ok {
				return &DriftError{Code: DriftItem, Message: fmt.Sprintf("field %q item %d: not an object", field, i)}
			}
			if de := checkEmptyItem(field, i, entry, ce, allow); de != nil {
				return de
			}
			arr[i] = ce
		}
		out[field] = arr
	}
	return nil
}

// checkEmptyItem applies the non-empty rule to the fields of one coerced array
// element (REQ-SP-003). A field is skipped when allow names "<field>.<item>".
func checkEmptyItem(field string, i int, schema, elem map[string]any, allow map[string]bool) *DriftError {
	for _, k := range sortedKeys(schema) {
		spec, ok := schema[k].(string)
		if !ok {
			continue
		}
		if allow[field+"."+k] {
			continue
		}
		val, present := elem[k]
		if !present {
			continue
		}
		if fieldEmpty(schemaFieldType(spec), val) {
			return &DriftError{
				Code:    DriftItem,
				Message: fmt.Sprintf("field %q item %d field %q: empty", field, i, k),
			}
		}
	}
	return nil
}

// fieldEmpty reports whether v is empty per the non-empty rule of REQ-SP-003: a
// string that trims to "" or an array of length 0.
func fieldEmpty(want string, v any) bool {
	switch {
	case want == "string":
		s, ok := v.(string)
		return ok && strings.TrimSpace(s) == ""
	case want == "array" || strings.HasSuffix(want, "[]"):
		arr, ok := normalize(v).([]any)
		return ok && len(arr) == 0
	default:
		return false
	}
}

// asMapParam reads a param that holds a schema object; other values yield nil.
func asMapParam(v any) map[string]any {
	if m, ok := normalize(v).(map[string]any); ok {
		return m
	}
	return nil
}

// allowEmptyNames parses the allow_empty param (REQ-SP-003): a list of field
// names, or "<field>.<item field>" for item fields. []any of strings and
// []string are accepted; other entries are ignored.
func allowEmptyNames(v any) map[string]bool {
	out := map[string]bool{}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				out[s] = true
			}
		}
	case []string:
		for _, s := range t {
			out[s] = true
		}
	}
	return out
}

// agreementLenientMin is the shortest string that may match by substring in
// Agreement, so short fragments do not count as agreement.
const agreementLenientMin = 8

// leafValue is one leaf of a value tree in Agreement: a lowercased string or a
// number as float64.
type leafValue struct {
	isNum bool
	num   float64
	str   string
}

// Agreement scores a script output against a fallback output (REQ-SP-007 and the
// "Agreement" definition): the share of the fallback's leaf values (strings
// trimmed and lowercased, numbers as float64) that also appear in the script
// output, plus the number of expected leaves. Empty strings, booleans and nil are
// not leaves; maps and arrays are walked recursively. A string matches by exact
// equality, or, when both it and the candidate are at least agreementLenientMin
// characters long, when one contains the other. The score is 1.0 when there are
// no expected leaves.
func Agreement(expected, got any) (float64, int) {
	var want []leafValue
	collectLeaves(expected, &want)
	if len(want) == 0 {
		return 1, 0
	}
	var gotLeaves []leafValue
	collectLeaves(got, &gotLeaves)
	nums := map[float64]bool{}
	strs := map[string]bool{}
	for _, l := range gotLeaves {
		if l.isNum {
			nums[l.num] = true
		} else {
			strs[l.str] = true
		}
	}
	found := 0
	for _, l := range want {
		switch {
		case l.isNum:
			if nums[l.num] {
				found++
			}
		case strs[l.str]:
			found++
		case lenientStringMatch(l.str, strs):
			found++
		}
	}
	return float64(found) / float64(len(want)), len(want)
}

// collectLeaves appends the leaf values of v to leaves (REQ-SP-007): strings are
// trimmed and lowercased with empty strings skipped, numbers are canonicalized to
// float64, and booleans and nil are skipped.
func collectLeaves(v any, leaves *[]leafValue) {
	switch t := normalize(v).(type) {
	case map[string]any:
		for _, k := range sortedKeys(t) {
			collectLeaves(t[k], leaves)
		}
	case []any:
		for _, e := range t {
			collectLeaves(e, leaves)
		}
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		if s != "" {
			*leaves = append(*leaves, leafValue{str: s})
		}
	case json.Number, float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		if f, ok := asFloat(t); ok {
			*leaves = append(*leaves, leafValue{isNum: true, num: f})
		}
	}
}

// lenientStringMatch reports whether an expected string matches a got string by
// substring, both at least agreementLenientMin characters long (REQ-SP-007).
func lenientStringMatch(s string, gotStrs map[string]bool) bool {
	if len(s) < agreementLenientMin {
		return false
	}
	for g := range gotStrs {
		if len(g) < agreementLenientMin {
			continue
		}
		if strings.Contains(g, s) || strings.Contains(s, g) {
			return true
		}
	}
	return false
}
