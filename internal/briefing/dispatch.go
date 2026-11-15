package briefing

import (
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// BuildDispatchPrompt composes the prompt a dispatch would send right now for
// issue, without creating the worktree or tmux session. workDir is the
// project's working directory the worktree path is resolved against. live
// reports whether a lead session is running (nil: none is); the prompt says
// whether the project lead is live. The server and issue-cli both call this so their
// output is byte-identical.
func BuildDispatchPrompt(proj *tracker.Project, issue *tracker.Issue, wf *tracker.WorkflowConfig, workDir string, live func(name string) bool) tracker.DispatchPrompt {
	var issues []*tracker.Issue
	if proj != nil && proj.IssueDir != "" {
		issues, _ = proj.LoadIssues()
	}
	wt := tracker.IssueWorktree(workDir, issue, wf)
	route := tracker.LeadFor(proj, issue, issues, live)
	yoloOn := tracker.YOLOOn != nil && proj != nil && tracker.YOLOOn(workDir, proj.Slug)
	if wf.IsFreshStatus(issue.Status) {
		decisions := tracker.IssueDecisionBlock(proj, issues, issue, issue.Status, wf, tracker.DecisionsNow())
		prompt, info := buildStageBriefing(stageInput{proj: proj, issue: issue, wf: wf, worktree: wt, workDir: workDir,
			route: route, decisions: decisions, yoloOn: yoloOn})
		return tracker.DispatchPrompt{
			Prompt:       prompt,
			Slug:         issue.Slug,
			Status:       issue.Status,
			Worktree:     wt.Path,
			Branch:       wt.Branch,
			Session:      tracker.AgentDisplayName(proj, tracker.AgentSessionName(issue.Slug)),
			Lead:         route.Lead,
			DecisionRefs: decisions.Refs(),
			Stage:        &info,
		}
	}
	prompt := tracker.BuildAgentPromptYOLO(proj, issue, wf, wt.Path, wt.Branch, yoloOn)
	prompt += tracker.HandoffPromptSection(wf)
	prompt += tracker.InjectProject(proj, tracker.DepartmentPromptSection(proj, issue))
	prompt += tracker.InjectProject(proj, tracker.LeadPromptSection(issue, route))
	decisions := tracker.IssueDecisionBlock(proj, issues, issue, issue.Status, wf, tracker.DecisionsNow())
	prompt += tracker.InjectProject(proj, tracker.DecisionsPromptSection(decisions, issue.Slug))
	prompt += tracker.InjectProject(proj, RulesPromptSection(wf))
	prompt += tracker.InjectProject(proj, MemoryPromptSection(proj, issue))
	prompt += tracker.InjectProject(proj, tracker.BranchOverlapPromptSection(tracker.IssueBranchOverlaps(proj, workDir, issue, issues), issue.Slug))
	prompt += tracker.ShellPromptSection(tracker.DetectShell())
	prompt += tracker.BuildPromptSection(proj, workDir, wf)
	prompt += tracker.InjectProject(proj, tracker.CIPromptSection(workDir, wf))
	return tracker.DispatchPrompt{
		Prompt:       prompt,
		Slug:         issue.Slug,
		Status:       issue.Status,
		Worktree:     wt.Path,
		Branch:       wt.Branch,
		Session:      tracker.AgentDisplayName(proj, tracker.AgentSessionName(issue.Slug)),
		Lead:         route.Lead,
		DecisionRefs: decisions.Refs(),
		MemoryIDs:    MemoryPromptIDs(proj, issue),
	}
}
