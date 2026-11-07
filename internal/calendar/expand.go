package calendar

import (
	"fmt"
	"time"
)

// maxOccurrences caps one Occurrences call.
const maxOccurrences = 2000

// maxExpandDays caps the days one Occurrences call walks.
const maxExpandDays = 100000

// civilDay is a calendar date with no zone. Repeats expand in calendar-day
// arithmetic on civil days, so a DST change never shifts an occurrence.
type civilDay struct {
	y int
	m time.Month
	d int
}

func dayOf(t time.Time, loc *time.Location) civilDay {
	y, m, d := t.In(loc).Date()
	return civilDay{y, m, d}
}

// parseDay parses a strict "YYYY-MM-DD" date.
func parseDay(s string) (civilDay, bool) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return civilDay{}, false
	}
	y, m, d := t.Date()
	return civilDay{y, m, d}, true
}

// parseClock parses a strict "HH:MM" time of day.
func parseClock(s string) (h, m int, ok bool) {
	if len(s) != 5 {
		return 0, 0, false
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, false
	}
	return t.Hour(), t.Minute(), true
}

func (c civilDay) utc() time.Time { return time.Date(c.y, c.m, c.d, 0, 0, 0, 0, time.UTC) }

func (c civilDay) add(n int) civilDay {
	y, m, d := time.Date(c.y, c.m, c.d+n, 0, 0, 0, 0, time.UTC).Date()
	return civilDay{y, m, d}
}

// ord is the number of days since 1970-01-01.
func (c civilDay) ord() int64 { return c.utc().Unix() / 86400 }

func (c civilDay) before(o civilDay) bool { return c.ord() < o.ord() }

func (c civilDay) weekday() time.Weekday { return c.utc().Weekday() }

func (c civilDay) String() string { return fmt.Sprintf("%04d-%02d-%02d", c.y, int(c.m), c.d) }

func (c civilDay) at(loc *time.Location, h, m, s int) time.Time {
	return time.Date(c.y, c.m, c.d, h, m, s, 0, loc)
}

func (c civilDay) midnight(loc *time.Location) time.Time { return c.at(loc, 0, 0, 0) }

var weekdayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// pattern is the expansion base of one item.
type pattern struct {
	base    civilDay
	h, m, s int
	timed   bool          // occurrences have a clock time; else they are dated (local midnight)
	span    time.Duration // timed events: End - Start; 0 = a point
	days    int           // dated: days covered, at least 1
	until   civilDay
	hasEnd  bool
	ok      bool
}

func patternOf(it Item, loc *time.Location) pattern {
	var p pattern
	switch {
	case it.Kind == KindReminder || (it.Kind == KindEvent && !it.AllDay):
		if it.Start == 0 {
			return p
		}
		start := time.UnixMilli(it.Start).In(loc)
		p.base = dayOf(start, loc)
		p.h, p.m, p.s = start.Clock()
		p.timed = true
		if it.Kind == KindEvent && it.End > it.Start {
			p.span = time.Duration(it.End-it.Start) * time.Millisecond
		}
	case it.Kind == KindEvent || it.Kind == KindNote || it.Kind == KindTask || it.Kind == KindHabit:
		base, ok := parseDay(it.Date)
		if !ok {
			return p
		}
		p.base = base
		p.days = 1
		if it.Kind == KindEvent {
			if end, ok := parseDay(it.EndDate); ok && !end.before(base) {
				p.days = int(end.ord()-base.ord()) + 1
			}
		}
		if it.Kind == KindTask || it.Kind == KindHabit {
			if h, m, ok := parseClock(it.Time); ok {
				p.h, p.m = h, m
				p.timed = true
				p.days = 0
			}
		}
	default:
		return p
	}
	if it.Repeat.Repeats() && it.Repeat.Until != "" {
		if until, ok := parseDay(it.Repeat.Until); ok {
			p.until, p.hasEnd = until, true
		}
	}
	p.ok = true
	return p
}

// instant is the occurrence instant on day d.
func (p pattern) instant(d civilDay, loc *time.Location) time.Time {
	if p.timed {
		return d.at(loc, p.h, p.m, p.s)
	}
	return d.midnight(loc)
}

