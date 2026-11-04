package workflow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestCompareNumeric covers equality and ordering with mixed number types and
// numeric strings.
func TestCompareNumeric(t *testing.T) {
	cases := []struct {
		left  any
		op    string
		right any
		want  bool
	}{
		{float64(200), "==", 200, true},
		{float64(200), ">", 199, true},
		{float64(200), ">=", float64(200), true},
		{float64(199), "<", 200, true},
		{float64(200), "<=", 199, false},
		{"7", "==", 7, true},
		{json.Number("7"), "==", "7", true},
		{7, "!=", 8, true},
		{"b", ">", "a", true},
		{"a", "<", "b", true},
		{true, "==", true, true},
		{true, "==", "true", false},
		{map[string]any{"a": 1.0}, "==", map[string]any{"a": 1.0}, true},
	}
	for _, tc := range cases {
		got, err := Compare(tc.left, tc.op, tc.right)
		if err != nil {
			t.Fatalf("Compare(%#v %s %#v): %v", tc.left, tc.op, tc.right, err)
		}
		if got != tc.want {
			t.Fatalf("Compare(%#v %s %#v) = %v, want %v", tc.left, tc.op, tc.right, got, tc.want)
		}
	}
}

// TestCompareContains covers substring, element and key membership.
func TestCompareContains(t *testing.T) {
	cases := []struct {
		left  any
		op    string
		right any
		want  bool
	}{
		{"hello world", "contains", "world", true},
		{"hello", "contains", "xyz", false},
		{[]any{1.0, 2.0, 3.0}, "contains", 2, true},
		{[]any{"a", "b"}, "contains", "b", true},
		{[]any{"a"}, "not_contains", "b", true},
		{map[string]any{"k": 1.0}, "contains", "k", true},
		{map[string]any{"k": 1.0}, "contains", "nope", false},
	}
	for _, tc := range cases {
		got, err := Compare(tc.left, tc.op, tc.right)
		if err != nil {
			t.Fatalf("Compare(%#v %s %#v): %v", tc.left, tc.op, tc.right, err)
		}
		if got != tc.want {
			t.Fatalf("Compare(%#v %s %#v) = %v, want %v", tc.left, tc.op, tc.right, got, tc.want)
		}
	}
}

// TestCompareExistsAndTruthy covers the unary ops.
func TestCompareExistsAndTruthy(t *testing.T) {
	if ok, _ := Compare(nil, "exists", nil); ok {
		t.Fatal("exists(nil) = true, want false")
	}
	if ok, _ := Compare(1, "exists", nil); !ok {
		t.Fatal("exists(1) = false, want true")
	}
	if ok, _ := Compare(nil, "not_exists", nil); !ok {
		t.Fatal("not_exists(nil) = false, want true")
	}
	falsy := []any{nil, false, 0.0, "", "false", []any{}, map[string]any{}}
	for _, v := range falsy {
		if ok, _ := Compare(v, "truthy", nil); ok {
			t.Fatalf("truthy(%#v) = true, want false", v)
		}
		if ok, _ := Compare(v, "falsy", nil); !ok {
			t.Fatalf("falsy(%#v) = false, want true", v)
		}
	}
	truthy := []any{1, "0", "x", true, []any{1.0}, map[string]any{"a": 1.0}}
	for _, v := range truthy {
		if ok, _ := Compare(v, "truthy", nil); !ok {
			t.Fatalf("truthy(%#v) = false, want true", v)
		}
	}
}

// TestCompareUnknownOp errors on an unknown operator.
func TestCompareUnknownOp(t *testing.T) {
	if _, err := Compare(1, "??", 2); err == nil {
		t.Fatal("Compare(??) = nil error, want error")
	}
}

