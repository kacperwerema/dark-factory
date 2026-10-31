package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/cmd/issue-cli/templates"
	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/yolo"
)

// AC4: under YOLO the gate out of design records the open questions as
// assumed, and they wait in the issue's entry of the review after YOLO;
// the owner's answer or Agree settles each.
func TestYOLOPassAssumesOpenClarifyQuestionsIntoTheReview(t *testing.T) {
	proj := makeYOLOProject(t)
	path := filepath.Join(proj.IssueDir, "design.md")
	addClarify(t, path)
	now := time.Now()

	yoloOn(t, proj, now, time.Hour)
	out, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err != nil {
		t.Fatalf("YOLO transition: %v", err)
	}
	assertContains(t, out, "YOLO: clarify Q1, Q2 assumed (the recommendation)")
	body := readFile(t, path)
	if strings.Count(body, "**Answer:** assumed — YOLO: took the recommendation") != 2 {
		t.Fatalf("body:\n%s", body)
	}

	entry := func() yolo.Entry {
		t.Helper()
		issue := loadLeadIssue(t, proj, "design")
		for _, e := range yolo.QueueEntries([]*tracker.Issue{issue}) {
			if e.Slug == "design" {
				return e
			}
		}
		t.Fatal("design has no entry in the review after YOLO")
		return yolo.Entry{}
	}
	e := entry()
	if len(e.Gates) != 1 || len(e.Questions) != 2 || e.Questions[0].Question != "Q1" || e.Questions[0].From != "in design" ||
		!strings.Contains(e.Questions[0].Why, "assumed On the Station") || e.Detail() != "1 gate, 2 assumed answers" {
		t.Fatalf("entry = %+v (%s)", e, e.Detail())
	}

	// The owner's answer (relayed) replaces Q1's assumed line: it leaves the queue.
	ctx, _, _ := newTestContext(proj, false)
	if err := runClarify(ctx, []string{"design", "--answer", "Q1=2", "--quote", "In the row."}); err != nil {
		t.Fatal(err)
	}
	// A yolo-review: naming Q2 (the review's Agree) settles Q2.
	if err := tracker.AddCommentBy(path, 0, tracker.YOLOReviewComment("agreed", "fine", "viewer", now.Format("2006-01-02"), "Q2"), "app", tracker.OwnerAuthor); err != nil {
		t.Fatal(err)
	}
	if e := entry(); len(e.Questions) != 0 || len(e.Gates) != 1 {
		t.Fatalf("after answer and agree: %+v", e)
	}

	// A second pass assumes nothing more: no open question is left.
	if out, _, err := runYOLOTransition(t, proj, now, "design", "--to", "in progress"); err != nil || strings.Contains(out, "YOLO: clarify") {
		t.Fatalf("second pass: %v\n%s", err, out)
	}
}

// Without YOLO the gate assumes nothing: the questions wait for the owner.
func TestNoYOLONoAssumedClarifyAnswers(t *testing.T) {
	proj := makeYOLOProject(t)
	path := filepath.Join(proj.IssueDir, "design.md")
	addClarify(t, path)
	if _, _, err := runYOLOTransition(t, proj, time.Now(), "design", "--to", "backlog"); err == nil {
		t.Fatal("the gate passed without YOLO")
	}
	if c := tracker.ParseClarify(loadLeadIssue(t, proj, "design").BodyRaw); len(c.Open()) != 2 {
		t.Fatalf("open = %d", len(c.Open()))
	}
}

// AC1: the bundled template's design step and the briefing describe the
// clarify round, and YOLO keeps those lines.
func TestTemplateAndBriefingDescribeTheClarifyRound(t *testing.T) {
	data, err := templates.FS.ReadFile("workflow/development.yaml")
	if err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(wfPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	wf, err := tracker.LoadWorkflow(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	design := wf.StatusPrompt("in design")
	for _, want := range []string{"## Clarify", "### Q1. <question>", "2–4 numbered options", "(recommended)", "Cost: <cost>", "the recommendation first",
		"**Answer:** —", "Nothing to ask: <why>.", "**Answer:** assumed — <reason>", "issue-cli clarify <slug>", "a `question:` comment is for a blocker only"} {
		if !strings.Contains(design, want) {
			t.Errorf("in design prompt lacks %q", want)
		}
	}
	if idea := wf.StatusPrompt("idea"); !strings.Contains(idea, "clarify round (`## Clarify`), all at once") || strings.Contains(idea, "with the human") {
		t.Errorf("idea prompt = %q", idea)
	}
	kept, _ := tracker.YOLOGuidance(design)
	for _, want := range []string{"Write the clarify round once", "Nothing to ask: <why>.", "--assume", "for a blocker only"} {
		if !strings.Contains(kept, want) {
			t.Errorf("YOLO drops %q from the design prompt:\n%s", want, kept)
		}
	}

	old := tracker.YOLOOn
	t.Cleanup(func() { tracker.YOLOOn = old })
	proj := &tracker.Project{Slug: "proj"}
	issue := &tracker.Issue{Slug: "api/fix", Title: "Fix", Status: "in design", BodyRaw: "Body."}
	for _, on := range []bool{false, true} {
		tracker.YOLOOn = func(string, string) bool { return on }
		p := tracker.BuildDispatchPrompt(proj, issue, wf, "/w", nil).Prompt
		for _, want := range []string{"Questions for the owner go only in the design's clarify round", "issue-cli --project proj clarify <slug>", "a question: comment is for a blocker only"} {
			if !strings.Contains(p, want) {
				t.Errorf("YOLO %v: briefing lacks %q", on, want)
			}
		}
	}
	tracker.YOLOOn = func(string, string) bool { return true }
	if rule := tracker.YOLORule("api/fix"); !strings.Contains(rule, "## Clarify round, each marked assumed (issue-cli clarify api/fix --assume)") {
		t.Errorf("YOLO rule = %s", rule)
	}
}
