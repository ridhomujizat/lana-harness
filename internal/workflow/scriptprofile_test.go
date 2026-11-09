package workflow

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// profileCase is one CheckProfile expectation: wantCode is the drift code, or ""
// for a pass, and path/signal are substrings of the message on a hit.
type profileCase struct {
	name     string
	params   map[string]any
	baseline []any
	out      any
	want     bool
	path     string
	signal   string
}

// profileRunCases runs the shared CheckProfile table (REQ-DG-002).
func profileRunCases(t *testing.T, cases []profileCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			de := CheckProfile(tc.params, tc.baseline, tc.out)
			if !tc.want {
				if de != nil {
					t.Fatalf("drift = %v, want nil", de)
				}
				return
			}
			if de == nil {
				t.Fatalf("no drift, want %s", DriftProfile)
			}
			if de.Code != DriftProfile {
				t.Fatalf("code = %q, want %q", de.Code, DriftProfile)
			}
			if tc.path != "" && !strings.Contains(de.Message, tc.path) {
				t.Fatalf("message = %q, want path %q", de.Message, tc.path)
			}
			if tc.signal != "" && !strings.Contains(de.Message, tc.signal+":") {
				t.Fatalf("message = %q, want signal %q", de.Message, tc.signal)
			}
		})
	}
}

// profileItems builds an output object holding one array field.
func profileItems(array string, elems ...map[string]any) map[string]any {
	arr := make([]any, len(elems))
	for i, e := range elems {
		arr[i] = e
	}
	return map[string]any{array: arr}
}

// profileNums builds item elements that all set one field.
func profileNums(field string, vals ...any) []map[string]any {
	out := make([]map[string]any, len(vals))
	for i, v := range vals {
		out[i] = map[string]any{field: v}
	}
	return out
}

// profileStrs builds item elements that all set one string field.
func profileStrs(field string, vals ...string) []map[string]any {
	out := make([]map[string]any, len(vals))
	for i, v := range vals {
		out[i] = map[string]any{field: v}
	}
	return out
}

// profileAB builds item elements with fields a and b for the copy signal.
func profileAB(pairs ...[2]string) []map[string]any {
	out := make([]map[string]any, len(pairs))
	for i, p := range pairs {
		out[i] = map[string]any{"a": p[0], "b": p[1]}
	}
	return out
}

// profileItemParams is the params of a node with one object[] array field.
func profileItemParams(item map[string]any) map[string]any {
	return map[string]any{
		"output_schema": map[string]any{"repos": "object[]"},
		"item_schema":   map[string]any{"repos": item},
	}
}

// profileItemParamsAllowEmpty is profileItemParams with an allow_empty list.
func profileItemParamsAllowEmpty(item map[string]any, allow []any) map[string]any {
	params := profileItemParams(item)
	params["allow_empty"] = allow
	return params
}

// TestProfileBaselineSkip checks that fewer than two baseline outputs skip the
// whole check (REQ-DG-001) even with a blatant drift.
func TestProfileBaselineSkip(t *testing.T) {
	params := profileItemParams(map[string]any{"n": "integer"})
	out := profileItems("repos", profileNums("n", 0, 0, 0)...)
	if de := CheckProfile(params, nil, out); de != nil {
		t.Fatalf("nil baseline: drift = %v, want nil", de)
	}
	if de := CheckProfile(params, []any{}, out); de != nil {
		t.Fatalf("empty baseline: drift = %v, want nil", de)
	}
	one := []any{profileItems("repos", profileNums("n", 5, 3, 7)...)}
	if de := CheckProfile(params, one, out); de != nil {
		t.Fatalf("single baseline: drift = %v, want nil", de)
	}
}

