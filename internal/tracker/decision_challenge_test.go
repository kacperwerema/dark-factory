package tracker

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Only done events count; a resolved closes its (ref, issue) alone.
func TestOpenChallengesFold(t *testing.T) {
	at := func(m int) string { return time.Date(2026, 11, 1, 9, m, 0, 0, time.UTC).Format(time.RFC3339) }
	evs := []DecisionEvent{
		{Op: DecisionOpPending, Event: DecisionChallenged, Ref: "a#D-1", Issue: "x", At: at(0)},
		{Op: DecisionOpDone, Event: DecisionChallenged, Ref: "a#D-1", Issue: "x", Why: "w1", At: at(0)},
		{Op: DecisionOpDone, Event: DecisionChallenged, Ref: "a#D-1", Issue: "y", Why: "w2", At: at(1)},
		{Op: DecisionOpFailed, Event: DecisionChallenged, Ref: "b#D-1", Issue: "x", At: at(2)},
		{Op: DecisionOpDone, Event: DecisionResolved, Ref: "a#D-1", Issue: "x", At: at(3)},
		{Op: DecisionOpPending, Event: DecisionResolved, Ref: "a#D-1", Issue: "y", At: at(4)},
	}
	open := OpenChallenges(evs)
	if len(open) != 1 || open[0].Issue != "y" || open[0].Why != "w2" {
		t.Fatalf("open = %+v", open)
	}
}

// The rule holds the challenger only, refuses without the log's folder,
// and under YOLO records a hold instead of failing.
func TestDecisionUnchallengedRule(t *testing.T) {
	dir := t.TempDir()
	if err := AppendDecisionEvents(dir, []DecisionEvent{{Write: "w", Op: DecisionOpDone, Event: DecisionChallenged, Ref: "rules#D-1", Issue: "x", Why: "too strict", At: time.Now().Format(time.RFC3339)}}); err != nil {
		t.Fatal(err)
	}
	mk := func(slug string) *Issue {
		return &Issue{Slug: slug, FilePath: filepath.Join(dir, slug+".md"), Status: "in design"}
	}
	w := &WorkflowConfig{IssuesRoot: dir}
	err := w.checkDecisionUnchallenged("", mk("x"))
	if err == nil || !strings.Contains(err.Error(), "rules#D-1") || !strings.Contains(err.Error(), "too strict") {
		t.Fatalf("challenger: %v", err)
	}
	if err := w.checkDecisionUnchallenged("", mk("y")); err != nil {
		t.Fatalf("another issue held: %v", err)
	}
	if err := (&WorkflowConfig{}).checkDecisionUnchallenged("", mk("x")); err == nil {
		t.Fatal("no issues folder: want a refusal")
	}
	if err := w.checkDecisionUnchallenged("since 2026-12-01", &Issue{Slug: "x", Created: "2026-10-01"}); err != nil {
		t.Fatalf("since: %v", err)
	}
	y := &WorkflowConfig{IssuesRoot: dir, DeferDecisionHolds: true}
	if err := y.checkDecisionUnchallenged("", mk("x")); err != nil {
		t.Fatalf("YOLO: %v", err)
	}
	if h := y.DecisionHolds(); len(h) != 1 || h[0].Rule != DecisionUnchallengedRule || h[0].Ref != "rules#D-1" || h[0].Why != "challenged by this issue: too strict" {
		t.Fatalf("holds = %+v", h)
	}
}
