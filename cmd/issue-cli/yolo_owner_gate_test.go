package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// makeOwnerGateProject is the YOLO test project with in design → backlog
// marked owner_only.
func makeOwnerGateProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := makeYOLOProject(t)
	wf := strings.Replace(yoloTestWorkflow, `      - type: "require_human_approval"
        status: "backlog"`, `      - type: "require_human_approval"
        status: "backlog"
        owner_only: true`, 1)
	if wf == yoloTestWorkflow {
		t.Fatal("test workflow did not change")
	}
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj
}

func TestYOLOOwnerOnlyGateRefuses(t *testing.T) {
	proj := makeOwnerGateProject(t)
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)

	_, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err == nil {
		t.Fatal("YOLO passed an owner_only gate")
	}
	if !strings.Contains(err.Error(), "this gate is the owner's even in YOLO") || !strings.Contains(err.Error(), "tell your lead") {
		t.Fatalf("refusal doesn't say why: %v", err)
	}
	issue := loadLeadIssue(t, proj, "design")
	if issue.Status != "in design" || issue.HumanApproval != "" {
		t.Fatalf("status = %q, approval = %q", issue.Status, issue.HumanApproval)
	}
	if comments, _ := tracker.LoadComments(issue.FilePath); len(comments) != 0 {
		t.Fatalf("a refused gate left comments: %+v", comments)
	}

	// The lead's session is refused too: YOLO never writes the approval.
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	if _, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog"); err == nil || !strings.Contains(err.Error(), "owner's even in YOLO") {
		t.Fatalf("lead: err = %v", err)
	}

	// The dry run keeps the approval problem and says why.
	out, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog", "--dry-run")
	if err == nil || !strings.Contains(out, "owner's even in YOLO") || !strings.Contains(out, `"backlog"`) {
		t.Fatalf("dry run: err = %v\n%s", err, out)
	}

	// The gate after it still passes in YOLO.
	wf := proj.LoadWorkflowForIssue(issue)
	if yoloEligible(wf, issue, "in design", "backlog", "backlog") {
		t.Fatal("owner_only gate eligible")
	}
	if !yoloEligible(wf, issue, "backlog", "in progress", "in progress") {
		t.Fatal("plain gate not eligible")
	}
}

// The owner's quoted approval, relayed by the CEO, passes it as today.
func TestYOLOOwnerOnlyGateOwnerApproves(t *testing.T) {
	proj := makeOwnerGateProject(t)
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)

	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	ctx, _, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	if err := runApprove(ctx, []string{"design", "--to", "backlog", "--quote", "launch it"}); err != nil {
		t.Fatal(err)
	}
	out, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err != nil {
		t.Fatalf("owner-approved gate refused: %v", err)
	}
	if strings.Contains(out, "passed without the owner") {
		t.Fatalf("owner's approval reported as a YOLO pass:\n%s", out)
	}
	issue := loadLeadIssue(t, proj, "design")
	comments, _ := tracker.LoadComments(issue.FilePath)
	if q := yolo.Unreviewed(issue, comments); len(q) != 0 {
		t.Fatalf("owner's approval in the after-the-fact queue: %+v", q)
	}
}

// With YOLO off, owner_only changes nothing: the gate waits as any gate.
func TestYOLOOwnerOnlyGateYOLOOff(t *testing.T) {
	proj := makeOwnerGateProject(t)
	now := time.Now()
	_, errOut, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err == nil {
		t.Fatal("gate passed without approval")
	}
	if strings.Contains(err.Error()+errOut, "YOLO") {
		t.Fatalf("YOLO off, refusal mentions YOLO: %v %s", err, errOut)
	}
}

// yolo on and yolo status list the gates YOLO never passes; the owner_only
// one is not among the gates it passes.
func TestYOLOStatusListsNeverPasses(t *testing.T) {
	proj := makeOwnerGateProject(t)
	now := time.Now()
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	run := func(json bool, args ...string) string {
		t.Helper()
		ctx, out, _ := newTestContext(proj, json)
		ctx.Now = func() time.Time { return now }
		if err := runYOLO(ctx, args); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	out := run(false, "on", "--until", "4h", "--quote", "go")
	if strings.Contains(out, "passes: in design → backlog") || !strings.Contains(out, "passes: backlog → in progress") {
		t.Fatalf("on lists the owner_only gate as passing:\n%s", out)
	}
	for _, want := range []string{
		"Never passes (the owner's, even in YOLO):",
		"  - in design → backlog: owner_only: spends money or publishes outside the machine",
		"  - the Human Testing approval: the human's own",
		"  - every gate of an issue labelled experiment",
		"  - release publish: external releases wait for the owner",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("on lacks %q:\n%s", want, out)
		}
	}

	out = run(false, "status")
	if !strings.Contains(out, "  - in design → backlog: owner_only") {
		t.Fatalf("status:\n%s", out)
	}
	out = run(true, "status")
	if !strings.Contains(out, `"never_passes"`) || !strings.Contains(out, `"approves": "backlog"`) {
		t.Fatalf("status --json:\n%s", out)
	}
}
