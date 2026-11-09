package workflow

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ruleURLPattern matches the value of the url rule (REQ-DG-003).
var ruleURLPattern = regexp.MustCompile(`^https?://\S+$`)

// ruleBoundPattern matches the min:N and max:N tokens, with N a non-negative
// integer written in digits only (REQ-DG-003).
var ruleBoundPattern = regexp.MustCompile(`^(?:min|max):[0-9]+$`)

// RuleRef names one rule token written on a schema field (REQ-DG-004). Path is
// "f" for a top-level output_schema field, or "f.k" for field k of the item
// schema of array field f; Token is the token as written, e.g. ">0" or "min:3".
type RuleRef struct {
	Path  string
	Token string
}

// ParseTypeSpec splits a schema type string (REQ-DG-003): the first word is the
// type, the words after it that are rule tokens are the rules, and the rest,
// joined by single spaces, is the free note. Rules stop at the first word that
// is not a rule token; the returned rules keep their order and duplicates.
func ParseTypeSpec(spec string) (typ string, rules []string, note string) {
	words := strings.Fields(spec)
	if len(words) == 0 {
		return "", nil, ""
	}
	typ = words[0]
	i := 1
	for i < len(words) && isRuleToken(words[i]) {
		rules = append(rules, words[i])
		i++
	}
	return typ, rules, strings.Join(words[i:], " ")
}

// isRuleToken reports whether tok is one of the rule tokens of REQ-DG-003: >0,
// >=0, url, min:N or max:N with N a non-negative integer.
func isRuleToken(tok string) bool {
	switch tok {
	case ">0", ">=0", "url":
		return true
	default:
		return ruleBoundPattern.MatchString(tok)
	}
}

// ruleFits reports whether a rule token may be written on a type name
// (REQ-DG-003): >0 and >=0 on integer and number, url on string, and min:N and
// max:N on string, integer, number and array types.
func ruleFits(token, typ string) bool {
	switch token {
	case ">0", ">=0":
		return typ == "integer" || typ == "number"
	case "url":
		return typ == "string"
	}
	if ruleBoundPattern.MatchString(token) {
		return typ == "string" || typ == "integer" || typ == "number" || isArrayType(typ)
	}
	return false
}

// isArrayType reports whether typ is an array field type: "array" or any name
// ending in "[]".
func isArrayType(typ string) bool {
	return typ == "array" || strings.HasSuffix(typ, "[]")
}

// ruleBound splits a min:N or max:N token into its op ("min" or "max") and N.
func ruleBound(token string) (op string, n int, ok bool) {
	i := strings.IndexByte(token, ':')
	if i < 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(token[i+1:])
	if err != nil {
		return "", 0, false
	}
	return token[:i], n, true
}

// ruleHolds reports whether value satisfies a rule token, given the field type
// (REQ-DG-004). skipEmpty skips the rule for an empty string in a field allowed
// by allow_empty. An unknown token holds.
func ruleHolds(token, typ string, value any, skipEmpty bool) bool {
	if skipEmpty {
		if s, ok := normalize(value).(string); ok && strings.TrimSpace(s) == "" {
			return true
		}
	}
	switch token {
	case ">0":
		f, ok := asFloat(value)
		return ok && f > 0
	case ">=0":
		f, ok := asFloat(value)
		return ok && f >= 0
	case "url":
		s, ok := normalize(value).(string)
		return ok && ruleURLPattern.MatchString(s)
	}
	if op, n, ok := ruleBound(token); ok {
		return ruleBoundHolds(op, n, typ, value)
	}
	return true
}

// ruleBoundHolds applies min:N or max:N: the rune length for a string, the
// length for an array, and the value for a number (REQ-DG-003).
func ruleBoundHolds(op string, n int, typ string, value any) bool {
	var have int
	switch {
	case typ == "string":
		s, _ := normalize(value).(string)
		have = utf8.RuneCountInString(s)
	case isArrayType(typ):
		arr, _ := normalize(value).([]any)
		have = len(arr)
	default:
		f, ok := asFloat(value)
		if !ok {
			return true
		}
		if op == "min" {
			return f >= float64(n)
		}
		return f <= float64(n)
	}
	if op == "min" {
		return have >= n
	}
	return have <= n
}

