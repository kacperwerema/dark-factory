package main

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The crate store (L2, the hall's Issues tab) is the project's issue list:
// every issue as a crate, on shelves by status in workflow order, with the
// classic list's filters as pills and side bars. Done and parked shelves
// fold to the most recently changed crates. The classic list stays at
// ?classic=1 (handlers_list.go). No owner design yet: drafted from the
// design system and the decisions page (factory/issue-list-screen D-1).

// storeFolded is how many crates a done or parked shelf shows before "+N".
const storeFolded = 10

// storeFacetMax is how many rows a side facet lists (most crates first).
const storeFacetMax = 8

// Quick-pill values (?show=), on top of the classic filters.
const (
	storeShowOpen  = "open"
	storeShowYou   = "you"
	storeShowAgent = "agent"
)

// IssuesWbData is the whole crate store page.
type IssuesWbData struct {
	Header     WbHeader
	Count      WbStat
	Summary    WbSummaryBox
	Search     WbSearchBar
	Shelves    []WbShelf
	Total      int  // every issue in the project
	NoMatch    bool // issues exist, none passes the filters
	ClearURL   string
	ClassicURL string
	Facets     []WbBars
	Add        WbCommandCard
	NewURL     string
}

// WbShelf is one status's crates. More counts the crates folded away,
// MoreURL shows them all.
type WbShelf struct {
	Label, Status string
	Crates        []WbCrateCard
	More          int
	MoreURL       string
}

// WbCrateCard is one issue in the store: its station link, tags, worker and
// the hall's markers.
type WbCrateCard struct {
	Title, Slug, URL string
	Status           string
	Tags             []WbLink
	Priority         *WbChip
	Labels           []WbLink
	Robot            *WbRobot
	// You says what waits on the owner ("gate in progress", "question").
	You        string
	CI, YOLO   bool
	Score      *tracker.ScoreBreakdown
	Created    string
	ShowStatus bool // a flat list (sorted by score) names the status
}

// storeFilter is the page's query: the classic list's filters plus show.
type storeFilter struct {
	FilterParams
	Show, Sort string
}

func storeFilterFrom(q url.Values) storeFilter {
	return storeFilter{FilterParams: FilterParams{
		Status:   q.Get("status"),
		System:   q.Get("system"),
		Priority: q.Get("priority"),
		Label:    q.Get("label"),
		Assignee: q.Get("assignee"),
		Search:   q.Get("search"),
		Type:     q.Get("type"),
	}, Show: q.Get("show"), Sort: strings.TrimSpace(q.Get("sort"))}
}

func (f storeFilter) url(prefix string) string {
	v := url.Values{}
	for k, val := range map[string]string{"status": f.Status, "system": f.System, "priority": f.Priority, "label": f.Label,
		"assignee": f.Assignee, "search": f.Search, "type": f.Type, "show": f.Show, "sort": f.Sort} {
		if val != "" {
			v.Set(k, val)
		}
	}
	if len(v) > 0 {
		return prefix + "/?" + v.Encode()
	}
	return prefix + "/"
}

func (f storeFilter) any() bool {
	return f.Status != "" || f.System != "" || f.Priority != "" || f.Label != "" || f.Assignee != "" || f.Search != "" || f.Type != "" || f.Show != ""
}

// storeIssue is an issue with what the store knows about it.
type storeIssue struct {
	*tracker.Issue
	Type  string
	Robot *WbRobot
	You   string
	CI    bool
	YOLO  bool
}

func (s *Server) handleIssuesWb(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	wf := proj.LoadWorkflow()
	agents, _ := s.buildAgentsModel(proj, prefix)
	open := openOnly(issues)
	waiting := waitingOnOwner(proj, open)
	hc := newHallContext(proj, prefix, open, agents, waiting, now)
	you := map[string]string{}
	for _, it := range waiting {
		key := strings.ToLower(it.Issue.Slug)
		if you[key] == "" {
			you[key] = waitingWords(it)
		}
	}
	items := make([]storeIssue, 0, len(issues))
	for _, issue := range issues {
		key := strings.ToLower(issue.Slug)
		it := storeIssue{Issue: issue, Type: resolvedType(wf, issue), You: you[key], CI: hc.blamed[key], YOLO: hc.marked[key]}
		if wk, ok := hc.workerOf[key]; ok {
			rb := workerRobot(wk)
			it.Robot = &rb
		}
		items = append(items, it)
	}
	data := buildIssuesWb(items, wf, prefix, storeFilterFrom(r.URL.Query()))
	lot, queue := s.decisionsLot(proj, now)
	data.Header = WbHeader{WaitingURL: prefix + "/needs-you", Crumbs: wbCrumbs(2, proj.Name, prefix+"/company"), Tabs: wbHallTabs(prefix, "issues"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, Waiting: queue, HideDone: true}
	data.Header.Crumbs[1].Color = lot.Color
	data.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	data.Header.Crumbs[2] = WbCrumb{Label: "Product hall", Current: true, Color: companyProductColor}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "issues_wb.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// waitingWords is what an item asks of the owner, for a crate's chip.
func waitingWords(it WaitingItem) string {
	switch it.Kind {
	case "gate":
		return "gate · " + it.Target
	case "playtest":
		return "playtest to run"
	case "checks":
		return "checks to run"
	}
	return "question"
}

// match is the classic filters (filterIssues, filterByType) plus show.
func (f storeFilter) match(it storeIssue, wf *tracker.WorkflowConfig) bool {
	if len(filterIssues([]*tracker.Issue{it.Issue}, f.FilterParams)) == 0 {
		return false
	}
	if f.Type != "" && wf.HasTypes() && it.Type != f.Type {
		return false
	}
	switch f.Show {
	case storeShowOpen:
		return !storeFolds(it.Status)
	case storeShowYou:
		return it.You != ""
	case storeShowAgent:
		return it.Robot != nil
	}
	return true
}

// storeFolds reports whether a status's shelf folds (done, parked).
func storeFolds(status string) bool {
	k := statusKey(status)
	return k == "done" || k == "parked"
}

// buildIssuesWb lays out the store from items (most recently changed first).
func buildIssuesWb(items []storeIssue, wf *tracker.WorkflowConfig, prefix string, f storeFilter) IssuesWbData {
	scoring := wf.Scoring.Enabled
	if f.Sort == "" && scoring && wf.Scoring.DefaultSort == "score_desc" {
		f.Sort = "score"
	}
	byScore := f.Sort == "score" && scoring
	d := IssuesWbData{Total: len(items), ClearURL: prefix + "/", ClassicURL: prefix + "/?classic=1", NewURL: prefix + "/?classic=1&new=1"}

	var shown []storeIssue
	you, working := 0, 0
	for _, it := range items {
		if !f.match(it, wf) {
			continue
		}
		shown = append(shown, it)
		if it.You != "" {
			you++
		}
		if it.Robot != nil {
			working++
		}
	}
	d.NoMatch = len(items) > 0 && len(shown) == 0

	d.Count = WbStat{Value: fmt.Sprint(len(items)), Label: "issues"}
	if len(items) == 1 {
		d.Count.Label = "issue"
	}
	if len(shown) != len(items) {
		d.Count = WbStat{Value: fmt.Sprint(len(shown)), Label: fmt.Sprintf("of %d shown", len(items))}
	}
	switch {
	case you > 0:
		d.Summary = WbSummaryBox{Text: fmt.Sprintf("%s waiting on you · %s", plural(you, "crate", "crates"), plural(working, "agent at work", "agents at work")), Key: "issues"}
	default:
		d.Summary = WbSummaryBox{Text: fmt.Sprintf("Nothing here waits on you · %s", plural(working, "agent at work", "agents at work")), OK: true, Key: "issues"}
	}

	d.Search = storeSearch(prefix, f, wf)
	d.Facets = storeFacets(items, wf, prefix, f)
	d.Add = WbCommandCard{Title: "New issue", Text: "Ask the CEO, or create it from a shell. The form opens the classic create dialog.", Command: `issue-cli create "<title>"`, Key: "issues.create"}

	if byScore {
		views := make([]*IssueView, len(shown))
		at := map[*tracker.Issue]storeIssue{}
		for i, it := range shown {
			views[i] = &IssueView{Issue: it.Issue}
			at[it.Issue] = it
		}
		attachScores(views, &wf.Scoring)
		sortViewsByScore(views)
		shelf := WbShelf{Label: "By score", Status: "score"}
		for _, v := range views {
			c := storeCrate(at[v.Issue], prefix, f)
			c.ShowStatus = true
			c.Score = v.Score
			shelf.Crates = append(shelf.Crates, c)
		}
		if len(shelf.Crates) > 0 {
			d.Shelves = []WbShelf{shelf}
		}
		return d
	}

	// Shelves in workflow order; statuses the workflow doesn't know follow.
	byStatus := map[string][]storeIssue{}
	for _, it := range shown {
		byStatus[statusKey(it.Status)] = append(byStatus[statusKey(it.Status)], it)
	}
	var order []string
	names := map[string]string{}
	for _, st := range wf.Statuses {
		k := statusKey(st.Name)
		if _, ok := names[k]; !ok {
			order, names[k] = append(order, k), st.Name
		}
	}
	var unknown []string
	for k, list := range byStatus {
		if _, ok := names[k]; !ok {
			unknown = append(unknown, k)
			names[k] = list[0].Status
			if names[k] == "" {
				names[k] = "no status"
			}
		}
	}
	sort.Strings(unknown)
	order = append(order, unknown...)
	// Folded shelves go last, in workflow order.
	sort.SliceStable(order, func(i, j int) bool { return !storeFolds(order[i]) && storeFolds(order[j]) })

	for _, k := range order {
		list := byStatus[k]
		if len(list) == 0 {
			continue
		}
		sort.SliceStable(list, func(i, j int) bool { return storeRank(list[i]) < storeRank(list[j]) })
		shelf := WbShelf{Label: fmt.Sprintf("%s · %d", names[k], len(list)), Status: names[k]}
		for i, it := range list {
			if storeFolds(k) && f.Status == "" && i == storeFolded {
				shelf.More = len(list) - storeFolded
				g := f
				g.Status, g.Show = it.Status, ""
				shelf.MoreURL = g.url(prefix)
				break
			}
			shelf.Crates = append(shelf.Crates, storeCrate(it, prefix, f))
		}
		d.Shelves = append(d.Shelves, shelf)
	}
	return d
}

// storeRank orders a shelf: waiting on you, an agent at work, the rest
// (each most recently changed first, as loaded).
func storeRank(it storeIssue) int {
	switch {
	case it.You != "":
		return 0
	case it.Robot != nil:
		return 1
	}
	return 2
}

// storeCrate is one issue as a crate card; tags link to their filter.
func storeCrate(it storeIssue, prefix string, f storeFilter) WbCrateCard {
	c := WbCrateCard{Title: it.Title, Slug: it.Slug, URL: prefix + "/issue/" + it.Slug, Status: it.Status, Robot: it.Robot, You: it.You, CI: it.CI, YOLO: it.YOLO, Created: it.Created}
	if c.Title == "" {
		c.Title = it.Slug
	}
	tag := func(label string, set func(*storeFilter)) WbLink {
		g := f
		set(&g)
		return WbLink{Label: label, URL: g.url(prefix)}
	}
	if it.System != "" {
		c.Tags = append(c.Tags, tag(it.System, func(g *storeFilter) { g.System = it.System }))
	}
	if it.Type != "" {
		c.Tags = append(c.Tags, tag(it.Type, func(g *storeFilter) { g.Type = it.Type }))
	}
	if it.Version != "" {
		c.Tags = append(c.Tags, WbLink{Label: "v" + it.Version})
	}
	if it.Priority != "" {
		tone := "idle"
		switch strings.ToLower(it.Priority) {
		case "critical", "high":
			tone = "red"
		}
		c.Priority = &WbChip{Tone: tone, Text: it.Priority, Title: "Priority", Dot: true}
	}
	for _, l := range it.Labels {
		c.Labels = append(c.Labels, tag(l, func(g *storeFilter) { g.Label = l }))
	}
	return c
}

// storeSearch is the search box with the quick pills and a removable pill
// per applied filter.
func storeSearch(prefix string, f storeFilter, wf *tracker.WorkflowConfig) WbSearchBar {
	sb := WbSearchBar{Action: prefix + "/", Name: "search", Value: f.Search, Placeholder: "title, slug or body", Key: "issues"}
	for _, k := range []struct{ name, val string }{{"status", f.Status}, {"system", f.System}, {"priority", f.Priority}, {"label", f.Label},
		{"assignee", f.Assignee}, {"type", f.Type}, {"show", f.Show}, {"sort", f.Sort}} {
		if k.val != "" {
			sb.Keep = append(sb.Keep, WbHiddenField{Name: k.name, Value: k.val})
		}
	}
	for _, p := range []struct{ show, label, tone string }{
		{"", "All", ""},
		{storeShowOpen, "Open", ""},
		{storeShowYou, "Waiting on you", "you"},
		{storeShowAgent, "Agent at work", ""},
	} {
		g := f
		g.Show = p.show
		key := p.show
		if key == "" {
			key = "all"
		}
		sb.Filters = append(sb.Filters, WbFilterPill{Label: p.label, URL: g.url(prefix), Tone: p.tone, On: f.Show == p.show, Key: "show_" + key})
	}
	if wf.Scoring.Enabled {
		g := f
		label := "Sort by score"
		if f.Sort == "score" {
			g.Sort, label = "", "Sorted by score"
		} else {
			g.Sort = "score"
		}
		sb.Filters = append(sb.Filters, WbFilterPill{Label: label, URL: g.url(prefix), On: f.Sort == "score", Key: "sort_score"})
	}
	for _, rm := range []struct {
		key, label, val string
		clear           func(*storeFilter)
	}{
		{"status", "Status", f.Status, func(g *storeFilter) { g.Status = "" }},
		{"system", "System", f.System, func(g *storeFilter) { g.System = "" }},
		{"priority", "Priority", f.Priority, func(g *storeFilter) { g.Priority = "" }},
		{"type", "Type", f.Type, func(g *storeFilter) { g.Type = "" }},
		{"assignee", "Assignee", assigneeWords(f.Assignee), func(g *storeFilter) { g.Assignee = "" }},
		{"label", "Label", f.Label, func(g *storeFilter) { g.Label = "" }},
		{"search", "Search", f.Search, func(g *storeFilter) { g.Search = "" }},
	} {
		if rm.val == "" {
			continue
		}
		g := f
		rm.clear(&g)
		sb.Filters = append(sb.Filters, WbFilterPill{Label: rm.label + ": " + rm.val, URL: g.url(prefix), Remove: true, Key: "clear_" + rm.key})
	}
	return sb
}

// assigneeWords names the assignee filter's special values.
func assigneeWords(a string) string {
	switch a {
	case "_claimed":
		return "claimed"
	case "_unclaimed":
		return "unclaimed"
	}
	return a
}

// storeFacets is the side bars: per field, how many crates pass the other
// filters with each value; a bar applies its value.
func storeFacets(items []storeIssue, wf *tracker.WorkflowConfig, prefix string, f storeFilter) []WbBars {
	type facet struct {
		title, key, empty string
		values            func(storeIssue) []string
		cur               string
		set               func(*storeFilter, string)
		all               bool // list every value, not the top storeFacetMax
	}
	facets := []facet{
		{"By system", "system", "No issue has a system.", func(it storeIssue) []string { return nonEmpty(it.System) }, f.System, func(g *storeFilter, v string) { g.System = v }, false},
		{"By priority", "priority", "No issue has a priority.", func(it storeIssue) []string { return nonEmpty(it.Priority) }, f.Priority, func(g *storeFilter, v string) { g.Priority = v }, true},
	}
	if wf.HasTypes() {
		facets = append(facets, facet{"By type", "type", "No types.", func(it storeIssue) []string { return nonEmpty(it.Type) }, f.Type, func(g *storeFilter, v string) { g.Type = v }, true})
	}
	facets = append(facets,
		facet{"By assignee", "assignee", "Nobody has claimed an issue.", func(it storeIssue) []string { return nonEmpty(it.Assignee) }, f.Assignee, func(g *storeFilter, v string) { g.Assignee = v }, false},
		facet{"Labels", "label", "No issue has a label.", func(it storeIssue) []string { return it.Labels }, f.Label, func(g *storeFilter, v string) { g.Label = v }, false},
	)
	var out []WbBars
	for _, fc := range facets {
		others := f
		fc.set(&others, "")
		counts := map[string]int{}
		for _, it := range items {
			if !others.match(it, wf) {
				continue
			}
			seen := map[string]bool{}
			for _, v := range fc.values(it) {
				if !seen[strings.ToLower(v)] {
					seen[strings.ToLower(v)] = true
					counts[v]++
				}
			}
		}
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if counts[keys[i]] != counts[keys[j]] {
				return counts[keys[i]] > counts[keys[j]]
			}
			return keys[i] < keys[j]
		})
		if !fc.all && len(keys) > storeFacetMax {
			// Keep the applied value listed even when it isn't in the top rows.
			top := keys[:storeFacetMax]
			for _, k := range keys[storeFacetMax:] {
				if strings.EqualFold(k, fc.cur) {
					top = append(top, k)
				}
			}
			keys = top
		}
		b := WbBars{Title: fc.title, Empty: fc.empty, Key: "issues_" + fc.key}
		if others.any() {
			b.Empty = "None among the issues these filters show."
		}
		most := 0
		for _, k := range keys {
			most = max(most, counts[k])
		}
		for _, k := range keys {
			g := f
			on := strings.EqualFold(k, fc.cur)
			if on {
				fc.set(&g, "")
			} else {
				fc.set(&g, k)
			}
			b.Rows = append(b.Rows, WbCountBar{Label: k, URL: g.url(prefix), Count: counts[k], Pct: counts[k] * 100 / most, On: on})
		}
		if fc.key == "assignee" {
			for _, sp := range []struct{ val, label string }{{"_claimed", "Claimed"}, {"_unclaimed", "Unclaimed"}} {
				n := 0
				for _, it := range items {
					if others.match(it, wf) && (it.Assignee != "") == (sp.val == "_claimed") {
						n++
					}
				}
				g := f
				on := fc.cur == sp.val
				if on {
					g.Assignee = ""
				} else {
					g.Assignee = sp.val
				}
				pct := 0
				if m := max(most, n); m > 0 {
					pct = n * 100 / m
				}
				b.Rows = append(b.Rows, WbCountBar{Label: sp.label, URL: g.url(prefix), Count: n, Pct: pct, On: on})
			}
		}
		out = append(out, b)
	}
	return out
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}
