package tracker

import (
	"strings"
	"testing"
	"time"
)

// nestedProject is GTM over Outbound (a group) and Marketing, Outbound
// over Sales; Ops on its own; a loop (LoopA ↔ LoopB) and a chain one group
// too deep (V under W under X under Y under Z)
// (factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it).
func nestedProject() *Project {
	return &Project{Slug: "acme", Name: "Acme", IssueDir: "/x/issues", Departments: []Department{
		{Name: "GTM", Mission: "Bring in the next clients"},
		{Name: "Outbound", Parent: "gtm", Mission: "Book meetings"},
		{Name: "Marketing", Parent: "gtm", Systems: []string{"SEO"}},
		{Name: "Sales", Parent: "outbound", Systems: []string{"Deals"}, Mission: "Close"},
		{Name: "Ops"},
		{Name: "LoopA", Parent: "loopb"},
		{Name: "LoopB", Parent: "loopa"},
		{Name: "Z"}, {Name: "Y", Parent: "z"}, {Name: "X", Parent: "y"}, {Name: "W", Parent: "x"}, {Name: "V", Parent: "w"},
	}}
}

func names(ds []Department) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Name)
	}
	return strings.Join(out, ",")
}

func TestNestedGroupChain(t *testing.T) {
	p := nestedProject()
	if g := p.GroupOf("sales"); g == nil || g.Name != "Outbound" {
		t.Errorf("GroupOf(sales) = %v, want Outbound", g)
	}
	if g := p.GroupOf("outbound"); g == nil || g.Name != "GTM" {
		t.Errorf("GroupOf(outbound) = %v, want GTM", g)
	}
	var chain []string
	for _, a := range p.Ancestors("Sales") {
		chain = append(chain, a.Name)
	}
	if strings.Join(chain, ",") != "Outbound,GTM" {
		t.Errorf("Ancestors(Sales) = %v", chain)
	}
	if got := names(p.Children("gtm")); got != "Outbound,Marketing" {
		t.Errorf("Children(gtm) = %s", got)
	}
	if got := names(p.Descendants("gtm")); got != "Outbound,Sales,Marketing" {
		t.Errorf("Descendants(gtm) = %s", got)
	}
	if got := names(p.Groups()); got != "GTM,Outbound,Z,Y,X" {
		t.Errorf("Groups() = %s", got)
	}
	for _, c := range []struct{ dept, problem string }{
		{"sales", ""}, {"loopa", "loop"}, {"loopb", "loop"}, {"w", ""}, {"v", "deep"}, {"ops", ""},
	} {
		if _, got := p.ParentChain(c.dept); got != c.problem {
			t.Errorf("ParentChain(%s) problem = %q, want %q", c.dept, got, c.problem)
		}
	}
	for _, d := range []string{"loopa", "loopb", "v"} {
		if g := p.GroupOf(d); g != nil {
			t.Errorf("%s's chain is broken: GroupOf = %v, want nil (top-level)", d, g)
		}
	}
	if g := p.GroupOf("w"); g == nil || g.Name != "X" {
		t.Errorf("W sits exactly 3 deep: GroupOf = %v", g)
	}
}

func TestNestedInScope(t *testing.T) {
	p := nestedProject()
	for _, c := range []struct {
		lead, owner string
		want        bool
	}{
		{"gtm", "Sales", true},
		{"gtm", "Outbound", true},
		{"outbound", "Sales", true},
		{"outbound", "Marketing", false},
		{"outbound", "GTM", false},
		{"sales", "Outbound", false},
		{"loopa", "LoopB", false},
	} {
		if got := p.InScope(c.lead, c.owner); got != c.want {
			t.Errorf("InScope(%s, %s) = %v, want %v", c.lead, c.owner, got, c.want)
		}
	}
}

func TestNestedLeadForWalksUpTheChain(t *testing.T) {
	p := nestedProject()
	deal := &Issue{Slug: "deals/acme", System: "Deals"}
	set := func(names ...string) func(string) bool {
		m := map[string]bool{}
		for _, n := range names {
			m[n] = true
		}
		return func(n string) bool { return m[n] }
	}
	const ceo, gtm, out, sales = "lead-acme", "lead-acme-gtm", "lead-acme-outbound", "lead-acme-sales"
	if r := LeadFor(p, deal, nil, set(ceo, gtm, out, sales)); r.Lead != sales || r.Covering || r.Group != "Outbound" {
		t.Errorf("all live: %+v", r)
	}
	if r := LeadFor(p, deal, nil, set(ceo, gtm, out)); r.Lead != out || !r.Covering {
		t.Errorf("sales down: want Outbound covering, got %+v", r)
	}
	r := LeadFor(p, deal, nil, set(ceo, gtm))
	if r.Lead != gtm || !r.Covering || r.Group != "GTM" || r.GroupLead != gtm {
		t.Errorf("sales and outbound down: want GTM covering, got %+v", r)
	}
	if s := LeadPromptSection(deal, r); !strings.Contains(s, "You report to the GTM lead lead-acme-gtm (live), covering for the Sales lead lead-acme-sales") {
		t.Errorf("prompt:\n%s", s)
	}
	if r := LeadFor(p, deal, nil, set(ceo)); r.Lead != ceo || !r.Covering {
		t.Errorf("all down: want the CEO, got %+v", r)
	}
}

func TestNestedDepartmentLeadBrief(t *testing.T) {
	p := nestedProject()
	live := func(n string) bool { return n == "lead-acme-gtm" }
	in := LeadBriefInput{Project: p, Root: t.TempDir(), Now: time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC), Live: live}

	got := DepartmentLeadBrief(nil, p.DepartmentByKey("sales"), in)
	if want := "report to the Outbound lead lead-acme-outbound while it is live (now not running), else the GTM lead lead-acme-gtm while it is live (now live), else the CEO lead-acme"; !strings.Contains(got, want) {
		t.Errorf("sales brief lacks %q:\n%s", want, got)
	}
	got = DepartmentLeadBrief(nil, p.DepartmentByKey("outbound"), in)
	for _, want := range []string{
		"report to the GTM lead lead-acme-gtm while it is live (now live), else the CEO lead-acme",
		"Outbound is a group: you are the lead of its departments' leads",
		"- Sales: lead lead-acme-sales (not running). Mission: Close",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("outbound brief lacks %q:\n%s", want, got)
		}
	}
	got = DepartmentLeadBrief(nil, p.DepartmentByKey("gtm"), in)
	for _, want := range []string{
		"- Outbound: lead lead-acme-outbound (not running). A group itself: Sales report to its lead",
		"Scope: GTM's issues and its departments' (Outbound, Sales, Marketing)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("gtm brief lacks %q:\n%s", want, got)
		}
	}
}