// RuleProblems returns one problem per rule token written on a field type it
// does not fit (REQ-DG-003, the validation issue bad_rule). Fields are walked
// top-level first, then item fields, each in sorted order; a nil result means
// every rule fits.
func RuleProblems(schema, itemSchema map[string]any) []string {
	var problems []string
	for _, field := range sortedKeys(schema) {
		spec, ok := schema[field].(string)
		if !ok {
			continue
		}
		typ, rules, _ := ParseTypeSpec(spec)
		for _, tok := range rules {
			if !ruleFits(tok, typ) {
				problems = append(problems, fmt.Sprintf("field %q: rule %q does not fit %s", field, tok, typ))
			}
		}
	}
	for _, field := range sortedKeys(itemSchema) {
		entry := asMapParam(itemSchema[field])
		if entry == nil {
			continue
		}
		for _, key := range sortedKeys(entry) {
			spec, ok := entry[key].(string)
			if !ok {
				continue
			}
			typ, rules, _ := ParseTypeSpec(spec)
			for _, tok := range rules {
				if !ruleFits(tok, typ) {
					problems = append(problems, fmt.Sprintf("field %q: rule %q does not fit %s", field+"."+key, tok, typ))
				}
			}
		}
	}
	return problems
}

// ruleViolation is one failed rule with the value that failed it, so CheckRules
// can render the item index and the offending value in the message.
type ruleViolation struct {
	ref   RuleRef
	item  int // 0-based array index, or -1 for a top-level field
	value any
	token string
	typ   string
}

// RuleViolations returns every rule a script output breaks, in walk order
// (REQ-DG-004). A missing field, a value of the wrong JSON type and a
// non-object array element are skipped, because the contract reports them. It
// returns nil when no rule fails.
func RuleViolations(params map[string]any, out any) []RuleRef {
	vs := collectRuleViolations(params, out)
	if len(vs) == 0 {
		return nil
	}
	refs := make([]RuleRef, len(vs))
	for i := range vs {
		refs[i] = vs[i].ref
	}
	return refs
}

// collectRuleViolations walks the output_schema and item_schema of params like
// CheckContract and collects the failed rules in a deterministic order.
func collectRuleViolations(params map[string]any, out any) []ruleViolation {
	m, ok := normalize(out).(map[string]any)
	if !ok {
		return nil
	}
	schema := asMapParam(params["output_schema"])
	itemSchema := asMapParam(params["item_schema"])
	allow := allowEmptyNames(params["allow_empty"])
	var vs []ruleViolation
	for _, field := range sortedKeys(schema) {
		spec, ok := schema[field].(string)
		if !ok {
			continue
		}
		typ, rules, _ := ParseTypeSpec(spec)
		if len(rules) == 0 {
			continue
		}
		val, present := m[field]
		if !present || !schemaTypeOK(typ, val) {
			continue
		}
		for _, tok := range rules {
			if ruleHolds(tok, typ, val, allow[field]) {
				continue
			}
			vs = append(vs, ruleViolation{ref: RuleRef{Path: field, Token: tok}, item: -1, value: val, token: tok, typ: typ})
		}
	}
	for _, field := range sortedKeys(itemSchema) {
		entry := asMapParam(itemSchema[field])
		if entry == nil {
			continue
		}
		arr, ok := normalize(m[field]).([]any)
		if !ok {
			continue
		}
		for i, elem := range arr {
			em, ok := normalize(elem).(map[string]any)
			if !ok {
				continue
			}
			for _, key := range sortedKeys(entry) {
				spec, ok := entry[key].(string)
				if !ok {
					continue
				}
				typ, rules, _ := ParseTypeSpec(spec)
				if len(rules) == 0 {
					continue
				}
				val, present := em[key]
				if !present || !schemaTypeOK(typ, val) {
					continue
				}
				path := field + "." + key
				for _, tok := range rules {
					if ruleHolds(tok, typ, val, allow[path]) {
						continue
					}
					vs = append(vs, ruleViolation{ref: RuleRef{Path: path, Token: tok}, item: i, value: val, token: tok, typ: typ})
				}
			}
		}
	}
	return vs
}

// CheckRules reports the first rule a script output breaks as a *DriftError with
// code rule_drift, or nil when every rule holds (REQ-DG-004).
func CheckRules(params map[string]any, out any) *DriftError {
	vs := collectRuleViolations(params, out)
	if len(vs) == 0 {
		return nil
	}
	return &DriftError{Code: DriftRule, Message: violationMessage(vs[0])}
}

