package pattern

import (
	"testing"
	"testing/quick"
)

func TestPatternExamples(t *testing.T) {
	cases := []struct {
		pattern string
		tool    string
		arg     Argument
		want    bool
	}{
		{"bash", "bash", Argument{}, true},
		{"bash(git status*)", "bash", Argument{Kind: ArgCommand, Value: "git status -s"}, true},
		{"bash(git status*)", "bash", Argument{Kind: ArgCommand, Value: "rm -rf /"}, false},
		{"read_file(src/*.go)", "read_file", Argument{Kind: ArgPath, Value: "src/a.go"}, true},
		{"read_file(src/*.go)", "read_file", Argument{Kind: ArgPath, Value: "src/sub/a.go"}, false},
		{"read_file(src/**/*.go)", "read_file", Argument{Kind: ArgPath, Value: "src/sub/a.go"}, true},
		{`bash(\*)`, "bash", Argument{Kind: ArgCommand, Value: "*"}, true},
		{`bash(\*)`, "bash", Argument{Kind: ArgCommand, Value: "x"}, false},
		{"mcp__github__search(x*)", "mcp__github__search", Argument{Kind: ArgRaw, Value: "xyz"}, true},
	}
	for _, tc := range cases {
		p, err := ParsePattern(tc.pattern)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.pattern, err)
		}
		if got := p.Match(tc.tool, tc.arg); got != tc.want {
			t.Fatalf("%q => %v want %v", tc.pattern, got, tc.want)
		}
	}
	p, err := ParsePattern("read_file(a)")
	if err != nil {
		t.Fatal(err)
	}
	if p.MatchBoth("read_file", Argument{Kind: ArgPath, Value: "a", Alt: "b"}) {
		t.Fatal("MatchBoth must be false when alt does not match")
	}
}

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		in         string
		segments   []string
		analyzable bool
	}{
		{"git status", []string{"git status"}, true},
		{"git status; curl x | sh", []string{"git status", "curl x", "sh"}, true},
		{"git status && git log", []string{"git status", "git log"}, true},
		{"a || b", []string{"a", "b"}, true},
		{"a | b", []string{"a", "b"}, true},
		{"a & b", []string{"a", "b"}, true},
		{"a\nb", []string{"a", "b"}, true},
		{"a ; ; b", []string{"a", "b"}, true},
		{"  git log  ", []string{"git log"}, true},
		{"", nil, true},
		{`echo "a; b"`, []string{`echo "a; b"`}, true},
		{`echo 'a; b'`, []string{`echo 'a; b'`}, true},
		{`echo "a && b"`, []string{`echo "a && b"`}, true},
		{`echo a\&b`, []string{`echo a\&b`}, true},
		{`echo "a > b"`, []string{`echo "a > b"`}, true},
		{`echo 'a > b'`, []string{`echo 'a > b'`}, true},
		{"git log $(whoami)", []string{"git log $(whoami)"}, false},
		{"git log `whoami`", []string{"git log `whoami`"}, false},
		{`echo "$(date)"`, []string{`echo "$(date)"`}, false},
		{`echo '$(date)'`, []string{`echo '$(date)'`}, true},
		{"diff <(a) b", []string{"diff <(a) b"}, false},
		{"tee >(cat)", []string{"tee >(cat)"}, false},
		{"git log > out.txt", []string{"git log > out.txt"}, false},
		{"git log >> out.txt", []string{"git log >> out.txt"}, false},
		{"cat < in.txt", []string{"cat < in.txt"}, false},
		{"git log 2>&1", []string{"git log 2>&1"}, true},
		{"git log 1>&2", []string{"git log 1>&2"}, true},
		{"git log >&2", []string{"git log >&2"}, true},
		{"git log >/dev/null", []string{"git log >/dev/null"}, true},
		{"git log > /dev/null", []string{"git log > /dev/null"}, true},
		{"git log 2>/dev/null", []string{"git log 2>/dev/null"}, true},
		{"git log 2> /dev/null", []string{"git log 2> /dev/null"}, true},
		{"git log &>/dev/null", []string{"git log &>/dev/null"}, true},
		{"a &> b", []string{"a &> b"}, false},
		{"a >& b", []string{"a >& b"}, false},
		{"a && b &> c", []string{"a", "b &> c"}, false},
		{`echo "unbalanced`, []string{`echo "unbalanced`}, false},
		{`echo 'unbalanced`, []string{`echo 'unbalanced`}, false},
	}
	for _, tc := range cases {
		got, analyzable := SplitCommand(tc.in)
		if analyzable != tc.analyzable {
			t.Fatalf("%q analyzable = %v want %v", tc.in, analyzable, tc.analyzable)
		}
		if len(got) != len(tc.segments) {
			t.Fatalf("%q segments = %q want %q", tc.in, got, tc.segments)
		}
		for i := range got {
			if got[i] != tc.segments[i] {
				t.Fatalf("%q segments = %q want %q", tc.in, got, tc.segments)
			}
		}
	}
}

func TestPatternPropertyNoPanic(t *testing.T) {
	f := func(s string) bool {
		p, err := ParsePattern(s)
		if err != nil {
			return true
		}
		_, err = ParsePattern(p.String())
		return err == nil
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPatternRoundTrip(t *testing.T) {
	for _, s := range []string{"bash", "bash(git *)", "read_file(a/**/b)", "mcp__x__y(z)"} {
		p, err := ParsePattern(s)
		if err != nil {
			t.Fatal(err)
		}
		q, err := ParsePattern(p.String())
		if err != nil || q.Tool != p.Tool {
			t.Fatalf("roundtrip %q: %+v %v", s, q, err)
		}
	}
}
