package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"gopkg.in/yaml.v3"
)

// workedExample is the keyword-research fixture: a placeholder plumber's
// research, clusters and positioning line.
func workedExample(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "keyword-research", "plumbing.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// section returns the text of "## name" in md, up to the next "## ".
func section(t *testing.T, md, name string) string {
	t.Helper()
	_, rest, ok := strings.Cut(md, "\n## "+name+"\n")
	if !ok {
		t.Fatalf("fixture has no ## %s", name)
	}
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// appendBody adds text at the end of the issue file at path.
func appendBody(t *testing.T, path, text string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeDeptFile(t, path, string(body)+"\n"+text)
}

// The worked example: a keyword-research issue created in Marketing walks
// research → shortlist → owner review on the fixture, each gate refusing
// what is missing; owner review → publish is the owner's (owner_only, the
// shelf's newest version approved), and done needs keywords.md, while
// brand.md and icp.md are left as they were.
func TestKeywordResearchWorkedExample(t *testing.T) {
	proj := makeMarketingProject(t)
	writeFoundationDocs(t, proj, "brand.md", "icp.md")
	brandBefore, _ := os.ReadFile(filepath.Join(proj.Root(), "departments", "marketing", "brand.md"))
	ctx, out, _ := newTestContext(proj, false)
	if err := runCreate(ctx, []string{"--department", "marketing", "--type", "keyword-research", "--title", "Keywords and positioning: a local plumber"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj.IssueDir, "keywords-and-positioning-a-local-plumber.md")
	issue := loadIssueByPath(t, proj.IssueDir, path)
	if issue.Type != "keyword-research" || issue.Status != "research" {
		t.Fatalf("new keyword research: type %q status %q\n%s", issue.Type, issue.Status, out)
	}
	slug := issue.Slug
	ex := workedExample(t)

	if err := runTransition(ctx, []string{slug, "--to", "shortlist"}); err == nil || !strings.Contains(err.Error(), "Research") {
		t.Fatalf("research → shortlist with no ## Research: err = %v", err)
	}
	appendBody(t, path, "## Research\n"+section(t, ex, "Research"))
	if err := runTransition(ctx, []string{slug, "--to", "shortlist"}); err != nil {
		t.Fatalf("research → shortlist: %v", err)
	}
	assertContains(t, loadIssueByPath(t, proj.IssueDir, path).BodyRaw, "our fit (own, contest or leave)")

	// The boxes first, then the report on the shelf.
	if err := runTransition(ctx, []string{slug, "--to", "owner review"}); err == nil || !strings.Contains(err.Error(), "Keywords") {
		t.Fatalf("shortlist → owner review with the Keywords boxes open: err = %v", err)
	}
	appendBody(t, path, "## Positioning\n"+section(t, ex, "Positioning"))
	tickAll(t, path)
	err := runTransition(ctx, []string{slug, "--to", "owner review"})
	if err == nil || !strings.Contains(err.Error(), "issue-cli output add --issue "+slug+" --kind report") {
		t.Fatalf("shortlist → owner review with no report on the shelf: err = %v", err)
	}
	src := filepath.Join(t.TempDir(), "keywords.md")
	writeDeptFile(t, src, ex)
	rep, err := outputs.Add(proj, outputs.AddInput{Title: "Keywords and positioning: a local plumber", Kind: "report", File: src, Issue: slug, Department: "marketing", By: "agent"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := runTransition(ctx, []string{slug, "--to", "owner review"}); err != nil {
		t.Fatalf("shortlist → owner review: %v", err)
	}

	// The owner's: the newest shelf version approved, then the stamp.
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err == nil || !strings.Contains(err.Error(), "still in review") {
		t.Fatalf("owner review → publish with the report in review: err = %v", err)
	}
	if _, err := outputs.Review(proj, rep.ID, outputs.StateApproved, "settled", "", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err == nil || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("owner review → publish without approval: err = %v", err)
	}
	body, _ := os.ReadFile(path)
	writeDeptFile(t, path, strings.Replace(string(body), "status: \"owner review\"", "status: \"owner review\"\nhuman_approval: \"publish\"", 1))
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err != nil {
		t.Fatalf("approved owner review → publish: %v", err)
	}
	assertContains(t, loadIssueByPath(t, proj.IssueDir, path).BodyRaw, "keywords.md holds the approved text")
	tickAll(t, path)
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err == nil || !strings.Contains(err.Error(), "keywords.md is missing") {
		t.Fatalf("publish → done before keywords.md is written: err = %v", err)
	}
	writeDeptFile(t, filepath.Join(proj.Root(), "departments", "marketing", "keywords.md"), "# Keywords and positioning: a local plumber\n\n## Positioning\n"+section(t, ex, "Positioning"))
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err != nil {
		t.Fatalf("publish → done: %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(proj.Root(), "departments", "marketing", "brand.md")); string(after) != string(brandBefore) {
		t.Errorf("keyword research changed brand.md:\n%s", after)
	}
}

// The template's keyword-research type: its owner review → publish gate is
// owner_only, its prompts keep paid data behind the openseo connector and
// leave brand.md alone, and a channel brief doesn't wait for keywords.md.
func TestKeywordResearchTemplate(t *testing.T) {
	proj := makeMarketingProject(t)
	raw, err := os.ReadFile(filepath.Join(proj.Root(), "workflows", "marketing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf tracker.WorkflowConfig
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	typ, ok := wf.Types["keyword-research"]
	if !ok {
		t.Fatal("marketing template has no keyword-research type")
	}
	if got := strings.Join(typ.Path, " → "); got != "research → shortlist → owner review → publish → done" {
		t.Errorf("path = %s", got)
	}
	ownerOnly := false
	for _, tr := range typ.Transitions {
		if tr.From != "owner review" || tr.To != "publish" {
			continue
		}
		for _, a := range tr.Actions {
			if a.Type == "require_human_approval" && a.OwnerOnly {
				ownerOnly = true
			}
		}
	}
	if !ownerOnly {
		t.Error("owner review → publish must be owner_only: YOLO never settles the positioning")
	}
	var prompts strings.Builder
	for _, st := range typ.Statuses {
		prompts.WriteString(st.Prompt)
	}
	for _, want := range []string{"Free data first", "`openseo`", "Keychain", "never write a key into a file", "Don't change brand.md or icp.md", "mem-guard check"} {
		if !strings.Contains(prompts.String(), want) {
			t.Errorf("keyword-research prompts lack %q", want)
		}
	}

	writeFoundationDocs(t, proj, "brand.md", "icp.md")
	seoIssueAt(t, proj, "seo-pass", "brief")
	ctx, _, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"seo/pass", "--to", "draft"}); err != nil {
		t.Fatalf("an seo-pass brief needs no keywords.md: %v", err)
	}
}
