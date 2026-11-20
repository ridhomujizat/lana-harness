package app

import (
	"errors"
	"testing"
)

// nameCandidate is one platform name lookup a test injects: the string a
// command would print, or the error it would fail with.
type nameCandidate struct {
	out string
	err error
}

// nameFrom makes a lookup that returns the candidate's result.
func nameFrom(c nameCandidate) func() (string, error) {
	return func() (string, error) { return c.out, c.err }
}

// TestHostNameFrom is the name the chip shows, the first candidate that
// answers with one after trimming, ".local" and all (spec 58, window
// design).
func TestHostNameFrom(t *testing.T) {
	noName := nameCandidate{err: errors.New("no such key")}
	cases := []struct {
		name       string
		candidates []nameCandidate
		want       string
	}{
		{
			name:       "the computer name wins",
			candidates: []nameCandidate{{out: "ridho\u2019s MacBook Pro\n"}, {out: "ridhos-MacBook-Pro\n"}, {out: "192.168.254.37\n"}},
			want:       "ridho\u2019s MacBook Pro",
		},
		{
			name:       "the computer name is trimmed and loses .local",
			candidates: []nameCandidate{{out: "  studio.local \n"}, {out: "studio\n"}},
			want:       "studio",
		},
		{
			name:       "an unset computer name falls through to the local hostname",
			candidates: []nameCandidate{noName, {out: "ridhos-MacBook-Pro\n"}, {out: "192.168.254.37\n"}},
			want:       "ridhos-MacBook-Pro",
		},
		{
			name:       "an empty computer name falls through too",
			candidates: []nameCandidate{{out: "   \n"}, {out: "ridhos-MacBook-Pro"}},
			want:       "ridhos-MacBook-Pro",
		},
		{
			name:       "both scutil names unset falls through to the hostname",
			candidates: []nameCandidate{noName, noName, {out: "192.168.254.37\n"}},
			want:       "192.168.254.37",
		},
		{
			name:       "the hostname loses .local",
			candidates: []nameCandidate{noName, noName, {out: "mac.local\n"}},
			want:       "mac",
		},
		{
			name:       "a name that is only the suffix counts as empty",
			candidates: []nameCandidate{{out: ".local"}, {out: "ridhos-MacBook-Pro"}},
			want:       "ridhos-MacBook-Pro",
		},
		{
			name:       "no name at all",
			candidates: []nameCandidate{noName, {out: ""}, noName},
			want:       "",
		},
	}
	for _, tc := range cases {
		candidates := make([]func() (string, error), len(tc.candidates))
		for i, c := range tc.candidates {
			candidates[i] = nameFrom(c)
		}
		if got := hostNameFrom(candidates...); got != tc.want {
			t.Errorf("%s: hostNameFrom(%v) = %q, want %q", tc.name, tc.candidates, got, tc.want)
		}
	}
}

// TestHostNameFromStops is the first answer, not the best one: a later
// candidate is never asked once an earlier one answers.
func TestHostNameFromStops(t *testing.T) {
	asked := false
	second := func() (string, error) {
		asked = true
		return "second", nil
	}
	if got := hostNameFrom(nameFrom(nameCandidate{out: "first"}), second); got != "first" {
		t.Errorf("hostNameFrom = %q, want first", got)
	}
	if asked {
		t.Error("hostNameFrom asked the second candidate after the first answered")
	}
}

// TestCleanHostName is a system name as the chip shows it.
func TestCleanHostName(t *testing.T) {
	cases := map[string]string{
		"ridho\u2019s MacBook Pro\n": "ridho\u2019s MacBook Pro",
		"  studio.local ":            "studio",
		"ridhos-MacBook-Pro":         "ridhos-MacBook-Pro",
		"mac.local":                  "mac",
		".local":                     "",
		"":                           "",
	}
	for in, want := range cases {
		if got := cleanHostName(in); got != want {
			t.Errorf("cleanHostName(%q) = %q, want %q", in, got, want)
		}
	}
}
