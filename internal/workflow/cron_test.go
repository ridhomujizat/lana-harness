package workflow

import (
	"strings"
	"testing"
	"time"

	_ "time/tzdata" // embed the zone database so Asia/Jakarta works everywhere
)

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

// TestCronMatchesJakarta covers AC-WF-003: 0 7 * * * fires at 07:00 Jakarta,
// not at 07:01.
func TestCronMatchesJakarta(t *testing.T) {
	loc := mustLocation(t, "Asia/Jakarta")
	c, err := ParseCron("0 7 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Matches(time.Date(2026, 1, 15, 7, 0, 0, 0, loc)) {
		t.Fatal("07:00 Jakarta should match")
	}
	if c.Matches(time.Date(2026, 1, 15, 7, 1, 0, 0, loc)) {
		t.Fatal("07:01 Jakarta should not match")
	}
	// Matches reads the time in its own location, so the same instant in UTC
	// (00:00) must not match.
	if c.Matches(time.Date(2026, 1, 15, 7, 0, 0, 0, loc).UTC()) {
		t.Fatal("the same instant in UTC should not match")
	}
}

// TestCronParts covers lists, ranges, steps, names and dow 0/7.
func TestCronParts(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		expr  string
		t     time.Time
		match bool
	}{
		{"*/15 * * * *", time.Date(2026, 1, 1, 0, 0, 0, 0, loc), true},
		{"*/15 * * * *", time.Date(2026, 1, 1, 0, 45, 0, 0, loc), true},
		{"*/15 * * * *", time.Date(2026, 1, 1, 0, 10, 0, 0, loc), false},
		{"0 9 * * 1-5", time.Date(2026, 1, 5, 9, 0, 0, 0, loc), true},   // Monday
		{"0 9 * * 1-5", time.Date(2026, 1, 11, 9, 0, 0, 0, loc), false}, // Sunday
		{"0,30 8 * * *", time.Date(2026, 1, 5, 8, 30, 0, 0, loc), true},
		{"0,30 8 * * *", time.Date(2026, 1, 5, 8, 15, 0, 0, loc), false},
		{"0 8 * * 1", time.Date(2026, 1, 5, 8, 0, 0, 0, loc), true},
		{"0 8 * * 1", time.Date(2026, 1, 6, 8, 0, 0, 0, loc), false},
		{"0 0 * * sun", time.Date(2026, 2, 1, 0, 0, 0, 0, loc), true},
		{"0 0 * * 7", time.Date(2026, 2, 1, 0, 0, 0, 0, loc), true},
		{"0 0 * * 0", time.Date(2026, 2, 1, 0, 0, 0, 0, loc), true},
		{"0 0 * jan-mar *", time.Date(2026, 2, 10, 0, 0, 0, 0, loc), true},
		{"0 0 * jan-mar *", time.Date(2026, 4, 10, 0, 0, 0, 0, loc), false},
		{"0 0 1-15/5 * *", time.Date(2026, 3, 11, 0, 0, 0, 0, loc), true},
	}
	for _, tc := range cases {
		c, err := ParseCron(tc.expr)
		if err != nil {
			t.Fatalf("ParseCron(%q): %v", tc.expr, err)
		}
		if got := c.Matches(tc.t); got != tc.match {
			t.Fatalf("%s at %s = %v, want %v", tc.expr, tc.t, got, tc.match)
		}
	}
}

// TestCronDomDowOr covers the standard rule: when both dom and dow are
// restricted a day matches when either matches.
func TestCronDomDowOr(t *testing.T) {
	c, err := ParseCron("0 0 1 * 1")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Matches(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)) { // Monday, not the 1st
		t.Fatal("Monday should match through dow")
	}
	if !c.Matches(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) { // the 1st, not Monday
		t.Fatal("the 1st should match through dom")
	}
	if c.Matches(time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC)) { // Tuesday, not the 1st
		t.Fatal("Tuesday the 6th should not match")
	}
}

