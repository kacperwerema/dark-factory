package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// The report from another project's lead: AC1 "A design sketch shown to the owner before building … (owner)" was
// open, and YOLO passed in design → backlog → in progress; the feature was
// built without the owner's look.
const ownerSketchBox = "- [ ] A design sketch shown to the owner before building: the phone sketch under ## Design (owner)"

// withDesignBody appends body to the YOLO project's design issue.
func withDesignBody(t *testing.T, proj *tracker.Project, body string) {
	t.Helper()
	issue := loadLeadIssue(t, proj, "design")
	raw, err := os.ReadFile(issue.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(issue.FilePath, append(raw, []byte("\n"+body+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// With YOLO on, an open (owner) box due before the build holds every gate
// up to the build; once the owner ticks it, YOLO passes them again.
func TestYOLOOwnerBoxBeforeBuildHoldsTheGatesUpToTheBuild(t *testing.T) {
	proj := makeYOLOProject(t)
	withDesignBody(t, proj, "## Acceptance criteria\n"+ownerSketchBox+"\n- [ ] Numbers come from core\n")
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)

	_, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog")
	if err == nil {
		t.Fatal("YOLO passed in design → backlog with the owner's sketch box open")
	}
	if !strings.Contains(err.Error(), "AC1") || !strings.Contains(err.Error(), "before the build") || !strings.Contains(err.Error(), "tell your lead") {
		t.Fatalf("refusal doesn't name the box: %v", err)
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
		t.Fatal("the gate isn't waiting on the owner: Needs you won't show it")
	}
	out, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog", "--dry-run")
	if err == nil || !strings.Contains(out, "AC1") {
		t.Fatalf("dry run: err = %v\n%s", err, out)
	}
	// backlog → in progress is up to the build too; human-testing → documentation is not.
	if yolo.Eligible(wf, issue, "backlog", "in progress") {
		t.Fatal("backlog → in progress passes with the box open")
	}
	if !yolo.Eligible(wf, issue, "human-testing", "documentation") {
		t.Fatal("a gate after the build is held")
	}

	// The owner ticks it: the gate passes under YOLO.
	raw, _ := os.ReadFile(issue.FilePath)
	ticked := strings.Replace(string(raw), "- [ ] A design sketch", "- [x] A design sketch", 1)
	if err := os.WriteFile(issue.FilePath, []byte(ticked), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog"); err != nil {
		t.Fatalf("ticked box still holds: %v", err)
	}
}

// An (owner) box that checks the built thing doesn't hold the build: it
// waits in the after-the-fact queue as before.
func TestYOLOOwnerBoxAfterTheBuildDoesntHoldTheDesignGate(t *testing.T) {
	proj := makeYOLOProject(t)
	withDesignBody(t, proj, "## Acceptance criteria\n- [ ] The page reads well on a phone (owner)\n")
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)
	if _, _, err := runYOLOTransition(t, proj, now, "design", "--to", "backlog"); err != nil {
		t.Fatalf("YOLO held the design gate on an owner box after the build: %v", err)
	}
}

// OwnerBoxesBeforeBuild: the text says before the build, or the box sits
// in an early status's section; unticked, owner-marked boxes only.
func TestOwnerBoxesBeforeBuild(t *testing.T) {
	proj := makeYOLOProject(t)
	wf := proj.LoadWorkflow()
	body := strings.Join([]string{
		"## Design",
		"- [ ] Owner picks the layout (owner)",
		"- [ ] Approach documented",
		"## Acceptance criteria",
		ownerSketchBox,
		"- [x] Mock-up approved before implementation (owner)",
		"- [ ] The board reads well (owner)",
		"- [ ] Shown before the build, `(owner)` named in quotes",
		"## Implementation",
		"- [ ] Sketch before any code (owner)",
	}, "\n")
	var ids []string
	for _, it := range tracker.OwnerBoxesBeforeBuild(wf, body) {
		ids = append(ids, it.ID)
	}
	if got := strings.Join(ids, ","); got != "D1,AC1,I1" {
		t.Fatalf("boxes = %s, want D1,AC1,I1", got)
	}
}