// violationMessage renders one failed rule, naming the field path, the item
// index for an item field, and the offending value or length.
func violationMessage(v ruleViolation) string {
	where := fmt.Sprintf("field %q", v.ref.Path)
	if v.item >= 0 {
		where = fmt.Sprintf("field %q item %d", v.ref.Path, v.item)
	}
	if _, _, ok := ruleBound(v.token); ok && (v.typ == "string" || isArrayType(v.typ)) {
		return fmt.Sprintf("%s: length %d is not %s", where, ruleLength(v.typ, v.value), v.token)
	}
	return fmt.Sprintf("%s: %v is not %s", where, v.value, v.token)
}

// ruleLength returns the counted length a min/max rule measures: the rune count
// of a string or the element count of an array.
func ruleLength(typ string, value any) int {
	if typ == "string" {
		s, _ := normalize(value).(string)
		return utf8.RuneCountInString(s)
	}
	arr, _ := normalize(value).([]any)
	return len(arr)
}

// DropRules returns a deep copy of params with the tokens of the given refs
// removed from their field type strings (REQ-DG-004). The type and the note
// stay, as do the other tokens and their order; an unknown path is ignored.
func DropRules(params map[string]any, refs []RuleRef) map[string]any {
	out := deepCopyParams(params)
	outSchema := asMapParam(out["output_schema"])
	outItems := asMapParam(out["item_schema"])
	for _, ref := range refs {
		field, key, isItem := splitPath(ref.Path)
		if !isItem {
			if outSchema == nil {
				continue
			}
			if spec, ok := outSchema[field].(string); ok {
				outSchema[field] = dropToken(spec, ref.Token)
			}
			continue
		}
		if outItems == nil {
			continue
		}
		entry := asMapParam(outItems[field])
		if entry == nil {
			continue
		}
		if spec, ok := entry[key].(string); ok {
			entry[key] = dropToken(spec, ref.Token)
			outItems[field] = entry
		}
	}
	if outSchema != nil {
		out["output_schema"] = outSchema
	}
	if outItems != nil {
		out["item_schema"] = outItems
	}
	return out
}

// dropToken removes the first occurrence of token from the rules of a type
// string, keeping the type, the other rules and the note.
func dropToken(spec, token string) string {
	typ, rules, note := ParseTypeSpec(spec)
	kept := make([]string, 0, len(rules))
	dropped := false
	for _, r := range rules {
		if !dropped && r == token {
			dropped = true
			continue
		}
		kept = append(kept, r)
	}
	return joinTypeSpec(typ, kept, note)
}

// MergeRules returns a deep copy of params with rules added to the type strings
// of the named field paths (REQ-DG-006). The value of an entry is one or more
// space-separated tokens. A token is skipped when it is not a rule token, when
// its path is unknown, when it does not fit the field type, or when it is
// already written; kept tokens are inserted after the existing rules and before
// the note.
func MergeRules(params map[string]any, rules map[string]string) map[string]any {
	out := deepCopyParams(params)
	outSchema := asMapParam(out["output_schema"])
	outItems := asMapParam(out["item_schema"])
	for _, path := range sortedStringKeys(rules) {
		field, key, isItem := splitPath(path)
		if !isItem {
			if outSchema == nil {
				continue
			}
			if spec, ok := outSchema[field].(string); ok {
				outSchema[field] = mergeTokens(spec, rules[path])
			}
			continue
		}
		if outItems == nil {
			continue
		}
		entry := asMapParam(outItems[field])
		if entry == nil {
			continue
		}
		if spec, ok := entry[key].(string); ok {
			entry[key] = mergeTokens(spec, rules[path])
			outItems[field] = entry
		}
	}
	if outSchema != nil {
		out["output_schema"] = outSchema
	}
	if outItems != nil {
		out["item_schema"] = outItems
	}
	return out
}

// mergeTokens appends the wanted tokens of tokensStr that fit the type and are
// not written yet to a type string, keeping the note.
func mergeTokens(spec, tokensStr string) string {
	typ, rules, note := ParseTypeSpec(spec)
	seen := make(map[string]bool, len(rules))
	for _, r := range rules {
		seen[r] = true
	}
	for _, tok := range strings.Fields(tokensStr) {
		if !isRuleToken(tok) || !ruleFits(tok, typ) || seen[tok] {
			continue
		}
		rules = append(rules, tok)
		seen[tok] = true
	}
	return joinTypeSpec(typ, rules, note)
}

