// Package jobs is the departments' recurring jobs: a schedule per job in
// projects.yaml (departments: jobs:), each run an issue in the department
// (fields job: and run:), a run log with the work files
// (<issues>/.jobs/runs.jsonl) and the owner's standing start per job
// (.jobs/<project>.json in the main checkout, never committed). Nothing here
// runs on a timer: a lead's check-in asks what is due (Status) and starts it
// (issue-cli job run). See docs/departments.md, "Recurring jobs".
package jobs

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Schedule is a job's parsed every/on/at.
type Schedule struct {
	Every string // day, week, month
	// Weekday is a weekly job's day.
	Weekday time.Weekday
	// Day is a monthly job's day of the month, 1..28; 0 is the last day.
	Day    int
	Hour   int
	Minute int
}

var weekdays = map[string]time.Weekday{
	"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday,
	"fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
}

// Parse reads a job's schedule. A weekly job defaults to Monday, a monthly
// one to the 1st, at defaults to 00:00.
func Parse(j tracker.Job) (Schedule, error) {
	s := Schedule{Every: strings.ToLower(strings.TrimSpace(j.Every))}
	on := strings.ToLower(strings.TrimSpace(j.On))
	switch s.Every {
	case "day":
		if on != "" {
			return s, fmt.Errorf("on: %q: a daily job has no on:", j.On)
		}
	case "week":
		s.Weekday = time.Monday
		if on != "" {
			d, ok := weekdays[on[:min(3, len(on))]]
			if !ok {
				return s, fmt.Errorf("on: %q: a weekly job runs on mon..sun", j.On)
			}
			s.Weekday = d
		}
	case "month":
		s.Day = 1
		switch {
		case on == "":
		case on == "last":
			s.Day = 0
		default:
			n, err := strconv.Atoi(on)
			if err != nil || n < 1 || n > 28 {
				return s, fmt.Errorf("on: %q: a monthly job runs on 1..28 or last", j.On)
			}
			s.Day = n
		}
	case "":
		return s, fmt.Errorf("every: is required (day, week or month)")
	default:
		return s, fmt.Errorf("every: %q: day, week or month", j.Every)
	}
	if at := strings.TrimSpace(j.At); at != "" {
		t, err := time.Parse("15:04", at)
		if err != nil {
			return s, fmt.Errorf("at: %q: a local time like 07:00", j.At)
		}
		s.Hour, s.Minute = t.Hour(), t.Minute()
	}
	return s, nil
}

// periodStart is the first day of the period holding t, at midnight.
func (s Schedule) periodStart(t time.Time) time.Time {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	switch s.Every {
	case "week":
		back := (int(day.Weekday()) + 6) % 7 // ISO weeks start on Monday
		return day.AddDate(0, 0, -back)
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	}
	return day
}

// next is the start of the period after the one starting at start.
func (s Schedule) next(start time.Time) time.Time {
	switch s.Every {
	case "week":
		return start.AddDate(0, 0, 7)
	case "month":
		return start.AddDate(0, 1, 0)
	}
	return start.AddDate(0, 0, 1)
}

// prev is the start of the period before the one starting at start.
func (s Schedule) prev(start time.Time) time.Time {
	switch s.Every {
	case "week":
		return start.AddDate(0, 0, -7)
	case "month":
		return start.AddDate(0, -1, 0)
	}
	return start.AddDate(0, 0, -1)
}

// dueAt is when the run of the period starting at start is due.
func (s Schedule) dueAt(start time.Time) time.Time {
	day := start
	switch s.Every {
	case "week":
		day = start.AddDate(0, 0, (int(s.Weekday)+6)%7)
	case "month":
		if s.Day == 0 {
			day = start.AddDate(0, 1, -1)
		} else {
			day = start.AddDate(0, 0, s.Day-1)
		}
	}
	return time.Date(day.Year(), day.Month(), day.Day(), s.Hour, s.Minute, 0, 0, day.Location())
}

// Key is the run key of the period starting at start: 2026-10-30,
// 2026-W44 (ISO week) or 2026-10.
func (s Schedule) Key(start time.Time) string {
	switch s.Every {
	case "week":
		y, w := start.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	case "month":
		return start.Format("2006-01")
	}
	return start.Format("2006-01-02")
}

// Latest is the newest period whose run is due by now: its key and when it
// was due.
func (s Schedule) Latest(now time.Time) (key string, due time.Time) {
	start := s.periodStart(now)
	if s.dueAt(start).After(now) {
		start = s.prev(start)
	}
	return s.Key(start), s.dueAt(start)
}

// Next is when the first run after now is due.
func (s Schedule) Next(now time.Time) time.Time {
	start := s.periodStart(now)
	if d := s.dueAt(start); d.After(now) {
		return d
	}
	return s.dueAt(s.next(start))
}

// Missed counts the periods between last's key and now's latest, both
// excluded: the runs nobody made. last "" (never ran) misses none.
func (s Schedule) Missed(last string, now time.Time) int {
	if last == "" {
		return 0
	}
	start := s.periodStart(now)
	if s.dueAt(start).After(now) {
		start = s.prev(start)
	}
	n := 0
	for i := 0; i < 400; i++ {
		start = s.prev(start)
		if k := s.Key(start); k <= last {
			return n
		}
		n++
	}
	return n
}

// Label is the schedule in words: "every day at 07:00", "Mon 07:00",
// "monthly on the 1st at 09:00".
func (s Schedule) Label() string {
	at := fmt.Sprintf("%02d:%02d", s.Hour, s.Minute)
	switch s.Every {
	case "week":
		return fmt.Sprintf("every %s %s", s.Weekday.String()[:3], at)
	case "month":
		if s.Day == 0 {
			return "monthly, last day " + at
		}
		return fmt.Sprintf("monthly on the %s %s", ordinal(s.Day), at)
	}
	return "daily " + at
}

func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return strconv.Itoa(n) + suffix
}
