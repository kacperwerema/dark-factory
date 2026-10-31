package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func storeWorkflow() *tracker.WorkflowConfig {
	return &tracker.WorkflowConfig{Statuses: []tracker.WorkflowStatus{{Name: "idea"}, {Name: "backlog"}, {Name: "in progress"}, {Name: "done"}, {Name: "parked"}}}
}

func storeItem(slug, status, system, priority string, labels ...string) storeIssue {
	return storeIssue{Issue: &tracker.Issue{Slug: slug, Title: slug, Status: status, System: system, Priority: priority, Labels: labels}}
}

func shelfLabels(d IssuesWbData) []string {
	var out []string
	for _, s := range d.Shelves {
		out = append(out, s.Label)
	}
	return out
}

func crateSlugs(d IssuesWbData) []string {
	var out []string
	for _, s := range d.Shelves {
		for _, c := range s.Crates {
			out = append(out, c.Slug)
		}
	}
	return out
}

// Proves: AC2 — shelves follow the workflow, unknown statuses after it, done
// and parked last; waiting on you and agents at work lead a shelf.
func TestCrateStoreShelvesInWorkflowOrder(t *testing.T) {
	agent := storeItem("b-agent", "backlog", "UI", "")
	agent.Robot = &WbRobot{State: "working", Label: "b-agent"}
	you := storeItem("b-you", "backlog", "UI", "")
	you.You = "gate · in progress"
	items := []storeIssue{
		storeItem("d-1", "done", "UI", ""),
		storeItem("b-plain", "backlog", "UI", ""),
		agent, you,
		storeItem("weird", "review", "", ""),
		storeItem("i-1", "idea", "CLI", "high", "bug"),
		storeItem("p-1", "parked", "", ""),
	}
	d := buildIssuesWb(items, storeWorkflow(), "/p/x", storeFilter{})
	if got, want := strings.Join(shelfLabels(d), " | "), "idea · 1 | backlog · 3 | review · 1 | done · 1 | parked · 1"; got != want {
		t.Errorf("shelves = %q, want %q", got, want)
	}
	if got, want := strings.Join(crateSlugs(d), " "), "i-1 b-you b-agent b-plain weird d-1 p-1"; got != want {
		t.Errorf("crates = %q, want %q", got, want)
	}
	c := d.Shelves[0].Crates[0]
	if c.URL != "/p/x/issue/i-1" || c.Priority == nil || c.Priority.Tone != "red" || len(c.Labels) != 1 || c.Labels[0].URL != "/p/x/?label=bug" {
		t.Errorf("crate i-1 = %+v", c)
	}
	if len(c.Tags) != 1 || c.Tags[0] != (WbLink{Label: "CLI", URL: "/p/x/?system=CLI"}) {
		t.Errorf("tags = %+v", c.Tags)
	}
	if d.Count.Value != "7" || d.Count.Label != "issues" {
		t.Errorf("count = %+v", d.Count)
	}
	if d.Summary.OK || d.Summary.Text != "1 crate waiting on you · 1 agent at work" {
		t.Errorf("summary = %+v", d.Summary)
	}
}

// Proves: AC5 — a done shelf shows the 10 most recently changed crates and
// links the rest; filtering to done shows them all.
func TestCrateStoreFoldsDone(t *testing.T) {
	var items []storeIssue
	for i := 0; i < 13; i++ {
		items = append(items, storeItem(fmt.Sprintf("done-%02d", i), "done", "", ""))
	}
	items = append(items, storeItem("open", "idea", "", ""))
	d := buildIssuesWb(items, storeWorkflow(), "/p/x", storeFilter{})
	done := d.Shelves[1]
	if len(done.Crates) != storeFolded || done.More != 3 || done.MoreURL != "/p/x/?status=done" || done.Crates[0].Slug != "done-00" {
		t.Errorf("done shelf = %d crates, more %d at %q, first %q", len(done.Crates), done.More, done.MoreURL, done.Crates[0].Slug)
	}
	d = buildIssuesWb(items, storeWorkflow(), "/p/x", storeFilter{FilterParams: FilterParams{Status: "done"}})
	if len(d.Shelves) != 1 || len(d.Shelves[0].Crates) != 13 || d.Shelves[0].More != 0 {
		t.Errorf("?status=done shelves = %+v", shelfLabels(d))
	}
	if d.Count.Value != "13" || d.Count.Label != "of 14 shown" {
		t.Errorf("count = %+v", d.Count)
	}
}

