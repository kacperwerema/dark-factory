package viewer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connections"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Connections on the company page: what passes between HQ, Product and the
// departments (internal/connections), marked against the connections
// projects.yaml declares (declared, not declared, declared with no
// traffic), as a list under the floor that
// static/connections.js also draws as lines between the rooms, a "⇄ in · out"
// line in each room's foot, and the drill-in page /p/<project>/connections.

// connEmptyHint is what the empty block and page say makes a line.
const connEmptyHint = "Lines appear when an issue in one department depends on (depends_on, blocked_on), cites a decision of, or was requested by another (issue-cli handoff <department>, requested_by:, issue-cli create --requested-by), or when an output is used (issue-cli output use <id> --by <department>)."

// WbConnections is the company page's Connections block; nil when the
// project has no departments.
type WbConnections struct {
	Rows []WbConnRow
	// Declared: projects.yaml declares connections, so rows carry marks.
	Declared bool
	// URL is the drill-in page; Hint what makes a line, for the empty state.
	URL, Hint string
	// Dense: the floor has too many lines to draw them all in full
	// (connDense); connections.js fades them until a room is picked.
	Dense bool
	// Roads are the rows folded into one road per pair (roads_view.go),
	// what connections.js draws.
	Roads []WbRoad
}

// WbConnRow is one edge: names, box keys (for the drawing), kind, counts.
type WbConnRow struct {
	From, To, Via          string
	FromBox, ToBox, ViaBox string
	Kind                   string
	Open, Closed           int
	// Waiting is how many open handoffs on the line wait for a forward.
	Waiting           int
	Label, URL, Color string
	// Route is the line against the declared connections (declared,
	// undeclared, unused; "" when its kind declares none), Mark its words.
	Route, Mark, What string
}

// ConnectionsData is the drill-in page.
type ConnectionsData struct {
	Header WbHeader
	Name   string
	// From, To and Kind are the filters; their tabs below.
	From, To, Kind             string
	FromTabs, ToTabs, KindTabs []WbShelfTab
	Sections                   []WbConnSection
	// NoDepartments: the project has none, so nothing can connect; Empty:
	// it has departments but no line at all (not only none matching).
	NoDepartments, Empty bool
	// Declared: projects.yaml declares connections (the legend's marks).
	Declared bool
	Hint     string
	JSONURL  string
}

// WbConnSection is one edge on the drill-in page with its items.
type WbConnSection struct {
	Row          WbConnRow
	Open, Closed []WbConnItem
}

// WbConnItem is one issue or output use behind an edge.
type WbConnItem struct {
	Slug, URL, Title, Status, Why, Output string
	// State, Route and Tone are a handoff's (handoffs_view.go).
	State, Route, Tone string
}

// boxColors are the boxes' colours: HQ, Product and each department's.
func boxColors(p *tracker.Project) map[string]string {
	c := shelfColors(p)
	c[connections.HQ] = companyHQColor
	return c
}

