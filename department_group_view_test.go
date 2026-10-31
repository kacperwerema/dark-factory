package main

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// withGTMGroup puts a Marketing department (system UI) under a GTM group
// with a goal Marketing's rolls up into.
func withGTMGroup(a *tracker.Project) {
	target := 10.0
	a.Departments = []tracker.Department{
		{Name: "GTM", Color: "#F2A541", Mission: "A steady pipeline", Goals: []tracker.Goal{{ID: "pipeline", Name: "Pipeline", Target: &target}}},
		{Name: "Marketing", Parent: "gtm", Systems: []string{"UI"}, Goals: []tracker.Goal{{ID: "pages", Name: "Pages", Target: &target, RollsUp: "pipeline"}}},
		{Name: "Ops"},
	}
}

func TestCompanyShowsAGroupWithItsDepartmentsInside(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	stubLeadChat(t, false)
	withGTMGroup(&a)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, company := getBody(t, ts.URL+"/p/agents/company?classic=1")

	g := strings.Index(company, `data-testid="company.room.group"`)
	inside := strings.Index(company, `data-testid="company.room.group.departments"`)
	mkt := strings.Index(company, `<span class="wb-room-title">Marketing`)
	ops := strings.Index(company, `<span class="wb-room-title">Ops`)
	if g < 0 || inside < g || mkt < inside || ops < mkt {
		t.Fatalf("want the GTM room, Marketing inside it, then Ops (group %d, inside %d, marketing %d, ops %d)", g, inside, mkt, ops)
	}
	if strings.Count(company, `<span class="wb-room-title">Marketing`) != 1 {
		t.Error("Marketing is drawn once, inside GTM")
	}
	assertContains(t, company, "1 department · ", "A steady pipeline", "lead-agents-gtm", `data-spawn-lead="/p/agents/lead/spawn?dept=gtm"`, `data-testid="company.room.group.waiting"`)
	if c := strings.Count(company, `data-testid="company.room.dept"`); c != 2 {
		t.Errorf("department rooms = %d, want Marketing and Ops", c)
	}
}

func TestMapDrawsAGroupAsOneBuilding(t *testing.T) {
	a := setupAgentsProject(t)
	withGTMGroup(&a)
	b := mapDepartmentsOf(&a, []*tracker.Issue{{Slug: "x", System: "UI", Status: "backlog"}})
	if len(b) != 4 || b[2].Name != "GTM" || len(b[2].Rooms) != 1 || b[2].Rooms[0].Name != "Marketing" || b[2].Open != 1 || b[3].Name != "Ops" {
		t.Fatalf("buildings = %+v", b)
	}
	if b[2].Width <= b[2].Rooms[0].Width || b[2].Height <= b[2].Rooms[0].Height {
		t.Errorf("the group's building holds its rooms: %+v", b[2])
	}
}