// Proves: AC3 — the classic filters and the quick pills narrow the store,
// each applied filter is a pill that removes it.
func TestCrateStoreFilters(t *testing.T) {
	agent := storeItem("a", "in progress", "UI", "high")
	agent.Robot = &WbRobot{State: "working"}
	agent.Assignee = "agent-a"
	you := storeItem("y", "backlog", "CLI", "low", "bug")
	you.You = "question"
	items := []storeIssue{agent, you, storeItem("d", "done", "UI", "low"), storeItem("i", "idea", "UI", "", "bug")}
	wf := storeWorkflow()
	cases := []struct {
		f    storeFilter
		want string
	}{
		{storeFilter{Show: storeShowOpen}, "i y a"},
		{storeFilter{Show: storeShowYou}, "y"},
		{storeFilter{Show: storeShowAgent}, "a"},
		{storeFilter{FilterParams: FilterParams{System: "ui"}}, "i a d"},
		{storeFilter{FilterParams: FilterParams{Label: "bug", Priority: "low"}}, "y"},
		{storeFilter{FilterParams: FilterParams{Assignee: "_claimed"}}, "a"},
		{storeFilter{FilterParams: FilterParams{Assignee: "_unclaimed", Status: "idea"}}, "i"},
		{storeFilter{FilterParams: FilterParams{Search: "y"}}, "y"},
	}
	for _, c := range cases {
		d := buildIssuesWb(items, wf, "/p/x", c.f)
		if got := strings.Join(crateSlugs(d), " "); got != c.want {
			t.Errorf("%+v: crates %q, want %q", c.f, got, c.want)
		}
	}

	d := buildIssuesWb(items, wf, "/p/x", storeFilter{FilterParams: FilterParams{System: "UI", Assignee: "_claimed"}, Show: storeShowOpen})
	pills := map[string]WbFilterPill{}
	for _, p := range d.Search.Filters {
		pills[p.Key] = p
	}
	if p := pills["show_open"]; !p.On {
		t.Error("Open pill not on")
	}
	if p := pills["show_you"]; p.URL != "/p/x/?assignee=_claimed&show=you&system=UI" || p.Tone != "you" {
		t.Errorf("you pill = %+v", p)
	}
	if p := pills["clear_system"]; !p.Remove || p.URL != "/p/x/?assignee=_claimed&show=open" || p.Label != "System: UI" {
		t.Errorf("clear system = %+v", p)
	}
	if p := pills["clear_assignee"]; p.Label != "Assignee: claimed" {
		t.Errorf("clear assignee = %+v", p)
	}
	if _, ok := pills["sort_score"]; ok {
		t.Error("score pill without scoring")
	}
	keep := map[string]string{}
	for _, k := range d.Search.Keep {
		keep[k.Name] = k.Value
	}
	if keep["system"] != "UI" || keep["show"] != "open" || keep["assignee"] != "_claimed" {
		t.Errorf("search keeps %v", keep)
	}
}

// Proves: AC4 — a facet counts what passes the other filters, and its bar
// applies (or, when on, drops) its value.
func TestCrateStoreFacets(t *testing.T) {
	items := []storeIssue{
		storeItem("a", "idea", "UI", "high", "bug"),
		storeItem("b", "idea", "UI", "low"),
		storeItem("c", "idea", "CLI", "low", "bug"),
	}
	d := buildIssuesWb(items, storeWorkflow(), "/p/x", storeFilter{FilterParams: FilterParams{System: "UI"}})
	facets := map[string]WbBars{}
	for _, b := range d.Facets {
		facets[b.Key] = b
	}
	sys := facets["issues_system"]
	if len(sys.Rows) != 2 || sys.Rows[0].Label != "UI" || sys.Rows[0].Count != 2 || !sys.Rows[0].On || sys.Rows[0].URL != "/p/x/" {
		t.Errorf("system facet = %+v", sys.Rows)
	}
	if sys.Rows[1].Label != "CLI" || sys.Rows[1].Count != 1 || sys.Rows[1].URL != "/p/x/?system=CLI" || sys.Rows[1].Pct != 50 {
		t.Errorf("system facet CLI = %+v", sys.Rows[1])
	}
	// Priorities count only UI issues.
	pri := facets["issues_priority"]
	if len(pri.Rows) != 2 || pri.Rows[0].Count != 1 || pri.Rows[1].Count != 1 || pri.Rows[0].URL != "/p/x/?priority=high&system=UI" {
		t.Errorf("priority facet = %+v", pri.Rows)
	}
	as := facets["issues_assignee"]
	if len(as.Rows) != 2 || as.Rows[0].Label != "Claimed" || as.Rows[0].Count != 0 || as.Rows[1].Label != "Unclaimed" || as.Rows[1].Count != 2 {
		t.Errorf("assignee facet = %+v", as.Rows)
	}
	if as.Empty == "" || facets["issues_type"].Key != "" {
		t.Errorf("type facet without types, or no empty text: %+v", facets)
	}
}

