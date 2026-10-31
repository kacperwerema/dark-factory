package proof

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/checks"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// judgementRun is a ux-test run that left the owner's judgement step TP9
// (proves AC4) pending.
func judgementRun(slug, status string) string {
	return "session: s\nissue: " + slug + "\nmode: auto\nstarted: \"2026-10-23T15:00:00Z\"\nsteps:\n  - id: TP9\n    status: " + status + "\n    judgement_step: true\n    proves: [AC4]\n"
}

func TestJudgementStepMakesACriterionTheOwners(t *testing.T) {
	f := newFixture(t, "TestFoo")
	writeRun(t, f.root, "s", "1-auto", judgementRun(f.issue.Slug, "pending"))
	// A trailer for AC4 as well: the worker's commit, not the owner's check.
	gitAt(t, f.root, t0, "checkout", "-q", "work/"+f.issue.Slug)
	commitFile(t, f.root, t0.Add(5*time.Hour), "pkg/bar.go", "package pkg\n", "bar\n\nProves: AC4")
	gitAt(t, f.root, t0, "checkout", "-q", "main")

	r := Collect(f.root, f.issue, testsComment(t0.Add(6*time.Hour)))
	ac4 := criterion(t, r, "AC4")
	if ac4.Owner == "" || !strings.Contains(ac4.Owner, "TP9") {
		t.Fatalf("AC4 owner = %q, want the judgement step TP9 named", ac4.Owner)
	}
	if !ac4.Proven() {
		t.Fatalf("AC4 = %+v: the gate still counts the trailer as today", ac4.Links)
	}
	ticked, err := TickProven(f.issue.FilePath, r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(ticked, ","), "AC4") {
		t.Errorf("ticked %v: an owner-only criterion must not be ticked from a trailer", ticked)
	}
	pending := r.OwnerWaiting()
	if len(pending) != 1 || pending[0].ID != "AC4" || pending[0].Text != "A step proves it" {
		t.Errorf("owner waiting = %+v, want AC4", pending)
	}

	// The owner's playtest passes TP9: now it is proven by the owner and ticks.
	writeRun(t, f.root, "s", "2-judge", strings.Replace(strings.Replace(judgementRun(f.issue.Slug, "pass"), "mode: auto", "mode: judge", 1), "15:00:00Z", "16:00:00Z", 1))
	r = Collect(f.root, f.issue, testsComment(t0.Add(6*time.Hour)))
	if got := r.OwnerWaiting(); len(got) != 0 {
		t.Errorf("owner waiting after the owner's pass = %+v", got)
	}
	ticked, _ = TickProven(f.issue.FilePath, r)
	if !strings.Contains(strings.Join(ticked, ","), "AC4") {
		t.Errorf("ticked %v, want AC4 after the owner's judgement step passed", ticked)
	}
}

func TestYOLOGateDefersOnlyOwnerCriteria(t *testing.T) {
	f := newFixture(t, "TestFoo")
	writeRun(t, f.root, "s", "1-auto", judgementRun(f.issue.Slug, "pending"))
	if err := tracker.SaveData(f.issue.FilePath, tracker.DataStore{Proofs: map[string][]tracker.ProofLink{
		"AC3": {{Kind: tracker.ProofValue, Ref: "690 vs 380"}},
	}}); err != nil {
		t.Fatal(err)
	}
	r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	if err := GateError(r); err == nil || !strings.Contains(err.Error(), "AC4") {
		t.Fatalf("YOLO off: GateError = %v, want AC4 unproven as today", err)
	}
	if err := YOLOGateError(r); err != nil {
		t.Fatalf("YOLO: GateError = %v, want nil when only the owner's AC4 is unproven", err)
	}

	// A criterion that is not the owner's still blocks under YOLO.
	if err := tracker.SaveData(f.issue.FilePath, tracker.DataStore{}); err != nil {
		t.Fatal(err)
	}
	r = Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	err := YOLOGateError(r)
	if err == nil || !strings.Contains(err.Error(), "AC3") || strings.Contains(err.Error(), "AC4 ") {
		t.Fatalf("YOLO with AC3 unproven: %v, want AC3 named and AC4 left to the owner", err)
	}
}

func TestOwnerMarkedCriterion(t *testing.T) {
	f := newFixture(t, "TestFoo")
	body, _ := os.ReadFile(f.issue.FilePath)
	marked := strings.Replace(string(body), "- [ ] The change proves it", "- [ ] The change proves it (owner)", 1)
	if err := os.WriteFile(f.issue.FilePath, []byte(marked), 0o644); err != nil {
		t.Fatal(err)
	}
	f.issue.BodyRaw = marked[strings.Index(marked, "\n---\n")+5:]
	r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	if c := criterion(t, r, "AC2"); c.Owner != "marked (owner)" || !c.Proven() {
		t.Fatalf("AC2 = owner %q proven %v", c.Owner, c.Proven())
	}
	ticked, _ := TickProven(f.issue.FilePath, r)
	if strings.Contains(strings.Join(ticked, ","), "AC2") {
		t.Errorf("ticked %v: the owner ticks an (owner) box", ticked)
	}
}

