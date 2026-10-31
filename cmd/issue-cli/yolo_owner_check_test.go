package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/yolo"
)

// ownerCheckFixture is makeProofFixture with AC2 the owner's: a ux-test run
// left its judgement step TP9 pending. AC1 is proven by a value.
func ownerCheckFixture(t *testing.T) (*tracker.Project, string) {
	t.Helper()
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "cli/sample")
	proj, issuePath, _ := makeProofFixture(t, "2026-10-23")
	body := strings.Replace(readFile(t, issuePath), "- [ ] The card glows (visual)", "- [ ] The owner likes the colours", 1)
	if err := os.WriteFile(issuePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(projectRoot(proj), ".playtest", "runs", "colours", "1-auto")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	results := "session: colours\nissue: cli/sample\nmode: auto\nstarted: \"2026-10-23T15:00:00Z\"\nsteps:\n  - id: TP9\n    status: pending\n    judgement_step: true\n    proves: [AC2]\n"
	if err := os.WriteFile(filepath.Join(run, "results.yaml"), []byte(results), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "AC1", "--value", "3 cards"}); err != nil {
		t.Fatal(err)
	}
	return proj, issuePath
}

func TestYOLOOwnerCheckWaitsInsteadOfBlocking(t *testing.T) {
	proj, issuePath := ownerCheckFixture(t)
	now := time.Now()

	// YOLO off: the gate refuses as today.
	_, _, err := runYOLOTransition(t, proj, now, "cli/sample", "--to", "human-testing")
	if err == nil || !strings.Contains(err.Error(), "AC2 The owner likes the colours") {
		t.Fatalf("YOLO off: err = %v, want AC2 unproven", err)
	}

	// A value is no proof the owner looked: prove doesn't tick AC2.
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{"cli/sample", "AC2", "--value", "looks fine to me"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "owner check (playtest judgement step TP9)")
	if strings.Contains(readFile(t, issuePath), "- [x] The owner likes the colours") {
		t.Fatal("prove ticked the owner's AC2 from a value")
	}
	if err := tracker.SaveData(issuePath, tracker.DataStore{Proofs: map[string][]tracker.ProofLink{"AC1": {{Kind: tracker.ProofValue, Ref: "3 cards"}}}}); err != nil {
		t.Fatal(err)
	}

	if err := yolo.TurnOn(yoloRoot(proj), proj.Slug, yolo.State{Until: now.Add(time.Hour), By: "owner", Words: "go", How: "viewer"}, now); err != nil {
		t.Fatal(err)
	}
	out, _, err := runYOLOTransition(t, proj, now, "cli/sample", "--to", "human-testing")
	if err != nil {
		t.Fatalf("YOLO on: %v", err)
	}
	assertContains(t, out, "YOLO: AC2: owner check, not done (playtest judgement step TP9)")
	body := readFile(t, issuePath)
	assertContains(t, body, "status: \"human-testing\"")
	assertContains(t, body, "- [ ] The owner likes the colours")

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runYOLOQueue(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), `      AC2: owner check, not done (playtest judgement step TP9) "The owner likes the colours"  from testing`)

	// The owner agrees without the check: it leaves the queue.
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	ctx, _, _ = newTestContext(proj, false)
	if err := runYOLOReview(ctx, []string{"cli/sample", "--agree", "--quote", "fine, I saw it", "--status", "AC2"}); err != nil {
		t.Fatal(err)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	if q := yolo.Queue(issues); len(q) != 0 {
		t.Fatalf("queue after the review = %+v", q)
	}
}

func TestYOLOExperimentOwnerCheckStillBlocks(t *testing.T) {
	proj, issuePath := ownerCheckFixture(t)
	body := strings.Replace(readFile(t, issuePath), "status: \"testing\"", "status: \"testing\"\nlabels:\n  - experiment", 1)
	if err := os.WriteFile(issuePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := yolo.TurnOn(yoloRoot(proj), proj.Slug, yolo.State{Until: now.Add(time.Hour), By: "owner", Words: "go", How: "viewer"}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runYOLOTransition(t, proj, now, "cli/sample", "--to", "human-testing"); err == nil {
		t.Fatal("an experiment's owner check passed under YOLO")
	}
}

// HT1 and a box marked "(owner)" are the owner's though no gate checks them:
// under YOLO the gate that passes after testing queues them by name, a tick
// takes them out, and with YOLO off nothing is queued.
func TestYOLOHumanTestingBoxJoinsTheQueue(t *testing.T) {
	proj := makeYOLOProject(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "testing")
	path := filepath.Join(proj.IssueDir, "testing.md")
	body := "---\ntitle: \"Testing\"\nstatus: \"human-testing\"\n---\n\n## Test Plan\n### Manual\n- [ ] Repro steps re-run and the bug is gone (owner)\n- [x] Sandbox prepared\n\n## Human Testing\n- [ ] Human verification complete if required\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	// YOLO off: the approval gate waits and nothing is queued, as today.
	if _, _, err := runYOLOTransition(t, proj, now, "testing", "--to", "documentation"); err == nil {
		t.Fatal("YOLO off: the gate passed")
	}
	if strings.Contains(readFile(t, path), "yolo-owner-check:") {
		t.Fatal("YOLO off: an owner check was queued")
	}

	yoloOn(t, proj, now, time.Hour)
	out, _, err := runYOLOTransition(t, proj, now, "testing", "--to", "documentation")
	if err != nil {
		t.Fatalf("YOLO on: %v", err)
	}
	assertContains(t, out, "YOLO: TP1: owner check, not done (marked (owner))")
	assertContains(t, out, "YOLO: HT1: owner check, not done (Human Testing box)")
	assertContains(t, readFile(t, path), "- [ ] Human verification complete if required")
	assertContains(t, readFile(t, path), "- [ ] Repro steps re-run and the bug is gone (owner)")

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runYOLOQueue(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), `      HT1: owner check, not done (Human Testing box) "Human verification complete if required"  from human-testing`)
	assertContains(t, stdout.String(), `      TP1: owner check, not done (marked (owner)) "Repro steps re-run and the bug is gone (owner)"  from human-testing`)
	if strings.Contains(stdout.String(), "TP2") {
		t.Fatalf("the ticked worker box was queued:\n%s", stdout.String())
	}

	// The owner ticks HT1: it leaves the queue; TP1 still waits.
	if err := os.WriteFile(path, []byte(strings.Replace(readFile(t, path), "- [ ] Human verification", "- [x] Human verification", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	q := yolo.Queue(issues)
	if len(q) != 2 || q[1].Check != "TP1" {
		t.Fatalf("queue after the tick = %+v, want the documentation pass and TP1", q)
	}
}
