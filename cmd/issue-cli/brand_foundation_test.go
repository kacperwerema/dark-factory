package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// writeFoundationDocs writes the files a settled foundation leaves in the
// Marketing department.
func writeFoundationDocs(t *testing.T, proj *tracker.Project, names ...string) {
	t.Helper()
	for _, n := range names {
		writeDeptFile(t, filepath.Join(proj.Root(), "departments", "marketing", n), "# "+n+"\n\nSettled with the owner.\n")
	}
}

// tickAll ticks every box in the issue at path.
func tickAll(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeDeptFile(t, path, strings.ReplaceAll(string(body), "- [ ]", "- [x]"))
}

// Basic first: a channel brief can't move to draft before the brand and
// the ICP are written, and the refusal says how to start them.
func TestChannelBriefNeedsTheBrandFoundation(t *testing.T) {
	proj := makeMarketingProject(t)
	seoIssueAt(t, proj, "seo-pass", "brief")
	ctx, _, _ := newTestContext(proj, false)
	err := runTransition(ctx, []string{"seo/pass", "--to", "draft"})
	if err == nil {
		t.Fatal("brief → draft without brand.md should fail")
	}
	for _, want := range []string{"departments/marketing/brand.md is missing", "issue-cli create --department marketing --type foundation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in:\n%v", want, err)
		}
	}

	writeFoundationDocs(t, proj, "brand.md")
	if err := runTransition(ctx, []string{"seo/pass", "--to", "draft"}); err == nil || !strings.Contains(err.Error(), "icp.md is missing") {
		t.Fatalf("brand.md alone: err = %v, want icp.md missing", err)
	}
	writeFoundationDocs(t, proj, "icp.md")
	if err := runTransition(ctx, []string{"seo/pass", "--to", "draft"}); err != nil {
		t.Fatalf("with brand.md and icp.md: %v", err)
	}
}

// The foundation walks research → brand → icp → owner review → publish →
// done in research mode, and can't finish before its docs are written.
func TestFoundationWalksResearchToPublishedDocs(t *testing.T) {
	proj := makeMarketingProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runCreate(ctx, []string{"--department", "marketing", "--type", "foundation", "--title", "Brand foundation: plumbing"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj.IssueDir, "brand-foundation-plumbing.md")
	issue := loadIssueByPath(t, proj.IssueDir, path)
	if issue.Type != "foundation" || issue.Status != "research" {
		t.Fatalf("new foundation: type %q status %q\n%s", issue.Type, issue.Status, out)
	}
	slug := issue.Slug

	if err := runTransition(ctx, []string{slug, "--to", "brand"}); err == nil || !strings.Contains(err.Error(), "Research") {
		t.Fatalf("research → brand with no ## Research: err = %v", err)
	}
	body, _ := os.ReadFile(path)
	writeDeptFile(t, path, string(body)+"\n## Research\n\n"+strings.Repeat("- https://competitor.example: what they say about drains and boilers\n", 4))
	if err := runTransition(ctx, []string{slug, "--to", "brand"}); err != nil {
		t.Fatalf("research → brand: %v", err)
	}
	assertContains(t, loadIssueByPath(t, proj.IssueDir, path).BodyRaw, "## Brand")
	for _, to := range []string{"icp", "owner review"} {
		if err := runTransition(ctx, []string{slug, "--to", to}); err == nil {
			t.Fatalf("→ %s with the last section's boxes open should fail", to)
		}
		tickAll(t, path)
		if err := runTransition(ctx, []string{slug, "--to", to}); err != nil {
			t.Fatalf("→ %s: %v", to, err)
		}
	}
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err == nil || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("owner review → publish without approval: err = %v", err)
	}

	body, _ = os.ReadFile(path)
	writeDeptFile(t, path, strings.Replace(string(body), "status: \"owner review\"", "status: \"owner review\"\nhuman_approval: \"publish\"", 1))
	if err := runTransition(ctx, []string{slug, "--to", "publish"}); err != nil {
		t.Fatalf("approved owner review → publish: %v", err)
	}
	issue = loadIssueByPath(t, proj.IssueDir, path)
	assertContains(t, issue.BodyRaw, "brand.md and icp.md hold the approved text")
	tickAll(t, path)
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err == nil || !strings.Contains(err.Error(), "brand.md is missing") {
		t.Fatalf("publish → done before the docs are written: err = %v", err)
	}
	writeFoundationDocs(t, proj, "brand.md", "icp.md")
	if err := runTransition(ctx, []string{slug, "--to", "done"}); err != nil {
		t.Fatalf("publish → done: %v", err)
	}
}

// Market size runs on its own path and gates nothing: channel work doesn't
// wait for market.md.
func TestMarketSizeIsItsOwnPath(t *testing.T) {
	proj := makeMarketingProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runProcess(ctx, []string{"workflow", "--department", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "research → market size → owner review → publish → done")
	assertContains(t, out.String(), "research → brand → icp → owner review → publish → done")

	writeFoundationDocs(t, proj, "brand.md", "icp.md")
	seoIssueAt(t, proj, "piece", "brief")
	if err := runTransition(ctx, []string{"seo/pass", "--to", "draft"}); err != nil {
		t.Fatalf("a piece needs no market.md: %v", err)
	}
}
