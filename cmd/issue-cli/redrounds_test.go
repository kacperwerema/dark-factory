package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/critique"
	"github.com/kacperwerema/dark-factory/internal/redrounds"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A gate's command that fails, counting its runs in runs.log; it passes once
// the issues dir has a file named green.
const redRoundsWorkflow = `allow_shell: true
max_red_rounds: %d
statuses:
  - name: "in progress"
  - name: "testing"
transitions:
  - from: "in progress"
    to: "testing"
    actions:
      - type: "validate"
        rule: "command_succeeds"
        command: "echo run >> runs.log; test -f green"
`

func makeRedRoundsProject(t *testing.T, max int) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	wf := strings.Replace(redRoundsWorkflow, "%d", itoa(max), 1)
	if max == 0 {
		wf = strings.Replace(wf, "max_red_rounds: 0\n", "", 1)
	}
	if err := os.WriteFile(filepath.Join(root, "workflow.yaml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(issues, "loop.md"), []byte("---\ntitle: \"Loop\"\nstatus: \"in progress\"\n---\n\nBody of Loop.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "loop")
	return &tracker.Project{Slug: "proj", Name: "Proj", IssueDir: issues, WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}
}

func runCount(t *testing.T, proj *tracker.Project) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(proj.IssueDir, "runs.log"))
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "run\n")
}

func capQuestions(t *testing.T, proj *tracker.Project) []redrounds.Question {
	t.Helper()
	comments, err := tracker.LoadComments(filepath.Join(proj.IssueDir, "loop.md"))
	if err != nil {
		t.Fatal(err)
	}
	return redrounds.Questions(comments)
}

