package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
	"gopkg.in/yaml.v3"
)

// bundledMarketing is the bundled marketing template, parsed.
func bundledMarketing(t *testing.T) *tracker.WorkflowConfig {
	t.Helper()
	raw, err := workflowupgrade.ReadTemplate("marketing")
	if err != nil {
		t.Fatal(err)
	}
	var wf tracker.WorkflowConfig
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	return &wf
}

// statusPrompt is the prompt status has on typ's path.
func statusPrompt(t *testing.T, wf *tracker.WorkflowConfig, typ, status string) string {
	t.Helper()
	s := wf.ForType(typ).GetStatus(status)
	if s == nil {
		t.Fatalf("%s has no status %q", typ, status)
	}
	return s.Prompt
}

// The competitor scan walks research → competitors → owner review →
// publish → done: research needs its sources and the brand foundation,
// competitors its boxes and the report on the shelf, publish the owner's
// approval of the newest shelf version (owner_only, so not even YOLO), and
// done needs competitors.md written. Its prompts keep it to web search,
// name the file's four sections and leave the foundation's files alone.
func TestCompetitorScanTypeShape(t *testing.T) {
	wf := bundledMarketing(t)
	typ, ok := wf.Types["competitor-scan"]
	if !ok {
		t.Fatal("the marketing template has no competitor-scan type")
	}
	if got := strings.Join(typ.Path, " → "); got != "research → competitors → owner review → publish → done" {
		t.Errorf("path = %s", got)
	}
	assertContains(t, typ.Description, "(system SEO)")

	research := statusPrompt(t, wf, "competitor-scan", "research")
	for _, want := range []string{"brand.md and icp.md first", "Web search only", "No paid data: no `openseo` call", "no log-in"} {
		assertContains(t, research, want)
	}
	heavy, err := os.ReadFile(filepath.Join("templates", "departments", "_shared", "heavy-tools.md"))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, research, strings.TrimSpace(string(heavy)))

	competitors := statusPrompt(t, wf, "competitor-scan", "competitors")
	for _, want := range []string{"### Direct", "### Adjacent", "### Queries", "### Topic gap", "### Method and limits", "--kind report"} {
		assertContains(t, competitors, want)
	}
	publish := statusPrompt(t, wf, "competitor-scan", "publish")
	for _, want := range []string{"departments/<department>/competitors.md", "## Direct", "## Adjacent", "## Queries", "## Baseline", "Don't change brand.md, icp.md or keywords.md", "publish nothing outside the machine"} {
		assertContains(t, publish, want)
	}
	// Nothing in the type calls out: no connector, no paid call.
	for _, s := range []string{"research", "competitors", "owner review", "publish"} {
		p := statusPrompt(t, wf, "competitor-scan", s)
		for _, bad := range []string{"connector exec", "maxCostUsd", "--url <"} {
			if strings.Contains(p, bad) {
				t.Errorf("%s prompt names %q: the scan reads web search only and publishes nothing", s, bad)
			}
		}
	}

	scoped := wf.ForType("competitor-scan")
	var gate *tracker.WorkflowTransition
	for i, tr := range scoped.Transitions {
		if tr.From == "owner review" && tr.To == "publish" {
			gate = &scoped.Transitions[i]
		}
	}
	if gate == nil {
		t.Fatal("no owner review → publish edge")
	}
	var ownerOnly, approved bool
	for _, a := range gate.Actions {
		ownerOnly = ownerOnly || (a.Type == "require_human_approval" && a.OwnerOnly)
		approved = approved || (a.Type == "validate" && a.Rule == "outputs_approved: report")
	}
	if !ownerOnly || !approved {
		t.Errorf("owner review → publish: owner_only %v, outputs_approved: report %v; want both", ownerOnly, approved)
	}

	found := false
	for _, e := range wf.Evals {
		if e.ID == "competitor-scan" && strings.Join(e.Types, ",") == "competitor-scan" {
			found = true
		}
	}
	if !found {
		t.Error("no competitor-scan eval scores the type's runs")
	}
}

