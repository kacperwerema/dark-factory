package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// ledgerWorkflow is yoloTestWorkflow with the ledger's design rules on
// in design → backlog; approval says whether that edge also needs the
// owner's approval.
func ledgerWorkflow(rules, approval bool) string {
	wf := yoloTestWorkflow
	edge := "  - from: \"in design\"\n    to: \"backlog\"\n    actions:\n      - type: \"require_human_approval\"\n        status: \"backlog\"\n"
	if !strings.Contains(wf, edge) {
		panic("yoloTestWorkflow changed: update ledgerWorkflow")
	}
	repl := "  - from: \"in design\"\n    to: \"backlog\"\n    actions:\n"
	if rules {
		repl += "      - type: \"validate\"\n        rule: \"decisions_cited\"\n      - type: \"validate\"\n        rule: \"citations_current\"\n"
	}
	if approval {
		repl += "      - type: \"require_human_approval\"\n        status: \"backlog\"\n"
	}
	if !rules && !approval {
		repl += "      - type: \"validate\"\n        rule: \"body_not_empty\"\n"
	}
	return strings.Replace(wf, edge, repl, 1)
}

// makeLedgerProject is the YOLO test project with a hard project decision
// (rules#D-1), a replaced one (rules#D-2 → D-3), and the design issue's
// ## Design set to design.
func makeLedgerProject(t *testing.T, rules, approval bool, design string) *tracker.Project {
	t.Helper()
	proj := makeYOLOProject(t)
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(proj.WorkflowFile, ledgerWorkflow(rules, approval))
	write(filepath.Join(proj.IssueDir, "rules.md"), "---\ntitle: \"Rules\"\nstatus: \"done\"\n---\n\n## Decisions\n\n"+
		`- **D-1** · 2026-10-01 · chat: Agents merge into local main, "merge locally" {scope: project; hard}`+"\n"+
		`- **D-2** · 2026-10-02 · chat: Old storage rule`+"\n"+
		`- **D-3** · 2026-10-03 · chat: New storage rule (replaces D-2)`+"\n")
	write(filepath.Join(proj.IssueDir, "design.md"), "---\ntitle: \"Design\"\nstatus: \"in design\"\ncreated: \"2026-10-31\"\n---\n\nBody of Design.\n\n## Design\n\n"+design+"\n")
	return proj
}

// AC1 through issue-cli: the uncited hard decision refuses in design →
// backlog with its ref, text and fixes; citing it, or n/a with a reason,
// passes; a bare n/a refuses.
func TestTransitionDecisionsCited(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		design string
		pass   bool
		want   []string
	}{
		{"We build it.", false, []string{"decisions_cited: 1 hard decision in force is not cited in ## Design", "rules#D-1 (project): Agents merge into local main", `issue-cli append design --section Design --body "Follows rules#D-1: …"`, `"n/a rules#D-1: <why>"`}},
		{"Follows rules#D-1: merges into local main.", true, nil},
		{"n/a rules#D-1: this design merges nothing", true, nil},
		{"n/a rules#D-1", false, []string{"its n/a line has no reason"}},
		{"Follows rules#D-1; stores per rules#D-2.", false, []string{"citations_current", "rules#D-2 is replaced by rules#D-3: New storage rule"}},
	} {
		proj := makeLedgerProject(t, true, false, tc.design)
		_, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
		if tc.pass {
			if err != nil {
				t.Errorf("%q: want pass, got:\n%v", tc.design, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%q: passed, want a refusal", tc.design)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%q: refusal lacks %q:\n%v", tc.design, w, err)
			}
		}
		if s := loadLeadIssue(t, proj, "design").Status; s != "in design" {
			t.Errorf("%q: status moved to %q", tc.design, s)
		}
	}
}

