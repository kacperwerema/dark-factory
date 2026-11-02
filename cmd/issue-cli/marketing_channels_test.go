package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// makeMarketingProject is makeDepartmentProject with Marketing on the
// bundled marketing template (channels as types) instead of content.
func makeMarketingProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := makeDepartmentProject(t)
	wf, err := workflowupgrade.ReadTemplate("marketing")
	if err != nil {
		t.Fatal(err)
	}
	writeDeptFile(t, filepath.Join(proj.Root(), "workflows", "marketing.yaml"), string(wf))
	return proj
}

func TestCreateSeoPassStartsAtItsBrief(t *testing.T) {
	proj := makeMarketingProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runCreate(ctx, []string{"--department", "marketing", "--type", "seo-pass", "--system", "SEO", "--title", "client-a.example: SEO pass"}); err != nil {
		t.Fatal(err)
	}
	issue := loadIssueByPath(t, proj.IssueDir, filepath.Join(proj.IssueDir, "SEO", "client-a-example-seo-pass.md"))
	if issue.Type != "seo-pass" || issue.Status != "brief" || proj.DepartmentOf(issue) != "Marketing" {
		t.Fatalf("issue = type %q status %q department %q\n%s", issue.Type, issue.Status, proj.DepartmentOf(issue), out)
	}
	assertContains(t, out.String(), "Type: seo-pass")

	out.Reset()
	if err := runCreate(ctx, []string{"--department", "marketing", "--system", "SEO", "--title", "One post"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Type: piece (default; also: foundation, market-size, seo-pass")
}

// seoIssueAt writes an issue of type typ at status with every Published box
// ticked, ready to leave publish.
func seoIssueAt(t *testing.T, proj *tracker.Project, typ, status string) string {
	t.Helper()
	path := filepath.Join(proj.IssueDir, "SEO", "pass.md")
	writeDeptFile(t, path, "---\ntitle: \"pass\"\nstatus: \""+status+"\"\nsystem: \"SEO\"\ntype: \""+typ+"\"\n---\n\nA pass.\n\n## Published\n- [x] Published: https://client-a.example/faq\n")
	return path
}

func TestSeoPassMeasuresBeforeDone(t *testing.T) {
	proj := makeMarketingProject(t)
	path := seoIssueAt(t, proj, "seo-pass", "publish")
	ctx, _, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"seo/pass", "--to", "done"}); err == nil || !strings.Contains(err.Error(), "measure") {
		t.Fatalf("seo-pass publish → done: err = %v, want it sent to measure", err)
	}
	if err := runTransition(ctx, []string{"seo/pass", "--to", "measure"}); err != nil {
		t.Fatalf("publish → measure: %v", err)
	}
	issue := loadIssueByPath(t, proj.IssueDir, path)
	if !strings.Contains(issue.BodyRaw, "## Measure") || !strings.Contains(issue.BodyRaw, "Goal values recorded") {
		t.Fatalf("measure should add the Measure boxes:\n%s", issue.BodyRaw)
	}
	if err := runTransition(ctx, []string{"seo/pass", "--to", "done"}); err == nil {
		t.Fatal("measure → done with the Measure boxes open should fail")
	}
	body, _ := os.ReadFile(path)
	writeDeptFile(t, path, strings.ReplaceAll(string(body), "- [ ]", "- [x]"))
	if err := runTransition(ctx, []string{"seo/pass", "--to", "done"}); err != nil {
		t.Fatalf("measure → done: %v", err)
	}
}

func TestPieceSkipsMeasure(t *testing.T) {
	proj := makeMarketingProject(t)
	seoIssueAt(t, proj, "piece", "publish")
	ctx, _, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"seo/pass", "--to", "measure"}); err == nil {
		t.Fatal("a piece has no measure status")
	}
	if err := runTransition(ctx, []string{"seo/pass", "--to", "done"}); err != nil {
		t.Fatalf("piece publish → done: %v", err)
	}
}

// process takes --department in front of its topic's own parse, so the
// channels show with their paths.
func TestProcessWorkflowListsTheDepartmentsChannels(t *testing.T) {
	proj := makeMarketingProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runProcess(ctx, []string{"workflow", "--department", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "seo-pass")
	assertContains(t, out.String(), "publish → measure → done")
}
