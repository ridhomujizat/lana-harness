package knowledge

import (
	"strings"
	"testing"
)

func TestScopeOf(t *testing.T) {
	tests := []struct {
		rel  string
		want string
	}{
		{"wiki/Gaji.md", ScopeShared},
		{"sources/2026-11-01 Rapat.pdf", ScopeShared},
		{"log.md", ScopeShared},
		{"agents/dana/wiki/Gaji.md", "dana"},
		{"agents/dana/index.md", "dana"},
		{"agents/rio/journal/2026-11-01.md", "rio"},
		{"agents/shared/wiki/x.md", ScopeShared},
		{"agents/Dana/wiki/x.md", ScopeShared},
		{"agents/index.md", ScopeShared},
	}
	for _, tc := range tests {
		if got := ScopeOf(tc.rel); got != tc.want {
			t.Errorf("ScopeOf(%q) = %q, want %q", tc.rel, got, tc.want)
		}
	}
}

func TestWikiDir(t *testing.T) {
	tests := []struct {
		scope string
		want  string
	}{
		{"", "wiki"},
		{ScopeShared, "wiki"},
		{"dana", "agents/dana/wiki"},
	}
	for _, tc := range tests {
		if got := WikiDir(tc.scope); got != tc.want {
			t.Errorf("WikiDir(%q) = %q, want %q", tc.scope, got, tc.want)
		}
	}
}

func TestJournalDir(t *testing.T) {
	tests := []struct {
		scope string
		want  string
	}{
		{"", "journal"},
		{ScopeShared, "journal"},
		{"dana", "agents/dana/journal"},
	}
	for _, tc := range tests {
		if got := JournalDir(tc.scope); got != tc.want {
			t.Errorf("JournalDir(%q) = %q, want %q", tc.scope, got, tc.want)
		}
	}
}

func TestValidScopeName(t *testing.T) {
	valid := []string{"dana", "a", "a_b", "a-b", "agent9", "a" + strings.Repeat("b", 31)}
	for _, name := range valid {
		if !ValidScopeName(name) {
			t.Errorf("ValidScopeName(%q) = false, want true", name)
		}
	}
	invalid := []string{"", "Dana", "1dana", "_dana", "dana!", ScopeShared, "a" + strings.Repeat("b", 32)}
	for _, name := range invalid {
		if ValidScopeName(name) {
			t.Errorf("ValidScopeName(%q) = true, want false", name)
		}
	}
}
