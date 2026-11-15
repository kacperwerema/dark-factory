package tracker

import "strings"

// The two statuses that mean the same in every workflow. They are written
// only in this package: everything else asks IsClosedStatus, IsLastStatus,
// IsParkedStatus or Project.IssueDone (TestNoClosedStatusLiteralsOutsideTracker).
const (
	statusDone   = "done"
	statusParked = "parked"
)

// IsClosedStatus is the one answer to "is nobody working on this any more?":
// the status that ends the workflow, any status listed after it (a side exit
// such as obsolete), and parked. A nil or empty workflow, or a status the
// workflow doesn't list, knows only done and parked.
func IsClosedStatus(w *WorkflowConfig, status string) bool {
	s := statusFold(status)
	if s == statusDone || s == statusParked {
		return true
	}
	if w == nil {
		return false
	}
	end := w.endIndex()
	for i, st := range w.Statuses {
		if statusFold(st.Name) == s {
			return i >= end
		}
	}
	return false
}

// IsParkedStatus reports whether status is parked: closed with a reason, so
// not finished.
func IsParkedStatus(status string) bool { return statusFold(status) == statusParked }

// IsLastStatus reports whether status finishes the workflow: done, or the
// end of a file that has none (published). Parked is closed but not this.
func (w *WorkflowConfig) IsLastStatus(status string) bool {
	s := statusFold(status)
	if s == statusDone {
		return true
	}
	end := w.endIndex()
	return end >= 0 && statusFold(w.Statuses[end].Name) == s
}

// endIndex is where the lifecycle ends: done, else the last status that
// isn't optional (optional ones after it are side exits), else the last.
// -1 when there are no statuses.
func (w *WorkflowConfig) endIndex() int {
	if w == nil {
		return -1
	}
	end := -1
	for i, st := range w.Statuses {
		if statusFold(st.Name) == statusDone {
			return i
		}
		if !st.Optional {
			end = i
		}
	}
	if end < 0 {
		return len(w.Statuses) - 1
	}
	return end
}

func statusFold(status string) string { return strings.ToLower(strings.TrimSpace(status)) }

// IssueClosed is IsClosedStatus over the issue's own workflow, so a
// department's issue closes where its file ends. For a pass over many issues
// use ClosedIssueFunc.
func (p *Project) IssueClosed(issue *Issue) bool {
	if IsClosedStatus(nil, issue.Status) {
		return true
	}
	return p != nil && IsClosedStatus(p.LoadWorkflowForIssue(issue), issue.Status)
}

// ClosedIssueFunc is Project.IssueClosed with each workflow loaded once.
func ClosedIssueFunc(proj *Project) func(*Issue) bool {
	workflows := map[string]*WorkflowConfig{}
	return func(issue *Issue) bool {
		if IsClosedStatus(nil, issue.Status) {
			return true
		}
		if proj == nil {
			return false
		}
		key := issue.System + "|" + issue.Type
		wf, ok := workflows[key]
		if !ok {
			wf = proj.LoadWorkflowForIssue(issue)
			workflows[key] = wf
		}
		return IsClosedStatus(wf, issue.Status)
	}
}
