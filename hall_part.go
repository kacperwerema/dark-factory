package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// A part of the hall's line, opened: a station (/hall/station/<status>), a
// gate (/hall/gate/<status>) or the CI scanner (/hall/ci). It shows what is
// inside: every issue there, the status prompt, the transitions in and out
// with their actions, the overlays that change them, what waits at a gate.
// Read-only; editing the workflow from here is
// factory/hall-edit-the-workflow-from-the-hall.

// HallPartData is the whole drill-in page.
type HallPartData struct {
	Header WbHeader
	// Kind is station, gate or ci; Status the status the station or gate is.
	Kind, Status string
	HallURL      string
	Title, Sub   string
	// Station is the status with every issue in it (no hallCrates cap).
	Station     *WbHallStation
	Description string
	Prompt      string
	// In and Out are the transitions into and out of the status (a gate:
	// the transitions it stands on, in In).
	In, Out  []WbPartEdge
	Overlays []WbPartOverlay
	// Waiting is a gate's issues waiting on the owner; Raised: YOLO is on
	// and the barrier is up.
	Waiting []WbPartWaiting
	Raised  bool
	CI      *WbPartCI
}

// WbPartEdge is one transition. Any: from "*" (any status); Gate: it needs
// the owner's approval; Implied: no transitions: entry, the lifecycle's next
// step; Replace: an overlay edge that overwrites the base edge.
type WbPartEdge struct {
	From, To, URL               string
	Any, Gate, Implied, Replace bool
	Actions                     []string
}

// WbPartOverlay is a type or a system that changes the status or its edges.
type WbPartOverlay struct {
	Kind, Name  string
	Skips       bool
	Description string
	Prompt      string
	Edges       []WbPartEdge
}

// WbPartWaiting is an issue waiting at a gate.
type WbPartWaiting struct {
	Slug, Title, URL, Since string
	Approx                  bool
}

// WbPartCI is the CI scanner opened.
type WbPartCI struct {
	Red                         bool
	State, Branch, Failed       string
	GreenSha, RedSince, Updated string
	Commands                    []WbFact
	Failures, LogTail           []string
	Merged                      []WbFact
	Blamed                      []WbParked
	StatusURL                   string
}

// hallPartKey is a status in a drill-in URL: lower case, spaces as dashes.
func hallPartKey(status string) string {
	return strings.ReplaceAll(statusKey(status), " ", "-")
}

// hallPartURL is the drill-in page of a station or gate on the status.
func hallPartURL(prefix, kind, status string) string {
	return prefix + "/hall/" + kind + "/" + hallPartKey(status)
}

