package main

import (
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// withNestedGroups puts Sales (system UI) under Outbound under GTM, and
// Marketing under GTM, with goals rolling up each level
// (factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it).
func withNestedGroups(a *tracker.Project) {
	target := 10.0
	a.Departments = []tracker.Department{
		{Name: "GTM", Color: "#F2A541", Mission: "A steady pipeline", Goals: []tracker.Goal{{ID: "pipeline", Name: "Pipeline", Target: &target}}},
		{Name: "Outbound", Parent: "gtm", Color: "#7AA2F7", Mission: "Book meetings", Goals: []tracker.Goal{{ID: "meetings", Name: "Meetings", Target: &target, RollsUp: "pipeline"}}},
		{Name: "Sales", Parent: "outbound", Systems: []string{"UI"}, Goals: []tracker.Goal{{ID: "calls", Name: "Calls", Target: &target, RollsUp: "meetings"}}},
		{Name: "Marketing", Parent: "gtm", Systems: []string{"SEO"}},
		{Name: "Ops"},
	}
}

func TestCompanyNestsAGroupInsideAGroup(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	stubLeadChat(t, false)
	withNestedGroups(&a)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, company := getBody(t, ts.URL+"/p/agents/company")

	at := func(box string) int { return strings.Index(company, `data-box="`+box+`"`) }
	gtm, out, sales, mkt, ops := at("gtm"), at("outbound"), at("sales"), at("marketing"), at("ops")
	if gtm < 0 || out < gtm || sales < out || mkt < sales || ops < mkt {
		t.Fatalf("want GTM, Outbound inside it, Sales inside Outbound, then Marketing, then Ops (gtm %d, outbound %d, sales %d, marketing %d, ops %d)", gtm, out, sales, mkt, ops)
	}
	if c := strings.Count(company, `data-testid="company.room.group"`); c != 2 {
		t.Errorf("group rooms = %d, want GTM and Outbound", c)
	}
	if c := strings.Count(company, `data-testid="company.room.dept"`); c != 3 {
		t.Errorf("department rooms = %d, want Sales, Marketing and Ops", c)
	}
	if strings.Count(company, `data-box="sales"`) != 1 {
		t.Error("Sales is drawn once, inside Outbound")
	}
	assertContains(t, company, "2 departments · ", "1 department · ", "lead-agents-outbound")
}

func TestMapDrawsANestedGroupInsideItsGroup(t *testing.T) {
	a := setupAgentsProject(t)
	withNestedGroups(&a)
	b := mapDepartmentsOf(&a, []*tracker.Issue{{Slug: "x", System: "UI", Status: "backlog"}, {Slug: "y", System: "UI", Status: "backlog"}})
	if len(b) != 4 || b[2].Name != "GTM" || b[3].Name != "Ops" {
		t.Fatalf("buildings = %+v", b)
	}
	g := b[2]
	if len(g.Rooms) != 2 || g.Rooms[0].Name != "Outbound" || len(g.Rooms[0].Rooms) != 1 || g.Rooms[0].Rooms[0].Name != "Sales" || g.Rooms[1].Name != "Marketing" {
		t.Fatalf("GTM rooms = %+v", g.Rooms)
	}
	if g.Open != 2 || g.Rooms[0].Open != 2 {
		t.Errorf("open counts roll up: GTM %d, Outbound %d, want 2 and 2", g.Open, g.Rooms[0].Open)
	}
	if g.Width <= g.Rooms[0].Width || g.Rooms[0].Width <= g.Rooms[0].Rooms[0].Width || g.Rooms[0].Height <= g.Rooms[0].Rooms[0].Height {
		t.Errorf("each group holds its rooms: %+v", g)
	}
	html := renderTemplate(t, "wb/skyline", b)
	if c := strings.Count(html, `data-testid="map.lot.group"`); c != 2 {
		t.Errorf("map groups drawn = %d, want GTM and Outbound:\n%s", c, html)
	}
	if !strings.Contains(html, `title="Outbound (group): 2 open"`) || !strings.Contains(html, `title="Sales: 2 open"`) {
		t.Errorf("nested building titles:\n%s", html)
	}
}

func TestMapNestedGroupsFitTheLot(t *testing.T) {
	a := setupAgentsProject(t)
	withNestedGroups(&a)
	for i := 0; i < 8; i++ {
		a.Departments = append(a.Departments, tracker.Department{Name: "Team" + string(rune('A'+i)), Parent: "outbound"})
	}
	var open []*tracker.Issue
	for i := 0; i < 30; i++ {
		open = append(open, &tracker.Issue{Slug: "x", System: "UI", Status: "backlog"})
	}
	if w := skylineTotal(mapDepartmentsOf(&a, open)); w > skylineWidth+40 {
		t.Errorf("skyline %dpx, want it near the lot's %dpx", w, skylineWidth)
	}
}

func TestCrewNestsAGroupsColumns(t *testing.T) {
	m := &AgentsModel{
		Project:     "p",
		ProjectLead: AgentNode{Name: "lead-p", Live: true},
		Workers:     []AgentNode{{Name: "agent-s", State: "working", Department: "sales", Live: true}},
	}
	a := &tracker.Project{}
	withNestedGroups(a)
	d := buildCrew(m, a.Departments, "/p/p", "agent-s", time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	if len(d.Columns) != 3 || d.Columns[1].Name != "GTM" {
		t.Fatalf("columns = %+v", d.Columns)
	}
	g := d.Columns[1]
	if len(g.Children) != 2 || g.Children[0].Name != "Outbound" || len(g.Children[0].Children) != 1 || len(g.Children[0].Children[0].Workers) != 1 {
		t.Fatalf("GTM column = %+v", g)
	}
	if d.Panel == nil || d.Panel.Card.Name != "agent-s" {
		t.Errorf("a worker two groups down is selectable: %+v", d.Panel)
	}
}

func TestLeadsNestsAGroupsLeads(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	stubLeadChat(t, false)
	withNestedGroups(&a)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, page := getBody(t, ts.URL+"/leads")
	at := func(s string) int { return strings.Index(page, s) }
	gtm, out, outGroup, sales := at(`data-testid="leads.group.gtm"`), at(`data-testid="leads.dept_lead.outbound"`), at(`data-testid="leads.group.outbound"`), at(`data-testid="leads.dept_lead.sales"`)
	if gtm < 0 || out < gtm || outGroup < out || sales < outGroup {
		t.Fatalf("want GTM's list holding Outbound's lead, its list holding Sales's (gtm %d, outbound %d, its list %d, sales %d)", gtm, out, outGroup, sales)
	}
}

// renderTemplate runs one named template of the viewer with data.
func renderTemplate(t *testing.T, name string, data any) string {
	t.Helper()
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&out, name, data); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// The nested-groups scenario board: HQ's clients roll up from GTM's
// pipeline, itself from Outbound's meetings (Sales's calls and demos) and
// Marketing's pages: mean(mean(20, 60), 100) = 70.
func TestNestedGroupsScenarioRollsUpIntoHQ(t *testing.T) {
	proj := scenarioProject(t, "nested-groups")
	rep := goals.Resolve(&proj, time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	for key, want := range map[string]int{"outbound/meetings": 40, "gtm/pipeline": 70, "hq/clients": 70} {
		if g := rep.Goal(key); g == nil || !g.RolledUp || g.Pct() != want {
			t.Errorf("%s = %+v, want rolled up to %d%%", key, g, want)
		}
	}
	if a := proj.Ancestors("sales"); len(a) != 2 || a[0].Name != "Outbound" || a[1].Name != "GTM" {
		t.Errorf("Sales's chain = %v", a)
	}
}
