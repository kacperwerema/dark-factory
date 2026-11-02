package connections

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// project has Marketing (system SEO) and Sales (system Leads) under GTM.
func project(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{
		Slug: "acme", IssueDir: issues, WorkDir: root,
		Departments: []tracker.Department{
			{Name: "GTM"},
			{Name: "Marketing", Systems: []string{"SEO"}, Parent: "GTM"},
			{Name: "Sales", Systems: []string{"Leads"}, Parent: "GTM"},
		},
	}
}

// issue parses frontmatter lines and a body into an issue with slug.
func issue(t *testing.T, slug, front, body string) *tracker.Issue {
	t.Helper()
	data := "---\ntitle: \"" + slug + "\"\n" + front + "\n---\n" + body + "\n"
	i, err := tracker.ParseIssue(strings.ReplaceAll(slug, "/", "-")+".md", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	i.Slug = slug
	return i
}

func derive(p *tracker.Project, issues []*tracker.Issue, outs []*outputs.Output, comments map[string][]tracker.Comment) []Edge {
	return Derive(Input{
		Project: p, Issues: issues, Outputs: outs, Now: now,
		Decisions: tracker.BuildDecisionIndex(issues, nil),
		Comments:  func(i *tracker.Issue) []tracker.Comment { return comments[i.Slug] },
	})
}

func find(edges []Edge, from, to, kind string) *Edge {
	for i := range edges {
		if edges[i].From == from && edges[i].To == to && edges[i].Kind == kind {
			return &edges[i]
		}
	}
	return nil
}

func slugs(items []Item) string {
	var s []string
	for _, i := range items {
		s = append(s, i.Slug)
	}
	return strings.Join(s, ",")
}

func TestDependencyAcrossDepartments(t *testing.T) {
	p := project(t)
	engine := issue(t, "factory/engine", "status: \"in progress\"\nsystem: \"Factory\"", "")
	done := issue(t, "factory/old", "status: \"done\"\nsystem: \"Factory\"", "")
	seoPage := issue(t, "seo/page", "status: \"backlog\"\nsystem: \"SEO\"\ndepends_on: \"factory/engine, factory/old, factory/missing, seo/sibling\"", "")
	sibling := issue(t, "seo/sibling", "status: \"backlog\"\nsystem: \"SEO\"", "")
	lead := issue(t, "leads/call", "status: \"idea\"\nsystem: \"Leads\"\nblocked_on: \"seo/page\"", "")
	shipped := issue(t, "seo/shipped", "status: \"done\"\nsystem: \"SEO\"\ndone_at: \"2026-10-20T10:00:00Z\"\ndepends_on: \"factory/engine\"", "")
	ancient := issue(t, "seo/ancient", "status: \"done\"\nsystem: \"SEO\"\ndone_at: \"2026-08-01\"\ndepends_on: \"factory/engine\"", "")

	edges := derive(p, []*tracker.Issue{engine, done, seoPage, sibling, lead, shipped, ancient}, nil, nil)

	e := find(edges, Product, "marketing", Dependency)
	if e == nil {
		t.Fatalf("no Product → Marketing dependency in %+v", edges)
	}
	if slugs(e.Open) != "seo/page" || e.Open[0].Why != "depends_on factory/engine" {
		t.Errorf("open = %+v, want seo/page waiting on factory/engine", e.Open)
	}
	// Done in the last 30 days is listed closed; older is gone.
	if slugs(e.Closed) != "seo/shipped" {
		t.Errorf("closed = %q, want seo/shipped", slugs(e.Closed))
	}
	if find(edges, "marketing", "marketing", Dependency) != nil {
		t.Error("a same-department depends_on is not a connection")
	}
	if e := find(edges, "marketing", "sales", Dependency); e == nil || slugs(e.Open) != "leads/call" || e.Open[0].Why != "blocked_on seo/page" {
		t.Errorf("blocked_on Marketing → Sales = %+v", e)
	}
	for _, e := range edges {
		for _, it := range append(e.Open, e.Closed...) {
			if strings.Contains(it.Why, "factory/old") || strings.Contains(it.Why, "factory/missing") {
				t.Errorf("a done or missing target is not an open dependency: %+v", it)
			}
		}
	}
}

func TestRequestsByFieldAndCitedDecision(t *testing.T) {
	p := project(t)
	brand := issue(t, "seo/brand", "status: \"in progress\"\nsystem: \"SEO\"", "## Decisions\n\n- **D-1** · 2026-10-30 · lead chat: engine requests go via the CEO, \"yes\"\n")
	asked := issue(t, "factory/engine-change", "status: \"in design\"\nsystem: \"Factory\"\nrequested_by: \"Marketing\"", "")
	cites := issue(t, "factory/connections", "status: \"backlog\"\nsystem: \"Factory\"", "Routing (seo/brand#D-1): via the CEO.")
	fromHQ := issue(t, "leads/outreach", "status: \"idea\"\nsystem: \"Leads\"\nrequested_by: \"hq\"", "")
	nobody := issue(t, "factory/unknown", "status: \"idea\"\nsystem: \"Factory\"\nrequested_by: \"Accounting\"", "")
	comments := map[string][]tracker.Comment{
		"factory/connections": {{Text: "approval: \"go\" → in design — recorded by lead-acme"}},
		"leads/outreach":      {{Text: "dispatch: \"go\"", Author: "lead-acme"}},
	}

	edges := derive(p, []*tracker.Issue{brand, asked, cites, fromHQ, nobody}, nil, comments)

	straight := find(edges, "marketing", Product, Request)
	if straight == nil || straight.Via != "" || slugs(straight.Open) != "factory/engine-change" || straight.Open[0].Why != "requested_by: Marketing" {
		t.Fatalf("straight request = %+v", straight)
	}
	var via *Edge
	for i := range edges {
		if edges[i].Kind == Request && edges[i].Via == HQ {
			via = &edges[i]
		}
	}
	if via == nil || via.From != "marketing" || via.To != Product || slugs(via.Open) != "factory/connections" || via.Open[0].Why != "cites seo/brand#D-1" {
		t.Fatalf("via-HQ request = %+v", via)
	}
	if e := find(edges, HQ, "sales", Request); e == nil || e.Via != "" {
		t.Errorf("an HQ request is drawn straight from HQ: %+v", e)
	}
	for _, e := range edges {
		if slugs(e.Open) == "factory/unknown" {
			t.Errorf("requested_by naming no department is not drawn: %+v", e)
		}
	}
	if got := Label(p, *via); got != "Marketing → HQ → Product · 1 request" {
		t.Errorf("label = %q", got)
	}
	c := CountsByBox(edges)
	if c[HQ].Out != 2 || c[HQ].In != 1 || c[Product].In != 2 {
		t.Errorf("counts = %+v", c)
	}
	if got := Select(edges, Filter{To: HQ}); len(got) != 1 || got[0].Via != HQ {
		t.Errorf("filter to hq keeps the via-HQ line: %+v", got)
	}
}

func TestOutputsLinkedAndUsed(t *testing.T) {
	p := project(t)
	maker := issue(t, "seo/brand", "status: \"done\"\nsystem: \"SEO\"", "")
	user := issue(t, "leads/pitch", "status: \"in progress\"\nsystem: \"Leads\"\ndepends_on: \"seo/brand\"", "")
	outs := []*outputs.Output{
		{ID: "a1", Department: "marketing", Title: "ICP doc", Issue: "seo/brand", State: outputs.StateApproved,
			Uses: []outputs.Use{{For: "product", Date: now.Add(-48 * time.Hour), By: "lead-acme-marketing"}, {For: "leads/pitch", Date: now.Add(-60 * 24 * time.Hour)}, {For: "Sales", Date: now.Add(-60 * 24 * time.Hour)}}},
		{ID: "r1", Department: "marketing", Title: "Rejected", Issue: "seo/brand", State: outputs.StateRejected},
	}
	edges := derive(p, []*tracker.Issue{maker, user}, outs, nil)

	toSales := find(edges, "marketing", "sales", Output)
	if toSales == nil || slugs(toSales.Open) != "leads/pitch" || toSales.Open[0].Output != "a1" {
		t.Fatalf("Marketing → Sales output = %+v", toSales)
	}
	if find(edges, "marketing", "sales", Dependency) != nil {
		t.Error("a dependency on a done issue is not drawn")
	}
	toProduct := find(edges, "marketing", Product, Output)
	if toProduct == nil || len(toProduct.Open) != 1 || !strings.Contains(toProduct.Open[0].Why, "recorded by lead-acme-marketing") {
		t.Fatalf("recorded use = %+v", toProduct)
	}
	// The old use by leads/pitch is the same item as the link (kept once,
	// open); the 60-day-old use by Sales itself is folded closed.
	if len(toSales.Open) != 1 || len(toSales.Closed) != 1 || toSales.Closed[0].Slug != "" {
		t.Errorf("old use closed = %+v", toSales.Closed)
	}
	for _, e := range edges {
		for _, it := range e.Open {
			if it.Output == "r1" {
				t.Error("a rejected output feeds nobody")
			}
		}
	}
}

func TestNoDepartmentsNoEdges(t *testing.T) {
	p := &tracker.Project{Slug: "solo"}
	a := issue(t, "ui/a", "status: \"idea\"\nsystem: \"UI\"\ndepends_on: \"ui/b\"", "")
	b := issue(t, "ui/b", "status: \"idea\"\nsystem: \"UI\"", "")
	if edges := derive(p, []*tracker.Issue{a, b}, nil, nil); len(edges) != 0 {
		t.Errorf("Product to Product is no connection: %+v", edges)
	}
}

func TestResolveBox(t *testing.T) {
	p := project(t)
	for in, want := range map[string]string{"HQ": HQ, "product": Product, "marketing": "marketing", "Sales": "sales", "Accounting": "", "": ""} {
		if got := ResolveBox(p, in); got != want {
			t.Errorf("ResolveBox(%q) = %q, want %q", in, got, want)
		}
	}
}
