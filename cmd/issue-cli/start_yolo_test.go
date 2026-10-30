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

// startYOLOProject is makeYOLOProject with backlog → in progress also
// requiring an assignee, as the bundled workflow does, and an unclaimed
// backlog issue (in design → backlog clears the assignee).
func startYOLOProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := makeYOLOProject(t)
	wfText := strings.Replace(yoloTestWorkflow, `      - type: "require_human_approval"
        status: "in progress"
`, `      - type: "require_human_approval"
        status: "in progress"
      - type: "validate"
        rule: "has_assignee"
`, 1)
	if err := os.WriteFile(proj.WorkflowFile, []byte(wfText), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "ready.md"), []byte("---\ntitle: \"Ready\"\nstatus: \"backlog\"\n---\n\nBody of Ready.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "ready")
	return proj
}

func runYOLOStart(t *testing.T, proj *tracker.Project, now time.Time, args ...string) (string, string, error) {
	t.Helper()
	ctx, out, errOut := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	err := runStart(ctx, args)
	return out.String(), errOut.String(), err
}

func TestStartPassesYOLOGateFromBacklog(t *testing.T) {
	proj := startYOLOProject(t)
	now := time.Now()

	// Off: start refuses and changes nothing.
	if _, _, err := runYOLOStart(t, proj, now, "ready"); err == nil || !strings.Contains(err.Error(), "human approval for \"in progress\" is missing") {
		t.Fatalf("YOLO off: err = %v", err)
	}
	if issue := loadLeadIssue(t, proj, "ready"); issue.Status != "backlog" || issue.Assignee != "" {
		t.Fatalf("refused start changed the issue: status %q, assignee %q", issue.Status, issue.Assignee)
	}

	yoloOn(t, proj, now, time.Hour)
	out, _, err := runYOLOStart(t, proj, now, "ready")
	if err != nil {
		t.Fatalf("YOLO on: %v\n%s", err, out)
	}
	if !strings.Contains(out, "YOLO: gate backlog → in progress passed without the owner") {
		t.Fatalf("output lacks the YOLO line:\n%s", out)
	}
	issue := loadLeadIssue(t, proj, "ready")
	if issue.Status != "in progress" || issue.Assignee == "" || issue.HumanApproval != "" {
		t.Fatalf("status %q, assignee %q, approval %q", issue.Status, issue.Assignee, issue.HumanApproval)
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	q := yolo.Unreviewed(issue, comments)
	if len(q) != 1 || q[0].Status != "in progress" || q[0].From != "backlog" {
		t.Fatalf("queue = %+v", q)
	}
}
