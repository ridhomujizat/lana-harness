package workflow

import "github.com/ridhomujizat/lana-harness/internal/scriptrt"

// TemplateParams returns the params that are rendered as templates (REQ-SP-001,
// design #44 "Catalog and validation"). For script_parse the "script" param is
// JavaScript and is never parsed or rendered, so it is left out. Other types
// return n.Params unchanged. It never mutates n.Params.
func TemplateParams(n Node) map[string]any {
	if n.Type != "script_parse" {
		return n.Params
	}
	out := make(map[string]any, len(n.Params))
	for k, v := range n.Params {
		if k == "script" {
			continue
		}
		out[k] = v
	}
	return out
}

// checkScriptParse validates the script_parse-specific params (REQ-SP-013)
// beyond the generic required-param rule: output_schema, item_schema,
// allow_empty, script, on_drift and timeout_ms. It runs only for script_parse
// nodes.
func (v *validator) checkScriptParse(n Node) {
	schema, ok := v.checkScriptOutputSchema(n)
	if !ok {
		return
	}
	if schema != nil {
		v.checkScriptItemSchema(n, schema)
	}
	v.checkScriptAllowEmpty(n)
	v.checkScriptSource(n)
	v.checkScriptDrift(n)
	v.checkScriptTimeout(n)
}

// checkScriptOutputSchema validates the output_schema of a script_parse node
// (REQ-SP-013): a non-empty object whose every value is a known schema type.
// An absent schema returns (nil, true) because the required-param rule already
// reports it. A bad schema returns (nil, false) so the checks that depend on it
// are skipped.
func (v *validator) checkScriptOutputSchema(n Node) (map[string]any, bool) {
	raw, present := n.Params["output_schema"]
	if !present || isMissingValue(raw) {
		return nil, true
	}
	schema, ok := normalize(raw).(map[string]any)
	if !ok || len(schema) == 0 {
		v.add(n.ID, "bad_schema", "node %s needs param %q to be a non-empty object of field types", nodeLabel(n), "output_schema")
		return nil, false
	}
	for _, field := range sortedMapKeys(schema) {
		spec, ok := schema[field].(string)
		if !ok || !scriptSchemaTypeKnown(schemaFieldType(spec)) {
			v.add(n.ID, "bad_schema", "node %s has a bad %q type for field %q", nodeLabel(n), "output_schema", field)
			return nil, false
		}
	}
	return schema, true
}

// checkScriptItemSchema validates the optional item_schema (REQ-SP-013): an
// object whose keys are object[] fields of output_schema and whose values are
// non-empty objects of known field types.
func (v *validator) checkScriptItemSchema(n Node, schema map[string]any) {
	raw, present := n.Params["item_schema"]
	if !present || raw == nil {
		return
	}
	itemSchema, ok := normalize(raw).(map[string]any)
	if !ok {
		v.add(n.ID, "bad_schema", "node %s needs param %q to be an object", nodeLabel(n), "item_schema")
		return
	}
	for _, field := range sortedMapKeys(itemSchema) {
		spec, ok := schema[field].(string)
		if !ok || schemaFieldType(spec) != "object[]" {
			v.add(n.ID, "bad_schema", "node %s names %q field %q that is not an object[] field of output_schema", nodeLabel(n), "item_schema", field)
			continue
		}
		entry, ok := normalize(itemSchema[field]).(map[string]any)
		if !ok || len(entry) == 0 {
			v.add(n.ID, "bad_schema", "node %s needs %q[%q] to be a non-empty object of field types", nodeLabel(n), "item_schema", field)
			continue
		}
		for _, key := range sortedMapKeys(entry) {
			espec, ok := entry[key].(string)
			if !ok || !scriptSchemaTypeKnown(schemaFieldType(espec)) {
				v.add(n.ID, "bad_schema", "node %s has a bad %q type for field %q of %q", nodeLabel(n), "item_schema", key, field)
			}
		}
	}
}

// checkScriptAllowEmpty validates the optional allow_empty param (REQ-SP-013):
// a list of strings.
func (v *validator) checkScriptAllowEmpty(n Node) {
	raw, present := n.Params["allow_empty"]
	if !present || raw == nil {
		return
	}
	if !isStringList(raw) {
		v.add(n.ID, "bad_schema", "node %s needs param %q to be a list of strings", nodeLabel(n), "allow_empty")
	}
}

// checkScriptSource validates the optional script param (REQ-SP-013): a string
// that compiles and defines a parse function. An empty string is valid and skips
// the compile. The size is checked first so an over-long script is not compiled.
func (v *validator) checkScriptSource(n Node) {
	raw, present := n.Params["script"]
	if !present {
		return
	}
	src, ok := raw.(string)
	if !ok {
		v.add(n.ID, "bad_script", "node %s needs param %q to be a string", nodeLabel(n), "script")
		return
	}
	if src == "" {
		return
	}
	if len(src) > scriptrt.MaxScriptBytes {
		v.add(n.ID, "bad_script", "node %s has a script of %d bytes, over the %d byte limit", nodeLabel(n), len(src), scriptrt.MaxScriptBytes)
		return
	}
	if _, err := scriptrt.Compile(src); err != nil {
		v.add(n.ID, "bad_script", "node %s has a bad script: %v", nodeLabel(n), err)
	}
}

// checkScriptDrift validates the optional on_drift param (REQ-SP-013): absent or
// "" is fine, otherwise it must name one of the drift actions.
func (v *validator) checkScriptDrift(n Node) {
	raw, present := n.Params["on_drift"]
	if !present {
		return
	}
	switch s := raw.(type) {
	case nil:
	case string:
		if s != "" && s != "regenerate" && s != "fallback_only" && s != "fail" {
			v.add(n.ID, "missing_param", "node %s needs param %q to be one of regenerate, fallback_only, fail", nodeLabel(n), "on_drift")
		}
	default:
		v.add(n.ID, "missing_param", "node %s needs param %q to be one of regenerate, fallback_only, fail", nodeLabel(n), "on_drift")
	}
}

// checkScriptTimeout validates the optional timeout_ms param (REQ-SP-013): a
// number between 100 and 10000 milliseconds.
func (v *validator) checkScriptTimeout(n Node) {
	raw, present := n.Params["timeout_ms"]
	if !present || raw == nil {
		return
	}
	if f, ok := asNumber(raw); !ok || f < 100 || f > 10000 {
		v.add(n.ID, "missing_param", "node %s needs param %q between 100 and 10000 milliseconds", nodeLabel(n), "timeout_ms")
	}
}

// scriptSchemaTypeKnown reports whether name is a schema type accepted by
// schemaTypeOK (the types of REQ-WF-005).
func scriptSchemaTypeKnown(name string) bool {
	switch name {
	case "string", "integer", "number", "boolean", "object", "array",
		"string[]", "integer[]", "number[]", "object[]":
		return true
	default:
		return false
	}
}

// isStringList reports whether v is a []string or a []any whose entries are all
// strings.
func isStringList(v any) bool {
	switch t := v.(type) {
	case []string:
		return true
	case []any:
		for _, e := range t {
			if _, ok := e.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}
