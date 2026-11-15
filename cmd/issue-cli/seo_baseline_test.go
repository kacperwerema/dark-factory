package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"gopkg.in/yaml.v3"
)

// The seo-baseline sections brief → draft appends: the four the agent ticks,
// each box saying what the site must have and what checks it, and the
// owner's.
var (
	seoBaselineAgentSections = []string{"Technical basics", "Waitlist", "Pre-rendered pages", "Measurement"}
	seoBaselineOwnerSection  = "Owner steps"
)

// marketingTemplate is the bundled marketing workflow as Marketing installs it.
func marketingTemplate(t *testing.T, proj *tracker.Project) (tracker.WorkflowConfig, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(proj.Root(), "workflows", "marketing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf tracker.WorkflowConfig
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	return wf, string(raw)
}

// seoBaselineSections is each section brief → draft appends on the
// seo-baseline path, with its boxes.
func seoBaselineSections(t *testing.T, typ tracker.WorkflowType) map[string][]string {
	t.Helper()
	got := map[string][]string{}
	for _, tr := range typ.Transitions {
		if tr.From != "brief" || tr.To != "draft" {
			continue
		}
		for _, a := range tr.Actions {
			if a.Type != "append_section" {
				continue
			}
			for _, it := range tracker.ListCheckboxes(a.Body) {
				got[a.Title] = append(got[a.Title], it.Label())
			}
		}
	}
	return got
}

// AC1: the bundled template's seo-baseline type carries the four items, each
// box naming what the site must have and what checks it, and its brief →
// draft doesn't wait for brand.md (technical, not content).
func TestSeoBaselineTemplate(t *testing.T) {
	proj := makeMarketingProject(t)
	wf, raw := marketingTemplate(t, proj)
	typ, ok := wf.Types["seo-baseline"]
	if !ok {
		t.Fatal("marketing template has no seo-baseline type")
	}
	if got := strings.Join(typ.Path, " → "); got != "brief → draft → owner review → publish → measure → done" {
		t.Errorf("path = %s", got)
	}
	sections := seoBaselineSections(t, typ)
	for _, name := range seoBaselineAgentSections {
		boxes := sections[name]
		if len(boxes) == 0 {
			t.Errorf("brief → draft appends no ## %s", name)
		}
		for _, b := range boxes {
			if !strings.Contains(b, "checked by") {
				t.Errorf("## %s box says what the site must have but not what checks it: %q", name, b)
			}
		}
	}
	for _, want := range []string{"sitemap.xml", "robots.txt", "JSON-LD", "Open Graph image 1200×630", "llms.txt", "X-Robots-Tag: noindex",
		"server-side", "consent", "unsubscribe", "honeypot", "static site generator", "article template", "IndexNow", "bots check", "URL Inspection"} {
		found := false
		for _, name := range seoBaselineAgentSections {
			for _, b := range sections[name] {
				found = found || strings.Contains(b, want)
			}
		}
		if !found {
			t.Errorf("no baseline box names %q", want)
		}
	}

	// The gates: the four sections and the change set on the shelf before
	// review; the base owner review → publish stays owner_only.
	var gates []string
	for _, tr := range typ.Transitions {
		for _, a := range tr.Actions {
			if a.Type == "validate" {
				gates = append(gates, tr.From+" → "+tr.To+": "+a.Rule)
			}
		}
	}
	all := strings.Join(gates, "\n")
	for _, want := range []string{"brief → draft: has_section", "draft → owner review: section_checkboxes_checked: Technical basics",
		"draft → owner review: section_checkboxes_checked: Waitlist", "draft → owner review: section_checkboxes_checked: Pre-rendered pages",
		"draft → owner review: section_checkboxes_checked: Measurement", "draft → owner review: has_output: site",
		"publish → measure: section_checkboxes_checked: Owner steps"} {
		assertContains(t, all, want)
	}
	if strings.Contains(all, "file_exists") {
		t.Errorf("the baseline is technical, not content: no brand.md gate on its path\n%s", all)
	}
	ownerOnly := false
	for _, tr := range wf.Transitions {
		if tr.From == "owner review" && tr.To == "publish" {
			for _, a := range tr.Actions {
				ownerOnly = ownerOnly || (a.Type == "require_human_approval" && a.OwnerOnly)
			}
		}
	}
	for _, tr := range typ.Transitions {
		if tr.From == "owner review" && tr.To == "publish" {
			t.Errorf("seo-baseline must keep the base owner review → publish gate, not its own")
		}
	}
	if !ownerOnly {
		t.Error("owner review → publish must be owner_only: deploying publishes outside the machine")
	}

	// An eval scores its runs, and the walk-through the prompts name exists.
	evalFound := false
	for _, e := range wf.Evals {
		for _, ty := range e.Types {
			evalFound = evalFound || ty == "seo-baseline"
		}
	}
	if !evalFound {
		t.Error("no eval scores seo-baseline")
	}
	if _, err := os.Stat(filepath.Join("..", "..", "docs", "seo-baseline.md")); err != nil {
		t.Errorf("the walk-through the template names: %v", err)
	}

	// Generic: the type's text names no real site, only placeholders.
	_, block, _ := strings.Cut(raw, "\n  seo-baseline:\n")
	block, _, _ = strings.Cut(block, "\n  # More channels:")
	if m := regexp.MustCompile(`\b[a-z0-9-]+\.(app|com|pl|io|net|org|dev|de|co)\b`).FindAllString(block, -1); len(m) > 0 {
		t.Errorf("seo-baseline names real domains (keep it generic): %v", m)
	}

	// Channel briefs still wait for the brand foundation.
	seoIssueAt(t, proj, "seo-pass", "brief")
	ctx, _, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"seo/pass", "--to", "draft"}); err == nil || !strings.Contains(err.Error(), "brand.md") {
		t.Fatalf("an seo-pass brief with no brand.md: err = %v", err)
	}
}

