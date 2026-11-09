package workflow

import (
	"reflect"
	"testing"
)

// TestParseTypeSpec covers the type/rules/note split of REQ-DG-003, including
// the words that look like rules but are not.
func TestParseTypeSpec(t *testing.T) {
	cases := []struct {
		spec  string
		typ   string
		rules []string
		note  string
	}{
		{"integer >0 stars today", "integer", []string{">0"}, "stars today"},
		{"string url", "string", []string{"url"}, ""},
		{"string min:20 a note", "string", []string{"min:20"}, "a note"},
		{"string a url-ish note", "string", nil, "a url-ish note"},
		{"", "", nil, ""},
		{"number >=0 max:5 >0", "number", []string{">=0", "max:5", ">0"}, ""},
		{"boolean", "boolean", nil, ""},
		{"string min:-1 x", "string", nil, "min:-1 x"},
		{"string min: x", "string", nil, "min: x"},
		{"string [] ", "string", nil, "[]"},
	}
	for _, tc := range cases {
		typ, rules, note := ParseTypeSpec(tc.spec)
		if typ != tc.typ || note != tc.note || !reflect.DeepEqual(rules, tc.rules) {
			t.Fatalf("ParseTypeSpec(%q) = (%q, %v, %q), want (%q, %v, %q)",
				tc.spec, typ, rules, note, tc.typ, tc.rules, tc.note)
		}
	}
}

// TestRuleFits covers the fit table of REQ-DG-003.
func TestRuleFits(t *testing.T) {
	cases := []struct {
		token string
		typ   string
		fit   bool
	}{
		{">0", "integer", true},
		{">0", "number", true},
		{">0", "string", false},
		{">0", "boolean", false},
		{">=0", "number", true},
		{">=0", "array", false},
		{"url", "string", true},
		{"url", "integer", false},
		{"min:3", "string", true},
		{"min:3", "integer", true},
		{"min:3", "number", true},
		{"min:3", "array", true},
		{"min:3", "string[]", true},
		{"min:3", "object[]", true},
		{"min:3", "boolean", false},
		{"min:3", "object", false},
		{"min:3", "widget", false},
		{"max:5", "integer[]", true},
		{"max:5", "object", false},
	}
	for _, tc := range cases {
		if got := ruleFits(tc.token, tc.typ); got != tc.fit {
			t.Fatalf("ruleFits(%q, %q) = %v, want %v", tc.token, tc.typ, got, tc.fit)
		}
	}
}

