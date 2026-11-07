package calendar

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func dayKeys(ts []time.Time, loc *time.Location) []string {
	out := make([]string, len(ts))
	for i, ts := range ts {
		out[i] = ts.In(loc).Format("2006-01-02 15:04")
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AC-CAL-005: a weekly repeat on Mon and Thu that ends on a Thursday.
func TestExpandWeeklyUntilACCAL005(t *testing.T) {
	loc := mustLoc(t, "Asia/Jakarta")
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, loc) // a Monday
	it := Item{Kind: KindEvent, Title: "Gym", Start: start.UnixMilli(), End: start.Add(time.Hour).UnixMilli(),
		Repeat: Repeat{Freq: FreqWeekly, Days: []string{"mon", "thu"}, Until: "2026-11-19"}}
	got := dayKeys(Occurrences(it, loc, time.Date(2026, 11, 1, 0, 0, 0, 0, loc), time.Date(2026, 12, 1, 0, 0, 0, 0, loc)), loc)
	want := []string{"2026-11-02 09:00", "2026-11-05 09:00", "2026-11-09 09:00", "2026-11-12 09:00", "2026-11-16 09:00", "2026-11-19 09:00"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestExpandMonthlySkipsShortMonths(t *testing.T) {
	loc := mustLoc(t, "Asia/Jakarta")
	it := Item{Kind: KindTask, Title: "Rent", Date: "2026-01-31", Repeat: Repeat{Freq: FreqMonthly}}
	got := dayKeys(Occurrences(it, loc, time.Date(2026, 1, 1, 0, 0, 0, 0, loc), time.Date(2026, 8, 1, 0, 0, 0, 0, loc)), loc)
	want := []string{"2026-01-31 00:00", "2026-03-31 00:00", "2026-05-31 00:00", "2026-07-31 00:00"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestExpandYearlyLeapDay(t *testing.T) {
	loc := time.UTC
	it := Item{Kind: KindTask, Title: "Leap", Date: "2024-02-29", Time: "08:00", Repeat: Repeat{Freq: FreqYearly}}
	got := dayKeys(Occurrences(it, loc, time.Date(2023, 1, 1, 0, 0, 0, 0, loc), time.Date(2030, 1, 1, 0, 0, 0, 0, loc)), loc)
	want := []string{"2024-02-29 08:00", "2028-02-29 08:00"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

// A daily repeat keeps its wall clock across the US spring-forward change
// (2026-03-08 in America/New_York).
func TestExpandDailyAcrossDST(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	start := time.Date(2026, 3, 6, 9, 0, 0, 0, loc)
	it := Item{Kind: KindReminder, Title: "Pills", Start: start.UnixMilli(), Repeat: Repeat{Freq: FreqDaily}}
	occ := Occurrences(it, loc, time.Date(2026, 3, 6, 0, 0, 0, 0, loc), time.Date(2026, 3, 11, 0, 0, 0, 0, loc))
	got := dayKeys(occ, loc)
	want := []string{"2026-03-06 09:00", "2026-03-07 09:00", "2026-03-08 09:00", "2026-03-09 09:00", "2026-03-10 09:00"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	if d := occ[2].Sub(occ[1]); d != 23*time.Hour {
		t.Fatalf("Mar 7 → Mar 8 = %v, want 23h", d)
	}
}

func TestExpandWeekdays(t *testing.T) {
	loc := time.UTC
	it := Item{Kind: KindHabit, Title: "Stretch", Date: "2026-11-01", Repeat: Repeat{Freq: FreqWeekdays}}
	got := dayKeys(Occurrences(it, loc, time.Date(2026, 11, 1, 0, 0, 0, 0, loc), time.Date(2026, 11, 9, 0, 0, 0, 0, loc)), loc)
	want := []string{"2026-11-02 00:00", "2026-11-03 00:00", "2026-11-04 00:00", "2026-11-05 00:00", "2026-11-06 00:00"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestExpandOverlapAtRangeStart(t *testing.T) {
	loc := mustLoc(t, "Asia/Jakarta")
	from := time.Date(2026, 11, 4, 0, 0, 0, 0, loc)
	to := from.AddDate(0, 0, 3)
	allDay := Item{Kind: KindEvent, Title: "Trip", AllDay: true, Date: "2026-11-03", EndDate: "2026-11-05"}
	if got := dayKeys(Occurrences(allDay, loc, from, to), loc); !equalStrings(got, []string{"2026-11-03 00:00"}) {
		t.Fatalf("multi-day all-day: %v", got)
	}
	start := time.Date(2026, 11, 3, 23, 0, 0, 0, loc)
	night := Item{Kind: KindEvent, Title: "Night", Start: start.UnixMilli(), End: start.Add(2 * time.Hour).UnixMilli()}
	if got := dayKeys(Occurrences(night, loc, from, to), loc); !equalStrings(got, []string{"2026-11-03 23:00"}) {
		t.Fatalf("timed crossing from: %v", got)
	}
	ended := Item{Kind: KindEvent, Title: "Earlier", Start: start.Add(-2 * time.Hour).UnixMilli(), End: start.UnixMilli()}
	if got := Occurrences(ended, loc, from, to); len(got) != 0 {
		t.Fatalf("ended before range: %v", got)
	}
	reminder := Item{Kind: KindReminder, Title: "Late", Start: start.UnixMilli()}
	if got := Occurrences(reminder, loc, from, to); len(got) != 0 {
		t.Fatalf("point before range: %v", got)
	}
}

func TestExpandNeverBeforeBase(t *testing.T) {
	loc := time.UTC
	it := Item{Kind: KindHabit, Title: "Read", Date: "2026-11-05", Repeat: Repeat{Freq: FreqDaily}}
	got := Occurrences(it, loc, time.Date(2026, 11, 1, 0, 0, 0, 0, loc), time.Date(2026, 11, 7, 0, 0, 0, 0, loc))
	if len(got) != 2 {
		t.Fatalf("got %v", dayKeys(got, loc))
	}
	single := Item{Kind: KindNote, Title: "n", Date: "2026-12-01"}
	if got := Occurrences(single, loc, time.Date(2026, 11, 1, 0, 0, 0, 0, loc), time.Date(2026, 11, 7, 0, 0, 0, 0, loc)); len(got) != 0 {
		t.Fatalf("single outside: %v", got)
	}
	if got := Occurrences(Item{Kind: KindTask, Title: "bad"}, loc, time.Unix(0, 0), time.Now()); got != nil {
		t.Fatalf("invalid item: %v", got)
	}
}

func TestExpandCap(t *testing.T) {
	loc := time.UTC
	start := time.Date(2020, 1, 1, 8, 0, 0, 0, loc)
	it := Item{Kind: KindReminder, Title: "Daily", Start: start.UnixMilli(), Repeat: Repeat{Freq: FreqDaily}}
	if got := Occurrences(it, loc, start, start.AddDate(10, 0, 0)); len(got) != maxOccurrences {
		t.Fatalf("len %d, want %d", len(got), maxOccurrences)
	}
}

func TestOccurrenceHelpers(t *testing.T) {
	loc := mustLoc(t, "Asia/Jakarta")
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, loc)
	ev := Item{Kind: KindEvent, Title: "Gym", Start: start.UnixMilli(), End: start.Add(90 * time.Minute).UnixMilli(),
		Repeat: Repeat{Freq: FreqDaily}}
	occ := start.AddDate(0, 0, 3)
	if !IsOccurrence(ev, loc, occ) {
		t.Fatal("Nov 5 09:00 should be an occurrence")
	}
	if IsOccurrence(ev, loc, occ.Add(time.Minute)) || IsOccurrence(ev, loc, start.AddDate(0, 0, -1)) {
		t.Fatal("off-grid instants are not occurrences")
	}
	s, e := OccurrenceSpan(ev, occ, loc)
	if !s.Equal(occ) || e.Sub(s) != 90*time.Minute {
		t.Fatalf("span %v–%v", s, e)
	}
	task := Item{Kind: KindTask, Title: "t", Date: "2026-11-05"}
	mid := time.Date(2026, 11, 5, 0, 0, 0, 0, loc)
	if !IsOccurrence(task, loc, mid) {
		t.Fatal("task midnight is its occurrence")
	}
	if due := DueAt(task, mid, loc); !due.Equal(mid.Add(9 * time.Hour)) {
		t.Fatalf("task due %v", due)
	}
	s, e = OccurrenceSpan(task, mid, loc)
	if !s.Equal(mid) || !e.Equal(mid.AddDate(0, 0, 1)) {
		t.Fatalf("task span %v–%v", s, e)
	}
	timedTask := Item{Kind: KindTask, Title: "t", Date: "2026-11-05", Time: "15:30"}
	at := mid.Add(15*time.Hour + 30*time.Minute)
	if due := DueAt(timedTask, at, loc); !due.Equal(at) {
		t.Fatalf("timed task due %v", due)
	}
	if s, e := OccurrenceSpan(timedTask, at, loc); !s.Equal(at) || !e.Equal(at) {
		t.Fatalf("point span %v–%v", s, e)
	}
}
