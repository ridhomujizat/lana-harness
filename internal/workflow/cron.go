package workflow

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed 5-field cron schedule "minute hour dom month dow"
// (REQ-WF-003). Month and weekday names are accepted, and an asterisk means the
// full range.
type Cron struct {
	minute        uint64 // bits 0..59
	hour          uint64 // bits 0..23
	dom           uint64 // bits 1..31
	month         uint64 // bits 1..12
	dow           uint64 // bits 0..6, 0 = Sunday
	domRestricted bool
	dowRestricted bool
}

// Cron month and weekday name tables.
var (
	cronMonths = map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}
	cronDays = map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}
	weekdayNames = [7]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
)

// ParseCron parses a 5-field cron expression. Each field accepts *, numbers,
// lists (a,b), ranges (a-b) and steps (*/n and a-b/n); month and weekday names
// are allowed and dow 0 and 7 both mean Sunday. The macros @hourly, @daily,
// @weekly and @monthly are also accepted.
func ParseCron(expr string) (Cron, error) {
	fields, err := cronFields(expr)
	if err != nil {
		return Cron{}, err
	}
	minute, _, err := parseCronField(fields[0], 0, 59, nil)
	if err != nil {
		return Cron{}, err
	}
	hour, _, err := parseCronField(fields[1], 0, 23, nil)
	if err != nil {
		return Cron{}, err
	}
	dom, domRestricted, err := parseCronField(fields[2], 1, 31, nil)
	if err != nil {
		return Cron{}, err
	}
	month, _, err := parseCronField(fields[3], 1, 12, cronMonths)
	if err != nil {
		return Cron{}, err
	}
	dow, dowRestricted, err := parseCronField(fields[4], 0, 7, cronDays)
	if err != nil {
		return Cron{}, err
	}
	if dow&(1<<7) != 0 {
		dow |= 1 << 0
		dow &^= 1 << 7
	}
	return Cron{
		minute:        minute,
		hour:          hour,
		dom:           dom,
		month:         month,
		dow:           dow,
		domRestricted: domRestricted,
		dowRestricted: dowRestricted,
	}, nil
}

// cronFields expands a macro and splits expr into its five fields.
func cronFields(expr string) ([]string, error) {
	e := strings.TrimSpace(expr)
	switch strings.ToLower(e) {
	case "@hourly":
		e = "0 * * * *"
	case "@daily":
		e = "0 0 * * *"
	case "@weekly":
		e = "0 0 * * 0"
	case "@monthly":
		e = "0 0 1 * *"
	}
	fields := strings.Fields(e)
	if len(fields) != 5 {
		return nil, fmt.Errorf("workflow: cron %q wants 5 fields, got %d", expr, len(fields))
	}
	return fields, nil
}

// parseCronField parses one field into a bitmask and reports whether it is
// restricted (not a bare *). min and max bound the accepted values.
func parseCronField(field string, min, max int, names map[string]int) (uint64, bool, error) {
	var bits uint64
	restricted := field != "*"
	for _, part := range strings.Split(field, ",") {
		rangePart := part
		step := 1
		if i := strings.IndexByte(part, '/'); i >= 0 {
			rangePart = part[:i]
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return 0, false, fmt.Errorf("workflow: bad cron step %q", part)
			}
			step = n
		}
		var lo, hi int
		switch {
		case rangePart == "*":
			lo, hi = min, max
		case strings.Contains(rangePart, "-"):
			i := strings.IndexByte(rangePart, '-')
			l, ok1 := cronValue(rangePart[:i], names)
			h, ok2 := cronValue(rangePart[i+1:], names)
			if !ok1 || !ok2 {
				return 0, false, fmt.Errorf("workflow: bad cron field %q", field)
			}
			lo, hi = l, h
		default:
			v, ok := cronValue(rangePart, names)
			if !ok {
				return 0, false, fmt.Errorf("workflow: bad cron field %q", field)
			}
			lo, hi = v, v
		}
		if lo < min || hi > max || lo > hi {
			return 0, false, fmt.Errorf("workflow: cron field %q out of range", field)
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, restricted, nil
}

// cronValue parses a number or a name (case-insensitive).
func cronValue(s string, names map[string]int) (int, bool) {
	s = strings.TrimSpace(s)
	if names != nil {
		if v, ok := names[strings.ToLower(s)]; ok {
			return v, true
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Matches reports whether the schedule fires at t, at minute precision and in
// t's own location. When both dom and dow are restricted a day matches when
// either matches (the standard cron rule).
func (c Cron) Matches(t time.Time) bool {
	if c.minute&(1<<uint(t.Minute())) == 0 {
		return false
	}
	if c.hour&(1<<uint(t.Hour())) == 0 {
		return false
	}
	if c.month&(1<<uint(int(t.Month()))) == 0 {
		return false
	}
	domMatch := c.dom&(1<<uint(t.Day())) != 0
	dowMatch := c.dow&(1<<uint(int(t.Weekday()))) != 0
	switch {
	case c.domRestricted && c.dowRestricted:
		return domMatch || dowMatch
	case c.domRestricted:
		return domMatch
	case c.dowRestricted:
		return dowMatch
	default:
		return true
	}
}

// Next returns the first matching minute strictly after after, searching up to
// five years. It returns the zero time when nothing matches.
func (c Cron) Next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := after.AddDate(5, 0, 0)
	for t.Before(limit) {
		if c.Matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

// DescribeCron renders a short English description for the UI, falling back to
// the expression itself when it has no short form.
func DescribeCron(expr string) string {
	fields, err := cronFields(expr)
	if err != nil {
		return expr
	}
	minF, hourF, domF, monF, dowF := fields[0], fields[1], fields[2], fields[3], fields[4]
	allDays := domF == "*" && monF == "*" && dowF == "*"
	if n, ok := cronStep(minF); ok && hourF == "*" && allDays {
		return fmt.Sprintf("Every %d min", n)
	}
	if minF == "*" && hourF == "*" && allDays {
		return "Every minute"
	}
	if minF == "0" && hourF == "*" && allDays {
		return "Every hour"
	}
	if mi, ok := cronInt(minF); ok {
		if hi, ok := cronInt(hourF); ok && domF == "*" && monF == "*" {
			clock := fmt.Sprintf("%02d:%02d", hi, mi)
			switch dowF {
			case "*":
				return "Every day " + clock
			case "1-5":
				return "Weekdays " + clock
			default:
				if name, ok := cronWeekday(dowF); ok {
					return name + "s " + clock
				}
			}
		}
	}
	return expr
}

// cronStep returns the step of a */n field.
func cronStep(f string) (int, bool) {
	if !strings.HasPrefix(f, "*/") {
		return 0, false
	}
	n, err := strconv.Atoi(f[2:])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// cronInt parses a plain integer field.
func cronInt(f string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(f))
	return n, err == nil
}

// cronWeekday resolves a single weekday field to its name (0 and 7 = Sunday).
func cronWeekday(f string) (string, bool) {
	f = strings.ToLower(strings.TrimSpace(f))
	n, ok := cronDays[f]
	if !ok {
		var err error
		n, err = strconv.Atoi(f)
		if err != nil {
			return "", false
		}
	}
	if n == 7 {
		n = 0
	}
	if n < 0 || n > 6 {
		return "", false
	}
	return weekdayNames[n], true
}
