package connections

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// declared is project with routes: Marketing → Product via HQ, Sales →
// Marketing via GTM, and a feed Marketing → Sales.
func declared(t *testing.T) *tracker.Project {
	p := project(t)
	p.Connections = &tracker.Connections{
		Requests: []tracker.Route{
			{From: "marketing", To: "product", Via: "hq"},
			{From: "Sales", To: "marketing", Via: "gtm"},
			{From: "nobody", To: "product"}, // lint's; skipped here
		},
		Feeds: []tracker.Route{{From: "marketing", To: "sales", What: "brand and ICP"}},
	}
	return p
}

func TestDeclaredResolvesAndSkipsUnknown(t *testing.T) {
	got := Declared(declared(t))
	if len(got) != 3 {
		t.Fatalf("want 3 declared (unknown box skipped), got %+v", got)
	}
	if got[1] != (Decl{From: "sales", To: "marketing", Kind: Request, Via: "gtm"}) {
		t.Errorf("names resolve to keys: %+v", got[1])
	}
	if got[2].Kind != Output || got[2].What != "brand and ICP" {
		t.Errorf("feed: %+v", got[2])
	}
}

func TestMarkDeclaredUndeclaredUnused(t *testing.T) {
	p := declared(t)
	asked := issue(t, "factory/brand-fields", "status: \"idea\"\nsystem: \"Factory\"\nrequested_by: \"Marketing\"", "")
	offRoute := issue(t, "factory/crm", "status: \"idea\"\nsystem: \"Factory\"\nrequested_by: \"Sales\"", "")
	dep := issue(t, "seo/page", "status: \"idea\"\nsystem: \"SEO\"\ndepends_on: \"factory/crm\"", "")
	ceoAsk := issue(t, "leads/list", "status: \"idea\"\nsystem: \"Leads\"\nrequested_by: \"hq\"", "")
	outs := []*outputs.Output{{ID: "o1", Department: "sales", Title: "Call notes", State: outputs.StateApproved,
		Uses: []outputs.Use{{For: "marketing", Date: now}}}}
	edges := Mark(p, derive(p, []*tracker.Issue{asked, offRoute, dep, ceoAsk}, outs, nil))

	if e := find(edges, "marketing", Product, Request); e == nil || e.Route != RouteDeclared || e.DeclaredVia != HQ {
		t.Errorf("Marketing → Product is declared via HQ: %+v", e)
	}
	if e := find(edges, "sales", Product, Request); e == nil || e.Route != RouteUndeclared {
		t.Errorf("Sales → Product is traffic off the routes: %+v", e)
	}
	if e := find(edges, HQ, "sales", Request); e == nil || e.Route != "" {
		t.Errorf("HQ may ask anyone: never marked off route: %+v", e)
	}
	if e := find(edges, Product, "marketing", Dependency); e == nil || e.Route != "" {
		t.Errorf("dependencies are never declared, so never marked: %+v", e)
	}
	if e := find(edges, "sales", "marketing", Output); e == nil || e.Route != RouteUndeclared {
		t.Errorf("Sales → Marketing output: feeds are declared, this one isn't: %+v", e)
	}
	e := find(edges, "sales", "marketing", Request)
	if e == nil || e.Route != RouteUnused || e.Via != "gtm" || len(e.Open) != 0 {
		t.Errorf("Sales → GTM → Marketing is declared with no traffic: %+v", e)
	}
	if e := find(edges, "marketing", "sales", Output); e == nil || e.Route != RouteUnused || e.What != "brand and ICP" {
		t.Errorf("the feed has no traffic: %+v", e)
	}
	if got := Label(p, *find(edges, "sales", Product, Request)); got != "Sales → Product · 1 request · not declared" {
		t.Errorf("label: %q", got)
	}
	if got := Label(p, *find(edges, "sales", "marketing", Request)); got != "Sales → GTM → Marketing · request · declared, no traffic" {
		t.Errorf("label: %q", got)
	}
	if c := CountsByBox(edges)["gtm"]; c.In+c.Out != 0 {
		t.Errorf("an unused line counts nothing: %+v", c)
	}
}

func TestMarkWithoutDeclarationsChangesNothing(t *testing.T) {
	p := project(t)
	asked := issue(t, "factory/brand-fields", "status: \"idea\"\nsystem: \"Factory\"\nrequested_by: \"Marketing\"", "")
	edges := Mark(p, derive(p, []*tracker.Issue{asked}, nil, nil))
	if len(edges) != 1 || edges[0].Route != "" {
		t.Errorf("no connections: block, no marks: %+v", edges)
	}
	// Only feeds declared: requests stay unmarked.
	p.Connections = &tracker.Connections{Feeds: []tracker.Route{{From: "marketing", To: "sales"}}}
	edges = Mark(p, derive(p, []*tracker.Issue{asked}, nil, nil))
	if e := find(edges, "marketing", Product, Request); e == nil || e.Route != "" {
		t.Errorf("requests: not declared, request lines unmarked: %+v", e)
	}
}

func TestCheckRequest(t *testing.T) {
	p := declared(t)
	if d, err := CheckRequest(p, "marketing", Product); err != nil || d == nil || d.Via != HQ {
		t.Errorf("declared route: %+v %v", d, err)
	}
	for _, c := range [][2]string{{HQ, "sales"}, {"", Product}, {"sales", "sales"}} {
		if _, err := CheckRequest(p, c[0], c[1]); err != nil {
			t.Errorf("%s → %s is never refused: %v", c[0], c[1], err)
		}
	}
	_, err := CheckRequest(p, "sales", Product)
	if err == nil {
		t.Fatal("Sales → Product is off the routes")
	}
	for _, want := range []string{"Sales → Product is not a declared request route", "Sales may ask: Marketing via GTM.", "issue-cli create --requested-by sales", "- {from: sales, to: product, via: hq}"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q:\n%s", want, err)
		}
	}
	if _, err := CheckRequest(p, "gtm", Product); err == nil || !strings.Contains(err.Error(), "GTM has no declared request routes. Ask the CEO") {
		t.Errorf("a box without routes is pointed at the CEO: %v", err)
	}
	if _, err := CheckRequest(project(t), "sales", Product); err != nil {
		t.Errorf("nothing declared, nothing refused: %v", err)
	}
}

