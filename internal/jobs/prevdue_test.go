package jobs

import (
	"testing"
	"time"
)

func TestPrevDue(t *testing.T) {
	loc := time.UTC
	weekly := Schedule{Every: "week", Weekday: time.Monday, Hour: 8, Minute: 30}
	for now, want := range map[time.Time]time.Time{
		// Thursday: latest due Mon 10-26, the one before Mon 10-19.
		time.Date(2026, 10, 29, 12, 0, 0, 0, loc): time.Date(2026, 10, 19, 8, 30, 0, 0, loc),
		// Monday before 08:30: this week's isn't due yet.
		time.Date(2026, 10, 26, 8, 0, 0, 0, loc): time.Date(2026, 10, 12, 8, 30, 0, 0, loc),
	} {
		if got := weekly.PrevDue(now); !got.Equal(want) {
			t.Errorf("weekly PrevDue(%s) = %s, want %s", now, got, want)
		}
	}
	monthly := Schedule{Every: "month", Day: 1}
	if got := monthly.PrevDue(time.Date(2026, 10, 29, 0, 0, 0, 0, loc)); !got.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, loc)) {
		t.Errorf("monthly PrevDue = %s", got)
	}
}
