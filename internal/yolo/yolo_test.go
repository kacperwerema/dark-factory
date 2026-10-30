package yolo

import (
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func TestParseUntil(t *testing.T) {
	now := time.Date(2026, 10, 29, 22, 10, 0, 0, time.Local)
	cases := []struct {
		in      string
		want    time.Time
		forever bool
		err     bool
	}{
		{in: "08:00", want: time.Date(2026, 10, 30, 8, 0, 0, 0, time.Local)},
		{in: "morning", want: time.Date(2026, 10, 30, 8, 0, 0, 0, time.Local)},
		{in: "23:30", want: time.Date(2026, 10, 29, 23, 30, 0, 0, time.Local)},
		{in: "4h", want: now.Add(4 * time.Hour)},
		{in: "off", forever: true},
		{in: "", err: true},
		{in: "-1h", err: true},
		{in: "25:00", err: true},
	}
	for _, c := range cases {
		got, forever, err := ParseUntil(c.in, now)
		if (err != nil) != c.err || forever != c.forever || !got.Equal(c.want) {
			t.Errorf("ParseUntil(%q) = %v, %v, %v", c.in, got, forever, err)
		}
	}
}

func TestCheck(t *testing.T) {
	now := time.Now()
	st := &State{On: true, Until: now.Add(time.Hour), Valves: Valves{CIRedMinutes: 30}}
	if on, _ := Check(st, now, nil); !on {
		t.Fatal("on state reads off")
	}
	if on, why := Check(st, now.Add(2*time.Hour), nil); on || why != "expired" {
		t.Fatalf("expired: %v %q", on, why)
	}
	red := &ci.State{State: ci.Red, RedAt: now.Add(-31 * time.Minute)}
	if on, why := Check(st, now, red); on || why != "valve: CI red 31 min" {
		t.Fatalf("valve: %v %q", on, why)
	}
	red.RedAt = now.Add(-10 * time.Minute)
	if on, _ := Check(st, now, red); !on {
		t.Fatal("10 min red tripped the 30 min valve")
	}
	st.Valves.CIRedMinutes = 0
	red.RedAt = now.Add(-5 * time.Hour)
	if on, _ := Check(st, now, red); !on {
		t.Fatal("no CI valve, yet CI red turned YOLO off")
	}
	forever := &State{On: true, Forever: true}
	if on, _ := Check(forever, now.Add(1000*time.Hour), nil); !on {
		t.Fatal("until turned off expired")
	}
}

func TestPassesAndReview(t *testing.T) {
	issue := &tracker.Issue{Slug: "a", Title: "A"}
	comments := []tracker.Comment{
		{ID: 1, Date: "2026-10-29T23:04:00+01:00", Text: tracker.ApprovalComment("YOLO", tracker.YOLOHow("agent-a", "in design"), "2026-10-29", "backlog")},
		{ID: 2, Text: tracker.ApprovalComment("ok", "viewer", "2026-10-29", "in progress")},
		{ID: 3, Text: tracker.ApprovalComment("YOLO", tracker.YOLOHow("agent-a", "human-testing"), "2026-10-30", "documentation")},
		{ID: 4, Text: tracker.YOLOReviewComment("agreed", "fine", "viewer", "2026-10-30", "backlog")},
	}
	ps := Passes(issue, comments)
	if len(ps) != 2 || ps[0].Review != "agreed" || ps[1].Review != "" || ps[1].From != "human-testing" {
		t.Fatalf("passes = %+v", ps)
	}
	if u := Unreviewed(issue, comments); len(u) != 1 || u[0].Status != "documentation" {
		t.Fatalf("unreviewed = %+v", u)
	}
	r, ok := tracker.ParseYOLOReviewComment(tracker.YOLOReviewComment("back to human-testing", "redo the checks", "viewer", "2026-10-30", "documentation"))
	if !ok || r.Verdict != "back to human-testing" || r.Words != "redo the checks" || r.Status != "documentation" {
		t.Fatalf("review = %+v %v", r, ok)
	}
}

func TestTurnOnOffHistory(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	if err := TurnOn(root, "p", State{Until: now.Add(time.Hour)}, now); err == nil {
		t.Fatal("turned on without words")
	}
	if err := TurnOn(root, "p", State{Words: "go"}, now); err == nil {
		t.Fatal("turned on without an end")
	}
	if err := TurnOn(root, "p", State{Words: "go", Until: now.Add(time.Hour)}, now); err != nil {
		t.Fatal(err)
	}
	if err := TurnOn(root, "p", State{Words: "go", Until: now.Add(time.Hour)}, now); err == nil {
		t.Fatal("turned on twice")
	}
	st, on, err := Active(root, "p", now.Add(2*time.Hour))
	if err != nil || on || st.EndReason != "expired" {
		t.Fatalf("after the end: %+v %v %v", st, on, err)
	}
	if h := History(root, "p"); len(h) != 1 {
		t.Fatalf("history = %+v", h)
	}
	// Already closed: reading again adds nothing.
	Active(root, "p", now.Add(3*time.Hour))
	if h := History(root, "p"); len(h) != 1 {
		t.Fatalf("history grew: %+v", h)
	}
}
