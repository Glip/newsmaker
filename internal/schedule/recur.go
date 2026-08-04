package schedule

import (
	"fmt"
	"sort"
	"time"
)

const (
	RepeatNone    = ""
	RepeatWeekly  = "weekly"
	RepeatMonthly = "monthly"
)

// HorizonMonths caps how far a series is materialized without an until date.
const HorizonMonths = 6

// NextOccurrences returns send times from start (inclusive when it matches the rule)
// until exclusive of times after until (if set) or after start+HorizonMonths.
// Calendar arithmetic uses time.Local so weekday/day-of-month match the compose UI.
func NextOccurrences(kind string, weekdays []int, start time.Time, until time.Time) ([]time.Time, error) {
	if start.IsZero() {
		return nil, fmt.Errorf("start required")
	}
	loc := time.Local
	localStart := start.In(loc)
	horizon := localStart.AddDate(0, HorizonMonths, 0)
	var untilLocal time.Time
	if !until.IsZero() {
		untilLocal = until.In(loc)
		if !untilLocal.After(localStart) {
			return nil, fmt.Errorf("until must be after start")
		}
	}

	var out []time.Time
	switch kind {
	case RepeatWeekly:
		days, err := normalizeWeekdays(weekdays)
		if err != nil {
			return nil, err
		}
		out = expandWeekly(localStart, days, untilLocal, horizon)
	case RepeatMonthly:
		out = expandMonthly(localStart, untilLocal, horizon)
	default:
		return nil, fmt.Errorf("unknown repeat kind %q", kind)
	}
	for i := range out {
		out[i] = out[i].UTC()
	}
	return out, nil
}

// weekdayMon1: Monday=1 … Sunday=7 → Go Weekday.
func toGoWeekday(d int) time.Weekday {
	if d == 7 {
		return time.Sunday
	}
	return time.Weekday(d)
}

func normalizeWeekdays(weekdays []int) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	for _, d := range weekdays {
		if d < 1 || d > 7 {
			return nil, fmt.Errorf("weekday must be 1..7, got %d", d)
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("выберите хотя бы один день недели")
	}
	sort.Ints(out)
	return out, nil
}

func expandWeekly(start time.Time, weekdays []int, until, horizon time.Time) []time.Time {
	want := map[time.Weekday]bool{}
	for _, d := range weekdays {
		want[toGoWeekday(d)] = true
	}
	hour, min, sec := start.Clock()
	nsec := start.Nanosecond()
	loc := start.Location()

	var out []time.Time
	// Walk calendar days from start's date; first candidate at/after start.
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	for {
		if day.After(horizon) {
			break
		}
		t := time.Date(day.Year(), day.Month(), day.Day(), hour, min, sec, nsec, loc)
		if !until.IsZero() && t.After(until) {
			break
		}
		if !t.Before(start) && want[day.Weekday()] {
			out = append(out, t)
		}
		day = day.AddDate(0, 0, 1)
		if day.After(horizon) && (until.IsZero() || !day.After(until)) {
			// continue until we pass horizon on next check
		}
		if len(out) > 400 {
			break
		}
	}
	return out
}

func expandMonthly(start time.Time, until, horizon time.Time) []time.Time {
	dom := start.Day()
	hour, min, sec := start.Clock()
	nsec := start.Nanosecond()
	loc := start.Location()

	var out []time.Time
	y, m := start.Year(), start.Month()
	for {
		t := clampMonthDay(y, m, dom, hour, min, sec, nsec, loc)
		if t.After(horizon) {
			break
		}
		if !until.IsZero() && t.After(until) {
			break
		}
		if !t.Before(start) {
			out = append(out, t)
		}
		m++
		if m > 12 {
			m = 1
			y++
		}
		if len(out) > 400 {
			break
		}
	}
	return out
}

func clampMonthDay(year int, month time.Month, day, hour, min, sec, nsec int, loc *time.Location) time.Time {
	// First of next month minus a day = last day of target month.
	firstNext := time.Date(year, month+1, 1, 0, 0, 0, 0, loc)
	lastDay := firstNext.AddDate(0, 0, -1).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, hour, min, sec, nsec, loc)
}

// RepeatLabel returns a short Russian description for calendar UI.
func RepeatLabel(kind string, weekdays []int) string {
	switch kind {
	case RepeatWeekly:
		names := []string{"", "пн", "вт", "ср", "чт", "пт", "сб", "вс"}
		days, err := normalizeWeekdays(weekdays)
		if err != nil {
			return "еженедельно"
		}
		parts := make([]string, 0, len(days))
		for _, d := range days {
			parts = append(parts, names[d])
		}
		return joinComma(parts)
	case RepeatMonthly:
		return "ежемесячно"
	default:
		return ""
	}
}

func joinComma(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += ", " + parts[i]
	}
	return out
}