// KeepHoldingRules returns, per field path, the tokens of rules that fit their
// field type and hold on every non-nil output (REQ-DG-006). A token is judged
// on an output only when the field is present with the right JSON type; a
// missing field or array element is skipped. Paths with no kept token are
// omitted, and the result is never nil.
func KeepHoldingRules(params map[string]any, rules map[string]string, outputs ...any) map[string]string {
	allow := allowEmptyNames(params["allow_empty"])
	outSchema := asMapParam(params["output_schema"])
	outItems := asMapParam(params["item_schema"])
	kept := map[string]string{}
	for _, path := range sortedStringKeys(rules) {
		field, key, isItem := splitPath(path)
		typ, ok := fieldType(outSchema, outItems, field, key, isItem)
		if !ok {
			continue
		}
		var keep []string
		for _, tok := range strings.Fields(rules[path]) {
			if !isRuleToken(tok) || !ruleFits(tok, typ) {
				continue
			}
			if !holdsOnAllOutputs(typ, isItem, field, key, tok, allow[path], outputs) {
				continue
			}
			keep = append(keep, tok)
		}
		if len(keep) > 0 {
			kept[path] = strings.Join(keep, " ")
		}
	}
	return kept
}

// fieldType returns the type word of a field path from the schema or the item
// schema.
func fieldType(schema, itemSchema map[string]any, field, key string, isItem bool) (string, bool) {
	spec, ok := lookupSpec(schema, itemSchema, field, key, isItem)
	if !ok {
		return "", false
	}
	typ, _, _ := ParseTypeSpec(spec)
	return typ, true
}

// lookupSpec returns the type string of a field path.
func lookupSpec(schema, itemSchema map[string]any, field, key string, isItem bool) (string, bool) {
	if !isItem {
		spec, ok := schema[field].(string)
		return spec, ok
	}
	entry := asMapParam(itemSchema[field])
	if entry == nil {
		return "", false
	}
	spec, ok := entry[key].(string)
	return spec, ok
}

// holdsOnAllOutputs reports whether a token holds on every non-nil output.
func holdsOnAllOutputs(typ string, isItem bool, field, key, token string, skipEmpty bool, outputs []any) bool {
	for _, out := range outputs {
		if out == nil {
			continue
		}
		holds, applicable := tokenHoldsOnOutput(typ, isItem, field, key, token, skipEmpty, out)
		if applicable && !holds {
			return false
		}
	}
	return true
}

// tokenHoldsOnOutput reports whether a token holds on one output. applicable is
// false when the field is absent or of the wrong JSON type, so that output does
// not judge the token.
func tokenHoldsOnOutput(typ string, isItem bool, field, key, token string, skipEmpty bool, out any) (holds, applicable bool) {
	m, ok := normalize(out).(map[string]any)
	if !ok {
		return false, false
	}
	if !isItem {
		val, present := m[field]
		if !present || !schemaTypeOK(typ, val) {
			return false, false
		}
		return ruleHolds(token, typ, val, skipEmpty), true
	}
	arr, ok := normalize(m[field]).([]any)
	if !ok {
		return false, false
	}
	holds, applicable = true, false
	for _, elem := range arr {
		em, ok := normalize(elem).(map[string]any)
		if !ok {
			continue
		}
		val, present := em[key]
		if !present || !schemaTypeOK(typ, val) {
			continue
		}
		applicable = true
		if !ruleHolds(token, typ, val, skipEmpty) {
			holds = false
		}
	}
	return holds, applicable
}

// splitPath splits a field path into its parts: "f" is a top-level field and
// "f.k" is field k of the item schema of array field f.
func splitPath(path string) (field, key string, isItem bool) {
	if i := strings.IndexByte(path, '.'); i >= 0 {
		return path[:i], path[i+1:], true
	}
	return path, "", false
}

// joinTypeSpec rebuilds a type string from its parts, in the form
// "<type> <rule>... <note>".
func joinTypeSpec(typ string, rules []string, note string) string {
	parts := make([]string, 0, len(rules)+2)
	parts = append(parts, typ)
	parts = append(parts, rules...)
	if note != "" {
		parts = append(parts, note)
	}
	return strings.Join(parts, " ")
}

// sortedStringKeys returns the keys of m sorted, for deterministic walks.
func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// deepCopyParams returns a deep copy of params, so the rule helpers never mutate
// their input.
func deepCopyParams(params map[string]any) map[string]any {
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = deepCopyValue(v)
	}
	return out
}

// deepCopyValue copies nested maps and slices; scalars are shared.
func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = deepCopyValue(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	default:
		return v
	}
}
