package connections

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Declared connections: projects.yaml's connections: requests: (who may ask
// whom) and feeds: (whose outputs another uses). Load marks each derived
// request and output edge against them and adds the declared ones nothing
// flows on yet; CheckRequest refuses a request off the declared routes and
// RelayPending says who still has to forward one. Lint checks the entries
// (internal/lint, the departments check). See docs/departments.md#connections.

// Route marks on an edge, set only for a kind the project declares.
const (
	// RouteDeclared: a declared route or feed matches the edge's from → to.
	RouteDeclared = "declared"
	// RouteUndeclared: traffic on a pair nothing declares.
	RouteUndeclared = "undeclared"
	// RouteUnused: declared, nothing flows on it.
	RouteUnused = "unused"
)

// Decl is one declared connection with its ends resolved to box keys.
type Decl struct {
	From, To, Kind, Via, What string
}

// Declared are the project's declared connections whose boxes all resolve,
// requests first, in projects.yaml order; lint names the others.
func Declared(p *tracker.Project) []Decl {
	if p == nil || p.Connections == nil {
		return nil
	}
	var out []Decl
	add := func(kind string, rs []tracker.Route) {
		for _, r := range rs {
			from, to := ResolveBox(p, r.From), ResolveBox(p, r.To)
			via := ""
			if strings.TrimSpace(r.Via) != "" {
				if via = ResolveBox(p, r.Via); via == "" {
					continue
				}
			}
			if from == "" || to == "" || from == to || via == from || via == to {
				continue
			}
			if kind == Output {
				via = ""
			}
			out = append(out, Decl{From: from, To: to, Kind: kind, Via: via, What: strings.TrimSpace(r.What)})
		}
	}
	add(Request, p.Connections.Requests)
	add(Output, p.Connections.Feeds)
	return out
}

// DeclaresKind reports whether the project declares any connection of kind
// (requests: for request, feeds: for output); dependencies never are.
func DeclaresKind(p *tracker.Project, kind string) bool {
	if p == nil || p.Connections == nil {
		return false
	}
	switch kind {
	case Request:
		return len(p.Connections.Requests) > 0
	case Output:
		return len(p.Connections.Feeds) > 0
	}
	return false
}

// index is the position of the declared connection of kind from → to, or -1.
func index(decls []Decl, from, to, kind string) int {
	for i, d := range decls {
		if d.From == from && d.To == to && d.Kind == kind {
			return i
		}
	}
	return -1
}

// findDecl is the declared connection of kind from → to, or nil.
func findDecl(decls []Decl, from, to, kind string) *Decl {
	if i := index(decls, from, to, kind); i >= 0 {
		return &decls[i]
	}
	return nil
}

// IsDeclared reports whether from → to of kind is declared.
func IsDeclared(p *tracker.Project, from, to, kind string) bool {
	return index(Declared(p), from, to, kind) >= 0
}

// Mark sets Route on each request and output edge of a kind the project
// declares (a request from HQ only when declared: HQ is never off route),
// and appends a RouteUnused edge for every declared connection no
// edge travels.
func Mark(p *tracker.Project, edges []Edge) []Edge {
	decls := Declared(p)
	used := map[int]bool{}
	for i := range edges {
		e := &edges[i]
		if !DeclaresKind(p, e.Kind) {
			continue
		}
		j := index(decls, e.From, e.To, e.Kind)
		if j < 0 {
			// HQ may ask anyone, and a group its own departments
			// (CheckRequest): those requests are never off route.
			if !(e.Kind == Request && (e.From == HQ || GroupAsksOwn(p, e.From, e.To))) {
				e.Route = RouteUndeclared
			}
			continue
		}
		e.Route, e.DeclaredVia, e.What = RouteDeclared, decls[j].Via, decls[j].What
		used[j] = true
	}
	for j, d := range decls {
		if !used[j] {
			edges = append(edges, Edge{From: d.From, To: d.To, Kind: d.Kind, Via: d.Via, DeclaredVia: d.Via, What: d.What, Route: RouteUnused})
		}
	}
	return edges
}