// AC2: account, DNS, CDN and deploy steps are owner steps: every Owner steps
// box is owner-marked, those words appear in no box an agent ticks, and a
// worker's tick is refused.
func TestSeoBaselineOwnerSteps(t *testing.T) {
	proj := makeMarketingProject(t)
	wf, _ := marketingTemplate(t, proj)
	sections := seoBaselineSections(t, wf.Types["seo-baseline"])
	owner := sections[seoBaselineOwnerSection]
	joined := strings.Join(owner, "\n")
	for _, want := range []string{"Search Console", "Domain property", "DNS", "service account", "Bing Webmaster Tools", "Cloudflare", "deploy"} {
		assertContains(t, joined, want)
	}
	for _, b := range owner {
		if !tracker.IsOwnerMarked(b) {
			t.Errorf("Owner steps box isn't owner-marked: %q", b)
		}
	}
	for _, name := range seoBaselineAgentSections {
		for _, b := range sections[name] {
			if tracker.IsOwnerMarked(b) || tracker.LooksLikeOwnerCheck(b) {
				t.Errorf("## %s box reads as the owner's: %q", name, b)
			}
			for _, w := range []string{"account", "DNS", "Cloudflare", "CDN", "log in"} {
				if strings.Contains(b, w) {
					t.Errorf("## %s box an agent ticks names %q: %q", name, w, b)
				}
			}
		}
	}

	slug, path := seoBaselineAtDraft(t, proj)
	data, _ := os.ReadFile(path)
	asWorker(t)
	ctx, _, _ := newTestContext(proj, false)
	err := runCheck(ctx, []string{slug, "--section", seoBaselineOwnerSection, "--all"})
	if err == nil || !strings.Contains(err.Error(), "these boxes are the owner's") {
		t.Fatalf("a worker ticking the owner's steps: err = %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(data) {
		t.Fatalf("a refused tick changed the issue:\n%s", after)
	}
}

// seoBaselineAtDraft creates an seo-baseline issue in Marketing, with no
// brand.md, and moves it to draft.
func seoBaselineAtDraft(t *testing.T, proj *tracker.Project) (string, string) {
	t.Helper()
	ctx, out, _ := newTestContext(proj, false)
	if err := runCreate(ctx, []string{"--department", "marketing", "--type", "seo-baseline", "--title", "Site A: SEO baseline"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj.IssueDir, "site-a-seo-baseline.md")
	issue := loadIssueByPath(t, proj.IssueDir, path)
	if issue.Type != "seo-baseline" || issue.Status != "brief" {
		t.Fatalf("new seo-baseline: type %q status %q\n%s", issue.Type, issue.Status, out)
	}
	if err := runTransition(ctx, []string{issue.Slug, "--to", "draft"}); err == nil || !strings.Contains(err.Error(), "Owner answers") {
		t.Fatalf("brief → draft with no ## Owner answers: err = %v", err)
	}
	appendBody(t, path, "## Owner answers\n1. A waitlist.\n2. One country, one language.\n3. Two posts a week; the owner approves each.\n4. The technical base and measurement.\n5. Search and training bots.\n")
	if err := runTransition(ctx, []string{issue.Slug, "--to", "draft"}); err != nil {
		t.Fatalf("brief → draft without brand.md: %v", err)
	}
	body := loadIssueByPath(t, proj.IssueDir, path).BodyRaw
	for _, s := range append(append([]string{}, seoBaselineAgentSections...), seoBaselineOwnerSection) {
		assertContains(t, body, "## "+s+"\n")
	}
	return issue.Slug, path
}

// tickSections ticks every box in the named ## sections of the issue file.
func tickSections(t *testing.T, path string, names ...string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(body), "\n")
	in := false
	for i, l := range lines {
		if strings.HasPrefix(l, "## ") {
			in = false
			for _, n := range names {
				in = in || l == "## "+n
			}
		}
		if in {
			lines[i] = strings.Replace(l, "- [ ]", "- [x]", 1)
		}
	}
	writeDeptFile(t, path, strings.Join(lines, "\n"))
}

// The path walked on a test project: each gate refuses what is missing, the
// owner's stamp before publish, and the owner's steps before measure.
func TestSeoBaselineWalk(t *testing.T) {
	proj := makeMarketingProject(t)
	slug, path := seoBaselineAtDraft(t, proj)
	ctx, _, _ := newTestContext(proj, false)

	if err := runTransition(ctx, []string{slug, "--to", "owner review"}); err == nil || !strings.Contains(err.Error(), "Technical basics") {
		t.Fatalf("draft → owner review with the boxes open: err = %v", err)
	}
	tickSections(t, path, seoBaselineAgentSections...)
	err := runTransition(ctx, []string{slug, "--to", "owner review"})
	if err == nil || !strings.Contains(err.Error(), "issue-cli output add --issue "+slug+" --kind site") {
		t.Fatalf("draft → owner review with no change set on the shelf: err = %v", err)
	}
	src := filepath.Join(t.TempDir(), "changes.md")
	writeDeptFile(t, src, "# Site A: SEO baseline\n\n## Technical basics\n- sitemap.xml, robots.txt\n")
	out, err := outputs.Add(proj, outputs.AddInput{Title: "Site A: SEO baseline", Kind: "site", File: src, Issue: slug, Department: "marketing", By: "agent"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := runTransition(ctx, []string{slug, "--to", "owner review"}); err != nil {
		t.Fatalf("draft → owner review: %v", err)
	}

	if _, err := outputs.Review(proj, out.ID, outputs.StateApproved, "deploy it", "", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err == nil || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("owner review → publish without the owner's stamp: err = %v", err)
	}
	body, _ := os.ReadFile(path)
	writeDeptFile(t, path, strings.Replace(string(body), "status: \"owner review\"", "status: \"owner review\"\nhuman_approval: \"publish\"", 1))
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err != nil {
		t.Fatalf("approved owner review → publish: %v", err)
	}

	tickSections(t, path, "Published")
	if err := runTransition(ctx, []string{slug, "--to", "measure"}); err == nil || !strings.Contains(err.Error(), "Owner steps") {
		t.Fatalf("publish → measure with the owner's steps open: err = %v", err)
	}
	tickSections(t, path, seoBaselineOwnerSection) // the owner's ticks
	if err := runTransition(ctx, []string{slug, "--to", "measure"}); err != nil {
		t.Fatalf("publish → measure: %v", err)
	}
	tickSections(t, path, "Measure")
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err != nil {
		t.Fatalf("measure → done: %v", err)
	}
}
