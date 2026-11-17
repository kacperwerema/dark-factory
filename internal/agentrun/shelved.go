package agentrun

import (
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A report's worker is done once its report is on the shelf and the issue
// waits on the owner's review: the session sits idle until the owner
// answers, which can take hours. Such a session doesn't count against the
// worker cap (LiveWorkers), and the lead tick closes it (FinishedSessions
// with SweepInput.Shelved); the issue, its progress: comments and the shelf
// keep what it did, and a re-dispatch resumes from them
// (tracker.ResumePromptSection).

// ShelvedReport is an open issue whose work is on the shelf, waiting on the
// owner's review.
type ShelvedReport struct {
	Slug, Status string
	Output       *outputs.Output
}

// ShelvedReports maps the worker session names (agent-<slug>,
// limited-<slug>) of proj's open issues whose work is on the shelf
// (outputs.Shelved) to that issue. The shelf is read only when an open
// issue's path gates on has_output.
func ShelvedReports(proj *tracker.Project, issues []*tracker.Issue) map[string]ShelvedReport {
	if proj == nil {
		return nil
	}
	closed := tracker.ClosedIssueFunc(proj)
	wfs := proj.WorkflowMemo()
	var list []*outputs.Output
	loaded := false
	out := map[string]ShelvedReport{}
	for _, issue := range issues {
		if closed(issue) {
			continue
		}
		wf := wfs.ForIssue(issue)
		if _, _, ok := wf.HasOutputRule(); !ok {
			continue
		}
		if !loaded {
			list, _ = outputs.Load(proj)
			loaded = true
		}
		o := outputs.Shelved(list, wf, issue)
		if o == nil {
			continue
		}
		for _, name := range WorkerSessions(issue.Slug) {
			out[name] = ShelvedReport{Slug: issue.Slug, Status: issue.Status, Output: o}
		}
	}
	return out
}

// OutputLabel names an output in a line: its id and title.
func OutputLabel(o *outputs.Output) string {
	if o == nil {
		return ""
	}
	if o.Title == "" {
		return o.ID
	}
	return o.ID + " \"" + o.Title + "\""
}
