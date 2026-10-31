package main

import (
	"fmt"

	"github.com/michal-franc/issue-viewer/internal/connections"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Declared request routes (projects.yaml connections: requests:) where
// issue-cli makes or starts a request: create and set-meta requested_by
// check the route, dispatch waits for the relay. See
// docs/departments.md#connections.

// checkRoute is the declared route a request from → to takes, or the
// refusal when a department lead or a worker asks off the routes. The CEO
// and the owner's shell route freely.
func checkRoute(proj *tracker.Project, me Identity, from, to string) (*connections.Decl, error) {
	if me.Role != tracker.RoleDepartmentLead && me.Role != tracker.RoleWorker {
		return nil, nil
	}
	return connections.CheckRequest(proj, from, to)
}

// routeNote is the line create and set-meta print for a request on a
// declared via route: who forwards it.
func routeNote(proj *tracker.Project, d *connections.Decl) string {
	if d == nil || d.Via == "" {
		return ""
	}
	lead := connections.RelayLead(proj, d.Via)
	return fmt.Sprintf("  Route: %s → %s → %s (projects.yaml connections:): %s forwards it, by dispatching it or recording its approval:. Tell %s.",
		connections.BoxName(proj, d.From), connections.BoxName(proj, d.Via), connections.BoxName(proj, d.To), lead, lead)
}

// relayRefusal is why a department lead may not dispatch issue yet: it is
// a request on a route declared via another box whose lead hasn't
// forwarded it (connections.RelayPending). "" when it may.
func relayRefusal(proj *tracker.Project, me Identity, issue *tracker.Issue) string {
	if me.Role != tracker.RoleDepartmentLead || (!connections.DeclaresKind(proj, connections.Request) && !connections.IsHandoff(issue)) {
		return ""
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	via := connections.RelayPending(proj, issue, comments)
	if via == "" || connections.ResolveBox(proj, me.Department) == via {
		return ""
	}
	from := connections.ResolveBox(proj, issue.Field(connections.RequestedByField))
	lead := connections.RelayLead(proj, via)
	return fmt.Sprintf("%s is a request %s → %s → %s (projects.yaml connections: requests:): %s forwards it first (issue-cli handoff forward %s, or by dispatching it or recording its approval:).\nAsk %s: issue-cli comment %s \"question: forward this?\"",
		issue.Slug, connections.BoxName(proj, from), connections.BoxName(proj, via), connections.BoxName(proj, connections.BoxOf(proj, issue)), lead, issue.Slug, lead,
		issue.Slug)
}
