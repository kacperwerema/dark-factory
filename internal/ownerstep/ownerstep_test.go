package ownerstep

import (
	"testing"
	"time"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func ev(op, id string, when time.Time) Event {
	return Event{Op: op, ID: id, At: when.Format(time.RFC3339)}
}

// Proves: AC3 — a step with a 7-day repeat is closed by its tick and opens
// again on the calendar day 7 days after the tick, due that day; it counts
// from the tick, not the old due day, and a DST change in between moves
// nothing.
func TestOwnerStepRepeats(t *testing.T) {
	dir := t.TempDir()
	add := ev(OpAdd, "st-aaaaaa", at(t, "2026-10-20 09:00"))
	add.Title, add.Body, add.Due, add.Repeat = "Reinstall the phone build", "open Xcode, Run on the phone", "2026-10-22", 7
	if err := Append(dir, add); err != nil {
		t.Fatal(err)
	}
	// Ticked late, on 10-24 at 23:30, the night before Europe's DST change.
	if err := Append(dir, ev(OpDone, "st-aaaaaa", at(t, "2026-10-24 23:30"))); err != nil {
		t.Fatal(err)
	}
	evs, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	steps := Fold(evs)
	s := steps[0]
	if s.Due != "2026-10-31" {
		t.Fatalf("next due %q, want 2026-10-31 (the tick's day + 7)", s.Due)
	}
	for _, c := range []struct {
		now  string
		open bool
	}{
		{"2026-10-25 08:00", false},
		{"2026-10-30 23:59", false},
		{"2026-10-31 00:01", true},
		{"2026-11-05 12:00", true},
	} {
		if got := s.IsOpen(at(t, c.now)); got != c.open {
			t.Errorf("%s: open %v, want %v", c.now, got, c.open)
		}
	}
	if len(Open(steps, at(t, "2026-10-31 00:01"))) != 1 {
		t.Error("Open leaves out the reopened step")
	}
	// A step without a repeat stays closed.
	once := ev(OpAdd, "st-bbbbbb", at(t, "2026-10-20 09:00"))
	once.Title, once.Body, once.Due = "Upload the logo", "512 px PNG", "2026-10-22"
	steps = Fold([]Event{once, ev(OpDone, "st-bbbbbb", at(t, "2026-10-21 10:00"))})
	if steps[0].IsOpen(at(t, "2027-01-01 10:00")) {
		t.Error("a ticked step without a repeat opened again")
	}
	// A drop ends a repeating step for good.
	steps = Fold([]Event{add, ev(OpDone, "st-aaaaaa", at(t, "2026-10-24 10:00")), ev(OpDrop, "st-aaaaaa", at(t, "2026-10-25 10:00"))})
	if steps[0].IsOpen(at(t, "2026-12-01 10:00")) {
		t.Error("a dropped repeating step opened again")
	}
}

// Proves: AC4 — overdue is today after the due day: the due day itself
// is "due today", the next day is overdue, counted in calendar days.
func TestOwnerStepOverdue(t *testing.T) {
	s := Step{ID: "st-cccccc", Due: "2026-10-24"}
	for _, c := range []struct {
		now     string
		overdue bool
		line    string
	}{
		{"2026-10-23 23:59", false, "due 2026-10-24"},
		{"2026-10-24 23:59", false, "due today"},
		{"2026-10-25 00:00", true, "overdue: was due 2026-10-24 (1 day ago)"},
		{"2026-10-27 01:00", true, "overdue: was due 2026-10-24 (3 days ago)"},
	} {
		now := at(t, c.now)
		if got := s.Overdue(now); got != c.overdue {
			t.Errorf("%s: overdue %v, want %v", c.now, got, c.overdue)
		}
		if got := s.DueLine(now); got != c.line {
			t.Errorf("%s: %q, want %q", c.now, got, c.line)
		}
	}
	// Overdue sorts first: by due day.
	open := Open([]Step{{ID: "late", Due: "2026-11-30"}, {ID: "early", Due: "2026-10-01"}}, at(t, "2026-10-10 10:00"))
	if open[0].ID != "early" {
		t.Errorf("order %v", open)
	}
}

func TestParseDueAndRepeat(t *testing.T) {
	now := at(t, "2026-11-16 10:00")
	for raw, want := range map[string]string{"today": "2026-11-16", "tomorrow": "2026-11-17", "+7d": "2026-11-23", "+2w": "2026-11-30", "2026-12-01": "2026-12-01"} {
		got, err := ParseDue(raw, now)
		if err != nil || got != want {
			t.Errorf("ParseDue(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, bad := range []string{"", "2026-11-15", "next week", "+3m"} {
		if _, err := ParseDue(bad, now); err == nil {
			t.Errorf("ParseDue(%q) took it", bad)
		}
	}
	for raw, want := range map[string]int{"": 0, "7d": 7, "2w": 14, "30D": 30} {
		if got, err := ParseRepeat(raw); err != nil || got != want {
			t.Errorf("ParseRepeat(%q) = %d, %v", raw, got, err)
		}
	}
	for _, bad := range []string{"0d", "7", "1y", "400d"} {
		if _, err := ParseRepeat(bad); err == nil {
			t.Errorf("ParseRepeat(%q) took it", bad)
		}
	}
	if RepeatLabel(7) != "7d" || RepeatLabel(14) != "2w" || RepeatLabel(10) != "10d" {
		t.Error("RepeatLabel")
	}
}
