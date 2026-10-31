package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/connections"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// withConnections adds Marketing (system UI) and two issues that cross it:
// a Marketing issue waiting on Product's loner, and a Product issue
// Marketing asked for, relayed by the CEO.
func withConnections(t *testing.T, a *tracker.Project) {
	t.Helper()
	a.Departments = []tracker.Department{{Name: "Marketing", Systems: []string{"UI"}, Color: "#E2559B"}}
	write := func(rel, content string) {
		path := filepath.Join(a.IssueDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("UI/waits.md", "---\ntitle: \"Waits\"\nstatus: \"backlog\"\ndepends_on: \"loner\"\n---\n\nBody.\n")
	write("ask.md", "---\ntitle: \"Ask\"\nstatus: \"backlog\"\nrequested_by: \"Marketing\"\n---\n\nBody.\n"+
		"<!-- issue-viewer-comments\n{\"id\":1,\"block\":0,\"date\":\"2026-10-30T10:00:00Z\",\"text\":\"approval: \\\"go\\\" → backlog — recorded by lead-agents\",\"status\":\"open\",\"source\":\"cli\"}\n-->\n")
}

func TestCompanyPageListsConnections(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	withConnections(t, &a)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, company := getBody(t, ts.URL+"/p/agents/company")
	assertContains(t, company,
		`data-testid="company.connections"`,
		`data-from="marketing" data-to="product" data-via="hq" data-kind="request" data-open="1"`,
		`<b>Marketing</b> → HQ → <b>Product</b>`,
		`data-from="product" data-to="marketing" data-via="" data-kind="dependency" data-open="1"`,
		`href="/p/agents/connections?from=product&amp;kind=dependency&amp;to=marketing"`,
		`data-box="hq"`, `data-box="product"`, `data-box="marketing"`,
		`⇄ in 1 · out 1`,
		`/static/connections.js`)
	if strings.Contains(company, `company.connections.empty`) {
		t.Error("lines and the empty state together")
	}
}

func TestCompanyPageConnectionsEmptyAndNone(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{a})
	_, company := getBody(t, ts.URL+"/p/agents/company")
	ts.Close()
	if strings.Contains(company, `data-testid="company.connections"`) {
		t.Error("no departments, no Connections block")
	}

	a.Departments = []tracker.Department{{Name: "Marketing", Systems: []string{"SEO"}}}
	ts = newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, company = getBody(t, ts.URL+"/p/agents/company")
	assertContains(t, company, `data-testid="company.connections.empty"`, `No connections yet`, `issue-cli output use`)
	_, page := getBody(t, ts.URL+"/p/agents/connections")
	assertContains(t, page, `data-testid="connections.empty"`, `No connections yet`)
}

func TestConnectionsPageFiltersAndJSON(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	withConnections(t, &a)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	_, page := getBody(t, ts.URL+"/p/agents/connections")
	assertContains(t, page, `data-testid="connections.page"`, `Marketing → HQ → Product · 1 request`,
		`Product → Marketing · 1 dependency`, `href="/p/agents/issue/ui/waits"`, `depends_on loner`, `requested_by: Marketing`)

	_, page = getBody(t, ts.URL+"/p/agents/connections?kind=dependency")
	assertContains(t, page, `Product → Marketing · 1 dependency`)
	if strings.Contains(page, `1 request</h2>`) || strings.Contains(page, `requested_by: Marketing`) {
		t.Error("the kind filter kept a request")
	}
	_, page = getBody(t, ts.URL+"/p/agents/connections?to=hq&kind=dependency")
	assertContains(t, page, `No connections match these filters.`)

	_, body := getBody(t, ts.URL+"/p/agents/connections.json?from=marketing")
	var edges []connections.Edge
	if err := json.Unmarshal([]byte(body), &edges); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if len(edges) != 1 || edges[0].Kind != connections.Request || edges[0].Via != connections.HQ || edges[0].Open[0].Slug != "ask" {
		t.Errorf("json = %+v", edges)
	}

	_, home := getBody(t, ts.URL+"/")
	assertContains(t, home, `data-testid="chip.connections"`, `⇄ 2`)
}

func TestCompanyPageMarksDeclaredConnections(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	withConnections(t, &a)
	// Marketing → Product's request is traffic off the routes; the declared
	// request back and the feed carry nothing.
	a.Connections = &tracker.Connections{
		Requests: []tracker.Route{{From: "product", To: "marketing"}},
		Feeds:    []tracker.Route{{From: "marketing", To: "product", What: "landing copy"}},
	}
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, company := getBody(t, ts.URL+"/p/agents/company")
	assertContains(t, company,
		`against the routes declared in projects.yaml`,
		`data-from="marketing" data-to="product" data-via="hq" data-kind="request" data-open="1" data-color="#E2559B" data-route="undeclared"`,
		`<span class="wb-conn-mark wb-conn-mark--undeclared" data-testid="company.connections.mark">not declared</span>`,
		`data-from="product" data-to="marketing" data-via="" data-kind="request" data-open="0" data-color="#3D7BF7" data-route="unused"`,
		`data-kind="output" data-open="0"`,
		`<span class="wb-muted">landing copy</span>`,
		`no traffic</span>`,
		`data-kind="dependency" data-open="1" data-color="#3D7BF7" data-route="">`)

	_, page := getBody(t, ts.URL+"/p/agents/connections")
	assertContains(t, page, `marked against projects.yaml connections:`,
		`data-testid="connections.edge.mark">not declared</span>`,
		`data-testid="connections.edge.unused"`,
		`data-testid="connections.legend.declared"`,
		`Product → Marketing · request · declared, no traffic`)

	_, body := getBody(t, ts.URL+"/p/agents/connections.json?kind=request")
	var edges []connections.Edge
	if err := json.Unmarshal([]byte(body), &edges); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	routes := map[string]string{}
	for _, e := range edges {
		routes[e.From+">"+e.To] = e.Route
	}
	if routes["marketing>product"] != connections.RouteUndeclared || routes["product>marketing"] != connections.RouteUnused {
		t.Errorf("json routes = %v", routes)
	}
}