// TestRuleViolationsCases covers RuleViolations of REQ-DG-004: top-level and
// item fields, the allow_empty skip, a missing field and a wrong JSON type.
func TestRuleViolationsCases(t *testing.T) {
	itemParams := map[string]any{
		"output_schema": map[string]any{"repos": "object[]"},
		"item_schema":   map[string]any{"repos": map[string]any{"stars": "integer >0"}},
	}
	cases := []struct {
		name   string
		params map[string]any
		out    any
		want   []RuleRef
	}{
		{
			name:   "top level passes",
			params: map[string]any{"output_schema": map[string]any{"count": "integer >0"}},
			out:    map[string]any{"count": 3},
		},
		{
			name:   "top level fails",
			params: map[string]any{"output_schema": map[string]any{"count": "integer >0"}},
			out:    map[string]any{"count": 0},
			want:   []RuleRef{{Path: "count", Token: ">0"}},
		},
		{
			name:   "missing field skipped",
			params: map[string]any{"output_schema": map[string]any{"count": "integer >0"}},
			out:    map[string]any{},
		},
		{
			name:   "wrong JSON type skipped",
			params: map[string]any{"output_schema": map[string]any{"count": "integer >0"}},
			out:    map[string]any{"count": "x"},
		},
		{
			name:   "not a number type skipped",
			params: map[string]any{"output_schema": map[string]any{"count": "integer >0"}},
			out:    "not an object",
		},
		{
			name: "empty allowed skips the rule",
			params: map[string]any{
				"output_schema": map[string]any{"name": "string min:3"},
				"allow_empty":   []any{"name"},
			},
			out: map[string]any{"name": ""},
		},
		{
			name:   "empty not allowed fails",
			params: map[string]any{"output_schema": map[string]any{"name": "string min:3"}},
			out:    map[string]any{"name": "  "},
			want:   []RuleRef{{Path: "name", Token: "min:3"}},
		},
		{
			name:   "short string fails min",
			params: map[string]any{"output_schema": map[string]any{"name": "string min:3"}},
			out:    map[string]any{"name": "ab"},
			want:   []RuleRef{{Path: "name", Token: "min:3"}},
		},
		{
			name:   "url fails",
			params: map[string]any{"output_schema": map[string]any{"u": "string url"}},
			out:    map[string]any{"u": "not a url"},
			want:   []RuleRef{{Path: "u", Token: "url"}},
		},
		{
			name:   "url passes",
			params: map[string]any{"output_schema": map[string]any{"u": "string url"}},
			out:    map[string]any{"u": "https://example.com/x"},
		},
		{
			name:   "array length fails max",
			params: map[string]any{"output_schema": map[string]any{"tags": "object[] max:2"}},
			out:    map[string]any{"tags": []any{map[string]any{}, map[string]any{}, map[string]any{}}},
			want:   []RuleRef{{Path: "tags", Token: "max:2"}},
		},
		{
			name:   "item fails",
			params: itemParams,
			out:    map[string]any{"repos": []any{map[string]any{"stars": 5}, map[string]any{"stars": 0}}},
			want:   []RuleRef{{Path: "repos.stars", Token: ">0"}},
		},
		{
			name:   "item missing field and non-object element skipped",
			params: itemParams,
			out:    map[string]any{"repos": []any{map[string]any{}, "nope"}},
		},
		{
			name: "item empty allowed skips",
			params: map[string]any{
				"output_schema": map[string]any{"repos": "object[]"},
				"item_schema":   map[string]any{"repos": map[string]any{"note": "string min:3"}},
				"allow_empty":   []any{"repos.note"},
			},
			out: map[string]any{"repos": []any{map[string]any{"note": ""}}},
		},
		{
			name:   "rules walked in order",
			params: map[string]any{"output_schema": map[string]any{"n": "number >=0 max:5"}},
			out:    map[string]any{"n": -1},
			want:   []RuleRef{{Path: "n", Token: ">=0"}},
		},
	}
	for _, tc := range cases {
		got := RuleViolations(tc.params, tc.out)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: RuleViolations = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// TestCheckRulesMessages covers the DriftError message format of REQ-DG-004.
func TestCheckRulesMessages(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]any
		out    any
		want   string
	}{
		{
			name:   "top level",
			params: map[string]any{"output_schema": map[string]any{"count": "integer >0"}},
			out:    map[string]any{"count": 0},
			want:   `field "count": 0 is not >0`,
		},
		{
			name: "item",
			params: map[string]any{
				"output_schema": map[string]any{"repos": "object[]"},
				"item_schema":   map[string]any{"repos": map[string]any{"stars": "integer >0"}},
			},
			out:  map[string]any{"repos": []any{map[string]any{"stars": 5}, map[string]any{"stars": 0}}},
			want: `field "repos.stars" item 1: 0 is not >0`,
		},
		{
			name:   "length",
			params: map[string]any{"output_schema": map[string]any{"name": "string min:5"}},
			out:    map[string]any{"name": "abc"},
			want:   `field "name": length 3 is not min:5`,
		},
	}
	for _, tc := range cases {
		de := CheckRules(tc.params, tc.out)
		if de == nil {
			t.Fatalf("%s: no drift, want %q", tc.name, tc.want)
		}
		if de.Code != DriftRule {
			t.Fatalf("%s: code = %q, want %q", tc.name, de.Code, DriftRule)
		}
		if de.Message != tc.want {
			t.Fatalf("%s: message = %q, want %q", tc.name, de.Message, tc.want)
		}
	}
	if de := CheckRules(map[string]any{"output_schema": map[string]any{"count": "integer >0"}}, map[string]any{"count": 1}); de != nil {
		t.Fatalf("CheckRules = %v, want nil", de)
	}
}