// spanAt is the span of an occurrence starting at start; end == start for a point.
func (p pattern) spanAt(start time.Time, loc *time.Location) (time.Time, time.Time) {
	switch {
	case p.timed:
		return start, start.Add(p.span)
	default:
		d := dayOf(start, loc)
		return d.midnight(loc), d.add(p.days).midnight(loc)
	}
}

// lookback is how many days before a range an occurrence may start and still overlap it.
func (p pattern) lookback() int {
	if p.timed {
		return int(p.span / (24 * time.Hour))
	}
	return p.days - 1
}

// matches reports whether day d carries an occurrence of the item.
func (p pattern) matches(r Repeat, d civilDay) bool {
	if d.before(p.base) {
		return false
	}
	if p.hasEnd && p.until.before(d) {
		return false
	}
	switch r.Freq {
	case "", FreqNone:
		return d == p.base
	case FreqDaily:
		return true
	case FreqWeekdays:
		wd := d.weekday()
		return wd >= time.Monday && wd <= time.Friday
	case FreqWeekly:
		if len(r.Days) == 0 {
			return d.weekday() == p.base.weekday()
		}
		wd := d.weekday()
		for _, name := range r.Days {
			if w, ok := weekdayNames[name]; ok && w == wd {
				return true
			}
		}
		return false
	case FreqMonthly:
		return d.d == p.base.d
	case FreqYearly:
		return d.m == p.base.m && d.d == p.base.d
	}
	return false
}

// overlaps reports whether the span [s, e) (or the point s when e == s)
// overlaps [from, to).
func overlaps(s, e, from, to time.Time) bool {
	if !e.After(s) {
		return !s.Before(from) && s.Before(to)
	}
	return s.Before(to) && e.After(from)
}

// Occurrences returns the computed (original, pre-override) occurrence
// instants of it whose span overlaps [from, to), in order (REQ-CAL-003).
//
// The base day is the local day of Start (timed events, reminders) or Date
// (all-day events, tasks, notes, habits). The time of day is Start's local
// clock, or Time for tasks and habits, else local midnight. Repeats expand in
// civil-day arithmetic in loc, so occurrences keep their wall clock across a
// DST change. Monthly repeats on day 29–31 skip months without that day;
// yearly repeats on Feb 29 occur in leap years only; Until is inclusive. At
// most 2000 instants are returned.
func Occurrences(it Item, loc *time.Location, from, to time.Time) []time.Time {
	if loc == nil {
		loc = time.Local
	}
	p := patternOf(it, loc)
	if !p.ok || !from.Before(to) {
		return nil
	}
	first := dayOf(from, loc).add(-p.lookback() - 1)
	if first.before(p.base) {
		first = p.base
	}
	last := dayOf(to.Add(-time.Nanosecond), loc)
	if !it.Repeat.Repeats() {
		last = p.base
	} else if p.hasEnd && p.until.before(last) {
		last = p.until
	}
	var out []time.Time
	for d, n := first, 0; !last.before(d) && n < maxExpandDays; d, n = d.add(1), n+1 {
		if !p.matches(it.Repeat, d) {
			continue
		}
		occ := p.instant(d, loc)
		s, e := p.spanAt(occ, loc)
		if !overlaps(s, e, from, to) {
			continue
		}
		out = append(out, occ)
		if len(out) >= maxOccurrences {
			break
		}
	}
	return out
}

// OccurrenceSpan returns the span of the occurrence of it at occ. For point
// entries (reminders, timed tasks and habits) end equals start. Dated
// occurrences cover whole local days.
func OccurrenceSpan(it Item, occ time.Time, loc *time.Location) (start, end time.Time) {
	if loc == nil {
		loc = time.Local
	}
	return patternOf(it, loc).spanAt(occ, loc)
}

// IsOccurrence reports whether at is a computed occurrence instant of it.
func IsOccurrence(it Item, loc *time.Location, at time.Time) bool {
	for _, occ := range Occurrences(it, loc, at, at.Add(time.Millisecond)) {
		if occ.Equal(at) {
			return true
		}
	}
	return false
}

// DueAt is when the action of the occurrence at occ fires (contract
// "action"): the occurrence start, or 09:00 local for a task or habit
// without a time.
func DueAt(it Item, occ time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	if (it.Kind == KindTask || it.Kind == KindHabit) && !patternOf(it, loc).timed {
		return dayOf(occ, loc).at(loc, 9, 0, 0)
	}
	return occ
}
