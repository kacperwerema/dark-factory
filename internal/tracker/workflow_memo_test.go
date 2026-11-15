package tracker

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

// repoProject is a department-less project over this repo's workflow.yaml:
// types, system overlays (API, CLI) and off-path statuses to rescue.
func repoProject(t *testing.T) *Project {
	t.Helper()
	file, err := filepath.Abs("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return &Project{Name: "p", Slug: "p", WorkDir: t.TempDir(), WorkflowFile: file}
}

// shape is what a caller reads off a scoped workflow.
func shape(wf *WorkflowConfig) string {
	var edges []string
	for _, tr := range wf.Transitions {
		edges = append(edges, fmt.Sprintf("%s>%s:%d", tr.From, tr.To, len(tr.Actions)))
	}
	var prompts []string
	for _, s := range wf.Statuses {
		prompts = append(prompts, fmt.Sprintf("%s|%v|%v|%d|%s", s.Name, s.Global, s.Optional, len(s.Validation), wf.StatusPrompt(s.Name)))
	}
	return fmt.Sprintf("%s\n%v\n%v\n%s %s %v", wf.PathLine(), edges, prompts, wf.ActiveType, wf.OffPathStatus, wf.typePath)
}

// A pass over many issues clones once per (department, type, system), and
// each issue still gets what LoadWorkflowForIssue gives it, the off-path
// rescue included, without the rescue reaching the issues sharing its key.
func TestWorkflowMemoClonesOncePerKey(t *testing.T) {
	p := repoProject(t)
	var issues []*Issue
	keys := map[string]bool{}
	for i := 0; i < 20; i++ {
		for _, typ := range []string{"tweak", "feature", "", "bogus"} {
			for _, sys := range []string{"API", "CLI", "Viewer", ""} {
				issues = append(issues, &Issue{Slug: fmt.Sprintf("x/%s-%s-%d", typ, sys, i), Type: typ, System: sys, Status: "in progress"})
				keys[typ+"/"+sys] = true
			}
		}
	}
	m := p.WorkflowMemo()
	m.ForIssue(issues[0]) // parse the file outside the count
	before := cloneCount.Load()
	for _, issue := range issues {
		m.ForIssue(issue)
	}
	// "" and "bogus" both resolve to the default type: fewer keys than
	// spellings, never more.
	if got := cloneCount.Load() - before; got > int64(len(keys)) {
		t.Errorf("%d issues cloned %d times, want at most %d (one per key)", len(issues), got, len(keys))
	}

	// A tweak on in design is off its path: rescued for itself only.
	off := &Issue{Slug: "x/off", Type: "tweak", System: "CLI", Status: "in design"}
	on := &Issue{Slug: "x/on", Type: "tweak", System: "CLI", Status: "in progress"}
	for _, issue := range []*Issue{on, off, on, off, {Type: "feature", Status: "idea"}, nil} {
		want, got := shape(p.LoadWorkflowForIssue(issue)), shape(m.ForIssue(issue))
		if got != want {
			t.Errorf("%v: memo gives\n%s\nwant\n%s", issue, got, want)
		}
	}
	if wf := m.ForIssue(off); wf.OffPathStatus != "in design" {
		t.Errorf("off-path issue: OffPathStatus %q", wf.OffPathStatus)
	}
	if wf := m.ForIssue(on); wf.OffPathStatus != "" || wf.GetStatus("in design") != nil {
		t.Error("the rescue reached an issue on its path")
	}
}

// Each issue's config keeps its own decision runtime: holds one issue's
// rules record never show on another's.
func TestWorkflowMemoKeepsDecisionStatePerIssue(t *testing.T) {
	m := repoProject(t).WorkflowMemo()
	a := m.ForIssue(&Issue{Slug: "x/a", Type: "tweak", Status: "in progress"})
	a.holdDecisions([]DecisionHold{{Ref: "x/y#D-1"}})
	b := m.ForIssue(&Issue{Slug: "x/b", Type: "tweak", Status: "in progress"})
	if len(a.DecisionHoldsFound()) == 0 {
		t.Fatal("no hold recorded on a")
	}
	if got := b.DecisionHoldsFound(); len(got) != 0 {
		t.Errorf("b sees a's holds: %v", got)
	}
}

// The memo's callers read what it hands out: running each one's reads over
// a memo config leaves the shared entry as it was.
func TestWorkflowMemoCallersDoNotMutate(t *testing.T) {
	p := repoProject(t)
	m := p.WorkflowMemo()
	issue := &Issue{Slug: "x/a", Type: "tweak", System: "CLI", Status: "testing", BodyRaw: "## Acceptance criteria\n\n- [ ] AC1 x\n"}
	before := m.ForIssue(issue)
	snap := shape(before)
	stats := append([]WorkflowStatus(nil), before.Statuses...)
	wf := m.ForIssue(issue)
	NextGate(wf, issue)
	WaitingOnGate(wf, issue, nil)
	wf.ReadyForApproval(issue, "testing", "shipping", nil)
	wf.HasRole(issue.Status, StatusRoleOwnerTest)
	wf.RoleStatus(StatusRoleWork)
	wf.SectionBoxes(CriteriaSection)
	wf.ResolveType(issue.Type)
	wf.PlaytestForOwner()
	ResolveWorktree(p.WorkDir, issue.Slug, wf)
	if got := shape(m.ForIssue(issue)); got != snap {
		t.Errorf("the reads changed the shared entry:\n%s\nwas\n%s", got, snap)
	}
	if !reflect.DeepEqual(stats, m.ForIssue(issue).Statuses) {
		t.Error("the reads changed a shared status")
	}
}