// TestRuleProblemsCases covers the bad_rule problems of REQ-DG-003.
func TestRuleProblemsCases(t *testing.T) {
	cases := []struct {
		name       string
		schema     map[string]any
		itemSchema map[string]any
		want       []string
	}{
		{"clean", map[string]any{"f": "integer >0"}, nil, nil},
		{
			"url on integer",
			map[string]any{"f": "integer url"},
			nil,
			[]string{`field "f": rule "url" does not fit integer`},
		},
		{
			"gt on string",
			map[string]any{"f": "string >0"},
			nil,
			[]string{`field "f": rule ">0" does not fit string`},
		},
		{"non string spec skipped", map[string]any{"f": 5}, nil, nil},
		{
			"item field",
			map[string]any{"repos": "object[]"},
			map[string]any{"repos": map[string]any{"k": "boolean >0"}},
			[]string{`field "repos.k": rule ">0" does not fit boolean`},
		},
		{
			"sorted top level",
			map[string]any{"b": "string >0", "a": "integer url"},
			nil,
			[]string{`field "a": rule "url" does not fit integer`, `field "b": rule ">0" does not fit string`},
		},
	}
	for _, tc := range cases {
		got := RuleProblems(tc.schema, tc.itemSchema)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: RuleProblems = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestDropRules drops the violated tokens, keeps the type and note, and never
// mutates its input.
func TestDropRules(t *testing.T) {
	params := map[string]any{
		"output_schema": map[string]any{
			"count": "integer >0 stars today",
			"n":     "number >=0 max:5 >0",
		},
		"item_schema": map[string]any{
			"repos": map[string]any{"stars": "integer >0", "name": "string min:3 note"},
		},
	}
	got := DropRules(params, []RuleRef{
		{Path: "count", Token: ">0"},
		{Path: "n", Token: "max:5"},
		{Path: "repos.stars", Token: ">0"},
		{Path: "missing", Token: ">0"},
	})
	wantSchema := map[string]any{
		"count": "integer stars today",
		"n":     "number >=0 >0",
	}
	wantItems := map[string]any{
		"repos": map[string]any{"stars": "integer", "name": "string min:3 note"},
	}
	if !reflect.DeepEqual(got["output_schema"], wantSchema) {
		t.Fatalf("output_schema = %#v, want %#v", got["output_schema"], wantSchema)
	}
	if !reflect.DeepEqual(got["item_schema"], wantItems) {
		t.Fatalf("item_schema = %#v, want %#v", got["item_schema"], wantItems)
	}
	if params["output_schema"].(map[string]any)["count"] != "integer >0 stars today" {
		t.Fatalf("input mutated: %#v", params)
	}
}

// TestMergeRulesCases covers the token insertion of REQ-DG-006.
func TestMergeRulesCases(t *testing.T) {
	cases := []struct {
		name   string
		spec   string
		path   string
		tokens string
		want   string
	}{
		{"insert after type", "integer stars today", "f", ">0", "integer >0 stars today"},
		{"dedupe", "integer >0 x", "f", ">0", "integer >0 x"},
		{"append several", "integer", "f", ">0 max:5", "integer >0 max:5"},
		{"does not fit", "boolean", "f", ">0", "boolean"},
		{"not a rule token", "integer", "f", "bogus", "integer"},
		{"unknown path", "integer", "other", ">0", "integer"},
	}
	for _, tc := range cases {
		params := map[string]any{"output_schema": map[string]any{"f": tc.spec}}
		got := MergeRules(params, map[string]string{tc.path: tc.tokens})
		schema := got["output_schema"].(map[string]any)
		if schema["f"] != tc.want {
			t.Fatalf("%s: spec = %q, want %q", tc.name, schema["f"], tc.want)
		}
	}
	itemParams := map[string]any{
		"output_schema": map[string]any{"repos": "object[]"},
		"item_schema":   map[string]any{"repos": map[string]any{"stars": "integer"}},
	}
	got := MergeRules(itemParams, map[string]string{"repos.stars": ">0"})
	items := got["item_schema"].(map[string]any)
	entry := items["repos"].(map[string]any)
	if entry["stars"] != "integer >0" {
		t.Fatalf("item spec = %q, want %q", entry["stars"], "integer >0")
	}
	if itemParams["item_schema"].(map[string]any)["repos"].(map[string]any)["stars"] != "integer" {
		t.Fatalf("input mutated: %#v", itemParams)
	}
}

// TestKeepHoldingRulesCases covers the rule keeping of REQ-DG-006: a token is
// kept only when it holds on every non-nil output.
func TestKeepHoldingRulesCases(t *testing.T) {
	params := map[string]any{"output_schema": map[string]any{"count": "integer", "name": "string"}}
	rules := map[string]string{"count": ">0", "name": "min:3"}

	got := KeepHoldingRules(params, rules,
		map[string]any{"count": 5, "name": "abcd"},
		map[string]any{"count": 7, "name": "hello"},
	)
	if want := map[string]string{"count": ">0", "name": "min:3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kept = %v, want %v", got, want)
	}

	got = KeepHoldingRules(params, rules, map[string]any{"count": 5, "name": "abcd"}, map[string]any{"count": 0, "name": "hij"})
	if want := map[string]string{"name": "min:3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kept = %v, want %v", got, want)
	}

	got = KeepHoldingRules(params, rules, map[string]any{"count": 0, "name": "hi"})
	if len(got) != 0 || got == nil {
		t.Fatalf("kept = %v, want an empty non-nil map", got)
	}

	// A nil output is ignored, an absent field is skipped and a wrong JSON type
	// is skipped.
	got = KeepHoldingRules(params, rules,
		nil,
		map[string]any{"name": "abcd"},
		map[string]any{"count": "x"},
	)
	if want := map[string]string{"count": ">0", "name": "min:3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kept = %v, want %v", got, want)
	}

	// A wrong token, an unknown path and a token that does not fit are dropped.
	got = KeepHoldingRules(params, map[string]string{"count": "bogus >0", "missing": ">0", "name": ">0"}, map[string]any{"count": 5, "name": "abc"})
	if want := map[string]string{"count": ">0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kept = %v, want %v", got, want)
	}

	itemParams := map[string]any{
		"output_schema": map[string]any{"repos": "object[]"},
		"item_schema":   map[string]any{"repos": map[string]any{"stars": "integer"}},
	}
	got = KeepHoldingRules(itemParams, map[string]string{"repos.stars": ">0"},
		map[string]any{"repos": []any{map[string]any{"stars": 3}, map[string]any{"stars": 5}}},
	)
	if want := map[string]string{"repos.stars": ">0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kept = %v, want %v", got, want)
	}
	got = KeepHoldingRules(itemParams, map[string]string{"repos.stars": ">0"},
		map[string]any{"repos": []any{map[string]any{"stars": 3}, map[string]any{"stars": 0}}},
	)
	if len(got) != 0 {
		t.Fatalf("kept = %v, want no token", got)
	}
}
