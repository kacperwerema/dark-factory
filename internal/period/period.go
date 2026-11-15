// Package period is the one place that knows where a day, week, month or
// quarter starts and how a week is written. Weeks are ISO weeks: they start
// on Monday, in t's own location. TestOneWeekBoundary refuses a second copy.
package period

import (
	"fmt"
	"time"
)

// DayStart is midnight of t's day.
func DayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// WeekStart is the Monday 00:00 of t's week.
func WeekStart(t time.Time) time.Time {
	y, m, d := t.Date()
	back := (int(t.Weekday()) + 6) % 7
	return time.Date(y, m, d-back, 0, 0, 0, 0, t.Location())
}

// DaysBack is midnight of the date n calendar days before t's date, in t's
// location: the start of a "last n days" window. A thing dated exactly n
// days back is inside it, so the window holds that whole day. It counts
// dates, never hours: a DST day is 23 or 25 hours long.
func DaysBack(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d-n, 0, 0, 0, 0, t.Location())
}

// MonthStart is midnight of the first of t's month.
func MonthStart(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, t.Location())
}

// QuarterStart is midnight of the first day of t's quarter.
func QuarterStart(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m-(m-1)%3, 1, 0, 0, 0, 0, t.Location())
}

// WeekKey is t's ISO week as "2026-W45".
func WeekKey(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

// WeekLabel is t's ISO week without its year: "W45".
func WeekLabel(t time.Time) string {
	_, w := t.ISOWeek()
	return fmt.Sprintf("W%02d", w)
}

// MonthKey is t's month as "2026-11".
func MonthKey(t time.Time) string { return t.Format("2006-01") }

// DayKey is t's day as "2026-11-04".
func DayKey(t time.Time) string { return t.Format("2006-01-02") }

// ValidWeekKey reports whether key is a week key WeekKey could have written.
func ValidWeekKey(key string) bool {
	var y, w int
	if n, err := fmt.Sscanf(key, "%4d-W%02d", &y, &w); err != nil || n != 2 {
		return false
	}
	return key == fmt.Sprintf("%d-W%02d", y, w) && w >= 1 && w <= 53 && y >= 2000
}