// TestProfileZeros covers the zeros signal of REQ-DG-002 for item and top-level
// number fields.
func TestProfileZeros(t *testing.T) {
	itemParams := profileItemParams(map[string]any{"n": "integer"})
	topParams := map[string]any{"output_schema": map[string]any{"n": "integer"}}
	cases := []profileCase{
		{
			name:   "item positive",
			params: itemParams,
			baseline: []any{
				profileItems("repos", profileNums("n", 5, 3, 7)...),
				profileItems("repos", profileNums("n", 2, 9, 4)...),
			},
			out:    profileItems("repos", profileNums("n", 0, 0, 0)...),
			want:   true,
			path:   "repos.n",
			signal: "zeros",
		},
		{
			name:   "item current below share",
			params: itemParams,
			baseline: []any{
				profileItems("repos", profileNums("n", 5, 3, 7)...),
				profileItems("repos", profileNums("n", 2, 9, 4)...),
			},
			out:  profileItems("repos", profileNums("n", 0, 0, 0, 6, 8)...),
			want: false,
		},
		{
			name:   "item boundary shares",
			params: itemParams,
			baseline: []any{
				profileItems("repos", profileNums("n", 0, 4, 4, 4, 4)...),
				profileItems("repos", profileNums("n", 5, 3, 7)...),
			},
			out:    profileItems("repos", profileNums("n", 0, 0, 0, 0, 5)...),
			want:   true,
			path:   "repos.n",
			signal: "zeros",
		},
		{
			name:   "item too few current values",
			params: itemParams,
			baseline: []any{
				profileItems("repos", profileNums("n", 5, 3, 7)...),
				profileItems("repos", profileNums("n", 2, 9, 4)...),
			},
			out:  profileItems("repos", profileNums("n", 0, 0)...),
			want: false,
		},
		{
			name:   "top-level positive",
			params: topParams,
			baseline: []any{
				map[string]any{"n": 5},
				map[string]any{"n": 3},
				map[string]any{"n": 7},
			},
			out:    map[string]any{"n": 0},
			want:   true,
			path:   "n",
			signal: "zeros",
		},
		{
			name:   "top-level baseline has a zero",
			params: topParams,
			baseline: []any{
				map[string]any{"n": 5},
				map[string]any{"n": 0},
				map[string]any{"n": 0},
			},
			out:  map[string]any{"n": 0},
			want: false,
		},
		{
			name:   "top-level baseline mostly non-zero",
			params: topParams,
			baseline: []any{
				map[string]any{"n": 5},
				map[string]any{"n": 0},
				map[string]any{"n": 7},
			},
			out:    map[string]any{"n": 0},
			want:   true,
			path:   "n",
			signal: "zeros",
		},
	}
	profileRunCases(t, cases)
}

// TestProfileConstant covers the constant signal of REQ-DG-002.
func TestProfileConstant(t *testing.T) {
	params := profileItemParams(map[string]any{"s": "string"})
	strs := func(vals ...string) map[string]any {
		return profileItems("repos", profileStrs("s", vals...)...)
	}
	cases := []profileCase{
		{
			name:   "item positive",
			params: params,
			baseline: []any{
				strs("a", "b", "c"),
				strs("d", "e", "f"),
			},
			out:    strs("x", "x", "x"),
			want:   true,
			path:   "repos.s",
			signal: "constant",
		},
		{
			name:   "item current varied",
			params: params,
			baseline: []any{
				strs("a", "b", "c"),
				strs("d", "e", "f"),
			},
			out:  strs("x", "y", "z"),
			want: false,
		},
		{
			name:   "item boundary share",
			params: params,
			baseline: []any{
				strs("a", "b", "c"),
				strs("d", "e", "f"),
			},
			out:    strs("x", "x", "x", "x", "y"),
			want:   true,
			path:   "repos.s",
			signal: "constant",
		},
		{
			name:   "item baseline not varied",
			params: params,
			baseline: []any{
				strs("a", "a", "a"),
				strs("b", "b", "b"),
			},
			out:  strs("x", "x", "x"),
			want: false,
		},
		{
			name:   "item too few current values",
			params: params,
			baseline: []any{
				strs("a", "b", "c"),
				strs("d", "e", "f"),
			},
			out:  strs("x", "x"),
			want: false,
		},
		{
			name:   "item baseline output too few values",
			params: params,
			baseline: []any{
				strs("a", "b"),
				strs("d", "e", "f"),
			},
			out:  strs("x", "x", "x"),
			want: false,
		},
		{
			name:   "top-level string cannot reach three values",
			params: map[string]any{"output_schema": map[string]any{"t": "string"}},
			baseline: []any{
				map[string]any{"t": "a"},
				map[string]any{"t": "b"},
				map[string]any{"t": "c"},
			},
			out:  map[string]any{"t": "x"},
			want: false,
		},
	}
	profileRunCases(t, cases)
}

