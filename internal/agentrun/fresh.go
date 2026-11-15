package agentrun

import (
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// FreshStart is an issue that entered a session: fresh status while its
// worker was live (the stage: mark): the lead's dispatch --fresh ends that
// session and starts a new one at Status, or Hold says why it can't yet.
type FreshStart struct {
	Slug    string
	Session string
	Status  string
	Words   string
	// Live reports whether the old session is still running: dispatch
	// --fresh closes it first.
	Live bool
	Hold string
}

// FreshStarts lists the issues of proj with a stage: mark no dispatch has
// answered. It only reads panes and comments.
func FreshStarts(proj *tracker.Project, issues []*tracker.Issue, yoloOn bool) []FreshStart {
	var out []FreshStart
	for _, issue := range issues {
		if f, ok := FreshStartOf(proj, issue, yoloOn); ok {
			out = append(out, f)
		}
	}
	return out
}

// FreshStartOf is issue's line in FreshStarts; ok is false when it has none.
func FreshStartOf(proj *tracker.Project, issue *tracker.Issue, yoloOn bool) (FreshStart, bool) {
	if proj.IssueClosed(issue) {
		return FreshStart{}, false
	}
	comments := issue.Comments()
	status, ok := tracker.FreshPending(comments)
	if !ok {
		return FreshStart{}, false
	}
	wf := proj.LoadWorkflowForIssue(issue)
	f := FreshStart{Slug: issue.Slug, Session: tracker.AgentSessionName(issue.Slug), Status: issue.Status, Words: tracker.DispatchWords(comments)}
	f.Live = IsLive(proj, f.Session)
	switch {
	case issue.Status != status || !wf.IsFreshStatus(issue.Status):
		// The issue moved on, or the status is no longer fresh: the mark
		// is stale and nothing starts on it.
		return FreshStart{}, false
	case f.Live && PaneWorking(Target(proj, f.Session), dialogLines):
		f.Hold = "its worker is still in a turn: it writes HANDOFF.md and ends it first"
	case f.Words == "":
		f.Hold = "no dispatch: words on the issue to quote; the owner decides"
	case !yoloOn && tracker.WaitingOnGate(wf, issue, comments) != "":
		f.Hold = "it only waits on the owner's approval"
	}
	return f, true
}
