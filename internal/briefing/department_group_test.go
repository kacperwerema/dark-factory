package briefing

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// groupProject is a project with GTM over Marketing and Sales and Ops on
// its own: one level (nested_group_test.go has the deeper ones).
func groupProject() *tracker.Project {
	return &tracker.Project{Slug: "acme", Name: "Acme", IssueDir: "/x/issues", Departments: []tracker.Department{
		{Name: "GTM", Mission: "Bring in the next clients"},
		{Name: "Marketing", Parent: "gtm", Systems: []string{"SEO"}, Mission: "Every site refreshed weekly"},
		{Name: "Sales", Parent: "GTM"},
		{Name: "Ops"},
	}}
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

func TestDepartmentLeadBriefRoutes(t *testing.T) {
	p := groupProject()
	in := LeadBriefInput{Project: p, Root: t.TempDir(), Now: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC), Live: func(string) bool { return false }}
	if got := DepartmentLeadBrief(nil, p.DepartmentByKey("marketing"), in); strings.Contains(got, "Your routes") {
		t.Errorf("no connections: block, no routes line:\n%s", got)
	}
	p.Connections = &tracker.Connections{Requests: []tracker.Route{{From: "Marketing", To: "product", Via: "hq"}, {From: "marketing", To: "sales"}}}
	if got := DepartmentLeadBrief(nil, p.DepartmentByKey("marketing"), in); !strings.Contains(got, "- Your routes (projects.yaml connections:): you may ask product via hq; sales.") {
		t.Errorf("routes line:\n%s", got)
	}
	if got := DepartmentLeadBrief(nil, p.DepartmentByKey("sales"), in); !strings.Contains(got, "projects.yaml declares none from you") {
		t.Errorf("no routes from Sales:\n%s", got)
	}
}
