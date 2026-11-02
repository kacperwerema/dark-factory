package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// routedProject is goalsProject with GTM over Marketing and Sales (system
// Leads) and routes: Marketing → Product via HQ, Sales → Marketing via GTM.
func routedProject(t *testing.T) *tracker.Project {
	proj := goalsProject(t)
	proj.Departments[0].Parent = "GTM"
	proj.Departments = append(proj.Departments,
		tracker.Department{Name: "GTM"},
		tracker.Department{Name: "Sales", Systems: []string{"Leads"}, Parent: "GTM"})
	proj.Connections = &tracker.Connections{
		Requests: []tracker.Route{{From: "marketing", To: "product", Via: "hq"}, {From: "sales", To: "marketing", Via: "gtm"}},
		Feeds:    []tracker.Route{{From: "marketing", To: "sales"}},
	}
	return proj
}

func asLeadOf(t *testing.T, dept string) {
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", tracker.LeadScopeDepartmentPrefix+dept)
}

func TestCreateOnADeclaredRouteNamesTheRelay(t *testing.T) {
	asDepartmentLead(t)
	proj := routedProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runCreate(ctx, []string{"--title", "Engine brand fields", "--system", "Factory"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, createdFile(t, out.String()), "requested_by: \"Marketing\"")
	assertContains(t, out.String(), "Route: Marketing → HQ → Product (projects.yaml connections:): lead-acme forwards it")
}

func TestCreateOffTheRoutesIsRefused(t *testing.T) {
	asLeadOf(t, "sales")
	proj := routedProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	err := runCreate(ctx, []string{"--title", "CRM fields", "--system", "Factory"})
	if err == nil {
		t.Fatal("Sales → Product is not declared: refused")
	}
	for _, want := range []string{"Sales → Product is not a declared request route", "Sales may ask: Marketing via GTM.", "- {from: sales, to: product, via: hq}"} {
		assertContains(t, err.Error(), want)
	}
	if matches, _ := filepath.Glob(filepath.Join(proj.IssueDir, "Factory", "*.md")); len(matches) != 0 {
		t.Errorf("a refused create writes nothing: %v", matches)
	}

	// The CEO files it for Sales.
	asLead(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runCreate(ctx, []string{"--title", "CRM fields", "--system", "Factory", "--requested-by", "sales"}); err != nil {
		t.Fatalf("the CEO routes freely: %v", err)
	}
	assertContains(t, createdFile(t, out.String()), "requested_by: \"Sales\"")
}

func TestSetMetaRequestedByOffTheRoutes(t *testing.T) {
	asLeadOf(t, "sales")
	proj := routedProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	err := runSetMeta(ctx, []string{"engine", "--key", "requested_by", "--value", "sales"})
	if err == nil || !strings.Contains(err.Error(), "set-meta: Sales → Product is not a declared request route") {
		t.Errorf("set-meta off the routes: %v", err)
	}
}

func TestDispatchWaitsForTheRelay(t *testing.T) {
	proj := routedProject(t)
	mustWrite(t, filepath.Join(proj.IssueDir, "call-list.md"), "---\ntitle: \"Call list\"\nstatus: \"idea\"\nsystem: \"SEO\"\nrequested_by: \"Sales\"\n---\nBody.\n")
	asDepartmentLead(t)
	ctx, _ := goalsCtx(t, proj, false)
	err := runDispatch(ctx, []string{"call-list", "--quote", "go"})
	if err == nil {
		t.Fatal("Marketing's lead dispatching a Sales request declared via GTM before GTM forwards it")
	}
	for _, want := range []string{"call-list is a request Sales → GTM → Marketing", "lead-acme-gtm forwards it first", `issue-cli comment call-list "question: forward this?"`} {
		assertContains(t, err.Error(), want)
	}

	issue := callList(t, proj)
	if err := tracker.AddComment(issue.FilePath, 0, "approval: \"ok\" → backlog — recorded by lead-acme-gtm", ""); err != nil {
		t.Fatal(err)
	}
	if got := relayRefusal(proj, whoAmI(proj, nil), callList(t, proj)); got != "" {
		t.Errorf("GTM's lead forwarded it: %q", got)
	}
}

func TestOutputUseOffTheFeeds(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	proj.Connections = &tracker.Connections{Feeds: []tracker.Route{{From: "marketing", To: "sales"}}}
	o := addOutput(t, proj, "--issue", "seo-pass", "--title", "ICP", "--kind", "report", "--url", "https://acme.example/icp")
	ctx, out := goalsCtx(t, proj, false)
	if err := runOutput(ctx, []string{"use", shortID(o.ID), "--by", "engine"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "No declared feed Marketing → Product (projects.yaml connections: feeds:)")
}

func TestConnectionsCommandMarksDeclared(t *testing.T) {
	asLead(t)
	proj := routedProject(t)
	mustWrite(t, filepath.Join(proj.IssueDir, "ask.md"), "---\ntitle: \"Ask\"\nstatus: \"idea\"\nrequested_by: \"Sales\"\n---\nBody.\n")
	ctx, out := goalsCtx(t, proj, false)
	if err := runConnections(ctx, nil); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "Sales → Product · 1 request · not declared")
	assertContains(t, s, "Marketing → HQ → Product · request · declared, no traffic")
	assertContains(t, s, "Marketing → Sales · output · declared, no traffic")
	assertContains(t, s, "Declared in projects.yaml connections: 3")
}

func callList(t *testing.T, proj *tracker.Project) *tracker.Issue {
	t.Helper()
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	issue := tracker.FindIssue(issues, "call-list")
	if issue == nil {
		t.Fatal("no call-list issue")
	}
	return issue
}
