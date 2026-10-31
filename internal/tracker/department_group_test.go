package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// groupProject is a project with GTM over Marketing and Sales and Ops on
// its own: one level (nested_group_test.go has the deeper ones).
func groupProject() *Project {
	return &Project{Slug: "acme", Name: "Acme", IssueDir: "/x/issues", Departments: []Department{
		{Name: "GTM", Mission: "Bring in the next clients"},
		{Name: "Marketing", Parent: "gtm", Systems: []string{"SEO"}, Mission: "Every site refreshed weekly"},
		{Name: "Sales", Parent: "GTM"},
		{Name: "Ops"},
	}}
}

func TestGroupOfAndChildren(t *testing.T) {
	p := groupProject()
	if g := p.GroupOf("Marketing"); g == nil || g.Name != "GTM" {
		t.Errorf("GroupOf(Marketing) = %v, want GTM", g)
	}
	if g := p.GroupOf("ops"); g != nil {
		t.Errorf("GroupOf(ops) = %v, want nil", g)
	}
	var names []string
	for _, c := range p.Children("gtm") {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "Marketing,Sales" {
		t.Errorf("Children(gtm) = %v", names)
	}
	if p.IsGroup("Marketing") || !p.IsGroup("GTM") {
		t.Error("IsGroup: only GTM is a group")
	}
	if gs := p.Groups(); len(gs) != 1 || gs[0].Name != "GTM" {
		t.Errorf("Groups() = %v", gs)
	}
}

func TestInScope(t *testing.T) {
	p := groupProject()
	for _, c := range []struct {
		lead, owner string
		want        bool
	}{
		{"gtm", "GTM", true},
		{"gtm", "Marketing", true},
		{"gtm", "Sales", true},
		{"gtm", "Ops", false},
		{"gtm", "", false},
		{"marketing", "Marketing", true},
		{"marketing", "Sales", false},
		{"marketing", "GTM", false},
		{"gone", "Gone", false},
	} {
		if got := p.InScope(c.lead, c.owner); got != c.want {
			t.Errorf("InScope(%s, %s) = %v, want %v", c.lead, c.owner, got, c.want)
		}
	}
}

func TestLeadForWalksDepartmentGroupCEO(t *testing.T) {
	p := groupProject()
	seo := &Issue{Slug: "seo/faq", System: "SEO"}
	set := func(names ...string) func(string) bool {
		m := map[string]bool{}
		for _, n := range names {
			m[n] = true
		}
		return func(n string) bool { return m[n] }
	}
	const ceo, gtm, mkt = "lead-acme", "lead-acme-gtm", "lead-acme-marketing"

	r := LeadFor(p, seo, nil, set(ceo, gtm, mkt))
	if r.Lead != mkt || r.Covering || r.Group != "GTM" || r.GroupLead != gtm {
		t.Errorf("all live: %+v", r)
	}
	r = LeadFor(p, seo, nil, set(ceo, gtm))
	if r.Lead != gtm || !r.Covering {
		t.Errorf("department lead down: want the group lead covering, got %+v", r)
	}
	if s := LeadPromptSection(seo, r); !strings.Contains(s, "You report to the GTM lead lead-acme-gtm (live), covering for the Marketing lead lead-acme-marketing") {
		t.Errorf("prompt:\n%s", s)
	}
	r = LeadFor(p, seo, nil, set(ceo))
	if r.Lead != ceo || !r.Covering {
		t.Errorf("both down: want the CEO covering, got %+v", r)
	}
	r = LeadFor(p, seo, nil, set(ceo, gtm, mkt))
	if s := LeadPromptSection(seo, r); !strings.Contains(s, "who reports to the GTM lead lead-acme-gtm, under the CEO lead-acme") {
		t.Errorf("prompt:\n%s", s)
	}
}

func TestDepartmentLeadBriefGroup(t *testing.T) {
	p := groupProject()
	live := func(n string) bool { return n == "lead-acme-marketing" }
	in := LeadBriefInput{Project: p, Root: t.TempDir(), Now: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC), Live: live}

	got := DepartmentLeadBrief(nil, p.DepartmentByKey("gtm"), in)
	for _, want := range []string{
		"report to the CEO lead-acme",
		"GTM is a group: you are the lead of its departments' leads",
		"## Your departments",
		"- Marketing: lead lead-acme-marketing (live). Mission: Every site refreshed weekly",
		"- Sales: lead lead-acme-sales (not running)",
		"Scope: GTM's issues and its departments' (Marketing, Sales)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("group brief lacks %q:\n%s", want, got)
		}
	}

	got = DepartmentLeadBrief(nil, p.DepartmentByKey("marketing"), in)
	for _, want := range []string{
		"report to the GTM lead lead-acme-gtm while it is live (now not running), else the CEO lead-acme",
		"Scope: only Marketing's issues",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("child brief lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "## Your departments") {
		t.Errorf("a child brief lists departments:\n%s", got)
	}
}

func TestLoadProjectsGroup(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "projects.yaml")
	if err := os.WriteFile(cfg, []byte(`projects:
  - name: acme
    dir: ./issues
    departments:
      - name: GTM
        mission: Bring in clients
      - name: Marketing
        parent: gtm
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := LoadProjects(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p := &ps[0]
	if g := p.GroupOf("marketing"); g == nil || g.Name != "GTM" || !p.IsGroup("GTM") {
		t.Errorf("GroupOf(marketing) = %v, departments %+v", g, p.Departments)
	}
}

func TestDepartmentLeadBriefRoutes(t *testing.T) {
	p := groupProject()
	in := LeadBriefInput{Project: p, Root: t.TempDir(), Now: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC), Live: func(string) bool { return false }}
	if got := DepartmentLeadBrief(nil, p.DepartmentByKey("marketing"), in); strings.Contains(got, "Your routes") {
		t.Errorf("no connections: block, no routes line:\n%s", got)
	}
	p.Connections = &Connections{Requests: []Route{{From: "Marketing", To: "product", Via: "hq"}, {From: "marketing", To: "sales"}}}
	if got := DepartmentLeadBrief(nil, p.DepartmentByKey("marketing"), in); !strings.Contains(got, "- Your routes (projects.yaml connections:): you may ask product via hq; sales.") {
		t.Errorf("routes line:\n%s", got)
	}
	if got := DepartmentLeadBrief(nil, p.DepartmentByKey("sales"), in); !strings.Contains(got, "projects.yaml declares none from you") {
		t.Errorf("no routes from Sales:\n%s", got)
	}
}