// Proves: AC6 — an empty project and a filter that matches nothing say so.
func TestCrateStoreEmptyStates(t *testing.T) {
	d := buildIssuesWb(nil, storeWorkflow(), "/p/x", storeFilter{})
	if d.Total != 0 || d.NoMatch || len(d.Shelves) != 0 || d.Count.Value != "0" {
		t.Errorf("empty project = %+v", d)
	}
	d = buildIssuesWb([]storeIssue{storeItem("a", "idea", "", "")}, storeWorkflow(), "/p/x", storeFilter{FilterParams: FilterParams{Status: "nope"}})
	if !d.NoMatch || d.ClearURL != "/p/x/" || d.Count.Label != "of 1 shown" {
		t.Errorf("no match = %+v", d)
	}
}

func TestCrateStoreSortsByScore(t *testing.T) {
	wf := storeWorkflow()
	wf.Scoring = tracker.ScoringConfig{Enabled: true, DefaultSort: "score_desc"}
	d := buildIssuesWb([]storeIssue{storeItem("a", "idea", "", "low"), storeItem("b", "done", "", "critical")}, wf, "/p/x", storeFilter{})
	if len(d.Shelves) != 1 || d.Shelves[0].Label != "By score" || !d.Shelves[0].Crates[0].ShowStatus {
		t.Fatalf("score shelf = %+v", shelfLabels(d))
	}
	on := false
	for _, p := range d.Search.Filters {
		if p.Key == "sort_score" {
			on = p.On
		}
	}
	if !on {
		t.Error("score pill not on under default_sort: score_desc")
	}
}

// Proves: AC1, AC8, AC10 — the project root is the crate store; the classic
// list stays at ?classic=1, and every hall page has the Issues tab.
func TestCrateStorePage(t *testing.T) {
	proj, _ := setupTestProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/test-project/")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		`<link rel="stylesheet" href="/static/workshop.css">`, `<title>Crate store · Dark Factory</title>`,
		`aria-current="page" data-testid="hall.tab.issues">Issues</span>`, `href="/p/test-project/hall" data-testid="hall.tab.line"`,
		`<h1 class="wb-h1 wb-h1--big">Crate store</h1>`,
		`href="/p/test-project/?classic=1" data-testid="issues.classic">Classic view</a>`,
		`<div class="wb-statbig-num">3</div><div class="wb-statbig-label">issues</div>`,
		`data-testid="search.issues"`, `data-testid="filter.show_you">Waiting on you</a>`,
		`data-testid="issues.shelf.in_progress"`, `<span>in progress · 1</span>`,
		`data-testid="issues.crate.bug-in-login"`, `href="/p/test-project/issue/bug-in-login" data-testid="issues.crate.bug-in-login.title">Bug in login</a>`,
		`<a class="wb-tag-pill" href="/p/test-project/?system=Auth">Auth</a>`, `href="/p/test-project/?version=1.0">v1.0</a>`,
		`<a class="wb-tag-pill wb-tag-pill--label" href="/p/test-project/?label=urgent">urgent</a>`,
		`data-testid="bars.issues_system"`, `href="/p/test-project/?new=1" data-create-open data-testid="issues.new">New issue</a>`,
		`data-testid="issues.create"`, `data-post="/p/test-project/issues/create"`, `data-testid="bars.issues_status"`, `data-testid="bars.issues_version"`,
	)
	if strings.Contains(html, `data-testid="nav.sidebar"`) || strings.Contains(html, "list.row.") {
		t.Error("the crate store renders the classic list")
	}

	_, html = getBody(t, ts.URL+"/p/test-project/?system=Nope")
	assertContains(t, html, `data-testid="issues.nomatch"`, `href="/p/test-project/" data-testid="issues.nomatch.clear"`, `of 3 shown`)

	_, classic := getBody(t, ts.URL+"/p/test-project/?classic=1&system=Auth")
	assertContains(t, classic, `data-testid="nav.sidebar"`, `data-testid="list.row.bug-in-login"`, `<input type="hidden" name="classic" value="1">`,
		`href="/p/test-project/?classic=1" class="clear-btn"`)
	if strings.Contains(classic, "list.row.add-dark-mode") {
		t.Error("classic filter lost")
	}

	for _, page := range []string{"/hall", "/decisions", "/systems", "/docs", "/retros", "/health", "/stats", "/updates"} {
		_, body := getBody(t, ts.URL+"/p/test-project"+page)
		if !strings.Contains(body, `href="/p/test-project/" data-testid="hall.tab.issues">Issues</a>`) {
			t.Errorf("%s: no Issues tab", page)
		}
	}
}

