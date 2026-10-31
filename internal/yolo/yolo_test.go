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

func TestOwnerChecksWaitUntilTickedOrReviewed(t *testing.T) {
	issue := &tracker.Issue{Slug: "a", Title: "A", BodyRaw: "## Acceptance Criteria\n- [ ] One\n- [ ] Two\n- [ ] Three\n"}
	check := func(id string) string {
		return tracker.YOLOOwnerCheckComment(tracker.YOLOOwnerCheck{ID: id, Why: "playtest judgement step TP9", Text: "The " + id + " box", From: "testing"})
	}
	comments := []tracker.Comment{
		{ID: 1, Date: "2026-10-30T23:00:00+01:00", Author: "agent-a", Text: check("AC2")},
		{ID: 2, Author: "agent-a", Text: check("AC3")},
		{ID: 3, Author: "agent-a", Text: check("AC2")}, // the gate ran again: once
		{ID: 4, Text: tracker.YOLOReviewComment("agreed", "fine without it", "viewer", "2026-10-31", "AC3")},
	}
	u := Unreviewed(issue, comments)
	if len(u) != 1 || u[0].Check != "AC2" || u[0].What != "The AC2 box" || u[0].From != "testing" || u[0].By != "agent-a" || u[0].Label() != "AC2: owner check, not done" || u[0].Key() != "AC2" {
		t.Fatalf("unreviewed = %+v", u)
	}
	if len(Passes(issue, comments)) != 0 {
		t.Fatal("an owner check counted as a gate pass")
	}
	issue.BodyRaw = "## Acceptance Criteria\n- [ ] One\n- [x] Two\n- [ ] Three\n"
	if u := Unreviewed(issue, comments); len(u) != 0 {
		t.Fatalf("ticked AC2 still waits: %+v", u)
	}
	oc, ok := tracker.ParseYOLOOwnerCheckComment(tracker.YOLOOwnerCheckComment(tracker.YOLOOwnerCheck{ID: "TP1", Why: "guided check walk step S3: skipped", Text: `says "hi" (twice)`, From: "testing"}))
	if !ok || oc.ID != "TP1" || oc.Why != "guided check walk step S3: skipped" || oc.Text != `says "hi" (twice)` || oc.From != "testing" {
		t.Fatalf("round trip = %+v %v", oc, ok)
	}
}

func TestOwnerChecksLeaveOnTheOwnersSendBack(t *testing.T) {
	issue := &tracker.Issue{Slug: "a", Title: "A", BodyRaw: "## Human Testing\n- [ ] Human verification complete if required\n"}
	check := tracker.YOLOOwnerCheckComment(tracker.YOLOOwnerCheck{ID: "HT1", Why: "Human Testing box", Text: "Human verification complete if required", From: "human-testing"})
	comments := []tracker.Comment{
		{ID: 1, Author: "agent-a", Text: check},
		// An agent's comment that looks like one is no send-back.
		{ID: 2, Author: "agent-a", Text: tracker.RejectedComment("redo", "cli", "2026-10-31", "documentation", "in progress")},
	}
	if u := Unreviewed(issue, comments); len(u) != 1 || u[0].Check != "HT1" {
		t.Fatalf("unreviewed = %+v, want HT1", u)
	}
	comments = append(comments, tracker.Comment{ID: 3, Author: tracker.OwnerAuthor, Text: tracker.RejectedComment("the colours are off", "viewer", "2026-10-31", "documentation", "in progress")})
	if u := Unreviewed(issue, comments); len(u) != 0 {
		t.Fatalf("sent back, still waits: %+v", u)
	}
	// The next gate queues it again while it is unticked.
	comments = append(comments, tracker.Comment{ID: 4, Author: "agent-a", Text: check})
	if u := Unreviewed(issue, comments); len(u) != 1 || u[0].Comment != 4 {
		t.Fatalf("queued again = %+v", u)
	}
}
