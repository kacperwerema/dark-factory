package briefing

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// nestedProject is GTM over Outbound (a group) and Marketing, Outbound
// over Sales; Ops on its own; a loop (LoopA ↔ LoopB) and a chain one group
// too deep (V under W under X under Y under Z)
// (factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it).
func nestedProject() *tracker.Project {
	return &tracker.Project{Slug: "acme", Name: "Acme", IssueDir: "/x/issues", Departments: []tracker.Department{
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
