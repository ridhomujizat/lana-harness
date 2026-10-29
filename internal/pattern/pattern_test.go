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
		t.Fatal("MatchBoth harus salah untuk alt tidak cocok")
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