// TestCompareIfExample covers the AC-WF-006 style condition with a JSON number
// against a Go int.
func TestCompareIfExample(t *testing.T) {
	left, err := Render("{{ nodes.fetch.output.status }}", Scope{Nodes: map[string]any{
		"fetch": map[string]any{"status": float64(200)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := Compare(left, "==", 200)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("Compare(%#v == 200) = false, want true", left)
	}
}

// TestExtractJSON covers the whole text, fenced and prose-wrapped forms.
func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name string
		text string
		want any
	}{
		{"whole object", `{"a":1}`, map[string]any{"a": 1.0}},
		{"whole array", `[1,2,3]`, []any{1.0, 2.0, 3.0}},
		{"trimmed", "  {\"a\":1}  ", map[string]any{"a": 1.0}},
		{"json fence", "Here you go:\n```json\n{\"score\": 8}\n```\nDone.", map[string]any{"score": 8.0}},
		{"plain fence", "```\n{\"ok\": true}\n```", map[string]any{"ok": true}},
		{"prose wrapped", `The result is {"ok": true} and done.`, map[string]any{"ok": true}},
		{"brace in string", `{"s":"a}b"}`, map[string]any{"s": "a}b"}},
	}
	for _, tc := range cases {
		got, err := ExtractJSON(tc.text)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s = %#v, want %#v", tc.name, got, tc.want)
		}
	}
}

// TestExtractJSONErrors returns an error when no object or array is present.
func TestExtractJSONErrors(t *testing.T) {
	for _, text := range []string{"", "no json here", "42"} {
		if _, err := ExtractJSON(text); err == nil {
			t.Fatalf("ExtractJSON(%q) = nil error, want error", text)
		}
	}
}

// TestCheckSchema covers type checks, notes, missing fields and error text.
func TestCheckSchema(t *testing.T) {
	schema := map[string]any{
		"score": "integer 1-10",
		"name":  "string",
		"links": "string[]",
		"ratio": "number",
	}
	ok := map[string]any{
		"score": float64(8),
		"name":  "x",
		"links": []any{"a", "b"},
		"ratio": 0.5,
	}
	if err := CheckSchema(schema, ok); err != nil {
		t.Fatalf("CheckSchema(valid) = %v", err)
	}

	err := CheckSchema(map[string]any{"score": "integer"}, map[string]any{"score": "8"})
	want := `field "score": want integer, got string`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("CheckSchema(type) = %v, want contains %q", err, want)
	}

	err = CheckSchema(map[string]any{"links": "string[]"}, map[string]any{})
	want = `field "links": missing`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("CheckSchema(missing) = %v, want contains %q", err, want)
	}

	err = CheckSchema(map[string]any{"score": "integer"}, map[string]any{"score": 7.5})
	if err == nil || !strings.Contains(err.Error(), "want integer, got number") {
		t.Fatalf("CheckSchema(fraction) = %v, want integer error", err)
	}

	err = CheckSchema(map[string]any{"links": "string[]"}, map[string]any{"links": []any{"a", 1.0}})
	if err == nil || !strings.Contains(err.Error(), `field "links"`) {
		t.Fatalf("CheckSchema(elem) = %v, want error about links", err)
	}

	err = CheckSchema(map[string]any{"found": "boolean"}, map[string]any{"found": "yes"})
	if err == nil || !strings.Contains(err.Error(), `field "found": want boolean, got string`) {
		t.Fatalf("CheckSchema(bool) = %v", err)
	}
}

// TestCheckSchemaMultiple joins every problem into one error.
func TestCheckSchemaMultiple(t *testing.T) {
	err := CheckSchema(map[string]any{"a": "string", "b": "integer"}, map[string]any{})
	if err == nil {
		t.Fatal("CheckSchema = nil, want error")
	}
	if !strings.Contains(err.Error(), `field "a": missing`) || !strings.Contains(err.Error(), `field "b": missing`) {
		t.Fatalf("CheckSchema = %v, want both problems", err)
	}
}

// TestCheckSchemaNotObject rejects a non-object value.
func TestCheckSchemaNotObject(t *testing.T) {
	if err := CheckSchema(map[string]any{"a": "string"}, "not an object"); err == nil {
		t.Fatal("CheckSchema(string) = nil, want error")
	}
}
