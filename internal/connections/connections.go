// Package connections is what passes between a project's boxes (HQ,
// Product, its departments and groups), derived from what is on disk:
// requests (requested_by:, a decision cited across departments),
// outputs (a shelf output another department's issue links to or that was
// recorded as used) and dependencies (depends_on, blocked_on across
// departments). Lead messages are not on disk and never a source. Load also
// marks the edges against the connections projects.yaml declares
// (declared.go). The company page draws the edges, the
// connections page and issue-cli connections list them. See
// docs/departments.md#connections.
package connections

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Kinds of connection, in the order the page lists them.
const (
	Request    = "request"
	Output     = "output"
	Dependency = "dependency"
)

// Kinds are the kinds, for flags and filters.
var Kinds = []string{Request, Output, Dependency}

// HQ and Product are the two boxes every project has.
const (
	HQ      = goals.HQKey
	Product = goals.ProductKey
)

// RequestedByField is the frontmatter key naming the department (or hq)
// that asked for an issue: issue-cli create fills it.
const RequestedByField = "requested_by"

// ClosedWindow is how long a closed item stays listed under an edge.
const ClosedWindow = 30 * 24 * time.Hour

// Item is one thing behind an edge: an issue, or an output's use.
type Item struct {
	Slug   string `json:"slug,omitempty"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
	// Why is the link that makes it a connection, in one line.
	Why string `json:"why"`
	// Output is the shelf output's id, for an output edge.
	Output string `json:"output,omitempty"`
	// State and Route are a handoff's (issue-cli handoff): "waiting on HQ
	// to forward", "at backlog", "done", "declined: …"; the route taken.
	State string `json:"state,omitempty"`
	Route string `json:"route,omitempty"`
}

// Edge is one directed line between two boxes, of one kind.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	// Via is "hq" for a request the CEO relayed, a group's key for one its
	// lead relayed; "" for a straight line. On an unused declared edge it
	// is the declared via:.
	Via string `json:"via,omitempty"`
	// Route is the edge against the declared connections (declared.go):
	// declared, undeclared or unused; "" when its kind declares none.
	Route string `json:"route,omitempty"`
	// DeclaredVia and What are the declared route's via: and what:.
	DeclaredVia string `json:"declared_via,omitempty"`
	What        string `json:"what,omitempty"`
	Open        []Item `json:"open"`
	Closed      []Item `json:"closed,omitempty"`
}

// Input is what Derive reads; the caller loads it once.
type Input struct {
	Project   *tracker.Project
	Issues    []*tracker.Issue
	Decisions *tracker.DecisionIndex
	Outputs   []*outputs.Output
	// Comments loads an issue's comments; nil reads none (no via-HQ).
	Comments func(*tracker.Issue) []tracker.Comment
	Now      time.Time
}

// BoxOf is the box an issue belongs to: its department's key, else Product.
func BoxOf(p *tracker.Project, issue *tracker.Issue) string {
	if name := p.DepartmentOf(issue); name != "" {
		return tracker.DepartmentKey(name)
	}
	return Product
}

// ResolveBox is the box key a name or key stands for (hq, Product, a
// department), or "" when it names none.
func ResolveBox(p *tracker.Project, name string) string {
	name = strings.TrimSpace(name)
	switch strings.ToLower(name) {
	case "":
		return ""
	case HQ:
		return HQ
	case Product:
		return Product
	}
	if d := p.DepartmentByKey(name); d != nil {
		return tracker.DepartmentKey(d.Name)
	}
	return ""
}

// BoxName is the name a box key shows as.
func BoxName(p *tracker.Project, key string) string {
	switch key {
	case HQ:
		return "HQ"
	case Product:
		return tracker.ProductName
	}
	if d := p.DepartmentByKey(key); d != nil {
		return d.Name
	}
	return key
}

// Boxes are the box keys a filter or flag accepts: hq, product and every
// department, in projects.yaml order.
func Boxes(p *tracker.Project) []string {
	return append([]string{HQ, Product}, p.DepartmentKeys()...)
}

func closed(issue *tracker.Issue) bool {
	switch strings.ToLower(strings.TrimSpace(issue.Status)) {
	case "done", "parked", "closed":
		return true
	}
	return false
}

// recent reports whether a closed issue closed within the window; an
// issue without done_at is not listed.
func recent(issue *tracker.Issue, now time.Time) bool {
	at := strings.TrimSpace(issue.DoneAt)
	if at == "" {
		return false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, at); err == nil {
			return now.Sub(t) <= ClosedWindow
		}
	}
	return false
}

type edgeKey struct{ from, to, kind, via string }

type builder struct {
	in    Input
	edges map[edgeKey]*Edge
	seen  map[edgeKey]map[string]bool
}

// add files an item under its edge, once per edge (the first reason wins).
func (b *builder) add(from, to, kind, via string, item Item, isOpen bool) {
	if from == "" || to == "" || from == to {
		return
	}
	k := edgeKey{from, to, kind, via}
	e := b.edges[k]
	if e == nil {
		e = &Edge{From: from, To: to, Kind: kind, Via: via}
		b.edges[k], b.seen[k] = e, map[string]bool{}
	}
	id := item.Slug
	if item.Output != "" {
		id = "output:" + item.Output + ":" + item.Slug
	}
	if b.seen[k][id] {
		return
	}
	b.seen[k][id] = true
	if isOpen {
		e.Open = append(e.Open, item)
	} else {
		e.Closed = append(e.Closed, item)
	}
}

// issueItem is the item for issue with why, and whether it is listed open
// (true), closed (false) or not at all (ok false).
func (b *builder) issueItem(issue *tracker.Issue, why string) (item Item, isOpen, ok bool) {
	item = Item{Slug: issue.Slug, Title: issue.Title, Status: issue.Status, Why: why}
	if !closed(issue) {
		return item, true, true
	}
	return item, false, recent(issue, b.in.Now)
}

// Derive is every edge between the project's boxes, most open items first.
func Derive(in Input) []Edge {
	b := &builder{in: in, edges: map[edgeKey]*Edge{}, seen: map[edgeKey]map[string]bool{}}
	p := in.Project
	bySlug := map[string]*tracker.Issue{}
	for _, issue := range in.Issues {
		bySlug[strings.ToLower(issue.Slug)] = issue
	}
	ceo := tracker.LeadSessionName(p)

	for _, x := range in.Issues {
		to := BoxOf(p, x)

		// Dependencies: x waits on an open issue in another box.
		deps := map[string]string{}
		var order []string
		for _, slug := range tracker.DependsOn(x) {
			if _, ok := deps[slug]; !ok {
				deps[slug], order = "depends_on "+slug, append(order, slug)
			}
		}
		for _, raw := range tracker.BlockedOn(x) {
			slug := strings.ToLower(strings.TrimPrefix(raw, "#"))
			if _, ok := deps[slug]; !ok {
				deps[slug], order = "blocked_on "+slug, append(order, slug)
			}
		}
		for _, slug := range order {
			y := bySlug[slug]
			if y == nil || closed(y) {
				continue
			}
			if item, isOpen, ok := b.issueItem(x, deps[slug]); ok {
				b.add(BoxOf(p, y), to, Dependency, "", item, isOpen)
			}
		}

		// Requests: asked for by another box, or decided on another box's issue.
		var asks []struct{ from, why string }
		if who := strings.TrimSpace(x.Field(RequestedByField)); who != "" {
			if from := ResolveBox(p, who); from != "" {
				asks = append(asks, struct{ from, why string }{from, "requested_by: " + BoxName(p, from)})
			}
		}
		if in.Decisions != nil {
			for _, e := range in.Decisions.Cited(x.Slug) {
				if e.Issue != nil && e.Issue != x {
					asks = append(asks, struct{ from, why string }{BoxOf(p, e.Issue), "cites " + e.Ref})
				}
			}
		}
		if len(asks) > 0 {
			var comments []tracker.Comment
			if in.Comments != nil {
				comments = in.Comments(x)
			}
			byCEO := relayedBy(comments, ceo)
			for _, a := range asks {
				v := ""
				switch {
				case a.from == HQ:
				case byCEO:
					v = HQ
				default:
					// A group's lead forwarding a request of one of its
					// departments: a group up the chain, below the target
					// when the target is a group above the asker.
					for _, grp := range p.Ancestors(a.from) {
						g := tracker.DepartmentKey(grp.Name)
						if g == to {
							break
						}
						if relayedBy(comments, RelayLead(p, g)) {
							v = g
							break
						}
					}
				}
				item, isOpen, ok := b.issueItem(x, a.why)
				if h, isH := HandoffOf(p, x, comments); isH && h.From == a.from {
					// A handoff draws on the route it took, forwarded or not.
					v, item.State, item.Route = h.Via, h.Word, RouteNames(p, ParseRoute(x.Field(HandoffRouteField)))
				}
				if ok {
					b.add(a.from, to, Request, v, item, isOpen)
				}
			}
		}
	}

	// Outputs: an output's making issue linked from another box's issue, or
	// a recorded use.
	for _, o := range in.Outputs {
		if o.State == outputs.StateRejected {
			continue
		}
		from := o.Department
		if from == "" {
			continue
		}
		if maker := strings.ToLower(strings.TrimSpace(o.Issue)); maker != "" {
			for _, x := range in.Issues {
				if !linksTo(x, maker, in.Decisions) {
					continue
				}
				if item, isOpen, ok := b.issueItem(x, "links to "+o.Issue+", which made "+o.Title); ok {
					item.Output = o.ID
					b.add(from, BoxOf(p, x), Output, "", item, isOpen)
				}
			}
		}
		for _, u := range o.Uses {
			to, item := "", Item{Output: o.ID, Title: o.Title, Why: "used " + u.Date.Format("2006-01-02")}
			if u.By != "" {
				item.Why += " · recorded by " + u.By
			}
			if x := bySlug[strings.ToLower(u.For)]; x != nil {
				to, item.Slug, item.Status = BoxOf(p, x), x.Slug, x.Status
			} else {
				to = ResolveBox(p, u.For)
			}
			b.add(from, to, Output, "", item, in.Now.Sub(u.Date) <= ClosedWindow)
		}
	}

	out := make([]Edge, 0, len(b.edges))
	for _, e := range b.edges {
		if len(e.Open)+len(e.Closed) > 0 {
			out = append(out, *e)
		}
	}
	rank := map[string]int{Request: 0, Output: 1, Dependency: 2}
	sort.Slice(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if len(a.Open) != len(c.Open) {
			return len(a.Open) > len(c.Open)
		}
		if a.From != c.From {
			return a.From < c.From
		}
		if a.To != c.To {
			return a.To < c.To
		}
		if a.Kind != c.Kind {
			return rank[a.Kind] < rank[c.Kind]
		}
		return a.Via < c.Via
	})
	return out
}

// linksTo reports whether issue depends on, is blocked on or cites a
// decision on the issue maker.
func linksTo(issue *tracker.Issue, maker string, x *tracker.DecisionIndex) bool {
	if strings.EqualFold(issue.Slug, maker) {
		return false
	}
	for _, s := range tracker.DependsOn(issue) {
		if s == maker {
			return true
		}
	}
	for _, s := range tracker.BlockedOn(issue) {
		if strings.EqualFold(strings.TrimPrefix(s, "#"), maker) {
			return true
		}
	}
	if x != nil {
		for _, e := range x.Cited(issue.Slug) {
			if e.Issue != nil && strings.EqualFold(e.Issue.Slug, maker) {
				return true
			}
		}
	}
	return false
}

// relayedBy reports whether an approval: or dispatch: comment was written or
// recorded by the relay lead's session (the CEO, a group's lead), or it
// forwarded a handoff (issue-cli handoff forward).
func relayedBy(comments []tracker.Comment, ceo string) bool {
	if ceo != "" && forwarded(comments, ceo) {
		return true
	}
	for _, c := range comments {
		t := strings.TrimSpace(c.Text)
		if !strings.HasPrefix(t, "approval:") && !strings.HasPrefix(t, "dispatch:") {
			continue
		}
		if tracker.CommentAuthor(c) == ceo {
			return true
		}
	}
	return false
}

// Filter keeps the edges touching From / To (the via box counts as either
// end of a relayed line) and of Kind; empty fields keep all.
type Filter struct{ From, To, Kind string }

// Select is the edges that match f, in order.
func Select(edges []Edge, f Filter) []Edge {
	var out []Edge
	for _, e := range edges {
		if f.From != "" && e.From != f.From && !(e.Via != "" && f.From == e.Via) {
			continue
		}
		if f.To != "" && e.To != f.To && !(e.Via != "" && f.To == e.Via) {
			continue
		}
		if f.Kind != "" && e.Kind != f.Kind {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Counts are a box's open items in and out.
type Counts struct{ In, Out int }

// CountsByBox totals open items per box; a relayed request counts at its
// via box (HQ, a group) too.
func CountsByBox(edges []Edge) map[string]Counts {
	out := map[string]Counts{}
	for _, e := range edges {
		n := len(e.Open)
		c := out[e.From]
		c.Out += n
		out[e.From] = c
		c = out[e.To]
		c.In += n
		out[e.To] = c
		if e.Via != "" {
			c = out[e.Via]
			c.In += n
			c.Out += n
			out[e.Via] = c
		}
	}
	return out
}

// OpenTotal is the open items across every edge.
func OpenTotal(edges []Edge) int {
	n := 0
	for _, e := range edges {
		n += len(e.Open)
	}
	return n
}

// Waiting is how many of the edge's open handoffs wait for a forward.
func Waiting(e Edge) int {
	n := 0
	for _, it := range e.Open {
		if strings.HasPrefix(it.State, "waiting") {
			n++
		}
	}
	return n
}

// Label is an edge's line, e.g. "Marketing → HQ → Product · 2 requests",
// with its route mark when it has one (" · not declared").
func Label(p *tracker.Project, e Edge) string {
	path := BoxName(p, e.From) + " → "
	if e.Via != "" {
		path += BoxName(p, e.Via) + " → "
	}
	path += BoxName(p, e.To)
	if e.Route == RouteUnused {
		return path + " · " + e.Kind + " · " + RouteWord(e.Route)
	}
	path += " · " + CountWord(len(e.Open), e.Kind)
	if n := Waiting(e); n > 0 {
		path += fmt.Sprintf(" · %d waiting to be forwarded", n)
	}
	if e.Route == RouteUndeclared {
		path += " · " + RouteWord(e.Route)
	}
	return path
}

// CountWord is "1 request", "2 outputs", "3 dependencies".
func CountWord(n int, kind string) string {
	word := kind
	if n != 1 {
		if kind == Dependency {
			word = "dependencies"
		} else {
			word += "s"
		}
	}
	return strconv.Itoa(n) + " " + word
}

// Load derives the edges from the project's issues (loaded by the caller),
// its decisions, its outputs log and, for requests only, comments, and
// marks them against the declared connections (Mark): unused declared
// ones come last.
func Load(p *tracker.Project, issues []*tracker.Issue, now time.Time) []Edge {
	if p == nil || len(p.Departments) == 0 {
		return nil
	}
	outs, _ := outputs.Load(p)
	return Mark(p, Derive(Input{
		Project: p, Issues: issues, Outputs: outs, Now: now,
		Decisions: tracker.BuildDecisionIndex(issues, nil),
		Comments: func(i *tracker.Issue) []tracker.Comment {
			c, _ := tracker.LoadComments(i.FilePath)
			return c
		},
	}))
}