func TestCrewPutsAGroupsDepartmentsInItsColumn(t *testing.T) {
	m := &AgentsModel{
		Project:     "p",
		ProjectLead: AgentNode{Name: "lead-p", Live: true},
		DepartmentLeads: []AgentNode{
			{Name: "lead-p-gtm", Department: "GTM", Live: true},
			{Name: "lead-p-marketing", Department: "Marketing"},
		},
		Workers: []AgentNode{{Name: "agent-m", State: "working", Department: "marketing", Live: true}},
	}
	a := &tracker.Project{}
	withGTMGroup(a)
	d := buildCrew(m, a.Departments, "/p/p", "agent-m", time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	if len(d.Columns) != 3 || d.Columns[1].Name != "GTM" || d.Columns[2].Name != "Ops" {
		t.Fatalf("columns = %+v", d.Columns)
	}
	g := d.Columns[1]
	if g.Lead == nil || g.Lead.Name != "lead-p-gtm" || len(g.Children) != 1 || g.Children[0].Name != "Marketing" || len(g.Children[0].Workers) != 1 {
		t.Fatalf("GTM column = %+v", g)
	}
	if d.Panel == nil || d.Panel.Card.Name != "agent-m" {
		t.Errorf("a worker inside a group's column is selectable: panel = %+v", d.Panel)
	}
}

func TestLeadsListsAGroupsDepartmentLeads(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	stubLeadChat(t, false)
	withGTMGroup(&a)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, page := getBody(t, ts.URL+"/leads")
	g := strings.Index(page, `data-testid="leads.dept_lead.gtm"`)
	nested := strings.Index(page, `data-testid="leads.group.gtm"`)
	mkt := strings.Index(page, `data-testid="leads.dept_lead.marketing"`)
	if g < 0 || nested < g || mkt < nested {
		t.Fatalf("want GTM lead, then its group list holding Marketing's (gtm %d, group %d, marketing %d)", g, nested, mkt)
	}
}

// From the New department tile: GTM installs the group and Marketing, the
// room shows with Marketing inside, and with both templates in the tile
// offers no more. A second Marketing, under the group or not, is refused.
func TestNewDeptTileAddsAGroupThenNoMore(t *testing.T) {
	config, proj := deptSandbox(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, company := getBody(t, ts.URL+"/p/acme/company?classic=1")
	assertContains(t, company, `value="gtm"`, "group: installs Marketing under it")
	if strings.Contains(company, `data-testid="company.new_dept.parent"`) {
		t.Error("no group yet: the tile asks for none")
	}
	_, preview := getBody(t, ts.URL+"/p/acme/departments/new?template=gtm")
	assertContains(t, preview, `data-testid="dept_new.child.marketing"`, "In the group: Marketing", "parent: gtm", "departments/gtm/role.md")

	resp := postAdd(t, ts.URL, url.Values{"template": {"gtm"}}, sameOrigin)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("add gtm: status %d", resp.StatusCode)
	}
	cfg, _ := os.ReadFile(config)
	assertContains(t, string(cfg), "- name: GTM", "- name: Marketing", "parent: gtm")

	_, company = getBody(t, ts.URL+resp.Header.Get("Location"))
	assertContains(t, company, "✓ Added GTM: 6 files written", `data-testid="floor.tile.group"`, "Every department template is added")
	if strings.Contains(company, `data-testid="company.new_dept.form"`) {
		t.Error("every template is added: the tile still has its form")
	}

	resp = postAdd(t, ts.URL, url.Values{"template": {"marketing"}, "parent": {"gtm"}}, sameOrigin)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("marketing again under gtm: status %d, want 409", resp.StatusCode)
	}
	if after, _ := os.ReadFile(config); string(after) != string(cfg) {
		t.Error("a refused Add changed projects.yaml")
	}
}

// With a group of the owner's own (Biz over Sales) the tile asks which
// group a new department goes under, and Add puts it there.
func TestNewDeptTileAddsADepartmentUnderAGroup(t *testing.T) {
	config, _ := deptSandbox(t)
	f, err := os.OpenFile(config, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("    departments:\n      - name: Biz\n      - name: Sales\n        parent: biz\n")
	f.Close()
	projects, err := tracker.LoadProjects(config)
	if err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, projects)
	defer ts.Close()

	_, company := getBody(t, ts.URL+"/p/acme/company?classic=1")
	assertContains(t, company, `data-testid="company.new_dept.parent"`, `<option value="biz">Biz</option>`)
	resp := postAdd(t, ts.URL, url.Values{"template": {"marketing"}, "parent": {"biz"}}, sameOrigin)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("add under biz: status %d", resp.StatusCode)
	}
	if projects, err = tracker.LoadProjects(config); err != nil {
		t.Fatal(err)
	}
	if kids := projects[0].Children("biz"); len(kids) != 2 || kids[1].Name != "Marketing" {
		t.Fatalf("Biz's departments = %+v", kids)
	}
}
