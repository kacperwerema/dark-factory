package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

const pageRulesBox = "- [ ] Page rules: <title> at most 60 characters with the brand suffix, primary phrase first, and the page served with charset=utf-8; checked by issue-cli page-check <url>"

// TestMarketingTemplateHasThePageRules is AC3: a freshly added Marketing
// has the rule in role.md, the weekly SEO pass runs page-check on each
// sitemap and opens one fix per site, and the base Draft and the
// seo-baseline's Technical basics each have a page-check box.
func TestMarketingTemplateHasThePageRules(t *testing.T) {
	proj := installMarketingWith(t, marketingWorkflow(t))
	dept := filepath.Join(proj.Root(), "departments", "marketing")
	role, err := os.ReadFile(filepath.Join(dept, "role.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Page rules", "`<title>` at most 60 characters including the brand suffix", "primary phrase first", "charset=utf-8", "Content-Type header", "`issue-cli page-check <url>`", "<site>/sitemap.xml"} {
		assertContains(t, string(role), want)
	}
	job, err := os.ReadFile(filepath.Join(dept, "jobs", "weekly-seo-pass.md"))
	if err != nil {
		// The job brief is read from the template when not written out.
		job, err = os.ReadFile(filepath.Join("templates", "departments", "marketing", "jobs", "weekly-seo-pass.md"))
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"`issue-cli page-check <site>/sitemap.xml`", "one fix for the site listing every failing URL", "unless one is already open", `--title "<site>: page rules (title length, charset)"`} {
		assertContains(t, string(job), want)
	}

	wf, _ := marketingTemplate(t, proj)
	var draft string
	for _, tr := range wf.Transitions {
		if tr.From == "brief" && tr.To == "draft" {
			for _, a := range tr.Actions {
				if a.Title == "Draft" {
					draft = a.Body
				}
			}
		}
	}
	assertContains(t, draft, pageRulesBox)
	tech := seoBaselineSections(t, wf.Types["seo-baseline"])["Technical basics"]
	if !strings.Contains(strings.Join(tech, "\n"), "charset=utf-8, and the article template keeps <title> at most 60 characters with the brand suffix, primary phrase first; checked by issue-cli page-check <site>/sitemap.xml") {
		t.Errorf("seo-baseline Technical basics has no page-rules box:\n%s", strings.Join(tech, "\n"))
	}
}

// TestPieceWaitsForThePageRulesBox is AC4: a piece can't move draft →
// owner review while the page-rules box is open, and can once it's ticked;
// owner review → publish stays the one owner_only gate.
func TestPieceWaitsForThePageRulesBox(t *testing.T) {
	proj := installMarketingWith(t, marketingWorkflow(t))
	ctx, out, _ := newTestContext(proj, false)
	if err := runCreate(ctx, []string{"--department", "marketing", "--type", "piece", "--title", "Weekly meal plan guide"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj.IssueDir, "weekly-meal-plan-guide.md")
	slug := loadIssueByPath(t, proj.IssueDir, path).Slug
	appendBody(t, path, "A guide to planning a week of dinners, for busy parents, on the site.\n")
	if err := runTransition(ctx, []string{slug, "--to", "draft"}); err != nil {
		t.Fatalf("brief → draft: %v\n%s", err, out)
	}
	body, _ := os.ReadFile(path)
	assertContains(t, string(body), pageRulesBox)
	// Every Draft box but the page rules ticked.
	lines := strings.Split(string(body), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "- [ ] ") && !strings.Contains(l, "Page rules:") {
			lines[i] = strings.Replace(l, "- [ ]", "- [x]", 1)
		}
	}
	writeDeptFile(t, path, strings.Join(lines, "\n"))
	err := runTransition(ctx, []string{slug, "--to", "owner review"})
	if err == nil || !strings.Contains(err.Error(), "Draft") {
		t.Fatalf("draft → owner review with the page-rules box open: err = %v", err)
	}
	tickSections(t, path, "Draft")
	if err := runTransition(ctx, []string{slug, "--to", "owner review"}); err != nil {
		t.Fatalf("draft → owner review with the page rules ticked: %v", err)
	}
	assertChannelPath(t, loadMarketingWorkflow(t), "piece", piecePath)
	assertMarketingLintClean(t, proj, "with the page-rules boxes")
}

// TestInstalledMarketingGetsThePageRules is AC5: department update replaces
// an unedited role.md and weekly-seo-pass.md from before the page rules,
// and workflow upgrade proposes page-rules to a workflow file without it.
func TestInstalledMarketingGetsThePageRules(t *testing.T) {
	proj := installMarketingWith(t, marketingWorkflow(t))
	dept := filepath.Join(proj.Root(), "departments", "marketing")
	earlier := filepath.Join("..", "..", "internal", "depttemplate", "testdata", "earlier", "marketing")
	installed := map[string]string{
		"role.md":                 filepath.Join(earlier, "role.md.7"),
		"jobs/weekly-seo-pass.md": filepath.Join(earlier, "jobs", "weekly-seo-pass.md.7"),
	}
	for dst, src := range installed {
		old, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(old), "page-check") {
			t.Fatalf("%s already has the page rules: not the text from before them", src)
		}
		mustWrite(t, filepath.Join(dept, dst), string(old))
	}
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"update", "marketing"}); err != nil {
		t.Fatalf("department update: %v\n%s", err, out)
	}
	for dst := range installed {
		b, err := os.ReadFile(filepath.Join(dept, dst))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "issue-cli page-check") {
			t.Errorf("department update left %s without the page rules:\n%s", dst, out)
		}
	}

	// The workflow boxes come through workflow upgrade's page-rules entry.
	text := marketingWorkflow(t)
	for _, cut := range []string{
		"\n" + `          - [ ] Page rules: <title> at most 60 characters with the brand suffix, primary phrase first, and the page served with charset=utf-8; checked by issue-cli page-check <url> (or "no page yet" when it has no URL before publish)`,
		"\n" + `              - [ ] Page rules: every text response (HTML, sitemap.xml, robots.txt, llms.txt) sends Content-Type with charset=utf-8, and the article template keeps <title> at most 60 characters with the brand suffix, primary phrase first; checked by issue-cli page-check <site>/sitemap.xml`,
		"\n    - page-rules@v0.65.0",
	} {
		if !strings.Contains(text, cut) {
			t.Fatalf("marketing template lacks %q; update this test", cut)
		}
		text = strings.Replace(text, cut, "", 1)
	}
	plan, err := workflowupgrade.BuildPlan("workflows/marketing.yaml", []byte(text), "", false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range plan.Proposals {
		found = found || p.ID == "page-rules"
	}
	if !found {
		t.Errorf("workflow upgrade should propose page-rules to a Marketing without it, got %v", plan.Proposals)
	}
}