// competitorScanReport puts a competitor scan report for slug on the shelf.
func competitorScanReport(t *testing.T, proj *tracker.Project, slug string) *outputs.Output {
	t.Helper()
	src := filepath.Join(t.TempDir(), "scan.md")
	writeDeptFile(t, src, "# Competitor scan: plumbing\n\n## Proposed competitors.md\n")
	o, err := outputs.Add(proj, outputs.AddInput{Title: "Competitor scan: plumbing", Kind: "report", File: src, Issue: slug, Department: "marketing", By: "agent"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestCompetitorScanWalksToCompetitorsMD(t *testing.T) {
	proj := makeMarketingProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runCreate(ctx, []string{"--department", "marketing", "--type", "competitor-scan", "--system", "SEO", "--title", "Competitor scan: plumbing"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj.IssueDir, "SEO", "competitor-scan-plumbing.md")
	issue := loadIssueByPath(t, proj.IssueDir, path)
	if issue.Type != "competitor-scan" || issue.Status != "research" || proj.DepartmentOf(issue) != "Marketing" {
		t.Fatalf("new scan: type %q status %q department %q\n%s", issue.Type, issue.Status, proj.DepartmentOf(issue), out)
	}
	slug := issue.Slug

	body, _ := os.ReadFile(path)
	writeDeptFile(t, path, string(body)+"\n## Research\n\n"+researchLinks(5))
	err := runTransition(ctx, []string{slug, "--to", "competitors"})
	if err == nil || !strings.Contains(err.Error(), "brand.md is missing") || !strings.Contains(err.Error(), "--type foundation") {
		t.Fatalf("research → competitors before the brand foundation: err = %v", err)
	}
	writeFoundationDocs(t, proj, "brand.md", "icp.md")
	if err := runTransition(ctx, []string{slug, "--to", "competitors"}); err != nil {
		t.Fatalf("research → competitors: %v", err)
	}
	assertContains(t, loadIssueByPath(t, proj.IssueDir, path).BodyRaw, "## Competitors")

	tickAll(t, path)
	if err := runTransition(ctx, []string{slug, "--to", "owner review"}); err == nil || !strings.Contains(err.Error(), "Competitor scan: <topic>") {
		t.Fatalf("competitors → owner review with no report on the shelf: err = %v", err)
	}
	rep := competitorScanReport(t, proj, slug)
	if err := runTransition(ctx, []string{slug, "--to", "owner review"}); err != nil {
		t.Fatalf("competitors → owner review: %v", err)
	}

	if _, err := outputs.Review(proj, rep.ID, outputs.StateApproved, "settled", "", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err == nil || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("owner review → publish without the owner's approval: err = %v", err)
	}
	body, _ = os.ReadFile(path)
	writeDeptFile(t, path, strings.Replace(string(body), "status: \"owner review\"", "status: \"owner review\"\nhuman_approval: \"publish\"", 1))
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err != nil {
		t.Fatalf("approved owner review → publish: %v", err)
	}
	assertContains(t, loadIssueByPath(t, proj.IssueDir, path).BodyRaw, "competitors.md holds the approved text")
	tickAll(t, path)
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err == nil || !strings.Contains(err.Error(), "competitors.md is missing") {
		t.Fatalf("publish → done before competitors.md: err = %v", err)
	}
	writeFoundationDocs(t, proj, "competitors.md")
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err != nil {
		t.Fatalf("publish → done: %v", err)
	}
}

// The weekly SEO pass (the seo-pass brief and the job's own brief) reads
// competitors.md where it exists and says so where it doesn't, with the
// command that writes it.
func TestSEOBriefsReadCompetitors(t *testing.T) {
	wf := bundledMarketing(t)
	brief := statusPrompt(t, wf, "seo-pass", "brief")
	job, err := os.ReadFile(filepath.Join("templates", "departments", "marketing", "jobs", "weekly-seo-pass.md"))
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"seo-pass brief prompt": brief, "jobs/weekly-seo-pass.md": string(job)} {
		for _, want := range []string{"competitors.md", "## Queries", "## Direct", "## Baseline", "No competitors.md yet", "--type competitor-scan --system SEO"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s doesn't say %q", name, want)
			}
		}
	}
}
