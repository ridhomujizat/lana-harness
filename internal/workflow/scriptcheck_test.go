package workflow

import (
	"reflect"
	"strings"
	"testing"
)

// TestCheckContractCases covers the contract check of REQ-SP-003: coercion,
// schema, the non-empty rule and the item schema, with the first failure winning.
func TestCheckContractCases(t *testing.T) {
	objectSchema := map[string]any{"repos": "object[]"}
	reposItem := map[string]any{
		"output_schema": objectSchema,
		"item_schema":   map[string]any{"repos": map[string]any{"url": "string"}},
	}
	cases := []struct {
		name     string
		params   map[string]any
		in       any
		want     any    // expected output when wantCode is empty
		wantCode string // "" for a pass
		wantMsg  string // substring of the drift message
	}{
		{
			name:   "passes",
			params: map[string]any{"output_schema": map[string]any{"name": "string", "count": "integer"}},
			in:     map[string]any{"name": "x", "count": 12},
			want:   map[string]any{"name": "x", "count": float64(12)},
		},
		{
			name:     "non-object input",
			params:   map[string]any{"output_schema": map[string]any{"a": "string"}},
			in:       "hello",
			wantCode: DriftScript,
			wantMsg:  "output is string, want an object",
		},
		{
			name:     "missing field",
			params:   map[string]any{"output_schema": map[string]any{"a": "string", "b": "integer"}},
			in:       map[string]any{"a": "x"},
			wantCode: DriftSchema,
			wantMsg:  `field "b": missing`,
		},
		{
			name:   "wrong type coerced ok",
			params: map[string]any{"output_schema": map[string]any{"n": "integer"}},
			in:     map[string]any{"n": "12"},
			want:   map[string]any{"n": float64(12)},
		},
		{
			name:     "empty string field",
			params:   map[string]any{"output_schema": map[string]any{"a": "string"}},
			in:       map[string]any{"a": "  "},
			wantCode: DriftEmpty,
			wantMsg:  `field "a": empty`,
		},
		{
			name:     "empty array field",
			params:   map[string]any{"output_schema": map[string]any{"tags": "string[]"}},
			in:       map[string]any{"tags": []any{}},
			wantCode: DriftEmpty,
			wantMsg:  `field "tags": empty`,
		},
		{
			name: "allow_empty top-level field passes",
			params: map[string]any{
				"output_schema": map[string]any{"note": "string", "name": "string"},
				"allow_empty":   []any{"note"},
			},
			in:   map[string]any{"note": "", "name": "x"},
			want: map[string]any{"note": "", "name": "x"},
		},
		{
			name:     "item_schema missing item field",
			params:   reposItem,
			in:       map[string]any{"repos": []any{map[string]any{"title": "t"}}},
			wantCode: DriftItem,
			wantMsg:  `field "repos" item 0: field "url": missing`,
		},
		{
			name:     "item empty field",
			params:   reposItem,
			in:       map[string]any{"repos": []any{map[string]any{"url": ""}}},
			wantCode: DriftItem,
			wantMsg:  `field "repos" item 0 field "url": empty`,
		},
		{
			name: "allow_empty repos.description lets empty item field pass",
			params: map[string]any{
				"output_schema": objectSchema,
				"item_schema":   map[string]any{"repos": map[string]any{"url": "string", "description": "string"}},
				"allow_empty":   []any{"repos.description"},
			},
			in:   map[string]any{"repos": []any{map[string]any{"url": "u", "description": ""}}},
			want: map[string]any{"repos": []any{map[string]any{"url": "u", "description": ""}}},
		},
		{
			name: "item element coercion",
			params: map[string]any{
				"output_schema": objectSchema,
				"item_schema":   map[string]any{"repos": map[string]any{"count": "integer"}},
			},
			in:   map[string]any{"repos": []any{map[string]any{"count": "3"}}},
			want: map[string]any{"repos": []any{map[string]any{"count": float64(3)}}},
		},
	}
	for _, tc := range cases {
		got, de := CheckContract(tc.params, tc.in)
		if tc.wantCode == "" {
			if de != nil {
				t.Fatalf("%s: drift = %v, want pass", tc.name, de)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s = %#v, want %#v", tc.name, got, tc.want)
			}
			continue
		}
		if de == nil {
			t.Fatalf("%s: no drift, want %s", tc.name, tc.wantCode)
		}
		if de.Code != tc.wantCode {
			t.Fatalf("%s: code = %q, want %q", tc.name, de.Code, tc.wantCode)
		}
		if !strings.Contains(de.Message, tc.wantMsg) {
			t.Fatalf("%s: message = %q, want contains %q", tc.name, de.Message, tc.wantMsg)
		}
		if de.Error() != de.Code+": "+de.Message {
			t.Fatalf("%s: Error() = %q", tc.name, de.Error())
		}
	}
}

// TestCheckContractAllowEmptyStringSlice accepts a []string allow_empty.
func TestCheckContractAllowEmptyStringSlice(t *testing.T) {
	params := map[string]any{
		"output_schema": map[string]any{"note": "string", "name": "string"},
		"allow_empty":   []string{"note"},
	}
	got, de := CheckContract(params, map[string]any{"note": "", "name": "x"})
	if de != nil {
		t.Fatalf("drift = %v, want pass", de)
	}
	if want := map[string]any{"note": "", "name": "x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
}

// TestAgreementCases covers the Agreement definition of REQ-SP-007: leaf
// collection, exact and lenient string matching, and the empty case.
func TestAgreementCases(t *testing.T) {
	cases := []struct {
		name     string
		expected any
		got      any
		want     float64
		leaves   int
	}{
		{
			name:     "identical",
			expected: map[string]any{"a": "value", "b": 3},
			got:      map[string]any{"a": "value", "b": 3},
			want:     1,
			leaves:   2,
		},
		{
			name:     "half missing",
			expected: map[string]any{"a": "one", "b": "two"},
			got:      map[string]any{"a": "one"},
			want:     0.5,
			leaves:   2,
		},
		{
			name:     "empty expected",
			expected: map[string]any{},
			got:      map[string]any{"a": "x"},
			want:     1,
			leaves:   0,
		},
		{
			name:     "substring leniency expected inside got",
			expected: map[string]any{"t": "github trending repositories"},
			got:      map[string]any{"t": "top github trending repositories today"},
			want:     1,
			leaves:   1,
		},
		{
			name:     "substring leniency got inside expected",
			expected: map[string]any{"t": "a very long repository name"},
			got:      map[string]any{"t": "repository name"},
			want:     1,
			leaves:   1,
		},
		{
			name:     "short pair does not match",
			expected: map[string]any{"t": "abc"},
			got:      map[string]any{"t": "abcdefgh"},
			want:     0,
			leaves:   1,
		},
		{
			name:     "numbers only, booleans and nil skipped",
			expected: map[string]any{"n": 3, "b": true, "z": nil},
			got:      map[string]any{"n": 3.0},
			want:     1,
			leaves:   1,
		},
		{
			name:     "nested arrays and maps",
			expected: map[string]any{"rows": []any{map[string]any{"id": 1}, map[string]any{"id": 2}}},
			got:      []any{1, 2},
			want:     1,
			leaves:   2,
		},
	}
	for _, tc := range cases {
		score, leaves := Agreement(tc.expected, tc.got)
		if leaves != tc.leaves {
			t.Fatalf("%s: leaves = %d, want %d", tc.name, leaves, tc.leaves)
		}
		if score != tc.want {
			t.Fatalf("%s: score = %v, want %v", tc.name, score, tc.want)
		}
	}
}