// TestProfileLength covers the length signal of REQ-DG-002.
func TestProfileLength(t *testing.T) {
	params := profileItemParams(map[string]any{"s": "string"})
	long := strings.Repeat("a", 8)
	strs := func(vals ...string) map[string]any {
		return profileItems("repos", profileStrs("s", vals...)...)
	}
	cases := []profileCase{
		{
			name:   "item positive short",
			params: params,
			baseline: []any{
				strs(long, long, long),
				strs(long, long, long),
			},
			out:    strs("a", "b", "c"),
			want:   true,
			path:   "repos.s",
			signal: "length",
		},
		{
			name:   "item within ratio",
			params: params,
			baseline: []any{
				strs(long, long, long),
				strs(long, long, long),
			},
			out:  strs("abcd", "efgh", "ijkl"),
			want: false,
		},
		{
			name:   "item boundary low ratio",
			params: params,
			baseline: []any{
				strs(long, long, long),
				strs(long, long, long),
			},
			out:  strs("ab", "cd", "ef"),
			want: false,
		},
		{
			name:   "item boundary high ratio",
			params: params,
			baseline: []any{
				strs(long, long, long),
				strs(long, long, long),
			},
			out:  strs(strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)),
			want: false,
		},
		{
			name:   "top-level positive",
			params: map[string]any{"output_schema": map[string]any{"t": "string"}},
			baseline: []any{
				map[string]any{"t": long},
				map[string]any{"t": long},
				map[string]any{"t": long},
			},
			out:    map[string]any{"t": "a"},
			want:   true,
			path:   "t",
			signal: "length",
		},
		{
			name:   "item too few current values",
			params: params,
			baseline: []any{
				strs(long, long, long),
				strs(long, long, long),
			},
			out:  strs("a", "b"),
			want: false,
		},
	}
	profileRunCases(t, cases)
}

// TestProfileCopy covers the copy signal of REQ-DG-002.
func TestProfileCopy(t *testing.T) {
	params := profileItemParams(map[string]any{"a": "string", "b": "string"})
	base := []any{
		profileItems("repos", profileAB([2]string{"x", "bbbbbbbb"}, [2]string{"y", "bbbbbbbb"}, [2]string{"z", "bbbbbbbb"})...),
		profileItems("repos", profileAB([2]string{"p", "bbbbbbbb"}, [2]string{"q", "bbbbbbbb"}, [2]string{"r", "bbbbbbbb"})...),
	}
	cases := []profileCase{
		{
			name:     "positive",
			params:   params,
			baseline: base,
			out: profileItems("repos",
				profileAB([2]string{"d1 bbbbbbbb", "bbbbbbbb"}, [2]string{"d2 bbbbbbbb", "bbbbbbbb"}, [2]string{"d3 bbbbbbbb", "bbbbbbbb"})...),
			want:   true,
			path:   "repos.a",
			signal: "copy",
		},
		{
			name:     "current below share",
			params:   params,
			baseline: base,
			out: profileItems("repos",
				profileAB([2]string{"p1 bbbbbbbb", "bbbbbbbb"}, [2]string{"p2 bbbbbbbb", "bbbbbbbb"}, [2]string{"p3 bbbbbbbb", "bbbbbbbb"}, [2]string{"z1", "bbbbbbbb"}, [2]string{"z2", "bbbbbbbb"})...),
			want: false,
		},
		{
			name:   "baseline share too high",
			params: params,
			baseline: []any{
				profileItems("repos", profileAB([2]string{"q bbbbbbbb", "bbbbbbbb"}, [2]string{"q bbbbbbbb", "bbbbbbbb"}, [2]string{"q bbbbbbbb", "bbbbbbbb"})...),
				profileItems("repos", profileAB([2]string{"q bbbbbbbb", "bbbbbbbb"}, [2]string{"q bbbbbbbb", "bbbbbbbb"}, [2]string{"q bbbbbbbb", "bbbbbbbb"})...),
			},
			out: profileItems("repos",
				profileAB([2]string{"q1 bbbbbbbb", "bbbbbbbb"}, [2]string{"q2 bbbbbbbb", "bbbbbbbb"}, [2]string{"q3 bbbbbbbb", "bbbbbbbb"})...),
			want: false,
		},
		{
			name:   "boundary current share",
			params: params,
			baseline: []any{
				profileItems("repos", profileAB([2]string{"abc", "bbbbbbbb"}, [2]string{"def", "bbbbbbbb"}, [2]string{"ghi", "bbbbbbbb"})...),
				profileItems("repos", profileAB([2]string{"jkl", "bbbbbbbb"}, [2]string{"mno", "bbbbbbbb"}, [2]string{"pqr", "bbbbbbbb"})...),
			},
			out: profileItems("repos",
				profileAB([2]string{"p1 bbbbbbbb", "bbbbbbbb"}, [2]string{"p2 bbbbbbbb", "bbbbbbbb"}, [2]string{"p3 bbbbbbbb", "bbbbbbbb"}, [2]string{"p4 bbbbbbbb", "bbbbbbbb"}, [2]string{"zzzzzzzzz", "bbbbbbbb"})...),
			want:   true,
			path:   "repos.a",
			signal: "copy",
		},
		{
			name:     "too few current elements",
			params:   params,
			baseline: base,
			out: profileItems("repos",
				profileAB([2]string{"d1 bbbbbbbb", "bbbbbbbb"}, [2]string{"d2 bbbbbbbb", "bbbbbbbb"})...),
			want: false,
		},
		{
			name:   "b value too short",
			params: params,
			baseline: []any{
				profileItems("repos", profileAB([2]string{"abc", "bb"}, [2]string{"def", "bb"}, [2]string{"ghi", "bb"})...),
				profileItems("repos", profileAB([2]string{"jkl", "bb"}, [2]string{"mno", "bb"}, [2]string{"pqr", "bb"})...),
			},
			out: profileItems("repos",
				profileAB([2]string{"xbbx1", "bb"}, [2]string{"xbbx2", "bb"}, [2]string{"xbbx3", "bb"})...),
			want: false,
		},
	}
	profileRunCases(t, cases)
}

