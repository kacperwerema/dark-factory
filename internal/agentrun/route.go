package agentrun

import (
	"time"

	"github.com/michal-franc/issue-viewer/internal/routing"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// PaneLimit is a live agent whose pane shows its CLI's usage-limit message
// now: reported on the Crew page and status.md, never killed or typed into.
type PaneLimit struct {
	Session string
	Limit   routing.Limit
}

// SubscriptionsDir is where the machine's subscription state lives; a var
// so tests point it at their sandbox.
var SubscriptionsDir = routing.Dir

// ScanLimits reads the last lines of every live agent and lead pane of proj
// (root scopes standalone sessions, as ForProject), records each limit
// message found as that subscription resting (routing.Mark), and returns the
// agents showing one. It only reads panes.
func ScanLimits(proj *tracker.Project, root string, now time.Time) []PaneLimit {
	var out []PaneLimit
	dir := SubscriptionsDir()
	for _, s := range ForProject(proj, root, ListSessions()) {
		name := BareName(s.Name)
		if !hasManagedPrefix(name) {
			continue
		}
		pane, err := CapturePane(Target(proj, name))
		if err != nil {
			continue
		}
		l, ok := routing.FindLimit("", pane, now)
		if !ok {
			continue
		}
		out = append(out, PaneLimit{Session: name, Limit: l})
		if dir != "" {
			slug := ""
			if proj != nil {
				slug = proj.Slug
			}
			routing.Mark(dir, l, slug, name, now)
		}
	}
	return out
}

// Subscriptions reads the machine's subscription state (empty on error).
func Subscriptions() *routing.State {
	st, _ := routing.Load(SubscriptionsDir())
	return st
}

// RouteAgent picks the agent for a start of subj in proj under wf's routing:
// rules (Product's file when wf is nil): it scans proj's panes for limit
// messages first, so a subscription that just hit its limit is passed over.
// explicit is the owner's --agent, which wins.
func RouteAgent(proj *tracker.Project, wf *tracker.WorkflowConfig, root string, subj routing.Subject, explicit string, now time.Time) routing.Choice {
	ScanLimits(proj, root, now)
	if wf == nil && proj != nil {
		wf = proj.LoadWorkflow()
	}
	var cfg *tracker.RoutingConfig
	if wf != nil {
		cfg = wf.Routing
	}
	return routing.Pick(cfg, subj, explicit, Subscriptions(), now)
}

// RouteIssue is RouteAgent for issue's worker, on the workflow the issue walks.
func RouteIssue(proj *tracker.Project, issue *tracker.Issue, root, explicit string, now time.Time) routing.Choice {
	return RouteAgent(proj, proj.LoadWorkflowForIssue(issue), root, routing.IssueSubject(proj, issue), explicit, now)
}

// RouteStep is the launch report's line for a choice.
func RouteStep(c routing.Choice) Step {
	s := Step{Name: "Agent: " + c.Agent + " (" + c.Why + ")", Status: "ok"}
	switch {
	case c.Hold != "":
		s.Status, s.Detail = "warning", c.Hold+"; starting "+c.Agent+" anyway, as asked (auto-start holds instead). A wrong rest: issue-cli agents clear <agent>"
	case c.Warning != "":
		s.Status, s.Detail = "warning", c.Warning
	}
	return s
}
