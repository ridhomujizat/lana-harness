package workbench

import (
	"testing"
	"time"
)

func TestParseOSC7(t *testing.T) {
	cases := []struct{ in, want string }{
		{"7;file://host/tmp/a%20b", "/tmp/a b"},
		{"7;file://localhost/Users/x", "/Users/x"},
		{"7;file://host/", "/"},
		{"7;/tmp/plain", "/tmp/plain"},
		{"7;", ""},
		{"4;1;50", ""},
		{"", ""},
		{"7;file://host/a%zz", ""},
	}
	for _, c := range cases {
		if got := parseOSC7([]byte(c.in)); got != c.want {
			t.Errorf("parseOSC7(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSameTrackedIncludesCwd(t *testing.T) {
	a := Session{Cwd: "/one"}
	b := Session{Cwd: "/two"}
	if sameTracked(a, b) {
		t.Fatal("a working directory change was not tracked")
	}
	b.Cwd = "/one"
	if !sameTracked(a, b) {
		t.Fatal("equal snapshots were reported as changed")
	}
}

func TestScreenCapturesOSC7(t *testing.T) {
	var dir string
	sc := newScreen(80, 24, func(string) {}, func(d string) { dir = d })
	sc.write([]byte("\x1b]7;file://host/tmp/a%20b\x07"))
	if dir != "/tmp/a b" {
		t.Fatalf("working directory = %q, want %q", dir, "/tmp/a b")
	}
	sc.write([]byte("\x1b]7;file://host/tmp/c\x1b\\"))
	if dir != "/tmp/c" {
		t.Fatalf("working directory = %q, want %q", dir, "/tmp/c")
	}
}

func TestMergeCwd(t *testing.T) {
	past := time.Now().Add(-time.Hour)

	// A process read that repeats the directory the session started in is not a
	// change, so an OSC 7 report wins.
	s := &session{procCwd: "/start", oscDir: "/osc", oscAt: time.Now()}
	s.mergeCwdLocked("/start")
	if s.cwd != "/osc" {
		t.Fatalf("repeated read: cwd = %q, want /osc", s.cwd)
	}

	// With no OSC 7 report the process read is used.
	s = &session{procCwd: "/start"}
	s.mergeCwdLocked("/other")
	if s.cwd != "/other" {
		t.Fatalf("no osc: cwd = %q, want /other", s.cwd)
	}

	// A process read that changes to another directory after the OSC 7 report
	// wins over it.
	s = &session{procCwd: "/start", oscDir: "/osc", oscAt: past}
	s.mergeCwdLocked("/other")
	if s.cwd != "/other" {
		t.Fatalf("newer process read: cwd = %q, want /other", s.cwd)
	}

	// An OSC 7 report that arrives after the process read wins over it.
	s = &session{procCwd: "/start"}
	s.mergeCwdLocked("/other")
	s.onWorkingDirectory("/osc")
	s.mergeCwdLocked("/other")
	if s.cwd != "/osc" {
		t.Fatalf("newer osc: cwd = %q, want /osc", s.cwd)
	}
}