// Proves: AC7 — New issue reaches the create form from the store and from
// every classic page's sidebar.
func TestCrateStoreNewIssueOpensCreateForm(t *testing.T) {
	proj, _ := setupTestProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, board := getBody(t, ts.URL+"/p/test-project/stats?classic=1")
	assertContains(t, board, `href="/p/test-project/?classic=1&amp;new=1" class="btn btn-primary sidebar-create"`)
	_, list := getBody(t, ts.URL+"/p/test-project/?classic=1&new=1")
	assertContains(t, list, `id="create-modal"`, `params.get('new') === '1'`)
}

// Proves: AC3 — the store filters by status and version from its facets,
// and a facet past storeFacetMax folds the rest under "+N more"
// (classic-page-gaps G7, G8, G9).
func TestCrateStoreStatusVersionFacetsAndMore(t *testing.T) {
	var items []storeIssue
	for i := 0; i < storeFacetMax+3; i++ {
		it := storeItem(fmt.Sprintf("s-%d", i), "idea", fmt.Sprintf("Sys%02d", i), "")
		if i < 2 {
			it.Version = "0.10"
		}
		items = append(items, it)
	}
	items = append(items, storeItem("b-1", "backlog", "Sys00", ""))
	d := buildIssuesWb(items, storeWorkflow(), "/p/x", storeFilter{})
	byKey := map[string]WbBars{}
	for _, b := range d.Facets {
		byKey[b.Key] = b
	}
	st := byKey["issues_status"]
	if len(st.Rows) != 2 || st.Rows[0].Label != "idea" || st.Rows[0].URL != "/p/x/?status=idea" {
		t.Fatalf("status facet = %+v", st.Rows)
	}
	ver := byKey["issues_version"]
	if len(ver.Rows) != 1 || ver.Rows[0].Label != "0.10" || ver.Rows[0].Count != 2 || ver.Rows[0].URL != "/p/x/?version=0.10" {
		t.Fatalf("version facet = %+v", ver.Rows)
	}
	sys := byKey["issues_system"]
	if len(sys.Rows) != storeFacetMax || len(sys.More) != 3 {
		t.Fatalf("system facet: %d rows, %d more; want %d and 3", len(sys.Rows), len(sys.More), storeFacetMax)
	}

	d = buildIssuesWb(items, storeWorkflow(), "/p/x", storeFilter{Version: "0.10"})
	if got := crateSlugs(d); strings.Join(got, ",") != "s-0,s-1" {
		t.Fatalf("version filter shows %v", got)
	}
	found := false
	for _, p := range d.Search.Filters {
		if p.Key == "clear_version" && p.URL == "/p/x/" {
			found = true
		}
	}
	if !found {
		t.Error("no removable Version pill")
	}
}

// Proves: AC3 — New issue opens the store's own dialog, not the classic
// page: statuses and templates from createOptions, system from the filter
// (classic-page-gaps G5, G6).
func TestCrateStoreCreateDialog(t *testing.T) {
	wf := storeWorkflow()
	d := buildIssuesWb(nil, wf, "/p/x", storeFilter{FilterParams: FilterParams{System: "UI"}})
	if d.NewURL != "/p/x/?new=1" {
		t.Errorf("NewURL = %q", d.NewURL)
	}
	if d.Create.PostURL != "/p/x/issues/create" || d.Create.System != "UI" {
		t.Errorf("create = %+v", d.Create)
	}
	if strings.Join(d.Create.Statuses, ",") != "idea" {
		t.Errorf("creatable statuses = %v", d.Create.Statuses)
	}
	if got := createStatus([]string{"idea", "in design"}, "In Design"); got != "in design" {
		t.Errorf("createStatus preset = %q", got)
	}
	if got := createStatus([]string{"idea"}, "done"); got != "idea" {
		t.Errorf("createStatus fallback = %q", got)
	}
}