// TestCronMacros covers @hourly, @daily, @weekly and @monthly.
func TestCronMacros(t *testing.T) {
	cases := []struct {
		expr  string
		t     time.Time
		match bool
	}{
		{"@hourly", time.Date(2026, 1, 5, 3, 0, 0, 0, time.UTC), true},
		{"@hourly", time.Date(2026, 1, 5, 3, 1, 0, 0, time.UTC), false},
		{"@daily", time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), true},
		{"@weekly", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), true}, // Sunday
		{"@monthly", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), true},
		{"@monthly", time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), false},
	}
	for _, tc := range cases {
		c, err := ParseCron(tc.expr)
		if err != nil {
			t.Fatalf("ParseCron(%q): %v", tc.expr, err)
		}
		if got := c.Matches(tc.t); got != tc.match {
			t.Fatalf("%s at %s = %v, want %v", tc.expr, tc.t, got, tc.match)
		}
	}
}

// TestCronParseErrors rejects malformed expressions and out-of-range fields.
func TestCronParseErrors(t *testing.T) {
	for _, expr := range []string{
		"0 7 * *",     // too few fields
		"0 7 * * * *", // too many fields
		"60 7 * * *",  // minute out of range
		"0 24 * * *",  // hour out of range
		"0 7 0 * *",   // dom out of range
		"0 7 * 13 *",  // month out of range
		"0 7 * * 9",   // dow out of range
		"*/0 * * * *", // zero step
		"x 7 * * *",   // bad value
	} {
		if _, err := ParseCron(expr); err == nil {
			t.Fatalf("ParseCron(%q) = nil error, want error", expr)
		}
	}
}

// TestCronNextFirstAfter covers the strictly-after rule and crosses a month end.
func TestCronNextFirstAfter(t *testing.T) {
	loc := mustLocation(t, "Asia/Jakarta")
	c, err := ParseCron("0 7 * * *")
	if err != nil {
		t.Fatal(err)
	}
	// The same minute as a match must roll to the next day.
	after := time.Date(2026, 1, 15, 7, 0, 0, 0, loc)
	if got := c.Next(after); !got.Equal(time.Date(2026, 1, 16, 7, 0, 0, 0, loc)) {
		t.Fatalf("Next(%s) = %s, want next day 07:00", after, got)
	}
	// Crossing a month end.
	after = time.Date(2026, 1, 31, 8, 0, 0, 0, loc)
	if got := c.Next(after); !got.Equal(time.Date(2026, 2, 1, 7, 0, 0, 0, loc)) {
		t.Fatalf("Next(%s) = %s, want Feb 1 07:00", after, got)
	}
}

// TestCronNextImpossible returns the zero time when nothing matches.
func TestCronNextImpossible(t *testing.T) {
	c, err := ParseCron("0 0 30 2 *") // February 30 never happens
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); !got.IsZero() {
		t.Fatalf("Next = %s, want zero time", got)
	}
}

// TestDescribeCron covers the short English descriptions and the fallback.
func TestDescribeCron(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		{"0 7 * * *", "Every day 07:00"},
		{"0 8 * * 1", "Mondays 08:00"},
		{"*/15 * * * *", "Every 15 min"},
		{"0 * * * *", "Every hour"},
		{"0 9 * * 1-5", "Weekdays 09:00"},
		{"0 0 * * sun", "Sundays 00:00"},
		{"nonsense", "nonsense"},
	}
	for _, tc := range cases {
		if got := DescribeCron(tc.expr); got != tc.want {
			t.Fatalf("DescribeCron(%q) = %q, want %q", tc.expr, got, tc.want)
		}
	}
	if got := DescribeCron("0 0 1 * *"); !strings.Contains(got, "0 0 1 * *") {
		t.Fatalf("DescribeCron(fallback) = %q, want the expression", got)
	}
}