// connURL is the drill-in page filtered on from, to and kind.
func connURL(prefix, from, to, kind string) string {
	q := url.Values{}
	for k, v := range map[string]string{"from": from, "to": to, "kind": kind} {
		if v != "" {
			q.Set(k, v)
		}
	}
	u := prefix + "/connections"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func connRow(p *tracker.Project, prefix string, e connections.Edge, colors map[string]string) WbConnRow {
	r := WbConnRow{
		From: connections.BoxName(p, e.From), To: connections.BoxName(p, e.To),
		FromBox: e.From, ToBox: e.To, ViaBox: e.Via, Kind: e.Kind,
		Open: len(e.Open), Closed: len(e.Closed), Waiting: connections.Waiting(e),
		Label: connections.Label(p, e), URL: connURL(prefix, e.From, e.To, e.Kind), Color: colors[e.From],
		Route: e.Route, Mark: connections.RouteWord(e.Route), What: e.What,
	}
	if e.Via != "" {
		r.Via = connections.BoxName(p, e.Via)
	}
	if r.Color == "" {
		r.Color = "#8A93A6"
	}
	return r
}

// companyConnections is the block for the company page.
func companyConnections(p *tracker.Project, prefix string, edges []connections.Edge) *WbConnections {
	if len(p.Departments) == 0 {
		return nil
	}
	colors := boxColors(p)
	c := &WbConnections{URL: prefix + "/connections", Hint: connEmptyHint, Declared: len(connections.Declared(p)) > 0}
	for _, e := range edges {
		c.Rows = append(c.Rows, connRow(p, prefix, e, colors))
	}
	c.Roads = companyRoads(p, prefix, edges, colors)
	c.Dense = connDense(len(p.Departments), len(c.Rows))
	return c
}

// connDenseDepts and connDenseRows are where a floor turns dense: more
// departments than a small company has (1-3 keep today's floor) and enough
// lines to cross every room.
const (
	connDenseDepts = 3
	connDenseRows  = 8
)

// connDense reports whether the company floor fades its lines until a room
// is hovered, focused or tapped (D-2 of
// ui/the-map-at-scale-10-lots-and-nested-groups-stay-readable-and-fast).
func connDense(depts, rows int) bool {
	return depts > connDenseDepts && rows >= connDenseRows
}

// connFoot is a room's "⇄ in N · out M" foot item, linking to the page
// filtered on the box; nil when nothing goes in or out.
func connFoot(prefix, box string, counts map[string]connections.Counts) *WbFootItem {
	c, ok := counts[box]
	if !ok || c.In+c.Out == 0 {
		return nil
	}
	u := connURL(prefix, box, "", "")
	if c.Out == 0 {
		u = connURL(prefix, "", box, "")
	}
	return &WbFootItem{Text: fmt.Sprintf("⇄ in %d · out %d", c.In, c.Out), URL: u}
}

// addConnFoot appends a room's connections item to its foot, and to its
// group children's.
func addConnFoot(room *WbRoomDept, prefix string, counts map[string]connections.Counts) {
	if f := connFoot(prefix, room.Box, counts); f != nil {
		room.Foot = append(room.Foot, *f)
	}
	for i := range room.Children {
		addConnFoot(&room.Children[i], prefix, counts)
	}
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	q := r.URL.Query()
	data := s.buildConnections(proj, prefix, q.Get("from"), q.Get("to"), q.Get("kind"), time.Now())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "connections.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleConnectionsJSON(w http.ResponseWriter, r *http.Request, proj *tracker.Project) {
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	q := r.URL.Query()
	edges := connections.Select(connections.Load(proj, issues, time.Now()), connFilter(proj, q.Get("from"), q.Get("to"), q.Get("kind")))
	if edges == nil {
		edges = []connections.Edge{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(edges)
}

// connFilter keeps only known boxes and kinds; anything else filters
// nothing.
func connFilter(p *tracker.Project, from, to, kind string) connections.Filter {
	f := connections.Filter{From: connections.ResolveBox(p, from), To: connections.ResolveBox(p, to)}
	if slices.Contains(connections.Kinds, strings.ToLower(kind)) {
		f.Kind = strings.ToLower(kind)
	}
	return f
}

func (s *Server) buildConnections(p *tracker.Project, prefix, from, to, kind string, now time.Time) ConnectionsData {
	company := s.buildCompany(p, prefix, now)
	f := connFilter(p, from, to, kind)
	d := ConnectionsData{Header: company.Header, Name: p.Name, From: f.From, To: f.To, Kind: f.Kind, Hint: connEmptyHint, JSONURL: prefix + "/connections.json"}
	d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "Connections", Current: true}
	d.NoDepartments = len(p.Departments) == 0
	d.Declared = len(connections.Declared(p)) > 0

	issues, _ := tracker.LoadIssues(p.IssueDir)
	all := connections.Load(p, issues, now)
	d.Empty = len(all) == 0
	colors := boxColors(p)

	tabs := func(cur string, url func(string) string, count func(string) int) []WbShelfTab {
		out := []WbShelfTab{{Label: "Any", URL: url(""), Count: count(""), Current: cur == ""}}
		for _, b := range connections.Boxes(p) {
			out = append(out, WbShelfTab{Label: connections.BoxName(p, b), URL: url(b), Color: colors[b], Count: count(b), Current: cur == b})
		}
		return out
	}
	open := func(edges []connections.Edge) int { return connections.OpenTotal(edges) }
	d.FromTabs = tabs(f.From, func(b string) string { return connURL(prefix, b, f.To, f.Kind) },
		func(b string) int {
			return open(connections.Select(all, connections.Filter{From: b, To: f.To, Kind: f.Kind}))
		})
	d.ToTabs = tabs(f.To, func(b string) string { return connURL(prefix, f.From, b, f.Kind) },
		func(b string) int {
			return open(connections.Select(all, connections.Filter{From: f.From, To: b, Kind: f.Kind}))
		})
	d.KindTabs = []WbShelfTab{{Label: "Any kind", URL: connURL(prefix, f.From, f.To, ""), Count: open(connections.Select(all, connections.Filter{From: f.From, To: f.To})), Current: f.Kind == ""}}
	for _, k := range connections.Kinds {
		d.KindTabs = append(d.KindTabs, WbShelfTab{Label: k, URL: connURL(prefix, f.From, f.To, k), Count: open(connections.Select(all, connections.Filter{From: f.From, To: f.To, Kind: k})), Current: f.Kind == k})
	}

	item := func(it connections.Item) WbConnItem {
		ci := WbConnItem{Slug: it.Slug, Title: it.Title, Status: it.Status, Why: it.Why, Output: it.Output, State: it.State, Route: it.Route}
		switch {
		case it.State == "done":
			ci.Tone = "green"
		case strings.HasPrefix(it.State, "declined"):
			ci.Tone = "red"
		}
		if it.Slug != "" {
			ci.URL = prefix + "/issue/" + it.Slug
		}
		return ci
	}
	for _, e := range connections.Select(all, f) {
		sec := WbConnSection{Row: connRow(p, prefix, e, colors)}
		for _, it := range e.Open {
			sec.Open = append(sec.Open, item(it))
		}
		for _, it := range e.Closed {
			sec.Closed = append(sec.Closed, item(it))
		}
		d.Sections = append(d.Sections, sec)
	}
	return d
}