// The fourth red round of the same check posts a question for the owner,
// and the next transition refuses without running the check again; the
// owner resolving the question starts the count over.
func TestFourthRedRoundBecomesQuestionAndStopsRetries(t *testing.T) {
	proj := makeRedRoundsProject(t, 0) // unset: the default 4
	// Rounds dated before the question, which carries the wall clock.
	now := time.Now().Add(-time.Hour)
	for round := 1; round <= 3; round++ {
		_, _, err := runYOLOTransition(t, proj, now.Add(time.Duration(round)*time.Minute), "loop", "--to", "testing")
		if err == nil {
			t.Fatalf("round %d passed a red gate", round)
		}
		if want := "red " + itoa(round) + " of 4"; !strings.Contains(err.Error(), want) {
			t.Fatalf("round %d refusal lacks %q:\n%v", round, want, err)
		}
		if qs := capQuestions(t, proj); len(qs) != 0 {
			t.Fatalf("question raised at round %d", round)
		}
	}
	_, _, err := runYOLOTransition(t, proj, now.Add(4*time.Minute), "loop", "--to", "testing")
	if err == nil || !strings.Contains(err.Error(), "question for the owner") || !strings.Contains(err.Error(), "red 4 of 4") {
		t.Fatalf("fourth round: %v", err)
	}
	qs := capQuestions(t, proj)
	if len(qs) != 1 || !qs[0].Open || !strings.HasPrefix(qs[0].Check, "gate:command_succeeds:") {
		t.Fatalf("questions after round 4 = %+v", qs)
	}
	if n := runCount(t, proj); n != 4 {
		t.Fatalf("check ran %d times, want 4", n)
	}

	// No automatic retry: refused before the check runs, even once fixed.
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "green"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = runYOLOTransition(t, proj, now.Add(5*time.Minute), "loop", "--to", "testing")
	if err == nil || !strings.Contains(err.Error(), "not re-run") {
		t.Fatalf("held transition: %v", err)
	}
	if n := runCount(t, proj); n != 4 {
		t.Fatalf("held check ran again: %d runs", n)
	}
	if len(capQuestions(t, proj)) != 1 {
		t.Fatal("question posted twice")
	}

	// rounds shows it.
	ctx, out, _ := newTestContext(proj, false)
	if err := runRounds(ctx, []string{"loop"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "red 4 of 4") || !strings.Contains(out.String(), "question for the owner: comment #") {
		t.Fatalf("rounds output:\n%s", out)
	}
	// Each round says why it counted (AC4).
	for _, want := range []string{
		"round 1 · count 1 · first red round",
		"round 2 · count 2 · same failure: shell check failed (exit #)",
		"as round 1\n",
		"round 4 · count 4 · same failure: ",
		"as round 3\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("rounds output lacks %q:\n%s", want, out)
		}
	}

	// The owner resolves it: the count starts over and the check runs.
	if err := tracker.ToggleComment(filepath.Join(proj.IssueDir, "loop.md"), qs[0].CommentID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runYOLOTransition(t, proj, time.Now().Add(time.Minute), "loop", "--to", "testing"); err != nil {
		t.Fatalf("after the owner resolved it: %v", err)
	}
	if n := runCount(t, proj); n != 5 {
		t.Fatalf("check ran %d times, want 5", n)
	}
}

func TestRedRoundsCapFromWorkflow(t *testing.T) {
	proj := makeRedRoundsProject(t, 2)
	now := time.Now()
	runYOLOTransition(t, proj, now, "loop", "--to", "testing")
	_, _, err := runYOLOTransition(t, proj, now.Add(time.Minute), "loop", "--to", "testing")
	if err == nil || !strings.Contains(err.Error(), "red 2 of 2") || len(capQuestions(t, proj)) != 1 {
		t.Fatalf("max_red_rounds: 2 not applied: %v", err)
	}
}

func TestRoundsCannotFixRaisesQuestion(t *testing.T) {
	proj := makeRedRoundsProject(t, 0)
	ctx, _, _ := newTestContext(proj, false)
	if err := runRounds(ctx, []string{"loop", "--cannot-fix", "test:TestFoo"}); err == nil || !strings.Contains(err.Error(), "--why") {
		t.Fatalf("--cannot-fix without --why: %v", err)
	}
	ctx, out, _ := newTestContext(proj, true)
	if err := runRounds(ctx, []string{"loop", "--cannot-fix", "test:TestFoo", "--why", "needs a real Chrome"}); err != nil {
		t.Fatal(err)
	}
	var got roundsJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Max != 4 || len(got.Streaks) != 1 || !got.Streaks[0].CannotFix || got.Streaks[0].Question == 0 {
		t.Fatalf("rounds --json = %+v", got)
	}
	qs := capQuestions(t, proj)
	if len(qs) != 1 || qs[0].Check != "test:TestFoo" {
		t.Fatalf("questions = %+v", qs)
	}
	// The transition is held now, without running the gate.
	if _, _, err := runYOLOTransition(t, proj, time.Now(), "loop", "--to", "testing"); err == nil || !strings.Contains(err.Error(), "test:TestFoo") {
		t.Fatalf("transition not held: %v", err)
	}
	if runCount(t, proj) != 0 {
		t.Fatal("held gate ran")
	}
}

// Parking runs none of the held check, so an open cap question on the
// forward transition does not refuse it; the forward transition stays held.
func TestOpenCapQuestionDoesNotRefuseParking(t *testing.T) {
	proj := makeRedRoundsProject(t, 2)
	wf := strings.Replace(redRoundsWorkflow, "%d", "2", 1)
	wf = strings.Replace(wf, "  - name: \"testing\"\n", "  - name: \"testing\"\n  - name: \"parked\"\n    optional: true\n    global: true\n", 1)
	wf += "  - from: \"*\"\n    to: \"parked\"\n"
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Hour)
	for round := 1; round <= 2; round++ {
		if _, _, err := runYOLOTransition(t, proj, now.Add(time.Duration(round)*time.Minute), "loop", "--to", "testing"); err == nil {
			t.Fatalf("round %d passed a red gate", round)
		}
	}
	if qs := capQuestions(t, proj); len(qs) != 1 || !qs[0].Open {
		t.Fatalf("questions after the cap = %+v", qs)
	}

	// The forward transition still refuses without running the check.
	_, _, err := runYOLOTransition(t, proj, now.Add(3*time.Minute), "loop", "--to", "testing")
	if err == nil || !strings.Contains(err.Error(), "not re-run") {
		t.Fatalf("held forward transition: %v", err)
	}
	if n := runCount(t, proj); n != 2 {
		t.Fatalf("held check ran again: %d runs", n)
	}

	// Parking passes.
	if _, _, err := runYOLOTransition(t, proj, now.Add(4*time.Minute), "loop", "--to", "parked"); err != nil {
		t.Fatalf("parking refused by the cap question: %v", err)
	}
	path := filepath.Join(proj.IssueDir, "loop.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	issue, err := tracker.ParseIssue(path, data)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Status != "parked" {
		t.Fatalf("status = %q, want parked", issue.Status)
	}
}

// A fixture workflow that keeps the critic gate on (dark-factory turned it
// off in 14a94c2; other projects still run it).
const critiqueRoundsWorkflow = `statuses:
  - name: "in progress"
  - name: "testing"
transitions:
  - from: "in progress"
    to: "testing"
    actions:
      - type: "validate"
        rule: "critique"
`

func makeCritiqueRoundsProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := makeRedRoundsProject(t, 0)
	if err := os.WriteFile(proj.WorkflowFile, []byte(critiqueRoundsWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj
}

func recordHeld(t *testing.T, proj *tracker.Project, at time.Time, finding string) {
	t.Helper()
	run := &critique.Run{Slug: "loop", From: "in progress", To: "testing", At: at, Agent: "claude",
		Report: critique.Parse("blocks issues/loop.md:3 — " + finding + "\nCoverage: all")}
	if err := critique.Record(proj.IssueDir, run); err != nil {
		t.Fatal(err)
	}
}

// AC1 end to end: four held critic runs with new findings each time leave
// the critique gate holding on its own finding, with no cap question; the
// same finding four times raises it and the transition is held by it.
func TestCritiqueRoundsCapOnlyOnARepeatedFinding(t *testing.T) {
	check := redrounds.CritiqueCheck("in progress", "testing")
	now := time.Now().Add(-time.Hour)

	proj := makeCritiqueRoundsProject(t)
	for i := 1; i <= 4; i++ {
		recordHeld(t, proj, now.Add(time.Duration(i)*time.Minute), fmt.Sprintf("new problem %c", 'a'+i))
	}
	_, _, err := runYOLOTransition(t, proj, now.Add(5*time.Minute), "loop", "--to", "testing")
	if err == nil || strings.Contains(err.Error(), "question for the owner") {
		t.Fatalf("new findings: %v", err)
	}
	if qs := capQuestions(t, proj); len(qs) != 0 {
		t.Fatalf("cap question raised on new findings: %+v", qs)
	}
	ctx, out, _ := newTestContext(proj, false)
	if err := runRounds(ctx, []string{"loop"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "red 1 of 4 · "+check) || !strings.Contains(out.String(), "round 4 · count 1 · all failures new: the count starts over") {
		t.Fatalf("rounds output:\n%s", out)
	}

	proj = makeCritiqueRoundsProject(t)
	for i := 1; i <= 4; i++ {
		recordHeld(t, proj, now.Add(time.Duration(i)*time.Minute), "The same problem.")
	}
	_, _, err = runYOLOTransition(t, proj, now.Add(5*time.Minute), "loop", "--to", "testing")
	if err == nil || !strings.Contains(err.Error(), "not re-run") {
		t.Fatalf("same finding four times: %v", err)
	}
	if qs := capQuestions(t, proj); len(qs) != 1 || qs[0].Check != check {
		t.Fatalf("questions = %+v", qs)
	}
}

// AC3 end to end: the owner's comment after three red rounds starts the
// count over, so the fourth red round is "red 1 of 4" and raises nothing.
func TestOwnerCommentStartsRedRoundsOver(t *testing.T) {
	proj := makeRedRoundsProject(t, 0)
	now := time.Now().Add(-time.Hour)
	for round := 1; round <= 3; round++ {
		runYOLOTransition(t, proj, now.Add(time.Duration(round)*time.Minute), "loop", "--to", "testing")
	}
	path := filepath.Join(proj.IssueDir, "loop.md")
	if err := tracker.AddCommentBy(path, 0, "try a file named green", "viewer", tracker.OwnerAuthor); err != nil {
		t.Fatal(err)
	}
	// The comment carries the wall clock: the next round comes after it.
	_, _, err := runYOLOTransition(t, proj, time.Now().Add(time.Minute), "loop", "--to", "testing")
	if err == nil || !strings.Contains(err.Error(), "red 1 of 4") {
		t.Fatalf("after the owner's comment: %v", err)
	}
	if qs := capQuestions(t, proj); len(qs) != 0 {
		t.Fatalf("question raised: %+v", qs)
	}
}

// A cap question on a check the transition no longer runs (the critic
// gate was taken out of workflow.yaml) stays open but doesn't hold it.
func TestCapQuestionOnARemovedCheckDoesNotHold(t *testing.T) {
	proj := makeRedRoundsProject(t, 0)
	path := filepath.Join(proj.IssueDir, "loop.md")
	text := redrounds.QuestionText(redrounds.Streak{Check: redrounds.CritiqueCheck("in progress", "testing"), Rounds: 4}, 4)
	if err := tracker.AddCommentBy(path, 0, text, "cli", "agent-loop"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "green"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runYOLOTransition(t, proj, time.Now(), "loop", "--to", "testing"); err != nil {
		t.Fatalf("held on a check the transition doesn't run: %v", err)
	}
	if n := runCount(t, proj); n != 1 {
		t.Fatalf("gate ran %d times, want 1", n)
	}
	if qs := capQuestions(t, proj); len(qs) != 1 || !qs[0].Open {
		t.Fatalf("the question should stay open: %+v", qs)
	}
}
