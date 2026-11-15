package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
