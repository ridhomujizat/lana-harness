package main

import "testing"

func TestVersionString(t *testing.T) {
	origVersion, origCommit, origBuildDate := version, commit, buildDate
	t.Cleanup(func() {
		version, commit, buildDate = origVersion, origCommit, origBuildDate
	})

	cases := []struct {
		name      string
		version   string
		commit    string
		buildDate string
		want      string
	}{
		{name: "neither set", version: "1.2.3", want: "lana 1.2.3"},
		{name: "commit only", version: "1.2.3", commit: "abc123", want: "lana 1.2.3 (abc123)"},
		{name: "commit and build date", version: "1.2.3", commit: "abc123", buildDate: "2026-11-01", want: "lana 1.2.3 (abc123, 2026-11-01)"},
		{name: "build date only", version: "1.2.3", buildDate: "2026-11-01", want: "lana 1.2.3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			version, commit, buildDate = tc.version, tc.commit, tc.buildDate
			if got := versionString(); got != tc.want {
				t.Fatalf("versionString() = %q, want %q", got, tc.want)
			}
		})
	}
}
