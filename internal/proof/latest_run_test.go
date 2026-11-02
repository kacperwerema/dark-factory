package proof

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// addBody appends text to the fixture issue's file and its BodyRaw.
func addBody(t *testing.T, f fixture, text string) {
	t.Helper()
	body, err := os.ReadFile(f.issue.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	more := string(body) + text
	if err := os.WriteFile(f.issue.FilePath, []byte(more), 0o644); err != nil {
		t.Fatal(err)
	}
	f.issue.BodyRaw = more[strings.Index(more, "\n---\n")+5:]
}

// run is a results.yaml for session s started at the given hour.
func run(slug, mode, hour, steps string) string {
	return "session: s\nissue: " + slug + "\nmode: " + mode + "\nstarted: \"2026-10-23T" + hour + ":00:00Z\"\nsteps:\n" + steps
}

// An older run marked TP2 a judgement step; the newest run of the same
// session doesn't (the do:/expect: came out): TP2 is not the owner's box.
func TestJudgementComesFromTheNewestRunOfTheSession(t *testing.T) {
	f := newFixture(t, "TestFoo")
	addBody(t, f, "\n## Test Plan\n- [ ] TP1 Automated\n- [ ] TP2 Was the owner's look, now automated\n")
	writeRun(t, f.root, "s", "1-old", run(f.issue.Slug, "auto", "15", "  - id: TP1\n    status: pass\n  - id: TP2\n    status: pass\n    judgement_step: true\n"))
	writeRun(t, f.root, "s", "2-new", run(f.issue.Slug, "auto", "16", "  - id: TP1\n    status: pass\n  - id: TP2\n    status: pass\n"))

	r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	for _, it := range tracker.ListCheckboxes(f.issue.BodyRaw) {
		if it.ID == "TP2" {
			if why := r.OwnerBoxWhy(it); why != "" {
				t.Fatalf("TP2 is the owner's (%s): only an older run marks it a judgement step", why)
			}
		}
	}
	if got := r.OwnerWaiting(); len(got) != 0 {
		t.Fatalf("owner waiting = %+v, want none", got)
	}

	// A -step run of TP1 alone leaves TP2's newest run where it was.
	writeRun(t, f.root, "s", "3-only-tp1", run(f.issue.Slug, "auto", "17", "  - id: TP1\n    status: pass\n    judgement_step: true\n"))
	r = Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	var got []string
	for _, w := range r.OwnerWaiting() {
		got = append(got, w.ID)
	}
	if strings.Join(got, ",") != "TP1" {
		t.Fatalf("owner waiting = %v, want TP1 only (its newest run marks it; TP2's doesn't)", got)
	}
}

// An older run links TP5 to AC2, the newest to AC6: only AC6 is linked.
func TestStepLinksComeFromTheNewestRunOfTheSession(t *testing.T) {
	f := newFixture(t, "TestFoo")
	addBody(t, f, "- [ ] Fifth\n- [ ] Sixth\n\n## Test Plan\n- [ ] TP5 Shows it\n")
	writeRun(t, f.root, "s", "1-old", run(f.issue.Slug, "auto", "15", "  - id: TP5\n    status: pass\n    judgement_step: true\n    proves: [AC2]\n"))
	writeRun(t, f.root, "s", "2-new", run(f.issue.Slug, "auto", "16", "  - id: TP5\n    status: pass\n    judgement_step: true\n    proves: [AC6]\n"))

	r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	for _, c := range r.Criteria {
		for _, l := range c.Links {
			if l.Kind == tracker.ProofStep && c.ID != "AC6" {
				t.Errorf("%s links step %s: the newest run moved TP5 to AC6", c.ID, l.Ref)
			}
		}
	}
	ac6 := criterion(t, r, "AC6")
	if len(ac6.Links) != 1 || ac6.Links[0].Ref != "TP5" {
		t.Fatalf("AC6 links = %+v, want TP5", ac6.Links)
	}
	if ac2 := criterion(t, r, "AC2"); ac2.Owner != "" {
		t.Errorf("AC2 owner = %q: the old run's owner link stayed", ac2.Owner)
	}
}

// The owner-box hint for a judgement step names its session and says how
// to make it automated again; the ways to prove end with the re-run.
func TestJudgementHintSaysDropDoExpectAndRerun(t *testing.T) {
	f := newFixture(t, "TestFoo")
	addBody(t, f, "\n## Test Plan\n- [ ] TP2 The owner's look\n")
	writeRun(t, f.root, "s", "1-auto", run(f.issue.Slug, "auto", "15", "  - id: TP2\n    status: pass\n    judgement_step: true\n  - id: TP9\n    status: pending\n    judgement_step: true\n    proves: [AC4]\n"))
	r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	want := "TP2 is a judgement step (do:/expect: in s); drop them if the step is automated, then re-run make ux-test"
	if got := r.JudgementHint("TP2"); got != want {
		t.Fatalf("hint = %q\nwant  %q", got, want)
	}
	if got := r.JudgementHint("AC4"); !strings.HasPrefix(got, "TP9 is a judgement step (do:/expect: in s)") {
		t.Errorf("AC4 hint = %q, want its judgement step TP9 named", got)
	}
	if got := r.JudgementHint("AC1"); got != "" {
		t.Errorf("AC1 hint = %q, want none", got)
	}
	if w := Ways("x", "AC1")[0]; !strings.HasSuffix(w, "then re-run make ux-test") {
		t.Errorf("ways[0] = %q, want it to end with the re-run", w)
	}
}