// RoutesFrom are the declared request routes from box, as "GTM" or
// "Product via HQ".
func RoutesFrom(p *tracker.Project, box string) []string {
	var out []string
	for _, d := range Declared(p) {
		if d.Kind != Request || d.From != box {
			continue
		}
		s := BoxName(p, d.To)
		if d.Via != "" {
			s += " via " + BoxName(p, d.Via)
		}
		out = append(out, s)
	}
	return out
}

// CheckRequest is the declared route a request from → to takes, or a
// refusal naming the routes from has. Nothing is refused while the project
// declares no requests:, for a request from HQ (the CEO routes) or within
// one box.
func CheckRequest(p *tracker.Project, from, to string) (*Decl, error) {
	if !DeclaresKind(p, Request) || from == "" || from == HQ || from == to {
		return nil, nil
	}
	if d := findDecl(Declared(p), from, to, Request); d != nil {
		return d, nil
	}
	// A group directs its own departments: its week plan files their work
	// straight (the lead of leads, factory/factory-departments-floor-map-and-capabilities#D-6;
	// factory/the-lead-of-leads-at-work-the-gtm-lead-plans-its-departments-week-and-reports-on-the-company-page#D-5).
	if GroupAsksOwn(p, from, to) {
		return nil, nil
	}
	name := BoxName(p, from)
	msg := fmt.Sprintf("%s → %s is not a declared request route (projects.yaml connections: requests:).\n", name, BoxName(p, to))
	if rs := RoutesFrom(p, from); len(rs) > 0 {
		msg += fmt.Sprintf("%s may ask: %s. Ask one of them instead,", name, strings.Join(rs, "; "))
	} else {
		msg += fmt.Sprintf("%s has no declared request routes. Ask the CEO with a question: comment;", name)
	}
	msg += fmt.Sprintf(" the CEO can file it for you (issue-cli create --requested-by %s),\nor the owner declares the route in projects.yaml:\n  connections:\n    requests:\n      - {from: %s, to: %s, via: hq}", from, from, to)
	return nil, fmt.Errorf("%s", msg)
}

// GroupAsksOwn reports whether from is a group department to sits under,
// at any depth: a group may ask its own departments without a declared
// route (factory/nested-groups-…#D-2).
func GroupAsksOwn(p *tracker.Project, from, to string) bool {
	return UnderGroup(p, to, from)
}

// UnderGroup reports whether box dept sits under group box group, right
// under it or deeper.
func UnderGroup(p *tracker.Project, dept, group string) bool {
	for _, g := range p.Ancestors(dept) {
		if tracker.DepartmentKey(g.Name) == group {
			return true
		}
	}
	return false
}

// RelayLead is the session that forwards a request via box: the CEO for
// hq, the group's lead for a group.
func RelayLead(p *tracker.Project, via string) string {
	if via == HQ {
		return tracker.LeadSessionName(p)
	}
	if d := p.DepartmentByKey(via); d != nil {
		return tracker.DepartmentLeadName(p, d.Name)
	}
	return ""
}

// RelayPending is the box that still has to forward issue: its
// requested_by: route is declared via that box and no approval: or
// dispatch: comment was written or recorded by the box's lead. "" when the
// issue is on no via route or was forwarded.
func RelayPending(p *tracker.Project, issue *tracker.Issue, comments []tracker.Comment) string {
	if IsHandoff(issue) {
		return HandoffWaiting(p, issue, comments)
	}
	from := ResolveBox(p, issue.Field(RequestedByField))
	if from == "" {
		return ""
	}
	d := findDecl(Declared(p), from, BoxOf(p, issue), Request)
	if d == nil || d.Via == "" {
		return ""
	}
	if relayedBy(comments, RelayLead(p, d.Via)) {
		return ""
	}
	return d.Via
}

// RouteWord is how a route mark reads on a line: "" for none.
func RouteWord(route string) string {
	switch route {
	case RouteUnused:
		return "declared, no traffic"
	case RouteUndeclared:
		return "not declared"
	case RouteDeclared:
		return "declared"
	}
	return ""
}