// writeCheckScript puts a guided check for the issue in root and a machine
// run record with the given step lines.
func writeCheckScript(t *testing.T, root, slug string, steps ...string) {
	t.Helper()
	script := filepath.Join(root, checks.Dir, "walk.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\n# issue: "+slug+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, checks.RecordDir, "walk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := "script: walk\nissue: " + slug + "\nmode: machine\n" + strings.Join(steps, "\n") + "\nboxes passed: AC1\n"
	if err := os.WriteFile(filepath.Join(dir, "20261023T150000Z-machine.txt"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGuidedCheckOwnerStepsAreTheOwners(t *testing.T) {
	f := newFixture(t, "TestFoo")
	body, _ := os.ReadFile(f.issue.FilePath)
	withManual := string(body) + "\n## Test Plan\n### Manual\n- [ ] The owner walks the sandbox\n"
	if err := os.WriteFile(f.issue.FilePath, []byte(withManual), 0o644); err != nil {
		t.Fatal(err)
	}
	f.issue.BodyRaw = withManual[strings.Index(withManual, "\n---\n")+5:]
	writeCheckScript(t, f.root, f.issue.Slug,
		"step S1 pass boxes=AC1",
		"step S2 eyes boxes=AC2",
		"step S3 skip boxes=TP1 note=touches your real environment; run it from a terminal")

	r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	if c := criterion(t, r, "AC1"); c.Owner != "" {
		t.Errorf("AC1 owner = %q: its step passed in the machine run", c.Owner)
	}
	ac2 := criterion(t, r, "AC2")
	if !strings.Contains(ac2.Owner, "walk") || !strings.Contains(ac2.Owner, "S2") || !strings.Contains(ac2.Owner, "needs your eyes") {
		t.Errorf("AC2 owner = %q, want the guided check step left for the owner", ac2.Owner)
	}
	ticked, _ := TickProven(f.issue.FilePath, r)
	if strings.Contains(strings.Join(ticked, ","), "AC2") {
		t.Errorf("ticked %v: AC2's trailer must not tick the owner's check", ticked)
	}
	var ids []string
	for _, w := range r.OwnerWaiting() {
		ids = append(ids, w.ID)
		if w.ID == "TP1" && (w.Text != "The owner walks the sandbox" || !strings.Contains(w.Why, "skipped")) {
			t.Errorf("TP1 = %+v", w)
		}
	}
	if strings.Join(ids, ",") != "AC2,TP1" {
		t.Errorf("owner waiting = %v, want AC2 and the skipped Manual box TP1", ids)
	}
}

func TestHumanTestingAndOwnerMarkedBoxesAreTheOwners(t *testing.T) {
	f := newFixture(t, "TestFoo")
	body, _ := os.ReadFile(f.issue.FilePath)
	more := string(body) + "\n## Test Plan\n### Manual\n- [ ] Repro steps re-run and the bug is gone (owner)\n- [ ] Sandbox prepared\n\n## Human Testing\n- [ ] Human verification complete if required\n"
	if err := os.WriteFile(f.issue.FilePath, []byte(more), 0o644); err != nil {
		t.Fatal(err)
	}
	f.issue.BodyRaw = more[strings.Index(more, "\n---\n")+5:]
	r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	var got []string
	for _, w := range r.OwnerWaiting() {
		got = append(got, w.ID+" "+w.Why)
	}
	if strings.Join(got, ",") != "TP1 marked (owner),HT1 Human Testing box" {
		t.Fatalf("owner waiting = %v, want TP1 and HT1 (not the worker's TP2)", got)
	}
	// They are no criteria: the gate, YOLO or not, is as before.
	if (GateError(r) == nil) != (YOLOGateError(r) == nil) {
		t.Fatalf("GateError %v, YOLOGateError %v", GateError(r), YOLOGateError(r))
	}
}

// A box outside the criteria whose id is a judgement step (step id = box
// id) is the owner's: a worker never ticks "TP7 … (owner's look)".
func TestJudgementStepBoxIsTheOwners(t *testing.T) {
	f := newFixture(t, "TestFoo")
	body, _ := os.ReadFile(f.issue.FilePath)
	more := string(body) + "\n## Test Plan\n- [x] TP1 Done by the ux-test\n- [ ] TP2 The page reads like the Workshop Board (owner's look)\n- [ ] TP3 Not a judgement step\n"
	if err := os.WriteFile(f.issue.FilePath, []byte(more), 0o644); err != nil {
		t.Fatal(err)
	}
	f.issue.BodyRaw = more[strings.Index(more, "\n---\n")+5:]
	writeRun(t, f.root, "s", "1-auto", "session: s\nissue: "+f.issue.Slug+"\nmode: auto\nstarted: \"2026-10-23T15:00:00Z\"\nsteps:\n  - id: TP2\n    status: pass\n    judgement_step: true\n  - id: TP3\n    status: pass\n")
	r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	var got []string
	for _, w := range r.OwnerWaiting() {
		got = append(got, w.ID+" "+w.Why)
	}
	if strings.Join(got, ",") != "TP2 playtest judgement step TP2" {
		t.Fatalf("owner waiting = %v, want only TP2 as a judgement step", got)
	}
}

// A ux-test (auto) pass of a judgement step checks the parts are there; it
// is not the owner's look, so the criterion still waits for the owner and
// is never ticked from it.
func TestAutoPassOfAJudgementStepIsNotTheOwners(t *testing.T) {
	f := newFixture(t, "TestFoo")
	writeRun(t, f.root, "s", "1-auto", judgementRun(f.issue.Slug, "pass"))
	r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	ac4 := criterion(t, r, "AC4")
	if ac4.Owner == "" || ac4.OwnerProven() {
		t.Fatalf("AC4 owner %q proven %v: an auto pass must not prove it for the owner", ac4.Owner, ac4.OwnerProven())
	}
	if got := r.OwnerWaiting(); len(got) != 1 || got[0].ID != "AC4" {
		t.Errorf("owner waiting = %+v, want AC4", got)
	}
	ticked, _ := TickProven(f.issue.FilePath, r)
	if strings.Contains(strings.Join(ticked, ","), "AC4") {
		t.Errorf("ticked %v: AC4 is the owner's", ticked)
	}
}

func TestIsOwnerCriterionAnywhere(t *testing.T) {
	for text, want := range map[string]bool{
		"The change proves it (owner)":                      true,
		"(owner) The owner picks the name":                  true,
		"Picks (owner) the name from the shortlist":         true,
		"Owner visual check of the board (after ship)":      true,
		"owner read-through of the docs":                    true,
		"Owner decides the price":                           true,
		"Owner picks one of the three names":                true,
		"The owner's review of the page":                    false,
		"The owner review step is the owner's (YOLO queue)": false,
		"The test proves it":                                false,
		"Owner boxes are refused for agents (tests)":        false,
		"Owner checks page lists every open box":            false,
		"An agent never ticks the owner's box":              false,
		"Ownership moves to the lead":                       false,
		"Owner-only criteria defer under YOLO (tests)":      false,
		"A box marked (visual) renders a shot":              false,
	} {
		if got := IsOwnerCriterion(text); got != want {
			t.Errorf("IsOwnerCriterion(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestOwnerMarkedAnywhereDefersUnderYOLO(t *testing.T) {
	for _, crit := range []string{"(owner) The change proves it", "Owner visual check of the change (after ship)"} {
		f := newFixture(t, "TestFoo")
		body, _ := os.ReadFile(f.issue.FilePath)
		marked := strings.Replace(string(body), "- [ ] The change proves it", "- [ ] "+crit, 1)
		if err := os.WriteFile(f.issue.FilePath, []byte(marked), 0o644); err != nil {
			t.Fatal(err)
		}
		f.issue.BodyRaw = marked[strings.Index(marked, "\n---\n")+5:]
		r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
		if c := criterion(t, r, "AC2"); c.Owner != "marked (owner)" {
			t.Fatalf("%q: AC2 owner = %q, want marked (owner)", crit, c.Owner)
		}
		if why := r.OwnerBoxWhy(tracker.Criteria(f.issue.BodyRaw)[1]); why == "" {
			t.Errorf("%q: OwnerBoxWhy is empty: an agent could tick it", crit)
		}
		for _, w := range r.OwnerWaiting() {
			if w.ID == "AC2" {
				goto queued
			}
		}
		t.Errorf("%q: AC2 not in the owner's queue: %+v", crit, r.OwnerWaiting())
	queued:
		for _, c := range r.Unproven() {
			if c.ID == "AC2" {
				t.Errorf("%q: AC2 counted unproven by the gate (Proven %v)", crit, c.Proven())
			}
		}
		if err := YOLOGateError(r); err != nil && strings.Contains(err.Error(), "AC2") {
			t.Errorf("%q: YOLO gate blocks on the owner's AC2: %v", crit, err)
		}
		ticked, _ := TickProven(f.issue.FilePath, r)
		if strings.Contains(strings.Join(ticked, ","), "AC2") {
			t.Errorf("%q: ticked %v: the owner ticks an owner box", crit, ticked)
		}
	}
}