// TestProfileShape covers the shape signal of REQ-DG-002.
func TestProfileShape(t *testing.T) {
	params := profileItemParams(map[string]any{"s": "string"})
	strs := func(vals ...string) map[string]any {
		return profileItems("repos", profileStrs("s", vals...)...)
	}
	urlBase := []any{
		strs("https://example.com/one", "https://example.com/two", "https://example.com/three"),
		strs("https://example.com/four", "https://example.com/five", "https://example.com/six"),
	}
	cases := []profileCase{
		{
			name:     "item positive",
			params:   params,
			baseline: urlBase,
			out:      strs("plain text value one1", "plain text value two2", "plain text value 3333"),
			want:     true,
			path:     "repos.s",
			signal:   "shape",
		},
		{
			name:     "item same class",
			params:   params,
			baseline: urlBase,
			out:      strs("https://example.com/a", "https://example.com/b", "https://example.com/c"),
			want:     false,
		},
		{
			name:     "item boundary share",
			params:   params,
			baseline: urlBase,
			out: strs(
				"https://example.com/p", "https://example.com/q", "https://example.com/r",
				"some plain text value", "another text value", "third text sample"),
			want: false,
		},
		{
			name:     "item too few current values",
			params:   params,
			baseline: urlBase,
			out:      strs("plain text one", "plain text two"),
			want:     false,
		},
		{
			name:   "item baseline not dominant",
			params: params,
			baseline: []any{
				strs("https://example.com/a", "https://example.com/b", "https://example.com/c", "https://example.com/d", "plain one", "plain two"),
				strs("https://example.com/e", "https://example.com/f", "https://example.com/g", "https://example.com/h", "plain three", "plain four"),
			},
			out:  strs("plain text a", "plain text b", "plain text c"),
			want: false,
		},
	}
	profileRunCases(t, cases)
}

// TestProfileBlank covers the blank signal of REQ-DG-002.
func TestProfileBlank(t *testing.T) {
	params := profileItemParams(map[string]any{"d": "string"})
	params["allow_empty"] = []any{"repos.d"}
	strs := func(vals ...string) map[string]any {
		return profileItems("repos", profileStrs("d", vals...)...)
	}
	base := []any{
		strs("a", "b", "c"),
		strs("d", "e", "f"),
	}
	cases := []profileCase{
		{
			name:     "item positive",
			params:   params,
			baseline: base,
			out:      strs("", "", ""),
			want:     true,
			path:     "repos.d",
			signal:   "blank",
		},
		{
			name:     "item current below share",
			params:   params,
			baseline: base,
			out:      strs("", "", "", "x", "y"),
			want:     false,
		},
		{
			name:     "item boundary share",
			params:   params,
			baseline: base,
			out:      strs("", "", "", "", "z"),
			want:     true,
			path:     "repos.d",
			signal:   "blank",
		},
		{
			name:   "item baseline share too high",
			params: params,
			baseline: []any{
				strs("", "", "a"),
				strs("", "b"),
			},
			out:  strs("", "", ""),
			want: false,
		},
		{
			name:     "field not allow_empty",
			params:   profileItemParams(map[string]any{"d": "string"}),
			baseline: base,
			out:      strs("", "", ""),
			want:     false,
		},
		{
			name:     "item too few current values",
			params:   params,
			baseline: base,
			out:      strs("", ""),
			want:     false,
		},
	}
	profileRunCases(t, cases)
}

