package agentrun

import (
	"os/exec"
	"time"

	"github.com/michal-franc/issue-viewer/internal/autostart"
	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// LeadSpec builds the launch spec for the project lead, rooted at the
// project checkout root. The caller fills in AgentType and Headless, then
// starts it.
func LeadSpec(proj *tracker.Project, wf *tracker.WorkflowConfig, root string, now time.Time) Spec {
	return Spec{
		WorkDir:   root,
		Workflow:  wf,
		Session:   tracker.LeadSessionName(proj),
		LeadScope: tracker.LeadScopeProject,
		Prompt:    tracker.LeadBrief(wf, tracker.LeadBriefInput{Project: proj, Root: root, Now: now, AutoStart: AutostartBrief(proj, root)}),
	}
}

// AutostartBrief reads the project's auto-start for the lead brief: nil
// when it is off. root is the project checkout; the state lives in the main
// checkout, beside .yolo/.
func AutostartBrief(proj *tracker.Project, root string) *tracker.AutoStartBrief {
	st, err := autostart.Load(ci.RepoRoot(root), proj.Slug)
	if err != nil || !st.On {
		return nil
	}
	return &tracker.AutoStartBrief{Words: st.Words, Below: st.Below, MaxWorkers: st.MaxWorkers, Paused: st.Paused}
}

// MainBranch names the repository's main branch: main, else master, else "".
func MainBranch(workdir string) string {
	for _, candidate := range []string{"main", "master"} {
		if exec.Command("git", "-C", workdir, "rev-parse", "--verify", "--quiet", "refs/heads/"+candidate).Run() == nil {
			return candidate
		}
	}
	return ""
}

// DepartmentLeadSpec builds the launch spec for department dept's lead,
// rooted at the project checkout like the CEO's. wf is the department's
// workflow. The caller fills in AgentType and Headless, then starts it.
func DepartmentLeadSpec(proj *tracker.Project, dept *tracker.Department, wf *tracker.WorkflowConfig, root string, now time.Time) Spec {
	return Spec{
		WorkDir:   root,
		Workflow:  wf,
		Session:   tracker.DepartmentLeadName(proj, dept.Name),
		LeadScope: tracker.LeadScopeDepartment(dept.Name),
		Prompt: tracker.DepartmentLeadBrief(wf, dept, tracker.LeadBriefInput{Project: proj, Root: root, Now: now,
			Live: func(name string) bool { return IsLive(proj, name) }}),
	}
}