func TestRelayPending(t *testing.T) {
	p := declared(t)
	ask := issue(t, "seo/call-list", "status: \"idea\"\nsystem: \"SEO\"\nrequested_by: \"sales\"", "")
	if got := RelayPending(p, ask, nil); got != "gtm" {
		t.Errorf("Sales → Marketing waits for GTM's lead: %q", got)
	}
	byGTM := []tracker.Comment{{Text: "approval: \"ok\" → backlog — recorded by " + tracker.DepartmentLeadName(p, "GTM")}}
	if got := RelayPending(p, ask, byGTM); got != "" {
		t.Errorf("GTM's lead approved it: %q", got)
	}
	byMarketing := []tracker.Comment{{Text: "dispatch: \"go\" — recorded by " + tracker.DepartmentLeadName(p, "Marketing")}}
	if got := RelayPending(p, ask, byMarketing); got != "gtm" {
		t.Errorf("the receiving lead is no relay: %q", got)
	}
	engine := issue(t, "factory/x", "status: \"idea\"\nsystem: \"Factory\"\nrequested_by: \"marketing\"", "")
	if got := RelayPending(p, engine, nil); got != HQ {
		t.Errorf("Marketing → Product waits for the CEO: %q", got)
	}
	own := issue(t, "seo/own", "status: \"idea\"\nsystem: \"SEO\"", "")
	if got := RelayPending(p, own, nil); got != "" {
		t.Errorf("no request, no relay: %q", got)
	}
}

func TestDeriveViaGroupLead(t *testing.T) {
	p := project(t)
	ask := issue(t, "seo/call-list", "status: \"idea\"\nsystem: \"SEO\"\nrequested_by: \"sales\"", "")
	gtm := tracker.DepartmentLeadName(p, "GTM")
	edges := derive(p, []*tracker.Issue{ask}, nil, map[string][]tracker.Comment{
		ask.Slug: {{Text: "approval: \"ok\" → backlog — recorded by " + gtm}},
	})
	if e := find(edges, "sales", "marketing", Request); e == nil || e.Via != "gtm" {
		t.Fatalf("a request GTM's lead forwarded runs through GTM: %+v", edges)
	}
	if got := Label(p, edges[0]); got != "Sales → GTM → Marketing · 1 request" {
		t.Errorf("label: %q", got)
	}
}

// A group directs its own departments: its week plan files their work
// straight, never off route; it still asks anyone else by the routes.
func TestGroupAsksItsOwnDepartmentsStraight(t *testing.T) {
	p := declared(t)
	for _, to := range []string{"marketing", "sales"} {
		if d, err := CheckRequest(p, "gtm", to); err != nil || d != nil {
			t.Errorf("gtm → %s: %+v %v", to, d, err)
		}
	}
	if _, err := CheckRequest(p, "marketing", "sales"); err == nil {
		t.Error("a department asking a sibling still follows the routes")
	}
	planned := issue(t, "seo/refresh", "status: \"brief\"\nsystem: \"SEO\"\nrequested_by: \"gtm\"", "")
	edges := Mark(p, derive(p, []*tracker.Issue{planned}, nil, nil))
	if e := find(edges, "gtm", "marketing", Request); e == nil || e.Route != "" {
		t.Errorf("GTM → Marketing is never marked off route: %+v", e)
	}
}

// Nested groups: GTM over Outbound over Sales. GTM asks Sales straight (an
// ancestor directs every department under it), Outbound too; Sales asks
// Marketing via its parent group's lead or the grandparent's
// (factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it#D-2).
func TestNestedGroupAsksAndRelays(t *testing.T) {
	p := declared(t)
	p.Departments = []tracker.Department{
		{Name: "GTM"},
		{Name: "Outbound", Parent: "gtm"},
		{Name: "Sales", Parent: "outbound", Systems: []string{"Leads"}},
		{Name: "Marketing", Parent: "gtm", Systems: []string{"SEO"}},
	}
	for _, from := range []string{"gtm", "outbound"} {
		if d, err := CheckRequest(p, from, "sales"); err != nil || d != nil {
			t.Errorf("%s → sales: %+v %v", from, d, err)
		}
	}
	if GroupAsksOwn(p, "outbound", "marketing") {
		t.Error("Outbound does not hold Marketing")
	}
	if !UnderGroup(p, "sales", "gtm") || UnderGroup(p, "marketing", "outbound") {
		t.Error("UnderGroup: Sales is under GTM, Marketing not under Outbound")
	}
	ask := issue(t, "seo/call-list", "status: \"idea\"\nsystem: \"SEO\"\nrequested_by: \"sales\"", "")
	gtm := tracker.DepartmentLeadName(p, "GTM")
	edges := derive(p, []*tracker.Issue{ask}, nil, map[string][]tracker.Comment{
		ask.Slug: {{Text: "approval: \"ok\" → backlog — recorded by " + gtm}},
	})
	if e := find(edges, "sales", "marketing", Request); e == nil || e.Via != "gtm" {
		t.Fatalf("Sales's request the GTM lead forwarded runs through GTM: %+v", edges)
	}
}
