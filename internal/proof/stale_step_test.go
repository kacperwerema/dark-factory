package proof

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A step renumbered in the session file after a run (TP4 → TP7): the old
// run's TP4 link is stale and proves nothing; the new id's link counts.
func TestCollectMarksStepsGoneFromTheSessionStale(t *testing.T) {
	f := newFixture(t, "TestFoo")
	sessions := filepath.Join(f.root, "playtest", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	session := "session: s\nissue: " + f.issue.Slug + "\nsteps:\n  - id: TP7\n    title: renumbered\n    proves: [AC4]\n"
	if err := os.WriteFile(filepath.Join(sessions, "s.yaml"), []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(started, id string) string {
		return "session: s\nissue: " + f.issue.Slug + "\nmode: auto\nstarted: \"" + started + "\"\nsteps:\n  - id: " + id + "\n    status: pass\n    proves: [AC4]\n"
	}
	writeRun(t, f.root, "s", "1-auto", run("2026-10-23T15:00:00Z", "TP4"))

	c := criterion(t, Collect(f.root, f.issue, nil), "AC4")
	if c.Proven() || len(c.Links) != 1 {
		t.Fatalf("AC4 = %+v, want one stale link that proves nothing", c.Links)
	}
	l := c.Links[0]
	if !l.Stale || !strings.HasPrefix(l.Check.State, "stale") || !strings.Contains(l.Check.Fix, "TP7") {
		t.Errorf("TP4 = %+v, want stale with a fix naming the session's step ids", l)
	}

	writeRun(t, f.root, "s", "2-auto", run("2026-10-23T16:00:00Z", "TP7"))
	c = criterion(t, Collect(f.root, f.issue, nil), "AC4")
	if !c.Proven() {
		t.Errorf("AC4 = %+v, want proven by TP7", c.Links)
	}
	for _, l := range c.Links {
		if l.Ref == "TP4" && (l.Check.OK || !l.Stale) {
			t.Errorf("TP4 = %+v, want still stale", l)
		}
	}
}
