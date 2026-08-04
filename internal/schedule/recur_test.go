package schedule

import (
	"testing"
	"time"
)

func TestNextOccurrencesWeeklySelectedDays(t *testing.T) {
	// Wednesday 2026-08-05 15:00 local
	start := time.Date(2026, 8, 5, 15, 0, 0, 0, time.Local)
	// Mon=1, Wed=3, Fri=5
	got, err := NextOccurrences(RepeatWeekly, []int{1, 3, 5}, start, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 3 {
		t.Fatalf("expected several occurrences, got %d", len(got))
	}
	first := got[0].In(time.Local)
	if first.Weekday() != time.Wednesday || first.Hour() != 15 {
		t.Fatalf("first=%v want Wednesday 15:00", first)
	}
	second := got[1].In(time.Local)
	if second.Weekday() != time.Friday {
		t.Fatalf("second weekday=%v want Friday", second.Weekday())
	}
	third := got[2].In(time.Local)
	if third.Weekday() != time.Monday {
		t.Fatalf("third weekday=%v want Monday", third.Weekday())
	}
	last := got[len(got)-1].In(time.Local)
	horizon := start.AddDate(0, HorizonMonths, 0)
	if last.After(horizon) {
		t.Fatalf("last %v after horizon %v", last, horizon)
	}
}

func TestNextOccurrencesWeeklySkipsUnselectedStartDay(t *testing.T) {
	// Tuesday — only Mon/Wed selected → first should be Wednesday
	start := time.Date(2026, 8, 4, 10, 30, 0, 0, time.Local)
	got, err := NextOccurrences(RepeatWeekly, []int{1, 3}, start, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("empty")
	}
	first := got[0].In(time.Local)
	if first.Weekday() != time.Wednesday || first.Day() != 5 {
		t.Fatalf("first=%v want Wed Aug 5", first)
	}
	if first.Hour() != 10 || first.Minute() != 30 {
		t.Fatalf("clock=%v want 10:30", first)
	}
}

func TestNextOccurrencesMonthlyClampsDay(t *testing.T) {
	start := time.Date(2026, 1, 31, 12, 0, 0, 0, time.Local)
	until := time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local)
	got, err := NextOccurrences(RepeatMonthly, nil, start, until)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d want 3 (Jan31, Feb28, Mar31)", len(got))
	}
	feb := got[1].In(time.Local)
	if feb.Month() != time.February || feb.Day() != 28 {
		t.Fatalf("feb=%v want Feb 28", feb)
	}
	mar := got[2].In(time.Local)
	if mar.Month() != time.March || mar.Day() != 31 {
		t.Fatalf("mar=%v want Mar 31", mar)
	}
}

func TestNextOccurrencesUntil(t *testing.T) {
	start := time.Date(2026, 8, 5, 9, 0, 0, 0, time.Local)
	until := time.Date(2026, 8, 12, 9, 0, 0, 0, time.Local)
	got, err := NextOccurrences(RepeatWeekly, []int{3}, start, until) // Wed only
	if err != nil {
		t.Fatal(err)
	}
	// Aug 5 and Aug 12 inclusive
	if len(got) != 2 {
		t.Fatalf("got %d want 2: %v", len(got), got)
	}
}

func TestNextOccurrencesUntilBeforeStart(t *testing.T) {
	start := time.Date(2026, 8, 5, 9, 0, 0, 0, time.Local)
	until := start.Add(-time.Hour)
	_, err := NextOccurrences(RepeatMonthly, nil, start, until)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRepeatLabel(t *testing.T) {
	if got := RepeatLabel(RepeatMonthly, nil); got != "ежемесячно" {
		t.Fatalf("monthly: %q", got)
	}
	if got := RepeatLabel(RepeatWeekly, []int{1, 3}); got != "пн, ср" {
		t.Fatalf("weekly: %q", got)
	}
}