func (s *Server) handleHallPart(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix, kind, key string) {
	data, ok := s.buildHallPart(proj, prefix, kind, key, time.Now())
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "hall_part.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildHallPart builds the page; false when there is no such part: an
// unknown status, a status no gate stands before, or no ci: block.
func (s *Server) buildHallPart(p *tracker.Project, prefix, kind, key string, now time.Time) (HallPartData, bool) {
	wf := p.LoadWorkflow()
	var status *tracker.WorkflowStatus
	for i := range wf.Statuses {
		if hallPartKey(wf.Statuses[i].Name) == key {
			status = &wf.Statuses[i]
		}
	}
	if kind != "ci" && status == nil {
		return HallPartData{}, false
	}

	company := s.buildCompany(p, prefix, now)
	lot := company.Lot
	d := HallPartData{Kind: kind, HallURL: prefix + "/hall"}
	d.Header = WbHeader{Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek, Waiting: company.Waiting, HideDone: true, Tabs: wbHallTabs(prefix, "line")}
	d.Header.Crumbs = append(wbCrumbs(1, p.Name, prefix+"/company"), WbCrumb{Current: true})
	d.Header.Crumbs[1] = WbCrumb{Label: p.Name, URL: prefix + "/company", Color: lot.Color}
	d.Header.Crumbs[2] = WbCrumb{Label: "Product hall", URL: d.HallURL, Color: companyProductColor}
	d.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	if c := wbCIChip(lot.CI, lot.CILine); c != nil {
		d.Header.Chips = append(d.Header.Chips, *c)
	}

	issues, _ := tracker.LoadIssues(p.IssueDir)
	sort.Slice(issues, func(i, j int) bool { return issues[i].Slug < issues[j].Slug })
	issues = productIssues(p, issues)
	open := openOnly(issues)
	agents, _ := s.buildAgentsModel(p, prefix)
	waiting := waitingOnOwner(p, open)
	hc := newHallContext(p, prefix, open, agents, waiting, now)

	switch kind {
	case "station":
		d.Status = status.Name
		d.Title = hallPartStationName(wf, status.Name)
		d.Sub = "station · what happens while an issue is in this status"
		d.Description, d.Prompt = status.Description, strings.TrimSpace(status.Prompt)
		st := &WbHallStation{Name: d.Title, Status: status.Name, Who: status.Description}
		// Done is the only status the hall's open issues leave; read it
		// from every issue.
		here := hc.byStatus[statusKey(status.Name)]
		if statusKey(status.Name) == "done" {
			for _, issue := range issues {
				if statusKey(issue.Status) == "done" {
					here = append(here, issue)
				}
			}
		}
		for _, issue := range hc.sorted(here) {
			c := hc.crate(issue, "")
			c.URL = prefix + "/issue/" + issue.Slug
			st.Crates = append(st.Crates, c)
		}
		st.Count = len(st.Crates)
		d.Station = st
		d.In, d.Out = hallPartEdges(prefix, wf, status.Name)
		d.Overlays = hallPartOverlays(prefix, wf, status.Name, false)
	case "gate":
		gates := hallGateEdges(prefix, wf, status.Name)
		if len(gates) == 0 {
			return HallPartData{}, false
		}
		d.Status = status.Name
		d.Title = "Gate before " + status.Name
		d.Sub = "your approval to enter " + status.Name
		d.In = gates
		d.Overlays = hallPartOverlays(prefix, wf, status.Name, true)
		d.Raised = lot.YOLO != nil && lot.YOLO.On
		for _, it := range waiting {
			if it.Kind != "gate" || statusKey(it.Target) != statusKey(status.Name) {
				continue
			}
			pw := WbPartWaiting{Slug: it.Issue.Slug, Title: it.Issue.Title, URL: prefix + "/issue/" + it.Issue.Slug, Approx: it.Approx}
			if !it.Since.IsZero() {
				pw.Since = shortDuration(now.Sub(it.Since))
			}
			d.Waiting = append(d.Waiting, pw)
		}
	case "ci":
		if !wf.CI.Enabled() {
			return HallPartData{}, false
		}
		d.Title = "CI scanner"
		d.Sub = "local CI on the integration branch, after every merge"
		d.CI = hallPartCI(prefix, wf, hc)
	default:
		return HallPartData{}, false
	}
	d.Header.Crumbs[3].Label = d.Title
	return d, true
}

// hallPartStationName is the station's sign, as the hall draws it.
func hallPartStationName(wf *tracker.WorkflowConfig, status string) string {
	for _, stop := range factoryLine(wf) {
		if strings.EqualFold(stop.Name, status) {
			return stationName(stop)
		}
	}
	return stationName(lineStop{Name: status})
}

// hallPartEdges is the transitions into and out of the status. A step of the
// lifecycle with no transitions: entry is listed as implied (no actions).
func hallPartEdges(prefix string, wf *tracker.WorkflowConfig, status string) (in, out []WbPartEdge) {
	k := statusKey(status)
	for _, t := range wf.Transitions {
		switch {
		case statusKey(t.To) == k:
			in = append(in, partEdge(prefix, t, t.From))
		case statusKey(t.From) == k || t.From == "*":
			out = append(out, partEdge(prefix, t, t.To))
		}
	}
	// The neighbours on the lifecycle, when no entry covers the step.
	for i, st := range wf.Statuses {
		if statusKey(st.Name) != k {
			continue
		}
		if i > 0 && !hasEdge(in, wf.Statuses[i-1].Name, status) {
			prev := wf.Statuses[i-1].Name
			in = append([]WbPartEdge{{From: prev, To: status, URL: hallPartURL(prefix, "station", prev), Implied: true}}, in...)
		}
		if i+1 < len(wf.Statuses) && !hasEdge(out, status, wf.Statuses[i+1].Name) {
			next := wf.Statuses[i+1].Name
			out = append([]WbPartEdge{{From: status, To: next, URL: hallPartURL(prefix, "station", next), Implied: true}}, out...)
		}
	}
	return in, out
}

func hasEdge(edges []WbPartEdge, from, to string) bool {
	for _, e := range edges {
		if statusKey(e.From) == statusKey(from) && statusKey(e.To) == statusKey(to) {
			return true
		}
	}
	return false
}

// hallGateEdges is the transitions that need the owner's approval to enter
// the status.
func hallGateEdges(prefix string, wf *tracker.WorkflowConfig, status string) []WbPartEdge {
	var out []WbPartEdge
	for _, t := range wf.Transitions {
		if transitionGates(t, status) {
			out = append(out, partEdge(prefix, t, t.From))
		}
	}
	return out
}

// transitionGates reports whether the transition holds an approval for the
// status.
func transitionGates(t tracker.WorkflowTransition, status string) bool {
	for _, a := range t.Actions {
		if a.Type != "require_human_approval" {
			continue
		}
		target := a.Status
		if target == "" {
			target = t.To
		}
		if statusKey(target) == statusKey(status) {
			return true
		}
	}
	return false
}

// partEdge is a transition as an edge; other is the status at its far end,
// which the edge links to.
func partEdge(prefix string, t tracker.WorkflowTransition, other string) WbPartEdge {
	e := WbPartEdge{From: t.From, To: t.To, Any: t.From == "*", Replace: t.Replace}
	if other != "*" {
		e.URL = hallPartURL(prefix, "station", other)
	}
	for _, a := range t.Actions {
		if a.Type == "require_human_approval" {
			e.Gate = true
		}
		e.Actions = append(e.Actions, actionWords(a))
	}
	for _, f := range t.Fields {
		e.Actions = append(e.Actions, "asks for "+f.Name+" before it commits")
	}
	return e
}

// actionWords is one transition action in words, as the owner reads it.
func actionWords(a tracker.WorkflowAction) string {
	switch a.Type {
	case "validate":
		rule := a.Rule
		if a.Field != "" {
			rule += " " + a.Field
		}
		return "checks: " + strings.TrimSpace(rule)
	case "require_human_approval":
		if a.Status != "" {
			return "needs your approval for " + a.Status
		}
		return "needs your approval"
	case "append_section":
		return "adds the section ## " + a.Title
	case "inject_prompt":
		return "gives the agent: " + clipRunes(strings.Join(strings.Fields(a.Prompt), " "), 160)
	case "set_fields":
		if a.Value == "" {
			return "clears " + a.Field
		}
		return fmt.Sprintf("sets %s to %s", a.Field, a.Value)
	}
	if a.Field != "" {
		return a.Type + " " + a.Field
	}
	return a.Type
}

// hallPartOverlays is every type and system that changes the status: its
// prompt or description, an edge into or out of it, or (types) a path that
// skips it. gate: only edges that gate the status count.
func hallPartOverlays(prefix string, wf *tracker.WorkflowConfig, status string, gate bool) []WbPartOverlay {
	k := statusKey(status)
	read := func(kind, name string, statuses []tracker.WorkflowStatus, transitions []tracker.WorkflowTransition) (WbPartOverlay, bool) {
		o := WbPartOverlay{Kind: kind, Name: name}
		for _, st := range statuses {
			if statusKey(st.Name) == k && !gate {
				o.Description, o.Prompt = st.Description, strings.TrimSpace(st.Prompt)
			}
		}
		for _, t := range transitions {
			switch {
			case gate && !transitionGates(t, status):
			case gate || statusKey(t.To) == k:
				o.Edges = append(o.Edges, partEdge(prefix, t, t.From))
			case statusKey(t.From) == k || t.From == "*":
				o.Edges = append(o.Edges, partEdge(prefix, t, t.To))
			}
		}
		return o, o.Description != "" || o.Prompt != "" || len(o.Edges) > 0
	}
	var out []WbPartOverlay
	var names []string
	for name := range wf.Types {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t := wf.Types[name]
		o, ok := read("type", name, t.Statuses, t.Transitions)
		if len(t.Path) > 0 && !gate {
			o.Skips = true
			for _, st := range t.Path {
				if statusKey(st) == k {
					o.Skips = false
				}
			}
		}
		if ok || o.Skips {
			out = append(out, o)
		}
	}
	names = names[:0]
	for name := range wf.Systems {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sys := wf.Systems[name]
		if o, ok := read("system", name, sys.Statuses, sys.Transitions); ok {
			out = append(out, o)
		}
	}
	return out
}

// hallPartCI is the CI scanner opened: its commands, its state and what
// landed since the last green run.
func hallPartCI(prefix string, wf *tracker.WorkflowConfig, hc *hallContext) *WbPartCI {
	st := hc.ci
	if st == nil {
		st = &ci.State{}
	}
	c := &WbPartCI{Red: st.IsRed(), State: st.State, Branch: st.Branch, Failed: st.Failed, Failures: st.Failures, LogTail: st.LogTail, StatusURL: prefix + "/status"}
	if c.State == "" {
		c.State = "no run yet"
	}
	if c.Branch == "" {
		c.Branch = wf.CI.Branch
	}
	c.GreenSha, c.RedSince = shortSha(st.GreenSha), shortSha(st.RedSince)
	if !st.Updated.IsZero() {
		c.Updated = shortDuration(hc.now.Sub(st.Updated)) + " ago"
	}
	for _, cmd := range wf.CI.Run {
		tier := cmd.Tier
		if tier == "" {
			tier = "default"
		}
		c.Commands = append(c.Commands, WbFact{Label: tier, Value: cmd.Cmd, Mono: true})
	}
	for _, m := range st.Merged {
		label := m.Slug
		if label == "" {
			label = m.Branch
		}
		c.Merged = append(c.Merged, WbFact{Label: shortSha(m.Sha), Value: strings.TrimSpace(label + " " + m.Subject)})
	}
	for slug := range hc.blamed {
		c.Blamed = append(c.Blamed, WbParked{Slug: slug, URL: prefix + "/issue/" + slug})
	}
	sort.Slice(c.Blamed, func(i, j int) bool { return c.Blamed[i].Slug < c.Blamed[j].Slug })
	return c
}

func shortSha(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
