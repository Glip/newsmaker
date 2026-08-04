package httpserver

import (
	"time"

	"newsmaker/internal/schedule"
)

type calDay struct {
	Day     int
	Date    time.Time
	InMonth bool
	IsToday bool
	Posts   []schedule.Post
}

type calMonth struct {
	Year      int
	Month     time.Month
	Title     string
	YearMonth string // 2006-01 for query
	PrevYM    string
	NextYM    string
	Weeks     [][]calDay
	Weekdays  []string
}

func buildCalendar(year int, month time.Month, posts []schedule.Post, now time.Time) calMonth {
	loc := now.Location()
	first := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	nextMonth := first.AddDate(0, 1, 0)
	prevMonth := first.AddDate(0, -1, 0)

	// Monday-first: Go Weekday Sunday=0 … Saturday=6 → shift so Monday=0
	startOffset := (int(first.Weekday()) + 6) % 7
	gridStart := first.AddDate(0, 0, -startOffset)

	byDay := map[string][]schedule.Post{}
	for _, p := range posts {
		local := p.SendAt.In(loc)
		key := local.Format("2006-01-02")
		byDay[key] = append(byDay[key], p)
	}

	todayKey := now.In(loc).Format("2006-01-02")
	weeks := make([][]calDay, 0, 6)
	cursor := gridStart
	for w := 0; w < 6; w++ {
		week := make([]calDay, 7)
		for d := 0; d < 7; d++ {
			key := cursor.Format("2006-01-02")
			week[d] = calDay{
				Day:     cursor.Day(),
				Date:    cursor,
				InMonth: cursor.Month() == month,
				IsToday: key == todayKey,
				Posts:   byDay[key],
			}
			cursor = cursor.AddDate(0, 0, 1)
		}
		weeks = append(weeks, week)
		// Stop early if we've left the month and filled a full trailing week of next month only
		if cursor.Month() != month && w >= 3 {
			trailingEmpty := true
			for _, day := range week {
				if day.InMonth || len(day.Posts) > 0 {
					trailingEmpty = false
					break
				}
			}
			if trailingEmpty && w > 0 {
				// keep this week if any day in month was shown earlier; only drop fully empty extra weeks after month ends
			}
		}
	}
	// Drop trailing weeks that are entirely outside the month and have no posts
	for len(weeks) > 4 {
		last := weeks[len(weeks)-1]
		useful := false
		for _, day := range last {
			if day.InMonth || len(day.Posts) > 0 {
				useful = true
				break
			}
		}
		if useful {
			break
		}
		weeks = weeks[:len(weeks)-1]
	}

	monthNames := []string{
		"", "январь", "февраль", "март", "апрель", "май", "июнь",
		"июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь",
	}
	return calMonth{
		Year:      year,
		Month:     month,
		Title:     monthNames[int(month)] + " " + first.Format("2006"),
		YearMonth: first.Format("2006-01"),
		PrevYM:    prevMonth.Format("2006-01"),
		NextYM:    nextMonth.Format("2006-01"),
		Weeks:     weeks,
		Weekdays:  []string{"пн", "вт", "ср", "чт", "пт", "сб", "вс"},
	}
}

func parseYearMonth(s string, now time.Time) (int, time.Month) {
	if t, err := time.ParseInLocation("2006-01", s, now.Location()); err == nil {
		return t.Year(), t.Month()
	}
	n := now.In(now.Location())
	return n.Year(), n.Month()
}
