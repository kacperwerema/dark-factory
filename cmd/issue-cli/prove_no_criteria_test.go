package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// noCriteriaFixture is a project whose tweak path (idea → in progress →
// testing → human-testing) runs the proof gate at testing → human-testing,
// and whose feature path also asks for criteria at idea → in design; one
// issue of the type given, with no ## Acceptance Criteria.
func noCriteriaFixture(t *testing.T, typ string) (*tracker.Project, string) {
	t.Helper()
	dir := t.TempDir()
	workflow := `statuses:
  - name: "idea"
  - name: "in design"
  - name: "in progress"
  - name: "testing"
  - name: "human-testing"
transitions:
  - from: "idea"
    to: "in design"
  - from: "in design"
    to: "in progress"
    actions:
      - type: validate
        rule: "criteria_checkboxes: Acceptance Criteria"
  - from: "idea"
    to: "in progress"
  - from: "in progress"
    to: "testing"
  - from: "testing"
    to: "human-testing"
    actions:
      - type: validate
        rule: "has_assignee"
      - type: validate
        rule: "criteria_proven: since 2026-10-01"
types:
  feature:
    description: "full path"
    path: [idea, in design, in progress, testing, human-testing]
  tweak:
    description: "short path"
    path: [idea, in progress, testing, human-testing]
`
	workflowPath := filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(workflowPath, []byte(workflow), 0644); err != nil {
		t.Fatal(err)
	}
	issuesDir := filepath.Join(dir, "issues")
	if err := os.MkdirAll(filepath.Join(issuesDir, "CLI"), 0755); err != nil {
		t.Fatal(err)
	}
	issue := "---\ntitle: \"sample\"\nstatus: \"idea\"\nsystem: \"CLI\"\ntype: \"" + typ + "\"\nassignee: \"a\"\ncreated: \"2026-11-01\"\n---\n\n## Repro\nIt breaks.\n"
	if err := os.WriteFile(filepath.Join(issuesDir, "CLI", "sample.md"), []byte(issue), 0644); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{Name: "test", Slug: "test", IssueDir: issuesDir, WorkflowFile: workflowPath}, "cli/sample"
}

func TestProveListWithNoCriteriaSaysNothingToProve(t *testing.T) {
	proj, slug := noCriteriaFixture(t, "tweak")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProve(ctx, []string{slug, "--list"}); err != nil {
		t.Fatalf("prove --list on an issue whose path never asks for criteria: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "no acceptance criteria: nothing to prove") {
		t.Fatalf("want the nothing-to-prove line, got:\n%s", out)
	}
	if !strings.Contains(out, "testing → human-testing passes without them") || !strings.Contains(out, "issue-cli append cli/sample --section 'Acceptance Criteria'") {
		t.Fatalf("want the proof gate named and the command that adds a criterion, got:\n%s", out)
	}

	jctx, jout, _ := newTestContext(proj, true)
	if err := runProve(jctx, []string{slug}); err != nil {
		t.Fatalf("prove --json: %v", err)
	}
	var got proveOutput
	if err := json.Unmarshal(jout.Bytes(), &got); err != nil {
		t.Fatalf("prove --json: %v\n%s", err, jout.String())
	}
	if got.Total != 0 || got.Slug != slug || got.Criteria == nil {
		t.Fatalf("prove --json: want total 0, slug and an empty criteria list, got %+v", got)
	}

	// Linking still needs a criterion to link.
	if err := runProve(ctx, []string{slug, "AC1", "--test", "TestX"}); err == nil {
		t.Fatal("prove AC1 --test on an issue with no criteria: want an error")
	}
}

func TestProveListWithNoCriteriaOnAPathThatAsksForThem(t *testing.T) {
	proj, slug := noCriteriaFixture(t, "feature")
	ctx, stdout, _ := newTestContext(proj, false)
	err := runProve(ctx, []string{slug, "--list"})
	if err == nil {
		t.Fatalf("prove --list on a feature with no criteria: want an error, got:\n%s", stdout.String())
	}
	msg := err.Error()
	if !strings.Contains(msg, "asks for them at in design → in progress") || !strings.Contains(msg, "issue-cli append cli/sample --section 'Acceptance Criteria'") {
		t.Fatalf("want the transition that asks for criteria and how to add one, got: %s", msg)
	}
}

func TestRequiresDropsAProofGateThatWouldPass(t *testing.T) {
	proj, slug := noCriteriaFixture(t, "tweak")
	ctx, _, _ := newTestContext(proj, false)
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	wf := proj.LoadWorkflowForIssue(issue)
	requires, _ := issueTransitionContract(wf, issue, "testing", "human-testing")
	if strings.Contains(strings.Join(requires, "\n"), "proof link") {
		t.Fatalf("no criteria: the proof gate passes, so Requires must not list it: %q", requires)
	}
	if len(requires) != 1 {
		t.Fatalf("want the other requirement kept, got %q", requires)
	}

	withCriteria := *issue
	withCriteria.BodyRaw += "\n## Acceptance Criteria\n- [ ] It works\n"
	requires, _ = issueTransitionContract(wf, &withCriteria, "testing", "human-testing")
	if !strings.Contains(strings.Join(requires, "\n"), "proof link") {
		t.Fatalf("with criteria the proof gate stays in Requires: %q", requires)
	}

	old := withCriteria
	old.Created = "2026-09-01"
	requires, _ = issueTransitionContract(wf, &old, "testing", "human-testing")
	if strings.Contains(strings.Join(requires, "\n"), "proof link") {
		t.Fatalf("created before the gate's date: the proof gate passes, so Requires must not list it: %q", requires)
	}
}

func TestFirstWorkStatusGuidanceNamesTheCriteriaGate(t *testing.T) {
	for _, tc := range []struct {
		typ, status, want string
	}{
		{"feature", "in design", "this path asks for ## Acceptance Criteria at in design → in progress. Write them now, before the code"},
		{"tweak", "in progress", "optional on this path; the proof gate at testing → human-testing passes without them"},
		{"tweak", "testing", ""},
	} {
		proj, slug := noCriteriaFixture(t, tc.typ)
		ctx, _, _ := newTestContext(proj, false)
		issue, _, err := findIssueOrErr(ctx, slug)
		if err != nil {
			t.Fatal(err)
		}
		wf := proj.LoadWorkflowForIssue(issue)
		got := criteriaGuidance(wf, issue, tc.status)
		if tc.want == "" {
			if got != "" {
				t.Errorf("%s at %s: want no criteria guidance, got %q", tc.typ, tc.status, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s at %s: want %q in the guidance, got %q", tc.typ, tc.status, tc.want, got)
		}
		with := *issue
		with.BodyRaw += "\n## Acceptance Criteria\n- [ ] It works\n"
		if g := criteriaGuidance(wf, &with, tc.status); g != "" {
			t.Errorf("%s at %s with criteria: want no guidance, got %q", tc.typ, tc.status, g)
		}
	}
}

func TestTransitionIntoFirstWorkStatusPrintsCriteriaGuidance(t *testing.T) {
	proj, slug := noCriteriaFixture(t, "tweak")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{slug, "--to", "in progress"}); err != nil {
		t.Fatalf("transition: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "Acceptance criteria: optional on this path") {
		t.Fatalf("want the criteria line in the guidance, got:\n%s", stdout.String())
	}
}
