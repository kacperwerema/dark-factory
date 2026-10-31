package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/connections"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Roads on the company floor: one road per pair of boxes that pass work,
// whatever kinds of traffic it carries (the Connections block's rows folded
// by pair), with the traffic as parcels static/connections.js moves along
// it. A request the CEO or a group lead relayed travels two roads, through
// the relay's box. See ui/roads-between-departments-the-map-shows-roads-
// with-work-moving-on-them-instead-of-dependency-lines.

// Parcel kinds: the three edge kinds, with a request that is a handoff
// (issue-cli handoff) as its own kind.
const parcelHandoff = "handoff"

// WbRoad is one road between boxes A and B (A first in Boxes order).
type WbRoad struct {
	A     string `json:"a"`
	B     string `json:"b"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Label string `json:"label"`
	// Route: "undeclared" when any traffic on it is off the declared
	// routes, "quiet" when it is only declared routes with no traffic, ""
	// otherwise.
	Route   string       `json:"route,omitempty"`
	Open    int          `json:"open"`
	Traffic []WbRoadFlow `json:"traffic,omitempty"`
	// Rows are the Connections block's rows (by index) the road carries,
	// so hovering a row lights its road.
	Rows []int `json:"rows"`
}

// WbRoadFlow is the open traffic of one kind in one direction: Dir "ab"
// travels from A to B. Color is the sending box's.
type WbRoadFlow struct {
	Dir   string `json:"dir"`
	Kind  string `json:"kind"`
	N     int    `json:"n"`
	Color string `json:"color"`
}

// roadLegs are the pairs an edge travels: straight, or through its relay
// unless the relay is a group the sender or the receiver sits in (a line
// through it would loop out and back).
func roadLegs(p *tracker.Project, e connections.Edge) [][2]string {
	v := e.Via
	if v == "" || v == e.From || v == e.To || connections.UnderGroup(p, e.From, v) || connections.UnderGroup(p, e.To, v) {
		return [][2]string{{e.From, e.To}}
	}
	return [][2]string{{e.From, v}, {v, e.To}}
}

// companyRoads folds the edges into roads, in the order their first edge
// comes.
func companyRoads(p *tracker.Project, prefix string, edges []connections.Edge, colors map[string]string) []WbRoad {
	order := map[string]int{}
	for i, b := range connections.Boxes(p) {
		order[b] = i
	}
	rank := func(b string) int {
		if i, ok := order[b]; ok {
			return i
		}
		return len(order)
	}
	var roads []WbRoad
	at := map[[2]string]int{}
	// fwd counts open items per direction, for the road's link.
	fwd := map[[2]string]int{}
	quietOnly := map[[2]string]bool{}
	for i, e := range edges {
		color := colors[e.From]
		if color == "" {
			color = "#8A93A6"
		}
		for _, leg := range roadLegs(p, e) {
			a, b := leg[0], leg[1]
			if a == b {
				continue
			}
			dir := "ab"
			if rank(b) < rank(a) || (rank(b) == rank(a) && b < a) {
				a, b, dir = b, a, "ba"
			}
			key := [2]string{a, b}
			j, ok := at[key]
			if !ok {
				j = len(roads)
				at[key] = j
				quietOnly[key] = true
				roads = append(roads, WbRoad{A: a, B: b, Name: connections.BoxName(p, a) + " ⇄ " + connections.BoxName(p, b)})
			}
			r := &roads[j]
			if !slices.Contains(r.Rows, i) {
				r.Rows = append(r.Rows, i)
			}
			if e.Route != connections.RouteUnused {
				quietOnly[key] = false
			}
			if e.Route == connections.RouteUndeclared {
				r.Route = connections.RouteUndeclared
			}
			fwd[[2]string{leg[0], leg[1]}] += len(e.Open)
			handoffs := 0
			for _, it := range e.Open {
				if it.State != "" {
					handoffs++
				}
			}
			addFlow(r, dir, e.Kind, len(e.Open)-handoffs, color)
			addFlow(r, dir, parcelHandoff, handoffs, color)
		}
	}
	for k, j := range at {
		r := &roads[j]
		if quietOnly[k] && r.Route == "" {
			r.Route = "quiet"
		}
		from, to := r.A, r.B
		if fwd[[2]string{r.B, r.A}] > fwd[[2]string{r.A, r.B}] {
			from, to = r.B, r.A
		}
		r.URL = connURL(prefix, from, to, "")
		r.Label = roadLabel(r)
	}
	return roads
}

// addFlow adds n open items of kind in dir to the road.
func addFlow(r *WbRoad, dir, kind string, n int, color string) {
	if n <= 0 {
		return
	}
	r.Open += n
	for i := range r.Traffic {
		if f := &r.Traffic[i]; f.Dir == dir && f.Kind == kind {
			f.N += n
			return
		}
	}
	r.Traffic = append(r.Traffic, WbRoadFlow{Dir: dir, Kind: kind, N: n, Color: color})
}

// parcelOrder is the order the label lists kinds in.
var parcelOrder = []string{connections.Request, parcelHandoff, connections.Output, connections.Dependency}

// roadLabel is "Marketing ⇄ Product: 2 requests, 1 handoff open · not
// declared", or "… : declared, no traffic yet" for a quiet road.
func roadLabel(r *WbRoad) string {
	if r.Route == "quiet" {
		return r.Name + ": declared, no traffic yet"
	}
	var parts []string
	for _, k := range parcelOrder {
		n := 0
		for _, f := range r.Traffic {
			if f.Kind == k {
				n += f.N
			}
		}
		if n == 0 {
			continue
		}
		word := k
		if k == parcelHandoff {
			word = "handoff"
		}
		if k == connections.Dependency {
			parts = append(parts, connections.CountWord(n, k))
			continue
		}
		if n != 1 {
			word += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, word))
	}
	s := r.Name + ": "
	if len(parts) == 0 {
		s += "nothing open"
	} else {
		s += strings.Join(parts, ", ") + " open"
	}
	if r.Route == connections.RouteUndeclared {
		s += " · " + connections.RouteWord(r.Route)
	}
	return s
}
