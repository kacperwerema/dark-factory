package lint

import (
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func TestCheckDepartmentsConnections(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Departments = []tracker.Department{
		{Name: "GTM"},
		{Name: "Marketing", Parent: "gtm"},
		{Name: "Sales", Parent: "gtm"},
		{Name: "Ops"},
	}
	env.Project.Connections = &tracker.Connections{
		Requests: []tracker.Route{
			{From: "marketing", To: "product", Via: "hq"}, // fine
			{From: "sales", To: "marketing", Via: "gtm"},  // fine: within the group
			{From: "hq", To: "ops"},                       // fine
			{From: "ops", To: "product"},                  // D-8
			{From: "finance", To: "product", Via: "hq"},
			{From: "sales", To: ""},
			{From: "ops", To: "ops"},
			{From: "sales", To: "marketing", Via: "gtm"},
			{From: "ops", To: "sales", Via: "sales"},
			{From: "ops", To: "marketing", Via: "gtm"},
			{From: "marketing", To: "ops", Via: "sales"},
			{From: "hq", To: "sales", Via: "gtm"},
			{From: "marketing", To: "sales", Via: "nowhere"},
		},
		Feeds: []tracker.Route{
			{From: "marketing", To: "sales"}, // fine
			{From: "hq", To: "sales"},
			{From: "sales", To: "marketing", Via: "gtm"},
		},
	}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Where+": "+f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"projects.yaml (connections): requests[3]: Ops → Product has no via: engine requests go to Product through the CEO",
		`requests[4]: from: "finance" names no box`,
		"requests[5]: to: is empty",
		"requests[6]: Ops → Ops goes from a box to itself",
		"requests[7]: Sales → Marketing is declared twice",
		"requests[8]: Ops → Sales via Sales: the route goes through one of its own ends",
		"requests[9]: Ops → Marketing via GTM: Ops is not in that group",
		"requests[10]: via: Sales is no group",
		"requests[11]: a request from HQ needs no via",
		`requests[12]: via: "nowhere" names no box`,
		"feeds[1]: a feed from HQ",
		"feeds[2]: via: on a feed is ignored",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	if len(got) != 12 {
		t.Errorf("got %d findings, want 12:\n%s", len(got), all)
	}
}

// Nested groups: via: names any group above the asker
// (factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it#D-2).
func TestCheckDepartmentsConnectionsNested(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	env.Project.Departments = []tracker.Department{
		{Name: "GTM"},
		{Name: "Outbound", Parent: "gtm"},
		{Name: "Sales", Parent: "outbound"},
		{Name: "Marketing", Parent: "gtm"},
	}
	env.Project.Connections = &tracker.Connections{
		Requests: []tracker.Route{
			{From: "sales", To: "marketing", Via: "gtm"},      // fine: GTM is Sales's grandparent group
			{From: "sales", To: "gtm", Via: "outbound"},       // fine: its own group
			{From: "marketing", To: "sales", Via: "outbound"}, // Marketing is not under Outbound
		},
	}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Message)
	}
	all := strings.Join(got, "\n")
	if len(got) != 1 || !strings.Contains(all, "requests[2]: Marketing → Sales via Outbound: Marketing is not in that group") {
		t.Errorf("want only the Marketing via Outbound finding:\n%s", all)
	}
}
