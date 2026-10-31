package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

// reportProject is a project on the bundled development template with one
// report issue at idea, run as its worker. The temp dir is no git repo: a
// report issue needs no branch and no commit.
func reportProject(t *testing.T) (proj *tracker.Project, slug, issuePath string) {
	t.Helper()
	tpl, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "issues", "Research"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "workflow.yaml"), string(tpl))
	issuePath = filepath.Join(root, "issues", "Research", "bench.md")
	mustWrite(t, issuePath, "---\ntitle: \"Benchmark pack\"\nstatus: \"idea\"\nsystem: \"Research\"\ntype: \"report\"\nassignee: \"agent-x\"\ncreated: \"2026-10-31\"\n---\n\nWhich golden tasks should the benchmark pack hold?\n")
	slug = "research/benchmark-pack"
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", slug)
	proj = &tracker.Project{Slug: "proj", Name: "Proj", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}
	return proj, slug, issuePath
}

func tickSection(t *testing.T, proj *tracker.Project, slug, issuePath, section string) {
	t.Helper()
	var ids []string
	for _, it := range tracker.ListCheckboxes(readFile(t, issuePath)) {
		if strings.EqualFold(it.Section, section) && !it.Checked {
			ids = append(ids, it.ID)
		}
	}
	if len(ids) == 0 {
		t.Fatalf("no open boxes in %s:\n%s", section, readFile(t, issuePath))
	}
	ctx, _, _ := newTestContext(proj, false)
	if err := runCheck(ctx, append([]string{slug}, ids...)); err != nil {
		t.Fatalf("check %v: %v", ids, err)
	}
}

// A report issue walks idea → in progress → human-testing → done with no
// build boxes and no commit; the report is on the shelf with the issue, and
// the owner review is the owner's (the YOLO queue under YOLO).
func TestReportTypeGoesIdeaToDoneWithoutABuild(t *testing.T) {
	proj, slug, issuePath := reportProject(t)
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)

	if _, _, err := runYOLOTransition(t, proj, now, slug, "--to", "in progress"); err != nil {
		t.Fatalf("idea → in progress: %v", err)
	}
	body := readFile(t, issuePath)
	assertContains(t, body, "## Research")
	for _, build := range []string{"## Implementation", "## Test Plan", "## Testing", "## Shipping", "## Documentation"} {
		if strings.Contains(body, build) {
			t.Errorf("a report issue got %s:\n%s", build, body)
		}
	}

	tickSection(t, proj, slug, issuePath, "Research")
	ctx, _, _ := newTestContext(proj, false)
	if err := runComment(ctx, []string{slug, "--text", "report: hold 12 tasks; follow-ups: none"}); err != nil {
		t.Fatal(err)
	}
	// No report on the shelf: has_output refuses and says how to add one.
	_, _, err := runYOLOTransition(t, proj, now, slug, "--to", "human-testing")
	if err == nil || !strings.Contains(err.Error(), "no report on the shelf for "+slug) || !strings.Contains(err.Error(), "issue-cli output add --issue "+slug+" --kind report") {
		t.Fatalf("without a report: err = %v", err)
	}

	report := filepath.Join(t.TempDir(), "report.md")
	mustWrite(t, report, "# Benchmark pack\n\n12 tasks.\n")
	ctx, out, _ := newTestContext(proj, false)
	if err := runOutput(ctx, []string{"add", "--issue", slug, "--kind", "report", "--title", "Benchmark pack", "--file", report}); err != nil {
		t.Fatalf("output add: %v", err)
	}
	assertContains(t, out.String(), "[review] report · Benchmark pack")

	if _, _, err := runYOLOTransition(t, proj, now, slug, "--to", "human-testing"); err != nil {
		t.Fatalf("in progress → human-testing: %v", err)
	}
	body = readFile(t, issuePath)
	assertContains(t, body, "## Owner Review")
	var owner tracker.CheckboxItem
	for _, it := range tracker.ListCheckboxes(body) {
		if it.Section == "Owner Review" {
			owner = it
		}
	}
	// The owner's box: the worker can't tick it.
	ctx, _, _ = newTestContext(proj, false)
	if err := runCheck(ctx, []string{slug, owner.ID}); err == nil {
		t.Fatalf("a worker ticked the owner's box %s", owner.ID)
	}

	// YOLO passes done's approval and leaves the owner box in the queue.
	out2, _, err := runYOLOTransition(t, proj, now, slug, "--to", "done")
	if err != nil {
		t.Fatalf("human-testing → done: %v", err)
	}
	assertContains(t, out2, "YOLO: gate human-testing → done passed without the owner")
	assertContains(t, out2, "YOLO: "+owner.ID+": owner check, not done")
	body = readFile(t, issuePath)
	assertContains(t, body, "status: \"done\"")
	assertContains(t, body, "- [ ] Report read; approved or rejected on the shelf (owner)")
}

// With YOLO off, done waits for the owner's approval.
func TestReportTypeDoneWaitsForTheOwner(t *testing.T) {
	proj, slug, issuePath := reportProject(t)
	body := strings.Replace(readFile(t, issuePath), "status: \"idea\"", "status: \"human-testing\"", 1)
	mustWrite(t, issuePath, body)
	_, _, err := runYOLOTransition(t, proj, time.Now(), slug, "--to", "done")
	if err == nil || !strings.Contains(err.Error(), "done") {
		t.Fatalf("YOLO off: err = %v, want the done approval", err)
	}
	if !strings.Contains(readFile(t, issuePath), "status: \"human-testing\"") {
		t.Fatal("the refused transition moved the issue")
	}
}
