package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

const criteriaGateWorkflow = `statuses:
  - name: "idea"
    description: "raw"
  - name: "in design"
    description: "design"
  - name: "backlog"
    description: "ready"
  - name: "done"
    description: "shipped"
transitions:
  - from: "idea"
    to: "in design"
    actions:
      - type: "append_section"
        title: "Acceptance Criteria"
        body: |
          - [ ] First observable requirement
      - type: "append_section"
        title: "Must not"
        body: |
          - <what this change must not do; name a test or a command in backticks when one can catch it>
  - from: "in design"
    to: "backlog"
    actions:
      - type: "validate"
        rule: "criteria_checkboxes: Acceptance Criteria"
  - from: "backlog"
    to: "done"
`

// Leaving design prints the criteria findings as warnings and still moves
// the issue: the lint never refuses a transition (#D-1).
func TestTransitionWarnsAboutUnfalsifiableCriteria(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "issues"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflow.yaml"), []byte(criteriaGateWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "---\ntitle: \"Vague\"\nstatus: \"in design\"\n---\n\n## Acceptance Criteria\n\n" +
		"- [ ] The page works on a phone\n- [ ] `issue-cli status` names it\n- [ ] The owner likes the colours (owner)\n\n" +
		"## Must not\n\n- " + tracker.MustNotPlaceholder + "\n"
	if err := os.WriteFile(filepath.Join(root, "issues", "vague.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "vague")
	proj := &tracker.Project{Slug: "proj", Name: "Proj", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}

	// The dry run lists them too, ready to go.
	dry, _, err := runYOLOTransition(t, proj, time.Now(), "vague", "--to", "backlog", "--dry-run")
	if err != nil {
		t.Fatalf("dry run refused: %v\n%s", err, dry)
	}
	out, _, err := runYOLOTransition(t, proj, time.Now(), "vague", "--to", "backlog")
	if err != nil {
		t.Fatalf("transition refused: %v", err)
	}
	for name, text := range map[string]string{"dry run": dry, "transition": out} {
		if !strings.Contains(text, `⚠ criteria: vague AC1: AC1 says "works"`) || !strings.Contains(text, "issue-cli check-edit vague AC1") {
			t.Errorf("%s output lacks the AC1 warning and its fix:\n%s", name, text)
		}
		if !strings.Contains(text, "vague ## Must not") {
			t.Errorf("%s output lacks the Must not warning:\n%s", name, text)
		}
		if strings.Contains(text, "criteria: vague AC2") || strings.Contains(text, "criteria: vague AC3") {
			t.Errorf("%s output warns about a falsifiable or owner box:\n%s", name, text)
		}
	}
	if issue := loadLeadIssue(t, proj, "vague"); issue.Status != "backlog" {
		t.Fatalf("status = %q, want backlog: a warning never refuses", issue.Status)
	}
	// Any other edge says nothing.
	out, _, err = runYOLOTransition(t, proj, time.Now(), "vague", "--to", "done")
	if err != nil || strings.Contains(out, "criteria:") {
		t.Fatalf("backlog → done: err %v, output warns:\n%s", err, out)
	}
}