// AC5: a workflow with neither rule moves the same uncited design as
// before, YOLO on or off, and writes no hold.
func TestTransitionWithoutDecisionRulesUnchanged(t *testing.T) {
	now := time.Now()
	for _, on := range []bool{false, true} {
		proj := makeLedgerProject(t, false, on, "We build it; stores per rules#D-2.")
		if on {
			yoloOn(t, proj, now, time.Hour)
		}
		out, errOut, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
		if err != nil {
			t.Fatalf("yolo=%v: %v", on, err)
		}
		if strings.Contains(out+errOut, "decisions_cited") || strings.Contains(out+errOut, "citations_current") || strings.Contains(out, "yolo-hold") {
			t.Errorf("yolo=%v: output names a ledger rule:\n%s%s", on, out, errOut)
		}
		comments, _ := tracker.LoadComments(loadLeadIssue(t, proj, "design").FilePath)
		for _, c := range comments {
			if strings.HasPrefix(c.Text, "yolo-hold:") {
				t.Errorf("yolo=%v: wrote %q", on, c.Text)
			}
		}
	}
}

// AC6: with YOLO on, an uncited hard decision and a replaced citation pass
// the gate; each leaves one yolo-hold: comment naming its ref, listed in
// the review queue; a dry run writes nothing.
func TestYOLOPassesDecisionHoldsIntoTheReview(t *testing.T) {
	now := time.Now()
	proj := makeLedgerProject(t, true, true, "We build it; stores per rules#D-2.")
	yoloOn(t, proj, now, time.Hour)

	out, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	assertContains(t, out, "YOLO: decisions_cited: rules#D-1 not cited in ## Design — would pass")
	path := loadLeadIssue(t, proj, "design").FilePath
	if comments, _ := tracker.LoadComments(path); len(comments) != 0 {
		t.Fatalf("dry run wrote comments: %+v", comments)
	}

	out, _, err = runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err != nil {
		t.Fatalf("YOLO transition: %v", err)
	}
	assertContains(t, out, "YOLO: decisions_cited: rules#D-1 not cited in ## Design — passed, in the after-the-fact queue")
	assertContains(t, out, "YOLO: citations_current: rules#D-2 replaced by rules#D-3 — passed")
	issue := loadLeadIssue(t, proj, "design")
	if issue.Status != "backlog" {
		t.Fatalf("status = %q", issue.Status)
	}
	comments, _ := tracker.LoadComments(path)
	var holds []string
	for _, c := range comments {
		if strings.HasPrefix(c.Text, "yolo-hold:") {
			holds = append(holds, c.Text)
		}
	}
	want := []string{
		"yolo-hold: decisions_cited rules#D-1 from in design: not cited in ## Design",
		"yolo-hold: citations_current rules#D-2 from in design: replaced by rules#D-3",
	}
	if strings.Join(holds, "\n") != strings.Join(want, "\n") {
		t.Fatalf("holds:\n%s\nwant:\n%s", strings.Join(holds, "\n"), strings.Join(want, "\n"))
	}

	var entry yolo.Entry
	for _, e := range yolo.QueueEntries([]*tracker.Issue{issue}) {
		if e.Slug == "design" {
			entry = e
		}
	}
	if len(entry.Decisions) != 2 || entry.Decisions[0].Decision != "rules#D-1" || entry.Decisions[0].Hold != "decisions_cited" ||
		entry.Decisions[0].Label() != "rules#D-1: decisions_cited passed in YOLO" || !strings.Contains(entry.Detail(), "2 decision holds") {
		t.Fatalf("entry = %+v (%s)", entry, entry.Detail())
	}

	// The owner's Agree naming the ref settles it.
	if err := tracker.AddCommentBy(path, 0, tracker.YOLOReviewComment("agreed", "fine", "viewer", now.Format("2006-01-02"), "rules#D-1"), "app", tracker.OwnerAuthor); err != nil {
		t.Fatal(err)
	}
	comments, _ = tracker.LoadComments(path)
	for _, p := range yolo.Unreviewed(issue, comments) {
		if p.Decision == "rules#D-1" {
			t.Fatalf("rules#D-1 still waits after the owner agreed: %+v", p)
		}
	}
}
