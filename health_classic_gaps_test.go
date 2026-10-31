package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/health"
	"github.com/michal-franc/issue-viewer/internal/lint"
)

// TestHealthWbCarriesTheClassicRows: the Workshop Health page shows what
// only the classic one did: the lines-of-code trend with its table,
// decided recommendations, who reviewed and when, every listed lint
// finding with its fix.
func TestHealthWbCarriesTheClassicRows(t *testing.T) {
	now := time.Now()
	proj, root := setupTestProject(t)
	in := healthWbIn{healthIn: healthIn{Proj: &proj, Prefix: "/p/x", Range: "30d", Root: root, Now: now}, Review: "review-x"}
	in.Snaps = []*health.Snapshot{
		healthSnap(now.Add(-48*time.Hour), map[string]float64{"codebase.source_lines": 1000, "codebase.test_ratio": 0.3}),
		healthSnap(now.Add(-time.Hour), map[string]float64{"codebase.source_lines": 1200, "codebase.test_ratio": 0.42}),
	}
	in.Snapshot = in.Snaps[1]
	in.Opinion = &health.Opinion{Date: now.Add(-24 * time.Hour).Format("2006-01-02T15:04"), Score: 70, Assessment: "Fine."}
	classic := buildHealthPage(in.healthIn)
	classic.Review = &ReviewView{Date: "2026-10-30", Reviewer: "review-x", Recs: []RecView{
		{ID: "R1", Title: "Split the store", Proposed: true},
		{ID: "R2", Title: "Drop the old flag", State: "filed", Issue: "ui/flag", IssueHref: "/p/x/issue/ui/flag"},
		{ID: "R3", Title: "Rename things", State: "dismissed", Reason: "not now"},
	}}
	classic.Lint = &LintPanel{Warnings: 60, Command: "issue-cli health lint"}
	for i := 0; i < 50; i++ {
		classic.Lint.Findings = append(classic.Lint.Findings, lint.Finding{Check: "undated", Where: fmt.Sprintf("ui/i%d", i), Message: "no date", Fix: "issue-cli set-meta"})
	}
	p := buildHealthWb(in, classic)

	var loc *WbTrendCard
	for i := range p.Trends {
		if p.Trends[i].Key == "loc" {
			loc = &p.Trends[i]
		}
	}
	if loc == nil || loc.Locked || len(loc.Charts) != 1 || len(loc.Tables) != 1 || !strings.Contains(loc.Sub, "test ratio 0.42") {
		t.Fatalf("loc trend = %+v", loc)
	}
	if len(p.Opinion.DecidedRecs) != 2 || p.Opinion.Reviewer != "review-x" || p.Opinion.Date != "2026-10-30" {
		t.Errorf("opinion = %+v", p.Opinion)
	}
	if len(p.Setup.Rows) != healthSetupRows || len(p.Setup.Rest) != 50-healthSetupRows || p.Setup.More != 10 {
		t.Errorf("lint rows %d, rest %d, more %d", len(p.Setup.Rows), len(p.Setup.Rest), p.Setup.More)
	}

	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	render := func(name string, data any) string {
		var b strings.Builder
		if err := srv.tmpl.ExecuteTemplate(&b, name, data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return b.String()
	}
	p.Opinion.Age = "1 day ago"
	assertContains(t, render("wb/health-recs", p.Opinion), `data-testid="health.decided"`, `filed: <a href="/p/x/issue/ui/flag"`, "dismissed: not now")
	assertContains(t, render("wb/health-opinion", p.Opinion), `data-testid="health.opinion.by"`, "Reviewed 2026-10-30 by")
	assertContains(t, render("wb/health-setup", p.Setup), `id="lint"`, `data-testid="health.lint.rest"`, "45 more warnings", `data-testid="health.lint.fix">· issue-cli set-meta`, "and 10 more warnings")
	assertContains(t, render("wb/trend-card", *loc), `data-testid="health.trend.loc"`, `data-testid="health.chart_table"`)
}
