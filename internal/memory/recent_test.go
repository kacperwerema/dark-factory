package memory

import (
	"testing"
	"time"
)

// TestRecentCountsCalendarDays: the memory page's Recently changed (7 days)
// starts at midnight of the date 7 days back. On the day after the 25-hour
// day, now minus 7×24h lands at 01:30 of that date and drops an event
// learned at 00:30 on it.
func TestRecentCountsCalendarDays(t *testing.T) {
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Skip("no tz data:", err)
	}
	now := time.Date(2026, 10, 26, 0, 30, 0, 0, warsaw)
	at := func(tm time.Time) string { return tm.UTC().Format(time.RFC3339) }
	events := []Event{
		{Op: OpAdd, ID: "m-before", At: at(time.Date(2026, 10, 18, 23, 59, 0, 0, warsaw))},
		{Op: OpAdd, ID: "m-edge", At: at(time.Date(2026, 10, 19, 0, 30, 0, 0, warsaw))},
		{Op: OpUse, IDs: []string{"m-edge"}, At: at(time.Date(2026, 10, 20, 9, 0, 0, 0, warsaw))},
		{Op: OpAdd, ID: "m-new", At: at(time.Date(2026, 10, 25, 12, 0, 0, 0, warsaw))},
	}
	for i := range events {
		if events[i].Op == OpAdd {
			events[i].Memory = &Memory{ID: events[i].ID, Text: "x", Created: events[i].At}
		}
	}
	var got []string
	for _, e := range fold(events).Recent(now, 7) {
		got = append(got, e.ID)
	}
	if len(got) != 2 || got[0] != "m-new" || got[1] != "m-edge" {
		t.Fatalf("Recent(7) = %v, want [m-new m-edge]: an event dated exactly 7 days back is in the window, the day before is not", got)
	}
}