// TestProfileCount covers the count signal of REQ-DG-002.
func TestProfileCount(t *testing.T) {
	params := profileItemParams(map[string]any{"name": "string"})
	items := func(n int) map[string]any {
		vals := make([]string, n)
		for i := range vals {
			vals[i] = string(rune('a' + i%26))
		}
		return profileItems("repos", profileStrs("name", vals...)...)
	}
	cases := []profileCase{
		{
			name:     "positive",
			params:   params,
			baseline: []any{items(9), items(9)},
			out:      items(2),
			want:     true,
			path:     "repos",
			signal:   "count",
		},
		{
			name:     "boundary ratio",
			params:   params,
			baseline: []any{items(10), items(10)},
			out:      items(3),
			want:     false,
		},
		{
			name:     "below ratio",
			params:   params,
			baseline: []any{items(10), items(10)},
			out:      items(2),
			want:     true,
			path:     "repos",
			signal:   "count",
		},
		{
			name:     "baseline median too small",
			params:   params,
			baseline: []any{items(3), items(4)},
			out:      items(0),
			want:     false,
		},
		{
			name:     "applies with allow_empty",
			params:   profileItemParamsAllowEmpty(map[string]any{"name": "string"}, []any{"repos"}),
			baseline: []any{items(6), items(6)},
			out:      items(1),
			want:     true,
			path:     "repos",
			signal:   "count",
		},
	}
	profileRunCases(t, cases)
}

// TestProfileZerosVote covers the per-output baseline vote of REQ-DG-011 for the
// zeros signal: a strict majority of voting outputs must be normal, fewer than
// two voting outputs skip the signal, and outputs with fewer than profileMinValues
// values do not vote.
func TestProfileZerosVote(t *testing.T) {
	params := profileItemParams(map[string]any{"n": "integer"})
	good := func() map[string]any { return profileItems("repos", profileNums("n", 5, 3, 7)...) }
	bad := func() map[string]any { return profileItems("repos", profileNums("n", 0, 0, 0)...) }
	short := func() map[string]any { return profileItems("repos", profileNums("n", 0, 0)...) }
	out := profileItems("repos", profileNums("n", 0, 0, 0)...)
	cases := []profileCase{
		{
			name:     "two normal two bad voters do not fire",
			params:   params,
			baseline: []any{good(), good(), bad(), bad()},
			out:      out,
			want:     false,
		},
		{
			name:     "three normal two bad voters fire",
			params:   params,
			baseline: []any{good(), good(), good(), bad(), bad()},
			out:      out,
			want:     true,
			path:     "repos.n",
			signal:   "zeros",
		},
		{
			name:     "short outputs do not vote",
			params:   params,
			baseline: []any{good(), good(), bad(), short(), short()},
			out:      out,
			want:     true,
			path:     "repos.n",
			signal:   "zeros",
		},
		{
			name:     "short output cannot tip the vote",
			params:   params,
			baseline: []any{good(), good(), bad(), bad(), short()},
			out:      out,
			want:     false,
		},
		{
			name:     "one voting output skips the signal",
			params:   params,
			baseline: []any{good(), short()},
			out:      out,
			want:     false,
		},
	}
	profileRunCases(t, cases)
}

// TestProfileLengthMedian covers the REQ-DG-011 length reference: it is the median
// of the per-output mean rune lengths, so a single huge outlier output does not
// lift it the way a pooled mean would.
func TestProfileLengthMedian(t *testing.T) {
	params := profileItemParams(map[string]any{"s": "string"})
	eight := func(i int) string { return strings.Repeat(string(rune('a'+i)), 8) }
	huge := strings.Repeat("z", 500)
	base := []any{
		profileItems("repos", profileStrs("s", eight(0), eight(1), eight(2))...),
		profileItems("repos", profileStrs("s", eight(3), eight(4), eight(5))...),
		profileItems("repos", profileStrs("s", eight(6), eight(7), eight(8))...),
		profileItems("repos", profileStrs("s", eight(9), eight(10), eight(11))...),
		profileItems("repos", profileStrs("s", huge)...),
	}
	cases := []profileCase{
		{
			name:     "median reference ignores the pooled outlier",
			params:   params,
			baseline: base,
			out:      profileItems("repos", profileStrs("s", strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40))...),
			want:     true,
			path:     "repos.s",
			signal:   "length",
		},
		{
			name:     "within the ratio of the median reference",
			params:   params,
			baseline: base,
			out:      profileItems("repos", profileStrs("s", strings.Repeat("a", 20), strings.Repeat("b", 20), strings.Repeat("c", 20))...),
			want:     false,
		},
	}
	profileRunCases(t, cases)
}

