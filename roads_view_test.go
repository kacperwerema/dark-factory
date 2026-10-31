package main

import (
	"encoding/json"
	"html"
	"regexp"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/connections"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func roadsProject() *tracker.Project {
	return &tracker.Project{Name: "Acme", Departments: []tracker.Department{
		{Name: "GTM", Color: "#F2A541"},
		{Name: "Marketing", Color: "#E2559B", Parent: "GTM"},
		{Name: "Sales", Color: "#17A2B8", Parent: "GTM"},
		{Name: "Design", Color: "#E25555"},
	}}
}

func TestCompanyRoadsFoldPairsAndRelays(t *testing.T) {
	p := roadsProject()
	open := func(n int, state string) []connections.Item {
		var out []connections.Item
		for range n {
			out = append(out, connections.Item{Slug: "x", State: state})
		}
		return out
	}
	edges := []connections.Edge{
		// Relayed by the CEO: two roads, through HQ.
		{From: "marketing", To: "product", Kind: connections.Request, Via: connections.HQ, Open: open(1, "waiting on HQ to forward")},
		// Relayed by GTM, which holds both: one road.
		{From: "sales", To: "marketing", Kind: connections.Request, Via: "gtm", Open: open(1, "at backlog")},
		// Both ways on one pair: one road, two flows.
		{From: "product", To: "marketing", Kind: connections.Dependency, Open: open(2, "")},
		{From: "design", To: "marketing", Kind: connections.Output, Open: open(1, ""), Route: connections.RouteUndeclared},
		{From: "marketing", To: "design", Kind: connections.Request, Open: open(1, ""), Route: connections.RouteDeclared},
		// Declared, no traffic: a quiet road.
		{From: "design", To: connections.HQ, Kind: connections.Output, Route: connections.RouteUnused},
	}
	roads := companyRoads(p, "/p/acme", edges, boxColors(p))
	got := map[string]WbRoad{}
	for _, r := range roads {
		got[r.A+"-"+r.B] = r
	}
	if len(roads) != 6 {
		t.Fatalf("roads = %d, want 6: %+v", len(roads), roads)
	}
	for _, k := range []string{"hq-marketing", "hq-product", "marketing-sales", "product-marketing", "marketing-design", "hq-design"} {
		if _, ok := got[k]; !ok {
			t.Errorf("no road %s in %v", k, got)
		}
	}
	if r := got["hq-marketing"]; r.Label != "HQ ⇄ Marketing: 1 handoff open" || r.Traffic[0].Dir != "ba" || r.Traffic[0].Kind != parcelHandoff {
		t.Errorf("relay leg = %+v", r)
	}
	if r := got["marketing-sales"]; r.Label != "Marketing ⇄ Sales: 1 handoff open" || r.URL != "/p/acme/connections?from=sales&to=marketing" {
		t.Errorf("group relay = %+v", r)
	}
	if r := got["product-marketing"]; r.Label != "Product ⇄ Marketing: 2 dependencies open" || r.Open != 2 {
		t.Errorf("dependency road = %+v", r)
	}
	r := got["marketing-design"]
	if r.Route != connections.RouteUndeclared || len(r.Traffic) != 2 || len(r.Rows) != 2 {
		t.Errorf("two-way road = %+v", r)
	}
	if r.Label != "Marketing ⇄ Design: 1 request, 1 output open · not declared" {
		t.Errorf("label = %q", r.Label)
	}
	if r := got["hq-design"]; r.Route != "quiet" || r.Open != 0 || r.Label != "HQ ⇄ Design: declared, no traffic yet" {
		t.Errorf("quiet road = %+v", r)
	}
}

func TestCompanyPageCarriesRoads(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	withConnections(t, &a)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, company := getBody(t, ts.URL+"/p/agents/company")
	assertContains(t, company, `<template data-road-parts>`, `data-testid="company.connections.road"`, `data-testid="company.connections.parcel"`)
	m := regexp.MustCompile(`(?s)<script type="application/json" data-roads>(.*?)</script>`).FindStringSubmatch(company)
	if m == nil {
		t.Fatal("no roads data on the page")
	}
	var roads []WbRoad
	if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &roads); err != nil {
		t.Fatalf("roads data: %v: %s", err, m[1])
	}
	// Marketing → HQ → Product (relayed) and Product → Marketing
	// (dependency): HQ ⇄ Marketing, HQ ⇄ Product, Product ⇄ Marketing.
	if len(roads) != 3 {
		t.Errorf("roads = %+v, want 3", roads)
	}
}
