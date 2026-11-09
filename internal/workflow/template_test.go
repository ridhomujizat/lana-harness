package workflow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestRenderExactExpressionKeepsType covers AC-WF-007: a parameter that is one
// expression keeps the resolved JSON type.
func TestRenderExactExpressionKeepsType(t *testing.T) {
	sc := Scope{Nodes: map[string]any{
		"a": map[string]any{"items": []any{1.0, 2.0, 3.0}},
	}}
	got, err := Render("{{ nodes.a.output.items }}", sc)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("Render type = %T, want []any", got)
	}
	if len(arr) != 3 {
		t.Fatalf("Render len = %d, want 3", len(arr))
	}
}

// TestRenderNodesOutputSegment skips the literal output segment after the id.
func TestRenderNodesOutputSegment(t *testing.T) {
	sc := Scope{Nodes: map[string]any{
		"fetch": json.RawMessage(`{"status":200}`),
	}}
	for _, tmpl := range []string{"{{ nodes.fetch.output.status }}", "{{ nodes.fetch.status }}"} {
		got, err := Render(tmpl, sc)
		if err != nil {
			t.Fatalf("%s: %v", tmpl, err)
		}
		if got != float64(200) {
			t.Fatalf("%s = %#v, want 200", tmpl, got)
		}
	}
}