// TestProfileTrendingFixture runs the AC-DG-001 live fixture: both silent-drift
// outputs drift against the five baseline outputs, and each baseline output
// checked against the other four passes.
func TestProfileTrendingFixture(t *testing.T) {
	fx := profileLoadTrending(t)
	if len(fx.Baseline) != 5 {
		t.Fatalf("baseline = %d, want 5", len(fx.Baseline))
	}
	params := profileTrendingParams()
	base := make([]any, len(fx.Baseline))
	for i, e := range fx.Baseline {
		base[i] = e.Output
	}
	for _, e := range fx.SilentDrift {
		de := CheckProfile(params, base, e.Output)
		if de == nil {
			t.Errorf("run %d: no drift, want %s", e.Run, DriftProfile)
			continue
		}
		if de.Code != DriftProfile {
			t.Errorf("run %d: code = %q, want %q", e.Run, de.Code, DriftProfile)
		}
		t.Logf("run %d: %s", e.Run, de.Message)
	}
	for i, e := range fx.Baseline {
		other := make([]any, 0, len(fx.Baseline)-1)
		for j, o := range fx.Baseline {
			if j == i {
				continue
			}
			other = append(other, o.Output)
		}
		if de := CheckProfile(params, other, e.Output); de != nil {
			t.Errorf("run %d against other four: drift = %v, want nil", e.Run, de)
		}
	}
}

// TestProfileTrendingDriftVote runs the AC-DG-007 contaminated baseline: three
// good and both silent-drift outputs vote, both silent-drift outputs drift, and
// each remaining good output passes. It proves two bad outputs of five cannot
// silence the copy signal (REQ-DG-011).
func TestProfileTrendingDriftVote(t *testing.T) {
	fx := profileLoadTrending(t)
	if len(fx.Baseline) < 5 || len(fx.SilentDrift) < 2 {
		t.Fatalf("fixture too small: baseline %d, silent drift %d", len(fx.Baseline), len(fx.SilentDrift))
	}
	params := profileTrendingParams()
	base := make([]any, 0, 5)
	for i := 0; i < 3; i++ {
		base = append(base, fx.Baseline[i].Output)
	}
	for _, e := range fx.SilentDrift {
		base = append(base, e.Output)
	}
	for _, e := range fx.SilentDrift {
		de := CheckProfile(params, base, e.Output)
		if de == nil {
			t.Errorf("silent drift run %d: no drift, want %s", e.Run, DriftProfile)
			continue
		}
		if de.Code != DriftProfile {
			t.Errorf("silent drift run %d: code = %q, want %q", e.Run, de.Code, DriftProfile)
		}
		t.Logf("silent drift run %d: %s", e.Run, de.Message)
	}
	for _, i := range []int{3, 4} {
		e := fx.Baseline[i]
		if de := CheckProfile(params, base, e.Output); de != nil {
			t.Errorf("good run %d against the voted baseline: drift = %v, want nil", e.Run, de)
		}
	}
}

// profileLoadTrending reads testdata/trending_profile.json (AC-DG-001, AC-DG-007).
func profileLoadTrending(t *testing.T) profileFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/trending_profile.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx profileFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return fx
}

// profileTrendingParams is the script_parse params of the trending fixture tests.
func profileTrendingParams() map[string]any {
	return map[string]any{
		"output_schema": map[string]any{"repos": "object[]", "digest": "string"},
		"item_schema": map[string]any{"repos": map[string]any{
			"name":        "string",
			"url":         "string",
			"description": "string",
			"stars_today": "integer",
		}},
		"allow_empty": []any{"repos.description"},
	}
}

// profileFixtureEntry is one output in the trending_profile.json fixture.
type profileFixtureEntry struct {
	Run    int            `json:"run"`
	Via    string         `json:"via"`
	Output map[string]any `json:"output"`
}

// profileFixture is the trending_profile.json fixture.
type profileFixture struct {
	Source      string                `json:"source"`
	Baseline    []profileFixtureEntry `json:"baseline"`
	SilentDrift []profileFixtureEntry `json:"silent_drift"`
}
