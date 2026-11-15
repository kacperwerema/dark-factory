package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// labelDesignFirst gives the YOLO project's design issue the design-first
// label.
func labelDesignFirst(t *testing.T, proj *tracker.Project) {
	t.Helper()
	issue := loadLeadIssue(t, proj, "design")
	raw, err := os.ReadFile(issue.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	labelled := strings.Replace(string(raw), "status: \"in design\"\n", "status: \"in design\"\nlabels:\n  - "+tracker.DesignFirstLabel+"\n", 1)
	if labelled == string(raw) {
		t.Fatal("design issue did not change")
	}
	if err := os.WriteFile(issue.FilePath, []byte(labelled), 0o644); err != nil {
		t.Fatal(err)
	}
}

// With YOLO on, in design → backlog on a design-first issue is refused as
// waiting on the owner; the issue waits on that gate (Needs you), and its
// later gates pass as usual.
func TestYOLODesignFirstWaitsAtTheDesignGate(t *testing.T) {
	proj := makeYOLOProject(t)
	labelDesignFirst(t, proj)
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)

	_, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err == nil {
		t.Fatal("YOLO passed the design gate of a design-first issue")
	}
	if !strings.Contains(err.Error(), "labelled design-first") || !strings.Contains(err.Error(), "tell your lead") {
		t.Fatalf("refusal doesn't say why: %v", err)
	}
	issue := loadLeadIssue(t, proj, "design")
	if issue.Status != "in design" || issue.HumanApproval != "" {
		t.Fatalf("status = %q, approval = %q", issue.Status, issue.HumanApproval)
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	if len(comments) != 0 {
		t.Fatalf("a refused gate left comments: %+v", comments)
	}
	wf := proj.LoadWorkflowForIssue(issue)
	if tracker.WaitingOnGate(wf, issue, comments) == "" {
		t.Fatal("the design gate isn't waiting on the owner: Needs you won't show it")
	}

	out, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog", "--dry-run")
	if err == nil || !strings.Contains(out, "labelled design-first") {
		t.Fatalf("dry run: err = %v\n%s", err, out)
	}

	if !yolo.Eligible(wf, issue, "backlog", "in progress") {
		t.Fatal("a design-first issue's gate after design isn't eligible")
	}
	if got := yolo.OwnerReason(wf, issue, "backlog", "in progress"); got != "" {
		t.Fatalf("later gate held: %s", got)
	}
}

// Without the label the same gate passes in YOLO.
func TestYOLOWithoutDesignFirstPassesTheDesignGate(t *testing.T) {
	proj := makeYOLOProject(t)
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)

	if _, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog"); err != nil {
		t.Fatalf("YOLO refused the design gate of an unlabelled issue: %v", err)
	}
	if issue := loadLeadIssue(t, proj, "design"); issue.Status != "backlog" {
		t.Fatalf("status = %q", issue.Status)
	}
}

// yolo status lists the design-first rule among what YOLO never passes.
func TestYOLOStatusListsDesignFirst(t *testing.T) {
	proj := makeYOLOProject(t)
	found := false
	for _, g := range yolo.OwnerGates(proj, yolo.Valves{}) {
		found = found || g.Why == tracker.YOLONeverDesignFirst
	}
	if !found {
		t.Fatal("OwnerGates leaves out the design-first rule")
	}
}