// TestRenderPaths resolves every root and path form.
func TestRenderPaths(t *testing.T) {
	sc := Scope{
		Input:    map[string]any{"list": []any{"a", "b", "c"}, "nested": map[string]any{"x": true}},
		State:    map[string]any{"k": "v"},
		Run:      map[string]any{"id": "r1"},
		Workflow: map[string]any{"id": "w1"},
		Item:     "item-0", ItemIndex: 2, HasItem: true,
	}
	cases := []struct {
		tmpl string
		want any
	}{
		{"{{ input.list[1] }}", "b"},
		{"{{ input.nested.x }}", true},
		{"{{ state.k }}", "v"},
		{"{{ run.id }}", "r1"},
		{"{{ workflow.id }}", "w1"},
		{"{{ item }}", "item-0"},
		{"{{ item_index }}", 2},
		{"{{ input.missing }}", nil},
		{"{{ input.list[9] }}", nil},
		{"{{ input.nested.missing.deep }}", nil},
	}
	for _, tc := range cases {
		got, err := Render(tc.tmpl, sc)
		if err != nil {
			t.Fatalf("%s: %v", tc.tmpl, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s = %#v, want %#v", tc.tmpl, got, tc.want)
		}
	}
}

// TestRenderItemIndexOutsideLoop is nil when no item is active.
func TestRenderItemIndexOutsideLoop(t *testing.T) {
	got, err := Render("{{ item_index }}", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("item_index = %#v, want nil", got)
	}
}

// TestRenderMixedText inserts values into text with the right formatting.
func TestRenderMixedText(t *testing.T) {
	sc := Scope{Input: map[string]any{
		"name":  "Ada",
		"count": 3.5,
		"ok":    true,
		"obj":   map[string]any{"a": 1.0},
		"arr":   []any{"x", "y"},
	}}
	cases := []struct {
		tmpl string
		want string
	}{
		{"Hi {{ input.name }}!", "Hi Ada!"},
		{"x={{ input.missing }};", "x=;"},
		{"n={{ input.count }}", "n=3.5"},
		{"b={{ input.ok }}", "b=true"},
		{"v={{ input.obj }}", `v={"a":1}`},
		{"v={{ input.arr }}", `v=["x","y"]`},
		{"{{ input.name }} has {{ input.count }}", "Ada has 3.5"},
	}
	for _, tc := range cases {
		got, err := Render(tc.tmpl, sc)
		if err != nil {
			t.Fatalf("%s: %v", tc.tmpl, err)
		}
		if got != tc.want {
			t.Fatalf("%s = %#v, want %q", tc.tmpl, got, tc.want)
		}
	}
}

// TestRenderNoTemplateReturnsString leaves plain strings untouched.
func TestRenderNoTemplateReturnsString(t *testing.T) {
	got, err := Render("  plain  ", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "  plain  " {
		t.Fatalf("got %#v, want the original string", got)
	}
}

// TestRenderWalkMapsAndSlices renders nested params.
func TestRenderWalkMapsAndSlices(t *testing.T) {
	sc := Scope{Input: map[string]any{"v": "V"}}
	in := map[string]any{
		"a": "{{ input.v }}",
		"b": []any{"{{ input.v }}", 7.0},
		"c": map[string]any{"d": "x{{ input.v }}"},
	}
	got, err := Render(in, sc)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"a": "V",
		"b": []any{"V", 7.0},
		"c": map[string]any{"d": "xV"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Render = %#v, want %#v", got, want)
	}
}

// TestTemplateErrors covers unclosed, empty, unknown-root and bad-index input.
func TestTemplateErrors(t *testing.T) {
	for _, tmpl := range []string{
		"{{ input",
		"{{  }}",
		"{{ bogus.x }}",
		"{{ input[abc] }}",
		"{{ input[-1] }}",
		"{{ .x }}",
	} {
		if _, err := Render(tmpl, Scope{}); err == nil {
			t.Fatalf("%s: want error, got nil", tmpl)
		}
	}
}

// TestCheckTemplates is a parse-only check over nested params.
func TestCheckTemplates(t *testing.T) {
	ok := map[string]any{"a": "{{ input.x }}", "b": []any{"{{ nodes.n.output.y }}"}}
	if err := CheckTemplates(ok); err != nil {
		t.Fatalf("CheckTemplates(valid) = %v", err)
	}
	bad := map[string]any{"a": []any{"{{ input"}}
	err := CheckTemplates(bad)
	if err == nil || !strings.Contains(err.Error(), "{{ input") {
		t.Fatalf("CheckTemplates(invalid) = %v, want syntax error", err)
	}
	if err := CheckTemplates(map[string]any{"x": "{{ nope.y }}"}); err == nil {
		t.Fatal("CheckTemplates(unknown root) = nil, want error")
	}
}

// TestTemplateRefs lists nodes.<id> references in order without duplicates.
func TestTemplateRefs(t *testing.T) {
	in := map[string]any{
		"a": "{{ nodes.foo.output.x }}",
		"b": "{{ nodes.bar.y }} and {{ nodes.foo.z }}",
		"c": "{{ input.x }}",
	}
	got := TemplateRefs(in)
	want := []string{"foo", "bar"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TemplateRefs = %#v, want %#v", got, want)
	}
}

// TestTemplateParams checks the spec #44 rule: the script of a script_parse node
// is left out of the rendered params through a copy, while every other type
// returns its params map unchanged (REQ-SP-001).
func TestTemplateParams(t *testing.T) {
	spParams := map[string]any{"input": "x", "goal": "g", "script": "var s = 1;"}
	got := TemplateParams(Node{Type: "script_parse", Params: spParams})
	if _, ok := got["script"]; ok {
		t.Fatalf("TemplateParams(script_parse) kept script: %#v", got)
	}
	if got["input"] != "x" || got["goal"] != "g" {
		t.Fatalf("TemplateParams(script_parse) = %#v", got)
	}
	got["input"] = "y"
	if spParams["input"] != "x" || spParams["goal"] != "g" || spParams["script"] != "var s = 1;" {
		t.Fatalf("TemplateParams mutated the original params: %#v", spParams)
	}

	msgParams := map[string]any{"text": "hi"}
	msg := TemplateParams(Node{Type: "message", Params: msgParams})
	msg["text"] = "changed"
	if msgParams["text"] != "changed" {
		t.Fatalf("TemplateParams(message) did not return the same map")
	}
}
